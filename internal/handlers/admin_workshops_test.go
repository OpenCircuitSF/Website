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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
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
	h := NewAdminWorkshopsHandler(store, invalidator, mailing.NewCampaignStore(pool), audit.New(pool), "https://example.com")
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
	mux.Handle("POST /admin/workshops/{id}/announce", requireAdmin(http.HandlerFunc(h.Announce)))
	// #0136: the server-side body preview. See admin_workshop_preview_test.go.
	mux.Handle("POST /admin/workshops/{id}/preview", requireAdmin(http.HandlerFunc(h.Preview)))
	return mux
}

func uniqueAdminWorkshopTitle(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-subtest-workshop-%d", testdb.Unique())
}

// intPtrString formats a *int for a test failure message. %v on a *int
// prints the pointer's hex address, not the value it points to, which makes
// a failure message like "Capacity = 0xc0001234, want 30" useless for
// diagnosing anything (#0155, a ride-along nit from #0146's review).
func intPtrString(p *int) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d", *p)
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
		// #0136
		{http.MethodPost, targetPath + "/preview", ""},
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

// ── Cover image validation at the API boundary (#0138) ─────────────────────────
//
// Client-side validation (web/src/lib/workshopAdmin.ts's isSafeCoverImage)
// doesn't protect a value that arrives by any other path, and internal/seo's
// absoluteURL prefixes whatever cover_image holds without checking it
// (#0055's phase-3 review) -- so the handler itself must reject an unsafe
// value, not just the editor.

func TestIsSafeCoverImage(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{"site-relative path", "/assets/workshops/soldering.jpg", true},
		{"bare root", "/", true},
		{"protocol-relative", "//evil.host/x.jpg", false},
		{"backslash-backslash (normalizes to protocol-relative)", `\\evil.host/x.jpg`, false},
		{"slash-backslash (normalizes to protocol-relative)", `/\evil.host/x.jpg`, false},
		{"backslash-slash (normalizes to protocol-relative)", `\/evil.host/x.jpg`, false},
		{"absolute https to another host", "https://example.com/cover.jpg", false},
		{"absolute http", "http://example.com/cover.jpg", false},
		{"javascript scheme", "javascript:alert(1)", false},
		{"no leading slash", "assets/cover.jpg", false},
		{"empty", "", false},
		// #0138 bounce, finding 1: an interior control character reassembles
		// into a protocol-relative URL once a browser strips it back out.
		{"tab between slashes", "/\t/evil.host/x.jpg", false},
		{"LF between slashes", "/\n/evil.host/x.jpg", false},
		{"CR between slashes", "/\r/evil.host/x.jpg", false},
		{"CRLF between slashes", "/\r\n/evil.host/x.jpg", false},
		{"tab then backslash", "/\t\\evil.host/x.jpg", false},
		{"LF then backslash", "/\n\\evil.host/x.jpg", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isSafeCoverImage(c.value); got != c.want {
				t.Errorf("isSafeCoverImage(%q) = %v, want %v", c.value, got, c.want)
			}
		})
	}
}

func TestAdminWorkshops_CreateRejectsUnsafeCoverImage(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-badcover@example.com")
	seedSession(t, pool, admin, "workshops-admin-badcover-token")

	badValues := []string{
		"//evil.host/x.jpg", `\\evil.host/x.jpg`, "https://evil.host/x.jpg",
		// #0138 bounce, finding 1: control character between the slashes.
		"/\t/evil.host/x.jpg",
	}
	for _, v := range badValues {
		title := uniqueAdminWorkshopTitle(t)
		body := fmt.Sprintf(`{"title":%q,"cover_image":%q}`, title, v)
		resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/workshops", "workshops-admin-badcover-token", body)
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST /admin/workshops with cover_image=%q status = %d, want 400 (body=%s)", v, resp.StatusCode, respBody)
		}
	}
}

