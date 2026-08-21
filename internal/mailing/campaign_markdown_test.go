package mailing

import (
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
