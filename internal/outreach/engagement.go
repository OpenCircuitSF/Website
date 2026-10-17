package outreach

import (
	"context"
	"fmt"
	"time"
)

// Engagement polling windows (#0547). A post is captured hourly for its first
// week and daily to thirty days, then never again. The five-minute slack keeps
// a fifteen-minute tick from stretching an hourly capture to seventy-five.
const (
	engagementHourlyFor = 7 * 24 * time.Hour
	engagementStopAfter = 30 * 24 * time.Hour
	hourlyGap           = 55 * time.Minute
	dailyGap            = 23*time.Hour + 55*time.Minute
)

// EngagementDue reports whether a post posted at postedAt is due a capture at
// now, given the time of its latest capture (nil when it has none).
func EngagementDue(postedAt time.Time, last *time.Time, now time.Time) bool {
	age := now.Sub(postedAt)
	if age >= engagementStopAfter {
		return false
	}
	if last == nil {
		return true
	}
	if age < engagementHourlyFor {
		return now.Sub(*last) >= hourlyGap
	}
	return now.Sub(*last) >= dailyGap
}

// Counts are the four engagement numbers of one post.
type Counts struct{ Likes, Reposts, Replies, Quotes int }

// Add returns the field-wise sum.
func (c Counts) Add(o Counts) Counts {
	return Counts{c.Likes + o.Likes, c.Reposts + o.Reposts, c.Replies + o.Replies, c.Quotes + o.Quotes}
}

// Engagement is one captured snapshot.
type Engagement struct {
	Counts
	CapturedAt time.Time
}

// EngagementTarget is a posted, remotely identified post the tracker may
// capture.
type EngagementTarget struct {
	PostID       int64
	Channel      string
	RemoteID     string
	PostedAt     time.Time
	LastCaptured *time.Time
}

// Mention is a mention, reply or quote of one of our accounts. Everything but
// the ids and timestamps is third-party content.
type Mention struct {
	ID                                    int64
	Channel, RemoteID, Kind, Author, Text string
	URL                                   *string
	PostID                                *int64
	CreatedAt, FetchedAt                  time.Time
	// Filled by the list queries only.
	Wave          *string
	WorkshopID    *int64
	WorkshopTitle *string
}

// EngagementTargets returns the posted posts on the given channels that have a
// remote id and were posted within the last thirty days, with the time of
// their latest snapshot. Whether each is due is EngagementDue's call.
func (s *Store) EngagementTargets(ctx context.Context, channels []string) ([]EngagementTarget, error) {
	if len(channels) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.channel, p.remote_id, p.posted_at, max(e.captured_at)
		FROM outreach_posts p LEFT JOIN outreach_engagement e ON e.post_id = p.id
		WHERE p.status = 'posted' AND p.remote_id IS NOT NULL AND p.posted_at IS NOT NULL
		  AND p.posted_at > now() - interval '30 days' AND p.channel = ANY($1)
		GROUP BY p.id ORDER BY p.id`, channels)
	if err != nil {
		return nil, fmt.Errorf("outreach: engagement targets: %w", err)
	}
	defer rows.Close()
	var out []EngagementTarget
	for rows.Next() {
		var t EngagementTarget
		if err := rows.Scan(&t.PostID, &t.Channel, &t.RemoteID, &t.PostedAt, &t.LastCaptured); err != nil {
			return nil, fmt.Errorf("outreach: scan engagement target: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RecordEngagement stores one snapshot, even when the counts are unchanged:
// the latest capture time is what the due check reads.
func (s *Store) RecordEngagement(ctx context.Context, postID int64, at time.Time, c Counts) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO outreach_engagement (post_id, captured_at, likes, reposts, replies, quotes)
		VALUES ($1, $2, $3, $4, $5, $6)`, postID, at, c.Likes, c.Reposts, c.Replies, c.Quotes)
	if err != nil {
		return fmt.Errorf("outreach: record engagement: %w", err)
	}
	return nil
}

// LatestEngagement returns each given post's newest snapshot; posts with none
// are absent.
func (s *Store) LatestEngagement(ctx context.Context, postIDs []int64) (map[int64]Engagement, error) {
	out := map[int64]Engagement{}
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (post_id) post_id, captured_at, likes, reposts, replies, quotes
		FROM outreach_engagement WHERE post_id = ANY($1) ORDER BY post_id, captured_at DESC, id DESC`, postIDs)
	if err != nil {
		return nil, fmt.Errorf("outreach: latest engagement: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var e Engagement
		if err := rows.Scan(&id, &e.CapturedAt, &e.Likes, &e.Reposts, &e.Replies, &e.Quotes); err != nil {
			return nil, fmt.Errorf("outreach: scan engagement: %w", err)
		}
		out[id] = e
	}
	return out, rows.Err()
}

// ReactionTotalsByOutreach sums each post's latest snapshot, grouped by
// outreach. An outreach with no snapshot is absent.
func (s *Store) ReactionTotalsByOutreach(ctx context.Context) (map[int64]Counts, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.outreach_id, sum(l.likes), sum(l.reposts), sum(l.replies), sum(l.quotes)
		FROM outreach_posts p
		JOIN (SELECT DISTINCT ON (post_id) post_id, likes, reposts, replies, quotes
		      FROM outreach_engagement ORDER BY post_id, captured_at DESC, id DESC) l ON l.post_id = p.id
		GROUP BY p.outreach_id`)
	if err != nil {
		return nil, fmt.Errorf("outreach: reaction totals: %w", err)
	}
	defer rows.Close()
	out := map[int64]Counts{}
	for rows.Next() {
		var id int64
		var c Counts
		if err := rows.Scan(&id, &c.Likes, &c.Reposts, &c.Replies, &c.Quotes); err != nil {
			return nil, fmt.Errorf("outreach: scan reaction totals: %w", err)
		}
		out[id] = c
	}
	return out, rows.Err()
}

