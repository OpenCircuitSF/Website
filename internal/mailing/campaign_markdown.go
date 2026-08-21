// Markdown -> HTML / plain-text conversion for campaign bodies (#0042).
// This is the "one authored Markdown source, two rendered parts" engine
// campaign_render.go's RenderCampaign builds on, the way templates.go's
// emailContent is the shared source for the five transactional templates.
//
// Library choice: github.com/yuin/goldmark. It is the de facto standard Go
// CommonMark implementation (used by Hugo, Docker's docs toolchain, and
// most other Go static-site generators), has zero external dependencies of
// its own (confirmed: go.sum after `go get` added exactly one module,
// github.com/yuin/goldmark, no transitive additions), and is actively
// maintained. No other Markdown library was already present in go.sum.
//
// Raw HTML decision: campaign bodies are admin-authored (not public input),
// but PRD/CLAUDE.md §9 still forbid anything that could inject a script or
// fetch a third-party resource into thousands of recipients' inboxes, and a
// campaign body is stored and re-rendered on every send/preview/test-send —
// worth treating carefully rather than trusting the author's browser. Two
// deliberate, independent decisions:
//
//  1. Raw HTML in the Markdown source (a literal <script>, <iframe>, or any
//     other tag typed directly into the body) is SUPPRESSED, not passed
//     through and not displayed as escaped text. This is goldmark's default
//     ("safe") behavior — html.WithUnsafe() is never passed to
//     campaignMarkdown below. Confirmed against goldmark's own source
//     (renderer/html/html.go: renderRawHTML and renderHTMLBlock only emit
//     anything when r.Unsafe is true) and pinned by
//     TestRenderMarkdownHTML_RawHTMLIsSuppressed. A goldmark link/image
//     destination using a dangerous scheme (javascript:, vbscript:, and
//     similar — see html.IsDangerousURL) is dropped the same way, for the
//     same reason, by the same default.
//  2. Markdown-native images (`![alt](src)`) are stripped entirely,
//     regardless of scheme — see campaignImageRenderer below. There is no
//     mechanism in this project for hosting an image a campaign body could
//     legitimately reference, so the only images that could ever appear are
//     third-party URLs, and "an untracked remote image in a campaign body
//     is exactly an open-tracking pixel by another name" (this issue's
//     brief, echoing CLAUDE.md §9). Rather than allowlisting one host,
//     images are simply not a supported campaign construct.
//
// No goldmark extensions (extension.GFM etc.) are enabled. Tables,
// strikethrough, autolinks-from-bare-URLs and task lists are convenient,
// but each adds AST node kinds the plain-text walker below would need to
// special-case; staying on plain CommonMark keeps that walker exhaustive
// over every kind goldmark can hand it, rather than falling back to a
// generic "recurse into children" default for a kind it has never seen.
// CommonMark autolinks (`<https://example.com>`) still work — that syntax
// is core CommonMark, not a GFM extension.
package mailing

