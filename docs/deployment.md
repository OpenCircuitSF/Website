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

`deploy/systemd/opencircuit-backup.timer`, `opencircuit-backup.service`, and
`opencircuit-backup-alert.service` (`#0229`) schedule the nightly backup and
alert on failure — see `deploy/systemd/README.md`'s "Backup timer and failure
alert" section for install steps, and the Backups section below for what has
and has not been verified.

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

### What actually exists today — `PRD.md` §10.6 corrected to match (`#0229`)

`PRD.md` §10.6 used to describe **S3** as the backup target (30-day S3
lifecycle, encryption at rest, a bucket that blocks public access, IAM scoped
to a bucket prefix). That was never what shipped, and `#0229` corrected the
PRD rather than leave it describing infrastructure nobody was building — see
its `## Decision`. The scripts actually in this repo (`scripts/db/backup.sh`,
`pull-backups.sh`, `restore.sh` — copied from ShortLinks in `#0001`, generic
enough to work unmodified for the `opencircuit` database) implement a
**different, already-working design**, now also §10.6's design of record:

1. `backup.sh` runs `pg_dump -Fc` (or `--format=plain | gzip`) into a local
   directory tree, one subfolder per database, on the database host itself
   (`/var/backups/postgres/<db>/` by default). It prunes dumps older than
   `BACKUP_RETENTION_DAYS` (default 14) after each successful run.
2. `pull-backups.sh` runs on a **separate** machine (a Mac mini, per its
   header comment) and `rsync`s the whole backup tree over SSH — a pull,
   not a push, so a compromised server never holds credentials for the
   offsite copy.
