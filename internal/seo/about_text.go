package seo

import (
	"maps"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
)

// SiteName is the group's name, the same as web/src/lib/branding.ts's
// APP_NAME (TestSiteNameMatchesBranding pins the two together).
const SiteName = "Open Circuit SF"

// About is the About page's copy as plain text (#0729), built from the same
// embedded web/src/content/about.json the page renders. No HTML anywhere:
// a paragraph is its text and link label, a list is "- item" lines, and a
// links block is "Label: href" lines.
func About() publicapi.AboutText {
	page := staticPages["/about"]
	a := publicapi.AboutText{Title: page.H1, Links: maps.Clone(page.Links)}
	for _, b := range page.Intro {
		a.Intro = append(a.Intro, blockText(b))
	}
	for _, s := range page.Sections {
		lines := make([]string, 0, len(s.Blocks))
		for _, b := range s.Blocks {
			lines = append(lines, blockText(b))
		}
		a.Sections = append(a.Sections, publicapi.AboutSection{Heading: s.Heading, Text: strings.Join(lines, "\n")})
	}
	return a
}

// blockText is one content block as plain text.
func blockText(b staticBlock) string {
	switch b.Type {
	case "list":
		lines := make([]string, len(b.Items))
		for i, it := range b.Items {
			lines[i] = "- " + it
		}
		return strings.Join(lines, "\n")
	case "links":
		lines := make([]string, len(b.Links))
		for i, l := range b.Links {
			lines[i] = l.Label + ": " + l.Href
		}
		return strings.Join(lines, "\n")
	}
	return strings.TrimSpace(b.Strong + " " + b.Text + b.Link.Label + b.Tail)
}
