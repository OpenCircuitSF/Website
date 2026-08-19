package seo

import (
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
