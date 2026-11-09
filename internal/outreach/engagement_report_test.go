package outreach

import (
	"context"
	"testing"
	"time"
	_ "time/tzdata"
)

func TestEngagementDelta(t *testing.T) {
	ptr := func(c Counts) *Counts { return &c }
	for _, tc := range []struct {
		name string
		base *Counts
		cur  Counts
		want Counts
	}{
		{"no change", ptr(Counts{3, 1, 2, 0}), Counts{3, 1, 2, 0}, Counts{}},
		{"rise in each counter", ptr(Counts{1, 1, 1, 1}), Counts{2, 3, 4, 5}, Counts{1, 2, 3, 4}},
		{"unlike", ptr(Counts{3, 0, 0, 0}), Counts{2, 1, 1, 1}, Counts{0, 1, 1, 1}},
		{"every counter falls", ptr(Counts{5, 5, 5, 5}), Counts{1, 2, 3, 4}, Counts{}},
		{"first capture inside window", nil, Counts{2, 1, 0, 1}, Counts{2, 1, 0, 1}},
		{"nil baseline, zero current", nil, Counts{}, Counts{}},
	} {
		if got := EngagementDelta(tc.base, tc.cur); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestCountsTotal(t *testing.T) {
	if got := (Counts{1, 2, 3, 4}).Total(); got != 10 {
		t.Fatalf("Total = %d", got)
	}
	if got := (ChannelEngagement{Counts{1, 0, 0, 0}, 2}).Total(); got != 3 {
		t.Fatalf("ChannelEngagement.Total = %d", got)
	}
}

func TestEngagementSummary(t *testing.T) {
	sinceMode := time.Date(2026, 11, 7, 17, 0, 0, 0, time.UTC)
	ce := func(l, r, p, q, m int) ChannelEngagement { return ChannelEngagement{Counts{l, r, p, q}, m} }
	for _, tc := range []struct {
		name     string
		b, m     ChannelEngagement
		phrase   string
		expected string
	}{
		{"empty 24h", ce(0, 0, 0, 0, 0), ce(0, 0, 0, 0, 0), WindowPhrase(24, time.Time{}), "No new engagement on Bluesky or Mastodon in the last 24 hours."},
		{"empty 1h", ce(0, 0, 0, 0, 0), ce(0, 0, 0, 0, 0), WindowPhrase(1, time.Time{}), "No new engagement on Bluesky or Mastodon in the last hour."},
		{"empty since", ce(0, 0, 0, 0, 0), ce(0, 0, 0, 0, 0), WindowPhrase(0, sinceMode), "No new engagement on Bluesky or Mastodon since Nov 7 at 9:00 AM PST."},
		{"mixed", ce(3, 1, 0, 0, 1), ce(2, 0, 0, 0, 0), "x", "7 new interactions: Bluesky 5 (3 likes, 1 repost, 1 mention), Mastodon 2 (2 likes)."},
		{"exactly one", ce(0, 0, 0, 0, 0), ce(0, 0, 1, 0, 0), "x", "1 new interaction: Mastodon 1 (1 reply)."},
		{"singulars", ce(1, 1, 1, 1, 1), ce(0, 0, 0, 0, 0), "x", "5 new interactions: Bluesky 5 (1 like, 1 repost, 1 reply, 1 quote, 1 mention)."},
		{"plurals", ce(2, 2, 2, 2, 2), ce(0, 0, 0, 0, 0), "x", "10 new interactions: Bluesky 10 (2 likes, 2 reposts, 2 replies, 2 quotes, 2 mentions)."},
		{"bluesky zero", ce(0, 0, 0, 0, 0), ce(0, 2, 0, 1, 0), "x", "3 new interactions: Mastodon 3 (2 reposts, 1 quote)."},
		{"mentions only", ce(0, 0, 0, 0, 1), ce(0, 0, 0, 0, 0), "x", "1 new interaction: Bluesky 1 (1 mention)."},
	} {
		if got := EngagementSummary(tc.b, tc.m, tc.phrase); got != tc.expected {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.expected)
		}
	}
}

func TestWindowPhrase(t *testing.T) {
	for _, tc := range []struct {
		hours int
		since time.Time
		want  string
	}{
		{1, time.Time{}, "in the last hour"},
		{2, time.Time{}, "in the last 2 hours"},
		{168, time.Time{}, "in the last 168 hours"},
		{0, time.Date(2026, 7, 1, 16, 0, 0, 0, time.UTC), "since Jul 1 at 9:00 AM PDT"},
	} {
		if got := WindowPhrase(tc.hours, tc.since); got != tc.want {
			t.Errorf("WindowPhrase(%d) = %q, want %q", tc.hours, got, tc.want)
		}
	}
}

func TestPacificDate(t *testing.T) {
	if got := PacificDate(time.Date(2026, 11, 10, 3, 30, 0, 0, time.UTC)); got != "2026-11-09" {
		t.Fatalf("PacificDate = %q", got)
	}
}

// t2031 is the fixed window start for DB tests, far from any time.Now() row.
var t2031 = time.Date(2031, 3, 10, 12, 0, 0, 0, time.UTC)

func record(t *testing.T, s *Store, postID int64, at time.Time, c Counts) {
	t.Helper()
	if err := s.RecordEngagement(context.Background(), postID, at, c); err != nil {
		t.Fatal(err)
	}
}

func findPost(w EngagementWindow, id int64) (PostDelta, bool) {
	for _, p := range w.Posts {
		if p.PostID == id {
			return p, true
		}
	}
	return PostDelta{}, false
}

func TestEngagementWindow_Deltas(t *testing.T) {
	s := NewStore(testPool(t))
	T, h := t2031, time.Hour
	posted := T.Add(-48 * h)
	_, a := seedPosted(t, s, "bluesky", "at://a/"+uniq(), posted)
	_, b := seedPosted(t, s, "bluesky", "at://b/"+uniq(), posted)
	_, c := seedPosted(t, s, "mastodon", "m-c-"+uniq(), posted)
	_, d := seedPosted(t, s, "mastodon", "m-d-"+uniq(), posted)
	_, e := seedPosted(t, s, "instagram", "ig-"+uniq(), posted)
	record(t, s, a.ID, T.Add(-h), Counts{3, 0, 0, 0})
	record(t, s, a.ID, T.Add(10*h), Counts{5, 1, 0, 0})
	record(t, s, a.ID, T.Add(30*h), Counts{50, 50, 0, 0}) // after until: ignored
	record(t, s, b.ID, T.Add(-h), Counts{4, 0, 0, 0})
	record(t, s, b.ID, T.Add(10*h), Counts{3, 0, 0, 0})
	record(t, s, c.ID, T.Add(2*h), Counts{1, 0, 1, 0})
	record(t, s, d.ID, T.Add(-2*h), Counts{9, 0, 0, 0})
	record(t, s, e.ID, T.Add(3*h), Counts{9, 0, 0, 0})
	w, err := s.EngagementWindow(context.Background(), T, T.Add(24*h))
	if err != nil {
		t.Fatal(err)
	}
	var ours []PostDelta
	for _, p := range w.Posts {
		switch p.PostID {
		case a.ID, b.ID, c.ID, d.ID, e.ID:
			ours = append(ours, p)
		}
	}
	if len(ours) != 2 || ours[0].PostID != a.ID || ours[1].PostID != c.ID {
		t.Fatalf("posts = %+v, want A then C", ours)
	}
	if ours[0].Delta != (Counts{2, 1, 0, 0}) || ours[1].Delta != (Counts{1, 0, 1, 0}) {
		t.Fatalf("deltas = %+v %+v", ours[0].Delta, ours[1].Delta)
	}
	if ours[0].Channel != "bluesky" || ours[0].Wave != "announce" || ours[0].WorkshopTitle == "" {
		t.Fatalf("post fields = %+v", ours[0])
	}
}

func TestEngagementWindow_BoundaryInstants(t *testing.T) {
	s := NewStore(testPool(t))
	T := t2031.Add(100 * 24 * time.Hour)
	until := T.Add(24 * time.Hour)
	_, p := seedPosted(t, s, "bluesky", "at://bd/"+uniq(), T.Add(-time.Hour))
	record(t, s, p.ID, T, Counts{2, 0, 0, 0})     // exactly since: baseline
	record(t, s, p.ID, until, Counts{5, 0, 0, 0}) // exactly until: current
	w, err := s.EngagementWindow(context.Background(), T, until)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := findPost(w, p.ID)
	if !ok || got.Delta != (Counts{3, 0, 0, 0}) {
		t.Fatalf("post = %+v (found %v), want delta 3 likes", got, ok)
	}
	// A post whose only snapshot is exactly at since has no current in the window.
	_, q := seedPosted(t, s, "bluesky", "at://bd2/"+uniq(), T.Add(-time.Hour))
	record(t, s, q.ID, T, Counts{7, 0, 0, 0})
	w, err = s.EngagementWindow(context.Background(), T, until)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findPost(w, q.ID); ok {
		t.Fatal("snapshot exactly at since counted as current")
	}
}

func TestEngagementWindow_Mentions(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	tag := uniq()
	cleanMentions(t, s.pool, tag)
	T := t2031.Add(200 * 24 * time.Hour)
	until := T.Add(24 * time.Hour)
	_, tied := seedPosted(t, s, "bluesky", "at://mt/"+tag, T.Add(-time.Hour))
	_, untied := seedPosted(t, s, "bluesky", "at://mu/"+tag, T.Add(-time.Hour))
	record(t, s, tied.ID, T.Add(5*time.Hour), Counts{0, 0, 1, 0})
	ins := func(ch, rid, kind string, post *int64, at time.Time) {
		t.Helper()
		if _, err := s.InsertMention(ctx, Mention{Channel: ch, RemoteID: rid + tag, Kind: kind, Author: "a", Text: "x", PostID: post, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	ins("bluesky", "m1-", "mention", nil, T.Add(time.Hour))
	ins("bluesky", "m2-", "mention", nil, T.Add(-time.Hour))       // backfill: created before since
	ins("bluesky", "m3-", "mention", nil, until.Add(time.Second))  // after until
	ins("bluesky", "m4-", "reply", &tied.ID, T.Add(2*time.Hour))   // already in the counter
	ins("bluesky", "m5-", "reply", &untied.ID, T.Add(2*time.Hour)) // no later snapshot: counts
	ins("mastodon", "m6-", "mention", nil, until)                  // exactly until: counts
	w, err := s.EngagementWindow(ctx, T, until)
	if err != nil {
		t.Fatal(err)
	}
	if w.Mentions["bluesky"] != 2 || w.Mentions["mastodon"] != 1 || len(w.Mentions) != len(ReportChannels) {
		t.Fatalf("mentions = %v, want bluesky 2, mastodon 1", w.Mentions)
	}
	empty, err := s.EngagementWindow(ctx, T.Add(-48*time.Hour), T.Add(-30*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range ReportChannels {
		if v, ok := empty.Mentions[ch]; !ok || v != 0 {
			t.Fatalf("empty mentions = %v", empty.Mentions)
		}
	}
}

func TestEngagementWindow_DataAsOf(t *testing.T) {
	s := NewStore(testPool(t))
	T := t2031.Add(300 * 24 * time.Hour)
	until := T.Add(24 * time.Hour)
	_, p := seedPosted(t, s, "bluesky", "at://da/"+uniq(), T)
	record(t, s, p.ID, T.Add(3*time.Hour), Counts{})
	record(t, s, p.ID, until.Add(time.Hour), Counts{})
	w, err := s.EngagementWindow(context.Background(), T, until)
	if err != nil {
		t.Fatal(err)
	}
	// Other tests' 2031 rows lie before T, so the newest at or before until is ours.
	if w.DataAsOf == nil || !w.DataAsOf.Equal(T.Add(3*time.Hour)) {
		t.Fatalf("DataAsOf = %v, want %v", w.DataAsOf, T.Add(3*time.Hour))
	}
}
