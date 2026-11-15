package guides

import (
	"strings"
	"testing"
)

func TestCatalogLoadsEmbeddedGuides(t *testing.T) {
	ws := WorkshopGuides()
	if len(ws) != 1 || ws[0].Slug != "programming-leds" || ws[0].Title != "Programming LEDs" {
		t.Fatalf("workshop guides = %+v", ws)
	}
	ts := Topics()
	if len(ts) != 16 {
		t.Fatalf("got %d topics, want 16", len(ts))
	}
	if len(All()) != 17 || All()[0].Kind != Workshop {
		t.Fatalf("All() = %+v", All())
	}
	b, ok := Lookup("baud-rate")
	if !ok {
		t.Fatal("baud-rate missing")
	}
	if !strings.ContainsRune(b.Hook, 0xad) {
		t.Error("baud-rate hook should keep its soft hyphen on the page")
	}
	if strings.ContainsRune(b.Description, 0xad) || b.Description == b.Hook {
		t.Errorf("baud-rate description should be its plain override, got %q", b.Description)
	}
	d, _ := Lookup("delay-vs-millis")
	if d.Description != d.Hook {
		t.Errorf("a topic without an override describes itself by its hook")
	}
}

func TestLookupAndKinds(t *testing.T) {
	if !IsWorkshopGuide("programming-leds") || IsTopic("programming-leds") {
		t.Error("programming-leds is a workshop guide")
	}
	if !IsTopic("baud-rate") || IsWorkshopGuide("baud-rate") {
		t.Error("baud-rate is a topic")
	}
	if _, ok := Lookup("nope"); ok || IsTopic("nope") || IsWorkshopGuide("programming-leds-2") {
		t.Error("unknown slugs must miss")
	}
}

func TestGuidePath(t *testing.T) {
	if p := (Guide{Kind: Workshop, Slug: "x"}).Path(); p != "/workshops/x" {
		t.Errorf("workshop path %q", p)
	}
	if p := (Guide{Kind: Topic, Slug: "y"}).Path(); p != "/guides/y" {
		t.Errorf("topic path %q", p)
	}
}

func TestLoadRejectsBadCatalog(t *testing.T) {
	const w = `{"slug":"w","title":"W","hook":"h"}`
	const tp = `{"slug":"t","title":"T","hook":"h","minutes":2,"post":"p"}`
	cases := map[string]string{
		"malformed JSON":                     `{`,
		"empty lists":                        `{"workshopGuides":[],"topics":[]}`,
		"duplicate slug":                     `{"workshopGuides":[` + w + `],"topics":[{"slug":"w","title":"T","hook":"h","minutes":2,"post":"p"}]}`,
		"bad slug":                           `{"workshopGuides":[{"slug":"Bad_Slug","title":"W","hook":"h"}],"topics":[` + tp + `]}`,
		"empty title":                        `{"workshopGuides":[{"slug":"w","title":"","hook":"h"}],"topics":[` + tp + `]}`,
		"empty hook":                         `{"workshopGuides":[` + w + `],"topics":[{"slug":"t","title":"T","hook":" ","minutes":2,"post":"p"}]}`,
		"zero minutes":                       `{"workshopGuides":[` + w + `],"topics":[{"slug":"t","title":"T","hook":"h","post":"p"}]}`,
		"soft hyphen in hook-as-description": `{"workshopGuides":[` + w + `],"topics":[{"slug":"t","title":"T","hook":"a­b","minutes":2,"post":"p"}]}`,
		"topic without post":                 `{"workshopGuides":[` + w + `],"topics":[{"slug":"t","title":"T","hook":"h","minutes":2}]}`,
		"workshop guide with post":           `{"workshopGuides":[{"slug":"w","title":"W","hook":"h","post":"p"}],"topics":[` + tp + `]}`,
	}
	for name, raw := range cases {
		if _, err := load([]byte(raw)); err == nil {
			t.Errorf("%s: load accepted it", name)
		}
	}
	if _, err := load([]byte(`{"workshopGuides":[` + w + `],"topics":[` + tp + `]}`)); err != nil {
		t.Errorf("valid catalog rejected: %v", err)
	}
}

func TestTopicsCarryPostCopy(t *testing.T) {
	for _, g := range Topics() {
		if strings.TrimSpace(g.Post) == "" || !isPlainText(g.Post) {
			t.Errorf("topic %q post copy = %q", g.Slug, g.Post)
		}
	}
	for _, g := range WorkshopGuides() {
		if g.Post != "" {
			t.Errorf("workshop guide %q carries post copy", g.Slug)
		}
	}
}
