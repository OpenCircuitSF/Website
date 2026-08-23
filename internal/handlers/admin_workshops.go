// Admin workshop CRUD API (#0051; PRD §5.2, §6.2, §8): create, edit, list,
// and delete workshops. Backed by internal/workshops.Store.
//
// # Why PATCH alone drives every status transition
//
// Unlike AdminCampaignsHandler, PRD §8's route table gives workshops no
// dedicated /publish, /unpublish, or /cancel sub-routes — only
// `GET/POST/PATCH/DELETE /admin/workshops[/{id}]`. Patch therefore accepts an
// optional `status` field and is the sole path to published/canceled; see
// internal/workshops/store.go's package doc comment for exactly what each
// transition does to published_at. Create never accepts status — every new
// workshop starts in 'draft' (workshops.Store.Create's own contract).
//
// # Cache invalidation (#0051's own acceptance criterion, and carried in
// from #0073's review, binding on this issue)
//
// Every mutation (Create, Patch, Delete) calls invalidator.InvalidateWorkshops()
// AFTER the store write commits, so a stale title/summary/cover-image never
// lingers in internal/seo's per-path meta cache or the sitemap past the
// mutation itself — #0073's review made this explicit and testable
// (issues/0051.md's carried-in section). A nil invalidator (test-only; every
// production call site in cmd/opencircuit/main.go passes a real *seo.Site)
// simply skips the call — *seo.Site.InvalidateWorkshops is safe to call even
// while its WorkshopSource is nil (it only clears caches, see
// internal/seo/seo.go/sitemap.go), so production always passes the real
// *seo.Site regardless of whether the real WorkshopSource has been wired in
// yet (#0054's job — see internal/seo/workshop.go's doc comment).
package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// workshopStore is the behavior AdminWorkshopsHandler needs from the data
// layer. *workshops.Store (#0051) satisfies it.
type workshopStore interface {
	List(ctx context.Context) ([]workshops.Workshop, error)
	GetByID(ctx context.Context, id int64) (workshops.Workshop, error)
	Create(ctx context.Context, in workshops.CreateInput) (workshops.Workshop, error)
	Update(ctx context.Context, id int64, in workshops.UpdateInput) (workshops.Workshop, error)
	Delete(ctx context.Context, id int64) error
}

// workshopCacheInvalidator is the narrow seam over *seo.Site's
// InvalidateWorkshops method — a local interface (rather than importing
// internal/seo directly) so this package's tests can supply a fake that
// counts calls without constructing a real Site. *seo.Site satisfies it.
type workshopCacheInvalidator interface {
	InvalidateWorkshops()
}

// AdminWorkshopsHandler serves the admin-only workshop CRUD routes (PRD
// §5.2/§6.2/§8; #0051):
//
//	GET    /admin/workshops      — list every workshop (any status), newest-created first
//	POST   /admin/workshops      — create a new draft workshop (server-generated slug)
//	GET    /admin/workshops/{id} — one workshop, with its interest_ids
//	PATCH  /admin/workshops/{id} — content edit and/or status transition (see doc comment above)
//	DELETE /admin/workshops/{id} — hard delete, refused (409) when an email_campaign still references it
//
// All routes MUST be mounted behind middleware.RequireSession then
// middleware.RequireAdmin, exactly like every other admin handler — see
// cmd/opencircuit/main.go's adminRoutes.
type AdminWorkshopsHandler struct {
	store       workshopStore
	invalidator workshopCacheInvalidator // nil disables cache invalidation (test-only — see doc comment above)
	auditor     *audit.Logger
}

// NewAdminWorkshopsHandler constructs an AdminWorkshopsHandler. A nil
// auditor disables audit writes (matching every other admin handler's
// nil-tolerance); a nil invalidator disables the SEO/sitemap cache clear.
// cmd/opencircuit/main.go's servePostgres path currently passes nil for the
// invalidator too — a deliberate, safe-by-construction deferral (#0051's
// review Ruling 1: the nil is never dereferenced, and the caches it would
// invalidate hold no workshop-derived bytes yet either way) that #0054 is
// bound to replace with a real *seo.Site.
func NewAdminWorkshopsHandler(store workshopStore, invalidator workshopCacheInvalidator, auditor *audit.Logger) *AdminWorkshopsHandler {
	return &AdminWorkshopsHandler{store: store, invalidator: invalidator, auditor: auditor}
}

func (h *AdminWorkshopsHandler) invalidate() {
	if h.invalidator != nil {
		h.invalidator.InvalidateWorkshops()
	}
}

