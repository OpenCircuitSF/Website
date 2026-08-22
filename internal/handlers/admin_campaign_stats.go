// Admin campaign stats (#0049; PRD §11): GET /admin/campaigns/{id}/stats,
// the per-campaign outcome screen — counts by send status, bounce/complaint
// counts reconciled from email_events, and the list of failed sends with
// their error messages.
//
// Read-only, matching admin_campaign_preflight.go's shape: no write, no
// audit row. All three underlying reads (StatusCounts, EventCounts,
// FailedSends) come from internal/mailing.CampaignStatsStore — see that
// file's package doc comment for why bounce/complaint counts are reconciled
// from email_events rather than trusted off email_sends.status.
package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// campaignStatsGatherer is the narrow seam this handler needs from #0049's
// own data layer. *mailing.CampaignStatsStore satisfies it.
type campaignStatsGatherer interface {
	StatusCounts(ctx context.Context, campaignID int64) (mailing.CampaignSendCounts, error)
	EventCounts(ctx context.Context, campaignID int64) (mailing.CampaignEventCounts, error)
	FailedSends(ctx context.Context, campaignID int64) ([]mailing.FailedSend, error)
}

// campaignStatsCampaignStore is the narrow campaign-data seam this handler
// needs: GetByID, for a 404 distinguishable from "a real campaign with zero
// sends" and for the campaign's current status in the response.
// *mailing.CampaignStore satisfies it.
type campaignStatsCampaignStore interface {
	GetByID(ctx context.Context, id int64) (mailing.Campaign, error)
}

// AdminCampaignStatsHandler serves GET /admin/campaigns/{id}/stats. Must be
// mounted behind middleware.RequireSession then middleware.RequireAdmin,
// exactly like every other admin handler — see cmd/opencircuit/main.go's
// adminRoutes.
type AdminCampaignStatsHandler struct {
	stats campaignStatsGatherer
	store campaignStatsCampaignStore
}

// NewAdminCampaignStatsHandler constructs an AdminCampaignStatsHandler.
func NewAdminCampaignStatsHandler(stats campaignStatsGatherer, store campaignStatsCampaignStore) *AdminCampaignStatsHandler {
	return &AdminCampaignStatsHandler{stats: stats, store: store}
}

// ── Response shapes ──────────────────────────────────────────────────────────

type campaignStatsCounts struct {
	Queued     int64 `json:"queued"`
	Sending    int64 `json:"sending"`
	Sent       int64 `json:"sent"`
	Failed     int64 `json:"failed"`
	Bounced    int64 `json:"bounced"`
	Complained int64 `json:"complained"`
	Skipped    int64 `json:"skipped"`
}

type campaignStatsReconciled struct {
	Bounced    int64 `json:"bounced"`
	Complained int64 `json:"complained"`
}

type campaignStatsFailedSend struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Error    string `json:"error,omitempty"`
	Attempts int    `json:"attempts"`
}

type campaignStatsResponse struct {
	CampaignID  int64                     `json:"campaign_id"`
	Status      string                    `json:"status"`
	Counts      campaignStatsCounts       `json:"counts"`
	Reconciled  campaignStatsReconciled   `json:"reconciled"`
	FailedSends []campaignStatsFailedSend `json:"failed_sends"`
}

// Stats handles GET /admin/campaigns/{id}/stats: 404 for an unknown
// campaign (checked via GetByID, matching every other campaign route's
// not-found handling); any other read error is a 500. FailedSends always
// serializes as `[]`, never `null`, matching this codebase's other list
// responses (e.g. campaignsListResponse).
func (h *AdminCampaignStatsHandler) Stats(w http.ResponseWriter, r *http.Request) {
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

	counts, err := h.stats.StatusCounts(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	reconciled, err := h.stats.EventCounts(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	failed, err := h.stats.FailedSends(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	failedViews := make([]campaignStatsFailedSend, 0, len(failed))
	for _, f := range failed {
		failedViews = append(failedViews, campaignStatsFailedSend{
			ID: f.ID, Email: f.Email, Error: f.Error, Attempts: f.Attempts,
		})
	}

	writeJSON(w, http.StatusOK, campaignStatsResponse{
		CampaignID: campaign.ID,
		Status:     campaign.Status,
		Counts: campaignStatsCounts{
			Queued: counts.Queued, Sending: counts.Sending, Sent: counts.Sent,
			Failed: counts.Failed, Bounced: counts.Bounced, Complained: counts.Complained,
			Skipped: counts.Skipped,
		},
		Reconciled: campaignStatsReconciled{
			Bounced: reconciled.Bounced, Complained: reconciled.Complained,
		},
		FailedSends: failedViews,
	})
}
