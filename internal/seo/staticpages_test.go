// Tests for #0520's single-source static pages (staticpages.go): /about and
// /privacy render their FULL text into the server HTML, and that text comes
// from the same JSON files the SPA imports -- there is no hand-typed second
// copy in this package to keep in step by a guard.
package seo

import (
	"encoding/xml"
	"html"
	"os"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
)

// singleSourcePages pairs each single-source route with the shared JSON
// file that is BOTH the SPA's and this package's source for it, and the
// .svelte view expected to import that file (the SPA half of the
// single-source contract -- a view that hard-typed its text back in would
// drift from the server HTML by construction, and this is the check that
// catches it).
var singleSourcePages = []struct {
	route  string
	source string
	svelte string
}{
	{"/about", "web/src/content/about.json", "web/src/views/About.svelte"},
	{"/privacy", "web/src/content/privacy.json", "web/src/views/PrivacyPolicy.svelte"},
	// #0735: the docs pages, one shared JSON file each, all rendered by Docs.svelte.
	{"/docs", "web/src/content/docs/index.json", "web/src/views/Docs.svelte"},
	{"/docs/using-the-site", "web/src/content/docs/using-the-site.json", "web/src/views/Docs.svelte"},
	{"/docs/newsletter", "web/src/content/docs/newsletter.json", "web/src/views/Docs.svelte"},
	{"/docs/feeds", "web/src/content/docs/feeds.json", "web/src/views/Docs.svelte"},
}

// staticBlockTexts returns each piece of plain text one block contributes
// to the page: the paragraph and its strong lead, a list's items, or a
// links block's anchor labels.
func staticBlockTexts(b staticBlock) []string {
	switch b.Type {
	case "p":
		out := []string{b.Text}
		if b.Strong != "" {
			out = append(out, b.Strong)
		}
		if b.Link.Label != "" {
			out = append(out, b.Link.Label)
		}
		if b.Tail != "" {
			out = append(out, b.Tail)
		}
		return out
	case "list":
		return b.Items
	case "links":
		out := make([]string, 0, len(b.Links))
		for _, l := range b.Links {
			out = append(out, l.Label)
		}
		return out
	case "code":
		return []string{b.Text, b.Label}
	}
	return nil
}

// everyStaticText walks a page's intro and section blocks and returns each
// piece of text the server must render: the h1, every section heading, and
// every block's own texts.
func everyStaticText(sp staticPageData) []string {
	out := []string{sp.H1}
	for _, b := range sp.Intro {
		out = append(out, staticBlockTexts(b)...)
	}
	for _, s := range sp.Sections {
		out = append(out, s.Heading)
		for _, b := range s.Blocks {
			out = append(out, staticBlockTexts(b)...)
		}
	}
	return out
}

// TestRender_SingleSourcePagesServeFullText is #0520's criterion 1: every
// piece of text in the shared source files appears in the raw server HTML
// for its route.
func TestRender_SingleSourcePagesServeFullText(t *testing.T) {
	r := newTestRenderer(nil)
	for _, p := range singleSourcePages {
		sp, ok := staticPages[p.route]
		if !ok {
			t.Fatalf("staticPages has no entry for %s -- the shared source file is not embedded or not keyed to this route", p.route)
		}
		body := string(r.Render(p.route))

		if got := extractTag(t, body, "h1"); got != sp.H1 {
			t.Errorf("%s: <h1> = %q, want %q", p.route, got, sp.H1)
		}

		// html/template escapes text content (notably apostrophes to
		// &#39;), so each expected text is compared in its escaped form --
		// the same convention fallback_test.go uses for the empty states.
		for _, text := range everyStaticText(sp) {
			if !strings.Contains(body, html.EscapeString(text)) {
				t.Errorf("%s: rendered body is missing text %q", p.route, text)
			}
		}

		// The links a block references resolve to real anchors in the
		// server HTML, with the same external attributes the SPA renders.
		for _, key := range []string{"discord", "luma"} {
			if href, ok := sp.Links[key]; ok {
				want := `<a href="` + href + `" target="_blank" rel="noopener noreferrer">`
				if !strings.Contains(body, want) {
					t.Errorf("%s: missing external link %s: %s", p.route, want, body)
				}
			}
		}
		if email, ok := sp.Links["email"]; ok {
			if !strings.Contains(body, `<a href="`+email+`">contact@opencircuitsf.com</a>`) {
				t.Errorf("%s: missing the mailto contact link: %s", p.route, body)
			}
		}
	}
}

