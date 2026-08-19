package seo

import (
	"encoding/xml"
	"strings"
	"sync"
	"time"
)

// marketingRoutes is the sitemap's curated static-route portion (#0020's
// acceptance criteria: "/", "/about", "/workshops", "/subscribe"). This is
// deliberately NOT handlers.StaticRoutes() -- that table also contains the
// Phase 1 auth/account/token routes (/login, /account, /admin, /confirm,
// /preferences, /unsubscribe, /register/verify, /recover/verify,
// /subscribe/thanks), none of which belong in a public sitemap; several
// (/confirm, /preferences, /unsubscribe) carry tokens in their query string
// and indexing them would be a token leak (#0020's Notes). Every entry here
// is still asserted to be a handlers.IsKnownRoute member in sitemap_test.go,
// so this curated list can never silently drift into advertising a URL the
// client router would 404.
var marketingRoutes = []string{"/", "/about", "/workshops", "/subscribe"}

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
	built   string         // "today" in RFC 3339 date form, used as the static routes' <lastmod>

	ttl time.Duration
	now func() time.Time

	mu      sync.RWMutex
	cached  []byte
	expires time.Time
}

// NewSitemap constructs a Sitemap over baseURL (no trailing slash) and an
// optional WorkshopSource.
func NewSitemap(baseURL string, source WorkshopSource) *Sitemap {
	baseURL = strings.TrimSuffix(baseURL, "/")
	return &Sitemap{
		baseURL: baseURL,
		source:  source,
		built:   time.Now().UTC().Format("2006-01-02"),
		ttl:     defaultCacheTTL,
		now:     time.Now,
	}
}

// Build generates the sitemap.xml bytes from the marketing route list plus
// every published workshop, filtering out draft/unpublished/canceled
// workshops (#0020's carried-in criterion). Every <loc> is verified against
// handlers.IsKnownRoute by the caller's own construction (marketingRoutes is
// a curated subset asserted in sitemap_test.go; workshop detail paths always
// match the dynamic pattern by construction of the URL itself).
func (s *Sitemap) Build() ([]byte, error) {
	set := urlset{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9"}

	for _, path := range marketingRoutes {
		set.URLs = append(set.URLs, sitemapURL{
			Loc:     s.baseURL + path,
			LastMod: s.built,
		})
	}

	if s.source != nil {
		workshops, err := s.source.Workshops()
		if err != nil {
			return nil, err
		}
		for _, w := range workshops {
			if w.Status != WorkshopPublished {
				continue // draft, unpublished, canceled -- excluded
			}
			set.URLs = append(set.URLs, sitemapURL{
				Loc:     s.baseURL + "/workshops/" + w.Slug,
				LastMod: w.UpdatedAt,
			})
		}
	}

	body, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
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

// Invalidate clears the cached sitemap so the next request rebuilds it.
// Called on workshop mutation (#0051), same trigger as
// Renderer.InvalidateWorkshops.
func (s *Sitemap) Invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}
