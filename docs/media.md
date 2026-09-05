# Workshop cover images (`/media/`)

How a workshop gets a picture. `cover_image` on a workshop row holds a
same-site path like `/media/soldering-101.jpg`; Apache serves that path
straight off the disk of the production box, out of `/var/www/media`, without
the Go service being involved at all.

This document covers the Apache carve-out that makes those URLs resolve, why it
is shaped the way it is, and how to add an image.

## The problem

The whole site is a reverse proxy. `001-www.opencircuitsf.com-le-ssl.conf` ends
with a wildcard that hands everything to the Go service:

```apache
ProxyPass / http://127.0.0.1:8080/
ProxyPassReverse / http://127.0.0.1:8080/
```

That service has no `/media` route — neither the Go handlers nor the Svelte
router define one. So with only the wildcard in place, every image URL is
proxied to the app, falls through its routing, and renders the SPA's 404 view.
The image does not 404 in a way that looks like a missing file; it renders a
full "page not found" screen inside an `<img>` tag, which is a confusing thing
to debug.

## The fix

Three lines, above the wildcard `ProxyPass /`:

```apache
ProxyPass /media !
Alias /media/ /var/www/media/
Alias /media  /var/www/media
```

`ProxyPass … !` only *excludes* the prefix from the proxy — on its own it would
produce a 404, because nothing would then be serving the path. The `Alias`
lines are what actually serve the files. Both directives are required.

**Ordering is load-bearing.** Apache evaluates `ProxyPass` directives
top-to-bottom and takes the first match, so the exclusion must appear before
the wildcard. This is the same rule that governs `/api/events`'s
`flushpackets=on` line (see `deploy/apache/README.md`), and `/media` is the
third of three exclusions in this vhost, alongside `/.well-known/` and
`/prototypes`. All three exist for the identical reason: the Go service has no
route there.

## The `<Directory>` block

```apache
<Directory "/var/www/media">
    AllowOverride None
    Options -Indexes -ExecCGI +FollowSymLinks
    Require all granted

    Header always set Cache-Control "public, max-age=604800"
    Header always set Content-Security-Policy "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox"
    Header always set X-Content-Type-Options "nosniff"
</Directory>
```

Four choices worth keeping:

- **`-Indexes`** — no directory listing, unlike `/prototypes`, which turns
  indexes *on* deliberately. Every image here is referenced by exact path, so
  nothing needs to browse the directory, and a listing of everything ever
  uploaded is not something to publish by accident.
- **`max-age=604800`** — cached hard for a week. Renaming the file is how you
  bust that cache, which is the practical reason `cover_image` stores an exact
  path rather than a name plus some convention.
- **The CSP and `nosniff`** — `default-src 'none'; img-src 'self'; … sandbox`.
  This is the one directory on the site that takes operator-supplied files, so
  it is the one place a stray `.html` or `.svg` could otherwise run script in
  the site's own origin. The policy makes nothing served from here executable,
  regardless of extension or sniffed type. `Header always set` inside a
  `<Directory>` *replaces* the vhost-level CSP for these requests rather than
  adding a second header.
- **`-ExecCGI`** — belt and braces alongside the CSP.

## Why on disk, and why not `/assets/`

**On disk rather than in `web/public/`:** adding a photo would otherwise mean a
rebuild, a redeploy, and a permanently larger binary, since the frontend is
compiled into the Go binary by `//go:embed`. A file dropped in `/var/www/media`
is live immediately, with no deploy.

**Deliberately not under `/assets/`.** That prefix belongs to Vite's hashed
build output, which *is* embedded. Excluding `/assets/` from the proxy would
cut the SPA off from its own JS and CSS.

> The `migrations/000020_create_workshops.up.sql` comment on `cover_image` still
> reads `-- path under /assets`. That predates this setup and is now
> misleading — the path is under `/media`. Worth correcting the next time that
> area is touched.

## Adding an image

There is no upload endpoint. The admin editor's cover image field is a plain
text box, so the workflow is two steps:

```bash
scp -i ~/.aws/AWS.pem soldering-101.jpg ec2-user@<host>:/var/www/media/
```

Then type `/media/soldering-101.jpg` into the workshop's cover image field.

`/var/www/media` is owned by `ec2-user`, so the copy needs no `sudo`.

### Validation

`cover_image` is checked server-side by `isSafeCoverImage`
(`internal/handlers/admin_workshops.go`), on both Create and Patch. A value is
accepted only if it:

1. contains no control characters (`< 0x20` or `0x7f`),
2. starts with a single `/`, and
3. is not protocol-relative — `//evil.example` is rejected, and backslashes are
   normalized to forward slashes first, so `\\evil.example` is caught too.

The rule is same-site paths only: no absolute URLs, no host can be smuggled in.
A client-side twin lives in `web/src/lib/workshopAdmin.ts`, and
`internal/handlers/url_validator_fixture_test.go` exists specifically to keep
the two from drifting apart.

## Operational notes

- **These files are not in git and are not backed up.** The nightly job
  (`deploy/systemd/opencircuit-backup.service`) is `pg_dump` only —
  `BACKUP_DATABASES=opencircuit`, nothing touching the filesystem. Cover images
  live only on that instance. A rebuilt box comes back with the database intact
  and every workshop image broken. If these images matter, they need either a
  backup path of their own or a copy kept in the repo.
- **A redeploy does not disturb them.** `scripts/deploy.sh` never touches
  `/var/www`, so images survive `git pull` + rebuild + restart.
- **The directory is currently empty** (checked 2026-09-04). The wiring is in
  place and unused.
- **The repo's copy of the vhost is in sync.**
  `deploy/apache/installed-001-www.opencircuitsf.com-le-ssl.conf` matches the
  live file byte for byte as of 2026-09-04. Nothing enforces that — it is kept
  current by hand, so re-diff it after any change to the running config.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Image URL renders the SPA's 404 page | The `ProxyPass /media !` line is missing, or sits *below* the wildcard `ProxyPass /` |
| Plain Apache 404 | Exclusion present but an `Alias` line is missing, or the file isn't in `/var/www/media` |
| 403 | File mode — the file needs to be readable by the `apache` user |
| Image loads but a stale version | The week-long `Cache-Control`; rename the file and update `cover_image` |
| Save rejects the path | `isSafeCoverImage` — check it starts with exactly one `/` |

Verify a change with `sudo apachectl configtest` before
`sudo systemctl reload httpd`, then confirm the header block is actually
applied:

```bash
curl -sI https://www.opencircuitsf.com/media/<file> | grep -iE 'HTTP|cache-control|content-security'
```