func TestAdminWorkshops_CreateAcceptsSafeCoverImageAndRoundTrips(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-goodcover@example.com")
	seedSession(t, pool, admin, "workshops-admin-goodcover-token")

	title := uniqueAdminWorkshopTitle(t)
	body := fmt.Sprintf(`{"title":%q,"cover_image":"/assets/workshops/soldering.jpg"}`, title)
	resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/workshops", "workshops-admin-goodcover-token", body)
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /admin/workshops with safe cover_image status = %d, want 201 (body=%s)", resp.StatusCode, respBody)
	}
	var created struct {
		ID         int64  `json:"id"`
		CoverImage string `json:"cover_image"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		t.Fatalf("decode created workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, created.ID)
	if created.CoverImage != "/assets/workshops/soldering.jpg" {
		t.Errorf("created.CoverImage = %q, want %q", created.CoverImage, "/assets/workshops/soldering.jpg")
	}
}

func TestAdminWorkshops_PatchRejectsUnsafeCoverImage(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-patchbadcover@example.com")
	seedSession(t, pool, admin, "workshops-admin-patchbadcover-token")

	store := workshops.NewStore(pool)
	target, err := store.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed target workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, target.ID)

	resp := doJSON(t, srv.Client(), http.MethodPatch, fmt.Sprintf("%s/admin/workshops/%d", srv.URL, target.ID),
		"workshops-admin-patchbadcover-token", `{"cover_image":"//evil.host/x.jpg"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH cover_image=//evil.host/x.jpg status = %d, want 400 (body=%s)", resp.StatusCode, body)
	}

	// #0138 bounce, finding 1: a control character between the slashes must
	// be rejected at the API boundary too, not just the bare "//" prefix.
	resp2 := doJSON(t, srv.Client(), http.MethodPatch, fmt.Sprintf("%s/admin/workshops/%d", srv.URL, target.ID),
		"workshops-admin-patchbadcover-token", "{\"cover_image\":\"/\\t/evil.host/x.jpg\"}")
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH cover_image=/<TAB>/evil.host/x.jpg status = %d, want 400 (body=%s)", resp2.StatusCode, body2)
	}

	after, err := store.GetByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("reload workshop: %v", err)
	}
	if after.CoverImage != nil {
		t.Errorf("after rejected PATCH, CoverImage = %v, want nil (unchanged)", after.CoverImage)
	}
}

