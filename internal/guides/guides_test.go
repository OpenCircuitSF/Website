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
	if len(ts) != 38 {
		t.Fatalf("got %d topics, want 38", len(ts))
	}
	if len(All()) != 39 || All()[0].Kind != Workshop {
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
	const sets = `"sets":[{"id":"s","title":"S","blurb":"b"}],`
	const w = `{"slug":"w","title":"W","hook":"h","set":"s"}`
	const tp = `{"slug":"t","title":"T","hook":"h","minutes":2,"post":"p","set":"s"}`
	cases := map[string]string{
		"malformed JSON":                     `{`,
		"empty lists":                        `{` + sets + `"workshopGuides":[],"topics":[]}`,
		"duplicate slug":                     `{` + sets + `"workshopGuides":[` + w + `],"topics":[{"slug":"w","title":"T","hook":"h","minutes":2,"post":"p","set":"s"}]}`,
		"bad slug":                           `{` + sets + `"workshopGuides":[{"slug":"Bad_Slug","title":"W","hook":"h","set":"s"}],"topics":[` + tp + `]}`,
		"empty title":                        `{` + sets + `"workshopGuides":[{"slug":"w","title":"","hook":"h","set":"s"}],"topics":[` + tp + `]}`,
		"empty hook":                         `{` + sets + `"workshopGuides":[` + w + `],"topics":[{"slug":"t","title":"T","hook":" ","minutes":2,"post":"p","set":"s"}]}`,
		"zero minutes":                       `{` + sets + `"workshopGuides":[` + w + `],"topics":[{"slug":"t","title":"T","hook":"h","post":"p","set":"s"}]}`,
		"soft hyphen in hook-as-description": `{` + sets + `"workshopGuides":[` + w + `],"topics":[{"slug":"t","title":"T","hook":"a­b","minutes":2,"post":"p","set":"s"}]}`,
		"topic without post":                 `{` + sets + `"workshopGuides":[` + w + `],"topics":[{"slug":"t","title":"T","hook":"h","minutes":2,"set":"s"}]}`,
		"workshop guide with post":           `{` + sets + `"workshopGuides":[{"slug":"w","title":"W","hook":"h","post":"p","set":"s"}],"topics":[` + tp + `]}`,
	}
	for name, raw := range cases {
		if _, _, err := load([]byte(raw)); err == nil {
			t.Errorf("%s: load accepted it", name)
		}
	}
	if _, _, err := load([]byte(`{` + sets + `"workshopGuides":[` + w + `],"topics":[` + tp + `]}`)); err != nil {
		t.Errorf("valid catalog rejected: %v", err)
	}
}

// TestSetsLoadedAndEveryGuideHasKnownSet (#0676): the embedded catalog's sets
// load in order and every guide names one of them.
func TestSetsLoadedAndEveryGuideHasKnownSet(t *testing.T) {
	sets := Sets()
	if len(sets) == 0 {
		t.Fatal("no sets loaded")
	}
	known := map[string]bool{}
	for _, s := range sets {
		if s.ID == "" || s.Title == "" {
			t.Errorf("set %+v lacks an id or title", s)
		}
		known[s.ID] = true
	}
	if !known["esp32"] {
		t.Error("the esp32 set is missing")
	}
	for _, g := range All() {
		if !known[g.Set] {
			t.Errorf("%s names unknown set %q", g.Slug, g.Set)
		}
	}
	if Workshop.String() != "workshop" || Topic.String() != "topic" {
		t.Errorf("Kind.String: %q, %q", Workshop, Topic)
	}
}

func TestLoadRejectsUnknownSet(t *testing.T) {
	raw := `{"sets":[{"id":"s","title":"S"}],"workshopGuides":[{"slug":"w","title":"W","hook":"h","set":"s"}],` +
		`"topics":[{"slug":"t","title":"T","hook":"h","minutes":2,"post":"p","set":"nope"}]}`
	if _, _, err := load([]byte(raw)); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("err = %v, want an undeclared-set error", err)
	}
	dup := `{"sets":[{"id":"s","title":"S"},{"id":"s","title":"S2"}],"workshopGuides":[{"slug":"w","title":"W","hook":"h","set":"s"}],` +
		`"topics":[{"slug":"t","title":"T","hook":"h","minutes":2,"post":"p","set":"s"}]}`
	if _, _, err := load([]byte(dup)); err == nil || !strings.Contains(err.Error(), "duplicate set") {
		t.Fatalf("err = %v, want a duplicate-set error", err)
	}
}

func TestLoadRejectsMissingSet(t *testing.T) {
	raw := `{"sets":[{"id":"s","title":"S"}],"workshopGuides":[{"slug":"w","title":"W","hook":"h"}],` +
		`"topics":[{"slug":"t","title":"T","hook":"h","minutes":2,"post":"p","set":"s"}]}`
	if _, _, err := load([]byte(raw)); err == nil || !strings.Contains(err.Error(), "needs a set") {
		t.Fatalf("err = %v, want a missing-set error", err)
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
