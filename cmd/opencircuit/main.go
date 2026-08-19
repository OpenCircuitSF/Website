package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"golang.org/x/time/rate"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/devstore"
	"github.com/brennanMKE/OpenCircuitSF/internal/events"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/seo"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/web"
)

const version = "0.1.0"

func main() {
	// Subcommand routing: `opencircuit serve` starts the HTTP server;
	// `opencircuit seed` bootstraps the admin user; anything else (including
	// no argument or `version`) prints the version.
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "serve":
		if err := serve(); err != nil {
			log.Fatalf("opencircuit: %v", err)
		}
	case "seed":
		if err := seed(); err != nil {
			log.Fatalf("opencircuit: %v", err)
		}
	default:
		fmt.Printf("opencircuit %s\n", version)
	}
}

// serve loads configuration, connects the database pool (Postgres path) or
// constructs the in-memory dev store (dev path), mounts the routes, and listens
// on the configured port until the process is terminated.
func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// ── Backend selection ────────────────────────────────────────────────────
	// Dev mode is engaged ONLY by an explicit STORAGE=json — never by an empty
	// DATABASE_URL — so production can never silently fall back to the in-memory
	// store and lose data. Without STORAGE=json the Postgres path always runs.
	if cfg.DevMode() {
		return serveDevMode(cfg)
	}
	return servePostgres(cfg)
}

// servePostgres is the production path: connects to Postgres, constructs the
// real stores, and serves.
func servePostgres(cfg *config.Config) error {
	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	wa, err := auth.NewWebAuthn(cfg)
	if err != nil {
		return err
	}

	// MAILER_NOOP guard (carried into #0026 from #0027's review): refuse to
	// start with outbound email silently disabled anywhere but a local dev
	// box. Without this, #0026's uniform 202 makes a no-op'd mailer
	// completely invisible — the caller always sees the same success
	// response, and the operator sees no error, while every confirmation
	// and already-subscribed email simply stops arriving.
	if err := checkMailerNoOp(cfg, slog.Default()); err != nil {
		return err
	}

	// Mailer: the real SES v2 API mailer, authenticated via the EC2 instance
	// role's default AWS credential chain (#0027) — replacing the SMTP-based
	// auth.SESMailer, which could never authenticate (this project has no
	// static AWS credentials and no SES SMTP username/password in
	// configuration; see #0007). MAILER_NOOP=true swaps in a stdout no-op
	// instead, for local development against Postgres before SES domain
	// verification/DKIM/production access is done (CLAUDE.md §10 item 2).
	//
	// sesSender is the shared internal/mailing.Mailer primitive: constructed
	// once here and used two ways below — wrapped by auth.NewSESMailerWithSender
	// for the three transactional auth emails, and passed directly to
	// handlers.NewSubscribeHandler (#0026) for the double opt-in
	// confirmation and already-subscribed emails. Building it once avoids
	// two separate SES v2 API clients (and two separate credential
	// resolutions) for what is the same underlying send primitive.
	var sesSender mailing.Mailer
	var mailer auth.Mailer
	if cfg.MailerNoOp {
		sesSender = noOpMailingMailer{}
		mailer = auth.NoOpMailer{BaseURL: cfg.BaseURL}
	} else {
		sender, err := mailing.NewSESMailer(ctx, cfg)
		if err != nil {
			return fmt.Errorf("opencircuit: constructing SES mailer: %w", err)
		}
		sesSender = sender
		mailer = auth.NewSESMailerWithSender(sender, cfg)
	}

	// Append-only audit log writer, shared by every service/handler that
	// records an action. Auth ceremonies write their entries inside their own
	// transaction (WriteTx); API/admin handlers log-and-continue (Record) since
	// their action has already committed.
	auditLogger := audit.New(pool)

	store := auth.NewStore(pool)
	regSvc := auth.NewRegistrationService(store, wa, mailer, auditLogger, cfg)
	loginSvc := auth.NewLoginService(store, wa, mailer, auditLogger, slog.Default())
	recoverSvc := auth.NewRecoveryService(store, wa, mailer, auditLogger)
	authH := handlers.NewAuthHandler(regSvc, loginSvc, recoverSvc, slog.Default())
	credsH := handlers.NewCredentialsHandler(store, auditLogger)
	settingsH := handlers.NewSettingsHandler(store, auditLogger)
	// Admin user management: list/detail/deactivate/reactivate. The
	// deactivate/reactivate paths write their account.deactivated/reactivated
	// audit row inside the store's transaction (WriteTx) so it commits
	// atomically with the active flip and session deletion.
	adminUsersH := handlers.NewAdminUsersHandler(store, auditLogger)
	// Admin audit log view: paginated, newest-first, optional ?user_id=
	// filter. Reads through audit.Reader over the same shared pool as the writer.
	adminAuditH := handlers.NewAdminAuditHandler(audit.NewReader(pool))

	// Subscribe endpoint (#0026): double opt-in signup with anti-abuse
	// controls. subscribers.Store and interests.Store are both #0025/#0023's
	// approved data layers; NoSuppressions is the seam #0033's suppressions
	// table (not yet built — a separate, later-numbered issue) will replace
	// with a real check — see handlers.SuppressionChecker's doc comment.
	subscribersStore := subscribers.NewStore(pool)
	interestsStore := interests.NewStore(pool)
	subscribeH := handlers.NewSubscribeHandler(
		subscribersStore, interestsStore, sesSender,
		handlers.NoSuppressions{}, store, auditLogger, cfg.BaseURL, slog.Default(),
	)

	// Admin-only interest taxonomy CRUD (#0024, PRD §5.2/§6.1): reuses the
	// same interestsStore constructed just above for the subscribe endpoint —
	// interests.Store is a stateless wrapper over the shared pool, so one
	// instance safely backs both call sites.
	adminInterestsH := handlers.NewAdminInterestsHandler(interestsStore, auditLogger)

	// SSE event broker: the in-memory pub/sub singleton reused for live
	// campaign send progress once the mailing subsystem lands.
	broker := events.NewBroker()
	eventsH := handlers.NewEventsHandler(broker)

	// Current user profile: GET /api/me returns {id, email, is_admin} read
	// straight off the RequireSession-attached context, so the Svelte SPA can
	// gate the admin view. Stateless — no data-layer dependency.
	meH := handlers.NewMeHandler()

	// requireSession guards the authenticated account-management routes; the
	// store satisfies middleware.SessionResolver via ResolveSession.
	requireSession := middleware.RequireSession(store)
	// requireAdmin composes the session guard with the admin check; admin-only
	// routes wrap their handler with requireAdmin(...). RequireSession runs
	// first (attaching the user / answering 401), then RequireAdmin (403 for a
	// non-admin).
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}

	return mountAndServe(cfg, pool,
		authH, credsH, settingsH, adminUsersH, adminAuditH, adminInterestsH, eventsH, meH, subscribeH,
		requireSession, requireAdmin, nil /* no outer middleware in production */)
}

