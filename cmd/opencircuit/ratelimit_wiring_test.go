package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/events"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// TestMountAndServe_RateLimitsAuthLoginStart closes the gap #0008's review
// identified: ratelimit_test.go proves the limiter itself is correct in
// isolation, but nothing proved it is actually WIRED to an auth route in
// production — that was only established by reading cmd/opencircuit/main.go.
// This test calls mountAndServe — the exact unexported function servePostgres
// calls in production — over a real HTTP listener and drives GET
// /auth/login/start (wired in main.go to loginLimiter, rate.Every(time.Minute/10),
// burst 10) past its burst from one IP, expecting the 11th request within the
// burst window to come back 429. If a future edit ever drops the limiter from
// that route, or wires the wrong limiter, this fails; ratelimit_test.go alone
// would not have caught it.
func TestMountAndServe_RateLimitsAuthLoginStart(t *testing.T) {
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
	defer pool.Close()

	// Grab a free ephemeral port, then release it immediately so
	// mountAndServe's http.ListenAndServe can bind it. There's a small window
	// where another process could steal the port, but that's the standard
	// idiom for this kind of real-listener test and is stable enough here.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg := &config.Config{
		Port:             port,
		BaseURL:          baseURL,
		WebAuthnRPID:     "localhost",
		WebAuthnRPOrigin: baseURL,
	}

	wa, err := auth.NewWebAuthn(cfg)
	if err != nil {
		t.Fatalf("NewWebAuthn: %v", err)
	}
	var mailer auth.Mailer = auth.NoOpMailer{BaseURL: cfg.BaseURL}
	auditLogger := audit.New(pool)
	store := auth.NewStore(pool)
	regSvc := auth.NewRegistrationService(store, wa, mailer, auditLogger, cfg)
	loginSvc := auth.NewLoginService(store, wa, mailer, auditLogger, slog.Default())
	recoverSvc := auth.NewRecoveryService(store, wa, mailer, auditLogger)
	authH := handlers.NewAuthHandler(regSvc, loginSvc, recoverSvc, slog.Default())
	credsH := handlers.NewCredentialsHandler(store, auditLogger)
	settingsH := handlers.NewSettingsHandler(store, auditLogger)
	adminUsersH := handlers.NewAdminUsersHandler(store, auditLogger)
	adminAuditH := handlers.NewAdminAuditHandler(audit.NewReader(pool))
	broker := events.NewBroker()
	eventsH := handlers.NewEventsHandler(broker)
	meH := handlers.NewMeHandler()
	requireSession := middleware.RequireSession(store)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		// mountAndServe blocks in http.ListenAndServe for the life of the
		// process; the test never calls it again, so the goroutine and its
		// listener simply outlive this test within the test binary.
		errCh <- mountAndServe(cfg, pool,
			authH, credsH, settingsH, adminUsersH, adminAuditH, nil, /* adminInterestsH: not exercised by this test */
			nil,               /* adminSubscribersH: not exercised by this test */
			nil,               /* adminSuppressionsH: not exercised by this test */
			nil,               /* adminCampaignsH: not exercised by this test */
			nil,               /* adminCampaignAudienceH: not exercised by this test */
			nil,               /* adminCampaignPreviewH: not exercised by this test */
			nil,               /* adminCampaignPreflightH: not exercised by this test */
			eventsH, meH, nil, /* subscribeH: not exercised by this test */
			nil, nil, nil, nil, /* publicInterestsH, preferencesH, confirmH, unsubscribeH: not exercised by this test */
			nil, /* sesNotifyH: not exercised by this test */
			nil, /* sendWorker: not exercised by this test */
			requireSession, requireAdmin, nil, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, baseURL, errCh, ready)

	const clientIP = "198.51.100.42" // TEST-NET-2 (RFC 5737); unused elsewhere here

	for i := 1; i <= 10; i++ {
		resp, err := doLoginStart(client, baseURL, clientIP)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("request %d: got 429 before exhausting the burst of 10 — loginLimiter's burst is misconfigured or IP bucketing is broken", i)
		}
	}

	resp, err := doLoginStart(client, baseURL, clientIP)
	if err != nil {
		t.Fatalf("request 11: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("request 11: status = %d, want %d — GET /auth/login/start does not appear to be wired to loginLimiter in mountAndServe",
			resp.StatusCode, http.StatusTooManyRequests)
	}

	select {
	case err := <-errCh:
		t.Fatalf("mountAndServe exited unexpectedly: %v", err)
	default:
	}
}

// doLoginStart issues one GET /auth/login/start request against baseURL,
// tagged with clientIP via X-Forwarded-For so the rate limiter buckets every
// call in this test to the same synthetic client regardless of the loopback
// source address the real TCP connection uses.
func doLoginStart(client *http.Client, baseURL, clientIP string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/auth/login/start", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Forwarded-For", clientIP)
	return client.Do(req)
}

