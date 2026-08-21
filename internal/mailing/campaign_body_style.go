// Inline brand styling for the campaign body fragment (#0043).
//
// RenderMarkdownHTML (campaign_markdown.go) returns a sanitized but
// completely unstyled HTML fragment — goldmark's default output for
// <h1>-<h6>, <p>, <ul>/<ol>, <li>, <blockquote>, <pre>/<code>, <a>, <hr>,
// <strong>, <em> carries no attributes at all. #0043's scope note is
// explicit that RenderMarkdownHTML itself must not change (it stays a
// fragment, already sanitized — no re-sanitizing, no re-escaping). This file
// is the seam #0042's reviewer identified as this issue's actual remaining
// work: applying inline, mail-safe styling to that fragment's OUTPUT, at
// however many levels of nesting a real campaign body has, so it reads as
// the dark terminal brand instead of black-on-#10231a — the "background
// stripped" failure #0028's review flagged as a residual risk for the
// transactional templates and a guaranteed defect here, since the campaign
// card's own <td> is exactly the kind of ancestor-only color declaration
// that review warned about (fixed generally in templates.go's card <td>,
// see its comment, but heading/list/blockquote copy still needs its own
// styling to read as "mono headers, green accents" rather than plain body
// text).
//
// Approach: targeted, anchored string/regexp substitution on goldmark's
// KNOWN, deterministic output shape — not a general HTML rewriter, and not
// a second pass through anything RenderMarkdownHTML itself does. This is
// safe specifically because campaignMarkdown (campaign_markdown.go) enables
// no goldmark extensions (no auto-heading-IDs, no GFM), so every element
// this file targets renders in exactly one of a small, known set of forms:
//
//	<h1>..<h6>              no attributes, ever (no heading-ID extension)
//	<p>                     no attributes, ever
//	<ul>                    no attributes, ever
//	<ol> or <ol start="N">  "start" only when the source list doesn't begin
//	                        at 1
//	<li>                    no attributes, ever
//	<blockquote>            no attributes, ever
//	<pre>                   no attributes, ever
//	<code> or               "class" only on a fenced block whose opening
//	<code class="...">      fence names a language (```go)
//	<a href="...">  or      "title" only when the Markdown link syntax
//	<a href="..." title="..."> supplies one ([text](url "title"))
//	<hr />                  XHTML self-closing form (ghtml.WithXHTML())
//
// Every regexp below is anchored to one of these exact shapes and preserves
// any existing attribute rather than discarding it. If a future goldmark
// version or a newly enabled extension changes this shape, the regexp
// simply stops matching and the element renders unstyled (inherits the card
// <td>'s own color/font-family — legible, just not accented) rather than
// producing malformed markup; it cannot make the output less safe than
// RenderMarkdownHTML's own guarantees, since it never touches href/src
// values or introduces new tags.
package mailing

import (
	"regexp"
	"strconv"
)

var (
	campaignHeadingOpenRE    = regexp.MustCompile(`<h([1-6])>`)
	campaignParagraphOpenRE  = regexp.MustCompile(`<p>`)
	campaignULOpenRE         = regexp.MustCompile(`<ul>`)
	campaignOLOpenRE         = regexp.MustCompile(`<ol( start="\d+")?>`)
	campaignLIOpenRE         = regexp.MustCompile(`<li>`)
	campaignBlockquoteOpenRE = regexp.MustCompile(`<blockquote>`)
	campaignPreOpenRE        = regexp.MustCompile(`<pre>`)
	campaignCodeOpenRE       = regexp.MustCompile(`<code( class="[^"]*")?>`)
	campaignLinkOpenRE       = regexp.MustCompile(`<a href="([^"]*)"( title="[^"]*")?>`)
	campaignHrRE             = regexp.MustCompile(`<hr\s*/?>`)
)

