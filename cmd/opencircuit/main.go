package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // #0156: embed the IANA tzdata database in the binary so
	// internal/handlers' announceLocation (America/Los_Angeles,
	// admin_workshop_announce.go) can load at package init without relying
	// on the host having its own copy. A missing host tzdata is not the
	// deploy target's normal case today (Amazon Linux ships it,
	// CLAUDE.md §7), but a future scratch/distroless base would otherwise
	// panic the whole binary at startup rather than just the announce
	// path — mustLoadLocation's panic-on-missing-zone behavior is correct
	// and stays; this only removes the host as a source of that failure.

	"github.com/jackc/pgx/v5/pgxpool"
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
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/seo"
	"github.com/brennanMKE/OpenCircuitSF/internal/sesnotify"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
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

// buildSEOSite constructs the *seo.Site shared by mountAndServe's per-route
// meta-tag middleware, GET /sitemap.xml, and GET /robots.txt (#0019, #0020),
// reading the embedded, built dist/index.html (web/index.html's %%OC_*%%
// placeholder markers) once. Called from servePostgres and serveDevMode --
// not from inside mountAndServe itself -- specifically so the caller can
// also hand the identical *seo.Site to NewAdminWorkshopsHandler as its cache
// invalidator (servePostgres only; #0054, carried in from #0051's review
// Ruling 1): threading one shared instance out to both call sites is what
// makes a workshop mutation and the SEO renderer/sitemap provably read from
// the same cache, rather than leaving mountAndServe to build its own,
// second, never-invalidated Site the way it did before this issue.
// source is nil on the dev-mode path (STORAGE=json has no workshops-table
// backing -- see serveDevMode's call site).
func buildSEOSite(cfg *config.Config, source seo.WorkshopSource) (*seo.Site, error) {
	indexHTML, err := fs.ReadFile(web.DistFS(), "index.html")
	if err != nil {
		return nil, fmt.Errorf("buildSEOSite: read embedded index.html: %w", err)
	}
	return seo.NewSite(indexHTML, cfg.BaseURL, source), nil
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
	// approved data layers. suppressionsStore (#0033) is the real
	// implementation of handlers.SuppressionChecker — it replaces the
	// handlers.NoSuppressions placeholder every branch used before this
	// table existed; see internal/subscribers/suppressions.go's package doc
	// comment and handlers.SuppressionChecker's doc comment.
	subscribersStore := subscribers.NewStore(pool)
	interestsStore := interests.NewStore(pool)
	suppressionsStore := subscribers.NewSuppressionStore(pool)
	subscribeH := handlers.NewSubscribeHandler(
		subscribersStore, interestsStore,
		suppressionsStore, auditLogger, cfg.BaseURL, slog.Default(),
	)

	// sesEventsStore (internal/sesnotify, #0038) is constructed here, ahead of
	// sesNotifyH below, because adminSubscribersH (#0039) also needs it — the
	// admin subscriber detail view's soft-bounce count reads through the same
	// pool-only CountRecentSoftBouncesPool method the SES ingestion
	// handler's transaction-scoped CountRecentSoftBounces mirrors (renamed
	// from …TransientBounces by #0109, which widened the count to include
	// Undetermined bounces).
	sesEventsStore := sesnotify.NewStore(pool)

	// Admin-only interest taxonomy CRUD (#0024, PRD §5.2/§6.1): reuses the
	// same interestsStore constructed just above for the subscribe endpoint —
	// interests.Store is a stateless wrapper over the shared pool, so one
	// instance safely backs both call sites.
	adminInterestsH := handlers.NewAdminInterestsHandler(interestsStore, auditLogger)

	// Campaign store, constructed here (ahead of the rest of the campaign
	// wiring further below) so it can be handed to adminWorkshopsH just
	// below as well: #0056's announce-to-list shortcut and #0041's
	// hand-composed campaigns share the exact same CampaignStore.Create
	// call path over the exact same instance, never two independently
	// constructed ones.
	campaignsStore := mailing.NewCampaignStore(pool)

	// Workshop CRUD (#0051, PRD §5.2/§6.2/§8): admin create/list/edit/delete
	// plus the public read routes (#0053/#0054's index and detail pages).
	// workshopsStore is internal/workshops' data-access layer over
	// migration 000020's workshops/workshop_interests tables (#0050).
	//
	// #0054 wires the cache-invalidation seam #0051 deliberately deferred
	// (carried in from #0073's review, formerly a literal nil here): site is
	// built by buildSEOSite from the SAME workshopsStore, wrapped by
	// workshopSEOSource (workshop_seo_source.go), that this handler mutates
	// -- and the identical *seo.Site pointer is threaded into mountAndServe
	// below, so AdminWorkshopsHandler.invalidate() and the SEO
	// renderer/sitemap GET /workshops/{slug} and GET /sitemap.xml read from
	// are provably the same instance, not two independently-constructed
	// ones (see cmd/opencircuit/seo_wiring_test.go).
	//
	// campaignsStore (#0056) backs POST /admin/workshops/{id}/announce —
	// see internal/handlers/admin_workshop_announce.go's package doc
	// comment for why that route lives on this handler rather than its own
	// type.
	workshopsStore := workshops.NewStore(pool)
	site, err := buildSEOSite(cfg, workshopSEOSource{store: workshopsStore})
	if err != nil {
		return err
	}
	adminWorkshopsH := handlers.NewAdminWorkshopsHandler(workshopsStore, site, campaignsStore, auditLogger, cfg.BaseURL)
	publicWorkshopsH := handlers.NewPublicWorkshopsHandler(workshopsStore, interestsStore)

	// Admin subscribers screen (#0032, PRD §5.2/§6.2): list/search/detail,
	// manual suppress, clear-complaint, and manual add. manualAdd (subscribeH)
	// lets its Create dispatch through the exact same newSignup/existingSignup
	// path #0026's public endpoint uses — see
	// internal/handlers/admin_subscribers.go's package doc comment.
	adminSubscribersH := handlers.NewAdminSubscribersHandler(subscribersStore, interestsStore, subscribeH, suppressionsStore, sesEventsStore, store, auditLogger)

	// Admin suppression-list screen (#0100, PRD §5.2/§6.2): the admin
	// surface over suppressionsStore that #0033 built but never gave a
	// caller — list every row (joined to subscriber status), and reverse
	// one with a required note. A separate handler from adminSubscribersH:
	// a suppression can exist with no subscribers row at all (#0060), so it
	// is not a subscriber sub-resource — see
	// internal/handlers/admin_suppressions.go's package doc comment.
	adminSuppressionsH := handlers.NewAdminSuppressionsHandler(suppressionsStore, subscribersStore, auditLogger)

	// Admin campaign CRUD (#0041, PRD §5.2/§6.6/§8): create/list/edit
	// campaigns plus the send/cancel status transitions. preflight is nil
	// until #0045 wires in an adapter over its own mailing.Preflight — see
	// internal/handlers/admin_campaigns.go's campaignPreflightChecker doc
	// comment for why that is not a compliance gap (the send worker's own
	// copy of the gate is authoritative regardless). campaignsStore itself
	// is constructed further up, alongside workshopsStore, so
	// adminWorkshopsH's announce shortcut (#0056) and adminCampaignsH here
	// share one instance.
	//
	// audienceStore (#0044, PRD §6.6) is internal/mailing's eligibility
	// engine: the four audience predicates, the dry-run Preview behind GET
	// /admin/campaigns/{id}/audience (adminCampaignAudienceH below), and the
	// send-time re-check #0045's worker will call inside its claim
	// transaction. It is also handed to adminCampaignsH so Send can verify
	// the operator's typed confirm_count against the real audience count
	// (#0041's carried-in TODO) instead of accepting it unverified.
	audienceStore := mailing.NewAudienceStore(pool)

	// SSE event broker: the in-memory pub/sub singleton shared by GET
	// /api/events (eventsH below) and, as of #0048, the send worker's live
	// progress publisher constructed just below. Built here — ahead of
	// newSendWorkerIfEnabled — rather than alongside eventsH further down,
	// specifically so the worker can be wired to it at construction time.
	broker := events.NewBroker()
	eventsH := handlers.NewEventsHandler(broker)

	// Campaign send progress publisher (#0048, PRD §8): the broker-backed
	// implementation of mailing.ProgressPublisher that newSendWorkerIfEnabled
	// wires into WorkerDeps.Progress below, replacing the literal `nil`
	// #0045 left there. Broadcasts over the SAME broker GET /api/events
	// streams from — see internal/handlers/campaign_progress.go's package
	// doc comment for why PublishAll (every connected admin), not Publish
	// (one userID).
	campaignProgressPub := handlers.NewCampaignProgressPublisher(broker, slog.Default())

	// Send worker (#0045, PRD §6.6): the loop that turns queued email_sends
	// rows into delivered mail, and the authoritative copy of the pre-send
	// gate. sendWorker is nil when MAILER_NOOP=true (see
	// newSendWorkerIfEnabled's doc comment) — worker_wiring_test.go proves
	// every branch of that decision. sendStore is non-nil whenever
	// sendWorker would be constructible even if SEND_WORKER_ENABLED=false,
	// so the advisory preflight adapter below still works on an instance
	// that deliberately doesn't run the worker itself (CLAUDE.md §10 item
	// 4's "exactly one instance runs the worker" scaling story).
	sendStore := newSendStoreIfEnabled(cfg, pool, audienceStore, store)
	sendWorker, err := newSendWorkerIfEnabled(cfg, sendStore, audienceStore, auditLogger, sesSender, store, campaignProgressPub, slog.Default())
	if err != nil {
		return fmt.Errorf("opencircuit: constructing send worker: %w", err)
	}

	// Outbox worker (#0126, PRD §6.11): drains internal/outbox's
	// outbound_queue — confirmation/already-subscribed mail from
	// subscribeH below, and registration/recovery/sessions-revoked mail
	// from internal/auth, all durable now instead of an in-process
	// goroutine lost on a crash. See newOutboxWorker's own doc comment for
	// why this runs unconditionally (no SEND_WORKER_ENABLED-shaped gate).
	outboxWorker, err := newOutboxWorker(pool, sesSender, store, cfg, slog.Default())
	if err != nil {
		return fmt.Errorf("opencircuit: constructing outbox worker: %w", err)
	}

	adminCampaignsH := handlers.NewAdminCampaignsHandler(campaignsStore, handlers.NewCampaignPreflightChecker(sendStore), audienceStore, auditLogger)
	adminCampaignAudienceH := handlers.NewAdminCampaignAudienceHandler(audienceStore)

	// Admin campaign read-only preflight (#0047, PRD §5.2/§6.6): GET
	// .../preflight, the compose UI's dry-run evaluation of #0045's send
	// gate — same mailing.Preflight function, same SendStore.GatherPreflight
	// read, as the advisory checker adminCampaignsH.Send uses and the
	// worker's own authoritative copy. nil when sendStore is nil
	// (MAILER_NOOP=true — see newSendStoreIfEnabled): adminRoutes omits its
	// one route in that case, mirroring adminCampaignsH's own preflight
	// nil-tolerance.
	var adminCampaignPreflightH *handlers.AdminCampaignPreflightHandler
	if sendStore != nil {
		adminCampaignPreflightH = handlers.NewAdminCampaignPreflightHandler(sendStore, campaignsStore, store, cfg.EmailFrom)
	}

	// Admin campaign stats (#0049, PRD §11): GET .../stats, the per-campaign
	// outcome screen (counts by send status, bounce/complaint counts
	// reconciled from email_events, failed sends with error messages).
	// Deliberately NOT gated on sendStore/MAILER_NOOP like
	// adminCampaignPreflightH above — a stats read has no dependency on
	// whether THIS instance can send (see
	// internal/mailing/campaign_stats.go's package doc comment) — so it is
	// always constructed alongside campaignsStore whenever Postgres is in
	// use.
	campaignStatsStore := mailing.NewCampaignStatsStore(pool)
	adminCampaignStatsH := handlers.NewAdminCampaignStatsHandler(campaignStatsStore, campaignsStore)

	// Admin overview dashboard (#0061, PRD §5.2): GET /admin/overview, the
	// /admin landing screen. Reuses subscribersStore, interestsStore,
	// campaignsStore, campaignStatsStore, and store (settings) constructed
	// above rather than building any new store — see
	// internal/handlers/admin_dashboard.go's package doc comment for why
	// every figure it reads is already a single aggregate query on an
	// existing store. cfg.SESSandbox is the manually-set operational flag
	// that field's own doc comment (internal/config/config.go) explains.
	adminDashboardH := handlers.NewAdminDashboardHandler(
		subscribersStore, interestsStore, campaignsStore, campaignStatsStore, store, cfg.SESSandbox,
	)

	// Admin campaign preview and test send (#0046, PRD §5.2/§6.6): POST
	// .../preview (render without sending) and POST .../test (a real send
	// to the requesting admin's own address, through a dedicated synthetic
	// subscriber row — see internal/handlers/admin_campaign_preview.go's
	// package doc comment). subscribersStore, store (mailing.SettingsReader
	// for physical_address), and sesSender are all already constructed
	// above for the subscribe/send-worker paths; reused here rather than
	// building a second copy of any of them. sesSender is the SAME
	// mailing.Mailer the send worker uses (including under MAILER_NOOP=true,
	// where it is the log-only noOpMailingMailer) — never nil in
	// production, so a test send behaves exactly like every other outbound
	// path in this project under MAILER_NOOP.
	adminCampaignPreviewH := handlers.NewAdminCampaignPreviewHandler(
		campaignsStore, subscribersStore, store, mailing.MarkdownCampaignRenderer{}, sesSender, auditLogger,
		cfg.BaseURL, cfg.EmailListDomain, cfg.EmailFrom,
	)

	// Public interests read (#0029, PRD §6.1/§7.3): GET /api/interests, the
	// checkbox-grid source for SubscribeForm.svelte and PreferenceCenter.svelte.
	// Deliberately calls ListActive (never ListAll, which is admin-only via
	// adminInterestsH above) — see public_interests.go's package doc comment.
	publicInterestsH := handlers.NewPublicInterestsHandler(interestsStore)

	// Preference center (#0031, PRD §6.4): GET/PATCH /api/preferences,
	// token-authenticated by manage_token. See preferences.go's package doc
	// comment for why "Unsubscribe from everything" rides the same PATCH
	// route instead of a new one.
	preferencesH := handlers.NewPreferencesHandler(subscribersStore, interestsStore, auditLogger, nil)

	// Confirmation (#0030, PRD §6.3): POST /api/subscribe/confirm completes
	// double opt-in. A new handler/file rather than an addition to
	// subscribe.go — see confirm.go's package doc comment.
	confirmH := handlers.NewConfirmHandler(subscribersStore, interestsStore, auditLogger, slog.Default())

	// One-click unsubscribe (#0034, PRD §6.5 path 1, RFC 8058): GET/POST
	// /api/unsubscribe. Reuses subscribersStore (its FindByManageToken,
	// Unsubscribe, and RotateManageToken) — no new store construction
	// needed. See unsubscribe.go's package doc comment for why this route
	// carries no session/CSRF guard and why GET/POST behave so differently.
	unsubscribeH := handlers.NewUnsubscribeHandler(subscribersStore, auditLogger, nil, slog.Default())

	// SES bounce/complaint event ingestion (#0038, PRD §6.7): POST
	// /api/ses/notifications, an SNS HTTPS subscription endpoint.
	// sesVerifier (internal/sesnotify, #0037) is the trust boundary —
	// signature + TopicArn allowlist — checked before anything in the body
	// is acted on. sesEventsStore (constructed above, alongside
	// adminSubscribersH) backs the raw email_events log; the handler also
	// reuses subscribersStore/suppressionsStore constructed above so its
	// per-message transaction (pool.Begin, passed directly: see
	// ses_notifications.go's package doc comment for why these three
	// stores are held concretely rather than behind narrow interfaces)
	// writes the event row, the suppression row, and the subscriber status
	// change atomically — #0033's hard block. store (internal/auth) backs
	// #0039's soft-bounce threshold/window settings — a genuine narrow
	// interface, unlike the three stores above (see soft_bounce.go).
	sesVerifier := sesnotify.NewVerifier(cfg.AWSRegion, cfg.SESEventsTopicARN, slog.Default())
	sesNotifyH := handlers.NewSESNotificationsHandler(
		sesVerifier, pool, sesEventsStore, subscribersStore, suppressionsStore, store, auditLogger, slog.Default(),
	)

	// broker/eventsH (GET /api/events) were constructed earlier, alongside
	// the send worker's campaign progress publisher — see that comment for
	// why.

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
		authH, credsH, settingsH, adminUsersH, adminAuditH, adminInterestsH, adminSubscribersH, adminSuppressionsH, adminCampaignsH, adminCampaignAudienceH, adminCampaignPreviewH, adminCampaignPreflightH, adminCampaignStatsH, adminWorkshopsH, adminDashboardH, eventsH, meH, subscribeH,
		publicInterestsH, preferencesH, confirmH, unsubscribeH, publicWorkshopsH, sesNotifyH, sendWorker, outboxWorker, site,
		requireSession, requireAdmin, nil, /* no outer middleware in production */
		nil /* ready: only the wiring tests observe listener readiness directly */)
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

