package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// countingWorkshopInvalidator is a fake workshopCacheInvalidator that counts
// calls — mirrors admin_campaigns_test.go's stubPreflightChecker: the store
// layer is real (backed by TEST_DATABASE_URL), but *seo.Site itself is not
// practical to construct in this package's tests (it needs an embedded
// index.html template), so the narrow local seam is faked instead, exactly
// the same shape campaignPreflightChecker already established.
type countingWorkshopInvalidator struct {
	calls int
}

func (c *countingWorkshopInvalidator) InvalidateWorkshops() { c.calls++ }

// adminWorkshopsMux wires the real admin workshops CRUD routes guarded by
// RequireSession then RequireAdmin, backed by real stores — mirrors
// adminCampaignsMux/adminInterestsMux.
func adminWorkshopsMux(pool *pgxpool.Pool, invalidator workshopCacheInvalidator) http.Handler {
	authStore := auth.NewStore(pool)
	store := workshops.NewStore(pool)
	h := NewAdminWorkshopsHandler(store, invalidator, audit.New(pool))
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/workshops", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("POST /admin/workshops", requireAdmin(http.HandlerFunc(h.Create)))
	mux.Handle("GET /admin/workshops/{id}", requireAdmin(http.HandlerFunc(h.Get)))
	mux.Handle("PATCH /admin/workshops/{id}", requireAdmin(http.HandlerFunc(h.Patch)))
	mux.Handle("DELETE /admin/workshops/{id}", requireAdmin(http.HandlerFunc(h.Delete)))
	return mux
}

func uniqueAdminWorkshopTitle(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-subtest-workshop-%d", time.Now().UnixNano())
}

func cleanupAdminWorkshop(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, id)
	})
}

func auditActionsForWorkshop(t *testing.T, pool *pgxpool.Pool, id int64) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT action FROM audit_log WHERE target_type = $1 AND target_id = $2 ORDER BY id ASC`,
		audit.TargetWorkshop, id)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("scan audit action: %v", err)
		}
		actions = append(actions, a)
	}
	return actions
}

type decodedWorkshop struct {
	ID          int64   `json:"id"`
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	Status      string  `json:"status"`
	PublishedAt *string `json:"published_at"`
	InterestIDs []int64 `json:"interest_ids"`
}

func decodeWorkshop(t *testing.T, body []byte) decodedWorkshop {
	t.Helper()
	var w decodedWorkshop
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatalf("decode workshop: %v (body=%s)", err, body)
	}
	return w
}

// ── Authorization guard ──────────────────────────────────────────────────────

func TestAdminWorkshops_NonAdminForbidden(t *testing.T) {
	pool := interestsTestPool(t) // truncates the auth tables, matching adminInterestsMux's own pool
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	user := seedUser(t, pool, "regular-workshops@example.com")
	seedSession(t, pool, user, "workshops-user-token")

	store := workshops.NewStore(pool)
	target, err := store.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed target workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, target.ID)
	targetPath := fmt.Sprintf("/admin/workshops/%d", target.ID)

	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/admin/workshops", ""},
		{http.MethodPost, "/admin/workshops", fmt.Sprintf(`{"title":%q}`, uniqueAdminWorkshopTitle(t))},
		{http.MethodGet, targetPath, ""},
		{http.MethodPatch, targetPath, `{"title":"X"}`},
		{http.MethodDelete, targetPath, ""},
	}
	for _, c := range cases {
		resp := doJSON(t, srv.Client(), c.method, srv.URL+c.path, "workshops-user-token", c.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s (non-admin) status = %d, want 403", c.method, c.path, resp.StatusCode)
		}
	}
	// The DELETE case above must not have succeeded despite the forbidden
	// response.
	if _, err := store.GetByID(context.Background(), target.ID); err != nil {
		t.Errorf("guard target %d missing after a forbidden DELETE: %v", target.ID, err)
	}
}

func TestAdminWorkshops_Unauthenticated(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	resp := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/admin/workshops", "", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /admin/workshops (no session) status = %d, want 401", resp.StatusCode)
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestAdminWorkshops_CreateGeneratesSlugAndAudits(t *testing.T) {
	pool := interestsTestPool(t)
	inv := &countingWorkshopInvalidator{}
	srv := httptest.NewServer(adminWorkshopsMux(pool, inv))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-create@example.com")
	seedSession(t, pool, admin, "workshops-admin-token")

	title := uniqueAdminWorkshopTitle(t)
	resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/workshops", "workshops-admin-token",
		fmt.Sprintf(`{"title":%q}`, title))
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /admin/workshops status = %d, want 201 (body=%s)", resp.StatusCode, body)
	}
	created := decodeWorkshop(t, body)
	cleanupAdminWorkshop(t, pool, created.ID)

	if created.Status != workshops.StatusDraft {
		t.Errorf("created.Status = %q, want %q", created.Status, workshops.StatusDraft)
	}
	if created.Slug == "" {
		t.Errorf("created.Slug is empty")
	}
	if inv.calls != 1 {
		t.Errorf("invalidator.calls = %d after Create, want 1", inv.calls)
	}

	actions := auditActionsForWorkshop(t, pool, created.ID)
	if len(actions) != 1 || actions[0] != audit.ActionWorkshopCreated {
		t.Errorf("audit actions for created workshop = %v, want [%s]", actions, audit.ActionWorkshopCreated)
	}
}

func TestAdminWorkshops_CreateEmptyTitleRejected(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-badtitle@example.com")
	seedSession(t, pool, admin, "workshops-admin-badtitle-token")

	resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/workshops", "workshops-admin-badtitle-token", `{"title":"   "}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /admin/workshops with blank title status = %d, want 400", resp.StatusCode)
	}
}

