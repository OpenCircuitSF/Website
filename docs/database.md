# Database Schema & Migrations

PostgreSQL, schema managed by [`golang-migrate`](https://github.com/golang-migrate/migrate)
(`migrations/`, numbered `000001`–`000019`). No ORM — every
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
| `000012_create_suppressions` | `suppressions` — a global send-block list, keyed by normalized email address rather than subscriber id so it survives both resubscribe attempts and hard deletion of the subscriber row (`#0060`); no foreign key to `subscribers` (`#0033`, PRD §6.2). `reason` is CHECK-constrained to `hard_bounce \| complaint \| manual \| repeated_soft_bounce`. `email` carries the identical `lower(trim(email))` CHECK as `subscribers.email` (carried in from `#0025`'s review) so a differently-cased row can never silently fail to match at the send gate. Its primary key was `email` alone at creation, later widened by `000013` below |
| `000013_scope_suppressions_by_reason` | Widens `suppressions`' primary key from `(email)` to `(email, reason)` (`#0100`). Under the single-column key, `Add`'s first-writer-wins upsert meant only the FIRST reason recorded for an address ever persisted — a later `hard_bounce` for an already-`complained` address silently no-opped, so clearing the complaint deleted the only row and let mail resume to a permanently-failing address. No data migration: the old `(email)` uniqueness already implies `(email, reason)` uniqueness, so the widened key can never conflict with existing rows. `down.sql` intentionally fails if any address carries more than one reason, rather than silently discarding a suppression to narrow the key back |
| `000014_create_email_events` | `email_events` — the raw SES/SNS bounce/complaint/delivery event log (`#0038`, PRD §6.7), written before interpretation so a bug in the interpretation logic is recoverable from the evidence already stored. Deduped by `UNIQUE (sns_message_id, recipient)` — SNS's at-least-once delivery means the identical message can arrive again, and a single `Bounce`/`Complaint` carries an array of recipients, so the key is composite rather than keyed on `sns_message_id` alone. `ses_message_id` (SES's id for the outbound email) is a plain indexed column, not part of the dedupe key — it's `#0049`'s reconciliation key, joined read-side against `email_sends` once that table exists (Phase 5). `event_type` is deliberately unconstrained (SES adds event types over time, unlike `subscribers.status`'s closed set). A partial index on `(recipient, received_at DESC) WHERE event_type = 'Bounce' AND bounce_type = 'Transient'` is added here for `#0039`'s soft-bounce counting query, so that issue is a pure read against a table it never has to alter |
| `000015_seed_soft_bounce_settings` | Seeds two `settings` rows for `#0039`'s repeated-soft-bounce threshold: `soft_bounce_threshold_count` (`5`) and `soft_bounce_threshold_window_days` (`30`), matching PRD §6.5's "5 soft bounces in 30 days". `UpdateSetting` only mutates an existing key, so both rows must exist before `PATCH /admin/settings` can change them — the same reason `000008` seeds `physical_address`. No table changes; `email_events` and its partial index already exist from `000014` |
| `000016_widen_soft_bounce_counting` | Replaces `000014`'s `idx_email_events_soft_bounce` partial index with one matching `#0109`'s widened counting predicate: `bounce_type IN ('Transient', 'Undetermined')` (PRD §6.5 says "5 soft bounces", not "5 transient bounces" — an address that only ever produces `Undetermined` bounces must not go unsuppressed forever) `AND (bounce_subtype IS NULL OR bounce_subtype NOT IN ('MessageTooLarge', 'ContentRejected', 'AttachmentRejected'))` (those three describe a fault in OUR message, not evidence the recipient's address is bad, and must never suppress a live subscriber). `000014` is append-only and untouched; this migration only drops and recreates the index under the same name. No table changes |

`internal/interests` and `internal/subscribers` are the corresponding
Go store packages; see their package doc comments for the store-layer
normalization and transition rules layered on top of the constraints above.

## Campaign schema (Phase 5, per `PRD.md` §6.2)

| Migration | Tables / change |
|---|---|
| `000017_create_campaigns` | `email_campaigns`, `campaign_interests`, `email_sends` (`#0040`). `email_campaigns.status` is CHECK-constrained to `draft \| scheduled \| sending \| sent \| canceled \| failed`; `audience_mode` to `all \| any_of \| all_of \| none_selected` (default `any_of`, per PRD §6.2). `workshop_id` is a plain nullable `BIGINT` with no `REFERENCES` clause — `workshops` doesn't exist until Phase 6, and PRD §6.2's migration-ordering note says to keep Phase 5 independently deployable and attach the FK with a later `ALTER TABLE` (`#0050`) rather than reorder the phases. `campaign_interests` is a join table with `ON DELETE CASCADE` on both `campaign_id` and `interest_id`. `email_sends` is one row per `(campaign, subscriber)`, materialized when a send starts (`#0044`); `UNIQUE (campaign_id, subscriber_id)` is the idempotency guarantee that makes re-materializing after a crash safe under `ON CONFLICT ... DO NOTHING` (never `DO UPDATE`, which would be a documented path from `sent` back to `queued`). `status` is CHECK-constrained to `queued \| sent \| failed \| skipped` — `skipped` was carried in from `#0044`'s planning pass so that issue's send-time re-check (a recipient who was eligible at materialization but has since unsubscribed or been suppressed) doesn't need its own `ALTER TABLE`. `bounced`/`complained` were never reachable (SES bounce/complaint events land only in `email_events`, `#0038`) and were removed from this CHECK by `#0131`, edited directly into this migration rather than stacked, since the project was still greenfield. `idx_email_sends_queued` is a partial index on `(campaign_id, id) WHERE status = 'queued'` for the send worker's `FOR UPDATE SKIP LOCKED` claim query; `idx_email_sends_message_id` on `ses_message_id` serves `#0049`'s SES-event reconciliation |
| `000018_add_campaign_send_state` | Adds `email_campaigns.materialized_at` (the "materialize exactly once" marker `#0045`'s send worker needs to tell "audience complete" from "crashed halfway through materializing") and `email_campaigns.test_sent_at` (the `no_test_send` preflight gate's storage — `#0045` reads it, `#0046` writes it). Widens `email_sends_status_check` to add a fifth value, `sending` — the per-row claim state between `queued` and `sent`/`failed` the worker's atomic per-row claim (`UPDATE ... SET status='sending', attempts=attempts+1, claimed_at=now() WHERE id=$1 AND status='queued'`) requires. Adds `email_sends.claimed_at` (`#0122`, edited into this migration in place — greenfield until first deploy, CLAUDE.md §1's append-only rule applies once deployed, not before): the timestamp `OrphanSweep` uses to tell a crashed worker's abandoned `sending` row (`claimed_at` older than `worker.go`'s `orphanStaleAfter`, or `NULL`) from a live worker's in-flight one, which it must never un-claim. `down.sql` drops `claimed_at` and resets any `sending` rows to `queued` before narrowing the CHECK back, or a rollback against a database that was mid-send would fail. Seeds two settings rows matching `000008`'s `ON CONFLICT DO NOTHING` idiom: `max_send_rate = '10'` (PRD §6.6's default, the worker's operator-editable rate dial) and `default_from_name = ''` (PRD §9, the `Message.From` display-name setting) |
| `000019_mark_synthetic_subscribers` | Adds `subscribers.synthetic boolean NOT NULL DEFAULT false`, marking the per-admin dedicated test-send recipient row `#0046`'s `ensureTestRecipient` (`internal/handlers/admin_campaign_preview.go`) finds-or-creates so a `POST /admin/campaigns/{id}/test` call has a real, working `manage_token` to anchor the unsubscribe link to without touching a genuine subscriber. Sole writer: `ensureTestRecipient`'s `Create` call, via `NewSignup.Synthetic`. Sole readers: `subscribers.Store.List` and `subscribers.Store.StatusCounts`, both of which exclude `synthetic = true` unconditionally (not a `ListFilter` option) so the `#0032` admin subscribers screen can never surface one no matter what it filters by. Deliberately **not** consulted by `FindByEmail`, `FindByManageToken`, `GetByID`, `FindByConfirmToken`, or `internal/mailing`'s `audienceWhere` — the first four must keep resolving the row exactly like a real one (that is what makes the test message's unsubscribe link genuinely clickable), and `audienceWhere`'s `status = 'active'` equality was already correct and needed no change |

`internal/interests` and `internal/subscribers` are the corresponding Go
store packages for the tables above them; `internal/mailing` now owns
`email_campaigns`/`email_sends` (`#0041`'s `CampaignStore`, `#0044`'s
`AudienceStore`, `#0045`'s `SendStore`).

## Planned schema (Phase 5+ continued, per `PRD.md` §6.2)

Not yet implemented. `PRD.md` §6.2 is the authoritative source for exact
column definitions; the tables it describes, roughly in build order:

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
