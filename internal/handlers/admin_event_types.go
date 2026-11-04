// Admin event-type CRUD API (#0613; PRD §5.2, §8), modelled on interests.go.
// Choosing the default is a PATCH field, never a sub-route:
// cmd/opencircuit/admin_wiring_test.go sends every admin route an empty body
// with an admin session, and a bodyless POST would really move the default.
// No SEO cache is invalidated: nothing server-rendered contains a type yet.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/eventtypes"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// eventTypeStore is the behavior AdminEventTypesHandler needs from the data
// layer. *eventtypes.Store satisfies it.
type eventTypeStore interface {
	ListAll(ctx context.Context) ([]eventtypes.EventType, error)
	GetByID(ctx context.Context, id int64) (eventtypes.EventType, error)
	Create(ctx context.Context, slug, name string, sortOrder int) (eventtypes.EventType, error)
	Update(ctx context.Context, id int64, name string, sortOrder int, enabled bool) (eventtypes.EventType, error)
	SetDefault(ctx context.Context, id int64) error
	UsageCounts(ctx context.Context) (map[int64]int64, error)
	Delete(ctx context.Context, id int64) error
}

// AdminEventTypesHandler serves the admin-only event-type routes. They MUST be
// mounted behind middleware.RequireSession then middleware.RequireAdmin.
//
//	GET    /admin/event-types      — every type, with a per-type listing count
//	POST   /admin/event-types      — create a type
//	PATCH  /admin/event-types/{id} — name/sort_order/enabled, or is_default alone
//	DELETE /admin/event-types/{id} — refused (409) when in use or the default
type AdminEventTypesHandler struct {
	store   eventTypeStore
	auditor *audit.Logger
}

// NewAdminEventTypesHandler constructs the handler. A nil auditor disables
// audit writes.
func NewAdminEventTypesHandler(store eventTypeStore, auditor *audit.Logger) *AdminEventTypesHandler {
	return &AdminEventTypesHandler{store: store, auditor: auditor}
}

type eventTypeView struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	IsDefault     bool   `json:"is_default"`
	SortOrder     int    `json:"sort_order"`
	WorkshopCount int64  `json:"workshop_count"`
	CreatedAt     string `json:"created_at"`
}

func toEventTypeView(et eventtypes.EventType, counts map[int64]int64) eventTypeView {
	return eventTypeView{
		ID:            et.ID,
		Slug:          et.Slug,
		Name:          et.Name,
		Enabled:       et.Enabled,
		IsDefault:     et.IsDefault,
		SortOrder:     et.SortOrder,
		WorkshopCount: counts[et.ID],
		CreatedAt:     et.CreatedAt.UTC().Format(time.RFC3339),
	}
}

type eventTypesResponse struct {
	EventTypes []eventTypeView `json:"event_types"`
}

// List handles GET /admin/event-types.
func (h *AdminEventTypesHandler) List(w http.ResponseWriter, r *http.Request) {
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
	views := make([]eventTypeView, 0, len(all))
	for _, et := range all {
		views = append(views, toEventTypeView(et, counts))
	}
	writeJSON(w, http.StatusOK, eventTypesResponse{EventTypes: views})
}

type createEventTypeRequest struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order,omitempty"`
}

const eventTypeSlugMessage = "slug must be lowercase and hyphenated (e.g. \"hack-night\")"

// Create handles POST /admin/event-types.
func (h *AdminEventTypesHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req createEventTypeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !eventtypes.ValidSlug(req.Slug) {
		writeError(w, http.StatusBadRequest, eventTypeSlugMessage)
		return
	}
	created, err := h.store.Create(r.Context(), req.Slug, name, req.SortOrder)
	switch {
	case err == nil:
	case errors.Is(err, eventtypes.ErrInvalidSlug):
		writeError(w, http.StatusBadRequest, eventTypeSlugMessage)
		return
	case errors.Is(err, eventtypes.ErrDuplicateSlug):
		writeError(w, http.StatusConflict, "an event type with that slug already exists")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := created.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionEventTypeCreated,
			TargetType: audit.TargetEventType,
			TargetID:   &targetID,
			Metadata:   map[string]any{"slug": created.Slug, "name": created.Name, "sort_order": created.SortOrder},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusCreated, toEventTypeView(created, map[int64]int64{}))
}

