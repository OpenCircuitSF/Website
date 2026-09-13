# SEO & Social Preview Cards

`internal/seo` is fully built: per-route meta injection, `sitemap.xml`,
`robots.txt`, schema.org `Event` JSON-LD, the archive source (`#0123`), and
(`#0273`) a generated per-workshop Open Graph card. This document describes
the subsystem as it exists in this tree — every claim below was checked
against the package's source, not against the previous draft of this file.
It deliberately says nothing about what is deployed; `CLAUDE.md` §7 is the
record of what production runs, and parts of what follows post-date it.

`PRD.md` §7.4 was corrected against this file's account on 2026-09-04
(`#0422`) and no longer describes the pre-implementation plan — see `## PRD
§7.4's correction` at the bottom for what changed. This file remains the
fuller reference for implementation detail; §7.4 stays a short summary by
design (`CLAUDE.md` §11 is the index of those sections).

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
adapted down to (`cmd/opencircuit/workshop_seo_source.go`'s
`workshopSEOSource` and `cmd/opencircuit/campaign_archive_seo_source.go`'s
`campaignArchiveSEOSource`, constructed by `main.go`'s `servePostgres`), and
both are nil-tolerant: `Renderer` and `Sitemap` accept `nil` and degrade to
the generic fallback rather than erroring, which is what lets a deploy mode
with no campaigns-table backing still work. `WorkshopSource` deliberately
returns every workshop of every status, and so does
`ArchiveSource.ArchiveEntryBySlug` for its single row: the published-only
and published-or-canceled filtering happens in `internal/seo` itself, so
`go test ./internal/seo/...` exercises it rather than trusting whatever the
real store's `WHERE` clause happens to do. `ArchiveSource.ArchiveEntries`
is the deliberate exception — it is specified to return only published
campaigns, and the real adapter gets that from
`mailing.CampaignStore.ListArchived`'s own `archive_status = 'published'`
filter — so `Sitemap.Build`'s `Published` check over that list is defence
in depth rather than the primary exclusion.

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

**Validate against Google's Rich Results Test after any change here
(`#0149`).** The tool only accepts a publicly reachable URL, so this is a
post-deploy step that `scripts/check.sh` cannot perform. Paste a workshop
detail URL into https://search.google.com/test/rich-results and record
errors *and* warnings, not just errors. The first run, on 2026-09-12
against `/workshops/programming-leds`, reported only two missing optional
properties, `offers` and `performer`; both are deliberately not emitted.
Repeat the run after any change to `jsonld.go`, and include a canceled
workshop next time — `EventCancelled` has not yet been through the tool.

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

**Three `/api` prefixes are allowed back out of that blanket disallow
(`#0518`):** `/api/workshops`, `/api/archive`, and `/api/interests`. Every
public page's server-rendered `<body>` is just `<div id="app"></div>` — all
visible text (workshop listings, workshop detail, archive listings, archive
detail) is fetched by the SPA from these three endpoints
(`web/src/lib/api.ts`) — and Googlebot does not fetch a robots-disallowed
resource even while it renders JavaScript, so disallowing all of `/api`
left rendered archive and workshop pages with a header and footer but no
body text. These three are public, read-only, and carry no token, which is
exactly the reason `/confirm`, `/preferences`, and `/unsubscribe` stay
disallowed above and these do not. Google and Bing both resolve a
conflicting `Allow`/`Disallow` pair by longest match rather than line
order, so the `Allow` lines override `Disallow: /api` for just these
prefixes without needing to reorder anything. Every other `/api/*` route
(`/api/me`, `/api/events`, `/api/subscribe`, `/api/preferences`,
`/api/unsubscribe`, `/api/list-stats`, `/api/crt-session`, `/api/ses/*`) is
session/admin-gated, token-bearing, state-changing, or a server-to-server
webhook, and stays disallowed — see `internal/seo/robots.go`'s
`allowedAPIPaths` doc comment for the full route-by-route audit.

API responses also carry `X-Robots-Tag: noindex` (`seo.NoIndexAPIMiddleware`,
wired around the whole mux in `cmd/opencircuit`'s `mountAndServe`), so a
crawler may fetch the allowed JSON to render a page's text without indexing
the JSON response itself as a search result. The header is scoped to the
literal `/api/` path prefix only — the SPA shell, `/sitemap.xml`,
`/robots.txt`, and the rendered archive/workshop HTML pages it exists to
keep crawlable never carry it.

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

- workshop create, update, and delete (`AdminWorkshopsHandler`'s `Create`,
  `Patch`, and `Delete` — publish and cancel are status changes made
  through `Patch`; `#0051`'s original trigger),
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

## Server-rendered fallback content (`#0519`)

Bing's first crawl of the home page reported "H1 tag missing" — accurately:
the server's HTML for every route was `<div id="app"></div>`, empty until
the SPA bundle ran. `#0019`'s meta injection above solved this for `<title>`
and Open Graph tags, but never touched the visible body, so any client that
does not execute JavaScript (a crawler with limited rendering, a
link-preview fetcher, Bing's on-page check) saw a page with no heading, no
text, and no links.

**Decision: server fallback content inside `#app`, not Svelte SSR or
prerendering.** SSR/prerendering was ruled out for four reasons specific to
this codebase: there is no JavaScript runtime at request time (the box has
neither Node nor, per `#0509`, a Go toolchain — see `CLAUDE.md` §7), the
views are not hydration-shaped (`Home.svelte` draws a canvas in `onMount`,
every data view fetches in `onMount`, `App.svelte` shows a `Loading…`
placeholder until session-check resolves), the release pipeline would need
a second build target, and — the deciding factor — `#0019`'s token
substitution, cache, and `Invalidate()` path already do exactly the work
this needs.

**Shape.** `web/index.html` (and the `web/dist/index.html` placeholder)
gained one more token, `tokenBody` (`%%OC_BODY%%`), substituted inside
`<div id="app">…</div>` the same way the `<head>` tokens are substituted —
unescaped, like `tokenJSONLD`, because the value is already a complete,
contextually-escaped HTML fragment (see below), not raw text. For a route
with content, the substituted value is a `<header>` with the site nav, then
a `<main id="main-content">` holding one `<h1>` and the route's own body.
For every other route (private/token-bearing routes, an unknown path, a
draft workshop, a withheld/unsent archive entry) the substituted value is
`""`, so the served HTML is byte-for-byte what it was before this issue.

**Never coexists with the SPA.** Svelte 5's `mount()` *appends* to its
target rather than replacing it, so the server's fallback nodes would
duplicate once Svelte mounts, unless cleared first. `web/src/lib/mountTarget.ts`'s
`prepareMountTarget()` empties `#app` synchronously, immediately before
`web/src/main.ts` calls `mount()` — the server's nodes are gone before
Svelte creates its first one, so the fallback and the real view never share
the DOM or the accessibility tree at any point, and a screen reader can
never encounter either one twice. The fallback's `<h1>` deliberately
carries no `tabindex` — `App.svelte`'s `#0238` focus effect and `#0517`'s
styling both key off `h1[tabindex="-1"]`, so the fallback heading can never
become a spurious focus target even if it somehow survived past mount.

**Where the content lives:** `internal/seo/fallback.go`. `staticPageCopy`
holds the `/`, `/about`, `/privacy`, and `/subscribe` copy — a deliberate
first-slice cut for `/about` and `/privacy` (the intro paragraph and a
couple of sections, not the whole page) — plus the `/workshops` and
`/archive` index headings/ledes/empty-state strings, all copied verbatim
from the corresponding `.svelte` view with `{APP_NAME}` expanded and inline
`<strong>` dropped. `/workshops` and `/archive` build their lists from the
same `WorkshopSource`/`ArchiveSource` this package already reads for meta
tags, mirroring `internal/workshops/store.go`'s `ListVisible` exactly for
filtering and order. A workshop or archive detail page's body goes through
`mailing.RenderMarkdownHTML` — the *same* call the JSON API makes
(`renderWorkshopBodyHTML`, `PublicArchiveHandler.GetBySlug`), so the two can
never diverge. One `html/template` set (`pageTemplate`, parsed once at
package init) produces every page; every substituted value goes through its
contextual escaping (text, or a URL inside an `href="…"`), except the
rendered Markdown body, which is already-sanitized HTML from goldmark's
safe mode — the sole `template.HTML` conversion in the file.

**A source error never blanks the heading.** `renderPage` returns
`cacheable=false` when a `WorkshopSource`/`ArchiveSource` read or a
`RenderMarkdownHTML` call fails; the `<h1>`, nav, and (for an index) empty
list still render, and `Render` (`seo.go`) skips storing that degraded body
so the very next request — still within the TTL — sees a recovered source's
real content, with no `Invalidate` call needed.

**Caching and invalidation need no new mechanism.** The fallback lives
inside the same cached rendered bytes the meta tags do, so `Renderer.Invalidate`
clears it on the exact call that already clears the sitemap. A withheld
`/archive/{slug}` (or an unpublished workshop slug) drops its body
immediately, without waiting for `Invalidate`: `resolve` re-runs on every
request, and once the entry no longer qualifies, the request moves to the
shared fallback bucket, whose page content is the zero value. An *index*
page (`/workshops`, `/archive`) is cached under its one static key, so a
mutation there needs the ordinary `Invalidate` call (or the 60s TTL) to be
reflected — the same bound the sitemap already has.

**Scope: one slice.** This covers acceptance criteria 1–7 and 9 of `#0519`
— one `<h1>` per public route, full archive/workshop text, plain nav links,
nothing on private routes, escaping, caching, invalidation. Full static copy
for `/about`/`/privacy`, removing the `Loading…` flash for public routes,
410/404 status codes for a withheld/unknown archive or workshop page,
Home's "Next up" list, and workshop `signup_url` in raw HTML are deliberate
follow-ups, filed separately rather than folded into this slice.

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

## PRD §7.4's correction

`PRD.md` §7.4 used to describe the pre-implementation plan almost verbatim,
unchanged since before the subsystem was built. As of `#0422` (2026-09-04)
it no longer does. For the historical record, the six divergences that
existed until then, all re-derived against the code in that pass rather
than assumed from this file:

- it omitted `/archive/{slug}` and `ArchiveSource` entirely (`#0123`);
- it omitted `twitter:title`/`twitter:description` and the JSON-LD token
  (`#0055`, `#0273`);
- it said the cache is keyed "per path" — that was the design `#0073`
  found and fixed; the real cache is keyed by resolved bucket, specifically
  to avoid unbounded growth from distinct nonexistent paths;
- it said invalidation happens "on workshop mutation" only — `#0319`
  widened that to the admin archive toggle and the send worker's
  archive-publish transition;
- it didn't mention `#0273`'s per-workshop card generation, its route, or
  its `og:type` decision at all; and
- it listed `GET /favicon.svg` as something this subsystem serves, which
  (see `## Sitemap and robots.txt` above) it doesn't.

§7.4 is now a short, accurate summary of the same facts this file covers in
full — it can drift again as the subsystem changes further, so treat this
file as the fuller reference and re-verify both against `internal/seo`
itself rather than against each other.
