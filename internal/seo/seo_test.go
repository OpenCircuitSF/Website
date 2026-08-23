package seo

import (
	"encoding/json"
	"fmt"
	"os"
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
%%OC_JSONLD%%
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
	paths := []string{"/", "/about", "/privacy", "/workshops", "/subscribe"}
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
			CoverImage: "/workshops/solder-101/cover.jpg", Status: WorkshopPublished, Published: true,
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

// --- JSON-LD Event structured data (#0055) ---------------------------

// fullWorkshop is a workshop with every field #0055's Event JSON-LD needs
// set, reused as a base by several tests below via a copy-and-tweak.
// fullWorkshop builds a workshop that HAS been published at some point
// (Published: true) -- the common case every existing caller wants,
// including a "published" one that later became WorkshopCanceled (mirroring
// #0051's "published -> canceled preserves published_at" rule). Callers
// exercising #0171's actual defect -- a canceled workshop that was NEVER
// published -- override Published: false explicitly on the returned value,
// so that case can never pass by accident.
func fullWorkshop(slug string, status WorkshopStatus) Workshop {
	return Workshop{
		Slug:            slug,
		Title:           "Intro to Soldering",
		Summary:         "Learn the basics of through-hole soldering.",
		CoverImage:      "/workshops/" + slug + "/cover.jpg",
		Status:          status,
		Published:       true,
		StartsAt:        "2026-09-12T18:00:00Z",
		EndsAt:          "2026-09-12T20:00:00Z",
		LocationName:    "Open Circuit SF",
		LocationAddress: "123 Maker Way, San Francisco, CA",
	}
}

// extractJSONLD finds the `<script type="application/ld+json">...</script>`
// block in body, parses its contents as JSON, and returns the decoded
// object -- failing the test if the block is missing or isn't valid JSON.
// Used instead of substring checks so tests actually prove the emitted
// block is a syntactically valid document, not just that some expected
// words appear somewhere in the response body.
func extractJSONLD(t *testing.T, body string) map[string]any {
	t.Helper()
	const open = `<script type="application/ld+json">`
	const closeTag = `</script>`
	start := strings.Index(body, open)
	if start == -1 {
		t.Fatalf("no JSON-LD <script> block found in body: %s", body)
	}
	start += len(open)
	end := strings.Index(body[start:], closeTag)
	if end == -1 {
		t.Fatalf("JSON-LD <script> block has no closing tag: %s", body)
	}
	raw := body[start : start+end]
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("JSON-LD block did not parse as JSON: %v\nraw: %s", err, raw)
	}
	return decoded
}

// assertNoJSONLD fails the test if body contains a JSON-LD script block at
// all -- the negative counterpart to extractJSONLD, for routes/workshops
// #0055 says must not get one.
func assertNoJSONLD(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, `application/ld+json`) {
		t.Errorf("unexpected JSON-LD block in body: %s", body)
	}
}

// TestRender_JSONLD_PublishedWorkshopHasFullEvent is #0055's headline
// acceptance criterion: a published workshop with a date and a location gets
// a structurally valid schema.org Event carrying every required field.
func TestRender_JSONLD_PublishedWorkshopHasFullEvent(t *testing.T) {
	source := fakeWorkshopSource{"solder-101": fullWorkshop("solder-101", WorkshopPublished)}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/solder-101"))

	ev := extractJSONLD(t, body)

	if ev["@context"] != "https://schema.org" {
		t.Errorf("@context = %v, want https://schema.org", ev["@context"])
	}
	if ev["@type"] != "Event" {
		t.Errorf("@type = %v, want Event", ev["@type"])
	}
	if ev["name"] != "Intro to Soldering" {
		t.Errorf("name = %v, want the workshop title", ev["name"])
	}
	if ev["startDate"] != "2026-09-12T18:00:00Z" {
		t.Errorf("startDate = %v, want an ISO 8601 timestamp with an offset", ev["startDate"])
	}
	if ev["endDate"] != "2026-09-12T20:00:00Z" {
		t.Errorf("endDate = %v", ev["endDate"])
	}
	if ev["eventStatus"] != "https://schema.org/EventScheduled" {
		t.Errorf("eventStatus = %v, want EventScheduled for a published workshop", ev["eventStatus"])
	}
	if ev["eventAttendanceMode"] != "https://schema.org/OfflineEventAttendanceMode" {
		t.Errorf("eventAttendanceMode = %v", ev["eventAttendanceMode"])
	}
	loc, ok := ev["location"].(map[string]any)
	if !ok {
		t.Fatalf("location is not an object: %v", ev["location"])
	}
	if loc["@type"] != "Place" {
		t.Errorf("location.@type = %v, want Place", loc["@type"])
	}
	if loc["name"] != "Open Circuit SF" {
		t.Errorf("location.name = %v", loc["name"])
	}
	if loc["address"] != "123 Maker Way, San Francisco, CA" {
		t.Errorf("location.address = %v", loc["address"])
	}
	if ev["description"] != "Learn the basics of through-hole soldering." {
		t.Errorf("description = %v", ev["description"])
	}
	org, ok := ev["organizer"].(map[string]any)
	if !ok {
		t.Fatalf("organizer is not an object: %v", ev["organizer"])
	}
	if org["@type"] != "Organization" || org["name"] != "Open Circuit SF" {
		t.Errorf("organizer = %v, want Open Circuit SF -- no venue named as organizer", org)
	}
	if org["url"] != testBaseURL+"/" {
		t.Errorf("organizer.url = %v, want the site URL", org["url"])
	}
	if ev["image"] != testBaseURL+"/workshops/solder-101/cover.jpg" {
		t.Errorf("image = %v, want an absolute URL", ev["image"])
	}
	if ev["url"] != testBaseURL+"/workshops/solder-101" {
		t.Errorf("url = %v, want the canonical workshop URL", ev["url"])
	}
}

