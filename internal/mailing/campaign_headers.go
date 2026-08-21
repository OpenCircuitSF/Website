// Header construction for campaign email (#0035). This is the seam #0045's
// send worker calls once it exists: build one Message per recipient, set
// Message.Headers = CampaignHeaders(baseURL, listDomain, recipient's own
// manage_token), and hand the Message to SESMailer.Send. Nothing in this
// file builds a Message on its own — the campaign body itself (subject,
// HTML/text content, the "Manage your interests · Unsubscribe from
// everything" footer) is #0043's job, which already reuses the same
// emailContent/renderHTML/renderText machinery the five transactional
// templates in transactional_templates.go use, including its
// ShowListFooter footer (tested since #0028's
// TestConfirmationAndAlreadySubscribed_CarryFooterLink). This file supplies
// only the RFC 8058 one-click headers layered on top of that body.
package mailing

import (
	"net/url"
	"strings"
)

// campaignListID is the List-Id header value PRD §6.5 gives verbatim for
// every campaign email:
//
//	List-Id: Open Circuit SF <list.opencircuitsf.com>
//
// The domain here is "list.opencircuitsf.com" (singular) — deliberately
// distinct from config.Config.EmailListDomain ("lists.opencircuitsf.com",
// plural), which is the mailto: address's real mail-receiving domain
// (#0057 points its MX at SES). RFC 2919 List-Id values are opaque
// identifiers, not DNS names that have to resolve, so the two are allowed
// to differ; PRD §6.5 and this issue's acceptance criteria both give the
// singular form literally, and TestSESMailer_Send_MultipartWithCustomHeaders
// (internal/mailing/ses_mailer_test.go) already pinned this exact string
// before this issue landed. Kept as its own constant — not derived from
// EmailListDomain — so it can never silently pick up the "s" if that
// field's value changes.
const campaignListID = "Open Circuit SF <list.opencircuitsf.com>"

// CampaignHeaders builds the RFC 8058 one-click unsubscribe header set PRD
// §6.5 requires on every campaign email, and ONLY campaign email — see
// transactional_templates.go's package doc and this package's
// TestNoTransactionalMessageCarriesCampaignHeaders, which proves none of
// the five transactional Build*Email functions ever set Message.Headers.
// The three headers work as a set: List-Unsubscribe-Post without
// List-Unsubscribe does nothing, and an HTTPS-only List-Unsubscribe
// without List-Unsubscribe-Post is treated as the older, less-trusted
// two-click form.
//
//   - baseURL is config.Config.BaseURL, the canonical
//     https://www.opencircuitsf.com host (CLAUDE.md §7) — never a bare
//     apex. The HTTPS form points at #0034's POST/GET /api/unsubscribe
//     endpoint, not the SPA's /unsubscribe confirmation page the
//     campaign footer's Path-2 links use.
//   - listDomain is config.Config.EmailListDomain
//     ("lists.opencircuitsf.com"). It must never be the bare apex
//     opencircuitsf.com: #0057 points this subdomain's MX at SES for
//     inbound Path-3 handling, and pointing the apex's own MX at SES
//     would hijack all mail to the domain (CLAUDE.md §9). config.Load
//     requires EMAIL_LIST_DOMAIN and #0045's Preflight gate independently
//     refuses to start a send with it unset (list_domain_unset) — but this
//     function does not trust either caller. A blank or all-whitespace
//     listDomain drops the mailto: form entirely rather than emit the
//     syntactically invalid "mailto:unsubscribe@?subject=..." this issue
//     (#0105) was filed over; List-Unsubscribe still validates with only
//     the HTTPS form present (RFC 8058 permits either or both), and
//     List-Unsubscribe-Post still applies, so one-click unsubscribe keeps
//     working.
//   - manageToken is the recipient's own subscribers.manage_token — the
//     exact value #0034's endpoint accepts as its ?token= query
//     parameter. Callers (the send worker, #0045) MUST build one Message
//     per recipient carrying THAT recipient's own token; reusing one
//     recipient's token across a batch send would let recipient A
//     unsubscribe recipient B with a single click. See
//     TestCampaignHeaders_MultiRecipientBatch_TokensDoNotCrossContaminate.
func CampaignHeaders(baseURL, listDomain, manageToken string) []Header {
	httpsURL := baseURL + "/api/unsubscribe?token=" + url.QueryEscape(manageToken)
	listUnsubscribe := "<" + httpsURL + ">"

	// A blank (or whitespace-only) listDomain would otherwise interpolate
	// into "mailto:unsubscribe@?subject=..." — a syntactically invalid URI
	// (#0105). config.Load requires EMAIL_LIST_DOMAIN and #0045's Preflight
	// gate refuses to start a send without it, but this function is a pure
	// seam any caller (including a future test or #0045 wiring bug) could
	// still invoke with an empty string, so it does not trust either guard.
	// RFC 8058 permits an HTTPS-only List-Unsubscribe, so dropping the
	// mailto: form here is a strictly valid degrade, not a broken one.
	if strings.TrimSpace(listDomain) != "" {
		mailtoURI := "mailto:unsubscribe@" + listDomain + "?subject=" + mailtoQueryEscape("unsubscribe:"+manageToken)
		listUnsubscribe = "<" + httpsURL + ">, <" + mailtoURI + ">"
	}

	return []Header{
		{Name: "List-Unsubscribe", Value: listUnsubscribe},
		{Name: "List-Unsubscribe-Post", Value: "List-Unsubscribe=One-Click"},
		{Name: "List-Id", Value: campaignListID},
	}
}

// mailtoQueryEscape percent-encodes s for use as a mailto: URI query
// component (RFC 6068's <hfields>), which — unlike an HTTP query string —
// encodes a literal space as %20, never "+". url.QueryEscape alone is the
// HTTP/RFC 3986 form; used unmodified here it would leave a stray "+" in a
// mail client's rendered Subject line the moment a token or address needed
// to encode a space.
func mailtoQueryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
