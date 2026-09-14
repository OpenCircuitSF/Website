package seo

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
)

// TestSitemap_MarketingRoutesAreKnownRoutes is #0020's carried-in criterion:
// "Any path that internal/handlers' isKnownRoute rejects must never appear
// in a <loc>." Asserted directly against the exported wrapper rather than
// trusted, since marketingRoutes is a hand-curated subset of the full route
// table, not derived from it.
func TestSitemap_MarketingRoutesAreKnownRoutes(t *testing.T) {
	for _, p := range marketingRoutes {
		if !handlers.IsKnownRoute(p) {
			t.Errorf("marketingRoutes contains %q, which handlers.IsKnownRoute rejects", p)
		}
	}
}

// TestSitemap_StaticRoutesPresentWithLastmodPolicy covers #0020's first
// criterion (all six static marketing routes present as <loc>) together
// with #0533's lastmod policy: the three purely static copy pages
// (/about, /privacy, /subscribe) deliberately OMIT <lastmod> — no truthful
// date exists for copy that never changes, and Google discounts lastmod for
// a site where it is not consistently accurate. The three data-derived
// pages (/, /workshops, /archive) also omit it here because their sources
// are nil, so they have no data to derive a date from.
func TestSitemap_StaticRoutesPresentWithLastmodPolicy(t *testing.T) {
	s := NewSitemap(testBaseURL, nil, nil)
	body, err := s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var parsed urlset
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("sitemap is not valid XML: %v\nbody:\n%s", err, body)
	}

	locs := map[string]bool{}
	lastmods := map[string]string{}
	for _, u := range parsed.URLs {
		locs[u.Loc] = true
		lastmods[u.Loc] = u.LastMod
	}
	for _, path := range marketingRoutes {
		loc := testBaseURL + path
		if !locs[loc] {
			t.Errorf("sitemap missing <loc>%s</loc>", loc)
		}
	}
	for _, path := range []string{"/about", "/privacy", "/subscribe"} {
		if got := lastmods[testBaseURL+path]; got != "" {
			t.Errorf("%s: <lastmod> = %q, want it omitted (#0533: static copy has no truthful date to state)", path, got)
		}
	}
	for _, path := range []string{"/", "/workshops", "/archive"} {
		if got := lastmods[testBaseURL+path]; got != "" {
			t.Errorf("%s: <lastmod> = %q, want it omitted when the data source is nil (no data, no date)", path, got)
		}
	}
}

// TestSitemap_DataDerivedLastmodForIndexPages is #0533's criterion 2:
// /workshops' lastmod is the newest PUBLISHED workshop's UpdatedAt,
// /archive's is the newest published entry's, and /'s is whichever source
// changed most recently. A draft workshop with a later date must not leak
// into any of the three, and the entries' own <lastmod> values are
// unchanged (criterion 5).
func TestSitemap_DataDerivedLastmodForIndexPages(t *testing.T) {
	source := fakeWorkshopSource{
		"older":  {Slug: "older", Status: WorkshopPublished, Published: true, UpdatedAt: "2026-07-01"},
		"newest": {Slug: "newest", Status: WorkshopPublished, Published: true, UpdatedAt: "2026-08-15"},
		"draft":  {Slug: "draft", Status: WorkshopDraft, UpdatedAt: "2026-12-31"},
	}
	archive := fakeArchiveSource{
		"07-2026": {Slug: "07-2026", Subject: "July", UpdatedAt: "2026-08-01", Published: true},
		"09-2026": {Slug: "09-2026", Subject: "September", UpdatedAt: "2026-09-05", Published: true},
	}
	s := NewSitemap(testBaseURL, source, archive)
	body, err := s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var parsed urlset
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("sitemap is not valid XML: %v\nbody:\n%s", err, body)
	}
	lastmods := map[string]string{}
	for _, u := range parsed.URLs {
		lastmods[u.Loc] = u.LastMod
	}

	want := map[string]string{
		testBaseURL + "/":                 "2026-09-05",
		testBaseURL + "/workshops":        "2026-08-15",
		testBaseURL + "/archive":          "2026-09-05",
		testBaseURL + "/workshops/newest": "2026-08-15",
		testBaseURL + "/workshops/older":  "2026-07-01",
		testBaseURL + "/archive/09-2026":  "2026-09-05",
		testBaseURL + "/archive/07-2026":  "2026-08-01",
		testBaseURL + "/about":            "",
		testBaseURL + "/privacy":          "",
		testBaseURL + "/subscribe":        "",
	}
	for loc, wantLastMod := range want {
		if got := lastmods[loc]; got != wantLastMod {
			t.Errorf("%s: <lastmod> = %q, want %q", loc, got, wantLastMod)
		}
	}
	if _, ok := lastmods[testBaseURL+"/workshops/draft"]; ok {
		t.Error("draft workshop appeared in the sitemap")
	}
}