// TestRender_JSONLD_CanceledWorkshopEmitsEventCancelled is #0055's binding
// ruling on a canceled workshop: it still gets an Event block, carrying its
// OWN real name/dates (not the generic fallback), with eventStatus set to
// EventCancelled -- distinct from a workshop simply not existing. This is a
// deliberate divergence from the <title>/og:* tags, which stay on the
// generic fallback for a canceled workshop per #0135's review ruling; that
// half is asserted separately below.
func TestRender_JSONLD_CanceledWorkshopEmitsEventCancelled(t *testing.T) {
	source := fakeWorkshopSource{"solder-101": fullWorkshop("solder-101", WorkshopCanceled)}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/solder-101"))

	ev := extractJSONLD(t, body)
	if ev["eventStatus"] != "https://schema.org/EventCancelled" {
		t.Errorf("eventStatus = %v, want EventCancelled for a canceled workshop", ev["eventStatus"])
	}
	if ev["name"] != "Intro to Soldering" {
		t.Errorf("name = %v, want the canceled workshop's real title in the structured data", ev["name"])
	}
}

// TestRender_JSONLD_CanceledWorkshopKeepsGenericSocialCard is the other half
// of the ruling above: #0135's review left internal/seo's <title>/og:* fields
// for a canceled workshop on the generic site fallback, and #0055 must not
// change that -- only the JSON-LD block gets the workshop's real identity.
func TestRender_JSONLD_CanceledWorkshopKeepsGenericSocialCard(t *testing.T) {
	source := fakeWorkshopSource{"solder-101": fullWorkshop("solder-101", WorkshopCanceled)}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/solder-101"))

	title := extractTag(t, body, "title")
	if strings.Contains(title, "Intro to Soldering") {
		t.Errorf("canceled workshop's <title> = %q, want the generic site fallback (unchanged from #0135's ruling)", title)
	}
}

// TestRender_JSONLD_NeverPublishedCanceledOmitsBlockAndUsesGenericCard is
// #0171's own regression test on the SEO renderer: a workshop canceled
// directly from draft (Status=canceled, Published=false -- draft ->
// canceled never stamps published_at) must be treated exactly like a draft
// or an unknown slug -- no per-workshop JSON-LD Event, and the generic
// fallback <title>, never the workshop's real one. Before this issue,
// workshopRouteMeta's gate checked Status alone, so this exact workshop
// would have produced a real, indexable EventCancelled block for a
// workshop the public was never told existed.
func TestRender_JSONLD_NeverPublishedCanceledOmitsBlockAndUsesGenericCard(t *testing.T) {
	w := fullWorkshop("secret-canceled-draft", WorkshopCanceled)
	w.Published = false
	source := fakeWorkshopSource{"secret-canceled-draft": w}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/secret-canceled-draft"))

	assertNoJSONLD(t, body)

	title := extractTag(t, body, "title")
	if strings.Contains(title, "Intro to Soldering") {
		t.Errorf("never-published canceled workshop's <title> = %q, want the generic site fallback, never its real title", title)
	}
}

