# systemd units for Open Circuit SF

This directory contains the systemd units for the EC2 host:

| Unit | Purpose |
|---|---|
| `opencircuit.service` | Runs `/usr/local/bin/opencircuit serve` as a dedicated non-root user, listens on `127.0.0.1:8080` behind the Apache reverse proxy, restarted automatically on failure |
| `opencircuit-backup.timer` | Fires `opencircuit-backup.service` nightly (`#0229`) |
| `opencircuit-backup.service` | Runs `scripts/db/backup.sh` as `postgres`; `OnFailure=` chains to the alert unit below |
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

Install and enable the timer (the `.service` files are triggered, not
enabled directly — see the "No `[Install]` section" note in
`opencircuit-backup.service`):

```bash
sudo cp opencircuit-backup.service opencircuit-backup.timer opencircuit-backup-alert.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now opencircuit-backup.timer
```

Test the backup path by hand before trusting the timer:

```bash
sudo systemctl start opencircuit-backup.service   # runs backup.sh once, right now
sudo systemctl status opencircuit-backup.service
sudo journalctl -u opencircuit-backup -n 50
```

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
