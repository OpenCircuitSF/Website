package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
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
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
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

	// Mailer: stdout NoOpMailer for now. auth.SESMailer (SMTP-based) has no
	// working credential source — this project has no static AWS credentials
	// and no SES SMTP username/password in configuration (#0007) — so it
	// cannot authenticate. #0027 replaces it with the SES v2 API mailer,
	// authenticated via the EC2 instance role, and wires it up here.
	var mailer auth.Mailer = auth.NoOpMailer{BaseURL: cfg.BaseURL}

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
		authH, credsH, settingsH, adminUsersH, adminAuditH, eventsH, meH,
		requireSession, requireAdmin, nil /* no outer middleware in production */)
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

	return mountAndServe(cfg, ds,
		authH, credsH, settingsH, adminUsersH, adminAuditH, eventsH, meH,
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
	eventsH *handlers.EventsHandler,
	meH *handlers.MeHandler,
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

	// Current user profile — behind RequireSession; returns the caller's
	// {id, email, is_admin} for the SPA to gate the admin view.
	mux.Handle("GET /api/me", requireSession(http.HandlerFunc(meH.Me)))

	// SSE stream — behind RequireSession; pushes events to the authenticated
	// user's connected clients (campaign send progress, once mailing lands).
	mux.Handle("GET /api/events", requireSession(http.HandlerFunc(eventsH.Stream)))

	// Svelte SPA — the catch-all served LAST. Under the Go 1.22 mux this
	// "GET /" pattern is the least specific, so every explicit route above wins
	// over it. It serves hashed assets from the embedded web/dist directly and
	// falls back to index.html for any other path, so SPA deep links survive a
	// hard refresh.
	mux.Handle("GET /", handlers.NewSPAHandler(web.DistFS()))

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
