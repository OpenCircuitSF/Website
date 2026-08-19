package seo

import (
	"bytes"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// Site wires the meta-tag injector (#0019), sitemap.xml, and robots.txt
// (#0020) into the HTTP server. It is the single type cmd/opencircuit's
// mountAndServe constructs and mounts.
type Site struct {
	renderer *Renderer
	sitemap  *Sitemap
	robots   []byte
}

// NewSite constructs a Site from the built index.html template bytes
// (web.DistFS()'s index.html), the canonical base URL (cfg.BaseURL, e.g.
// "https://www.opencircuitsf.com" -- no trailing slash), and an optional
// WorkshopSource (nil until #0051/#0054 land).
func NewSite(indexHTML []byte, baseURL string, source WorkshopSource) *Site {
	return &Site{
		renderer: NewRenderer(indexHTML, baseURL, source),
		sitemap:  NewSitemap(baseURL, source),
		robots:   BuildRobotsTxt(baseURL),
	}
}

// InvalidateWorkshops clears both the per-path meta cache and the sitemap
// cache. #0051's workshop mutation handlers (create/update/publish/cancel)
// call this so a stale title, summary, or cover image doesn't linger for up
// to the cache TTL after an edit.
func (s *Site) InvalidateWorkshops() {
	s.renderer.InvalidateWorkshops()
	s.sitemap.Invalidate()
}

// Middleware wraps next (in practice, handlers.NewSPAHandler's catch-all)
// and rewrites its response body when next served the HTML shell, replacing
// it with the per-path rendering from Renderer.Render. It deliberately does
// NOT decide the status code itself: it buffers next's response, and if
// next's response was HTML, re-serves Renderer.Render's body at whatever
// status next chose (200 for a known route, 404 for a miss per #0022) --
// preserving SPAHandler's 404 behavior is #0019's amended, binding
// criterion, and mirroring the upstream status rather than recomputing it is
// what makes that guarantee structural rather than something the two
// packages have to agree on by convention.
//
// A non-HTML response (a hashed JS/CSS asset, a PNG favicon, a missing-asset
// 404) is passed through completely unchanged -- Renderer.Render is never
// even called for those requests.
func (s *Site) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := newRecorder()
		next.ServeHTTP(rec, r)

		ct := rec.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			copyHeader(w.Header(), rec.Header())
			w.WriteHeader(rec.status)
			_, _ = w.Write(rec.body.Bytes())
			return
		}

		body := s.renderer.Render(r.URL.Path)

		h := w.Header()
		copyHeader(h, rec.Header())
		h.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(rec.status)
		if _, err := w.Write(body); err != nil {
			log.Printf("seo: write injected index.html for %s: %v", r.URL.Path, err)
		}
	})
}

// SitemapHandler serves GET /sitemap.xml (#0020).
func (s *Site) SitemapHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := s.sitemap.Render()
		if err != nil {
			http.Error(w, "failed to build sitemap", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write(body)
	}
}

// RobotsHandler serves GET /robots.txt (#0020). robots.txt has no dynamic
// content (it doesn't depend on workshop data), so it's computed once at
// Site construction rather than cached-with-TTL like the sitemap.
func (s *Site) RobotsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(s.robots)
	}
}

// recorder is a minimal http.ResponseWriter that buffers a response so
// Middleware can inspect its Content-Type and status before deciding whether
// to replace the body. Deliberately hand-rolled rather than reusing
// net/http/httptest.ResponseRecorder to keep the production request path
// free of a package whose doc comment identifies it as a testing helper.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newRecorder() *recorder {
	return &recorder{header: make(http.Header), status: http.StatusOK}
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }

func (r *recorder) WriteHeader(status int) { r.status = status }

// copyHeader copies every header from src to dst except Content-Length,
// which the caller sets itself once the final (possibly replaced) body
// length is known.
func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		if k == "Content-Length" {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
