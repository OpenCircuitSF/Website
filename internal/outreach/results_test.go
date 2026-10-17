package outreach

import (
	"reflect"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

func d(y int, m time.Month, day, h int) time.Time { return time.Date(y, m, day, h, 0, 0, 0, time.UTC) }

func TestResultsWindow(t *testing.T) {
	ws := d(2026, 10, 20, 18)
	farWs := d(2026, 12, 1, 18)
	cases := []struct {
		name         string
		created      time.Time
		starts       *time.Time
		now          time.Time
		wantF, wantT time.Time
	}{
		{"normal, includes today", d(2026, 10, 1, 15), &farWs, d(2026, 10, 9, 10), d(2026, 10, 1, 0), d(2026, 10, 10, 0)},
		{"nil startsAt", d(2026, 10, 1, 15), nil, d(2026, 10, 9, 10), d(2026, 10, 1, 0), d(2026, 10, 10, 0)},
		{"clamped to workshop plus two days", d(2026, 10, 1, 0), &ws, d(2026, 11, 15, 0), d(2026, 10, 1, 0), d(2026, 10, 22, 0)},
		{"outreach started after the workshop", d(2026, 10, 25, 9), &ws, d(2026, 10, 26, 0), d(2026, 10, 25, 0), d(2026, 10, 26, 0)},
		{"120 day cap", d(2026, 1, 1, 0), nil, d(2026, 10, 9, 0), d(2026, 6, 12, 0), d(2026, 10, 10, 0)},
		{"non-UTC input is read as UTC days", time.Date(2026, 10, 1, 23, 0, 0, 0, time.FixedZone("x", -8*3600)), nil, d(2026, 10, 2, 5), d(2026, 10, 2, 0), d(2026, 10, 3, 0)},
	}
	for _, tc := range cases {
		f, to := ResultsWindow(tc.created, tc.starts, tc.now)
		if !f.Equal(tc.wantF) || !to.Equal(tc.wantT) {
			t.Errorf("%s: got %s..%s want %s..%s", tc.name, f.Format("2006-01-02"), to.Format("2006-01-02"), tc.wantF.Format("2006-01-02"), tc.wantT.Format("2006-01-02"))
		}
	}
}

func sp(s string) *string { return &s }

func day(date string, n int64) shortlinks.DayBucket {
	return shortlinks.DayBucket{Date: date, Count: n}
}

func lb(key string, n int64) shortlinks.LinkBucket {
	id := int64(1)
	return shortlinks.LinkBucket{LinkID: &id, Key: key, Count: n}
}

func ls(key string, days ...shortlinks.DayBucket) shortlinks.LinkSeries {
	id := int64(1)
	return shortlinks.LinkSeries{LinkID: &id, Key: key, Days: days}
}

func rollup(by []shortlinks.LinkBucket, series []shortlinks.LinkSeries) *shortlinks.CampaignRollup {
	r := &shortlinks.CampaignRollup{WindowFrom: "2026-10-01", WindowTo: "2026-10-09", ByLink: by, SeriesByLink: series}
	tot := map[string]int64{}
	for _, s := range series {
		addDays(tot, s.Days)
	}
	r.Timeseries.Days = sortedDays(tot)
	for _, b := range by {
		r.ClickCount += b.Count
	}
	return r
}

// assertInvariant: per date, the series sum equals the rollup's timeseries.
func assertInvariant(t *testing.T, res Results, r *shortlinks.CampaignRollup) {
	t.Helper()
	got := map[string]int64{}
	for _, s := range res.Series {
		addDays(got, s.Days)
	}
	want := map[string]int64{}
	addDays(want, r.Timeseries.Days)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("series sum %v != timeseries %v", got, want)
	}
}

