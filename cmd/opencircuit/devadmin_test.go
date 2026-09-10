package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// TestNewDevAdminAutoLogin_OffIsNilNil is #0402's acceptance criterion that
// unset/false changes nothing: DevAdminLogin=false must return (nil, nil)
// with no log output, regardless of BASE_URL — mirroring
// TestCheckMailerNoOp_Unset's shape for the sibling guard it was extracted
// alongside.
func TestNewDevAdminAutoLogin_OffIsNilNil(t *testing.T) {
	cfg := &config.Config{DevAdminLogin: false, BaseURL: "https://www.opencircuitsf.com"}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	mw, err := newDevAdminAutoLogin(context.Background(), cfg, nil, nil, logger)
	if err != nil {
		t.Fatalf("newDevAdminAutoLogin with DevAdminLogin=false: got err=%v, want nil", err)
	}
	if mw != nil {
		t.Errorf("newDevAdminAutoLogin with DevAdminLogin=false: got non-nil middleware, want nil")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log output when DEV_ADMIN_LOGIN is unset, got %q", buf.String())
	}
}

// TestNewDevAdminAutoLogin_RefusedOutsideLocalhost_BeforeAnyDBAccess is
// #0402's startup-refusal acceptance criterion, proved with nil store AND
// nil pool: if the loopback check ran AFTER any store/pool use, this call
// would panic on a nil pointer dereference instead of returning a clean
// error. Returning the error proves the refusal precedes every database
// access, not just that it eventually happens (mirrors
// TestCheckMailerNoOp_RefusedOutsideLocalhost).
func TestNewDevAdminAutoLogin_RefusedOutsideLocalhost_BeforeAnyDBAccess(t *testing.T) {
	cfg := &config.Config{DevAdminLogin: true, BaseURL: "https://www.opencircuitsf.com", AdminEmail: "admin@localhost"}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	mw, err := newDevAdminAutoLogin(context.Background(), cfg, nil, nil, logger)
	if err == nil {
		t.Fatal("newDevAdminAutoLogin with DevAdminLogin=true and a non-local BASE_URL: got nil error, want a refusal")
	}
	if mw != nil {
		t.Errorf("newDevAdminAutoLogin refusal: got non-nil middleware, want nil")
	}
	if !strings.Contains(err.Error(), cfg.BaseURL) {
		t.Errorf("error %q does not name the offending BASE_URL", err.Error())
	}
	if !strings.Contains(err.Error(), "DEV_ADMIN_LOGIN") {
		t.Errorf("error %q does not name DEV_ADMIN_LOGIN", err.Error())
	}
}

// TestBaseURLIsLoopback_MatchesMailerNoOpCases drives the SAME host cases
// TestMailerNoOpAllowed (mailer_noop_test.go) uses through baseURLIsLoopback
// directly — the predicate #0402 extracted mailerNoOpAllowed's body into so
// newDevAdminAutoLogin's startup gate reuses it rather than writing a
// second, independently-maintained host check. Passing here alongside
// TestMailerNoOpAllowed passing unmodified is the proof the extraction
// changed nothing about the sharp cases (sub.localhost and
// 127.0.0.1.evil.com both false).
func TestBaseURLIsLoopback_MatchesMailerNoOpCases(t *testing.T) {
	cases := []struct {
		baseURL string
		want    bool
	}{
		{"http://localhost:8080", true},
		{"https://localhost", true},
		{"http://127.0.0.1:8080", true},
		{"http://127.0.0.1", true},
		{"https://www.opencircuitsf.com", false},
		{"https://opencircuitsf.com", false},
		{"http://sub.localhost:8080", false},
		{"http://127.0.0.1.evil.com", false},
		{"not a url", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.baseURL, func(t *testing.T) {
			if got := baseURLIsLoopback(c.baseURL); got != c.want {
				t.Errorf("baseURLIsLoopback(%q) = %v, want %v", c.baseURL, got, c.want)
			}
		})
	}
}

