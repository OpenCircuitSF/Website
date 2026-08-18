# Placeholder site

A single self-contained page for `opencircuitsf.com` to point social links at
while the real Svelte + Go site is built. No build step, no dependencies, no
external requests.

```
placeholder/
├── index.html          the whole page — CSS and JS inline
├── logo-mask-512.png   full logo, white alpha mask (tinted by CSS)
├── mark-mask-64.png    chip mark, white alpha mask (header)
├── mark-green-32.png   favicon
├── apple-touch-icon.png
└── og-default.png      1200×630 social share card
```

Total weight is about 290 KB, most of it the two logo masks.

## ⚠️ It must be served over HTTP — `file://` will not work

Opening `index.html` by double-clicking shows the page **with no logo**. CSS
`mask-image` is subject to origin restrictions, and under `file://` every file is
its own opaque origin, so the mask fails to load and the masked element renders
fully transparent. The `<img>`-based favicon still works, which makes this
confusing to diagnose.

To preview locally:

```bash
cd placeholder && python3 -m http.server 8000
# then open http://localhost:8000
```

This is purely a local-preview artifact — over HTTP or HTTPS it works everywhere.

## Before it goes live

Nothing — the page is ready to deploy. The Discord invite
(`https://discord.gg/Fq9ug6QXV3`) is live and is the page's only call to action,
so **check it still resolves before each deploy**. Discord invites can be set to
expire or be revoked, and a dead invite leaves the page with nothing to click.

**There is deliberately no email address on the page.** A `mailto:` on a public
page gets harvested by scrapers within days and the address is then permanently
spammed. Discord is the only contact route for now. The eventual answer is a
server-side contact form that never exposes an address — worth filing alongside
the Phase 3 signup work if anyone needs to reach the group without a Discord
account (a venue offering space, or press).

## Deploying today

### Option A — S3 + CloudFront (recommended if EC2 isn't up yet)

Cheapest and fastest to a working HTTPS site; nothing to patch or keep running.

```bash
aws s3 mb s3://opencircuitsf-site --region us-west-2
aws s3 sync placeholder/ s3://opencircuitsf-site/ --delete \
    --cache-control "public, max-age=300"
```

Then: request an ACM certificate for `opencircuitsf.com` and
`www.opencircuitsf.com` **in `us-east-1`** (CloudFront only reads certificates
from that region — this catches people out), create a CloudFront distribution
with the bucket as origin via OAC, set the default root object to `index.html`,
and point Route 53 A/AAAA alias records at the distribution.

Swapping to EC2 later is a Route 53 record change.

### Option B — EC2 + Apache (if the instance already exists)

```bash
sudo mkdir -p /var/www/opencircuitsf
sudo rsync -a placeholder/ /var/www/opencircuitsf/
sudo chown -R www-data:www-data /var/www/opencircuitsf
```

Minimal vhost, with `www` redirecting to the apex so the passkey relying-party
origin stays singular later:

```apache
<VirtualHost *:80>
    ServerName opencircuitsf.com
    ServerAlias www.opencircuitsf.com
    DocumentRoot /var/www/opencircuitsf
    <Directory /var/www/opencircuitsf>
        Require all granted
        Options -Indexes
    </Directory>
</VirtualHost>
```

```bash
sudo certbot --apache -d opencircuitsf.com -d www.opencircuitsf.com
```

When the real site ships, this vhost becomes the `ProxyPass` to the Go service on
`127.0.0.1:8080` (PRD §10, issue #0064).

## What it does and doesn't do

**Does:** adapts to light and dark (OS setting plus a manual auto → light → dark
toggle persisted in `localStorage`), applies the stored theme before first paint,
renders correctly from 305 px up, ships correct Open Graph and Twitter Card tags
so shared links preview properly, honours `prefers-reduced-motion`, is keyboard
navigable with a skip link and visible focus rings.

**Doesn't:** collect email. There is no backend. Signup is Phase 3 of the PRD
(issues #0023–#0032); until then the Discord link is the conversion path.

## Relationship to the real site

This is a **reference implementation, not throwaway code**. Its token block,
theme toggle, and terminal motifs are the validated source that issues
[#0011](../issues/0011.md) (design tokens) and [#0013](../issues/0013.md)
(motif components) port into the Svelte app. The palette here matches PRD §4.2
exactly.

`og-default.png` is a first pass; issue [#0021](../issues/0021.md) covers the
final version once the display typeface (Archivo) is available — this one is set
in Arial Black as a stand-in.
