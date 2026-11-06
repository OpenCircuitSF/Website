package outreach

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// NewsletterLink is one row of outreach_newsletter_links (#0643): the
// ShortLink made for one event in one newsletter issue, inside that event's
// Outreach ShortLinks campaign.
type NewsletterLink struct {
	ID              int64
	EmailCampaignID int64
	WorkshopID      int64
	CampaignSlug    string
	Key             string
	ShortURL        string
	CreatedAt       time.Time
}

const newsletterLinkCols = `id, email_campaign_id, workshop_id, campaign_slug, short_link_key, short_url, created_at`

func scanNewsletterLink(row pgx.Row) (NewsletterLink, error) {
	var l NewsletterLink
	err := row.Scan(&l.ID, &l.EmailCampaignID, &l.WorkshopID, &l.CampaignSlug, &l.Key, &l.ShortURL, &l.CreatedAt)
	return l, err
}

// GetNewsletterLink returns the stored link for the pair, or ErrNotFound.
func (s *Store) GetNewsletterLink(ctx context.Context, emailCampaignID, workshopID int64) (NewsletterLink, error) {
	l, err := scanNewsletterLink(s.pool.QueryRow(ctx,
		`SELECT `+newsletterLinkCols+` FROM outreach_newsletter_links WHERE email_campaign_id = $1 AND workshop_id = $2`,
		emailCampaignID, workshopID))
	if errors.Is(err, pgx.ErrNoRows) {
		return NewsletterLink{}, ErrNotFound
	}
	if err != nil {
		return NewsletterLink{}, fmt.Errorf("outreach: get newsletter link: %w", err)
	}
	return l, nil
}

// InsertNewsletterLink records the pair's link. When the pair already has one
// the existing row is returned with created=false.
func (s *Store) InsertNewsletterLink(ctx context.Context, emailCampaignID, workshopID int64, campaignSlug, key, shortURL string) (NewsletterLink, bool, error) {
	l, err := scanNewsletterLink(s.pool.QueryRow(ctx, `
		WITH k AS (
		  INSERT INTO outreach_newsletter_links (email_campaign_id, workshop_id, campaign_slug, short_link_key, short_url)
		  VALUES ($1, $2, $3, $4, $5)
		  ON CONFLICT ON CONSTRAINT outreach_newsletter_links_unique DO NOTHING
		  RETURNING *)
		SELECT `+newsletterLinkCols+` FROM k`, emailCampaignID, workshopID, campaignSlug, key, shortURL))
	if err == nil {
		return l, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return NewsletterLink{}, false, fmt.Errorf("outreach: insert newsletter link: %w", err)
	}
	l, err = s.GetNewsletterLink(ctx, emailCampaignID, workshopID)
	return l, false, err
}