func TestBuildResultsNoFold(t *testing.T) {
	posts := []Post{
		{ID: 1, Channel: "bluesky", ShortLinkKey: sp("k1")},
		{ID: 2, Channel: "mastodon", ShortLinkKey: sp("k2")},
		{ID: 3, Channel: "instagram"}, // unprepared
		{ID: 4, Channel: "tiktok", ShortLinkKey: sp("k4")},
	}
	r := rollup(
		[]shortlinks.LinkBucket{lb("k1", 7), lb("k2", 3), lb("zz", 4), lb("", 2)},
		[]shortlinks.LinkSeries{ls("k1", day("2026-10-02", 5), day("2026-10-03", 2)), ls("k2", day("2026-10-03", 3)), ls("zz", day("2026-10-03", 4)), ls("", day("2026-10-04", 2))},
	)
	res := BuildResults(posts, r)
	if res.PostClicks[1] != 7 || res.PostClicks[2] != 3 || res.PostClicks[4] != 0 {
		t.Fatalf("post clicks %v", res.PostClicks)
	}
	if _, ok := res.PostClicks[3]; ok {
		t.Fatal("unprepared post must be absent from PostClicks")
	}
	if res.UnmatchedClicks != 6 {
		t.Fatalf("unmatched %d", res.UnmatchedClicks)
	}
	if res.Channels[0].Clicks != 7 || !res.Channels[0].HasDailySeries || res.Channels[2].HasDailySeries || res.Channels[2].Prepared != 0 || res.Channels[3].Prepared != 1 {
		t.Fatalf("channels %+v", res.Channels)
	}
	ids := []string{}
	for _, s := range res.Series {
		ids = append(ids, s.ID)
	}
	// tiktok is prepared with no clicks: a flat honest line; instagram has none.
	if !reflect.DeepEqual(ids, []string{"bluesky", "mastodon", "tiktok", "other"}) {
		t.Fatalf("series %v", ids)
	}
	if len(res.Series[2].Days) != 0 {
		t.Fatalf("tiktok days %v", res.Series[2].Days)
	}
	other := res.Series[3]
	if !other.IsOther || len(other.Channels) != 0 || other.Channels == nil {
		t.Fatalf("other %+v", other)
	}
	assertInvariant(t, res, r)
}

func TestBuildResultsFold(t *testing.T) {
	posts := []Post{
		{ID: 1, Channel: "bluesky", ShortLinkKey: sp("b1")},
		{ID: 2, Channel: "mastodon", ShortLinkKey: sp("m1")},
		{ID: 3, Channel: "mastodon", ShortLinkKey: sp("m2")},
	}
	// m2 is not charted by name: it is inside ShortLinks' fold, so mastodon
	// is not exact and its named series m1 moves to Other too.
	other := shortlinks.LinkSeries{IsOther: true, Days: []shortlinks.DayBucket{day("2026-10-03", 4)}}
	r := rollup(
		[]shortlinks.LinkBucket{lb("b1", 5), lb("m1", 2), lb("m2", 4)},
		[]shortlinks.LinkSeries{ls("b1", day("2026-10-02", 5)), ls("m1", day("2026-10-02", 2)), other},
	)
	res := BuildResults(posts, r)
	if res.Channels[1].HasDailySeries || res.Channels[1].Clicks != 6 {
		t.Fatalf("mastodon %+v", res.Channels[1])
	}
	last := res.Series[len(res.Series)-1]
	if !last.IsOther || !reflect.DeepEqual(last.Channels, []string{"mastodon"}) {
		t.Fatalf("other %+v", last)
	}
	if !reflect.DeepEqual(last.Days, []shortlinks.DayBucket{day("2026-10-02", 2), day("2026-10-03", 4)}) {
		t.Fatalf("other days %v", last.Days)
	}
	if res.Series[0].ID != "bluesky" {
		t.Fatalf("series %+v", res.Series)
	}
	assertInvariant(t, res, r)
}

func TestBuildResultsEmptyRollup(t *testing.T) {
	res := BuildResults(nil, &shortlinks.CampaignRollup{})
	if len(res.Series) != 0 || len(res.PostClicks) != 0 || len(res.Channels) != len(channels) {
		t.Fatalf("%+v", res)
	}
}