// wiringHTTPTimeout bounds every real HTTP round trip the wiring tests in
// this package make against their own mountAndServe-driven listener
// (#0084). It used to be a flat 5s, which — unlike the readiness wait below
// — has no polling or signal-based substitute: a single request either
// completes or it doesn't, so the only lever is the ceiling itself. Under
// heavy concurrent load (reproduced with `pgbench -c 40` plus dozens of
// competing CPU-bound processes; see issues/0084.md's Verification) a real
// handler round trip — CPU-scheduled, then a real Postgres query — occasionally
// exceeded 5s and failed with "context deadline exceeded", even though
// nothing was actually broken. 20s is generous relative to the sub-second
// steady-state cost of every request these tests make (a handful of
// single-row reads/writes), not an expected wait.
const wiringHTTPTimeout = 20 * time.Second

// wiringDBOpTimeout bounds a single, ordinary DB statement (a TRUNCATE, a
// cleanup DELETE) issued by a wiring test's setup/cleanup — same reasoning
// and same load-tested bound as wiringHTTPTimeout above: a single statement
// has no polling substitute, so under load the ceiling is the only lever,
// and 20s is generous relative to the millisecond-scale cost of these
// specific statements against a handful of rows.
const wiringDBOpTimeout = 20 * time.Second

// wiringReadinessDeadline bounds how long a wiring test waits for
// mountAndServe's `ready` channel to close (#0084). Unlike
// wiringHTTPTimeout/wiringDBOpTimeout above, this genuinely is NOT
// network/DB-load-sensitive — `ready` closes the instant the TCP listener
// is bound (see mountAndServe's doc comment in main.go), before any request
// is made — so exceeding even a modest bound here means goroutine
// scheduling itself is pathologically starved, i.e. something is genuinely
// stuck, not just slow. Kept well below wiringHTTPTimeout deliberately: a
// test that's actually broken should fail fast rather than wait 20s to
// discover the listener never bound.
const wiringReadinessDeadline = 10 * time.Second

// wiringDBConnectTimeout bounds each wiring test's own db.Connect(ctx, dsn)
// call (#0084/#0087 review — five identical, previously-uncommented 30s
// literals: this file, subscribe_wiring_test.go, admin_wiring_test.go,
// shutdown_wiring_test.go, shutdown_budget_wiring_test.go). Unlike
// wiringHTTPTimeout/wiringDBOpTimeout above, this is NOT sized against an
// observed load-induced failure — none of the failures #0084 reproduced was
// a connect timeout. It is a generous, mostly-decorative ceiling: measured
// directly against this project's own db.Connect (pgxpool.NewWithConfig +
// Ping) on 2026-08-19 under genuine ambient load (load average ~290-315,
// from other concurrent agent sessions — see issues/0084.md's Work log),
// eight consecutive calls took 7-161ms, none over 200ms. 30s is ~180x that
// worst observation; it exists to fail a genuinely unreachable/misconfigured
// database eventually, not to survive scheduling contention the way
// wiringHTTPTimeout/wiringDBOpTimeout do.
const wiringDBConnectTimeout = 30 * time.Second

// waitForHealthy waits for mountAndServe's listener to signal readiness
// (see mountAndServe's `ready` parameter, main.go), then makes exactly one
// GET /health request to confirm the full handler stack — including the DB
// ping — actually answers, so the test's first real request isn't racing
// anything. It fails fast if mountAndServe has already exited.
//
// Before #0084 this polled GET /health in a loop against a fixed wall-clock
// deadline — a real network+DB round trip repeated until it succeeded or
// the deadline passed. Under heavy concurrent load that deadline could be
// exceeded before the listener ever came up, which is what produced the
// "context deadline exceeded" flake issues/0084.md describes: the failure
// had nothing to do with what the test was checking. Waiting on `ready`
// instead needs no network round trip and isn't sensitive to how loaded the
// machine is, so it removes the timing dependency rather than enlarging it.
func waitForHealthy(t *testing.T, client *http.Client, baseURL string, errCh chan error, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
		// Listener is bound; confirmed below with one real request.
	case err := <-errCh:
		t.Fatalf("mountAndServe exited before becoming healthy: %v", err)
	case <-time.After(wiringReadinessDeadline):
		t.Fatalf("mountAndServe did not signal readiness within %s — this does not depend on network/DB load "+
			"(ready closes the instant the listener is bound), so exceeding it means something is genuinely stuck",
			wiringReadinessDeadline)
	}

	resp, err := client.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("GET /health after readiness signal: %v", err)
	}
	resp.Body.Close()
}
