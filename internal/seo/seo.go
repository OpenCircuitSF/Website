// Package seo server-injects per-route <title>, description, and Open
// Graph / Twitter Card tags into the built SPA's index.html before it is
// served, plus generates sitemap.xml and robots.txt (#0019, #0020).
//
// Social crawlers (Discord, Slack, iMessage, X, Facebook, LinkedIn) fetch a
// URL's raw HTML and never execute JavaScript, so a plain single-page app
// gives every shared link the same generic preview card. PRD §3.4 calls this
// "the single most important technical detail for a site whose whole job is
// receiving social traffic."
//
// Design (PRD §7.4):
//  1. Hold the built index.html in memory with placeholder markers (see
//     web/index.html's %%OC_*%% tokens) for title, description, og:title,
//     og:description, og:image, og:url, og:type, and twitter:card.
//  2. On each request, match the path against a route table: a compiled-in
//     table for the static marketing routes, the workshop store for
//     /workshops/{slug} (nil until #0051/#0054 land -- falls back to the
//     generic default), and a distinct default for unknown paths (404).
//  3. Substitute and serve. Every substituted value is HTML-escaped.
//  4. Cache the rendered bytes per resolved metadata bucket (not per raw
//     path -- #0073) with a short TTL and a bounded entry count; invalidate
//     on workshop mutation (Site.InvalidateWorkshops).
//
// internal/handlers/routes.go remains the single source of truth for "is
// this path real" (IsKnownRoute) -- this package imports it rather than
// re-deriving its own route table, per both issues' Notes.
package seo

import (
	"html"
	"strings"
	"sync"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
)

// Placeholder tokens substituted into the built index.html. Must match the
// literal %%OC_*%% markers in web/index.html exactly -- a mismatch here means
// a token is left unsubstituted in the served HTML rather than failing loudly,
// which is why seo_test.go asserts every token is gone from rendered output.
const (
	tokenTitle         = "%%OC_TITLE%%"
	tokenDescription   = "%%OC_DESCRIPTION%%"
	tokenOGTitle       = "%%OC_OG_TITLE%%"
	tokenOGDescription = "%%OC_OG_DESCRIPTION%%"
	tokenOGImage       = "%%OC_OG_IMAGE%%"
	tokenOGURL         = "%%OC_OG_URL%%"
	tokenOGType        = "%%OC_OG_TYPE%%"
	tokenTwitterCard   = "%%OC_TWITTER_CARD%%"
)

// defaultCacheTTL is the short TTL PRD §7.4 calls for on the rendered
// index.html cache. Short enough that a workshop edit made without going
// through Site.InvalidateWorkshops (a manual DB fix, say) is still visible
// within a minute; long enough that a burst of crawler/bot traffic against
// one path doesn't re-render on every request.
const defaultCacheTTL = 60 * time.Second

// maxCacheEntries bounds Renderer.cache regardless of what keying scheme
// populates it -- defense in depth against a future re-keying reintroducing
// unbounded growth, which is exactly the defect #0073 fixed: the cache used
// to be keyed by raw request path, so 25,000 unauthenticated requests to
// distinct unknown paths grew RSS from ~59 MB to ~212 MB (~6 KB retained per
// distinct path, never pruned). Keying by resolved metadata bucket (below)
// already collapses the actual attack surface -- every unknown path shares
// one entry -- but this cap exists so that property is not the only thing
// standing between the cache and unbounded growth.
//
// The realistic keyspace is tiny: the handful of static routes, one fallback
// bucket, one not-found bucket, and one entry per distinct *published*
// workshop slug actually requested (bounded by real catalog size, not by
// how many distinct paths an attacker can invent). 512 is comfortable
// headroom above that and should almost never be reached in practice.
//
// Eviction policy: a write that would grow the cache past the bound flushes
// the entire cache first, then inserts the new entry. A full flush rather
// than a partial (e.g. LRU) eviction mirrors InvalidateWorkshops' existing
// reasoning below: re-rendering is cheap (a string substitution over an
// in-memory template), so a hard cap that occasionally costs one wasted
// generation is simpler to reason about than per-entry recency bookkeeping.
const maxCacheEntries = 512

// Cache keys for the two buckets that aren't an exact static-route path.
// Prefixed with a NUL byte so they can never collide with a real URL path
// (which always starts with "/"), and distinguished from a
// "workshop:{slug}" key by construction.
const (
	cacheKeyFallback = "\x00fallback"
	cacheKeyNotFound = "\x00notfound"
)