// TestSitemap_ByteIdenticalAcrossConstructions is #0533's criterion 4:
// restarting the service (constructing a fresh Site over unchanged data)
// must leave the sitemap byte-identical. Two Sites are constructed here
// with a real pause between them -- before #0533 each stamped the static
// routes with its own construction date, so this comparison is exactly the
// check that would fail against that regression. The other half of the
// regression shape (a construction-date stamp on the static copy pages) is
// caught immediately by TestSitemap_StaticRoutesPresentWithLastmodPolicy,
// which requires /about, /privacy, and /subscribe to carry NO lastmod at
// all.
func TestSitemap_ByteIdenticalAcrossConstructions(t *testing.T) {
	source := fakeWorkshopSource{
		"solder-101": {Slug: "solder-101", Status: WorkshopPublished, Published: true, UpdatedAt: "2026-08-01"},
	}
	archive := fakeArchiveSource{
		"09-2026": {Slug: "09-2026", Subject: "September", UpdatedAt: "2026-09-05", Published: true},
	}

	first := NewSite([]byte(testTemplate), testBaseURL, source, archive)
	firstBody, err := first.sitemap.Render()
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}

	time.Sleep(1100 * time.Millisecond) // construct the second Site at a genuinely different time

	second := NewSite([]byte(testTemplate), testBaseURL, source, archive)
	secondBody, err := second.sitemap.Render()
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}

	if !bytes.Equal(firstBody, secondBody) {
		t.Fatalf("two Sites built at different times produced different sitemap bytes:\nfirst:\n%s\nsecond:\n%s", firstBody, secondBody)
	}
}

// TestSitemap_ExcludesUnpublishedWorkshops is #0020's second criterion:
// draft, unpublished, and canceled workshops are excluded -- only a
// published workshop's slug should ever appear.
func TestSitemap_ExcludesUnpublishedWorkshops(t *testing.T) {
	source := fakeWorkshopSource{
		// #0177: status=published always implies a stamped published_at
		// (#0171, proved by execution), so Published must be true here --
		// Published: false paired with WorkshopPublished is a state #0171
		// proved unreachable. Harmless to the assertion below (the sitemap
		// gate is status-only, per the canceled-2 comment above), but it
		// made the conjunctive gate pass for the wrong reason.
		"published-1": {Slug: "published-1", Status: WorkshopPublished, Published: true, UpdatedAt: "2026-08-01"},
		"draft-1":     {Slug: "draft-1", Status: WorkshopDraft},
		"unpub-1":     {Slug: "unpub-1", Status: WorkshopUnpublished},
		// canceled-1 was never published (Published defaults false: a
		// draft canceled outright). canceled-2 WAS published before the
		// cancel (Published: true, mirroring published_at staying
		// non-NULL across the transition -- see #0171's review notes).
		// #0174: the sitemap gate is status-only (Sitemap.Build's own
		// "w.Status != WorkshopPublished" check, sitemap.go, checks only
		// w.Status, never w.Published), so canceled-2 is the fixture
		// that actually exercises the gate -- a hypothetical regression
		// that swapped the check to key off Published instead of Status
		// would still exclude canceled-1 (Published already false) but
		// would wrongly include canceled-2. Both are kept so a
		// never-published and a was-published canceled workshop are each
		// covered.
		"canceled-1": {Slug: "canceled-1", Status: WorkshopCanceled},
		"canceled-2": {Slug: "canceled-2", Status: WorkshopCanceled, Published: true},
	}
	s := NewSitemap(testBaseURL, source, nil)
	body, err := s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	xmlStr := string(body)

	if !strings.Contains(xmlStr, testBaseURL+"/workshops/published-1") {
		t.Errorf("sitemap missing the published workshop: %s", xmlStr)
	}
	for _, excluded := range []string{"draft-1", "unpub-1", "canceled-1", "canceled-2"} {
		if strings.Contains(xmlStr, "/workshops/"+excluded) {
			t.Errorf("sitemap contains excluded (non-published) workshop %q: %s", excluded, xmlStr)
		}
	}
}