// TestRender_JSONLD_MissingStartDateOmitsBlock: a published workshop with no
// date set yet cannot produce a spec-valid startDate, so no block should be
// emitted at all rather than a fabricated or empty date.
func TestRender_JSONLD_MissingStartDateOmitsBlock(t *testing.T) {
	w := fullWorkshop("no-date", WorkshopPublished)
	w.StartsAt = ""
	source := fakeWorkshopSource{"no-date": w}
	r := newTestRenderer(source)
	assertNoJSONLD(t, string(r.Render("/workshops/no-date")))
}

// TestRender_JSONLD_MissingLocationOmitsBlock: a published, dated workshop
// with no venue set yet (LocationName and LocationAddress both empty --
// "Location TBA" in the UI) also cannot produce a spec-valid Event, since
// location is required for a non-virtual event.
func TestRender_JSONLD_MissingLocationOmitsBlock(t *testing.T) {
	w := fullWorkshop("no-venue", WorkshopPublished)
	w.LocationName = ""
	w.LocationAddress = ""
	source := fakeWorkshopSource{"no-venue": w}
	r := newTestRenderer(source)
	assertNoJSONLD(t, string(r.Render("/workshops/no-venue")))
}

// TestRender_JSONLD_OnlyOneOfNameOrAddressStillProducesLocation confirms
// LocationName and LocationAddress are independently optional -- having
// just one of the two is still enough for a valid Place, not treated the
// same as having neither.
func TestRender_JSONLD_OnlyOneOfNameOrAddressStillProducesLocation(t *testing.T) {
	w := fullWorkshop("address-only", WorkshopPublished)
	w.LocationName = ""
	source := fakeWorkshopSource{"address-only": w}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/address-only"))

	ev := extractJSONLD(t, body)
	loc, ok := ev["location"].(map[string]any)
	if !ok {
		t.Fatalf("location missing when only address was set: %v", ev)
	}
	if loc["name"] != "123 Maker Way, San Francisco, CA" {
		t.Errorf("location.name = %v, want the address to double as the name when no separate venue name is set", loc["name"])
	}
}

// TestRender_JSONLD_DraftAndUnknownSlugOmitBlock confirms neither a draft
// workshop (never public) nor an unknown slug produces any JSON-LD, matching
// the same visibility rule as the fallback <title>/og:* tags.
func TestRender_JSONLD_DraftAndUnknownSlugOmitBlock(t *testing.T) {
	source := fakeWorkshopSource{"secret-draft": fullWorkshop("secret-draft", WorkshopDraft)}
	r := newTestRenderer(source)
	for _, slug := range []string{"does-not-exist", "secret-draft"} {
		assertNoJSONLD(t, string(r.Render("/workshops/"+slug)))
	}
}

// TestRender_JSONLD_StaticRoutesHaveNoBlock confirms the token is simply
// removed (never left as a stray empty <script> tag or leaked into a
// non-workshop page) for every static marketing route.
func TestRender_JSONLD_StaticRoutesHaveNoBlock(t *testing.T) {
	r := newTestRenderer(nil)
	for _, p := range []string{"/", "/about", "/privacy", "/workshops", "/subscribe", "/admin"} {
		assertNoJSONLD(t, string(r.Render(p)))
	}
}

