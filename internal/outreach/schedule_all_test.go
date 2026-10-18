package outreach

import (
	"testing"
	"time"
)

func planPost(id int64, ch, wave, status, body string, prepared bool) Post {
	p := Post{ID: id, Channel: ch, Wave: wave, Status: status, Body: body}
	if prepared {
		u := "https://go.example/k"
		p.ShortURL = &u
	}
	return p
}

func TestSuggestedAt(t *testing.T) {
	start := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	if got := SuggestedAt(Wave{Name: "announce"}, &start); got != nil {
		t.Errorf("announce = %v, want nil", got)
	}
	if got := SuggestedAt(Wave{DaysBefore: 7}, nil); got != nil {
		t.Errorf("undated = %v, want nil", got)
	}
	got := SuggestedAt(Wave{DaysBefore: 1}, &start)
	if got == nil || !got.Equal(start.AddDate(0, 0, -1)) {
		t.Errorf("last call = %v", got)
	}
}

func TestPlanScheduleAll(t *testing.T) {
	start := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	now := start.AddDate(0, 0, -2) // reminder (7d) has passed, last-call (1d) is ahead
	all := func(string) bool { return true }
	const minL, maxL = time.Minute, 366 * 24 * time.Hour
	good := "Workshop https://go.example/k"

	posts := []Post{
		planPost(1, "bluesky", "announce", "draft", good, true),
		planPost(2, "bluesky", "reminder", "draft", good, true),
		planPost(3, "bluesky", "last-call", "draft", good, true),
		planPost(4, "mastodon", "announce", "draft", good, true),
		planPost(5, "mastodon", "reminder", "scheduled", good, true),
		planPost(6, "mastodon", "last-call", "draft", "needs {link}", false),
		planPost(7, "instagram", "announce", "manual", good, true),
	}
	items, skips := PlanScheduleAll(posts, &start, now, minL, maxL, all)
	if len(items) != 1 || items[0].PostID != 3 || !items[0].ScheduledAt.Equal(start.AddDate(0, 0, -1)) {
		t.Fatalf("items = %+v", items)
	}
	type key struct{ ch, wave, reason string }
	var got []key
	for _, s := range skips {
		got = append(got, key{s.Channel, s.Wave, s.Reason})
	}
	want := []key{
		{"bluesky", "announce", "no_suggested_time"},
		{"bluesky", "reminder", "time_passed"},
		{"mastodon", "announce", "no_suggested_time"},
		// mastodon reminder is scheduled already: not listed
		{"mastodon", "last-call", "no_link"},
	}
	if len(got) != len(want) {
		t.Fatalf("skips = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("skip %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	for _, s := range skips {
		if s.Channel == "instagram" || s.Channel == "tiktok" || s.Channel == "youtube" {
			t.Errorf("manual channel listed: %+v", s)
		}
		if s.Reason == "time_passed" && s.SuggestedAt == nil {
			t.Error("time_passed skip carries no suggested time")
		}
	}
}

func TestPlanScheduleAllRules(t *testing.T) {
	start := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	good := "Workshop https://go.example/k"
	one := func(p Post, st *time.Time, now time.Time, cfg func(string) bool) (int, string) {
		items, skips := PlanScheduleAll([]Post{p}, st, now, time.Minute, 30*24*time.Hour, cfg)
		for _, s := range skips {
			if s.Channel == p.Channel && s.Wave == p.Wave {
				return len(items), s.Reason
			}
		}
		return len(items), ""
	}
	yes := func(string) bool { return true }
	no := func(string) bool { return false }
	early := start.AddDate(0, 0, -10)

	if n, r := one(planPost(1, "bluesky", "reminder", "draft", good, true), &start, early, no); n != 0 || r != "not_connected" {
		t.Errorf("not connected: %d %q", n, r)
	}
	if n, r := one(planPost(1, "bluesky", "reminder", "draft", "x {link}", false), &start, early, yes); n != 0 || r != "no_link" {
		t.Errorf("no link: %d %q", n, r)
	}
	if n, r := one(planPost(1, "bluesky", "reminder", "draft", good, true), nil, early, yes); n != 0 || r != "no_suggested_time" {
		t.Errorf("undated: %d %q", n, r)
	}
	far := start.AddDate(0, 0, 100)
	if n, r := one(planPost(1, "bluesky", "reminder", "draft", good, true), &far, early, yes); n != 0 || r != "too_far" {
		t.Errorf("too far: %d %q", n, r)
	}
	long := good
	for len(long) < 400 {
		long += " word"
	}
	if n, r := one(planPost(1, "bluesky", "reminder", "draft", long, true), &start, early, yes); n != 0 || r != "invalid" {
		t.Errorf("invalid: %d %q", n, r)
	}
	if n, r := one(planPost(1, "bluesky", "reminder", "failed", good, true), &start, early, yes); n != 0 || r != "not_drafted" {
		// a failed post is not listed, but the slot has a post so no not_drafted either
		if r != "" {
			t.Errorf("failed post listed: %d %q", n, r)
		}
	}
	if n, r := one(planPost(1, "bluesky", "reminder", "draft", good, true), &start, early, yes); n != 1 || r != "" {
		t.Errorf("happy: %d %q", n, r)
	}
}
