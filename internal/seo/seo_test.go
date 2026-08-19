package seo

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// testTemplate is a minimal stand-in for the built index.html: it carries
// every %%OC_*%% token seo.go substitutes, plus enough surrounding markup to
// make "did the wrong thing leak into the wrong place" bugs visible (e.g. a
// token substituted into the wrong tag).
const testTemplate = `<!doctype html>
<html><head>
<title>%%OC_TITLE%%</title>
<meta name="description" content="%%OC_DESCRIPTION%%" />
<meta property="og:title" content="%%OC_OG_TITLE%%" />
<meta property="og:description" content="%%OC_OG_DESCRIPTION%%" />
<meta property="og:image" content="%%OC_OG_IMAGE%%" />
<meta property="og:url" content="%%OC_OG_URL%%" />
<meta property="og:type" content="%%OC_OG_TYPE%%" />
<meta name="twitter:card" content="%%OC_TWITTER_CARD%%" />
</head><body><div id="app"></div></body></html>`

const testBaseURL = "https://www.opencircuitsf.com"

func newTestRenderer(source WorkshopSource) *Renderer {
	return NewRenderer([]byte(testTemplate), testBaseURL, source)
}

// --- Route matching -------------------------------------------------------

// TestRender_StaticRoutesGetDistinctTitles is #0019's headline acceptance
// criterion: the compiled-in route table supplies metadata for /, /about,
// /workshops, /subscribe, and the four must be genuinely different from each
// other -- a bug that served the same title for every route would still pass
// a test that only checked "some title is present".
func TestRender_StaticRoutesGetDistinctTitles(t *testing.T) {
	r := newTestRenderer(nil)
	paths := []string{"/", "/about", "/workshops", "/subscribe"}
	titles := make(map[string]bool)
	for _, p := range paths {
		body := string(r.Render(p))
		title := extractTag(t, body, "title")
		if titles[title] {
			t.Errorf("path %q produced a title already used by another route: %q", p, title)
		}
		titles[title] = true

		if !strings.Contains(body, `og:url" content="`+testBaseURL+p+`"`) {
			t.Errorf("path %q: og:url does not reference the requesting path; body=%s", p, body)
		}
	}
	if len(titles) != len(paths) {
		t.Fatalf("got %d distinct titles for %d routes", len(titles), len(paths))
	}
}

// TestRender_UnknownPathGetsDistinctNotFoundDefault is the criterion carried
// in from #0022's review: the 404 route gets its OWN default title/
// description, not a copy of the site default and not one of the marketing
// routes' titles.
func TestRender_UnknownPathGetsDistinctNotFoundDefault(t *testing.T) {
	r := newTestRenderer(nil)

	homeTitle := extractTag(t, string(r.Render("/")), "title")
	notFoundBody := string(r.Render("/this-page-does-not-exist"))
	notFoundTitle := extractTag(t, notFoundBody, "title")

	if notFoundTitle == homeTitle {
		t.Errorf("404 default title equals the home route's title (%q) -- #0022's carried-in criterion requires a distinct default", notFoundTitle)
	}
	if !strings.Contains(notFoundTitle, "Not Found") {
		t.Errorf("404 title = %q, want it to say the page was not found", notFoundTitle)
	}
	if !strings.Contains(notFoundBody, testBaseURL+"/og-default.png") {
		t.Error("404 default should still reference the default OG image (#0021)")
	}
}

// TestRender_KnownNonMarketingRouteGetsFallback confirms an auth/account
// route that IS known (handlers.IsKnownRoute) but has no dedicated entry --
// e.g. /admin -- gets the generic fallback, not the 404 default. This is the
// property that would silently break if metaFor's known-route branch and
// its not-found branch were ever swapped.
func TestRender_KnownNonMarketingRouteGetsFallback(t *testing.T) {
	r := newTestRenderer(nil)
	body := string(r.Render("/admin"))
	title := extractTag(t, body, "title")
	if strings.Contains(title, "Not Found") {
		t.Errorf("/admin (a known route) got the 404 default title %q", title)
	}
}

// TestRender_WorkshopDetailUsesStoreData confirms /workshops/{slug} looks
// the workshop up and uses its title, summary, and cover image, per #0019's
// acceptance criterion "wired in #0054" -- this is the plumbing that
// criterion depends on, exercised here with a fake source since the real
// store doesn't exist yet (#0051).
func TestRender_WorkshopDetailUsesStoreData(t *testing.T) {
	source := fakeWorkshopSource{
		"solder-101": {
			Slug: "solder-101", Title: "Intro to Soldering",
			Summary:    "Learn the basics of through-hole soldering.",
			CoverImage: "/workshops/solder-101/cover.jpg", Status: WorkshopPublished,
		},
	}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/solder-101"))

	if !strings.Contains(body, "Intro to Soldering") {
		t.Errorf("workshop title not found in rendered body: %s", body)
	}
	if !strings.Contains(body, "Learn the basics of through-hole soldering.") {
		t.Errorf("workshop summary not found in rendered body: %s", body)
	}
	if !strings.Contains(body, testBaseURL+"/workshops/solder-101/cover.jpg") {
		t.Errorf("workshop cover image not resolved to an absolute URL: %s", body)
	}
}

