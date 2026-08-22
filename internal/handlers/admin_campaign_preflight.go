// Admin campaign read-only preflight (#0047; PRD §5.2/§6.6): GET
// /admin/campaigns/{id}/preflight, a dry-run evaluation of #0045's send gate
// that never sends anything and never transitions a campaign.
//
// # Why this endpoint exists
//
// #0047's acceptance criterion — "a send button disabled until every
// pre-send check passes" — is unimplementable without evaluating
// mailing.Preflight without sending: the only two existing call sites
// (admin_campaigns.go's Send, gated behind campaignPreflightChecker, and
// #0045's worker) both sit on the write path. Polling POST .../send to find
// out whether it WOULD succeed is exactly the thing that must not happen.
// This handler calls the SAME pure function
// (mailing.Preflight over a mailing.SendStore.GatherPreflight read) those two
// call sites use, so there remains exactly one copy of every requirement —
// see mailing.Preflight's own doc comment. It adds no requirement, no
// override, no bypass: CLAUDE.md §9's "never make the physical_address check
// bypassable from the UI" is untouched, because this endpoint cannot cause a
// send.
//
// # summary
//
// The response also carries the three facts #0047's send-confirmation dialog
// checks against — subject, from address, recipient count — read from the
// STORED row via GatherPreflight, never from the compose screen's own
// buffer. Confirming a destructive action against an operator's unsaved
// textarea is confirming against nothing.
//
// This handler writes no audit row: a dry-run read is not an auditable
// mutation, matching #0044's decision for GET .../audience.
package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// campaignPreflightGatherer is the narrow seam AdminCampaignPreflightHandler
// needs from #0045's data layer: the same GatherPreflight
// campaignPreflightAdapter (admin_campaigns.go) calls, plus GetByID so the
// response can report the subject/from/recipient-count summary without a
// second, redundant assembly of PreflightInput. *mailing.SendStore satisfies
// GatherPreflight; *mailing.CampaignStore satisfies GetByID — see
// NewAdminCampaignPreflightHandler.
type campaignPreflightGatherer interface {
	GatherPreflight(ctx context.Context, campaignID int64) (mailing.PreflightInput, error)
}

// campaignPreflightCampaignStore is the narrow campaign-data seam this
// handler needs for the response's summary (subject, recipient count) and
// for a 404 distinguishable from "campaign has no unmet requirements".
type campaignPreflightCampaignStore interface {
	GetByID(ctx context.Context, id int64) (mailing.Campaign, error)
}

// AdminCampaignPreflightHandler serves GET /admin/campaigns/{id}/preflight.
// Must be mounted behind middleware.RequireSession then
// middleware.RequireAdmin, exactly like every other admin handler — see
// cmd/opencircuit/main.go's adminRoutes.
type AdminCampaignPreflightHandler struct {
	gatherer campaignPreflightGatherer
	store    campaignPreflightCampaignStore
	settings mailing.SettingsReader
	fromAddr string
}

// NewAdminCampaignPreflightHandler constructs an
// AdminCampaignPreflightHandler. gatherer is *mailing.SendStore (#0045);
// store is *mailing.CampaignStore (#0041); settings/fromAddr let the
// response compute the same From header a real send would carry
// (mailing.ResolveFromHeader — see #0046's Test handler for the identical
// composition).
func NewAdminCampaignPreflightHandler(
	gatherer campaignPreflightGatherer,
	store campaignPreflightCampaignStore,
	settings mailing.SettingsReader,
	fromAddr string,
) *AdminCampaignPreflightHandler {
	return &AdminCampaignPreflightHandler{gatherer: gatherer, store: store, settings: settings, fromAddr: fromAddr}
}

type campaignPreflightSummary struct {
	Subject    string `json:"subject"`
	From       string `json:"from"`
	Recipients int64  `json:"recipients"`
}

type campaignPreflightResponse struct {
	OK      bool                       `json:"ok"`
	Unmet   []campaignPreflightFailure `json:"unmet"`
	Summary campaignPreflightSummary   `json:"summary"`
}

// Preflight handles GET /admin/campaigns/{id}/preflight: evaluates
// mailing.Preflight over a fresh GatherPreflight read and reports the
// result, never sending anything or transitioning the campaign. 404 for an
// unknown campaign (checked via GetByID, matching every other campaign
// route's not-found handling); any other GatherPreflight error is a 500.
func (h *AdminCampaignPreflightHandler) Preflight(w http.ResponseWriter, r *http.Request) {
	id, ok := parseCampaignID(w, r)
	if !ok {
		return
	}

	campaign, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	in, err := h.gatherer.GatherPreflight(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	result := mailing.Preflight(in)
	failures := make([]campaignPreflightFailure, len(result.Failures))
	for i, f := range result.Failures {
		failures[i] = campaignPreflightFailure{Code: f.Code, Message: f.Message}
	}

	recipients := in.AudienceCount
	if recipients < 0 {
		recipients = 0
	}

	writeJSON(w, http.StatusOK, campaignPreflightResponse{
		OK:    result.OK(),
		Unmet: failures,
		Summary: campaignPreflightSummary{
			Subject:    campaign.Subject,
			From:       mailing.ResolveFromHeader(r.Context(), h.settings, h.fromAddr),
			Recipients: recipients,
		},
	})
}
