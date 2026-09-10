package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/crt"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// crtTestPool returns the package's single shared pool (opened once in
// TestMain — #0091) or skips if TEST_DATABASE_URL was unset. Truncates the
// auth tables on entry, exactly like interestsTestPool (settings_test.go's
// seedAdmin/seedUser use fixed literal emails per call site, so a prior
// test's row must be gone before the next one seeds the same address).
// Deliberately NEVER truncates crt_commands itself: migrations/000028 seeds
// it with eighteen rows every other test in this file may rely on being
// present, and tests here scope their OWN rows with a "zz-test-" slug
// prefix and clean up individually, matching interestsTestPool's own
// convention for `interests`.
func crtTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	truncateCredsTables(t, testDBPool)
	return testDBPool
}

// testCrtSlug returns a slug scoped to this test run and registers cleanup
// that deletes any row left behind under it.
func testCrtSlug(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	slug := fmt.Sprintf("zz-test-%d", testdb.Unique())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM crt_commands WHERE slug = $1`, slug)
	})
	return slug
}

// adminCrtCommandsMux wires the real admin CRT-commands CRUD routes guarded
// by RequireSession then RequireAdmin, backed by a real *auth.Store (session
// resolution) and a real *crt.Store + audit.Logger (the data layer),
// mirroring adminInterestsMux's own construction exactly.
//
// It also mounts the public read (GET /api/crt-session) alongside, and
// returns the *PublicCrtSessionHandler it built so a caller can advance its
// clock (h.now) past crtSessionTTL — the same TTL the real handler uses in
// production, which would otherwise silently serve a cached, pre-mutation
// response to a test polling it right after a PATCH. Most callers ignore
// the second return value; TestAdminCrtCommands_ReorderChangesPublicEndpointOrder
// and TestAdminCrtCommands_DeactivateRemovesFromPublicThenReactivateRestoresPosition
// are the two that need it, per #0393's acceptance criteria ("changes the
// order the public endpoint serves").
func adminCrtCommandsMux(pool *pgxpool.Pool) (http.Handler, *PublicCrtSessionHandler) {
	authStore := auth.NewStore(pool)
	crtStore := crt.NewStore(pool)
	h := NewAdminCrtCommandsHandler(crtStore, audit.New(pool))
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/crt-commands", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("POST /admin/crt-commands", requireAdmin(http.HandlerFunc(h.Create)))
	mux.Handle("PATCH /admin/crt-commands/{id}", requireAdmin(http.HandlerFunc(h.Patch)))
	mux.Handle("DELETE /admin/crt-commands/{id}", requireAdmin(http.HandlerFunc(h.Delete)))
	publicH := NewPublicCrtSessionHandler(crtStore)
	mux.Handle("GET /api/crt-session", http.HandlerFunc(publicH.List))
	return mux, publicH
}

type decodedCrtCommand struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Command   string `json:"command"`
	Output    string `json:"output"`
	Source    string `json:"source"`
	SortOrder int    `json:"sort_order"`
	Active    bool   `json:"active"`
}

func decodeCrtCommand(t *testing.T, body []byte) decodedCrtCommand {
	t.Helper()
	var c decodedCrtCommand
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatalf("decode crt command: %v (body=%s)", err, body)
	}
	return c
}

func decodeCrtCommandsList(t *testing.T, body []byte) []decodedCrtCommand {
	t.Helper()
	var resp struct {
		Commands []decodedCrtCommand `json:"commands"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode crt commands list: %v (body=%s)", err, body)
	}
	return resp.Commands
}

type decodedPublicCrtCommand struct {
	Cmd    string   `json:"cmd"`
	Out    []string `json:"out"`
	Source string   `json:"source"`
}

func decodePublicCrtSession(t *testing.T, body []byte) []decodedPublicCrtCommand {
	t.Helper()
	var resp struct {
		Commands []decodedPublicCrtCommand `json:"commands"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode public crt session: %v (body=%s)", err, body)
	}
	return resp.Commands
}

func crtCommandExists(t *testing.T, pool *pgxpool.Pool, slug string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM crt_commands WHERE slug = $1)`, slug,
	).Scan(&exists); err != nil {
		t.Fatalf("check crt command exists %q: %v", slug, err)
	}
	return exists
}

