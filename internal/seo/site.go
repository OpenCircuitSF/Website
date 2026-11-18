package seo

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
)

// Site wires the meta-tag injector (#0019), sitemap.xml, robots.txt (#0020),
// and the per-workshop Open Graph card generator (#0273) into the HTTP
// server. It is the single type cmd/opencircuit's mountAndServe constructs
// and mounts. #0532 added the per-archive card generator; #0531 added the
// archive's Atom feed.
type Site struct {
	renderer *Renderer
	sitemap  *Sitemap
	robots   []byte
	cards    *cardCache
	feed     *Feed
	guideArt map[string]guideArt
	thumbMu  sync.Mutex
	thumbs   map[string]guideThumb
}

// NewSite constructs a Site from the built index.html template bytes
// (web.DistFS()'s index.html), the canonical base URL (cfg.BaseURL, e.g.
// "https://www.opencircuitsf.com" -- no trailing slash), an optional
// WorkshopSource (nil only under STORAGE=json, which has no workshops-table
// backing), and an optional ArchiveSource (#0123, nil under that same
// STORAGE=json path, which has no email_campaigns-table backing either --
// mirrors WorkshopSource's own nil-tolerant contract).
func NewSite(indexHTML []byte, baseURL string, source WorkshopSource, archive ArchiveSource) *Site {
	return newSite(indexHTML, baseURL, source, archive, defaultGuideArt)
}

// newSite is NewSite with the guide art set injected (#0709), so tests can
// use a fixture map instead of the embedded images.
func newSite(indexHTML []byte, baseURL string, source WorkshopSource, archive ArchiveSource, art map[string]guideArt) *Site {
	return &Site{
		guideArt: art,
		thumbs:   map[string]guideThumb{},
		renderer: newRenderer(indexHTML, baseURL, source, archive, art),
		sitemap:  NewSitemap(baseURL, source, archive),
		robots:   BuildRobotsTxt(baseURL),
		cards:    newCardCache(mustCardRenderer()),
		feed:     NewFeed(baseURL, archive),
	}
}

// Invalidate clears the per-path meta cache, the sitemap cache, and the
// per-workshop card cache (#0273) -- every cached rendering this Site holds,
// not only workshop-derived ones. Named InvalidateWorkshops until #0325,
// back when #0051's workshop mutation handlers (create/update/publish/
// cancel) were its only callers. #0319 added two more:
// internal/handlers.AdminCampaignArchiveHandler (the admin campaign archive
// toggle) and internal/mailing.Worker (the send worker's own
// archive-publish transition, via the ArchiveCacheInvalidator seam). All
// three call this SAME method through the SAME *Site instance --
// cmd/opencircuit/seo_wiring_test.go proves the instance identity -- so a
// stale title, summary, cover image, generated card, or archive entry never
// lingers past the cache TTL after any of those three actions. Calls
// sitemap.invalidate and cards.invalidate (both unexported since #0337/
// #0273, see their own doc comments) rather than an exported
// Sitemap.Invalidate/cardCache.Invalidate -- *Site is the only type meant to
// satisfy either caller's single-method invalidator interface (see
// invalidator_satisfier_guard_test.go).
func (s *Site) Invalidate() {
	s.renderer.Invalidate()
	s.sitemap.invalidate()
	s.cards.invalidate()
	s.feed.invalidate()
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

// FeedHandler serves GET /archive/feed.xml (#0531): the newsletter
// archive's Atom feed. Registered as its own, more specific pattern in
// cmd/opencircuit/main.go's mountAndServe -- handlers.archiveDetailPattern
// (`^/archive/[^/]+$`) does not match the two-segment path, so no change to
// internal/handlers/routes.go is needed. Serves from the Feed's TTL cache,
// which Site.Invalidate clears, so withholding a campaign drops it from the
// feed immediately (criterion 5) through the same invalidation path the
// sitemap and archive pages use.
func (s *Site) FeedHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := s.feed.Render()
		if err != nil {
			http.Error(w, "failed to build feed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
		_, _ = w.Write(body)
	}
}

// WorkshopCardHandler serves GET /events/{slug}/og.png and the older
// GET /workshops/{slug}/og.png (#0273, #0614): the
// generated per-workshop Open Graph card. handlers.workshopDetailPattern
// (`^/events/[^/]+$`) does not match a path with a second segment, so
// this route needs no change to internal/handlers/routes.go -- it is
// registered as its own, more specific pattern in
// cmd/opencircuit/main.go's mountAndServe, which Go 1.22's ServeMux
// resolves ahead of the "GET /" SPA catch-all.
//
// Gated on Renderer.cardableWorkshop (seo.go), which applies the exact same
// workshopIsCardable predicate workshopRouteMeta's og:image branch uses on
// its own already-fetched Workshop -- a draft, unpublished, canceled, or
// unknown slug 404s here rather than serving a generic PNG under a
// workshop-specific URL, which would itself leak the slug's existence
// (#0273's acceptance criterion 8). This is also why a canceled workshop,
// which keeps the generic og-default.png fallback per #0135's ruling, 404s
// here too rather than falling back -- there is nothing for this URL to
// serve for a slug workshopRouteMeta never points at it.
func (s *Site) WorkshopCardHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		wk, ok := s.renderer.cardableWorkshop(slug)
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.serveCard(w, r, "workshop:"+wk.Slug, wk.Title, cardMetaLine(wk))
	}
}

