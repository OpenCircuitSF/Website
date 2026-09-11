# AWS work requested — Open Circuit SF

Written 2026-09-05 for an AWS operator working **outside** this repository. It
is self-contained: every fact below was read live from the account or the box,
and the sources are named so you can re-derive them rather than trust them.

**Account `378152330719`, region `us-east-1` throughout.** Do not use
`us-west-2` — `PRD.md` §10.3 and `docs/email-setup.md` both name it and both are
stale; the instance, the SES identity, the configuration set and the MAIL FROM
MX are all in `us-east-1`.

---

## Guardrails — read before anything else

**1. Never touch the apex MX.** `opencircuitsf.com. 3600 IN MX 1 SMTP.GOOGLE.COM.`
is a real Google Workspace mailbox that predates this project. Pointing it at
SES hijacks all human mail for the domain. Nothing requested here reads or
writes it.

**2. `SetActiveReceiptRuleSet` is account-wide per region.** One active receipt
rule set exists per account per region, and **this account also hosts a second
project (ShortLinks)**. Activating a new set displaces whatever is active. Part
1's second question exists to establish that before anything is created.

**3. Do not run `certbot --apache` on the box.** The live certificate is an
ECDSA **wildcard** (`*.opencircuitsf.com` + apex) issued via the `dns-route53`
authenticator. `--apache` is HTTP-01 and cannot issue a wildcard — running it
would replace the wildcard with a two-name cert and take `go.opencircuitsf.com`
(the other project) offline. A stale command in our own runbook says otherwise;
we are fixing it.

**4. Nothing here requires changing `/.well-known/`.** It is an Apache carve-out
serving a Bluesky DID from disk. Certificate renewal does not use it (DNS-01),
so no change below should touch it.

---

## Part 1 — two read-only answers we cannot get ourselves

The instance's own identity (`certbot-dns-updater`) is denied both calls, and
the instance role is denied IAM reads. Confirmed three ways.

```bash
aws sesv2 get-account --region us-east-1
aws ses describe-active-receipt-rule-set --region us-east-1
```

**What we need from the first:** `ProductionAccessEnabled`.

This is not curiosity. Production currently runs `SES_SANDBOX=false` and
`MAX_SEND_RATE=10`. If the account is **still sandboxed**, both are wrong:
the sandbox caps sending at 1/second and 200/day to verified identities only,
so `MAX_SEND_RATE=10` is ten times the cap, and `SES_SANDBOX=false` stops our
code applying sandbox-aware handling while AWS still enforces it. We would fix
the configuration rather than send.

**What we need from the second:** whether a rule set is already active.

- **Empty** → Part 2 may create and activate its own rule set.
- **Non-empty** → **stop.** Tell us the name and its rules; we will add a rule
  to the existing set instead of activating a new one, because activation would
  displace the other project's configuration.

---

## Part 2 — SES inbound mail for unsubscribe handling

**Purpose.** RFC 8058 requires a one-click unsubscribe, and a mail-based path is
one of three we must support. Mail sent to `unsubscribe@lists.opencircuitsf.com`
must land in S3 for our service to process.

**Only proceed once Part 1's second answer is known.**

### The DNS changes — two record sets

Zone `Z0825067RV8QY5UIKS96` (`opencircuitsf.com.`). D1 is **already applied**
(see below); D2 is the one remaining `CREATE`, at a name that holds no record
set today.

| # | Name | Type | Value | TTL |
|---|---|---|---|---|
| D1 | `_amazonses.lists.opencircuitsf.com` | TXT | **already applied** — `"APWUrtnPLURlLWOGg0ybU3t6HbptTzDE77f8JE1YHX0="`, confirmed live 2026-09-11. Do not re-run as `CREATE`; it will fail | 300 |
| D2 | `lists.opencircuitsf.com` | MX | `10 inbound-smtp.us-east-1.amazonaws.com` | 300 |

**One non-obvious consequence, already checked.** The zone has a wildcard
`*.opencircuitsf.com` **A record** answering `98.84.75.184` (the web server).
Per RFC 4592 a wildcard stops applying at any name owning a record, so
creating D2 makes `lists.opencircuitsf.com` answer NODATA for A and CNAME as
well as gaining an MX.

That is safe: nothing in our application builds an HTTPS URL on that name. It is
used only to construct a `mailto:` header.