func TestAdminWorkshops_PatchAcceptsSafeCoverImageAndRoundTrips(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-patchgoodcover@example.com")
	seedSession(t, pool, admin, "workshops-admin-patchgoodcover-token")

	store := workshops.NewStore(pool)
	target, err := store.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed target workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, target.ID)

	resp := doJSON(t, srv.Client(), http.MethodPatch, fmt.Sprintf("%s/admin/workshops/%d", srv.URL, target.ID),
		"workshops-admin-patchgoodcover-token", `{"cover_image":"/assets/workshops/soldering.jpg"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH with safe cover_image status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}

	after, err := store.GetByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("reload workshop: %v", err)
	}
	if after.CoverImage == nil || *after.CoverImage != "/assets/workshops/soldering.jpg" {
		t.Errorf("after.CoverImage = %v, want \"/assets/workshops/soldering.jpg\"", after.CoverImage)
	}
}

// ── #0152: signup_url validated at the API boundary ─────────────────────────

func TestIsSafeLinkHref(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{"https to any host", "https://eventbrite.com/e/soldering-101", true},
		{"http to any host", "http://example.com/rsvp", true},
		{"mailto", "mailto:hello@opencircuitsf.com", true},
		{"root-relative path", "/rsvp", true},
		{"case-insensitive scheme", "HTTPS://example.com/rsvp", true},
		{"javascript scheme", "javascript:alert(1)", false},
		{"data scheme", "data:text/html,evil", false},
		{"vbscript scheme", "vbscript:msgbox(1)", false},
		{"protocol-relative", "//evil.host/x", false},
		{"backslash-backslash (normalizes to protocol-relative)", `\\evil.host/x`, false},
		{"slash-backslash (normalizes to protocol-relative)", `/\evil.host/x`, false},
		{"backslash-slash (normalizes to protocol-relative)", `\/evil.host/x`, false},
		{"empty", "", false},
		{"whitespace only", "   ", false},
		// #0138's control-character-scheme-bypass class, reused here verbatim
		// per #0152's acceptance criterion (same rule as isSafeLinkHref, not
		// a third variant): a browser deletes ASCII tab/LF/CR before
		// resolving a URL, so an interior control character can reassemble
		// a dangerous scheme a naive check would miss.
		{"tab inside scheme", "java\tscript:alert(1)", false},
		{"CR inside scheme", "java\rscript:alert(1)", false},
		{"LF inside scheme", "java\nscript:alert(1)", false},
		{"tab between slashes", "/\t/evil.host/x", false},
		{"leading control char", "\x01javascript:alert(1)", false},
		{"NUL prefix", "\x00javascript:alert(1)", false},
		{"trailing DEL", "javascript:alert(1)\x7f", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isSafeLinkHref(c.value); got != c.want {
				t.Errorf("isSafeLinkHref(%q) = %v, want %v", c.value, got, c.want)
			}
		})
	}
}

func TestAdminWorkshops_CreateRejectsUnsafeSignupURL(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-badsignup@example.com")
	seedSession(t, pool, admin, "workshops-admin-badsignup-token")

	badValues := []string{
		"javascript:alert(1)", "data:text/html,evil", "//evil.host/x",
		// #0138 bounce, finding 1: control character reassembles a
		// dangerous scheme once the browser strips it back out.
		"java\tscript:alert(1)",
	}
	for _, v := range badValues {
		title := uniqueAdminWorkshopTitle(t)
		body := fmt.Sprintf(`{"title":%q,"signup_url":%q}`, title, v)
		resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/workshops", "workshops-admin-badsignup-token", body)
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST /admin/workshops with signup_url=%q status = %d, want 400 (body=%s)", v, resp.StatusCode, respBody)
		}
	}
}

func TestAdminWorkshops_CreateAcceptsSafeSignupURLAndRoundTrips(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-goodsignup@example.com")
	seedSession(t, pool, admin, "workshops-admin-goodsignup-token")

	title := uniqueAdminWorkshopTitle(t)
	body := fmt.Sprintf(`{"title":%q,"signup_url":"https://eventbrite.com/e/soldering-101"}`, title)
	resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/workshops", "workshops-admin-goodsignup-token", body)
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /admin/workshops with safe signup_url status = %d, want 201 (body=%s)", resp.StatusCode, respBody)
	}
	var created struct {
		ID        int64  `json:"id"`
		SignupURL string `json:"signup_url"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		t.Fatalf("decode created workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, created.ID)
	if created.SignupURL != "https://eventbrite.com/e/soldering-101" {
		t.Errorf("created.SignupURL = %q, want %q", created.SignupURL, "https://eventbrite.com/e/soldering-101")
	}
}

func TestAdminWorkshops_PatchRejectsUnsafeSignupURL(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-patchbadsignup@example.com")
	seedSession(t, pool, admin, "workshops-admin-patchbadsignup-token")

	store := workshops.NewStore(pool)
	target, err := store.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed target workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, target.ID)

	resp := doJSON(t, srv.Client(), http.MethodPatch, fmt.Sprintf("%s/admin/workshops/%d", srv.URL, target.ID),
		"workshops-admin-patchbadsignup-token", `{"signup_url":"javascript:alert(1)"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH signup_url=javascript:alert(1) status = %d, want 400 (body=%s)", resp.StatusCode, body)
	}

	// #0138 bounce, finding 1: a control character between the slashes must
	// be rejected at the API boundary too, not just the bare "//" prefix.
	resp2 := doJSON(t, srv.Client(), http.MethodPatch, fmt.Sprintf("%s/admin/workshops/%d", srv.URL, target.ID),
		"workshops-admin-patchbadsignup-token", "{\"signup_url\":\"/\\t/evil.host/x\"}")
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH signup_url=/<TAB>/evil.host/x status = %d, want 400 (body=%s)", resp2.StatusCode, body2)
	}

	after, err := store.GetByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("reload workshop: %v", err)
	}
	if after.SignupURL != nil {
		t.Errorf("after rejected PATCH, SignupURL = %v, want nil (unchanged)", after.SignupURL)
	}
}

func TestAdminWorkshops_PatchAcceptsSafeSignupURLAndRoundTrips(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-patchgoodsignup@example.com")
	seedSession(t, pool, admin, "workshops-admin-patchgoodsignup-token")

	store := workshops.NewStore(pool)
	target, err := store.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed target workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, target.ID)

	resp := doJSON(t, srv.Client(), http.MethodPatch, fmt.Sprintf("%s/admin/workshops/%d", srv.URL, target.ID),
		"workshops-admin-patchgoodsignup-token", `{"signup_url":"https://eventbrite.com/e/soldering-101"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH with safe signup_url status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}

	after, err := store.GetByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("reload workshop: %v", err)
	}
	if after.SignupURL == nil || *after.SignupURL != "https://eventbrite.com/e/soldering-101" {
		t.Errorf("after.SignupURL = %v, want \"https://eventbrite.com/e/soldering-101\"", after.SignupURL)
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

// TestAdminWorkshops_PatchClearsOptionalFieldWithExplicitEmptyString is
// #0139's end-to-end regression: an admin who blanks an optional field and
// saves must see it actually gone from the database, not just absent from
// the request. patchWorkshopRequest already used *string pointers before
// this issue (checked, not assumed -- see admin_workshops.go's Patch), so
// the fix lived entirely client-side (lib/workshopAdmin.ts); this proves the
// server half of the contract those pointer fields promise. It also proves
// the inverse in the same request: a field the PATCH body never mentions at
// all must survive untouched.
func TestAdminWorkshops_PatchClearsOptionalFieldWithExplicitEmptyString(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-clear@example.com")
	seedSession(t, pool, admin, "workshops-admin-clear-token")

	store := workshops.NewStore(pool)
	summary := "Original summary, to be cleared"
	signupURL := "https://example.com/rsvp"
	w, err := store.Create(context.Background(), workshops.CreateInput{
		Title:     uniqueAdminWorkshopTitle(t),
		Summary:   &summary,
		SignupURL: &signupURL,
	})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, w.ID)
	path := fmt.Sprintf("/admin/workshops/%d", w.ID)

	// Sanity: the seeded row genuinely has both fields set before the PATCH.
	seeded, err := store.GetByID(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("GetByID after seed: %v", err)
	}
	if seeded.Summary == nil || *seeded.Summary != summary {
		t.Fatalf("seeded Summary = %v, want %q", seeded.Summary, summary)
	}
	if seeded.SignupURL == nil || *seeded.SignupURL != signupURL {
		t.Fatalf("seeded SignupURL = %v, want %q", seeded.SignupURL, signupURL)
	}

	// The PATCH body carries an explicit "" for summary (clear it) and never
	// mentions signup_url at all (leave it alone).
	resp := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+path, "workshops-admin-clear-token", `{"summary":""}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH clearing summary status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}

	// Read the value back from the database, not from the response body --
	// the acceptance criterion is that the value is actually gone, not just
	// absent from what this one request happened to echo.
	updated, err := store.GetByID(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("GetByID after patch: %v", err)
	}
	if updated.Summary != nil {
		t.Errorf("Summary = %q after explicit empty-string PATCH, want nil (cleared)", *updated.Summary)
	}
	if updated.SignupURL == nil || *updated.SignupURL != signupURL {
		t.Errorf("SignupURL = %v after a PATCH that never mentioned it, want unchanged %q", updated.SignupURL, signupURL)
	}
}

// TestAdminWorkshops_PatchTreatsWhitespaceOnlyOptionalFieldAsCleared is
// #0147's regression: normalizeOptionalCampaignField used to compare
// *v == "" without trimming, so a whitespace-only value like "   " was
// stored verbatim instead of being treated as a clear. It also proves two
// things in the same request: a field the PATCH never mentions is left
// alone, and a field set to real content padded with leading/trailing
// spaces is stored exactly as sent -- the fix only changes what counts as
// "blank," it does not trim genuine content.
func TestAdminWorkshops_PatchTreatsWhitespaceOnlyOptionalFieldAsCleared(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-ws-clear@example.com")
	seedSession(t, pool, admin, "workshops-admin-ws-clear-token")

	store := workshops.NewStore(pool)
	summary := "Original summary, to be cleared by whitespace"
	signupURL := "https://example.com/rsvp"
	w, err := store.Create(context.Background(), workshops.CreateInput{
		Title:     uniqueAdminWorkshopTitle(t),
		Summary:   &summary,
		SignupURL: &signupURL,
	})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, w.ID)
	path := fmt.Sprintf("/admin/workshops/%d", w.ID)

	// The PATCH body carries a whitespace-only summary (must clear, not
	// store the spaces), a location_note padded with real content (must be
	// stored verbatim, not trimmed), and never mentions signup_url (must be
	// left alone).
	resp := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+path, "workshops-admin-ws-clear-token",
		`{"summary":"   ","location_note":"  Padded real content  "}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH with whitespace-only summary status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}

	updated, err := store.GetByID(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("GetByID after patch: %v", err)
	}
	if updated.Summary != nil {
		t.Errorf("Summary = %q after whitespace-only PATCH, want nil (cleared)", *updated.Summary)
	}
	if updated.SignupURL == nil || *updated.SignupURL != signupURL {
		t.Errorf("SignupURL = %v after a PATCH that never mentioned it, want unchanged %q", updated.SignupURL, signupURL)
	}
	const wantNote = "  Padded real content  "
	if updated.LocationNote == nil || *updated.LocationNote != wantNote {
		t.Errorf("LocationNote = %v, want verbatim %q (real content is not trimmed)", updated.LocationNote, wantNote)
	}
}

