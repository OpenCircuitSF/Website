package mailing

import (
	"html"
	"regexp"
	"strings"
	"testing"
)

// TestRenderMarkdownHTML_RawHTMLIsSuppressed pins the raw-HTML decision
// documented in campaign_markdown.go's package doc: a literal tag typed
// into the Markdown source is never rendered as live markup.
//
// Mutation check performed by hand per this issue's brief: added
// ghtml.WithUnsafe() to campaignMarkdown's renderer options and re-ran this
// test — it failed (the raw <script> tags appeared verbatim in the output,
// `strings.Contains(got, "<script>")` became true), confirming the
// assertion actually depends on the safe-mode default rather than passing
// vacuously. Reverted with `cp` from a copy taken before the mutation; `git
// diff` on campaign_markdown.go was empty afterward.
func TestRenderMarkdownHTML_RawHTMLIsSuppressed(t *testing.T) {
	got, err := RenderMarkdownHTML(`Hello <script>alert(document.cookie)</script> world.`)
	if err != nil {
		t.Fatalf("RenderMarkdownHTML: %v", err)
	}
	if strings.Contains(got, "<script>") || strings.Contains(got, "</script>") {
		t.Fatalf("raw <script> passed through: %s", got)
	}
	if !strings.Contains(got, "raw HTML omitted") {
		t.Fatalf("expected goldmark's raw-HTML-omitted marker, got: %s", got)
	}
}

// TestRenderMarkdownHTML_HTMLBlockIsSuppressed covers the block-level form
// (a raw HTML block on its own line), the second of goldmark's two raw-HTML
// node kinds (RawHTML inline, HTMLBlock block) — both must be off.
func TestRenderMarkdownHTML_HTMLBlockIsSuppressed(t *testing.T) {
	got, err := RenderMarkdownHTML("<div onclick=\"steal()\">\ntext\n</div>\n")
	if err != nil {
		t.Fatalf("RenderMarkdownHTML: %v", err)
	}
	if strings.Contains(got, "onclick") || strings.Contains(got, "<div") {
		t.Fatalf("raw HTML block passed through: %s", got)
	}
}

// TestRenderMarkdownHTML_ImagesAreStripped proves the campaignImageRenderer
// override actually replaces goldmark's default <img> rendering — see the
// priority-ordering explanation on campaignMarkdown.
//
// Mutation check performed by hand: changed the override's registered
// priority from 0 to 2000 (above the default renderer's 1000) and re-ran —
// this test failed (`<img` appeared in the output), confirming the priority
// direction documented on campaignMarkdown is the one that actually wins.
// Reverted via `cp`; `git diff` empty afterward.
func TestRenderMarkdownHTML_ImagesAreStripped(t *testing.T) {
	got, err := RenderMarkdownHTML(`![a sunset photo](https://tracker.example/pixel.gif)`)
	if err != nil {
		t.Fatalf("RenderMarkdownHTML: %v", err)
	}
	if strings.Contains(got, "<img") {
		t.Fatalf("an <img> tag survived: %s", got)
	}
	if strings.Contains(got, "tracker.example") {
		t.Fatalf("the remote host leaked into the output: %s", got)
	}
	if !strings.Contains(got, "a sunset photo") {
		t.Fatalf("expected the alt text to survive as plain text, got: %s", got)
	}
}

func TestRenderMarkdownHTML_ImageWithNoAltProducesNoArtifact(t *testing.T) {
	got, err := RenderMarkdownHTML(`![](https://tracker.example/pixel.gif)`)
	if err != nil {
		t.Fatalf("RenderMarkdownHTML: %v", err)
	}
	if strings.Contains(got, "<img") || strings.Contains(got, "tracker.example") {
		t.Fatalf("image leaked through: %s", got)
	}
}

// TestRenderMarkdownHTML_DangerousLinkSchemeIsNeutralized covers the other
// half of the "no third-party fetch, no injection" requirement: a link
// destination goldmark itself classifies as dangerous (javascript:,
// vbscript:, file:, data: other than an inline image) never reaches the
// href attribute.
func TestRenderMarkdownHTML_DangerousLinkSchemeIsNeutralized(t *testing.T) {
	got, err := RenderMarkdownHTML(`[click me](javascript:alert(document.cookie))`)
	if err != nil {
		t.Fatalf("RenderMarkdownHTML: %v", err)
	}
	if strings.Contains(got, "javascript:") {
		t.Fatalf("dangerous scheme leaked into href: %s", got)
	}
	if !strings.Contains(got, "click me") {
		t.Fatalf("expected the link text to survive, got: %s", got)
	}
}