// mailerNoOpAllowed reports whether MAILER_NOOP=true is permitted for the
// given BASE_URL: only when its host is exactly "localhost" or "127.0.0.1".
// Carried into #0026 from #0027's review — MAILER_NOOP previously had none
// of the three guards STORAGE=json already uses (explicit opt-in, a loud
// startup warning, a hard refusal on the wrong path); this supplies the
// last two. An unparseable BASE_URL is treated as disallowed rather than
// erroring here, so the caller gets one uniform "not permitted" outcome
// regardless of why.
func mailerNoOpAllowed(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1"
}

// checkMailerNoOp enforces mailerNoOpAllowed at startup. When MAILER_NOOP is
// unset it is a no-op. When set and disallowed, it returns an error so
// serve() fails clearly instead of silently disabling every outbound email
// the service sends — including #0026's confirmation and
// already-subscribed emails, which the endpoint's uniform 202 makes
// otherwise invisible to any caller. When set and allowed, it logs a single
// slog.Warn so the operator has a durable record that email is disabled,
// even though nothing about the request path can surface it.
func checkMailerNoOp(cfg *config.Config, logger *slog.Logger) error {
	if !cfg.MailerNoOp {
		return nil
	}
	if !mailerNoOpAllowed(cfg.BaseURL) {
		return fmt.Errorf("opencircuit: MAILER_NOOP=true is only permitted when BASE_URL's host is localhost or 127.0.0.1 (got %q)", cfg.BaseURL)
	}
	logger.Warn("opencircuit: MAILER_NOOP=true — outbound email is disabled; messages are logged instead of sent (refused outside localhost/127.0.0.1)")
	return nil
}

// noOpMailingMailer implements mailing.Mailer by logging the message instead
// of sending it, mirroring auth.NoOpMailer's approach but at the
// internal/mailing.Message level #0026's subscribe handler depends on
// directly. internal/mailing has no no-op implementation of its own to
// reuse here (RecordingMailer exists for tests, not for a "log to stdout in
// dev" mode), and adding one there is out of #0026's scope — this endpoint
// only calls internal/mailing, it does not modify it. Only ever constructed
// when checkMailerNoOp has already confirmed MAILER_NOOP=true is permitted.
type noOpMailingMailer struct{}

