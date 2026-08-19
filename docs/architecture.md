# Open Circuit SF — Architecture Overview

This document is the high-level map of how the site fits together. Read it
first, then follow the "Where to look" table at the bottom to dive into any
subsystem.

The service is a fork-and-strip of [ShortLinks](https://github.com/brennanMKE/ShortLinks)
(see `PRD.md` §3) — the auth/session/admin skeleton below is carried over
largely unchanged; the mailing-list, campaign, and workshop subsystems are
this project's own and land in Phases 3–6 of `PRD.md` §12.

---

## System diagram

```
Browser / curl
      │  HTTPS :443
      ▼
┌──────────────┐
│  Apache 2    │  TLS termination (Let's Encrypt)
│  reverse     │  ProxyPass → 127.0.0.1:8080
│  proxy       │  apex + plain HTTP 301 → https://www.opencircuitsf.com
└──────┬───────┘
       │  HTTP  127.0.0.1:8080
       ▼
┌──────────────────────────────────────────┐
│  Go service   cmd/opencircuit serve      │
│                                          │
│  http.ServeMux (Go 1.22+ pattern routing)│
│  ├─ /auth/*            AuthHandler       │
│  ├─ /account/*         Credentials /     │
│  │                     Settings handlers │
│  ├─ /admin/*           Admin handlers    │
│  ├─ GET /api/me        MeHandler         │
│  ├─ GET /api/events    EventsHandler     │
│  ├─ GET /health        HealthHandler     │
│  └─ GET /              SPAHandler        │
│        (catch-all → embedded index.html) │
│                                          │
│  In-process                              │
│  └─ SSE broker (in-memory pub/sub) —     │
│     reused for live campaign send        │
│     progress once Phase 5 lands          │
└───────────────┬──────────────────────────┘
                │  pgx/v5 pool
                ▼
       ┌────────────────┐
       │  PostgreSQL    │
       │  (local EC2)   │
       └────────────────┘
```

The mailing-list surface (public subscribe/preference-center/unsubscribe
routes, `internal/subscribers`, `internal/interests`, `internal/mailing`,
`internal/sesnotify`, `internal/inbound`, `internal/workshops`) is not yet
built — it lands in Phases 3–7 and will extend this diagram, not replace it.

---

## Components

### `cmd/opencircuit` — the binary entry point

`cmd/opencircuit/main.go` is a thin dispatcher with subcommands:

| Subcommand | Purpose |
|---|---|
| `serve` | Wires all dependencies and starts the HTTP server on `127.0.0.1:<PORT>` (default 8080). |
| `seed` | Idempotent bootstrap: ensures the admin user (`ADMIN_EMAIL`) exists. Safe to re-run. Currently a minimal placeholder — `#0010` extends it per `PRD.md`. |
| _(default)_ | Prints the version string and exits. |

`serve()` branches on `internal/config.Config.DevMode()`: `STORAGE=json`
routes to `serveDevMode` (the in-memory `internal/devstore`, no Postgres);
anything else routes to `servePostgres` (the real database path). Both paths
construct the same handlers and call the shared `mountAndServe`, which
registers the route table and starts `http.ListenAndServe`.

### `internal/` packages (Phase 0 — carried over from ShortLinks)

| Package | Role |
|---|---|
| `config` | Loads `Config` from environment variables (and a `.env` file in development). Collects all validation errors before returning. |
| `db` | Opens and pings the `*pgxpool.Pool`. The pool is the single shared connection object injected into every store. |
| `auth` | WebAuthn registration, login, and recovery ceremonies; session management; passkey credential store. Talks to `users`, `passkey_credentials`, `webauthn_challenges`, `pending_registrations`, and `sessions` tables. See [`auth.md`](auth.md) and [`passkeys.md`](passkeys.md). |
| `audit` | Append-only `audit_log` writer. Two write paths: `WriteTx` (inside a ceremony's transaction, so the row commits or rolls back atomically) and `Record` (fire-and-forget from API handlers whose action has already committed). |
| `events` | In-memory pub/sub `Broker`. Each `GET /api/events` SSE stream subscribes for the authenticated user; `#0048` wires campaign-send-progress as the first real publisher. |
| `devstore` | In-memory storage backend for `./scripts/dev.sh` (`STORAGE=json`). Implements every store interface the handlers depend on for users, sessions, settings, credentials (stubbed), and audit — no Postgres required. See [`dev.md`](dev.md). |
| `handlers` | All `http.Handler` implementations: auth, credentials, settings, users (admin), audit (admin), me, events, health, and the SPA catch-all. |
| `middleware` | `RequireSession` (reads the session cookie, resolves the session, attaches the user to context), `RequireAdmin` (checks `is_admin`), `DevAutoLogin` (dev-mode-only auto session), and per-IP token-bucket `RateLimiter`. |
| `testdb` | Test helper that serializes integration tests sharing one PostgreSQL test database (`TEST_DATABASE_URL`), via a session-level advisory lock. |

### `web/` — the embedded Svelte SPA

Built to `web/dist/` and embedded into the Go binary at compile time via
`//go:embed all:dist` (`web/embed.go`). See [`frontend.md`](frontend.md).

### Not yet built (Phases 1–7)

| Package (planned) | Role | Phase |
|---|---|---|
| `internal/interests` | The interest taxonomy | 3 |
| `internal/subscribers` | Signup, confirmation, preferences, unsubscribe, suppression | 3–4 |
| `internal/mailing` | Campaigns, rendering, audience, send worker, SES mailer | 5 |
| `internal/sesnotify` | SNS webhook, bounce and complaint ingestion | 5 |
| `internal/inbound` | Inbound `mailto:` unsubscribe processing | 7 |
| `internal/workshops` | Workshop records and public pages | 6 |
| `internal/seo` | Server-injected meta tags per route, sitemap, structured data | 2/7 |

---

## Where to look

| I want to… | Look at |
|---|---|
| Understand the route table | `cmd/opencircuit/main.go`'s `mountAndServe` |
| Change a passkey ceremony | `internal/auth/{registration,login,recovery}.go`, [`passkeys.md`](passkeys.md) |
| Add a config value | `internal/config/config.go`, [`configuration.md`](configuration.md), `#0007` |
| Change the schema | `migrations/`, [`database.md`](database.md) |
| Work on the SPA shell | `web/src/App.svelte`, `web/src/lib/stores.ts`, [`frontend.md`](frontend.md) |
| Run everything locally | `./scripts/dev.sh`, [`dev.md`](dev.md) |
| Understand the mailing list design | `PRD.md` §6, [`mailing-list.md`](mailing-list.md) |
| Understand unsubscribe compliance | `PRD.md` §6.5, [`unsubscribe.md`](unsubscribe.md) |
| Deploy or touch production | [`deployment.md`](deployment.md), `deploy/` |
