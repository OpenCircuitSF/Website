package seo

import (
	"encoding/xml"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// #0531: an Atom feed of the newsletter archive at GET /archive/feed.xml.
//
// # Atom, not RSS 2.0
//
// Criterion 1 names the choice and asks it be recorded: Atom's date format
// (RFC 3339, which archived_at already is) and entry IDs (an IRI that must
// be stable and permanent) are strictly specified, where RSS 2.0 leaves
// both loose -- the two things a permanent archive most needs to get
// right. This mirrors the sitemap's own use of RFC 3339 dates.
//
// # What is in the feed
//
// Published campaigns only -- the same Published predicate the sitemap
// (sitemap.go's Build), the archive index page (fallback.go's
// buildArchiveIndexItems), and the archive JSON API apply, re-checked here
// as defence in depth exactly as sitemap.go does. Entries are capped at
// feedMaxEntries, newest first by archived_at (a stable sort, so the
// feed's ordering claim holds by construction rather than by store
// convention). Nothing private ever enters one: the entry fields are the
// slug, subject, archived_at, #0528's description, and the same
// RenderMarkdownPageHTML body the archive page shows -- no recipients, no
// tokens, no internal IDs (#0531 criterion 6), because ArchiveSource
// carries nothing else.

// feedMaxEntries caps the feed's entry count. A stated number (criterion
// 2), not "everything": the archive is permanent and grows one entry per
// send, and a feed reader only needs the recent window -- the full history
// is what the archive index itself is for.
const feedMaxEntries = 20

// atomFeed models the RFC 4287 feed element. XMLName/Xmlns emit
// <feed xmlns="http://www.w3.org/2005/Atom">; Updated is the newest
// entry's archived_at and is omitted only for an archive with zero
// published entries -- a degenerate empty-archive feed with no
// data-derived date to state (RFC 4287 requires the element; #0533's
// no-clock-dates rule means an empty archive serves none rather than
// stamping the build).
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Xmlns   string      `xml:"xmlns,attr"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Links   []atomLink  `xml:"link"`
	Author  atomAuthor  `xml:"author"`
	Updated string      `xml:"updated,omitempty"`
	Entries []atomEntry `xml:"entry"`
}

// atomLink is one Atom link element.
type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr,omitempty"`
}

// atomAuthor is the feed-level author, inherited by every entry (RFC 4287
// 4.2.1: an entry without its own author falls back to the feed's).
type atomAuthor struct {
	Name string `xml:"name"`
}

// atomEntry is one archive entry. ID is the entry's canonical archive URL
// -- the permanent, per-slug IRI the criterion requires (a campaign's slug
// never changes, so neither does this, no matter how the campaign is
// edited). Published and Updated are both archived_at: the archive date is
// when the issue became public, and there is no other data-derived
// revision date to claim. Summary is #0528's description text; Content is
// the same RenderMarkdownPageHTML body the archive page shows, as Atom
// type="html" (escaped HTML text) -- encoding/xml escapes the chardata
// itself, which is what makes a hostile subject or body incapable of
// breaking the document (criterion 7).
type atomEntry struct {
	Title     string       `xml:"title"`
	ID        string       `xml:"id"`
	Link      atomLink     `xml:"link"`
	Published string       `xml:"published"`
	Updated   string       `xml:"updated"`
	Summary   string       `xml:"summary"`
	Content   *atomContent `xml:"content,omitempty"`
}

// atomContent is the full newsletter, type="html" (escaped inline HTML
// text, the RFC 4287 form for HTML payloads) rather than type="xhtml",
// which would demand a namespace-correct div wrapper around markup that
// already renders correctly as escaped text in every reader.
type atomContent struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

// Feed generates and caches the archive's Atom feed (#0531). Safe for
// concurrent use; mirrors Sitemap's TTL-cache shape so it invalidates the
// same way.
type Feed struct {
	baseURL string
	archive ArchiveSource // nilable, see ArchiveSource doc

	ttl time.Duration
	now func() time.Time

	mu      sync.RWMutex
	cached  []byte
	expires time.Time
}

// NewFeed constructs a Feed over baseURL (no trailing slash) and an
// optional ArchiveSource -- nil only under STORAGE=json, which has no
// email_campaigns-table backing, yielding a feed with title, id, and links
// but no entries (mirroring the sitemap's own nil-source posture).
func NewFeed(baseURL string, archive ArchiveSource) *Feed {
	baseURL = strings.TrimSuffix(baseURL, "/")
	return &Feed{
		baseURL: baseURL,
		archive: archive,
		ttl:     defaultCacheTTL,
		now:     time.Now,
	}
}

