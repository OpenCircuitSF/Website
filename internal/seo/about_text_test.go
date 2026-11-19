package seo

import (
	"os"
	"strings"
	"testing"
)

// TestAboutTextFromSharedSource (#0729): About is the About page's own copy,
// as plain text.
func TestAboutTextFromSharedSource(t *testing.T) {
	a := About()
	if a.Title != "About Open Circuit SF" {
		t.Errorf("Title = %q", a.Title)
	}
	if len(a.Intro) == 0 || !strings.HasPrefix(a.Intro[0], "Open Circuit SF is a San Francisco group") {
		t.Errorf("Intro = %q", a.Intro)
	}
	if a.Links["email"] != "mailto:contact@opencircuitsf.com" {
		t.Errorf("email link = %q", a.Links["email"])
	}
	all := append([]string{a.Title}, a.Intro...)
	found := false
	for _, s := range a.Sections {
		all = append(all, s.Heading, s.Text)
		if s.Heading == "Get involved" && strings.Contains(s.Text, "Join the Discord: https://discord.gg/") {
			found = true
		}
	}
	if !found {
		t.Error("Get involved section lacks the Discord link line")
	}
	for _, s := range all {
		if strings.Contains(s, "<") {
			t.Errorf("markup in %q", s)
		}
	}
}

// TestSiteNameMatchesBranding: SiteName is branding.ts's APP_NAME.
func TestSiteNameMatchesBranding(t *testing.T) {
	b, err := os.ReadFile("../../web/src/lib/branding.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "APP_NAME = '"+SiteName+"'") {
		t.Errorf("branding.ts does not declare APP_NAME = '%s'", SiteName)
	}
}