// TestRender_WorkshopDetailUnknownSlugFallsBack confirms a slug with no
// matching (or non-published) workshop still renders successfully with the
// generic fallback rather than erroring or leaking a draft's data.
func TestRender_WorkshopDetailUnknownSlugFallsBack(t *testing.T) {
	source := fakeWorkshopSource{
		"secret-draft": {Slug: "secret-draft", Title: "Unreleased Workshop", Status: WorkshopDraft},
	}
	r := newTestRenderer(source)

	for _, slug := range []string{"does-not-exist", "secret-draft"} {
		body := string(r.Render("/workshops/" + slug))
		if strings.Contains(body, "Unreleased Workshop") {
			t.Errorf("slug %q: draft workshop title leaked into rendered body: %s", slug, body)
		}
	}
}

// --- Escaping ---------------------------------------------------------

// TestRender_EscapesInjectedValues is the genuine injection-surface test:
// a workshop title containing HTML-significant characters must come out
// escaped in the served markup, not verbatim -- otherwise a workshop titled
// e.g. `"><script>alert(1)</script>` breaks out of the meta tag's attribute
// and the crawler (or a browser rendering a cached copy) executes it.
func TestRender_EscapesInjectedValues(t *testing.T) {
	malicious := `Solder & Circuits <script>alert(1)</script> "quoted"`
	source := fakeWorkshopSource{
		"xss-test": {
			Slug: "xss-test", Title: malicious, Summary: malicious,
			Status: WorkshopPublished,
		},
	}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/xss-test"))

	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("unescaped <script> tag leaked into rendered HTML: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped &lt;script&gt; in body, got: %s", body)
	}
	if !strings.Contains(body, "&amp;") {
		t.Errorf("expected escaped &amp; for the literal & in the title, got: %s", body)
	}
	if !strings.Contains(body, "&#34;quoted&#34;") {
		t.Errorf("expected escaped double quotes, got: %s", body)
	}
}

// TestRender_AllTokensSubstituted confirms no %%OC_*%% placeholder token
// survives into the served HTML for any route -- an unsubstituted token
// would mean a crawler receives literal template syntax instead of content.
func TestRender_AllTokensSubstituted(t *testing.T) {
	r := newTestRenderer(nil)
	tokens := []string{
		tokenTitle, tokenDescription, tokenOGTitle, tokenOGDescription,
		tokenOGImage, tokenOGURL, tokenOGType, tokenTwitterCard,
	}
	for _, path := range []string{"/", "/about", "/workshops", "/subscribe", "/nonexistent", "/workshops/some-slug"} {
		body := string(r.Render(path))
		for _, tok := range tokens {
			if strings.Contains(body, tok) {
				t.Errorf("path %q: unsubstituted token %q leaked into rendered body", path, tok)
			}
		}
	}
}

// --- Cache + invalidation ---------------------------------------------

// TestRender_CachesWithinTTL proves the cache actually intercepts a second
// request within the TTL: a source that returns different data on every call
// would show a different title on the second Render if the cache weren't
// serving the first render's bytes.
func TestRender_CachesWithinTTL(t *testing.T) {
	source := &mutatingWorkshopSource{title: "Original Title"}
	r := newTestRenderer(source)
	clock := &fakeClock{t: time.Unix(0, 0)}
	r.now = clock.now

	first := string(r.Render("/workshops/mutable"))
	source.title = "Changed Title"
	clock.advance(1 * time.Second) // well within the 60s TTL
	second := string(r.Render("/workshops/mutable"))

	if !strings.Contains(first, "Original Title") {
		t.Fatalf("first render missing expected title: %s", first)
	}
	if strings.Contains(second, "Changed Title") {
		t.Error("cache did not hold within the TTL: second render picked up the source mutation")
	}
	if !strings.Contains(second, "Original Title") {
		t.Error("second render (still within TTL) should have served the cached first render verbatim")
	}
}

// TestRender_CacheExpiresAfterTTL confirms the OTHER side of the cache
// contract: once the TTL has elapsed, a stale cached render must NOT keep
// being served -- this is what makes InvalidateWorkshops' "short TTL"
// promise real rather than an indefinite cache with a misleading name.
func TestRender_CacheExpiresAfterTTL(t *testing.T) {
	source := &mutatingWorkshopSource{title: "Original Title"}
	r := newTestRenderer(source)
	clock := &fakeClock{t: time.Unix(0, 0)}
	r.now = clock.now

	_ = r.Render("/workshops/mutable")
	source.title = "Changed Title"
	clock.advance(r.ttl + time.Second) // past the TTL
	after := string(r.Render("/workshops/mutable"))

	if !strings.Contains(after, "Changed Title") {
		t.Errorf("expected the TTL-expired render to pick up the source mutation, got: %s", after)
	}
}