// newSendStoreIfEnabled constructs the send worker's own data-access layer
// (#0045) whenever outbound email is genuinely configured. It exists as its
// own function — rather than inlined in servePostgres — so
// worker_wiring_test.go can exercise this decision directly, and so
// adminCampaignsH's preflight adapter (handlers.NewCampaignPreflightChecker)
// can be wired from the same *mailing.SendStore the worker itself uses,
// which is what makes the advisory HTTP check and the worker's own
// authoritative check gather PreflightInput identically.
//
// Returns nil under MAILER_NOOP=true, matching mailing.NewWorker's own
// refusal — see newSendWorkerIfEnabled below for why: nothing about
// SEND_WORKER_ENABLED affects this decision, because the advisory preflight
// checker is useful even on an instance that (per CLAUDE.md §10 item 4's
// scaling story) does not itself run the worker.
func newSendStoreIfEnabled(cfg *config.Config, pool *pgxpool.Pool, audienceStore *mailing.AudienceStore, settings mailing.SettingsReader) *mailing.SendStore {
	if cfg.MailerNoOp {
		return nil
	}
	return mailing.NewSendStore(pool, audienceStore, settings, mailing.MarkdownCampaignRenderer{}, cfg.BaseURL, cfg.EmailListDomain, cfg.EmailReplyTo)
}

// newSendWorkerIfEnabled constructs the send worker, or returns (nil, nil)
// when it must not run on THIS instance:
//
//   - MAILER_NOOP=true: refused, matching mailing.NewWorker's own doc
//     comment — noOpMailingMailer.Send returns the literal message id
//     "noop", and writing that into email_sends.ses_message_id would poison
//     #0038's bounce/complaint join key and #0049's stats with rows that
//     claim to have been delivered.
//   - SEND_WORKER_ENABLED=false: the scaling knob #0045's plan §1
//     describes — if the site is ever scaled to two instances, exactly one
//     of them sets this true.
//
// A single slog.Warn records either refusal, since nothing about the
// request path can otherwise surface "campaigns will not send" to an
// operator.
func newSendWorkerIfEnabled(cfg *config.Config, sendStore *mailing.SendStore, audienceStore *mailing.AudienceStore, auditLogger *audit.Logger, mailer mailing.Mailer, settings mailing.SettingsReader, progress mailing.ProgressPublisher, log *slog.Logger) (*mailing.Worker, error) {
	if cfg.MailerNoOp {
		log.Warn("opencircuit: MAILER_NOOP=true — the send worker will not start; scheduled campaigns will not send")
		return nil, nil
	}
	if !cfg.SendWorkerEnabled {
		log.Info("opencircuit: SEND_WORKER_ENABLED=false — this instance will not run the send worker")
		return nil, nil
	}
	if sendStore == nil {
		// Unreachable in production (sendStore is nil only under
		// MAILER_NOOP, already handled above) — defensive rather than a
		// nil-pointer panic inside mailing.NewWorker.
		return nil, fmt.Errorf("opencircuit: send worker enabled but SendStore was not constructed")
	}
	return mailing.NewWorker(mailing.WorkerDeps{
		Store:          sendStore,
		Audience:       audienceStore,
		Audit:          auditLogger,
		Mailer:         mailer,
		Render:         mailing.MarkdownCampaignRenderer{},
		Settings:       settings,
		Progress:       progress, // #0048: the broker-backed implementation, or nil (matching every call site's nil-tolerance) in tests that pass none
		BaseURL:        cfg.BaseURL,
		ListDomain:     cfg.EmailListDomain,
		FromAddr:       cfg.EmailFrom,
		ReplyTo:        cfg.EmailReplyTo,
		EnvMaxSendRate: cfg.MaxSendRate,
		BatchSize:      cfg.SendBatchSize,
		Log:            log,
	})
}

// newOutboxWorker constructs #0126's transactional-mail worker
// (internal/mailing.OutboxWorker), draining internal/outbox. Unlike
// newSendWorkerIfEnabled (the campaign worker), this always runs whenever
// Postgres is in use — there is no SEND_WORKER_ENABLED-shaped scaling knob
// here, deliberately: transactional mail (signup confirmations, password
// recovery) is core functionality with no per-instance drain-order concern
// the way one campaign's SKIP LOCKED claim has, so every instance running
// it is the correct topology, not something that needs exactly one
// instance opted in. mailer is sesSender — the SAME mailing.Mailer the
// campaign send worker and #0046's test-send handler use, including under
// MAILER_NOOP=true (the no-op logger), so transactional mail behaves like
// every other outbound path in this project under that flag.
func newOutboxWorker(pool *pgxpool.Pool, mailer mailing.Mailer, settings mailing.SettingsReader, cfg *config.Config, log *slog.Logger) (*mailing.OutboxWorker, error) {
	return mailing.NewOutboxWorker(mailing.OutboxWorkerDeps{
		Store:          outbox.NewStore(pool),
		Events:         subscribers.NewStore(pool),
		Mailer:         mailer,
		Settings:       settings,
		BaseURL:        cfg.BaseURL,
		EnvMaxSendRate: cfg.MaxSendRate,
		Log:            log,
	})
}