// RouteMeta is the set of substituted values for one route.
type RouteMeta struct {
	Title         string
	Description   string
	OGTitle       string
	OGDescription string
	OGImage       string
	OGURL         string
	OGType        string
	TwitterCard   string
}

// Renderer holds the built index.html template in memory and produces a
// per-path rendering of it with RouteMeta substituted in. It is safe for
// concurrent use.
type Renderer struct {
	template []byte
	baseURL  string // canonical origin, no trailing slash, e.g. "https://www.opencircuitsf.com"
	static   map[string]RouteMeta
	notFound RouteMeta
	fallback RouteMeta
	workshop WorkshopSource // nilable -- see WorkshopSource doc

	ttl time.Duration
	now func() time.Time // injectable for deterministic tests

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	body    []byte
	expires time.Time
}

// NewRenderer constructs a Renderer over the given built index.html template
// bytes (web.DistFS()'s index.html in production; an in-memory fixture in
// tests) and the canonical base URL (e.g. cfg.BaseURL, no trailing slash).
// source may be nil -- until #0051/#0054 wire a real workshop store,
// /workshops/{slug} requests fall back to the generic default metadata (still
// served with SPAHandler's own 200, since the path matches the known dynamic
// pattern regardless of whether the slug exists).
func NewRenderer(template []byte, baseURL string, source WorkshopSource) *Renderer {
	baseURL = strings.TrimSuffix(baseURL, "/")
	r := &Renderer{
		template: template,
		baseURL:  baseURL,
		workshop: source,
		ttl:      defaultCacheTTL,
		now:      time.Now,
		cache:    make(map[string]cacheEntry),
	}
	r.static = defaultStaticRouteMeta(baseURL)
	r.fallback = defaultFallbackMeta(baseURL)
	r.notFound = defaultNotFoundMeta(baseURL)
	return r
}

// defaultStaticRouteMeta builds the compiled-in route table required by
// #0019's acceptance criteria: distinct metadata for /, /about, /workshops,
// and /subscribe. /privacy was added at #0070 for the same reason /about was
// added at #0019: a route worth sharing gets its own title/description
// rather than the generic fallback below.
func defaultStaticRouteMeta(baseURL string) map[string]RouteMeta {
	image := baseURL + "/og-default.png"
	return map[string]RouteMeta{
		"/": {
			Title:         "Open Circuit SF — Hands-on electronics workshops in San Francisco",
			Description:   "A hands-on electronics workshop and community space in San Francisco. Solder, build, and learn at in-person workshops.",
			OGTitle:       "Open Circuit SF",
			OGDescription: "A hands-on electronics workshop and community space in San Francisco.",
			OGImage:       image,
			OGURL:         baseURL + "/",
			OGType:        "website",
			TwitterCard:   "summary_large_image",
		},
		"/about": {
			Title:         "About — Open Circuit SF",
			Description:   "What Open Circuit SF is, who runs it, and why it exists: a hands-on electronics workshop and community space in San Francisco.",
			OGTitle:       "About Open Circuit SF",
			OGDescription: "What Open Circuit SF is, who runs it, and why it exists.",
			OGImage:       image,
			OGURL:         baseURL + "/about",
			OGType:        "website",
			TwitterCard:   "summary_large_image",
		},
		"/privacy": {
			Title:         "Privacy Policy — Open Circuit SF",
			Description:   "What Open Circuit SF collects when you subscribe (email, interests, signup IP, UTM source), why, how long it's kept, and how to unsubscribe or request erasure.",
			OGTitle:       "Privacy Policy — Open Circuit SF",
			OGDescription: "What Open Circuit SF collects, why, and how to leave.",
			OGImage:       image,
			OGURL:         baseURL + "/privacy",
			OGType:        "website",
			TwitterCard:   "summary_large_image",
		},
		"/workshops": {
			Title:         "Workshops — Open Circuit SF",
			Description:   "Upcoming hands-on electronics workshops at Open Circuit SF: soldering, PCB design, and hardware hacking in San Francisco.",
			OGTitle:       "Workshops at Open Circuit SF",
			OGDescription: "Upcoming hands-on electronics workshops: soldering, PCB design, and hardware hacking.",
			OGImage:       image,
			OGURL:         baseURL + "/workshops",
			OGType:        "website",
			TwitterCard:   "summary_large_image",
		},
		"/subscribe": {
			Title:         "Subscribe — Open Circuit SF",
			Description:   "Get notified about new workshops and events at Open Circuit SF, San Francisco's hands-on electronics workshop space.",
			OGTitle:       "Subscribe to Open Circuit SF",
			OGDescription: "Get notified about new workshops and events.",
			OGImage:       image,
			OGURL:         baseURL + "/subscribe",
			OGType:        "website",
			TwitterCard:   "summary_large_image",
		},
	}
}

