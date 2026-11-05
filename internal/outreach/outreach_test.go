package outreach

import (
	"encoding/json"
	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/serversettings"
	"os"
	"regexp"
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
		{"youtube", "YouTube Shorts", "youtube", "video", 0, false},
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

// #0622: the screens say Final; the stored name and the post copy do not change.
func TestThirdWaveIsLabelledFinal(t *testing.T) {
	w, ok := WaveByName("last-call")
	if !ok {
		t.Fatal("last-call wave not found")
	}
	if w.Label != "Final" {
		t.Errorf("Label = %q, want Final", w.Label)
	}
	if w.DaysBefore != 1 {
		t.Errorf("DaysBefore = %d, want 1", w.DaysBefore)
	}
	if _, ok := WaveByName("final"); ok {
		t.Error("a wave is named final; the stored name must stay last-call")
	}
}

func TestDestinationURLExact(t *testing.T) {
	ch, _ := ChannelByName("bluesky")
	w, _ := WaveByName("reminder")
	got := DestinationURL("https://www.opencircuitsf.com/", Facts{Slug: "soldering-101"}, ch, w)
	want := "https://www.opencircuitsf.com/events/soldering-101?utm_campaign=soldering-101&utm_content=reminder&utm_medium=social&utm_source=bluesky"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if rel := DestinationURL("", Facts{Slug: "x"}, ch, w); !strings.HasPrefix(rel, "/events/x?") {
		t.Errorf("relative = %q", rel)
	}
}

func TestDraft(t *testing.T) {
	start := time.Date(2026, 11, 7, 19, 30, 0, 0, time.UTC) // 11:30 AM PST
	f := Facts{Slug: "s", Title: "Soldering 101", StartsAt: &start, LocationName: strp("The Shop")}
	for _, ch := range Channels() {
		for _, w := range Waves() {
			d := Draft(ch, w, f)
			if strings.Contains(d, "\u00b7") || strings.Contains(d, "New workshop:") {
				t.Errorf("%s/%s draft has a label or separator: %q", ch.Name, w.Name, d)
			}
			for _, need := range []string{"Soldering 101", "The Shop"} {
				if !strings.Contains(d, need) {
					t.Errorf("%s/%s draft missing %q: %q", ch.Name, w.Name, need, d)
				}
			}
			hasLink := strings.Contains(d, LinkToken)
			wantLink := ch.Name != "bluesky" && ch.Name != "instagram"
			if hasLink != wantLink {
				t.Errorf("%s/%s link token present = %v, want %v: %q", ch.Name, w.Name, hasLink, wantLink, d)
			}
		}
	}
	ig, _ := ChannelByName("instagram")
	an, _ := WaveByName("announce")
	if d := Draft(ig, an, f); !strings.Contains(d, "Sign up at the link in our bio.") || strings.Contains(d, "http") {
		t.Errorf("instagram draft = %q", d)
	}
	bs, _ := ChannelByName("bluesky")
	tba := Draft(bs, an, Facts{Title: "T"})
	if tba != "We're running T." {
		t.Errorf("undated, venueless draft = %q", tba)
	}
}

// ledsFacts is the Programming-LEDs-shaped fixture of the content review.
func ledsFacts() Facts {
	start := time.Date(2026, 10, 20, 1, 30, 0, 0, time.UTC) // Mon Oct 19, 6:30 PM PDT
	return Facts{
		Slug: "programming-leds", Title: "Programming LEDs", StartsAt: &start,
		LocationName: strp("Sequoia Fabrica"),
		Summary:      "Wire up a strip of addressable LEDs and write a few lines of code to make them do something. Take the board home.",
		Hashtags:     "#SanFrancisco #Electronics",
	}
}

// TestDraftNeverNamesAType (#0615): Facts has no type field, so a draft for a
// Hack Night or any other type has no way to call itself a workshop.
func TestDraftNeverNamesAType(t *testing.T) {
	start := time.Date(2026, 11, 7, 19, 30, 0, 0, time.UTC)
	f := Facts{Slug: "s", Title: "Solder Social", StartsAt: &start, LocationName: strp("The Shop")}
	for _, ch := range Channels() {
		for _, w := range Waves() {
			if d := Draft(ch, w, f); strings.Contains(strings.ToLower(d), "workshop") {
				t.Errorf("%s/%s draft says workshop: %q", ch.Name, w.Name, d)
			}
		}
	}
}

func TestDraftGolden(t *testing.T) {
	want := map[string]string{
		"bluesky/announce":    "We're running Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nWire up a strip of addressable LEDs and write a few lines of code to make them do something. Take the board home.\n\n#SanFrancisco #Electronics",
		"bluesky/reminder":    "One week out: Programming LEDs is happening at Sequoia Fabrica on Monday, Oct 19.\n\nWire up a strip of addressable LEDs and write a few lines of code to make them do something. Take the board home.\n\nA few seats may still be open.\n\n#SanFrancisco #Electronics",
		"bluesky/last-call":   "Tomorrow: Programming LEDs at Sequoia Fabrica. Last call to sign up.\n\n#SanFrancisco #Electronics",
		"mastodon/announce":   "Join us for Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nWire up a strip of addressable LEDs and write a few lines of code to make them do something. Take the board home.\n\nSpots are limited, so you can sign up here: {link}\n\n#SanFrancisco #Electronics",
		"mastodon/reminder":   "One week to go until Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nWire up a strip of addressable LEDs and write a few lines of code to make them do something. Take the board home.\n\nSign up here: {link}\n\n#SanFrancisco #Electronics",
		"mastodon/last-call":  "Last call for Programming LEDs at Sequoia Fabrica, tomorrow.\n\nSign up here: {link}\n\n#SanFrancisco #Electronics",
		"instagram/announce":  "Join us for Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nWire up a strip of addressable LEDs and write a few lines of code to make them do something. Take the board home.\n\nSign up at the link in our bio.\n\n#SanFrancisco #Electronics",
		"instagram/last-call": "Last call for Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nSign up at the link in our bio.\n\n#SanFrancisco #Electronics",
		"instagram/reminder":  "Coming up: Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nWire up a strip of addressable LEDs and write a few lines of code to make them do something. Take the board home.\n\nSign up at the link in our bio.\n\n#SanFrancisco #Electronics",
		"tiktok/announce":     "Join us for Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nWire up a strip of addressable LEDs and write a few lines of code to make them do something.\n\nSign up: {link}\n\nOpen Circuit SF: https://www.opencircuitsf.com\n\n#SanFrancisco #Electronics",
		"tiktok/last-call":    "Last call for Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nSign up: {link}\n\nOpen Circuit SF: https://www.opencircuitsf.com\n\n#SanFrancisco #Electronics",
		"tiktok/reminder":     "Coming up: Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nWire up a strip of addressable LEDs and write a few lines of code to make them do something.\n\nSign up: {link}\n\nOpen Circuit SF: https://www.opencircuitsf.com\n\n#SanFrancisco #Electronics",
		"youtube/announce":    "Join us for Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nWire up a strip of addressable LEDs and write a few lines of code to make them do something.\n\nSign up: {link}\n\nOpen Circuit SF: https://www.opencircuitsf.com\n\n#SanFrancisco #Electronics",
		"youtube/last-call":   "Last call for Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nSign up: {link}\n\nOpen Circuit SF: https://www.opencircuitsf.com\n\n#SanFrancisco #Electronics",
		"youtube/reminder":    "Coming up: Programming LEDs at Sequoia Fabrica on Monday, Oct 19.\n\nWire up a strip of addressable LEDs and write a few lines of code to make them do something.\n\nSign up: {link}\n\nOpen Circuit SF: https://www.opencircuitsf.com\n\n#SanFrancisco #Electronics",
	}
	f := ledsFacts()
	for _, ch := range Channels() {
		for _, w := range Waves() {
			got := Draft(ch, w, f)
			if exp, ok := want[ch.Name+"/"+w.Name]; ok && got != exp {
				t.Errorf("%s/%s:\n got %q\nwant %q", ch.Name, w.Name, got, exp)
			}
			if ch.CharLimit > 0 {
				body := strings.ReplaceAll(got, LinkToken, "https://go.opencircuitsf.com/u/pcNPlc")
				if err := ValidateForSend(ch, body); err != nil {
					t.Errorf("%s/%s: %v", ch.Name, w.Name, err)
				}
			}
		}
	}
}

// handChannels are the three the same video goes to by hand (#0635).
var handChannels = []string{"instagram", "tiktok", "youtube"}

func firstParagraph(s string) string {
	head, _, _ := strings.Cut(s, "\n\n")
	return head
}

func TestHandDraftsOpenDifferentlyBySection(t *testing.T) {
	want := map[string]string{"announce": "Join us for ", "reminder": "Coming up: ", "last-call": "Last call for "}
	for _, name := range handChannels {
		ch, _ := ChannelByName(name)
		seen := map[string]string{}
		for _, w := range Waves() {
			lead := firstParagraph(Draft(ch, w, ledsFacts()))
			if !strings.HasPrefix(lead, want[w.Name]) {
				t.Errorf("%s/%s opens %q, want prefix %q", name, w.Name, lead, want[w.Name])
			}
			if other, dup := seen[lead]; dup {
				t.Errorf("%s: %s and %s open the same: %q", name, other, w.Name, lead)
			}
			seen[lead] = w.Name
		}
	}
}

func TestHandDraftFinalDropsTheSummary(t *testing.T) {
	const summary = "Wire up a strip"
	for _, name := range handChannels {
		ch, _ := ChannelByName(name)
		for _, w := range Waves() {
			d := Draft(ch, w, ledsFacts())
			if has := strings.Contains(d, summary); has != (w.Name != "last-call") {
				t.Errorf("%s/%s: summary present = %v: %q", name, w.Name, has, d)
			}
			// A caption stays up, so a relative day would become false.
			low := strings.ToLower(d)
			if strings.Contains(low, "tomorrow") || strings.Contains(low, "one week") {
				t.Errorf("%s/%s carries a relative day: %q", name, w.Name, d)
			}
		}
	}
}

func TestHandDraftUndated(t *testing.T) {
	f := Facts{Title: "Hack Night", LocationName: strp("Sequoia Fabrica"), Hashtags: "#sf"}
	for _, name := range handChannels {
		ch, _ := ChannelByName(name)
		for _, w := range Waves() {
			d := Draft(ch, w, f)
			if w.Name == "reminder" && firstParagraph(d) != "Coming up: Hack Night at Sequoia Fabrica." {
				t.Errorf("%s/reminder undated = %q", name, d)
			}
			if strings.Contains(d, " on ") || strings.Contains(d, "One week") {
				t.Errorf("%s/%s undated claims a date: %q", name, w.Name, d)
			}
		}
	}
}

func TestHandDraftsFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/hand_drafts.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]string
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture) != 9 {
		t.Errorf("fixture has %d keys, want 9", len(fixture))
	}
	for _, name := range handChannels {
		ch, _ := ChannelByName(name)
		for _, w := range Waves() {
			key := name + "/" + w.Name
			got, ok := fixture[key]
			if !ok {
				t.Errorf("%s is missing from the fixture", key)
				continue
			}
			if want := Draft(ch, w, ledsFacts()); got != want {
				t.Errorf("%s:\n fixture %q\n   draft %q", key, got, want)
			}
		}
	}
}

