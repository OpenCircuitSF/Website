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
	start := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC) // Mon Oct 19, 7:00 PM PDT
	if got := SuggestedAt(Wave{Name: "announce"}, &start, 10); got != nil {
		t.Errorf("announce = %v, want nil", got)
	}
	if got := SuggestedAt(Wave{DaysBefore: 7}, nil, 10); got != nil {
		t.Errorf("undated = %v, want nil", got)
	}
	got := SuggestedAt(Wave{DaysBefore: 1}, &start, 10)
	if want := time.Date(2026, 10, 18, 17, 0, 0, 0, time.UTC); got == nil || !got.Equal(want) {
		t.Errorf("last call = %v, want %v", got, want)
	}
	if got := SuggestedAt(Wave{DaysBefore: 1}, &start, 99); got == nil || got.In(pacific).Hour() != DefaultPostHour {
		t.Errorf("bad hour should fall back to %d: %v", DefaultPostHour, got)
	}
	if got := SuggestedAt(Wave{DaysBefore: 1}, &start, 7); got == nil || got.In(pacific).Hour() != 7 {
		t.Errorf("configured hour not used: %v", got)
	}
}

// Daylight saving ends 2026-11-01. A suggested time is the same Pacific
// wall-clock hour on the day N days before, whichever side of the change it
// lands on, and is never one hour off the way plain subtraction would be.
func TestSuggestedAtAcrossDST(t *testing.T) {
	// Thu Nov 5, 6:30 PM PST.
	start := time.Date(2026, 11, 6, 2, 30, 0, 0, time.UTC)
	rem := SuggestedAt(Wave{DaysBefore: 7}, &start, 10)
	last := SuggestedAt(Wave{DaysBefore: 1}, &start, 10)
	if want := time.Date(2026, 10, 29, 17, 0, 0, 0, time.UTC); !rem.Equal(want) { // 10:00 PDT
		t.Errorf("reminder = %v, want %v", rem.UTC(), want)
	}
	if want := time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC); !last.Equal(want) { // 10:00 PST
		t.Errorf("last call = %v, want %v", last.UTC(), want)
	}
	for _, got := range []*time.Time{rem, last} {
		if h, m := got.In(pacific).Hour(), got.In(pacific).Minute(); h != 10 || m != 0 {
			t.Errorf("%v is %d:%02d Pacific, want 10:00", got, h, m)
		}
	}
	// A workshop the evening of Nov 1 itself, reminder across the change.
	nov1 := time.Date(2026, 11, 2, 2, 0, 0, 0, time.UTC) // Nov 1, 6:00 PM PST
	if got := SuggestedAt(Wave{DaysBefore: 1}, &nov1, 10); !got.Equal(time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("day before the change = %v", got.UTC())
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
	items, skips := PlanScheduleAll(posts, &start, now, minL, maxL, 10, all)
	if len(items) != 1 || items[0].PostID != 3 || !items[0].ScheduledAt.Equal(time.Date(2026, 10, 18, 17, 0, 0, 0, time.UTC)) {
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
		items, skips := PlanScheduleAll([]Post{p}, st, now, time.Minute, 30*24*time.Hour, 10, cfg)
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

func TestPlanScheduleAllIgnoresUndraftedOptionalWaves(t *testing.T) {
	start := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
	now := start.AddDate(0, 0, -30)
	_, skips := PlanScheduleAll([]Post{planPost(1, "bluesky", "announce", "draft", "x", true)}, &start, now, time.Minute, 366*24*time.Hour, 10, func(string) bool { return true })
	for _, s := range skips {
		if s.Wave != "announce" {
			t.Errorf("undrafted optional wave listed: %+v", s)
		}
	}
	if len(skips) == 0 {
		t.Error("the undrafted Announce slot of mastodon should still be listed")
	}
}