// TestRender_SingleSourcePagesStructure pins the structural details the
// SPA's StaticPage.svelte renders and the server HTML must match: muted
// paragraphs carry the SPA stylesheet's own class, strong leads render as
// <strong>, list blocks render as a plain <ul>/<li>, and each section is
// one <section> with its heading as an <h2>.
func TestRender_SingleSourcePagesStructure(t *testing.T) {
	r := newTestRenderer(nil)
	about := string(r.Render("/about"))
	privacy := string(r.Render("/privacy"))

	if !strings.Contains(about, `<h2>How workshops run</h2>`) {
		t.Errorf("about: section headings not rendered as h2: %s", about)
	}
	if !strings.Contains(about, `<li>roughly monthly, beginner-friendly by default</li>`) {
		t.Errorf("about: list items not rendered as a plain <ul>/<li> list: %s", about)
	}
	if !strings.Contains(about, `href="mailto:contact@opencircuitsf.com">contact@opencircuitsf.com</a>.`) {
		t.Errorf("about: inline mailto contact link with trailing period not rendered: %s", about)
	}
	if !strings.Contains(privacy, `<strong>Unsubscribe.</strong> Every campaign email we send carries`) {
		t.Errorf("privacy: strong lead not rendered ahead of its paragraph text: %s", privacy)
	}
	if !strings.Contains(privacy, `class="text-muted">Last updated: 2026-08-19`) {
		t.Errorf("privacy: muted 'Last updated' paragraph not rendered with the SPA's muted class: %s", privacy)
	}

	// #0519 criterion 4, restated for the new branch: exactly one <h1>.
	for path, body := range map[string]string{"/about": about, "/privacy": privacy} {
		if got := strings.Count(body, "<h1"); got != 1 {
			t.Errorf("%s: <h1> count = %d, want 1", path, got)
		}
	}
}

// TestStaticSourceIsTheOneSource checks the single-source contract's SPA
// half (#0520 criterion 2): each view must still import its content JSON,
// and links.ts must re-export the external URLs from about.json rather than
// retyping them -- the guard-kept second copies this issue removed must
// not quietly regrow inside the views.
func TestStaticSourceIsTheOneSource(t *testing.T) {
	cases := []struct{ svelte, ref string }{
		{"web/src/views/About.svelte", "../content/about.json"},
		{"web/src/views/PrivacyPolicy.svelte", "../content/privacy.json"},
		{"web/src/lib/links.ts", "../content/about.json"},
		{"web/src/views/Docs.svelte", "../content/docs/"},
	}
	for _, c := range cases {
		src, err := os.ReadFile(repoPath(c.svelte))
		if err != nil {
			t.Fatalf("reading %s: %v", c.svelte, err)
		}
		if !strings.Contains(string(src), c.ref) {
			t.Errorf("%s no longer references the shared source %q -- the SPA side of #0520's single-source contract is broken", c.svelte, c.ref)
		}
	}
}