// patchEventTypeRequest is the PATCH body. A nil field is left unchanged. Slug
// is json.RawMessage so that the key's mere presence is refused, whatever its
// value. IsDefault may only be true and only be sent alone, so a partial apply
// cannot happen.
type patchEventTypeRequest struct {
	Name      *string         `json:"name,omitempty"`
	SortOrder *int            `json:"sort_order,omitempty"`
	Enabled   *bool           `json:"enabled,omitempty"`
	IsDefault *bool           `json:"is_default,omitempty"`
	Slug      json.RawMessage `json:"slug,omitempty"`
}

// Patch handles PATCH /admin/event-types/{id}.
func (h *AdminEventTypesHandler) Patch(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseEventTypeID(w, r)
	if !ok {
		return
	}
	var req patchEventTypeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Slug != nil {
		writeError(w, http.StatusBadRequest, "an event type's slug cannot be changed")
		return
	}
	hasFields := req.Name != nil || req.SortOrder != nil || req.Enabled != nil
	if req.IsDefault == nil && !hasFields {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	if req.IsDefault != nil {
		if !*req.IsDefault {
			writeError(w, http.StatusBadRequest, "choose another type as the default instead")
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
	case errors.Is(err, eventtypes.ErrNotFound):
		writeError(w, http.StatusNotFound, "event type not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var updated eventtypes.EventType
	if req.IsDefault != nil {
		err = h.store.SetDefault(r.Context(), id)
		switch {
		case err == nil:
		case errors.Is(err, eventtypes.ErrNotFound):
			writeError(w, http.StatusNotFound, "event type not found")
			return
		case errors.Is(err, eventtypes.ErrDisabled):
			writeError(w, http.StatusConflict, "only an enabled event type can be the default; enable it first")
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
				Action:     audit.ActionEventTypeDefaultChanged,
				TargetType: audit.TargetEventType,
				TargetID:   &targetID,
				Metadata:   map[string]any{"slug": updated.Slug, "name": updated.Name},
				IP:         clientIP(r),
			})
		}
	} else {
		name := current.Name
		if req.Name != nil {
			name = strings.TrimSpace(*req.Name)
			if name == "" {
				writeError(w, http.StatusBadRequest, "name cannot be empty")
				return
			}
		}
		sortOrder := current.SortOrder
		if req.SortOrder != nil {
			sortOrder = *req.SortOrder
		}
		enabled := current.Enabled
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		updated, err = h.store.Update(r.Context(), id, name, sortOrder, enabled)
		switch {
		case err == nil:
		case errors.Is(err, eventtypes.ErrNotFound):
			writeError(w, http.StatusNotFound, "event type not found")
			return
		case errors.Is(err, eventtypes.ErrIsDefault):
			writeError(w, http.StatusConflict, "cannot disable the default event type; choose another default first")
			return
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		action := audit.ActionEventTypeUpdated
		switch {
		case current.Enabled && !updated.Enabled:
			action = audit.ActionEventTypeDisabled
		case !current.Enabled && updated.Enabled:
			action = audit.ActionEventTypeEnabled
		}
		if h.auditor != nil {
			actorID := actor.ID
			targetID := updated.ID
			h.auditor.Record(r.Context(), audit.Entry{
				ActorID:    &actorID,
				Action:     action,
				TargetType: audit.TargetEventType,
				TargetID:   &targetID,
				Metadata: map[string]any{
					"slug":           updated.Slug,
					"old_name":       current.Name,
					"new_name":       updated.Name,
					"old_sort_order": current.SortOrder,
					"new_sort_order": updated.SortOrder,
					"old_enabled":    current.Enabled,
					"new_enabled":    updated.Enabled,
				},
				IP: clientIP(r),
			})
		}
	}

	counts, err := h.store.UsageCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, toEventTypeView(updated, counts))
}

// Delete handles DELETE /admin/event-types/{id}.
func (h *AdminEventTypesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseEventTypeID(w, r)
	if !ok {
		return
	}
	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, eventtypes.ErrNotFound):
		writeError(w, http.StatusNotFound, "event type not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	err = h.store.Delete(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, eventtypes.ErrNotFound):
		writeError(w, http.StatusNotFound, "event type not found")
		return
	case errors.Is(err, eventtypes.ErrInUse):
		writeError(w, http.StatusConflict, "cannot delete an event type that listings still use; disable it instead")
		return
	case errors.Is(err, eventtypes.ErrIsDefault):
		writeError(w, http.StatusConflict, "cannot delete the default event type; choose another default first")
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
			Action:     audit.ActionEventTypeDeleted,
			TargetType: audit.TargetEventType,
			TargetID:   &targetID,
			Metadata:   map[string]any{"slug": current.Slug, "name": current.Name},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "event type deleted"})
}

func parseEventTypeID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid event type id")
		return 0, false
	}
	return id, true
}
