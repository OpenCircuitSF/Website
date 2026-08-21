// Templates for the transactional emails (#0028): confirm-subscription,
// already-subscribed, passkey-registration magic link, and account-recovery
// magic link. These are NOT campaign mail (#0043) — no List-Unsubscribe
// headers, no audience substitution loop — but they share one design
// constraint with campaign mail: email clients are not browsers (PRD §6.6).
//
// Every template renders from a single emailContent value (see below): the
// four Build* functions in transactional_templates.go populate one struct
// once, then hand it to both renderHTML and renderText. That is what "HTML
// and text rendered from one source" means here — there is exactly one place
// per message that decides what the copy says, and the two renderers only
// decide how it looks in each format. A future template that hand-wrote
// separate HTML and text copy would drift the moment one was edited and not
// the other; this shape makes that impossible by construction.
package mailing

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// Palette. PRD §4.2 defines these as CSS custom properties for the browser
// site; email clients don't support custom properties (or often <style>
// blocks at all), so the same brand colors are baked into inline
// styles/attributes at every call site instead of referenced by name.
//
// CLAUDE.md §8: #68FF23 measures 14.8:1 against the dark terminal ground but
// only 1.32:1 on white. That fact drives one rule followed everywhere below:
// colorHeading (the bright green) is used ONLY inside an element that also
// carries its own bgcolor="colorCardBG" attribute (not just a CSS
// background-color, which Outlook drops on <body> and can drop elsewhere) —
// so the dark background it needs for contrast is never optional. Nothing
// renders green text against an unknown or unguaranteed background; the
// button goes further still and uses dark text on the green fill so it stays
// legible even in the hypothetical case the button's own background is
// stripped and it falls back to a plain white canvas.
const (
	colorPageBG     = "#0b0f0d" // outer table — the page canvas
	colorCardBG     = "#10231a" // inner card — the only place colorHeading appears
	colorCardBorder = "#274a37"
	colorBodyText   = "#e7f5ec" // body copy on the card; never green
	colorMutedText  = "#9fb8ab" // footer / fine print on the card
	colorHeading    = "#68ff23" // bright accent green — guarded, see above
	colorButtonBG   = "#68ff23"
	colorButtonText = "#04140a" // near-black — legible on green AND on white
	colorLinkText   = "#8fe36b" // footer links on the dark card (>7:1 on colorCardBG)
)

// fontMono and fontBody are bulletproof, install-everywhere font stacks.
// html/CSS custom properties and @font-face are both off the table for mail.
const (
	fontMono = "'SFMono-Regular',Consolas,'Liberation Mono',Menlo,Courier,monospace"
	fontBody = "-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"
)

// ctaToken is a literal placeholder authors put in IntroParagraphs/
// NoteParagraphs copy where the sentence needs to name how the reader
// triggers the primary action. renderHTML and renderText each substitute
// their own word for it — "button" in HTML, where one actually exists, and
// "link" in text, where the reader only ever sees a raw URL.
//
// This exists because of #0076: before it, the four Build* functions wrote
// the literal word "button" directly into paragraph copy shared verbatim by
// both renderers, so the text part told readers to click a button that was
// never there. A shared string constant guarantees renderHTML and renderText
// can never independently drift on THIS word without an edit to this file —
// but by construction they now say different things for the same paragraph,
// which is the whole point: the two parts can differ, and default to doing
// so for the one word that must differ.
const ctaToken = "{{cta}}"

// resolveCTA substitutes ctaToken in a paragraph with the noun that fits the
// format currently rendering it.
func resolveCTA(s, word string) string {
	return strings.ReplaceAll(s, ctaToken, word)
}

// emailContent is the single data model every transactional template renders
// from. Populate one value; call renderHTML and renderText on it.
type emailContent struct {
	Subject   string
	Preheader string // hidden preview text; shown by the client's inbox list, never in the body
	Eyebrow   string // small mono label above the heading, e.g. "$ opencircuit/confirm"
	Heading   string

	IntroParagraphs []string
	ButtonText      string
	ButtonURL       string
	NoteParagraphs  []string // e.g. expiry line, "if you didn't request this" line

	// ShowListFooter gates the mailing-list-specific footer (manage/unsubscribe
	// links and the physical address). Registration and recovery mail are
	// account email, not mailing-list email, so they leave this false.
	ShowListFooter  bool
	ManageURL       string
	UnsubscribeURL  string
	PhysicalAddress string
}

// formatDuration renders a time.Duration the way these templates state an
// expiry: the largest whole unit that divides it evenly, falling back to
// minutes. It exists so the copy is derived from the same TTL value the
// store actually enforces (auth.registrationTTL, auth.recoveryTTL, the
// confirm-token TTL) rather than a hand-typed string that can drift from it.
func formatDuration(d time.Duration) string {
	switch {
	case d > 0 && d%(24*time.Hour) == 0:
		return pluralUnit(int(d/(24*time.Hour)), "day")
	case d > 0 && d%time.Hour == 0:
		return pluralUnit(int(d/time.Hour), "hour")
	default:
		return pluralUnit(int(d/time.Minute), "minute")
	}
}

