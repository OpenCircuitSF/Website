package handlers

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// bannedOutreachJargon reads the banned-word list the SPA's copy guard uses
// (#0591) so the two cannot drift: the server sends labels the SPA shows
// verbatim, and those bypass the guard's scan of the .svelte sources.
func bannedOutreachJargon(t *testing.T) *regexp.Regexp {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "lib", "outreachCopy.guard.test.ts"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^export const BANNED = /(.+)/i;`).FindSubmatch(b)
	if m == nil {
		t.Fatal("outreachCopy.guard.test.ts no longer exports BANNED as a /regex/i literal")
	}
	re, err := regexp.Compile("(?i)" + string(m[1]))
	if err != nil {
		t.Fatalf("BANNED does not compile as RE2: %v", err)
	}
	return re
}

// #0593: the labels the server sends carry none of the banned jargon.
func TestOutreachServerLabelsAvoidBannedJargon(t *testing.T) {
	banned := bannedOutreachJargon(t)
	if !banned.MatchString("a Prepare button") || banned.MatchString("Announcement") {
		t.Fatal("the loaded pattern does not behave like the SPA's guard")
	}
	check := func(what, s string) {
		if banned.MatchString(s) {
			t.Errorf("%s %q contains banned jargon", what, s)
		}
	}
	for _, r := range []string{outreach.ThumbReasonNoCover, outreach.ThumbReasonNotUploaded, outreach.ThumbReasonNoStorage, outreach.ThumbReasonMissing, outreach.ThumbReasonFormat, outreach.ThumbReasonTooBig} {
		check("thumbnail reason", r)
	}
	for _, c := range outreach.Channels() {
		check("channel label", c.Label)
	}
	for _, w := range outreach.Waves() {
		check("wave label", w.Label)
	}
	for _, c := range outreach.Channels() {
		for _, w := range outreach.Waves() {
			cl, wl := slotLabels(c.Name, w.Name)
			check("slot channel label", cl)
			check("slot wave label", wl)
		}
	}
}
