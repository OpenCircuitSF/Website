package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// TestValidSettingValue_MaxSendRate is #0045's carried-in obligation: only
// registrations_enabled was validated before this issue, which is exactly
// how a whitespace physical_address became possible (CLAUDE.md's own Notes
// on #0045). max_send_rate must be a positive integer in 1..1000 — an admin
// saving "fast" must get a 400, not a worker that silently falls back to
// MAX_SEND_RATE.
func TestValidSettingValue_MaxSendRate(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"10", true},
		{"1", true},
		{"1000", true},
		{"0", false},
		{"-5", false},
		{"1001", false},
		{"fast", false},
		{"", false},
		{" 10", false},
		{"10.5", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			if got := validSettingValue("max_send_rate", tc.value); got != tc.want {
				t.Errorf("validSettingValue(%q, %q) = %v, want %v", "max_send_rate", tc.value, got, tc.want)
			}
		})
	}
}

// settingsTestPool returns the package's single shared pool (opened once in
// TestMain — #0091) or skips if TEST_DATABASE_URL was unset. It truncates the
// auth tables AND resets the settings table to its seeded state on entry,
// so each run starts clean and the registrations_enabled gate begins
// disabled (matching the migration seed). The settings table is not covered
// by the credentials suite's truncate, so it is reset explicitly here. It
// also registers a t.Cleanup that resets settings again on teardown, so a
// test that mutates settings (e.g. TestAdminSettings_PhysicalAddressRoundTrip,
// or #0039's threshold tests) cannot leak that state into whichever test
// runs next under -shuffle=on (#0121).
func settingsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	truncateCredsTables(t, testDBPool)
	resetSettings(t, testDBPool)
	t.Cleanup(func() { resetSettings(t, testDBPool) })
	return testDBPool
}

// settingsMigrationsDir is where resetSettings looks for the migration seed
// — the repo's migrations/ directory, relative to this package's own
// directory (`go test` runs with the package directory as its working
// directory). settingsSeedStatements reads it as a var, not a constant, so
// TestResetSettings_PreservesFutureMigrationSeed can point it at a fixture
// directory to prove resetSettings needs no code change for a new seeded
// key, then restore it.
//
// That global-var swap-and-restore is safe only because this package has
// zero t.Parallel() calls — two tests here never interleave, so no other
// test can observe the fixture directory mid-swap. Adding a t.Parallel()
// call anywhere in this package would reopen that window (#0217, following
// #0215's review notes on the same hazard one package over, in
// internal/auth's setRegistrationsEnabled).
var settingsMigrationsDir = "../../migrations"

// settingsSeedStmtRe matches one `INSERT INTO settings ...;` statement,
// case-insensitively, anchored to the start of a line (allowing leading
// horizontal whitespace, #0217 — a statement indented inside e.g. a `DO`
// block or just a differently-styled migration file would otherwise silently
// fail to match) so a doc comment that merely mentions "settings" cannot
// match. `(?s)` lets `.` cross the statement's own newlines; the match still
// stops at the first `;`, which is safe here because none of these
// statements contain a semicolon in a string or subquery.
var settingsSeedStmtRe = regexp.MustCompile(`(?ims)^[ \t]*INSERT INTO settings\b.*?;`)

