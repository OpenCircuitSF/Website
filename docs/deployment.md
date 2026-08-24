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

## Backups (`PRD.md` §10.6, `#0062`)

The subscriber list is the single most valuable and least reconstructible
asset in the system. `#0062` performed and verified a full restore drill
against a local PostgreSQL 16 cluster (see **Restore drill** below) — this
is a demonstrated round trip, not a belief. **Everything below the drill is
either already implemented, or explicitly marked as not yet built /
not re-verified on the server.**

### What actually exists today — and a gap from `PRD.md` §10.6

`PRD.md` §10.6 and this issue's original acceptance criteria describe
**S3** as the backup target (30-day S3 lifecycle, encryption at rest, a
bucket that blocks public access, IAM scoped to a bucket prefix). **That is
not what is implemented.** The scripts actually in this repo
(`scripts/db/backup.sh`, `pull-backups.sh`, `restore.sh` — copied from
ShortLinks in `#0001`, generic enough to work unmodified for the
`opencircuit` database) implement a **different, already-working design**:

1. `backup.sh` runs `pg_dump -Fc` (or `--format=plain | gzip`) into a local
   directory tree, one subfolder per database, on the database host itself
   (`/var/backups/postgres/<db>/` by default). It prunes dumps older than
   `BACKUP_RETENTION_DAYS` (default 14) after each successful run.
2. `pull-backups.sh` runs on a **separate** machine (a Mac mini, per its
   header comment) and `rsync`s the whole backup tree over SSH — a pull,
   not a push, so a compromised server never holds credentials for the
   offsite copy.

