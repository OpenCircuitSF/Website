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
> misleading twice over — the path is under `/media`, not `/assets`, and
> `/assets/` is Vite's own embedded build output, not workshop media at all.
> `000020` is frozen (`CLAUDE.md` §1) and cannot be edited to fix this, so the
> correction is recorded instead in `isSafeCoverImage`'s doc comment
> (`internal/handlers/admin_workshops.go`), which already quoted the same
> wrong text when explaining why the validator rejects other-host URLs
> (`#0432`).

## Adding an image

**Upload it through the admin console (`#0433`, reopening `#0153`).** The
workshop editor's "Cover image" field now has an upload control next to the
text box: choose a JPEG or PNG, and `POST /admin/media/upload`
(`internal/handlers/admin_media_upload.go`) validates it (magic bytes only —
never the filename extension or the browser's claimed `Content-Type`),
strips every metadata segment/chunk that isn't required to render (EXIF,
XMP, IPTC, GPS, device make/model — see "What gets stripped" below), writes
it under `/var/www/media` with a server-generated, content-hashed filename,
and returns the `/media/<name>` path, which the editor drops straight into
the cover image field. Nothing is committed to source control, and nothing
about the request trusts a byte the browser sent for anything beyond the
image's own pixel data.

**Until `#0465`'s production enablement lands, the upload control 503s on
this box specifically.** The endpoint is fully built and tested (see
`internal/media` and `internal/handlers/admin_media_upload_test.go`), but
the service cannot yet write `/var/www/media` in production — that
directory's group ownership and the systemd unit's `ReadWritePaths=` are
`#0465`'s approval-gated change, not this one's. A 503 naming `MEDIA_DIR`
means exactly that: the code works, the box isn't wired up for it yet. The
`scp` workflow below is unaffected and stays the fallback for exactly this
case.

**The `scp` workflow — always available, and the only option before `#0465`:**

```bash
scp -i ~/.aws/AWS.pem soldering-101.jpg ec2-user@<host>:/var/www/media/
```

Then type `/media/soldering-101.jpg` into the workshop's cover image field.

`/var/www/media` is owned by `ec2-user`, so the copy needs no `sudo`. This
keeps working unchanged after `#0465` — the directory's ownership stays
`ec2-user`, with the service only added to its group.

### What gets stripped

The strip is lossless segment/chunk surgery over the raw bytes — no decode,
no re-encode, so pixel data is bit-identical to the original minus the
dropped metadata (the same technique `#0417` used by hand for the two
covers already on disk, generalised into an **allowlist**: keep only what
renders, drop everything else, rather than a denylist built from one
sample's segments).

- **JPEG**: keeps `APP0` (JFIF) and `APP2` only when it carries an embedded
  ICC colour profile; drops every other `APPn` (`APP1` — Exif *and* XMP,
  including any GPS tag — and `APP13` — IPTC/Photoshop — among them) and
  `COM`. Pixel data (`DQT`/`SOF`/`DHT`/`SOS` onward) is untouched.
- **PNG**: keeps `IHDR`/`PLTE`/`IDAT`/`IEND` plus the rendering-relevant
  ancillaries `tRNS`/`gAMA`/`cHRM`/`sRGB`/`iCCP`/`sBIT`; drops everything
  else, including `eXIf`/`tEXt`/`iTXt`/`zTXt`/`tIME`.
- **Everything else is refused outright**, including SVG (script-bearing
  markup — the CSP below is a backstop, not a licence to skip validation).
  **HEIC/HEIF gets its own error message**, since an iPhone shoots HEIC by
  default: export or share the photo as JPEG first.

No `exiftool`, no Pillow, no imaging library of any kind — the box has
neither, and the strip needs neither.

### Filename

The stored filename is generated **server-side**, never from the client's
filename: `<sanitised-stem>-<hash-of-stripped-bytes>.<ext>`. This makes
traversal impossible (no caller-supplied byte ever reaches a path
component), makes the week-long `max-age` below a non-issue (different
bytes always produce a different name), and converges re-uploads of
identical bytes onto the same file instead of accumulating near-duplicates.
Upload is capped at 5 MiB and an absurd pixel count is refused before
anything is written (`image.DecodeConfig`, never a full decode — a memory
decision on a box with ~166 MB available, not a style one).

**Orphan pruning is deliberately out of scope.** Nothing deletes an
unreferenced upload — see `issues/0433.md`'s Plan for why a reliable
reference count isn't available. Deleting a file nothing references is an
operator action, same as it always was.

### Validation

`cover_image` is checked server-side by `isSafeCoverImage`
(`internal/handlers/admin_workshops.go`), on both Create and Patch — the
upload endpoint's own returned path is checked against this SAME function
as a post-condition, never a second, narrower definition of "safe path." A
value is accepted only if it:

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
  - **2026-09-05 (`#0434`):** the "backup path of their own" now exists —
    `scripts/db/backup-media.sh` and `scripts/db/restore-media.sh` are
    written, and `deploy/systemd/opencircuit-backup.service` gained a second
    `ExecStart=` to run the media leg on the same nightly schedule as the
    database dump. **The sentence above stays true until it is actually
    installed and running in production**, per `#0434` acceptance #5 — that
    install is blocked on `#0435` (the timer has never been installed at
    all) and needs the user's go-ahead (`CLAUDE.md` §5b). See `#0434` for
    what has and has not been verified so far.
- **A redeploy does not disturb them.** `scripts/deploy.sh` never touches
  `/var/www`, so images survive `git pull` + rebuild + restart.
- **Uploads are audited.** Every successful `POST /admin/media/upload`
  writes an `audit_log` row (`audit.ActionMediaImageUploaded`) recording the
  actor, filename, stored path, format, byte size, and dimensions — no
  target row, since the upload is not tied to any one workshop (`#0433`).
- **`MEDIA_DIR` (`docs/configuration.md`) controls where uploads land.**
  Unset (the default until `#0465`), the upload endpoint 503s naming the
  variable rather than the route being silently omitted. `#0434`'s nightly
  media backup already covers whatever this directory holds, so uploads
  growing the directory changes that backup's size, not its scope.
- **Populated as of 2026-09-04.** The two live workshop covers,
  `soldering.jpg` and `programming_leds.jpg`, were copied here from their old
  home in `web/public/` and now serve at `/media/soldering.jpg` and
  `/media/programming_leds.jpg` with the documented `max-age=604800` and
  sandboxing CSP, confirmed by `curl -sI`. `#0417`'s dimensions/EXIF work on
  these two files remains open.
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
| Upload button gives a 503 naming `MEDIA_DIR` | Expected until `#0465` lands — use the `scp` workflow above |
| Upload gives a 415 naming HEIC | Export/share the photo as JPEG first (see "What gets stripped") — this server does not convert HEIC |
| Upload gives a 507 | The media directory is out of disk space |

Verify a change with `sudo apachectl configtest` before
`sudo systemctl reload httpd`, then confirm the header block is actually
applied:

```bash
curl -sI https://www.opencircuitsf.com/media/<file> | grep -iE 'HTTP|cache-control|content-security'
```
