package outreach

import (
	"strings"
	"testing"
	"time"
)

func strp(s string) *string { return &s }

func TestChannelTable(t *testing.T) {
	want := []Channel{
		{"bluesky", "Bluesky", "bluesky", "social", 300, true},
		{"mastodon", "Mastodon", "mastodon", "social", 500, true},
		{"instagram", "Instagram", "instagram", "social", 0, false},
		{"tiktok", "TikTok", "tiktok", "video", 0, false},
		{"youtube", "YouTube", "youtube", "video", 0, false},
	}
	got := Channels()
	if len(got) != len(want) {
		t.Fatalf("channels = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("channel %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if _, ok := ChannelByName("myspace"); ok {
		t.Error("unknown channel resolved")
	}
	ws := Waves()
	if len(ws) != 3 || ws[0].Name != "announce" || ws[1].DaysBefore != 7 || ws[2].Name != "last-call" || ws[2].DaysBefore != 1 {
		t.Errorf("waves = %+v", ws)
	}
	if _, ok := WaveByName("last-call"); !ok {
		t.Error("last-call not found")
	}
}

func TestDestinationURLExact(t *testing.T) {
	ch, _ := ChannelByName("bluesky")
	w, _ := WaveByName("reminder")
	got := DestinationURL("https://www.opencircuitsf.com/", Facts{Slug: "soldering-101"}, ch, w)
	want := "https://www.opencircuitsf.com/workshops/soldering-101?utm_campaign=soldering-101&utm_content=reminder&utm_medium=social&utm_source=bluesky"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if rel := DestinationURL("", Facts{Slug: "x"}, ch, w); !strings.HasPrefix(rel, "/workshops/x?") {
		t.Errorf("relative = %q", rel)
	}
}

func TestDraft(t *testing.T) {
	start := time.Date(2026, 11, 7, 19, 30, 0, 0, time.UTC) // 11:30 AM PST
	f := Facts{Slug: "s", Title: "Soldering 101", StartsAt: &start, LocationName: strp("The Shop")}
	for _, ch := range Channels() {
		for _, w := range Waves() {
			d := Draft(ch, w, f)
			for _, need := range []string{"Soldering 101", "Sat, Nov 7 · 11:30 AM", "The Shop", LinkToken} {
				if !strings.Contains(d, need) {
					t.Errorf("%s/%s draft missing %q: %q", ch.Name, w.Name, need, d)
				}
			}
		}
	}
	ig, _ := ChannelByName("instagram")
	an, _ := WaveByName("announce")
	if !strings.Contains(Draft(ig, an, f), "Link in bio") {
		t.Error("instagram draft lacks link-in-bio line")
	}
	bs, _ := ChannelByName("bluesky")
	tba := Draft(bs, an, Facts{Title: "T"})
	if !strings.Contains(tba, "Date TBA") || !strings.Contains(tba, "Location TBA") {
		t.Errorf("tba draft = %q", tba)
	}
}

func TestFingerprint(t *testing.T) {
	start := time.Date(2026, 11, 7, 19, 30, 0, 0, time.UTC)
	base := Facts{Slug: "s", Title: "T", StartsAt: &start, LocationName: strp("A"), LocationAddress: strp("B")}
	fp := Fingerprint(base)
	later := start.Add(time.Hour)
	for name, f := range map[string]Facts{
		"title":    {Title: "T2", StartsAt: &start, LocationName: strp("A"), LocationAddress: strp("B")},
		"date":     {Title: "T", StartsAt: &later, LocationName: strp("A"), LocationAddress: strp("B")},
		"place":    {Title: "T", StartsAt: &start, LocationName: strp("A2"), LocationAddress: strp("B")},
		"address":  {Title: "T", StartsAt: &start, LocationName: strp("A"), LocationAddress: strp("B2")},
		"no-start": {Title: "T", LocationName: strp("A"), LocationAddress: strp("B")},
	} {
		if Fingerprint(f) == fp {
			t.Errorf("fingerprint unchanged by %s", name)
		}
	}
	same := base
	same.Slug = "other-slug"
	if Fingerprint(same) != fp {
		t.Error("slug must not affect the fingerprint")
	}
}
