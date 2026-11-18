package handlers

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// Cache-Control values (#0716). Hashed /assets/* files never change under
// their name, so they are cached for a year; the HTML shell is revalidated
// on every load so a new release is picked up immediately (#0715).
const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheShell     = "no-cache"
)

// precompressExts are the /assets/* extensions gzipped once at construction.
var precompressExts = map[string]bool{
	".js": true, ".css": true, ".svg": true, ".json": true,
	".map": true, ".txt": true, ".xml": true, ".html": true,
}

// SPAHandler serves the embedded Svelte single-page app. It is mounted on the
// catch-all "GET /" pattern, which is the least-specific route under the Go 1.22
// ServeMux, so the explicit /health, /u/{key}, /api/*, /auth/*, /account/*, and
// /admin/* patterns always win over it.
//
// Behavior:
//   - A request whose path maps to a real embedded file (e.g. the hashed
//     /assets/index-*.js or /assets/index-*.css) is served directly with the
//     correct Content-Type via http.FileServerFS.
//   - A path with no matching embedded file and no extension falls back to
//     index.html so a hard refresh on a client-side route still loads the
//     app. The status code depends on whether the path is in the SPA's own
//     known-route table (routes.go, #0022): a real route (e.g. /about) gets
//     200; anything else gets 404 while still serving the same index.html,
//     so the client-side router's NotFound view renders instead of a bare
//     404 or (worse) an indexable soft-404 that returns 200 for every typo.
//   - (#0522) A dynamic deep link whose live record the SPA's own JSON API
//     would 404 or 410 gets that SAME status from the deep-link resolvers
//     below, still with the shell as the body: an unknown/draft/unsent
//     slug is a real 404 and a withheld archive campaign a real 410, not a
//     200 a search engine reads as a successful page. Resolvers are
//     optional: with none wired (STORAGE=json dev mode, existing tests),
//     the routes table alone decides, exactly as before this issue.
//   - (#0716) Hashed /assets/* files carry Cache-Control cacheImmutable;
//     the HTML shell carries no-cache. Root files (favicons, images, the
//     web manifest) are not content-hashed and get no Cache-Control here.
//   - (#0716) Compressible /assets/* files are gzipped once at construction
//     (gzip.BestCompression) and served from memory with Content-Encoding:
//     gzip to clients that accept it and send no Range; middleware.Compress
//     sees the encoding already set and passes them through. Range and
//     identity requests fall through to http.FileServerFS.
type SPAHandler struct {
	dist    fs.FS
	fileSrv http.Handler
	// deepLinks, in registration order, each get first claim on a request's
	// status before the known-route table decides. All non-nil (mountAndServe
	// filters nil handlers out at the call site).
	deepLinks []SPADeepLinkStatus
	// gz maps an assets/* fs name to its gzipped bytes (#0716).
	gz map[string][]byte
}

// SPADeepLinkStatus lets a live-data owner decide the HTTP status for an SPA
// deep link whose JSON API twin already answers 404/410 (#0522). Implemented
// by PublicArchiveHandler and PublicWorkshopsHandler over their own stores;
// ok=false means "this path isn't mine", and the next resolver (or the
// static route table) decides.
type SPADeepLinkStatus interface {
	SPAStatus(r *http.Request) (int, bool)
}

// NewSPAHandler constructs an SPAHandler over the embedded dist FS. The provided
// fs.FS must be rooted at the build output so index.html and assets/* live at
// its root (see web.DistFS). The resolvers are optional (#0522); each must be
// non-nil.
func NewSPAHandler(dist fs.FS, deepLinks ...SPADeepLinkStatus) *SPAHandler {
	return &SPAHandler{
		dist:      dist,
		fileSrv:   http.FileServerFS(dist),
		deepLinks: deepLinks,
		gz:        precompressAssets(dist),
	}
}

