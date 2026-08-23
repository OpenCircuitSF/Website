package main

import (
	"context"
	"errors"

	"github.com/brennanMKE/OpenCircuitSF/internal/seo"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// workshopSEOSource adapts *workshops.Store to internal/seo.WorkshopSource
// (#0054, carried in from #0051's review Ruling 1): the real implementation
// of the seam seo.NewSite's third argument has taken as a literal nil since
// #0051 landed. Constructed once in servePostgres and handed to buildSEOSite
// alongside the same workshopsStore AdminWorkshopsHandler uses, so the
// renderer/sitemap caches and the admin CRUD path read the same underlying
// data.
//
// WorkshopSource's two methods deliberately take no context.Context (see
// that interface's doc comment in internal/seo/workshop.go) -- this mirrors
// Sitemap.Build's own context-free construction: internal/seo renders off
// its own TTL/mutation-invalidated cache, not per-request work, so there is
// no request context to thread through here. context.Background() is safe:
// both underlying store reads are single indexed queries (GetBySlug's unique
// index on slug, List's primary-key scan over a catalog PRD sizes in the
// tens of rows -- CLAUDE.md §5, this project has no performance requirement)
// and are themselves shielded from repeated execution by internal/seo's own
// cache, not called on every request.
type workshopSEOSource struct {
	store *workshops.Store
}

// WorkshopBySlug satisfies seo.WorkshopSource. ok=false (not an error) for
// an unknown slug, matching WorkshopSource's own doc comment ("no workshop
// has that slug at all") -- status filtering (published-only for a social
// card, per WorkshopStatus's doc comment) happens in the seo package, not
// here; this adapter returns the workshop regardless of status, same as
// workshops.Store.GetBySlug itself.
func (s workshopSEOSource) WorkshopBySlug(slug string) (seo.Workshop, bool, error) {
	w, err := s.store.GetBySlug(context.Background(), slug)
	if err != nil {
		if errors.Is(err, workshops.ErrNotFound) {
			return seo.Workshop{}, false, nil
		}
		return seo.Workshop{}, false, err
	}
	return toSEOWorkshop(w), true, nil
}

// Workshops satisfies seo.WorkshopSource: every workshop, any status --
// filtering to published-only for the sitemap is internal/seo's own job
// (sitemap.go's Build), not this adapter's.
func (s workshopSEOSource) Workshops() ([]seo.Workshop, error) {
	ws, err := s.store.List(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]seo.Workshop, 0, len(ws))
	for _, w := range ws {
		out = append(out, toSEOWorkshop(w))
	}
	return out, nil
}

// toSEOWorkshop narrows a workshops.Workshop row to the subset
// internal/seo needs (internal/seo/workshop.go's Workshop doc comment: "an
// adapter there narrows to this shape"). UpdatedAt is formatted as a bare
// RFC 3339 date (YYYY-MM-DD) -- Sitemap's <lastmod> doc comment says that
// precision is sufficient, and a zero time.Time (never expected from a real
// row, but defensive) renders as "" so Sitemap omits <lastmod> entirely
// rather than emitting a bogus 0001-01-01.
func toSEOWorkshop(w workshops.Workshop) seo.Workshop {
	var cover, summary string
	if w.CoverImage != nil {
		cover = *w.CoverImage
	}
	if w.Summary != nil {
		summary = *w.Summary
	}
	var updatedAt string
	if !w.UpdatedAt.IsZero() {
		updatedAt = w.UpdatedAt.UTC().Format("2006-01-02")
	}
	return seo.Workshop{
		Slug:       w.Slug,
		Title:      w.Title,
		Summary:    summary,
		CoverImage: cover,
		Status:     seo.WorkshopStatus(w.Status),
		UpdatedAt:  updatedAt,
	}
}
