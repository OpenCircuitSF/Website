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

## Where to look (once built)

| Concern | Package (planned) |
|---|---|
| Meta tag injection, sitemap, robots.txt | `internal/seo` |
| Current plain SPA catch-all (pre-SEO) | `internal/handlers/static.go` |
| Workshop data the meta tags read from | `internal/workshops` (Phase 6) |