// TestSitemap_NoWorkshopSourceOmitsWorkshopSection confirms the Notes'
// "until #0051 lands, the workshop portion can be empty" behavior: a nil
// source still produces a valid sitemap with just the static routes, rather
// than erroring.
func TestSitemap_NoWorkshopSourceOmitsWorkshopSection(t *testing.T) {
	s := NewSitemap(testBaseURL, nil, nil)
	body, err := s.Build()
	if err != nil {
		t.Fatalf("Build with nil source: %v", err)
	}
	if !strings.Contains(string(body), testBaseURL+"/about") {
		t.Error("static routes should still be present with a nil workshop source")
	}
}

// TestSitemap_404PathNeverAppears is #0020's carried-in criterion,
// restated for the specific case #0022's fifth criterion was about: an
// unknown/404 path must never be listed. This can't literally happen by
// construction (nothing feeds 404 paths into Build), so this test guards the
// property the way the issue asks -- assert it, don't just rely on it being
// structurally true.
func TestSitemap_404PathNeverAppears(t *testing.T) {
	s := NewSitemap(testBaseURL, nil, nil)
	body, err := s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	xmlStr := string(body)
	for _, p := range []string{"/dashboard", "/this-page-does-not-exist", "/aboot"} {
		if handlers.IsKnownRoute(p) {
			t.Fatalf("test fixture bug: %q is actually a known route", p)
		}
		if strings.Contains(xmlStr, testBaseURL+p+"<") {
			t.Errorf("sitemap contains an unknown (404) path %q: %s", p, xmlStr)
		}
	}
}

// TestSitemap_CachesAndInvalidates mirrors the Renderer cache tests: within
// the TTL a mutation is not reflected; invalidate() (unexported since
// #0337 -- see its own doc comment) forces an immediate refresh.
func TestSitemap_CachesAndInvalidates(t *testing.T) {
	source := &mutatingSitemapSource{slug: "workshop-a"}
	s := NewSitemap(testBaseURL, source, nil)
	clock := &fakeClock{t: time.Unix(0, 0)}
	s.now = clock.now

	first, err := s.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	source.slug = "workshop-b"
	clock.advance(1 * time.Second)
	second, err := s.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(first), "workshop-a") {
		t.Fatalf("first render missing expected slug: %s", first)
	}
	if strings.Contains(string(second), "workshop-b") {
		t.Error("sitemap cache did not hold within the TTL")
	}

	s.invalidate()
	third, err := s.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(third), "workshop-b") {
		t.Error("Invalidate did not force a fresh sitemap build")
	}
}

type mutatingSitemapSource struct{ slug string }

func (m *mutatingSitemapSource) WorkshopBySlug(slug string) (Workshop, bool, error) {
	return Workshop{}, false, nil
}

func (m *mutatingSitemapSource) Workshops() ([]Workshop, error) {
	return []Workshop{{Slug: m.slug, Status: WorkshopPublished, UpdatedAt: "2026-08-01"}}, nil
}