// precompressAssets gzips every compressible regular file under assets/,
// keeping only those that shrink. A missing assets/ (the committed
// placeholder dist) yields an empty map; a per-file error is logged and the
// file is served uncompressed.
func precompressAssets(dist fs.FS) map[string][]byte {
	out := map[string][]byte{}
	err := fs.WalkDir(dist, "assets", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			log.Printf("spa precompress: %s: %v", name, err)
			return nil
		}
		if !d.Type().IsRegular() || !precompressExts[path.Ext(name)] {
			return nil
		}
		raw, err := fs.ReadFile(dist, name)
		if err != nil {
			log.Printf("spa precompress: read %s: %v", name, err)
			return nil
		}
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if _, err := zw.Write(raw); err != nil {
			log.Printf("spa precompress: gzip %s: %v", name, err)
			return nil
		}
		if err := zw.Close(); err != nil {
			log.Printf("spa precompress: gzip %s: %v", name, err)
			return nil
		}
		if buf.Len() < len(raw) {
			out[name] = buf.Bytes()
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("spa precompress: %v", err)
	}
	return out
}

// ServeHTTP implements http.Handler. It serves the requested embedded file when
// it exists, otherwise it serves index.html (200 for a known route, 404 for a
// miss -- see isKnownRoute) so the SPA's client-side view routing works on a
// hard refresh of a deep link.
func (h *SPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// fs paths are relative and never start with a slash; the root request maps
	// to index.html, handled below.
	name := strings.TrimPrefix(r.URL.Path, "/")

	if name != "" && h.fileExists(name) {
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", cacheImmutable)
		}
		if gzBody, ok := h.gz[name]; ok {
			w.Header().Add("Vary", "Accept-Encoding")
			if middleware.AcceptsGzip(r) && r.Header.Get("Range") == "" {
				if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
					w.Header().Set("Content-Type", ct)
				}
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Content-Length", strconv.Itoa(len(gzBody)))
				w.WriteHeader(http.StatusOK)
				if r.Method != http.MethodHead {
					_, _ = w.Write(gzBody)
				}
				return
			}
		}
		h.fileSrv.ServeHTTP(w, r)
		return
	}

	// #0717: the SPA is split into hashed chunks under assets/. A stale tab
	// asking for a chunk from a previous build must get a 404 -- never the
	// HTML shell served as JavaScript -- so its import() rejects and the
	// SPA reloads the same URL (web/src/lib/routeViews.ts). This holds even
	// for an extensionless assets/ path. no-cache keeps the 404 from being
	// heuristically cached past the next deploy.
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", cacheShell)
		http.NotFound(w, r)
		return
	}

	// A request for a path that looks like a static asset (it has a file
	// extension, e.g. /favicon.ico) but has no matching embedded file must NOT
	// fall back to the HTML shell. Returning index.html for /favicon.ico made
	// browsers receive an HTML document for an icon URL, which they can cache as
	// a broken/garbage icon. Such paths get a clean 404 instead. SPA deep links
	// with no extension (e.g. /about, or a typo'd /aboot) still fall through to
	// index.html below -- isKnownRoute decides which status code they get.
	if path.Ext(name) != "" {
		http.NotFound(w, r)
		return
	}

	// #0522: a deep link into live data gets its status from the owner of
	// that data, not the route table — the JSON API's own 404/410 decision
	// is what a crawler should see on the HTML route too. The body stays
	// the shell in every case, so the SPA's own not-found/withheld views
	// still render on a hard refresh.
	for _, resolver := range h.deepLinks {
		if status, ok := resolver.SPAStatus(r); ok {
			h.serveIndex(w, status)
			return
		}
	}

	status := http.StatusOK
	if !isKnownRoute(r.URL.Path) {
		status = http.StatusNotFound
	}
	h.serveIndex(w, status)
}

// fileExists reports whether name resolves to a regular file in the embedded
// dist FS. Directories return false so a directory path falls through to the
// index.html SPA fallback rather than rendering a file listing.
func (h *SPAHandler) fileExists(name string) bool {
	info, err := fs.Stat(h.dist, name)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// serveIndex writes dist/index.html as the SPA shell at the given status code.
// Used for the root path and every unmatched deep link -- status is 200 for a
// path in the known-route table (routes.go) and 404 for everything else, per
// #0022.
func (h *SPAHandler) serveIndex(w http.ResponseWriter, status int) {
	data, err := fs.ReadFile(h.dist, "index.html")
	if err != nil {
		http.Error(w, "SPA index.html not found in embedded assets", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", cacheShell)
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
