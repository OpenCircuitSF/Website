package seo

import (
	"encoding/json"
	"net/http"
	"strings"
)

// maxPageMetaPath bounds GET /api/page-meta's path parameter (#0711).
const maxPageMetaPath = 1024

// Meta returns the RouteMeta that Render substitutes into path's head: the
// same normalisation and the same resolve call, so the JSON endpoint and the
// server-rendered HTML cannot disagree (#0711).
func (r *Renderer) Meta(path string) RouteMeta {
	_, m := r.resolve(normalizePath(path))
	return m
}

// pageMetaJSON is GET /api/page-meta's body (#0711). It deliberately carries
// no JSON-LD: share sheets reading a live tab never look at it.
type pageMetaJSON struct {
	Title         string `json:"title"`
	Description   string `json:"description"`
	OGTitle       string `json:"og_title"`
	OGDescription string `json:"og_description"`
	OGImage       string `json:"og_image"`
	OGURL         string `json:"og_url"`
	OGType        string `json:"og_type"`
	TwitterCard   string `json:"twitter_card"`
	Canonical     string `json:"canonical,omitempty"`
}

// PageMetaHandler serves GET /api/page-meta?path=/x (#0711): the preview meta
// the server would render into /x's head, so the SPA can refresh its head on
// client-side navigation and a live-tab share (Safari, Messages) reads the
// right card. Public and read-only; it exposes nothing the page's own HTML
// does not. Any query or fragment inside path is ignored, as the HTML
// request's own path would be.
func (s *Site) PageMetaHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" || !strings.HasPrefix(p, "/") || len(p) > maxPageMetaPath {
			http.Error(w, "path must be an absolute path", http.StatusBadRequest)
			return
		}
		if i := strings.IndexAny(p, "?#"); i >= 0 {
			p = p[:i]
		}
		m := s.renderer.Meta(p)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pageMetaJSON{
			Title: m.Title, Description: m.Description,
			OGTitle: m.OGTitle, OGDescription: m.OGDescription,
			OGImage: m.OGImage, OGURL: m.OGURL, OGType: m.OGType,
			TwitterCard: m.TwitterCard, Canonical: m.Canonical,
		})
	}
}
