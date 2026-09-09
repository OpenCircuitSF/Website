# Mailing List

The core subsystem of this project and the reason it's a fork of ShortLinks
rather than a rewrite from scratch — the auth/session/admin skeleton carries
over so this subsystem can be built without reinventing account management.
Not yet implemented; this is a stub with real headings pending Phases 3–5.
`PRD.md` §6 (459 lines) is authoritative — extract the subsection you need
rather than reading it whole:

```bash
sed -n '/^### 6\.1 /,/^#\{2,3\} [0-9]/p' PRD.md   # interest taxonomy
sed -n '/^### 6\.2 /,/^#\{2,3\} [0-9]/p' PRD.md   # database schema
sed -n '/^### 6\.3 /,/^#\{2,3\} [0-9]/p' PRD.md   # subscription flow (double opt-in)
sed -n '/^### 6\.4 /,/^#\{2,3\} [0-9]/p' PRD.md   # preference center
sed -n '/^### 6\.5 /,/^#\{2,3\} [0-9]/p' PRD.md   # unsubscribe — see unsubscribe.md
sed -n '/^### 6\.6 /,/^#\{2,3\} [0-9]/p' PRD.md   # sending engine
sed -n '/^### 6\.7 /,/^#\{2,3\} [0-9]/p' PRD.md   # SES event ingestion — see email-setup.md
```

## Interest taxonomy (Phase 3, `#0023`)

Interests are **rows in a table, not a Go enum** — new workshop themes
appear constantly, and adding one must never require a deploy. Seed list
(12 interests: microcontrollers, soldering, homelab, home-automation,
pcb-design, sensors-iot, robotics, radio-rf, retro-computing, 3d-printing,
test-equipment, beginner). A subscriber with **zero** interests selected is
a valid, expected state — they receive only general announcements.

### Changing the taxonomy (`#0471`)

`migrations/000009_create_interests` only ever seeds these twelve rows once,
on a fresh install. It is frozen the moment it has run against production
(`CLAUDE.md` §1 — production has been past it since Phase 3) and must never
be edited again, by this project or a downstream. That is fine, because it
was never meant to be the only way the taxonomy changes — there are three
supported channels, and none touches `000009`:

- **A live catalog change** — add, rename (the display `name` only),
  redescribe, reorder, deactivate, or hard-delete an interest no subscriber
  has ever selected — is an **admin-console action**, not a migration:
  `POST`/`PATCH`/`DELETE /admin/interests` (`internal/interests`, `#0024`).
  It takes effect immediately, with no deploy and no restart. Two limits are
  load-bearing and are not obvious from the verb list:
  - **`slug` is immutable through this channel.** `interests.Store.Update`
    takes no slug parameter, and the PATCH body type carries a slug field
    only so a request that includes one is refused outright with an explicit
    400 explaining why, rather than being rejected with the same generic
    message a typo would produce (`#0475`). The reason is a scope decision
    about the admin channel, not a technical one: measured directly, nothing
    references an interest by slug — `subscriber_interests`,
    `campaign_interests`, and `workshop_interests` all key off `id`, and no
    issued preference-center link or other URL carries a slug at all
    (`#0023`'s own Gotchas said as much at the time, conditionally, and this
    is that condition resolved). A slug is the taxonomy's stable external
    name, so changing one is a reviewed, recorded operation rather than a
    form field — see the "Changing an existing slug" bullet below for the
    supported way to do it. **Creating a new interest and deactivating the
    old one is not a substitute for a rename**: it mints a new `id`, and
    every one of the three join tables above references the interest by
    `id`, so that path strands each existing subscriber selection, campaign
    segment, and workshop tag on a row no longer offered on the signup
    form.
  - **`DELETE` is refused (409) when any of `subscriber_interests`,
    `campaign_interests`, or `workshop_interests` references the interest**
    (`#0474`). All three are `ON DELETE CASCADE`
    (`migrations/000010_create_subscribers.up.sql`,
    `migrations/000017_create_campaigns.up.sql`,
    `migrations/000020_create_workshops.up.sql`), so `interests.Store.Delete`
    locks the interest row first, then checks all three, and the error names
    which one blocked it — a subscriber's selection, a campaign's target
    segment, or a workshop's topic tag. Locking first (`#0477`) means a
    reference committed by another admin while the delete was waiting on the
    lock is seen by the check rather than cascaded away. Prefer
    `PATCH {"active": false}` for any interest
    with history: deactivation preserves the row and every
    `subscriber_interests`/`campaign_interests`/`workshop_interests` row
    that references it. (Before `#0474`, `DELETE` consulted
    `subscriber_interests` only, so an interest tagged on a workshop or
    targeted by a campaign but selected by no subscriber could be
    hard-deleted, silently dropping those rows via the cascade — the
    campaign case was unrecoverable, since a sent campaign's target segment
    cannot be reconstructed afterward.)