// Send logs the message instead of delivering it, returning a fake message
// ID (never "", so callers can't mistake it for a zero value indicating
// failure).
func (noOpMailingMailer) Send(_ context.Context, msg mailing.Message) (string, error) {
	log.Printf("MAILER_NOOP: email to %s: %s", msg.To, msg.Subject)
	return "noop", nil
}

// serveDevMode boots the app without PostgreSQL using the in-memory dev store.
// All handler interfaces are satisfied by *devstore.Store or its companion
// types. The Postgres connect, pgxpool, and migration paths are entirely skipped.
func serveDevMode(cfg *config.Config) error {
	log.Printf("opencircuit: STORAGE=json — starting with in-memory dev store (no Postgres)")

	ds := devstore.New(cfg.AdminEmail)

	// WebAuthn is still needed for the config but auth routes are stubbed.
	// In dev mode WebAuthnRPID/Origin may be empty; provide localhost defaults.
	if cfg.WebAuthnRPID == "" {
		cfg.WebAuthnRPID = "localhost"
	}
	if cfg.WebAuthnRPOrigin == "" {
		cfg.WebAuthnRPOrigin = fmt.Sprintf("http://localhost:%d", cfg.Port)
	}

	// Auth handler — registration/login/recovery are all stubs in dev mode.
	// Logout still works (deletes the dev session cookie).
	authH := handlers.NewAuthHandler(
		devstore.DevRegistrar{},
		devstore.NewDevLoginService(ds),
		devstore.DevRecoverer{},
		slog.Default(),
	)

	// Credential, settings, user-management, and audit handlers use the dev store.
	credsH := handlers.NewCredentialsHandler(ds, nil /* no audit in dev */)
	settingsH := handlers.NewSettingsHandler(ds, nil)
	adminUsersH := handlers.NewAdminUsersHandler(ds, nil)
	adminAuditH := handlers.NewAdminAuditHandler(ds)

	// SSE broker.
	broker := events.NewBroker()
	eventsH := handlers.NewEventsHandler(broker)

	meH := handlers.NewMeHandler()

	// Session middleware backed by the dev store.
	requireSession := middleware.RequireSession(ds)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}

	// Dev-only auto-login middleware: on every request that has no session cookie,
	// mint a dev session for the seeded mock admin and inject it into the request
	// so RequireSession accepts it immediately — no passkey ceremony needed.
	// The hard guardrail (cfg.DevMode() check) is enforced inside DevAutoLogin.
	devAutoLogin := middleware.DevAutoLogin(ds, cfg.DevMode())

	// Subscribe (#0026) has no dev-store backing yet: internal/devstore's own
	// doc comment says subscriber/interest fakes are "added incrementally as
	// those later phases land", and this pass's lane is
	// internal/subscribers, internal/handlers, internal/interests, and this
	// file — not internal/devstore. Passing nil here (mountAndServe only
	// registers POST /api/subscribe when non-nil) means STORAGE=json keeps
	// booting and serving every other route exactly as before; only the
	// subscribe endpoint is unavailable in dev mode until a devstore
	// backing lands.
	var subscribeH *handlers.SubscribeHandler

	// Admin interests CRUD (#0024) has the same devstore gap as subscribeH
	// above — internal/devstore has no interests-table backing yet. Passing
	// nil here (mountAndServe only registers /admin/interests* when
	// non-nil) leaves every other admin route working in STORAGE=json mode.
	var adminInterestsH *handlers.AdminInterestsHandler

	return mountAndServe(cfg, ds,
		authH, credsH, settingsH, adminUsersH, adminAuditH, adminInterestsH, eventsH, meH, subscribeH,
		requireSession, requireAdmin, devAutoLogin)
}

