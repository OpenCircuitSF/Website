# Apache virtual host for opencircuitsf.com

This directory contains the Apache (`httpd`) virtual host configuration that
terminates TLS and reverse-proxies all traffic for `www.opencircuitsf.com` to
the Go service listening on `127.0.0.1:8080`. The vhost also answers for the
apex (`opencircuitsf.com`, via `ServerAlias`) but a `RewriteRule` 301-redirects
any request that didn't arrive on `www` before it reaches the proxy rules —
`www` is the canonical host, per PRD §10.2 and `CLAUDE.md` §7.

Tested on **Amazon Linux 2023** with `httpd` and `mod_ssl`.

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
`/etc/letsencrypt/live/opencircuitsf.com/`. Obtain a single certificate
covering both names with certbot:

```bash
sudo certbot --apache -d opencircuitsf.com -d www.opencircuitsf.com
```

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
  `#0064`'s acceptance criteria, which are a known tracker defect — see
  `CLAUDE.md` §7.