// ── Patch: status transitions and audit action naming ──────────────────────────

func TestAdminWorkshops_PatchPublishUnpublishCancel(t *testing.T) {
	pool := interestsTestPool(t)
	inv := &countingWorkshopInvalidator{}
	srv := httptest.NewServer(adminWorkshopsMux(pool, inv))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-transitions@example.com")
	seedSession(t, pool, admin, "workshops-admin-transitions-token")

	store := workshops.NewStore(pool)
	w, err := store.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, w.ID)
	path := fmt.Sprintf("/admin/workshops/%d", w.ID)

	// draft -> published: workshop.published, published_at stamped.
	resp := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+path, "workshops-admin-transitions-token",
		fmt.Sprintf(`{"title":%q,"status":"published"}`, w.Title))
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH to published status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	published := decodeWorkshop(t, body)
	if published.Status != workshops.StatusPublished {
		t.Fatalf("Status after publish = %q, want %q", published.Status, workshops.StatusPublished)
	}
	if published.PublishedAt == nil {
		t.Fatalf("PublishedAt nil after publish")
	}

	// published -> draft: workshop.unpublished, published_at cleared.
	resp = doJSON(t, srv.Client(), http.MethodPatch, srv.URL+path, "workshops-admin-transitions-token",
		fmt.Sprintf(`{"title":%q,"status":"draft"}`, w.Title))
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH to draft status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	unpublished := decodeWorkshop(t, body)
	if unpublished.PublishedAt != nil {
		t.Fatalf("PublishedAt = %v after unpublish, want nil", *unpublished.PublishedAt)
	}

	// draft -> published again, then published -> canceled: workshop.canceled.
	resp = doJSON(t, srv.Client(), http.MethodPatch, srv.URL+path, "workshops-admin-transitions-token",
		fmt.Sprintf(`{"title":%q,"status":"published"}`, w.Title))
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH to published (2nd) status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}

	resp = doJSON(t, srv.Client(), http.MethodPatch, srv.URL+path, "workshops-admin-transitions-token",
		fmt.Sprintf(`{"title":%q,"status":"canceled"}`, w.Title))
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH to canceled status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	canceled := decodeWorkshop(t, body)
	if canceled.Status != workshops.StatusCanceled {
		t.Fatalf("Status after cancel = %q, want %q", canceled.Status, workshops.StatusCanceled)
	}
	if canceled.PublishedAt == nil {
		t.Fatalf("PublishedAt cleared on cancel, want preserved")
	}

	if inv.calls != 4 {
		t.Errorf("invalidator.calls = %d after 4 PATCH mutations, want 4", inv.calls)
	}

	wantActions := []string{
		audit.ActionWorkshopPublished,
		audit.ActionWorkshopUnpublished,
		audit.ActionWorkshopPublished,
		audit.ActionWorkshopCanceled,
	}
	gotActions := auditActionsForWorkshop(t, pool, w.ID)
	if len(gotActions) != len(wantActions) {
		t.Fatalf("audit actions = %v, want %v", gotActions, wantActions)
	}
	for i, want := range wantActions {
		if gotActions[i] != want {
			t.Errorf("audit action[%d] = %q, want %q", i, gotActions[i], want)
		}
	}
}

