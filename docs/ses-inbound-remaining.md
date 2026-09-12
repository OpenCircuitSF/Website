# SES inbound unsubscribe — what is built, and what remains

Status of `#0057` (AWS/DNS plumbing) and `#0058` (the handler), written
2026-09-12. This is the "what is left" companion to
[`aws-requests.md`](aws-requests.md), which is the operator runbook for the
AWS objects themselves.

Everything below was measured against the live account
(`378152330719`, `us-east-1`) and the production host, not taken from a plan.

---

## 1. What already exists

All of these were created and verified; nothing here is outstanding.

| # | Object | Value | State |
|---|---|---|---|
| A1 | SES domain identity | `lists.opencircuitsf.com` | **verified** — `VerificationStatus: SUCCESS`, DKIM `SUCCESS` |
| A2 | S3 bucket | `opencircuitsf-inbound` | created, `us-east-1` |
| A3 | Block Public Access | all four flags | on |
| A4 | Bucket policy | `ses.amazonaws.com` `s3:PutObject` on `unsubscribe/*` only, conditioned on `AWS:SourceAccount` **and** the exact rule ARN | applied |
| A5 | Lifecycle rule | `expire-inbound-30d`, 30 days, prefix `unsubscribe/` | applied |
| A6 | SNS topic | `arn:aws:sns:us-east-1:378152330719:opencircuit-inbound-mail` | created |
| A7 | Topic policy | `ses.amazonaws.com` `SNS:Publish`, same two conditions | applied |
| A8 | Receipt rule set | `opencircuit-inbound` | created, **not active** |
| A9 | Receipt rule | `unsubscribe`, one `S3Action` with `TopicArn` set, `ObjectKeyPrefix: unsubscribe/`, `TlsPolicy: Optional`, `ScanEnabled: true` | created |
| D1 | Verification TXT | `_amazonses.lists.opencircuitsf.com` | published |
| D3 | DKIM CNAMEs | three `*._domainkey.lists.opencircuitsf.com` | published |

Two things worth knowing about that list:

- **SES accepted the rule at creation**, which means it validated the bucket
  and topic policies at that moment. The permissions are proven, not assumed.
- `AMAZON_SES_SETUP_NOTIFICATION` is sitting in
  `s3://opencircuitsf-inbound/unsubscribe/` — SES's own test write, further
  confirmation the write path works.

### A correction the original plan got wrong

`#0057`'s plan specified **D1 (the `_amazonses` TXT)** as the verification
record and said DKIM was unnecessary on a receive-only domain. That is the SES
**v1** verification path. `sesv2 create-email-identity` enables Easy DKIM, and
an identity created that way verifies through the **three DKIM CNAMEs**
instead. The identity sat `Pending` with `VerificationInfo.ErrorType:
TYPE_NOT_FOUND` for over an hour — SES was looking for records the plan never
said to create. Publishing D3 is what verified it. The TXT alone never would
have.

---

## 2. What remains

### 2a. Two AWS steps, gated on approval

Both are the deliberate stop points. Each has a one-command undo.

**Step 7 — activate the rule set.** Account-wide in `us-east-1`.
The gate was re-checked: `describe-active-receipt-rule-set` is empty and
`list-receipt-rule-sets` returns `[]`, so nothing is displaced. This account
also hosts ShortLinks, which is why the check matters.

    aws --profile admin ses set-active-receipt-rule-set \
      --rule-set-name opencircuit-inbound --region us-east-1

Undo: `set-active-receipt-rule-set` with no `--rule-set-name`.

**Step 8 — the MX.** The only step that starts routing mail.

    aws --profile admin route53 change-resource-record-sets \
      --hosted-zone-id Z0825067RV8QY5UIKS96 \
      --change-batch '{"Changes":[{"Action":"CREATE","ResourceRecordSet":{
        "Name":"lists.opencircuitsf.com","Type":"MX","TTL":300,
        "ResourceRecords":[{"Value":"10 inbound-smtp.us-east-1.amazonaws.com"}]}}]}'

Undo: delete that record set; the wildcard resumes within the 300s TTL.

