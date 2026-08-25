# AWS IAM setup for SES

What has to be added by hand in the AWS console before the SES work can
finish, and why each piece exists.

**Written 2026-08-25**, during the SES enablement pass. Everything below was
measured against account `378152330719` — the policy names, ARNs, and instance
ID are the real ones, not examples.

---

## The short version

The EC2 instance has **no IAM role attached**, so the running service has no
AWS credentials at all and cannot call SES no matter how well SES itself is
configured. Fixing that means creating a role and an instance profile, and the
`cli-admin` user cannot do it — it has `AmazonSESFullAccess`, `AmazonSNSFullAccess`,
`AmazonRoute53FullAccess`, `AmazonS3FullAccess`, `AmazonEC2FullAccess`, and an
inline policy called `cli-admin-iam-support` that grants IAM **read** plus
management of `ses-smtp-*` **users** — but nothing that touches **roles** or
**instance profiles**.

**One statement added to that existing inline policy unblocks everything.** It
is in [Part 1](#part-1-widen-cli-admin). If you would rather not widen
`cli-admin` at all, [Part 3](#part-3-alternative--build-the-role-yourself)
builds the same role by hand instead.

---

## The facts this rests on

| | |
|---|---|
| AWS account | `378152330719` |
| EC2 instance | `i-0e3bd89e87d1c2364` (`t4g.nano`, **`us-east-1`**) |
| Current instance role | **none** — the metadata service 404s `iam/security-credentials/` |
| SES region | **`us-west-2`** (PRD §10.3 — chosen for inbound receiving support, and independent of where the instance lives) |
| SES identity | `arn:aws:ses:us-west-2:378152330719:identity/mailing.opencircuitsf.com` — **verified**, DKIM `SUCCESS`, custom MAIL FROM `bounce.mailing.opencircuitsf.com` `SUCCESS` |
| SES configuration set | `arn:aws:ses:us-west-2:378152330719:configuration-set/opencircuit-transactional` |
| SNS topic for events | `arn:aws:sns:us-west-2:378152330719:opencircuit-ses-events` |
| Admin CLI user | `arn:aws:iam::378152330719:user/cli-admin` |

The instance being in `us-east-1` while SES is in `us-west-2` is fine and
deliberate. IAM is global; the SDK reaches SES in whatever region
`AWS_REGION` names, which `/etc/opencircuit/config.env` already sets to
`us-west-2`.

**The sending identity is the `mailing.` subdomain, not the apex** (user's
decision, 2026-08-25). `opencircuitsf.com` carries the domain's real human
mail through Google Workspace, and sending bulk list mail as the apex would
put a spam complaint against a workshop announcement onto the same domain
reputation as that business mail. Isolating it costs nothing and is
irreversible-ish once subscribers have filed the sender, so it was decided
before anything reached DNS. Concretely:

| | |
|---|---|
| `From:` | `Open Circuit SF <hello@mailing.opencircuitsf.com>` |
| `Reply-To:` | `contact@opencircuitsf.com` — replies land in the normal Google inbox |
| Envelope / `Return-Path` | `bounce.mailing.opencircuitsf.com` |
| Apex MX | **untouched** — still `1 smtp.google.com` |
| Apex SPF / TXT | **untouched** — the apex needs no SES-related record at all, because nothing sends as the apex |
| DMARC | at `_dmarc.mailing.opencircuitsf.com`, so the policy applies to list mail only and the apex keeps its current (absent) posture |

That last row is the reason DMARC is *not* at the apex: a receiver resolving
DMARC for `mailing.opencircuitsf.com` checks `_dmarc.mailing.…` first and only
falls back to the organizational domain if it is missing. Putting it on the
subdomain gives the list its own policy without changing anything about how
the world treats your Google Workspace mail.

---

## Part 1 — widen `cli-admin`

### What to add

In the console: **IAM → Users → `cli-admin` → Permissions → the inline policy
`cli-admin-iam-support` → Edit → JSON**, and add these two statements to the
existing `Statement` array. Leave everything already in that policy alone.

```json
{
  "Sid": "ManageOpenCircuitInstanceRole",
  "Effect": "Allow",
  "Action": [
    "iam:CreateRole",
    "iam:DeleteRole",
    "iam:TagRole",
    "iam:UntagRole",
    "iam:PutRolePolicy",
    "iam:DeleteRolePolicy",
    "iam:AttachRolePolicy",
    "iam:DetachRolePolicy",
    "iam:UpdateAssumeRolePolicy",
    "iam:CreateInstanceProfile",
    "iam:DeleteInstanceProfile",
    "iam:AddRoleToInstanceProfile",
    "iam:RemoveRoleFromInstanceProfile"
  ],
  "Resource": [
    "arn:aws:iam::378152330719:role/opencircuit-*",
    "arn:aws:iam::378152330719:instance-profile/opencircuit-*"
  ]
},
{
  "Sid": "PassOpenCircuitRoleToEC2Only",
  "Effect": "Allow",
  "Action": "iam:PassRole",
  "Resource": "arn:aws:iam::378152330719:role/opencircuit-*",
  "Condition": {
    "StringEquals": { "iam:PassedToService": "ec2.amazonaws.com" }
  }
}
```

### Why it is shaped that way

- **Name-scoped, not `*`.** Both statements are confined to ARNs beginning
  `opencircuit-`. `cli-admin` still cannot create, edit, or delete any other
  role in the account, which is the difference between this and attaching
  `IAMFullAccess`.
- **`iam:PassRole` is separate and conditioned.** Attaching a role to an
  instance is not "create a role" — it is "hand this role's credentials to
  EC2", and AWS gates it behind `PassRole` specifically because it is the
  privilege-escalation step. The `iam:PassedToService` condition means the
  role can only ever be handed to EC2, never to Lambda, ECS, or anything else.
- **No read actions listed.** The policy's existing `ReadAllIam` statement
  already grants `iam:Get*` and `iam:List*` on `*`.
- **Nothing EC2-side is needed.** `AmazonEC2FullAccess` already covers
  `ec2:AssociateIamInstanceProfile` and friends.

### Removing it afterwards

This is a one-time setup grant. Once the role exists and is attached, both
statements can be deleted again — the running instance keeps its role, and
nothing in the deploy path needs to create roles a second time. Keep them only
if you would rather manage the role from the CLI going forward.

---

## Part 2 — what gets created with it

For review. If you grant Part 1, these are the exact documents that will be
created; if you would rather build them yourself, Part 3 is the console walk-through.

### The role: `opencircuit-instance`

**Trust policy** — who may assume it. EC2 only:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Service": "ec2.amazonaws.com" },
      "Action": "sts:AssumeRole"
    }
  ]
}
```

**Permission policy** — an inline policy named `opencircuit-ses-send`. This is
PRD §10.5's "scoped tightly: no `ses:*`, no wildcard resources":

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SendThroughVerifiedIdentityAndConfigSet",
      "Effect": "Allow",
      "Action": [
        "ses:SendEmail",
        "ses:SendRawEmail"
      ],
      "Resource": [
        "arn:aws:ses:us-west-2:378152330719:identity/mailing.opencircuitsf.com",
        "arn:aws:ses:us-west-2:378152330719:configuration-set/opencircuit-transactional"
      ]
    }
  ]
}
```

