package guideposts

import (
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

func mustChannel(t *testing.T, name string) outreach.Channel {
	t.Helper()
	ch, ok := outreach.ChannelByName(name)
	if !ok {
		t.Fatalf("no channel %q", name)
	}
	return ch
}

func TestDestinationURL_UniquePerPostAndDeterministic(t *testing.T) {
	bs := mustChannel(t, "bluesky")
	a := DestinationURL("https://site.example.test/", "baud-rate", bs, 7)
	want := "https://site.example.test/guides/baud-rate?utm_campaign=guides&utm_content=gp7&utm_medium=social&utm_source=bluesky"
	if a != want {
		t.Errorf("DestinationURL = %q, want %q", a, want)
	}
	if a != DestinationURL("https://site.example.test", "baud-rate", bs, 7) {
		t.Error("not deterministic")
	}
	if a == DestinationURL("https://site.example.test", "baud-rate", bs, 8) {
		t.Error("two posts share a destination")
	}
}

func TestDefaultBody_EveryTopicFitsEveryChannel(t *testing.T) {
	const real = "https://go.opencircuitsf.com/u/AbC123"
	for _, g := range guides.Topics() {
		body := DefaultBody(g)
		for _, name := range []string{"bluesky", "mastodon"} {
			ch := mustChannel(t, name)
			if err := ValidateDraft(ch, body); err != nil {
				t.Errorf("%s on %s: %v", g.Slug, name, err)
			}
			if err := outreach.ValidateForSend(ch, strings.ReplaceAll(body, outreach.LinkToken, real)); err != nil {
				t.Errorf("%s on %s with a real link: %v", g.Slug, name, err)
			}
		}
	}
}

func TestValidateDraft_MeasuresTokenAsShortURL(t *testing.T) {
	bs := mustChannel(t, "bluesky")
	// 300 graphemes total once the token is the 43-grapheme sample URL.
	fits := strings.Repeat("a", 300-len(sampleShortURL)) + outreach.LinkToken
	if err := ValidateDraft(bs, fits); err != nil {
		t.Errorf("exactly at the limit: %v", err)
	}
	if err := ValidateDraft(bs, "a"+fits); err == nil {
		t.Error("one over the limit passed")
	}
}

func TestMastodonIdempotencyKey_NeverCollidesWithOutreach(t *testing.T) {
	for _, gen := range []int{0, 1, 3} {
		g, o := MastodonIdempotencyKey(42, gen), outreach.MastodonIdempotencyKey(42, gen)
		if g == o {
			t.Errorf("gen %d: both %q", gen, g)
		}
		if !strings.HasPrefix(g, "opencircuitsf-guide-post-42") {
			t.Errorf("key %q", g)
		}
	}
	if MastodonIdempotencyKey(42, 2) != "opencircuitsf-guide-post-42-g2" {
		t.Error("generation suffix")
	}
}

func TestNormalizeCampaignSlug(t *testing.T) {
	if s, err := NormalizeCampaignSlug("  "); err != nil || s != CampaignSlug {
		t.Errorf("blank = %q, %v", s, err)
	}
	if s, err := NormalizeCampaignSlug("guides-spring-2027"); err != nil || s != "guides-spring-2027" {
		t.Errorf("valid = %q, %v", s, err)
	}
	for _, bad := range []string{"Guides", "a_b", "a--b", "-a", "a b", "a/b"} {
		if _, err := NormalizeCampaignSlug(bad); err != ErrBadCampaignSlug {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSuggestedCampaign_UsesPacificMonth(t *testing.T) {
	if got := SuggestedCampaign(time.Date(2026, 12, 1, 3, 0, 0, 0, time.UTC)); got != "guides-2026-11" {
		t.Errorf("SuggestedCampaign = %q, want guides-2026-11", got)
	}
}

func TestClickWindow_StartsOnSinceDayEndsTomorrow(t *testing.T) {
	from, to := ClickWindow(time.Date(2026, 11, 3, 17, 30, 0, 0, time.UTC), time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC))
	if !from.Equal(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("window = %v .. %v", from, to)
	}
}

func TestClickWindow_ClampsTo366Days(t *testing.T) {
	from, to := ClickWindow(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC))
	if to.Sub(from) != 366*24*time.Hour {
		t.Errorf("span = %v", to.Sub(from))
	}
}

func TestPostRef_MatchesDestinationUTMContent(t *testing.T) {
	u := DestinationURL("https://s.test", "baud-rate", mustChannel(t, "bluesky"), 42)
	if PostRef(42) != "gp42" || !strings.Contains(u, "utm_content="+PostRef(42)) {
		t.Errorf("PostRef %q not in %q", PostRef(42), u)
	}
}

func TestContainsURL_RequiresBoundary(t *testing.T) {
	const u = "https://go.test/u/abc"
	cases := map[string]bool{
		"x " + u:         true,
		u + " more":      true,
		"see " + u + ".": true,
		u + "d":          false,
		u + "_":          false,
		"none":           false,
		u + "d and " + u: true,
	}
	for text, want := range cases {
		if got := containsURL(text, u); got != want {
			t.Errorf("containsURL(%q) = %v, want %v", text, got, want)
		}
	}
}