// auditActionsForCrtSlug mirrors auditActionsForSlug (interests_test.go).
func auditActionsForCrtSlug(t *testing.T, pool *pgxpool.Pool, slug string) []string {
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

// TestAdminCrtCommands_NonAdminForbidden asserts a non-admin with a valid
// session is rejected with 403 on all four routes, and that no mutation
// occurs -- mirrors TestAdminInterests_NonAdminForbidden.
func TestAdminCrtCommands_NonAdminForbidden(t *testing.T) {
	pool := crtTestPool(t)
	mux, _ := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	user := seedUser(t, pool, "regular-crt@example.com") // is_admin = FALSE
	seedSession(t, pool, user, "user-crt-token")
	slug := testCrtSlug(t, pool)

	targetSlug := testCrtSlug(t, pool)
	target, err := crt.NewStore(pool).Create(context.Background(), targetSlug, "guard --target", "guard target output", crt.SourceStatic, 0)
	if err != nil {
		t.Fatalf("seed target command: %v", err)
	}
	targetPath := fmt.Sprintf("/admin/crt-commands/%d", target.ID)

	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/admin/crt-commands", ""},
		{http.MethodPost, "/admin/crt-commands", fmt.Sprintf(`{"slug":%q,"command":"x","output":"y"}`, slug)},
		{http.MethodPatch, targetPath, `{"command":"x"}`},
		{http.MethodDelete, targetPath, ""},
	}
	for _, c := range cases {
		resp := doJSON(t, srv.Client(), c.method, srv.URL+c.path, "user-crt-token", c.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s (non-admin) status = %d, want 403", c.method, c.path, resp.StatusCode)
		}
	}
	if crtCommandExists(t, pool, slug) {
		t.Errorf("command %q exists after a forbidden POST, want no row created", slug)
	}
	if !crtCommandExists(t, pool, targetSlug) {
		t.Errorf("guard target %q was deleted despite a forbidden DELETE", targetSlug)
	}
}

// TestAdminCrtCommands_Unauthenticated asserts a request with no session
// cookie is rejected with 401 on all four routes.
func TestAdminCrtCommands_Unauthenticated(t *testing.T) {
	pool := crtTestPool(t)
	mux, _ := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	targetSlug := testCrtSlug(t, pool)
	target, err := crt.NewStore(pool).Create(context.Background(), targetSlug, "guard --target", "guard target output", crt.SourceStatic, 0)
	if err != nil {
		t.Fatalf("seed target command: %v", err)
	}
	targetPath := fmt.Sprintf("/admin/crt-commands/%d", target.ID)

	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/admin/crt-commands", ""},
		{http.MethodPost, "/admin/crt-commands", `{"slug":"zz-test-anon","command":"x","output":"y"}`},
		{http.MethodPatch, targetPath, `{"command":"x"}`},
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

func TestAdminCrtCommands_ListIncludesSeededDefaultSession(t *testing.T) {
	pool := crtTestPool(t)
	mux, _ := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-crt-list@example.com")
	seedSession(t, pool, admin, "admin-crt-list-token")

	resp := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/admin/crt-commands", "admin-crt-list-token", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	list := decodeCrtCommandsList(t, body)
	if len(list) < 18 {
		t.Fatalf("list has %d commands, want at least the 18 seeded", len(list))
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestAdminCrtCommands_CreateValidatesSlugAndSource(t *testing.T) {
	pool := crtTestPool(t)
	mux, _ := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-crt-create@example.com")
	seedSession(t, pool, admin, "admin-crt-create-token")

	for _, bad := range []string{"Upper-Case", "has_underscore", "trailing-"} {
		body := fmt.Sprintf(`{"slug":%q,"command":"x","output":"y"}`, bad)
		resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/crt-commands", "admin-crt-create-token", body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("slug %q status = %d, want 400", bad, resp.StatusCode)
		}
		if crtCommandExists(t, pool, bad) {
			t.Errorf("slug %q was created despite invalid format", bad)
		}
	}

	slug := testCrtSlug(t, pool)
	badSource := fmt.Sprintf(`{"slug":%q,"command":"x","output":"y","source":"not-a-source"}`, slug)
	resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/crt-commands", "admin-crt-create-token", badSource)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad source status = %d, want 400", resp.StatusCode)
	}
	if crtCommandExists(t, pool, slug) {
		t.Errorf("command %q was created despite invalid source", slug)
	}
}