// defaultFallbackMeta is used for a path that IS in handlers.IsKnownRoute
// (so SPAHandler serves it with 200) but has no dedicated entry above --
// the Phase 1 auth/account views (/login, /account, /admin, /confirm,
// /preferences, /unsubscribe, /subscribe/thanks, /register/verify,
// /recover/verify). These are never meant to be shared as social previews,
// so a generic card is correct, not a bug.
func defaultFallbackMeta(baseURL string) RouteMeta {
	return RouteMeta{
		Title:         "Open Circuit SF",
		Description:   "A hands-on electronics workshop and community space in San Francisco.",
		OGTitle:       "Open Circuit SF",
		OGDescription: "A hands-on electronics workshop and community space in San Francisco.",
		OGImage:       baseURL + "/og-default.png",
		OGURL:         baseURL + "/",
		OGType:        "website",
		TwitterCard:   "summary_large_image",
	}
}

// defaultNotFoundMeta is the distinct 404 default carried in from #0022's
// review: a real title and description of its own (not a copy of the site
// default), and no expectation of a meaningful OG card beyond the default
// image from #0021 -- a 404 should not be shared as a preview.
func defaultNotFoundMeta(baseURL string) RouteMeta {
	return RouteMeta{
		Title:         "Page Not Found — Open Circuit SF",
		Description:   "The page you're looking for doesn't exist or may have moved.",
		OGTitle:       "Page Not Found",
		OGDescription: "The page you're looking for doesn't exist or may have moved.",
		OGImage:       baseURL + "/og-default.png",
		OGURL:         baseURL + "/",
		OGType:        "website",
		TwitterCard:   "summary",
	}
}

// resolve determines both the RouteMeta for a request path AND the cache
// bucket key its rendered bytes belong under, in priority order: (1) an
// exact static-route entry, keyed by the path itself -- bounded to len(r.static)
// entries, (2) a workshop-detail match resolved against the workshop source,
// keyed by slug -- bounded by the number of distinct published workshops
// actually requested, not by how many distinct paths a client can invent,
// (3) the known-but-uncataloged fallback, which is byte-identical for every
// route that reaches it and therefore shares ONE cache key regardless of
// which such route was requested, or (4) the not-found default when
// handlers.IsKnownRoute rejects the path -- also byte-identical across every
// unknown path, so it too shares one cache key. This is the fix for #0073:
// caching by raw request path let an unauthenticated client grow the cache
// without bound by requesting distinct nonexistent paths, even though every
// one of them renders the identical not-found body.
//
// normalizedPath is the path AFTER trailing-slash normalization, matching
// handlers.IsKnownRoute's own normalization so the two never disagree about
// which bucket a path falls into.
func (r *Renderer) resolve(normalizedPath string) (cacheKey string, meta RouteMeta) {
	if m, ok := r.static[normalizedPath]; ok {
		return normalizedPath, m
	}
	if slug, ok := handlers.WorkshopDetailSlug(normalizedPath); ok {
		if m, ok := r.workshopMeta(slug); ok {
			return "workshop:" + slug, m
		}
		// Matches the dynamic pattern but no published workshop found (not
		// yet wired, wrong slug, or a draft/canceled workshop) -- still a
		// "known" path per handlers.IsKnownRoute (SPAHandler serves 200), so
		// it gets the generic fallback rather than the 404 default.
		return cacheKeyFallback, r.fallback
	}
	if handlers.IsKnownRoute(normalizedPath) {
		return cacheKeyFallback, r.fallback
	}
	return cacheKeyNotFound, r.notFound
}

// workshopMeta looks up slug in the configured WorkshopSource and, if found
// and published, builds a RouteMeta from its title/summary/cover image.
func (r *Renderer) workshopMeta(slug string) (RouteMeta, bool) {
	if r.workshop == nil {
		return RouteMeta{}, false
	}
	w, ok, err := r.workshop.WorkshopBySlug(slug)
	if err != nil || !ok || w.Status != WorkshopPublished {
		return RouteMeta{}, false
	}
	image := absoluteURL(r.baseURL, w.CoverImage)
	if image == "" {
		image = r.baseURL + "/og-default.png"
	}
	title := w.Title + " — Open Circuit SF"
	return RouteMeta{
		Title:         title,
		Description:   w.Summary,
		OGTitle:       w.Title,
		OGDescription: w.Summary,
		OGImage:       image,
		OGURL:         r.baseURL + "/workshops/" + slug,
		OGType:        "website",
		TwitterCard:   "summary_large_image",
	}, true
}

