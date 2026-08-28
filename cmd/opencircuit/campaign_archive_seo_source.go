package main

import (
	"context"
	"errors"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/seo"
)

// campaignArchiveSEOSource adapts *mailing.CampaignStore to
// internal/seo.ArchiveSource (#0123, PRD §6.8) — the real implementation of
// the seam seo.NewSite's fourth argument takes. Constructed once in
// servePostgres and handed to buildSEOSite alongside the same
// campaignsStore the public/admin archive handlers use, so the
// renderer/sitemap caches and the archive read path see the same
// underlying data.
//
// Mirrors workshopSEOSource's own context-free construction (see that
// file's doc comment): internal/seo renders off its own TTL/
// mutation-invalidated cache, not per-request work, so context.Background()
// is safe here for the identical reason.
type campaignArchiveSEOSource struct {
	store *mailing.CampaignStore
}

// ArchiveEntryBySlug satisfies seo.ArchiveSource. ok=false (not an error)
// for an unknown slug, matching ArchiveSource's own doc comment -- status
// filtering (published-only for a social card) happens in the seo package,
// not here; this adapter returns the campaign regardless of status/
// archive_status, same as mailing.CampaignStore.GetBySlug itself.
func (s campaignArchiveSEOSource) ArchiveEntryBySlug(slug string) (seo.ArchiveEntry, bool, error) {
	c, err := s.store.GetBySlug(context.Background(), slug)
	if err != nil {
		if errors.Is(err, mailing.ErrCampaignNotFound) {
			return seo.ArchiveEntry{}, false, nil
		}
		return seo.ArchiveEntry{}, false, err
	}
	return toSEOArchiveEntry(c), true, nil
}

// ArchiveEntries satisfies seo.ArchiveSource: every PUBLISHED campaign
// (mailing.CampaignStore.ListArchived already filters to archive_status =
// 'published' at the SQL level — see that method's own doc comment), which
// is also every entry this adapter can ever return with Published=true, so
// the sitemap's own defence-in-depth filter (sitemap.go's Build) is a
// no-op in practice for this list and a real exclusion for
// ArchiveEntryBySlug's single-row lookup above.
func (s campaignArchiveSEOSource) ArchiveEntries() ([]seo.ArchiveEntry, error) {
	campaigns, err := s.store.ListArchived(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]seo.ArchiveEntry, 0, len(campaigns))
	for _, c := range campaigns {
		out = append(out, toSEOArchiveEntry(c))
	}
	return out, nil
}

// toSEOArchiveEntry narrows a mailing.Campaign row to the subset
// internal/seo needs (internal/seo/archive.go's ArchiveEntry doc comment).
// UpdatedAt is formatted as a bare RFC 3339 date (YYYY-MM-DD) from
// ArchivedAt -- Sitemap's <lastmod> doc comment says that precision is
// sufficient, and a nil ArchivedAt (a campaign that has never been sent)
// renders as "" so Sitemap omits <lastmod> entirely, same as
// toSEOWorkshop's identical zero-time handling.
func toSEOArchiveEntry(c mailing.Campaign) seo.ArchiveEntry {
	var preheader string
	if c.Preheader != nil {
		preheader = *c.Preheader
	}
	var updatedAt string
	if c.ArchivedAt != nil {
		updatedAt = c.ArchivedAt.UTC().Format("2006-01-02")
	}
	return seo.ArchiveEntry{
		Slug:      c.Slug,
		Subject:   c.Subject,
		Preheader: preheader,
		UpdatedAt: updatedAt,
		Published: c.ArchiveStatus == mailing.ArchiveStatusPublished,
	}
}