// campaignHeadingStyle returns the inline style for a heading of the given
// level (1-6): mono font (matching templates.go's card heading), the same
// accent green as the transactional templates' <h1> — safe here because
// this fragment only ever renders inside the branded card <td>, which
// always carries bgcolor=colorCardBG (see wrapCampaignHTML) — the same
// "green only inside a guaranteed-dark-background element" rule
// templates.go's own package doc states.
func campaignHeadingStyle(level int) string {
	size := 20 - (level-1)*2
	if size < 13 {
		size = 13
	}
	lineHeight := size + 8
	return `font-family:` + fontMono + `;font-size:` + strconv.Itoa(size) + `px;line-height:` + strconv.Itoa(lineHeight) + `px;color:` + colorHeading + `;margin:20px 0 12px;`
}

// styleCampaignBodyHTML applies templates.go's brand palette and bulletproof
// font stacks to an already-rendered, already-sanitized campaign body
// fragment, purely by adding inline style="..." attributes to the fragment's
// own opening tags. It never touches an href/src value, never adds or
// removes a tag, and never re-parses or re-sanitizes the fragment — the
// fragment's sanitization guarantees (RenderMarkdownHTML: no raw HTML, no
// <img>, no dangerous-scheme hrefs) are exactly as strong afterward as
// before, because this function only inserts style attributes into tags
// RenderMarkdownHTML already emitted.
func styleCampaignBodyHTML(fragment string) string {
	fragment = campaignHeadingOpenRE.ReplaceAllStringFunc(fragment, func(m string) string {
		groups := campaignHeadingOpenRE.FindStringSubmatch(m)
		level := int(groups[1][0] - '0')
		return `<h` + groups[1] + ` style="` + campaignHeadingStyle(level) + `">`
	})
	fragment = campaignParagraphOpenRE.ReplaceAllString(fragment,
		`<p style="font-family:`+fontBody+`;font-size:15px;line-height:24px;color:`+colorBodyText+`;margin:0 0 16px;">`)
	fragment = campaignULOpenRE.ReplaceAllString(fragment,
		`<ul style="font-family:`+fontBody+`;font-size:15px;line-height:24px;color:`+colorBodyText+`;margin:0 0 16px;padding-left:22px;">`)
	fragment = campaignOLOpenRE.ReplaceAllStringFunc(fragment, func(m string) string {
		groups := campaignOLOpenRE.FindStringSubmatch(m)
		return `<ol` + groups[1] + ` style="font-family:` + fontBody + `;font-size:15px;line-height:24px;color:` + colorBodyText + `;margin:0 0 16px;padding-left:22px;">`
	})
	fragment = campaignLIOpenRE.ReplaceAllString(fragment, `<li style="margin:0 0 6px;">`)
	fragment = campaignBlockquoteOpenRE.ReplaceAllString(fragment,
		`<blockquote style="margin:0 0 16px;padding:4px 0 4px 14px;border-left:3px solid `+colorCardBorder+`;color:`+colorMutedText+`;">`)
	fragment = campaignPreOpenRE.ReplaceAllString(fragment,
		`<pre style="margin:0 0 16px;padding:12px;background-color:`+colorPageBG+`;border:1px solid `+colorCardBorder+`;border-radius:4px;overflow-x:auto;font-family:`+fontMono+`;font-size:13px;line-height:20px;color:`+colorBodyText+`;">`)
	fragment = campaignCodeOpenRE.ReplaceAllStringFunc(fragment, func(m string) string {
		groups := campaignCodeOpenRE.FindStringSubmatch(m)
		return `<code` + groups[1] + ` style="font-family:` + fontMono + `;">`
	})
	fragment = campaignLinkOpenRE.ReplaceAllStringFunc(fragment, func(m string) string {
		groups := campaignLinkOpenRE.FindStringSubmatch(m)
		return `<a href="` + groups[1] + `"` + groups[2] + ` style="color:` + colorLinkText + `;text-decoration:underline;">`
	})
	fragment = campaignHrRE.ReplaceAllString(fragment,
		`<hr style="border:none;border-top:1px solid `+colorCardBorder+`;margin:20px 0;" />`)
	return fragment
}