// TestAdminWorkshops_PatchClearsCapacityWithExplicitNull is #0146's fix:
// capacity is Optional[int] on the wire (internal/handlers/optional.go), not
// *int, precisely so an explicit JSON `null` can mean "clear it" without
// being indistinguishable from an absent key. Mirrors
// TestAdminWorkshops_PatchClearsOptionalFieldWithExplicitEmptyString's
// shape: read the value back from the database, and prove the leave-alone
// guard has teeth on a field in the SAME request.
func TestAdminWorkshops_PatchClearsCapacityWithExplicitNull(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-capacity-clear@example.com")
	seedSession(t, pool, admin, "workshops-admin-capacity-clear-token")

	store := workshops.NewStore(pool)
	capacity := 30
	signupURL := "https://example.com/rsvp-capacity-clear"
	w, err := store.Create(context.Background(), workshops.CreateInput{
		Title:     uniqueAdminWorkshopTitle(t),
		Capacity:  &capacity,
		SignupURL: &signupURL,
	})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, w.ID)
	path := fmt.Sprintf("/admin/workshops/%d", w.ID)

	// Sanity: the seeded row genuinely has a capacity before the PATCH.
	seeded, err := store.GetByID(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("GetByID after seed: %v", err)
	}
	if seeded.Capacity == nil || *seeded.Capacity != capacity {
		t.Fatalf("seeded Capacity = %s, want %d", intPtrString(seeded.Capacity), capacity)
	}

	// The PATCH body carries an explicit null for capacity (clear it) and
	// never mentions signup_url at all (leave it alone).
	resp := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+path,
		"workshops-admin-capacity-clear-token", `{"capacity": null}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH clearing capacity status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}

	// Read the value back from the database, not the response body -- the
	// acceptance criterion is that the value is actually gone.
	updated, err := store.GetByID(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("GetByID after patch: %v", err)
	}
	if updated.Capacity != nil {
		t.Errorf("Capacity = %d after explicit-null PATCH, want nil (cleared)", *updated.Capacity)
	}
	if updated.SignupURL == nil || *updated.SignupURL != signupURL {
		t.Errorf("SignupURL = %v after a PATCH that never mentioned it, want unchanged %q", updated.SignupURL, signupURL)
	}
}

// TestAdminWorkshops_PatchWithoutCapacityLeavesItAlone is the other half of
// #0146's acceptance criteria: a PATCH that never mentions capacity at all
// (as opposed to mentioning it as null) must leave the stored value alone --
// the "absent" and "explicit null" wire states must resolve differently.
func TestAdminWorkshops_PatchWithoutCapacityLeavesItAlone(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-capacity-leave@example.com")
	seedSession(t, pool, admin, "workshops-admin-capacity-leave-token")

	store := workshops.NewStore(pool)
	capacity := 15
	w, err := store.Create(context.Background(), workshops.CreateInput{
		Title:    uniqueAdminWorkshopTitle(t),
		Capacity: &capacity,
	})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, w.ID)
	path := fmt.Sprintf("/admin/workshops/%d", w.ID)

	// The PATCH body mentions a different field and never mentions capacity.
	newSummary := "Updated by a PATCH that never mentions capacity"
	resp := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+path,
		"workshops-admin-capacity-leave-token", fmt.Sprintf(`{"summary":%q}`, newSummary))
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}

	updated, err := store.GetByID(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("GetByID after patch: %v", err)
	}
	if updated.Capacity == nil || *updated.Capacity != capacity {
		t.Errorf("Capacity = %s after a PATCH that never mentioned it, want unchanged %d", intPtrString(updated.Capacity), capacity)
	}
	if updated.Summary == nil || *updated.Summary != newSummary {
		t.Errorf("Summary = %v, want %q", updated.Summary, newSummary)
	}
}

// TestAdminWorkshops_PatchSetsCapacityToNewValue is the third state: a PATCH
// carrying a real number for capacity must set it, whether or not the row
// had one before.
func TestAdminWorkshops_PatchSetsCapacityToNewValue(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-capacity-set@example.com")
	seedSession(t, pool, admin, "workshops-admin-capacity-set-token")

	store := workshops.NewStore(pool)
	w, err := store.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, w.ID)
	path := fmt.Sprintf("/admin/workshops/%d", w.ID)

	resp := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+path,
		"workshops-admin-capacity-set-token", `{"capacity": 25}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH setting capacity status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}

	updated, err := store.GetByID(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("GetByID after patch: %v", err)
	}
	if updated.Capacity == nil || *updated.Capacity != 25 {
		t.Errorf("Capacity = %s after PATCH {capacity: 25}, want 25", intPtrString(updated.Capacity))
	}
}

