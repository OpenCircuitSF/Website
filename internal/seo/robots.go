package seo

import "strings"

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

// BuildRobotsTxt renders robots.txt: allow the public site, disallow the
// listed namespaces, and reference the sitemap so crawlers that don't
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
	b.WriteString("\n")
	b.WriteString("Sitemap: ")
	b.WriteString(baseURL)
	b.WriteString("/sitemap.xml\n")

	return []byte(b.String())
}
