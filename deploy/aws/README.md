# AWS objects for inbound unsubscribe (`#0057`)

Ready-to-apply policy and rule documents for the AWS-side plumbing that
routes mail sent to `unsubscribe@lists.opencircuitsf.com` into S3 and
notifies `POST /api/ses/inbound` (`#0058`, already built and merged). The
full step-by-step runbook — ordering, the account-wide gate, and rollback —
is [`docs/email-setup.md`](../../docs/email-setup.md#inbound-unsubscribe-phase-4-0057).
This file is only the manifest: which plan row each document is, and the
exact command that applies it.

None of these have been applied to AWS by an agent. Read-only checks
(2026-09-11) found the SES domain identity and its DNS verification TXT
(A1/D1) and the S3 bucket (A2) already exist; everything else in this table
does not yet exist in the account.

**These JSON files must stay free of comments** — they are passed verbatim
to `aws ... --policy file://…` / `--rule file://…` / `--change-batch
file://…`, and IAM/S3/SNS/SES all reject an unrecognized top-level key in a
policy or rule document. Only the two Route 53 change batches (D1, D2) carry
a `Comment` field, because `Comment` is itself part of Route 53's
`ChangeBatch` schema.

**A9's `--rule-set-name` and the `AWS:SourceArn` condition in A4/A7 assume
the gate found no active rule set**, so a fresh set named
`opencircuit-inbound` is created. If `aws ses
describe-active-receipt-rule-set --region us-east-1` instead returns an
already-active set, replace `opencircuit-inbound` with that set's real name
in A4, A7, and the `--rule-set-name` flag below before applying anything,
and add `--after <name-of-its-last-rule>` to A9's `create-receipt-rule` call
so the new rule is appended rather than inserted first. Do not create or
activate a second rule set in that case — see `docs/email-setup.md`.

| # | File | Plan row | Applies with |
|---|---|---|---|
| A4 | `A4-s3-bucket-policy.json` | S3 bucket policy: `ses.amazonaws.com` may `s3:PutObject` only under `unsubscribe/`, scoped to this account and this exact receipt rule | `aws s3api put-bucket-policy --bucket opencircuitsf-inbound --policy file://deploy/aws/A4-s3-bucket-policy.json --region us-east-1` |
| A5 | `A5-s3-lifecycle.json` | S3 lifecycle rule `expire-inbound-30d`: expires objects under `unsubscribe/` after 30 days | `aws s3api put-bucket-lifecycle-configuration --bucket opencircuitsf-inbound --lifecycle-configuration file://deploy/aws/A5-s3-lifecycle.json --region us-east-1` |
| A7 | `A7-sns-topic-policy.json` | SNS topic policy on `opencircuit-inbound-mail`: owner keeps full control, plus `ses.amazonaws.com` may `SNS:Publish`, scoped the same way as A4 | `aws sns set-topic-attributes --topic-arn arn:aws:sns:us-east-1:378152330719:opencircuit-inbound-mail --attribute-name Policy --attribute-value file://deploy/aws/A7-sns-topic-policy.json --region us-east-1` |
| A9 | `A9-ses-receipt-rule.json` | The `unsubscribe` receipt rule: single `S3Action` with `TopicArn` set (not a second, separate SNS action — see `docs/email-setup.md`), `TlsPolicy: Optional`, `ObjectKeyPrefix: "unsubscribe/"` | `aws ses create-receipt-rule --rule-set-name opencircuit-inbound --rule file://deploy/aws/A9-ses-receipt-rule.json --region us-east-1` |
| A11 | `A11-iam-inline-policy.json` | IAM inline policy on the `opencircuit-instance` role: `s3:GetObject`/`s3:DeleteObject` on the `unsubscribe/` prefix only, never the whole bucket | `aws iam put-role-policy --role-name opencircuit-instance --policy-name opencircuit-inbound-s3 --policy-document file://deploy/aws/A11-iam-inline-policy.json` |
| D1 | `D1-route53-change-batch-txt.json` | SES domain-identity verification TXT for `lists.opencircuitsf.com`. **Already applied** — kept for reference and as the exact rollback undo | `aws route53 change-resource-record-sets --hosted-zone-id Z0825067RV8QY5UIKS96 --change-batch file://deploy/aws/D1-route53-change-batch-txt.json` |
| D2 | `D2-route53-change-batch-mx.json` | The inbound MX for `lists.opencircuitsf.com`. **Apply last** — see the ordering and RFC 4592 note in `docs/email-setup.md` | `aws route53 change-resource-record-sets --hosted-zone-id Z0825067RV8QY5UIKS96 --change-batch file://deploy/aws/D2-route53-change-batch-mx.json` |

Not represented as a file here, because each is a single flag-driven command
with no JSON body worth templating — see the runbook for the exact
invocation: the account-wide gate (`describe-active-receipt-rule-set`), A2
(`create-bucket`), A3 (`put-public-access-block`), A6 (`sns create-topic`),
A8 (`create-receipt-rule-set`), A10 (`set-active-receipt-rule-set`), and A12
(the SNS HTTPS subscription, deferred until `#0058`'s code is deployed and
`SES_INBOUND_TOPIC_ARN` is set — see the runbook).

Every file here validates as JSON (`python3 -m json.tool <file>`); none has
been checked against a live AWS API call, since applying any of them is the
user's step, not an agent's (`CLAUDE.md` §8b, §9; this issue's own plan,
"Split: user versus agent").
