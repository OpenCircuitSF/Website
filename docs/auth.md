# Authentication and Sessions

This document covers the session-based authentication and authorization
layer: session lifecycle, the session-guard middleware, public vs. protected
routes, the registration gate, and admin authorization. The passkey WebAuthn
ceremonies themselves (registration, login, account recovery) are covered in
[`passkeys.md`](passkeys.md). This subsystem is a straight port from
ShortLinks (Phase 0) — nothing about it is specific to this project's
mailing-list/workshop features.

## Overview

The site is passkey-only. There are no passwords. A successful WebAuthn
ceremony (registration, login, or account recovery) is the only way to
obtain a session. Once a session exists, all subsequent requests are
authenticated by a server-side database lookup on the session token carried
in a cookie.

## Session lifecycle

- **Token generation** — `auth.NewSessionToken` (`internal/auth/session.go`)
  generates 32 cryptographically random bytes and encodes them as unpadded
  URL-safe base64, yielding an opaque token safe in both URLs and
  `Set-Cookie` headers.
- **Creation** — `Store.CreateSession` (`internal/auth/store.go`) inserts a
  `sessions` row inside the *same transaction* as the WebAuthn ceremony's
  finish step (registration, login, or recovery), so the account operation
  and the session are created atomically.
- **Validation** — every protected request resolves its cookie token
  against the `sessions` table via `middleware.RequireSession`, which
  attaches the resolved user to the request context.
- **Sliding expiry** — a valid session's `last_seen_at` and `expires_at`
  slide forward on each authenticated request (see
  `Store.ResolveSession`'s implementation for the exact window).
- **Logout** — `POST /auth/logout` deletes the current session.
  `POST /auth/logout/all` ("sign out everywhere") revokes every session for
  the account, on every device, without touching enrolled passkeys.

## Middleware

`internal/middleware`:

- `RequireSession(store)` — reads the session cookie, resolves it against
  the store, and either attaches the user to context or answers `401`.
  Works identically against the real `auth.Store` and the dev-mode
  `devstore.Store` (both satisfy `middleware.SessionResolver`).
- `RequireAdmin(next)` — composed *after* `RequireSession`; answers `403`
  for a non-admin, `401` is already handled by the session guard running
  first.
- `DevAutoLogin` — dev-mode only (hard `panic` guard if constructed outside
  `STORAGE=json`); mints a session for the seeded mock admin on any request
  with no session cookie. See [`dev.md`](dev.md).
- `RateLimiter` — per-IP token-bucket limiter, applied to the abuse-prone
  public auth endpoints (register/login/recover start).

## Registration gate

`POST /auth/register/start` checks the `registrations_enabled` runtime
setting (read fresh from the database on every request, not cached) before
starting a registration ceremony. An admin flips it via
`PATCH /admin/settings`, and the change takes effect immediately — no
restart or redeploy.

## Admin authorization

`users.is_admin` gates every `/admin/*` route via `RequireAdmin`. The first
user to register on a completely fresh install (no users in the database
yet) is promoted to admin automatically; `ADMIN_EMAIL`
(`internal/config`) also pre-authorizes a specific address for the bootstrap
case. See [`architecture.md`](architecture.md) for where the admin views
(`internal/handlers/{users,settings,audit}.go`, `web/src/views/Admin.svelte`)
live.

## Where to look

| Concern | File |
|---|---|
| Session token generation/validation | `internal/auth/session.go`, `internal/auth/store.go` |
| Session middleware | `internal/middleware/auth.go` |
| Dev-mode auto-login | `internal/middleware/devauth.go` |
| Rate limiting | `internal/middleware/ratelimit.go` |
| Auth HTTP handlers | `internal/handlers/auth.go` |
| "Sign out everywhere" | `internal/handlers/auth.go`'s `LogoutAll`, `internal/auth/login.go` |
