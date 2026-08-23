package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// interestsTestPool returns the package's single shared pool (opened once in
// TestMain — #0091) or skips if TEST_DATABASE_URL was unset. It truncates the
// auth tables (same set as credsTestPool/settingsTestPool) on entry only,
// but deliberately NEVER truncates `interests` or `subscribers` --
// interests holds the twelve-row production taxonomy seeded by migration
// 000009 that every other package's tests rely on staying present
// (internal/interests/store_test.go's own doc comment explains why), and
// truncating subscribers would race #0026's concurrent test runs against the
// same shared TEST_DATABASE_URL. Rows this file creates in either table are
// scoped with a "zz-test-" slug/email prefix and cleaned up individually via
// t.Cleanup, matching internal/interests/store_test.go's testSlug
// convention.
func interestsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	truncateCredsTables(t, testDBPool)
	return testDBPool
}

// testInterestSlug returns a slug scoped to this test run, never colliding
// with a seeded taxonomy slug, and registers cleanup that deletes any row
// left behind under it (directly, bypassing the store's subscriber guard --
// a leftover row from a failed assertion must not survive regardless).
func testInterestSlug(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	slug := fmt.Sprintf("zz-test-%d", testdb.Unique())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM interests WHERE slug = $1`, slug)
	})
	return slug
}

// seedSubscriberLinkedToInterest inserts a minimal subscribers row linked to
// interestID via subscriber_interests, mirroring
// internal/interests/store_test.go's seedSubscriberWithInterest. Cleanup
// deletes the subscriber; subscriber_interests cascades (ON DELETE CASCADE,
// migrations/000010).
func seedSubscriberLinkedToInterest(t *testing.T, pool *pgxpool.Pool, interestID int64) int64 {
	t.Helper()
	ctx := context.Background()
	email := fmt.Sprintf("zz-test-sub-%d@example.com", testdb.Unique())
	var subID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO subscribers (email, manage_token) VALUES ($1, $2) RETURNING id`,
		email, fmt.Sprintf("zz-test-token-%d", testdb.Unique()),
	).Scan(&subID); err != nil {
		t.Fatalf("seed subscriber: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, subID)
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO subscriber_interests (subscriber_id, interest_id) VALUES ($1, $2)`,
		subID, interestID,
	); err != nil {
		t.Fatalf("link subscriber to interest: %v", err)
	}
	return subID
}

// adminInterestsMux wires the real admin interests CRUD routes guarded by
// RequireSession then RequireAdmin, backed by a real *auth.Store (session
// resolution) and a real *interests.Store + audit.Logger (the data layer).
// Requests therefore flow through the genuine session + admin middleware,
// proving the routes are protected exactly as wired in main.go, and the
// audit rows are written for real.
func adminInterestsMux(pool *pgxpool.Pool) http.Handler {
	authStore := auth.NewStore(pool)
	interestsStore := interests.NewStore(pool)
	h := NewAdminInterestsHandler(interestsStore, audit.New(pool))
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/interests", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("POST /admin/interests", requireAdmin(http.HandlerFunc(h.Create)))
	mux.Handle("PATCH /admin/interests/{id}", requireAdmin(http.HandlerFunc(h.Patch)))
	mux.Handle("DELETE /admin/interests/{id}", requireAdmin(http.HandlerFunc(h.Delete)))
	return mux
}

// decodedInterest mirrors interestView for test assertions.
type decodedInterest struct {
	ID              int64   `json:"id"`
	Slug            string  `json:"slug"`
	Name            string  `json:"name"`
	Description     *string `json:"description"`
	SortOrder       int     `json:"sort_order"`
	Active          bool    `json:"active"`
	SubscriberCount int64   `json:"subscriber_count"`
}

func decodeInterest(t *testing.T, body []byte) decodedInterest {
	t.Helper()
	var it decodedInterest
	if err := json.Unmarshal(body, &it); err != nil {
		t.Fatalf("decode interest: %v (body=%s)", err, body)
	}
	return it
}

func decodeInterestsList(t *testing.T, body []byte) []decodedInterest {
	t.Helper()
	var resp struct {
		Interests []decodedInterest `json:"interests"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode interests list: %v (body=%s)", err, body)
	}
	return resp.Interests
}

