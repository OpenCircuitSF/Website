package outreach

import (
	"sort"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

// maxResultsDays caps the results window so a long-lived outreach does not
// ask ShortLinks for an unbounded range.
const maxResultsDays = 120

// utcDate truncates t to its UTC midnight.
func utcDate(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ResultsWindow is the half-open UTC-day window [from, to) the results view
// asks ShortLinks for. It starts on the day outreach began and ends tomorrow,
// so today's clicks so far are included, clamped to the day after the
// workshop when there is one. Always pass it explicitly: ShortLinks' default
// window is the campaign's own start-to-end span, which for these campaigns
// is the workshop session itself.
func ResultsWindow(createdAt time.Time, startsAt *time.Time, now time.Time) (from, to time.Time) {
	from = utcDate(createdAt)
	to = utcDate(now).AddDate(0, 0, 1)
	if startsAt != nil {
		if limit := utcDate(*startsAt).AddDate(0, 0, 2); limit.Before(to) {
			to = limit
		}
	}
	if !to.After(from) {
		to = from.AddDate(0, 0, 1)
	}
	if to.Sub(from) > maxResultsDays*24*time.Hour {
		from = to.AddDate(0, 0, -maxResultsDays)
	}
	return from, to
}

// ChannelClicks is one channel's click total and chart state.
type ChannelClicks struct {
	Name           string
	Label          string
	Clicks         int64
	HasDailySeries bool
	Prepared       int
}

// Series is one line on the daily chart.
type Series struct {
	ID       string   // channel name, or "other"
	Label    string   // channel label, or "Other links"
	Channels []string // [name] for a channel; the folded channel names for "other"
	IsOther  bool
	Days     []shortlinks.DayBucket // ascending, zero days omitted
}

// Results is a campaign rollup shaped around a workshop's posts.
type Results struct {
	ClickCount       int64
	ExcludedBotCount int64
	UnmatchedClicks  int64
	WindowFrom       string
	WindowTo         string
	PostClicks       map[int64]int64 // post id -> clicks; prepared posts only
	Channels         []ChannelClicks // every channel, Channels() order
	Series           []Series        // channel series in Channels() order, then "other"
}

// addDays merges src into dst, summing per date.
func addDays(dst map[string]int64, src []shortlinks.DayBucket) {
	for _, d := range src {
		dst[d.Date] += d.Count
	}
}

func sortedDays(m map[string]int64) []shortlinks.DayBucket {
	out := make([]shortlinks.DayBucket, 0, len(m))
	for date, n := range m {
		if n != 0 {
			out = append(out, shortlinks.DayBucket{Date: date, Count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// BuildResults matches a rollup's per-link clicks to posts by short link key.
//
// A channel gets its own daily line only when that is exact: every one of its
// links that has clicks must be one of the (at most six) links ShortLinks
// charts by name. Anything else, including ShortLinks' own "other" fold,
// sums into one "Other links" series, so the series always add up to the
// campaign's daily totals. Daily shares are never estimated.
func BuildResults(posts []Post, r *shortlinks.CampaignRollup) Results {
	res := Results{
		ClickCount: r.ClickCount, ExcludedBotCount: r.ExcludedBotCount,
		WindowFrom: r.WindowFrom, WindowTo: r.WindowTo,
		PostClicks: map[int64]int64{},
	}
	keyPost := map[string]Post{}
	prepared := map[string]int{}
	for _, p := range posts {
		if p.ShortLinkKey == nil || *p.ShortLinkKey == "" {
			continue
		}
		keyPost[*p.ShortLinkKey] = p
		res.PostClicks[p.ID] = 0
		prepared[p.Channel]++
	}
	chClicks := map[string]int64{}
	keyClicks := map[string]int64{}
	for _, row := range r.ByLink {
		p, ok := keyPost[row.Key]
		if row.Key == "" || !ok {
			res.UnmatchedClicks += row.Count
			continue
		}
		res.PostClicks[p.ID] += row.Count
		chClicks[p.Channel] += row.Count
		keyClicks[row.Key] += row.Count
	}
	named := map[string]bool{}
	for _, s := range r.SeriesByLink {
		if !s.IsOther && s.Key != "" {
			named[s.Key] = true
		}
	}
	exact := map[string]bool{}
	for _, ch := range channels {
		ok := true
		for key, p := range keyPost {
			if p.Channel == ch.Name && keyClicks[key] > 0 && !named[key] {
				ok = false
			}
		}
		exact[ch.Name] = ok
	}

	chDays := map[string]map[string]int64{}
	other := map[string]int64{}
	for _, s := range r.SeriesByLink {
		p, isPost := keyPost[s.Key]
		switch {
		case s.IsOther || s.Key == "" || !isPost || !exact[p.Channel]:
			addDays(other, s.Days)
		default:
			if chDays[p.Channel] == nil {
				chDays[p.Channel] = map[string]int64{}
			}
			addDays(chDays[p.Channel], s.Days)
		}
	}

	var folded []string
	for _, ch := range channels {
		cc := ChannelClicks{Name: ch.Name, Label: ch.Label, Clicks: chClicks[ch.Name], Prepared: prepared[ch.Name]}
		if !exact[ch.Name] {
			folded = append(folded, ch.Name)
		}
		if exact[ch.Name] && prepared[ch.Name] > 0 {
			cc.HasDailySeries = true
			res.Series = append(res.Series, Series{ID: ch.Name, Label: ch.Label, Channels: []string{ch.Name}, Days: sortedDays(chDays[ch.Name])})
		}
		res.Channels = append(res.Channels, cc)
	}
	if od := sortedDays(other); len(od) > 0 {
		if folded == nil {
			folded = []string{}
		}
		res.Series = append(res.Series, Series{ID: "other", Label: "Other links", Channels: folded, IsOther: true, Days: od})
	}
	return res
}
