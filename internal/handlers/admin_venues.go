// Admin venue CRUD API (#0617; PRD §5.2, §8), modelled on admin_event_types.go.
// Choosing the default is a PATCH field, never a sub-route:
// cmd/opencircuit/admin_wiring_test.go sends every admin route an empty body with
// an admin session, and a bodyless POST would really move the default. Every
// successful write invalidates the SEO cache, since server-rendered pages carry a
// listing's venue name, address and note.
package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/defaults"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/venues"
)

// venueStore is the behavior AdminVenuesHandler needs from the data layer.
// *venues.Store satisfies it.
type venueStore interface {
	ListAll(ctx context.Context) ([]venues.Venue, error)
	GetByID(ctx context.Context, id int64) (venues.Venue, error)
	Create(ctx context.Context, in venues.CreateInput) (venues.Venue, error)
	Update(ctx context.Context, id int64, in venues.UpdateInput) (venues.Venue, error)
	SetDefault(ctx context.Context, id int64) error
	UsageCounts(ctx context.Context) (map[int64]int64, error)
	Delete(ctx context.Context, id int64) error
}

// AdminVenuesHandler serves the admin-only venue routes. They MUST be mounted
// behind middleware.RequireSession then middleware.RequireAdmin.
//
//	GET    /admin/venues      — every venue, with a per-venue listing count
//	POST   /admin/venues      — create a venue
//	PATCH  /admin/venues/{id} — name/address/note/sort_order/enabled, or is_default alone
//	DELETE /admin/venues/{id} — refused (409) when in use or the default
type AdminVenuesHandler struct {
	store   venueStore
	auditor *audit.Logger
	// invalidator is the shared *seo.Site; nil disables invalidation (tests).
	invalidator seoCacheInvalidator
}

// NewAdminVenuesHandler constructs the handler. A nil auditor disables audit
// writes, and a nil invalidator disables cache invalidation.
func NewAdminVenuesHandler(store venueStore, auditor *audit.Logger, invalidator seoCacheInvalidator) *AdminVenuesHandler {
	return &AdminVenuesHandler{store: store, auditor: auditor, invalidator: invalidator}
}

func (h *AdminVenuesHandler) invalidate() {
	if h.invalidator != nil {
		h.invalidator.Invalidate()
	}
}