func TestDraftsCarryNoClockTime(t *testing.T) {
	timeRe := regexp.MustCompile(`\d{1,2}:\d{2}|\b[AP]M\b`)
	for _, f := range []Facts{ledsFacts(), {Title: "T", StartsAt: ledsFacts().StartsAt}} {
		for _, ch := range Channels() {
			for _, w := range Waves() {
				if d := Draft(ch, w, f); timeRe.MatchString(d) {
					t.Errorf("%s/%s carries a clock time: %q", ch.Name, w.Name, d)
				}
			}
		}
	}
}

func TestDraftBlueskyAndMastodonDiffer(t *testing.T) {
	bs, _ := ChannelByName("bluesky")
	ma, _ := ChannelByName("mastodon")
	for _, f := range []Facts{ledsFacts(), {Title: "T"}, {Title: "T", Summary: "S."}} {
		for _, w := range Waves() {
			if Draft(bs, w, f) == Draft(ma, w, f) {
				t.Errorf("%s: bluesky and mastodon drafts are byte-identical for %+v", w.Name, f)
			}
		}
	}
}

func TestDraftTruncatesSummaryToFit(t *testing.T) {
	f := ledsFacts()
	f.Summary = strings.Repeat("A sentence that fills the space. ", 20)
	for _, name := range []string{"bluesky", "mastodon"} {
		ch, _ := ChannelByName(name)
		for _, w := range Waves() {
			d := Draft(ch, w, f)
			body := strings.ReplaceAll(d, LinkToken, "https://go.opencircuitsf.com/u/pcNPlc")
			if err := ValidateForSend(ch, body); err != nil {
				t.Errorf("%s/%s: %v", name, w.Name, err)
			}
		}
	}
	bs, _ := ChannelByName("bluesky")
	an, _ := WaveByName("announce")
	if d := Draft(bs, an, f); !strings.Contains(d, "fills the space.\n\n#") {
		t.Errorf("summary not cut at a sentence boundary: %q", d)
	}
	f.Summary = strings.Repeat("word ", 200)
	if d := Draft(bs, an, f); !strings.Contains(d, "\u2026") || Length(bs, d) > 300 {
		t.Errorf("one overlong sentence not cut at a word: %q", d)
	}
}