func pluralUnit(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// renderHTML builds the full HTML document: inline styles and a
// single-column nested-table layout throughout, no <style> block anywhere so
// nothing about the layout can depend on one being honored, and an explicit
// bgcolor ATTRIBUTE (not just a CSS background-color) on every table that
// needs a background — Outlook's Word rendering engine drops body {
// background } and unrecognized CSS but still honors the legacy bgcolor
// attribute, which is the entire reason this project's acceptance criteria
// call it out by name.
func (c emailContent) renderHTML() string {
	var b strings.Builder

	b.WriteString("<!DOCTYPE html>\n")
	b.WriteString(`<html lang="en"><head><meta charset="UTF-8">` + "\n")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1.0">` + "\n")
	b.WriteString("<title>" + html.EscapeString(c.Subject) + "</title></head>\n")
	b.WriteString(`<body style="margin:0;padding:0;background-color:` + colorPageBG + `;">` + "\n")

	if c.Preheader != "" {
		// Hidden preheader: shown by the client's inbox preview, never in the
		// rendered body. display:none is layout-irrelevant (it hides an inert
		// span), not a dependency on <style> for anything the message needs.
		b.WriteString(`<div style="display:none;max-height:0;overflow:hidden;opacity:0;mso-hide:all;">` +
			html.EscapeString(c.Preheader) + `</div>` + "\n")
	}

	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="` + colorPageBG + `" style="background-color:` + colorPageBG + `;">` + "\n")
	b.WriteString(`<tr><td align="center" style="padding:32px 16px;">` + "\n")

	// Card. The card <td> carries an explicit color (colorBodyText), not just
	// bgcolor — #0028's review measured this exact gap: every <p>/<h1> below
	// sets its own inline color, so the card container's own color was never
	// exercised by the five transactional templates. #0043's campaign wrapper
	// (campaign_render.go) drops an unstyled Markdown-shaped HTML fragment
	// into an equivalent card <td>, which — without this — would inherit the
	// document default (black) onto colorCardBG and render near-invisible.
	// Harmless here: every child element below sets its own color and
	// overrides this one by CSS specificity/inheritance rules either way.
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:480px;">` + "\n")
	b.WriteString(`<tr><td bgcolor="` + colorCardBG + `" style="background-color:` + colorCardBG + `;color:` + colorBodyText + `;border:1px solid ` + colorCardBorder + `;border-radius:6px;padding:32px 28px;font-family:` + fontBody + `;">` + "\n")

	if c.Eyebrow != "" {
		b.WriteString(`<div style="font-family:` + fontMono + `;font-size:12px;letter-spacing:0.04em;color:` + colorMutedText + `;margin:0 0 12px;">` +
			html.EscapeString(c.Eyebrow) + `</div>` + "\n")
	}
	b.WriteString(`<h1 style="font-family:` + fontMono + `;font-size:20px;line-height:28px;color:` + colorHeading + `;margin:0 0 20px;">` +
		html.EscapeString(c.Heading) + `</h1>` + "\n")

	for _, p := range c.IntroParagraphs {
		p = resolveCTA(p, "button")
		b.WriteString(`<p style="font-family:` + fontBody + `;font-size:15px;line-height:24px;color:` + colorBodyText + `;margin:0 0 16px;">` +
			html.EscapeString(p) + `</p>` + "\n")
	}

	if c.ButtonURL != "" {
		b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:8px 0 24px;"><tr>` + "\n")
		b.WriteString(`<td bgcolor="` + colorButtonBG + `" style="background-color:` + colorButtonBG + `;border-radius:4px;">` + "\n")
		b.WriteString(`<a href="` + html.EscapeString(c.ButtonURL) + `" style="display:inline-block;padding:12px 28px;font-family:` + fontMono + `;font-size:14px;font-weight:bold;color:` + colorButtonText + `;text-decoration:none;">` +
			html.EscapeString(c.ButtonText) + `</a></td></tr></table>` + "\n")
		b.WriteString(`<p style="font-family:` + fontBody + `;font-size:12px;line-height:18px;color:` + colorMutedText + `;margin:0 0 24px;word-break:break-all;">` +
			`If the button above doesn't work, copy this link into your browser: ` +
			`<a href="` + html.EscapeString(c.ButtonURL) + `" style="color:` + colorLinkText + `;">` + html.EscapeString(c.ButtonURL) + `</a></p>` + "\n")
	}

	for _, p := range c.NoteParagraphs {
		p = resolveCTA(p, "button")
		b.WriteString(`<p style="font-family:` + fontBody + `;font-size:13px;line-height:20px;color:` + colorMutedText + `;margin:0 0 12px;">` +
			html.EscapeString(p) + `</p>` + "\n")
	}

	b.WriteString("</td></tr></table>\n")

	// Footer (outside the card — no background dependency; muted text on the
	// page canvas, matched by a light-mode-safe fallback since colorMutedText
	// reads acceptably on white too, unlike colorHeading).
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:480px;">` + "\n")
	b.WriteString(`<tr><td style="padding:20px 12px 0;font-family:` + fontBody + `;font-size:12px;line-height:18px;color:` + colorMutedText + `;text-align:center;">` + "\n")

	if c.ShowListFooter {
		// colorMutedText matches this footer's original inline styling.
		b.WriteString(listFooterHTML(c.ManageURL, c.UnsubscribeURL, c.PhysicalAddress, colorMutedText))
	}
	b.WriteString(`<p style="margin:8px 0 0;">Open Circuit SF &middot; opencircuitsf.com</p>` + "\n")
	b.WriteString("</td></tr></table>\n")

	b.WriteString("</td></tr></table>\n")
	b.WriteString("</body></html>\n")
	return b.String()
}

