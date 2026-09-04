# SEO & Social Preview Cards

Not yet implemented — lands with the public marketing pages (Phase 2) and
is refined in Phase 7. This is a stub with real headings pending that
work; `PRD.md` §7.4 is authoritative:

```bash
sed -n '/^### 7\.4 /,/^#\{2,3\} [0-9]/p' PRD.md
```

## Why this matters here specifically

The site's entire purpose is to receive social traffic — workshop
announcements shared into Discord, Slack, iMessage, X, Facebook, and
LinkedIn. None of those crawlers execute JavaScript, so a plain SPA gives
every URL the same generic card regardless of which workshop or page it
actually links to. Server-injected meta tags are not optional polish for
this project; they're the mechanism that makes sharing work at all.

## Planned approach (`internal/seo`)

1. Hold the built `index.html` in memory with placeholder markers for
   `<title>`, `<meta name="description">`, `og:title`, `og:description`,
   `og:image`, `og:url`, `og:type`, and `twitter:card`.
2. On each SPA request, match the path against a small route table. For
   `/workshops/{slug}`, look up the workshop and use its title, summary,
   and cover image. For static routes, use a compiled-in table.
3. Substitute and serve. **HTML-escape every substituted value** — a
   workshop title or description is admin-entered content, not a trusted
   constant.
4. Cache the rendered `index.html` per path with a short TTL; invalidate
   on workshop mutation so an edited title doesn't serve stale social
   cards.

This replaces the current `internal/handlers.NewSPAHandler`'s plain
catch-all (`GET /`, serving the embedded `index.html` unmodified) with a
path-aware variant — the plain SPA handler is not deleted, just extended,
since most routes (the SPA's own view-switch paths) have no per-route
metadata to inject.

## Also planned

- `GET /sitemap.xml` — generated from published workshops + the static
  route table.
- `GET /robots.txt`.
- `GET /favicon.svg` (see [`design.md`](design.md) for the logo asset set
  this draws from).
- JSON-LD `Event` structured data inside each workshop detail page's
  server-injected `<head>` — what makes workshops eligible for rich
  results in search, distinct from the Open Graph card that governs how a
  shared link previews in chat apps.

## Per-workshop Open Graph card (#0273)

Before #0273, every workshop without a `cover_image` shared the single
generic `og-default.png`. `internal/seo`'s `Site.WorkshopCardHandler` now
generates a distinct 1200x630 card per published workshop, server-side, at
request time:

- **Route**: `GET /workshops/{slug}/og.png`, registered in
  `cmd/opencircuit/main.go`'s `mountAndServe` as a pattern more specific
  than the `GET /` SPA catch-all — no change to
  `internal/handlers/routes.go`'s `workshopDetailPattern`
  (`^/workshops/[^/]+$`) was needed, since that pattern does not match a
  path with a second segment.
- **Eligibility**: gated on `Renderer.cardableWorkshop` (`internal/seo/seo.go`)
  — `Status == WorkshopPublished && Published` — the exact predicate
  `workshopRouteMeta`'s `og:image` branch also uses, so the two cannot
  drift apart. A canceled workshop keeps the generic `og-default.png`
  fallback per `#0135`'s ruling; a draft or unknown slug 404s at this route
  rather than serving a generic PNG under a workshop-specific URL, which
  would itself leak the slug's existence.
- **`cover_image` still wins.** When a workshop has one set, it is used
  unchanged; the generated card is only the fallback for a workshop that
  doesn't.
- **Rendering**: `golang.org/x/image/font/opentype` draws the workshop's
  title (Archivo 800, shrink-to-fit then word-wrapped to at most 3 lines,
  ellipsized if still too long), an optional "date · venue" line
  (JetBrains Mono, omitted entirely if neither `StartsAt` nor
  `LocationName` is set), and a fixed "$ opencircuitsf.com" command line,
  onto a pre-rendered base image (dark ground + tinted logo mark + one
  rule) embedded from `internal/seo/cardassets/`. The title block is
  vertically centered within its band rather than pinned to a fixed
  offset — a workshop with no date/venue would otherwise leave a large
  empty band through the card's middle. Encoded with the standard library's
  `image/png`.
- **Regenerating the embedded assets**: `assets/og/build-card-fonts.py`
  (converts the project's self-hosted `.woff2` faces to the static TTFs
  `golang.org/x/image/font/sfnt` requires) and
  `assets/og/build-card-base.py` (the base card PNG). Both write into
  `internal/seo/cardassets/`, since `//go:embed` cannot read a path outside
  its own package.
- **Caching**: an in-memory `cardCache`, keyed by slug, invalidated by the
  same `Site.Invalidate()` every other cache in this package already goes
  through. Served with `Cache-Control: public, max-age=3600`, a strong
  `ETag`, and `304` on a matching `If-None-Match`.
- **`og:type` stays `website`** for workshop pages — a deliberate decision,
  not an oversight. Open Graph has no `event` type; `article` would mean
  editorial written content, and `article:published_time` is a publication
  timestamp, not the event date. The event semantics are already carried
  correctly by `#0055`'s schema.org `Event` JSON-LD, and the date/venue a
  reader needs is rendered directly into the card image. `/archive/{slug}`
  pages keep `og:type: article`, correctly — those pages are articles.
- **`og:site_name`** is a static literal in `web/index.html` (it never
  varies by route); **`twitter:title`/`twitter:description`** are explicit
  tokens that mirror `og:title`/`og:description` at substitution time
  rather than independent `RouteMeta` fields.

## Where to look (once built)

| Concern | Package (planned) |
|---|---|
| Meta tag injection, sitemap, robots.txt | `internal/seo` |
| Current plain SPA catch-all (pre-SEO) | `internal/handlers/static.go` |
| Workshop data the meta tags read from | `internal/workshops` (Phase 6) |