// TestIsValidCapacity is a pure unit test over isValidCapacity's boundary,
// with no database and no HTTP round trip — #0155's decided range is
// 1..2147483647 (INT's max), and 0 is deliberately rejected rather than
// reused as a second spelling of "no limit" (NULL already means that; see
// isValidCapacity's doc comment).
func TestIsValidCapacity(t *testing.T) {
	cases := []struct {
		name  string
		value int
		want  bool
	}{
		{"below minimum", -1, false},
		{"zero, deliberately rejected (#0155)", 0, false},
		{"at minimum", 1, true},
		{"just above minimum", 2, true},
		{"ordinary value", 30, true},
		{"at maximum (INT max, 2^31-1)", 2147483647, true},
		{"one below maximum", 2147483646, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isValidCapacity(c.value); got != c.want {
				t.Errorf("isValidCapacity(%d) = %v, want %v", c.value, got, c.want)
			}
		})
	}
}

// TestAdminWorkshops_CreateRejectsOutOfRangeCapacity is #0155's acceptance
// criterion for Create: every boundary below the minimum, at the minimum,
// at the maximum, and above the maximum, asserting the STATUS CODE (not
// merely "something failed") and — for the rejected cases — that nothing
// was stored. `3000000000` is the value #0146's review found returning a
// bare 500 (Postgres INT overflow) before this issue; it must now be a 400
// from the handler, never reaching the database at all.
func TestAdminWorkshops_CreateRejectsOutOfRangeCapacity(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-capacity-range-create@example.com")
	seedSession(t, pool, admin, "workshops-admin-capacity-range-create-token")

	cases := []struct {
		name     string
		capacity string // raw JSON literal
	}{
		{"below minimum: -1", "-1"},
		{"zero, rejected not treated as unlimited (#0155)", "0"},
		{"above INT max: 3000000000 (#0146's review found this a 500)", "3000000000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title := uniqueAdminWorkshopTitle(t)
			body := fmt.Sprintf(`{"title":%q,"capacity":%s}`, title, c.capacity)
			resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/workshops", "workshops-admin-capacity-range-create-token", body)
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("POST /admin/workshops with capacity=%s status = %d, want 400 (body=%s)", c.capacity, resp.StatusCode, respBody)
			}

			// Nothing was stored: this title never made it into the table.
			var count int
			err := pool.QueryRow(context.Background(), `SELECT count(*) FROM workshops WHERE title = $1`, title).Scan(&count)
			if err != nil {
				t.Fatalf("count workshops by title: %v", err)
			}
			if count != 0 {
				t.Errorf("capacity=%s: %d row(s) stored despite the 400, want 0", c.capacity, count)
			}
		})
	}
}

