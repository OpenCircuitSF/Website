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
