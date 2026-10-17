# Configuration & Environment Variables

`internal/config.Load()` reads all runtime configuration from the process
environment (and a `.env` file in development, via `godotenv` — a missing
`.env` is not an error, since production sets variables via systemd
instead). It collects **every** validation error before returning, rather
than failing on the first one, so a broken environment reports its full set
of problems in one pass.

**This document is the reference; `.env.example` is the production
template** (decided 2026-09-04, `#0423`) — `docs/deployment.md`'s install
step is `sudo cp .env.example /etc/opencircuit/config.env`, so that file is
what production actually gets, and it now ships working values wherever one
is knowable and safe to publish, not illustrative ones. Read here for the
*why* behind a variable; `.env.example`'s own header comment records the
same split for anyone editing that file directly. The two must never
disagree about what the loader reads or requires — see the change note on
each row below that `#0423` touched.

## Variables

| Variable | Required | Default | Notes |
|---|---|---|---|
| `PORT` | no | `8080` | |
| `BASE_URL` | yes (non-dev) | — | Public base URL. Drives every canonical URL: `og:url`, `og:image`, `sitemap.xml` `<loc>`, and the `Sitemap:` line in `robots.txt`. Must match the host the browser is actually on — the **www** form in production, since the apex 301s to www; see the gotcha below |
| `DATABASE_URL` | yes, unless `STORAGE=json` | — | Postgres connection string |
| `STORAGE` | no | *(unset → Postgres)* | Only the literal value `json` selects the in-memory dev store. An empty/unset `DATABASE_URL` does **not** engage dev mode — production can never silently fall back to the in-memory store. Added to `.env.example` 2026-09-04 (`#0423`) — it was previously absent despite the loader reading it since `#0007`; production's own `config.env` was never affected, since an absent variable and an explicit `STORAGE=postgres` behave identically, but the gap meant `.env.example` no longer matched what `internal/config.Load()` actually reads. |
| `WEBAUTHN_RP_ID` | yes (non-dev) | — | WebAuthn relying-party ID — the apex domain, so a passkey stays valid across apex and www |
| `WEBAUTHN_RP_ORIGIN` | yes (non-dev) | — | Must match the browser's real origin exactly — the **www** form in production; see the gotcha below |
| `SESSION_SECRET` | yes (non-dev) | — | HMAC signing key for session cookies. Generate with `openssl rand -hex 32`. `.env.example` ships this **blank** rather than a placeholder string, so a copied template that's never had a real value set fails closed at startup (`config: missing required variable SESSION_SECRET`) instead of silently signing sessions with a key published in a public repository — see `#0067`. |
| `AWS_REGION` | yes | — | SES region — production runs `us-east-1` (`AGENTS.md` §7; corrected 2026-09-04, #0421, this row previously said "e.g. `us-west-2`", which was never the real region). No static credentials anywhere in configuration — the EC2 instance role supplies them via the AWS SDK's default credential chain; locally the chain falls back to `~/.aws/credentials`. |
| `SES_CONFIGURATION_SET` | **yes**, unless `SEND_WORKER_ENABLED=false` | — | SES configuration set used for transactional/campaign sends. Listed as optional here until 2026-08-25, when the first production boot proved otherwise: `mailing.NewSESMailer` returns `cannot construct SES mailer: missing SES_CONFIGURATION_SET` and the service crash-loops without it. The named set need not exist in SES yet. Currently `opencircuit-transactional`. `#0472`: leaving this unset while also setting `SEND_WORKER_ENABLED=false` is a deliberate, supported way to run with no SES at all — see that row below. Leaving it unset with `SEND_WORKER_ENABLED` at its default (`true`) still crash-loops exactly as before; the two conditions are both required so an operator who simply forgot this variable is not silently mistaken for one who wants no email. |
| `EMAIL_FROM` | yes | — | RFC 5322 From header. **The address must sit on a verified SES identity**, which is the `mailing.` subdomain, not the apex — production runs `Open Circuit SF <contact@mailing.opencircuitsf.com>` with `EMAIL_REPLY_TO=contact@opencircuitsf.com`, so replies reach the Google Workspace inbox `#0271` made the public contact address. `.env.example` ships this same working subdomain value (corrected 2026-09-04, `#0423` — it previously shipped the apex form "for readability", which cannot send at all). See `docs/email-setup.md` |
| `EMAIL_REPLY_TO` | no | — | Reply-To header for outbound mail |
| `EMAIL_LIST_DOMAIN` | yes | — | Subdomain used for inbound unsubscribe handling, e.g. `lists.opencircuitsf.com`. Interpolated into the `mailto:` form of every campaign's `List-Unsubscribe` header (`mailing.CampaignHeaders`, `#0035`); required with no default (`#0105`) so a misconfigured deploy fails loud at boot instead of emitting a malformed `mailto:unsubscribe@?subject=…` header. Never point the apex MX at SES — see `AGENTS.md` §9. |
| `SES_INBOUND_BUCKET` | no | — | S3 bucket SES writes inbound mail to (PRD §6.5 path 3, `#0057`/`#0058`) — production value `opencircuitsf-inbound`, under the key prefix `unsubscribe/`. `internal/inbound.NewS3Store` treats an empty value the same as `SESEventsTopicARN` below: not a boot failure, just nothing to fetch successfully until it's set. As of 2026-09-11 the bucket exists in AWS but this variable is not yet set in production `config.env` — see `docs/email-setup.md`'s "Inbound unsubscribe" runbook. |
| `SES_INBOUND_TOPIC_ARN` | no | *(unset → reject all)* | The SNS topic ARN `POST /api/ses/inbound` (`#0058`) accepts notifications from — the `unsubscribe` receipt rule's own S3 action `TopicArn` (`#0057`). Production value `arn:aws:sns:us-east-1:AWS_ACCOUNT_ID:opencircuit-inbound-mail` — the account id is a placeholder, redacted from this public repo per `#0511` and held in the private repo brennanMKE/AWS. Deliberately a **separate** value from `SES_EVENTS_TOPIC_ARN` below, never shared: `internal/handlers` dispatches SES message types by `TopicArn`, so one shared topic would let a bounce/complaint notification and an inbound-mail notification satisfy each other's verifier. Same not-yet-provisioned convention as `SES_EVENTS_TOPIC_ARN`: empty means `sesnotify.Verifier` rejects every inbound message rather than refusing to boot. Present in `.env.example` since `#0058`; not yet added to `docs/configuration.md` until this pass, and not yet set in production `config.env` — both gated on `#0057`'s AWS objects existing, see `docs/email-setup.md`. |
| `SES_EVENTS_TOPIC_ARN` | no | *(unset → reject all)* | The single SNS topic ARN `internal/sesnotify` accepts SES bounce/complaint notifications from (`#0037`/`#0107`). A valid SNS signature only proves SNS sent a message, not that it arrived on *our* topic — any AWS account can subscribe our public endpoint to a topic of their own — so `sesnotify` also checks `TopicArn` against this value. Deliberately not in the required set above: the topic didn't exist yet when this variable was added (`AGENTS.md` §10 item 2), and an empty value means every notification is rejected rather than the service failing to boot. Added to `.env.example` 2026-09-04 (`#0423`) — it was previously absent despite the loader reading it since `#0037`. |
| `MAILER_NOOP` | no | `false` | `true` selects `auth.NoOpMailer` instead of the real SES v2 API mailer on the Postgres serve path (`#0027`). For local development against Postgres before SES is set up (`AGENTS.md` §10 item 2) — `NoOpMailer` logs the full verification/recovery link to stdout, which is how `#0008`'s manual passkey verification procedure reads the magic link. Production leaves this unset. As of `#0045`, this also refuses to start the send worker at all: `noOpMailingMailer.Send` returns the literal message id `"noop"`, which would poison `#0038`'s bounce/complaint join key and `#0049`'s stats if it ever reached `email_sends.ses_message_id`. |
| `DEV_ADMIN_LOGIN` | no | `false` | `true` establishes an admin session for `ADMIN_EMAIL` on every request on the Postgres serve path, with no passkey ceremony — the `STORAGE=postgres` counterpart to `STORAGE=json`'s always-on `DevAutoLogin`, closing the gap where the mailing subsystem (interests, subscribers, campaigns, suppressions — `AGENTS.md` §5) has no dev store to exercise it against (`#0402`). Guarded exactly like `MAILER_NOOP` above: the service refuses to start at all — not just to skip the bypass — when this is `true` and `BASE_URL`'s host is anything but `localhost`/`127.0.0.1` (reuses `mailerNoOpAllowed`'s own host predicate, `cmd/opencircuit/main.go`). Never creates the account it targets — startup fails with a named error (`opencircuit seed`, which only creates the missing row, or `scripts/db-reset.sh`, which drops and rebuilds the whole database) if `ADMIN_EMAIL` has no existing, active row. `scripts/dev.sh --postgres` sets this to `true` automatically; see `docs/dev.md`'s "Running against Postgres" section. Production leaves this unset. |
| `SES_SANDBOX` | no | `true` | No live AWS call anywhere in this codebase can detect sandbox status, so this is a manually-set operational flag: leave it `true` until AWS grants SES production access (`AGENTS.md` §10 item 2), then flip it to `false`. The admin overview dashboard (`#0061`) warns while this is `true`. Added to `.env.example` and here 2026-09-04 (`#0423`) — it was previously absent from both despite the loader reading it since `#0038`/`#0061`. |
| `MAX_SEND_RATE` | no | `10` | Messages/second ceiling; keep below the SES quota. This is the environment-level ceiling, not the operator dial — see below. |
| `SEND_BATCH_SIZE` | no | `50` | Messages per send-worker batch |
| `SEND_WORKER_ENABLED` | no | `true` | Set `false` on a second instance to avoid double-sending. Has no effect when `MAILER_NOOP=true` — the worker never starts regardless (see above). `#0472`: set `false` together with a blank `SES_CONFIGURATION_SET` to turn SES off entirely (`AGENTS.md` §10 item 2's prescribed recipe, made to actually boot as of this issue). `sesSender` becomes `mailing.UnconfiguredMailer`, which fails every `Send` loudly (`mailing.ErrSESUnconfigured`) instead of sending for real or silently no-opping the way `MAILER_NOOP` does — registration/recovery/etc. still enqueue onto `outbound_queue` and simply retry-then-abandon on its existing backoff, the same outcome already documented above for "a magic link requested before SES exists." `SEND_WORKER_ENABLED=false` with `SES_CONFIGURATION_SET` still set constructs the real SES mailer as usual — this combination is scoped to an actually-unconfigured SES, not to the flag alone. |
| `SHORTLINKS_BASE_URL` | no | *(unset → outreach unavailable)* | Base URL of the ShortLinks server whose REST API `internal/shortlinks` calls (`#0543`) — `https://go.opencircuitsf.com` in production, `http://localhost:5173` against a local dev server. Fallback when no value is saved in admin Settings → Outreach connections. When this or the token is blank, `shortlinks.New` returns a nil client, outreach is reported unavailable, and the service boots normally. |
| `SHORTLINKS_API_TOKEN` | no | *(unset → outreach unavailable)* | ShortLinks API token (`slk_…`), sent only as `Authorization: Bearer`. A secret: never log, commit, or paste it; the client never includes it in an error. Create one under Account → API tokens on the ShortLinks site, and prefer a dedicated token per environment. Fallback when no value is saved in admin Settings → Outreach connections. |
| `BLUESKY_HANDLE` | no | *(unset → Bluesky falls back to manual)* | Handle of the Bluesky account Outreach posts as (`#0545`). Fallback when no value is saved in admin Settings → Outreach connections. The Bluesky channel is "configured" only when this and the app password are both set; otherwise it shows "not configured" in the Outreach panel and behaves like a manual channel, and boot logs one warning naming the missing variable. |
| `BLUESKY_APP_PASSWORD` | no | *(unset → Bluesky falls back to manual)* | A Bluesky **app password** (Settings → Privacy and security → App passwords), never the account password. A secret: never log, commit, or paste it; the client never includes it, or any session token, in an error. Fallback when no value is saved in admin Settings → Outreach connections. |
| `BLUESKY_PDS_URL` | no | `https://bsky.social` | Base URL of the PDS to log in to and post through. Only change it for a self-hosted PDS. Fallback when no value is saved in admin Settings → Outreach connections. |
| `MASTODON_BASE_URL` | no | *(unset → Mastodon falls back to manual)* | Base URL of the Mastodon instance Outreach posts to (`https` required; `http` only for `localhost`). Fallback when no value is saved in admin Settings → Outreach connections. |
| `MASTODON_TOKEN` | no | *(unset → Mastodon falls back to manual)* | Access token for the posting account (Preferences → Development, scopes `write:statuses read:statuses read:notifications profile` — the two read scopes are for the engagement tracker, `#0547`, and `profile` (or `read:accounts` on instances older than 4.3) is for Test connection in Settings; without a read scope every read is a 403, which the tracker logs naming the scope), sent only as `Authorization: Bearer`. A secret, handled as the Bluesky password is. Fallback when no value is saved in admin Settings → Outreach connections. |
| `ADMIN_EMAIL` | yes | — | Pre-authorized as admin on first registration when no users exist yet |
| `MEDIA_DIR` | no | *(unset → 503)* | Directory `POST /admin/media/upload` (`#0433`) writes EXIF-stripped image uploads to — `/var/www/media` in production, once `#0465`'s group-ownership and `ReadWritePaths=` change lands. Left unset, the endpoint refuses with a named 503 rather than the service failing to boot or the route being silently omitted; the `scp` workflow (`docs/media.md`) is unaffected either way. Every test in `internal/media`/`internal/handlers` points this at a `t.TempDir()`. |

**Outreach connections in Settings (`#0564`).** The seven outreach variables
above (ShortLinks, Bluesky, Mastodon) can also be saved in admin Settings →
Outreach connections, which stores them in the `outreach_connection_values`
table and applies them immediately, with no restart. Precedence is per field:
a value saved in Settings wins; otherwise the environment variable is used
when it is non-blank; otherwise the built-in default (only
`BLUESKY_PDS_URL` has one); otherwise the field is unset. Clearing a saved
value in Settings deletes it, so the field falls back to its environment
variable. There is no override that turns off a channel configured through
the environment: to disable it, remove the variable from `config.env`.
Secrets (the ShortLinks token, the Bluesky app password, the Mastodon token)
are write-only: Settings shows only "Set" or "Not set" and where the value
comes from, and no API response, log line, or audit entry carries one. They
are stored in plaintext, protected like the rest of the database and its
backups, because the only available encryption key would be an environment
variable on the same box as the database; each can be revoked at its
service, and revisit this if backups ever leave operator-controlled hosts.
A malformed value no longer stops the service from booting: it leaves only
its own group unconfigured, logs one warning naming the group (never a
value), and Settings shows the group's error. Test connection checks the
saved values with a harmless read (ShortLinks `GET /api/me`, a Bluesky
sign-in, Mastodon `verify_credentials`), and the Mastodon token needs the
`profile` scope (or `read:accounts` on instances older than 4.3) for it.

**Outreach engagement tracking (`#0547`) has no variables of its own.** Each
half runs exactly when its channel's client is configured (Bluesky: handle and
app password; Mastodon: base URL and token). Every 15 minutes it reads mention,
reply and quote notifications, and it captures likes, reposts, replies and
quotes for each automatically posted post hourly for 7 days, then daily to 30
days, then stops. Bluesky counts come from the public AppView
(`public.api.bsky.app`), unauthenticated; Mastodon counts come from our own
instance's copy of the status, so they can lag what other servers show. A post
marked posted by hand has no remote id and is never tracked. Nothing is
polled until a channel is configured, and none of it has been exercised
against a live platform: it is tested against local fakes only.

**`WEBAUTHN_RP_ID` and `WEBAUTHN_RP_ORIGIN` are not interchangeable.** A
mismatch between `RP_ORIGIN` and the browser's actual origin fails every
passkey ceremony with an opaque error — check this first if a Phase 1
ceremony fails (`AGENTS.md` §7).

**`BASE_URL` must be the www form, not the apex.** Production 301s the apex
to `https://www.opencircuitsf.com` (`AGENTS.md` §7). `internal/seo` trusts
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
requested (`AGENTS.md` §10 item 2), the account is capped at 1 message/second
and 200/day, and can only send to verified addresses. The default
`MAX_SEND_RATE=10` exceeds that 1/s cap, so set `MAX_SEND_RATE=1` (and
correspondingly keep the `max_send_rate` setting at `1` or below) while
developing against the sandbox — the worker does not detect which mode the
account is in; it simply obeys the configured rate and backs off on the
`ThrottlingException` the sandbox produces when exceeded.

See `PRD.md` §9 for the source PRD listing and `issues/0007.md` for the issue
that landed this variable set.
