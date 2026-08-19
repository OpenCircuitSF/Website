# Database Schema & Migrations

PostgreSQL, schema managed by [`golang-migrate`](https://github.com/golang-migrate/migrate)
(`migrations/`, numbered `000001`–`000011`). No ORM — every
store issues hand-written SQL through `pgx/v5`.

## Applying migrations

```bash
migrate -path migrations -database "$DATABASE_URL" up      # apply all pending
migrate -path migrations -database "$DATABASE_URL" down -all  # reverse all (dev only)
migrate -version                                             # confirm the CLI is installed
```

`scripts/db/create.sql` creates the `opencircuit` role and database (run
once, as a Postgres superuser, before the first `migrate up`).
`scripts/db/drop.sql` tears both down for a clean local reset.
`internal/testdb` provides cross-process serialization for integration tests
sharing a `TEST_DATABASE_URL` database — run `go test ./...` with that
variable set to exercise every DB-backed test rather than skip it.

## Current schema (Phase 0 — auth and admin)

| Migration | Tables / change |
|---|---|
| `000001_create_users` | `users` — account records; the first registrant on a fresh install can be promoted to admin |
| `000002_create_auth_credentials` | `pending_registrations`, `passkey_credentials`, `webauthn_challenges` |
| `000003_create_sessions` | `sessions` — active authenticated sessions, looked up by cookie token on every request |
| `000004_create_settings` | `settings` — runtime configuration changeable without a restart (seeds `registrations_enabled`) |
| `000005_create_audit_log` | `audit_log` — append-only record of every significant action |
| `000006_passkey_backup_flags` | Adds `backup_eligible`/`backup_state` to `passkey_credentials` (go-webauthn's Backup Eligible flag is immutable per credential; must match at registration and every assertion) |
| `000007_enforce_session_passkey_not_null` | Enforces `NOT NULL` on `passkey_credentials.user_id` and four `sessions` columns — see the migration's own comments for the defensive existence-check-then-`ALTER` pattern and why its `down.sql` is a deliberate no-op |
| `000008_add_physical_address_setting` | Seeds `settings.physical_address` with an empty string. Empty means "not set": `#0045`'s send worker refuses to start a campaign until an admin fills it in, because CAN-SPAM §7704 requires a physical postal address in every commercial message. `ON CONFLICT DO NOTHING` keeps `up` idempotent |

This is a straight port of ShortLinks' auth schema, renumbered to a
contiguous range — ShortLinks' `links`, `clicks`, `campaigns`,
`url_filter_rules`, and UTM migrations were deleted outright in `#0004`
(they served a URL shortener this project doesn't have).

## Mailing list schema (Phase 3, per `PRD.md` §6.2)

| Migration | Tables / change |
|---|---|
| `000009_create_interests` | `interests` — the workshop interest taxonomy (`#0023`). Rows, not a Go enum: new themes appear constantly and adding one must not require a deploy. `slug` is CHECK-constrained to lowercase-hyphenated and UNIQUE. Seeds the twelve PRD §6.1 slugs via `ON CONFLICT (slug) DO NOTHING`, so `up` stays idempotent against an already-seeded database. Numbered ahead of `subscribers` because `subscriber_interests` (below) carries a foreign key to it |
| `000010_create_subscribers` | `subscribers` — list membership independent of any user account, plus the consent evidence (`signup_ip`, `signup_user_agent`, `created_at`, `confirmed_at`) a deliverability complaint needs answering (`#0025`, `#0075`'s privacy policy). `status` is CHECK-constrained to `pending \| active \| unsubscribed \| bounced \| complained`; nothing in `internal/subscribers` ever moves a subscriber out of `complained` (CLAUDE.md §9 — only a future admin path can). `confirm_token` and `manage_token` are both `UNIQUE`, 32 random bytes from `crypto/rand` (not an HMAC of the email, per PRD §6.4). `email` is CHECK-constrained to `lower(trim(email))` — Gmail dots and `+tag` suffixes are deliberately never stripped. Also creates `subscriber_interests` (composite PK, `ON DELETE CASCADE` both ways) — a subscriber with zero rows here is valid and expected (general announcements only, PRD §6.1) |
| `000011_add_subscribers_already_subscribed_sent_at` | Adds `subscribers.already_subscribed_sent_at`, a `TIMESTAMPTZ` claim column for the "you're already subscribed" email (`#0026` review finding 1). Tracks the last successful send the same way `confirm_sent_at` does, so `ClaimAlreadySubscribedSend`/`ReleaseAlreadySubscribedClaim` (`internal/subscribers`) apply the identical atomic claim-before-send cooldown to both outbound messages the signup endpoint can send |

`internal/interests` and `internal/subscribers` are the corresponding
Go store packages; see their package doc comments for the store-layer
normalization and transition rules layered on top of the constraints above.

## Planned schema (Phase 3+ continued, per `PRD.md` §6.2)

Not yet implemented. `PRD.md` §6.2 is the authoritative source for exact
column definitions; the tables it describes, roughly in build order:

- `suppressions` — the global suppression list checked before every send (`#0033`)
- `email_campaigns` and its send-tracking tables (`#0040`) — an unrelated
  concept from ShortLinks' `campaigns` table (link grouping), which this
  project never ported; the word is coincidental
- `workshops`

## Conventions

- One migration per logical change; `up`/`down` pairs are both required and
  must be tested (`migrate up` then `migrate down` then `migrate up` again,
  cleanly, with no orphaned objects).
- Never edit an already-applied migration's SQL in place — `golang-migrate`
  tracks version *numbers*, not file *content*, so an in-place edit silently
  diverges any database that already ran the old version from one that
  runs the edited version fresh. If a fix is needed, write a new migration.
  (`000007`'s own header explains a concrete incident of this class from
  ShortLinks' history, which this project's fresh migration set otherwise
  has no need to repeat.)
- A migration that could find data violating a new constraint (e.g. a new
  `NOT NULL`) should fail loudly before making any change, not guess or
  silently drop rows — see `000007_enforce_session_passkey_not_null.up.sql`
  for the pattern.
