package seo

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSPAHandler stands in for handlers.NewSPAHandler in these tests: it
// reproduces just the two behaviors Middleware has to interoperate with --
// serving index.html at 200 for a "known" path and at 404 for an "unknown"
// one -- without importing internal/handlers (which would make internal/seo
// depend on internal/handlers depend on internal/seo if handlers ever needed
// this package, an import cycle risk not worth taking for a test double).
type fakeSPAHandler struct {
	knownPaths map[string]bool
}

func (h fakeSPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("Content-Type", "application/javascript")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("console.log('hi')"))
		return
	}
	status := http.StatusOK
	if !h.knownPaths[r.URL.Path] {
		status = http.StatusNotFound
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(testTemplate)) // unsubstituted -- Middleware should discard this and re-render
}

// TestMiddleware_PreservesUpstreamStatus is the binding, amended criterion
// on #0019: injecting metadata must NEVER change SPAHandler's 200-vs-404
// decision. Proven for both a known route (200) and an unknown one (404) so
// the property is guarded in both directions, not just "some status passes
// through".
func TestMiddleware_PreservesUpstreamStatus(t *testing.T) {
	site := NewSite([]byte(testTemplate), testBaseURL, nil, nil)
	next := fakeSPAHandler{knownPaths: map[string]bool{"/": true, "/about": true}}
	mw := site.Middleware(next)

	cases := []struct {
		path       string
		wantStatus int
	}{
		{"/", http.StatusOK},
		{"/about", http.StatusOK},
		{"/this-page-does-not-exist", http.StatusNotFound},
		{"/dashboard", http.StatusNotFound},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.wantStatus {
			t.Errorf("path %q: status = %d, want %d", c.path, rec.Code, c.wantStatus)
		}
	}
}

// TestMiddleware_404GetsInjectedMetadata is the other half of the same
// carried-in criterion, guarded TOGETHER as the issue requires: an unknown
// path returns 404 status AND injected default (not blank, not the raw
// unsubstituted template) metadata in the same response.
func TestMiddleware_404GetsInjectedMetadata(t *testing.T) {
	site := NewSite([]byte(testTemplate), testBaseURL, nil, nil)
	next := fakeSPAHandler{knownPaths: map[string]bool{"/": true}}
	mw := site.Middleware(next)

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/this-page-does-not-exist", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "%%OC_TITLE%%") {
		t.Errorf("404 response still has the unsubstituted title token: %s", body)
	}
	if !strings.Contains(body, "Not Found") {
		t.Errorf("404 response missing the distinct not-found title: %s", body)
	}
	if !strings.Contains(body, testBaseURL+"/og-default.png") {
		t.Errorf("404 response missing the default OG image: %s", body)
	}
}

// TestMiddleware_NonHTMLPassesThroughUnchanged confirms a hashed asset
// response is never touched by the injector -- Content-Type, status, and
// body all pass through byte-for-byte.
func TestMiddleware_NonHTMLPassesThroughUnchanged(t *testing.T) {
	site := NewSite([]byte(testTemplate), testBaseURL, nil, nil)
	next := fakeSPAHandler{knownPaths: map[string]bool{}}
	mw := site.Middleware(next)

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/index-abc123.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/javascript" {
		t.Errorf("Content-Type = %q, want application/javascript", ct)
	}
	if rec.Body.String() != "console.log('hi')" {
		t.Errorf("body altered: %q", rec.Body.String())
	}
}

// TestSitemapHandler_ContentType covers #0020's "served with correct content
// types" criterion for sitemap.xml.
func TestSitemapHandler_ContentType(t *testing.T) {
	site := NewSite([]byte(testTemplate), testBaseURL, nil, nil)
	rec := httptest.NewRecorder()
	site.SitemapHandler()(rec, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("Content-Type = %q, want application/xml", ct)
	}
	if !strings.Contains(rec.Body.String(), "<urlset") {
		t.Errorf("body does not look like a sitemap: %s", rec.Body.String())
	}
}