func TestRenderMarkdownHTML_SafeLinkSchemesSurvive(t *testing.T) {
	for _, md := range []string{
		`[site](https://example.com/path)`,
		`[site](http://example.com/path)`,
		`[email us](mailto:hello@opencircuitsf.com)`,
	} {
		got, err := RenderMarkdownHTML(md)
		if err != nil {
			t.Fatalf("RenderMarkdownHTML(%q): %v", md, err)
		}
		if !strings.Contains(got, `href="`) {
			t.Errorf("RenderMarkdownHTML(%q) dropped a safe link entirely: %s", md, got)
		}
	}
}

// TestRenderMarkdownHTML_BodyLinkPolicyDocumented pins #0166's decision:
// after #0136 deleted renderMarkdownPreview and the isSafeLinkHref call it
// made over every body link, #0138's criterion 5 (which claimed a body-link
// URL policy) was amended rather than restored server-side, because the
// three forms goldmark now admits that the deleted validator refused don't
// grant an attacker anything the criterion didn't already permit:
//
//   - "//evil.host/x" and "///evil.host/x" both resolve (WHATWG URL,
//     verified against https://www.opencircuitsf.com/workshops/x) to
//     "https://evil.host/x" -- exactly the ordinary absolute-https link
//     #0138 always allowed for an off-site ticketing page.
//   - "data:image/png|gif|jpeg|webp" link destinations survive as a live
//     "<a href=\"data:...\">" -- goldmark's IsDangerousURL allowlists those
//     four raster types -- but a real Chromium refuses top-level navigation
//     to a data: URL ("Not allowed to navigate top frame to data URL"),
//     confirmed by this issue rather than assumed, so this is inert.
//     "data:image/svg+xml" (the executable one) is still refused.
//
// If any of these ever flips, campaigns' link policy changed underneath
// this test (this function is exactly the one email_campaigns.body_md
// renders through) and deserves its own review, not a widened assertion.
func TestRenderMarkdownHTML_BodyLinkPolicyDocumented(t *testing.T) {
	cases := []struct {
		name       string
		md         string
		wantHref   string
		wantNoLink bool // true if goldmark drops the link entirely (href="")
	}{
		// The three forms #0166 catalogued.
		{name: "protocol-relative resolves off-site but survives", md: `[x](//evil.host/x)`, wantHref: `//evil.host/x`},
		{name: "triple-slash resolves off-site identically and survives", md: `[x](///evil.host/x)`, wantHref: `///evil.host/x`},
		{name: "data:image/png survives as a live but browser-inert href", md: `[x](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=)`, wantHref: `data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=`},
		// The executable data: subtype stays refused -- not part of the
		// #0138/#0138-image allowlist goldmark's IsDangerousURL carries.
		{name: "data:image/svg+xml stays refused", md: `[x](data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciLz4=)`, wantNoLink: true},
		// Legitimate cases that must keep working unchanged.
		{name: "https:// stays live", md: `[site](https://example.com/path)`, wantHref: `https://example.com/path`},
		{name: "mailto: stays live", md: `[email us](mailto:hello@opencircuitsf.com)`, wantHref: `mailto:hello@opencircuitsf.com`},
		{name: "root-relative path stays live", md: `[x](/workshops/soldering-101)`, wantHref: `/workshops/soldering-101`},
	}

	hrefRE := regexp.MustCompile(`href="([^"]*)"`)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RenderMarkdownHTML(c.md)
			if err != nil {
				t.Fatalf("RenderMarkdownHTML(%q): %v", c.md, err)
			}
			m := hrefRE.FindStringSubmatch(got)
			if m == nil {
				t.Fatalf("RenderMarkdownHTML(%q): no href attribute found: %s", c.md, got)
			}
			href := html.UnescapeString(m[1])
			if c.wantNoLink {
				if href != "" {
					t.Fatalf("RenderMarkdownHTML(%q): expected the destination to be refused (href=\"\"), got href=%q: %s", c.md, href, got)
				}
				return
			}
			if href != c.wantHref {
				t.Fatalf("RenderMarkdownHTML(%q): href = %q, want %q: %s", c.md, href, c.wantHref, got)
			}
		})
	}
}

// TestRenderMarkdownHTML_TextIsHTMLEscaped confirms ordinary text content
// (not raw HTML, just literal < > & characters an author might type) is
// escaped rather than injected, e.g. "5 < 10 & true" in body copy.
func TestRenderMarkdownHTML_TextIsHTMLEscaped(t *testing.T) {
	got, err := RenderMarkdownHTML(`5 < 10 & true`)
	if err != nil {
		t.Fatalf("RenderMarkdownHTML: %v", err)
	}
	if strings.Contains(got, "5 < 10") {
		t.Fatalf("literal < survived unescaped: %s", got)
	}
	if !strings.Contains(got, "&lt;") || !strings.Contains(got, "&amp;") {
		t.Fatalf("expected HTML-entity escaping, got: %s", got)
	}
}