// absoluteURL resolves a possibly-relative asset path against baseURL. An
// already-absolute (http/https) URL is returned unchanged; an empty input
// returns "".
func absoluteURL(baseURL, urlOrPath string) string {
	if urlOrPath == "" {
		return ""
	}
	if strings.HasPrefix(urlOrPath, "http://") || strings.HasPrefix(urlOrPath, "https://") {
		return urlOrPath
	}
	if !strings.HasPrefix(urlOrPath, "/") {
		urlOrPath = "/" + urlOrPath
	}
	return baseURL + urlOrPath
}

// normalizePath applies the same trailing-slash normalization as
// handlers.IsKnownRoute (unexported there), so metaFor and the SPAHandler's
// own 200-vs-404 decision are looking at the identical string.
func normalizePath(path string) string {
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		return strings.TrimSuffix(path, "/")
	}
	return path
}

// Render returns the rendered index.html bytes for path, with every
// placeholder token substituted and HTML-escaped, serving from the TTL cache
// when available. It does not decide the HTTP status code -- that remains
// SPAHandler's job (via handlers.IsKnownRoute), preserving #0022's 404
// behavior; Render only ever changes the body.
//
// The cache is keyed by resolve's bucket key, not the raw path (#0073) --
// resolving that key requires the same work as resolving the meta itself
// (including a WorkshopSource lookup for a /workshops/{slug} request), so
// unlike the previous per-path cache, a repeat request for a workshop-detail
// page no longer skips that lookup on a cache hit. That trades one bounded,
// indexed store read per request for eliminating the unbounded per-path
// memory growth; the substitution/escaping work itself is still cached and
// skipped on a hit.
func (r *Renderer) Render(path string) []byte {
	normalized := normalizePath(path)
	key, meta := r.resolve(normalized)

	r.mu.RLock()
	entry, ok := r.cache[key]
	r.mu.RUnlock()
	if ok && r.now().Before(entry.expires) {
		return entry.body
	}

	body := r.substitute(meta)
	r.store(key, body)
	return body
}

// store writes body into the cache under key, enforcing maxCacheEntries: a
// write that would grow the cache past the bound flushes it entirely first.
// See maxCacheEntries' doc comment for the eviction policy's rationale.
func (r *Renderer) store(key string, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.cache[key]; !exists && len(r.cache) >= maxCacheEntries {
		r.cache = make(map[string]cacheEntry)
	}
	r.cache[key] = cacheEntry{body: body, expires: r.now().Add(r.ttl)}
}

// substitute performs the actual token replacement. Every value is passed
// through html.EscapeString -- workshop titles/summaries flow in from
// user-controlled admin input (#0051), so this is a genuine injection
// surface once <meta content="..."> receives them, not a defensive
// nicety. html.EscapeString escapes &, <, >, ' and ", which covers both a
// double-quoted attribute value and <title> text content.
func (r *Renderer) substitute(m RouteMeta) []byte {
	replacer := strings.NewReplacer(
		tokenTitle, html.EscapeString(m.Title),
		tokenDescription, html.EscapeString(m.Description),
		tokenOGTitle, html.EscapeString(m.OGTitle),
		tokenOGDescription, html.EscapeString(m.OGDescription),
		tokenOGImage, html.EscapeString(m.OGImage),
		tokenOGURL, html.EscapeString(m.OGURL),
		tokenOGType, html.EscapeString(m.OGType),
		tokenTwitterCard, html.EscapeString(m.TwitterCard),
	)
	return []byte(replacer.Replace(string(r.template)))
}

// InvalidateWorkshops clears every cached rendering. Called on workshop
// mutation (create/update/publish/cancel, #0051) so a stale title or cover
// image doesn't linger for up to the TTL. Clearing the whole cache rather
// than just workshop-detail entries is deliberate: it is cheap (re-render is
// just a string substitution over an in-memory template, no I/O) and simpler
// to reason about than tracking which cached paths are workshop-derived.
func (r *Renderer) InvalidateWorkshops() {
	r.mu.Lock()
	r.cache = make(map[string]cacheEntry)
	r.mu.Unlock()
}
