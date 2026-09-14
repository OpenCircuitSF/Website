# Unsubscribe & List Hygiene

Built and shipped. Phase 4 (`#0033`–`#0039`) landed paths 1 and 2, and
`#0057`/`#0058` built the inbound `mailto:` path's code — one production
step for path 3 is still outstanding, noted under that path below.
`PRD.md` §6.5 is the design authority:

```bash
sed -n '/^### 6\.5 /,/^#\{2,3\} [0-9]/p' PRD.md
```

This is the part of the mailing-list subsystem that most implementations
get wrong, and getting it wrong is what lands a domain in spam folders.
Gmail and Yahoo bulk-sender requirements (effective 2024) make paths 1 and
2 below mandatory in practice, not optional niceties.

## Three paths, all required

### Path 1 — One-click unsubscribe (RFC 8058)

Every campaign email carries `List-Unsubscribe` and
`List-Unsubscribe-Post: List-Unsubscribe=One-Click` headers.

- `POST /api/unsubscribe` accepts the request with **no session and no
  CSRF token** — the mail provider posts it, not the user's browser.
- Must respond `200` quickly and unsubscribe **synchronously**.
- `GET /api/unsubscribe` must **never** unsubscribe — mail clients and
  security scanners prefetch every GET URL in a message; a GET that
  mutates state unsubscribes people who never clicked anything. GET
  redirects to a confirmation page instead.
- An unknown or already-used token still returns `200` with a neutral "you
  are unsubscribed" page, never a `404` — a provider seeing errors on the
  unsubscribe endpoint downgrades sender reputation.

### Path 2 — In-body link → preference center

Every campaign footer links both "Manage your interests" and "Unsubscribe
from everything." Granular management measurably reduces full
unsubscribes — someone tired of one topic drops that interest instead of
leaving the list entirely.

### Path 3 — Inbound `mailto:` unsubscribe (optional in v1)

The one path ShortLinks has no precedent for. A reply or send to
`unsubscribe@lists.opencircuitsf.com` is received by an SES receipt rule
set, stored to S3, and notified via SNS to `POST /api/ses/inbound`
(signature-verified); `internal/inbound` fetches the object, extracts a
token from the `Subject:` line (preferred) or falls back to matching the
`From:` address, and unsubscribes — or leaves the object for manual review
if neither matches.

**"Left for manual review" has a reviewer (`#0499`).** The S3 object under
`unsubscribe/` is left in place — never deleted — for three outcomes: no
subject token or `From:` match, an unparseable message, and one classified
as an auto-reply. `#0057`'s `expire-inbound-30d` lifecycle rule deletes that
object after 30 days regardless, so each of the three additionally writes a
durable `audit_log` row (`audit.ActionInboundMailParked`,
`target_type = 'inbound_mail'`) carrying the S3 key, the SNS message id, the
`From:` address and `Subject` when known, and why the message wasn't acted
on — never the message body, since the body already lives in S3 for as long
as that lasts. `audit_log` has no equivalent expiry, so this record outlives
the object it describes. An admin finds these at **GET `/admin/audit`**,
filtered to `target_type=inbound_mail` (the admin console's audit screen
already supports a target-type filter, `#0114`) — no SSH access to the box
required. See `internal/handlers/ses_inbound.go`'s `recordParked` for the
exact metadata shape and `internal/audit/actions.go`'s
`ActionInboundMailParked` doc comment for the full rationale.

A fourth parked case exists since `#0500`: when S3 answers **NoSuchKey** for
the key a notification names, the handler parks instead of retrying — a
missing object cannot succeed on redelivery, and SNS's policy would otherwise
retry the same doomed notification to exhaustion before dropping it. The row
is the same shape (key, SNS message id, reason `object_missing`) but never
carries a `From:`/`Subject`, since nothing was ever fetched or parsed; the
90-day redaction sweep skips it naturally, because it never carries an
address to redact.