Both ARNs are required, not one or the other. `internal/mailing`'s SES mailer
calls the **v2** `SendEmail` with `ConfigurationSetName` always set
(`ses_mailer.go`), and SES authorizes that call against the identity **and**
the configuration set as two separate resources. Omitting the configuration-set
ARN produces an `AccessDenied` that reads as though the identity were the
problem.

`ses:SendRawEmail` is included because the v2 API maps onto it for MIME
content; the mailer sends `multipart/alternative` for HTML campaigns.

### The instance profile: `opencircuit-instance`

An instance profile is just a container that lets EC2 hand a role to an
instance — same name as the role by convention. The role goes into the
profile, and the profile is associated with `i-0e3bd89e87d1c2364`.

### What is deliberately *not* in it

- **No `s3:*`.** PRD §10.5 also lists `s3:GetObject`/`s3:DeleteObject` on
  `arn:aws:s3:::opencircuitsf-inbound/*`, but that is for the **inbound**
  `mailto:` unsubscribe path (PRD §6.5 path 3, issue `#0057`, Phase 4). That
  bucket does not exist and inbound is out of scope for this pass. Add the
  statement when `#0057` is built, not before.
- **No `ses:GetAccount`, `ses:ListEmailIdentities`, or any other read.** The
  service never introspects SES; `SES_SANDBOX` is a manually-set flag in
  `config.env` precisely because no live AWS call in this codebase can detect
  sandbox status.
- **No SNS permissions.** Events flow *from* SES *to* SNS *to* an HTTPS POST
  at `/api/ses/notifications`. The service is the receiving end and never
  calls SNS itself — it verifies the message signature against the SNS signing
  certificate instead (`internal/sesnotify`).

---

## Part 3 — alternative: build the role yourself

Skip Part 1 entirely if you would rather not widen `cli-admin`. Console steps:

1. **IAM → Roles → Create role.**
2. Trusted entity type **AWS service**, use case **EC2**. Next.
3. Skip attaching managed policies — this role gets an inline one. Next.
4. Role name: **`opencircuit-instance`**. Create.
5. Open the new role → **Permissions → Add permissions → Create inline policy
   → JSON**. Paste the `opencircuit-ses-send` document from Part 2. Name it
   **`opencircuit-ses-send`**. Create.
