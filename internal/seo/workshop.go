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

// Workshop is the subset of workshop data #0019's meta injector and #0020's
// sitemap generator need. #0051's real store returns richer rows; an
// adapter there narrows to this shape.
type Workshop struct {
	Slug       string
	Title      string
	Summary    string
	CoverImage string // root-relative path or absolute URL; "" falls back to the default OG image
	Status     WorkshopStatus
	UpdatedAt  string // RFC 3339 date (YYYY-MM-DD is sufficient for <lastmod>); "" omits <lastmod>
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