// Build generates the feed XML from the archive source's published
// entries, newest first, capped at feedMaxEntries. A source error aborts
// the whole build -- a half-populated feed must never be served, matching
// Sitemap.Build's own posture.
func (f *Feed) Build() ([]byte, error) {
	feed := atomFeed{
		Xmlns:   "http://www.w3.org/2005/Atom",
		Title:   "Open Circuit SF newsletter",
		ID:      f.baseURL + "/archive/feed.xml",
		Links:   []atomLink{{Rel: "self", Href: f.baseURL + "/archive/feed.xml", Type: "application/atom+xml"}, {Rel: "alternate", Href: f.baseURL + "/archive", Type: "text/html"}},
		Author:  atomAuthor{Name: "Open Circuit SF"},
		Entries: []atomEntry{},
	}

	var entries []ArchiveEntry
	if f.archive != nil {
		all, err := f.archive.ArchiveEntries()
		if err != nil {
			return nil, err
		}
		for _, e := range all {
			if !e.Published {
				continue // pending (never sent), withheld -- excluded
			}
			entries = append(entries, e)
		}
	}

	// Newest first by archived_at, by construction (criterion 2). Stable
	// sort: equal timestamps keep the source's own order, and
	// empty/unparseable dates -- which a published campaign never has, the
	// archive-publish transition writes archived_at -- sort last as zero
	// time rather than crashing the comparison.
	sort.SliceStable(entries, func(i, j int) bool {
		ti, tj := parseFeedTime(entries[i].ArchivedAt), parseFeedTime(entries[j].ArchivedAt)
		return ti.After(tj)
	})

	for _, e := range entries {
		if len(feed.Entries) >= feedMaxEntries {
			break
		}
		// archived_at is RFC 3339 from the adapter; the re-format through
		// UTC normalizes any offset-bearing form to a Z-suffixed stamp,
		// which RFC 4287's date construct wants.
		archived := e.ArchivedAt
		if t := parseFeedTime(e.ArchivedAt); !t.IsZero() {
			archived = t.UTC().Format(time.RFC3339)
		}
		feed.Entries = append(feed.Entries, atomEntry{
			Title:     e.Subject,
			ID:        f.baseURL + "/archive/" + e.Slug,
			Link:      atomLink{Href: f.baseURL + "/archive/" + e.Slug, Type: "text/html"},
			Published: archived,
			Updated:   archived,
			Summary:   archiveDescription(e),
			Content:   feedContent(e),
		})
	}

	if len(feed.Entries) > 0 {
		feed.Updated = feed.Entries[0].Updated
	}

	body, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}

// feedContent renders e's body through mailing.RenderMarkdownPageHTML --
// the same web renderer the archive page uses (#0042, #0519), never the
// email pipeline -- for the entry's Atom content. A render error omits the
// content element rather than failing the whole feed or emitting a
// fabricated payload; the entry keeps its title, summary, and link.
func feedContent(e ArchiveEntry) *atomContent {
	if strings.TrimSpace(e.BodyMD) == "" {
		return nil
	}
	rendered, err := mailing.RenderMarkdownPageHTML(e.BodyMD)
	if err != nil {
		return nil
	}
	return &atomContent{Type: "html", Body: rendered}
}

// parseFeedTime parses an RFC 3339 timestamp, returning zero time for
// empty or unparseable input so the sort above stays total and
// deterministic.
func parseFeedTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Render returns the cached feed bytes, rebuilding when the TTL has
// expired -- Sitemap.Render's shape, so the two dynamic XML documents
// behave identically.
func (f *Feed) Render() ([]byte, error) {
	f.mu.RLock()
	cached, expires := f.cached, f.expires
	f.mu.RUnlock()
	if cached != nil && f.now().Before(expires) {
		return cached, nil
	}

	body, err := f.Build()
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.cached = body
	f.expires = f.now().Add(f.ttl)
	f.mu.Unlock()

	return body, nil
}

// invalidate clears the cached feed so the next request rebuilds it.
// Deliberately unexported, mirroring Sitemap.invalidate (#0337): an
// exported Invalidate() here would structurally satisfy
// handlers.seoCacheInvalidator and mailing.ArchiveCacheInvalidator, both
// single-method interfaces requiring exactly that shape -- see
// invalidator_satisfier_guard_test.go's TestInvalidatorSatisfierSet.
// Withholding a campaign or publishing a new one reaches this through the
// same Site.Invalidate call that clears the meta, sitemap, and card
// caches (#0319's callers), which is what makes the feed drop a withheld
// campaign immediately (criterion 5).
func (f *Feed) invalidate() {
	f.mu.Lock()
	f.cached = nil
	f.mu.Unlock()
}