// settingsSeedStatements extracts every `INSERT INTO settings ...;`
// statement out of every *.up.sql file in settingsMigrationsDir, in
// filename order, and returns them verbatim.
//
// #0132: resetSettings used to restate the migration seed as hand-typed
// INSERTs (registrations_enabled from 000004, physical_address from 000008,
// soft_bounce_threshold_count and #0124's three send_health_* rows from
// 000015, max_send_rate and default_from_name from 000018) — a second,
// hand-maintained copy of what migrations/ already seeds. #0130 had to fix
// that copy once already when it silently dropped two of the six; the
// duplication itself was still there and would silently drop the *next*
// migration's new row the same way. This reads the actual SQL the
// migrations run — not a Go-side restatement, not a snapshot of a live
// table (which a differently-ordered package's uncleaned test data can
// pollute before this package's own tests ever run) — so a migration that
// adds a new `INSERT INTO settings ... ON CONFLICT (key) DO NOTHING`
// statement is picked up automatically, with no matching edit here.
func settingsSeedStatements() ([]string, error) {
	entries, err := os.ReadDir(settingsMigrationsDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", settingsMigrationsDir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var stmts []string
	for _, name := range names {
		content, err := os.ReadFile(filepath.Join(settingsMigrationsDir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		stmts = append(stmts, settingsSeedStmtRe.FindAllString(string(content), -1)...)
	}
	if len(stmts) == 0 {
		return nil, fmt.Errorf("no INSERT INTO settings statements found under %s", settingsMigrationsDir)
	}
	return stmts, nil
}

// resetSettings restores the settings table to exactly what a fresh
// migration run produces: it clears the table, then replays every
// `INSERT INTO settings ...;` statement settingsSeedStatements finds in
// migrations/*.up.sql. This isolates each settings test from values left by
// earlier tests (the auth suite flips the registrations_enabled row;
// #0039's own tests may flip the threshold rows to exercise
// configurability). Callers that want the DB left in the seeded state on
// teardown, not just on entry, must register their own
// t.Cleanup(func() { resetSettings(t, pool) }) — settingsTestPool does this
// already.
func resetSettings(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	stmts, err := settingsSeedStatements()
	if err != nil {
		t.Fatalf("resetSettings: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
	defer cancel()
	if _, err := pool.Exec(ctx, `DELETE FROM settings`); err != nil {
		t.Fatalf("clear settings: %v", err)
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("replay migration seed statement %q: %v", stmt, err)
		}
	}
}

// TestSettingsSeedStmtRe_MatchesIndentedInsert is #0217's fix to a silent
// false negative in #0132's original `^INSERT` anchor: a migration whose
// `INSERT INTO settings ...;` statement is indented (inside a `DO` block, or
// just a differently-styled file) would fail to match and would be dropped
// from the seed with no error anywhere — settingsSeedStatements only
// t.Fatalf's when it finds zero statements across every migration file, not
// when it silently misses one inside a file that has others. No live
// database needed: this asserts directly against the compiled regex.
func TestSettingsSeedStmtRe_MatchesIndentedInsert(t *testing.T) {
	sql := "  \tINSERT INTO settings (key, value, updated_at)\n" +
		"VALUES ('indented_test_key', 'yes', now())\n" +
		"ON CONFLICT (key) DO NOTHING;\n"
	matches := settingsSeedStmtRe.FindAllString(sql, -1)
	if len(matches) != 1 {
		t.Fatalf("FindAllString found %d matches, want 1 for an indented INSERT INTO settings statement (sql=%q)", len(matches), sql)
	}
	if !strings.Contains(matches[0], "indented_test_key") {
		t.Errorf("match = %q, want it to contain the indented statement's key", matches[0])
	}
}

// TestResetSettings_PreservesFutureMigrationSeed is #0132's mutation proof.
// A hand-maintained literal list in resetSettings (the pre-#0132 shape)
// would silently drop any settings row a future migration seeds that the
// list doesn't name — exactly what #0130 found and fixed for two rows, and
// exactly the defect class this issue closes structurally rather than by
// re-syncing the copy.
//
// It points settingsMigrationsDir at a throwaway directory holding a single
// fixture *.up.sql file — standing in for a brand new migration that seeds
// a settings key resetSettings has never heard of — and asserts the key
// survives a DELETE-and-reseed with zero edits to resetSettings or to any
// list of known keys, because there is no such list any more.
func TestResetSettings_PreservesFutureMigrationSeed(t *testing.T) {
	pool := settingsTestPool(t)
	ctx := context.Background()

	extraKey := fmt.Sprintf("zz_test_future_setting_%d", testdb.Unique())
	fixtureDir := t.TempDir()
	fixture := fmt.Sprintf(
		"-- fixture standing in for a future migration (#0132's mutation proof)\n"+
			"INSERT INTO settings (key, value, updated_at)\n"+
			"VALUES ('%s', 'present', now())\n"+
			"ON CONFLICT (key) DO NOTHING;\n", extraKey)
	if err := os.WriteFile(filepath.Join(fixtureDir, "999999_future.up.sql"), []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture migration: %v", err)
	}

	saved := settingsMigrationsDir
	settingsMigrationsDir = fixtureDir
	t.Cleanup(func() {
		settingsMigrationsDir = saved
		resetSettings(t, pool)
	})

	resetSettings(t, pool)

	var value string
	err := pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, extraKey).Scan(&value)
	if err != nil {
		t.Fatalf("expected %s to survive resetSettings, got: %v", extraKey, err)
	}
	if value != "present" {
		t.Errorf("value = %q, want %q", value, "present")
	}
}

// seedAdmin inserts an admin account and returns its id.
func seedAdmin(t *testing.T, pool *pgxpool.Pool, email string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, is_admin, active, created_at)
		 VALUES ($1, TRUE, TRUE, now()) RETURNING id`, email,
	).Scan(&id); err != nil {
		t.Fatalf("seed admin %s: %v", email, err)
	}
	return id
}

// settingValue reads the current value of a settings key.
func settingValue(t *testing.T, pool *pgxpool.Pool, key string) string {
	t.Helper()
	ctx := context.Background()
	var v string
	if err := pool.QueryRow(ctx,
		`SELECT value FROM settings WHERE key = $1`, key).Scan(&v); err != nil {
		t.Fatalf("read setting %q: %v", key, err)
	}
	return v
}

// settingUpdatedAt reads the current updated_at of a settings key.
func settingUpdatedAt(t *testing.T, pool *pgxpool.Pool, key string) time.Time {
	t.Helper()
	ctx := context.Background()
	var ts time.Time
	if err := pool.QueryRow(ctx,
		`SELECT updated_at FROM settings WHERE key = $1`, key).Scan(&ts); err != nil {
		t.Fatalf("read setting updated_at %q: %v", key, err)
	}
	return ts
}

// adminMux builds the real admin settings route table guarded by RequireSession
// then RequireAdmin, backed by the real *auth.Store. Requests therefore flow
// through the genuine session + admin middleware, proving the routes are
// protected exactly as wired in main.go.
func adminMux(store *auth.Store) http.Handler {
	h := NewSettingsHandler(store, nil)
	requireSession := middleware.RequireSession(store)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/settings", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("PATCH /admin/settings", requireAdmin(http.HandlerFunc(h.Patch)))
	return mux
}

// decodeSettings parses a settingsResponse body keyed by setting key.
func decodeSettings(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var resp struct {
		Settings []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode settings: %v (body=%s)", err, body)
	}
	out := make(map[string]string, len(resp.Settings))
	for _, s := range resp.Settings {
		out[s.Key] = s.Value
	}
	return out
}

// TestAdminSettings_NonAdminForbidden asserts a non-admin user with a VALID
// session is rejected with 403 on both GET and PATCH — proving RequireAdmin
// guards the routes and is reached only after RequireSession succeeds.
func TestAdminSettings_NonAdminForbidden(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	user := seedUser(t, pool, "regular@example.com") // is_admin = FALSE
	seedSession(t, pool, user, "user-token")

	// GET → 403
	getReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/settings", nil)
	getResp, err := srv.Client().Do(withCookie(getReq, "user-token"))
	if err != nil {
		t.Fatalf("GET request: %v", err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET non-admin status = %d, want 403", getResp.StatusCode)
	}

	// PATCH → 403, and the row must not change.
	patchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
		jsonBody(`{"key":"registrations_enabled","value":"true"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := srv.Client().Do(withCookie(patchReq, "user-token"))
	if err != nil {
		t.Fatalf("PATCH request: %v", err)
	}
	patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusForbidden {
		t.Fatalf("PATCH non-admin status = %d, want 403", patchResp.StatusCode)
	}
	if got := settingValue(t, pool, "registrations_enabled"); got != "false" {
		t.Errorf("registrations_enabled = %q after forbidden PATCH, want unchanged false", got)
	}
}

// TestAdminSettings_Unauthenticated asserts a request with no session cookie is
// rejected with 401 on both routes — proving the session guard runs first.
func TestAdminSettings_Unauthenticated(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	getReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/settings", nil)
	getResp, err := srv.Client().Do(getReq) // no cookie
	if err != nil {
		t.Fatalf("GET request: %v", err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET unauthenticated status = %d, want 401", getResp.StatusCode)
	}

	patchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
		jsonBody(`{"key":"registrations_enabled","value":"true"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := srv.Client().Do(patchReq) // no cookie
	if err != nil {
		t.Fatalf("PATCH request: %v", err)
	}
	patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("PATCH unauthenticated status = %d, want 401", patchResp.StatusCode)
	}
}

// TestAdminSettings_GetReturnsSeeded asserts an admin GET returns the seeded
// registrations_enabled=false in the {"settings":[...]} shape.
func TestAdminSettings_GetReturnsSeeded(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/settings", nil)
	resp, err := srv.Client().Do(withCookie(req, "admin-token"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	settings := decodeSettings(t, body)
	if settings["registrations_enabled"] != "false" {
		t.Errorf("registrations_enabled = %q, want false", settings["registrations_enabled"])
	}
}

// TestAdminSettings_PatchUpdatesRow asserts an admin PATCH flips
// registrations_enabled to true: the DB value changes, updated_at advances, and
// the response carries the new value.
func TestAdminSettings_PatchUpdatesRow(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	before := settingUpdatedAt(t, pool, "registrations_enabled")
	// Ensure a measurable clock tick so the updated_at advance is observable.
	time.Sleep(2 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
		jsonBody(`{"key":"registrations_enabled","value":"true"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(withCookie(req, "admin-token"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	settings := decodeSettings(t, body)
	if settings["registrations_enabled"] != "true" {
		t.Errorf("response registrations_enabled = %q, want true", settings["registrations_enabled"])
	}
	if got := settingValue(t, pool, "registrations_enabled"); got != "true" {
		t.Errorf("DB registrations_enabled = %q, want true", got)
	}
	if after := settingUpdatedAt(t, pool, "registrations_enabled"); !after.After(before) {
		t.Errorf("updated_at not advanced: before %v after %v", before, after)
	}
}

// TestAdminSettings_PhysicalAddressRoundTrip is #0009's criterion that the
// Settings screen can edit physical_address as a setting (not an env var, per
// #0007's Notes — #0045's send worker reads it fresh from this table at send
// time). Confirms: it is present (seeded empty by migration 000008), any
// free-text value is accepted (no format validation, unlike
// registrations_enabled), and it round-trips through both the response body
// and a fresh DB read.
func TestAdminSettings_PhysicalAddressRoundTrip(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	// Seeded empty.
	if got := settingValue(t, pool, "physical_address"); got != "" {
		t.Fatalf("seeded physical_address = %q, want empty", got)
	}

	const addr = "Open Circuit SF, PO Box 1234, San Francisco, CA 94104"
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
		jsonBody(`{"key":"physical_address","value":"`+addr+`"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(withCookie(req, "admin-token"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	settings := decodeSettings(t, body)
	if settings["physical_address"] != addr {
		t.Errorf("response physical_address = %q, want %q", settings["physical_address"], addr)
	}
	if got := settingValue(t, pool, "physical_address"); got != addr {
		t.Errorf("DB physical_address = %q, want %q", got, addr)
	}

	// A subsequent GET reflects the same value, proving the Settings tab reads
	// it back through the same envelope it edits it through.
	getReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/settings", nil)
	getResp, err := srv.Client().Do(withCookie(getReq, "admin-token"))
	if err != nil {
		t.Fatalf("GET request: %v", err)
	}
	defer getResp.Body.Close()
	getBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("read GET body: %v", err)
	}
	if got := decodeSettings(t, getBody)["physical_address"]; got != addr {
		t.Errorf("GET physical_address = %q, want %q", got, addr)
	}
}

// TestAdminSettings_PatchInvalidValue asserts a non-boolean value for
// registrations_enabled is rejected with 400 and the row is left unchanged.
func TestAdminSettings_PatchInvalidValue(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
		jsonBody(`{"key":"registrations_enabled","value":"maybe"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(withCookie(req, "admin-token"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := settingValue(t, pool, "registrations_enabled"); got != "false" {
		t.Errorf("registrations_enabled = %q after invalid PATCH, want unchanged false", got)
	}
}

// TestAdminSettings_PatchUnknownKey asserts a key absent from the settings table
// is rejected with 400 (no arbitrary key creation) and no new row is inserted.
func TestAdminSettings_PatchUnknownKey(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
		jsonBody(`{"key":"some_unknown_key","value":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(withCookie(req, "admin-token"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	ctx := context.Background()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM settings WHERE key = 'some_unknown_key')`).Scan(&exists); err != nil {
		t.Fatalf("check unknown key: %v", err)
	}
	if exists {
		t.Error("an unknown key was created by PATCH; arbitrary key creation must be forbidden")
	}
}

// TestAdminSettings_SoftBounceThresholdRoundTrip is #0039's "the threshold
// must be configurable" acceptance criterion (#0124 retired the companion
// window setting — see that test below): an admin can PATCH the streak
// threshold through the same envelope as any other setting, and the new
// value round-trips through the response and a fresh DB read.
func TestAdminSettings_SoftBounceThresholdRoundTrip(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	// Seeded by migrations/000015.
	if got := settingValue(t, pool, "soft_bounce_threshold_count"); got != "5" {
		t.Fatalf("seeded soft_bounce_threshold_count = %q, want 5", got)
	}

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
		jsonBody(`{"key":"soft_bounce_threshold_count","value":"3"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(withCookie(req, "admin-token"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	settings := decodeSettings(t, body)
	if settings["soft_bounce_threshold_count"] != "3" {
		t.Errorf("response soft_bounce_threshold_count = %q, want 3", settings["soft_bounce_threshold_count"])
	}
	if got := settingValue(t, pool, "soft_bounce_threshold_count"); got != "3" {
		t.Errorf("DB soft_bounce_threshold_count = %q, want 3", got)
	}
}

// TestAdminSettings_SoftBounceThresholdWindowDaysRetired is #0124's
// regression guard for the retirement itself: soft_bounce_threshold_count's
// old companion window setting no longer exists as a row, so PATCHing it
// must be rejected the same way any other unknown key is (400,
// ErrSettingNotFound) — not silently accepted into a row that would then
// exist with no code ever reading it again.
func TestAdminSettings_SoftBounceThresholdWindowDaysRetired(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	var exists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM settings WHERE key = 'soft_bounce_threshold_window_days')`,
	).Scan(&exists); err != nil {
		t.Fatalf("checking for a retired row: %v", err)
	}
	if exists {
		t.Fatal("soft_bounce_threshold_window_days row still exists, want retired by #0124")
	}

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
		jsonBody(`{"key":"soft_bounce_threshold_window_days","value":"30"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(withCookie(req, "admin-token"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (unknown setting key)", resp.StatusCode)
	}
}

// TestAdminSettings_SoftBounceThresholdRejectsNonPositiveValue asserts the
// streak threshold key rejects "0", a negative number, and a non-numeric
// value with 400, leaving the seeded row unchanged — the same shape guard
// registrations_enabled already gets for its own value space.
func TestAdminSettings_SoftBounceThresholdRejectsNonPositiveValue(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	for _, tc := range []struct {
		key   string
		value string
	}{
		{"soft_bounce_threshold_count", "0"},
		{"soft_bounce_threshold_count", "-1"},
		{"soft_bounce_threshold_count", "five"},
	} {
		req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
			jsonBody(`{"key":"`+tc.key+`","value":"`+tc.value+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := srv.Client().Do(withCookie(req, "admin-token"))
		if err != nil {
			t.Fatalf("request(%s=%s): %v", tc.key, tc.value, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s=%q status = %d, want 400", tc.key, tc.value, resp.StatusCode)
		}
	}

	if got := settingValue(t, pool, "soft_bounce_threshold_count"); got != "5" {
		t.Errorf("soft_bounce_threshold_count = %q after rejected PATCHes, want unchanged 5", got)
	}
}

// TestAdminSettings_SendHealthSettingsRoundTrip is #0124's "all three
// circuit-breaker settings are admin-configurable" criterion: each seeded
// by migrations/000015, each PATCH-able, each round-tripping through the
// response and a fresh DB read.
func TestAdminSettings_SendHealthSettingsRoundTrip(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	for _, tc := range []struct {
		key, seeded, newValue string
	}{
		{"send_health_min_sample", "50", "100"},
		{"send_health_bounce_pct", "5.0", "7.5"},
		{"send_health_complaint_pct", "0.1", "0.2"},
	} {
		if got := settingValue(t, pool, tc.key); got != tc.seeded {
			t.Fatalf("seeded %s = %q, want %q", tc.key, got, tc.seeded)
		}

		req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
			jsonBody(`{"key":"`+tc.key+`","value":"`+tc.newValue+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := srv.Client().Do(withCookie(req, "admin-token"))
		if err != nil {
			t.Fatalf("request(%s): %v", tc.key, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read body(%s): %v", tc.key, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200; body: %s", tc.key, resp.StatusCode, body)
		}
		settings := decodeSettings(t, body)
		if settings[tc.key] != tc.newValue {
			t.Errorf("response %s = %q, want %q", tc.key, settings[tc.key], tc.newValue)
		}
		if got := settingValue(t, pool, tc.key); got != tc.newValue {
			t.Errorf("DB %s = %q, want %q", tc.key, got, tc.newValue)
		}
	}
}

// TestAdminSettings_SendHealthSettingsRejectInvalidValues is the boundary
// guard for #0124's three settings: send_health_min_sample must be a
// positive integer; the two percentages must be in (0, 100].
func TestAdminSettings_SendHealthSettingsRejectInvalidValues(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)
	srv := httptest.NewServer(adminMux(store))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	for _, tc := range []struct {
		key   string
		value string
	}{
		{"send_health_min_sample", "0"},
		{"send_health_min_sample", "-1"},
		{"send_health_min_sample", "fifty"},
		{"send_health_bounce_pct", "0"},
		{"send_health_bounce_pct", "-1"},
		{"send_health_bounce_pct", "100.1"},
		{"send_health_bounce_pct", "not-a-number"},
		{"send_health_complaint_pct", "0"},
		{"send_health_complaint_pct", "-0.1"},
		{"send_health_complaint_pct", "101"},
	} {
		req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
			jsonBody(`{"key":"`+tc.key+`","value":"`+tc.value+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := srv.Client().Do(withCookie(req, "admin-token"))
		if err != nil {
			t.Fatalf("request(%s=%s): %v", tc.key, tc.value, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s=%q status = %d, want 400", tc.key, tc.value, resp.StatusCode)
		}
	}
}

// TestAdminSettings_PatchOpensRegistrationGate is the end-to-end tie-in: with the
// real registration handler mounted alongside the admin settings routes, a
// POST /auth/register/start is 403 (Registration closed) while
// registrations_enabled=false, then — after an admin PATCH sets it to true —
// the SAME request is no longer 403. This proves the registration gate reads the
// setting fresh from the DB on each attempt, so an admin toggle takes effect
// immediately without a restart.
func TestAdminSettings_PatchOpensRegistrationGate(t *testing.T) {
	pool := settingsTestPool(t)
	store := auth.NewStore(pool)

	// Wire the real registration service into the real AuthHandler so
	// /auth/register/start runs the genuine RegistrationsEnabled DB read.
	cfg := &config.Config{
		WebAuthnRPID:     "opencircuitsf.com",
		WebAuthnRPOrigin: "https://www.opencircuitsf.com",
	}
	wa, err := auth.NewWebAuthn(cfg)
	if err != nil {
		t.Fatalf("NewWebAuthn: %v", err)
	}
	regSvc := auth.NewRegistrationService(store, wa, &gateMailer{}, nil, cfg)
	authH := NewAuthHandler(regSvc, nil, nil, nil)

	settingsH := NewSettingsHandler(store, nil)
	requireSession := middleware.RequireSession(store)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register/start", authH.RegisterStart)
	mux.Handle("PATCH /admin/settings", requireAdmin(http.HandlerFunc(settingsH.Patch)))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")

	// 1. Gate closed (seeded false): register/start must be 403.
	startReq1, _ := http.NewRequest(http.MethodPost, srv.URL+"/auth/register/start",
		jsonBody(`{"email":"newuser@example.com"}`))
	startReq1.Header.Set("Content-Type", "application/json")
	startResp1, err := srv.Client().Do(startReq1)
	if err != nil {
		t.Fatalf("register/start (closed): %v", err)
	}
	startResp1.Body.Close()
	if startResp1.StatusCode != http.StatusForbidden {
		t.Fatalf("register/start while closed: status = %d, want 403", startResp1.StatusCode)
	}

	// 2. Admin opens registration.
	patchReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
		jsonBody(`{"key":"registrations_enabled","value":"true"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := srv.Client().Do(withCookie(patchReq, "admin-token"))
	if err != nil {
		t.Fatalf("PATCH open registration: %v", err)
	}
	patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH open registration: status = %d, want 200", patchResp.StatusCode)
	}

	// 3. Same register/start request must NO LONGER be 403 — the gate read the
	// new value immediately. (200 generic success here; the email is unknown so
	// the flow proceeds past the gate.)
	startReq2, _ := http.NewRequest(http.MethodPost, srv.URL+"/auth/register/start",
		jsonBody(`{"email":"newuser@example.com"}`))
	startReq2.Header.Set("Content-Type", "application/json")
	startResp2, err := srv.Client().Do(startReq2)
	if err != nil {
		t.Fatalf("register/start (open): %v", err)
	}
	startResp2.Body.Close()
	if startResp2.StatusCode == http.StatusForbidden {
		t.Fatalf("register/start still 403 after opening the gate; the setting was not read fresh")
	}
	if startResp2.StatusCode != http.StatusOK {
		t.Fatalf("register/start after open: status = %d, want 200", startResp2.StatusCode)
	}
}

// gateMailer is a no-op Mailer for the gate tie-in test: the registration flow
// reaches the mailer only once it passes the registrations_enabled gate, so a
// successful send confirms the gate opened.
type gateMailer struct{}

func (gateMailer) SendVerification(context.Context, string, string) error { return nil }
func (gateMailer) SendRecovery(context.Context, string, string) error     { return nil }
func (gateMailer) SendSessionsRevoked(context.Context, string, time.Time) error {
	return nil
}
