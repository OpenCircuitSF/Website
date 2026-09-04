# SEO & Social Preview Cards

`internal/seo` is built and live: per-route meta injection, `sitemap.xml`,
`robots.txt`, schema.org `Event` JSON-LD, and (`#0273`) a generated
per-workshop Open Graph card. This document describes it as it actually
exists — every claim below was checked against the package's source, not
against the previous draft of this file.

`PRD.md` §7.4 is stale, not authoritative — see `## PRD §7.4 is out of
date` at the bottom before trusting it over this file.

## Why this matters here specifically

The site's entire purpose is to receive social traffic — workshop
announcements shared into Discord, Slack, iMessage, X, Facebook, and
LinkedIn. None of those crawlers execute JavaScript, so a plain SPA gives
every URL the same generic card regardless of which page it actually
links to. Server-injected meta tags are the mechanism that makes sharing
work at all, not optional polish.

## Per-route meta injection

`seo.Renderer` (`internal/seo/seo.go`) holds the built `index.html`
template in memory and substitutes a fixed set of `%%OC_*%%` placeholder
tokens (`web/index.html`'s markers): `TITLE`, `DESCRIPTION`, `OG_TITLE`,
`OG_DESCRIPTION`, `OG_IMAGE`, `OG_URL`, `OG_TYPE`, `TWITTER_CARD`,
`TWITTER_TITLE`, `TWITTER_DESCRIPTION`, and `JSONLD`. `og:site_name` is a
static literal in `web/index.html`, not a token — it never varies by
route. `twitter:title`/`twitter:description` (`#0273`) explicitly mirror
`og:title`/`og:description` at substitution time rather than being
independent fields, so there is one value per route to keep correct, not
two that can drift; `twitter:image` was deliberately not added at all —
Twitter/X falls back to `og:image` on its own.

Every substituted value is passed through `html.EscapeString` — a workshop
title or summary is admin-entered content, not a trusted constant, so this
is a real injection surface once it lands inside a `<meta content="...">`
attribute. The `JSONLD` token is the one exception: it is either `""` or a
complete `<script type="application/ld+json">...</script>` block whose
body was produced by `encoding/json.Marshal`, which already HTML-escapes
`<`, `>`, and `&` — escaping it a second time would corrupt the JSON.

`Site.Middleware` (`internal/seo/site.go`) wraps `handlers.NewSPAHandler`'s
catch-all: it buffers the SPA handler's response, and if the response was
HTML, replaces the body with `Renderer.Render(path)`'s output at whatever
status the SPA handler chose (200 for a known route, 404 for a miss —
`#0022`'s behavior is preserved structurally, not by convention, since
`Middleware` mirrors the upstream status rather than recomputing it). A
non-HTML response — a hashed JS/CSS asset, a PNG favicon, a missing-asset
404 — passes through unchanged; `Renderer.Render` is never even called for
those.

Resolution order (`Renderer.resolve`), in priority:

1. An exact match in the compiled-in static route table — `/`, `/about`,
   `/privacy`, `/workshops`, `/subscribe`, `/archive` — each with its own
   title, description, and OG fields.
2. A `/workshops/{slug}` match, resolved against the configured
   `WorkshopSource`.
3. An `/archive/{slug}` match, resolved against the configured
   `ArchiveSource` (`#0123`).
4. The generic fallback metadata, for any other path
   `handlers.IsKnownRoute` accepts — the Phase 1 auth/account views
   (`/login`, `/account`, `/admin`, `/confirm`, `/preferences`,
   `/unsubscribe`, `/subscribe/thanks`, `/register/verify`,
   `/recover/verify`). These are never meant to be shared as social
   previews, so a generic card is correct, not a gap.
5. A distinct not-found default — its own title/description, not a copy of
   the site default — for anything `IsKnownRoute` rejects.

## Workshop and archive sources

Both `WorkshopSource` and `ArchiveSource` (`internal/seo/workshop.go`,
`internal/seo/archive.go`) are narrow interfaces the real stores are
adapted down to (`cmd/opencircuit/main.go`'s `workshopSEOSource` and
`campaignArchiveSEOSource`), and both are nil-tolerant: `Renderer` and
`Sitemap` accept `nil` and degrade to the generic fallback rather than
erroring, which is what lets a deploy mode with no campaigns-table backing
still work. Each interface returns every row regardless of status —
status filtering (published-only, published-or-canceled, etc.) happens in
`internal/seo` itself, so it is exercised by `go test ./internal/seo/...`
rather than trusted to whatever the real store's `WHERE` clause happens to
do.

A workshop is eligible for its own metadata when
`(Status == WorkshopPublished || Status == WorkshopCanceled) &&
Published` — `Published` (`#0171`) is `true` only once the workshop has
actually gone live at least once, which keeps a canceled-before-ever-
published draft out of anything indexable. An archive entry is eligible
when `Published` (`archive_status == 'published'`, `#0123`/PRD §6.8).

## Event JSON-LD (`#0055`)

`internal/seo/jsonld.go` builds a schema.org `Event` block for a
`/workshops/{slug}` page — structured data for Google Rich Results,
independent of the Open Graph card. A block is only emitted when the
workshop is published-or-canceled-and-published (the same gate above)
**and** has both a start date and a location (name or address). Missing
either renders no `<script>` tag at all — Google's Event guidance treats
`name`/`startDate`/`location` as required for a non-virtual event, and a
block missing one of them fails Rich Results validation, which is worse
than omitting structured data for a workshop whose details aren't
finalized.

`eventAttendanceMode` is always `OfflineEventAttendanceMode`; there is no
online/virtual concept in `internal/workshops.Workshop` to branch on. The
organizer is always a fixed `Open Circuit SF` (with the site URL), never
derived from the workshop's own venue fields.

**Canceled workshops diverge from the social-card fallback on purpose.**
`workshopRouteMeta` still serves a canceled workshop's `<title>`/`og:*`
tags from the generic site fallback (see `## Deliberate limits` below),
but its JSON-LD is real and per-workshop, using schema.org's
`EventCancelled` status — the field schema.org designed specifically so a
search result can say "this was scheduled and got canceled" rather than
pretending the event never existed or silently vanishing it. Because that
per-workshop JSON-LD must not leak between different canceled workshops,
a canceled-and-published workshop gets its own cache entry
(`"workshop:{slug}"`), not the single shared fallback bucket every other
canceled/draft/unknown slug shares.

## Sitemap and `robots.txt`

`GET /sitemap.xml` (`internal/seo/sitemap.go`) lists the curated marketing
routes (`/`, `/about`, `/privacy`, `/workshops`, `/subscribe`, `/archive`
— deliberately not `handlers.StaticRoutes()`, which also holds the Phase 1
auth/account/token routes that must never be indexed), every workshop with
`Status == WorkshopPublished` (draft, unpublished, **and canceled** are all
excluded — canceled workshops are absent from the sitemap on purpose, see
below), and every archive entry with `Published == true` (excludes pending
and withheld, per PRD §6.8). It is cached with a 60-second TTL and rebuilt
on invalidation.

`GET /robots.txt` (`internal/seo/robots.go`) disallows `/admin`,
`/account`, `/api`, `/auth`, `/confirm`, `/preferences`, and
`/unsubscribe` — the last three are known SPA routes that carry a token in
the query string, so an indexed URL there is a token leak — and points
crawlers at `/sitemap.xml`. It has no dynamic content, so it's built once
at `Site` construction rather than cached with a TTL.

`GET /favicon.svg` is **not** part of `internal/seo`. It is a plain static
file under `web/public/` (and therefore `web/dist/`), served by
`handlers.SPAHandler`'s ordinary embedded-file lookup like any hashed
asset. An earlier draft of this document listed it as something
`internal/seo` generates; it doesn't.

## Cache invalidation

`Site.Invalidate` (`internal/seo/site.go`) clears three caches in one
call: the per-route meta cache (`Renderer.Invalidate`), the sitemap cache
(`Sitemap.invalidate`), and the per-workshop card cache
(`cardCache.invalidate`, `#0273`). `cmd/opencircuit/main.go` constructs
exactly one `*Site` and threads the same pointer into every caller that
needs to invalidate it, so all three caches are cleared together
regardless of which caller triggered it. As of `#0319` those callers are:

- workshop create/update/publish/cancel (`#0051`'s original trigger),
- the admin campaign archive toggle
  (`handlers.AdminCampaignArchiveHandler`), and
- the send worker's own archive-publish transition
  (`internal/mailing.Worker`, via the `ArchiveCacheInvalidator` seam).

Both the meta-render cache and the card cache are bounded (512 and 64
entries respectively) with a full-flush-on-overflow eviction policy rather
than per-entry LRU bookkeeping — re-rendering is cheap (a string
substitution, or an 8–13ms PNG render), so a hard cap that occasionally
costs one wasted regeneration is simpler to reason about. The meta cache
is additionally keyed by *resolved bucket*, not by raw request path
(`#0073`): every unknown path shares one "not found" cache entry rather
than growing the cache once per distinct path an anonymous client can
invent. The render TTL is 60 seconds, so a mutation that reaches the
database without going through `Site.Invalidate` (a manual fix, say) is
still visible within a minute.

## Per-workshop Open Graph card (`#0273`)

Before `#0273`, a workshop without a `cover_image` shared the single
generic `og-default.png` — every such workshop looked identical when
shared. `Site.WorkshopCardHandler` now generates a distinct 1200×630 PNG
per eligible workshop, server-side, at request time.

- **Route**: `GET /workshops/{slug}/og.png`, registered in
  `cmd/opencircuit/main.go`'s `mountAndServe` as its own pattern, more
  specific than the `GET /` SPA catch-all — no change to
  `internal/handlers/routes.go`'s workshop-detail pattern was needed,
  since it doesn't match a path with a second segment.
- **Eligibility**: `Status == WorkshopPublished && Published` — the exact
  predicate `workshopRouteMeta`'s own `og:image` branch uses on the
  workshop it already fetched, so the two structurally cannot drift apart.
  A canceled workshop keeps the generic fallback (below) and 404s at this
  route rather than serving a generic PNG under a workshop-specific URL,
  which would itself leak the slug's existence; the same 404 applies to a
  draft or unknown slug.
- **`cover_image` still wins.** When a workshop has one set, it is served
  unchanged; the generated card is only the fallback for a workshop that
  doesn't.
- **Rendering**: `golang.org/x/image/font/opentype` draws the workshop's
  title (Archivo 800, shrunk through a descending size ladder then
  greedily word-wrapped to at most 3 lines, ellipsized if it still
  overflows), an optional "date · venue" line (JetBrains Mono, omitted
  entirely if neither is set), and a fixed `$ opencircuitsf.com` line,
  composited onto a pre-rendered base image (dark ground, tinted logo
  mark, one rule) embedded from `internal/seo/cardassets/`. The title
  block is vertically centered within its band rather than pinned to a
  fixed offset, so a workshop with no date/venue doesn't leave an empty
  band through the card's middle.
- **Pacific time, zone-labeled.** The date/venue line renders in
  `America/Los_Angeles` with the zone abbreviation shown (`Jan 2, 2026,
  6:00 PM PDT`/`PST`, as the instant falls) — a deliberate second-review
  correction (`#0273`'s bounce) from an initial UTC rendering. The
  workshops this card advertises are physical, in-person Bay Area events,
  and there is no viewer request to key a timezone off in the first
  place: this handler is fetched by unfurler crawlers, not browsers. This
  mirrors `#0144`'s identical ruling for the workshop-announcement email
  body, rather than inventing a second answer to the same question.
- **Regenerating the embedded assets**: `assets/og/build-card-fonts.py`
  (converts the project's self-hosted `.woff2` faces to the static TTFs
  `golang.org/x/image/font/sfnt` requires) and
  `assets/og/build-card-base.py` (the base card PNG). Both write into
  `internal/seo/cardassets/`, since `//go:embed` cannot read a path
  outside its own package.
- **Caching**: an in-memory cache keyed by slug, cleared by
  `Site.Invalidate` alongside the other two caches. Served with
  `Cache-Control: public, max-age=3600`, a strong `ETag`
  (SHA-256 of the PNG bytes), and `304` on a matching `If-None-Match`.

## Deliberate limits

These are settled decisions, not gaps waiting to be closed. A doc that
lists only capabilities invites someone to "fix" a choice that was made on
purpose — read the linked issue before reopening any of these.

- **No cover-image upload endpoint in v1 (`#0153`).** `cover_image` is
  path-or-URL text entry, validated as an absolute same-origin path with
  no control characters — an admin can only reference an image someone has
  already put on the server by some other means (in practice, a commit and
  a deploy). Decided against an upload endpoint because the volume doesn't
  justify the surface (content-type sniffing, path traversal, size
  limits, collision handling, orphan pruning, for a field set a handful
  of times a month), it would be the first mutable-data directory in a
  deploy model that is otherwise `//go:embed` plus Postgres, and AWS
  storage wasn't available to choose blind at the time. Revisit if `#0232`
  (making the platform usable as a dependency) lands — a downstream
  consumer can't commit an image into *this* repository's `assets/`.
- **A canceled workshop's card and sitemap presence stay generic
  (`#0135`).** `#0051`'s reviewer ruled that canceled workshops must never
  appear in the sitemap or carry their own unfurled OG card — independent
  of the public workshop *index*'s own visibility rule, which `#0135`
  separately widened to include canceled workshops so a bookmarking
  visitor isn't told a canceled workshop silently vanished. `internal/seo`
  was deliberately left alone by that change: a canceled workshop's
  `<title>`/`og:*` tags fall back to the generic site metadata, and it is
  excluded from `sitemap.xml`, even though (see `## Event JSON-LD` above)
  its schema.org `Event` block is real and workshop-specific.
- **`og:type` stays `website` for workshop pages, not `article`
  (`#0273`).** Open Graph has no `event` type, so the real choice was
  between `website` and `article`. `article` was rejected: it means
  editorial written content, and the field it would unlock,
  `article:published_time`, is a *publication* timestamp — not the event
  date a shared workshop link actually needs to convey. The event
  semantics already live correctly in the schema.org `Event` JSON-LD
  above, and the date/venue a reader needs is rendered directly into the
  card image itself, so `og:type` has nothing left to add.
  `/archive/{slug}` pages keep `og:type: article`, correctly — those pages
  really are articles.

## Where to look

| Concern | File |
|---|---|
| Meta tag injection, route resolution, caching | `internal/seo/seo.go` |
| `Site` wiring: middleware, sitemap/robots/card handlers, `Invalidate` | `internal/seo/site.go` |
| schema.org `Event` JSON-LD | `internal/seo/jsonld.go` |
| `sitemap.xml` | `internal/seo/sitemap.go` |
| `robots.txt` | `internal/seo/robots.go` |
| Per-workshop OG card rendering (`#0273`) | `internal/seo/card.go` |
| Embedded card fonts and base image | `internal/seo/cardassets/` |
| `WorkshopSource`/`Workshop` shape | `internal/seo/workshop.go` |
| `ArchiveSource`/`ArchiveEntry` shape | `internal/seo/archive.go` |
| Card asset regeneration scripts | `assets/og/build-card-fonts.py`, `assets/og/build-card-base.py` |
| Route wiring (`GET /sitemap.xml`, `/robots.txt`, `/workshops/{slug}/og.png`, `GET /`) | `cmd/opencircuit/main.go`'s `mountAndServe` |
| Plain SPA catch-all `Site.Middleware` wraps | `internal/handlers/static.go` (`SPAHandler`) |
| `%%OC_*%%` placeholder markers | `web/index.html` |
| `IsKnownRoute`/`WorkshopDetailSlug`/`ArchiveDetailSlug` | `internal/handlers/routes.go` |

## PRD §7.4 is out of date

`PRD.md` §7.4 still describes the pre-implementation plan almost verbatim
and has not been updated as the subsystem was actually built. Concretely,
as of this writing it:

- omits `/archive/{slug}` and `ArchiveSource` entirely (`#0123`);
- omits `twitter:title`/`twitter:description` and the JSON-LD token
  (`#0055`, `#0273`);
- says the cache is keyed "per path" — that was the design `#0073` found
  and fixed; the real cache is keyed by resolved bucket, specifically to
  avoid unbounded growth from distinct nonexistent paths;
- says invalidation happens "on workshop mutation" only — `#0319` widened
  that to the admin archive toggle and the send worker's archive-publish
  transition;
- doesn't mention `#0273`'s per-workshop card generation, its route, or
  its `og:type` decision at all; and
- lists `GET /favicon.svg` as something this subsystem serves, which (see
  above) it doesn't.

This file, not §7.4, should be treated as the current description of
`internal/seo`. Correcting §7.4 itself is out of scope here — a concurrent
pass owns `PRD.md` this session (`#0414`/`#0418`) — and is worth its own
issue.
