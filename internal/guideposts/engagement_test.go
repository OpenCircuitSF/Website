package guideposts

import (
	"context"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// postedGuide makes a guide post and marks it posted with the given remote id.
func postedGuide(t *testing.T, s *Store, channel, remoteID string, at time.Time) Post {
	t.Helper()
	p := linked(t, s, channel)
	if ok, err := s.pool.Exec(context.Background(),
		`UPDATE guide_posts SET status = 'posted', posted_at = $2, remote_id = $3 WHERE id = $1`, p.ID, at, remoteID); err != nil || ok.RowsAffected() != 1 {
		t.Fatalf("mark posted: %v", err)
	}
	return p
}

func TestEngagement_TargetsRecordAndLatest(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	tag := now.Format("150405.000000000")
	fresh := postedGuide(t, s, "bluesky", "at://guide/fresh"+tag, now.Add(-time.Hour))
	masto := postedGuide(t, s, "mastodon", "9001"+tag, now.Add(-2*time.Hour))
	old := postedGuide(t, s, "bluesky", "at://guide/old"+tag, now.Add(-31*24*time.Hour))
	draft := linked(t, s, "bluesky")

	targets, err := s.EngagementTargets(ctx, []string{"bluesky"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]outreach.EngagementTarget{}
	for _, tg := range targets {
		seen[tg.PostID] = tg
	}
	if tg, ok := seen[fresh.ID]; !ok || tg.RemoteID != "at://guide/fresh"+tag || tg.Channel != "bluesky" || tg.LastCaptured != nil {
		t.Errorf("fresh target = %+v (present %v)", tg, ok)
	}
	for name, id := range map[string]int64{"mastodon (channel filtered)": masto.ID, "31 days old": old.ID, "draft": draft.ID} {
		if _, ok := seen[id]; ok {
			t.Errorf("%s post listed as a target", name)
		}
	}
	if got, _ := s.EngagementTargets(ctx, nil); got != nil {
		t.Errorf("no channels returned %v", got)
	}

	t1 := now.Add(-30 * time.Minute).Truncate(time.Microsecond)
	t2 := now.Add(-5 * time.Minute).Truncate(time.Microsecond)
	if err := s.RecordEngagement(ctx, fresh.ID, t1, outreach.Counts{Likes: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEngagement(ctx, fresh.ID, t2, outreach.Counts{Likes: 4, Reposts: 3, Replies: 2, Quotes: 1}); err != nil {
		t.Fatal(err)
	}
	targets, _ = s.EngagementTargets(ctx, []string{"bluesky"})
	for _, tg := range targets {
		if tg.PostID == fresh.ID && (tg.LastCaptured == nil || !tg.LastCaptured.Equal(t2)) {
			t.Errorf("LastCaptured = %v, want %v", tg.LastCaptured, t2)
		}
	}
	latest, err := s.LatestEngagement(ctx, []int64{fresh.ID, masto.ID})
	if err != nil {
		t.Fatal(err)
	}
	if e := latest[fresh.ID]; e.Counts != (outreach.Counts{Likes: 4, Reposts: 3, Replies: 2, Quotes: 1}) || !e.CapturedAt.Equal(t2) {
		t.Errorf("latest = %+v", e)
	}
	if _, ok := latest[masto.ID]; ok {
		t.Error("a post with no snapshot is present")
	}
	if err := s.RecordEngagement(ctx, fresh.ID, now, outreach.Counts{Likes: -1}); err == nil {
		t.Error("a negative count was stored")
	}
}

func TestEngagement_CascadesOnDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := postedGuide(t, s, "bluesky", "at://guide/cascade"+time.Now().Format("150405.000000000"), time.Now())
	if err := s.RecordEngagement(ctx, p.ID, time.Now(), outreach.Counts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM guide_posts WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM guide_post_engagement WHERE post_id = $1`, p.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("snapshots left after delete: %d (%v)", n, err)
	}
}
