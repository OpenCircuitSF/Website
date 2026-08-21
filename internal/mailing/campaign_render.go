// Campaign rendering orchestration (#0042): one CampaignRenderInput in,
// one HTML document and one plain-text alternative out, both built from
// the same in.BodyMarkdown via campaign_markdown.go, with the recipient's
// manage token substituted into the footer's preference-center and
// unsubscribe links and the physical mailing address appended to both
// parts.
//
// Scope boundary with #0043 ("Build the campaign HTML email template"):
// this file's wrapCampaignHTML produces a complete, valid, mail-safe HTML
// document — single-column table layout, inline styles only, footer with
// both PRD §6.5 labels and the physical address — but deliberately does
// NOT carry the dark/terminal brand skin (colorPageBG/colorCardBG/mono
// headers from templates.go) or the specific per-client rendering
// verification #0043's acceptance criteria call for. That is a real seam,
// not an oversight: reusing templates.go's emailContent/renderHTML
// directly for this would mean embedding goldmark's unstyled output
// (plain <p>/<h2>/<ul> with no inline color) onto that dark card, which
// renders as near-invisible text — the same "background stripped" failure
// #0028's review flagged, except guaranteed rather than residual. Applying
// inline brand styling to arbitrary Markdown-shaped HTML (a heading, a
// list, a link, at N levels of nesting) is exactly the "single-column
// table layout, inline styles only... dark ground, mono headers, green
// accents" work #0043's acceptance criteria already describe as its own.
// #0043 should replace wrapCampaignHTML (or the block it builds around
// bodyHTML) with the branded version; RenderMarkdownHTML, RenderMarkdownText,
// CampaignRenderInput, and the token/address handling below are meant to
// carry over unchanged.
package mailing

import (
	"errors"
	"fmt"
	"html"
	"strings"
)

// CampaignRenderInput is the input to RenderCampaign. Field names and types
// match issues/0045.md §5's plan exactly — the send worker builds one of
// these per recipient inside its send loop — and issues/0046.md's preview
// endpoint builds one per preview request from a stored campaign row.
type CampaignRenderInput struct {
	Subject         string
	Preheader       string
	BodyMarkdown    string
	BaseURL         string
	ManageToken     string
	PhysicalAddress string
}

// CampaignRenderer is the seam issues/0045.md §5 calls
// "w.render.Campaign(CampaignRenderInput{...})" — the send worker's only
// dependency on this package for rendering. The interface exists so the
// worker's tests can inject a fake instead of exercising a real render on
// every case; MarkdownCampaignRenderer is the only production
// implementation.
type CampaignRenderer interface {
	Campaign(CampaignRenderInput) (htmlBody, textBody string, err error)
}

// MarkdownCampaignRenderer is the CampaignRenderer this issue ships. It is
// stateless — a zero value is ready to use — since campaignMarkdown
// (campaign_markdown.go) is itself a stateless, concurrency-safe package
// value shared by every render.
type MarkdownCampaignRenderer struct{}

// Campaign implements CampaignRenderer.
func (MarkdownCampaignRenderer) Campaign(in CampaignRenderInput) (string, string, error) {
	return RenderCampaign(in)
}

// Sentinel errors for RenderCampaign's "fail loudly" requirement: a missing
// manage token or base URL would otherwise produce a broken or empty
// unsubscribe href, which is a compliance failure (a dead one-click link),
// not a cosmetic one — this issue's acceptance criteria call that out by
// name for the token; BaseURL gets the identical treatment because a blank
// BaseURL produces the identical failure mode (a relative "/unsubscribe?…"
// href with nothing to resolve against in a recipient's mail client).
var (
	ErrMissingManageToken = errors.New("mailing: campaign render: manage token is empty")
	ErrMissingBaseURL     = errors.New("mailing: campaign render: base URL is empty")
)

// RenderCampaign renders one campaign to a complete HTML document and a
// complete plain-text alternative from in.BodyMarkdown, substituting
// in.ManageToken into the footer's "Manage your interests" /
// "Unsubscribe from everything" links (PRD §6.5's exact wording — the
// literal labels #0043's carried-in criteria pin) and appending
// in.PhysicalAddress to both parts.
//
// Determinism: for a fixed CampaignRenderInput, RenderCampaign always
// produces the identical two strings — no time-of-day, no randomness, no
// map iteration anywhere in this path (confirmed:
// TestRenderCampaign_Deterministic renders the same input twice and
// compares byte-for-byte). #0046's preview and #0045's actual send must
// agree, or an operator previews one thing and sends another.
//
// Render failures — an empty token, an empty base URL, or a panic
// somewhere in the Markdown conversion (a defensive backstop for
// pathological input, e.g. runaway nesting) — return a typed error rather
// than a partial document; RenderCampaign never panics out of this
// function. #0045's plan (§2) calls this during preflight with the
// sentinel manage token "PREFLIGHT-DRY-RUN" specifically to surface
// body_render_failed as an error before a send starts, and discards the
// rendered bytes either way.
func RenderCampaign(in CampaignRenderInput) (htmlOut, textOut string, err error) {
	defer func() {
		if r := recover(); r != nil {
			htmlOut, textOut = "", ""
			err = fmt.Errorf("mailing: campaign render panicked: %v", r)
		}
	}()

	if strings.TrimSpace(in.ManageToken) == "" {
		return "", "", ErrMissingManageToken
	}
	if strings.TrimSpace(in.BaseURL) == "" {
		return "", "", ErrMissingBaseURL
	}

	bodyHTML, bodyText, err := renderCampaignBody(in.BodyMarkdown)
	if err != nil {
		return "", "", fmt.Errorf("mailing: campaign render: %w", err)
	}

	// Same construction the five existing transactional templates use
	// (BuildConfirmationEmail et al., transactional_templates.go) and the
	// exact one #0043's carried-in criteria specify reusing.
	manageURL := in.BaseURL + "/preferences?token=" + in.ManageToken
	unsubscribeURL := in.BaseURL + "/unsubscribe?token=" + in.ManageToken

	htmlOut = wrapCampaignHTML(in.Subject, in.Preheader, bodyHTML, manageURL, unsubscribeURL, in.PhysicalAddress)
	textOut = wrapCampaignText(bodyText, manageURL, unsubscribeURL, in.PhysicalAddress)
	return htmlOut, textOut, nil
}

