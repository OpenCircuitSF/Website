# Deployment & Operations

Production is not yet live for this codebase — the domain currently serves
a static placeholder (`CLAUDE.md` §7). This is a stub with real headings;
`PRD.md` §10 (89 lines) and `CLAUDE.md` §7/§10 are authoritative in the
meantime.

## Current production facts (`CLAUDE.md` §7)

| | |
|---|---|
| Canonical host | `https://www.opencircuitsf.com` — apex and plain HTTP both 301 to it |
| Server | Apache 2.4.68, Amazon Linux, OpenSSL 3.5.7 |
| TLS | Let's Encrypt |
| Already on the box | PostgreSQL and Apache |
| Currently served | the static placeholder (`placeholder/`) |

## Planned topology (`PRD.md` §10.1)

```
                       Route 53  (opencircuitsf.com)
                            │
                  ┌─────────┴──────────┐
                  │                    │
              A: apex/www         A: go.
                  │                    │
                  ▼                    ▼
        ┌──────────────────────────────────────┐
        │  EC2 (t4g.small, Amazon Linux 2023)  │
        │  ┌────────────────────────────────┐  │
        │  │ Apache 2 — TLS (Let's Encrypt) │  │
        │  │  vhost opencircuitsf.com →8080 │  │
        │  │  vhost go.opencircuitsf.com    │  │
        │  │                          →8081 │  │
        │  └───────┬──────────────┬─────────┘  │
        │          ▼              ▼            │
        │   opencircuit     shortlinks         │
        │   (systemd)       (systemd)          │
        │          │              │            │
        │          ▼              ▼            │
        │   PostgreSQL: opencircuit, shortlinks│
        └──────────────────────────────────────┘
                  │                    ▲
                  ▼ SES v2 API         │ SNS HTTPS
             AWS SES ─────── events ───┘
                  │
                  ▼ inbound (lists.opencircuitsf.com MX)
```

One EC2 instance hosts **both** this service and the separate ShortLinks
deploy, behind one Apache instance with two vhosts. `opencircuit` and
`shortlinks` are independent systemd units and independent PostgreSQL
databases — a redeploy of one must never touch the other.

## `deploy/`

`deploy/systemd/opencircuit.service` and `deploy/apache/opencircuitsf.com.conf`
are this project's own deploy config (`#0066` renamed and retargeted the
ShortLinks assets `#0001` copied wholesale). The Apache vhost redirects the
apex to the canonical `www` host per the DNS table below.

## DNS (Route 53) — see [`email-setup.md`](email-setup.md) for the mail-specific records

| Name | Type | Value | Purpose |
|---|---|---|---|
| `www.opencircuitsf.com` | A | EC2 Elastic IP | **Canonical host** |
| `opencircuitsf.com` | A | EC2 Elastic IP | 301 → `www` |
| `go.opencircuitsf.com` | A | EC2 Elastic IP | ShortLinks |

> **Known defect in the issue tracker:** `#0064`'s acceptance criteria say
> the vhost should redirect `www` → apex. Production does the opposite,
> and `WEBAUTHN_RP_ORIGIN` depends on `www` staying canonical — correct
> that criterion before implementing `#0064` (`CLAUDE.md` §7).

## IAM (`PRD.md` §10.5)

The EC2 instance role is scoped tightly: `ses:SendEmail`/`ses:SendRawEmail`
restricted to the verified identity ARN and configuration set;
`s3:GetObject`/`s3:DeleteObject` on the inbound bucket only. No `ses:*`, no
wildcard resources.

## Backups (`PRD.md` §10.6)

Nightly `pg_dump` to S3 with a 30-day lifecycle, following the same script
pattern as ShortLinks' `scripts/db/backup.sh`/`pull-backups.sh`/
`restore.sh` (already present in this repo, copied in `#0001`, not yet
retargeted at the `opencircuit` database). The subscriber list is the
single most valuable and least reconstructible asset in the system —
verify a restore before launch, not after the first incident.

## Bootstrap admin (`opencircuit seed`)

After the first `migrate up` on a fresh database, run:

```bash
opencircuit seed
```

This idempotently ensures the `ADMIN_EMAIL` user exists with `is_admin = true`
and `active = true` — safe to re-run; a second run is a no-op that exits `0`
and does not create a duplicate row. Unlike ShortLinks' version, it does not
seed a test link — this project has no `links` table (`PRD.md` §3.2). The
interest taxonomy is seeded by the migration (`#0023`), not by this command.

**The seeded admin has no passkey.** `seed` only creates the user row; it
cannot enroll a credential. First sign-in must use **"Recover account"** on
the login page, not "Register" — Register rejects an email that already has a
user row, so trying to register `ADMIN_EMAIL` after seeding silently does
nothing. Recovery adds a passkey to an existing account without creating a
new user and does not check the `registrations_enabled` gate, which is why it
is the correct path for the seeded admin (mirrors ShortLinks
`DEPLOYMENT.md` step 6). This is non-obvious and blocks first login if
forgotten:

1. Open the site and click **"Recover account / lost passkey"** on the login
   page.
2. Enter the `ADMIN_EMAIL` address and submit.
3. Follow the magic link from the recovery email to complete the passkey
   ceremony (`navigator.credentials.create()`).
4. You are redirected in as the admin user; `is_admin` is preserved
   throughout recovery.

## Open items blocking a real deploy (`CLAUDE.md` §10)

Tracked so a phase doesn't stall silently on one of these — none are code:

1. Rename the GitHub repo `Website` → `OpenCircuitSF`.
2. SES: verify domain in `us-west-2`, Easy DKIM, custom MAIL FROM, DMARC at
   `p=none`, request production access — blocks real sends from Phase 3.
3. Physical mailing address (PO box) — `#0045` refuses to start a campaign
   without it.
4. Sending identity (`hello@` vs. `workshops@`) and who reads the reply-to
   inbox — undecided, `PRD.md` §14 Q2 defaults to `hello@`.
5. Whether the domain needs human mailboxes — determines the apex MX,
   undecided, `PRD.md` §14 Q3.
6. Server-side details: instance ID/size/region, SSH access,
   `DocumentRoot`, vhost file, certbot renewal schedule, whether the
   existing Postgres is the target — undocumented, capture as encountered.

## Where to look

| Concern | File |
|---|---|
| systemd unit | `deploy/systemd/opencircuit.service` |
| Apache vhost | `deploy/apache/opencircuitsf.com.conf` |
| DB backup/restore scripts | `scripts/db/{backup,restore,pull-backups}.sh` |
| DB create/drop | `scripts/db/{create,drop}.sql` |
