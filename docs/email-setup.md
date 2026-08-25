# Email Setup (AWS SES)

**Configured 2026-08-25, except for two things** — the EC2 instance role and
SES production access. Everything below that describes DNS, the identity, the
configuration set, and event ingestion is a record of what exists, measured
against account `378152330719`; the two gaps are called out where they bite.
See [`aws-iam-setup.md`](aws-iam-setup.md) for the instance-role work,
[`mailing-list.md`](mailing-list.md) for the sending engine that consumes this
setup, and [`unsubscribe.md`](unsubscribe.md) for the inbound `mailto:` path
(Phase 4, `#0057`, not built).

## Sending happens on a subdomain, not the apex

**`mailing.opencircuitsf.com`** is the verified SES identity. The apex is
*not*, deliberately (user's decision, 2026-08-25).

`opencircuitsf.com` carries the project's real human mail through **Google
Workspace** — its MX is `1 smtp.google.com` and it has a `google._domainkey`
DKIM record. Sending bulk list mail as the apex would put a spam complaint
against a workshop announcement onto the same domain reputation as that
business mail. Isolating the list onto its own subdomain costs nothing up
front and is close to irreversible afterwards, once subscribers have filed the
sender and receivers have built reputation on it — so it was decided before
anything reached DNS.

| | |
|---|---|
| SES identity | `mailing.opencircuitsf.com` — verified, DKIM `SUCCESS` |
| `From:` | `Open Circuit SF <hello@mailing.opencircuitsf.com>` (`EMAIL_FROM`) |
| `Reply-To:` | `contact@opencircuitsf.com` (`EMAIL_REPLY_TO`) — replies land in the normal Google inbox, so the subdomain never needs to receive mail |
| Envelope / `Return-Path` | `bounce.mailing.opencircuitsf.com` (custom MAIL FROM, `SUCCESS`) |
| **Apex MX** | **untouched.** Still Google. `CLAUDE.md` §9's "never point the apex MX at SES" is not theoretical here — it would hijack real mail |
| **Apex SPF / TXT** | **untouched.** The apex needs no SES record at all, because nothing sends as the apex |
| Inbound `mailto:` unsubscribe | `lists.opencircuitsf.com` (`EMAIL_LIST_DOMAIN`) — a *third*, separate subdomain, and not built yet |

The apex currently has **no SPF and no DMARC record at all**. That is a
pre-existing gap in the Google Workspace setup, not something this project
introduced or needs — but it is worth closing independently one day.

## Region

**`us-west-2`** — closest to San Francisco, and one of the regions that
supports SES **inbound** email receiving (required for the `mailto:`
unsubscribe path). Verify the inbound-region list before committing to a
different region; it is shorter than the sending-region list, and the
whole project should sit in one region rather than split sending and
receiving across two.

## Domain verification and DNS (Route 53)

Hosted zone `Z0825067RV8QY5UIKS96`. These seven records were created
2026-08-25 and all resolve; SES reports the identity verified with DKIM and
MAIL FROM both `SUCCESS`.

| Name | Type | Value | Purpose |
|---|---|---|---|
| `<3 tokens>._domainkey.mailing.opencircuitsf.com` | CNAME | `<token>.dkim.amazonses.com` | Easy DKIM, RSA 2048 |
| `bounce.mailing.opencircuitsf.com` | MX | `10 feedback-smtp.us-west-2.amazonses.com` | Custom MAIL FROM |
| `bounce.mailing.opencircuitsf.com` | TXT | `v=spf1 include:amazonses.com ~all` | SPF for the envelope domain |
| `mailing.opencircuitsf.com` | TXT | `v=spf1 include:amazonses.com -all` | SPF for the `From:` domain. `-all`, not `~all`: nothing but SES ever sends as this name, so a hard fail is safe and stronger |
| `_dmarc.mailing.opencircuitsf.com` | TXT | `v=DMARC1; p=none; rua=mailto:contact@opencircuitsf.com; fo=1` | DMARC, **subdomain-scoped** |

**Not created, on purpose:** anything at the apex, and
`lists.opencircuitsf.com` (Phase 4 inbound, `#0057`).

**DMARC is on the subdomain, not the apex, and that is the point.** A receiver
resolving DMARC for `mailing.opencircuitsf.com` checks `_dmarc.mailing.…`
first and only falls back to the organizational domain when it is absent. So
the list gets a real DMARC policy while the apex keeps exactly the posture it
had — nothing about Google Workspace mail changes.

**DMARC rollout:** it is at `p=none` today. Read the aggregate reports at
`contact@opencircuitsf.com` for two weeks, then move to `p=quarantine`, then
`p=reject` once clean. Don't jump straight to `p=reject` — a misconfiguration
at that policy silently drops mail with no visibility into why. Note the
report volume is real: aggregate XML arrives daily from every receiver that
sees your mail.

**Note on the wildcard.** The zone has a `*.opencircuitsf.com` CNAME pointing
at the web server. Creating explicit records at `mailing.` and
`bounce.mailing.` suppresses wildcard synthesis for those exact names, so they
no longer resolve as web hosts. Nothing served them, so nothing broke — but it
is the kind of thing to remember before adding a record at a name you expect
the wildcard to keep covering.

## Production access

**Still in the sandbox as of 2026-08-25** — `aws sesv2 get-account --region
us-west-2` reports `ProductionAccessEnabled: false` with `SendingEnabled:
true`. That is 200 messages/day to verified recipients only, which is enough
to prove the whole pipeline (including SES's simulator addresses) but not to
launch. `cli-admin`'s inline policy already grants `support:CreateCase`, so
the request can be filed from the CLI.

New SES accounts are sandboxed: 200 messages/day, verified recipients
only. **Request production access early** — approval takes roughly 24
hours and everything downstream (any real send to a non-verified address)
is blocked on it. Describe the use case honestly: opt-in announcement
email for a community electronics workshop group, double opt-in, one-click
unsubscribe, bounce and complaint handling wired to suppression. The
sandbox is enough to develop against; it is not enough to launch.

## IAM

> **This is the open blocker.** The instance has **no IAM role attached at
> all** — the metadata service 404s `iam/security-credentials/`. Every other
> piece below works, and none of it can send a byte until this is fixed.
> [`aws-iam-setup.md`](aws-iam-setup.md) has the exact policies and the
> console steps.

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
[`mailing-list.md`](mailing-list.md#ses-event-ingestion).

**All of it is provisioned as of 2026-08-25:**

| Piece | Value |
|---|---|
| Configuration set | `opencircuit-transactional`, reputation metrics on |
| Event destination | `sns-events` — SEND, DELIVERY, BOUNCE, COMPLAINT, REJECT, RENDERING_FAILURE, DELIVERY_DELAY |
| SNS topic | `arn:aws:sns:us-west-2:378152330719:opencircuit-ses-events` |
| Topic policy | owner full control, plus `sns:Publish` for `ses.amazonaws.com` conditioned on `SourceAccount` and a `SourceArn` under this account's SES |
| Subscription | HTTPS → `https://www.opencircuitsf.com/api/ses/notifications`, **auto-confirmed** |
| `SES_EVENTS_TOPIC_ARN` | set in `/etc/opencircuit/config.env` |

The subscription auto-confirming is worth more than it looks: it means
`internal/sesnotify` verified a real SNS signature, matched the `TopicArn`
against its allowlist, and fetched the `SubscribeURL` exactly once — the whole
verification path exercised against real SNS rather than a fixture.

**Order matters here.** `SES_EVENTS_TOPIC_ARN` has to be set *and the service
restarted* before the SNS subscription is created. The handler rejects any
message whose `TopicArn` is not on the allowlist, and with the variable unset
the allowlist is empty — so the `SubscriptionConfirmation` itself gets
rejected and the subscription hangs in `pending confirmation`.

The topic was also switched to **`SignatureVersion 2`** (SHA-256). SNS still
defaults to version 1 (SHA-1); the handler logs
`verified a SignatureVersion 1 (SHA-1) message` when it sees one, which is how
the default was caught.

### Account-level suppression list — the second layer (`#0038` criterion 8)

PRD §6.7: enable SES's own account-level suppression list as belt-and-
suspenders alongside this project's `suppressions` table. Our table is
authoritative for OUR sending decisions (it's what `#0026`'s subscribe flow
and the future send worker check); SES's own list protects the AWS account's
sending reputation if ours ever has a bug and lets a permanently-failing
address through.

**Already enabled** — checked 2026-08-25, `get-account` returns
`SuppressedReasons: ["BOUNCE", "COMPLAINT"]`. It appears to predate this
project. No action needed; the command below is kept for reference and for
rebuilding the account from scratch.

This is an AWS account setting, not code — it can't be verified by
`go test` and isn't claimed as done by any commit:

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

Two left, in the order they block things:

1. **The EC2 instance role** — see the IAM section above and
   [`aws-iam-setup.md`](aws-iam-setup.md). Nothing sends without it.
2. **SES production access** — the account is still sandboxed. The sandbox is
   enough to prove the pipeline end to end against verified addresses and
   SES's simulator (`bounce@simulator.amazonses.com`,
   `complaint@simulator.amazonses.com`), but not enough to mail a real list.

They are independent of each other. When both are done, flip `SES_SANDBOX=false`
and `SEND_WORKER_ENABLED=true` in `/etc/opencircuit/config.env` and restart.

Resolved, no longer open:

- Domain verification, Easy DKIM, custom MAIL FROM, and DMARC — all done, on
  the `mailing.` subdomain.
- The sending identity question (`PRD.md` §14 Q2) — it is
  `hello@mailing.opencircuitsf.com`, with `Reply-To: contact@opencircuitsf.com`
  so replies reach a Google Workspace inbox that a human already reads. That
  also settles the "who reads that inbox" half of `CLAUDE.md` §10 item 4.

Still not resolved, and not an SES problem: **campaigns refuse to start
without a `physical_address` setting** (`#0045`, CAN-SPAM §7704,
`CLAUDE.md` §10 item 3). That needs a PO box.
