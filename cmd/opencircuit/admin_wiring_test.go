package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/cadence"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/crt"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/events"
	"github.com/brennanMKE/OpenCircuitSF/internal/eventtypes"
	"github.com/brennanMKE/OpenCircuitSF/internal/guideposts"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/serversettings"
	"github.com/brennanMKE/OpenCircuitSF/internal/sesnotify"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/venues"
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
	registerWiringTest(t)
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
	// #0566: attach the server settings handler so both of its routes are
	// swept by the 401/403 proof below.
	settingsH.SetServerSettings(handlers.NewAdminServerSettingsHandler(serversettings.NewManager(serversettings.NewStore(pool), nil, nil, nil, nil), auditLogger))
	// #0652: attach the API tokens handler so its three admin routes are
	// swept below, and /api/v1/token is mounted for the bearer checks.
	apiTokensH := handlers.NewAPITokensHandler(apitokens.NewStore(pool), auditLogger)
	// #0708: the ticket upload route, so its failed-auth limit is proved on
	// the real mux. A wrong ticket is refused before the media directory is
	// read, so none is set.
	apiTokensH.SetMediaUpload(handlers.NewMediaV1(func() string { return "" }, cfg.BaseURL, auditLogger))
	// #0659, #0676: the guide-posts handler, shared by its admin routes
	// (mounted through adminOutreachH below) and the v1 guide-post ops.
	guideStore := guideposts.NewStore(pool, outbox.NewStore(pool))
	adminGuidePostsH := handlers.NewAdminGuidePostsHandler(guideStore, guideposts.NewLinker(guideStore, nil), (*shortlinks.Client)(nil), auditLogger, cfg.BaseURL, (*guideposts.Publisher)(nil))
	settingsH.SetAPITokens(apiTokensH)
	adminUsersH := handlers.NewAdminUsersHandler(store, auditLogger)
	adminAuditH := handlers.NewAdminAuditHandler(audit.NewReader(pool))
	interestsStore := interests.NewStore(pool)
	// #0024: exercised the same way as adminUsersH/adminAuditH above, so this
	// test's gap-closing argument (main.go's REAL mountAndServe, not a
	// per-handler suite's own small mux) covers /admin/interests too.
	adminInterestsH := handlers.NewAdminInterestsHandler(interestsStore, auditLogger)
	eventTypesStore := eventtypes.NewStore(pool)
	// #0613: exercised the same way as adminInterestsH above.
	adminEventTypesH := handlers.NewAdminEventTypesHandler(eventTypesStore, auditLogger, nil)
	venuesStore := venues.NewStore(pool)
	adminVenuesH := handlers.NewAdminVenuesHandler(venuesStore, auditLogger, nil)
	subscribersStore := subscribers.NewStore(pool)
	// #0032: exercised the same way as adminInterestsH above. manualAdd is
	// nil (matching this test's own "subscribeH: not exercised by this test"
	// choice below) — POST /admin/subscribers therefore answers 503 rather
	// than actually dispatching a signup, which is still proof enough that
	// RequireAdmin let the request through (neither 401 nor 403); see the
	// "admin session" case's comment.
	adminSubscribersH := handlers.NewAdminSubscribersHandler(subscribersStore, interestsStore, nil, nil, store, auditLogger)
	// #0125: exercised the same way as adminSubscribersH above.
	importsStore := subscribers.NewImportStore(pool)
	adminImportsH := handlers.NewAdminImportsHandler(importsStore, interestsStore, auditLogger)
	// #0128: exercised the same way as adminSubscribersH above.
	adminPendingH := handlers.NewAdminPendingHandler(subscribersStore, outbox.NewStore(pool), store, auditLogger)
	// #0100: exercised the same way as adminSubscribersH above.
	suppressionsStore := subscribers.NewSuppressionStore(pool)
	adminSuppressionsH := handlers.NewAdminSuppressionsHandler(suppressionsStore, subscribersStore, auditLogger)
	// #0124: exercised the same way as adminSuppressionsH above.
	adminDeliverabilityH := handlers.NewAdminDeliverabilityHandler(
		subscribersStore, subscribersStore, subscribersStore, sesnotify.NewStore(pool), mailing.NewCampaignStatsStore(pool), auditLogger,
	)
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
	// #0123: exercised the same way as adminCampaignStatsH above — a real
	// PATCH over the same real campaignsStore, auditor non-nil so this
	// test's route table proof covers the audited path too. The invalidator
	// (#0319) is nil, matching adminWorkshopsH's own nil below — this
	// test's guard proof is unaffected either way since RequireAdmin runs
	// before the handler ever calls it.
	adminCampaignArchiveH := handlers.NewAdminCampaignArchiveHandler(campaignsStore, auditLogger, nil)
	// #0051: exercised the same way as adminCampaignStatsH above. The
	// invalidator is nil (matching main.go's own production wiring — see
	// that call site's comment: *seo.Site is wired in by #0054), which has
	// no bearing on this test's guard proof since RequireAdmin runs before
	// the handler ever calls it.
	workshopsStore := workshops.NewStore(pool)
	adminWorkshopsH := handlers.NewAdminWorkshopsHandler(workshopsStore, nil, campaignsStore, auditLogger, cfg.BaseURL)
	// #0678: the event coverage service, mounted through adminWorkshopsH,
	// so the route loop proves its five admin routes are admin-only. A nil
	// Luma feed: the report's Luma part answers "off" with no network call.
	eventCadenceH := handlers.NewEventCadenceHandler(cadence.NewStore(pool), workshopsStore, eventTypesStore, venuesStore, nil, auditLogger, cfg.BaseURL)
	adminWorkshopsH.SetCadence(eventCadenceH)
	// #0653, #0675: the v1 operation registry, built as main.go builds it
	// (#0677 moved it here, below the stores its event ops share).
	apiTokensH.SetV1(handlers.BuildV1Registry(
		api.Config{Version: "wiring-version", Idempotency: api.NewIdempotencyStore(pool)},
		handlers.V1Deps{
			Engagement: outreach.NewStore(pool), BaseURL: cfg.BaseURL, GuidePosts: adminGuidePostsH,
			Workshops: adminWorkshopsH, Venues: venuesStore, EventTypes: eventTypesStore, Interests: interestsStore,
			Cadence: eventCadenceH,
		},
	))
	// #0433: exercised the same way as adminWorkshopsH above — a real
	// handler over a real (if unused by this test) directory, so this
	// test's route table proof covers POST /admin/media/upload too.
	adminMediaH := handlers.NewAdminMediaHandler(t.TempDir(), auditLogger)
	// #0061: exercised the same way as adminCampaignStatsH above — a real
	// read-only GET over the same real stores already constructed for the
	// other admin handlers in this test (subscribersStore, interestsStore,
	// campaignsStore, campaignStatsStore, store).
	adminDashboardH := handlers.NewAdminDashboardHandler(
		subscribersStore, interestsStore, campaignsStore, campaignStatsStore, store, outbox.NewStore(pool), cfg.SESSandbox,
	)
	// #0516: exercised the same way as adminDashboardH above — needs no
	// store at all (the values are compiled-in strings), so this test's
	// route-table proof covers GET /admin/version too, including its
	// session+admin authorization.
	adminVersionH := handlers.NewAdminVersionHandler("wiring-version", "0123456789abcdef0123456789abcdef01234567", nil)
	// #0393: exercised the same way as adminDashboardH above — a real store,
	// so this test's guard proof covers /admin/crt-commands too.
	crtStore := crt.NewStore(pool)
	adminCrtCommandsH := handlers.NewAdminCrtCommandsHandler(crtStore, auditLogger)
	// #0544: a real store with a nil ShortLinks client, so the route-table
	// proof covers the outreach routes' session+admin authorization too. The
	// {id} placeholder is left unresolved for the /admin/outreach* paths (a
	// 400 from the real handler still proves RequireAdmin let it through).
	adminOutreachH := handlers.NewAdminOutreachHandler(outreach.NewStore(pool), outreach.NewStore(pool), outreach.NewStore(pool), workshopsStore, (*shortlinks.Client)(nil), auditLogger, cfg.BaseURL, (*outreach.Publisher)(nil))
	// #0659: the guide-post routes, mounted through adminOutreachH, so the
	// route loop proves all nine are admin-only. adminGuidePostsH is built
	// above, before the v1 registry (#0676) that shares it.
	adminOutreachH.SetGuidePosts(adminGuidePostsH)
	// #0643: mounted through adminCampaignsH, so its guard is proven here too.
	adminCampaignsH.SetUpcomingEvents(handlers.NewAdminNewsletterEventsHandler(campaignsStore, workshopsStore, outreach.NewStore(pool), outreach.NewStore(pool), (*shortlinks.Client)(nil), auditLogger, cfg.BaseURL))
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
	site, err := buildSEOSite(cfg, nil, nil)
	if err != nil {
		t.Fatalf("build seo site: %v", err)
	}

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, pool,
			authH, credsH, settingsH, adminUsersH, adminAuditH, adminInterestsH, adminEventTypesH, adminVenuesH, adminSubscribersH, adminImportsH, adminPendingH, adminSuppressionsH, adminDeliverabilityH, adminCampaignsH, adminCampaignAudienceH, adminCampaignPreviewH, adminCampaignPreflightH, adminCampaignStatsH, adminCampaignArchiveH, adminWorkshopsH, adminMediaH, adminDashboardH, adminVersionH, adminCrtCommandsH, adminOutreachH, eventsH, meH, nil, /* subscribeH: not exercised by this test */
			nil, nil, nil, nil, nil, nil, nil, /* publicInterestsH, preferencesH, confirmH, unsubscribeH, publicWorkshopsH, publicListStatsH, publicCrtSessionH: not exercised by this test */
			nil, /* publicArchiveH: not exercised by this test */
			nil, /* sesNotifyH: not exercised by this test */
			nil, /* sesInboundH: not exercised by this test */
			nil, /* sendWorker: not exercised by this test */
			nil, /* outboxWorker: not exercised */
			nil, /* outreachPublisher: not exercised */
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

	// #0652: two real API tokens. bearerSecret is sent as a Bearer token to
	// every admin route and must be refused (criterion 4); the second is the
	// {id} target the admin case's revoke consumes.
	apiTokenStore := apitokens.NewStore(pool)
	bearerTok, bearerSecret, err := apiTokenStore.Create(context.Background(), "wiring bearer", []string{apitokens.ScopeEngagementRead}, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("seed bearer api token: %v", err)
	}
	targetAPIToken, _, err := apiTokenStore.Create(context.Background(), "wiring target", []string{apitokens.ScopeEngagementRead}, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("seed target api token: %v", err)
	}

	// A dedicated throwaway interest for the /admin/interests/{id} family,
	// seeded through the real store rather than a literal id. AGENTS.md §8b
	// exists because of exactly this test: #0024's first pass targeted a
	// hardcoded /admin/interests/1 — the real seeded "microcontrollers"
	// taxonomy row — and a guard-removal mutation against DELETE permanently
	// destroyed it. This target is created fresh per run and swept up below
	// by its zz-wiring- prefix, so it stays safe even while the guard under
	// test is deliberately gone.
	targetSlug := fmt.Sprintf("zz-wiring-%d", testdb.Unique())
	targetEventType, err := eventTypesStore.Create(context.Background(), fmt.Sprintf("zz-wiring-%d", testdb.Unique()), "Wiring guard target", 0)
	if err != nil {
		t.Fatalf("seeding throwaway event type: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM event_types WHERE slug LIKE 'zz-wiring-%'`)
	})
	targetVenue, err := venuesStore.Create(context.Background(), venues.CreateInput{Name: "zz-wiring venue target"})
	if err != nil {
		t.Fatalf("seeding throwaway venue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE workshops SET venue_id = NULL WHERE venue_id IN (SELECT id FROM venues WHERE name = 'zz-wiring venue target')`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM venues WHERE name = 'zz-wiring venue target'`)
	})
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
	// seeded through the real store — never a literal id, same AGENTS.md §8b
	// reasoning as targetInterest above. The admin-session case for
	// GET/POST .../{id} routes below reaches the real handler and may act on
	// this row for real (POST .../clear-complaint 409s since it's never
	// complained; POST .../suppress 400s since this test sends no body) —
	// neither mutates it, but the cleanup below is unconditional regardless.
	targetSubscriber, err := subscribersStore.Create(context.Background(), subscribers.NewSignup{
		Email:      fmt.Sprintf("zz-wiring-%d@example.com", testdb.Unique()),
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
	// seeded through the real store — never a literal id, same AGENTS.md §8b
	// reasoning as targetInterest/targetSubscriber above. The admin-session
	// case reaches the real handler for every verb (GET, PATCH, POST
	// .../send, POST .../cancel); this test sends no body on any route, so
	// POST .../send 400s on the missing confirm_count (#0047's plan) and
	// POST .../cancel 409s (the campaign never left draft) — neither
	// mutates the row, but the cleanup below is unconditional regardless,
	// matching targetSubscriber's own comment above.
	targetCampaign, err := campaignsStore.Create(context.Background(), mailing.CampaignInput{
		Name:         fmt.Sprintf("zz-wiring-%d", testdb.Unique()),
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
	// seeded through the real store — never a literal id, same AGENTS.md
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
		Title: fmt.Sprintf("zz-wiring-%d", testdb.Unique()),
	})
	if err != nil {
		t.Fatalf("seed target workshop: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), wiringDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM workshops WHERE title LIKE 'zz-wiring-%'`)
	})

	// A dedicated throwaway crt_commands row for the
	// /admin/crt-commands/{id} family (#0393), seeded through the real
	// store — never a literal id, same AGENTS.md §8b reasoning as every
	// other target* above. The admin-session case reaches the real handler
	// for every verb (GET, POST, PATCH, DELETE); this test sends no body on
	// PATCH, so it 400s on the empty/invalid JSON body, and DELETE
	// genuinely removes the row — still proof enough that RequireAdmin let
	// the request through, the same standard targetWorkshop's own comment
	// above applies. The cleanup below is unconditional regardless of
	// whether DELETE actually ran.
	targetCrtSlug := fmt.Sprintf("zz-wiring-%d", testdb.Unique())
	targetCrtCommand, err := crtStore.Create(context.Background(), targetCrtSlug, "wiring --target", "wiring guard target output", crt.SourceStatic, 0)
	if err != nil {
		t.Fatalf("seed target crt command: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), wiringDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM crt_commands WHERE slug LIKE 'zz-wiring-%'`)
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
		bearer string // when set: Authorization: Bearer <bearer>, no cookie
		want   func(status int) bool
	}{
		// #0652: a real API token never authenticates an admin route.
		{"api token as bearer", "", bearerSecret, func(status int) bool { return status == http.StatusUnauthorized }},
		{"no session", "", "", func(status int) bool { return status == http.StatusUnauthorized }},
		{"non-admin session", "wiring-nonadmin-token", "", func(status int) bool { return status == http.StatusForbidden }},
		// The admin session must reach the real handler — i.e. neither guard
		// response — not necessarily succeed outright. Several routes below
		// need a request body this test does not construct (a well-formed
		// PATCH /admin/settings payload, a valid POST /admin/interests
		// payload); a 400 from the real handler is exactly as strong a proof
		// that RequireAdmin let the request through as a 200 is, and avoids
		// this test needing to know every handler's request shape.
		{"admin session", "wiring-admin-token", "", func(status int) bool {
			return status != http.StatusUnauthorized && status != http.StatusForbidden
		}},
	}
	for _, route := range adminRoutes(settingsH, adminUsersH, adminAuditH, adminInterestsH, adminEventTypesH, adminVenuesH, adminSubscribersH, adminImportsH, adminPendingH, adminSuppressionsH, adminDeliverabilityH, adminCampaignsH, adminCampaignAudienceH, adminCampaignPreviewH, adminCampaignPreflightH, adminCampaignStatsH, adminCampaignArchiveH, adminWorkshopsH, adminMediaH, adminDashboardH, adminVersionH, adminCrtCommandsH, adminOutreachH) {
		path := resolveAdminRoutePath(route.path, targetUserID, targetInterest.ID, targetEventType.ID, targetVenue.ID, targetSubscriber.ID, targetCampaign.ID, targetWorkshop.ID, targetCrtCommand.ID, targetAPIToken.ID)
		for _, c := range cases {
			req, err := http.NewRequest(route.method, baseURL+path, nil)
			if err != nil {
				t.Fatalf("%s %s %s: build request: %v", c.name, route.method, path, err)
			}
			if c.cookie != "" {
				req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: c.cookie})
			}
			if c.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+c.bearer)
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

	// #0678: the coverage report on the real mux. No session is a 401;
	// the admin session gets the report, with Luma "off" (nil feed).
	coverageReq := func(cookie string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, baseURL+"/admin/event-coverage", nil)
		if err != nil {
			t.Fatalf("build coverage request: %v", err)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("coverage request: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	if code, b := coverageReq(""); code != http.StatusUnauthorized {
		t.Errorf("GET /admin/event-coverage with no session = %d %s, want 401", code, b)
	}
	if code, b := coverageReq("wiring-admin-token"); code != http.StatusOK || !strings.Contains(string(b), `"luma":{"status":"off"`) {
		t.Errorf("GET /admin/event-coverage as admin = %d %s, want 200 with luma status off", code, b)
	}

	// #0652: /api/v1/token on the real mux — Bearer only, GET only.
	v1Body := func(method, url, bearer, cookie string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, url, nil)
		if err != nil {
			t.Fatalf("build v1 request: %v", err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("v1 request: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	v1 := func(method, url, bearer, cookie string) int {
		t.Helper()
		code, _ := v1Body(method, url, bearer, cookie)
		return code
	}
	v1ErrCode := func(b []byte) string {
		var e api.ErrorBody
		if err := json.Unmarshal(b, &e); err != nil {
			return "undecodable: " + string(b)
		}
		return e.Error.Code
	}
	v1URL := baseURL + "/api/v1/token"
	// #0675: every v1 error is the envelope, including the token 401.
	if code, b := v1Body(http.MethodGet, v1URL, "", ""); code != http.StatusUnauthorized || v1ErrCode(b) != api.CodeUnauthorized {
		t.Errorf("GET /api/v1/token with no token = %d %s, want 401 unauthorized", code, b)
	}
	// #0675: whoami, the JSON 404 and the public OpenAPI document.
	if code, b := v1Body(http.MethodGet, baseURL+"/api/v1/me", bearerSecret, ""); code != http.StatusOK || !strings.Contains(string(b), `"server_version":"wiring-version"`) {
		t.Errorf("GET /api/v1/me with Bearer = %d %s, want 200 with server_version", code, b)
	}
	if code, b := v1Body(http.MethodGet, baseURL+"/api/v1/nope", bearerSecret, ""); code != http.StatusNotFound || v1ErrCode(b) != api.CodeNotFound {
		t.Errorf("GET /api/v1/nope = %d %s, want 404 not_found", code, b)
	}
	if got := v1(http.MethodGet, baseURL+api.OpenAPIPath, "", ""); got != http.StatusOK {
		t.Errorf("GET %s with no credentials = %d, want 200", api.OpenAPIPath, got)
	}
	// #0675: every registered operation refuses no token and a session
	// cookie alone. Path parameters are filled with 1; the request never
	// reaches the operation.
	for _, op := range apiTokensH.V1().Operations() {
		u := baseURL + regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(op.Path, "1")
		if got := v1(op.Method, u, "", ""); got != http.StatusUnauthorized {
			t.Errorf("%s %s with no token = %d, want 401", op.Method, op.Path, got)
		}
		if got := v1(op.Method, u, "", "wiring-admin-token"); got != http.StatusUnauthorized {
			t.Errorf("%s %s with admin session cookie = %d, want 401", op.Method, op.Path, got)
		}
	}
	if got := v1(http.MethodGet, v1URL, bearerSecret, ""); got != http.StatusOK {
		t.Errorf("GET /api/v1/token with Bearer = %d, want 200", got)
	}
	if got := v1(http.MethodGet, v1URL+"?access_token="+bearerSecret, "", ""); got != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/token with ?access_token = %d, want 401", got)
	}
	if got := v1(http.MethodGet, v1URL, "", "wiring-admin-token"); got != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/token with admin session cookie = %d, want 401", got)
	}
	if got := v1(http.MethodPost, v1URL, bearerSecret, ""); got != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/v1/token with Bearer = %d, want 405", got)
	}
	// #0653: /api/v1/engagement on the real mux.
	engURL := baseURL + "/api/v1/engagement"
	if got := v1(http.MethodGet, engURL, bearerSecret, ""); got != http.StatusOK {
		t.Errorf("GET /api/v1/engagement with Bearer = %d, want 200", got)
	}
	if got := v1(http.MethodPost, engURL, bearerSecret, ""); got != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/v1/engagement with Bearer = %d, want 405", got)
	}
	if got := v1(http.MethodGet, engURL, "", ""); got != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/engagement with no token = %d, want 401", got)
	}
	if got := v1(http.MethodGet, engURL, "", "wiring-admin-token"); got != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/engagement with admin session cookie = %d, want 401", got)
	}
	// #0679: /mcp on the real mux — Bearer only, stateless JSON. Raw HTTP,
	// so this package needs no MCP client.
	mcpURL := baseURL + "/mcp"
	mcpPost := func(method, bearer, cookie, body string) (int, string, []byte) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, mcpURL, rd)
		if err != nil {
			t.Fatalf("build mcp request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("mcp request: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), b
	}
	const mcpInit = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"wiring","version":"0"}}}`
	if code, _, b := mcpPost(http.MethodPost, "", "", mcpInit); code != http.StatusUnauthorized || v1ErrCode(b) != api.CodeUnauthorized {
		t.Errorf("POST /mcp with no key = %d %s, want 401 unauthorized", code, b)
	}
	if code, _, _ := mcpPost(http.MethodPost, "", "wiring-admin-token", mcpInit); code != http.StatusUnauthorized {
		t.Errorf("POST /mcp with admin session cookie = %d, want 401", code)
	}
	if code, ct, b := mcpPost(http.MethodPost, bearerSecret, "", mcpInit); code != http.StatusOK || !strings.HasPrefix(ct, "application/json") || !strings.Contains(string(b), `"wiring-version"`) {
		t.Errorf("POST /mcp initialize with Bearer = %d %q %s, want 200 application/json naming wiring-version", code, ct, b)
	}
	if code, _, _ := mcpPost(http.MethodGet, bearerSecret, "", ""); code != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp with Bearer = %d, want 405 (stateless)", code)
	}
	// #0674: the real mux rate-limits /api/v1 per token. A fresh token gets
	// at least its full minute's budget, then a 429; the bucket refills
	// during the loop, so the count is a floor, not exact.
	_, rateSecret, err := apiTokenStore.Create(context.Background(), "wiring rate", []string{apitokens.ScopeEngagementRead}, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("seed rate api token: %v", err)
	}
	okCount, limited := 0, false
	for i := 0; i < 2*middleware.APIv1RequestsPerMinute && !limited; i++ {
		switch got := v1(http.MethodGet, v1URL, rateSecret, ""); got {
		case http.StatusOK:
			okCount++
		case http.StatusTooManyRequests:
			limited = true
		default:
			t.Fatalf("rate loop request %d = %d", i, got)
		}
	}
	if !limited || okCount < middleware.APIv1RequestsPerMinute {
		t.Errorf("rate limit: %d OK before 429 (limited=%v), want >= %d then 429", okCount, limited, middleware.APIv1RequestsPerMinute)
	}
	if got := v1(http.MethodGet, v1URL, bearerSecret, ""); got != http.StatusOK {
		t.Errorf("another token after one was throttled = %d, want 200", got)
	}
	// #0683: failed bearer authentication is limited per client IP across
	// /api/v1/ and /mcp, with one shared bucket. The client is this test's
	// loopback peer, so X-Forwarded-For names the IP (ClientIP's rule);
	// distinct documentation addresses keep the loopback bucket the rest of
	// this test uses untouched.
	unknownKey, err := apitokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	fromIP := func(method, url, bearer, xff, body string) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, url, rd)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("X-Forwarded-For", xff)
		req.Header.Set("Authorization", "Bearer "+bearer)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	for _, c := range []struct{ name, method, url, body, xff string }{
		{"/api/v1", http.MethodGet, v1URL, "", "192.0.2.83"},
		{"/mcp", http.MethodPost, mcpURL, mcpInit, "192.0.2.84"},
	} {
		fails, limited := 0, false
		for i := 0; i < 3*middleware.APIAuthFailuresPerMinute && !limited; i++ {
			switch code, b := fromIP(c.method, c.url, unknownKey, c.xff, c.body); code {
			case http.StatusUnauthorized:
				fails++
			case http.StatusTooManyRequests:
				if v1ErrCode(b) != "rate_limited" {
					t.Errorf("%s 429 body %s", c.name, b)
				}
				limited = true
			default:
				t.Fatalf("%s bad-key request %d = %d %s", c.name, i, code, b)
			}
		}
		if !limited || fails < middleware.APIAuthFailuresPerMinute {
			t.Errorf("%s: %d 401s before 429 (limited=%v), want >= %d then 429", c.name, fails, limited, middleware.APIAuthFailuresPerMinute)
		}
		// Over the limit, a valid key from that IP is refused too, before
		// any lookup; the same key from another IP is unaffected.
		if code, _ := fromIP(c.method, c.url, bearerSecret, c.xff, c.body); code != http.StatusTooManyRequests {
			t.Errorf("%s valid key from the throttled IP = %d, want 429", c.name, code)
		}
		if code, _ := fromIP(c.method, c.url, bearerSecret, "192.0.2.85", c.body); code != http.StatusOK {
			t.Errorf("%s valid key from another IP = %d, want 200", c.name, code)
		}
	}
	// One bucket: the IP throttled on /api/v1 is throttled on /mcp too.
	if code, _ := fromIP(http.MethodPost, mcpURL, unknownKey, "192.0.2.83", mcpInit); code != http.StatusTooManyRequests {
		t.Errorf("/mcp from the IP throttled on /api/v1 = %d, want 429", code)
	}
	// #0708: the media upload route counts failed tickets (not API keys) in
	// that same bucket, so an IP that exhausts it there is throttled on
	// /api/v1 too.
	uploadURL := baseURL + "/api/v1/media/uploads/wiring-0708"
	fails, limited := 0, false
	for i := 0; i < 3*middleware.APIAuthFailuresPerMinute && !limited; i++ {
		switch code, b := fromIP(http.MethodPost, uploadURL, "oup_wrong", "192.0.2.86", ""); code {
		case http.StatusUnauthorized:
			fails++
		case http.StatusTooManyRequests:
			limited = true
		default:
			t.Fatalf("upload bad-ticket request %d = %d %s", i, code, b)
		}
	}
	if !limited || fails < middleware.APIAuthFailuresPerMinute {
		t.Errorf("upload: %d 401s before 429 (limited=%v), want >= %d then 429", fails, limited, middleware.APIAuthFailuresPerMinute)
	}
	if code, _ := fromIP(http.MethodGet, v1URL, unknownKey, "192.0.2.86", ""); code != http.StatusTooManyRequests {
		t.Errorf("/api/v1 from the IP throttled on the upload route = %d, want 429", code)
	}
	if _, err := apiTokenStore.Revoke(context.Background(), bearerTok.ID, time.Now()); err != nil {
		t.Fatalf("revoke bearer token: %v", err)
	}
	if got := v1(http.MethodGet, v1URL, bearerSecret, ""); got != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/token after revoke = %d, want 401", got)
	}

	select {
	case err := <-errCh:
		t.Fatalf("mountAndServe exited unexpectedly: %v", err)
	default:
	}
}

// resolveAdminRoutePath substitutes an adminRoute's {id} path parameter, if
// present, with an id this test seeded itself — never a literal or seeded
// one (AGENTS.md §8b: "Never target a literal or seeded id in a test").
// /admin/users/... routes take a user id; /admin/interests/... routes take
// an interest id; /admin/subscribers/... routes (#0032) take a subscriber
// id; /admin/campaigns/... routes (#0041) take a campaign id, including its
// /send and /cancel sub-routes. Routes with no {id} (e.g. /admin/settings,
// /admin/audit, /admin/subscribers itself) pass through unchanged.
func resolveAdminRoutePath(path string, userID, interestID, eventTypeID, venueID, subscriberID, campaignID, workshopID, crtCommandID, apiTokenID int64) string {
	switch {
	case strings.HasPrefix(path, "/admin/users/"):
		return strings.Replace(path, "{id}", fmt.Sprint(userID), 1)
	case strings.HasPrefix(path, "/admin/interests/"):
		return strings.Replace(path, "{id}", fmt.Sprint(interestID), 1)
	case strings.HasPrefix(path, "/admin/event-types/"):
		return strings.Replace(path, "{id}", fmt.Sprint(eventTypeID), 1)
	case strings.HasPrefix(path, "/admin/venues/"):
		return strings.Replace(path, "{id}", fmt.Sprint(venueID), 1)
	case strings.HasPrefix(path, "/admin/subscribers/"):
		return strings.Replace(path, "{id}", fmt.Sprint(subscriberID), 1)
	case strings.HasPrefix(path, "/admin/campaigns/"):
		return strings.Replace(path, "{id}", fmt.Sprint(campaignID), 1)
	case strings.HasPrefix(path, "/admin/workshops/"):
		return strings.Replace(path, "{id}", fmt.Sprint(workshopID), 1)
	case strings.HasPrefix(path, "/admin/crt-commands/"):
		return strings.Replace(path, "{id}", fmt.Sprint(crtCommandID), 1)
	case strings.HasPrefix(path, "/admin/api-tokens/"):
		return strings.Replace(path, "{id}", fmt.Sprint(apiTokenID), 1)
	default:
		return path
	}
}

// truncateAdminWiringTables clears the users/sessions rows this test seeds, so
// it starts and leaves the shared TEST_DATABASE_URL clean. audit_log is
// truncated too since RequireAdmin/RequireSession failures never write it but
// this keeps the cleanup symmetric with the handlers-package convention.
//
// These tables are shared by every *_wiring_test.go in this package that
// calls this helper, and the TRUNCATE has no isolation of its own: it clears
// every row, not just the ones the calling test seeded. That is safe today
// only because nothing in this package calls t.Parallel() (#0165 verified
// zero occurrences). If a test in this package ever gains t.Parallel(), one
// test's entry or cleanup TRUNCATE can run while a sibling is mid-test,
// wiping its seeded users and sessions out from under it.
//
// Reproduced (#0165): with t.Parallel() added temporarily to two of these
// tests and run under `-parallel 2`, 4 of 5 runs failed -- either a
// foreign-key violation seeding a session ("sessions_user_id_fkey", the
// user row truncated between insert calls) or spurious 401s from routes that
// should have returned 403/200 (the session row truncated mid-request). Nothing
// in the output points back at the truncate; it presents as an unrelated
// test's fixtures mysteriously vanishing.
//
// registerWiringTest (below) turns that into an immediate, named failure
// instead of silent corruption. Every *_wiring_test.go caller of this
// function calls registerWiringTest(t) once, before its first
// truncateAdminWiringTables call -- see admin_wiring_test.go's own callers
// for the pattern. Adding t.Parallel() to a test that skips that call is
// still a bug this guard cannot see; it protects the tests that use the
// established pattern, not the package against every possible misuse.
func truncateAdminWiringTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wiringDBOpTimeout)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`TRUNCATE sessions, passkey_credentials, audit_log, api_tokens, users RESTART IDENTITY CASCADE`,
	); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
}

// wiringTablesOwner names the test currently holding exclusive use of
// truncateAdminWiringTables's shared tables, or is nil when no such test is
// active. registerWiringTest sets it for the duration of a test (entry
// through t.Cleanup), not just for the instant of a TRUNCATE call, because
// the hazard is a sibling's TRUNCATE landing anywhere in that window.
var wiringTablesOwner atomic.Pointer[string]

// registerWiringTest claims exclusive use of truncateAdminWiringTables's
// shared tables for the duration of t and registers a t.Cleanup that
// releases the claim. Call it once, before a wiring test's first
// truncateAdminWiringTables call.
//
// #0209: this used to take a *pgxpool.Pool parameter that it never read.
// wiringTablesOwner is a package-level global -- correctly so, since the
// hazard it guards against (a sibling's TRUNCATE landing mid-test) is an
// in-process race, not something scoped to which database connection a test
// happens to hold. A pool parameter would have implied a per-pool or
// per-database claim, which is not what this does; dropped rather than kept
// for call-site symmetry with truncateAdminWiringTables(t, pool).
//
// If another *_wiring_test.go test already holds the claim -- which can only
// happen if this package gains t.Parallel() -- this fails the test
// immediately, naming both tests, instead of letting one test's TRUNCATE
// silently destroy the other's fixtures (#0165). #0209 additionally guards
// the case this can't reach: a test that calls truncateAdminWiringTables
// under t.Parallel() without ever calling registerWiringTest at all — see
// wiring_parallel_guard_test.go.
func registerWiringTest(t *testing.T) {
	t.Helper()
	name := t.Name()
	if !wiringTablesOwner.CompareAndSwap(nil, &name) {
		holder := "another test"
		if prev := wiringTablesOwner.Load(); prev != nil {
			holder = *prev
		}
		t.Fatalf("registerWiringTest: %s cannot run concurrently with %s -- "+
			"both use truncateAdminWiringTables's shared tables with no "+
			"isolation, so this package must not run *_wiring_test.go tests "+
			"under t.Parallel() (see truncateAdminWiringTables's doc comment, #0165)",
			name, holder)
	}
	t.Cleanup(func() {
		wiringTablesOwner.Store(nil)
	})
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