// renderCampaignBody is a package var, not a plain function call, so
// TestRenderCampaign_PanicIsRecoveredAsError can inject a fault and prove
// RenderCampaign's recover() (above) genuinely converts a panic into a
// typed error instead of crashing the caller — the property #0045's plan
// depends on: Preflight's dry render must never crash the send worker.
// Production always uses renderCampaignBodyDefault.
var renderCampaignBody = renderCampaignBodyDefault

func renderCampaignBodyDefault(md string) (bodyHTML, bodyText string, err error) {
	bodyHTML, err = RenderMarkdownHTML(md)
	if err != nil {
		return "", "", err
	}
	bodyText, err = RenderMarkdownText(md)
	if err != nil {
		return "", "", err
	}
	return bodyHTML, bodyText, nil
}

// wrapCampaignHTML assembles a complete, mail-safe HTML document around an
// already-sanitized body fragment: single-column table layout, inline
// styles only (no <style> block, no CSS custom properties), an explicit
// bgcolor attribute on the outer table (Outlook ignores a CSS
// background-color on <body>), and a footer carrying both required PRD
// §6.5 links plus the physical address. See this file's package doc for
// what it deliberately does not attempt (the brand skin).
func wrapCampaignHTML(subject, preheader, bodyHTML, manageURL, unsubscribeURL, physicalAddress string) string {
	const bg = "#ffffff"
	const text = "#10140f"
	const muted = "#4c574a"
	const link = "#2f7d0c"
	const rule = "#cfd8ca"
	const fontBody = "-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n")
	b.WriteString(`<html lang="en"><head><meta charset="UTF-8">` + "\n")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1.0">` + "\n")
	b.WriteString("<title>" + html.EscapeString(subject) + "</title></head>\n")
	b.WriteString(`<body style="margin:0;padding:0;background-color:` + bg + `;">` + "\n")

	if preheader != "" {
		b.WriteString(`<div style="display:none;max-height:0;overflow:hidden;opacity:0;mso-hide:all;">` +
			html.EscapeString(preheader) + `</div>` + "\n")
	}

	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="` + bg + `" style="background-color:` + bg + `;">` + "\n")
	b.WriteString(`<tr><td align="center" style="padding:24px 16px;">` + "\n")

	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:600px;">` + "\n")
	b.WriteString(`<tr><td style="font-family:` + fontBody + `;font-size:15px;line-height:24px;color:` + text + `;padding:8px 4px;">` + "\n")
	b.WriteString(bodyHTML)
	b.WriteString("\n</td></tr>\n")

	b.WriteString(`<tr><td style="padding:20px 4px 0;font-family:` + fontBody + `;font-size:12px;line-height:18px;color:` + muted + `;border-top:1px solid ` + rule + `;">` + "\n")
	b.WriteString(`<p style="margin:16px 0 8px;">` +
		`<a href="` + html.EscapeString(manageURL) + `" style="color:` + link + `;text-decoration:underline;">Manage your interests</a>` +
		` &middot; ` +
		`<a href="` + html.EscapeString(unsubscribeURL) + `" style="color:` + link + `;text-decoration:underline;">Unsubscribe from everything</a>` +
		`</p>` + "\n")
	if physicalAddress != "" {
		b.WriteString(`<p style="margin:0 0 8px;">` + html.EscapeString(physicalAddress) + `</p>` + "\n")
	}
	b.WriteString(`<p style="margin:0;">Open Circuit SF &middot; opencircuitsf.com</p>` + "\n")
	b.WriteString("</td></tr>\n")

	b.WriteString("</table>\n")
	b.WriteString("</td></tr></table>\n")
	b.WriteString("</body></html>\n")
	return b.String()
}

// wrapCampaignText appends the same two required footer links and the
// physical address to the rendered body text, in the same "--" / label /
// closing-line shape templates.go's renderText uses for the transactional
// templates' ShowListFooter footer, so a recipient sees a consistent
// pattern across every kind of mail this project sends.
func wrapCampaignText(bodyText, manageURL, unsubscribeURL, physicalAddress string) string {
	var lines []string
	if bodyText != "" {
		lines = append(lines, strings.TrimRight(bodyText, "\r\n"), "")
	}
	lines = append(lines, "--")
	lines = append(lines, "Manage your interests: "+manageURL)
	lines = append(lines, "Unsubscribe from everything: "+unsubscribeURL)
	if physicalAddress != "" {
		lines = append(lines, physicalAddress)
	}
	lines = append(lines, "Open Circuit SF - opencircuitsf.com")
	return strings.Join(lines, "\r\n") + "\r\n"
}
