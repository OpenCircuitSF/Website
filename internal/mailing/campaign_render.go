// Campaign rendering orchestration (#0042, branded wrapper landed by
// #0043): one CampaignRenderInput in, one HTML document and one plain-text
// alternative out, both built from the same in.BodyMarkdown via
// campaign_markdown.go, with the recipient's manage token substituted into
// the footer's preference-center and unsubscribe links and the physical
// mailing address appended to both parts.
//
// #0043 replaced wrapCampaignHTML's plain light-palette document with the
// dark/terminal brand skin: the same colorPageBG/colorCardBG/mono-header
// palette templates.go's five transactional templates use, applied to the
// arbitrary Markdown-shaped body fragment via campaign_body_style.go's
// styleCampaignBodyHTML (inline styling of goldmark's unstyled output —
// see that file's doc for why this is safe and why RenderMarkdownHTML
// itself does not need to change). RenderMarkdownHTML, RenderMarkdownText,
// and CampaignRenderInput are unchanged from #0042, exactly as #0043's
// carried-in criteria required.
//
// Footer reconciliation (#0043, resolving the two-footer conflict #0042's
// reviewer flagged): wrapCampaignHTML/wrapCampaignText no longer hand-write
// their own copy of the "Manage your interests · Unsubscribe from
// everything" block. Both now call templates.go's listFooterHTML /
// listFooterText — the exact functions emailContent.renderHTML/renderText
// call for the five transactional templates' ShowListFooter footer. There
// is now exactly one place in this package that renders that footer's
// markup; campaign mail and transactional mail differ only in the
// surrounding document (this file's dark-skinned wrapper vs.
// emailContent's card), never in the footer's wording or link
// construction.
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

	// styleCampaignBodyHTML brands bodyHTML's already-sanitized fragment in
	// place (inline styles only, no re-sanitization) — see
	// campaign_body_style.go.
	htmlOut = wrapCampaignHTML(in.Subject, in.Preheader, styleCampaignBodyHTML(bodyHTML), manageURL, unsubscribeURL, in.PhysicalAddress)
	textOut = wrapCampaignText(bodyText, manageURL, unsubscribeURL, in.PhysicalAddress)
	return htmlOut, textOut, nil
}