import (
	"bytes"
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	ghtml "github.com/yuin/goldmark/renderer/html"
	gmtext "github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// isDangerousLinkURL reports whether dest is a scheme goldmark's own HTML
// renderer refuses to emit as an href in safe mode (javascript:, vbscript:,
// file:, and data: other than a handful of inline image types — see
// html.IsDangerousURL). dest must already be normalized by
// normalizedLinkDestination below — see that function's doc for why passing
// raw, un-normalized bytes here is exactly the bug #0042's review bounced.
func isDangerousLinkURL(dest string) bool {
	return ghtml.IsDangerousURL([]byte(dest))
}

// normalizedLinkDestination is the single place this package normalizes a
// link/autolink destination, so the same normalized bytes feed both the
// dangerous-URL check and the text that gets written out — the two can
// never see different bytes because there is only one call site producing
// them.
//
// This mirrors, byte for byte, what goldmark's own HTML renderer computes
// for the same node (renderer/html/html.go): renderLink does
// `util.URLEscape(n.Destination, true)` and renderAutoLink does
// `util.URLEscape(n.URL(source), false)` — in each case computing the
// normalized destination exactly once and reusing it for both the
// IsDangerousURL check and the emitted href. resolveReference must match
// which of those two call sites this is standing in for: true for a
// *ast.Link's Destination, false for an *ast.AutoLink's URL(source).
//
// util.URLEscape(v, true) runs UnescapePunctuations, then
// ResolveNumericReferences, then ResolveEntityNames, then percent-escapes
// the result — so a backslash-escaped or entity-encoded scheme
// (`javascript\:alert(1)`, `javascript&#58;alert(1)`) normalizes to the
// same bytes the HTML part's danger check sees, and an ordinary query
// string's `&amp;` normalizes to a literal `&` the same way it does in the
// href, rather than surviving as literal HTML-entity text in the mail body
// a recipient reads.
func normalizedLinkDestination(raw []byte, resolveReference bool) string {
	return string(util.URLEscape(raw, resolveReference))
}

// campaignMarkdown is the single goldmark instance every campaign body
// parses and renders through — one configuration, reused for every render,
// so behavior can never drift between two call sites constructing their own
// goldmark.Markdown with slightly different options.
var campaignMarkdown = goldmark.New(
	goldmark.WithRendererOptions(
		ghtml.WithXHTML(),
		renderer.WithNodeRenderers(
			// Priority 0 beats the default html.Renderer's priority 1000:
			// goldmark's renderer registers node renderers from highest
			// priority to lowest and lets the LAST registration for a given
			// ast.NodeKind win (renderer/renderer.go's Render, a plain map
			// assignment with no "if absent" guard) — so a lower priority
			// number here is what makes this override take effect instead
			// of being overridden itself. Confirmed by
			// TestRenderMarkdownHTML_ImagesAreStripped.
			util.Prioritized(campaignImageRenderer{}, 0),
		),
	),
	// Deliberately no goldmark.WithExtensions(...) — see package doc above
	// — and deliberately no html.WithUnsafe() — see package doc above.
)

// campaignImageRenderer replaces goldmark's default <img> rendering for
// ast.KindImage nodes only; every other node kind still renders through the
// default html.Renderer registered by goldmark.New. See the package doc for
// why images are dropped rather than allowlisted.
type campaignImageRenderer struct{}

func (campaignImageRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindImage, renderCampaignImage)
}

func renderCampaignImage(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	img, ok := n.(*ast.Image)
	if !ok {
		return ast.WalkSkipChildren, nil
	}
	if alt := inlineText(img, source); alt != "" {
		if _, err := w.WriteString(`<em>[image not included: ` + html.EscapeString(alt) + `]</em>`); err != nil {
			return ast.WalkStop, err
		}
	}
	// Skip the image node's own children (the alt-text nodes) — they were
	// already consumed above via inlineText, and the default renderer
	// would otherwise try to render them a second time as loose inline
	// content once WalkSkipChildren stops applying at the next sibling.
	return ast.WalkSkipChildren, nil
}

// inlineText concatenates the literal text content of n's descendants,
// ignoring any inline markup (emphasis, code spans, nested links). Used
// only for alt text, where markup is rare and losing it is harmless.
func inlineText(n ast.Node, source []byte) string {
	var sb strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			sb.Write(v.Value(source))
		case *ast.String:
			sb.Write(v.Value)
		default:
			sb.WriteString(inlineText(c, source))
		}
	}
	return sb.String()
}

// RenderMarkdownHTML converts Markdown to a sanitized HTML fragment: no
// <script>, no raw HTML of any kind, no <img>, and no dangerous-scheme
// hrefs — see the package doc for what each of those means and why. The
// output is deterministic: the same md always produces the same bytes (no
// map iteration, no time-of-day, no randomness anywhere in this path).
//
// The returned HTML is a fragment (paragraphs, headings, lists, etc.), not
// a full document — callers that need a complete, mail-safe HTML document
// (inline styles, a table layout, a footer) build around it. RenderCampaign
// in campaign_render.go is this package's own caller.
func RenderMarkdownHTML(md string) (string, error) {
	var buf bytes.Buffer
	if err := campaignMarkdown.Convert([]byte(md), &buf); err != nil {
		return "", fmt.Errorf("mailing: render campaign markdown to html: %w", err)
	}
	return buf.String(), nil
}