3. `deploy/systemd/opencircuit-backup.timer` fires `backup.sh` nightly, and
   its `OnFailure=` chains to `opencircuit-backup-alert.service`
   (`scripts/db/backup-alert.sh`) so a failed run reaches a human rather than
   just leaving a non-zero exit code nobody reads (`#0229`; see "Schedule and
   failure alert" below).

This gets nightly dumps, an offsite copy, and a wired-up failure alert
without AWS, but it is **not** S3, has **no bucket-level
encryption/public-access-block/IAM scoping** (file permissions on the server
and the Mac mini are what protect it — `backup.sh` already `chmod 0700`s the
backup root and `chmod 0600`s each dump), and its retention is a local
`find -mtime` prune, not an S3 lifecycle rule.

**S3 upload is a deferred option, not abandoned.** `#0229`'s `## Decision`:
building it needs an AWS account, which does not exist yet (`CLAUDE.md` §10
item 2), and the user deferred all AWS work to deployment, same as SES.
`PRD.md` §10.6 now records S3 as an addable upload leg on top of `backup.sh`
once that account exists, rather than a redesign — nothing about the
local-dump/offsite-pull shape needs to change to add it later.

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
DSN="$(scripts/testdb.sh create 0062src)"   # captured ONCE — `create` drops-then-recreates,
psql "$DSN" -f seed.sql                     # so calling it a second time destroys what step 1 just made
#   seed.sql is not a repo file — it's a throwaway script you write for the
#   drill, containing INSERT ... RETURNING id statements, no hardcoded ids.

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
#    restore.sh (#0228) also reassigns ownership of every restored table,
#    sequence, and view to RESTORE_OWNER (default: opencircuit) after the
#    restore completes — see "Ownership" below for why this step exists and
#    how to prove it, not just trust it.

# 5. Compare restored against source: table-by-table row counts, sequence
#    last_values, schema_migrations, constraint counts, index counts — all
#    must match exactly (they did, in full, in this drill).

# 6. Prove the restore is actually usable, not just structurally present:
#    insert a new row and confirm it does not collide with restored ids,
#    and confirm FK / CHECK / UNIQUE constraints still reject bad data.

# 6b. Prove ownership the way that actually catches the defect (#0228): do
#    NOT just inspect pg_tables.tableowner as the role that ran the restore —
#    that check passes even when broken, because the restoring role can
#    always read what it just restored. Connect AS THE APPLICATION ROLE and
#    issue a real query:
psql "postgres://opencircuit:<password>@localhost:5432/opencircuit_test_0062dst?sslmode=disable" \
  -c "select count(*) from subscribers;"
#    permission denied here means the ownership step did not run or did not
#    reach this table — a passing catalog-only check would not have caught
#    that.

# 7. Clean up everything created:
scripts/testdb.sh drop 0062src
scripts/testdb.sh drop 0062dst
#    `testdb.sh drop` (#0228) now reports a real failure and exits non-zero
#    instead of silently claiming "does not exist" — see "A real script bug"
#    below. A database restore.sh created with RESTORE_CREATE=1 is still
#    owned, at the DATABASE level, by whoever ran createdb (RESTORE_OWNER
#    only reassigns the TABLES/SEQUENCES/VIEWS inside it, not the database
#    object itself) — if that is not `opencircuit`, `testdb.sh drop` will now
#    fail loudly with `ERROR: must be owner of database`, and you drop it by
#    hand as its actual owner instead of it silently leaking.
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
- **Roles and ownership (corrected — `#0228`)** — `backup.sh` dumps with
  `pg_dump --no-owner --no-privileges`, so the dump carries no role names or
  grants at all; without further action, ownership on restore is whichever
  role ran `pg_restore`/`psql`. **This section previously claimed that
  matched `opencircuit` before and after — that was true only because the
  local drill happened to run as the `opencircuit` role, and it is false in
  general.** `#0062`'s own phase-3 review reproduced the general case: running
  the restore as a *different* role (as production's documented
  `sudo -u postgres pg_restore` path does) left every table owned by that
  role instead, and `opencircuit` got `permission denied` on its first query
  — the restore reported success while leaving the app unable to read its own
  data. `restore.sh` now closes this itself (`#0228`): after the restore
  completes, it reassigns ownership of every table, sequence, and view to
  `RESTORE_OWNER` (default `opencircuit`, matching `scripts/db/create.sql`'s
  bootstrap role) — and because ownership implies full privileges on an
  object, this also replaces what `--no-privileges` stripped, without a
  separate `GRANT` step. Proven with a real dump/restore round trip on a
  local scratch database: restoring as a role other than `opencircuit`
  (simulating the `postgres` production path) left tables owned by that role
  and the app role locked out (`permission denied for table widgets`);
  restoring with the fix in place left every table and sequence owned by
  `opencircuit`, and connecting **as `opencircuit`** — not as the restoring
  role — both `SELECT`ed and `INSERT`ed successfully. Set `RESTORE_OWNER=""`
  to skip this step and inspect raw restore ownership instead.
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
- **A second real script bug, found by `#0062`'s review and fixed by
  `#0228`**: `scripts/testdb.sh drop` chained `db_exists && DROP DATABASE &&
  echo dropped || echo "does not exist"`, so a `DROP DATABASE` that failed
  for *any* reason (not just non-existence — e.g. `ERROR: must be owner of
  database`, exactly what happens when dropping a database `restore.sh`
  created as a different role) fell into the same `||` branch as "never
  existed," printed the misleading message, and **exited 0**, leaving the
  stray database behind. Reproduced against the pre-fix script and confirmed
  fixed: existence and drop-success are now checked separately, and a real
  drop failure prints the psql error, an explanatory message, and exits 1.
- **Two behaviours worth knowing before 3 a.m., not obvious from either
  script's happy path:**
  - A **custom-format** (`.dump`) re-restore over an already-populated target
    is genuinely idempotent: `pg_restore --clean --if-exists` drops and
    recreates each object, so running `restore.sh` twice in a row against the
    same target converges to the dump's contents rather than erroring or
    duplicating rows. Confirmed directly: a row inserted after the first
    restore was gone after the second, and the row count returned to the
    dump's own count.
  - A **plain-format** (`.sql.gz`) restore into a **non-empty** target is
    *not* idempotent — it aborts at the first `CREATE TABLE` under
    `ON_ERROR_STOP=1` (`relation "…" already exists`) and exits `3` via
    `pipefail`, leaving the target's existing contents untouched. It fails
    safe rather than partially applying, but the two formats are not
    interchangeable here — always restore into a fresh target
    (`RESTORE_CREATE=1`), which both the script header and this runbook's
    step 4 already say to do.

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

- **No S3.** `#0229` recorded this as a deliberate deferral, not an
  oversight — nothing here talks to AWS. There is no bucket, no lifecycle
  policy, no bucket-level encryption or public-access block, and no IAM
  policy scoped to a bucket prefix, because none of those AWS resources exist
  yet (`CLAUDE.md` §10 item 2). `PRD.md` §10.6 records it as an addable
  upload leg once the AWS account exists, not as work that was skipped by
  mistake.
- **No point-in-time recovery.** This is logical (`pg_dump`) backup only —
  a restore recovers to the moment of the last completed dump, not to any
  point in between. No WAL archiving, no continuous archiving, no PITR tool
  (e.g. `pgBackRest`, `wal-g`) is configured or evaluated here.
- **The offsite pull (`pull-backups.sh`) has never been run against a real
  target, in this drill or since.** There is no second machine anywhere in
  this environment to run it against — no Mac mini, no SSH host reachable
  from here. Its logic (`rsync -avz --delete-after --partial`, pull rather
  than push) was read and is straightforward, but "read and looks right" is
  exactly the standard this issue exists to reject for the dump/restore path,
  so the offsite leg is **explicitly unverified**, not merely undocumented.
  **What would verify it:** run `scripts/db/pull-backups.sh` on a real second
  machine (the Mac mini named in its own header comment) against a real
  `backup.sh`-populated `BACKUP_ROOT` reachable over SSH, confirm the local
  mirror matches the source tree byte-for-byte (`rsync -avzn --delete-after`
  a second time should report zero changes), and confirm a file deleted
  server-side (past retention) is pruned locally too on the next pull.
- **Volume and timing at production scale are unmeasured.** This drill's
  database has a handful of rows per table. `CLAUDE.md` §5 is explicit that
  there is no performance requirement on this project, so this is not a
  gap that needs closing — just don't assume the drill's sub-second timings
  say anything about a production-sized dump/restore.

### Schedule and failure alert (`#0229`) — built, not yet exercised on a real box

`backup.sh` exiting non-zero on failure was verified working by `#0062`, but
until `#0229` nothing consumed that exit code. Two systemd units now do:
`deploy/systemd/opencircuit-backup.timer` fires `opencircuit-backup.service`
(`backup.sh`, run as `postgres`) nightly at 07:30 UTC, and that service's
`OnFailure=opencircuit-backup-alert.service` runs
`scripts/db/backup-alert.sh` on any failure — which always logs a
high-priority journal entry (`journalctl -p err`) and optionally POSTs a
webhook if `BACKUP_ALERT_WEBHOOK_URL` is configured. See
`deploy/systemd/README.md`'s "Backup timer and failure alert" section for
install and test commands.

**What this is, and is not, verified against:** the three unit files pass a
structural well-formedness check (matching `[Section]` headers, every
non-comment line is `key=value`) and `scripts/db/backup-alert.sh` passes
`shellcheck`/`bash -n` and was run directly — both with and without
`BACKUP_ALERT_WEBHOOK_URL` set, confirming it always logs via `logger`, exits
`0` even when a webhook POST fails, and never masks the underlying failure.
**None of that is the same as systemd actually running these units.** There
is no systemd on this development machine (macOS) and no `systemd-analyze
verify` was available to run against the unit files themselves. Nothing here
proves the timer actually fires on schedule, that `OnFailure=` actually
triggers the alert unit the way the unit graph implies, or that `journalctl
-p err` or a real webhook endpoint is where anyone is actually looking.

### Must be re-verified on the server, not assumed from this drill

- **`BACKUP_RUN_AS=postgres` (the default, real `sudo -u postgres` path).**
  This drill ran with `BACKUP_RUN_AS=""` because the local Homebrew cluster
  has no `postgres` OS user and this workstation user already owns the
  cluster. The production path — `sudo -u postgres pg_dump`/`pg_restore` via
  peer authentication — was read, not executed, and needs its own drill on
  the actual EC2 instance once it exists. The same is true of `RESTORE_OWNER`
  reassignment (`#0228`): it was proven locally against a non-superuser role
  standing in for `postgres`, not against a real `sudo -u postgres` restore.
- **Actual disk paths and permissions on the box** — `BACKUP_ROOT` defaults
  to `/var/backups/postgres`; confirm it exists, is owned/writable as
  `backup.sh` expects, and has room for `BACKUP_RETENTION_DAYS` of dumps at
  real data volume. `deploy/systemd/opencircuit-backup.service`'s
  `WorkingDirectory=`/`ExecStart=` assume the repo is checked out at
  `/opt/opencircuit` — a placeholder (`CLAUDE.md` §10 item 6, still
  undocumented) that must be corrected to the real path before installing.
- **The timer and the alert unit, installed on a real systemd host** —
  confirm `opencircuit-backup.timer` actually fires nightly, that
  `BACKUP_DATABASES` (or the `.env` `DATABASE_URL` fallback) resolves to
  `opencircuit` on that box, and that a deliberately broken run (e.g. a bad
  `BACKUP_ROOT`) produces a real, seen alert — not just a journal line and a
  non-zero exit code nobody is watching. If `BACKUP_ALERT_WEBHOOK_URL` gets
  configured, confirm the webhook actually delivers to wherever a human
  looks.
- **`pull-backups.sh` end to end**, against the real Mac mini and a real SSH
  key — see "What this drill does not cover" above for exactly what that
  verification looks like.

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
| Backup timer + failure alert (`#0229`) | `deploy/systemd/opencircuit-backup.{service,timer}`, `opencircuit-backup-alert.service`, `scripts/db/backup-alert.sh` |
| Apache vhost | `deploy/apache/opencircuitsf.com.conf` |
| DB backup/restore scripts | `scripts/db/{backup,restore,pull-backups}.sh` |
| DB create/drop | `scripts/db/{create,drop}.sql` |