### The AWS objects

| # | Object | Name | Notes |
|---|---|---|---|
| A1 | SES domain identity | `lists.opencircuitsf.com` | **already exists and is verified**, confirmed 2026-09-11 — receive-only; no DKIM, no MAIL FROM, never used to send |
| A2 | S3 bucket | `opencircuitsf-inbound` | **already exists** (`head-bucket` → 403 against a random-name control's 404, confirmed 2026-09-11). Its public-access-block, lifecycle and bucket-policy state could not be read from here — read each with the matching `get-` command (below) before applying A3/A4/A5 |
| A3 | S3 Block Public Access | all four flags on A2 | holds inbound mail; must never be public |
| A4 | S3 bucket policy | on A2 | allow `ses.amazonaws.com` `s3:PutObject` on `…/unsubscribe/*` **only**, conditioned on `AWS:SourceAccount` **and** `AWS:SourceArn` of the exact receipt rule |
| A5 | S3 lifecycle rule | `expire-inbound-30d`, prefix `unsubscribe/` | `Expiration: 30 days` |
| A6 | SNS topic | `opencircuit-inbound-mail` | **separate from the existing `opencircuit-ses-events`** — do not reuse it; our handler dispatches on `TopicArn` |
| A7 | SNS topic policy | on A6 | allow `ses.amazonaws.com` `SNS:Publish`, same two conditions |
| A8 | SES receipt rule set | `opencircuit-inbound` | **or a rule added to the already-active set**, per Part 1 |
| A9 | SES receipt rule | `unsubscribe` in A8 | see below |
| A10 | Activate A8 | `SetActiveReceiptRuleSet` | **the account-wide step** |
| A11 | IAM inline policy | `opencircuit-inbound-s3` on role `opencircuit-instance` | `s3:GetObject`, `s3:DeleteObject` on `arn:aws:s3:::opencircuitsf-inbound/unsubscribe/*` — **the prefix, not the bucket** |

**A9's shape:**

- `Recipients: ["unsubscribe@lists.opencircuitsf.com"]` — the one mailbox, **not
  the domain**, so anything else sent to the subdomain gets SES's default 550
  rather than being silently captured.
- `Enabled: true`, `ScanEnabled: true`
- `TlsPolicy: Optional` — **not `Require`**. `Require` bounces senders whose MTA
  does not offer STARTTLS, and refusing an unsubscribe request is the wrong
  failure under RFC 8058.
- **One action only**: an `S3Action` → bucket A2, `ObjectKeyPrefix: "unsubscribe/"`,
  with its optional `TopicArn` set to A6.

**Why one action and not two.** A standalone SNS receipt action publishes the
message content with a 150 KB cap and **carries no S3 object key** — our
consumer could not find the mail. The `S3Action`'s own `TopicArn` produces a
notification that *does* name the key. One action satisfies both needs.

**Not yet:** an SNS HTTPS subscription to our endpoint. `POST /api/ses/inbound`
does not exist yet. A subscription that cannot be confirmed stays
`PendingConfirmation` and delivers nothing; mail still lands in S3 meanwhile, so
nothing is lost by waiting.

### Ordering — the MX goes last

Every AWS object is inert until the MX exists, so build the destination first
and route mail to it only at the end.

1. **Gate**: Part 1's `describe-active-receipt-rule-set`.
2. A1 — **already done** (identity verified 2026-09-11); confirm with
   `aws sesv2 get-email-identity --email-identity lists.opencircuitsf.com --region us-east-1`
   rather than creating it again.
3. D1 — **already applied**; confirm the TXT resolves rather than re-running
   it as `CREATE` (it will fail — the record set already exists).
4. A2 — **the bucket already exists**; read its current public-access-block,
   lifecycle and bucket-policy state with the matching `get-` command first
   (`put-bucket-policy` and `put-bucket-lifecycle-configuration` both
   **replace** the whole configuration rather than merging, so applying blind
   risks silently discarding whatever is already there). Then apply A3, A5,
   A4 in that order — lock it down, lifecycle, then policy.
5. A6, A7 — topic and policy.
6. A8, A9 — rule set and rule.
7. A10 — activate.
8. **D2 — the MX.** Mail begins flowing only here.
9. Verify: send a real message to `unsubscribe@lists.opencircuitsf.com` and
   confirm an object appears under `s3://opencircuitsf-inbound/unsubscribe/`.
10. A11 — the instance-role grant, last; nothing reads the bucket yet.

### Rollback

The failure mode is lost mail, so the riskiest step is last and fastest to undo.

| Undo | Effect |
|---|---|
| **Delete D2** | Inbound routing stops immediately; the wildcard resumes within 300s. **This alone fully reverts the routing change.** |
| `SetActiveReceiptRuleSet` back to the previous set (or none) | SES stops applying the rule |
| `DeleteReceiptRule` / `DeleteReceiptRuleSet` | removes A9 / A8 |
| Delete topic A6 | removes A6, A7 |
| Empty and delete A2 | removes A2–A5 |
| Delete D1, delete identity A1 | removes the verification |
| Delete the inline policy A11 | removes the grant |

**Confirm human mail still flows** — the check that matters most:

```bash
dig +noall +answer MX opencircuitsf.com          # must still be: 1 smtp.google.com.
dig +short MX lists.opencircuitsf.com            # 10 inbound-smtp.us-east-1.amazonaws.com, and ONLY that
dig +short MX bounce.mailing.opencircuitsf.com   # unchanged: 10 feedback-smtp.us-east-1.amazonses.com
```

Run the first before and after and diff it. Then send a message from an outside
address to a real Workspace mailbox on the apex and confirm delivery.

---

## Part 3 — if SES is still sandboxed

Should Part 1 report `ProductionAccessEnabled: false`, we will want production
access requested. Useful context for that request: this is a double opt-in
mailing list for a San Francisco electronics workshop, with unsubscribe
handling on three paths, a suppression list, bounce and complaint processing via
SNS, and a physical postal address enforced in every message.

**A trap worth knowing** if you touch the sending policy: in the sandbox, SES
authorises `SendEmail` against the **recipient's** identity ARN as well as the
sender's, so the role policy needs `identity/*` rather than a single identity.
A test send to `success@simulator.amazonses.com` will **not** catch a policy
that gets this wrong, because simulator addresses are not identities.

---

## What we have already verified, so you need not

Read live on 2026-09-04/05, read-only, with the rows below re-measured
2026-09-11 (each says so):

- Instance `i-0e3bd89e87d1c2364`, region `us-east-1`, AZ `us-east-1b`
- Instance role **`opencircuit-instance`** is attached (three ways: IMDS
  `iam/security-credentials/`, `iam/info`, and `sts get-caller-identity`
  returning `assumed-role/opencircuit-instance/i-0e3bd89e87d1c2364`)
- Sending identity `mailing.opencircuitsf.com`, DKIM and SPF present
- Custom MAIL FROM `bounce.mailing.opencircuitsf.com` → `feedback-smtp.us-east-1.amazonses.com`
- Configuration set `opencircuit-transactional`; events topic
  `arn:aws:sns:us-east-1:378152330719:opencircuit-ses-events`
- **`lists.opencircuitsf.com` (A1) already exists as a verified SES domain
  identity, and its `_amazonses.lists.opencircuitsf.com` TXT (D1,
  `"APWUrtnPLURlLWOGg0ybU3t6HbptTzDE77f8JE1YHX0="`) already resolves —
  confirmed 2026-09-11.** Skip ordering steps 2 and 3 below other than
  confirming; re-applying D1 as `CREATE` will fail because the record set
  already exists.
- **`s3://opencircuitsf-inbound` (A2) already exists** — `head-bucket` returns
  403 against it, versus 404 for a random-name control, confirmed 2026-09-11.
  **Its lockdown state (public-access-block, lifecycle, bucket policy) could
  not be read from here** — read each with the matching `get-` command before
  applying A3/A4/A5, since `put-bucket-policy` and
  `put-bucket-lifecycle-configuration` both **replace** rather than merge the
  existing configuration.
- DMARC lives at `_dmarc.mailing.opencircuitsf.com` (`p=none`), **not** at the
  apex — deliberate, and our own docs were wrong about it

**We could not read**, and did not pursue: the `opencircuit-instance` role's
attached policy documents, `sesv2 get-account`, and A2's public-access-block/
lifecycle/policy state. All denied or unreadable from every identity
available on the box. No credentials were created or sought.
