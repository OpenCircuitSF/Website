package handlers

import (
	"net/http"
	"net/url"
)

// MountLegacyWorkshopRedirects registers permanent redirects for the old
// public paths (#0614). Those URLs are already in sent email, flyers, short
// links and published posts. No store lookup happens here: a redirect that
// depended on a listing's existence would reveal drafts, so /events/{slug}
// alone answers 200 or 404. web/src/lib/router.ts's legacyRedirectTarget is
// the client-side twin of these targets.
func MountLegacyWorkshopRedirects(mux *http.ServeMux) {
	index := http.HandlerFunc(redirectLegacyWorkshopsIndex)
	detail := http.HandlerFunc(redirectLegacyWorkshopDetail)
	mux.Handle("GET /workshops", index)
	mux.Handle("GET /workshops/{$}", index)
	mux.Handle("GET /workshops/{slug}", detail)
	mux.Handle("GET /workshops/{slug}/{$}", detail)
}

// redirectLegacyWorkshopsIndex puts type=workshop first, because
// URLSearchParams.get returns the first value: a stray type= in an old link
// cannot override it.
func redirectLegacyWorkshopsIndex(w http.ResponseWriter, r *http.Request) {
	target := "/events?type=workshop"
	if r.URL.RawQuery != "" {
		target += "&" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

func redirectLegacyWorkshopDetail(w http.ResponseWriter, r *http.Request) {
	target := "/events/" + url.PathEscape(r.PathValue("slug"))
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}
