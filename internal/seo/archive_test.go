// Tests for the ArchiveSource wiring #0123 added to Renderer (seo.go) and
// Sitemap (sitemap.go): /archive and /archive/{slug} meta tags, and the
// sitemap's inclusion/exclusion rule.
package seo

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
)

type fakeArchiveSource map[string]ArchiveEntry

func (f fakeArchiveSource) ArchiveEntryBySlug(slug string) (ArchiveEntry, bool, error) {
	e, ok := f[slug]
	return e, ok, nil
}

func (f fakeArchiveSource) ArchiveEntries() ([]ArchiveEntry, error) {
	out := make([]ArchiveEntry, 0, len(f))
	for _, e := range f {
		out = append(out, e)
	}
	return out, nil
}

func newTestRendererWithArchive(archive ArchiveSource) *Renderer {
	return NewRenderer([]byte(testTemplate), testBaseURL, nil, archive)
}

// TestSitemap_ArchiveRouteIsKnown proves "/archive" (added to
// marketingRoutes by #0123) is a real client route, the same guarantee
// TestSitemap_MarketingRoutesAreKnownRoutes already holds every other
// marketingRoutes entry to.
func TestSitemap_ArchiveRouteIsKnown(t *testing.T) {
	if !handlers.IsKnownRoute("/archive") {
		t.Fatal(`"/archive" is in marketingRoutes but handlers.IsKnownRoute rejects it`)
	}
}

// TestRender_ArchiveDetailUsesStoreData is #0123's own acceptance
// criterion: "<title> from the campaign subject, meta description from
// the preheader, canonical URL, OG card" (PRD §6.8).
func TestRender_ArchiveDetailUsesStoreData(t *testing.T) {
	source := fakeArchiveSource{
		"workshop-night": {
			Slug: "workshop-night", Subject: "Workshop Night Is Back",
			Preheader: "Thursday, 6:30pm.", Published: true, UpdatedAt: "2026-08-01",
		},
	}
	r := newTestRendererWithArchive(source)
	body := string(r.Render("/archive/workshop-night"))

	if !strings.Contains(body, "Workshop Night Is Back") {
		t.Errorf("campaign subject not found in rendered body: %s", body)
	}
	if !strings.Contains(body, "Thursday, 6:30pm.") {
		t.Errorf("campaign preheader not found in rendered body: %s", body)
	}
	if !strings.Contains(body, testBaseURL+"/archive/workshop-night") {
		t.Errorf("canonical archive URL not found in rendered body: %s", body)
	}
}

// TestRender_ArchiveDetailUnpublishedFallsBack proves a pending (never
// sent) or withheld campaign never gets its own social-card content --
// only the generic fallback, the same posture workshopRouteMeta holds for
// a draft/unknown workshop.
func TestRender_ArchiveDetailUnpublishedFallsBack(t *testing.T) {
	source := fakeArchiveSource{
		"pending-campaign":  {Slug: "pending-campaign", Subject: "Secret Draft Subject", Published: false},
		"withheld-campaign": {Slug: "withheld-campaign", Subject: "Retracted Subject", Published: false},
	}
	r := newTestRendererWithArchive(source)

	for _, slug := range []string{"does-not-exist", "pending-campaign", "withheld-campaign"} {
		body := string(r.Render("/archive/" + slug))
		if strings.Contains(body, "Secret Draft Subject") || strings.Contains(body, "Retracted Subject") {
			t.Errorf("slug=%s: leaked a non-published campaign's subject into the rendered body: %s", slug, body)
		}
	}
}

// TestSitemap_ArchiveEntries_PublishedOnly is #0123's sitemap acceptance
// criterion: "withheld and unsent campaigns are excluded" from
// sitemap.xml.
func TestSitemap_ArchiveEntries_PublishedOnly(t *testing.T) {
	source := fakeArchiveSource{
		"published-1": {Slug: "published-1", Subject: "s", Published: true, UpdatedAt: "2026-08-01"},
		"pending-1":   {Slug: "pending-1", Subject: "s", Published: false},
		"withheld-1":  {Slug: "withheld-1", Subject: "s", Published: false},
	}
	s := NewSitemap(testBaseURL, nil, source)
	body, err := s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var parsed urlset
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("sitemap is not valid XML: %v\nbody:\n%s", err, body)
	}

	locs := make(map[string]bool, len(parsed.URLs))
	for _, u := range parsed.URLs {
		locs[u.Loc] = true
	}
	if !locs[testBaseURL+"/archive"] {
		t.Error("sitemap missing the archive index itself (/archive)")
	}
	if !locs[testBaseURL+"/archive/published-1"] {
		t.Error("sitemap missing the published archive entry")
	}
	if locs[testBaseURL+"/archive/pending-1"] {
		t.Error("sitemap includes a pending (never-sent) archive entry")
	}
	if locs[testBaseURL+"/archive/withheld-1"] {
		t.Error("sitemap includes a withheld archive entry")
	}
}