// ── Response shapes ──────────────────────────────────────────────────────────

type workshopView struct {
	ID              int64   `json:"id"`
	Slug            string  `json:"slug"`
	Title           string  `json:"title"`
	Summary         *string `json:"summary,omitempty"`
	BodyMD          *string `json:"body_md,omitempty"`
	StartsAt        *string `json:"starts_at,omitempty"`
	EndsAt          *string `json:"ends_at,omitempty"`
	LocationName    *string `json:"location_name,omitempty"`
	LocationAddress *string `json:"location_address,omitempty"`
	LocationNote    *string `json:"location_note,omitempty"`
	Capacity        *int    `json:"capacity,omitempty"`
	SignupURL       *string `json:"signup_url,omitempty"`
	CoverImage      *string `json:"cover_image,omitempty"`
	Status          string  `json:"status"`
	PublishedAt     *string `json:"published_at,omitempty"`
	InterestIDs     []int64 `json:"interest_ids"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

func toWorkshopView(w workshops.Workshop) workshopView {
	ids := w.InterestIDs
	if ids == nil {
		ids = []int64{}
	}
	return workshopView{
		ID:              w.ID,
		Slug:            w.Slug,
		Title:           w.Title,
		Summary:         w.Summary,
		BodyMD:          w.BodyMD,
		StartsAt:        formatTimePtr(w.StartsAt),
		EndsAt:          formatTimePtr(w.EndsAt),
		LocationName:    w.LocationName,
		LocationAddress: w.LocationAddress,
		LocationNote:    w.LocationNote,
		Capacity:        w.Capacity,
		SignupURL:       w.SignupURL,
		CoverImage:      w.CoverImage,
		Status:          w.Status,
		PublishedAt:     formatTimePtr(w.PublishedAt),
		InterestIDs:     ids,
		CreatedAt:       w.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:       w.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

type workshopsListResponse struct {
	Workshops []workshopView `json:"workshops"`
}

// ── GET /admin/workshops ─────────────────────────────────────────────────────

func (h *AdminWorkshopsHandler) List(w http.ResponseWriter, r *http.Request) {
	all, err := h.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views := make([]workshopView, 0, len(all))
	for _, wk := range all {
		views = append(views, toWorkshopView(wk))
	}
	writeJSON(w, http.StatusOK, workshopsListResponse{Workshops: views})
}

// ── GET /admin/workshops/{id} ────────────────────────────────────────────────

func (h *AdminWorkshopsHandler) Get(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, toWorkshopView(wk))
}

// ── POST /admin/workshops ────────────────────────────────────────────────────

type createWorkshopRequest struct {
	Title           string  `json:"title"`
	Summary         *string `json:"summary,omitempty"`
	BodyMD          *string `json:"body_md,omitempty"`
	StartsAt        *string `json:"starts_at,omitempty"`
	EndsAt          *string `json:"ends_at,omitempty"`
	LocationName    *string `json:"location_name,omitempty"`
	LocationAddress *string `json:"location_address,omitempty"`
	LocationNote    *string `json:"location_note,omitempty"`
	Capacity        *int    `json:"capacity,omitempty"`
	SignupURL       *string `json:"signup_url,omitempty"`
	CoverImage      *string `json:"cover_image,omitempty"`
	InterestIDs     []int64 `json:"interest_ids,omitempty"`
}

// Create handles POST /admin/workshops. Always creates in status='draft'
// (workshops.Store.Create's own contract); the slug is server-generated from
// title (never client-supplied — see internal/workshops/store.go's doc
// comment for the collision-suffix algorithm), so any "slug" field in the
// request body is simply ignored rather than round-tripped, matching
// decodeJSON's general DisallowUnknownFields posture for OTHER unrecognized
// fields elsewhere in this package (this one is recognized-but-unsettable,
// intentionally not rejected, since a client naively echoing the view back
// as a create payload shouldn't 400 on its own read-only field).
func (h *AdminWorkshopsHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	var req createWorkshopRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	startsAt, ok := parseOptionalTime(w, req.StartsAt, "starts_at")
	if !ok {
		return
	}
	endsAt, ok := parseOptionalTime(w, req.EndsAt, "ends_at")
	if !ok {
		return
	}

	created, err := h.store.Create(r.Context(), workshops.CreateInput{
		Title:           title,
		Summary:         normalizeOptionalCampaignField(req.Summary),
		BodyMD:          normalizeOptionalCampaignField(req.BodyMD),
		StartsAt:        startsAt,
		EndsAt:          endsAt,
		LocationName:    normalizeOptionalCampaignField(req.LocationName),
		LocationAddress: normalizeOptionalCampaignField(req.LocationAddress),
		LocationNote:    normalizeOptionalCampaignField(req.LocationNote),
		Capacity:        req.Capacity,
		SignupURL:       normalizeOptionalCampaignField(req.SignupURL),
		CoverImage:      normalizeOptionalCampaignField(req.CoverImage),
		InterestIDs:     req.InterestIDs,
	})
	switch {
	case err == nil:
	case errors.Is(err, workshops.ErrTitleRequired):
		writeError(w, http.StatusBadRequest, "title is required")
		return
	case errors.Is(err, workshops.ErrInterestNotFound):
		writeError(w, http.StatusBadRequest, "one or more interest ids do not exist")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	h.invalidate()

	if h.auditor != nil {
		actorID := actor.ID
		targetID := created.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionWorkshopCreated,
			TargetType: audit.TargetWorkshop,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"slug":         created.Slug,
				"title":        created.Title,
				"interest_ids": created.InterestIDs,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusCreated, toWorkshopView(created))
}

// ── PATCH /admin/workshops/{id} ──────────────────────────────────────────────

// patchWorkshopRequest is the PATCH /admin/workshops/{id} body. Every field
// is optional; a nil pointer leaves that field unchanged. There is
// deliberately NO slug field — slug is immutable through this route (same
// reasoning as AdminInterestsHandler.Patch: a workshop's slug may already be
// shared or archived elsewhere). Status IS present here, unlike
// patchCampaignRequest — see this file's package doc comment for why
// workshops has no dedicated transition routes.
type patchWorkshopRequest struct {
	Title           *string  `json:"title,omitempty"`
	Summary         *string  `json:"summary,omitempty"`
	BodyMD          *string  `json:"body_md,omitempty"`
	StartsAt        *string  `json:"starts_at,omitempty"`
	EndsAt          *string  `json:"ends_at,omitempty"`
	LocationName    *string  `json:"location_name,omitempty"`
	LocationAddress *string  `json:"location_address,omitempty"`
	LocationNote    *string  `json:"location_note,omitempty"`
	Capacity        *int     `json:"capacity,omitempty"`
	SignupURL       *string  `json:"signup_url,omitempty"`
	CoverImage      *string  `json:"cover_image,omitempty"`
	Status          *string  `json:"status,omitempty"`
	InterestIDs     *[]int64 `json:"interest_ids,omitempty"`
}

// Patch handles PATCH /admin/workshops/{id}: loads the current row, merges
// the provided fields (including an optional status transition) onto it,
// and calls Update with the full merged value — mirrors
// AdminInterestsHandler.Patch / AdminCampaignsHandler.Patch's shared
// merge-then-call-Update shape.
func (h *AdminWorkshopsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseWorkshopID(w, r)
	if !ok {
		return
	}

	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, workshops.ErrNotFound):
		writeError(w, http.StatusNotFound, "workshop not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var req patchWorkshopRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	title := current.Title
	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "title cannot be empty")
			return
		}
		title = trimmed
	}
	summary := current.Summary
	if req.Summary != nil {
		summary = normalizeOptionalCampaignField(req.Summary)
	}
	bodyMD := current.BodyMD
	if req.BodyMD != nil {
		bodyMD = normalizeOptionalCampaignField(req.BodyMD)
	}
	startsAt := current.StartsAt
	if req.StartsAt != nil {
		parsed, ok := parseOptionalTime(w, req.StartsAt, "starts_at")
		if !ok {
			return
		}
		startsAt = parsed
	}
	endsAt := current.EndsAt
	if req.EndsAt != nil {
		parsed, ok := parseOptionalTime(w, req.EndsAt, "ends_at")
		if !ok {
			return
		}
		endsAt = parsed
	}
	locationName := current.LocationName
	if req.LocationName != nil {
		locationName = normalizeOptionalCampaignField(req.LocationName)
	}
	locationAddress := current.LocationAddress
	if req.LocationAddress != nil {
		locationAddress = normalizeOptionalCampaignField(req.LocationAddress)
	}
	locationNote := current.LocationNote
	if req.LocationNote != nil {
		locationNote = normalizeOptionalCampaignField(req.LocationNote)
	}
	capacity := current.Capacity
	if req.Capacity != nil {
		capacity = req.Capacity
	}
	signupURL := current.SignupURL
	if req.SignupURL != nil {
		signupURL = normalizeOptionalCampaignField(req.SignupURL)
	}
	coverImage := current.CoverImage
	if req.CoverImage != nil {
		coverImage = normalizeOptionalCampaignField(req.CoverImage)
	}
	status := current.Status
	if req.Status != nil {
		status = strings.TrimSpace(*req.Status)
	}
	interestIDs := current.InterestIDs
	if req.InterestIDs != nil {
		interestIDs = *req.InterestIDs
	}

	updated, err := h.store.Update(r.Context(), id, workshops.UpdateInput{
		Title:           title,
		Summary:         summary,
		BodyMD:          bodyMD,
		StartsAt:        startsAt,
		EndsAt:          endsAt,
		LocationName:    locationName,
		LocationAddress: locationAddress,
		LocationNote:    locationNote,
		Capacity:        capacity,
		SignupURL:       signupURL,
		CoverImage:      coverImage,
		Status:          status,
		InterestIDs:     interestIDs,
	})
	switch {
	case err == nil:
	case errors.Is(err, workshops.ErrNotFound):
		writeError(w, http.StatusNotFound, "workshop not found")
		return
	case errors.Is(err, workshops.ErrTitleRequired):
		writeError(w, http.StatusBadRequest, "title cannot be empty")
		return
	case errors.Is(err, workshops.ErrUnknownStatus):
		writeError(w, http.StatusBadRequest, "status must be one of draft, published, canceled")
		return
	case errors.Is(err, workshops.ErrInterestNotFound):
		writeError(w, http.StatusBadRequest, "one or more interest ids do not exist")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	h.invalidate()

	if h.auditor != nil {
		actorID := actor.ID
		targetID := updated.ID
		action := audit.ActionWorkshopUpdated
		switch {
		case current.Status != workshops.StatusPublished && updated.Status == workshops.StatusPublished:
			action = audit.ActionWorkshopPublished
		case current.Status == workshops.StatusPublished && updated.Status == workshops.StatusDraft:
			action = audit.ActionWorkshopUnpublished
		case current.Status != workshops.StatusCanceled && updated.Status == workshops.StatusCanceled:
			action = audit.ActionWorkshopCanceled
		}
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     action,
			TargetType: audit.TargetWorkshop,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"slug":             updated.Slug,
				"old_status":       current.Status,
				"new_status":       updated.Status,
				"old_title":        current.Title,
				"new_title":        updated.Title,
				"old_interest_ids": current.InterestIDs,
				"new_interest_ids": updated.InterestIDs,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, toWorkshopView(updated))
}

// ── DELETE /admin/workshops/{id} ─────────────────────────────────────────────

// Delete handles DELETE /admin/workshops/{id}. Refuses with 409 and a clear
// message when an email_campaigns row still references this workshop
// (workshops.ErrHasCampaigns, SQLSTATE 23503 on
// email_campaigns_workshop_id_fkey — #0050's Ruling 1, binding on this
// issue). Writes workshop.deleted only on an actual deletion.
func (h *AdminWorkshopsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseWorkshopID(w, r)
	if !ok {
		return
	}

	// Read first so the audit metadata can record what was deleted (the
	// slug is otherwise gone once Delete succeeds) — mirrors
	// AdminInterestsHandler.Delete.
	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, workshops.ErrNotFound):
		writeError(w, http.StatusNotFound, "workshop not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	err = h.store.Delete(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, workshops.ErrNotFound):
		writeError(w, http.StatusNotFound, "workshop not found")
		return
	case errors.Is(err, workshops.ErrHasCampaigns):
		writeError(w, http.StatusConflict,
			"cannot delete a workshop that an email campaign still references; cancel it instead")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	h.invalidate()

	if h.auditor != nil {
		actorID := actor.ID
		targetID := id
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionWorkshopDeleted,
			TargetType: audit.TargetWorkshop,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"slug":  current.Slug,
				"title": current.Title,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "workshop deleted"})
}

// ── shared helpers ────────────────────────────────────────────────────────────

// parseWorkshopID reads and validates the {id} path value, writing a 400 and
// returning ok=false on a missing/invalid id.
func parseWorkshopID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid workshop id")
		return 0, false
	}
	return id, true
}

// parseOptionalTime parses an optional RFC 3339 timestamp field. A nil or
// empty raw is treated as "unset" (returns nil, true); a non-empty value
// that fails to parse writes a 400 and returns ok=false, mirroring
// AdminCampaignsHandler.Send's scheduled_at parsing.
func parseOptionalTime(w http.ResponseWriter, raw *string, field string) (*time.Time, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*raw))
	if err != nil {
		writeError(w, http.StatusBadRequest, field+" must be RFC 3339")
		return nil, false
	}
	return &parsed, true
}