// TestAdminWorkshops_CreateAcceptsBoundaryCapacities is the accepting half
// of #0155's boundary sweep: exactly at the minimum and exactly at the
// maximum, both read back from the database.
func TestAdminWorkshops_CreateAcceptsBoundaryCapacities(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-capacity-range-create-ok@example.com")
	seedSession(t, pool, admin, "workshops-admin-capacity-range-create-ok-token")
	store := workshops.NewStore(pool)

	cases := []struct {
		name     string
		capacity int
	}{
		{"at minimum: 1", 1},
		{"at maximum: 2147483647", 2147483647},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title := uniqueAdminWorkshopTitle(t)
			body := fmt.Sprintf(`{"title":%q,"capacity":%d}`, title, c.capacity)
			resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/workshops", "workshops-admin-capacity-range-create-ok-token", body)
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("POST /admin/workshops with capacity=%d status = %d, want 201 (body=%s)", c.capacity, resp.StatusCode, respBody)
			}
			var created struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(respBody, &created); err != nil {
				t.Fatalf("decode created workshop: %v", err)
			}
			cleanupAdminWorkshop(t, pool, created.ID)

			row, err := store.GetByID(context.Background(), created.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if row.Capacity == nil || *row.Capacity != c.capacity {
				t.Errorf("stored Capacity = %s, want %d", intPtrString(row.Capacity), c.capacity)
			}
		})
	}
}