// serveDevMode boots the app without PostgreSQL using the in-memory dev store.
// All handler interfaces are satisfied by *devstore.Store or its companion
// types. The Postgres connect, pgxpool, and migration paths are entirely skipped.
func serveDevMode(cfg *config.Config) error {
	log.Printf("opencircuit: STORAGE=json — starting with in-memory dev store (no Postgres)")

	ds := devstore.New(cfg.AdminEmail)

	// SEO site (#0019/#0020, threaded through #0054): dev mode has no
	// workshopsStore to adapt (internal/devstore has no workshops-table
	// backing -- see adminWorkshopsH/publicWorkshopsH's own nil comments
	// below), so the WorkshopSource stays nil here, same as before #0054 --
	// /workshops/{slug} and the sitemap's workshop portion fall back to
	// their documented generic defaults (seo.WorkshopSource's doc comment).
	site, err := buildSEOSite(cfg, nil)
	if err != nil {
		return err
	}

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

	// Admin subscribers screen (#0032) has the same devstore gap as
	// adminInterestsH/subscribeH above — internal/devstore has no
	// subscribers-table backing, and this handler's manual-add path needs a
	// real subscribeH anyway (nil here). Passing nil leaves every other admin
	// route working in STORAGE=json mode; adminRoutes omits its four routes
	// when nil, mirroring adminInterestsH's own nil-guard.
	var adminSubscribersH *handlers.AdminSubscribersHandler

	// Admin suppressions screen (#0100) has the same devstore gap as
	// adminSubscribersH above. Passing nil leaves every other admin route
	// working in STORAGE=json mode; adminRoutes omits its two routes when
	// nil, mirroring adminSubscribersH's own nil-guard.
	var adminSuppressionsH *handlers.AdminSuppressionsHandler

	// Admin campaign CRUD (#0041) has the same devstore gap as
	// adminSuppressionsH above. Passing nil leaves every other admin route
	// working in STORAGE=json mode; adminRoutes omits its six routes when
	// nil, mirroring adminSuppressionsH's own nil-guard.
	var adminCampaignsH *handlers.AdminCampaignsHandler

	// Admin campaign audience preview (#0044) has the same devstore gap as
	// adminCampaignsH above — internal/devstore has no subscribers/
	// suppressions/email_campaigns backing. Passing nil leaves every other
	// admin route working in STORAGE=json mode; adminRoutes omits its one
	// route when nil, mirroring adminCampaignsH's own nil-guard.
	var adminCampaignAudienceH *handlers.AdminCampaignAudienceHandler

	// Admin campaign preview and test send (#0046) has the same devstore
	// gap as adminCampaignAudienceH above — internal/devstore has no
	// subscribers/email_campaigns backing. Passing nil leaves every other
	// admin route working in STORAGE=json mode; adminRoutes omits its two
	// routes when nil, mirroring adminCampaignAudienceH's own nil-guard.
	var adminCampaignPreviewH *handlers.AdminCampaignPreviewHandler

	// Admin campaign read-only preflight (#0047) has the same devstore gap
	// as adminCampaignPreviewH above. Passing nil leaves every other admin
	// route working in STORAGE=json mode; adminRoutes omits its one route
	// when nil, mirroring adminCampaignPreviewH's own nil-guard.
	var adminCampaignPreflightH *handlers.AdminCampaignPreflightHandler

	// Admin campaign stats (#0049) has the same devstore gap as
	// adminCampaignPreflightH above. Passing nil leaves every other admin
	// route working in STORAGE=json mode; adminRoutes omits its one route
	// when nil, mirroring adminCampaignPreflightH's own nil-guard.
	var adminCampaignStatsH *handlers.AdminCampaignStatsHandler

	// Workshop CRUD (#0051) has the same devstore gap as adminCampaignStatsH
	// above -- internal/devstore has no workshops/workshop_interests
	// backing. Passing nil leaves every other route working in STORAGE=json
	// mode; adminRoutes omits its four routes when nil, and mountAndServe
	// only registers GET /api/workshops and GET /api/workshops/{slug} when
	// publicWorkshopsH is non-nil, mirroring publicInterestsH's own
	// nil-guard.
	var adminWorkshopsH *handlers.AdminWorkshopsHandler
	var publicWorkshopsH *handlers.PublicWorkshopsHandler

	// Admin overview dashboard (#0061) has the same devstore gap as
	// adminCampaignStatsH/adminWorkshopsH above -- it reads subscribers,
	// interests, campaigns, and campaign stats, none of which
	// internal/devstore backs. Passing nil leaves every other admin route
	// working in STORAGE=json mode; adminRoutes omits its one route when
	// nil.
	var adminDashboardH *handlers.AdminDashboardHandler

	// Public interests (#0029), confirm (#0030), preferences (#0031), and
	// one-click unsubscribe (#0034) all have the same devstore gap as
	// subscribeH/adminInterestsH above -- internal/devstore has no
	// interests/subscribers-table backing yet. Passing nil leaves every
	// other route working in STORAGE=json mode; mountAndServe only
	// registers GET /api/interests, POST /api/subscribe/confirm, GET/PATCH
	// /api/preferences, and GET/POST /api/unsubscribe when their handler is
	// non-nil, mirroring subscribeH's own nil-guard.
	var publicInterestsH *handlers.PublicInterestsHandler
	var preferencesH *handlers.PreferencesHandler
	var confirmH *handlers.ConfirmHandler
	var unsubscribeH *handlers.UnsubscribeHandler

	// SES event ingestion (#0038) has the same devstore gap as the handlers
	// above -- internal/devstore has no email_events/subscribers/
	// suppressions backing. Passing nil leaves every other route working in
	// STORAGE=json mode; mountAndServe only registers POST
	// /api/ses/notifications when non-nil.
	var sesNotifyH *handlers.SESNotificationsHandler

	// Send worker (#0045) has the same devstore gap as sesNotifyH above —
	// internal/devstore has no email_campaigns/email_sends backing, and the
	// worker is Postgres-only by design (mailing.NewWorker's own doc
	// comment). nil here means mountAndServe's shutdown sequence simply
	// skips the worker-stop step, mirroring every other nil-guarded
	// dev-mode gap.
	var sendWorker *mailing.Worker
	// Outbox worker (#0126) has the same devstore gap: no
	// subscribers/outbound_queue backing under STORAGE=json (see
	// internal/handlers/subscribe.go's STORAGE=json note, CLAUDE.md §5).
	var outboxWorker *mailing.OutboxWorker

	return mountAndServe(cfg, ds,
		authH, credsH, settingsH, adminUsersH, adminAuditH, adminInterestsH, adminSubscribersH, adminSuppressionsH, adminCampaignsH, adminCampaignAudienceH, adminCampaignPreviewH, adminCampaignPreflightH, adminCampaignStatsH, adminWorkshopsH, adminDashboardH, eventsH, meH, subscribeH,
		publicInterestsH, preferencesH, confirmH, unsubscribeH, publicWorkshopsH, sesNotifyH, sendWorker, outboxWorker, site,
		requireSession, requireAdmin, devAutoLogin,
		nil /* ready: only the wiring tests observe listener readiness directly */)
}

// adminRoute pairs an HTTP method and a Go 1.22 mux path pattern with the
// handler that serves it.
type adminRoute struct {
	method  string
	path    string
	handler http.Handler
}

