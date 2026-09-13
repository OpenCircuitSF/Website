package seo

import (
	"net/http"
	"strings"
)

// disallowedPaths lists every namespace robots.txt excludes (#0020). Beyond
// the obvious /admin and /api, /confirm, /preferences, and /unsubscribe are
// included even though all three ARE known SPA routes
// (handlers.IsKnownRoute) -- those URLs carry a token in the query string,
// and an indexed token URL is a token leak (#0020's Notes). /auth and
// /account cover the server-side auth endpoints and the account-management
// view respectively.
var disallowedPaths = []string{
	"/admin",
	"/account",
	"/api",
	"/auth",
	"/confirm",
	"/preferences",
	"/unsubscribe",
}

// allowedAPIPaths carves specific exceptions back out of the blanket
// "Disallow: /api" above (#0518). Every public page's server HTML is just
// `<div id="app"></div>` -- all visible text is fetched by the SPA from the
// API (web/src/lib/api.ts) -- so disallowing all of /api meant Googlebot,
// which does not fetch robots-blocked resources even while rendering
// JavaScript, rendered every archive and workshop page with a header and
// footer but no body text.
//
// Google and Bing both resolve a conflicting Allow/Disallow pair by
// LONGEST MATCH, not by which line comes first or which directive type
// wins ties -- so listing these after the Disallow block below is a
// readability choice only. Do not "fix" this by moving "Disallow: /api"
// later in the file; reordering changes nothing about which wins, and an
// Allow line placed anywhere in the file already overrides the shorter
// "Disallow: /api" for these three prefixes.
//
// This list is exactly the public, read-only, token-free /api routes that
// public pages' server-rendered fallback needs (#0519) and that Googlebot's
// JS-rendering pass needs (this issue) -- see cmd/opencircuit's
// mountAndServe route table for the full picture. Every other /api/*
// route -- /api/me (session-gated), /api/events (admin-gated SSE),
// /api/subscribe and /api/subscribe/confirm (state-changing / token-
// bearing), /api/preferences (token-bearing), /api/unsubscribe (token-
// bearing), /api/list-stats and /api/crt-session (public and read-only, but
// not needed to render page text, so deliberately left disallowed per
// #0518's acceptance criteria), and /api/ses/* (server-to-server webhooks)
// -- MUST stay disallowed. Adding an entry here is a security decision:
// prove first that the prefix cannot ALSO match a private route (a prefix
// match on "/api/workshops" would, for example, catch a hypothetical
// "/api/workshops-admin" too), then update this comment's route audit.
var allowedAPIPaths = []string{
	"/api/workshops",
	"/api/archive",
	"/api/interests",
}

// BuildRobotsTxt renders robots.txt: allow the public site, disallow the
// listed namespaces (carving the allowedAPIPaths exceptions back out of
// "Disallow: /api"), and reference the sitemap so crawlers that don't
// already know about it discover it.
func BuildRobotsTxt(baseURL string) []byte {
	baseURL = strings.TrimSuffix(baseURL, "/")

	var b strings.Builder
	b.WriteString("User-agent: *\n")
	b.WriteString("Allow: /\n")
	for _, p := range disallowedPaths {
		b.WriteString("Disallow: ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	for _, p := range allowedAPIPaths {
		b.WriteString("Allow: ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString("Sitemap: ")
	b.WriteString(baseURL)
	b.WriteString("/sitemap.xml\n")

	return []byte(b.String())
}

// NoIndexAPIMiddleware sets "X-Robots-Tag: noindex" on every response under
// /api/ (#0518), before handing the request to next. Crawlers are now
// allowed to FETCH the public /api/workshops, /api/archive, and
// /api/interests JSON (allowedAPIPaths above) so they can render page text
// that depends on it, but the JSON responses themselves are not pages and
// must never be indexed as search results in their own right.
//
// Wired ONCE around the whole mux in cmd/opencircuit's mountAndServe,
// rather than added to each /api handler individually, so a new /api route
// gets the header automatically and no handler can forget it. The header is
// set on the ResponseWriter before next runs, which is safe regardless of
// whether next later calls WriteHeader explicitly or implicitly via Write --
// either way happens after this middleware has already set the header, and
// no handler in this package overwrites or removes it.
//
// Every non-/api response (the SPA shell, /sitemap.xml, /robots.txt, and
// the rendered archive/workshop HTML pages that #0518 exists to keep
// crawlable) passes through untouched -- this must never regress to
// matching on method or content type, only on the literal "/api/" path
// prefix, so it stays correct as routes are added on either side of it.
func NoIndexAPIMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("X-Robots-Tag", "noindex")
		}
		next.ServeHTTP(w, r)
	})
}
