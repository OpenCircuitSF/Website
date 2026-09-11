# Federated Accounts — Open Circuit SF

**Scope:** identity architecture across `accounts.` / `www.` / `go.` / `mailinglist.opencircuitsf.com` and native apps
**Status:** Proposal — decision-ready, no code written
**Written:** 2026-08-18
**Grounding:** `PRD.md` (this repo), `HANDOFF.md`, `issues/0001–0064`, and the ShortLinks source at `~/Developer/brennanMKE/ShortLinks` (file:line citations throughout refer to that checkout)

---

## 1. Recommendation in brief

Build **one new Go service, `accounts.opencircuitsf.com`**, by copy-and-stripping ShortLinks the same way the Website PRD already does (§3) — keep `internal/auth`, `internal/middleware`, `internal/audit`, `internal/config`, the devstore, and the Svelte passkey views; delete the shortener. On top of that lifted stack, add a **minimal OAuth 2.1 profile**: `GET /oauth/authorize` + `POST /oauth/token` with Authorization Code + PKCE, ES256-signed JWT access tokens (RFC 9068 `at+jwt`), a JWKS endpoint, refresh-token rotation with reuse detection, and a static discovery document. No standalone IdP product, no full OIDC (no dynamic registration, no consent screens, no id_token) — the client list is fixed and first-party.

Relying parties (`www`, `go`, later `mailinglist`) never see a passkey ceremony. Each RP redirects to accounts to sign in, exchanges the returned code server-side, **upserts a local shadow user row keyed by the subject UUID, and mints its own ordinary server-side session** — the exact session machinery ShortLinks already has (`internal/auth/store.go:677`, `internal/middleware/auth.go:101`). JWTs cross the trust boundary only at handoff and refresh; per-request auth at every property stays a host-only cookie + DB lookup. Native apps authenticate **directly in-app with platform passkey APIs** against the accounts backend (no browser hop), then use the same code→token exchange.

The four decisions that matter most:

| # | Decision | Why |
|---|---|---|
| 1 | **Hand-roll a minimal OAuth 2.1 profile on the existing stack; adopt no IdP product** | Every off-the-shelf server (Keycloak, Zitadel, Authentik) replaces the already-working go-webauthn stack with its own login UI and adds an appliance to operate; Ory Hydra is headless but still a second service plus redirect choreography, and its `fosite` library is now flagged inactive. With a fixed client list (~5), no third parties, and a strong Go engineer, the OP surface is 4 endpoints and ~1.5k lines — smaller than operating any of those products. RFC 9700 (Jan 2025) + OAuth 2.1 draft-15 (Mar 2026) spell out every sharp edge to implement. |
| 2 | **WebAuthn RP ID = `opencircuitsf.com` (apex), ceremonies served only at accounts** | RP ID is baked into every credential forever; scoping it to `accounts.opencircuitsf.com` strands all passkeys if that host is ever renamed. Apex RP ID + a strict server-side origin allowlist (only the accounts origin, plus app origins later) is current consensus practice (web.dev, Corbado, Yubico) and means one AASA/assetlinks file covers web and both apps. Sibling subdomains cannot complete ceremonies because `clientDataJSON.origin` is checked server-side — go-webauthn already enforces this (`internal/auth/webauthn.go:38`). This also matches what PRD §9 already chose for www. |
| 3 | **RP-local sessions; JWTs only at the boundary** | Keeps `RequireSession`/`RequireAdmin`, the sessions table, the sliding window, audit, and the entire admin surface of both existing codebases unchanged. Revocation is a solved problem (delete the session row); no per-request JWT validation, no token-in-browser storage, no XSS token theft surface on the web properties. This is also what makes the ShortLinks mode switch small: federation changes *how a session is obtained*, not what a session is. |
| 4 | **Roles are per-property, stored in each RP's shadow user row; accounts owns only identity** | A www admin is not a go admin. The token carries who you are (`sub`, `email`), never what you may do. Each RP's `users.is_admin` keeps working exactly as today (`internal/middleware/auth.go:137`), role edits are effective on the next request with zero propagation machinery, and the accounts service stays small. Accounts keeps one global lever: `active=false`, which kills refresh everywhere within one access-token TTL. |

Also decided here: host-only `__Host-` cookies per property (never a `Domain=.opencircuitsf.com` cookie); ES256 over EdDSA; sign-out-everywhere = delete accounts sessions + refresh fails at every RP within ≤15 min; the mailing list starts inside www (as the PRD already specifies) and `mailinglist.opencircuitsf.com` is a reserved seam, not a Phase-1 service.

## 2. Rejected alternatives

| Alternative | Why it loses here |
|---|---|
| **Keycloak** | Java/Quarkus appliance, realistically 1–2 GB RAM on a t4g.small that also runs 3 Go services + Postgres. Its passkey support (production since 26.4, Sept 2025) means adopting *its* login UI and abandoning the tested go-webauthn stack, devstore, and audit integration. Operational weight is the whole story for a solo maintainer. |
| **Zitadel (server)** | Go+Postgres and passkey-first, but went **AGPL-3.0 with v3 (Mar 2025)**, is an event-sourced multi-tenant product with its own console and upgrade treadmill, and — like Keycloak — replaces rather than reuses the existing auth code. |
| **Authentik** | Python/Django + worker stack, documented 2 CPU/2 GB minimum, real worker memory regressions through 2025.10–2026.2. A homelab appliance, not a Go building block. |
| **Ory Hydra** | Closest conceptual fit (headless; your login UI stays). But it is still a second deployed service with its own DB schema and the login/consent redirect dance, for a fleet of four first-party RPs. Ory's OSS attention has shifted to Ory Network; `fosite` (its engine) is rated inactive by dependency-health trackers. The subset of Hydra this project needs is small enough to own. |
| **`ory/fosite` as a library** | Interface-heavy framework, now effectively unmaintained. Wrong bet for new solo-maintained code. |
| **`zitadel/oidc` v3 OP library** | The one defensible library route (Apache-2.0, OpenID-certified, maintained — the AGPL change covers the server product, not this library). Rejected narrowly: implementing its storage interfaces is comparable work to writing the 4 bespoke endpoints, and it drags in full OIDC semantics (id_token, nonce, discovery contract) this fleet doesn't need. Revisit only if a third-party RP ever appears — the bespoke profile deliberately keeps the wire shape (code+PKCE, JWKS, discovery JSON) so migrating to a certified stack later is a re-implementation behind the same URLs, not a breaking change. |
| **Shared `Domain=.opencircuitsf.com` session cookie instead of any token handoff** | Cheapest possible web SSO and legitimate for same-registrable-domain fleets — but one XSS'd or taken-over subdomain reads the cookie for every property including the IdP; sibling subdomains can cookie-toss shadow copies (Snyk demonstrated OAuth-flow hijack via tossing, 2024); and it does nothing for native apps, which can't share the cookie jar anyway. Host-only `__Host-` cookies + redirect SSO cost one redirect per property per session and remove the whole class. |
| **Full OIDC with id_token + `coreos/go-oidc` at each RP** | Publishing discovery + JWKS is nearly free and *is* included, but full OIDC id_token semantics (nonce handling, `c_hash`, userinfo) buy nothing over a validated `at+jwt` when all RP code is yours and shares one library. The shared library (§8) is the "20-line consumer" that go-oidc would have been. |
| **JWT-per-request at the RPs (stateless RP auth)** | Forfeits instant revocation, puts tokens where XSS can reach them (or forces cookie-JWT hybrids), and throws away the session code both codebases already have. Stateless auth solves a horizontal-scaling problem this single-host fleet does not have. |
| **Passkey ceremonies at each property (status quo of PRD §3.1)** | Four credential silos: a passkey enrolled at www does not work at go; every property carries the full ceremony surface, magic-link mailer, and recovery flow; native apps would need per-property association files. Centralizing at accounts is the entire point of this document. |

## 3. Architecture

