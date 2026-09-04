# Configuration & Environment Variables

`internal/config.Load()` reads all runtime configuration from the process
environment (and a `.env` file in development, via `godotenv` — a missing
`.env` is not an error, since production sets variables via systemd
instead). It collects **every** validation error before returning, rather
than failing on the first one, so a broken environment reports its full set
of problems in one pass.

## Variables

| Variable | Required | Default | Notes |
|---|---|---|---|
| `PORT` | no | `8080` | |
| `BASE_URL` | yes (non-dev) | — | Public base URL. Drives every canonical URL: `og:url`, `og:image`, `sitemap.xml` `<loc>`, and the `Sitemap:` line in `robots.txt`. Must match the host the browser is actually on — the **www** form in production, since the apex 301s to www; see the gotcha below |
| `DATABASE_URL` | yes, unless `STORAGE=json` | — | Postgres connection string |
| `STORAGE` | no | *(unset → Postgres)* | Only the literal value `json` selects the in-memory dev store. An empty/unset `DATABASE_URL` does **not** engage dev mode — production can never silently fall back to the in-memory store. |
| `WEBAUTHN_RP_ID` | yes (non-dev) | — | WebAuthn relying-party ID — the apex domain, so a passkey stays valid across apex and www |
| `WEBAUTHN_RP_ORIGIN` | yes (non-dev) | — | Must match the browser's real origin exactly — the **www** form in production; see the gotcha below |
| `SESSION_SECRET` | yes (non-dev) | — | HMAC signing key for session cookies. Generate with `openssl rand -hex 32`. `.env.example` ships this **blank** rather than a placeholder string, so a copied template that's never had a real value set fails closed at startup (`config: missing required variable SESSION_SECRET`) instead of silently signing sessions with a key published in a public repository — see `#0067`. |
| `AWS_REGION` | yes | — | SES region — production runs `us-east-1` (`CLAUDE.md` §7; corrected 2026-09-04, #0421, this row previously said "e.g. `us-west-2`", which was never the real region). No static credentials anywhere in configuration — the EC2 instance role supplies them via the AWS SDK's default credential chain; locally the chain falls back to `~/.aws/credentials`. |
| `SES_CONFIGURATION_SET` | **yes** | — | SES configuration set used for transactional/campaign sends. Listed as optional here until 2026-08-25, when the first production boot proved otherwise: `mailing.NewSESMailer` returns `cannot construct SES mailer: missing SES_CONFIGURATION_SET` and the service crash-loops without it. The named set need not exist in SES yet. Currently `opencircuit-transactional` |
| `EMAIL_FROM` | yes | — | RFC 5322 From header. **The address must sit on a verified SES identity**, which is the `mailing.` subdomain, not the apex — production runs `Open Circuit SF <contact@mailing.opencircuitsf.com>` with `EMAIL_REPLY_TO=contact@opencircuitsf.com`, so replies reach the Google Workspace inbox `#0271` made the public contact address. `.env.example` shows the apex form for readability; do not copy it to a host without verifying that identity in SES first. See `docs/email-setup.md` |
| `EMAIL_REPLY_TO` | no | — | Reply-To header for outbound mail |
| `EMAIL_LIST_DOMAIN` | yes | — | Subdomain used for inbound unsubscribe handling, e.g. `lists.opencircuitsf.com`. Interpolated into the `mailto:` form of every campaign's `List-Unsubscribe` header (`mailing.CampaignHeaders`, `#0035`); required with no default (`#0105`) so a misconfigured deploy fails loud at boot instead of emitting a malformed `mailto:unsubscribe@?subject=…` header. Never point the apex MX at SES — see `CLAUDE.md` §9. |
| `SES_INBOUND_BUCKET` | no | — | S3 bucket SES writes inbound mail to |
| `MAILER_NOOP` | no | `false` | `true` selects `auth.NoOpMailer` instead of the real SES v2 API mailer on the Postgres serve path (`#0027`). For local development against Postgres before SES is set up (`CLAUDE.md` §10 item 2) — `NoOpMailer` logs the full verification/recovery link to stdout, which is how `#0008`'s manual passkey verification procedure reads the magic link. Production leaves this unset. As of `#0045`, this also refuses to start the send worker at all: `noOpMailingMailer.Send` returns the literal message id `"noop"`, which would poison `#0038`'s bounce/complaint join key and `#0049`'s stats if it ever reached `email_sends.ses_message_id`. |
| `MAX_SEND_RATE` | no | `10` | Messages/second ceiling; keep below the SES quota. This is the environment-level ceiling, not the operator dial — see below. |
| `SEND_BATCH_SIZE` | no | `50` | Messages per send-worker batch |
| `SEND_WORKER_ENABLED` | no | `true` | Set `false` on a second instance to avoid double-sending. Has no effect when `MAILER_NOOP=true` — the worker never starts regardless (see above). |
| `ADMIN_EMAIL` | yes | — | Pre-authorized as admin on first registration when no users exist yet |

**`WEBAUTHN_RP_ID` and `WEBAUTHN_RP_ORIGIN` are not interchangeable.** A
mismatch between `RP_ORIGIN` and the browser's actual origin fails every
passkey ceremony with an opaque error — check this first if a Phase 1
ceremony fails (`CLAUDE.md` §7).

**`BASE_URL` must be the www form, not the apex.** Production 301s the apex
to `https://www.opencircuitsf.com` (`CLAUDE.md` §7). `internal/seo` trusts
`BASE_URL` verbatim for every `og:url`, `og:image`, and sitemap `<loc>` it
emits, so an apex `BASE_URL` publishes canonical URLs that immediately
redirect — diluting the canonical signal social crawlers and search engines
rely on. `#0072` found `.env.example` shipping the apex form despite
`WEBAUTHN_RP_ORIGIN` two lines below already getting this right.

## Removed from the ShortLinks skeleton (`#0007`)

Phase 0 ported ShortLinks' config loader unchanged. `#0007` removed the
variables that don't belong to this project:

- `SES_SMTP_HOST`, `SES_SMTP_PORT`, `SES_SMTP_USERNAME`, `SES_SMTP_PASSWORD`
  — ShortLinks sent transactional email over SMTP with IAM-derived SMTP
  credentials. This project has no static AWS credentials anywhere (the EC2
  instance role supplies them for API calls), so the SMTP-credential model
  doesn't apply; `#0027` replaces the SMTP-based `auth.SESMailer` with a
  mailer built on the SES v2 API, authenticated via the instance role.
- `CACHE_MAX_COST`, `CACHE_TTL_SECONDS` — ShortLinks' redirect cache
  variables. `internal/cache` was deleted in `#0002`; nothing read these
  fields.

These variables now have no effect on `Config` even if still present in a
stale environment (e.g. a leftover systemd `EnvironmentFile` line) —
`internal/config/config_test.go`'s `TestLoad_RemovedVariablesIgnored`
guards this.

## Runtime-editable values

Values an admin can change without a restart or redeploy live in the
`settings` table, not the environment: `registrations_enabled`,
`physical_address`, `max_send_rate`, `signup_enabled`, `default_from_name`.
`MAX_SEND_RATE` in the environment is the hard ceiling; the `settings` table's
`max_send_rate` value is the operator dial beneath it — the send worker
(`#0045`) computes `min(MAX_SEND_RATE, settings.max_send_rate)` fresh once
per batch, so an operator can throttle a running send without a restart. A
missing, blank, non-numeric, or non-positive `max_send_rate` row falls back
to `MAX_SEND_RATE`, never to unbounded; `PATCH /admin/settings` also rejects
a non-integer or out-of-range (`1..1000`) value for this key at write time
(`internal/handlers/settings.go`'s `validSettingValue`).

**Developing against the SES sandbox.** Before SES production access is
requested (`CLAUDE.md` §10 item 2), the account is capped at 1 message/second
and 200/day, and can only send to verified addresses. The default
`MAX_SEND_RATE=10` exceeds that 1/s cap, so set `MAX_SEND_RATE=1` (and
correspondingly keep the `max_send_rate` setting at `1` or below) while
developing against the sandbox — the worker does not detect which mode the
account is in; it simply obeys the configured rate and backs off on the
`ThrottlingException` the sandbox produces when exceeded.

See `PRD.md` §9 for the source PRD listing and `issues/0007.md` for the issue
that landed this variable set.
