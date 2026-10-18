package outreach

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PostLink is one row of outreach_links (#0568): the tracked short link for
// one (outreach, channel, wave) slot, created before any post exists.
type PostLink struct {
	ID         int64
	OutreachID int64
	Channel    string
	Wave       string
	Key        string
	ShortURL   string
	CreatedAt  time.Time
}

const postLinkCols = `id, outreach_id, channel, wave, short_link_key, short_url, created_at`

func scanPostLink(row pgx.Row) (PostLink, error) {
	var l PostLink
	err := row.Scan(&l.ID, &l.OutreachID, &l.Channel, &l.Wave, &l.Key, &l.ShortURL, &l.CreatedAt)
	return l, err
}

// ListPostLinks returns an outreach's slot links in creation order.
func (s *Store) ListPostLinks(ctx context.Context, outreachID int64) ([]PostLink, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+postLinkCols+` FROM outreach_links WHERE outreach_id = $1 ORDER BY id`, outreachID)
	if err != nil {
		return nil, fmt.Errorf("outreach: list post links: %w", err)
	}
	defer rows.Close()
	out := []PostLink{}
	for rows.Next() {
		l, err := scanPostLink(rows)
		if err != nil {
			return nil, fmt.Errorf("outreach: scan post link: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetPostLink returns the slot's link, or ErrNotFound.
func (s *Store) GetPostLink(ctx context.Context, outreachID int64, channel, wave string) (PostLink, error) {
	l, err := scanPostLink(s.pool.QueryRow(ctx,
		`SELECT `+postLinkCols+` FROM outreach_links WHERE outreach_id = $1 AND channel = $2 AND wave = $3`, outreachID, channel, wave))
	if errors.Is(err, pgx.ErrNoRows) {
		return PostLink{}, ErrNotFound
	}
	if err != nil {
		return PostLink{}, fmt.Errorf("outreach: get post link: %w", err)
	}
	return l, nil
}

// InsertPostLink records the slot's link. When the slot already has one the
// existing row is returned with created=false.
func (s *Store) InsertPostLink(ctx context.Context, outreachID int64, channel, wave, key, shortURL string) (PostLink, bool, error) {
	l, err := scanPostLink(s.pool.QueryRow(ctx, `
		WITH k AS (
		  INSERT INTO outreach_links (outreach_id, channel, wave, short_link_key, short_url)
		  VALUES ($1, $2, $3, $4, $5)
		  ON CONFLICT ON CONSTRAINT outreach_links_slot_unique DO NOTHING
		  RETURNING *)
		SELECT `+postLinkCols+` FROM k`, outreachID, channel, wave, key, shortURL))
	if err == nil {
		return l, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PostLink{}, false, fmt.Errorf("outreach: insert post link: %w", err)
	}
	l, err = s.GetPostLink(ctx, outreachID, channel, wave)
	return l, false, err
}
