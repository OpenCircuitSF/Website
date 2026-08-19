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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	go func() {
		// mountAndServe blocks in http.ListenAndServe for the life of the
		// process; the test never calls it again, so the goroutine and its
		// listener simply outlive this test within the test binary.
		errCh <- mountAndServe(cfg, pool,
			authH, credsH, settingsH, adminUsersH, adminAuditH, nil, /* adminInterestsH: not exercised by this test */
			nil, /* adminSubscribersH: not exercised by this test */
			eventsH, meH, nil, /* subscribeH: not exercised by this test */
			nil, nil, nil, /* publicInterestsH, preferencesH, confirmH: not exercised by this test */
			requireSession, requireAdmin, nil)
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	waitForHealthy(t, client, baseURL, errCh)

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

// waitForHealthy polls GET /health until it responds or the deadline passes,
// so the test's first real request isn't racing mountAndServe's listener
// coming up. It also fails fast if mountAndServe has already exited.
func waitForHealthy(t *testing.T, client *http.Client, baseURL string, errCh chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("mountAndServe exited before becoming healthy: %v", err)
		default:
		}
		resp, err := client.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become healthy within the deadline", baseURL)
}
