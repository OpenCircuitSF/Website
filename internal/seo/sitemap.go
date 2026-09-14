package seo

import (
	"encoding/xml"
	"strings"
	"sync"
	"time"
)

// marketingRoutes is the sitemap's curated static-route portion (#0020's
// acceptance criteria: "/", "/about", "/workshops", "/subscribe"; "/privacy"
// added at #0070 for the same reason -- a real, indexable public page;
// "/archive" added at #0123 (PRD §6.8) for the same reason: the archive
// index is the site's only recurring indexable page). This is
// deliberately NOT handlers.StaticRoutes() -- that table also contains the
// Phase 1 auth/account/token routes (/login, /account, /admin, /confirm,
// /preferences, /unsubscribe, /register/verify, /recover/verify,
// /subscribe/thanks), none of which belong in a public sitemap; several
// (/confirm, /preferences, /unsubscribe) carry tokens in their query string
// and indexing them would be a token leak (#0020's Notes). Every entry here
// is still asserted to be a handlers.IsKnownRoute member in sitemap_test.go,
// so this curated list can never silently drift into advertising a URL the
// client router would 404.
//
// <lastmod> per entry (#0533): only entries whose content is derived from
// data carry one, and it is derived from that same data -- "/workshops" from
// the newest published workshop's UpdatedAt, "/archive" from the newest
// published archive entry's UpdatedAt, and "/" from whichever of those two
// sources changed most recently (Home renders from both). The purely static
// copy pages -- "/about", "/privacy", "/subscribe" -- deliberately OMIT
// <lastmod> (the sitemap protocol allows this): nothing about their content
// changes on a deploy, so no truthful date exists to state, and Google
// discounts lastmod for a site where it is not consistently accurate --
// which would also discount the genuinely accurate workshop and archive
// dates. When a data source is absent (STORAGE=json) or has no published
// rows, its derived entries are omitted too. No entry's date is ever taken
// from the clock: #0533 removed the construction-time stamp that made every
// deploy claim every static page changed.
var marketingRoutes = []string{"/", "/about", "/privacy", "/workshops", "/subscribe", "/archive"}

