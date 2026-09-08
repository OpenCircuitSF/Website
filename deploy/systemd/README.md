# systemd units for Open Circuit SF

This directory contains the systemd units for the EC2 host:

| Unit | Purpose |
|---|---|
| `opencircuit.service` | Runs `/usr/local/bin/opencircuit serve` as a dedicated non-root user, listens on `127.0.0.1:8080` behind the Apache reverse proxy, restarted automatically on failure |
| `opencircuit-backup.timer` | Fires `opencircuit-backup.service` nightly (`#0229`) |
| `opencircuit-backup.service` | Runs `scripts/db/backup.sh` then `scripts/db/backup-media.sh` (`#0434`), both as `postgres`; `OnFailure=` chains to the alert unit below if either fails |
| `opencircuit-backup-alert.service` | Logs a high-priority journal entry and (if configured) POSTs a webhook when a backup run fails — see `scripts/db/backup-alert.sh` |

## Create the system user

The main service runs as an unprivileged `opencircuit` system user (and
group). Create it once before installing the service:

```bash
sudo useradd --system --no-create-home opencircuit
```

## Create the system user

The unit runs as an unprivileged `opencircuit` system user (and group). Create
it once before installing the service:

```bash
sudo useradd --system --no-create-home opencircuit
```

## Install

Install the unit, reload systemd, then enable and start the service so it runs
on boot:

```bash
sudo cp opencircuit.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable opencircuit && sudo systemctl start opencircuit
```

This assumes the binary is already installed at `/usr/local/bin/opencircuit`
and that `/etc/opencircuit/config.env` exists (see `DEPLOYMENT.md` steps 3 and
4).

## View logs

```bash
sudo journalctl -u opencircuit -f
```

## Notes

- `ExecStart` runs the `serve` subcommand, which is the verb the binary's
  `cmd/opencircuit/main.go` dispatches to start the HTTP server.
- `EnvironmentFile=/etc/opencircuit/config.env` supplies every variable from
  `.env.example`. Edit that file and `sudo systemctl restart opencircuit` to
  apply config-only changes — no rebuild needed.
- If the unit file itself changes, re-copy it and run
  `sudo systemctl daemon-reload` before restarting.

## Backup timer and failure alert (`#0229`)

`opencircuit-backup.service` assumes the repo is checked out at
`/opt/opencircuit` (`WorkingDirectory=` and `ExecStart=` both reference it) —
a placeholder matching ShortLinks' own `/opt/shortlinks` convention until the
real server layout is captured (`CLAUDE.md` §10 item 6, still undocumented).
**Edit the unit file to match the real path before installing it.**

**Also confirm `Environment=BACKUP_DATABASES=opencircuit` is present and
uncommented (`#0236`) before enabling the timer.** `scripts/db/backup.sh`
has no default database name to fall back on — with this unset it now exits 2
naming the missing configuration, rather than the pre-`#0236` behavior of
silently defaulting to `shortlinks` (a different project's database,
inherited from the ShortLinks port). A loud failure here is much cheaper than
discovering, some night later, that backups were never running.

**The offsite pull is a separate required install step, not covered by these
units.** `scripts/db/pull-backups.sh` runs on a *different* machine (a Mac
mini, per its header comment) — it is not wired into systemd at all, so it
needs its own reminder here. **Set `BACKUP_SSH_HOST` before running it
(`#0245`)** — the script no longer defaults to a host to pull backups from.
It used to default to `ec2-user@go.sstools.co`, a real ShortLinks production
hostname inherited from the same port; run unmodified, that would have opened
an SSH connection to another project's server instead of this one's. **Corrected
(`#0435`, 2026-09-08): this project's own EC2 host *is* now provisioned** — it
has been serving production since 2026-08-25 (`CLAUDE.md` §7 has the real
hostname and IP) — so the missing piece is no longer the host, it is which
*second* machine pulls from it. The user has mentioned a machine named "joe"
with large external storage as the eventual puller, but its hostname and
whether it is reachable for an unattended pull (a static address? behind a
VPN? always powered on?) are not established anywhere in this repo — that is
a question for the user, not a default to guess, so `#0435` treats scheduling
`pull-backups.sh` as **out of scope** and leaves the script exiting 2 until
someone supplies a real value:

```bash
BACKUP_SSH_HOST=ec2-user@<this-project's-host> bash scripts/db/pull-backups.sh
```

### Fix the `/var/backups/postgres` permission blocker (`#0435`)

**Confirmed read-only, 2026-09-08, and still true:** `/var/backups/postgres`
is `root:root 0700` — the `opencircuit-backup.service` unit above runs as
`User=postgres`, and postgres holds none of owner/group/other bits on that
directory, so it cannot even `stat` into it, let alone `mkdir` the
`opencircuit` subdirectory `backup.sh` needs. Every unit `start` fails at the
very first `mkdir -p "$BACKUP_ROOT"` until this is fixed. This must run
**before** the install step below, and needs the user's approval like every
other change in this section (`CLAUDE.md` §5b, §9):

```bash
sudo chown postgres:postgres /var/backups/postgres
```

No output on success. This is **not** recursive: it changes only the
directory's own owner/group, not its contents. `/var/backups/postgres`
currently holds one subdirectory, `shortlinks/`, containing two dump files
from a one-off manual run on 2026-08-17 (all `root:root 0700`, confirmed
read-only) — there is no cron job, timer, or running service on this box that
depends on `/var/backups/postgres` staying root-owned, so reassigning the
*parent* directory does not disturb anything live, and `shortlinks/` and its
contents keep their existing ownership untouched. Verify both facts after
running it:

```bash
stat -c '%U:%G %a' /var/backups/postgres /var/backups/postgres/shortlinks
```

Expect `postgres:postgres 700` on the first line and `root:root 700`,
unchanged, on the second.

**Why `chown`, not a mode change.** Mode `0700` with `root` as owner gives the
`postgres` OS user zero bits under any class — owner, group, or other — so
without a `chown`, granting `postgres` write access would mean opening the
directory to "other" (e.g. `0703`), which is strictly worse for a path whose
own contents include, per `scripts/db/backup.sh`'s own header comment,
"session tokens, passkey credentials, and the audit log." Making `postgres`
the owner keeps the directory exactly as private as it is today, to exactly
the two accounts (`root`, `postgres`) that legitimately need it.

### Bring the on-box checkout current for the media leg (`#0435`)

**A second, independent blocker, found by re-deriving rather than trusting
the last recorded state:** `/opt/opencircuit`'s git checkout is at `ef0a58f`
— 711 commits behind this repo's current `main`, and specifically from
*before* `#0434` added the media-backup leg. `scripts/db/backup-media.sh` and
`scripts/db/restore-media.sh` do not exist there at all, and the copy of
`opencircuit-backup.service` in that checkout is still the single-`ExecStart=`
version predating `#0434`. `sha256sum` confirms every *other* file this
section touches — `scripts/db/backup.sh`, `restore.sh`, `pull-backups.sh`,
`backup-alert.sh`, `opencircuit-backup.timer`, and
`opencircuit-backup-alert.service` — is already byte-identical on the box to
this repo's current commit, so only these three need updating.

