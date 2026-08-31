// Admin campaign archive toggle (#0123, PRD §6.8): PATCH
// /admin/campaigns/{id}/archive lets an admin pull a sent campaign's public
// archive page (published -> withheld) or restore it (withheld ->
// published). Backed by mailing.CampaignStore.SetArchiveStatus — see that
// method's own doc comment for why 'pending' is not a reachable target
// here (a campaign that has never sent has no archive page to toggle).
//
// # Cache invalidation (#0319)
//
// A successful Patch calls invalidator.Invalidate() after the store write
// commits, following AdminWorkshopsHandler's own established pattern
// (internal/handlers/admin_workshops.go's doc comment) rather than
// inventing a second mechanism -- it is the SAME *seo.Site instance and the
// SAME method (named InvalidateWorkshops until #0325), which clears both
// the per-path meta cache and the sitemap cache regardless of which admin
// action triggered it. Before #0319 this call didn't exist here at all, so
// withholding or re-publishing a campaign left sitemap.xml (and the
// archive page's own meta tags) stale for up to defaultCacheTTL (60s) --
// the one window where a delay is least welcome, since an admin withholds
// a campaign because something in it should not be public.
// seoCacheInvalidator (admin_workshops.go, named workshopCacheInvalidator
// until #0335) is reused directly rather than redeclared: same package,
// same shape, same underlying *seo.Site.
package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// campaignArchiveStore is the narrow slice of mailing.CampaignStore this
// handler needs. *mailing.CampaignStore satisfies it. GetByID is used only
// to read the pre-transition archive_status for the audit entry's
// from_status field (mirrors AdminCampaignsHandler.Cancel's own
// read-then-transition shape); SetArchiveStatus performs the actual write.
type campaignArchiveStore interface {
	GetByID(ctx context.Context, id int64) (mailing.Campaign, error)
	SetArchiveStatus(ctx context.Context, id int64, status string) (mailing.Campaign, error)
}

// AdminCampaignArchiveHandler serves PATCH /admin/campaigns/{id}/archive.
type AdminCampaignArchiveHandler struct {
	store   campaignArchiveStore
	auditor *audit.Logger
	// invalidator clears internal/seo's meta/sitemap caches after a
	// successful transition (#0319) -- seoCacheInvalidator
	// (admin_workshops.go, same package, named workshopCacheInvalidator
	// until #0335), not a new type. nil disables the call (test-only;
	// cmd/opencircuit/main.go's production wiring always passes the real
	// *seo.Site -- see NewAdminCampaignArchiveHandler's doc comment).
	invalidator seoCacheInvalidator
}

// NewAdminCampaignArchiveHandler constructs an AdminCampaignArchiveHandler.
// auditor may be nil in tests, matching every other admin campaign
// handler's own nil-guard convention. invalidator is *seo.Site in
// production (cmd/opencircuit/main.go passes the SAME instance
// adminWorkshopsH already uses, so both handlers invalidate through one
// shared cache) -- see this file's package doc comment, "Cache
// invalidation (#0319)".
func NewAdminCampaignArchiveHandler(store campaignArchiveStore, auditor *audit.Logger, invalidator seoCacheInvalidator) *AdminCampaignArchiveHandler {
	return &AdminCampaignArchiveHandler{store: store, auditor: auditor, invalidator: invalidator}
}

func (h *AdminCampaignArchiveHandler) invalidate() {
	if h.invalidator != nil {
		h.invalidator.Invalidate()
	}
}

// patchCampaignArchiveRequest is the PATCH body: the target archive_status,
// one of "published" or "withheld" (this issue's acceptance criterion:
// "toggles published / withheld"). No other field is accepted — the same
// "no status field on the content PATCH" posture admin_campaigns.go's own
// patchCampaignRequest carries, inverted: this route changes exactly one
// thing and nothing else.
type patchCampaignArchiveRequest struct {
	Status string `json:"status"`
}

// Patch handles PATCH /admin/campaigns/{id}/archive.
func (h *AdminCampaignArchiveHandler) Patch(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseCampaignID(w, r)
	if !ok {
		return
	}

	var req patchCampaignArchiveRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Status != mailing.ArchiveStatusPublished && req.Status != mailing.ArchiveStatusWithheld {
		writeError(w, http.StatusBadRequest, `status must be "published" or "withheld"`)
		return
	}

	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	fromStatus := current.ArchiveStatus

	updated, err := h.store.SetArchiveStatus(r.Context(), id, req.Status)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	case errors.Is(err, mailing.ErrArchiveStatusNotEditable):
		writeError(w, http.StatusConflict, "this campaign has not been sent yet, so it has no archive page to update")
		return
	case errors.Is(err, mailing.ErrUnknownArchiveStatus):
		writeError(w, http.StatusBadRequest, `status must be "published" or "withheld"`)
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// #0319: after the store write commits, not before -- a cache clear
	// ahead of a failed write would have nothing to invalidate for and
	// would just force one extra uncached render/sitemap build for no
	// reason.
	h.invalidate()

	if h.auditor != nil {
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionEmailCampaignArchiveUpdated,
			TargetType: audit.TargetEmailCampaign,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"from_status": fromStatus,
				// "new_status", not "to_status" -- the latter's "to"
				// token trips audit_email_metadata_guard_test.go's
				// (#0237) suspect-key scan (its own "to" IS a real
				// admin_campaign_preview.go recipient-address key, and
				// this guard cannot tell the two apart statically), even
				// though this value is one of "published"/"withheld",
				// never an address.
				"new_status": req.Status,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, toCampaignView(updated))
}
