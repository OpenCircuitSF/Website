# Configuration & Environment Variables

`internal/config.Load()` reads all runtime configuration from the process
environment (and a `.env` file in development, via `godotenv` — a missing
`.env` is not an error, since production sets variables via systemd
instead). It collects **every** validation error before returning, rather
than failing on the first one, so a broken environment reports its full set
of problems in one pass.

## Current variables (carried over from ShortLinks, Phase 0)

| Variable | Required | Default | Notes |
|---|---|---|---|
| `PORT` | no | `8080` | |
| `BASE_URL` | yes (non-dev) | — | Public base URL |
| `DATABASE_URL` | yes, unless `STORAGE=json` | — | Postgres connection string |
| `STORAGE` | no | *(unset → Postgres)* | Only the literal value `json` selects the in-memory dev store. An empty/unset `DATABASE_URL` does **not** engage dev mode — production can never silently fall back to the in-memory store. |
| `WEBAUTHN_RP_ID` | yes (non-dev) | — | WebAuthn relying-party ID — the apex domain |
| `WEBAUTHN_RP_ORIGIN` | yes (non-dev) | — | Must match the browser's real origin exactly; see the gotcha below |
| `SESSION_SECRET` | yes (non-dev) | — | HMAC signing key (`openssl rand -hex 32`) |
| `SES_SMTP_HOST` / `SES_SMTP_PORT` / `SES_SMTP_USERNAME` / `SES_SMTP_PASSWORD` | no | port `587` | SMTP transport for transactional email; a stdout `NoOpMailer` is used when username/password are blank (dev default) |
| `EMAIL_FROM` | no | — | |
| `ADMIN_EMAIL` | yes | — | Pre-authorized as admin on first registration when no users exist yet |

**`WEBAUTHN_RP_ID` and `WEBAUTHN_RP_ORIGIN` are not interchangeable.** A
mismatch between `RP_ORIGIN` and the browser's actual origin fails every
passkey ceremony with an opaque error — check this first if a Phase 1
ceremony fails (`CLAUDE.md` §7).

## Leftover fields pending #0007

`Config` still carries `CacheMaxCost`/`CacheTTLSeconds` (`CACHE_MAX_COST`/
`CACHE_TTL_SECONDS`) from the ShortLinks redirect cache, which `#0002`
deleted (`internal/cache` is gone). These fields are unused dead weight, not
a functional dependency — nothing reads them anymore. `#0007` ("Extend the
config loader for the new environment variables") is the issue that both
removes them and adds this project's own variables.

## Planned variables (Phase 3+, per `PRD.md` §9)

Not yet implemented — listed here so `#0007` and later phases have a
documented target, and so a reader doesn't have to reconstruct the full
`.env` shape from the PRD each time.

```env
# ── AWS / SES ──────────────────────────────────────────────────────────────
AWS_REGION=us-west-2
SES_CONFIGURATION_SET=opencircuit-transactional
EMAIL_FROM=Open Circuit SF <hello@opencircuitsf.com>
EMAIL_REPLY_TO=hello@opencircuitsf.com
EMAIL_LIST_DOMAIN=lists.opencircuitsf.com
SES_INBOUND_BUCKET=opencircuitsf-inbound
# No static credentials — the EC2 instance role provides them.

# ── Sending ────────────────────────────────────────────────────────────────
MAX_SEND_RATE=10          # messages/second, keep below the SES quota
SEND_BATCH_SIZE=50
SEND_WORKER_ENABLED=true  # false on a second instance to avoid double-sending
```

Runtime-editable values (admin-changeable without a restart or redeploy)
live in the `settings` table, not the environment: `registrations_enabled`
(already implemented), and — once their subsystems land —
`physical_address`, `max_send_rate`, `signup_enabled`, `default_from_name`.

See `PRD.md` §9 for the full list and `#0007`'s acceptance criteria for what
that issue specifically adds.