func TestAdminCrtCommands_CreateRejectsDuplicateSlug(t *testing.T) {
	pool := crtTestPool(t)
	mux, _ := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-crt-dup@example.com")
	seedSession(t, pool, admin, "admin-crt-dup-token")
	slug := testCrtSlug(t, pool)

	first := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/crt-commands", "admin-crt-dup-token",
		fmt.Sprintf(`{"slug":%q,"command":"x","output":"y"}`, slug))
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", first.StatusCode)
	}

	second := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/crt-commands", "admin-crt-dup-token",
		fmt.Sprintf(`{"slug":%q,"command":"x2","output":"y2"}`, slug))
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409", second.StatusCode)
	}
}

func TestAdminCrtCommands_CreateWritesAuditRow(t *testing.T) {
	pool := crtTestPool(t)
	mux, _ := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-crt-audit@example.com")
	seedSession(t, pool, admin, "admin-crt-audit-token")
	slug := testCrtSlug(t, pool)

	resp := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/crt-commands", "admin-crt-audit-token",
		fmt.Sprintf(`{"slug":%q,"command":"audited","output":"line"}`, slug))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	actions := auditActionsForCrtSlug(t, pool, slug)
	if len(actions) != 1 || actions[0] != audit.ActionCrtCommandCreated {
		t.Fatalf("audit actions for %q = %v, want exactly [crt_command.created]", slug, actions)
	}
}

// ── Patch: fields, reorder, deactivate/reactivate ────────────────────────────