// RenderMarkdownText converts Markdown to a genuinely readable plain-text
// alternative — not HTML with the tags stripped. Specifically:
//
//   - Links render as "text <url>" (or just "<url>" when the link text and
//     the URL are the same, e.g. a bare autolink) — the acceptance
//     criterion's exact form. A text part that drops URLs is useless to a
//     recipient who can't click; one that inlines the raw URL mid-sentence
//     for every link is unreadable, so the URL always appears set apart in
//     angle brackets after the link text instead.
//   - Headings, paragraphs, and blockquotes ("> " prefix) render as
//     distinct blocks separated by a blank line, matching the paragraph
//     spacing templates.go's renderText already uses for the transactional
//     templates.
//   - Lists render with "- " (bullets) or "N. " (ordered, honoring the
//     Markdown source's own start number), continuation lines of a
//     multi-line item indented to align under the text, not the marker.
//   - Emphasis is kept as light markup (*strong*, _emphasis_) rather than
//     silently dropped — losing which words the author emphasized is
//     exactly the kind of information a "tags stripped" text part loses.
//   - Code spans keep their backticks; code blocks are indented four
//     spaces and never word-wrapped, so nothing inside one is corrupted.
//   - Images and raw HTML are omitted here too, for the same reasons
//     RenderMarkdownHTML omits them — the text part must never say
//     something the HTML part refuses to render, or the two parts would
//     disagree about what third-party content the campaign fetches.
//
// Line endings are "\r\n" throughout, matching templates.go's renderText
// and the rest of this project's plain-text mail bodies (RFC 5322).
func RenderMarkdownText(md string) (string, error) {
	source := []byte(md)
	doc := campaignMarkdown.Parser().Parse(gmtext.NewReader(source))
	blocks := renderTextBlocks(doc, source)
	if len(blocks) == 0 {
		return "", nil
	}
	return strings.Join(blocks, "\r\n\r\n") + "\r\n", nil
}

// renderTextBlocks renders every direct block-level child of parent,
// dropping any that render to nothing (an HTML block, an empty node).
func renderTextBlocks(parent ast.Node, source []byte) []string {
	var blocks []string
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		if s := renderTextBlock(c, source); s != "" {
			blocks = append(blocks, s)
		}
	}
	return blocks
}

func renderTextBlock(n ast.Node, source []byte) string {
	switch v := n.(type) {
	case *ast.Heading:
		return renderTextInline(v, source)
	case *ast.Paragraph:
		return renderTextInline(v, source)
	case *ast.TextBlock:
		return renderTextInline(v, source)
	case *ast.ThematicBreak:
		return strings.Repeat("-", 40)
	case *ast.CodeBlock:
		return withLinePrefix(strings.TrimRight(string(v.Text(source)), "\n"), "    ")
	case *ast.FencedCodeBlock:
		return withLinePrefix(strings.TrimRight(string(v.Text(source)), "\n"), "    ")
	case *ast.Blockquote:
		inner := strings.Join(renderTextBlocks(v, source), "\r\n\r\n")
		return withLinePrefix(inner, "> ")
	case *ast.List:
		return renderTextList(v, source)
	case *ast.HTMLBlock:
		// Raw HTML block: suppressed, matching RenderMarkdownHTML's safe
		// mode. The two parts must never disagree about what the source
		// contained.
		return ""
	default:
		// Any other block-level container (goldmark has no others in the
		// base CommonMark node set, but this keeps the walker total rather
		// than silently dropping content from a future goldmark version):
		// recurse into its children generically.
		return strings.Join(renderTextBlocks(n, source), "\r\n\r\n")
	}
}

