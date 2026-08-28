// Public campaign archive read/write paths (#0123, migration 000025, PRD
// §6.8): every SENT campaign is also a permanent public web page at
// /archive/{slug}. This file owns the three archive-specific queries over
// email_campaigns that campaigns.go's CampaignStore doesn't already cover:
//
//   - GetBySlug: the lookup internal/handlers' public archive handler needs
//     to answer GET /api/archive/{slug} — deliberately returns the FULL row
//     (any status, any archive_status), never pre-filtered, because the
//     handler's own 404-vs-410-vs-404 rule (PRD §6.8's table) depends on
//     seeing the true state rather than a filtered absence. This mirrors
//     workshops.Store.GetBySlug's own "any status; caller decides
//     visibility" contract.
//   - ListArchived: the reverse-chronological index for GET /api/archive,
//     pre-filtered to archive_status = 'published' at the SQL level (never
//     "select everything and filter in Go") so a bug in the handler's own
//     filtering can't leak a pending or withheld campaign into the public
//     index the way it could if the filter were just defence in depth.
//   - SetArchiveStatus: the admin lever, PATCH /admin/campaigns/{id}/archive
//     — toggles published/withheld. Refuses (ErrArchiveStatusNotEditable)
//     while the campaign is still 'pending' (i.e. never sent): there is no
//     archive page yet for an admin to withhold, and PRD §6.8's table only
//     ever shows this transition starting from 'published'.
//
// Privacy (PRD §6.8's own "Privacy" paragraph, restated in this issue's
// acceptance criteria): every query in this file selects ONLY
// email_campaigns columns — campaignColumns, the exact same column list
// every other read in this package uses. None of them ever joins
// email_sends, so a per-recipient row, a recipient count, or a
// manage_token can never reach this file's callers even by accident of a
// future JOIN.
package mailing

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrArchiveStatusNotEditable is returned by SetArchiveStatus when the
// campaign's current archive_status is 'pending' — the campaign has never
// been sent, so there is no published archive page to withhold or
// re-publish. See this file's package doc comment.
var ErrArchiveStatusNotEditable = errors.New("mailing: campaign has not been archived yet")

// ErrUnknownArchiveStatus guards SetArchiveStatus against a target value
// outside {published, withheld} — defence in depth behind migration
// 000025's archive_status CHECK, mirroring ErrUnknownAudienceMode's role
// for audience_mode.
var ErrUnknownArchiveStatus = errors.New("mailing: unknown archive status")

// GetBySlug loads a single campaign by its permanent slug, any status, any
// archive_status. Returns ErrCampaignNotFound when no row matches — the
// public archive handler (internal/handlers/public_archive.go) is the sole
// caller and applies PRD §6.8's visibility rule (404 unless sent; 410 if
// withheld) itself, over the raw row this returns.
func (s *CampaignStore) GetBySlug(ctx context.Context, slug string) (Campaign, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+campaignColumns+` FROM email_campaigns WHERE slug = $1`, slug)
	c, err := scanCampaign(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Campaign{}, ErrCampaignNotFound
	case err != nil:
		return Campaign{}, fmt.Errorf("mailing: getting campaign by slug %q: %w", slug, err)
	}
	ids, err := s.interestIDs(ctx, c.ID)
	if err != nil {
		return Campaign{}, err
	}
	c.InterestIDs = ids
	return c, nil
}

// ListArchived returns every campaign with archive_status = 'published',
// reverse chronological by archived_at (the acceptance criterion's exact
// ordering) — the source for GET /api/archive and internal/seo's sitemap
// archive entries. Filtered at the SQL level, not in Go — see the package
// doc comment.
func (s *CampaignStore) ListArchived(ctx context.Context) ([]Campaign, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+campaignColumns+` FROM email_campaigns
		  WHERE archive_status = $1
		  ORDER BY archived_at DESC, id DESC`,
		ArchiveStatusPublished,
	)
	if err != nil {
		return nil, fmt.Errorf("mailing: listing archived campaigns: %w", err)
	}
	defer rows.Close()
	var campaigns []Campaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, fmt.Errorf("mailing: scanning archived campaign row: %w", err)
		}
		campaigns = append(campaigns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mailing: iterating archived campaign rows: %w", err)
	}
	return campaigns, nil
}

// SetArchiveStatus transitions a campaign's archive_status between
// 'published' and 'withheld' — the admin lever behind
// PATCH /admin/campaigns/{id}/archive. Refuses ErrUnknownArchiveStatus for
// any target other than those two (a caller can never set 'pending'
// through this method — that value only ever comes from the column
// default and the worker's own sent-transition write, worker_store.go's
// CompleteIfDone) and ErrArchiveStatusNotEditable when the campaign's
// CURRENT archive_status is still 'pending' (never sent — see this file's
// package doc comment). archived_at is left untouched by a withhold: it
// keeps recording when the page first went live, per Campaign.ArchivedAt's
// own doc comment.
func (s *CampaignStore) SetArchiveStatus(ctx context.Context, id int64, status string) (Campaign, error) {
	if status != ArchiveStatusPublished && status != ArchiveStatusWithheld {
		return Campaign{}, ErrUnknownArchiveStatus
	}

	row := s.pool.QueryRow(ctx,
		`UPDATE email_campaigns
		    SET archive_status = $2, updated_at = now()
		  WHERE id = $1 AND archive_status IN ($3, $4)
		  RETURNING `+campaignColumns,
		id, status, ArchiveStatusPublished, ArchiveStatusWithheld,
	)
	updated, err := scanCampaign(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Distinguish "no such campaign" (404) from "exists but not
		// archived yet" (409) — the UPDATE alone can't tell them apart,
		// mirroring Cancel's identical two-step disambiguation above.
		switch _, gerr := s.GetByID(ctx, id); {
		case errors.Is(gerr, ErrCampaignNotFound):
			return Campaign{}, ErrCampaignNotFound
		case gerr != nil:
			return Campaign{}, gerr
		default:
			return Campaign{}, ErrArchiveStatusNotEditable
		}
	case err != nil:
		return Campaign{}, fmt.Errorf("mailing: setting archive status for campaign %d: %w", id, err)
	}
	ids, err := s.interestIDs(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	updated.InterestIDs = ids
	return updated, nil
}
