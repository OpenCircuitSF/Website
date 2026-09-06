# Certbot deploy hook for opencircuitsf.com

This directory holds the one asset needed to close `#0447`: nothing on the box
currently reloads Apache after `certbot-renew.timer` renews the wildcard
certificate, so `mod_ssl` would go on serving the expired file after
2026-11-16.

| File | What it is |
|---|---|
| `reload-apache-deploy-hook.sh` | A certbot **deploy hook**: certifies the config (`httpd -t`) and then `systemctl reload httpd`. Certbot places `RENEWED_LINEAGE`/`RENEWED_DOMAINS` in the environment before running it; the script requires `RENEWED_LINEAGE` so it refuses to do anything if it is ever run outside a real certbot invocation. |

## Why a deploy-hook-directory script, not `DEPLOY_HOOK` in `/etc/sysconfig/certbot`

Both are legitimate certbot mechanisms; the acceptance criteria for `#0447`
require picking one. Read-only checks on the box (2026-09-06) rule out the
`/etc/sysconfig/certbot` route for this host specifically:

- `systemctl cat certbot-renew.service` shows a bare
  `ExecStart=/usr/bin/certbot renew --quiet` — it does not reference
  `/etc/sysconfig/certbot` or splice `$DEPLOY_HOOK` onto the command line.
  Setting `DEPLOY_HOOK=""` there today has no effect on the timer's actual
  invocation, and making it take effect would mean editing the systemd unit
  too — a second change this issue does not need.
- A script dropped into `/etc/letsencrypt/renewal-hooks/deploy/` needs no
  change to the unit, the sysconfig file, or the renewal conf. Certbot's
  `renew` subcommand scans that directory unconditionally, on every
  invocation shape (the timer's bare `--quiet`, a manual `certbot renew`, or
  `--dry-run --run-deploy-hooks`), and only runs what it finds there after a
  certificate has actually renewed.
- This host renews certificates for several unrelated domains under the same
  certbot install (`beerbeerbeer.me`, `eurekaplatforms.com`,
  `gregariousbots.social`, `sstools.co`, `terriblename.com`, alongside
  `opencircuitsf.com`) — all fronted by the same Apache process. A
  deploy-hooks-directory script fires for renewals of any of them, and
  reloading Apache after any of those renewals is desired, not just harmless.

## No-op when nothing renewed

A certbot deploy hook only runs after a successful renewal — that is what
the hook type itself guarantees, not something this script has to check for
on its own. The script still fails closed: if `httpd -t` does not pass, it
exits nonzero without reloading, so a bad renewal-time config can never take
down the currently-serving Apache process. The certificate itself is still
renewed and on disk either way; only the reload is skipped.

## Install (requires the user's approval — this is a change to production)

```bash
sudo install -m 0755 deploy/certbot/reload-apache-deploy-hook.sh \
  /etc/letsencrypt/renewal-hooks/deploy/reload-apache.sh
```

See `docs/deployment.md`'s TLS section (§9) for the full approval-ready
sequence, including how to prove the hook fires without deploying a real
certificate (`certbot renew --dry-run --run-deploy-hooks`) and exactly what
that dry run does and does not establish.

## Does not touch

Per `#0447` criterion 6, nothing here edits the installed vhosts,
`/etc/letsencrypt/renewal/opencircuitsf.com.conf`, or adds an `installer =`
line to that renewal config. The `/.well-known/` carve-out serving this
domain's Bluesky DID (`CLAUDE.md` §7) lives in the vhost, not in certbot's
renewal path, and stays untouched by this hook.