// serveCard is both card handlers' shared tail (#0532): render (or fetch
// from cardCache under key), then serve the PNG with a one-hour
// Cache-Control and a strong content-derived ETag carrying a 304 path.
// The 404 gate lives in each handler, next to its own cardability lookup,
// so each handler's doc comment can say exactly what it refuses.
func (s *Site) serveCard(w http.ResponseWriter, r *http.Request, key, title, meta string) {
	body, etag, err := s.cards.get(key, title, meta)
	if err != nil {
		http.Error(w, "failed to render card", http.StatusInternalServerError)
		return
	}
	writePNG(w, r, body, etag)
}

// writePNG serves body as a PNG with a one-hour Cache-Control and etag,
// answering a matching If-None-Match with 304.
func writePNG(w http.ResponseWriter, r *http.Request, body []byte, etag string) {
	writeImage(w, r, "image/png", body, etag)
}

// writeImage is writePNG for any image content type (#0710).
func writeImage(w http.ResponseWriter, r *http.Request, contentType string, body []byte, etag string) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("ETag", etag)
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(body)
}

// ArchiveCardHandler serves GET /archive/{slug}/og.png (#0532): the
// generated per-newsletter Open Graph card, mirroring WorkshopCardHandler
// above. handlers.archiveDetailPattern (`^/archive/[^/]+$`) does not match
// a path with a second segment, so this route needs no change to
// internal/handlers/routes.go -- it is registered as its own, more specific
// pattern in cmd/opencircuit/main.go's mountAndServe, which Go 1.22's
// ServeMux resolves ahead of the "GET /" SPA catch-all.
//
// Gated on Renderer.cardableArchive (seo.go), the same Published predicate
// archiveRouteMeta applies before pointing og:image at this URL -- a draft
// (never sent), withheld, or unknown slug 404s here rather than serving a
// generic PNG under a campaign-specific URL, which would leak the slug's
// existence (#0532 criterion 3). The card itself is renderCard's shared
// workshop rendering -- same base image, fonts, title ladder -- with the
// subject as the title and the Pacific archive date as the meta line
// (archiveCardMeta, card.go), matching the archive page's own date label
// (#0519). Cards cache under the "archive:"-namespaced key so an archive
// slug can never collide with an equal workshop slug, and clear through
// Site.Invalidate like every other cached rendering this Site holds.
func (s *Site) ArchiveCardHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		e, ok := s.renderer.cardableArchive(slug)
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.serveCard(w, r, "archive:"+e.Slug, e.Subject, archiveCardMeta(e))
	}
}

// GuideCardHandler serves GET /guides/{slug}/og.png (#0656): the Open
// Graph card for every guide in the embedded catalog (internal/guides),
// rendered by the same card builder as the event and archive cards. The
// workshop guide's card lives here too, not at /workshops/{slug}/og.png,
// which still renders the production event of the same slug. A slug
// outside the catalog is a 404. It serves the guide's hand-made art when
// present (#0709), else the generated card.
func (s *Site) GuideCardHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g, ok := guides.Lookup(r.PathValue("slug"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		if a, ok := s.guideArt[g.Slug]; ok {
			writePNG(w, r, a.data, a.etag)
			return
		}
		s.serveCard(w, r, "guide:"+g.Slug, g.Title, guideCardMeta(g))
	}
}

// GuideCard returns the PNG Open Graph card for the guide slug, the same
// bytes GuideCardHandler serves (#0658: the Bluesky thumbnail of a guide
// post): the hand-made art when present (#0709), else the generated card.
// The returned slice is the art's or the card cache's own; the caller must not
// modify it. An unknown slug is an error.
func (s *Site) GuideCard(slug string) (data []byte, mime string, err error) {
	g, ok := guides.Lookup(slug)
	if !ok {
		return nil, "", fmt.Errorf("seo: unknown guide %q", slug)
	}
	if a, ok := s.guideArt[g.Slug]; ok {
		return a.data, "image/png", nil
	}
	data, _, err = s.cards.get("guide:"+g.Slug, g.Title, guideCardMeta(g))
	if err != nil {
		return nil, "", err
	}
	return data, "image/png", nil
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
