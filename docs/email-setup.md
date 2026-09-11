# Email Setup (AWS SES)

**Configured 2026-08-25, except for two things** — the EC2 instance role and
SES production access. Everything below that describes DNS, the identity, the
configuration set, and event ingestion is a record of what exists, measured
against account `378152330719`; the two gaps are called out where they bite.
See [`aws-iam-setup.md`](aws-iam-setup.md) for the instance-role work,
[`mailing-list.md`](mailing-list.md) for the sending engine that consumes this
setup, and [`unsubscribe.md`](unsubscribe.md) for the inbound `mailto:` design
(Phase 4). The AWS/DNS build steps for that inbound path are their own
runbook below, **"Inbound unsubscribe (Phase 4, `#0057`)"** — as of
2026-09-11 the domain identity and its verification TXT (A1/D1) and the S3
bucket (A2) exist; the SNS topic, receipt rule, rule-set activation, MX, and
IAM grant do not yet.

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
| `From:` | `Open Circuit SF <contact@mailing.opencircuitsf.com>` (`EMAIL_FROM`) |
| `Reply-To:` | `contact@opencircuitsf.com` (`EMAIL_REPLY_TO`) — replies land in the normal Google inbox, so the subdomain never needs to receive mail |
| Envelope / `Return-Path` | `bounce.mailing.opencircuitsf.com` (custom MAIL FROM, `SUCCESS`) |
| **Apex MX** | **untouched.** Still Google. `CLAUDE.md` §9's "never point the apex MX at SES" is not theoretical here — it would hijack real mail |
| **Apex SPF / TXT** | **untouched.** The apex needs no SES record at all, because nothing sends as the apex |
| Inbound `mailto:` unsubscribe | `lists.opencircuitsf.com` (`EMAIL_LIST_DOMAIN`) — a *third*, separate subdomain, and not built yet |

The apex currently has **no SPF and no DMARC record at all**. That is a
pre-existing gap in the Google Workspace setup, not something this project
introduced or needs — but it is worth closing independently one day.

## Region