// startDevAdminWiringServer starts a real mountAndServe instance on an
// ephemeral 127.0.0.1 port, wired the same minimal way
// TestMountAndServe_AdminRoutesRequireSessionAndAdmin (admin_wiring_test.go)
// does: only meH and a real *seo.Site (required — site.SitemapHandler/
// RobotsHandler/WorkshopCardHandler/Middleware are called at REGISTRATION
// time, so a nil site would panic mountAndServe itself, unlike every other
// handler pointer here, which is only ever bound as a method value and
// never invoked by this test). Every other handler argument is nil: this
// test exercises GET /api/me and GET /health only, and a bound method value
// on a nil receiver does not panic until called.
func startDevAdminWiringServer(t *testing.T, pool handlers.Pinger, cfg *config.Config, requireSession, requireAdmin, outerMiddleware func(http.Handler) http.Handler) string {
	t.Helper()

	site, err := buildSEOSite(cfg, nil, nil)
	if err != nil {
		t.Fatalf("build seo site: %v", err)
	}
	meH := handlers.NewMeHandler()

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, pool,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			nil, meH, nil,
			nil, nil, nil, nil, nil, nil, nil, nil,
			nil,
			nil,
			nil,
			site,
			requireSession, requireAdmin, outerMiddleware, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, cfg.BaseURL, errCh, ready)
	return cfg.BaseURL
}

// devWiringPort allocates an ephemeral, currently-free 127.0.0.1 port
// (CLAUDE.md §8b — never bind a fixed, shared port for a verification
// server) the same way admin_wiring_test.go does: bind, read the assigned
// port, close the probe listener, and hand the number to mountAndServe's
// own cfg.Port to bind for real moments later.
func devWiringPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}
	return port
}

