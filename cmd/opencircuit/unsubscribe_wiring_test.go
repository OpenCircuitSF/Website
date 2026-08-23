package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// newUnsubscribeWiringHandler and its helpers below stand up the exact
// mountAndServe call this package's other *_wiring_test.go files use,
// scoped to just unsubscribeH so these two tests don't need to build every
// other handler.
func newUnsubscribeWiringHandler(pool *pgxpool.Pool) *handlers.UnsubscribeHandler {
	return handlers.NewUnsubscribeHandler(subscribers.NewStore(pool), audit.New(pool), nil, slog.Default())
}

// TestMountAndServe_UnsubscribePost_NeverRateLimited is #0103's core wiring
// proof: PRD §6.5 says a provider seeing errors on the unsubscribe endpoint
// downgrades sender reputation, and RateLimiter.Middleware's refusal path
// answers 429 — the one status this route must never produce. Before the
// fix, POST /api/unsubscribe was wired behind unsubscribeLimiter
// (rate.Every(time.Minute/10), burst 5) exactly like every other public
// route; this drives a burst of 30 POSTs — six times that burst — through
// mountAndServe from a single IP and asserts every single response is 200.
// TestMountAndServe_RateLimitsSubscribe is the shape this is inverted from.
func TestMountAndServe_UnsubscribePost_NeverRateLimited(t *testing.T) {
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

	unsubscribeH := newUnsubscribeWiringHandler(pool)
	// #0103 review (bounce of b8abe93): a stub `passthrough` here made this
	// test vacuous for anything requireSession would guard, because
	// mountAndServe takes requireSession as a parameter — wrapping the route
	// in it would have been invisible to a no-op stand-in. Use the real
	// middleware, exactly as admin_wiring_test.go and
	// ratelimit_wiring_test.go already do, so a future edit that wraps
	// POST /api/unsubscribe in requireSession makes this test fail on an
	// unauthenticated request the way it should. requireAdmin is not
	// exercised by this route, so it stays a no-op stand-in.
	requireSession := middleware.RequireSession(auth.NewStore(pool))
	requireAdmin := func(next http.Handler) http.Handler { return next }

	// #0054: a real *seo.Site, built the same way production does.
	site, err := buildSEOSite(cfg, nil)
	if err != nil {
		t.Fatalf("build seo site: %v", err)
	}

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, pool,
			nil, nil, nil, nil, nil, nil, /* adminInterestsH: not exercised by this test */
			nil, /* adminSubscribersH: not exercised by this test */
			nil, /* adminSuppressionsH: not exercised by this test */
			nil, /* adminCampaignsH: not exercised by this test */
			nil, /* adminCampaignAudienceH: not exercised by this test */
			nil, /* adminCampaignPreviewH: not exercised by this test */
			nil, /* adminCampaignPreflightH: not exercised by this test */
			nil, /* adminCampaignStatsH: not exercised by this test */
			nil, /* adminWorkshopsH: not exercised by this test */
			nil, nil, nil,
			nil, nil, nil, /* publicInterestsH, preferencesH, confirmH: not exercised by this test */
			unsubscribeH,
			nil, /* publicWorkshopsH: not exercised by this test */
			nil, /* sesNotifyH: not exercised by this test */
			nil, /* sendWorker: not exercised by this test */
			site,
			requireSession, requireAdmin, nil, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, baseURL, errCh, ready)

	const clientIP = "198.51.100.88" // TEST-NET-2 (RFC 5737); shared by every request

	// Every token here is unknown/garbage — the handler answers 200 neutral
	// for that regardless (#0034), so this burst never needs a seeded
	// subscriber. The only thing under test is whether the ROUTE itself can
	// ever answer non-200, not what the handler decides once reached.
	const burst = 30
	for i := 1; i <= burst; i++ {
		resp, err := doUnsubscribePost(client, baseURL, clientIP, fmt.Sprintf("wiring-burst-token-%d", i))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 — POST /api/unsubscribe must never answer anything but 200 (#0103)",
				i, resp.StatusCode)
		}
	}

	select {
	case err := <-errCh:
		t.Fatalf("mountAndServe exited unexpectedly: %v", err)
	default:
	}
}