// adminRoutes is the single, authoritative list of every admin-only route
// (session + is_admin required, PRD §5.2) that mountAndServe registers.
// Both mountAndServe (production wiring) and
// TestMountAndServe_AdminRoutesRequireSessionAndAdmin
// (cmd/opencircuit/admin_wiring_test.go, the real-mux authorization proof)
// call this exact function, so the test enumerates the routes actually
// registered rather than keeping its own hand-written list that could drift
// out of sync with what mountAndServe wires up. A route added here is
// automatically exercised — and its guard automatically proven — by that
// test with no edit to the test itself required.
//
// adminInterestsH, adminSubscribersH, adminSuppressionsH, adminCampaignsH,
// adminCampaignAudienceH, adminCampaignPreviewH, adminCampaignPreflightH,
// adminCampaignStatsH, adminWorkshopsH, and adminDashboardH may be nil (dev
// mode / STORAGE=json has no interests/subscribers-table backing yet — see
// mountAndServe's comment on the call site); their routes are simply
// omitted, mirroring mountAndServe's own former nil guard.
func adminRoutes(
	settingsH *handlers.SettingsHandler,
	adminUsersH *handlers.AdminUsersHandler,
	adminAuditH *handlers.AdminAuditHandler,
	adminInterestsH *handlers.AdminInterestsHandler,
	adminSubscribersH *handlers.AdminSubscribersHandler,
	adminSuppressionsH *handlers.AdminSuppressionsHandler,
	adminCampaignsH *handlers.AdminCampaignsHandler,
	adminCampaignAudienceH *handlers.AdminCampaignAudienceHandler,
	adminCampaignPreviewH *handlers.AdminCampaignPreviewHandler,
	adminCampaignPreflightH *handlers.AdminCampaignPreflightHandler,
	adminCampaignStatsH *handlers.AdminCampaignStatsHandler,
	adminWorkshopsH *handlers.AdminWorkshopsHandler,
	adminDashboardH *handlers.AdminDashboardHandler,
) []adminRoute {
	routes := []adminRoute{
		{http.MethodGet, "/admin/settings", http.HandlerFunc(settingsH.List)},
		{http.MethodPatch, "/admin/settings", http.HandlerFunc(settingsH.Patch)},
		{http.MethodGet, "/admin/users", http.HandlerFunc(adminUsersH.List)},
		{http.MethodGet, "/admin/users/{id}", http.HandlerFunc(adminUsersH.Get)},
		{http.MethodPost, "/admin/users/{id}/deactivate", http.HandlerFunc(adminUsersH.Deactivate)},
		{http.MethodPost, "/admin/users/{id}/reactivate", http.HandlerFunc(adminUsersH.Reactivate)},
		{http.MethodGet, "/admin/audit", http.HandlerFunc(adminAuditH.List)},
	}
	if adminInterestsH != nil {
		routes = append(routes,
			adminRoute{http.MethodGet, "/admin/interests", http.HandlerFunc(adminInterestsH.List)},
			adminRoute{http.MethodPost, "/admin/interests", http.HandlerFunc(adminInterestsH.Create)},
			adminRoute{http.MethodPatch, "/admin/interests/{id}", http.HandlerFunc(adminInterestsH.Patch)},
			adminRoute{http.MethodDelete, "/admin/interests/{id}", http.HandlerFunc(adminInterestsH.Delete)},
		)
	}
	if adminSubscribersH != nil {
		routes = append(routes,
			adminRoute{http.MethodGet, "/admin/subscribers", http.HandlerFunc(adminSubscribersH.List)},
			adminRoute{http.MethodPost, "/admin/subscribers", http.HandlerFunc(adminSubscribersH.Create)},
			adminRoute{http.MethodGet, "/admin/subscribers/{id}", http.HandlerFunc(adminSubscribersH.Get)},
			adminRoute{http.MethodPost, "/admin/subscribers/{id}/suppress", http.HandlerFunc(adminSubscribersH.Suppress)},
			adminRoute{http.MethodPost, "/admin/subscribers/{id}/clear-complaint", http.HandlerFunc(adminSubscribersH.ClearComplaint)},
			// #0059: streamed CSV export. Registered ahead of
			// /admin/subscribers/{id} in file order only for readability —
			// Go 1.22+'s pattern router already prefers the more specific
			// literal segment ("export") over the wildcard ({id}) regardless
			// of registration order, so there is no risk of this being
			// shadowed by the id route or of "export" being parsed as an id.
			adminRoute{http.MethodGet, "/admin/subscribers/export", http.HandlerFunc(adminSubscribersH.Export)},
			// #0060: GDPR/CCPA erasure — hard delete + permanent suppression.
			// Registered on the same {id} literal-vs-wildcard footing as
			// export above; Go 1.22+'s pattern router dispatches by method
			// first, so DELETE /admin/subscribers/{id} coexists safely with
			// GET /admin/subscribers/{id} on the identical path pattern.
			adminRoute{http.MethodDelete, "/admin/subscribers/{id}", http.HandlerFunc(adminSubscribersH.Erase)},
		)
	}
	if adminSuppressionsH != nil {
		routes = append(routes,
			adminRoute{http.MethodGet, "/admin/suppressions", http.HandlerFunc(adminSuppressionsH.List)},
			adminRoute{http.MethodPost, "/admin/suppressions/remove", http.HandlerFunc(adminSuppressionsH.Remove)},
		)
	}
	if adminCampaignsH != nil {
		routes = append(routes,
			adminRoute{http.MethodGet, "/admin/campaigns", http.HandlerFunc(adminCampaignsH.List)},
			adminRoute{http.MethodPost, "/admin/campaigns", http.HandlerFunc(adminCampaignsH.Create)},
			adminRoute{http.MethodGet, "/admin/campaigns/{id}", http.HandlerFunc(adminCampaignsH.Get)},
			adminRoute{http.MethodPatch, "/admin/campaigns/{id}", http.HandlerFunc(adminCampaignsH.Patch)},
			adminRoute{http.MethodPost, "/admin/campaigns/{id}/send", http.HandlerFunc(adminCampaignsH.Send)},
			adminRoute{http.MethodPost, "/admin/campaigns/{id}/cancel", http.HandlerFunc(adminCampaignsH.Cancel)},
		)
	}
	if adminCampaignAudienceH != nil {
		routes = append(routes,
			adminRoute{http.MethodGet, "/admin/campaigns/{id}/audience", http.HandlerFunc(adminCampaignAudienceH.Preview)},
		)
	}
	if adminCampaignPreviewH != nil {
		routes = append(routes,
			adminRoute{http.MethodPost, "/admin/campaigns/{id}/preview", http.HandlerFunc(adminCampaignPreviewH.Preview)},
			adminRoute{http.MethodPost, "/admin/campaigns/{id}/test", http.HandlerFunc(adminCampaignPreviewH.Test)},
		)
	}
	if adminCampaignPreflightH != nil {
		routes = append(routes,
			adminRoute{http.MethodGet, "/admin/campaigns/{id}/preflight", http.HandlerFunc(adminCampaignPreflightH.Preflight)},
		)
	}
	if adminCampaignStatsH != nil {
		routes = append(routes,
			adminRoute{http.MethodGet, "/admin/campaigns/{id}/stats", http.HandlerFunc(adminCampaignStatsH.Stats)},
		)
	}
	if adminWorkshopsH != nil {
		routes = append(routes,
			adminRoute{http.MethodGet, "/admin/workshops", http.HandlerFunc(adminWorkshopsH.List)},
			adminRoute{http.MethodPost, "/admin/workshops", http.HandlerFunc(adminWorkshopsH.Create)},
			adminRoute{http.MethodGet, "/admin/workshops/{id}", http.HandlerFunc(adminWorkshopsH.Get)},
			adminRoute{http.MethodPatch, "/admin/workshops/{id}", http.HandlerFunc(adminWorkshopsH.Patch)},
			adminRoute{http.MethodDelete, "/admin/workshops/{id}", http.HandlerFunc(adminWorkshopsH.Delete)},
			// #0056: the announce-to-list shortcut. Lives on
			// AdminWorkshopsHandler itself (see
			// internal/handlers/admin_workshop_announce.go), so it needs no
			// new parameter here or on mountAndServe -- just this one
			// registration line inside the existing nil guard above.
			adminRoute{http.MethodPost, "/admin/workshops/{id}/announce", http.HandlerFunc(adminWorkshopsH.Announce)},
			// #0136: the server-side body preview, replacing
			// web/src/lib/linkSafety.ts's client-side renderer (renamed from
			// markdown.ts by #0157). Lives on
			// AdminWorkshopsHandler itself, same as Announce above -- no new
			// parameter here or on mountAndServe.
			adminRoute{http.MethodPost, "/admin/workshops/{id}/preview", http.HandlerFunc(adminWorkshopsH.Preview)},
		)
	}
	if adminDashboardH != nil {
		routes = append(routes,
			// #0061: the /admin landing screen's single data call.
			adminRoute{http.MethodGet, "/admin/overview", http.HandlerFunc(adminDashboardH.Overview)},
		)
	}
	return routes
}

