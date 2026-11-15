package guideposts

import (
	"sort"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
)

// GuideShare is one catalog guide's sharing history, for the admin's
// recently-shared overview.
type GuideShare struct {
	Slug            string     `json:"slug"`
	Title           string     `json:"title"`
	URL             string     `json:"url"`
	LastPostedAt    *time.Time `json:"last_posted_at"`
	LastCampaign    string     `json:"last_campaign"`
	Channels        []string   `json:"channels"`
	Campaigns       []string   `json:"campaigns"`
	PostedCount     int        `json:"posted_count"`
	NextScheduledAt *time.Time `json:"next_scheduled_at"`
	Clicks          *int64     `json:"clicks"`
}

// Overview builds one GuideShare per entry of topics, in catalog order,
// including guides never posted. Posts for slugs not in topics are ignored.
// clicks is keyed by short-link key; a guide's Clicks is nil when
// clicksKnown is false or any of its linked posts is missing from clicks.
func Overview(topics []guides.Guide, posts []Post, base string, clicks map[string]int64, clicksKnown bool) []GuideShare {
	bySlug := map[string][]Post{}
	for _, p := range posts {
		bySlug[p.GuideSlug] = append(bySlug[p.GuideSlug], p)
	}
	out := make([]GuideShare, 0, len(topics))
	for _, g := range topics {
		row := GuideShare{
			Slug: g.Slug, Title: g.Title,
			URL:      strings.TrimRight(base, "/") + "/guides/" + g.Slug,
			Channels: []string{}, Campaigns: []string{},
		}
		channels, campaigns := map[string]bool{}, map[string]bool{}
		var sum int64
		known := clicksKnown
		for _, p := range bySlug[g.Slug] {
			if p.ShortLinkKey != nil {
				if n, ok := clicks[*p.ShortLinkKey]; ok {
					sum += n
				} else {
					known = false
				}
			}
			if p.Status == "scheduled" && p.ScheduledAt != nil &&
				(row.NextScheduledAt == nil || p.ScheduledAt.Before(*row.NextScheduledAt)) {
				t := *p.ScheduledAt
				row.NextScheduledAt = &t
			}
			if p.Status != "posted" || p.PostedAt == nil {
				continue
			}
			row.PostedCount++
			channels[p.Channel] = true
			campaigns[p.CampaignSlug] = true
			if row.LastPostedAt == nil || p.PostedAt.After(*row.LastPostedAt) {
				t := *p.PostedAt
				row.LastPostedAt = &t
				row.LastCampaign = p.CampaignSlug
			}
		}
		row.Channels = sortedKeys(channels)
		row.Campaigns = sortedKeys(campaigns)
		if known {
			s := sum
			row.Clicks = &s
		}
		out = append(out, row)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