- **A change to what a *fresh* install seeds by default** — the canonical
  list in `PRD.md` §6.1 itself changing — is a **new, additively-numbered
  migration**, never an edit to `000009`, following exactly the shape
  `000009` already uses: `INSERT INTO interests (...) VALUES (...) ON
  CONFLICT (slug) DO NOTHING` to add a default, or `UPDATE interests SET
  name = '...', description = '...' WHERE slug = '...'` to redescribe one in
  place — matching the row by its existing, unchanged slug (this preserves
  `id`, so no existing `subscriber_interests` row is orphaned or
  renumbered). **This channel's `WHERE slug = '...'` selects the row; it
  never appears on the `SET` side.** Changing the slug value itself is a
  different operation with its own conditions — the third bullet below. A
  migration in this family must never `DELETE` a row — `internal/db`'s
  `TestInterestTaxonomyMigrationGuardPassesOnRealMigrations` enforces both
  the idempotency and the no-delete rule mechanically for every migration
  numbered after `000009`.

  The matching `.down.sql` must be a documented no-op — a comment saying the
  seeded row is deliberately not removed on rollback — never a `DELETE`: the
  row may have acquired `subscriber_interests`/`workshop_interests`/
  `campaign_interests` associations while it existed, and `ON DELETE CASCADE`
  would take them with it. The guard enforces this in both directions.
- **Changing an existing slug** (`#0475`) is a migration, never an admin-API
  call. Measured directly: `subscriber_interests`, `campaign_interests`, and
  `workshop_interests` all reference an interest by `id`, and no issued
  preference-center link or other URL carries a slug, so a rename that keeps
  the row's `id` breaks nothing already in flight. The blessed shape has five
  conditions:
  - The statement is `UPDATE interests SET slug = 'new-slug' WHERE slug =
    'old-slug';` in a new, additively-numbered migration. Never a delete
    plus an insert — that mints a new `id`, and the three join tables above
    reference the row by `id`, so the associations would cascade away
    instead of carrying across.
  - The new value must satisfy the lowercase-hyphenated
    `interests_slug_format` CHECK constraint.
  - A collision with an existing slug raises a unique violation and aborts
    the migration. That fail-closed outcome is correct and must not be
    softened with `ON CONFLICT`.
  - Re-running the migration is a no-op, because the second run's `WHERE
    slug = 'old-slug'` matches no row. Unlike the seed channel above, the
    matching `.down.sql` **may** reverse this one: `UPDATE interests SET
    slug = 'old-slug' WHERE slug = 'new-slug';` removes no row and fires no
    cascade, so the no-op-down rule that governs a seeded row's rollback
    does not apply here.
  - The migration must **not** rewrite `audit_log.metadata`. Audit rows
    record what an operator actually did at the time and are the
    consent-evidence record, so a historical entry naming a since-renamed
    slug is correct, not stale.

  Two consequences follow, both visible and both fail-closed rather than
  silent: a browser tab left open across the change gets a 400 "unknown
  interest" on its next preference save, cleared by a reload, and a CSV
  import file still carrying the old slug is reported in the preview's
  `unknown_interest_slugs` rather than silently importing without the link.

## Subscription flow — double opt-in (Phase 3, `#0025`–`#0032`)

Standard double opt-in: a public form submits an email + optional interest
selection, the server sends a confirmation email with a token, and the
subscriber isn't active on the list until they click through. `POST
/api/subscribe` must return a **byte-identical response regardless of
whether the address is already subscribed** — varying the response by
subscription state turns the endpoint into an email-enumeration oracle
(`CLAUDE.md` §9; the handler test asserts this).

## Preference center (Phase 3)

A token-authenticated page (no login required — the token *is* the
authentication) where a subscriber can add/remove interests or unsubscribe
from everything. Linked from every campaign footer.

## Unsubscribe — see [`unsubscribe.md`](unsubscribe.md)

Three independent, all-required paths (one-click header, in-body
preference-center link, inbound `mailto:`) plus suppression-list and
bounce/complaint handling. Detailed separately since it's the part most
implementations get wrong and the part with real deliverability
consequences.

## Sending engine (Phase 5, `#0040`–`#0049`)

- **Transport: AWS SES v2 API**, not SMTP — authenticates via the EC2
  instance IAM role (no long-lived SMTP password in a config file),
  returns a `MessageId` per send (the join key for bounce/complaint
  events), and accepts a configuration set per message (how SNS event
  publishing gets enabled).
- **Send worker** (`internal/mailing/worker.go`) — one goroutine, started
  by `serve`, shut down on `SIGTERM`; polls for campaigns in `sending` or
  due `scheduled` state, materializes the audience once per campaign, then
  sends in batches at a capped rate (`MAX_SEND_RATE`, below the SES quota).
- **CAN-SPAM §7704 physical address requirement is enforced, not optional**
  — the send worker refuses to start a campaign without a configured
  `physical_address` setting (`CLAUDE.md` §9). This must never become
  bypassable from the admin UI.
- A `Mailer` interface seam (mirroring ShortLinks' pattern) keeps the send
  path testable with a recorder rather than hitting real SES in tests.

## SES event ingestion — see `email-setup.md`

Bounce/complaint/delivery events arrive via an SNS webhook
(`internal/sesnotify`), not polling. Every inbound SNS message's signature
must be verified before it's trusted — an unverified endpoint is an open
door for anyone to forge bounce events and mass-suppress the list.

## Where to look (once built)

| Concern | Package (planned) |
|---|---|
| Interest taxonomy | `internal/interests` |
| Signup, confirmation, preferences, unsubscribe, suppression | `internal/subscribers` |
| Campaigns, rendering, audience, send worker, SES mailer | `internal/mailing` |
| SNS webhook, bounce/complaint ingestion | `internal/sesnotify` |
| Inbound `mailto:` unsubscribe | `internal/inbound` |
