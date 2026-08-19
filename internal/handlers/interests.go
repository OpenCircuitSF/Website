package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// interestStore is the behavior the admin interests handler needs from the
// data layer. *interests.Store (#0023) satisfies it. Depending on an
// interface keeps the handler unit-testable with a fake, mirroring the
// AdminUsersHandler/SettingsHandler pattern.
type interestStore interface {
	ListAll(ctx context.Context) ([]interests.Interest, error)
	GetByID(ctx context.Context, id int64) (interests.Interest, error)
	Create(ctx context.Context, slug, name string, description *string, sortOrder int) (interests.Interest, error)
	Update(ctx context.Context, id int64, name string, description *string, sortOrder int, active bool) (interests.Interest, error)
	Delete(ctx context.Context, id int64) error
	SubscriberCounts(ctx context.Context) (map[int64]int64, error)
}

// AdminInterestsHandler serves the admin-only interest-taxonomy CRUD routes
// (PRD §5.2, §6.1; #0024):
//
//	GET    /admin/interests      — list every interest (active + inactive), with a per-interest subscriber count
//	POST   /admin/interests      — create a new interest
//	PATCH  /admin/interests/{id} — update name/description/sort_order/active (slug is immutable — see patchInterestRequest)
//	DELETE /admin/interests/{id} — hard-delete, refused (409) when any subscriber is associated
//
// All routes MUST be mounted behind middleware.RequireSession then
// middleware.RequireAdmin, exactly like AdminUsersHandler and
// SettingsHandler: RequireSession attaches the AuthUser and answers 401 for
// an absent/invalid session, RequireAdmin answers 403 for a non-admin. The
// handler re-reads the user from the context only to attribute audit
// entries; it does not re-check admin.
type AdminInterestsHandler struct {
	store interestStore
	// auditor records interest.created / .updated / .deactivated /
	// .reactivated / .deleted entries. May be nil in unit tests that do not
	// assert audit rows.
	auditor *audit.Logger
}

// NewAdminInterestsHandler constructs an AdminInterestsHandler over the data
// layer. A nil auditor disables audit writes.
func NewAdminInterestsHandler(store interestStore, auditor *audit.Logger) *AdminInterestsHandler {
	return &AdminInterestsHandler{store: store, auditor: auditor}
}

// interestView is the JSON shape for one interests row, extended with the
// per-interest subscriber count (#0024's acceptance criterion: "the number
// that makes this screen useful — it is what tells the operator whether a
// segment is worth a campaign"). description is omitted when NULL.
type interestView struct {
	ID              int64   `json:"id"`
	Slug            string  `json:"slug"`
	Name            string  `json:"name"`
	Description     *string `json:"description,omitempty"`
	SortOrder       int     `json:"sort_order"`
	Active          bool    `json:"active"`
	SubscriberCount int64   `json:"subscriber_count"`
	CreatedAt       string  `json:"created_at"`
}

func toInterestView(it interests.Interest, counts map[int64]int64) interestView {
	return interestView{
		ID:              it.ID,
		Slug:            it.Slug,
		Name:            it.Name,
		Description:     it.Description,
		SortOrder:       it.SortOrder,
		Active:          it.Active,
		SubscriberCount: counts[it.ID],
		CreatedAt:       it.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// interestsResponse is the GET /admin/interests body: {"interests":[{...}]}.
// The list is always present (never null) so the client gets a stable shape.
type interestsResponse struct {
	Interests []interestView `json:"interests"`
}

// List handles GET /admin/interests. Returns every interest — active and
// inactive — ordered by sort_order then name (interests.Store.ListAll),
// each carrying its subscriber_count. Admin-only via the middleware chain.
func (h *AdminInterestsHandler) List(w http.ResponseWriter, r *http.Request) {
	views, err := h.listViews(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, interestsResponse{Interests: views})
}

// listViews loads every interest plus subscriber counts and maps them to the
// response shape. Shared by List and by the mutation handlers below, which
// return it too (via reload) so the SPA does not need a second round trip.
func (h *AdminInterestsHandler) listViews(ctx context.Context) ([]interestView, error) {
	all, err := h.store.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := h.store.SubscriberCounts(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]interestView, 0, len(all))
	for _, it := range all {
		views = append(views, toInterestView(it, counts))
	}
	return views, nil
}

// createInterestRequest is the POST /admin/interests body. slug and name are
// required; description and sort_order are optional (sort_order defaults to
// 0, description to NULL/omitted).
type createInterestRequest struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	SortOrder   int     `json:"sort_order,omitempty"`
}

