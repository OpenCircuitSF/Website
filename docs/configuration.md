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
| `BASE_URL` | yes (non-dev) | — | Public base URL |
| `DATABASE_URL` | yes, unless `STORAGE=json` | — | Postgres connection string |
| `STORAGE` | no | *(unset → Postgres)* | Only the literal value `json` selects the in-memory dev store. An empty/unset `DATABASE_URL` does **not** engage dev mode — production can never silently fall back to the in-memory store. |
| `WEBAUTHN_RP_ID` | yes (non-dev) | — | WebAuthn relying-party ID — the apex domain, so a passkey stays valid across apex and www |
| `WEBAUTHN_RP_ORIGIN` | yes (non-dev) | — | Must match the browser's real origin exactly — the **www** form in production; see the gotcha below |
| `SESSION_SECRET` | yes (non-dev) | — | HMAC signing key for session cookies. Generate with `openssl rand -hex 32`. `.env.example` ships this **blank** rather than a placeholder string, so a copied template that's never had a real value set fails closed at startup (`config: missing required variable SESSION_SECRET`) instead of silently signing sessions with a key published in a public repository — see `#0067`. |
| `AWS_REGION` | yes | — | SES region, e.g. `us-west-2`. No static credentials anywhere in configuration — the EC2 instance role supplies them via the AWS SDK's default credential chain; locally the chain falls back to `~/.aws/credentials`. |
| `SES_CONFIGURATION_SET` | no | — | SES configuration set used for transactional/campaign sends |
| `EMAIL_FROM` | yes | — | RFC 5322 From header, e.g. `Open Circuit SF <hello@opencircuitsf.com>` |
| `EMAIL_REPLY_TO` | no | — | Reply-To header for outbound mail |
| `EMAIL_LIST_DOMAIN` | no | — | Subdomain used for inbound unsubscribe handling, e.g. `lists.opencircuitsf.com`. Never point the apex MX at SES — see `CLAUDE.md` §9. |
| `SES_INBOUND_BUCKET` | no | — | S3 bucket SES writes inbound mail to |
| `MAX_SEND_RATE` | no | `10` | Messages/second ceiling; keep below the SES quota. This is the environment-level ceiling, not the operator dial — see below. |
| `SEND_BATCH_SIZE` | no | `50` | Messages per send-worker batch |
| `SEND_WORKER_ENABLED` | no | `true` | Set `false` on a second instance to avoid double-sending |
| `ADMIN_EMAIL` | yes | — | Pre-authorized as admin on first registration when no users exist yet |

**`WEBAUTHN_RP_ID` and `WEBAUTHN_RP_ORIGIN` are not interchangeable.** A
mismatch between `RP_ORIGIN` and the browser's actual origin fails every
passkey ceremony with an opaque error — check this first if a Phase 1
ceremony fails (`CLAUDE.md` §7).

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
value is the operator dial beneath it.

See `PRD.md` §9 for the source PRD listing and `issues/0007.md` for the issue
that landed this variable set.
