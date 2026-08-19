package handlers

import (
	"sort"
	"testing"
)

// TestIsKnownRoute_StaticRoutes covers every static path in PRD §5.1 plus the
// Phase 1 auth routes, mirroring web/src/lib/router.test.ts's equivalent
// coverage of the TypeScript side of this same table.
func TestIsKnownRoute_StaticRoutes(t *testing.T) {
	known := []string{
		"/",
		"/workshops",
		"/about",
		"/subscribe",
		"/subscribe/thanks",
		"/confirm",
		"/preferences",
		"/unsubscribe",
		"/login",
		"/register/verify",
		"/recover/verify",
		"/account",
		"/admin",
	}
	for _, path := range known {
		if !isKnownRoute(path) {
			t.Errorf("isKnownRoute(%q) = false, want true", path)
		}
	}
}

// TestIsKnownRoute_WorkshopDetail confirms the one dynamic path parameter
// (/workshops/:slug) is recognized, mirroring router.ts's WORKSHOP_DETAIL.
func TestIsKnownRoute_WorkshopDetail(t *testing.T) {
	slugs := []string{
		"/workshops/solder-101",
		"/workshops/x",
		"/workshops/kicad-night-2026",
	}
	for _, path := range slugs {
		if !isKnownRoute(path) {
			t.Errorf("isKnownRoute(%q) = false, want true", path)
		}
	}
}

// TestIsKnownRoute_TrailingSlash confirms a trailing slash on a multi-segment
// path is treated the same as without it -- matching router.ts's parsePath
// normalization, so the Go and TypeScript route tables never disagree on a
// path that only differs by a trailing slash.
func TestIsKnownRoute_TrailingSlash(t *testing.T) {
	if !isKnownRoute("/about/") {
		t.Error(`isKnownRoute("/about/") = false, want true`)
	}
	// Root is left alone -- there is no "less than /" form to normalize to.
	if !isKnownRoute("/") {
		t.Error(`isKnownRoute("/") = false, want true`)
	}
}

// TestIsKnownRoute_Miss covers paths that must NOT be treated as known --
// typos, ShortLinks-era paths that no longer exist, and a two-segment
// workshops path that isn't the detail pattern. This is the assertion that
// actually distinguishes #0022's fix from the naive "always 200" SPA
// catch-all: every one of these must resolve to a miss (404), not a route.
func TestIsKnownRoute_Miss(t *testing.T) {
	misses := []string{
		"/dashboard",   // ShortLinks-era path, not part of this product's route table
		"/aboot",       // typo of /about
		"/workshops//", // double slash after trimming one still isn't a valid slug segment
		"/nonexistent-page",
		"",
	}
	for _, path := range misses {
		if isKnownRoute(path) {
			t.Errorf("isKnownRoute(%q) = true, want false", path)
		}
	}
}

// TestIsKnownRoute_WorkshopsIndexTrailingSlash pins down an easy-to-get-wrong
// case: "/workshops/" (the index route WITH a trailing slash) normalizes to
// "/workshops", which IS known -- it must not be treated as a
// workshop-detail route with an empty slug. Kept separate from
// TestIsKnownRoute_Miss's "/workshops//" (double slash, still a miss) so the
// single-slash/double-slash distinction is unambiguous in the test names.
func TestIsKnownRoute_WorkshopsIndexTrailingSlash(t *testing.T) {
	if !isKnownRoute("/workshops/") {
		t.Error(`isKnownRoute("/workshops/") = false, want true (normalizes to "/workshops")`)
	}
}

// TestIsKnownRoute_ExportedWrapperAgrees confirms the #0019/#0020 exported
// wrapper (IsKnownRoute) makes exactly the same decision as the unexported
// isKnownRoute it delegates to, for both a hit and a miss.
func TestIsKnownRoute_ExportedWrapperAgrees(t *testing.T) {
	if !IsKnownRoute("/about") {
		t.Error(`IsKnownRoute("/about") = false, want true`)
	}
	if IsKnownRoute("/aboot") {
		t.Error(`IsKnownRoute("/aboot") = true, want false`)
	}
}

// TestStaticRoutes_ContainsMarketingPaths confirms the #0019/#0020 exported
// route list includes the four marketing paths those issues curate a sitemap
// subset from, is sorted, and returns a fresh slice each call (so a caller
// mutating the result can't corrupt the package-level table).
func TestStaticRoutes_ContainsMarketingPaths(t *testing.T) {
	routes := StaticRoutes()
	want := []string{"/", "/about", "/subscribe", "/workshops"}
	for _, w := range want {
		found := false
		for _, r := range routes {
			if r == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("StaticRoutes() missing %q; got %v", w, routes)
		}
	}
	if !sort.StringsAreSorted(routes) {
		t.Errorf("StaticRoutes() not sorted: %v", routes)
	}
	routes[0] = "/mutated"
	if StaticRoutes()[0] == "/mutated" {
		t.Error("StaticRoutes() result aliases the package-level map; mutation leaked")
	}
}

// TestWorkshopDetailSlug covers the slug extraction #0019's meta injector and
// #0020's workshop lookup both need: a hit returns the decoded slug, a miss
// (including the workshops index path itself) returns ok=false.
func TestWorkshopDetailSlug(t *testing.T) {
	slug, ok := WorkshopDetailSlug("/workshops/solder-101")
	if !ok || slug != "solder-101" {
		t.Errorf("WorkshopDetailSlug(/workshops/solder-101) = (%q, %v), want (solder-101, true)", slug, ok)
	}

	slug, ok = WorkshopDetailSlug("/workshops/solder-101/")
	if !ok || slug != "solder-101" {
		t.Errorf("WorkshopDetailSlug with trailing slash = (%q, %v), want (solder-101, true)", slug, ok)
	}

	if _, ok := WorkshopDetailSlug("/workshops"); ok {
		t.Error("WorkshopDetailSlug(/workshops) = ok, want false (that's the index, not a detail slug)")
	}
	if _, ok := WorkshopDetailSlug("/about"); ok {
		t.Error("WorkshopDetailSlug(/about) = ok, want false")
	}
}