// TestAdminWorkshops_PatchRejectsOutOfRangeCapacity mirrors the Create
// sweep for PATCH: below the minimum, at zero, and above INT's max, each
// asserting a 400 and that the workshop's PREVIOUSLY-SET capacity survives
// unchanged — an out-of-range PATCH must not clear or corrupt the existing
// value.
func TestAdminWorkshops_PatchRejectsOutOfRangeCapacity(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-capacity-range-patch@example.com")
	seedSession(t, pool, admin, "workshops-admin-capacity-range-patch-token")
	store := workshops.NewStore(pool)

	cases := []struct {
		name     string
		capacity string // raw JSON literal
	}{
		{"below minimum: -1", "-1"},
		{"zero, rejected not treated as unlimited (#0155)", "0"},
		{"above INT max: 3000000000 (#0146's review found this a 500)", "3000000000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			existing := 42
			w, err := store.Create(context.Background(), workshops.CreateInput{
				Title:    uniqueAdminWorkshopTitle(t),
				Capacity: &existing,
			})
			if err != nil {
				t.Fatalf("seed workshop: %v", err)
			}
			cleanupAdminWorkshop(t, pool, w.ID)
			path := fmt.Sprintf("/admin/workshops/%d", w.ID)

			body := fmt.Sprintf(`{"capacity":%s}`, c.capacity)
			resp := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+path, "workshops-admin-capacity-range-patch-token", body)
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("PATCH capacity=%s status = %d, want 400 (body=%s)", c.capacity, resp.StatusCode, respBody)
			}

			updated, err := store.GetByID(context.Background(), w.ID)
			if err != nil {
				t.Fatalf("GetByID after rejected PATCH: %v", err)
			}
			if updated.Capacity == nil || *updated.Capacity != existing {
				t.Errorf("Capacity = %s after a rejected PATCH, want unchanged %d", intPtrString(updated.Capacity), existing)
			}
		})
	}
}

// TestAdminWorkshops_PatchAcceptsBoundaryCapacities is the accepting half
// for PATCH: exactly 1 and exactly 2147483647, both stored and read back
// from the database.
func TestAdminWorkshops_PatchAcceptsBoundaryCapacities(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshops-capacity-range-patch-ok@example.com")
	seedSession(t, pool, admin, "workshops-admin-capacity-range-patch-ok-token")
	store := workshops.NewStore(pool)

	cases := []struct {
		name     string
		capacity int
	}{
		{"at minimum: 1", 1},
		{"at maximum: 2147483647", 2147483647},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, err := store.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
			if err != nil {
				t.Fatalf("seed workshop: %v", err)
			}
			cleanupAdminWorkshop(t, pool, w.ID)
			path := fmt.Sprintf("/admin/workshops/%d", w.ID)

			body := fmt.Sprintf(`{"capacity":%d}`, c.capacity)
			resp := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+path, "workshops-admin-capacity-range-patch-ok-token", body)
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("PATCH capacity=%d status = %d, want 200 (body=%s)", c.capacity, resp.StatusCode, respBody)
			}

			updated, err := store.GetByID(context.Background(), w.ID)
			if err != nil {
				t.Fatalf("GetByID after PATCH: %v", err)
			}
			if updated.Capacity == nil || *updated.Capacity != c.capacity {
				t.Errorf("Capacity = %s after PATCH, want %d", intPtrString(updated.Capacity), c.capacity)
			}
		})
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
		fmt.Sprintf("zz-subtest-campaign-%d", testdb.Unique()), "Subject", "Body", "all", w.ID,
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