**The consequence to accept before running step 8.** Per RFC 4592 a wildcard
does not apply at a name owning any record, so creating this MX stops
`lists.opencircuitsf.com` resolving for *every* type — it currently answers
`98.84.75.184` via the zone wildcard. Verified safe: `EMAIL_LIST_DOMAIN` is
consumed in exactly one place, `mailing.CampaignHeaders`, and only to build the
`mailto:unsubscribe@<listDomain>` form of `List-Unsubscribe`. Nothing builds an
HTTPS URL on it. `List-Id` uses the deliberately different singular
`list.opencircuitsf.com`, which RFC 2919 makes an opaque identifier.

**Do not touch the apex MX.** It is `1 smtp.google.com` and carries real human
Google Workspace mail. Diff `dig +noall +answer MX opencircuitsf.com` before
and after.

### 2b. `A11` — the IAM grant, and it needs correcting first

The plan grants `s3:GetObject` and `s3:DeleteObject` on
`arn:aws:s3:::opencircuitsf-inbound/unsubscribe/*` to the instance role
**`opencircuit-instance`**.

**That role is not what production uses.** The live box runs
**`opencircuit-web-2026`** (see `#0508`). Applying `A11` as written grants to
the wrong principal and the handler would get `AccessDenied`. Grant to
`opencircuit-web-2026`, scoped to the prefix rather than the bucket.

### 2c. `A12` — the SNS subscription, now unblocked

`A12` was deferred because `POST /api/ses/inbound` did not exist. **`#0058`
built it** (resolved `f6fd14a`), so the subscription can be created — but only
*after* the code is deployed, because an HTTPS subscription that the endpoint
cannot confirm stays `PendingConfirmation` and delivers nothing.

Order: deploy → set config → restart → subscribe.

---

## 3. What `#0058` needs to actually work in production

The code is merged and green. Four things stand between that and a working
inbound path:

1. **A deploy.** Production is **900 commits** behind at `ef0a58f`, and its
   schema is at version **22** against **28** migrations on disk — so
   `000023`–`000028` would apply. Blocked in turn by `#0508` (the deploy
   tooling points at a decommissioned host) and `#0509` (no Go toolchain on the
   new box).
2. **Two config variables**, absent from the live `/etc/opencircuit/config.env`
   and already correct in `.env.example`:

       SES_INBOUND_BUCKET=opencircuitsf-inbound
       SES_INBOUND_TOPIC_ARN=arn:aws:sns:us-east-1:378152330719:opencircuit-inbound-mail

   Both are optional in the loader, so the service boots without them — the
   route simply is not registered. Setting them needs a restart, which is why
   it belongs in the deploy window.
3. **Steps 7 and 8 above**, or no mail ever arrives.
4. **`A11` corrected to `opencircuit-web-2026`**, or every fetch is denied.

---

## 4. Known follow-ups, none blocking

| Issue | What |
|---|---|
| `#0500` | `ObjectKey()`'s prefix fallback is believed dead against real SES. **The first real message is the oracle** — capture its JSON and either delete the fallback or pin it with a test. Sharp edge: a 404 routes to the retryable 500 path and would retry to exhaustion. |
| `#0501` | A `Delete` failure after a committed unsubscribe leaves a duplicate audit row on the `From:` path. Benign; the commit-then-delete ordering is correct. |
| `#0502` | `#0237`'s audit-metadata guard does not recognise `"from"`. Must land before `#0503`. |
| `#0503` | **Policy decision, the user's to make.** Whether a subscriber's address in the parked-path audit row joins the privacy policy's erasure list, and what retention applies to non-subscriber addresses now held permanently in an append-only table. |

## 5. The escape hatch, still open

`#0057` offers it and it remains reasonable: point the `mailto:` at a monitored
mailbox and process unsubscribes by hand. Paths 1 and 2 (the one-click and
preference-centre routes) are the mandatory ones; **path 3 is optional in v1**,
and at current list size — 4 active subscribers — manual handling is honest.

The argument for finishing it anyway is that everything expensive is already
built and verified; what remains is two commands, one IAM correction, and a
deploy that has to happen regardless.