Installing today's committed `opencircuit-backup.service` without first fixing
this would not fail loudly — the database leg (`ExecStart=` #1) would
succeed, then the media leg (`ExecStart=` #2) would fail with `bash: cannot
access '/opt/opencircuit/scripts/db/backup-media.sh': No such file or
directory`, which marks the *whole* oneshot unit failed and fires
`opencircuit-backup-alert.service` every single night from night one — even
though the database dump the operator actually cares about most had already
succeeded. A full redeploy (`git pull --rebase origin main && ./scripts/deploy.sh`,
see the Redeploy procedure in `docs/deployment.md`) would also fix this, but
that rebuilds the whole Go binary and SPA on a 418 MB box (`CLAUDE.md` §7) and
is `#0404`'s job at the next real deploy, not a prerequisite for turning
backups on today. The narrower fix — copying just these three files from a
local checkout of this repo, byte for byte — is what the rest of this section
assumes. Run from the local machine, **not** on the box:

```bash
scp scripts/db/backup-media.sh scripts/db/restore-media.sh ec2:/opt/opencircuit/scripts/db/
scp deploy/systemd/opencircuit-backup.service ec2:/opt/opencircuit/deploy/systemd/opencircuit-backup.service
```

Then confirm the bytes landed correctly (values below match this repo's
`main` as of `#0435`; if `main` has since moved, recompute locally with
`shasum -a 256` instead of trusting these):

```bash
ssh ec2 sha256sum /opt/opencircuit/scripts/db/backup-media.sh /opt/opencircuit/scripts/db/restore-media.sh /opt/opencircuit/deploy/systemd/opencircuit-backup.service
```

Expect:

```
98b7d45f3d915e6b38fddf9f16da0cd9cc57c05b0d526bdcced6daf3a871724d  /opt/opencircuit/scripts/db/backup-media.sh
542aeffd610b1595837b2d8a35474524e3b4e16c56fb697bad6561b65a29a5f9  /opt/opencircuit/scripts/db/restore-media.sh
edf5aa0bc3516e6629ced836034269198214e9dfd8f915db501ccb6c6cd034e8  /opt/opencircuit/deploy/systemd/opencircuit-backup.service
```

This leaves `/opt/opencircuit` at the same stale `main` commit in every other
respect (its own `git status`/`git log` will not reflect this) — it is a
targeted content copy, not a `git checkout`, chosen specifically so it cannot
touch anything the running `opencircuit.service` depends on, including the
checkout's own pre-existing, unrelated local modification to
`web/dist/index.html` (confirmed present and left alone).

### Install and enable the timer

Install and enable the timer (the `.service` files are triggered, not
enabled directly — see the "No `[Install]` section" note in
`opencircuit-backup.service`):

```bash
sudo cp opencircuit-backup.service opencircuit-backup.timer opencircuit-backup-alert.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now opencircuit-backup.timer
```

`daemon-reload` re-reads every unit file on the box (not just these three) —
this is a standard, safe, momentary operation; it does not stop or restart
any running service, including `opencircuit.service` or the two ShortLinks
services sharing this host. `enable --now` schedules the timer for its next
`OnCalendar=07:30:00 UTC` occurrence *and*, because the unit sets
`Persistent=true` and has never run before, systemd has no record of a prior
trigger — so on a fresh enable it is likely to also queue an **immediate**
run (within the unit's `RandomizedDelaySec=300`, i.e. within five minutes).
Do not be surprised to see two nearly-adjacent journal entries the first
night: this immediate catch-up run, and then the regular 07:30 UTC one.

Test the backup path by hand before trusting the timer — do this regardless
of whether the timer already fired on its own, since it gives an exact,
timestamped run to inspect:

```bash
sudo systemctl start opencircuit-backup.service   # runs backup.sh, then backup-media.sh, right now
sudo systemctl status opencircuit-backup.service
sudo journalctl -u opencircuit-backup -n 50
```

Expect `systemctl status` to report `Active: inactive (dead)` with
`Main PID: ... (code=exited, status=0/SUCCESS)` for a `Type=oneshot` unit that
has already finished successfully (`inactive` is the normal *resting* state
after a oneshot run, not a sign anything failed — check the exit status, not
the `Active:` word), and the journal to show `backup.sh`'s own banner output
(database name, format, retention) followed by `Done — all databases backed
up.`, then `backup-media.sh`'s equivalent for `/var/www/media`.

### Verify the dump is real, and prove a restore (`#0435`)

A oneshot unit exiting `0` is not proof of a usable backup — `#0434`'s review
established exactly that a script can exit clean while writing nothing
useful. Confirm the file, then confirm it restores.

**1. The file exists, is non-trivial in size, and is a structurally valid
archive** — `pg_restore --list` reads only the archive's table of contents; it
opens no database connection and changes nothing:

```bash
ls -la /var/backups/postgres/opencircuit/
sudo -u postgres pg_restore --list /var/backups/postgres/opencircuit/opencircuit-latest.dump | head -20
sudo -u postgres tar -tzf /var/backups/postgres/media/media-latest.tar.gz
```

Expect the first command to show a `.dump` file well under a megabyte (the
production database holds a handful of rows per table today — 6 subscribers,
1 email campaign, 3 workshops, 12 interests, measured 2026-09-08 — so the
compressed archive is small; that is expected, not a sign of a truncated
dump). Expect `pg_restore --list` to print a table of contents naming
`subscribers`, `email_campaigns`, `workshops`, `interests`, and the rest of
the schema's tables. Expect `tar -tzf` to list the files currently under
`/var/www/media` (`programming_leds.jpg`, `soldering.jpg`, as of 2026-09-08).
A zero-byte file, an empty listing, or a `tar`/`pg_restore` error here means
stop — the backup is not real yet, whatever the unit's exit code said.

**2. A real restore, into a scratch database, never over `opencircuit`**
(`CLAUDE.md` §8b: a mutation must run against a database created for it, never
a shared or production one):

```bash
sudo -u postgres BACKUP_RUN_AS="" RESTORE_CREATE=1 RESTORE_OWNER=opencircuit \
  bash /opt/opencircuit/scripts/db/restore.sh \
  /var/backups/postgres/opencircuit/opencircuit-latest.dump \
  opencircuit_restore_drill
```

Then compare row counts against the live database for the tables that
matter most — subscribers, campaigns, workshops, and the interest taxonomy:

```bash
for db in opencircuit opencircuit_restore_drill; do
  echo "--- $db ---"
  sudo -u postgres psql -tAc "
    select 'subscribers', count(*) from subscribers
    union all select 'email_campaigns', count(*) from email_campaigns
    union all select 'workshops', count(*) from workshops
    union all select 'interests', count(*) from interests;" -d "$db"
done
```

The two blocks of four numbers must match exactly (allowing for any real
signups/changes between the dump and this check — re-run the backup first if
you want a guaranteed-identical pair). Then drop the scratch database; it was
created only for this drill and nothing depends on it:

```bash
sudo -u postgres dropdb opencircuit_restore_drill
```

**Media backup (`#0434`) shares this unit and this timer.** The unit's second
`ExecStart=` runs `scripts/db/backup-media.sh`, which tars `/var/www/media`
(docs/media.md's workshop-cover carve-out) into `$BACKUP_ROOT/media/` — the
same root `scripts/db/backup.sh` writes under, so `pull-backups.sh`'s existing
rsync of the whole root already carries it offsite with no changes of its own.
It needs no permission beyond what the database leg already needs: reading
`/var/www/media` requires nothing extra (verified world-readable on the box,
2026-09-05), and writing under `$BACKUP_ROOT` needs the exact same `chown`
above against `/var/backups/postgres` — nothing additional. See
`scripts/db/backup-media.sh`'s header for the retention rationale (it skips
writing a new archive when the source is unchanged, since these files have no
upload endpoint and rarely move) and `scripts/db/restore-media.sh` for
restoring a dump — always into a scratch directory first, never straight over
`/var/www/media`, and note it needs `sudo`/root to restore ownership to
`ec2-user:ec2-user`.

Test the alert path by deliberately breaking a run (e.g. point `BACKUP_ROOT`
at a path `postgres` cannot write, per `docs/deployment.md`'s Backups
section) and confirming `opencircuit-backup-alert.service` fires:

```bash
sudo journalctl -u opencircuit-backup-alert -n 20
```

To also notify an external channel (Slack/Discord/Mattermost incoming
webhook, or a healthchecks.io-style "fail" URL), create
`/etc/opencircuit/backup-alert.env`:

```env
BACKUP_ALERT_WEBHOOK_URL=https://hooks.example.com/...
```

No such channel is configured anywhere in this repo — that URL does not exist
yet. Until it does, the journal log is the alert. See `docs/deployment.md`'s
Backups section for exactly what this pair of units has and has not been
verified against.

**`#0435`'s recommendation: leave `BACKUP_ALERT_WEBHOOK_URL` unconfigured for
now, deliberately, not as an oversight.** `/etc/opencircuit/backup-alert.env`
has no committed template and confirmed does not exist on the box
(2026-09-08) — there is nowhere written down that a Slack/Discord/Mattermost
webhook or healthchecks.io URL exists for this project at all (`CLAUDE.md` §10
items 2 and 6 record no such channel). Wiring one up would mean inventing a
destination with no real endpoint behind it, which is worse than no alert.
The journal-only path already works without any of that: a failed run logs
`daemon.err` (surfaced by `journalctl -p err`) and, independently, shows up in
`systemctl --failed` the moment `opencircuit-backup-alert.service` itself
runs. That is a real, if manual, signal — check `systemctl --failed`
periodically, or wire an external channel later once one actually exists.
Nothing here needs to be re-decided before the timer is installed; it is a
"leave as configured" call, not a blocker.