// listFooterHTML renders the mailing-list footer block PRD §6.5 Path 2
// requires in every list-related email: the "Manage your interests ·
// Unsubscribe from everything" link pair (the exact literal labels, per
// #0043's carried-in criterion from #0035's review) and, when present, the
// physical mailing address paragraph.
//
// This is the ONE place that markup is written. Before #0043, campaign mail
// (campaign_render.go's wrapCampaignHTML) had grown its own second copy of
// this exact block with the same two labels and the same URL construction —
// #0042's reviewer flagged the duplication as a drift risk. #0043 resolves
// it by extracting this function: emailContent.renderHTML's ShowListFooter
// branch and wrapCampaignHTML both call it, so there is exactly one place
// that can drift, not two that have to be kept manually in sync.
//
// linkColor lets a caller with a different page background than the
// transactional templates' (colorMutedText, tuned for the dark card/page
// combination) pass its own — but the wording, structure, and URL
// construction stay identical either way.
func listFooterHTML(manageURL, unsubscribeURL, physicalAddress, linkColor string) string {
	var b strings.Builder
	b.WriteString(`<p style="margin:0 0 8px;">` +
		`<a href="` + html.EscapeString(manageURL) + `" style="color:` + linkColor + `;text-decoration:underline;">Manage your interests</a>` +
		` &middot; ` +
		`<a href="` + html.EscapeString(unsubscribeURL) + `" style="color:` + linkColor + `;text-decoration:underline;">Unsubscribe from everything</a>` +
		`</p>` + "\n")
	if physicalAddress != "" {
		b.WriteString(`<p style="margin:0;">` + html.EscapeString(physicalAddress) + `</p>` + "\n")
	}
	return b.String()
}

// listFooterText is listFooterHTML's plain-text equivalent: the same two
// PRD §6.5 Path 2 labels (as "Label: URL" lines, matching this project's
// other plain-text link convention) and the physical address when present.
// Shared by emailContent.renderText and wrapCampaignText for the identical
// reason listFooterHTML is shared on the HTML side.
func listFooterText(manageURL, unsubscribeURL, physicalAddress string) []string {
	lines := []string{
		"Manage your interests: " + manageURL,
		"Unsubscribe from everything: " + unsubscribeURL,
	}
	if physicalAddress != "" {
		lines = append(lines, physicalAddress)
	}
	return lines
}

// renderText builds the plain-text alternative from the same emailContent
// value renderHTML used. \r\n line endings match the rest of this project's
// mail bodies (internal/auth's existing plain-text sends).
func (c emailContent) renderText() string {
	var lines []string

	if c.Eyebrow != "" {
		lines = append(lines, c.Eyebrow, "")
	}
	lines = append(lines, c.Heading, "")

	for _, p := range c.IntroParagraphs {
		lines = append(lines, resolveCTA(p, "link"), "")
	}

	if c.ButtonURL != "" {
		lines = append(lines, c.ButtonText+": "+c.ButtonURL, "")
	}

	for _, p := range c.NoteParagraphs {
		lines = append(lines, resolveCTA(p, "link"))
	}
	if len(c.NoteParagraphs) > 0 {
		lines = append(lines, "")
	}

	lines = append(lines, "--")
	if c.ShowListFooter {
		lines = append(lines, listFooterText(c.ManageURL, c.UnsubscribeURL, c.PhysicalAddress)...)
	}
	lines = append(lines, "Open Circuit SF - opencircuitsf.com")

	return strings.Join(lines, "\r\n") + "\r\n"
}
