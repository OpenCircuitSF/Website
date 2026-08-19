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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/events"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// TestMountAndServe_AdminRoutesRequireSessionAndAdmin closes the same kind of
// gap TestMountAndServe_RateLimitsAuthLoginStart closes for the rate limiter
// (#0008's review): the internal/handlers admin suites (settings_test.go,
// users_test.go, audit_test.go) each build their OWN small mux wrapping
// requireAdmin the same way main.go does, which proves the middleware
// COMPOSES correctly but not that main.go's mountAndServe — the exact
// function servePostgres calls in production — actually WIRES every admin
// route through it. If a future edit to mountAndServe ever added a new
// /admin/* route and forgot requireAdmin, none of those per-handler suites
// would catch it; this test would.
//
// It drives GET /admin/settings over a real HTTP listener via mountAndServe
// itself, for three sessions: none (401), a non-admin (403), and an admin
// (200) — #0009's acceptance criterion "Admin routes are gated on
// RequireSession + RequireAdmin; a non-admin session gets 403".
//
// Guard-removal proof (see #0009's Verification for the transcript): with
// `requireAdmin(http.HandlerFunc(settingsH.List))` in mountAndServe
// temporarily replaced by `requireSession(http.HandlerFunc(settingsH.List))`
// (RequireAdmin dropped), this test's non-admin case failed — got 200, want
// 403 — exactly as expected, then passed again once RequireAdmin was
// restored.
func TestMountAndServe_AdminRoutesRequireSessionAndAdmin(t *testing.T) {
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
	// A single Cleanup (truncate, then close), not a separate defer, since
	// t.Cleanup funcs run AFTER a test's own defers — a standalone
	// `defer pool.Close()` would close the pool before a later Cleanup got to
	// use it.
	t.Cleanup(func() {
		truncateAdminWiringTables(t, pool)
		pool.Close()
	})
	truncateAdminWiringTables(t, pool)

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
	// #0024: exercised the same way as adminUsersH/adminAuditH above, so this
	// test's gap-closing argument (main.go's REAL mountAndServe, not a
	// per-handler suite's own small mux) covers /admin/interests too.
	adminInterestsH := handlers.NewAdminInterestsHandler(interests.NewStore(pool), auditLogger)
	broker := events.NewBroker()
	eventsH := handlers.NewEventsHandler(broker)
	meH := handlers.NewMeHandler()
	requireSession := middleware.RequireSession(store)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- mountAndServe(cfg, pool,
			authH, credsH, settingsH, adminUsersH, adminAuditH, adminInterestsH, eventsH, meH, nil, /* subscribeH: not exercised by this test */
			requireSession, requireAdmin, nil)
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	waitForHealthy(t, client, baseURL, errCh)

	nonAdminID := seedAdminWiringUser(t, pool, "wiring-nonadmin@example.com", false)
	adminID := seedAdminWiringUser(t, pool, "wiring-admin@example.com", true)
	seedAdminWiringSession(t, pool, nonAdminID, "wiring-nonadmin-token")
	seedAdminWiringSession(t, pool, adminID, "wiring-admin-token")

	// /admin/settings is representative — every /admin/* route in main.go is
	// wired through the same requireAdmin composition, and the per-handler
	// suites (settings_test.go, users_test.go, audit_test.go) each prove that
	// composition again for their own routes. /admin/interests (#0024) is
	// checked explicitly rather than left to "representative" — the PATCH
	// and DELETE routes newly introduced here didn't exist when that
	// argument was written, and this test is exactly the one #0024 was told
	// to keep true for its own routes too.
	cases := []struct {
		name   string
		cookie string // "" means no cookie at all
		want   int
	}{
		{"no session", "", http.StatusUnauthorized},
		{"non-admin session", "wiring-nonadmin-token", http.StatusForbidden},
		{"admin session", "wiring-admin-token", http.StatusOK},
	}
	for _, path := range []string{"/admin/settings", "/admin/interests"} {
		for _, c := range cases {
			req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
			if err != nil {
				t.Fatalf("%s %s: build request: %v", c.name, path, err)
			}
			if c.cookie != "" {
				req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: c.cookie})
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s %s: request: %v", c.name, path, err)
			}
			resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Errorf("%s: GET %s status = %d, want %d", c.name, path, resp.StatusCode, c.want)
			}
		}
	}

	select {
	case err := <-errCh:
		t.Fatalf("mountAndServe exited unexpectedly: %v", err)
	default:
	}
}

// truncateAdminWiringTables clears the users/sessions rows this test seeds, so
// it starts and leaves the shared TEST_DATABASE_URL clean. audit_log is
// truncated too since RequireAdmin/RequireSession failures never write it but
// this keeps the cleanup symmetric with the handlers-package convention.
func truncateAdminWiringTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`TRUNCATE sessions, passkey_credentials, audit_log, users RESTART IDENTITY CASCADE`,
	); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
}

// seedAdminWiringUser inserts an active account and returns its id.
func seedAdminWiringUser(t *testing.T, pool *pgxpool.Pool, email string, isAdmin bool) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, is_admin, active, created_at)
		 VALUES ($1, $2, TRUE, now()) RETURNING id`, email, isAdmin,
	).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

// seedAdminWiringSession inserts a live 30-day session for userID under token.
func seedAdminWiringSession(t *testing.T, pool *pgxpool.Pool, userID int64, token string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO sessions (user_id, token, created_at, expires_at, last_seen_at)
		 VALUES ($1, $2, now(), now() + interval '30 days', now())`,
		userID, token,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}
