# Unsubscribe & List Hygiene

Not yet implemented — Phase 4 (`#0033`–`#0039`) in the issue tracker. This
is a stub with real headings pending that work; `PRD.md` §6.5 (93 lines) is
authoritative:

```bash
sed -n '/^### 6\.5 /,/^#\{2,3\} [0-9]/p' PRD.md
```

This is the part of the mailing-list subsystem that most implementations
get wrong, and getting it wrong is what lands a domain in spam folders.
Gmail and Yahoo bulk-sender requirements (effective 2024) make paths 1 and
2 below mandatory in practice, not optional niceties.

## Three paths, all required (except path 3, see below)

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

**Critical DNS detail:** the dedicated `lists.opencircuitsf.com` subdomain
carries its own MX pointed at SES inbound. **Never point the apex
domain's MX at SES** — that would hijack all mail to `opencircuitsf.com`,
including any human mailboxes (`CLAUDE.md` §9).

**This path is optional in v1.** A simpler fallback — point the `mailto:`
at a real monitored mailbox and process unsubscribes manually — is honest
and workable at launch volume. Ship paths 1 and 2 first.

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

## Where to look (once built)

| Concern | Package (planned) |
|---|---|
| Unsubscribe endpoints, preference center | `internal/subscribers` |
| Suppression list, bounce/complaint state machine | `internal/subscribers` |
| Inbound `mailto:` processing | `internal/inbound` |
| SES bounce/complaint event ingestion | `internal/sesnotify` — see [`email-setup.md`](email-setup.md) |