// mountAndServe registers all routes on a new ServeMux and starts listening.
// This is shared between the Postgres and dev paths to avoid code duplication.
// The `db` parameter satisfies handlers.Pinger for GET /health.
// outerMiddleware, when non-nil, wraps the entire mux as the outermost handler.
// It is used in dev mode only (serveDevMode) to apply the auto-login middleware;
// the production path always passes nil.
// ready, when non-nil, is closed the instant the TCP listener is bound and
// accepting connections — before srv.Serve is even called (#0084). It exists
// for the wiring tests (admin_wiring_test.go and its siblings): they used to
// detect "the server is up" by polling GET /health against a fixed deadline,
// which is a real network+DB round trip and, under heavy concurrent load,
// could itself take longer than that deadline — producing a
// "context deadline exceeded" failure that had nothing to do with what the
// test was actually checking (see issues/0084.md). Closing ready needs no
// network round trip and is not sensitive to machine load, so it removes
// that timing dependency instead of just giving it a bigger number. The
// production path (servePostgres/serveDevMode) always passes nil.
func mountAndServe(
	cfg *config.Config,
	pinger handlers.Pinger,
	authH *handlers.AuthHandler,
	credsH *handlers.CredentialsHandler,
	settingsH *handlers.SettingsHandler,
	adminUsersH *handlers.AdminUsersHandler,
	adminAuditH *handlers.AdminAuditHandler,
	adminInterestsH *handlers.AdminInterestsHandler,
	adminSubscribersH *handlers.AdminSubscribersHandler,
	adminSuppressionsH *handlers.AdminSuppressionsHandler,
	adminCampaignsH *handlers.AdminCampaignsHandler,
	adminCampaignAudienceH *handlers.AdminCampaignAudienceHandler,
	adminCampaignPreviewH *handlers.AdminCampaignPreviewHandler,
	adminCampaignPreflightH *handlers.AdminCampaignPreflightHandler,
	adminCampaignStatsH *handlers.AdminCampaignStatsHandler,
	adminWorkshopsH *handlers.AdminWorkshopsHandler,
	adminDashboardH *handlers.AdminDashboardHandler,
	eventsH *handlers.EventsHandler,
	meH *handlers.MeHandler,
	subscribeH *handlers.SubscribeHandler,
	publicInterestsH *handlers.PublicInterestsHandler,
	preferencesH *handlers.PreferencesHandler,
	confirmH *handlers.ConfirmHandler,
	unsubscribeH *handlers.UnsubscribeHandler,
	publicWorkshopsH *handlers.PublicWorkshopsHandler,
	sesNotifyH *handlers.SESNotificationsHandler,
	sendWorker *mailing.Worker,
	outboxWorker *mailing.OutboxWorker,
	site *seo.Site,
	requireSession func(http.Handler) http.Handler,
	requireAdmin func(http.Handler) http.Handler,
	outerMiddleware func(http.Handler) http.Handler,
	ready chan<- struct{},
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

	// Admin-only routes: runtime settings (read all / update one — the
	// registration gate `registrations_enabled` is read fresh from the DB on
	// each POST /auth/register/start, so a PATCH here takes effect
	// immediately), user management (list, detail + passkey counts,
	// deactivate/reactivate — audits account.deactivated/reactivated), the
	// append-only audit log (paginated via ?page=&per_page=, optional
	// ?user_id= filter), and the interest taxonomy CRUD (#0024, PRD
	// §5.2/§6.1: list with a per-interest subscriber count, create, update —
	// slug is immutable through this route — and a hard-delete refused
	// whenever any subscriber is associated).
	//
	// #0024's review bounced a version of this code that registered these
	// one `mux.Handle` call at a time: the wiring test that proves every
	// admin route is actually behind RequireAdmin only drove GET requests,
	// so a POST/PATCH/DELETE route missing the guard would ship with a green
	// suite. adminRoutes below is now the single source of truth for which
	// admin routes exist and what method each uses — this loop registers
	// every one of them behind requireAdmin, and
	// TestMountAndServe_AdminRoutesRequireSessionAndAdmin
	// (admin_wiring_test.go) calls this exact function to enumerate the same
	// list for its authorization proof. A route added to adminRoutes is
	// therefore covered by that test automatically; a route added by editing
	// mountAndServe directly (bypassing adminRoutes) is the mistake this
	// structure is meant to make hard to make.
	for _, r := range adminRoutes(settingsH, adminUsersH, adminAuditH, adminInterestsH, adminSubscribersH, adminSuppressionsH, adminCampaignsH, adminCampaignAudienceH, adminCampaignPreviewH, adminCampaignPreflightH, adminCampaignStatsH, adminWorkshopsH, adminDashboardH) {
		mux.Handle(r.method+" "+r.path, requireAdmin(r.handler))
	}

	// Current user profile — behind RequireSession; returns the caller's
	// {id, email, is_admin} for the SPA to gate the admin view.
	mux.Handle("GET /api/me", requireSession(http.HandlerFunc(meH.Me)))

	// SSE stream (#0048, PRD §8) — behind requireAdmin (session + is_admin),
	// not just requireSession: its one real consumer, live campaign send
	// progress, is admin data (CLAUDE.md §9's "admin data" framing), and
	// EventSource cannot set a custom Authorization header, so the existing
	// session COOKIE is what carries auth here — requireAdmin composes
	// RequireSession then RequireAdmin over that same cookie, exactly as it
	// does for every /admin/* route, so no separate auth mechanism is
	// needed for a stream a plain <script> tag can't attach a header to.
	//
	// Deliberately NOT registered through adminRoutes()/the admin loop
	// below, even though it is now admin-gated: adminRoutes and
	// TestAdminRoutesRegisteredOnlyViaTable (admin_routes_ast_test.go) are
	// scoped to /admin/*, on purpose (see that test's own doc comment) —
	// every /api/* route, including this one, carries its own guard
	// written directly at its mux.Handle call site instead, and
	// events_wiring_test.go proves this one requires session+admin at the
	// real mux (401/403/200), mirroring TestMountAndServe_
	// AdminRoutesRequireSessionAndAdmin's own proof shape for the table.
	mux.Handle("GET /api/events", requireAdmin(http.HandlerFunc(eventsH.Stream)))

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

	// Public interest taxonomy (#0029) — no auth, no rate limit: a read-only
	// GET over a taxonomy capped at a handful of admin-created rows, the
	// same trust level as GET /sitemap.xml below.
	if publicInterestsH != nil {
		mux.Handle("GET /api/interests", http.HandlerFunc(publicInterestsH.List))
	}

	// Public workshop read routes (#0051, PRD §6.2/§7.3/§8) — no auth, no
	// rate limit, same trust level as the interests read above: a read-only
	// GET filtered to already-published (or, for the detail route,
	// published/canceled) rows. publicWorkshopsH is nil in dev mode
	// (STORAGE=json — internal/devstore has no workshops-table backing yet,
	// see serveDevMode's comment).
	if publicWorkshopsH != nil {
		mux.Handle("GET /api/workshops", http.HandlerFunc(publicWorkshopsH.List))
		mux.Handle("GET /api/workshops/{slug}", http.HandlerFunc(publicWorkshopsH.GetBySlug))
	}

	// Preference center (#0031) and confirmation (#0030) — public,
	// unauthenticated, token-gated by the secret in the request itself
	// rather than a session. Rate-limited at the same scale as
	// subscribeLimiter above: these accept a caller-supplied token and,
	// while a 32-byte random value isn't brute-forceable at any rate this
	// limiter would need to stop, matching the existing public-endpoint
	// convention costs nothing and blunts naive scripted retries.
	preferencesLimiter := middleware.NewRateLimiter(rate.Every(time.Minute/10), 5)
	if preferencesH != nil {
		mux.Handle("GET /api/preferences", preferencesLimiter.Middleware(http.HandlerFunc(preferencesH.Get)))
		mux.Handle("PATCH /api/preferences", preferencesLimiter.Middleware(http.HandlerFunc(preferencesH.Patch)))
	}
	confirmLimiter := middleware.NewRateLimiter(rate.Every(time.Minute/5), 3)
	if confirmH != nil {
		mux.Handle("POST /api/subscribe/confirm", confirmLimiter.Middleware(http.HandlerFunc(confirmH.Confirm)))
	}

	// One-click unsubscribe (#0034, PRD §6.5 path 1, RFC 8058) — public,
	// unauthenticated, no CSRF (see unsubscribe.go's package doc comment for
	// why).
	//
	// #0103: POST /api/unsubscribe carries NO rate limiter at all — this is
	// the one status code PRD §6.5 says is worse than useless here ("a
	// provider seeing errors on the unsubscribe endpoint downgrades sender
	// reputation"), and RateLimiter.Middleware's refusal path answers 429,
	// not the handler's neutral 200. Gmail's and Yahoo's one-click infra
	// buckets essentially all of a domain's unsubscribe traffic onto a small
	// set of shared egress IPs (per #0077's X-Forwarded-For trust model), so
	// a per-IP limiter here doesn't throttle one abusive caller — it
	// eventually 429s a real subscriber's unsubscribe request because OTHER
	// people's one-click hits already spent that IP's bucket. Between "drop
	// the limiter" and "make the refusal path answer the same neutral 200
	// instead," dropping it is simpler and the same neutral response is
	// already every OTHER path's guarantee, not a new invariant to maintain
	// here.
	//
	// Is unlimited POST /api/unsubscribe a real DoS surface? No case is
	// made: Post does exactly one indexed SELECT on a random 32-byte token
	// (subscribers.Store.FindByManageToken, a btree lookup on manage_token,
	// which is UNIQUE per migrations/000010) and, on a miss — the outcome
	// for any request that isn't a legitimate footer/header link — nothing
	// else at all. That is cheaper than GET /api/interests or GET
	// /sitemap.xml below, neither of which is rate-limited either.
	//
	// GET /api/unsubscribe keeps unsubscribeLimiter (an ordinary,
	// 429-capable per-IP limiter). That is a deliberate, different choice
	// from POST: GET never touches the store (see unsubscribe.go) and
	// exists only to redirect a click or a scanner's prefetch to the SPA's
	// /unsubscribe view, so a 429 there just means the prefetch doesn't
	// happen — harmless, unlike a 429 on the actual RFC 8058 action.
	unsubscribeLimiter := middleware.NewRateLimiter(rate.Every(time.Minute/10), 5)
	if unsubscribeH != nil {
		mux.Handle("GET /api/unsubscribe", unsubscribeLimiter.Middleware(http.HandlerFunc(unsubscribeH.Get)))
		mux.Handle("POST /api/unsubscribe", http.HandlerFunc(unsubscribeH.Post))
	}

	// SES bounce/complaint event ingestion (#0038, PRD §6.7) — an SNS HTTPS
	// subscription endpoint, public by design. It carries NEITHER
	// requireSession/requireAdmin NOR a rate limiter, both deliberately:
	//
	//   - No session guard: SNS posts with no bearer credential of any
	//     kind — the signature internal/sesnotify.Verifier checks (plus its
	//     TopicArn allowlist) IS the authentication. Wrapping this in
	//     requireSession/requireAdmin would 403 every legitimate SNS
	//     delivery, and SNS backs off and eventually disables the
	//     subscription after enough failures — bounces and complaints stop
	//     arriving and nothing surfaces an error.
	//   - No rate limiter: every other public route above has one, but a
	//     single campaign send can produce thousands of bounce/complaint/
	//     delivery events in a burst, from many different SNS source IPs. A
	//     per-IP token bucket here would reject exactly the traffic this
	//     endpoint exists to receive. Cost is bounded instead by
	//     http.MaxBytesReader (512 KiB, ses_notifications.go) and by
	//     sesnotify.Verifier's certificate cache — the absence of a limiter
	//     here is deliberate, not an oversight.
	//
	// sesNotifyH is nil in dev mode (STORAGE=json — internal/devstore has no
	// email_events/subscribers/suppressions backing); the route is only
	// registered when a real handler is wired, matching every other
	// nil-guarded route above.
	if sesNotifyH != nil {
		mux.Handle("POST /api/ses/notifications", http.HandlerFunc(sesNotifyH.Notify))
	}

	// SEO: server-injected per-route meta tags (#0019) and generated
	// sitemap.xml / robots.txt (#0020). site is built by buildSEOSite in
	// servePostgres/serveDevMode (not here) and passed in, deliberately: on
	// the Postgres path it is the SAME *seo.Site instance already handed to
	// AdminWorkshopsHandler as its cache invalidator (#0054, carried in
	// from #0051's review Ruling 1), so a workshop mutation and the
	// GET /workshops/{slug} / GET /sitemap.xml routes mounted below read
	// from one shared cache rather than two independently-constructed ones.
	// On the dev-mode path (STORAGE=json) its WorkshopSource is nil --
	// /workshops/{slug} and the sitemap's workshop portion fall back to
	// their documented generic defaults (seo.WorkshopSource's doc comment).
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

	// Apply the outer middleware (dev auto-login) when provided. This must never
	// be non-nil on the production path — servePostgres always passes nil.
	var handler http.Handler = mux
	if outerMiddleware != nil {
		handler = outerMiddleware(mux)
	}

	srv := &http.Server{Addr: addr, Handler: handler}

	// #0084: bind the listener synchronously (net.Listen) instead of letting
	// http.ListenAndServe do it inside the goroutine below. This gives a bind
	// failure (e.g. the port already in use) a direct return instead of a
	// goroutine hop through errCh, and lets `ready` (see mountAndServe's doc
	// comment above) be closed the instant the socket is bound and able to
	// accept connections — before srv.Serve is even called.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("opencircuit %s listening on %s", version, addr)
	if ready != nil {
		close(ready)
	}

	// Start the send worker (#0045), if one was constructed. Its own ctx is
	// deliberately NOT the shutdown context below (see worker.go's package
	// doc comment) — SIGTERM reaches it only through sendWorker.Stop, the
	// first step of the shutdown sequence, never through cancelling this
	// context.
	if sendWorker != nil {
		go sendWorker.Run(context.Background())
	}
	// Outbox worker (#0126): same shape as sendWorker above — its own ctx,
	// stopped by signal through outboxWorker.Stop, never by cancelling
	// this context.
	if outboxWorker != nil {
		go outboxWorker.Run(context.Background())
	}

	// Graceful shutdown (#0081): before this, mountAndServe ended in a bare
	// http.ListenAndServe with no signal.Notify anywhere in the repo, so an
	// unhandled SIGTERM (every deploy, every systemd restart) terminated the
	// process mid-request and mid-send with no chance for any cleanup to
	// run. Reproduced consequence: a subscribe request's confirmation email
	// send, queued on subscribeH's async worker pool (#0026), could be
	// interrupted after its cooldown claim was taken but before the send
	// (or its compensating release) ever completed — stamping
	// confirm_sent_at, sending nothing, and silencing that visitor's retry
	// for subscribeResendCooldown (1 hour). See issues/0081.md's
	// ## Root cause for the reproduced before/after state.
	//
	// sigCh is buffered (size 1, standard signal.Notify idiom) so a signal
	// arriving before the select below is ready is never dropped.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	// signal.Stop releases sigCh's registration on every return path
	// (including the errCh branch below). Without this, a test binary that
	// calls mountAndServe and never receives a signal (the three existing
	// mountAndServe-based wiring tests) leaves a permanent SIGINT/SIGTERM
	// registration on a channel nobody ever reads again, which makes the
	// process SWALLOW both signals from then on — a Ctrl-C on a hung
	// `go test ./cmd/opencircuit/...` orphans the test binary still holding
	// testdb's advisory lock, blocking every other package's DB tests until
	// it's killed by hand.
	defer signal.Stop(sigCh)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		// The listener failed to start (e.g. the port is already in use) —
		// nothing was ever serving, so there is nothing to shut down.
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err

	case sig := <-sigCh:
		log.Printf("opencircuit: received %s, shutting down gracefully", sig)

		// worker.Stop runs FIRST, on its own independent budget
		// (workerCloseTimeout), per #0087's rule that these shutdown
		// budgets are never shared and #0045's plan §1: it is the only
		// step whose work is unbounded in principle (a large campaign's
		// drain) and the only one that can be told to stop cheaply —
		// Stop closes an internal channel that Run checks BETWEEN
		// messages and between batches, never during one, so a SIGTERM
		// here cannot leave a message accepted by SES and unrecorded in
		// Postgres (see worker.go's package doc comment). sendWorker is
		// nil under STORAGE=json, MAILER_NOOP=true, or
		// SEND_WORKER_ENABLED=false.
		if sendWorker != nil {
			workerStopCtx, workerStopCancel := context.WithTimeout(context.Background(), workerCloseTimeout)
			if err := sendWorker.Stop(workerStopCtx); err != nil {
				log.Printf("opencircuit: send worker stop: %v", err)
			}
			workerStopCancel()
		}
		// outboxWorker.Stop runs alongside sendWorker.Stop above, on its own
		// independent budget (outboxWorkerCloseTimeout) — same reasoning:
		// it releases any claimed-but-unsent outbound_queue row back to
		// 'queued' before returning (see OutboxWorker.Stop's doc comment),
		// so a restart picks it up immediately rather than waiting out
		// outboxOrphanStaleAfter.
		if outboxWorker != nil {
			outboxStopCtx, outboxStopCancel := context.WithTimeout(context.Background(), outboxWorkerCloseTimeout)
			if err := outboxWorker.Stop(outboxStopCtx); err != nil {
				log.Printf("opencircuit: outbox worker stop: %v", err)
			}
			outboxStopCancel()
		}

		// shutdownServerTimeout and subscribeCloseTimeout each bound ONE
		// step below, on their OWN independent context — not one shared
		// budget for both (#0087; before this they shared a single ctx,
		// see git history for the previous shape). http.Server.Shutdown
		// does not cancel in-flight request contexts, and GET /api/events
		// (internal/handlers/events.go, mounted above at "GET /api/events")
		// returns only on client disconnect — so a single open SSE stream
		// (one admin dashboard tab) can hold srv.Shutdown open for its
		// ENTIRE budget. Under the old shared-context shape that meant
		// subscribeH.Close received whatever was left, which could be
		// nothing: reproduced with a 2s shared budget, Shutdown returned
		// after 2.00s with context.DeadlineExceeded, leaving Close 0s — its
		// claim-release work was started (sendCancel/close(sendQueue) run
		// unconditionally, before any ctx is even consulted) but Close
		// itself returned immediately without waiting for it to finish,
		// which matters because that work can still be running when the
		// process exits (see subscribeCloseTimeout's own doc comment on the
		// detached-release-context risk that follows from this). Splitting
		// the budget means an open SSE stream can still cost Shutdown its
		// own budget — the same, lesser consequence #0081's review already
		// accepted for the OLD Close-first ordering (a dropped SSE
		// connection, which EventSource reconnects automatically) — but it
		// can no longer reach into Close's.
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownServerTimeout)
		defer shutdownCancel()

		// srv.Shutdown runs FIRST, matching Close's own doc comment
		// (subscribe.go): once Shutdown returns nil, it is guaranteed that
		// no request goroutine can still be inside enqueueSend — every
		// in-flight handler, including Subscribe, has already returned. If
		// Shutdown instead returns DeadlineExceeded (its own ctx bound hit
		// with a handler — e.g. an open SSE stream — still running), that
		// guarantee does NOT hold: a handler may still be executing
		// concurrently with the Close call below. This is harmless only
		// because enqueueSend's closeMu-guarded check still refuses a send
		// after closed flips true, and Close holds that same lock while
		// setting closed=true — the two can safely overlap. Calling Close
		// first (the order this file used before #0081's review) merely
		// widened that already-safe overlap window; it did not introduce a
		// new one. See enqueueSend's own comment for why the ordering
		// between closeMu.Lock and sendWG.Add/Wait, not the ordering of
		// these two top-level calls, is what actually makes the WaitGroup
		// misuse #0081 reproduced impossible.
		shutdownErr := srv.Shutdown(shutdownCtx)

		// subscribeH may be nil in dev mode (STORAGE=json — see
		// serveDevMode's comment on the call site). Its own, independent
		// context (see shutdownServerTimeout's doc comment above for why)
		// means a Shutdown that consumed its entire budget above — e.g. the
		// open-SSE-stream scenario — has no effect on how much time Close
		// gets here.
		if subscribeH != nil {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), subscribeCloseTimeout)
			defer closeCancel()
			if err := subscribeH.Close(closeCtx); err != nil {
				log.Printf("opencircuit: subscribe handler close: %v", err)
			}
		}

		return shutdownErr
	}
}

