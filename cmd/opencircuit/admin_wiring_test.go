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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/events"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/sesnotify"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// TestMountAndServe_AdminRoutesRequireSessionAndAdmin closes the same kind of
// gap TestMountAndServe_RateLimitsAuthLoginStart closes for the rate limiter
// (#0008's review): the internal/handlers admin suites (settings_test.go,
// users_test.go, audit_test.go, interests_test.go) each build their OWN small
// mux wrapping requireAdmin the same way main.go does, which proves the
// middleware COMPOSES correctly but not that main.go's mountAndServe — the
// exact function servePostgres calls in production — actually WIRES every
// admin route through it. If a future edit to mountAndServe ever added a new
// /admin/* route and forgot requireAdmin, none of those per-handler suites
// would catch it; this test would.
//
// #0024's review bounced an earlier version of this test for covering only
// GET: three of the four /admin/interests verbs — including DELETE, the
// destructive one — had no proof at the real-mux level, and a guard-removal
// mutation on any of them shipped a green suite. This version drives EVERY
// {method, path} pair in adminRoutes (cmd/opencircuit/main.go) — the exact
// function mountAndServe itself loops over to register the routes — over a
// real HTTP listener, for three sessions: none (want 401), a non-admin (want
// 403), and an admin (want anything except 401/403, i.e. the request reached
// the handler rather than being turned away by the guard). Because the test
// enumerates adminRoutes instead of hand-listing paths, a route added to that
// list is covered automatically — no edit to this test is required, which is
// the point: the failure mode this closes is a new route shipping with no
// guard and no test noticing.
//
// Guard-removal proof (see #0009's Verification for the original GET
// transcript, and #0024's Verification for all four verbs): with
// `requireAdmin(...)` in mountAndServe's adminRoutes loop temporarily
// replaced by `requireSession(...)` for one verb at a time (GET, POST,
// PATCH, DELETE), this test's non-admin case for that route failed — got
// 200 (or whatever the ungated handler returned), want 403 — for every one
// of the four, then passed again once RequireAdmin was restored.
func TestMountAndServe_AdminRoutesRequireSessionAndAdmin(t *testing.T) {
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
	interestsStore := interests.NewStore(pool)
	// #0024: exercised the same way as adminUsersH/adminAuditH above, so this
	// test's gap-closing argument (main.go's REAL mountAndServe, not a
	// per-handler suite's own small mux) covers /admin/interests too.
	adminInterestsH := handlers.NewAdminInterestsHandler(interestsStore, auditLogger)
	subscribersStore := subscribers.NewStore(pool)
	// #0032: exercised the same way as adminInterestsH above. manualAdd is
	// nil (matching this test's own "subscribeH: not exercised by this test"
	// choice below) — POST /admin/subscribers therefore answers 503 rather
	// than actually dispatching a signup, which is still proof enough that
	// RequireAdmin let the request through (neither 401 nor 403); see the
	// "admin session" case's comment.
	sesEventsStore := sesnotify.NewStore(pool)
	adminSubscribersH := handlers.NewAdminSubscribersHandler(subscribersStore, interestsStore, nil, nil, sesEventsStore, store, auditLogger)
	// #0100: exercised the same way as adminSubscribersH above.
	suppressionsStore := subscribers.NewSuppressionStore(pool)
	adminSuppressionsH := handlers.NewAdminSuppressionsHandler(suppressionsStore, subscribersStore, auditLogger)
	// #0041: exercised the same way as adminSuppressionsH above. preflight is
	// nil (matching this test's own "not exercised" convention for
	// not-yet-built seams). The admin-session case sends every route with no
	// body, so POST .../send answers 400 (confirm_count is required, #0047's
	// plan) rather than reaching the preflight seam or the state transition
	// — still proof enough that RequireAdmin let the request through, the
	// same standard this test already applies to POST /admin/subscribers
	// above.
	campaignsStore := mailing.NewCampaignStore(pool)
	audienceStore := mailing.NewAudienceStore(pool)
	adminCampaignsH := handlers.NewAdminCampaignsHandler(campaignsStore, nil, audienceStore, auditLogger)
	// #0044: exercised the same way as adminCampaignsH above.
	adminCampaignAudienceH := handlers.NewAdminCampaignAudienceHandler(audienceStore)
	// #0046: exercised the same way as adminCampaignsH above, EXCEPT the
	// mailer is deliberately nil (matching this test's own "not exercised"
	// convention for real outbound sends) — POST .../test therefore answers
	// 503 rather than attempting a real network call, which is still proof
	// enough that RequireAdmin let the request through, the same standard
	// this test already applies to POST /admin/subscribers's 503 above.
	// POST .../preview needs no mailer at all and reaches the real handler
	// (200, rendering targetCampaign below).
	adminCampaignPreviewH := handlers.NewAdminCampaignPreviewHandler(
		campaignsStore, subscribersStore, store, mailing.MarkdownCampaignRenderer{}, nil, auditLogger,
		cfg.BaseURL, "lists.example.com", "hello@example.com",
	)
	// #0047: exercised the same way as adminCampaignAudienceH above — a
	// real read-only GET, no mailer/mutation involved, so a real
	// *mailing.SendStore (backing GatherPreflight) is used rather than nil.
	sendStore := mailing.NewSendStore(pool, audienceStore, store, nil, cfg.BaseURL, "lists.example.com", "hello@example.com")
	adminCampaignPreflightH := handlers.NewAdminCampaignPreflightHandler(sendStore, campaignsStore, store, "hello@example.com")
	// #0049: exercised the same way as adminCampaignAudienceH/
	// adminCampaignPreflightH above — a real read-only GET over a real
	// *mailing.CampaignStatsStore, no mailer/mutation involved.
	campaignStatsStore := mailing.NewCampaignStatsStore(pool)
	adminCampaignStatsH := handlers.NewAdminCampaignStatsHandler(campaignStatsStore, campaignsStore)
	// #0051: exercised the same way as adminCampaignStatsH above. The
	// invalidator is nil (matching main.go's own production wiring — see
	// that call site's comment: *seo.Site is wired in by #0054), which has
	// no bearing on this test's guard proof since RequireAdmin runs before
	// the handler ever calls it.
	workshopsStore := workshops.NewStore(pool)
	adminWorkshopsH := handlers.NewAdminWorkshopsHandler(workshopsStore, nil, campaignsStore, auditLogger)
	broker := events.NewBroker()
	eventsH := handlers.NewEventsHandler(broker)
	meH := handlers.NewMeHandler()
	requireSession := middleware.RequireSession(store)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}

	// #0054: a real *seo.Site, built the same way production does (via
	// buildSEOSite) so this test exercises the real GET /sitemap.xml /
	// GET /robots.txt routes rather than a nil that would panic
	// site.Middleware/SitemapHandler.
	site, err := buildSEOSite(cfg, nil)
	if err != nil {
		t.Fatalf("build seo site: %v", err)
	}

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, pool,
			authH, credsH, settingsH, adminUsersH, adminAuditH, adminInterestsH, adminSubscribersH, adminSuppressionsH, adminCampaignsH, adminCampaignAudienceH, adminCampaignPreviewH, adminCampaignPreflightH, adminCampaignStatsH, adminWorkshopsH, eventsH, meH, nil, /* subscribeH: not exercised by this test */
			nil, nil, nil, nil, nil, /* publicInterestsH, preferencesH, confirmH, unsubscribeH, publicWorkshopsH: not exercised by this test */
			nil, /* sesNotifyH: not exercised by this test */
			nil, /* sendWorker: not exercised by this test */
			site,
			requireSession, requireAdmin, nil, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, baseURL, errCh, ready)

	nonAdminID := seedAdminWiringUser(t, pool, "wiring-nonadmin@example.com", false)
	adminID := seedAdminWiringUser(t, pool, "wiring-admin@example.com", true)
	seedAdminWiringSession(t, pool, nonAdminID, "wiring-nonadmin-token")
	seedAdminWiringSession(t, pool, adminID, "wiring-admin-token")

	// A dedicated target for the /admin/users/{id} family — distinct from
	// nonAdminID/adminID above, which identify the SESSION making the
	// request, not the id in the path. The admin-session case below reaches
	// the real handler, so a route like POST .../deactivate genuinely flips
	// this account's active flag; it is never used to authenticate, so that
	// has no bearing on the auth assertions themselves.
	targetUserID := seedAdminWiringUser(t, pool, "wiring-target@example.com", false)

	// A dedicated throwaway interest for the /admin/interests/{id} family,
	// seeded through the real store rather than a literal id. CLAUDE.md §8b
	// exists because of exactly this test: #0024's first pass targeted a
	// hardcoded /admin/interests/1 — the real seeded "microcontrollers"
	// taxonomy row — and a guard-removal mutation against DELETE permanently
	// destroyed it. This target is created fresh per run and swept up below
	// by its zz-wiring- prefix, so it stays safe even while the guard under
	// test is deliberately gone.
	targetSlug := fmt.Sprintf("zz-wiring-%d", time.Now().UnixNano())
	targetInterest, err := interestsStore.Create(context.Background(), targetSlug, "Wiring guard target", nil, 0)
	if err != nil {
		t.Fatalf("seed target interest: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), wiringDBOpTimeout)
		defer cancel()
		// Sweeps the target above plus anything an admin-session
		// POST /admin/interests actually persisted (see the "admin session"
		// case below) — scoped by the zz-wiring- prefix so the seeded
		// taxonomy (migrations/000009) is never touched.
		_, _ = pool.Exec(ctx, `DELETE FROM interests WHERE slug LIKE 'zz-wiring-%'`)
	})

	// A dedicated throwaway subscriber for the /admin/subscribers/{id} family,
	// seeded through the real store — never a literal id, same CLAUDE.md §8b
	// reasoning as targetInterest above. The admin-session case for
	// GET/POST .../{id} routes below reaches the real handler and may act on
	// this row for real (POST .../clear-complaint 409s since it's never
	// complained; POST .../suppress 400s since this test sends no body) —
	// neither mutates it, but the cleanup below is unconditional regardless.
	targetSubscriber, err := subscribersStore.Create(context.Background(), subscribers.NewSignup{
		Email:      fmt.Sprintf("zz-wiring-%d@example.com", time.Now().UnixNano()),
		ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("seed target subscriber: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), wiringDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM subscribers WHERE email LIKE 'zz-wiring-%'`)
	})

	// A dedicated throwaway campaign for the /admin/campaigns/{id} family,
	// seeded through the real store — never a literal id, same CLAUDE.md §8b
	// reasoning as targetInterest/targetSubscriber above. The admin-session
	// case reaches the real handler for every verb (GET, PATCH, POST
	// .../send, POST .../cancel); this test sends no body on any route, so
	// POST .../send 400s on the missing confirm_count (#0047's plan) and
	// POST .../cancel 409s (the campaign never left draft) — neither
	// mutates the row, but the cleanup below is unconditional regardless,
	// matching targetSubscriber's own comment above.
	targetCampaign, err := campaignsStore.Create(context.Background(), mailing.CampaignInput{
		Name:         fmt.Sprintf("zz-wiring-%d", time.Now().UnixNano()),
		Subject:      "Wiring guard target",
		BodyMD:       "wiring guard target body",
		AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed target campaign: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), wiringDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM email_campaigns WHERE name LIKE 'zz-wiring-%'`)
	})

	// A dedicated throwaway workshop for the /admin/workshops/{id} family,
	// seeded through the real store — never a literal id, same CLAUDE.md
	// §8b reasoning as targetInterest/targetSubscriber/targetCampaign above.
	// The admin-session case reaches the real handler for every verb (GET,
	// PATCH, DELETE); this test sends no body on any route, so PATCH 400s
	// (title cannot be empty resolves to the current non-empty title, but
	// status is required and this test sends none — see
	// workshops.ErrUnknownStatus) and DELETE genuinely removes the row —
	// still proof enough that RequireAdmin let the request through, the
	// same standard this test already applies to POST /admin/subscribers's
	// 503 above. The cleanup below is unconditional regardless of whether
	// DELETE actually ran.
	targetWorkshop, err := workshopsStore.Create(context.Background(), workshops.CreateInput{
		Title: fmt.Sprintf("zz-wiring-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("seed target workshop: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), wiringDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM workshops WHERE title LIKE 'zz-wiring-%'`)
	})

	// adminRoutes (cmd/opencircuit/main.go) is the single list mountAndServe
	// itself loops over to register every admin route behind requireAdmin.
	// Enumerating it here, rather than hand-listing paths, is what closes
	// #0024's review finding: a route added to that list is exercised by
	// this test automatically, with no edit to the test required, and every
	// verb the route uses (not just GET) is proven independently below.
	cases := []struct {
		name   string
		cookie string // "" means no cookie at all
		want   func(status int) bool
	}{
		{"no session", "", func(status int) bool { return status == http.StatusUnauthorized }},
		{"non-admin session", "wiring-nonadmin-token", func(status int) bool { return status == http.StatusForbidden }},
		// The admin session must reach the real handler — i.e. neither guard
		// response — not necessarily succeed outright. Several routes below
		// need a request body this test does not construct (a well-formed
		// PATCH /admin/settings payload, a valid POST /admin/interests
		// payload); a 400 from the real handler is exactly as strong a proof
		// that RequireAdmin let the request through as a 200 is, and avoids
		// this test needing to know every handler's request shape.
		{"admin session", "wiring-admin-token", func(status int) bool {
			return status != http.StatusUnauthorized && status != http.StatusForbidden
		}},
	}
	for _, route := range adminRoutes(settingsH, adminUsersH, adminAuditH, adminInterestsH, adminSubscribersH, adminSuppressionsH, adminCampaignsH, adminCampaignAudienceH, adminCampaignPreviewH, adminCampaignPreflightH, adminCampaignStatsH, adminWorkshopsH) {
		path := resolveAdminRoutePath(route.path, targetUserID, targetInterest.ID, targetSubscriber.ID, targetCampaign.ID, targetWorkshop.ID)
		for _, c := range cases {
			req, err := http.NewRequest(route.method, baseURL+path, nil)
			if err != nil {
				t.Fatalf("%s %s %s: build request: %v", c.name, route.method, path, err)
			}
			if c.cookie != "" {
				req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: c.cookie})
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s %s %s: request: %v", c.name, route.method, path, err)
			}
			resp.Body.Close()
			if !c.want(resp.StatusCode) {
				t.Errorf("%s: %s %s status = %d, unexpected for this case", c.name, route.method, path, resp.StatusCode)
			}
		}
	}

	select {
	case err := <-errCh:
		t.Fatalf("mountAndServe exited unexpectedly: %v", err)
	default:
	}
}