// Create handles POST /admin/interests. Validates slug format
// (interests.ValidSlug — same rule as the interests_slug_format CHECK) and
// uniqueness, and that name is non-empty, before inserting. Writes
// interest.created on success and returns the new row with 201.
func (h *AdminInterestsHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		// Unreachable behind RequireSession+RequireAdmin, but guard so the
		// handler never panics if mounted without the chain.
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	var req createInterestRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !interests.ValidSlug(req.Slug) {
		writeError(w, http.StatusBadRequest, "slug must be lowercase and hyphenated (e.g. \"home-automation\")")
		return
	}

	created, err := h.store.Create(r.Context(), req.Slug, req.Name, req.Description, req.SortOrder)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, interests.ErrInvalidSlug):
		writeError(w, http.StatusBadRequest, "slug must be lowercase and hyphenated (e.g. \"home-automation\")")
		return
	case errors.Is(err, interests.ErrDuplicateSlug):
		writeError(w, http.StatusConflict, "an interest with that slug already exists")
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
			Action:     audit.ActionInterestCreated,
			TargetType: audit.TargetInterest,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"slug":       created.Slug,
				"name":       created.Name,
				"sort_order": created.SortOrder,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusCreated, toInterestView(created, map[int64]int64{}))
}

// patchInterestRequest is the PATCH /admin/interests/{id} body. Every field
// is optional (a nil pointer leaves that field unchanged); any field present
// replaces the current value outright. There is deliberately NO slug field:
// interests.Store.Update takes no slug parameter (#0023's decision, "renaming
// a slug would break any already-issued preference-center link that
// references it by slug"), and decodeJSON's DisallowUnknownFields means a
// client that sends "slug" gets a 400 rather than having it silently
// ignored — the immutability is enforced, not just undocumented. See this
// issue's Gotchas for why #0024 chose not to add a rename path.
//
// Deactivating/reactivating is just PATCHing `active` — there is no separate
// route. The handler still writes a dedicated interest.deactivated /
// interest.reactivated audit action (not a generic interest.updated) when
// that specific field flips, because "this interest just left/rejoined the
// signup form" is the detail an operator scanning the log needs at a
// glance, mirroring AdminUsersHandler's account.deactivated/reactivated.
type patchInterestRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	SortOrder   *int    `json:"sort_order,omitempty"`
	Active      *bool   `json:"active,omitempty"`
}

// Patch handles PATCH /admin/interests/{id}. Loads the current row, merges
// the provided fields onto it, and calls Update with the full merged value
// (Update itself takes every field, not a partial patch). Returns the
// updated row with 200, or 404 if no such interest exists.
func (h *AdminInterestsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseInterestID(w, r)
	if !ok {
		return
	}

	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, interests.ErrNotFound):
		writeError(w, http.StatusNotFound, "interest not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var req patchInterestRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := current.Name
	if req.Name != nil {
		if *req.Name == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		name = *req.Name
	}
	description := current.Description
	if req.Description != nil {
		// An explicit empty string clears the description (stores NULL); any
		// other provided value replaces it outright.
		if *req.Description == "" {
			description = nil
		} else {
			description = req.Description
		}
	}
	sortOrder := current.SortOrder
	if req.SortOrder != nil {
		sortOrder = *req.SortOrder
	}
	wasActive := current.Active
	active := current.Active
	if req.Active != nil {
		active = *req.Active
	}

	updated, err := h.store.Update(r.Context(), id, name, description, sortOrder, active)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, interests.ErrNotFound):
		writeError(w, http.StatusNotFound, "interest not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		action := audit.ActionInterestUpdated
		switch {
		case wasActive && !updated.Active:
			action = audit.ActionInterestDeactivated
		case !wasActive && updated.Active:
			action = audit.ActionInterestReactivated
		}
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     action,
			TargetType: audit.TargetInterest,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"slug":           updated.Slug,
				"old_name":       current.Name,
				"new_name":       updated.Name,
				"old_sort_order": current.SortOrder,
				"new_sort_order": updated.SortOrder,
				"old_active":     wasActive,
				"new_active":     updated.Active,
			},
			IP: clientIP(r),
		})
	}

	counts, err := h.store.SubscriberCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, toInterestView(updated, counts))
}

// Delete handles DELETE /admin/interests/{id}. Refuses with 409 and a clear
// message when the interest has ever been selected by a subscriber
// (interests.ErrHasSubscribers) — the acceptance criterion's "deactivate
// instead" guidance is put directly in the response body, not just implied
// by the status code. Writes interest.deleted only on an actual deletion.
func (h *AdminInterestsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseInterestID(w, r)
	if !ok {
		return
	}

	// Read first so the audit metadata can record what was deleted (the slug
	// is otherwise gone once Delete succeeds) and so a 404 vs 409 is
	// unambiguous — Delete's own error already distinguishes them, this is
	// just for the metadata.
	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, interests.ErrNotFound):
		writeError(w, http.StatusNotFound, "interest not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	err = h.store.Delete(r.Context(), id)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, interests.ErrNotFound):
		writeError(w, http.StatusNotFound, "interest not found")
		return
	case errors.Is(err, interests.ErrHasSubscribers):
		writeError(w, http.StatusConflict,
			"cannot delete an interest with associated subscribers; deactivate it instead")
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
			Action:     audit.ActionInterestDeleted,
			TargetType: audit.TargetInterest,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"slug": current.Slug,
				"name": current.Name,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "interest deleted"})
}

// parseInterestID reads and validates the {id} path value, writing a 400 and
// returning ok=false on a missing/invalid id.
func parseInterestID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid interest id")
		return 0, false
	}
	return id, true
}
