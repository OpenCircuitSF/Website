# Passkeys / WebAuthn

The site is passkey-only — no passwords exist anywhere in the schema. This
document covers the three WebAuthn ceremonies: registration, login, and
account recovery. Session issuance once a ceremony succeeds is covered in
[`auth.md`](auth.md). Carried over from ShortLinks in Phase 0 unchanged.

## Library and configuration

Built on [`go-webauthn/webauthn`](https://github.com/go-webauthn/webauthn).
The relying party is configured from `WEBAUTHN_RP_ID` (the apex domain, so a
passkey stays valid across apex and `www`) and `WEBAUTHN_RP_ORIGIN` (the
exact origin the browser is on). **These two are not interchangeable** — a
mismatch fails every ceremony with an opaque error; see
[`configuration.md`](configuration.md) and `CLAUDE.md` §7.

## Registration (magic-link gated)

New accounts are not self-service in the naive sense — registration is
gated behind both the `registrations_enabled` setting and a magic-link
email, so an attacker can't enumerate or spam-register accounts:

1. `POST /auth/register/start {email}` — if registration is open, creates a
   `pending_registrations` row and emails a link containing a token. Always
   returns the same generic response regardless of whether the email is
   novel, already registered, or registration is closed for a *different*
   reason — see the uniform-response requirement in `CLAUDE.md` §9 (the
   same principle `#0026`'s subscribe endpoint follows later).
2. `GET /auth/register/verify?token=…` — the SPA's `RegisterVerify.svelte`
   view calls this to exchange the token for
   `PublicKeyCredentialCreationOptions`, which it hands to
   `navigator.credentials.create()`.
3. `POST /auth/register/finish?token=…` — verifies the browser's
   attestation, inserts the `users` and `passkey_credentials` rows and the
   session, all in one transaction.

## Login (passkey autofill + explicit)

Two paths, both ending at the same finish step:

- **Conditional UI (autofill)** — the email `<input>` uses
  `autocomplete="username webauthn"`; a discoverable credential challenge is
  requested on mount so the browser can surface a matching passkey inline in
  the autofill dropdown with no explicit click.
- **Explicit "Sign in"** — `GET /auth/login/start?email=…` returns a scoped
  challenge; `POST /auth/login/finish` verifies the assertion.

`GET /auth/login/start` is rate-limited per-IP (`internal/middleware.RateLimiter`).

## Account recovery

For a user who loses every enrolled passkey (a new device, a wiped
Keychain), recovery enrolls a **new** credential onto their **existing**
account rather than creating a new one:

1. `POST /auth/recover {email}` — same uniform-response requirement as
   registration.
2. `GET /auth/recover/verify?token=…` — same shape as registration verify.
3. `POST /auth/recover/finish?token=…` — adds the new credential to the
   existing account and issues a session; response shape differs slightly
   from registration finish (`{user_id}` vs. `{id, email, is_admin}`) — see
   `internal/handlers/auth.go`'s doc comments.

## Backup-eligible / backup-state flags

`passkey_credentials.backup_eligible`/`backup_state` (migration
`000006_passkey_backup_flags`) matter because go-webauthn's `ValidateLogin`
treats the **Backup Eligible** flag as immutable: the value recorded at
registration must equal the value presented on every subsequent assertion.
Every credential enrolled via iCloud Keychain or another synced passkey
provider sets `BE=true`; omitting these columns causes every synced-passkey
assertion to fail with a flag-consistency error. See the migration's own
comments for the incident this fixed in ShortLinks' history.

## Credential management

Authenticated users manage their own passkeys under `/account`
(`Account.svelte`, `internal/handlers/credentials.go`): list, rename, and
revoke — with the guard that the **last** passkey on an account cannot be
revoked without enrolling a replacement first (the backend refuses with
`409 cannot_revoke_last_credential`).

## Where to look

| Concern | File |
|---|---|
| Registration ceremony | `internal/auth/registration.go` |
| Login ceremony | `internal/auth/login.go` |
| Recovery ceremony | `internal/auth/recovery.go` |
| WebAuthn config/relying-party setup | `internal/auth/webauthn.go` |
| Credential store | `internal/auth/store.go` |
| SPA WebAuthn browser glue | `web/src/lib/webauthn.ts` |
| SPA views | `web/src/views/{Login,Account,RegisterVerify,RecoverVerify}.svelte` |
