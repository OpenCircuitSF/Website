package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/crt"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// crtCommandStore is the behavior the admin CRT-commands handler needs from
// the data layer. *crt.Store satisfies it. Depending on an interface keeps
// the handler unit-testable with a fake, mirroring interestStore's own
// convention in interests.go.
type crtCommandStore interface {
	List(ctx context.Context) ([]crt.Command, error)
	GetByID(ctx context.Context, id int64) (crt.Command, error)
	Create(ctx context.Context, slug, command, output, source string, sortOrder int) (crt.Command, error)
	Update(ctx context.Context, id int64, command, output, source string, sortOrder int, active bool) (crt.Command, error)
	Delete(ctx context.Context, id int64) error
}

// AdminCrtCommandsHandler serves the admin-only CRT-session CRUD routes
// (#0393's Design §2):
//
//	GET    /admin/crt-commands      — list every row, active and inactive
//	POST   /admin/crt-commands      — create a new row
//	PATCH  /admin/crt-commands/{id} — update command/output/source/sort_order/active (slug is immutable)
//	DELETE /admin/crt-commands/{id} — hard-delete (unconditional — see crt.Store.Delete's doc comment)
//
// All routes MUST be mounted behind middleware.RequireSession then
// middleware.RequireAdmin, exactly like AdminInterestsHandler. The handler
// re-reads the user from the context only to attribute audit entries; it
// does not re-check admin.
type AdminCrtCommandsHandler struct {
	store crtCommandStore
	// auditor records crt_command.created / .updated / .deleted entries. May
	// be nil in unit tests that do not assert audit rows.
	auditor *audit.Logger
}

// NewAdminCrtCommandsHandler constructs an AdminCrtCommandsHandler over the
// data layer. A nil auditor disables audit writes.
func NewAdminCrtCommandsHandler(store crtCommandStore, auditor *audit.Logger) *AdminCrtCommandsHandler {
	return &AdminCrtCommandsHandler{store: store, auditor: auditor}
}