// TestNewDevAdminAutoLogin_RealRouteTable is #0402's (and #0409's) DB-backed
// proof over the REAL mountAndServe route table, per Issues.md's phase-2
// step and the issue's own step-16 plan: no t.Parallel() anywhere in this
// package (cmd/opencircuit/wiring_parallel_guard_test.go enforces this),
// and — deliberately — no truncateAdminWiringTables/registerWiringTest,
// since this test seeds a uniquely-named admin
// (fmt.Sprintf("devadmin-%d@localhost", time.Now().UnixNano())) and cleans
// up only that user's own rows, never a literal or seeded id (CLAUDE.md
// §8b).
//
// Five assertions now, matching #0402's original three plus the two #0484
// added: with the DevAdminAutoLogin middleware wired, GET /api/me with NO
// cookie answers 200 and reports the seeded admin; a stale cookie (a token
// whose row is gone) and a garbage cookie (one that never was a token) each
// heal to 200 rather than sticking at 401; with nil passed for
// outerMiddleware (today's unchanged production shape), a credential-less
// request answers 401; and repeated credential-less requests against the
// wired server leave EXACTLY ONE row in sessions for that admin (the
// cache-or-mint contract).
func TestNewDevAdminAutoLogin_RealRouteTable(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), wiringDBConnectTimeout)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(pool.Close)

	adminEmail := fmt.Sprintf("devadmin-%d@localhost", time.Now().UnixNano())
	adminID := seedAdminWiringUser(t, pool, adminEmail, true)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), wiringDBOpTimeout)
		defer ccancel()
		if _, err := pool.Exec(cctx, `DELETE FROM sessions WHERE user_id = $1`, adminID); err != nil {
			t.Logf("cleanup: delete sessions for %s: %v", adminEmail, err)
		}
		if _, err := pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, adminID); err != nil {
			t.Logf("cleanup: delete user %s: %v", adminEmail, err)
		}
	})

	store := auth.NewStore(pool)
	requireSession := middleware.RequireSession(store)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}

	// ── Server A: DEV_ADMIN_LOGIN wired ─────────────────────────────────────
	portA := devWiringPort(t)
	baseURLA := fmt.Sprintf("http://127.0.0.1:%d", portA)
	cfgA := &config.Config{
		Port: portA, BaseURL: baseURLA,
		WebAuthnRPID: "localhost", WebAuthnRPOrigin: baseURLA,
		DevAdminLogin: true, AdminEmail: adminEmail,
	}
	devAdminMW, err := newDevAdminAutoLogin(context.Background(), cfgA, store, pool, slog.Default())
	if err != nil {
		t.Fatalf("newDevAdminAutoLogin: %v", err)
	}
	if devAdminMW == nil {
		t.Fatal("newDevAdminAutoLogin returned a nil middleware with DevAdminLogin=true on a loopback BASE_URL")
	}
	urlA := startDevAdminWiringServer(t, pool, cfgA, requireSession, requireAdmin, devAdminMW)

	// ── Server B: identical wiring, but outerMiddleware nil (today's
	// unchanged production shape) — proves unset/off changes nothing.
	portB := devWiringPort(t)
	baseURLB := fmt.Sprintf("http://127.0.0.1:%d", portB)
	cfgB := &config.Config{
		Port: portB, BaseURL: baseURLB,
		WebAuthnRPID: "localhost", WebAuthnRPOrigin: baseURLB,
	}
	urlB := startDevAdminWiringServer(t, pool, cfgB, requireSession, requireAdmin, nil)

	client := &http.Client{Timeout: wiringHTTPTimeout} // no CookieJar: every request is credential-less

	// Server A, no cookie: 200, reporting the seeded admin.
	resp, err := client.Get(urlA + "/api/me")
	if err != nil {
		t.Fatalf("GET %s/api/me: %v", urlA, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("server A (DEV_ADMIN_LOGIN wired) GET /api/me with no cookie: status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var me struct {
		ID      int64  `json:"id"`
		Email   string `json:"email"`
		IsAdmin bool   `json:"is_admin"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode /api/me body %s: %v", body, err)
	}
	if me.ID != adminID || me.Email != adminEmail || !me.IsAdmin {
		t.Errorf("/api/me = %+v, want {ID:%d Email:%s IsAdmin:true}", me, adminID, adminEmail)
	}

	// Server A, a stale cookie (a token whose sessions row was deleted) and
	// a garbage cookie (a value that was never a real token): #0409's two
	// healing states, which #0484 found pinned only one layer down against
	// a fake resolver (TestDevAdminAutoLogin_StaleCookieHeals,
	// TestDevAdminAutoLogin_GarbageCookieHeals) rather than through this
	// real mountAndServe table.
	//
	// One request per state, no insert-then-delete round trip for "stale":
	// reading auth.Store.ResolveSession shows a deleted row and a token
	// that never existed take the IDENTICAL code path. The first UPDATE
	// matches no row either way, so it falls through to the diagnostic
	// SELECT; that SELECT also finds no row either way, since the row is
	// simply absent in both cases; and both therefore return
	// auth.ErrSessionInvalid from the same "case errors.Is(derr,
	// pgx.ErrNoRows)" branch. An inserted-then-deleted row would reach that
	// exact branch too, so it would prove nothing a bare nonexistent token
	// does not already prove — the two client-observable "stale" and
	// "garbage" states are one server-side state.
	for _, tc := range []struct {
		name        string
		cookieValue string
	}{
		{"stale", "stale-token-never-minted-by-any-server"},
		{"garbage", "nonsense"},
	} {
		req, err := http.NewRequest(http.MethodGet, urlA+"/api/me", nil)
		if err != nil {
			t.Fatalf("%s cookie: build request: %v", tc.name, err)
		}
		req.Header.Set("Cookie", auth.SessionCookieName+"="+tc.cookieValue)
		resp3, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s cookie: GET %s/api/me: %v", tc.name, urlA, err)
		}
		body3, _ := io.ReadAll(resp3.Body)
		resp3.Body.Close()
		if resp3.StatusCode != http.StatusOK {
			t.Errorf("server A, %s cookie: status = %d, want 200 (must heal, not 401 — #0409); body=%s", tc.name, resp3.StatusCode, body3)
		}
	}

	// Server B, identical request, no auto-login wired: 401.
	resp2, err := client.Get(urlB + "/api/me")
	if err != nil {
		t.Fatalf("GET %s/api/me: %v", urlB, err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("server B (outerMiddleware nil) GET /api/me with no cookie: status = %d, want 401", resp2.StatusCode)
	}

	// Repeated credential-less requests against server A must leave exactly
	// one sessions row for this admin — the cache-or-mint contract.
	for i := 0; i < 5; i++ {
		r, err := client.Get(urlA + "/api/me")
		if err != nil {
			t.Fatalf("GET %s/api/me (repeat %d): %v", urlA, i, err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("repeat %d: status = %d, want 200", i, r.StatusCode)
		}
	}

	var sessionCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM sessions WHERE user_id = $1`, adminID,
	).Scan(&sessionCount); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessionCount != 1 {
		t.Errorf("sessions rows for %s after repeated credential-less requests = %d, want 1", adminEmail, sessionCount)
	}
}