// TestLoadStaticPageRejectsBadContent pins loadStaticPage's loud-failure
// posture: a page missing h1/intro/sections/links, an unknown block type,
// or a block referencing an unknown link key must error (mustLoadStaticPages
// turns that into a startup panic), never render a half page.
func TestLoadStaticPageRejectsBadContent(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"no h1", `{"intro":[{"type":"p","text":"x"}],"sections":[{"heading":"s","blocks":[{"type":"p","text":"y"}]}],"links":{}}`},
		{"no intro", `{"h1":"H","intro":[],"sections":[{"heading":"s","blocks":[{"type":"p","text":"y"}]}],"links":{}}`},
		{"unknown type", `{"h1":"H","intro":[{"type":"quote","text":"x"}],"sections":[{"heading":"s","blocks":[{"type":"p","text":"y"}]}],"links":{}}`},
		{"unknown link key", `{"h1":"H","intro":[{"type":"p","text":"x","link":{"key":"nope","label":"L"}}],"sections":[{"heading":"s","blocks":[{"type":"p","text":"y"}]}],"links":{}}`},
		{"code without label", `{"h1":"H","intro":[{"type":"code","text":"x"}],"sections":[{"heading":"s","blocks":[{"type":"p","text":"y"}]}],"links":{}}`},
		{"code without text", `{"h1":"H","intro":[{"type":"code","label":"L"}],"sections":[{"heading":"s","blocks":[{"type":"p","text":"y"}]}],"links":{}}`},
		{"links block missing key", `{"h1":"H","intro":[{"type":"links","items":[{"key":"gone","label":"L"}]}],"sections":[{"heading":"s","blocks":[{"type":"p","text":"y"}]}],"links":{}}`},
	}
	for _, c := range cases {
		if _, err := loadStaticPage([]byte(c.raw)); err == nil {
			t.Errorf("%s: loadStaticPage accepted invalid content", c.name)
		}
	}
}

// TestDocsPagesAreRoutedEverywhere (#0735): every docs content file is a
// known SPA route, has its own route meta read from its JSON, and is in the
// sitemap with no lastmod. A docs page missing from any one place fails here.
func TestDocsPagesAreRoutedEverywhere(t *testing.T) {
	paths := docsPaths()
	if len(paths) < 4 {
		t.Fatalf("docsPaths() = %v, want at least the four pass-A pages", paths)
	}
	r := newTestRenderer(nil)
	body, err := NewSitemap(testBaseURL, nil, nil).Build()
	if err != nil {
		t.Fatalf("sitemap Build: %v", err)
	}
	var parsed urlset
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid sitemap XML: %v", err)
	}
	lastmod := map[string]string{}
	have := map[string]bool{}
	for _, u := range parsed.URLs {
		have[u.Loc] = true
		lastmod[u.Loc] = u.LastMod
	}
	for _, p := range paths {
		if !handlers.IsKnownRoute(p) {
			t.Errorf("%s: not in handlers' knownStaticRoutes", p)
		}
		meta, ok := r.static[p]
		if !ok {
			t.Errorf("%s: no route meta", p)
		} else {
			if meta.Title != staticPages[p].Title {
				t.Errorf("%s: Title = %q, want %q", p, meta.Title, staticPages[p].Title)
			}
			if meta.Canonical != testBaseURL+p {
				t.Errorf("%s: Canonical = %q, want %q", p, meta.Canonical, testBaseURL+p)
			}
		}
		loc := testBaseURL + p
		if !have[loc] {
			t.Errorf("%s: missing from the sitemap", p)
		} else if lastmod[loc] != "" {
			t.Errorf("%s: sitemap lastmod = %q, want none", p, lastmod[loc])
		}
	}
}

// TestRender_DocsCodeAndInternalLinks (#0735): code blocks render as a
// keyboard-scrollable labelled region, internal links stay same-tab, and
// every docs page has exactly one <h1>.
func TestRender_DocsCodeAndInternalLinks(t *testing.T) {
	r := newTestRenderer(nil)
	feeds := string(r.Render("/docs/feeds"))
	want := `<pre tabindex="0" role="region" aria-label="Feed URL"><code>https://www.opencircuitsf.com/archive/feed.xml</code></pre>`
	if !strings.Contains(feeds, want) {
		t.Errorf("/docs/feeds: missing %s", want)
	}
	index := string(r.Render("/docs"))
	i := strings.Index(index, `<a href="/docs/newsletter"`)
	if i < 0 {
		t.Fatalf("/docs: no anchor to /docs/newsletter: %s", index)
	}
	anchor := index[i : i+strings.Index(index[i:], ">")+1]
	if anchor != `<a href="/docs/newsletter">` {
		t.Errorf("/docs: internal anchor = %s, want no target or rel", anchor)
	}
	for _, p := range docsPaths() {
		if got := strings.Count(string(r.Render(p)), "<h1"); got != 1 {
			t.Errorf("%s: <h1> count = %d, want 1", p, got)
		}
	}
}