func TestAdminWorkshops_PatchUnknownStatusRejected(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-badstatus@example.com")
	seedSession(t, pool, admin, "workshops-admin-badstatus-token")

	store := workshops.NewStore(pool)
	w, err := store.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, w.ID)

	resp := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+fmt.Sprintf("/admin/workshops/%d", w.ID),
		"workshops-admin-badstatus-token", fmt.Sprintf(`{"title":%q,"status":"bogus"}`, w.Title))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH with unknown status = %d, want 400", resp.StatusCode)
	}
}

// ── Delete: the binding 23503 -> 409 mapping ────────────────────────────────────

// TestAdminWorkshops_DeleteBlockedByCampaignReturns409 is #0051's binding
// carried-in acceptance criterion from #0050's review: DELETE must map
// SQLSTATE 23503 on email_campaigns_workshop_id_fkey to 409, never a bare
// 500.
func TestAdminWorkshops_DeleteBlockedByCampaignReturns409(t *testing.T) {
	pool := interestsTestPool(t)
	inv := &countingWorkshopInvalidator{}
	srv := httptest.NewServer(adminWorkshopsMux(pool, inv))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-delete409@example.com")
	seedSession(t, pool, admin, "workshops-admin-delete409-token")

	store := workshops.NewStore(pool)
	w, err := store.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, w.ID)

	var campaignID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO email_campaigns (name, subject, body_md, audience_mode, workshop_id)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		fmt.Sprintf("zz-subtest-campaign-%d", time.Now().UnixNano()), "Subject", "Body", "all", w.ID,
	).Scan(&campaignID); err != nil {
		t.Fatalf("seed referencing campaign: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM email_campaigns WHERE id = $1`, campaignID)
	})

	resp := doJSON(t, srv.Client(), http.MethodDelete, srv.URL+fmt.Sprintf("/admin/workshops/%d", w.ID),
		"workshops-admin-delete409-token", "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE blocked-by-campaign status = %d, want 409 (body=%s)", resp.StatusCode, body)
	}
	if inv.calls != 0 {
		t.Errorf("invalidator.calls = %d after a refused delete, want 0", inv.calls)
	}
	if _, err := store.GetByID(context.Background(), w.ID); err != nil {
		t.Errorf("workshop %d missing after refused delete: %v", w.ID, err)
	}
	actions := auditActionsForWorkshop(t, pool, w.ID)
	if len(actions) != 0 {
		t.Errorf("audit actions after refused delete = %v, want none", actions)
	}
}

func TestAdminWorkshops_DeleteSucceeds(t *testing.T) {
	pool := interestsTestPool(t)
	inv := &countingWorkshopInvalidator{}
	srv := httptest.NewServer(adminWorkshopsMux(pool, inv))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-delete-ok@example.com")
	seedSession(t, pool, admin, "workshops-admin-delete-ok-token")

	store := workshops.NewStore(pool)
	w, err := store.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}

	resp := doJSON(t, srv.Client(), http.MethodDelete, srv.URL+fmt.Sprintf("/admin/workshops/%d", w.ID),
		"workshops-admin-delete-ok-token", "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	if inv.calls != 1 {
		t.Errorf("invalidator.calls = %d after Delete, want 1", inv.calls)
	}
	if _, err := store.GetByID(context.Background(), w.ID); !errors.Is(err, workshops.ErrNotFound) {
		t.Errorf("GetByID after delete: err = %v, want ErrNotFound", err)
	}
	actions := auditActionsForWorkshop(t, pool, w.ID)
	if len(actions) != 1 || actions[0] != audit.ActionWorkshopDeleted {
		t.Errorf("audit actions after delete = %v, want [%s]", actions, audit.ActionWorkshopDeleted)
	}
}
