package guideposts

import (
	"context"
	"fmt"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// EngagementTargets returns the posted guide posts on the given channels that
// have a remote id and were posted within the last thirty days, with the time
// of their latest snapshot (#0704). It mirrors outreach.Store's method, so a
// *Store is an outreach.EngagementSource; whether each is due is
// outreach.EngagementDue's call.
func (s *Store) EngagementTargets(ctx context.Context, channels []string) ([]outreach.EngagementTarget, error) {
	if len(channels) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.channel, p.remote_id, p.posted_at, max(e.captured_at)
		FROM guide_posts p LEFT JOIN guide_post_engagement e ON e.post_id = p.id
		WHERE p.status = 'posted' AND p.remote_id IS NOT NULL AND p.posted_at IS NOT NULL
		  AND p.posted_at > now() - interval '30 days' AND p.channel = ANY($1)
		GROUP BY p.id ORDER BY p.id`, channels)
	if err != nil {
		return nil, fmt.Errorf("guideposts: engagement targets: %w", err)
	}
	defer rows.Close()
	var out []outreach.EngagementTarget
	for rows.Next() {
		var t outreach.EngagementTarget
		if err := rows.Scan(&t.PostID, &t.Channel, &t.RemoteID, &t.PostedAt, &t.LastCaptured); err != nil {
			return nil, fmt.Errorf("guideposts: scan engagement target: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RecordEngagement stores one snapshot, even when the counts are unchanged:
// the latest capture time is what the due check reads.
func (s *Store) RecordEngagement(ctx context.Context, postID int64, at time.Time, c outreach.Counts) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO guide_post_engagement (post_id, captured_at, likes, reposts, replies, quotes)
		VALUES ($1, $2, $3, $4, $5, $6)`, postID, at, c.Likes, c.Reposts, c.Replies, c.Quotes)
	if err != nil {
		return fmt.Errorf("guideposts: record engagement: %w", err)
	}
	return nil
}

// LatestEngagement returns each given guide post's newest snapshot; posts with
// none are absent.
func (s *Store) LatestEngagement(ctx context.Context, postIDs []int64) (map[int64]outreach.Engagement, error) {
	out := map[int64]outreach.Engagement{}
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (post_id) post_id, captured_at, likes, reposts, replies, quotes
		FROM guide_post_engagement WHERE post_id = ANY($1) ORDER BY post_id, captured_at DESC, id DESC`, postIDs)
	if err != nil {
		return nil, fmt.Errorf("guideposts: latest engagement: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var e outreach.Engagement
		if err := rows.Scan(&id, &e.CapturedAt, &e.Likes, &e.Reposts, &e.Replies, &e.Quotes); err != nil {
			return nil, fmt.Errorf("guideposts: scan engagement: %w", err)
		}
		out[id] = e
	}
	return out, rows.Err()
}

var _ outreach.EngagementSource = (*Store)(nil)