// TestRender_JSONLD_TitleWithQuotesStaysValidJSON is #0055's own acceptance
// criterion: "JSON string values escaped correctly; a title containing
// quotes does not break the block." A title/summary with quotes,
// backslashes, and an embedded "</script>" sequence must still produce a
// block that (a) parses as valid JSON with the literal characters intact and
// (b) never lets a literal "</script>" reach the page (which would close the
// script element early and dump the rest of the JSON as visible text/attempt
// to parse it as HTML).
func TestRender_JSONLD_TitleWithQuotesStaysValidJSON(t *testing.T) {
	tricky := `Solder & Circuits "Intro" <script>alert(1)</script> \backslash\`
	w := fullWorkshop("tricky-title", WorkshopPublished)
	w.Title = tricky
	w.Summary = tricky
	source := fakeWorkshopSource{"tricky-title": w}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/tricky-title"))

	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("literal, unescaped </script> sequence reached the page: %s", body)
	}
	ev := extractJSONLD(t, body)
	if ev["name"] != tricky {
		t.Errorf("name = %v, want the exact tricky title round-tripped through JSON escaping", ev["name"])
	}
	if ev["description"] != tricky {
		t.Errorf("description = %v, want the exact tricky summary round-tripped", ev["description"])
	}
}

// TestRender_JSONLD_DistinctCanceledWorkshopsDoNotShareCacheEntry is a
// regression test for the cache-bucket bug #0055 introduces the fix for:
// before this issue, EVERY non-published workshop slug (draft, canceled,
// unknown) shared the single cacheKeyFallback cache entry, which was safe
// only because they all rendered byte-identical bodies. Once a canceled
// workshop started carrying its own per-workshop JSON-LD, two different
// canceled workshops sharing that one cache entry would leak one workshop's
// structured data onto the other's page. resolve/workshopRouteMeta now give
// a canceled workshop its own "workshop:{slug}" cache key -- this proves
// two distinct canceled workshops render distinct bodies, not whichever one
// happened to populate the shared bucket first.
func TestRender_JSONLD_DistinctCanceledWorkshopsDoNotShareCacheEntry(t *testing.T) {
	a := fullWorkshop("canceled-a", WorkshopCanceled)
	a.Title = "Canceled Workshop A"
	b := fullWorkshop("canceled-b", WorkshopCanceled)
	b.Title = "Canceled Workshop B"
	source := fakeWorkshopSource{"canceled-a": a, "canceled-b": b}
	r := newTestRenderer(source)

	bodyA := string(r.Render("/workshops/canceled-a"))
	bodyB := string(r.Render("/workshops/canceled-b"))

	evA := extractJSONLD(t, bodyA)
	evB := extractJSONLD(t, bodyB)
	if evA["name"] != "Canceled Workshop A" {
		t.Errorf("canceled-a's JSON-LD name = %v, want its own title", evA["name"])
	}
	if evB["name"] != "Canceled Workshop B" {
		t.Errorf("canceled-b's JSON-LD name = %v, want its own title -- got %v, which would mean it leaked A's cached entry", evB["name"], evB["name"])
	}

	r.mu.RLock()
	_, hasA := r.cache["workshop:canceled-a"]
	_, hasB := r.cache["workshop:canceled-b"]
	_, hasFallback := r.cache[cacheKeyFallback]
	r.mu.RUnlock()
	if !hasA || !hasB {
		t.Errorf("expected both canceled workshops to have their own cache entry (workshop:canceled-a=%v, workshop:canceled-b=%v)", hasA, hasB)
	}
	if hasFallback {
		t.Errorf("canceled workshops should not populate the shared fallback bucket now that they carry per-workshop JSON-LD")
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
			Status: WorkshopPublished, Published: true,
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
		tokenOGImage, tokenOGURL, tokenOGType, tokenTwitterCard, tokenJSONLD,
	}
	for _, path := range []string{"/", "/about", "/privacy", "/workshops", "/subscribe", "/nonexistent", "/workshops/some-slug"} {
		body := string(r.Render(path))
		for _, tok := range tokens {
			if strings.Contains(body, tok) {
				t.Errorf("path %q: unsubstituted token %q leaked into rendered body", path, tok)
			}
		}
	}
}

// TestSourceTemplate_EachTokenAppearsExactlyOnce guards against the #0055
// review bounce: web/index.html (the real source template Renderer.substitute
// runs against once built into dist/index.html) once carried an explanatory
// comment that spelled out a live token literally (e.g. "%%OC_JSONLD%%" in
// its own doc comment). strings.NewReplacer does not know the difference
// between a comment and a real substitution site, so that second literal
// occurrence got replaced too, duplicating an ~860-byte JSON-LD block on
// every qualifying workshop page in a real `npm run build` artifact -- a
// defect the placeholder-template-only tests above (TestRender_*) cannot
// catch, since they never read the real source file. This test reads
// web/index.html directly from the repo (not the embedded/placeholder dist,
// per #0141) so it is meaningful in a clean checkout, and fails if any of
// seo.go's own token constants appears there more than once.
func TestSourceTemplate_EachTokenAppearsExactlyOnce(t *testing.T) {
	const templatePath = "../../web/index.html"
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("reading %s: %v", templatePath, err)
	}
	body := string(raw)

	tokens := []string{
		tokenTitle, tokenDescription, tokenOGTitle, tokenOGDescription,
		tokenOGImage, tokenOGURL, tokenOGType, tokenTwitterCard, tokenJSONLD,
	}
	for _, tok := range tokens {
		if n := strings.Count(body, tok); n != 1 {
			t.Errorf("%s: token %q appears %d times, want exactly 1 (a duplicate is almost always a literal mention of the token inside an explanatory comment, which strings.NewReplacer substitutes just like the real substitution site)", templatePath, tok, n)
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
	return Workshop{Slug: "mutable", Title: m.title, Summary: "s", Status: WorkshopPublished, Published: true, UpdatedAt: m.updatedAt}, true, nil
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
