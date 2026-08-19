# Email Setup (AWS SES)

Not yet configured — a port of ShortLinks' `email_setup.md` with the domain
swapped, extended by `#0027` (transactional email, likely) and `#0057`
(inbound unsubscribe). This is a stub with real headings pending that work;
`PRD.md` §10.2–10.5 is authoritative in the meantime. See
[`mailing-list.md`](mailing-list.md) for the sending engine that consumes
this setup and [`unsubscribe.md`](unsubscribe.md) for the inbound
`mailto:` path this DNS setup enables.

## Region

**`us-west-2`** — closest to San Francisco, and one of the regions that
supports SES **inbound** email receiving (required for the `mailto:`
unsubscribe path). Verify the inbound-region list before committing to a
different region; it is shorter than the sending-region list, and the
whole project should sit in one region rather than split sending and
receiving across two.

## Domain verification and DNS (Route 53)

| Name | Type | Value | Purpose |
|---|---|---|---|
| `<sel1..3>._domainkey.opencircuitsf.com` | CNAME | *(from SES)* | Easy DKIM |
| `mail.opencircuitsf.com` | MX | `10 feedback-smtp.us-west-2.amazonses.com` | Custom MAIL FROM |
| `mail.opencircuitsf.com` | TXT | `v=spf1 include:amazonses.com ~all` | SPF alignment |
| `lists.opencircuitsf.com` | MX | `10 inbound-smtp.us-west-2.amazonaws.com` | **Inbound unsubscribe only** — never point the apex MX at SES; that hijacks all mail to the domain (`CLAUDE.md` §9) |
| `_dmarc.opencircuitsf.com` | TXT | `v=DMARC1; p=quarantine; adkim=s; aspf=s; rua=mailto:…; fo=1` | DMARC |

**DMARC rollout:** start at `p=none` for two weeks, read the aggregate
reports, then move to `p=quarantine`, then `p=reject` once clean. Don't
jump straight to `p=reject` — a misconfiguration at that policy silently
drops mail with no visibility into why.

## Production access

New SES accounts are sandboxed: 200 messages/day, verified recipients
only. **Request production access early** — approval takes roughly 24
hours and everything downstream (any real send to a non-verified address)
is blocked on it. Describe the use case honestly: opt-in announcement
email for a community electronics workshop group, double opt-in, one-click
unsubscribe, bounce and complaint handling wired to suppression. The
sandbox is enough to develop against; it is not enough to launch.

## IAM

The EC2 instance role provides SES send permissions — **no static SMTP
credentials** should ever live in the config file or environment (`PRD.md`
§10.5). This is a deliberate departure from ShortLinks' SES-SMTP-with-
static-credentials pattern; see [`mailing-list.md`](mailing-list.md)'s
sending-engine section for why the v2 API (not SMTP) is used here.

## Event ingestion (bounce/complaint)

A configuration set (`opencircuit-transactional`) publishes delivery/bounce/
complaint/reject/rendering-failure events to an SNS topic, which POSTs to
`POST /api/ses/notifications` (`internal/sesnotify`, not yet built). Every
inbound message's SNS signature must be verified before it's trusted — see
[`mailing-list.md`](mailing-list.md#ses-event-ingestion).

## Open items (tracked in `CLAUDE.md` §10)

- SES domain verification in `us-west-2`, Easy DKIM, custom MAIL FROM, DMARC
  at `p=none`, and the production-access request are all **not started**
  as of Phase 0 — real sends are blocked on this, though the sandbox is
  enough to develop the sending engine against.
- The sending identity (`hello@` vs. `workshops@`) and who reads the
  reply-to inbox are undecided (`PRD.md` §14 Q2 defaults to `hello@`).