6. **EC2 → Instances → `i-0e3bd89e87d1c2364` → Actions → Security → Modify IAM
   role** → pick `opencircuit-instance` → Update.

Creating the role through the console builds the matching instance profile
automatically; the CLI path does not, which is why Part 1 lists the
instance-profile actions explicitly.

Step 6 takes effect immediately — no instance restart is needed, though
`opencircuit.service` must be restarted so the AWS SDK picks up credentials it
previously found nothing for.

---

## Verifying it worked

From your Mac (read-only is enough):

```bash
AWS_PROFILE=admin aws ec2 describe-instances \
  --instance-ids i-0e3bd89e87d1c2364 \
  --query 'Reservations[].Instances[].IamInstanceProfile' --output json
```

Expect the profile ARN, not `null`.

Then on the box — this is the check that actually matters, because it proves
the *instance* can reach credentials, not just that a role exists:

```bash
ssh ec2 'TOKEN=$(curl -s -X PUT "http://169.254.169.254/latest/api/token" \
  -H "X-aws-ec2-metadata-token-ttl-seconds: 60"); \
  curl -s -H "X-aws-ec2-metadata-token: $TOKEN" \
  http://169.254.169.254/latest/meta-data/iam/security-credentials/'
```

Expect `opencircuit-instance`. Before the role is attached this returns a 404
HTML body, which is the current state and is exactly how the gap was found.

---

## Why not static access keys

It would be quicker to mint an IAM user, put its keys in
`/etc/opencircuit/config.env`, and skip all of the above. Three reasons not to:

1. **PRD §10.5 and §9 both specify the instance role by name**, and
   `docs/configuration.md`'s `AWS_REGION` row says outright: "No static
   credentials anywhere in configuration — the EC2 instance role supplies them
   via the AWS SDK's default credential chain." Reversing that quietly would
   leave the docs describing a system that no longer exists.
2. **Keys in a config file do not rotate and do not expire.** Instance-role
   credentials are short-lived and refreshed automatically by the metadata
   service.
3. **The blast radius is different.** A leaked config file with static keys is
   usable from anywhere; instance-role credentials are only obtainable from
   the instance's own metadata endpoint.

Note that the box *does* already hold static keys at `/root/.aws/credentials`
— that is the `certbot-dns-updater` user, which has Route 53 write access so
the wildcard certificate can renew via DNS-01. It predates this project, is
readable only by root, and is unreachable from `opencircuit.service` anyway
(`ProtectHome=true` in the unit blocks `/root`). Leaving it alone is correct;
it is not a precedent for adding more.

---

## After the role is attached

The remaining SES work, in order — see `docs/email-setup.md` and `CLAUDE.md`
§10 item 2 for the full picture:

1. ~~DNS records~~ — **done.** Three DKIM CNAMEs, the MAIL FROM MX and SPF
   TXT, an SPF TXT on `mailing.`, and `_dmarc.mailing.` at `p=none`. All
   resolve; SES reports the identity verified with DKIM and MAIL FROM both
   `SUCCESS`. The apex MX and TXT were never in the change batch.
2. ~~Wait for verification~~ — **done**, it completed within minutes.
3. ~~Wire the event destination~~ — **done.** Configuration set
   `opencircuit-transactional` publishes SEND/DELIVERY/BOUNCE/COMPLAINT/REJECT/
   RENDERING_FAILURE/DELIVERY_DELAY to `opencircuit-ses-events`; the SNS HTTPS
   subscription to `/api/ses/notifications` **auto-confirmed**, which proves
   `internal/sesnotify`'s signature verification works end to end against real
   SNS. The topic was also moved to `SignatureVersion 2` (SHA-256) — the
   handler logs a warning on SHA-1, which is how the default was noticed.
4. **Request SES production access** — still to do. The account is in the
   sandbox (`ProductionAccessEnabled: false`): 200 messages/day, verified
   recipients only. `cli-admin`'s existing inline policy already grants
   `support:CreateCase`, so this can be filed from the CLI.
5. **Attach the IAM role** — this document's Part 1/Part 3. Still blocked.
6. Flip `SES_SANDBOX=false` and `SEND_WORKER_ENABLED=true`, restart.

Steps 4 and 5 are independent of each other; 6 waits on both.

The account-level suppression list (PRD §6.7, `#0038` criterion 8) is **already
enabled** — `SuppressedReasons` is already `["BOUNCE", "COMPLAINT"]`. No action
needed there.

One thing enabling SES will *not* unblock: campaigns still refuse to start
without a `physical_address` setting (`#0045`, CAN-SPAM §7704, `CLAUDE.md` §10
item 3). That needs a PO box, not an IAM change.