// BuildCampaignMessage renders one campaign for one recipient and assembles
// the complete outbound Message: subject, HTML/text bodies (via
// RenderCampaign), and the RFC 8058 one-click headers (via CampaignHeaders)
// — the same "one Build* function per kind of mail" shape the five
// transactional templates in transactional_templates.go use, so
// TestAllMessagesEnumeratesEveryBuildEmailFunc's builder guard (widened by
// #0043 from a name regex to a return-type check, per #0085's carried-in
// review note) sees this builder too instead of leaving campaign mail
// permanently invisible to it.
//
// to is the recipient address and listDomain is
// config.Config.EmailListDomain — both live outside CampaignRenderInput
// because they are CampaignHeaders' inputs, not RenderCampaign's; in.BaseURL
// is reused for CampaignHeaders' own baseURL parameter so the RFC 8058
// https:// unsubscribe link and the in-body Path 2 links are always built
// from the same base URL.
//
// #0045's send worker (issues/0045.md §0's ownership table: "per-recipient
// Message construction and CampaignHeaders") is free to call this directly
// per recipient instead of re-deriving the same assembly itself — this
// function does not claim any part of the worker's loop, claim/throttle, or
// shutdown behavior, only the one-recipient rendering step those own.
func BuildCampaignMessage(to string, in CampaignRenderInput, listDomain string) (Message, error) {
	htmlBody, textBody, err := RenderCampaign(in)
	if err != nil {
		return Message{}, err
	}
	return Message{
		To:       to,
		Subject:  in.Subject,
		HTMLBody: htmlBody,
		TextBody: textBody,
		Headers:  CampaignHeaders(in.BaseURL, listDomain, in.ManageToken),
	}, nil
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
// already-sanitized, already-branded body fragment (the caller applies
// styleCampaignBodyHTML before calling this): single-column table layout,
// inline styles only (no <style> block, no CSS custom properties), an
// explicit bgcolor attribute on both the outer table and the card (Outlook
// ignores a CSS background-color on <body> and can drop it elsewhere), the
// same dark/terminal palette and bulletproof font stacks templates.go
// defines for the five transactional templates, and a footer carrying both
// required PRD §6.5 links plus the physical address via templates.go's
// shared listFooterHTML — see this file's package doc for the
// reconciliation this replaces.
func wrapCampaignHTML(subject, preheader, styledBodyHTML, manageURL, unsubscribeURL, physicalAddress string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n")
	b.WriteString(`<html lang="en"><head><meta charset="UTF-8">` + "\n")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1.0">` + "\n")
	b.WriteString("<title>" + html.EscapeString(subject) + "</title></head>\n")
	b.WriteString(`<body style="margin:0;padding:0;background-color:` + colorPageBG + `;">` + "\n")

	if preheader != "" {
		b.WriteString(`<div style="display:none;max-height:0;overflow:hidden;opacity:0;mso-hide:all;">` +
			html.EscapeString(preheader) + `</div>` + "\n")
	}

	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="` + colorPageBG + `" style="background-color:` + colorPageBG + `;">` + "\n")
	b.WriteString(`<tr><td align="center" style="padding:24px 16px;">` + "\n")

	// Card. Explicit color on the <td> (not just bgcolor) — see
	// templates.go's identical comment on its own card <td> for why this
	// matters: styleCampaignBodyHTML already sets color on every element it
	// recognizes, but this is the fallback for anything it doesn't (a
	// goldmark node kind added by a future extension) and matches
	// templates.go's card exactly rather than trusting inheritance alone.
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:600px;">` + "\n")
	b.WriteString(`<tr><td bgcolor="` + colorCardBG + `" style="background-color:` + colorCardBG + `;color:` + colorBodyText + `;border:1px solid ` + colorCardBorder + `;border-radius:6px;padding:28px 24px;font-family:` + fontBody + `;">` + "\n")
	b.WriteString(styledBodyHTML)
	b.WriteString("\n</td></tr></table>\n")

	// Footer — outside the card, same pattern templates.go's renderHTML
	// uses: muted text on the page canvas, matched by a light-mode-safe
	// fallback (colorMutedText reads acceptably on white too, unlike
	// colorHeading — CLAUDE.md §8).
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:600px;">` + "\n")
	b.WriteString(`<tr><td style="padding:20px 12px 0;font-family:` + fontBody + `;font-size:12px;line-height:18px;color:` + colorMutedText + `;text-align:center;">` + "\n")
	b.WriteString(listFooterHTML(manageURL, unsubscribeURL, physicalAddress, colorMutedText))
	b.WriteString(`<p style="margin:8px 0 0;">Open Circuit SF &middot; opencircuitsf.com</p>` + "\n")
	b.WriteString("</td></tr></table>\n")

	b.WriteString("</td></tr></table>\n")
	b.WriteString("</body></html>\n")
	return b.String()
}

// wrapCampaignText appends the same two required footer links and the
// physical address to the rendered body text, via templates.go's shared
// listFooterText, in the same "--" / label / closing-line shape
// templates.go's renderText uses for the transactional templates'
// ShowListFooter footer, so a recipient sees a consistent pattern across
// every kind of mail this project sends.
func wrapCampaignText(bodyText, manageURL, unsubscribeURL, physicalAddress string) string {
	var lines []string
	if bodyText != "" {
		lines = append(lines, strings.TrimRight(bodyText, "\r\n"), "")
	}
	lines = append(lines, "--")
	lines = append(lines, listFooterText(manageURL, unsubscribeURL, physicalAddress)...)
	lines = append(lines, "Open Circuit SF - opencircuitsf.com")
	return strings.Join(lines, "\r\n") + "\r\n"
}
