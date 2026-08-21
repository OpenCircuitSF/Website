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
`POST /api/ses/notifications` (`internal/sesnotify` + `internal/handlers`,
`#0037`/`#0038`). Every inbound message's SNS signature and `TopicArn` are
verified before anything in the body is trusted — see
[`mailing-list.md`](mailing-list.md#ses-event-ingestion). The SNS topic
itself, the configuration set, and the event destination are not provisioned
yet (`CLAUDE.md` §10 item 2) — until then the code correctly rejects every
delivery, since `SES_EVENTS_TOPIC_ARN` is unset.

### Account-level suppression list — the second layer (`#0038` criterion 8)

PRD §6.7: enable SES's own account-level suppression list as belt-and-
suspenders alongside this project's `suppressions` table. Our table is
authoritative for OUR sending decisions (it's what `#0026`'s subscribe flow
and the future send worker check); SES's own list protects the AWS account's
sending reputation if ours ever has a bug and lets a permanently-failing
address through.

This is an AWS account setting, not code — it can't be verified by
`go test` and isn't claimed as done by any commit. Enable it once the SES
account exists (`CLAUDE.md` §10 item 2), from the EC2 instance role or an
operator's AWS CLI session:

```bash
aws sesv2 put-account-suppression-attributes \
    --suppressed-reasons BOUNCE COMPLAINT \
    --region us-west-2
```

What this does and does not cover:

- SES silently drops a send to any address on its account-level list —
  before the message ever reaches the recipient's mail server. This is a
  send-time guard, symmetrical with (but independent of) our own
  `suppressions` table check in the subscribe/send path.
- It does **not** replace `internal/sesnotify`'s event ingestion. SES's own
  list has no visibility into our `subscribers` table, can't drive our
  status transitions (`bounced`/`complained`) or audit trail, and PRD
  §6.5's state machine still needs the real bounce/complaint events this
  project's own webhook records.
- Verify it's active with
  `aws sesv2 get-account-suppression-attributes --region us-west-2`, which
  should echo back `{"SuppressedReasons": ["BOUNCE", "COMPLAINT"]}`.

## Open items (tracked in `CLAUDE.md` §10)

- SES domain verification in `us-west-2`, Easy DKIM, custom MAIL FROM, DMARC
  at `p=none`, and the production-access request are all **not started**
  as of Phase 0 — real sends are blocked on this, though the sandbox is
  enough to develop the sending engine against.
- The sending identity (`hello@` vs. `workshops@`) and who reads the
  reply-to inbox are undecided (`PRD.md` §14 Q2 defaults to `hello@`).
