package seo

import (
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

// TestSitemap_ContainsStaticRoutesWithLastmod covers #0020's first
// criterion: valid XML with <loc> and <lastmod> for each of the four static
// marketing routes.
func TestSitemap_ContainsStaticRoutesWithLastmod(t *testing.T) {
	s := NewSitemap(testBaseURL, nil)
	body, err := s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var parsed urlset
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("sitemap is not valid XML: %v\nbody:\n%s", err, body)
	}

	want := map[string]bool{
		testBaseURL + "/":          false,
		testBaseURL + "/about":     false,
		testBaseURL + "/workshops": false,
		testBaseURL + "/subscribe": false,
	}
	for _, u := range parsed.URLs {
		if _, ok := want[u.Loc]; ok {
			want[u.Loc] = true
			if u.LastMod == "" {
				t.Errorf("%s: missing <lastmod>", u.Loc)
			}
		}
	}
	for loc, found := range want {
		if !found {
			t.Errorf("sitemap missing <loc>%s</loc>", loc)
		}
	}
}

// TestSitemap_ExcludesUnpublishedWorkshops is #0020's second criterion:
// draft, unpublished, and canceled workshops are excluded -- only a
// published workshop's slug should ever appear.
func TestSitemap_ExcludesUnpublishedWorkshops(t *testing.T) {
	source := fakeWorkshopSource{
		"published-1": {Slug: "published-1", Status: WorkshopPublished, UpdatedAt: "2026-08-01"},
		"draft-1":     {Slug: "draft-1", Status: WorkshopDraft},
		"unpub-1":     {Slug: "unpub-1", Status: WorkshopUnpublished},
		"canceled-1":  {Slug: "canceled-1", Status: WorkshopCanceled},
	}
	s := NewSitemap(testBaseURL, source)
	body, err := s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	xmlStr := string(body)

	if !strings.Contains(xmlStr, testBaseURL+"/workshops/published-1") {
		t.Errorf("sitemap missing the published workshop: %s", xmlStr)
	}
	for _, excluded := range []string{"draft-1", "unpub-1", "canceled-1"} {
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
	s := NewSitemap(testBaseURL, nil)
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
	s := NewSitemap(testBaseURL, nil)
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
// the TTL a mutation is not reflected; Invalidate() forces an immediate
// refresh.
func TestSitemap_CachesAndInvalidates(t *testing.T) {
	source := &mutatingSitemapSource{slug: "workshop-a"}
	s := NewSitemap(testBaseURL, source)
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

	s.Invalidate()
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