// PostRemoteIDs maps the remote id of each of the channel's posts to its post
// id.
func (s *Store) PostRemoteIDs(ctx context.Context, channel string) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT remote_id, id FROM outreach_posts WHERE channel = $1 AND remote_id IS NOT NULL`, channel)
	if err != nil {
		return nil, fmt.Errorf("outreach: post remote ids: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var rid string
		var id int64
		if err := rows.Scan(&rid, &id); err != nil {
			return nil, fmt.Errorf("outreach: scan remote id: %w", err)
		}
		out[rid] = id
	}
	return out, rows.Err()
}

// InsertMention stores a mention; one already stored under the same remote id
// is left alone and reported as not inserted.
func (s *Store) InsertMention(ctx context.Context, m Mention) (inserted bool, err error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO outreach_mentions (channel, remote_id, post_id, kind, author, text, url, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (remote_id) DO NOTHING`,
		m.Channel, m.RemoteID, m.PostID, m.Kind, m.Author, m.Text, m.URL, m.CreatedAt)
	if err != nil {
		return false, fmt.Errorf("outreach: insert mention: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MentionCursor returns the channel's stored notification position.
func (s *Store) MentionCursor(ctx context.Context, channel string) (cursor string, ok bool, err error) {
	rows, err := s.pool.Query(ctx, `SELECT cursor FROM outreach_mention_cursors WHERE channel = $1`, channel)
	if err != nil {
		return "", false, fmt.Errorf("outreach: mention cursor: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(&cursor); err != nil {
			return "", false, fmt.Errorf("outreach: scan mention cursor: %w", err)
		}
		ok = true
	}
	return cursor, ok, rows.Err()
}

// SetMentionCursor stores the channel's notification position.
func (s *Store) SetMentionCursor(ctx context.Context, channel, cursor string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO outreach_mention_cursors (channel, cursor) VALUES ($1, $2)
		ON CONFLICT (channel) DO UPDATE SET cursor = EXCLUDED.cursor, updated_at = now()`, channel, cursor)
	if err != nil {
		return fmt.Errorf("outreach: set mention cursor: %w", err)
	}
	return nil
}

const mentionSelect = `SELECT m.id, m.channel, m.remote_id, m.post_id, m.kind, m.author, m.text, m.url, m.created_at, m.fetched_at,
	p.wave, o.workshop_id, w.title
	FROM outreach_mentions m
	LEFT JOIN outreach_posts p ON p.id = m.post_id
	LEFT JOIN outreach o ON o.id = p.outreach_id
	LEFT JOIN workshops w ON w.id = o.workshop_id`

func (s *Store) queryMentions(ctx context.Context, where string, args ...any) ([]Mention, error) {
	rows, err := s.pool.Query(ctx, mentionSelect+where, args...)
	if err != nil {
		return nil, fmt.Errorf("outreach: list mentions: %w", err)
	}
	defer rows.Close()
	out := []Mention{}
	for rows.Next() {
		var m Mention
		if err := rows.Scan(&m.ID, &m.Channel, &m.RemoteID, &m.PostID, &m.Kind, &m.Author, &m.Text, &m.URL, &m.CreatedAt, &m.FetchedAt,
			&m.Wave, &m.WorkshopID, &m.WorkshopTitle); err != nil {
			return nil, fmt.Errorf("outreach: scan mention: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListMentionsForOutreach returns the mentions tied to this outreach's posts,
// newest first.
func (s *Store) ListMentionsForOutreach(ctx context.Context, outreachID int64, limit int) ([]Mention, error) {
	return s.queryMentions(ctx, ` WHERE p.outreach_id = $1 ORDER BY m.created_at DESC, m.id DESC LIMIT $2`, outreachID, limit)
}

// ListRecentMentions returns the newest mentions across every workshop,
// untied ones included.
func (s *Store) ListRecentMentions(ctx context.Context, limit int) ([]Mention, error) {
	return s.queryMentions(ctx, ` ORDER BY m.created_at DESC, m.id DESC LIMIT $1`, limit)
}