// resolveAdminRoutePath substitutes an adminRoute's {id} path parameter, if
// present, with an id this test seeded itself — never a literal or seeded
// one (CLAUDE.md §8b: "Never target a literal or seeded id in a test").
// /admin/users/... routes take a user id; /admin/interests/... routes take
// an interest id; /admin/subscribers/... routes (#0032) take a subscriber
// id; /admin/campaigns/... routes (#0041) take a campaign id, including its
// /send and /cancel sub-routes. Routes with no {id} (e.g. /admin/settings,
// /admin/audit, /admin/subscribers itself) pass through unchanged.
func resolveAdminRoutePath(path string, userID, interestID, subscriberID, campaignID, workshopID int64) string {
	switch {
	case strings.HasPrefix(path, "/admin/users/"):
		return strings.Replace(path, "{id}", fmt.Sprint(userID), 1)
	case strings.HasPrefix(path, "/admin/interests/"):
		return strings.Replace(path, "{id}", fmt.Sprint(interestID), 1)
	case strings.HasPrefix(path, "/admin/subscribers/"):
		return strings.Replace(path, "{id}", fmt.Sprint(subscriberID), 1)
	case strings.HasPrefix(path, "/admin/campaigns/"):
		return strings.Replace(path, "{id}", fmt.Sprint(campaignID), 1)
	case strings.HasPrefix(path, "/admin/workshops/"):
		return strings.Replace(path, "{id}", fmt.Sprint(workshopID), 1)
	default:
		return path
	}
}

// truncateAdminWiringTables clears the users/sessions rows this test seeds, so
// it starts and leaves the shared TEST_DATABASE_URL clean. audit_log is
// truncated too since RequireAdmin/RequireSession failures never write it but
// this keeps the cleanup symmetric with the handlers-package convention.
func truncateAdminWiringTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wiringDBOpTimeout)
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
