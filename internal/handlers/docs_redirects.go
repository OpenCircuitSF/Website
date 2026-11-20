package handlers

import "net/http"

// MountDocsRedirects registers the permanent redirect from /agents, the
// short address for AI-agent users, to the website's MCP docs page
// /docs/mcp (#0735). The query string is kept. web/src/lib/router.ts's
// legacyRedirectTarget is the client-side twin.
func MountDocsRedirects(mux *http.ServeMux) {
	mux.HandleFunc("GET /agents", redirectToDocsMCP)
	mux.HandleFunc("GET /agents/{$}", redirectToDocsMCP)
}

func redirectToDocsMCP(w http.ResponseWriter, r *http.Request) {
	target := "/docs/mcp"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}