**The recorded address has its own retention policy (`#0503`).** An
auto-reply's `From:` is the subscriber's own address (`#0499`'s test asserts
the recorded `from` equals a real seeded subscriber's `Email`), so it is
retained in full and disclosed in the public privacy policy's audit-log item
alongside the other seven email-carrying audit call sites — the erasure
promise `#0075` built covers it. Every other parked reason is an address
with no relationship to the list: no erasure path can reach it, so holding
it forever is not an option the privacy policy can honestly make. Instead
`internal/audit`'s `Logger.RedactExpiredInboundSenders` — ridden by the
outbox worker's existing poll loop (one idempotent UPDATE per pass, served
by `000030`'s partial index) — replaces the local part with a fixed
placeholder (`redacted@<domain>`) once the row is **90 days** old, for every
parked reason except `auto_reply`. The domain, the S3 key, the message id,
the subject, and the row itself survive, so the entry stays a count an admin
can act on; only the identifying half goes.

**Where an erasure request has to reach.** A subscriber asking for erasure
of everything the privacy policy says survives it routes to
**`contact@opencircuitsf.com`** (`CLAUDE.md` §10 item 4; `#0075`'s policy
page names that address) — the same inbox that already owns erasure and
export requests. A non-subscriber writing to that address about an address
we hold for them gets the honest answer this section documents: nothing
manual needs to happen — the sweep redacts the local part at the 90-day
mark on its own, the row never carried the message body, and the domain
half is not identifying. If a request arrives sooner than the window, the
admin's options are to point at this documented schedule or redact the row
early by hand — the sweep's predicate makes an early manual redaction
idempotent with it (`split_part(...) <> 'redacted'` never matches twice).

**Critical DNS detail:** the dedicated `lists.opencircuitsf.com` subdomain
carries its own MX pointed at SES inbound. **Never point the apex
domain's MX at SES** — that would hijack all mail to `opencircuitsf.com`,
including any human mailboxes (`CLAUDE.md` §9).

**Code shipped; the AWS plumbing is not applied yet.** The PRD kept this
path optional in v1 with a manual-mailbox fallback, but `#0057`/`#0058`
built it instead: the signature-verified handler, the S3 fetch, the
token/`From:` matching, and the parked-review audit trail above are all
merged and deployed. What is not: the SNS topic, receipt rule, rule-set
activation, `lists.` MX record, and instance-role grant (A6–A11), plus the
`SES_INBOUND_BUCKET`/`SES_INBOUND_TOPIC_ARN` config — until
[`email-setup.md`](email-setup.md)'s "Inbound unsubscribe" runbook runs, a
reply to `unsubscribe@lists.opencircuitsf.com` cannot arrive anywhere: D2's
MX record does not exist yet.

## Unsubscribe state machine

| From | Event | To | Side effect |
|---|---|---|---|
| `active` | one-click / preferences / mailto | `unsubscribed` | rotate `manage_token` |
| `active` | SES hard bounce | `bounced` | insert into `suppressions` |
| `active` | SES complaint | `complained` | insert into `suppressions`, **never auto-resubscribe** |
| `active` | 5 soft bounces in 30 days | `bounced` | insert into `suppressions` |
| `unsubscribed` | new signup | `pending` | fresh confirm token; requires re-confirmation |
| `complained` | new signup | *(no change)* | return `202`, send nothing |

**`complained` subscribers never auto-resubscribe.** Only an admin can
clear that state (`CLAUDE.md` §9) — a complaint is a strong deliverability
signal that must not be silently overridden by the subscriber re-signing-up.

## Where to look

| Concern | Where |
|---|---|
| Unsubscribe endpoints, preference center (HTTP) | `internal/handlers/unsubscribe.go`, `internal/handlers/preferences.go` |
| Subscriber state machine, suppression list | `internal/subscribers` |
| Inbound `mailto:` processing | `internal/inbound`, handler in `internal/handlers/ses_inbound.go` |
| SES bounce/complaint event ingestion | `internal/sesnotify` — see [`email-setup.md`](email-setup.md) |
