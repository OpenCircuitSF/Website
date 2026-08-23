// Admin workshop body preview (#0136; PRD §5.2, §6.2): POST
// /admin/workshops/{id}/preview renders a workshop's STORED body_md through
// goldmark, mirroring AdminCampaignPreviewHandler.Preview
// (admin_campaign_preview.go, #0046) as closely as the two domains allow:
// same "never accept an unsaved-body override" contract (this route ignores
// its request body entirely — decodeJSON is never called — and always
// renders the row currently in the database), same admin gate
// (RequireSession then RequireAdmin), same 404/500 shape. It has no "test
// send" counterpart — a workshop page is not an email, there is nothing to
// deliver.
//
// # Why this closes #0052's known hole instead of re-hardening it again
//
// web/src/lib/markdown.ts was a dependency-free, hand-rolled Markdown->HTML
// renderer written for #0052's admin preview and rendered client-side via
// `{@html}`. Its first version shipped a live XSS hole (control characters
// smuggling `javascript:` past a scheme allowlist, past 22 passing tests —
// fixed in b562800) precisely because a hand-rolled sanitizer and a
// browser's URL-normalization algorithm can silently disagree about what a
// URL is. #0052's own reviewer named the fix: internal/mailing already
// renders email_campaigns.body_md through goldmark's safe mode
// (campaign_markdown.go, #0042) with zero raw HTML, no images, and every
// dangerous-scheme href dropped — proven safe against exactly this bypass
// class already. renderWorkshopBodyHTML below is a thin wrapper over that
// same goldmark instance (mailing.RenderMarkdownHTML), so a workshop body
// gets the identical security posture without a second sanitizer to keep in
// sync with the first.
//
// # Preview and publish share one function, not two goldmark configurations
//
// public_workshops.go's toPublicView calls the exact same
// renderWorkshopBodyHTML this file's Preview calls — there is only one call
// site in this package that turns a workshop's body_md into HTML. That is
// what makes "preview matches what actually publishes" true by
// construction rather than by two independently-written renderers
// happening to agree today. See
// TestAdminWorkshopPreview_MatchesPublishedRender for the end-to-end proof:
// it renders the SAME stored workshop through both this route and
// GET /api/workshops/{slug} and asserts the HTML is byte-identical.
package handlers

import (
	"errors"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// workshopPreviewResponse is POST /admin/workshops/{id}/preview's response
// shape. Deliberately narrower than campaignPreviewResponse
// (admin_campaign_preview.go): a workshop page has no subject line and no
// plain-text alternative to render, only the one HTML fragment
// WorkshopDetail.svelte's `.workshop-body` div shows.
type workshopPreviewResponse struct {
	HTML string `json:"html"`
}

// Preview handles POST /admin/workshops/{id}/preview: loads the STORED
// workshop row and renders its body_md through renderWorkshopBodyHTML —
// the same function public_workshops.go's toPublicView calls for the
// published page. Like AdminCampaignPreviewHandler.Preview, it never calls
// decodeJSON: there is no field in the request this handler ever reads, so
// "an unsaved-body override" is not a validation rule that could be
// forgotten later, it is a code path that does not exist. See
// TestAdminWorkshopPreview_IgnoresRequestBody.
func (h *AdminWorkshopsHandler) Preview(w http.ResponseWriter, r *http.Request) {
	id, ok := parseWorkshopID(w, r)
	if !ok {
		return
	}

	wk, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, workshops.ErrNotFound):
		writeError(w, http.StatusNotFound, "workshop not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	html, err := renderWorkshopBodyHTML(wk.BodyMD)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "workshop body does not render: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, workshopPreviewResponse{HTML: html})
}

// renderWorkshopBodyHTML renders an optional body_md through the exact same
// goldmark pipeline email_campaigns bodies use
// (internal/mailing.RenderMarkdownHTML, #0042/#0043): safe mode (no raw
// HTML, no <script>, no dangerous-scheme hrefs — html.WithUnsafe() is never
// passed), and Markdown-native images stripped to an inert "[image not
// included: ...]" marker (campaign_markdown.go's campaignImageRenderer) —
// the workshop admin's hand-rolled client renderer never supported inline
// images either (its link regex only matched `[text](url)`, never
// `![alt](url)`), so this is not a regression for workshop bodies. A nil or
// empty body_md renders to "" rather than erroring: an unwritten workshop
// body is not a render failure, matching goldmark's own behavior on empty
// input and the "empty is not a crash" posture
// AdminCampaignPreviewHandler's package doc comment documents for
// campaigns.
//
// This is the SINGLE function both admin_workshop_preview.go's Preview and
// public_workshops.go's toPublicView call — see this file's package doc
// comment for why that single call site is what makes preview/publish
// parity provable rather than assumed.
func renderWorkshopBodyHTML(bodyMD *string) (string, error) {
	if bodyMD == nil || *bodyMD == "" {
		return "", nil
	}
	return mailing.RenderMarkdownHTML(*bodyMD)
}

// renderWorkshopBodyHTMLPtr adapts renderWorkshopBodyHTML for
// publicWorkshopView.BodyHTML (a `*string`, `json:",omitempty"` — see
// public_workshops.go): a render error or an empty result both omit
// body_html from the response rather than 500ing the whole page, the same
// "degraded but safe" posture that file's own resolveInterests already
// uses for a failed interest-tag lookup. A render error here is not
// expected in practice (goldmark's Convert does not error on ordinary
// Markdown input; see RenderMarkdownHTML's own doc comment), but a
// malformed public page is a worse failure mode than a missing body
// section, so this never turns an error into a 500.
func renderWorkshopBodyHTMLPtr(bodyMD *string) *string {
	html, err := renderWorkshopBodyHTML(bodyMD)
	if err != nil || html == "" {
		return nil
	}
	return &html
}