// TestMountAndServe_UnsubscribePost_NoSessionNoCSRF pins #0034's carried-in
// acceptance criterion at the mux level, the way admin_wiring_test.go and
// TestMountAndServe_RateLimitsSubscribe pin their own routes' wiring rather
// than trusting the handler's own unit tests: a request with no session
// cookie and no CSRF token — this repository has no CSRF middleware at all
// (see #0034's Resolution notes), so "no CSRF token" here means "no header
// or form field a CSRF scheme would need" — must still succeed. Nothing
// today guards against a future change wrapping this route in
// requireSession; every handler unit test in internal/handlers would still
// pass if someone did, because that wrapping happens in mountAndServe, one
// layer outside what those tests exercise.
func TestMountAndServe_UnsubscribePost_NoSessionNoCSRF(t *testing.T) {
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

	unsubscribeH := newUnsubscribeWiringHandler(pool)
	// #0103 review (bounce of b8abe93): this is the test the bounce is
	// actually about — a stub `passthrough` for requireSession made it
	// impossible for this test to detect a future edit wrapping the route
	// in requireSession, since the guard under test was replaced by a no-op
	// inside the test itself. Use the real middleware, exactly as
	// admin_wiring_test.go and ratelimit_wiring_test.go already do:
	// middleware.RequireSession answers 401 on an empty token
	// (internal/middleware/auth.go), so with this test's cookie-less
	// request, wrapping POST /api/unsubscribe in requireSession now fails
	// this test at "status = 401, want 200" instead of passing silently.
	// requireAdmin is not exercised by this route, so it stays a no-op
	// stand-in.
	requireSession := middleware.RequireSession(auth.NewStore(pool))
	requireAdmin := func(next http.Handler) http.Handler { return next }

	// #0054: a real *seo.Site, built the same way production does.
	site, err := buildSEOSite(cfg, nil)
	if err != nil {
		t.Fatalf("build seo site: %v", err)
	}

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, pool,
			nil, nil, nil, nil, nil, nil,
			nil,
			nil,
			nil, /* adminCampaignsH: not exercised by this test */
			nil, /* adminCampaignAudienceH: not exercised by this test */
			nil, /* adminCampaignPreviewH: not exercised by this test */
			nil, /* adminCampaignPreflightH: not exercised by this test */
			nil, /* adminCampaignStatsH: not exercised by this test */
			nil, /* adminWorkshopsH: not exercised by this test */
			nil, nil, nil,
			nil, nil, nil,
			unsubscribeH,
			nil, /* publicWorkshopsH: not exercised by this test */
			nil, /* sesNotifyH: not exercised by this test */
			nil, /* sendWorker: not exercised by this test */
			site,
			requireSession, requireAdmin, nil, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, baseURL, errCh, ready)

	// A bare request: no Cookie header (so no session), no CSRF-scheme
	// header or form field, tagged with an X-Forwarded-For so the
	// (still-present) GET limiter and any future addition bucket it
	// somewhere deterministic rather than colliding with other tests.
	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/api/unsubscribe?token=wiring-nosession-token",
		strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", "198.51.100.89") // TEST-NET-2 (RFC 5737)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/unsubscribe: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — POST /api/unsubscribe must succeed with no session cookie and no CSRF token (#0034's carried-in criterion, pinned at the mux level by #0103)",
			resp.StatusCode)
	}

	select {
	case err := <-errCh:
		t.Fatalf("mountAndServe exited unexpectedly: %v", err)
	default:
	}
}

// doUnsubscribePost issues one RFC 8058-shaped POST /api/unsubscribe
// request against baseURL, tagged with clientIP via X-Forwarded-For so a
// rate limiter — if one were still wired to this route — would bucket
// every call in the burst test to the same synthetic client.
func doUnsubscribePost(client *http.Client, baseURL, clientIP, token string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/api/unsubscribe?token="+token,
		strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", clientIP)
	return client.Do(req)
}