type venueView struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Address       *string `json:"address"`
	Note          *string `json:"note"`
	Enabled       bool    `json:"enabled"`
	IsDefault     bool    `json:"is_default"`
	SortOrder     int     `json:"sort_order"`
	WorkshopCount int64   `json:"workshop_count"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func toVenueView(v venues.Venue, counts map[int64]int64) venueView {
	return venueView{
		ID:            v.ID,
		Name:          v.Name,
		Address:       v.Address,
		Note:          v.Note,
		Enabled:       v.Enabled,
		IsDefault:     v.IsDefault,
		SortOrder:     v.SortOrder,
		WorkshopCount: counts[v.ID],
		CreatedAt:     v.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     v.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

type venuesResponse struct {
	Venues []venueView `json:"venues"`
}

// List handles GET /admin/venues.
func (h *AdminVenuesHandler) List(w http.ResponseWriter, r *http.Request) {
	all, err := h.store.ListAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	counts, err := h.store.UsageCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views := make([]venueView, 0, len(all))
	hasDefault := false
	for _, v := range all {
		views = append(views, toVenueView(v, counts))
		hasDefault = hasDefault || v.IsDefault
	}
	// Still a 200: this list is how the admin reaches "Make default", the repair.
	if len(all) > 0 && !hasDefault {
		slog.Error("venues: no venue is the default; settings row is missing or invalid", "key", defaults.KeyVenue)
	}
	writeJSON(w, http.StatusOK, venuesResponse{Venues: views})
}

type createVenueRequest struct {
	Name      string  `json:"name"`
	Address   *string `json:"address,omitempty"`
	Note      *string `json:"note,omitempty"`
	SortOrder int     `json:"sort_order,omitempty"`
}

const noDefaultVenueMessage = "no default venue is set; choose one first"

// Create handles POST /admin/venues.
func (h *AdminVenuesHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req createVenueRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	created, err := h.store.Create(r.Context(), venues.CreateInput{
		Name:      name,
		Address:   normalizeOptionalCampaignField(req.Address),
		Note:      normalizeOptionalCampaignField(req.Note),
		SortOrder: req.SortOrder,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := created.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionVenueCreated,
			TargetType: audit.TargetVenue,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"name":       created.Name,
				"location":   created.Address,
				"note":       created.Note,
				"sort_order": created.SortOrder,
			},
			IP: clientIP(r),
		})
	}
	h.invalidate()
	writeJSON(w, http.StatusCreated, toVenueView(created, map[int64]int64{}))
}

// patchVenueRequest is the PATCH body. A nil field is left unchanged, and an
// explicit "" clears address or note. IsDefault may only be true and only be sent
// alone, so a partial apply cannot happen.
type patchVenueRequest struct {
	Name      *string `json:"name,omitempty"`
	Address   *string `json:"address,omitempty"`
	Note      *string `json:"note,omitempty"`
	SortOrder *int    `json:"sort_order,omitempty"`
	Enabled   *bool   `json:"enabled,omitempty"`
	IsDefault *bool   `json:"is_default,omitempty"`
}

// Patch handles PATCH /admin/venues/{id}.
func (h *AdminVenuesHandler) Patch(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseVenueID(w, r)
	if !ok {
		return
	}
	var req patchVenueRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	hasFields := req.Name != nil || req.Address != nil || req.Note != nil || req.SortOrder != nil || req.Enabled != nil
	if req.IsDefault == nil && !hasFields {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	if req.IsDefault != nil {
		if !*req.IsDefault {
			writeError(w, http.StatusBadRequest, "choose another venue as the default instead")
			return
		}
		if hasFields {
			writeError(w, http.StatusBadRequest, "is_default cannot be combined with other fields")
			return
		}
	}

	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, venues.ErrNotFound):
		writeError(w, http.StatusNotFound, "venue not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var updated venues.Venue
	if req.IsDefault != nil {
		err = h.store.SetDefault(r.Context(), id)
		switch {
		case err == nil:
		case errors.Is(err, venues.ErrNotFound):
			writeError(w, http.StatusNotFound, "venue not found")
			return
		case errors.Is(err, venues.ErrDisabled):
			writeError(w, http.StatusConflict, "only an enabled venue can be the default; enable it first")
			return
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		updated, err = h.store.GetByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if h.auditor != nil {
			actorID := actor.ID
			targetID := updated.ID
			h.auditor.Record(r.Context(), audit.Entry{
				ActorID:    &actorID,
				Action:     audit.ActionVenueDefaultChanged,
				TargetType: audit.TargetVenue,
				TargetID:   &targetID,
				Metadata:   map[string]any{"name": updated.Name},
				IP:         clientIP(r),
			})
		}
	} else {
		in := venues.UpdateInput{
			Name:      current.Name,
			Address:   current.Address,
			Note:      current.Note,
			SortOrder: current.SortOrder,
			Enabled:   current.Enabled,
		}
		if req.Name != nil {
			in.Name = strings.TrimSpace(*req.Name)
			if in.Name == "" {
				writeError(w, http.StatusBadRequest, "name cannot be empty")
				return
			}
		}
		if req.Address != nil {
			in.Address = normalizeOptionalCampaignField(req.Address)
		}
		if req.Note != nil {
			in.Note = normalizeOptionalCampaignField(req.Note)
		}
		if req.SortOrder != nil {
			in.SortOrder = *req.SortOrder
		}
		if req.Enabled != nil {
			in.Enabled = *req.Enabled
		}
		updated, err = h.store.Update(r.Context(), id, in)
		switch {
		case err == nil:
		case errors.Is(err, venues.ErrNotFound):
			writeError(w, http.StatusNotFound, "venue not found")
			return
		case errors.Is(err, venues.ErrIsDefault):
			writeError(w, http.StatusConflict, "cannot disable the default venue; choose another default first")
			return
		case errors.Is(err, defaults.ErrUnset):
			writeError(w, http.StatusConflict, noDefaultVenueMessage)
			return
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		action := audit.ActionVenueUpdated
		switch {
		case current.Enabled && !updated.Enabled:
			action = audit.ActionVenueDisabled
		case !current.Enabled && updated.Enabled:
			action = audit.ActionVenueEnabled
		}
		if h.auditor != nil {
			actorID := actor.ID
			targetID := updated.ID
			h.auditor.Record(r.Context(), audit.Entry{
				ActorID:    &actorID,
				Action:     action,
				TargetType: audit.TargetVenue,
				TargetID:   &targetID,
				Metadata: map[string]any{
					"old_name":       current.Name,
					"new_name":       updated.Name,
					"old_location":   current.Address,
					"new_location":   updated.Address,
					"old_note":       current.Note,
					"new_note":       updated.Note,
					"old_sort_order": current.SortOrder,
					"new_sort_order": updated.SortOrder,
					"old_enabled":    current.Enabled,
					"new_enabled":    updated.Enabled,
				},
				IP: clientIP(r),
			})
		}
	}

	h.invalidate()
	counts, err := h.store.UsageCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, toVenueView(updated, counts))
}

// Delete handles DELETE /admin/venues/{id}.
func (h *AdminVenuesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseVenueID(w, r)
	if !ok {
		return
	}
	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, venues.ErrNotFound):
		writeError(w, http.StatusNotFound, "venue not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	err = h.store.Delete(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, venues.ErrNotFound):
		writeError(w, http.StatusNotFound, "venue not found")
		return
	case errors.Is(err, venues.ErrInUse):
		writeError(w, http.StatusConflict, "cannot delete a venue that listings still use; disable it instead")
		return
	case errors.Is(err, venues.ErrIsDefault):
		writeError(w, http.StatusConflict, "cannot delete the default venue; choose another default first")
		return
	case errors.Is(err, defaults.ErrUnset):
		writeError(w, http.StatusConflict, noDefaultVenueMessage)
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := id
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionVenueDeleted,
			TargetType: audit.TargetVenue,
			TargetID:   &targetID,
			Metadata:   map[string]any{"name": current.Name},
			IP:         clientIP(r),
		})
	}
	h.invalidate()
	writeJSON(w, http.StatusOK, map[string]string{"message": "venue deleted"})
}

func parseVenueID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return 0, false
	}
	return id, true
}