// shutdownServerTimeout bounds srv.Shutdown ALONE (#0087 — it used to share
// a single budget with subscribeCloseTimeout below; see the shutdown
// sequence's own comment for why that was wrong and what it cost). Ordinary
// requests never block on a network call, so in steady state this returns
// in well under a second; the only thing that can hold it open for its full
// budget is a long-lived GET /api/events stream, since Shutdown does not
// cancel in-flight request contexts. That is exactly the scenario this
// bound exists to cap, rather than let it also eat into
// subscribeCloseTimeout's budget.
//
// var, not const: cmd/opencircuit's shutdown-budget wiring test overrides
// this (and subscribeCloseTimeout) to exercise both the short-Shutdown-
// budget and the split-vs-shared-context behavior deterministically,
// without waiting out the real production value.
var shutdownServerTimeout = 5 * time.Second

// subscribeCloseTimeout bounds subscribeH.Close ALONE (#0087), independent
// of shutdownServerTimeout above. Sized larger than shutdownServerTimeout
// deliberately: Close's job — cancelling sendCtx and waiting for every
// queued/in-flight send's claim to actually finish being released — is the
// step whose starvation has the worse consequence (issues/0087.md's "Why
// starving Close is worse than starving Shutdown": a stranded confirmation
// claim silences a visitor's retry for up to subscribeResendCooldown, an
// hour, versus a dropped SSE connection EventSource reconnects
// automatically), so it gets the majority of the 20s the two constants
// summed to under the old shared design.
//
// This also has to comfortably cover releaseSendClaim's own detached 5s
// context (subscribe.go) — the one piece of this sequence Close's own Wait
// does NOT bound (see releaseSendClaim's doc comment): a job that fails
// (including one cancelled by Close's own sendCtx cancellation) releases
// its claim on a context timed from when THAT release began, not from when
// Close was called, so in the worst case Close's Wait needs to cover very
// nearly the full 5s of a release that started right at the end of its own
// budget. 15s leaves 3x headroom over that 5s figure — a splitting-the-
// budget fix alone is judged sufficient (no separate tracking of the
// detached release context is added) PROVIDED this constant is kept
// meaningfully above 5s; do not shrink it without re-deriving that margin.
//
// deploy/systemd/opencircuit.service's TimeoutStopSec must exceed
// shutdownServerTimeout + subscribeCloseTimeout (currently 20s combined),
// or systemd SIGKILLs the process before this sequence gets the chance to
// finish on its own.
//
// var, not const: see shutdownServerTimeout's doc comment above.
var subscribeCloseTimeout = 15 * time.Second

// workerCloseTimeout bounds sendWorker.Stop ALONE, on its own independent
// budget per #0087's rule — sized to cover one in-flight message's SES call
// (sendMessageTimeout, internal/mailing/worker.go) plus its status write
// (writeStatusTimeout), not against any measured machine load (CLAUDE.md
// §5). If this budget is exceeded, Stop returns context.DeadlineExceeded
// and the process exits anyway; the consequence is exactly the §6 crash
// window worker.go's package doc comment already documents as the designed
// worst case, not a new failure mode this timeout introduces.
//
// var, not const: see shutdownServerTimeout's doc comment above — the
// wiring test shrinks this to exercise the budget deterministically.
var workerCloseTimeout = 10 * time.Second

// outboxWorkerCloseTimeout bounds outboxWorker.Stop ALONE (#0126), the same
// independent-budget shape workerCloseTimeout establishes above. Sized
// identically: one in-flight message's send (sendMessageTimeout) plus its
// status write (writeStatusTimeout), the same two constants
// outboxOrphanStaleAfter (internal/mailing/outbox_worker.go) is itself
// derived from.
var outboxWorkerCloseTimeout = 10 * time.Second