// TestRender_InvalidateWorkshopsBypassesCacheImmediately is the mutation
// hook itself: even well within the TTL, calling InvalidateWorkshops must
// force the next Render to reflect a source change immediately, without
// waiting out the TTL. This is the exact behavior workshop create/update/
// publish/cancel (#0051) depends on.
func TestRender_InvalidateWorkshopsBypassesCacheImmediately(t *testing.T) {
	source := &mutatingWorkshopSource{title: "Original Title"}
	r := newTestRenderer(source)
	clock := &fakeClock{t: time.Unix(0, 0)}
	r.now = clock.now

	_ = r.Render("/workshops/mutable")
	source.title = "Changed Title"
	clock.advance(1 * time.Second) // still well within the TTL

	r.InvalidateWorkshops()
	after := string(r.Render("/workshops/mutable"))

	if !strings.Contains(after, "Changed Title") {
		t.Errorf("InvalidateWorkshops did not force a fresh render within the TTL window; got: %s", after)
	}
}

// TestRender_UnknownPathsShareOneCacheEntry is #0073's headline fix: every
// unknown path renders the byte-identical not-found body, so caching them
// per raw path (the original defect -- 25,000 distinct unauthenticated
// requests measured RSS growing from ~59 MB to ~212 MB) is pure waste on
// top of being unbounded. After rendering many distinct unknown paths, the
// cache must hold exactly one entry for all of them.
func TestRender_UnknownPathsShareOneCacheEntry(t *testing.T) {
	r := newTestRenderer(nil)
	for i := 0; i < 5000; i++ {
		r.Render(fmt.Sprintf("/does-not-exist-%d", i))
	}
	r.mu.RLock()
	got := len(r.cache)
	r.mu.RUnlock()
	if got != 1 {
		t.Errorf("cache has %d entries after 5000 distinct unknown paths, want 1 (all should collapse to the not-found bucket)", got)
	}
}

// TestRender_CacheBoundedRegardlessOfKeying is the other half of #0073's
// acceptance criteria: bucket-keying alone isn't the only thing preventing
// unbounded growth -- maxCacheEntries must hold even if a future key scheme
// re-derives one key per request (exactly what the original per-path keying
// did). This drives Renderer.store directly with many distinct synthetic
// keys -- simulating what a naive future re-keying would produce -- rather
// than through Render/resolve, so it exercises the bound mechanism
// independent of the bucket-collapsing behavior TestRender_UnknownPathsShare
// OneCacheEntry already covers.
func TestRender_CacheBoundedRegardlessOfKeying(t *testing.T) {
	r := newTestRenderer(nil)
	for i := 0; i < 5000; i++ {
		r.store(fmt.Sprintf("synthetic-key-%d", i), []byte("body"))
	}
	r.mu.RLock()
	got := len(r.cache)
	r.mu.RUnlock()
	if got > maxCacheEntries {
		t.Errorf("cache has %d entries after 5000 distinct writes, want <= %d (maxCacheEntries)", got, maxCacheEntries)
	}
}

// --- test helpers -------------------------------------------------------

// extractTag returns the text content of the first <tag>...</tag> in html,
// failing the test if it isn't found.
func extractTag(t *testing.T, html, tag string) string {
	t.Helper()
	open := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	start := strings.Index(html, open)
	if start == -1 {
		t.Fatalf("no <%s> found in: %s", tag, html)
	}
	start += len(open)
	end := strings.Index(html[start:], closeTag)
	if end == -1 {
		t.Fatalf("no closing </%s> found in: %s", tag, html)
	}
	return html[start : start+end]
}

type fakeWorkshopSource map[string]Workshop

func (f fakeWorkshopSource) WorkshopBySlug(slug string) (Workshop, bool, error) {
	w, ok := f[slug]
	return w, ok, nil
}

func (f fakeWorkshopSource) Workshops() ([]Workshop, error) {
	out := make([]Workshop, 0, len(f))
	for _, w := range f {
		out = append(out, w)
	}
	return out, nil
}

// mutatingWorkshopSource always has exactly one published workshop
// ("mutable"), whose title and UpdatedAt can be changed mid-test to prove
// cache behavior -- title for the renderer's meta tags, updatedAt for the
// sitemap's <lastmod>, since the sitemap doesn't surface Title at all.
type mutatingWorkshopSource struct {
	title     string
	updatedAt string
}

func (m *mutatingWorkshopSource) WorkshopBySlug(slug string) (Workshop, bool, error) {
	if slug != "mutable" {
		return Workshop{}, false, nil
	}
	return Workshop{Slug: "mutable", Title: m.title, Summary: "s", Status: WorkshopPublished, UpdatedAt: m.updatedAt}, true, nil
}

func (m *mutatingWorkshopSource) Workshops() ([]Workshop, error) {
	w, _, _ := m.WorkshopBySlug("mutable")
	return []Workshop{w}, nil
}

// fakeClock lets tests advance seo's injected `now` deterministically
// instead of sleeping past a real TTL.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }
