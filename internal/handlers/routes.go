package handlers

import (
	"regexp"
	"sort"
	"strings"
)

// knownStaticRoutes mirrors web/src/lib/router.ts's STATIC_ROUTES: every path
// the SPA's client-side router recognizes as a real view rather than
// not-found. SPAHandler (static.go) uses this table to decide whether an
// unmatched deep-link path -- one with no embedded file and no extension --
// is a real route (serve the SPA shell, HTTP 200) or a miss (serve the same
// SPA shell so the client router's own NotFound view renders, but with HTTP
// 404). See #0022.
//
// #0019 (server-injected per-route meta tags) is expected to reuse this same
// table rather than duplicate it, so the two stay in sync by construction
// rather than by convention -- that claim is true of #0019 reusing *this* Go
// table from Go. It was previously (and wrongly) claimed of the relationship
// to router.ts's STATIC_ROUTES too: two files in two languages cannot be in
// sync "by construction" unless one is generated from the other, which is not
// the case here. That TypeScript mirror is kept honest instead by
// routes_parity_test.go (#0071), which fails `go test ./...` when this table
// and STATIC_ROUTES disagree in either direction -- by convention, enforced.
var knownStaticRoutes = map[string]bool{
	"/":                 true,
	"/workshops":        true,
	"/archive":          true,
	"/about":            true,
	"/privacy":          true,
	"/subscribe":        true,
	"/subscribe/thanks": true,
	"/confirm":          true,
	"/preferences":      true,
	"/unsubscribe":      true,
	"/login":            true,
	"/register/verify":  true,
	"/recover/verify":   true,
	"/account":          true,
	"/admin":            true,
}

// workshopDetailPattern is one of the two dynamic paths the router supports
// (/workshops/:slug), mirroring router.ts's WORKSHOP_DETAIL regex.
var workshopDetailPattern = regexp.MustCompile(`^/workshops/[^/]+$`)

// archiveDetailPattern is the other dynamic path (/archive/:slug, #0123),
// mirroring router.ts's ARCHIVE_DETAIL regex.
var archiveDetailPattern = regexp.MustCompile(`^/archive/[^/]+$`)

// isKnownRoute reports whether path matches an entry in the SPA's route
// table -- either a static path or the single dynamic pattern
// (/workshops/{slug}). A trailing slash on a multi-segment path is treated
// as equivalent to the path without it, matching router.ts's parsePath
// normalization, so "/about/" and "/about" are both known.
func isKnownRoute(path string) bool {
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	if knownStaticRoutes[path] {
		return true
	}
	return workshopDetailPattern.MatchString(path) || archiveDetailPattern.MatchString(path)
}

// IsKnownRoute is the exported form of isKnownRoute, added for #0019/#0020
// (internal/seo) so the meta-tag injector and the sitemap generator can ask
// "is this path real" against the exact same table SPAHandler uses for its
// 200-vs-404 decision, rather than re-deriving their own copy. Deliberately a
// thin wrapper rather than exporting isKnownRoute itself, so the existing
// in-package tests (routes_test.go, static_test.go) that already call the
// unexported name keep working untouched.
func IsKnownRoute(path string) bool {
	return isKnownRoute(path)
}

// StaticRoutes returns a sorted copy of every static path in the SPA's route
// table (knownStaticRoutes' keys). Exported for #0019/#0020 so
// internal/seo builds its own curated subsets (e.g. the sitemap's indexable
// routes, which deliberately excludes the auth/token paths present here) from
// this single source rather than hand-copying the list a second time. Returns
// a fresh slice on every call so callers can't mutate the package-level map
// through the result.
func StaticRoutes() []string {
	out := make([]string, 0, len(knownStaticRoutes))
	for p := range knownStaticRoutes {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// WorkshopDetailSlug reports whether path matches the /workshops/{slug}
// dynamic pattern and, if so, returns the decoded slug. Mirrors the trailing
// slash normalization isKnownRoute applies, so "/workshops/solder-101/" and
// "/workshops/solder-101" agree.
func WorkshopDetailSlug(path string) (slug string, ok bool) {
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	if !workshopDetailPattern.MatchString(path) {
		return "", false
	}
	return strings.TrimPrefix(path, "/workshops/"), true
}

// ArchiveDetailSlug reports whether path matches the /archive/{slug}
// dynamic pattern (#0123) and, if so, returns the decoded slug. Mirrors
// WorkshopDetailSlug's own trailing-slash normalization.
func ArchiveDetailSlug(path string) (slug string, ok bool) {
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	if !archiveDetailPattern.MatchString(path) {
		return "", false
	}
	return strings.TrimPrefix(path, "/archive/"), true
}