func TestAdminCrtCommands_PatchUpdatesFieldsAndRejectsSlug(t *testing.T) {
	pool := crtTestPool(t)
	mux, _ := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-crt-patch@example.com")
	seedSession(t, pool, admin, "admin-crt-patch-token")

	cstore := crt.NewStore(pool)
	slug := testCrtSlug(t, pool)
	created, err := cstore.Create(context.Background(), slug, "original", "orig line", crt.SourceStatic, 5)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp := doJSON(t, srv.Client(), http.MethodPatch,
		fmt.Sprintf("%s/admin/crt-commands/%d", srv.URL, created.ID), "admin-crt-patch-token",
		`{"command":"renamed","output":"new line","sort_order":9}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	got := decodeCrtCommand(t, body)
	if got.Command != "renamed" || got.Output != "new line" || got.SortOrder != 9 {
		t.Fatalf("patched command = %+v, want command=renamed output=\"new line\" sort_order=9", got)
	}
	if got.Slug != slug {
		t.Fatalf("slug changed to %q, want unchanged %q", got.Slug, slug)
	}

	// A body carrying "slug" is rejected outright (DisallowUnknownFields).
	slugAttempt := doJSON(t, srv.Client(), http.MethodPatch,
		fmt.Sprintf("%s/admin/crt-commands/%d", srv.URL, created.ID), "admin-crt-patch-token",
		fmt.Sprintf(`{"slug":%q}`, slug+"-renamed"))
	defer slugAttempt.Body.Close()
	if slugAttempt.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH with slug field status = %d, want 400", slugAttempt.StatusCode)
	}
	reread, err := cstore.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if reread.Slug != slug {
		t.Fatalf("slug is %q after rejected patch, want unchanged %q", reread.Slug, slug)
	}
}

func TestAdminCrtCommands_PatchNotFound(t *testing.T) {
	pool := crtTestPool(t)
	mux, _ := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-crt-patch-404@example.com")
	seedSession(t, pool, admin, "admin-crt-patch-404-token")

	resp := doJSON(t, srv.Client(), http.MethodPatch, srv.URL+"/admin/crt-commands/99999999", "admin-crt-patch-404-token",
		`{"command":"x"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestAdminCrtCommands_ReorderChangesPublicEndpointOrder is #0393's
// acceptance criterion "Reordering two rows in the admin UI changes the
// order the public endpoint serves", proved end to end: two rows are seeded
// adjacent and active, their sort_order swapped via two PATCHes (the same
// shape the admin screen's ↑/↓ controls use, per lib/admin.ts's
// reorderSwap), and the live GET /api/crt-session response is asserted to
// reflect the new order.
func TestAdminCrtCommands_ReorderChangesPublicEndpointOrder(t *testing.T) {
	pool := crtTestPool(t)
	mux, publicH := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// The public endpoint caches for crtSessionTTL (60s) in production, and
	// that same handler is mounted here — an un-advanced clock would have
	// this test's post-PATCH poll silently serve the pre-swap cached
	// response and never observe the reorder at all. Advanced past the TTL
	// immediately before the post-mutation check below.
	fakeNow := time.Now()
	publicH.now = func() time.Time { return fakeNow }

	admin := seedAdmin(t, pool, "admin-crt-reorder@example.com")
	seedSession(t, pool, admin, "admin-crt-reorder-token")

	cstore := crt.NewStore(pool)
	slugA := testCrtSlug(t, pool)
	slugB := testCrtSlug(t, pool)
	// Sort_order values far outside the seeded 10-180 range so this pair's
	// relative order is unambiguous regardless of the seed.
	a, err := cstore.Create(context.Background(), slugA, "cmd-a", "out-a", crt.SourceStatic, 9000)
	if err != nil {
		t.Fatalf("seed a: %v", err)
	}
	b, err := cstore.Create(context.Background(), slugB, "cmd-b", "out-b", crt.SourceStatic, 9010)
	if err != nil {
		t.Fatalf("seed b: %v", err)
	}

	orderOf := func(cmd string) int {
		resp := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/api/crt-session", "", "")
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		list := decodePublicCrtSession(t, body)
		for i, c := range list {
			if c.Cmd == cmd {
				return i
			}
		}
		t.Fatalf("command %q missing from public session (body=%s)", cmd, body)
		return -1
	}

	if orderOf("cmd-a") >= orderOf("cmd-b") {
		t.Fatalf("precondition failed: cmd-a should be before cmd-b before the swap")
	}

	// Swap: a takes b's sort_order and vice versa.
	swapA := doJSON(t, srv.Client(), http.MethodPatch,
		fmt.Sprintf("%s/admin/crt-commands/%d", srv.URL, a.ID), "admin-crt-reorder-token",
		`{"sort_order":9010}`)
	swapA.Body.Close()
	if swapA.StatusCode != http.StatusOK {
		t.Fatalf("swap a status = %d, want 200", swapA.StatusCode)
	}
	swapB := doJSON(t, srv.Client(), http.MethodPatch,
		fmt.Sprintf("%s/admin/crt-commands/%d", srv.URL, b.ID), "admin-crt-reorder-token",
		`{"sort_order":9000}`)
	swapB.Body.Close()
	if swapB.StatusCode != http.StatusOK {
		t.Fatalf("swap b status = %d, want 200", swapB.StatusCode)
	}

	fakeNow = fakeNow.Add(crtSessionTTL + time.Second)
	if orderOf("cmd-b") >= orderOf("cmd-a") {
		t.Fatalf("public endpoint order unchanged after reorder: want cmd-b before cmd-a")
	}
}

// TestAdminCrtCommands_DeactivateRemovesFromPublicThenReactivateRestoresPosition
// is #0393's acceptance criterion "Deactivating a row removes it from the
// public endpoint; reactivating restores it at its old position".
func TestAdminCrtCommands_DeactivateRemovesFromPublicThenReactivateRestoresPosition(t *testing.T) {
	pool := crtTestPool(t)
	mux, publicH := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Same cache-TTL concern as TestAdminCrtCommands_ReorderChangesPublicEndpointOrder
	// above: without an advanceable clock, every poll after the first would
	// silently return the cached pre-mutation response.
	fakeNow := time.Now()
	publicH.now = func() time.Time { return fakeNow }

	admin := seedAdmin(t, pool, "admin-crt-toggle@example.com")
	seedSession(t, pool, admin, "admin-crt-toggle-token")

	cstore := crt.NewStore(pool)
	slug := testCrtSlug(t, pool)
	created, err := cstore.Create(context.Background(), slug, "togglable --cmd", "togglable out", crt.SourceStatic, 9500)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	inPublicSession := func() bool {
		resp := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/api/crt-session", "", "")
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		for _, c := range decodePublicCrtSession(t, body) {
			if c.Cmd == "togglable --cmd" {
				return true
			}
		}
		return false
	}

	if !inPublicSession() {
		t.Fatalf("newly created active command missing from public session before deactivating")
	}

	deact := doJSON(t, srv.Client(), http.MethodPatch,
		fmt.Sprintf("%s/admin/crt-commands/%d", srv.URL, created.ID), "admin-crt-toggle-token",
		`{"active":false}`)
	deact.Body.Close()
	if deact.StatusCode != http.StatusOK {
		t.Fatalf("deactivate status = %d, want 200", deact.StatusCode)
	}
	fakeNow = fakeNow.Add(crtSessionTTL + time.Second)
	if inPublicSession() {
		t.Fatalf("deactivated command still present in public session")
	}

	react := doJSON(t, srv.Client(), http.MethodPatch,
		fmt.Sprintf("%s/admin/crt-commands/%d", srv.URL, created.ID), "admin-crt-toggle-token",
		`{"active":true}`)
	react.Body.Close()
	if react.StatusCode != http.StatusOK {
		t.Fatalf("reactivate status = %d, want 200", react.StatusCode)
	}
	fakeNow = fakeNow.Add(crtSessionTTL + time.Second)
	if !inPublicSession() {
		t.Fatalf("reactivated command missing from public session")
	}

	reread, err := cstore.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if reread.SortOrder != 9500 {
		t.Fatalf("sort_order changed across deactivate/reactivate: got %d, want unchanged 9500", reread.SortOrder)
	}

	actions := auditActionsForCrtSlug(t, pool, slug)
	if len(actions) < 2 {
		t.Fatalf("audit actions for %q = %v, want at least a create and two updates", slug, actions)
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestAdminCrtCommands_DeleteSucceedsUnconditionally(t *testing.T) {
	pool := crtTestPool(t)
	mux, _ := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-crt-delete@example.com")
	seedSession(t, pool, admin, "admin-crt-delete-token")

	cstore := crt.NewStore(pool)
	slug := testCrtSlug(t, pool)
	created, err := cstore.Create(context.Background(), slug, "deletable", "out", crt.SourceStatic, 0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp := doJSON(t, srv.Client(), http.MethodDelete,
		fmt.Sprintf("%s/admin/crt-commands/%d", srv.URL, created.ID), "admin-crt-delete-token", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	if crtCommandExists(t, pool, slug) {
		t.Fatalf("command %q still exists after a successful delete", slug)
	}
	// The row was seeded directly through the store (crt.NewStore.Create),
	// not via POST /admin/crt-commands, so there is no crt_command.created
	// row to expect here -- only the DELETE went through the handler.
	actions := auditActionsForCrtSlug(t, pool, slug)
	if len(actions) != 1 || actions[0] != audit.ActionCrtCommandDeleted {
		t.Fatalf("audit actions for %q = %v, want exactly [crt_command.deleted]", slug, actions)
	}
}

func TestAdminCrtCommands_DeleteNotFound(t *testing.T) {
	pool := crtTestPool(t)
	mux, _ := adminCrtCommandsMux(pool)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-crt-delete-404@example.com")
	seedSession(t, pool, admin, "admin-crt-delete-404-token")

	resp := doJSON(t, srv.Client(), http.MethodDelete, srv.URL+"/admin/crt-commands/99999999", "admin-crt-delete-404-token", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
