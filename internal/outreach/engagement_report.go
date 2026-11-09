package outreach

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ReportChannels are the channels the engagement report covers (#0653).
var ReportChannels = []string{"bluesky", "mastodon"}

// Total is the sum of the four counters.
func (c Counts) Total() int { return c.Likes + c.Reposts + c.Replies + c.Quotes }

// EngagementDelta is the net change from baseline to current, never
// negative per counter (an unlike counts 0). A nil baseline means the
// post's first capture is inside the window, so current counts in full.
func EngagementDelta(baseline *Counts, current Counts) Counts {
	if baseline == nil {
		return current
	}
	d := func(b, c int) int { return max(0, c-b) }
	return Counts{
		Likes:   d(baseline.Likes, current.Likes),
		Reposts: d(baseline.Reposts, current.Reposts),
		Replies: d(baseline.Replies, current.Replies),
		Quotes:  d(baseline.Quotes, current.Quotes),
	}
}

// PostDelta is one post's change over a window.
type PostDelta struct {
	PostID        int64
	Channel, Wave string
	URL           *string // remote_url
	PostedAt      *time.Time
	WorkshopTitle string
	StartsAt      *time.Time
	Delta         Counts
}

// EngagementWindow is everything the report needs for (since, until].
type EngagementWindow struct {
	Posts    []PostDelta    // delta total > 0 only; ordered by delta total desc, posted_at desc, post id
	Mentions map[string]int // per channel; every ReportChannels key present, 0 when none
	DataAsOf *time.Time
}

// EngagementWindow reports the net engagement change over (since, until]
// (#0653). It reads stored snapshots only and never triggers a capture.
//
// Each post compares its latest snapshot at or before until with its latest
// at or before since. Mentions are windowed by created_at, not fetched_at:
// a backfill on first enable or after an outage gives old rows a fresh
// fetched_at, and created_at keeps a window's answer stable. The accepted
// cost is a mention created in the window but fetched after until, which no
// window then reports. A reply or quote tied to one of our posts is left out
// when a snapshot of that post taken at or after it (and by until) already
// counts it.
//
// DataAsOf is the newest snapshot at or before until. It goes stale
// legitimately when no post is younger than 30 days, since nothing is
// captured then.
func (s *Store) EngagementWindow(ctx context.Context, since, until time.Time) (EngagementWindow, error) {
	out := EngagementWindow{Mentions: map[string]int{}}
	for _, c := range ReportChannels {
		out.Mentions[c] = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.channel, p.wave, p.remote_url, p.posted_at, w.title, w.starts_at,
		       c.likes, c.reposts, c.replies, c.quotes,
		       b.likes, b.reposts, b.replies, b.quotes
		FROM outreach_posts p
		JOIN outreach o ON o.id = p.outreach_id
		JOIN workshops w ON w.id = o.workshop_id
		JOIN LATERAL (SELECT captured_at, likes, reposts, replies, quotes FROM outreach_engagement e
		              WHERE e.post_id = p.id AND e.captured_at <= $2
		              ORDER BY e.captured_at DESC, e.id DESC LIMIT 1) c ON true
		LEFT JOIN LATERAL (SELECT likes, reposts, replies, quotes FROM outreach_engagement e
		              WHERE e.post_id = p.id AND e.captured_at <= $1
		              ORDER BY e.captured_at DESC, e.id DESC LIMIT 1) b ON true
		WHERE p.channel = ANY($3) AND c.captured_at > $1`, since, until, ReportChannels)
	if err != nil {
		return out, fmt.Errorf("outreach: engagement window: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p PostDelta
		var cur Counts
		var bl, br, bp, bq *int
		if err := rows.Scan(&p.PostID, &p.Channel, &p.Wave, &p.URL, &p.PostedAt, &p.WorkshopTitle, &p.StartsAt,
			&cur.Likes, &cur.Reposts, &cur.Replies, &cur.Quotes, &bl, &br, &bp, &bq); err != nil {
			return out, fmt.Errorf("outreach: engagement window: %w", err)
		}
		var base *Counts
		if bl != nil {
			base = &Counts{*bl, *br, *bp, *bq}
		}
		p.Delta = EngagementDelta(base, cur)
		if p.Delta.Total() > 0 {
			out.Posts = append(out.Posts, p)
		}
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("outreach: engagement window: %w", err)
	}
	sort.SliceStable(out.Posts, func(i, j int) bool {
		a, b := out.Posts[i], out.Posts[j]
		if a.Delta.Total() != b.Delta.Total() {
			return a.Delta.Total() > b.Delta.Total()
		}
		switch {
		case a.PostedAt != nil && b.PostedAt != nil && !a.PostedAt.Equal(*b.PostedAt):
			return a.PostedAt.After(*b.PostedAt)
		case a.PostedAt != nil && b.PostedAt == nil:
			return true
		case a.PostedAt == nil && b.PostedAt != nil:
			return false
		}
		return a.PostID < b.PostID
	})

	mrows, err := s.pool.Query(ctx, `
		SELECT m.channel, count(*) FROM outreach_mentions m
		WHERE m.created_at > $1 AND m.created_at <= $2 AND m.channel = ANY($3)
		  AND NOT (m.post_id IS NOT NULL AND m.kind IN ('reply','quote') AND EXISTS (
		        SELECT 1 FROM outreach_engagement e
		        WHERE e.post_id = m.post_id AND e.captured_at >= m.created_at AND e.captured_at <= $2))
		GROUP BY m.channel`, since, until, ReportChannels)
	if err != nil {
		return out, fmt.Errorf("outreach: engagement window: %w", err)
	}
	defer mrows.Close()
	for mrows.Next() {
		var ch string
		var n int
		if err := mrows.Scan(&ch, &n); err != nil {
			return out, fmt.Errorf("outreach: engagement window: %w", err)
		}
		out.Mentions[ch] = n
	}
	if err := mrows.Err(); err != nil {
		return out, fmt.Errorf("outreach: engagement window: %w", err)
	}

	if err := s.pool.QueryRow(ctx, `SELECT max(captured_at) FROM outreach_engagement WHERE captured_at <= $1`, until).Scan(&out.DataAsOf); err != nil {
		return out, fmt.Errorf("outreach: engagement window: %w", err)
	}
	return out, nil
}

// ChannelEngagement is one channel's new interactions in a window.
type ChannelEngagement struct {
	Counts
	Mentions int
}

// Total is the four counters plus mentions.
func (c ChannelEngagement) Total() int { return c.Counts.Total() + c.Mentions }

// EngagementSummary is the one-sentence notification text (#0653).
// windowPhrase is used only by the empty sentence.
func EngagementSummary(bluesky, mastodon ChannelEngagement, windowPhrase string) string {
	total := bluesky.Total() + mastodon.Total()
	if total == 0 {
		return "No new engagement on Bluesky or Mastodon " + windowPhrase + "."
	}
	var parts []string
	for _, ch := range []struct {
		name string
		c    ChannelEngagement
	}{{"Bluesky", bluesky}, {"Mastodon", mastodon}} {
		if ch.c.Total() == 0 {
			continue
		}
		var items []string
		for _, it := range []struct {
			k         int
			one, many string
		}{
			{ch.c.Likes, "like", "likes"},
			{ch.c.Reposts, "repost", "reposts"},
			{ch.c.Replies, "reply", "replies"},
			{ch.c.Quotes, "quote", "quotes"},
			{ch.c.Mentions, "mention", "mentions"},
		} {
			if it.k == 0 {
				continue
			}
			items = append(items, strconv.Itoa(it.k)+" "+plural(it.k, it.one, it.many))
		}
		parts = append(parts, fmt.Sprintf("%s %d (%s)", ch.name, ch.c.Total(), strings.Join(items, ", ")))
	}
	return fmt.Sprintf("%d new %s: %s.", total, plural(total, "interaction", "interactions"), strings.Join(parts, ", "))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// WindowPhrase describes the window for the empty sentence: hours > 0
// gives "in the last hour" / "in the last N hours"; hours == 0 gives
// "since Jan 2 at 3:04 PM MST" with since in America/Los_Angeles.
func WindowPhrase(hours int, since time.Time) string {
	switch {
	case hours == 1:
		return "in the last hour"
	case hours > 1:
		return fmt.Sprintf("in the last %d hours", hours)
	}
	return "since " + since.In(pacific).Format("Jan 2 at 3:04 PM MST")
}

// PacificDate formats t's calendar date in America/Los_Angeles as 2006-01-02.
func PacificDate(t time.Time) string { return t.In(pacific).Format("2006-01-02") }
