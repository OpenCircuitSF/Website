package outreach

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

func TestEngagementDue(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	const h, day = time.Hour, 24 * time.Hour
	for _, tc := range []struct {
		name string
		age  time.Duration
		last *time.Time
		want bool
	}{
		{"fresh, never captured", 10 * time.Minute, nil, true},
		{"6 days, last 50m ago", 6 * day, ago(50 * time.Minute), false},
		{"6 days, last 56m ago", 6 * day, ago(56 * time.Minute), true},
		{"8 days, last 23h ago", 8 * day, ago(23 * h), false},
		{"8 days, last 24h ago", 8 * day, ago(24 * h), true},
		{"29.9 days, last 25h ago", 29*day + 22*h, ago(25 * h), true},
		{"exactly 30 days", 30 * day, nil, false},
		{"31 days", 31 * day, nil, false},
		{"31 days, last long ago", 31 * day, ago(40 * h), false},
	} {
		if got := EngagementDue(now.Add(-tc.age), tc.last, now); got != tc.want {
			t.Errorf("%s: EngagementDue = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// seedPosted seeds a posted post with a remote id and a posted_at, straight
// into the table, on a throwaway workshop.
func seedPosted(t *testing.T, s *Store, channel, remoteID string, postedAt time.Time) (Outreach, Post) {
	t.Helper()
	w := seedWorkshop(t, s.pool)
	o, _, err := s.Create(context.Background(), w.ID, w.Slug)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePost(context.Background(), o.ID, channel, "announce", "x", "fp")
	if err != nil {
		t.Fatal(err)
	}
	var rid any
	if remoteID != "" {
		rid = remoteID
	}
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE outreach_posts SET status = 'posted', posted_at = $2, remote_id = $3 WHERE id = $1`, p.ID, postedAt, rid); err != nil {
		t.Fatal(err)
	}
	return o, p
}

// uniq returns a tag unique to this test run, for ids that outlive a
// workshop's cascade (mentions are not deleted with their post).
func uniq() string { return fmt.Sprintf("t%dx", testdb.Unique()) }

func cleanMentions(t *testing.T, pool *pgxpool.Pool, tag string) {
	t.Helper()
	del := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM outreach_mentions WHERE remote_id LIKE '%' || $1 || '%'`, tag)
	}
	del()
	t.Cleanup(del)
}

func TestEngagementStore_LatestAndTotals(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	o, p1 := seedPosted(t, s, "bluesky", "at://x/"+uniq(), time.Now().Add(-48*time.Hour))
	p2, err := s.CreatePost(ctx, o.ID, "mastodon", "announce", "x", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.LatestEngagement(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty ids: %v %v", got, err)
	}
	base := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	for i, c := range []Counts{{1, 0, 0, 0}, {3, 1, 2, 0}} {
		if err := s.RecordEngagement(ctx, p1.ID, base.Add(time.Duration(i)*time.Hour), c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordEngagement(ctx, p2.ID, base, Counts{Likes: 10}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestEngagement(ctx, []int64{p1.ID, p2.ID, p2.ID + 99999})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[p1.ID].Counts != (Counts{3, 1, 2, 0}) || !got[p1.ID].CapturedAt.Equal(base.Add(time.Hour)) {
		t.Errorf("latest = %+v", got)
	}
	tot, err := s.ReactionTotalsByOutreach(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// p1 counts once (its latest), plus p2.
	if want := (Counts{13, 1, 2, 0}); tot[o.ID] != want {
		t.Errorf("totals = %+v, want %+v (two snapshots of one post count once)", tot[o.ID], want)
	}
	if err := s.RecordEngagement(ctx, p1.ID, base, Counts{Likes: -1}); err == nil {
		t.Error("a negative count must be refused by the CHECK")
	}
}

func TestEngagementTargets(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	tag := uniq()
	_, ok := seedPosted(t, s, "bluesky", "at://ok/"+tag, time.Now().Add(-2*time.Hour))
	_, noRemote := seedPosted(t, s, "bluesky", "", time.Now().Add(-2*time.Hour))
	_, other := seedPosted(t, s, "instagram", "ig-"+tag, time.Now().Add(-2*time.Hour))
	_, old := seedPosted(t, s, "bluesky", "at://old/"+tag, time.Now().Add(-31*24*time.Hour))
	_, draft := seedPost(t, s, "draft")
	if _, err := s.pool.Exec(ctx, `UPDATE outreach_posts SET remote_id = 'at://draft/'||$2 WHERE id = $1`, draft.ID, tag); err != nil {
		t.Fatal(err)
	}
	captured := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := s.RecordEngagement(ctx, ok.ID, captured, Counts{}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EngagementTargets(ctx, nil); err != nil || got != nil {
		t.Errorf("no channels: %v %v", got, err)
	}
	got, err := s.EngagementTargets(ctx, []string{"bluesky", "mastodon"})
	if err != nil {
		t.Fatal(err)
	}
	by := map[int64]EngagementTarget{}
	for _, g := range got {
		by[g.PostID] = g
	}
	if _, in := by[ok.ID]; !in {
		t.Fatalf("the posted bluesky post is missing: %+v", got)
	}
	for name, p := range map[string]Post{"no remote id": noRemote, "other channel": other, "older than 30 days": old, "unposted": draft} {
		if _, in := by[p.ID]; in {
			t.Errorf("%s was returned", name)
		}
	}
	if lc := by[ok.ID].LastCaptured; lc == nil || !lc.Equal(captured) {
		t.Errorf("LastCaptured = %v, want %v", lc, captured)
	}
}

func TestMentionStore(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	tag := uniq()
	cleanMentions(t, s.pool, tag)
	o1, p1 := seedPosted(t, s, "bluesky", "at://ours1/"+tag, time.Now().Add(-time.Hour))
	_, p2 := seedPosted(t, s, "bluesky", "at://ours2/"+tag, time.Now().Add(-time.Hour))
	now := time.Now().UTC().Truncate(time.Second)
	link := "https://bsky.app/profile/alice.example.test/post/1"
	mk := func(rid string, post *int64, at time.Time) Mention {
		return Mention{Channel: "bluesky", RemoteID: rid + tag, PostID: post, Kind: "reply", Author: "@alice.example.test", Text: "hi", URL: &link, CreatedAt: at}
	}
	if ins, err := s.InsertMention(ctx, mk("a", &p1.ID, now.Add(-2*time.Minute))); err != nil || !ins {
		t.Fatalf("first insert: %v %v", ins, err)
	}
	if ins, err := s.InsertMention(ctx, mk("a", &p1.ID, now)); err != nil || ins {
		t.Errorf("a duplicate remote_id must not insert: %v %v", ins, err)
	}
	s.InsertMention(ctx, mk("b", &p1.ID, now))
	s.InsertMention(ctx, mk("c", &p2.ID, now))
	s.InsertMention(ctx, mk("untied", nil, now.Add(time.Minute)))
	bad := mk("bad", nil, now)
	bad.Kind = "like"
	if _, err := s.InsertMention(ctx, bad); err == nil {
		t.Error("a kind outside mention/reply/quote must be refused")
	}

	got, err := s.ListMentionsForOutreach(ctx, o1.ID, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("for outreach: %d %v", len(got), err)
	}
	if got[0].RemoteID != "b"+tag || got[1].RemoteID != "a"+tag {
		t.Errorf("order = %q, %q (newest first)", got[0].RemoteID, got[1].RemoteID)
	}
	if got[0].Wave == nil || *got[0].Wave != "announce" || got[0].WorkshopID == nil || got[0].WorkshopTitle == nil {
		t.Errorf("tie fields missing: %+v", got[0])
	}
	recent, err := s.ListRecentMentions(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var sawUntied bool
	for _, m := range recent {
		if m.RemoteID == "untied"+tag {
			sawUntied = m.PostID == nil && m.WorkshopID == nil && m.Wave == nil
		}
	}
	if !sawUntied {
		t.Error("recent mentions must include the untied one, with no workshop")
	}
	if few, _ := s.ListRecentMentions(ctx, 1); len(few) != 1 {
		t.Errorf("limit ignored: %d", len(few))
	}

	// Deleting the workshop cascades engagement and nulls mention post_id.
	if err := s.RecordEngagement(ctx, p2.ID, now, Counts{Likes: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM workshops WHERE id = (SELECT workshop_id FROM outreach WHERE id = $1)`, p2.OutreachID); err != nil {
		t.Fatal(err)
	}
	var n int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM outreach_engagement WHERE post_id = $1`, p2.ID).Scan(&n)
	var postID *int64
	if err := s.pool.QueryRow(ctx, `SELECT post_id FROM outreach_mentions WHERE remote_id = $1`, "c"+tag).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	if n != 0 || postID != nil {
		t.Errorf("after delete: engagement rows=%d, mention post_id=%v", n, postID)
	}
}

func TestMentionCursor(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	const ch = "zz-test-channel"
	del := func() { _, _ = s.pool.Exec(ctx, `DELETE FROM outreach_mention_cursors WHERE channel = $1`, ch) }
	del()
	t.Cleanup(del)
	if _, ok, err := s.MentionCursor(ctx, ch); err != nil || ok {
		t.Fatalf("unset: ok=%v err=%v", ok, err)
	}
	if err := s.SetMentionCursor(ctx, ch, "10"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMentionCursor(ctx, ch, "11"); err != nil {
		t.Fatal(err)
	}
	if c, ok, err := s.MentionCursor(ctx, ch); err != nil || !ok || c != "11" {
		t.Errorf("cursor = %q ok=%v err=%v, want the upserted 11", c, ok, err)
	}
}