```
                                   ┌───────────────────────────────┐
                                   │   Native apps (iOS/Android)   │
                                   │  passkey ceremony in-app      │
                                   │  (AuthenticationServices /    │
                                   │   Credential Manager)         │
                                   └──────┬────────────────────────┘
                                          │ HTTPS: /oauth/native/*, /oauth/token
                                          ▼
     Browser ──────────────────► accounts.opencircuitsf.com          :8082
        │                        ═══════════════════════════
        │  redirect SSO          IDENTITY AUTHORITY (trust anchor)
        │  (authorize/callback)  • users (subject UUID), passkeys,
        │                          WebAuthn ceremonies, recovery
        │                        • SSO sessions (__Host-ocsf_sso)
        │                        • /oauth/authorize /oauth/token
        │                        • JWKS + ES256 signing keys
        │                        • refresh tokens (rotated)
        │                        • audit log                DB: accounts
        │                                 │
        │          ┌──────────────────────┼──────────────────────┐
        │          │ JWT at handoff/      │ (same)               │ (same)
        ▼          ▼ refresh only         ▼                      ▼
   www.opencircuitsf.com  :8080   go.opencircuitsf.com :8081   mailinglist.* (future)
   ══════════════════════         ════════════════════════      starts inside www
   RELYING PARTY                  RELYING PARTY (ShortLinks,
   • shadow users(subject,        federated mode)
     email, is_admin, active)     • users + subject column
   • own sessions (__Host-…)      • own sessions (__Host-…)
   • subscribers/campaigns/       • links/clicks/campaigns
     workshops   DB: opencircuit  DB: shortlinks
        ▲
        │ serves /.well-known/* for the APEX host (AASA, assetlinks,
        │ webauthn) — association files live on the RP ID host
   opencircuitsf.com (apex, 301→www except /.well-known/)

   One EC2 host · Apache 2 vhosts (TLS) · one systemd unit per service
   one Postgres cluster · one database per service · no cross-DB FKs
```

**Trust boundaries.**

- The accounts service is the only component that ever sees a WebAuthn ceremony, a recovery email, or a signing key. Its database is the only place a credential exists.
- RPs trust exactly one thing from outside: an ES256 JWT that validates against `https://accounts.opencircuitsf.com/.well-known/jwks.json` with the checks in §5. They hold a client secret (confidential clients) and a refresh token per signed-in session; both live server-side only.
- Browsers hold only host-scoped opaque session cookies — one per property, unreadable and unsettable across properties (`__Host-` prefix forbids a `Domain` attribute).
- Native apps hold a refresh token in Keychain/Keystore and an access token in memory. They never hold a client secret (public clients, PKCE).
- Apache and the host OS are inside every boundary — single-host deployment means host compromise is total compromise regardless of design. The design's job is to keep *property-level* compromise (one XSS, one service bug, one dangling subdomain) contained to that property.

**Who owns what data.**

| Data | Owner | Notes |
|---|---|---|
| User identity: subject UUID, email, email_verified, display name, `active` kill switch | **accounts DB** | The only writable copy. Subject is the permanent cross-property key; email is mutable metadata. |
| Passkey credentials, WebAuthn challenges, pending registrations/recoveries | **accounts DB** | Lifted schema: `migrations/000001`, `000004`, `000009` from ShortLinks, plus a persisted `user_handle` (§7 — fixes the mirror-from-assertion gap at `internal/auth/login.go:207`). |
| SSO sessions, auth codes, refresh tokens, signing-key metadata, accounts audit log | **accounts DB** | |
| Shadow user rows: `(subject, email, is_admin, active, created_at, last_login_at)` | **each RP's DB** | Upserted at callback time. Email cached for display/audit only — the subject is the identity. Per-property `is_admin` and per-property `active` (an RP can ban a user locally without touching accounts). |
| RP sessions | **each RP's DB** | Same `sessions` table shape ShortLinks has (`migrations/000005`), plus `refresh_token` (encrypted) and `identity_checked_at` columns. |
| Per-property domain data (links, subscribers, campaigns, workshops…) | **each RP's DB** | Keyed to the RP's local `users.id` exactly as today — no schema churn in domain tables. |

**The user record lives at accounts, full stop.** RPs may cache, never author. If accounts and an RP disagree about email, accounts is right and the RP catches up at next callback/refresh. Deleting a user at accounts orphans shadow rows harmlessly (they stop being reachable — no session can be created for a dead subject); GDPR-style erasure additionally deletes shadow rows via the ops runbook, not via runtime coupling.

## 4. Auth flows

Notation: `A` = accounts service, `RP` = relying party (www or go), `B` = browser.

### 4.1 Browser sign-in at an RP (the SSO handoff)