func TestRenderMarkdownHTML_Deterministic(t *testing.T) {
	md := "# Heading\n\nSome **bold** text with a [link](https://example.com).\n"
	a, err := RenderMarkdownHTML(md)
	if err != nil {
		t.Fatalf("RenderMarkdownHTML: %v", err)
	}
	b, err := RenderMarkdownHTML(md)
	if err != nil {
		t.Fatalf("RenderMarkdownHTML: %v", err)
	}
	if a != b {
		t.Fatalf("non-deterministic output:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
}

// --- Plain-text renderer ---

func TestRenderMarkdownText_LinkRendersAsTextThenURL(t *testing.T) {
	got, err := RenderMarkdownText(`Come to our [workshop signup](https://opencircuitsf.com/workshops/1) page.`)
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	want := "workshop signup <https://opencircuitsf.com/workshops/1>"
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q in output, got: %q", want, got)
	}
}

func TestRenderMarkdownText_AutolinkRendersAsBareURL(t *testing.T) {
	got, err := RenderMarkdownText(`See <https://opencircuitsf.com> for details.`)
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	if !strings.Contains(got, "<https://opencircuitsf.com>") {
		t.Fatalf("expected bare autolink form, got: %q", got)
	}
	// Must not duplicate the URL as both link text and destination.
	if strings.Count(got, "opencircuitsf.com") != 1 {
		t.Fatalf("expected the URL to appear exactly once, got: %q", got)
	}
}

// TestRenderMarkdownText_DangerousLinkDropsURLButKeepsText proves the text
// renderer applies the identical goldmark dangerous-URL check the HTML
// renderer uses, so the two parts never disagree about which links are
// live — this was a real bug caught during implementation: an early
// version of writeTextInline had no scheme check at all and emitted
// "<javascript:alert(1)>" verbatim in the text part while the HTML part
// already stripped it.
//
// Mutation check performed by hand: reverted writeTextInline's *ast.Link
// case to the original two-branch form (no isDangerousLinkURL check) and
// re-ran — this test failed (the text part contained the javascript: URL).
// Reverted via `cp`; `git diff` empty afterward.
func TestRenderMarkdownText_DangerousLinkDropsURLButKeepsText(t *testing.T) {
	got, err := RenderMarkdownText(`[click me](javascript:alert(1))`)
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	if strings.Contains(got, "javascript:") {
		t.Fatalf("dangerous scheme leaked into text part: %q", got)
	}
	if !strings.Contains(got, "click me") {
		t.Fatalf("expected link text to survive, got: %q", got)
	}
}

func TestRenderMarkdownText_DangerousAutolinkDropsEntirely(t *testing.T) {
	got, err := RenderMarkdownText(`<javascript:alert(1)>`)
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	if strings.Contains(got, "javascript:") {
		t.Fatalf("dangerous autolink leaked into text part: %q", got)
	}
}

func TestRenderMarkdownText_ImageOmitsURLKeepsAltAsText(t *testing.T) {
	got, err := RenderMarkdownText(`![a chart](https://tracker.example/pixel.gif)`)
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	if strings.Contains(got, "tracker.example") {
		t.Fatalf("image URL leaked into text part: %q", got)
	}
	if !strings.Contains(got, "a chart") {
		t.Fatalf("expected alt text to survive, got: %q", got)
	}
}

func TestRenderMarkdownText_RawHTMLOmitted(t *testing.T) {
	got, err := RenderMarkdownText(`Hello <script>alert(1)</script> world.`)
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	if strings.Contains(got, "<script") {
		t.Fatalf("raw HTML leaked into text part: %q", got)
	}
}

func TestRenderMarkdownText_ListsRenderWithMarkers(t *testing.T) {
	got, err := RenderMarkdownText("- first\n- second\n\n1. one\n2. two\n")
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	for _, want := range []string{"- first", "- second", "1. one", "2. two"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got: %q", want, got)
		}
	}
}

func TestRenderMarkdownText_BlockquotePrefixed(t *testing.T) {
	got, err := RenderMarkdownText("> a quoted line\n> a second line\n")
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	if !strings.Contains(got, "> a quoted line a second line") {
		t.Fatalf("expected blockquote prefix, got: %q", got)
	}
}

func TestRenderMarkdownText_EmptyInputProducesEmptyOutput(t *testing.T) {
	got, err := RenderMarkdownText("")
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty output for empty input, got: %q", got)
	}
}