func doJSON(t *testing.T, client *http.Client, method, url, token, body string) *http.Response {
	t.Helper()
	var reqBody io.Reader
	if body != "" {
		reqBody = jsonBody(body)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req = withCookie(req, token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// interestExists reports whether an interests row with the given slug is
// still present.
func interestExists(t *testing.T, pool *pgxpool.Pool, slug string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM interests WHERE slug = $1)`, slug,
	).Scan(&exists); err != nil {
		t.Fatalf("check interest exists %q: %v", slug, err)
	}
	return exists
}

// auditActionsForSlug returns the ordered list of audit_log.action values
// whose metadata->>'slug' matches slug, newest last.
func auditActionsForSlug(t *testing.T, pool *pgxpool.Pool, slug string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT action FROM audit_log WHERE metadata->>'slug' = $1 ORDER BY id ASC`, slug)
	if err != nil {
		t.Fatalf("query audit actions for %q: %v", slug, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("scan audit action: %v", err)
		}
		out = append(out, a)
	}
	return out
}

// ── Authorization guard ──────────────────────────────────────────────────────

// TestAdminInterests_NonAdminForbidden asserts a non-admin with a VALID
// session is rejected with 403 on all four routes — proving RequireAdmin
// guards them and is reached only after RequireSession succeeds — and that
// no mutation occurs.
func TestAdminInterests_NonAdminForbidden(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	user := seedUser(t, pool, "regular@example.com") // is_admin = FALSE
	seedSession(t, pool, user, "user-token")
	slug := testInterestSlug(t, pool)

	// The PATCH/DELETE targets are a throwaway interest this test seeds
	// itself, NOT a literal id like "1" — a hardcoded id would silently
	// target real seeded taxonomy data (the "microcontrollers" row) the
	// moment RequireAdmin is ever mutated away in the way this test exists
	// to catch, which is exactly what happened once while proving this test
	// fails without the guard (see this issue's Gotchas). Guarded-off
	// requests never reach the handler in the correct build, but the test
	// fixture must be safe even when they do.
	targetSlug := testInterestSlug(t, pool)
	target, err := interests.NewStore(pool).Create(context.Background(), targetSlug, "Guard target", nil, 0)
	if err != nil {
		t.Fatalf("seed target interest: %v", err)
	}
	targetPath := fmt.Sprintf("/admin/interests/%d", target.ID)

	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/admin/interests", ""},
		{http.MethodPost, "/admin/interests", fmt.Sprintf(`{"slug":%q,"name":"X"}`, slug)},
		{http.MethodPatch, targetPath, `{"name":"X"}`},
		{http.MethodDelete, targetPath, ""},
	}
	for _, c := range cases {
		resp := doJSON(t, srv.Client(), c.method, srv.URL+c.path, "user-token", c.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s (non-admin) status = %d, want 403", c.method, c.path, resp.StatusCode)
		}
	}
	if interestExists(t, pool, slug) {
		t.Errorf("interest %q exists after a forbidden POST, want no row created", slug)
	}
	if !interestExists(t, pool, targetSlug) {
		t.Errorf("guard target %q was deleted despite a forbidden DELETE", targetSlug)
	}
}

// TestAdminInterests_Unauthenticated asserts a request with no session
// cookie is rejected with 401 on all four routes.
func TestAdminInterests_Unauthenticated(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	// See TestAdminInterests_NonAdminForbidden's comment: use a throwaway
	// seeded interest as the PATCH/DELETE target rather than a literal id,
	// so this fixture stays safe even under a guard-removal mutation.
	targetSlug := testInterestSlug(t, pool)
	target, err := interests.NewStore(pool).Create(context.Background(), targetSlug, "Guard target", nil, 0)
	if err != nil {
		t.Fatalf("seed target interest: %v", err)
	}
	targetPath := fmt.Sprintf("/admin/interests/%d", target.ID)

	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/admin/interests", ""},
		{http.MethodPost, "/admin/interests", `{"slug":"zz-test-anon","name":"X"}`},
		{http.MethodPatch, targetPath, `{"name":"X"}`},
		{http.MethodDelete, targetPath, ""},
	}
	for _, c := range cases {
		resp := doJSON(t, srv.Client(), c.method, srv.URL+c.path, "", c.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s (no session) status = %d, want 401", c.method, c.path, resp.StatusCode)
		}
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

// TestAdminInterests_ListIncludesSeededTaxonomyAndSubscriberCount asserts the
// admin GET returns at least the twelve seeded taxonomy rows, and that a
// freshly created test interest with two linked subscribers reports
// subscriber_count=2 while one with none reports 0 -- the acceptance
// criterion this whole screen exists for.
func TestAdminInterests_ListIncludesSeededTaxonomyAndSubscriberCount(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	istore := interests.NewStore(pool)
	slugWith := testInterestSlug(t, pool)
	slugWithout := testInterestSlug(t, pool)
	withSubs, err := istore.Create(context.Background(), slugWith, "Has subscribers", nil, 0)
	if err != nil {
		t.Fatalf("seed withSubs: %v", err)
	}
	if _, err := istore.Create(context.Background(), slugWithout, "No subscribers", nil, 0); err != nil {
		t.Fatalf("seed without: %v", err)
	}
	seedSubscriberLinkedToInterest(t, pool, withSubs.ID)
	seedSubscriberLinkedToInterest(t, pool, withSubs.ID)

	resp := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/admin/interests", "admin-token", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	list := decodeInterestsList(t, body)
	if len(list) < 12 {
		t.Fatalf("list has %d interests, want at least the 12 seeded", len(list))
	}

	var gotWith, gotWithout *decodedInterest
	for i := range list {
		switch list[i].Slug {
		case slugWith:
			gotWith = &list[i]
		case slugWithout:
			gotWithout = &list[i]
		}
	}
	if gotWith == nil || gotWith.SubscriberCount != 2 {
		t.Errorf("subscriber_count for %q = %+v, want 2", slugWith, gotWith)
	}
	if gotWithout == nil || gotWithout.SubscriberCount != 0 {
		t.Errorf("subscriber_count for %q = %+v, want 0", slugWithout, gotWithout)
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

// TestAdminInterests_CreateValidatesSlugFormat asserts every malformed slug is
// rejected with 400 and no row is created, and writes no audit row.
func TestAdminInterests_CreateValidatesSlugFormat(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	for _, bad := range []string{"Upper-Case", "has_underscore", "trailing-", "-leading", "double--hyphen"} {
		body := fmt.Sprintf(`{"slug":%q,"name":"Bad"}`, bad)
		resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/interests", "admin-token", body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("slug %q status = %d, want 400", bad, resp.StatusCode)
		}
		if interestExists(t, pool, bad) {
			t.Errorf("slug %q was created despite invalid format", bad)
		}
	}
}

// TestAdminInterests_CreateRejectsDuplicateSlug asserts a second Create with
// the same slug is rejected with 409 and the original row is untouched.
func TestAdminInterests_CreateRejectsDuplicateSlug(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")
	slug := testInterestSlug(t, pool)

	first := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/interests", "admin-token",
		fmt.Sprintf(`{"slug":%q,"name":"First"}`, slug))
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", first.StatusCode)
	}

	second := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/interests", "admin-token",
		fmt.Sprintf(`{"slug":%q,"name":"Second"}`, slug))
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409", second.StatusCode)
	}
}

