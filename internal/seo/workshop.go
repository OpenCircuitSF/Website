package seo

// WorkshopStatus is a workshop's publication state, as tracked by #0051's
// workshops store. Only WorkshopPublished workshops are ever surfaced in a
// social preview card (#0019) or the sitemap (#0020) -- draft, unpublished,
// and canceled workshops must not leak into either.
type WorkshopStatus string

const (
	WorkshopDraft       WorkshopStatus = "draft"
	WorkshopPublished   WorkshopStatus = "published"
	WorkshopUnpublished WorkshopStatus = "unpublished"
	WorkshopCanceled    WorkshopStatus = "canceled"
)

// Workshop is the subset of workshop data #0019's meta injector, #0020's
// sitemap generator, and #0055's JSON-LD Event builder need. #0051's real
// store returns richer rows; an adapter there narrows to this shape.
type Workshop struct {
	Slug       string
	Title      string
	Summary    string
	CoverImage string // root-relative path or absolute URL; "" falls back to the default OG image
	Status     WorkshopStatus
	UpdatedAt  string // RFC 3339 date (YYYY-MM-DD is sufficient for <lastmod>); "" omits <lastmod>

	// StartsAt / EndsAt back #0055's JSON-LD startDate/endDate. Full RFC 3339
	// timestamps WITH a UTC offset (Go's time.RFC3339, e.g.
	// "2026-09-12T18:00:00Z") -- schema.org/Google's Rich Results validator
	// wants an offset, a bare date is not enough. "" means "not yet
	// scheduled"; buildEvent (jsonld.go) treats a missing StartsAt as "not
	// enough data for a valid Event" and emits nothing rather than a
	// fabricated date.
	StartsAt string
	EndsAt   string

	// LocationName / LocationAddress back #0055's JSON-LD location (a
	// schema.org Place). Both are independently optional in the source data;
	// jsonld.go's eventLocation treats having NEITHER set as "no real venue
	// yet", which -- like a missing StartsAt -- is treated as not enough data
	// for a valid physical Event rather than papering over the gap with a
	// placeholder like "Location TBA" (the UI's own copy for this case,
	// web/src/lib/workshops.ts's workshopLocationLabel -- fine for a human
	// reader, not a real address a search engine's structured-data validator
	// will accept).
	LocationName    string
	LocationAddress string
}

// WorkshopSource supplies workshop data to the SEO renderer (#0019) and
// sitemap generator (#0020). #0051 (workshops CRUD API/store) has not landed
// yet, so both Renderer and Sitemap accept a nil source and degrade
// gracefully: workshop-detail pages fall back to generic default metadata,
// and the sitemap's workshop portion is simply empty (per #0020's Notes:
// "Until #0051 lands, the workshop portion of the sitemap can be empty").
// #0054 is expected to wire the real implementation in.
//
// Deliberately returns ALL workshops (every status), not pre-filtered to
// published -- the draft/unpublished/canceled exclusion is asserted inside
// this package (sitemap.go, seo.go) so it is covered by
// `go test ./internal/seo/...` per both issues' acceptance criteria, rather
// than trusted to whatever the real store's WHERE clause happens to do.
type WorkshopSource interface {
	// WorkshopBySlug returns the workshop at slug, or ok=false if no
	// workshop has that slug at all (regardless of status -- status
	// filtering happens in the caller).
	WorkshopBySlug(slug string) (w Workshop, ok bool, err error)
	// Workshops returns every workshop, of any status.
	Workshops() ([]Workshop, error)
}