// withLinePrefix splits s on line boundaries (accepting either "\n" or
// "\r\n" input, since it is fed both raw source bytes and already-CRLF
// output from nested renders) and rejoins with prefix on every non-blank
// line and "\r\n" endings throughout — used for code-block indentation and
// blockquote's "> " marker alike.
func withLinePrefix(s, prefix string) string {
	lines := splitLines(s)
	for i, l := range lines {
		if l == "" {
			lines[i] = strings.TrimRight(prefix, " ")
		} else {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\r\n")
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

// renderTextList renders a list's items as one block, "- " for a bullet
// list or "N. " (honoring the source's own start number) for an ordered
// one, with a nested block's continuation lines indented to align under
// the item's own text.
func renderTextList(list *ast.List, source []byte) string {
	var lines []string
	n := 0
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		li, ok := item.(*ast.ListItem)
		if !ok {
			continue
		}
		var marker string
		if list.IsOrdered() {
			marker = strconv.Itoa(list.Start+n) + ". "
		} else {
			marker = "- "
		}
		inner := strings.Join(renderTextBlocks(li, source), "\r\n\r\n")
		lines = append(lines, indentContinuation(marker, inner))
		n++
	}
	return strings.Join(lines, "\r\n")
}

// indentContinuation prefixes body's first line with marker and every
// later line with spaces the same width as marker, so a wrapped or nested
// list item's continuation lines up under its text rather than its bullet.
func indentContinuation(marker, body string) string {
	lines := splitLines(body)
	if len(lines) == 0 {
		return strings.TrimRight(marker, " ")
	}
	pad := strings.Repeat(" ", len(marker))
	out := make([]string, len(lines))
	for i, l := range lines {
		switch {
		case i == 0:
			out[i] = marker + l
		case l == "":
			out[i] = ""
		default:
			out[i] = pad + l
		}
	}
	return strings.Join(out, "\r\n")
}

// renderTextInline renders n's inline children (a Heading's or Paragraph's
// content) to one line of plain text.
func renderTextInline(n ast.Node, source []byte) string {
	var sb strings.Builder
	writeTextInline(&sb, n, source)
	return sb.String()
}

func writeTextInline(sb *strings.Builder, n ast.Node, source []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			sb.Write(v.Value(source))
			switch {
			case v.HardLineBreak():
				sb.WriteString("\r\n")
			case v.SoftLineBreak():
				sb.WriteByte(' ')
			}
		case *ast.String:
			sb.Write(v.Value)
		case *ast.CodeSpan:
			sb.WriteByte('`')
			writeTextInline(sb, v, source)
			sb.WriteByte('`')
		case *ast.Emphasis:
			marker := "_"
			if v.Level >= 2 {
				marker = "*"
			}
			sb.WriteString(marker)
			writeTextInline(sb, v, source)
			sb.WriteString(marker)
		case *ast.AutoLink:
			dest := normalizedLinkDestination(v.URL(source), false)
			if !isDangerousLinkURL(dest) {
				sb.WriteString("<" + dest + ">")
			}
			// Dangerous autolink: RenderMarkdownHTML drops the href
			// (renders no anchor), so the text part drops the URL too —
			// an autolink has no separate link text to fall back to.
		case *ast.Link:
			var inner strings.Builder
			writeTextInline(&inner, v, source)
			text := inner.String()
			dest := normalizedLinkDestination(v.Destination, true)
			switch {
			case isDangerousLinkURL(dest):
				// RenderMarkdownHTML renders this <a> with an empty href
				// (goldmark's own safe-mode behavior) rather than the
				// dangerous URL — the text part keeps the link's visible
				// text but drops the URL for the same reason.
				sb.WriteString(text)
			case text == "" || text == dest:
				sb.WriteString("<" + dest + ">")
			default:
				sb.WriteString(text + " <" + dest + ">")
			}
		case *ast.Image:
			// Omitted, matching RenderMarkdownHTML — see package doc.
			if alt := inlineText(v, source); alt != "" {
				sb.WriteString("[image not included: " + alt + "]")
			}
		case *ast.RawHTML:
			// Omitted, matching RenderMarkdownHTML's safe mode.
		default:
			writeTextInline(sb, c, source)
		}
	}
}