// TestRobotsHandler_ContentType covers the same criterion for robots.txt.
func TestRobotsHandler_ContentType(t *testing.T) {
	site := NewSite([]byte(testTemplate), testBaseURL, nil, nil)
	rec := httptest.NewRecorder()
	site.RobotsHandler()(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if !strings.Contains(rec.Body.String(), "User-agent: *") {
		t.Errorf("body does not look like robots.txt: %s", rec.Body.String())
	}
}

// TestSite_EmittedURLsShareConfiguredHost is #0072's guard: og:url, og:image,
// and every sitemap.xml <loc> -- plus, for good measure, robots.txt's
// Sitemap: line -- must all be built from the SAME configured base URL. The
// underlying defect was in .env.example (an apex BASE_URL against a www 301),
// not in this package -- internal/seo is correct to trust whatever baseURL it
// is constructed with -- but nothing previously asserted that all four
// emission points actually agree with each other. A deliberately distinctive
// host (never used by any other test in this package) makes a coincidental
// pass against a hardcoded value impossible.
func TestSite_EmittedURLsShareConfiguredHost(t *testing.T) {
	const configuredBaseURL = "https://configured-host.example.test"

	source := fakeWorkshopSource{
		"solder-101": {
			Slug: "solder-101", Title: "Intro to Soldering", Summary: "s",
			CoverImage: "/cover.jpg", Status: WorkshopPublished, Published: true, UpdatedAt: "2026-08-01",
		},
	}
	site := NewSite([]byte(testTemplate), configuredBaseURL, source, nil)

	// og:url and og:image, for both a static route and a workshop-detail route.
	for _, path := range []string{"/", "/about", "/workshops/solder-101"} {
		body := string(site.renderer.Render(path))
		ogURL := extractMetaContent(t, body, "og:url")
		ogImage := extractMetaContent(t, body, "og:image")
		if !strings.HasPrefix(ogURL, configuredBaseURL) {
			t.Errorf("path %q: og:url = %q, want it to start with configured host %q", path, ogURL, configuredBaseURL)
		}
		if !strings.HasPrefix(ogImage, configuredBaseURL) {
			t.Errorf("path %q: og:image = %q, want it to start with configured host %q", path, ogImage, configuredBaseURL)
		}
	}

	// Every sitemap <loc>, static and workshop-derived alike.
	sitemapBody, err := site.sitemap.Render()
	if err != nil {
		t.Fatalf("sitemap Render: %v", err)
	}
	var parsed urlset
	if err := xml.Unmarshal(sitemapBody, &parsed); err != nil {
		t.Fatalf("sitemap is not valid XML: %v\nbody:\n%s", err, sitemapBody)
	}
	if len(parsed.URLs) == 0 {
		t.Fatal("sitemap produced no <url> entries")
	}
	for _, u := range parsed.URLs {
		if !strings.HasPrefix(u.Loc, configuredBaseURL) {
			t.Errorf("sitemap <loc>%s</loc> does not start with configured host %q", u.Loc, configuredBaseURL)
		}
	}

	// robots.txt's Sitemap: line.
	if !strings.Contains(string(site.robots), "Sitemap: "+configuredBaseURL+"/sitemap.xml") {
		t.Errorf("robots.txt does not reference the configured host: %s", site.robots)
	}
}

// extractMetaContent returns the content="..." value of the first
// <meta property="{property}" content="..."> (or name="{property}") tag in
// html, failing the test if it isn't found.
func extractMetaContent(t *testing.T, html, property string) string {
	t.Helper()
	for _, attr := range []string{"property", "name"} {
		marker := attr + `="` + property + `" content="`
		start := strings.Index(html, marker)
		if start == -1 {
			continue
		}
		start += len(marker)
		end := strings.Index(html[start:], `"`)
		if end == -1 {
			t.Fatalf("meta %q content attribute not closed in: %s", property, html)
		}
		return html[start : start+end]
	}
	t.Fatalf("no meta tag for %q found in: %s", property, html)
	return ""
}

// TestSite_InvalidateClearsBothCaches confirms Site.Invalidate reaches BOTH
// the meta-tag renderer and the sitemap -- a partial wiring (only one of the
// two) would be easy to introduce and hard to notice, since each half's own
// cache tests already pass.
//
// An earlier version of this test only ever exercised the renderer half:
// it read site.renderer.Render but never site.sitemap.Render, so deleting
// Site.Invalidate's `s.sitemap.Invalidate()` call left the suite green
// despite the guard's name and #0020's commit message both claiming both
// halves were covered (#0073's review finding). This version drives and
// asserts BOTH halves against the SAME mutation, so it can't pass while
// either is unwired. Site.Invalidate was named InvalidateWorkshops until
// #0325; this test's own name and assertions were updated in that pass, but
// the property it proves is unchanged.
func TestSite_InvalidateClearsBothCaches(t *testing.T) {
	source := &mutatingWorkshopSource{title: "Original Title", updatedAt: "2026-01-01"}
	site := NewSite([]byte(testTemplate), testBaseURL, source, nil)

	_ = site.renderer.Render("/workshops/mutable")
	firstSitemap, err := site.sitemap.Render()
	if err != nil {
		t.Fatalf("sitemap Render: %v", err)
	}
	if strings.Contains(string(firstSitemap), "2026-08-18") {
		t.Fatal("test fixture bug: the pre-mutation sitemap render already shows the post-mutation lastmod")
	}

	source.title = "Changed Title"
	source.updatedAt = "2026-08-18"
	site.Invalidate()

	afterRenderer := string(site.renderer.Render("/workshops/mutable"))
	afterSitemap, err := site.sitemap.Render()
	if err != nil {
		t.Fatalf("sitemap Render: %v", err)
	}

	if !strings.Contains(afterRenderer, "Changed Title") {
		t.Error("Site.Invalidate did not clear the renderer's cache")
	}
	if !strings.Contains(string(afterSitemap), "2026-08-18") {
		t.Error("Site.Invalidate did not clear the sitemap's cache")
	}
}