// TestAdminInterests_CreateRequiresName asserts an empty name is rejected
// with 400 before any row is inserted.
func TestAdminInterests_CreateRequiresName(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")
	slug := testInterestSlug(t, pool)

	resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/interests", "admin-token",
		fmt.Sprintf(`{"slug":%q,"name":""}`, slug))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if interestExists(t, pool, slug) {
		t.Errorf("interest was created despite empty name")
	}
}

// TestAdminInterests_CreateWritesAuditRow asserts a successful Create writes
// an interest.created row attributed to the acting admin.
func TestAdminInterests_CreateWritesAuditRow(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")
	slug := testInterestSlug(t, pool)

	resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/interests", "admin-token",
		fmt.Sprintf(`{"slug":%q,"name":"Audited"}`, slug))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	actions := auditActionsForSlug(t, pool, slug)
	if len(actions) != 1 || actions[0] != audit.ActionInterestCreated {
		t.Fatalf("audit actions for %q = %v, want exactly [interest.created]", slug, actions)
	}
}

// ── Patch ─────────────────────────────────────────────────────────────────────

// TestAdminInterests_PatchUpdatesFieldsAndRejectsSlug asserts a PATCH updates
// name/description/sort_order in the database, and that a body carrying a
// "slug" field is rejected with 400 (DisallowUnknownFields) rather than
// silently accepted or silently ignored — proving slugs are immutable
// through this endpoint, not just by convention.
func TestAdminInterests_PatchUpdatesFieldsAndRejectsSlug(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	istore := interests.NewStore(pool)
	slug := testInterestSlug(t, pool)
	created, err := istore.Create(context.Background(), slug, "Original", nil, 5)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp := doJSON(t, srv.Client(), http.MethodPatch,
		fmt.Sprintf("%s/admin/interests/%d", srv.URL, created.ID), "admin-token",
		`{"name":"Renamed","description":"a desc","sort_order":9}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	got := decodeInterest(t, body)
	if got.Name != "Renamed" || got.SortOrder != 9 || got.Description == nil || *got.Description != "a desc" {
		t.Fatalf("patched interest = %+v, want name=Renamed sort_order=9 description=\"a desc\"", got)
	}
	if got.Slug != slug {
		t.Fatalf("slug changed to %q, want unchanged %q", got.Slug, slug)
	}

	// A body carrying "slug" is rejected outright.
	slugAttempt := doJSON(t, srv.Client(), http.MethodPatch,
		fmt.Sprintf("%s/admin/interests/%d", srv.URL, created.ID), "admin-token",
		fmt.Sprintf(`{"slug":%q}`, slug+"-renamed"))
	defer slugAttempt.Body.Close()
	if slugAttempt.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH with slug field status = %d, want 400", slugAttempt.StatusCode)
	}
	reread, err := istore.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if reread.Slug != slug {
		t.Fatalf("slug is %q after rejected patch, want unchanged %q", reread.Slug, slug)
	}
}

// TestAdminInterests_PatchNotFound asserts a PATCH on a nonexistent id
// returns 404.
func TestAdminInterests_PatchNotFound(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	resp := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+"/admin/interests/99999999", "admin-token",
		`{"name":"X"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestAdminInterests_PatchDeactivateAndReactivateWriteDistinctAuditActions
// asserts flipping `active` false then true writes interest.deactivated then
// interest.reactivated (not a generic interest.updated), and that
// ListActive stops/resumes returning the interest across the flip.
func TestAdminInterests_PatchDeactivateAndReactivateWriteDistinctAuditActions(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	istore := interests.NewStore(pool)
	slug := testInterestSlug(t, pool)
	created, err := istore.Create(context.Background(), slug, "Togglable", nil, 0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	deact := doJSON(t, srv.Client(), http.MethodPatch,
		fmt.Sprintf("%s/admin/interests/%d", srv.URL, created.ID), "admin-token",
		`{"active":false}`)
	deact.Body.Close()
	if deact.StatusCode != http.StatusOK {
		t.Fatalf("deactivate status = %d, want 200", deact.StatusCode)
	}

	active, err := istore.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	for _, it := range active {
		if it.ID == created.ID {
			t.Fatalf("interest %d still in ListActive after deactivating", created.ID)
		}
	}

	react := doJSON(t, srv.Client(), http.MethodPatch,
		fmt.Sprintf("%s/admin/interests/%d", srv.URL, created.ID), "admin-token",
		`{"active":true}`)
	react.Body.Close()
	if react.StatusCode != http.StatusOK {
		t.Fatalf("reactivate status = %d, want 200", react.StatusCode)
	}

	actions := auditActionsForSlug(t, pool, slug)
	if len(actions) != 2 || actions[0] != audit.ActionInterestDeactivated || actions[1] != audit.ActionInterestReactivated {
		t.Fatalf("audit actions for %q = %v, want [interest.deactivated interest.reactivated]", slug, actions)
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

// TestAdminInterests_DeleteRefusedWithSubscribers asserts a DELETE on an
// interest with an associated subscriber is refused with 409, a clear
// "deactivate instead" message, no interest.deleted audit row, and the row
// still present.
func TestAdminInterests_DeleteRefusedWithSubscribers(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	istore := interests.NewStore(pool)
	slug := testInterestSlug(t, pool)
	created, err := istore.Create(context.Background(), slug, "Has a subscriber", nil, 0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedSubscriberLinkedToInterest(t, pool, created.ID)

	resp := doJSON(t, srv.Client(), http.MethodDelete,
		fmt.Sprintf("%s/admin/interests/%d", srv.URL, created.ID), "admin-token", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var errBody struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatalf("decode error body: %v (body=%s)", err, body)
	}
	if errBody.Error == "" {
		t.Fatalf("error message is empty, want a clear reason mentioning deactivation")
	}
	if !interestExists(t, pool, slug) {
		t.Fatalf("interest %q was deleted despite having a subscriber", slug)
	}
	actions := auditActionsForSlug(t, pool, slug)
	for _, a := range actions {
		if a == audit.ActionInterestDeleted {
			t.Fatalf("interest.deleted audit row written for a refused delete")
		}
	}
}

// TestAdminInterests_DeleteSucceedsWithoutSubscribers asserts a DELETE on an
// interest with zero subscribers removes the row and writes interest.deleted.
func TestAdminInterests_DeleteSucceedsWithoutSubscribers(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	istore := interests.NewStore(pool)
	slug := testInterestSlug(t, pool)
	created, err := istore.Create(context.Background(), slug, "Deletable", nil, 0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp := doJSON(t, srv.Client(), http.MethodDelete,
		fmt.Sprintf("%s/admin/interests/%d", srv.URL, created.ID), "admin-token", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	if interestExists(t, pool, slug) {
		t.Fatalf("interest %q still exists after a successful delete", slug)
	}
	actions := auditActionsForSlug(t, pool, slug)
	if len(actions) != 1 || actions[0] != audit.ActionInterestDeleted {
		t.Fatalf("audit actions for %q = %v, want exactly [interest.deleted]", slug, actions)
	}
}

// TestAdminInterests_DeleteNotFound asserts a DELETE on a nonexistent id
// returns 404.
func TestAdminInterests_DeleteNotFound(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminInterestsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	resp := doJSON(t, srv.Client(), http.MethodDelete, srv.URL+"/admin/interests/99999999", "admin-token", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
