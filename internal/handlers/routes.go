package handlers

import "regexp"

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
// rather than by convention.
var knownStaticRoutes = map[string]bool{
	"/":                 true,
	"/workshops":        true,
	"/about":            true,
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

// workshopDetailPattern is the one dynamic path the router supports
// (/workshops/:slug), mirroring router.ts's WORKSHOP_DETAIL regex.
var workshopDetailPattern = regexp.MustCompile(`^/workshops/[^/]+$`)

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
	return workshopDetailPattern.MatchString(path)
}
