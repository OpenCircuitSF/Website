package guideposts

import (
	"reflect"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
)

func ovTopics() []guides.Guide {
	return []guides.Guide{{Kind: guides.Topic, Slug: "a", Title: "A"}, {Kind: guides.Topic, Slug: "b", Title: "B"}}
}

func ovPost(slug, channel, campaign, status, key string, posted *time.Time) Post {
	p := Post{GuideSlug: slug, Channel: channel, CampaignSlug: campaign, Status: status, PostedAt: posted}
	if key != "" {
		k := key
		p.ShortLinkKey = &k
	}
	return p
}

func at(day int) *time.Time {
	t := time.Date(2026, 11, day, 12, 0, 0, 0, time.UTC)
	return &t
}

func TestOverview_IncludesNeverPostedGuides(t *testing.T) {
	rows := Overview(ovTopics(), nil, "https://s.test/", nil, true)
	if len(rows) != 2 || rows[0].Slug != "a" || rows[1].Slug != "b" {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[1]
	if r.LastPostedAt != nil || r.LastCampaign != "" || r.PostedCount != 0 || r.URL != "https://s.test/guides/b" {
		t.Errorf("never-posted row = %+v", r)
	}
	if r.Channels == nil || len(r.Channels) != 0 || r.Campaigns == nil {
		t.Errorf("channels/campaigns must be empty, not nil: %+v", r)
	}
	if r.Clicks == nil || *r.Clicks != 0 {
		t.Errorf("clicks = %v, want 0", r.Clicks)
	}
}

func TestOverview_LastPostedCampaignAndChannels(t *testing.T) {
	posts := []Post{
		ovPost("a", "mastodon", "guides-2026-10", "posted", "k1", at(1)),
		ovPost("a", "bluesky", "guides-2026-11", "posted", "k2", at(9)),
		ovPost("a", "bluesky", "guides-2026-12", "draft", "", nil),
	}
	r := Overview(ovTopics(), posts, "https://s.test", nil, false)[0]
	if !r.LastPostedAt.Equal(*at(9)) || r.LastCampaign != "guides-2026-11" || r.PostedCount != 2 {
		t.Errorf("row = %+v", r)
	}
	if !reflect.DeepEqual(r.Channels, []string{"bluesky", "mastodon"}) ||
		!reflect.DeepEqual(r.Campaigns, []string{"guides-2026-10", "guides-2026-11"}) {
		t.Errorf("channels %v campaigns %v", r.Channels, r.Campaigns)
	}
}

func TestOverview_ClicksSumAcrossCampaigns(t *testing.T) {
	posts := []Post{
		ovPost("a", "bluesky", "c1", "posted", "k1", at(1)),
		ovPost("a", "bluesky", "c2", "posted", "k2", at(2)),
	}
	r := Overview(ovTopics(), posts, "", map[string]int64{"k1": 3, "k2": 4}, true)[0]
	if r.Clicks == nil || *r.Clicks != 7 {
		t.Errorf("clicks = %v, want 7", r.Clicks)
	}
}

func TestOverview_ClicksUnknownIsNull(t *testing.T) {
	posts := []Post{ovPost("a", "bluesky", "c1", "posted", "k1", at(1)), ovPost("a", "bluesky", "c2", "posted", "k2", at(2))}
	if r := Overview(ovTopics(), posts, "", map[string]int64{"k1": 3}, true)[0]; r.Clicks != nil {
		t.Errorf("a missing key must make clicks null, got %d", *r.Clicks)
	}
	if r := Overview(ovTopics(), posts, "", map[string]int64{"k1": 3, "k2": 1}, false)[0]; r.Clicks != nil {
		t.Error("clicksKnown=false must make clicks null")
	}
}

func TestOverview_IgnoresUnknownSlug(t *testing.T) {
	rows := Overview(ovTopics(), []Post{ovPost("gone", "bluesky", "c", "posted", "k", at(1))}, "", map[string]int64{}, true)
	if len(rows) != 2 || rows[0].PostedCount != 0 || rows[1].PostedCount != 0 {
		t.Errorf("rows = %+v", rows)
	}
}
