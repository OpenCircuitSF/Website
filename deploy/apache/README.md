# Apache virtual host for opencircuitsf.com

This directory contains the Apache (`httpd`) virtual host configuration that
terminates TLS and reverse-proxies all traffic for `www.opencircuitsf.com` to
the Go service listening on `127.0.0.1:8080`. The vhost also answers for the
apex (`opencircuitsf.com`, via `ServerAlias`) but a `RewriteRule` 301-redirects
any request that didn't arrive on `www` before it reaches the proxy rules —
`www` is the canonical host, per PRD §10.2 and `AGENTS.md` §7.

Tested on **Amazon Linux 2023** with `httpd` and `mod_ssl`.

## Files here

| File | What it is |
|---|---|
| `opencircuitsf.com.conf` | The **reference** vhost: one self-contained `:443` vhost doing apex→www, the `/.well-known/` exclusion, the proxy, and the security headers. Use it when standing up a fresh box. |
| `installed-001-www.opencircuitsf.com-le-ssl.conf` | A copy of what is actually running on the production box as `/etc/httpd/conf.d/001-www.opencircuitsf.com-le-ssl.conf`, **re-captured 2026-11-16 (`#0681`)**. **The live file is authoritative; this copy follows it.** It is byte-identical to the live file except for two comment lines that cite `AGENTS.md` §7/§8 where the live file still says `CLAUDE.md` (a stale name; the live comments are fixed on the next hand edit there). It differs from the reference file: no apex→www redirect (the box's `002-…` file does that), a short-ramp HSTS `max-age`, the backend port as `${PORT_OPENCIRCUIT}` rather than a literal, and the box's per-site log layout. Nothing enforces the sync — re-diff after any change to the running config: `diff <(ssh photon 'sudo cat /etc/httpd/conf.d/001-www.opencircuitsf.com-le-ssl.conf') deploy/apache/installed-001-www.opencircuitsf.com-le-ssl.conf`. |

## Box-wide definitions the installed copy relies on

The installed file is not self-contained. Two names in it are defined
elsewhere on the box, and neither definition is in this repo:

- **`${PORT_OPENCIRCUIT}`** — an Apache `Define` in
  `/etc/httpd/conf.d/000-ports.conf` (value `8080`). That file is generated
  from the box's own port registry and says not to edit it by hand; it sorts
  ahead of the `001-…` vhosts, so the variable is defined before any vhost
  uses it. To change the port, change the registry and the service's
  `PORT`, not this vhost.
- **`vhostlog`** — a `LogFormat` nickname in `/etc/httpd/conf/httpd.conf`
  (`"%V:%p %h %l %u %t \"%r\" %>s %b \"%{Referer}i\" \"%{User-Agent}i\""`, i.e.
  combined with the requested host and port prefixed). It is deliberately
  not named `vhost_combined`: certbot's `options-ssl-apache.conf`, which
  every `-le-ssl.conf` vhost `Include`s, redefines that nickname without the
  host field and would silently shadow it on `:443`.

Logs land in `logs/sites/www.opencircuitsf.com/{error_log,access_log}`
(relative to `ServerRoot`, i.e. `/var/log/httpd/sites/…`). The reference
`opencircuitsf.com.conf` still uses a literal `8080` and `combined` so it
stays usable on a fresh box that has neither definition.

## Install

On AL2023, drop the file into `/etc/httpd/conf.d/` — all `.conf` files in that
directory are loaded automatically. There is no `a2ensite` command:

```bash
sudo cp opencircuitsf.com.conf /etc/httpd/conf.d/ && sudo systemctl reload httpd
```

The proxy and SSL modules are included in the `httpd` and `mod_ssl` packages
installed during server setup — no separate module-enable step is needed.

## Obtain the Let's Encrypt certificate

The vhost references certificate paths under
`/etc/letsencrypt/live/opencircuitsf.com/`. That lineage is a **wildcard**
certificate — `opencircuitsf.com` + `*.opencircuitsf.com`, ECDSA — issued
through the **`dns-route53`** authenticator (DNS-01 via Route 53, which
never touches a vhost or an ACME webroot):

```bash
sudo certbot certonly --dns-route53 -d opencircuitsf.com -d '*.opencircuitsf.com'
```

This is the certificate `certbot-renew.timer` renews twice daily through
the same authenticator, so renewal never rewrites Apache config either.

**Do not issue a two-name `--apache` certificate into this lineage.** It
would replace the wildcard, and the wildcard is shared: every other name
under the domain — `go.opencircuitsf.com` (the separate ShortLinks deploy),
`lists.opencircuitsf.com`, any future subdomain — presents this same
certificate, so narrowing it takes those hosts offline. Let's Encrypt's
duplicate-certificate rate limit can then block reissuing the wildcard for
up to a week. See `docs/deployment.md` §9 for the full renewal picture.

## Notes

- The `/api/events` route is proxied with `flushpackets=on` and **must** appear
  before the wildcard `ProxyPass /` line. Apache evaluates `ProxyPass`
  directives top-to-bottom, so if the wildcard came first it would match the SSE
  path and response buffering would not be disabled.
- The `RewriteCond`/`RewriteRule` pair must appear before the `ProxyPass`
  directives so a request that arrived on the apex is redirected before it
  can be proxied to the backend.
- `www` is the canonical host; a request on any other name is redirected to
  it, never proxied directly. This matches production and is the opposite of
  an old, now-corrected reading of `#0064`'s acceptance criteria — see
  `AGENTS.md` §7.
- **Security headers** (HSTS, `X-Content-Type-Options`, `X-Frame-Options`,
  `Referrer-Policy`, and a `Content-Security-Policy` with no `unsafe-inline`
  for scripts) are set with `Header always set …`, which needs `mod_headers`.
  It ships in AL2023's `httpd` package and is loaded by default — if the
  headers don't appear in a response, confirm with `httpd -M | grep
  headers_module` before assuming the directive is wrong. **Now checked
  against the real AL2023 `httpd` 2.4.68** during the 2026-08-25 deploy —
  every module this vhost needs (`ssl`, `proxy`, `proxy_http`, `rewrite`,
  `headers`, `alias`) is loaded by default there, and the equivalent config
  passes `httpd -t` and serves correctly. The `script-src` hash is likewise
  now verified against a real `npm run build` output and against the bytes
  Apache serves. See `docs/deployment.md`'s "Security headers" section, and
  why style-src still needs `unsafe-inline`.
- **`/.well-known/` is excluded from the proxy on purpose.**
  `/.well-known/atproto-did` holds the domain's Bluesky DID and is served from
  disk; the Go service answers `404` on that path, so removing the
  `ProxyPass /.well-known/ !` line silently breaks Bluesky handle
  verification. Verify it by hash after any change to this file, not by status
  code alone.
- **The installed vhost on the real box is not a copy of this file.** That box
  already had a certbot-managed `001-www.…-le-ssl.conf` / `002-…` pair, where
  the `002` file does the apex→www redirect this file does inline. Adding this
  file verbatim would duplicate `ServerName www.opencircuitsf.com`. Treat it
  as the reference for the proxy / header / CSP block; edit the installed
  files. `AGENTS.md` §7 names them.
