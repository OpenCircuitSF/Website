package seo

// ArchiveEntry is the subset of a sent campaign's data #0019's meta
// injector and #0020's sitemap generator need for one archive page.
// Mirrors seo.Workshop's own narrowing-adapter role: the real store
// (internal/mailing.CampaignStore, #0041/#0123) returns a richer row; an
// adapter (cmd/opencircuit/campaign_archive_seo_source.go's
// campaignArchiveSEOSource) narrows to this shape.
type ArchiveEntry struct {
	Slug      string
	Subject   string
	Preheader string // "" falls back to a generic description
	UpdatedAt string // RFC 3339 date (YYYY-MM-DD is sufficient for <lastmod>); "" omits it
	Published bool   // archive_status == 'published' AND status == 'sent' (#0519 tightens this; see toSEOArchiveEntry)

	// BodyMD is the campaign's Markdown body (mailing.Campaign.BodyMD).
	// #0519's fallback.go renders it through mailing.RenderMarkdownPageHTML
	// -- the SAME call PublicArchiveHandler.GetBySlug makes (#0042) -- for
	// the server-rendered archive detail page, so the two never diverge.
	BodyMD string

	// ArchivedAt (#0519) is the campaign's archived_at as a full RFC 3339
	// UTC timestamp ("" when never archived) -- unlike UpdatedAt below, this
	// carries the time-of-day so fallback.go's archiveDateLabel can format
	// it in America/Los_Angeles rather than a UTC calendar date, matching
	// web/src/lib/archive.ts's formatArchivedDate (which formats in the
	// viewer's own zone). UpdatedAt stays a bare UTC date for Sitemap's
	// <lastmod>, which has no timezone-correctness requirement.
	ArchivedAt string
}

// ArchiveSource supplies published-campaign data to the SEO renderer
// (#0019) and sitemap generator (#0020), mirroring WorkshopSource's own
// "may be nil, callers degrade gracefully" contract — see that type's doc
// comment for the full reasoning, restated here for the archive case:
// #0123's real store was wired in from the start (unlike
// WorkshopSource's staged #0051/#0054 rollout), but keeping this an
// interface with the same nil-tolerant shape costs nothing and keeps
// internal/seo's two data sources structurally consistent.
//
// Deliberately returns ALL published entries (never draft/pending/
// withheld) — ArchiveEntries' one caller, sitemap.go's Build, still checks
// Published itself (defence in depth, matching WorkshopSource's own
// "caller re-checks Status" convention), so that exclusion is covered by
// `go test ./internal/seo/...` rather than trusted to whatever the real
// store's WHERE clause happens to do.
type ArchiveSource interface {
	// ArchiveEntryBySlug returns the entry at slug, or ok=false if no
	// campaign has that slug at all (regardless of archive_status —
	// status filtering happens in the caller, same convention as
	// WorkshopSource.WorkshopBySlug).
	ArchiveEntryBySlug(slug string) (e ArchiveEntry, ok bool, err error)
	// ArchiveEntries returns every published campaign.
	ArchiveEntries() ([]ArchiveEntry, error)
}