// mountAndServe registers all routes on a new ServeMux and starts listening.
// This is shared between the Postgres and dev paths to avoid code duplication.
// The `db` parameter satisfies handlers.Pinger for GET /health.
// outerMiddleware, when non-nil, wraps the entire mux as the outermost handler.
// It is used in dev mode only (serveDevMode) to apply the auto-login middleware;
// the production path always passes nil.
func mountAndServe(
	cfg *config.Config,
	pinger handlers.Pinger,
	authH *handlers.AuthHandler,
	credsH *handlers.CredentialsHandler,
	settingsH *handlers.SettingsHandler,
	adminUsersH *handlers.AdminUsersHandler,
	adminAuditH *handlers.AdminAuditHandler,
	adminInterestsH *handlers.AdminInterestsHandler,
	eventsH *handlers.EventsHandler,
	meH *handlers.MeHandler,
	subscribeH *handlers.SubscribeHandler,
	requireSession func(http.Handler) http.Handler,
	requireAdmin func(http.Handler) http.Handler,
	outerMiddleware func(http.Handler) http.Handler,
) error {
	// Per-IP rate limiters for the abuse-prone public auth endpoints.
	// Burst equals the per-window allowance so a fresh IP gets its full
	// quota immediately, then refills at the sustained rate.
	registerLimiter := middleware.NewRateLimiter(rate.Every(time.Hour/3), 3)  // 3 / hour / IP
	loginLimiter := middleware.NewRateLimiter(rate.Every(time.Minute/10), 10) // 10 / minute / IP
	recoverLimiter := middleware.NewRateLimiter(rate.Every(time.Hour/3), 3)   // 3 / hour / IP
	// #0026: "Per-IP token-bucket rate limit (5/min, burst 3)" verbatim.
	subscribeLimiter := middleware.NewRateLimiter(rate.Every(time.Minute/5), 3)

	mux := http.NewServeMux()
	mux.Handle("GET /health", handlers.NewHealthHandler(pinger))

	mux.Handle("POST /auth/register/start", registerLimiter.Middleware(http.HandlerFunc(authH.RegisterStart)))
	mux.HandleFunc("GET /auth/register/verify", authH.RegisterVerify)
	mux.HandleFunc("POST /auth/register/finish", authH.RegisterFinish)
	// The PRD lists login/start as the rate-limited login endpoint; it is
	// registered here as GET, so the 10/min limiter is attached to that route.
	mux.Handle("GET /auth/login/start", loginLimiter.Middleware(http.HandlerFunc(authH.LoginStart)))
	mux.HandleFunc("POST /auth/login/finish", authH.LoginFinish)
	mux.HandleFunc("POST /auth/logout", authH.Logout)
	// "Sign out everywhere" — session-guarded: revokes every session for the
	// authenticated account (including this one), never touches
	// passkey_credentials or users. Complements DELETE /account/credentials/{id},
	// the credential-level lever.
	mux.Handle("POST /auth/logout/all", requireSession(http.HandlerFunc(authH.LogoutAll)))
	mux.Handle("POST /auth/recover", recoverLimiter.Middleware(http.HandlerFunc(authH.RecoverStart)))
	mux.HandleFunc("GET /auth/recover/verify", authH.RecoverVerify)
	mux.HandleFunc("POST /auth/recover/finish", authH.RecoverFinish)

	// Passkey credential management — authenticated, operates only on the
	// caller's own credentials.
	mux.Handle("GET /account/credentials", requireSession(http.HandlerFunc(credsH.List)))
	mux.Handle("PATCH /account/credentials/{id}", requireSession(http.HandlerFunc(credsH.Rename)))
	mux.Handle("DELETE /account/credentials/{id}", requireSession(http.HandlerFunc(credsH.Revoke)))

	// Admin-only runtime settings: read all settings and update one. Both
	// behind RequireSession + RequireAdmin. The registration gate
	// (registrations_enabled) is read fresh from the DB on each
	// POST /auth/register/start, so a PATCH here takes effect immediately.
	mux.Handle("GET /admin/settings", requireAdmin(http.HandlerFunc(settingsH.List)))
	mux.Handle("PATCH /admin/settings", requireAdmin(http.HandlerFunc(settingsH.Patch)))

	// Admin-only user management: list users (status + last login), user
	// detail (+ passkey counts), deactivate a non-admin user (sets
	// active=false, deletes all their sessions, audits account.deactivated), and
	// reactivate (sets active=true, audits account.reactivated). All behind
	// RequireSession + RequireAdmin.
	mux.Handle("GET /admin/users", requireAdmin(http.HandlerFunc(adminUsersH.List)))
	mux.Handle("GET /admin/users/{id}", requireAdmin(http.HandlerFunc(adminUsersH.Get)))
	mux.Handle("POST /admin/users/{id}/deactivate", requireAdmin(http.HandlerFunc(adminUsersH.Deactivate)))
	mux.Handle("POST /admin/users/{id}/reactivate", requireAdmin(http.HandlerFunc(adminUsersH.Reactivate)))

	// Admin-only audit log: GET /admin/audit returns the append-only
	// audit_log newest-first, paginated via ?page=&per_page= (default 50, capped
	// at 200), with an optional ?user_id= filter scoped to one user's rows. Behind
	// RequireSession + RequireAdmin.
	mux.Handle("GET /admin/audit", requireAdmin(http.HandlerFunc(adminAuditH.List)))

	// Admin-only interest taxonomy CRUD (#0024, PRD §5.2/§6.1): list (with a
	// per-interest subscriber count), create, update (name/description/
	// sort_order/active — slug is immutable through this route), and a
	// hard-delete refused whenever any subscriber is associated. All behind
	// RequireSession + RequireAdmin. adminInterestsH is nil in dev mode
	// (STORAGE=json — see serveDevMode's comment) since internal/devstore
	// has no interests-table backing yet; the routes are only registered
	// when a real handler is wired, so dev mode boots and serves everything
	// else unaffected.
	if adminInterestsH != nil {
		mux.Handle("GET /admin/interests", requireAdmin(http.HandlerFunc(adminInterestsH.List)))
		mux.Handle("POST /admin/interests", requireAdmin(http.HandlerFunc(adminInterestsH.Create)))
		mux.Handle("PATCH /admin/interests/{id}", requireAdmin(http.HandlerFunc(adminInterestsH.Patch)))
		mux.Handle("DELETE /admin/interests/{id}", requireAdmin(http.HandlerFunc(adminInterestsH.Delete)))
	}

	// Current user profile — behind RequireSession; returns the caller's
	// {id, email, is_admin} for the SPA to gate the admin view.
	mux.Handle("GET /api/me", requireSession(http.HandlerFunc(meH.Me)))

	// SSE stream — behind RequireSession; pushes events to the authenticated
	// user's connected clients (campaign send progress, once mailing lands).
	mux.Handle("GET /api/events", requireSession(http.HandlerFunc(eventsH.Stream)))

	// Mailing-list signup (#0026) — public, unauthenticated, double opt-in.
	// Rate-limited per PRD §6.3's stated 5/min-burst-3, matching every other
	// public rate-limited endpoint's construction above. subscribeH is nil
	// in dev mode (STORAGE=json — see serveDevMode's comment) since
	// internal/devstore has no subscriber/interest backing yet; the route is
	// only registered when a real handler is wired, so dev mode boots and
	// serves everything else unaffected.
	if subscribeH != nil {
		mux.Handle("POST /api/subscribe", subscribeLimiter.Middleware(http.HandlerFunc(subscribeH.Subscribe)))
	}

	// SEO: server-injected per-route meta tags (#0019) and generated
	// sitemap.xml / robots.txt (#0020). indexHTML is the embedded, built
	// dist/index.html carrying the %%OC_*%% placeholder markers (web/index.html)
	// that seo.Site substitutes per request path -- social crawlers fetch the
	// raw HTML and never execute the SPA bundle, so the injected values have to
	// already be correct in what this handler serves. The workshop source is
	// nil until #0051 (workshops store) and #0054 (workshop detail view) land;
	// until then /workshops/{slug} and the sitemap's workshop portion both fall
	// back to their documented defaults (seo.WorkshopSource's doc comment).
	indexHTML, err := fs.ReadFile(web.DistFS(), "index.html")
	if err != nil {
		return fmt.Errorf("mountAndServe: read embedded index.html: %w", err)
	}
	site := seo.NewSite(indexHTML, cfg.BaseURL, nil)

	mux.Handle("GET /sitemap.xml", site.SitemapHandler())
	mux.Handle("GET /robots.txt", site.RobotsHandler())

	// Svelte SPA — the catch-all served LAST. Under the Go 1.22 mux this
	// "GET /" pattern is the least specific, so every explicit route above wins
	// over it. It serves hashed assets from the embedded web/dist directly and
	// falls back to index.html for any other path, so SPA deep links survive a
	// hard refresh. site.Middleware wraps it to rewrite index.html responses
	// with the per-path meta tags, preserving whichever status code
	// (200 known route / 404 miss, #0022) the SPA handler chose.
	mux.Handle("GET /", site.Middleware(handlers.NewSPAHandler(web.DistFS())))

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	log.Printf("opencircuit %s listening on %s", version, addr)

	// Apply the outer middleware (dev auto-login) when provided. This must never
	// be non-nil on the production path — servePostgres always passes nil.
	var handler http.Handler = mux
	if outerMiddleware != nil {
		handler = outerMiddleware(mux)
	}
	return http.ListenAndServe(addr, handler)
}
