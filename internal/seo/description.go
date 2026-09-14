package seo

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// #0528: a published campaign's meta description. archiveRouteMeta (seo.go)
// used to set a newsletter's <meta name="description"> and og:description
// from the campaign's preheader alone, falling back to a fixed generic
// sentence -- measured on production 2026-09-12, /archive/09-2026 (sent
// without a preheader) described every shared link as that generic
// sentence. The newsletter's own first paragraph is available and far
// better: the body is already rendered to HTML for the archive page
// (#0042), so the description derives from that same render rather than a
// second parse of the Markdown.

// archiveDescriptionMaxLen is the target length a body-derived description
// is truncated to, in runes. Search engines surface roughly 150-160
// characters of a meta description; 155 sits mid-band. The preheader is
// NOT truncated -- admins wrote it as a deliberate one-liner, and #0528's
// acceptance criteria scope truncation to the body-derived text.
const archiveDescriptionMaxLen = 155

// archiveDescriptionFallback is the last-resort description for a campaign
// with neither a preheader nor any prose in its body. Carried over from
// archiveRouteMeta's pre-#0528 fallback unchanged.
const archiveDescriptionFallback = "Read this email from Open Circuit SF."

// archiveDescription returns the description text for one archive entry.
// The preheader wins when set (#0528 criterion 4 -- it is also the email's
// own inbox preview line, so it is the author's chosen summary); otherwise
// the body's first paragraph of rendered prose (#0528 criterion 1); the
// generic sentence only when the body has no prose at all (criterion 5).
// The result is plain text, safe to HTML-escape at injection (substitute
// already html.EscapeString's every %%OC_*%% description value) and to
// XML-escape for the Atom feed (#0531's entry summaries).
func archiveDescription(e ArchiveEntry) string {
	if p := strings.TrimSpace(e.Preheader); p != "" {
		return p
	}
	if d := firstParagraphText(e.BodyMD); d != "" {
		return truncateDescription(d, archiveDescriptionMaxLen)
	}
	return archiveDescriptionFallback
}

// firstParagraphText renders md through mailing.RenderMarkdownPageHTML --
// the SAME call the archive detail page's body render makes
// (fallback.go's renderMarkdownOrEmpty, PublicArchiveHandler.GetBySlug's
// JSON API) -- and returns the first <p> element's text. Rendering first,
// rather than scraping the raw Markdown, is what discharges "no Markdown
// syntax, no HTML": goldmark has already resolved emphasis, links, and
// code spans into HTML elements, safe mode has already dropped any raw
// HTML the author pasted, and the parser walk below collects only
// character data. A heading, list, or blockquote is skipped for free --
// only a <p> qualifies -- so a body opening with "# Heading" (the
// September newsletter does) yields its first real paragraph, not the
// heading (#0528 criterion 2). Runs of whitespace inside the paragraph are
// collapsed, so a hard-wrapped paragraph reads as one line. Returns ""
// for an empty body, a render error, or a body with no <p> anywhere.
func firstParagraphText(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}
	rendered, err := mailing.RenderMarkdownPageHTML(md)
	if err != nil {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		return ""
	}
	var found string
	var walk func(n *html.Node) bool
	walk = func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.DataAtom == atom.P {
			// A <p> that yields no text at all (safe mode drops an entire
			// raw-HTML-only paragraph, which can leave an empty one) is not
			// prose -- keep looking at the following paragraphs rather than
			// stopping here. An image paragraph needs no such fall-through:
			// safe mode renders images as the visible text "[image not
			// included: alt]" (pinned by description_test.go), so that
			// paragraph already yields the page's own text.
			if text := collapseWhitespace(collectText(n)); text != "" {
				found = text
				return true
			}
			return false
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(doc)
	return found
}

// collectText concatenates every descendant text node of n, skipping
// non-text elements a paragraph could not meaningfully contribute to a
// plain-text description.
func collectText(n *html.Node) string {
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Script {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// collapseWhitespace trims s and replaces every run of Unicode whitespace
// with a single space -- #0528 criterion 1's "whitespace collapsed".
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateDescription shortens s to at most max runes at a word boundary,
// appending a single U+2026 only when anything was cut (#0528 criterion 3:
// "with an ellipsis only when truncated"). Deterministic for the same
// input: the cut point is the last ASCII space at or before max runes
// falling back to a hard rune cut when the span holds no space at all (a
// single word longer than max), and the ellipsis is appended after the
// trailing space is dropped so it never sits behind a gap. A U+2026 counts
// toward the "about 155 characters" target rather than against a hard cap,
// which is why the search window is over the pre-ellipsis runes.
func truncateDescription(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	cut := max
	for i := max; i > 0; i-- {
		if runes[i-1] == ' ' {
			cut = i - 1
			break
		}
	}
	head := strings.TrimRight(string(runes[:cut]), " ")
	return head + "…"
}