1. B: `GET https://go.opencircuitsf.com/admin` → RP's `RequireSession` finds no valid `__Host-go_session` cookie → SPA shows "Sign in".
2. B: user clicks sign in → `GET /auth/federated/login?return_to=/admin` on the RP.
3. RP: validates `return_to` (relative path only, must start `/`, no `//` or `\` — §12), generates `state` (32 random bytes) and a PKCE verifier, stores `{state, verifier, return_to}` in a short-lived (10 min) `__Host-go_oauth` cookie (HttpOnly, Secure, SameSite=Lax), 302 → `https://accounts.opencircuitsf.com/oauth/authorize?client_id=go&redirect_uri=https://go.opencircuitsf.com/auth/federated/callback&response_type=code&state=…&code_challenge=…&code_challenge_method=S256`.
4. A: validates `client_id` against the static registry and `redirect_uri` by **exact string match**. Checks for a live `__Host-ocsf_sso` cookie (same DB-backed sliding-window session as ShortLinks today, `internal/auth/store.go:677`).
   - **Session present** → skip to step 6. This is the "silent SSO" path — second and later properties sign in with one redirect round-trip, no user interaction.
   - **No session** → serve the login SPA (the lifted `Login.svelte` with conditional-UI passkey autofill), preserving the authorize parameters.
5. A: user completes the passkey ceremony — identical to ShortLinks `LoginService.StartLogin`/`FinishLogin` (`internal/auth/login.go:91,162`): challenge consumed single-use inside the transaction, sign-count/BE/BS rules applied, `account.login` audit row written in the same transaction, SSO session row created, `__Host-ocsf_sso` cookie set.
6. A: mints a single-use authorization code (60 s TTL) bound to `{subject, client_id, redirect_uri, code_challenge, sid}`, stores its hash, 302 → `https://go.opencircuitsf.com/auth/federated/callback?code=…&state=…`.
7. RP: verifies `state` equals the cookie value (CSRF gate), then back-channel `POST https://accounts.opencircuitsf.com/oauth/token` with `grant_type=authorization_code`, the code, the PKCE `code_verifier`, and its client secret (Basic auth).
8. A: validates client secret, code (unexpired, unconsumed — consumption is atomic; a second redemption of the same code revokes everything issued from it), PKCE verifier against the stored challenge, and `redirect_uri`. Returns `{access_token (JWT, aud "go", 10 min), refresh_token (rotating, bound to sid), expires_in}`.
9. RP: validates the JWT per §5. Upserts the shadow row: `INSERT INTO users (subject, email, …) VALUES … ON CONFLICT (subject) DO UPDATE SET email=…` (plus the one-time email-claim migration in §9.4). Rejects with a friendly "no access" page if the RP is staff-gated and the shadow row is absent/inactive — see §6.
10. RP: creates its own session row (existing `Store.CreateSession`, `internal/auth/store.go:736`), storing the refresh token (encrypted, §7) and `identity_checked_at=now()` on it; sets `__Host-go_session`; clears the oauth cookie; 302 → the validated `return_to`.

The user experience: first property = one passkey tap; every other property = an invisible redirect bounce.

### 4.2 Passkey registration (new account)

Runs entirely at accounts; lifted unchanged from ShortLinks registration (`internal/auth/registration.go`), including the `registrations_enabled` gate read fresh per request (`internal/auth/store.go:69`) and the enumeration-safe uniform response.

1. B (at `accounts.opencircuitsf.com/register`, typically arriving mid-authorize): submits email → `POST /auth/register/start`.
2. A: gate check → pending registration row (5-min TTL) → magic-link email via SES. Response is always "check your email".
3. B: opens `GET /auth/register/verify?token=…` → A returns creation options (`residentKey: required`, `userVerification: required`, ES256/RS256 — `internal/auth/webauthn.go:148`), **with RP ID `opencircuitsf.com`**.
4. B: `navigator.credentials.create()` → `POST /auth/register/finish?token=…`.
5. A, in one transaction (as `FinishRegistration` does today): consume challenge, verify attestation, create the user — **newly: with a generated subject UUID and the ceremony's user handle persisted** — insert the credential with BE/BS flags, delete the pending row, create the SSO session. Admin promotion (`registration.go:215`) is dropped at accounts: accounts has no `is_admin` (§6); the ADMIN_EMAIL bootstrap moves to the RPs' seed commands.
6. If registration was entered from an authorize request, A resumes it: the new SSO session satisfies step 4.1-4 and the code redirect fires. Otherwise land on the account page.

Adding a second passkey to a signed-in account and recovery-driven enrollment reuse the same ceremony machinery, as today.

### 4.3 Native app sign-in (no browser)

The app performs the WebAuthn ceremony itself with platform APIs — `ASAuthorizationPlatformPublicKeyCredentialProvider` (iOS 16+) / Credential Manager (Android) — against accounts endpoints shaped like the IETF *OAuth for First-Party Applications* draft's authorization-challenge flow:

1. App: `POST /oauth/native/start` `{client_id: "ios-app", code_challenge, code_challenge_method: "S256", email?}` → A rate-limits (as `GET /auth/login/start` today: 10/min/IP), issues assertion options exactly as `StartLogin` does (discoverable fallback, no enumeration), stores the challenge single-use, returns options + a ceremony handle.
2. App: runs the platform passkey sheet with `relyingPartyIdentifier: "opencircuitsf.com"`. The OS verifies the app↔domain association: iOS via the `webcredentials:opencircuitsf.com` entitlement against `https://opencircuitsf.com/.well-known/apple-app-site-association`; Android via `assetlinks.json` with the Play App Signing SHA-256 fingerprint.
3. App: `POST /oauth/native/finish` `{ceremony, assertion}` → A validates the assertion **with the app origins in its allowlist**: iOS assertions carry `origin: "https://opencircuitsf.com"`, Android assertions carry `origin: "android:apk-key-hash:<base64url sha256>"` — both must be in `WEBAUTHN_RP_ORIGINS` (§7). On success: SSO session row (marked `client=native`), single-use authorization code returned in the JSON body (no redirect).
4. App: `POST /oauth/token` with code + PKCE verifier (no client secret — public client) → `{access_token (aud as requested, 10 min), refresh_token (90-day idle / 365-day absolute, rotating)}`.
5. App stores the refresh token in Keychain (`kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`, non-syncing) / Keystore-wrapped storage; access token in memory only.
6. App calls APIs: accounts APIs with `aud:"accounts"` tokens; the go API with `aud:"go"` tokens via `Authorization: Bearer` — ShortLinks' bearer path (`internal/middleware/auth.go:23`) extended in federated mode to accept JWTs (§9.3).

Fallback (kept in reserve, not built first): `ASWebAuthenticationSession`/Custom Tabs driving the ordinary §4.1 authorize URL with an `https` callback. Build it only if a flow ever needs web UI (e.g. registration from the app before in-app registration exists).

### 4.4 Token refresh (web RP, server-side)

1. RP middleware, on any authenticated request where `sessions.identity_checked_at < now() − 15 min`: `POST /oauth/token` `{grant_type: refresh_token, refresh_token}` + client secret.
2. A: looks up the token by hash. Checks: not expired, not revoked, **not already used** (reuse ⇒ theft signal ⇒ revoke the whole family and the backing SSO session, audit `token.reuse_detected`), backing SSO session still alive, user still `active`.
3. A: rotates — marks old used, issues new refresh + new access token (fresh email/claims). RP updates the stored refresh token and `identity_checked_at`, refreshes shadow-row email if changed.
4. Failure (`invalid_grant`) → RP deletes its session row → the user is signed out of that property and re-enters via §4.1 (invisible if the SSO session still exists; a passkey tap if not).

Native apps run the same grant themselves; the 30–60 s reuse grace window absorbs retry races.

### 4.5 Account recovery (lost passkey)

Lifted verbatim from ShortLinks recovery (`internal/auth/recovery.go`): `POST /auth/recover` (3/hour/IP) → 15-min magic link → `navigator.credentials.create()` at accounts → new credential row added to the **existing** user, other credentials untouched, SSO session issued. Because RPs never hold credentials, recovery is invisible to them — the next authorize redirect just works. `Account.svelte` (lifted) additionally offers "sign out everywhere" (§4.6) and credential revoke, with ShortLinks' last-credential guard (`internal/auth/store.go:842`, the 409 `cannot_revoke_last_credential` behavior).

### 4.6 Sign-out

**Local (one property):** RP `POST /auth/logout` → delete RP session row, best-effort `POST /oauth/revoke` for that session's refresh token, clear cookie. The accounts SSO session survives — visiting the property again signs straight back in silently. The UI must say "signed out of go.opencircuitsf.com" and link to accounts for global sign-out; a local-only logout that looks global is a support ticket.

**Global (everywhere):** at `accounts.opencircuitsf.com/account` → "Sign out everywhere" → lifted `LogoutAll` (`internal/auth/login.go:340`): delete all SSO sessions for the user in one transaction, audit row in the same transaction, notification email after commit. Additionally: revoke all refresh-token families. Effect per surface: accounts immediately; every web RP and native app within ≤15 min (next refresh fails → session deleted). RP sessions deliberately do **not** get a synchronous kill in Phase 1 — §12 records the bounded-staleness tradeoff, and F6 adds optional direct revocation pings (accounts → each RP's internal endpoint) if instant SLO ever matters.

### 4.7 Admin/role changes propagating

1. Per-property role change (the normal case): www admin opens `/admin/users` (the ported screen, issue #0009), toggles `is_admin` or `active` on a **shadow row**. Effective on the target's next request at that property — `ResolveSession` joins the local users row exactly as ShortLinks does today (`internal/auth/store.go:677`), so there is nothing to propagate.
2. Global deactivation: operator flips `active=false` at accounts admin → accounts deletes the user's SSO sessions and revokes refresh families (the lifted `DeactivateUser` behavior, `internal/handlers/users.go:154`) → every property locks out within ≤15 min; authorize requests fail immediately.
3. Granting a new person access to a staff-gated property: they register at accounts (or already have an account), then a property admin creates/activates their shadow row. Default-deny: a valid accounts identity with no shadow row gets "no access" at staff-only RPs (§6).

## 5. Token design

One boundary-crossing token type: an **ES256-signed JWT access token**, RFC 9068 profile (`typ: "at+jwt"`), minted only by accounts. No id_token. Refresh tokens are opaque 32-byte random values (same `randomURLToken` discipline as `internal/auth/tokens.go:29`), stored hashed (SHA-256) server-side.

**Why ES256, not EdDSA:** identical security tier, but ES256 has zero compatibility asterisks in 2026 — universal in WebCrypto (Ed25519 only reached Chrome in 137, May 2025, ~79% reach), native in Secure Enclave/StrongBox (which speak P-256, not Ed25519 — this matters if DPoP/device-bound keys are ever added), and first-class in `golang-jwt/jwt/v5` (`SigningMethodES256`), which is already in ShortLinks' module graph as an indirect dependency of go-webauthn (`go.mod:22`). Promote it to a direct dependency.

**Claim set (access token):**

```json
{
  "iss": "https://accounts.opencircuitsf.com",
  "sub": "9f3c1c1e-8a5e-4a7e-9c1c-3f4b1a2d5e6f",   // subject UUID, permanent
  "aud": "go.opencircuitsf.com",                    // exactly one audience per token
  "exp": 1765432800,                                // iat + 600
  "iat": 1765432200,
  "jti": "…",                                       // 16 random bytes, base64url
  "sid": "…",                                       // accounts SSO session id
  "client_id": "go",
  "email": "person@example.com",
  "email_verified": true,
  "name": "Display Name",                           // optional
  "amr": ["webauthn"]
}
```

No roles, no scopes in v1 — `aud` is the authorization boundary between properties, and roles are RP-local (§6). A `scope` claim is reserved for later API tiering.

**TTLs:**

| Token | TTL | Rationale |
|---|---|---|
| Authorization code | 60 s, single-use | Only bridges one redirect. |
| Access token | 10 min | Bounded revocation staleness; refresh is cheap on one host. Clock-skew leeway 30 s (`jwt.WithLeeway`) — the hosts run NTP. |
| Refresh token (web RP) | 30-day sliding, capped by the backing SSO session (30-day sliding, matching ShortLinks `sessionTTL`, `internal/auth/store.go:20`) | Web RP session lifetime ≡ accounts session lifetime. |
| Refresh token (native) | 90-day idle, 365-day absolute | Consumer-mobile norm; re-auth is one passkey tap, so err short. |
| SSO cookie session | 30-day sliding | Lifted behavior. |

**Signing keys and JWKS.** P-256 private keys as PEM files in `/etc/accounts/keys/` (0600, owner `accounts`), never in the DB. `kid` = RFC 7638 JWK thumbprint. `GET /.well-known/jwks.json` serves the public halves of every key present, `Cache-Control: max-age=900`. An `accounts keygen` subcommand creates a key; the active signing key is named by `SIGNING_KID` (or newest-file default).

**Rotation procedure** (quarterly on a calendar reminder, and immediately on suspicion of compromise):

1. `accounts keygen` → new key file appears; restart/SIGHUP → JWKS now serves both keys; old key still signs.
2. Wait ≥ 30 min (2× JWKS cache TTL).
3. Set `SIGNING_KID` to the new kid; restart → new tokens signed with the new key.
4. Wait ≥ access TTL + cache TTL (25 min; use 24 h for slack).
5. Delete the old key file; restart. Compromise variant: skip the waits, delete the old key immediately, and accept a ≤15-min blip where outstanding tokens fail validation and every RP session re-establishes via refresh→authorize.

**What every RP must validate** (implemented once, in the shared library — §8):

| Check | How | Failure mode if skipped |
|---|---|---|
| Algorithm allowlist | `jwt.WithValidMethods(["ES256"])` — never trust the header | `alg:none` / HS256-with-public-key confusion → **anyone mints valid tokens**. The classic JWT kill shot. |
| Signature via `kid` → JWKS from the **pinned** issuer URL | Cached JWKS client; on unknown `kid`, refresh once, rate-limited to 1/5 min; never honor `jku`/`x5u`/`x5c` from the token | Attacker-supplied key URL → attacker-signed tokens accepted. |
| `iss` exact match | `== "https://accounts.opencircuitsf.com"` | Any JWT from any issuer your JWKS fetch can be confused into trusting passes. |
| `aud` contains self | `== "go.opencircuitsf.com"` (per-RP constant) | **Cross-property replay**: a token legitimately issued for www is accepted at go — an XSS or log leak at one property becomes credentials for all of them. This is the check most likely to be lazily skipped; it is the one that makes four properties four security domains. |
| `exp`/`nbf`/`iat` with 30 s leeway | library default + leeway | Expired stolen tokens replay forever; revocation stops meaning anything. |
| `typ == "at+jwt"` | header check | Refresh tokens or future other-typed JWTs replayed as access tokens. |
| HTTPS + TLS verification on the token/JWKS endpoints | default Go client, no `InsecureSkipVerify` | On-path host answers the JWKS fetch → attacker keys. |

RPs additionally enforce (not JWT validation, but part of the same handoff): `state` match, PKCE verifier secrecy, exact `redirect_uri` registration, and single-use codes — each maps to a §12 threat.

## 6. Authorization model

**Identity is global; authority is local.**

| Layer | Lives | Granted by | Enforced at |
|---|---|---|---|
| "Is a real person with a verified account" | accounts DB (`users.active`, `email_verified`) | registration + the `registrations_enabled` gate | authorize + every refresh |
| "May use property X at all" | X's shadow `users` row exists and `active=true` | X's admins (or X's policy: go could be open-to-all-identities, www is staff-only) | X's callback (§4.1 step 9) + `RequireSession` |
| "Is an admin of X" | X's `users.is_admin` | X's admins via X's `/admin/users` screen | X's `RequireAdmin` (`internal/middleware/auth.go:137`, unchanged) |

Accounts carries **no** `is_admin` on identities and no roles-in-token. Consequences, all intended: a www admin is nothing at go until a go admin says otherwise; compromising the accounts DB's *data* (not keys) does not grant admin anywhere; role checks never depend on token freshness — they read the local row on every request via the existing session join, so demotion is instant at the property that did it.

Accounts does have its own operator surface (`/admin/*` on the accounts service — user list, deactivate/reactivate, settings, audit), gated by an `is_operator` flag on accounts' own shadow concept of itself: accounts is RP #0 of its own identities, with the same shadow semantics (`users.is_operator`, seeded by `ADMIN_EMAIL` at `accounts seed`, mirroring ShortLinks' seed + recovery bootstrap path documented in `docs/passkeys.md` §"First-admin enrollment").

**Bootstrap chain on a fresh install:** `accounts seed` pre-creates the `ADMIN_EMAIL` identity (no passkey) → operator enrolls via the recovery flow (exactly the ShortLinks first-admin path) → each RP's `seed` command pre-creates a shadow row for `ADMIN_EMAIL` with `is_admin=true`, matched to the subject at first sign-in via the email-claim rule (§9.4). Issue #0010's semantics survive with a one-line change of meaning.

## 7. The accounts service

New repo (proposed: `github.com/brennanMKE/Accounts`, binary `accounts`, service name `accounts`). Built the way Website issue #0001 builds www: copy the ShortLinks tree, strip the shortener, keep the auth spine. It is deliberately the third copy of that skeleton — the pattern is proven and the PRD already commits to it (PRD §3).

**Lifted verbatim** (module path + branding + cookie name changes only):

| From ShortLinks | Carries |
|---|---|
| `internal/auth/` — `registration.go`, `login.go`, `recovery.go`, `store.go`, `session.go`, `tokens.go`, `webauthn.go`, `mailer.go`, `ses_mailer.go` + all `*_test.go` | The three ceremonies, single-use challenge consumption in-transaction, BE/BS handling, sliding sessions, enumeration-safe responses, `LogoutAll` with post-commit notification, the `Mailer` seam, `descope/virtualwebauthn`-driven ceremony tests |
| `internal/middleware/` — `auth.go`, `ratelimit.go`, `devauth.go` | `RequireSession` (+bearer transport), `RequireAdmin`, per-IP token buckets, dev auto-login with its dev-mode-only panic guard (`devauth.go:44`) |
| `internal/audit/`, `internal/config/` (pattern), `internal/db/`, `internal/devstore/` (trimmed to auth entities), `internal/testdb/` | Audit WriteTx/Record, all-errors-at-once config loading, pgx pool, `STORAGE=json` dev seam |
| `internal/handlers/` — `auth.go`, `me.go`, `users.go`, `credentials.go`, `settings.go`, `audit.go`, `health.go`, `static.go` | The full auth + admin HTTP surface |
| `web/src/lib/webauthn.ts`, `Login.svelte`, `Account.svelte`, `RegisterVerify.svelte`, `RecoverVerify.svelte`, embed build | Browser ceremony plumbing (base64url discipline) and views |
| `migrations/000001,000004,000005,000006,000007,000009,000013`, `deploy/`, `scripts/` | Schema + systemd/Apache/dev/backup scaffolding |

**Modified in the lift:**

1. `users` gains `subject UUID UNIQUE NOT NULL DEFAULT gen_random_uuid()` and `user_handle BYTEA UNIQUE NOT NULL` — today the handle is random per registration and never persisted; login mirrors it back from the assertion (`internal/auth/login.go:202-207`, `webauthn.go:64`). That works, but persisting it is required for `BeginLogin`'s allowCredentials path to present a stable handle and is simply correct bookkeeping for an identity provider. `is_admin` becomes `is_operator` (§6).
2. `webauthn.go`: `RPOrigins` becomes a list from `WEBAUTHN_RP_ORIGINS` (comma-separated) — go-webauthn already accepts a slice (`internal/auth/webauthn.go:38`); ShortLinks just passes one. Needed for the iOS (`https://opencircuitsf.com`) and Android (`android:apk-key-hash:…`) origins in F5.
3. Cookie name `__Host-ocsf_sso` (Path=/, Secure, HttpOnly, SameSite=Strict, **no Domain, no Expires quirks** — `SetSessionCookie`, `internal/auth/session.go:24`, gains the prefix; note `__Host-` requires dropping nothing else, the current attributes already qualify).
4. Registration admin-promotion logic (`registration.go:215`) removed; sessions table gains `client TEXT` (`web`/`native`) and is the anchor for `sid`.
5. Mailer: keep the `Mailer` interface, swap the SMTP implementation for SES v2 API with instance-role credentials, as PRD §6.6 already argues for www. (ShortLinks keeps SMTP; the interface is the seam.)
6. Drop: SESSION_SECRET (required-but-unused today — `docs/auth.md` states it plainly; note `docs/configuration.md`'s "used to sign session cookies" line is stale). Sessions stay opaque server-side tokens; no cookie signing exists to need a secret. Config keeps a deliberate required `DEPLOYMENT_MARKER`-free posture: just don't carry the dead variable.

**New package: `internal/oauth/`**

```
internal/oauth/
├── clients.go     static registry loaded from /etc/accounts/clients.json:
│                  {id, secret_hash?, redirect_uris[], audience, public bool}
├── codes.go       mint/consume authorization codes (hashed, 60s, single-use,
│                  bound to subject+client+redirect_uri+PKCE challenge+sid)
├── pkce.go        S256 verify
├── tokens.go      ES256 at+jwt minting (golang-jwt/v5), claim assembly
├── refresh.go     rotate-on-use, family tracking, reuse detection → family+sid
│                  revocation, grace window 45s
├── keys.go        key loading from KEYS_DIR, kid derivation, JWKS document
└── handlers.go    /oauth/authorize, /oauth/token, /oauth/revoke,
                   /oauth/native/start, /oauth/native/finish,
                   /.well-known/openid-configuration, /.well-known/jwks.json
```

**HTTP surface** (additions to the lifted table in `docs/auth.md`):

| Method | Path | Purpose | Auth |
|---|---|---|---|
| GET | `/oauth/authorize` | Validate client+redirect_uri; silent-SSO or login UI; issue code | `__Host-ocsf_sso` cookie (or none → login) |
| POST | `/oauth/token` | Code+PKCE exchange; refresh grant with rotation | Client secret (confidential) / PKCE only (public) |
| POST | `/oauth/revoke` | Revoke a refresh token/family | Client credentials |
| POST | `/oauth/native/start` | Begin in-app passkey assertion (first-party-apps shape) | None; rate-limited 10/min/IP |
| POST | `/oauth/native/finish` | Verify assertion, return code in body | None (assertion is the proof) |
| GET | `/.well-known/openid-configuration` | Static discovery JSON | None |
| GET | `/.well-known/jwks.json` | Public keys | None |
| POST/GET | `/auth/register/*`, `/auth/login/*`, `/auth/recover*`, `/auth/logout`, `/auth/logout/all` | Lifted ceremonies (rate limits as today: 3/h, 10/min, 3/h) | As today |
| GET | `/api/me` | Profile for the accounts SPA | Session |
| GET/PATCH/DELETE | `/account/credentials*` | Passkey management (rename/revoke, last-credential 409 guard) | Session |
| GET/POST/PATCH | `/admin/users*`, `/admin/settings`, `/admin/audit` | Operator surface | Session + `is_operator` |
| GET | `/healthz` | Liveness | None |

**Postgres DDL sketch** (beyond the lifted tables):

```sql
ALTER TABLE users
    ADD COLUMN subject      UUID  UNIQUE NOT NULL DEFAULT gen_random_uuid(),
    ADD COLUMN user_handle  BYTEA UNIQUE,             -- NOT NULL after backfill
    ADD COLUMN display_name TEXT,
    ADD COLUMN email_verified BOOLEAN NOT NULL DEFAULT TRUE;  -- magic-link proven
-- is_admin → is_operator rename in the accounts copy.

ALTER TABLE sessions ADD COLUMN client TEXT NOT NULL DEFAULT 'web';

CREATE TABLE oauth_codes (
    id             BIGSERIAL PRIMARY KEY,
    code_hash      BYTEA UNIQUE NOT NULL,       -- sha256(code)
    user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id     BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    client_id      TEXT NOT NULL,
    redirect_uri   TEXT NOT NULL,               -- '' for native
    code_challenge TEXT NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,        -- now()+60s
    consumed_at    TIMESTAMPTZ
);

CREATE TABLE refresh_tokens (
    id          BIGSERIAL PRIMARY KEY,
    family_id   UUID NOT NULL,                  -- constant across rotations
    token_hash  BYTEA UNIQUE NOT NULL,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id  BIGINT REFERENCES sessions(id) ON DELETE CASCADE,
    client_id   TEXT NOT NULL,
    issued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,           -- idle expiry
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,                    -- set on rotation; reuse ⇒ revoke family
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX idx_refresh_family ON refresh_tokens (family_id);
```

**Config/env** (`/etc/accounts/config.env`, loader in the lifted all-errors-at-once style, `internal/config/config.go:79`):

```env
PORT=8082
BASE_URL=https://accounts.opencircuitsf.com
DATABASE_URL=postgres://accounts:…@localhost:5432/accounts?sslmode=disable
STORAGE=                       # "json" for dev mode, as today (config.go:60)
WEBAUTHN_RP_ID=opencircuitsf.com
WEBAUTHN_RP_ORIGINS=https://accounts.opencircuitsf.com
ISSUER=https://accounts.opencircuitsf.com
KEYS_DIR=/etc/accounts/keys
SIGNING_KID=                   # empty = newest key
CLIENTS_FILE=/etc/accounts/clients.json
ACCESS_TOKEN_TTL=10m
AWS_REGION=us-west-2
EMAIL_FROM=Open Circuit SF Accounts <accounts@opencircuitsf.com>
ADMIN_EMAIL=offwhite@gmail.com
```

## 8. Shared library

**Recommendation: one small versioned Go module, not per-repo copies.** Proposed: `github.com/brennanMKE/AuthKit`, module `authkit`. Three-plus consumers (www, ShortLinks federated mode, future mailinglist, plus the accounts service's own tests) and the content is exactly the code where drift is dangerous: a validation-checklist bug copied into three repos is three CVEs; in a module it is one `go get -u`. For a solo maintainer the tag-and-bump workflow is lighter than keeping copies honest.

```
authkit/
├── token/      Claims struct; Verifier (JWKS cache w/ kid-miss refresh +
│               rate floor, ES256 allowlist, iss/aud/typ/leeway checks — §5)
├── rp/         RP-side plumbing: Config{Issuer, ClientID, ClientSecret,
│               RedirectURL, Audience}; LoginHandler (state+PKCE+return_to
│               cookie); CallbackHandler (state check, code exchange,
│               verified Identity out); Refresher (rotate + invalid_grant →
│               session-kill signal)
├── idfake/     httptest fake issuer: in-memory keys, /authorize auto-approve,
│               /token, JWKS — so RP tests and ShortLinks CI never need the
│               real accounts service
└── wellknown/  helpers to emit AASA / assetlinks.json / webauthn JSON
```

What it deliberately does **not** contain: sessions, shadow-user storage, middleware — those stay per-repo because they already exist per-repo (lifted ShortLinks code) and coupling their schemas through a library would make every RP migration a library release. The RP wires `CallbackHandler`'s `Identity` into its own `users`/`sessions` stores.

Versioning: semver tags from `v0.1.0`; consumers pin in `go.mod`; no replace directives in committed code (local `go.work` for cross-repo dev on the Mac mini). Compatibility promise starts at `v1.0.0` after ShortLinks federated mode ships (second consumer proves the API).

## 9. Adapting ShortLinks (the mode switch)

Design constraint honored: **`AUTH_MODE=local` is the default and is byte-for-byte today's behavior.** Standalone users of ShortLinks never see federation unless they opt in.

### 9.1 Config surface

```env
AUTH_MODE=local            # local (default) | federated
# federated mode additionally requires:
ACCOUNTS_ISSUER=https://accounts.opencircuitsf.com
OAUTH_CLIENT_ID=go
OAUTH_CLIENT_SECRET=…
OAUTH_AUDIENCE=go.opencircuitsf.com
# and no longer requires: WEBAUTHN_RP_ID, WEBAUTHN_RP_ORIGIN, SES_SMTP_* ,
# ADMIN_EMAIL stays (seed/bootstrap), SESSION_SECRET stays until removed for both modes.
```

`internal/config/config.go` gains `AuthMode` with validation in the existing all-errors-at-once pass (`config.go:112-128`): unknown value is a startup error; the WEBAUTHN_*/SES_* requirement set applies in local mode, the ACCOUNTS_*/OAUTH_* set in federated mode. `DevMode()` (`config.go:60`) is untouched and orthogonal.

### 9.2 Which code paths branch

Exactly one construction site branches: route registration in `cmd/shortlinks/main.go` (all routes are registered there today — `docs/auth.md` §"Public vs protected routes").

| Concern | `local` | `federated` |
|---|---|---|
| `/auth/register/*`, `/auth/login/*`, `/auth/recover*` | registered (today's handlers) | **not registered** → 404 via SPA catch-all |
| `/auth/federated/login`, `/auth/federated/callback` | not registered | registered — thin wrappers over `authkit/rp` that call the existing `Store.CreateSession` (`internal/auth/store.go:736`) |
| `/auth/logout`, `/auth/logout/all` | as today | as today, plus refresh-token revoke best-effort |
| `/account/credentials*` | as today | not registered (passkeys live at accounts; SPA links there) |
| `RequireSession`, `RequireAdmin`, rate limiter, sessions table, sliding window | **identical — zero changes** | identical |
| Bearer path (`internal/middleware/auth.go:23`) | opaque session tokens (today's #0077 iPhone behavior) | opaque tokens **or** `aud:"go…"` JWTs: token contains a dot → validate via `authkit/token`, map `sub`→shadow user, else opaque path |
| `WebAuthn` construction (`internal/auth/webauthn.go:34`) | as today | not constructed |
| Refresh sweep | — | session middleware add-on: stale `identity_checked_at` triggers §4.4 |

The ceremony services (`LoginService`, `RegistrationService`, `RecoveryService`) are simply not instantiated in federated mode — no code deleted, no forks inside `internal/auth`.

### 9.3 Schema in federated mode

One migration, applied in **both** modes (harmless in local):

```sql
ALTER TABLE users ADD COLUMN subject TEXT UNIQUE;          -- NULL for local accounts
ALTER TABLE sessions
    ADD COLUMN refresh_token_enc BYTEA,                    -- AES-GCM under a local key
    ADD COLUMN identity_checked_at TIMESTAMPTZ;
```

`passkey_credentials`, `webauthn_challenges`, `pending_registrations` stay in place and simply go quiet in federated mode — empty tables cost nothing, and they make `AUTH_MODE` reversible (switch back to local and existing local users' passkeys still work).

### 9.4 Migrating existing local users to federated subjects

At callback, given a verified identity `{subject, email, email_verified}`:

1. `SELECT … FROM users WHERE subject = $1` → hit: normal sign-in.
2. Miss and `email_verified`: `UPDATE users SET subject=$1 WHERE lower(email)=lower($2) AND subject IS NULL` — **claims** the legacy row; all links/ownership follow because domain tables key on `users.id`, which never changes.
3. Miss both: insert a fresh shadow row (or reject, if the deployment sets registrations closed — the existing `registrations_enabled` setting gets this second meaning in federated mode).

Email claiming is safe here because both sides prove email ownership by magic link (accounts registration, and ShortLinks' original registration — `docs/passkeys.md` ceremony 1). The claim is one-way and audited (`user.subject_claimed`). For the actual go.opencircuitsf.com deployment this migration is trivial: the only local user will be the operator.

### 9.5 Frontend differences per mode

The SPA learns the mode from a new unauthenticated `GET /api/config` → `{"auth_mode":"federated"}` (one constant handler; local returns `local`). `Login.svelte` branches: local renders today's email + conditional-UI ceremony; federated renders one button — "Sign in with your Open Circuit SF account" → `location = '/auth/federated/login?return_to=…'`. `Account.svelte` hides the passkey panel in federated mode and links to `https://accounts.opencircuitsf.com/account`. `RegisterVerify.svelte`/`RecoverVerify.svelte` are unreachable (their routes 404) — they stay in the tree for local mode. Net new Svelte: ~40 lines.

### 9.6 Dev mode in both auth modes

`STORAGE=json` + `DevAutoLogin` already bypass the ceremony entirely — the middleware mints a devstore session for the seeded admin with no WebAuthn involved (`internal/middleware/devauth.go:43-86`, wired only inside `serveDevMode`, `cmd/shortlinks/main.go:214-277`). Therefore **dev mode works unchanged under both `AUTH_MODE`s**: `./scripts/dev.sh` keeps its zero-dependency one-command boot. For exercising the federated wire itself without the real accounts service, `authkit/idfake` runs as an httptest server inside handler tests, and an optional `scripts/dev-federated.sh` can point `ACCOUNTS_ISSUER` at a locally running accounts binary (itself in `STORAGE=json`).

### 9.7 Keeping the standalone path from bit-rotting

- `AUTH_MODE=local` is the default: every existing test — the full ceremony suites (`internal/auth/*_test.go`, ~2,100 lines incl. `virtualwebauthn` ceremonies), middleware, devstore — keeps running against the local path on every `go test ./...`. Nothing to remember.
- New federated tests live beside them (`internal/handlers/federated_test.go` + `authkit/idfake`) and also run unconditionally — the fake issuer needs no environment.
- CI (or the Makefile `verify` target) adds one matrix smoke: boot the binary once with `AUTH_MODE=local` and once with `AUTH_MODE=federated` + fake issuer, assert `/healthz` and the route table (federated: `/auth/login/start` is 404, `/auth/federated/login` redirects; local: inverse).
- `docs/auth.md` in ShortLinks gains a "Federated mode" section; the mode variable is documented in `docs/configuration.md`.


## 10. Website (www) integration

**What changes conceptually.** PRD §2 "Passkey accounts for staff — copied from ShortLinks" and §3.1's copy list become: *sessions, middleware, audit, settings copied; ceremonies not copied*. www becomes an RP: staff sign in via accounts, `/admin/*` gating is unchanged (`RequireSession` + `RequireAdmin` over the local shadow row). Public subscribers are untouched — the mailing list's `manage_token` model (PRD §6.4) never involved accounts and stays exactly as specified; PRD §2's "no subscriber accounts" decision is unaffected (and if RSVP accounts ever happen, they now have an identity story waiting: PRD §2's Phase-7 caveat "Needs an identity story for RSVPers" is answered by accounts).

**Config (PRD §9):** `WEBAUTHN_RP_ID`/`WEBAUTHN_RP_ORIGIN` leave www's env entirely (they move to accounts); in their place: `ACCOUNTS_ISSUER`, `OAUTH_CLIENT_ID=www`, `OAUTH_CLIENT_SECRET`, `OAUTH_AUDIENCE=www.opencircuitsf.com`. `SESSION_SECRET` is dropped (dead today — see §7 note 6). The HANDOFF §5.1 warning about RP_ID/RP_ORIGIN mismatch migrates to the accounts deployment docs.

**Routes (PRD §5.1):** `/login` becomes the one-button federated redirect; `/register/verify` and `/recover/verify` are removed from the route table (those flows live at accounts). `/account` shrinks to profile display + "manage passkeys at accounts" link + sign-out. `/auth/federated/login` + `/auth/federated/callback` are added (server-side, not SPA views).

**Issue-by-issue impact (all 64 reviewed):**

| Verdict | Issues | Detail |
|---|---|---|
| **Invalidated / superseded** | **#0008** | "Verify the passkey auth stack end to end after the port" — its subject matter (registration/login/recovery ceremonies, credential rename/revoke, `virtualwebauthn` tests in www) no longer exists in this repo. Superseded by #0068/#0069 below. The ceremony-verification work it describes happens once, in the accounts repo. |
| **Changed** | **#0001** | Copy scope note: ceremonies/`credentials.go`/ceremony views still get copied (simplest diff vs ShortLinks) but are deleted in #0066 rather than verified in #0008. Or copy-excluding them from the start — implementer's choice; acceptance criterion "auth stack builds" becomes "session/middleware stack builds". |
| | **#0003** | Also strip `RegisterVerify.svelte`, `RecoverVerify.svelte`, and the ceremony halves of `Login.svelte`/`webauthn.ts` (keep `Account.svelte` shell). |
| | **#0004** | Migration set shrinks: keep users (reshaped as shadow: +`subject`, −nothing else), sessions (+`refresh_token_enc`, `identity_checked_at`), settings, audit_log. Drop passkey_credentials / webauthn_challenges / pending_registrations / backup-flags / session-passkey-NOT-NULL (ShortLinks migrations 000004/000009/000013 — the PRD §3.1 table row listing them shortens). |
| | **#0005** | Devstore: drop passkey/challenge/pending entities; `DevAutoLogin` unchanged (it never touched ceremonies — `internal/middleware/devauth.go:43`). |
| | **#0006** | `CLAUDE.md`/docs mention federation; `docs/auth.md` (www copy) documents RP-mode auth. |
| | **#0007** | Env var set per above. |
| | **#0009** | `/admin/users` manages **shadow rows + per-property roles** (add role toggle — ShortLinks has no promote/demote UI, `docs/auth.md` §"Who is an admin"; www needs one since registration-time promotion is gone). Settings/audit screens unchanged. |
| | **#0010** | Seed creates the ADMIN_EMAIL **shadow row** (`is_admin=true`, subject NULL until first sign-in claims it via §9.4 email matching). |
| | **#0064** | Runbook adds: accounts vhost/unit/DB, apex `/.well-known/` serving exception (see §11 — the apex currently 301s everything to www per HANDOFF §5, which would break AASA/assetlinks/ROR files), client-secret provisioning, key rotation. |
| **Unchanged** | #0002, #0011–#0063 | Brand, router, marketing views, SEO, and the whole mailing-list/campaign/workshop program (#0023–#0061) never touch identity — they sit behind `RequireSession`/`RequireAdmin`, whose contract is preserved byte-for-byte. #0062 (backups) and #0063 (a11y) unchanged in scope; #0062's runbook gains the accounts DB via #0074. |
| **Newly blocked** | #0009, #0010 (as changed), and new #0067–#0070 | Blocked on the accounts service (F1–F2) and AuthKit existing. Phase 0 (#0001–#0007) and Phase 2 (#0011–#0022) proceed regardless — see sequencing in §13. |

## 11. Operations

**DNS (Route 53), additions to PRD §10.2:**

| Name | Type | Value | Purpose |
|---|---|---|---|
| `accounts.opencircuitsf.com` | A | EC2 Elastic IP | Accounts service |
| `mailinglist.opencircuitsf.com` | — | *(not created)* | Reserved; do not create a record that points anywhere until the service exists — a parked A record is subdomain-takeover surface (§12) |

**Apache vhosts** (pattern from ShortLinks `deploy/apache/go.sstools.co.conf`):

| vhost | Proxy | Notes |
|---|---|---|
| `www.opencircuitsf.com` | → 127.0.0.1:8080 | as PRD |
| `go.opencircuitsf.com` | → 127.0.0.1:8081 | as PRD |
| `accounts.opencircuitsf.com` | → 127.0.0.1:8082 | + `ProxyPreserveHost On`, forward `X-Forwarded-For` (rate limiter reads it — `docs/auth.md` §"Rate limiting") |
| apex `opencircuitsf.com` | 301 → www **except** `Alias /.well-known/ /var/www/apex-wellknown/` served directly, correct `Content-Type: application/json`, **no redirect** | AASA and assetlinks.json are fetched by Apple/Google infrastructure that does not follow redirects reliably; `/.well-known/webauthn` (future ROR) must also be a direct 200. This is a real change to the live config: today the apex 301s everything (HANDOFF §5). |

TLS: extend the existing Let's Encrypt certbot setup with the accounts hostname (or move to a wildcard via DNS-01 — recommended once four hostnames exist; one renewal path instead of four).

**systemd:** `accounts.service` cloned from `deploy/systemd/shortlinks.service` — dedicated `accounts` system user, `EnvironmentFile=/etc/accounts/config.env` (0600), the same hardening set (`ProtectSystem=strict`, `NoNewPrivileges`), plus `ReadOnlyPaths=/etc/accounts/keys`. Boot order: accounts has no dependency on the RPs; RPs degrade gracefully when accounts is down (existing sessions keep working until their next 15-min identity check — set the check to *skip, log, and retry next request* on connection-refused, so an accounts outage does not sign everyone out; only refresh *rejections* kill sessions).

**Key material:** `/etc/accounts/keys/*.pem`, 0600 `accounts:accounts`, excluded from backups' public buckets — backed up encrypted (age/GPG to the existing S3 backup bucket with a separate key held offline on the Mac mini). Rotation per §5; put the quarterly rotation and the restore drill on the same calendar reminder.

**Databases:** `CREATE DATABASE accounts OWNER accounts` alongside `opencircuit` and `shortlinks` (per-service credentials, as PRD §10.1 already mandates "share nothing but the host"). Backups: extend the nightly `pg_dump` script (ShortLinks `scripts/db/backup.sh` pattern) to all three DBs. The accounts DB is now the **most** critical and least reconstructible data on the host — losing it invalidates every passkey (credentials are unrecoverable secrets, not re-derivable). Verify restore before F3, same discipline PRD §10.6 demands for subscribers.

**Local development (laptop / Mac mini), three tiers:**

1. **Single-service dev (the common case):** unchanged — each repo's `scripts/dev.sh`, `STORAGE=json`, auto-login, no DB, no accounts service. This remains the default developer loop for www and ShortLinks work.
2. **Federated-wire dev:** run accounts locally (`PORT=8082`, `STORAGE=json`, `WEBAUTHN_RP_ID=localhost`, origins `http://localhost:8082` — WebAuthn allows plain-http localhost) + the RP with `AUTH_MODE=federated`, `ACCOUNTS_ISSUER=http://localhost:8082`. Cookies are host-scoped to `localhost` ignoring ports, so distinct cookie **names** (`__Host-ocsf_sso`, `__Host-www_session`, `__Host-go_session`) are what keep them apart — the naming scheme is load-bearing in dev; note `__Host-` requires Secure, which browsers waive for localhost inconsistently — the cookie helper drops the prefix (not the flags) when the host is localhost.
3. **Full-stack rehearsal:** all three services + Postgres (three local DBs), driven by a `go.work` across the repos for cross-cutting AuthKit changes.

**Passkeys in dev:** localhost-RP-ID credentials are throwaway; Safari/Chrome virtual authenticator or `virtualwebauthn` tests cover ceremonies without enrolling junk into iCloud Keychain.

## 12. Security review

Assets: accounts DB (credentials, sessions), signing keys, client secrets, refresh tokens, each RP's session store. Adversaries: opportunistic web attackers (XSS, CSRF, phishing), token thieves (logs, referrers, malware), infrastructure drift (dangling DNS, stale configs).

| Threat | Mitigations (specific to this design) |
|---|---|
| **Stolen access token replayed** | 10-min TTL + 30 s leeway; `aud` binding means a token is only good at one property; tokens never transit browsers (server-side code exchange; fragment/query never carry tokens — only single-use 60 s codes); bearer JWTs accepted only on the native API path over TLS. |
| **Stolen refresh token** | Stored only server-side (RPs, encrypted at rest) or in Keychain/Keystore; rotation-on-use — a replayed old token trips reuse detection, revoking the family **and** the backing SSO session (audit `token.reuse_detected`, notification email via the lifted `LogoutAll` mail pattern, `internal/auth/login.go:379`). |
| **Authorization-code interception** | 60 s single-use codes bound to client_id + exact redirect_uri + PKCE S256; consuming twice revokes issued tokens; confidential clients also need the secret. Native: PKCE + claimed-https/app-bound delivery (no custom-scheme hijack surface — code returns in the JSON body of `/oauth/native/finish`). |
| **XSS on any one property** | No token or credential is readable by JS anywhere: all session cookies HttpOnly + `__Host-`; access tokens exist only in RP server memory (web) — an XSS at go can ride the victim's go session (irreducible) but cannot exfiltrate anything reusable at www or accounts, cannot read the SSO cookie (different host), and cannot silently run a WebAuthn ceremony usefully (assertions for RP ID `opencircuitsf.com` from origin `go.…` fail accounts' origin allowlist — `webauthn.go:38` check with only the accounts origin configured). |
| **Open redirect via `return_to`** | `return_to` never leaves the RP: it rides the RP's own `__Host-*_oauth` cookie, not the authorize URL. Validation: must begin `/`, reject `//`, backslash variants (`/\`), and absolute URLs. Accounts-side redirect targets are only exact-match registered `redirect_uri`s (OAuth 2.1 requirement) — no wildcard, no path-prefix matching. |
| **CSRF** | SameSite does **not** protect between sibling subdomains (same site!) — so: `state` (cookie-bound, single-use) on the OAuth flow; the RPs' existing JSON-POST APIs + SameSite=Strict session cookies stay (ShortLinks posture, `session.go:32`); accounts state-changing endpoints require the session cookie + JSON content type; the one-click-unsubscribe CSRF exemption in PRD §6.5 is unrelated and unchanged. Login CSRF (attacker forces victim into attacker's session) is closed by `state` + PKCE binding the callback to the browser that started it. |
| **Cookie tossing / fixation by a sibling subdomain** | `__Host-` prefix on every session cookie — a sibling cannot set `__Host-…` for another host, and no cookie carries `Domain`. |
| **Subdomain takeover** | No parked DNS records (mailinglist deliberately uncreated); all four A records point at the one Elastic IP; quarterly DNS review is in the ops calendar. Even a taken-over sibling gets: no shared cookie (host-only), no ceremony completion (origin allowlist), no code redemption (exact redirect_uri + client secret). Residual: phishing UX on a lookalike subdomain — passkeys themselves are the phishing defense (wrong origin ⇒ no credential). |
| **Signing-key compromise** | Keys on disk 0600 under a hardened unit, never in DB/backups-in-clear; short access TTL caps the forgery window after rotation; emergency rotation drops the old key immediately (§5); RPs pin the JWKS URL over TLS, so forged tokens also require DNS/TLS compromise to introduce a fake JWKS. Detection: RP-side validation failures and accounts token-issuance audit rows diverging. |
| **Replayed WebAuthn challenges** | Lifted behavior is already correct: challenges are stored server-side and **consumed single-use inside the ceremony transaction** (`internal/auth/login.go:197`, `store.go:546`), 5-min TTL; sign-count regression logs clone warnings (`login.go:229`); BE-flag immutability enforced by go-webauthn (`docs/passkeys.md` §BE/BS). Native ceremonies use the same store. |
| **Account enumeration** | Lifted behavior preserved end-to-end: uniform "check your email" on register (`registration.go` — response identical for registered emails), structurally identical login options for unknown emails via discoverable fallback (`login.go:100-108`), 200-always on recovery start. New surfaces hold the line: `/oauth/native/start` uses the same discoverable fallback; callback errors at RPs are generic. |
| **Rate-limit evasion / ceremony abuse** | Lifted per-IP token buckets on all start endpoints (3/h, 10/min, 3/h — `docs/auth.md`); `/oauth/token` gains its own bucket (20/min/IP) since it is new surface. |
| **Accounts outage ≠ fleet lockout** | RP sessions outlive accounts downtime (identity check skips on connection failure, §11); only *rejection* revokes. Availability failure mode is "cannot start new sessions", not "everyone signed out". |

Residual risks accepted deliberately: (1) global sign-out latency up to 15 min at RPs (F6 revocation pings close it if ever needed); (2) single-host blast radius — host compromise is total, unchanged from the PRD's baseline posture; (3) email-based claim of legacy ShortLinks accounts (§9.4) trusts historical magic-link verification — acceptable for a deployment whose local-user count is one.

## 13. Phased delivery plan

Interleaves with the Website plan (PRD §12): Website Phases 0 and 2 (scaffold, brand/marketing) have no auth dependency and can run in parallel with F1–F2. Website Phase 1 is **replaced** by F3. ShortLinks can deploy standalone at go.opencircuitsf.com *now* (campaign links are wanted early — PRD §1) and federate later in F4; nothing below blocks that.

**Decide before F1 starts** (all have defaults in §15): accounts repo/module name; AuthKit repo/module name; confirmation that RP ID = `opencircuitsf.com` (irreversible once real users enroll — this is the one decision that cannot be walked back); sequencing choice (accounts-first vs www-local-first).

| Phase | Builds | What works at the end |
|---|---|---|
| **F1 — Accounts service, standalone** | Copy-and-strip ShortLinks → accounts repo; subject UUID + persisted user_handle migrations; `__Host-ocsf_sso`; SES v2 mailer; operator surface; deploy (DNS, vhost, unit, DB, backups) | You can register, sign in with a passkey, recover, and manage credentials at `https://accounts.opencircuitsf.com`. No RP uses it yet. The RP ID die is cast here. |
| **F2 — Token issuance + AuthKit** | `internal/oauth/` (authorize, token, revoke, JWKS, discovery, refresh rotation, clients.json); `keygen`; AuthKit `token`/`rp`/`idfake`; wire tests against `idfake` + a scratch RP | `curl` + a test RP complete the full code+PKCE dance; JWKS rotation procedure rehearsed once end-to-end. |
| **F3 — www integration** *(replaces Website Phase 1)* | Website issues #0065–#0072 (below): trimmed migrations, config, federated login/callback, shadow-user admin with role toggles, seed, dev mode | Staff sign in to www via accounts; `/admin/*` fully gated; second-property silent SSO demonstrated (accounts → www with no second passkey tap). Website Phases 3–7 proceed unchanged on top. |
| **F4 — ShortLinks federated mode** | `AUTH_MODE` switch (§9 in full), AuthKit consumption, JWT bearer path, migration of the go deployment (operator account claims via email match) | go.opencircuitsf.com runs federated: one identity signs into www and go; ShortLinks standalone CI green in both modes; ShortLinks the *product* still ships local-mode by default. |
| **F5 — Native app enablement** | Apex `/.well-known/` serving (AASA, assetlinks); `/oauth/native/start|finish`; app-origin entries in `WEBAUTHN_RP_ORIGINS`; refresh TTL tiering for native; (app-side: Keychain storage, passkey sheet) | An iOS build signs in with the platform passkey sheet — no browser — and calls the go API with `aud:"go"` bearer JWTs. Gate: only start F5 when an app actually exists. |
| **F6 — Hardening & conveniences** | Direct revocation pings accounts→RPs (instant global logout); key-rotation automation; monitoring (auth failure rates, token-reuse alarms); optional mailinglist split-out as RP #3 | Global sign-out is immediate everywhere; rotation is a script, not a runbook; the mailinglist seam is exercised only if the service actually splits. |

Nothing is built before it is needed: no native endpoints before an app, no back-channel logout before the latency is felt, no mailinglist service before the split earns its keep, no ROR file before a second registrable domain exists.

## 14. Proposed issues

New Website-repo issues, continuing the `issues/NNNN.md` convention (em-dash titles, metadata table with a Phase row, `## Relation` links). Work in the accounts repo and ShortLinks repo is tracked in **their own** `issues/` folders per `issues/Issues.md`'s scope rule ("go.opencircuitsf.com … issues do **not** belong here"); their lists are sketched after the table.

| # | Title | One-line summary | Phase | Depends on | Supersedes / changes |
|---|---|---|---|---|---|
| 0065 | Adopt the federated accounts architecture | Record the FEDERATED-ACCOUNTS.md decisions in PRD §2/§3.1/§5/§9 amendments; retire the www-local passkey plan | F3 (file now) | — | changes #0001, #0006 |
| 0066 | Strip the passkey ceremony stack from the www tree | Remove ceremony services/handlers/views after the #0001 copy; keep sessions, middleware, audit | F3 | #0001–#0003 | changes #0002, #0003; supersedes #0008 (with #0068) |
| 0067 | Reshape auth migrations for RP mode | users +`subject`; sessions +`refresh_token_enc`,`identity_checked_at`; drop credential/challenge/pending migrations | F3 | #0004 | changes #0004 |
| 0068 | Integrate AuthKit: federated login and callback | `/auth/federated/login` + `/callback`, state+PKCE cookie, shadow-row upsert, session mint, refresh sweep | F3 | #0066, #0067, AuthKit v0 (F2) | supersedes #0008 |
| 0069 | Rework the Login and Account views for federated sign-in | One-button login redirect; Account links to accounts for passkeys; drop ceremony views from routes | F3 | #0068, #0014 | changes #0003; supersedes #0008's UI criteria |
| 0070 | Shadow-user admin screen with per-property roles | `/admin/users` lists shadow rows; is_admin/active toggles with audit entries; no credential UI | F3 | #0068, #0009 | changes #0009 |
| 0071 | Seed the bootstrap admin as a shadow row | `opencircuit seed` pre-creates ADMIN_EMAIL shadow row (is_admin) claimed at first federated sign-in | F3 | #0067 | changes #0010 |
| 0072 | Config loader for RP mode | ACCOUNTS_ISSUER/OAUTH_* vars in; WEBAUTHN_*/SESSION_SECRET out; all-errors-at-once validation | F3 | #0007 | changes #0007 |
| 0073 | Session revocation endpoint for accounts back-channel | Internal POST that deletes sessions by subject, authenticated by shared secret; wired when F6 lands | F6 | #0068 | — |
| 0074 | Deployment: accounts vhost, apex .well-known, three-DB backups | Runbook + Apache changes: accounts vhost/unit/DB; apex serves `/.well-known/` without redirect; backup all three DBs | F1 (docs), F5 (apex files) | #0064 | changes #0064, #0062's runbook scope |

Sketch of the sibling trackers (filed there, not here): **Accounts repo** — scaffold from ShortLinks; subject+user_handle migrations; `__Host-` cookie + multi-origin config; SES v2 mailer; operator surface; `internal/oauth` (authorize/token/revoke/JWKS/discovery/refresh-rotation); keygen + rotation script; native start/finish; deploy. **AuthKit repo** — token verifier; rp handlers; idfake; wellknown helpers; v0 tag. **ShortLinks repo** — `AUTH_MODE` config + route branching; subject column migration; federated callback via AuthKit; JWT bearer acceptance; `/api/config` + Login/Account branching; email-claim migration; dual-mode CI smoke; docs.

## 15. Open questions

Only decisions that need the user's answer; everything else in this document is recommended as-is.

| # | Question | Blocks | Recommended default |
|---|---|---|---|
| 1 | **Repo/module names**: accounts service and shared library | F1 / F2 (module paths are expensive to change — same argument as HANDOFF §2.1) | `github.com/brennanMKE/Accounts` (binary `accounts`), `github.com/brennanMKE/AuthKit` (module `authkit`) |
| 2 | **Sequencing**: accounts-first (F1–F2 before www Phase 1) as recommended, or build www with local auth per the existing 64 issues and migrate later? | Whether #0065–#0072 replace or follow Phase 1 | Accounts-first. Building www-local auth means doing Website Phase 1 twice; the accounts service is the same copy-and-strip effort Phase 1 already budgeted, relocated. Website Phases 0 and 2 fill the calendar meanwhile. |
| 3 | **Registration policy at accounts**: staff-only (`registrations_enabled=false`, operator invites via the gate) or open registration for a future member-facing feature? | F1 config only | Ship closed (the lifted default — `migrations/000006` seeds `false`). Open it the day a member-facing feature needs it; nothing else changes. |
| 4 | **Deploy ShortLinks standalone at go.opencircuitsf.com now** (campaigns wanted early), federating in F4 — accepting the small §9.4 email-claim migration later? | Nothing (both orders work) | Yes — deploy standalone now; the only account to migrate later is yours. |
| 5 | **Native app platforms and timing** — is an iOS app real enough to schedule F5, and is Android in scope? | F5 only | Defer F5 until an app project exists; design already reserves everything it needs (multi-origin allowlist, native endpoints, apex .well-known). |
| 6 | **Display name at registration** — collect one at accounts, or email-only identity like ShortLinks today? | F1 schema (nullable column either way) | Add the nullable `display_name`, don't collect it at registration; editable on the account page later. |

---

*Prepared from: Website `PRD.md` (all 14 sections), `HANDOFF.md`, `issues/Issues.md` + issue files #0001–#0064; ShortLinks `README.md`, `docs/architecture.md`, `docs/auth.md`, `docs/passkeys.md`, `docs/configuration.md`, `docs/database.md`, `internal/auth/*` (store, login, registration, recovery, session, tokens, users, webauthn, mailer), `internal/middleware/*` (auth, bearer tests, devauth, ratelimit), `internal/config/config.go`, `internal/handlers/{auth,me,users}.go`, `cmd/shortlinks/main.go`, `migrations/000001–000013`, `web/src/lib/webauthn.ts`, `deploy/`, `scripts/`; and August 2026 web research (OAuth 2.1 draft-15, RFC 9700, draft-ietf-oauth-first-party-apps, WebAuthn L3 Related Origin Requests, platform passkey APIs, JWKS/refresh-rotation practice — sources cited inline in §§2, 5).*