**`us-east-1`** — corrected 2026-09-03 (`#0418`); this section previously said
`us-west-2`, which was never production's real region. Proved three
independent ways: instance metadata's `placement/region`,
`AWS_REGION=us-east-1` in `/etc/opencircuit/config.env`, and the live custom
MAIL FROM MX record (`10 feedback-smtp.us-east-1.amazonses.com`, below). The
original reasoning — closest to San Francisco — does not carry over to
`us-east-1`; what still holds is that `us-east-1` supports SES **inbound**
email receiving (required for the `mailto:` unsubscribe path) and is the
region the EC2 instance itself already sits in, so sending and receiving stay
in one place. **Do not migrate to `us-west-2`** (`#0057`'s planning pass).

## Domain verification and DNS (Route 53)

Hosted zone `Z0825067RV8QY5UIKS96`. These seven records were created
2026-08-25 and all resolve; SES reports the identity verified with DKIM and
MAIL FROM both `SUCCESS`.

| Name | Type | Value | Purpose |
|---|---|---|---|
| `<3 tokens>._domainkey.mailing.opencircuitsf.com` | CNAME | `<token>.dkim.amazonses.com` | Easy DKIM, RSA 2048 |
| `bounce.mailing.opencircuitsf.com` | MX | `10 feedback-smtp.us-east-1.amazonses.com` | Custom MAIL FROM |
| `bounce.mailing.opencircuitsf.com` | TXT | `v=spf1 include:amazonses.com ~all` | SPF for the envelope domain |
| `mailing.opencircuitsf.com` | TXT | `v=spf1 include:amazonses.com -all` | SPF for the `From:` domain. `-all`, not `~all`: nothing but SES ever sends as this name, so a hard fail is safe and stronger |
| `_dmarc.mailing.opencircuitsf.com` | TXT | `v=DMARC1; p=none; rua=mailto:contact@opencircuitsf.com; fo=1` | DMARC, **subdomain-scoped** |

**Not created, on purpose:** anything at the apex, and (still, as of
2026-09-11) the MX at `lists.opencircuitsf.com` itself — see "Inbound
unsubscribe (Phase 4, `#0057`)" below. The identity-verification TXT at
`_amazonses.lists.opencircuitsf.com` is a different name and does already
exist.

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

**Note on the wildcard.** The zone has a `*.opencircuitsf.com` wildcard **A**
record pointing at the web server (`98.84.75.184`, measured against the
zone's authoritative nameserver — not a CNAME, and not
`ec2.smallsharptools.com`, which resolves to a different IP,
`44.222.209.183`; see `#0057`'s review). Creating explicit records at `mailing.` and
`bounce.mailing.` suppresses wildcard synthesis for those exact names, so they
no longer resolve as web hosts. Nothing served them, so nothing broke — but it
is the kind of thing to remember before adding a record at a name you expect
the wildcard to keep covering.

## Production access

**Still in the sandbox as of 2026-08-25** — `aws sesv2 get-account --region
us-east-1` reports `ProductionAccessEnabled: false` with `SendingEnabled:
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

> **Done 2026-08-25.** Role `opencircuit-instance` (inline policy
> `opencircuit-ses-send`) is attached to `i-0e3bd89e87d1c2364`; the service
> user resolves to
> `assumed-role/opencircuit-instance/i-0e3bd89e87d1c2364` and sends
> successfully. See [`aws-iam-setup.md`](aws-iam-setup.md).
>
> **One trap, recorded because it cost a live debugging round:** the policy's
> `Resource` must be `identity/*`, not just the sending domain's identity.
> While the account is in the **sandbox**, SES authorizes `SendEmail` against
> the *recipient's* identity ARN as well as the sender's, so a policy naming
> only `identity/mailing.opencircuitsf.com` fails every real send with
> `AccessDeniedException` on the recipient ARN — while a send to
> `success@simulator.amazonses.com` still succeeds, because simulator addresses
> are not identities. Verify an SES IAM policy against a real verified
> recipient, never only the simulator.

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
| SNS topic | `arn:aws:sns:us-east-1:378152330719:opencircuit-ses-events` |
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
    --region us-east-1
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
  `aws sesv2 get-account-suppression-attributes --region us-east-1`, which
  should echo back `{"SuppressedReasons": ["BOUNCE", "COMPLAINT"]}`.

## Inbound unsubscribe (Phase 4, `#0057`)

The AWS-side plumbing for PRD §6.5 path 3: mail sent to
`unsubscribe@lists.opencircuitsf.com` lands in S3, SES notifies an SNS
topic, and that topic POSTs to `POST /api/ses/inbound` — the handler
`#0058` already built and merged (`internal/inbound`,
`internal/handlers/ses_inbound.go`), reading two config variables,
`SES_INBOUND_BUCKET` and `SES_INBOUND_TOPIC_ARN` (`docs/configuration.md`).
This section is the step-by-step runbook for the AWS objects and DNS
records; everything in it is outward-facing and account-wide in one step
(A10 below), so **the user runs every command in this section**, not an
agent (`CLAUDE.md` §8b, §9).

All commands run in **`us-east-1`**, account `378152330719`. Ready-to-apply
JSON documents for the policy/rule steps live in
[`deploy/aws/`](../deploy/aws/README.md) — that directory's own `README.md`
is the file manifest; this section is the ordering, the gate, and the
verification.

### What is already done — checked 2026-09-11, read-only

| Row | State |
|---|---|
| **A1** — SES domain identity `lists.opencircuitsf.com` | Its DNS verification TXT exists (see D1 below), which only happens after `CreateEmailIdentity`/`VerifyDomainIdentity` runs — **treat A1 as created**, but re-confirm verification actually completed (step 0 below), since no credential available to this pass can call `ses:GetIdentityVerificationAttributes`. |
| **D1** — `_amazonses.lists.opencircuitsf.com` TXT | **Exists**: `"APWUrtnPLURlLWOGg0ybU3t6HbptTzDE77f8JE1YHX0="`. Kept as `deploy/aws/D1-route53-change-batch-txt.json` for reference and rollback only — **do not re-run it.** |
| **A2** — S3 bucket `opencircuitsf-inbound` | **Exists** (`head-bucket` → 403, against a random-name control returning 404 — 403 means the bucket is there and this identity just can't read it). Its public-access-block, lifecycle, and policy state could **not** be read from here — confirm each with the read commands in step 2 below before assuming any of A3/A4/A5 still need to be applied. |
| **D2** — `lists.opencircuitsf.com` MX | **Absent.** Still served only by the zone's `*.opencircuitsf.com` wildcard A record. This is correct — D2 is deliberately last (step 8). |

Everything else in the table below (A6–A11, A12) does not yet exist.

### The account-wide gate — run this first, always

```bash
aws ses describe-active-receipt-rule-set --region us-east-1
```

This account also runs ShortLinks and other services, and **a region has
exactly one active receipt rule set for the whole account.** Read the
result before doing anything else:

- **Empty (`{}`)** → no rule set is active. Proceed with A8/A9/A10 below
  exactly as written — create `opencircuit-inbound` and activate it.
- **Non-empty** (a `Metadata` block naming an existing set, plus its
  `Rules`) → **stop.** Do not create or activate a second rule set — that
  would silently replace whatever the existing set does for the whole
  account. Instead: add the `unsubscribe` rule to that existing set
  (`--rule-set-name <its name>` in place of `opencircuit-inbound` in A9's
  command, and `--after <name of its last rule>` so the new rule is
  appended rather than inserted first), edit the `AWS:SourceArn` condition
  in `deploy/aws/A4-s3-bucket-policy.json` and
  `deploy/aws/A7-sns-topic-policy.json` to name that set instead of
  `opencircuit-inbound`, and skip A8 and A10 entirely — the set is already
  active. If this turns out to be fiddly (another project's rule ordering,
  unclear ownership), take the escape hatch: point the `mailto:` at a
  monitored mailbox and process unsubscribes by hand instead.

### Order — every AWS object is inert until the MX exists, so the MX goes last

The whole safety argument for this ordering: nothing below can receive mail
until D2 (the MX) exists, so every object is built and locked down first,
and the very last step is the one that turns traffic on. Each step is
independently reversible — see **Rollback**, below.

**0. Confirm A1's verification actually completed** (the DNS record exists,
but that only proves the token was published, not that SES finished
checking it):

```bash
aws sesv2 get-email-identity --email-identity lists.opencircuitsf.com --region us-east-1
```

Expect `"VerificationStatus": "SUCCESS"` (or the v1-shaped
`aws ses get-identity-verification-attributes --identities
lists.opencircuitsf.com --region us-east-1` → `"VerificationStatus":
"Success"`, depending which API originally created the identity). If it
instead reads `"PENDING"`, the TXT record hasn't finished propagating or
SES hasn't polled it yet — wait and re-check before continuing; nothing
past this point works until it reads `Success`.

**1. Confirm A2's bucket lockdown state**, since existence was all that
could be checked from here:

```bash
aws s3api get-public-access-block --bucket opencircuitsf-inbound --region us-east-1
aws s3api get-bucket-lifecycle-configuration --bucket opencircuitsf-inbound --region us-east-1
aws s3api get-bucket-policy --bucket opencircuitsf-inbound --region us-east-1
```

For each: if it returns the expected configuration (all four
public-access-block flags `true`; a lifecycle rule named
`expire-inbound-30d` on prefix `unsubscribe/`; a policy matching
`deploy/aws/A4-s3-bucket-policy.json`), skip that step below. If it errors
(`NoSuchPublicAccessBlockConfiguration`, `NoSuchLifecycleConfiguration`,
`NoSuchBucketPolicy`) or the configuration differs, apply it:

**Read before you write.** `put-bucket-lifecycle-configuration` and
`put-bucket-policy` both **replace** the whole configuration rather than
merging into it. This bucket predates this pass — the `get-` commands above
are not optional busywork; applying either command blind would silently
discard any existing lifecycle rule or policy statement that isn't in the
files below.

```bash
# A3 — block all public access (skip if already set)
aws s3api put-public-access-block --bucket opencircuitsf-inbound --region us-east-1 \
  --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true

# A5 — 30-day lifecycle on the unsubscribe/ prefix (skip if already set)
aws s3api put-bucket-lifecycle-configuration --bucket opencircuitsf-inbound --region us-east-1 \
  --lifecycle-configuration file://deploy/aws/A5-s3-lifecycle.json

# A4 — allow SES to PutObject under unsubscribe/ only, scoped to this
# account and this exact receipt rule (skip if already set, or if the gate
# above found an existing rule set — edit the SourceArn first, see above)
aws s3api put-bucket-policy --bucket opencircuitsf-inbound --region us-east-1 \
  --policy file://deploy/aws/A4-s3-bucket-policy.json
```

Expect each command to return no output on success; confirm with the `get-`
commands above.

**2. A6 — create the SNS topic** (separate from the existing
`opencircuit-ses-events` topic — do not reuse it; `internal/sesnotify`
dispatches SES message types by `TopicArn`, and a shared topic would let
either endpoint's messages satisfy the other's verifier):

```bash
aws sns create-topic --name opencircuit-inbound-mail --region us-east-1
```

Confirm the returned `TopicArn` is exactly
`arn:aws:sns:us-east-1:378152330719:opencircuit-inbound-mail` — that value
is already baked into `.env.example`'s `SES_INBOUND_TOPIC_ARN` and into
`deploy/aws/A7-sns-topic-policy.json` / `A9-ses-receipt-rule.json`.

**3. A7 — set the topic policy** (owner keeps full control; `ses.amazonaws.com`
may publish, scoped the same way as A4):

```bash
aws sns set-topic-attributes --region us-east-1 \
  --topic-arn arn:aws:sns:us-east-1:378152330719:opencircuit-inbound-mail \
  --attribute-name Policy --attribute-value file://deploy/aws/A7-sns-topic-policy.json
```

**4. A8 — create the receipt rule set** (only if the gate above found none
active):

```bash
aws ses create-receipt-rule-set --rule-set-name opencircuit-inbound --region us-east-1
```

**5. A9 — create the `unsubscribe` rule.** One `S3Action` with its own
`TopicArn` set — **not** a second, separate SNS action. A standalone SNS
receipt-rule action publishes the raw message content (capped at 150 KB)
and carries no S3 object key; the S3 action's own optional `TopicArn`
field is what fires *after* SES finishes writing the object, and its
notification is what `#0058`'s `sesnotify.SESEvent.ObjectKey()` reads the
key from. `TlsPolicy: Optional`, not `Require` — refusing an unsubscribe
because the sender's MTA doesn't offer STARTTLS is the wrong failure on
this address (RFC 8058 treats unsubscribe handling as something that must
not error):

```bash
aws ses create-receipt-rule --region us-east-1 \
  --rule-set-name opencircuit-inbound \
  --rule file://deploy/aws/A9-ses-receipt-rule.json
```

(If the gate found an existing active set, use that set's name in
`--rule-set-name` and add `--after <name of its last rule>` — see the gate
section above.)

**If this fails with a "could not write to bucket" error**, it is likely
A4's prefix scoping: SES writes an `AMAZON_SES_SETUP_NOTIFICATION` object to
the bucket when a rule with an `S3Action` is created, and whether that write
honours `ObjectKeyPrefix` was not confirmed from here. Temporarily widen A4
to `"Resource": "arn:aws:s3:::opencircuitsf-inbound/*"`, re-apply it
(`put-bucket-policy`), create the rule, then re-apply the prefix-scoped
`deploy/aws/A4-s3-bucket-policy.json` to narrow it back.

**6. A10 — activate the rule set** (only if the gate above found none
active; skip entirely if you added a rule to an existing set instead — that
set is already active and this step would needlessly replace it):

```bash
aws ses set-active-receipt-rule-set --rule-set-name opencircuit-inbound --region us-east-1
```

**7. Confirm the apex MX before touching DNS again** — the check that
matters, because a mistake in the next step costs real human mail:

```bash
dig +noall +answer MX opencircuitsf.com
```

Must read exactly `1 smtp.google.com.` and nothing else. Nothing in this
runbook creates, edits, or deletes any record at the bare apex.

**8. D2 — the MX. Apply this last; mail begins flowing only here.**

```bash
aws route53 change-resource-record-sets \
  --hosted-zone-id Z0825067RV8QY5UIKS96 \
  --change-batch file://deploy/aws/D2-route53-change-batch-mx.json
```

Capture the returned `ChangeInfo.Id` and wait for it to sync:

```bash
aws route53 get-change --id <ChangeId from above>
```

Wait for `"Status": "INSYNC"` before treating the record as live (usually
well under a minute).

**The RFC 4592 consequence, and why it's safe.** The zone's
`*.opencircuitsf.com` wildcard **A** record (today `98.84.75.184`, the web
server — measured against the zone's authoritative nameserver
`ns-923.awsdns-51.net`; a CNAME query at the name returns empty while an A
query returns this value directly, so it is an A record, not a CNAME, and
`ec2.smallsharptools.com` is not what it resolves to) currently answers for
`lists.opencircuitsf.com`. Per RFC 4592 a wildcard does not apply at a name
that owns any record of its own, so creating this MX stops the wildcard
answering at `lists.opencircuitsf.com` for **every** record type, not just
MX — A and CNAME queries at that exact name go NODATA afterward.

This was re-verified against the current code for this pass, not just
carried over from the plan: `grep -rn "EmailListDomain\|EMAIL_LIST_DOMAIN"
internal/` shows `config.Config.EmailListDomain` reaches exactly one
consumer, `internal/mailing/campaign_headers.go`, which uses it only to
build the `mailto:` form of `List-Unsubscribe`
(`"mailto:unsubscribe@" + listDomain + "?subject=..."`). Nothing in this
codebase — Go or the SPA — ever constructs an HTTPS URL on
`lists.opencircuitsf.com`; the visually similar `List-Id` header uses the
deliberately different singular `list.opencircuitsf.com`, which RFC 2919
makes an opaque identifier that never needs to resolve at all. So losing
web/CNAME resolution at `lists.opencircuitsf.com` breaks nothing currently
built.

**9. Verify end to end.** Send a real message from an outside mail account
to `unsubscribe@lists.opencircuitsf.com`, then:

```bash
aws s3 ls s3://opencircuitsf-inbound/unsubscribe/ --region us-east-1
```

Confirm an object appears. This alone proves A1–A10 and D2 are wired
correctly — it does **not** yet exercise `#0058`'s handler, since that
needs step 11 below (the SNS subscription) confirmed first.

**10. A11 — grant the instance role read/delete on the prefix, not the
bucket.** Last, because nothing reads the bucket until `#0058`'s deployed
code does:

```bash
aws iam put-role-policy --role-name opencircuit-instance \
  --policy-name opencircuit-inbound-s3 \
  --policy-document file://deploy/aws/A11-iam-inline-policy.json
```

### After this issue: deploying `#0058`'s code and A12

**Out of scope for this issue** — belongs to `#0058`'s own deploy, not
`#0057`'s: setting `SES_INBOUND_BUCKET=opencircuitsf-inbound` and
`SES_INBOUND_TOPIC_ARN=arn:aws:sns:us-east-1:378152330719:opencircuit-inbound-mail`
in `/etc/opencircuit/config.env` and restarting `opencircuit.service` so
`POST /api/ses/inbound` actually starts verifying and processing messages.
Both variables are already correct in `.env.example`
(`docs/configuration.md`).

**A12 — the SNS HTTPS subscription — only after that restart**, not before:
an SNS subscription that cannot be confirmed by the endpoint (because the
topic ARN isn't in the handler's allowlist yet, or the new binary isn't
running at all) stays `PendingConfirmation` and delivers nothing. This
updates the original plan: A12 was deferred indefinitely because
`POST /api/ses/inbound` didn't exist yet; it now exists (`#0058` shipped and
resolved 2026-09-10), so the only remaining gate is the config-env change
and restart above, not the code.

```bash
aws sns subscribe --region us-east-1 \
  --topic-arn arn:aws:sns:us-east-1:378152330719:opencircuit-inbound-mail \
  --protocol https \
  --notification-endpoint https://www.opencircuitsf.com/api/ses/inbound
```

Confirm it auto-confirmed (mirrors the existing `opencircuit-ses-events`
subscription's behavior):

```bash
aws sns list-subscriptions-by-topic --region us-east-1 \
  --topic-arn arn:aws:sns:us-east-1:378152330719:opencircuit-inbound-mail
```

`SubscriptionArn` should be a real ARN, not the literal string
`PendingConfirmation`. Mail landing in S3 (step 9) still works with A12
un-confirmed — nothing is lost while this step waits — but a subscriber's
message will not actually be unsubscribed until it is.

### Rollback

Ordered from fastest/safest to slowest, matching the build order in
reverse for the parts that were actually created this pass:

| Undo | Effect |
|---|---|
| **Delete the D2 record set** (`lists.opencircuitsf.com` MX) | Inbound routing stops immediately; the wildcard resumes answering that name within the 300s TTL. **This alone fully reverts the routing change.** |
| `aws ses set-active-receipt-rule-set --region us-east-1` with no `--rule-set-name` (or back to whatever was active before, per the gate) | SES stops applying the rule |
| `aws ses delete-receipt-rule` / `delete-receipt-rule-set` | removes A9/A8 |
| `aws sns delete-topic` on `opencircuit-inbound-mail` | removes A6/A7 |
| Empty then delete the bucket `opencircuitsf-inbound` | removes A2–A5 — **but this bucket predates this pass; confirm nothing else depends on it before deleting** |
| Delete the D1 record set, delete the SES identity `lists.opencircuitsf.com` | removes the verification — again, both predate this pass |
| `aws iam delete-role-policy --role-name opencircuit-instance --policy-name opencircuit-inbound-s3` | removes the instance-role grant |

**Confirm apex mail still flows** after any of the above, especially after
D2:

- `dig +noall +answer MX opencircuitsf.com` → still exactly
  `1 smtp.google.com.`
- `dig +short MX lists.opencircuitsf.com` → `10
  inbound-smtp.us-east-1.amazonaws.com`, and the *only* MX at that name
- `dig +short MX bounce.mailing.opencircuitsf.com` → still `10
  feedback-smtp.us-east-1.amazonses.com`, unchanged
- Send from an outside address to a real Google Workspace mailbox on the
  apex and confirm delivery

Nothing in this section touches the apex, `bounce.mailing.`, `www`, the
zone wildcard, or the `/.well-known/` Apache exception.

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
  `contact@mailing.opencircuitsf.com`, with `Reply-To: contact@opencircuitsf.com`
  so replies reach a Google Workspace inbox that a human already reads. That
  also settles the "who reads that inbox" half of `CLAUDE.md` §10 item 4.

Still not resolved, and not an SES problem: **campaigns refuse to start
without a `physical_address` setting** (`#0045`, CAN-SPAM §7704,
`CLAUDE.md` §10 item 3). That needs a PO box.
