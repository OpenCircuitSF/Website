package handlers

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// distFS builds a minimal in-memory FS that mirrors the embedded web/dist
// layout used by SPAHandler in production.
func distFS(files map[string]string) fs.FS {
	m := fstest.MapFS{}
	for name, content := range files {
		m[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return m
}

func serveSPA(fsys fs.FS, path string) *httptest.ResponseRecorder {
	h := NewSPAHandler(fsys)
	mux := http.NewServeMux()
	mux.Handle("GET /", h)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestSPAFaviconServedFromDist confirms that /favicon.png is served as
// image/png (from the embedded dist root) and NOT intercepted by the SPA
// catch-all that returns index.html.
func TestSPAFaviconServedFromDist(t *testing.T) {
	pngMagic := "\x89PNG\r\n\x1a\n" // first 8 bytes of any PNG file
	fsys := distFS(map[string]string{
		"index.html":       "<!doctype html><title>Open Circuit SF</title><div id=\"app\"></div>",
		"favicon.png":      pngMagic + "fake-favicon-body",
		"apple-touch-icon.png": pngMagic + "fake-apple-touch-icon-body",
	})

	rec := serveSPA(fsys, "/favicon.png")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /favicon.png: status = %d, want 200", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "image/png") {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}

	body := rec.Body.String()
	if !strings.HasPrefix(body, pngMagic) {
		preview := body
		if len(preview) > 16 {
			preview = preview[:16]
		}
		t.Errorf("body does not start with PNG magic bytes; got %q", preview)
	}
	if strings.Contains(body, "<!doctype html>") {
		t.Error("GET /favicon.png returned index.html — static file not being served; check SPAHandler.fileExists")
	}
}

// TestSPAAppleTouchIconServedFromDist is the same check for the 180×180 iOS
// bookmark icon.
func TestSPAAppleTouchIconServedFromDist(t *testing.T) {
	pngMagic := "\x89PNG\r\n\x1a\n"
	fsys := distFS(map[string]string{
		"index.html":           "<!doctype html><title>Open Circuit SF</title><div id=\"app\"></div>",
		"apple-touch-icon.png": pngMagic + "fake-apple-touch-icon-body",
	})

	rec := serveSPA(fsys, "/apple-touch-icon.png")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /apple-touch-icon.png: status = %d, want 200", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "image/png") {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}

	body := rec.Body.String()
	if strings.Contains(body, "<!doctype html>") {
		t.Error("GET /apple-touch-icon.png returned index.html — static file not being served")
	}
}

// TestSPAMissingAssetReturns404 confirms a request for a path that looks like a
// static asset (has a file extension) but has no embedded file returns 404 —
// NOT the HTML shell. /favicon.ico is the motivating case: the site ships
// PNG-only favicons (no favicon.ico), and serving index.html for /favicon.ico
// let browsers cache an HTML document as a broken icon.
func TestSPAMissingAssetReturns404(t *testing.T) {
	fsys := distFS(map[string]string{
		"index.html":  "<!doctype html><title>Open Circuit SF</title><div id=\"app\"></div>",
		"favicon.png": "\x89PNG\r\n\x1a\nfake",
	})

	for _, p := range []string{"/favicon.ico", "/missing.png", "/nope.css"} {
		rec := serveSPA(fsys, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s returned index.html — missing asset must 404, not serve the SPA shell", p)
		}
	}
}

// TestSPAKnownRouteFallsBackToIndexWith200 confirms SPA deep-link fallback
// still works for a real route: a path that has no matching embedded file but
// IS in the SPA's known-route table (routes.go) returns index.html with 200,
// so a hard refresh on e.g. /about still loads the app normally.
func TestSPAKnownRouteFallsBackToIndexWith200(t *testing.T) {
	fsys := distFS(map[string]string{
		"index.html": "<!doctype html><title>Open Circuit SF</title><div id=\"app\"></div>",
	})

	rec := serveSPA(fsys, "/about")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /about: status = %d, want 200", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	if !strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Error("SPA fallback did not return index.html for a known deep-link path")
	}
}

// TestSPAPrivacyRouteReturns200 is #0070's acceptance criterion, named
// explicitly rather than folded into TestSPAKnownRouteFallsBackToIndexWith200
// because this is the exact route the parity guard's own doc comment
// (routes_parity_test.go) uses as its running example of a route that can be
// added to router.ts's STATIC_ROUTES alone and still 404 on the server if
// knownStaticRoutes here is not updated to match -- this test is the check
// that would have caught that.
func TestSPAPrivacyRouteReturns200(t *testing.T) {
	fsys := distFS(map[string]string{
		"index.html": "<!doctype html><title>Open Circuit SF</title><div id=\"app\"></div>",
	})

	rec := serveSPA(fsys, "/privacy")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /privacy: status = %d, want 200", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	if !strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Error("SPA fallback did not return index.html for /privacy")
	}
}

// TestSPAWorkshopDetailFallsBackToIndexWith200 is the same check for the one
// dynamic route the SPA supports (/workshops/:slug).
func TestSPAWorkshopDetailFallsBackToIndexWith200(t *testing.T) {
	fsys := distFS(map[string]string{
		"index.html": "<!doctype html><title>Open Circuit SF</title><div id=\"app\"></div>",
	})

	rec := serveSPA(fsys, "/workshops/solder-101")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /workshops/solder-101: status = %d, want 200", rec.Code)
	}
}

// TestSPAUnknownPathReturns404WithShell is #0022's core fix: a path that is
// NOT in the SPA's known-route table (a typo, or a path from an entirely
// different product like ShortLinks' /dashboard) must still get the SPA
// shell -- so the client-side router's own NotFound view renders -- but with
// an actual HTTP 404, not the naive SPA catch-all's 200. Returning 200 for
// every unknown path makes every typo an indexable soft-404 (PRD §5.1's
// concern, restated in this issue's Notes).
func TestSPAUnknownPathReturns404WithShell(t *testing.T) {
	fsys := distFS(map[string]string{
		"index.html": "<!doctype html><title>Open Circuit SF</title><div id=\"app\"></div>",
	})

	for _, p := range []string{"/dashboard", "/this-page-does-not-exist", "/aboot"} {
		rec := serveSPA(fsys, p)

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", p, rec.Code)
		}

		ct := rec.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s: Content-Type = %q, want text/html", p, ct)
		}

		if !strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s: expected the SPA shell body even on a 404, got %q", p, rec.Body.String())
		}
	}
}

