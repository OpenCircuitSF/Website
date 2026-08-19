# systemd service for Open Circuit SF

This directory contains the systemd unit that manages the Open Circuit SF Go
binary on the EC2 host. The service runs `/usr/local/bin/opencircuit serve` as
a dedicated non-root user, loads its configuration from
`/etc/opencircuit/config.env`, listens on `127.0.0.1:8080` behind the Apache
reverse proxy, and is restarted automatically on failure.

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
