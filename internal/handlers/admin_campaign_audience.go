// Admin campaign audience preview (#0044; PRD §5.2/§6.6): GET
// /admin/campaigns/{id}/audience, a read-only dry-run of "who would this
// campaign mail right now" — the count an operator checks before scheduling
// a send, and the count #0041's Send handler compares against the typed
// confirm_count (see admin_campaigns.go's Send).
//
// A separate handler and file, following AdminSuppressionsHandler's
// precedent rather than growing AdminCampaignsHandler, so the store seam
// stays narrow (campaignAudiencePreviewer below, LoadAudience+Preview only —
// no CampaignStore CRUD needed here).
//
// This endpoint reads only. It never materializes an email_sends row —
// mailing.AudienceStore.Preview's own contract — which is what makes it
// safe to poll from the compose screen on every checkbox change.
package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// campaignAudienceSampleLimit bounds AudiencePreview.Sample. A server-side
// constant, not a client-supplied query parameter: a client-controlled
// limit would turn a dry-run count endpoint into a bulk export of the
// subscriber list through a route whose purpose is a count.
const campaignAudienceSampleLimit = 20

// campaignAudiencePreviewer is the behavior AdminCampaignAudienceHandler
// needs from the data layer. *mailing.AudienceStore satisfies it.
type campaignAudiencePreviewer interface {
	LoadAudience(ctx context.Context, campaignID int64) (mailing.Audience, error)
	Preview(ctx context.Context, aud mailing.Audience, sampleLimit int) (mailing.AudiencePreview, error)
}

// AdminCampaignAudienceHandler serves GET /admin/campaigns/{id}/audience.
// Must be mounted behind middleware.RequireSession then
// middleware.RequireAdmin, exactly like every other admin handler — see
// cmd/opencircuit/main.go's adminRoutes.
type AdminCampaignAudienceHandler struct {
	store campaignAudiencePreviewer
}

// NewAdminCampaignAudienceHandler constructs an AdminCampaignAudienceHandler.
func NewAdminCampaignAudienceHandler(store campaignAudiencePreviewer) *AdminCampaignAudienceHandler {
	return &AdminCampaignAudienceHandler{store: store}
}

type campaignAudienceView struct {
	Mode        string   `json:"mode"`
	InterestIDs []int64  `json:"interest_ids"`
	Count       int64    `json:"count"`
	Sample      []string `json:"sample"`
	Warnings    []string `json:"warnings"`
}

// audienceErrorMessage maps the two audience-targeting guard errors
// mailing.Preview can return to an operator-facing message, stripping the
// "mailing: " package prefix — mirrors admin_campaigns.go's
// campaignAudienceErrorMessage, kept separate because ErrAudienceInterestsRequired
// (this issue's own materialize-time guard) is a distinct error value from
// ErrCampaignInterestsRequired (the write-time guard that function maps).
func audienceErrorMessage(err error) string {
	switch {
	case errors.Is(err, mailing.ErrAudienceInterestsRequired):
		return "this audience mode requires at least one interest to be selected"
	case errors.Is(err, mailing.ErrUnknownAudienceMode):
		return "unknown audience mode"
	default:
		return "invalid audience targeting"
	}
}

// Preview handles GET /admin/campaigns/{id}/audience.
func (h *AdminCampaignAudienceHandler) Preview(w http.ResponseWriter, r *http.Request) {
	id, ok := parseCampaignID(w, r)
	if !ok {
		return
	}

	aud, err := h.store.LoadAudience(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	preview, err := h.store.Preview(r.Context(), aud, campaignAudienceSampleLimit)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrUnknownAudienceMode), errors.Is(err, mailing.ErrAudienceInterestsRequired):
		writeError(w, http.StatusConflict, audienceErrorMessage(err))
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	interestIDs := aud.InterestIDs
	if interestIDs == nil {
		interestIDs = []int64{}
	}
	sample := preview.Sample
	if sample == nil {
		sample = []string{}
	}
	warnings := preview.Warnings
	if warnings == nil {
		warnings = []string{}
	}

	writeJSON(w, http.StatusOK, campaignAudienceView{
		Mode:        aud.Mode,
		InterestIDs: interestIDs,
		Count:       preview.Count,
		Sample:      sample,
		Warnings:    warnings,
	})
}