// crtCommandView is the admin JSON shape for one crt_commands row. output is
// the raw newline-separated string (not split into lines) — the admin
// screen's editor is a plain <textarea> bound directly to it (#0393's
// Design §4), so there is nothing to gain from splitting it server-side only
// to have the client join it back together.
type crtCommandView struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Command   string `json:"command"`
	Output    string `json:"output"`
	Source    string `json:"source"`
	SortOrder int    `json:"sort_order"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func toCrtCommandView(c crt.Command) crtCommandView {
	view := crtCommandView{
		ID:        c.ID,
		Slug:      c.Slug,
		Command:   c.Command,
		Output:    c.Output,
		Source:    c.Source,
		SortOrder: c.SortOrder,
		Active:    c.Active,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
	}
	if c.UpdatedAt != nil {
		view.UpdatedAt = c.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return view
}

// crtCommandsResponse is the GET /admin/crt-commands body:
// {"commands":[{...}]}. The list is always present (never null), matching
// interestsResponse's convention.
type crtCommandsResponse struct {
	Commands []crtCommandView `json:"commands"`
}

// List handles GET /admin/crt-commands. Returns every row — active and
// inactive — ordered by sort_order then slug (crt.Store.List). Admin-only
// via the middleware chain.
func (h *AdminCrtCommandsHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views := make([]crtCommandView, 0, len(rows))
	for _, c := range rows {
		views = append(views, toCrtCommandView(c))
	}
	writeJSON(w, http.StatusOK, crtCommandsResponse{Commands: views})
}

// createCrtCommandRequest is the POST /admin/crt-commands body. slug,
// command, and output are required; source defaults to "static" when
// omitted; sort_order defaults to 0.
type createCrtCommandRequest struct {
	Slug      string `json:"slug"`
	Command   string `json:"command"`
	Output    string `json:"output"`
	Source    string `json:"source,omitempty"`
	SortOrder int    `json:"sort_order,omitempty"`
}

// Create handles POST /admin/crt-commands. Validates slug format
// (crt.ValidSlug), a non-empty command and output, and source (crt.ValidSource
// when provided; defaults to crt.SourceStatic otherwise) before inserting.
// Writes crt_command.created on success and returns the new row with 201.
func (h *AdminCrtCommandsHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		// Unreachable behind RequireSession+RequireAdmin, but guard so the
		// handler never panics if mounted without the chain.
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	var req createCrtCommandRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Command == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}
	if req.Output == "" {
		writeError(w, http.StatusBadRequest, "output is required")
		return
	}
	if !crt.ValidSlug(req.Slug) {
		writeError(w, http.StatusBadRequest, "slug must be lowercase and hyphenated (e.g. \"workshops-next\")")
		return
	}
	source := req.Source
	if source == "" {
		source = crt.SourceStatic
	}
	if !crt.ValidSource(source) {
		writeError(w, http.StatusBadRequest, "source must be one of static, workshops, list_stats, interests")
		return
	}

	created, err := h.store.Create(r.Context(), req.Slug, req.Command, req.Output, source, req.SortOrder)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, crt.ErrInvalidSlug):
		writeError(w, http.StatusBadRequest, "slug must be lowercase and hyphenated (e.g. \"workshops-next\")")
		return
	case errors.Is(err, crt.ErrInvalidSource):
		writeError(w, http.StatusBadRequest, "source must be one of static, workshops, list_stats, interests")
		return
	case errors.Is(err, crt.ErrDuplicateSlug):
		writeError(w, http.StatusConflict, "a command with that slug already exists")
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
			Action:     audit.ActionCrtCommandCreated,
			TargetType: audit.TargetCrtCommand,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"slug":       created.Slug,
				"command":    created.Command,
				"source":     created.Source,
				"sort_order": created.SortOrder,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusCreated, toCrtCommandView(created))
}

// patchCrtCommandRequest is the PATCH /admin/crt-commands/{id} body. Every
// field is optional (a nil pointer leaves that field unchanged). There is
// deliberately NO slug field — crt.Store.Update takes no slug parameter, and
// decodeJSON's DisallowUnknownFields means a client that sends "slug" gets a
// 400 rather than having it silently ignored, mirroring
// patchInterestRequest's own immutable-slug convention.
type patchCrtCommandRequest struct {
	Command   *string `json:"command,omitempty"`
	Output    *string `json:"output,omitempty"`
	Source    *string `json:"source,omitempty"`
	SortOrder *int    `json:"sort_order,omitempty"`
	Active    *bool   `json:"active,omitempty"`
}

// Patch handles PATCH /admin/crt-commands/{id}. Loads the current row,
// merges the provided fields onto it, and calls Update with the full merged
// value. Returns the updated row with 200, or 404 if no such row exists.
// This is also how the admin screen reorders two rows: two PATCH calls
// swapping sort_order, the same pattern the interests admin screen already
// uses (web/src/lib/admin.ts's reorderSwap) — there is no separate reorder
// route.
func (h *AdminCrtCommandsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseCrtCommandID(w, r)
	if !ok {
		return
	}

	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, crt.ErrNotFound):
		writeError(w, http.StatusNotFound, "command not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var req patchCrtCommandRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	command := current.Command
	if req.Command != nil {
		if *req.Command == "" {
			writeError(w, http.StatusBadRequest, "command cannot be empty")
			return
		}
		command = *req.Command
	}
	output := current.Output
	if req.Output != nil {
		if *req.Output == "" {
			writeError(w, http.StatusBadRequest, "output cannot be empty")
			return
		}
		output = *req.Output
	}
	source := current.Source
	if req.Source != nil {
		source = *req.Source
	}
	if !crt.ValidSource(source) {
		writeError(w, http.StatusBadRequest, "source must be one of static, workshops, list_stats, interests")
		return
	}
	sortOrder := current.SortOrder
	if req.SortOrder != nil {
		sortOrder = *req.SortOrder
	}
	active := current.Active
	if req.Active != nil {
		active = *req.Active
	}

	updated, err := h.store.Update(r.Context(), id, command, output, source, sortOrder, active)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, crt.ErrNotFound):
		writeError(w, http.StatusNotFound, "command not found")
		return
	case errors.Is(err, crt.ErrInvalidSource):
		writeError(w, http.StatusBadRequest, "source must be one of static, workshops, list_stats, interests")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionCrtCommandUpdated,
			TargetType: audit.TargetCrtCommand,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"slug":           updated.Slug,
				"old_command":    current.Command,
				"new_command":    updated.Command,
				"old_source":     current.Source,
				"new_source":     updated.Source,
				"old_sort_order": current.SortOrder,
				"new_sort_order": updated.SortOrder,
				"old_active":     current.Active,
				"new_active":     updated.Active,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, toCrtCommandView(updated))
}

// Delete handles DELETE /admin/crt-commands/{id}. Unlike
// AdminInterestsHandler.Delete, there is no refused-delete case: crt.Store's
// row carries no history other code depends on, so every successful DELETE
// writes crt_command.deleted.
func (h *AdminCrtCommandsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseCrtCommandID(w, r)
	if !ok {
		return
	}

	// Read first so the audit metadata can record what was deleted (the slug
	// is otherwise gone once Delete succeeds), mirroring
	// AdminInterestsHandler.Delete's own convention.
	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, crt.ErrNotFound):
		writeError(w, http.StatusNotFound, "command not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if err := h.store.Delete(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, crt.ErrNotFound):
			writeError(w, http.StatusNotFound, "command not found")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := id
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionCrtCommandDeleted,
			TargetType: audit.TargetCrtCommand,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"slug":    current.Slug,
				"command": current.Command,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "command deleted"})
}

// parseCrtCommandID reads and validates the {id} path value, writing a 400
// and returning ok=false on a missing/invalid id. Mirrors
// parseInterestID.
func parseCrtCommandID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid command id")
		return 0, false
	}
	return id, true
}