// urlset / sitemapURL model the sitemaps.org XML schema (only the two
// elements #0020 requires: loc and lastmod).
type urlset struct {
	XMLName xml.Name     `xml:"urlset"`
	Xmlns   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// Sitemap generates and caches sitemap.xml (#0020). Safe for concurrent use.
type Sitemap struct {
	baseURL string
	source  WorkshopSource // nilable, see WorkshopSource doc
	archive ArchiveSource  // nilable, see ArchiveSource doc (#0123)

	ttl time.Duration
	now func() time.Time

	mu      sync.RWMutex
	cached  []byte
	expires time.Time
}

// NewSitemap constructs a Sitemap over baseURL (no trailing slash) and an
// optional WorkshopSource and ArchiveSource (#0123). Nothing here reads the
// clock (#0533): the sitemap's dates are derived entirely from source data,
// so two instances built at different times render byte-identical output
// when no data changed.
func NewSitemap(baseURL string, source WorkshopSource, archive ArchiveSource) *Sitemap {
	baseURL = strings.TrimSuffix(baseURL, "/")
	return &Sitemap{
		baseURL: baseURL,
		source:  source,
		archive: archive,
		ttl:     defaultCacheTTL,
		now:     time.Now,
	}
}

// Build generates the sitemap.xml bytes from the marketing route list
// (which now includes "/archive" itself), every published workshop
// (filtering out draft/unpublished/canceled, #0020's carried-in criterion),
// and every published archive entry (#0123, filtering out pending/withheld
// — PRD §6.8: "withheld campaigns are excluded from the sitemap"). Every
// <loc> is verified against handlers.IsKnownRoute by the caller's own
// construction (marketingRoutes is a curated subset asserted in
// sitemap_test.go; workshop/archive detail paths always match their
// dynamic pattern by construction of the URL itself).
//
// The static entries' <lastmod> values are data-derived (#0533, see
// marketingRoutes' doc comment): both source lists are fetched first, the
// newest published UpdatedAt of each becomes "/workshops" and "/archive"'s
// lastmod, and the later of the two becomes "/"'s. The three purely static
// copy pages carry no lastmod at all. A source error aborts the whole
// build, as before -- a half-populated sitemap must never be served.
func (s *Sitemap) Build() ([]byte, error) {
	set := urlset{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9"}

	var publishedWorkshops []Workshop
	if s.source != nil {
		ws, err := s.source.Workshops()
		if err != nil {
			return nil, err
		}
		for _, w := range ws {
			if w.Status != WorkshopPublished {
				continue // draft, unpublished, canceled -- excluded
			}
			publishedWorkshops = append(publishedWorkshops, w)
		}
	}

	var publishedEntries []ArchiveEntry
	if s.archive != nil {
		es, err := s.archive.ArchiveEntries()
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			if !e.Published {
				continue // pending (never sent), withheld -- excluded
			}
			publishedEntries = append(publishedEntries, e)
		}
	}

	workshopsMax := ""
	for _, w := range publishedWorkshops {
		workshopsMax = laterRFC3339Date(workshopsMax, w.UpdatedAt)
	}
	archiveMax := ""
	for _, e := range publishedEntries {
		archiveMax = laterRFC3339Date(archiveMax, e.UpdatedAt)
	}

	staticLastMod := map[string]string{
		"/":          laterRFC3339Date(workshopsMax, archiveMax),
		"/about":     "",
		"/privacy":   "",
		"/workshops": workshopsMax,
		"/subscribe": "",
		"/archive":   archiveMax,
	}
	for _, path := range marketingRoutes {
		set.URLs = append(set.URLs, sitemapURL{
			Loc:     s.baseURL + path,
			LastMod: staticLastMod[path],
		})
	}

	for _, w := range publishedWorkshops {
		set.URLs = append(set.URLs, sitemapURL{
			Loc:     s.baseURL + "/workshops/" + w.Slug,
			LastMod: w.UpdatedAt,
		})
	}

	for _, e := range publishedEntries {
		set.URLs = append(set.URLs, sitemapURL{
			Loc:     s.baseURL + "/archive/" + e.Slug,
			LastMod: e.UpdatedAt,
		})
	}

	body, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}

// laterRFC3339Date returns the later of two RFC 3339 dates (or full
// timestamps), treating "" as "no date". Values that fail to parse as
// either form lose to ones that parse; two unparseable non-empty values
// fall back to lexicographic comparison so the result is at least
// deterministic. Workshop and archive UpdatedAt values are produced by this
// repo's own adapters as bare YYYY-MM-DD dates, but the sources' doc
// comments only promise "RFC 3339", so both forms are accepted.
func laterRFC3339Date(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	at, aerr := parseSitemapDate(a)
	bt, berr := parseSitemapDate(b)
	switch {
	case aerr != nil && berr != nil:
		if b > a {
			return b
		}
		return a
	case aerr != nil:
		return b
	case berr != nil:
		return a
	case bt.After(at):
		return b
	default:
		return a
	}
}

// parseSitemapDate parses the two date shapes this package's sources
// produce: a full RFC 3339 timestamp or a bare YYYY-MM-DD date.
func parseSitemapDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

// Render returns the cached sitemap.xml bytes, rebuilding when the TTL has
// expired.
func (s *Sitemap) Render() ([]byte, error) {
	s.mu.RLock()
	cached, expires := s.cached, s.expires
	s.mu.RUnlock()
	if cached != nil && s.now().Before(expires) {
		return cached, nil
	}

	body, err := s.Build()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cached = body
	s.expires = s.now().Add(s.ttl)
	s.mu.Unlock()

	return body, nil
}

// invalidate clears the cached sitemap so the next request rebuilds it.
// Called alongside Renderer.Invalidate, via Site.Invalidate, which calls
// both: originally on workshop mutation (#0051), widened by #0319 to also
// cover the admin campaign archive toggle and the send worker's own
// archive-publish transition.
//
// Deliberately unexported (#0337) -- Sitemap already had a method with this
// exact zero-parameter, zero-result signature before Site.Invalidate was
// itself named InvalidateWorkshops (#0325's rename target). Shortening that
// method's name to Invalidate moved handlers.seoCacheInvalidator and
// mailing.ArchiveCacheInvalidator (both single-method interfaces requiring
// exactly `Invalidate()`) onto *Sitemap as an accidental, structural
// satisfier: passing a bare *Sitemap into either seam went from a compile
// error to a silent half-invalidation (sitemap cache cleared, meta cache
// left stale -- the meta-cache half of #0319's staleness class, not the
// sitemap staleness #0319 led with) with no source change of its own. *Site
// is the only type meant to satisfy either interface; unexporting this
// method removes *Sitemap from contention
// without touching Renderer.Invalidate, which stays exported and remains a
// (pre-existing, harmless -- see #0337) structural satisfier of both, since
// nothing in this codebase ever passes a bare *Renderer to either seam.
func (s *Sitemap) invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}