// An abbreviation is not a sentence end, so a cut never leaves "e.g." alone.
func TestSplitSentencesKeepsAbbreviations(t *testing.T) {
	got := splitSentences("Learn the basics, e.g. soldering and Ohm's law. Meet Dr. Ada at St. Mary's. Bring a laptop!")
	want := []string{"Learn the basics, e.g. soldering and Ohm's law.", "Meet Dr. Ada at St. Mary's.", "Bring a laptop!"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("splitSentences = %q, want %q", got, want)
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
	for name, f := range map[string]Facts{
		"summary": func() Facts { g := base; g.Summary = "New words"; return g }(),
		"tagline": func() Facts { g := base; g.Tagline = "Hook"; return g }(),
	} {
		if Fingerprint(f) == fp {
			t.Errorf("fingerprint unchanged by %s", name)
		}
	}
	tags := base
	tags.Hashtags = "#X"
	if Fingerprint(tags) != fp {
		t.Error("hashtags must not affect the fingerprint")
	}
	same := base
	same.Slug = "other-slug"
	if Fingerprint(same) != fp {
		t.Error("slug must not affect the fingerprint")
	}
}

// The hashtag line is the draft's own last paragraph, Bluesky makes each tag a
// facet, and the length check counts it (#0573).
func TestDraftHashtagsAreFacetsAndCounted(t *testing.T) {
	f := ledsFacts()
	f.Summary = strings.Repeat("A sentence that fills the space. ", 20)
	bs, _ := ChannelByName("bluesky")
	an, _ := WaveByName("announce")
	d := Draft(bs, an, f)
	if !strings.HasSuffix(d, "\n\n#SanFrancisco #Electronics") {
		t.Fatalf("hashtags are not the final line: %q", d)
	}
	var tags []string
	for _, fc := range bluesky.DetectFacets(d) {
		for _, ft := range fc.Features {
			if ft.Tag != "" {
				tags = append(tags, ft.Tag)
				if got := d[fc.Index.ByteStart:fc.Index.ByteEnd]; got != "#"+ft.Tag {
					t.Errorf("facet covers %q, want %q", got, "#"+ft.Tag)
				}
			}
		}
	}
	if strings.Join(tags, ",") != "SanFrancisco,Electronics" {
		t.Errorf("tag facets = %v", tags)
	}
	// The tags are inside the limit: the summary gave way to them, not the reverse.
	if n := Length(bs, d); n > bs.CharLimit {
		t.Errorf("draft is %d graphemes, over %d", n, bs.CharLimit)
	}
	f.Hashtags = ""
	if without := Draft(bs, an, f); Length(bs, without) <= Length(bs, d) || strings.Contains(without, "#") {
		t.Errorf("tags not counted against the summary budget: %d vs %d", Length(bs, without), Length(bs, d))
	}
}

// serversettings spells the channel names out (it cannot import this
// package); this keeps its list equal to the real one (#0588).
func TestServerSettingsChannelNamesMatchChannels(t *testing.T) {
	var names []string
	for _, c := range Channels() {
		names = append(names, c.Name)
	}
	got := serversettings.OutreachChannelNames
	if len(got) != len(names) {
		t.Fatalf("serversettings.OutreachChannelNames = %v, want %v", got, names)
	}
	for i := range names {
		if got[i] != names[i] {
			t.Fatalf("serversettings.OutreachChannelNames = %v, want %v", got, names)
		}
	}
}
