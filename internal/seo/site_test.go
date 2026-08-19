package seo

import (
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
	site := NewSite([]byte(testTemplate), testBaseURL, nil)
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
	site := NewSite([]byte(testTemplate), testBaseURL, nil)
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
	site := NewSite([]byte(testTemplate), testBaseURL, nil)
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

// TestSite_InvalidateWorkshopsClearsRendererCache confirms Site.
// InvalidateWorkshops reaches the meta-tag renderer's cache. #0020 extends
// this same test (renamed) to also cover the sitemap cache once that exists.
func TestSite_InvalidateWorkshopsClearsRendererCache(t *testing.T) {
	source := &mutatingWorkshopSource{title: "Original Title"}
	site := NewSite([]byte(testTemplate), testBaseURL, source)

	_ = site.renderer.Render("/workshops/mutable")
	source.title = "Changed Title"
	site.InvalidateWorkshops()
	after := string(site.renderer.Render("/workshops/mutable"))

	if !strings.Contains(after, "Changed Title") {
		t.Error("Site.InvalidateWorkshops did not clear the renderer's cache")
	}
}