func TestRenderMarkdownText_Deterministic(t *testing.T) {
	md := "# Heading\n\n- one\n- two\n\n[a link](https://example.com)\n"
	a, err := RenderMarkdownText(md)
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	b, err := RenderMarkdownText(md)
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	if a != b {
		t.Fatalf("non-deterministic output:\n--- a ---\n%q\n--- b ---\n%q", a, b)
	}
}

// hrefAttrRE extracts a link's href attribute value from a fragment of
// goldmark-rendered HTML; textLinkRE extracts the "<url>" a text-part link
// or autolink renders as (see writeTextInline). Both regexes assume the
// fixture markdown carries exactly one link, which every case below does.
var (
	hrefAttrRE = regexp.MustCompile(`href="([^"]*)"`)
	textLinkRE = regexp.MustCompile(`<([^<>]*)>`)
)

// TestLinkDestinationNormalization_HTMLAndTextAgree pins the #0042 review's
// bounce directly: RenderMarkdownHTML and RenderMarkdownText must compute
// the dangerous-URL check and the emitted destination from the *same*
// normalized bytes for every link and autolink, so the two parts can never
// disagree about which links are live or where an ordinary link points.
//
// html.UnescapeString undoes only the HTML-attribute-value escaping
// goldmark's html.Renderer applies when it writes the href attribute (e.g.
// turning a literal "&" back into "&amp;" is correct HTML syntax, not part
// of the destination-normalization bug) — the comparison is otherwise a
// direct byte comparison of the destination each part settled on.
//
// Mutation check (recorded in issues/0042.md's Verification): reverting the
// *ast.Link and *ast.AutoLink cases in writeTextInline to call
// isDangerousLinkURL and emit string(v.Destination) / string(v.URL(source))
// — the raw, un-normalized bytes from before this pass — makes the
// entity-encoded and backslash-escaped rows below fail, since the raw bytes
// read as safe while goldmark's HTML renderer (which normalizes first)
// classifies the same destination as dangerous.
func TestLinkDestinationNormalization_HTMLAndTextAgree(t *testing.T) {
	cases := []struct {
		name string
		md   string
	}{
		{"entity-encoded dangerous scheme", `[x](javascript&#58;alert(1))`},
		{"backslash-escaped dangerous scheme", `[x](javascript\:alert(1))`},
		{"backslash-escaped punctuation in path", `[x](https://ok.example/a\_b)`},
		{"already percent-encoded", `[x](https://ok.example/a%20b)`},
		{"protocol-relative", `[x](//evil.example/p)`},
		{"case-varied scheme", `[x](JAVASCRIPT:alert(1))`},
		{"literal space via bracketed destination", `[x](<https://ok.example/a b>)`},
		{"benign URL with entity-shaped query", `[docs](https://ok.example/s?a=1&amp;b=2)`},
		{"bare autolink, dangerous scheme", `<javascript:alert(1)>`},
		{"bare autolink, safe URL", `<https://ok.example/x>`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			htmlOut, err := RenderMarkdownHTML(tc.md)
			if err != nil {
				t.Fatalf("RenderMarkdownHTML(%q): %v", tc.md, err)
			}
			textOut, err := RenderMarkdownText(tc.md)
			if err != nil {
				t.Fatalf("RenderMarkdownText(%q): %v", tc.md, err)
			}

			hrefMatch := hrefAttrRE.FindStringSubmatch(htmlOut)
			if hrefMatch == nil {
				t.Fatalf("no href attribute found in HTML output: %s", htmlOut)
			}
			href := html.UnescapeString(hrefMatch[1])
			textMatch := textLinkRE.FindStringSubmatch(textOut)

			if href == "" {
				// The HTML part classified this destination as dangerous
				// and dropped it — the text part must drop it too.
				if textMatch != nil {
					t.Fatalf("HTML dropped the destination as dangerous but the text part kept it:\n  html: %s\n  text: %q", htmlOut, textOut)
				}
				return
			}
			if textMatch == nil {
				t.Fatalf("HTML kept the destination (href=%q) but the text part dropped it: %q", href, textOut)
			}
			if href != textMatch[1] {
				t.Fatalf("HTML and text parts disagree about the link destination:\n  html href = %q\n  text url  = %q", href, textMatch[1])
			}
		})
	}
}

func TestRenderMarkdownText_UsesCRLF(t *testing.T) {
	got, err := RenderMarkdownText("first paragraph\n\nsecond paragraph\n")
	if err != nil {
		t.Fatalf("RenderMarkdownText: %v", err)
	}
	if !strings.Contains(got, "\r\n") {
		t.Fatalf("expected CRLF line endings, got: %q", got)
	}
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("found a bare LF not part of a CRLF pair: %q", got)
	}
}
