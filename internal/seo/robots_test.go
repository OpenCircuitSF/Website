package seo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBuildRobotsTxt_AllowsRootDisallowsSensitiveNamespaces is #0020's third
// and fourth criteria: allow "/", disallow the admin/api/token namespaces,
// and reference the sitemap URL.
func TestBuildRobotsTxt_AllowsRootDisallowsSensitiveNamespaces(t *testing.T) {
	body := string(BuildRobotsTxt(testBaseURL))

	if !strings.Contains(body, "Allow: /\n") {
		t.Errorf("robots.txt does not allow /: %s", body)
	}
	for _, p := range []string{"/admin", "/account", "/api", "/auth", "/confirm", "/preferences", "/unsubscribe"} {
		if !strings.Contains(body, "Disallow: "+p+"\n") {
			t.Errorf("robots.txt missing Disallow: %s\ngot:\n%s", p, body)
		}
	}
	if !strings.Contains(body, "Sitemap: "+testBaseURL+"/sitemap.xml") {
		t.Errorf("robots.txt does not reference the sitemap URL: %s", body)
	}
}

// TestBuildRobotsTxt_AllowsPublicReadOnlyAPIEndpoints is #0518's first
// criterion: the three public, read-only, token-free endpoints public pages
// render their body text from (/api/workshops, /api/archive,
// /api/interests) get an explicit Allow, carving them back out of the
// blanket "Disallow: /api" above.
func TestBuildRobotsTxt_AllowsPublicReadOnlyAPIEndpoints(t *testing.T) {
	body := string(BuildRobotsTxt(testBaseURL))

	for _, p := range []string{"/api/workshops", "/api/archive", "/api/interests"} {
		if !strings.Contains(body, "Allow: "+p+"\n") {
			t.Errorf("robots.txt missing Allow: %s\ngot:\n%s", p, body)
		}
	}
	// The blanket Disallow must still be present -- the Allow lines above
	// are exceptions carved out of it by longest-match, not a replacement
	// for it.
	if !strings.Contains(body, "Disallow: /api\n") {
		t.Errorf("robots.txt must still disallow /api generally: %s", body)
	}
}

// TestBuildRobotsTxt_TokenAndPrivateAPIEndpointsStayDisallowed is #0518's
// second criterion: every /api/* route that is NOT one of the three public
// read-only endpoints above must remain covered only by the blanket
// "Disallow: /api" -- no Allow line exists for it, and in particular no
// Allow line's prefix accidentally catches it. This is the full route
// table enumerated from cmd/opencircuit's mountAndServe (main.go) as of
// #0518: /api/me (session-gated), /api/events (admin-gated), /api/subscribe
// and /api/subscribe/confirm (state-changing/token-bearing),
// /api/preferences (token-bearing, both GET and PATCH), /api/unsubscribe
// (token-bearing, both GET and POST), /api/list-stats and /api/crt-session
// (public read-only but not in the allowed set), and /api/ses/notifications
// and /api/ses/inbound (server-to-server webhooks).
func TestBuildRobotsTxt_TokenAndPrivateAPIEndpointsStayDisallowed(t *testing.T) {
	body := string(BuildRobotsTxt(testBaseURL))

	private := []string{
		"/api/me",
		"/api/events",
		"/api/subscribe",
		"/api/subscribe/confirm",
		"/api/preferences",
		"/api/unsubscribe",
		"/api/list-stats",
		"/api/crt-session",
		"/api/ses/notifications",
		"/api/ses/inbound",
	}
	allowed := []string{"/api/workshops", "/api/archive", "/api/interests"}

	for _, p := range private {
		for _, a := range allowed {
			if strings.HasPrefix(p, a) {
				t.Errorf("private route %q is covered by Allow prefix %q -- this would unintentionally expose it", p, a)
			}
		}
		if strings.Contains(body, "Allow: "+p+"\n") {
			t.Errorf("private route %q must not have its own Allow line: %s", p, body)
		}
	}
}

// TestBuildRobotsTxt_TokenRoutesAreKnownButDisallowed documents the
// distinction called out in #0020's Notes: /confirm, /preferences, and
// /unsubscribe ARE known SPA routes (so a real crawler CAN resolve them),
// but they must still be disallowed because they carry tokens. This test
// fails if a future edit "simplifies" robots.txt by deriving it from
// handlers.IsKnownRoute (which would make all three allowed, since "known"
// and "should be indexed" are different questions).
func TestBuildRobotsTxt_TokenRoutesAreKnownButDisallowed(t *testing.T) {
	body := string(BuildRobotsTxt(testBaseURL))
	for _, p := range []string{"/confirm", "/preferences", "/unsubscribe"} {
		if !strings.Contains(body, "Disallow: "+p) {
			t.Errorf("token-carrying route %q must be disallowed even though it is a known route", p)
		}
	}
}

// TestNoIndexAPIMiddleware_SetsHeaderOnAPIRoutesOnly is #0518 criterion 3,
// proved both directions over a real http.ServeMux (no live server needed):
// every /api/ response carries X-Robots-Tag: noindex, and the header never
// leaks onto the SPA shell, /sitemap.xml, or /robots.txt -- the exact three
// non-API surfaces #0518 names as needing to stay indexable.
func TestNoIndexAPIMiddleware_SetsHeaderOnAPIRoutesOnly(t *testing.T) {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	mux.HandleFunc("GET /api/workshops", ok)
	mux.HandleFunc("GET /api/archive", ok)
	mux.HandleFunc("GET /api/interests", ok)
	mux.HandleFunc("GET /api/me", ok)
	mux.HandleFunc("GET /sitemap.xml", ok)
	mux.HandleFunc("GET /robots.txt", ok)
	mux.HandleFunc("GET /", ok) // stands in for the SPA shell / rendered archive+workshop pages

	handler := NoIndexAPIMiddleware(mux)

	apiPaths := []string{"/api/workshops", "/api/archive", "/api/interests", "/api/me"}
	for _, p := range apiPaths {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
			t.Errorf("GET %s: X-Robots-Tag = %q, want %q", p, got, "noindex")
		}
	}

	nonAPIPaths := []string{"/sitemap.xml", "/robots.txt", "/", "/archive/09-2026", "/workshops/soldering-101"}
	for _, p := range nonAPIPaths {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if got := rec.Header().Get("X-Robots-Tag"); got != "" {
			t.Errorf("GET %s: X-Robots-Tag = %q, want unset -- this path must stay indexable", p, got)
		}
	}
}