This gets nightly dumps and an offsite copy without AWS, but it is **not**
S3, has **no bucket-level encryption/public-access-block/IAM scoping** (file
permissions on the server and the Mac mini are what protect it — `backup.sh`
already `chmod 0700`s the backup root and `chmod 0600`s each dump), and its
retention is a local `find -mtime` prune, not an S3 lifecycle rule. Treat
`PRD.md` §10.6's S3 language as aspirational until someone either builds S3
upload on top of these scripts or the PRD is corrected to match what
shipped — this drill did not decide which, and neither script talks to AWS
today. Worth a follow-up issue; not filed here per `CLAUDE.md` §9 (subagents
report, they don't file).

### Restore drill — exact commands run, in order

Run locally against a disposable PostgreSQL 16 cluster (Homebrew, macOS),
never against a database anyone depends on. Every database name below is
throwaway and none collides with `scripts/testdb.sh`'s per-agent pool.

```bash
# 1. Populate a scratch source database with representative data spanning
#    every table that matters: users, interests, subscribers +
#    subscriber_interests, suppressions, audit_log, workshops +
#    workshop_interests, email_campaigns + campaign_interests + email_sends,
#    email_events. Seed throwaway rows — never target a literal or seeded id
#    (CLAUDE.md §8b).
scripts/testdb.sh create 0062src
psql "$(scripts/testdb.sh create 0062src)" -f seed.sql   # INSERT ... RETURNING id, no hardcoded ids

# 2. Snapshot what "correct" looks like BEFORE backing up: row counts per
#    table, schema_migrations (version + dirty), every sequence's last_value
#    (NOT row count — see "Sequences" below), constraint count per table,
#    index count per table.

# 3. Back up with the real script, current-user auth for local dev
#    (BACKUP_RUN_AS="" — see the bug note below; the server run uses the
#    default BACKUP_RUN_AS=postgres via sudo peer auth instead):
BACKUP_ROOT=/tmp/backup-drill BACKUP_RUN_AS="" \
  bash scripts/db/backup.sh opencircuit_test_0062src
#    Success looks like: "ok — <size>" per database and a final
#    "Done — all databases backed up." with exit 0. Any pg_dump failure
#    leaves prior dumps untouched and the script exits non-zero.

# 4. Restore into a FRESH, differently-named database — never restore over
#    the source, so the drill can compare instead of destroying:
BACKUP_RUN_AS="" RESTORE_CREATE=1 \
  bash scripts/db/restore.sh /tmp/backup-drill/opencircuit_test_0062src/opencircuit_test_0062src-latest.dump \
    opencircuit_test_0062dst
#    Success looks like: "Done. Verify with: psql -d <target> -c '\dt'" with
#    no pg_restore errors printed above it. pg_restore's --clean --if-exists
#    makes a re-restore idempotent, but RESTORE_CREATE=1 here creates a
#    database that must not already exist — it errors loudly if it does.

# 5. Compare restored against source: table-by-table row counts, sequence
#    last_values, schema_migrations, constraint counts, index counts — all
#    must match exactly (they did, in full, in this drill).

# 6. Prove the restore is actually usable, not just structurally present:
#    insert a new row and confirm it does not collide with restored ids,
#    and confirm FK / CHECK / UNIQUE constraints still reject bad data.

# 7. Clean up everything created:
scripts/testdb.sh drop 0062src
scripts/testdb.sh drop 0062dst
```

The custom format (`pg_restore`, default) and the plain format
(`BACKUP_FORMAT=plain`, `gunzip | psql`) were **both** run through this full
drill — identical results on every check.

### What the drill found

- **Row counts** — every table (`users`, `interests`, `subscribers`,
  `subscriber_interests`, `suppressions`, `audit_log`, `workshops`,
  `workshop_interests`, `email_campaigns`, `campaign_interests`,
  `email_sends`, `email_events`, `settings`) matched source-to-restored
  exactly, in both formats.
- **Sequences** — this is the one that actually bites people. In this
  drill, `subscribers_id_seq` had advanced to `10` while only `5` rows
  existed (an earlier, unrelated seed attempt failed mid-transaction and
  rolled back — but the `nextval()` calls it made were **not** rolled back,
  because sequence advancement in PostgreSQL is never transactional). A
  naive restore approach that reset sequences from `max(id)` would have
  restored the sequence back down to `5` and the very next insert would
  have collided with a row that used to exist. `pg_dump`/`pg_restore`
  handle this correctly on their own — every sequence's `last_value`
  matched exactly after restore, in both formats — and inserting a new row
  post-restore got id `11` with zero collision, confirmed directly. Verify
  this yourself with `select sequencename, last_value from pg_sequences
  where schemaname='public'` before and after, never with `max(id)`.
- **`schema_migrations`** — `version=20, dirty=false` before and after, in
  both formats. `migrate` will refuse to run against a `dirty=true`
  database, so this check is not optional.
- **Extensions** — none. `grep -rn "CREATE EXTENSION" migrations/` returns
  nothing, so there is no extension dependency to worry about on restore.
- **Roles** — `backup.sh` dumps with `pg_dump --no-owner --no-privileges`,
  so the dump carries no role names at all; ownership on restore is
  whichever role runs `pg_restore`/`psql` (here, `opencircuit`, matching
  `scripts/db/create.sql`'s bootstrap role — confirmed identical
  `tableowner` before and after). The restore therefore does **not**
  require the dump-time role to exist on the target, only that the
  **restoring** role (`opencircuit`) already does.
- **Order and dependencies** — not a manual concern here: `pg_dump`/
  `pg_restore` topologically order the dump themselves (tables, then data
  via `COPY`, then constraints/indexes/sequences afterward), so FK order
  across `interests` → `subscribers` → `subscriber_interests` →
  `email_campaigns` → `email_sends`, etc. was handled correctly with no
  manual intervention in either format.
- **Constraint and index presence** — per-table counts
  (`information_schema.table_constraints`, `pg_indexes`) matched exactly,
  and live enforcement was confirmed by attempting (and getting rejected)
  a bad FK, a bad CHECK value, and a duplicate UNIQUE email against the
  restored database.
- **A real script bug, found and fixed by this drill**: `backup.sh` and
  `restore.sh` both defaulted `BACKUP_RUN_AS` with `${BACKUP_RUN_AS:-postgres}`,
  which in bash treats an *explicitly empty* value the same as *unset* — so
  the header comment's own documented local-dev path
  (`BACKUP_RUN_AS=""` to skip `sudo -u postgres`) silently fell back to
  `sudo -u postgres` anyway and failed with `sudo: unknown user postgres`
  on a machine with no `postgres` OS user (any Homebrew Postgres install).
  Fixed in both scripts to `${BACKUP_RUN_AS-postgres}` (unset-only), and
  re-verified: `BACKUP_RUN_AS=""` now actually skips `sudo` end to end,
  confirmed by a full backup → restore → row-check round trip after the
  fix. `shellcheck` and `bash -n` pass on both scripts.

### Before restoring over anything live

`restore.sh` **never drops a database itself** — that is deliberate (see its
own header). Restoring into an existing target restores object-by-object
with `pg_restore --clean --if-exists` (or a plain `psql` replay), which is
destructive at the object level the moment it starts. Before pointing it at
anything that is not a fresh scratch database:

1. Confirm the target database name out loud, from the actual command you
   are about to run — not from memory. A typo here overwrites the wrong
   database.
2. Prefer `RESTORE_CREATE=1` into a **new**, never-before-used name so a
   mistake is a stray database, not a destroyed one.
3. If you must restore over a live database, take a fresh `backup.sh` dump
   of *that* database's current state first, so the "before" is itself
   recoverable.
4. `psql -d <target> -c '\dt'` and a row-count spot check are the minimum
   post-restore sanity check — don't declare success on `restore.sh`'s exit
   code alone.

### What this drill does not cover — stated plainly, not left implied

- **No S3.** See the gap noted above — nothing here talks to AWS. There is
  no bucket, no lifecycle policy, no bucket-level encryption or
  public-access block, and no IAM policy scoped to a bucket prefix, because
  none of those AWS resources exist yet (`CLAUDE.md` §10 item 2 — the SES
  account, and by extension any S3-adjacent AWS footprint, is "not
  started").
- **No point-in-time recovery.** This is logical (`pg_dump`) backup only —
  a restore recovers to the moment of the last completed dump, not to any
  point in between. No WAL archiving, no continuous archiving, no PITR tool
  (e.g. `pgBackRest`, `wal-g`) is configured or evaluated here.
- **No automated alerting is wired up yet.** `backup.sh` already does its
  part — it exits non-zero if *any* database fails to dump, which is what a
  cron `MAILTO=` or a systemd timer's `OnFailure=` unit needs to fire — but
  no crontab entry, systemd timer/service unit, or `OnFailure=` target
  exists in this repo yet (`deploy/` has no backup timer file). That is
  server-provisioning work with no server to provision against yet; wiring
  it up is `#0064`'s job (or a dedicated issue), not verifiable locally.
- **The offsite pull (`pull-backups.sh`) was not run in this drill.** It
  needs a real SSH target (a Mac mini, per its header), which does not
  exist in this environment. Its logic (`rsync -avz --delete-after`,
  pull rather than push) was read and is straightforward, but "read and
  looks right" is exactly the standard this issue exists to reject for the
  dump/restore path — so treat the offsite leg as **unverified**, not
  merely undocumented, until someone runs it against a real remote host.
- **Volume and timing at production scale are unmeasured.** This drill's
  database has a handful of rows per table. `CLAUDE.md` §5 is explicit that
  there is no performance requirement on this project, so this is not a
  gap that needs closing — just don't assume the drill's sub-second timings
  say anything about a production-sized dump/restore.

### Must be re-verified on the server, not assumed from this drill

- **`BACKUP_RUN_AS=postgres` (the default, real `sudo -u postgres` path).**
  This drill ran with `BACKUP_RUN_AS=""` because the local Homebrew cluster
  has no `postgres` OS user and this workstation user already owns the
  cluster. The production path — `sudo -u postgres pg_dump`/`pg_restore` via
  peer authentication — was read, not executed, and needs its own drill on
  the actual EC2 instance once it exists.
- **Actual disk paths and permissions on the box** — `BACKUP_ROOT` defaults
  to `/var/backups/postgres`; confirm it exists, is owned/writable as
  `backup.sh` expects, and has room for `BACKUP_RETENTION_DAYS` of dumps at
  real data volume.
- **The cron/systemd timer itself**, once written — confirm it actually
  fires nightly, that `BACKUP_DATABASES` (or the `.env` `DATABASE_URL`
  fallback) resolves to `opencircuit` on that box, and that a deliberately
  broken run (e.g. a bad `BACKUP_ROOT`) produces a real alert someone sees,
  not just a non-zero exit code nobody is watching.
- **`pull-backups.sh` end to end**, against the real Mac mini and a real SSH
  key, per the point above.
- **Whether the S3-vs-local-Mac-mini gap gets closed or the PRD gets
  corrected** — a decision for a human/orchestrator, not this drill.

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
