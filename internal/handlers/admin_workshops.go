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
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
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

// announceCampaignStore is the narrow write Announce (admin_workshop_announce.go,
// #0056) needs from internal/mailing.CampaignStore: the same Create call
// AdminCampaignsHandler.Create makes for a hand-composed campaign, so the
// row Announce produces is indistinguishable from one composed by hand
// except for the WorkshopID link and its pre-filled content.
// *mailing.CampaignStore satisfies it.
type announceCampaignStore interface {
	Create(ctx context.Context, in mailing.CampaignInput) (mailing.Campaign, error)
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
	// campaigns backs Announce (admin_workshop_announce.go, #0056) — nil
	// disables that one route's write (test-only; every production call
	// site passes a real *mailing.CampaignStore, see NewAdminWorkshopsHandler's
	// doc comment).
	campaigns announceCampaignStore
	auditor   *audit.Logger
	// baseURL backs Announce's link to the workshop's own page
	// (admin_workshop_announce.go, #0145) — config.Config.BaseURL, the
	// same value AdminCampaignPreviewHandler and mailing.SendStore build
	// unsubscribe/manage links from. An empty baseURL (test-only; every
	// production call site passes cfg.BaseURL) produces a relative
	// "/workshops/{slug}" link rather than panicking.
	baseURL string
}

// NewAdminWorkshopsHandler constructs an AdminWorkshopsHandler. A nil
// auditor disables audit writes (matching every other admin handler's
// nil-tolerance); a nil invalidator disables the SEO/sitemap cache clear
// (test-only — see the package doc comment above). cmd/opencircuit/main.go's
// servePostgres path passes a real *seo.Site (#0054, carried in from
// #0051's review Ruling 1) built by buildSEOSite from the same workshopsStore
// this handler mutates, and threads that identical instance into
// mountAndServe too — see cmd/opencircuit/seo_wiring_test.go for the
// same-instance proof. Before #0054, this argument was a literal nil; #0051's
// review judged that safe-by-construction (the nil was never dereferenced,
// and the caches it would have invalidated held no workshop-derived bytes
// either way, since seo.NewSite's WorkshopSource was also nil at the time).
//
// campaigns (#0056) is the write side of the announce-to-list shortcut —
// *mailing.CampaignStore in production, the SAME instance
// adminCampaignsH is built from (cmd/opencircuit/main.go), so a draft
// created via Announce is created through the identical Create call path
// as one composed by hand in the campaign UI. A nil campaigns disables
// only POST /admin/workshops/{id}/announce (test-only — every production
// call site passes a real store); every other route on this handler is
// unaffected.
//
// baseURL (#0145) is config.Config.BaseURL, threaded through exactly like
// NewAdminCampaignPreviewHandler's baseURL parameter, so Announce's
// "view this workshop" link is built from the same canonical host every
// other outbound link in this codebase uses — never a hardcoded one.
func NewAdminWorkshopsHandler(store workshopStore, invalidator workshopCacheInvalidator, campaigns announceCampaignStore, auditor *audit.Logger, baseURL string) *AdminWorkshopsHandler {
	return &AdminWorkshopsHandler{store: store, invalidator: invalidator, campaigns: campaigns, auditor: auditor, baseURL: baseURL}
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
	coverImage := normalizeOptionalCampaignField(req.CoverImage)
	if coverImage != nil && !isSafeCoverImage(*coverImage) {
		writeError(w, http.StatusBadRequest, coverImageErrorMessage)
		return
	}
	signupURL := normalizeOptionalCampaignField(req.SignupURL)
	if signupURL != nil && !isSafeLinkHref(*signupURL) {
		writeError(w, http.StatusBadRequest, signupURLErrorMessage)
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
		SignupURL:       signupURL,
		CoverImage:      coverImage,
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
	Title           *string `json:"title,omitempty"`
	Summary         *string `json:"summary,omitempty"`
	BodyMD          *string `json:"body_md,omitempty"`
	StartsAt        *string `json:"starts_at,omitempty"`
	EndsAt          *string `json:"ends_at,omitempty"`
	LocationName    *string `json:"location_name,omitempty"`
	LocationAddress *string `json:"location_address,omitempty"`
	LocationNote    *string `json:"location_note,omitempty"`
	// Capacity is Optional[int], not *int (#0146): JSON null and an absent
	// key both decode to a nil *int, so *int alone cannot tell "clear it"
	// apart from "leave it alone". See optional.go's doc comment for why
	// and how. Every other field above stays *string/*[]int64 because
	// #0139 already gave every optional string an in-band clear sentinel
	// (an explicit ""), and InterestIDs already distinguishes "absent" (nil
	// pointer) from "clear to zero interests" (a non-nil pointer to an
	// empty slice, from an explicit `[]`) without any wrapper — capacity is
	// the only field with no spare value to reuse.
	Capacity    Optional[int] `json:"capacity"`
	SignupURL   *string       `json:"signup_url,omitempty"`
	CoverImage  *string       `json:"cover_image,omitempty"`
	Status      *string       `json:"status,omitempty"`
	InterestIDs *[]int64      `json:"interest_ids,omitempty"`
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
	switch {
	case !req.Capacity.Present:
		// key absent from the PATCH body: leave capacity alone.
	case !req.Capacity.Valid:
		capacity = nil // key present as `null`: explicit clear (#0146).
	default:
		v := req.Capacity.Value
		capacity = &v // key present with a value.
	}
	signupURL := current.SignupURL
	if req.SignupURL != nil {
		normalized := normalizeOptionalCampaignField(req.SignupURL)
		if normalized != nil && !isSafeLinkHref(*normalized) {
			writeError(w, http.StatusBadRequest, signupURLErrorMessage)
			return
		}
		signupURL = normalized
	}
	coverImage := current.CoverImage
	if req.CoverImage != nil {
		normalized := normalizeOptionalCampaignField(req.CoverImage)
		if normalized != nil && !isSafeCoverImage(*normalized) {
			writeError(w, http.StatusBadRequest, coverImageErrorMessage)
			return
		}
		coverImage = normalized
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

// coverImageErrorMessage is the 400 body for a cover_image that fails
// isSafeCoverImage, shared by Create and Patch.
const coverImageErrorMessage = `cover_image must be a site-relative path starting with "/" (an absolute URL to another host is not accepted)`

// isSafeCoverImage validates cover_image AT THE API BOUNDARY (#0138, found
// by #0055's phase-3 review): the admin editor already runs an equivalent
// check client-side (web/src/lib/workshopAdmin.ts's isSafeCoverImage), but
// client-side validation doesn't protect a value that arrives by any other
// path -- a hand-crafted request, a future import route, a bug in the
// editor -- and a garbage cover_image reaches internal/seo's absoluteURL
// unvalidated, which prefixes whatever the column holds into a syntactically
// invalid `image` URL in JSON-LD (#0055) and `og:image` (#0019).
//
// The accepted set mirrors the TypeScript twin exactly, and for the same
// reason: CLAUDE.md §9 ("no external CDNs ... self-contained by design") and
// PRD §6.2's own schema comment ("cover_image TEXT, -- path under /assets")
// both say a cover image belongs on THIS site, so an absolute URL to any
// other host -- which loads that host's asset (and leaks a referer) on
// every page view -- is refused outright, not merely restricted to https.
// A single leading "/" is required, and a leading "//" is rejected as
// protocol-relative (same scheme as the page, different host -- exactly as
// off-site as an absolute URL would be); a browser's URL parser treats a
// leading backslash the same as a forward slash for a special (http/https)
// base, so "\evil.host/x" and "/\evil.host/x" both normalize to that same
// "//evil.host/x" form and are caught by normalizing backslashes to
// slashes before the "//" check.
//
// #0138 bounce, finding 1: a prefix check alone isn't enough. A browser's
// URL parser deletes every ASCII tab, LF and CR from the input before
// parsing it, so e.g. "/\t/evil.host/x.jpg" doesn't start with "//" as
// written but reassembles into exactly that off-site form once the browser
// strips the control character back out. Reject the whole C0 control range
// plus DEL up front -- the same HAS_CONTROL_CHAR rule markdown.ts's
// isSafeLinkHref already uses for link destinations (#0052 pass 2) --
// rather than adding a third, narrower definition of "safe URL" to this
// codebase. \x00, \x0b, \x0c and \x7f don't actually resolve off-site (a
// browser percent-encodes them into the path instead), so rejecting those
// too is defence in depth, not strictly required to close the hole.
func isSafeCoverImage(v string) bool {
	if strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	if !strings.HasPrefix(v, "/") {
		return false
	}
	normalized := strings.ReplaceAll(v, `\`, "/")
	return !strings.HasPrefix(normalized, "//")
}

// signupURLErrorMessage is the 400 body for a signup_url that fails
// isSafeLinkHref, shared by Create and Patch.
const signupURLErrorMessage = `signup_url must be an http(s) URL, a mailto: link, or a same-site relative path (not a javascript:/data: scheme or a protocol-relative "//host" URL)`

// isSafeLinkHref validates signup_url AT THE API BOUNDARY (#0152, found by
// #0138's phase-3 review while confirming the cover_image fix): the admin
// editor's own signup_url field check (web/src/lib/workshopAdmin.ts's
// isHttpUrl) is a narrower client-only convenience, but the actual safety
// gate — the one that decides whether a stored signup_url is ever allowed to
// become a rendered `href` — is #0054's hasExternalSignup
// (web/src/lib/workshopDetail.ts), which calls markdown.ts's
// isSafeLinkHref. Nothing on the server enforced that same rule, so a
// javascript: (or control-character-smuggled) signup_url could be STORED by
// any caller that skips the SPA, exactly the shape #0138 closed for
// cover_image. #0152's acceptance criteria are explicit: reuse that rule,
// don't write a third, subtly different one.
//
// This is the Go twin of markdown.ts's isSafeLinkHref, not of
// isSafeCoverImage: a link is a navigation the reader chooses to follow
// (unlike a cover image, which the page loads unasked), so an absolute
// http(s):// URL to ANY host stays allowed — a workshop's signup_url is
// routinely an external ticketing site (Eventbrite, a Google Form) — as does
// mailto:. What's rejected: any other scheme (javascript:, data:,
// vbscript:, ...), a protocol-relative "//host" destination and its
// backslash spellings ("\\host", "/\host", "\/host" — a browser's URL
// parser treats a leading backslash the same as a forward slash for a
// special http/https base), and — per #0138's bounced pass 1 — any
// character in the C0 control range (0x00-0x1f) or DEL (0x7f) ANYWHERE in
// the string, because a browser deletes ASCII tab/LF/CR before resolving a
// URL, so an interior control character can reassemble a dangerous
// destination that a naive prefix/scheme check would miss (e.g.
// "java<TAB>script:alert(1)" doesn't look like it has an unsafe scheme
// until the browser strips the tab back out).
//
// Trim decision (#0152, following up on #0138 pass 2's pipeline-divergence
// note): this function trims internally before testing for emptiness or a
// scheme, exactly mirroring markdown.ts's isSafeLinkHref — so the value
// handed in does not need to be pre-trimmed by the caller, and neither
// Create nor Patch trims it before calling this (the same convention
// isSafeCoverImage already uses: validate the raw normalizeOptionalCampaignField
// result, let the rule itself decide what counts as "effectively blank").
// normalizeOptionalCampaignField's own pre-existing quirk — it tests
// TrimSpace(*v) == "" but returns the UNTRIMMED pointer — means a
// leading/trailing-whitespace signup_url can still be stored once it passes
// this check (e.g. " https://example.com" round-trips with its leading
// space intact). That is unchanged, pre-existing behavior shared by every
// optional campaign-style field (summary, location_name, ...), not
// something new this issue introduces, and generalizing
// normalizeOptionalCampaignField to trim was explicitly left for #0152 to
// decide, not required by it — deferred again here as out of scope; the
// rendering gate (hasExternalSignup) trims before calling isSafeLinkHref, so
// stray whitespace never affects what's shown.
func isSafeLinkHref(href string) bool {
	if strings.ContainsFunc(href, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	trimmed := strings.TrimFunc(href, isJSWhitespace)
	if trimmed == "" {
		return false
	}
	if hasURLScheme(trimmed) {
		return hasSafeLinkScheme(trimmed)
	}
	normalized := strings.ReplaceAll(trimmed, `\`, "/")
	return !strings.HasPrefix(normalized, "//")
}

// isJSWhitespace mirrors the EXACT set of codepoints JavaScript's
// String.prototype.trim() strips (ECMA-262's WhiteSpace and LineTerminator
// productions) -- deliberately not unicode.IsSpace/strings.TrimSpace, which
// disagree with it at two points, both confirmed by executing the real Go
// and TypeScript isSafeLinkHref side by side over a codepoint sweep (#0152
// verification): unicode.IsSpace treats U+0085 (NEL) as whitespace, which
// JS's trim() does not, so "trimSpace(NEL+\"javascript:...\")" strips to a
// bare dangerous scheme and Go would (wrongly) REJECT a value TS accepts;
// and unicode.IsSpace does NOT treat U+FEFF (BOM/ZWNBSP) as whitespace,
// which JS's trim() does, so a leading BOM would (wrongly) hide a
// javascript: scheme from Go's check while TS's trim() strips it and
// correctly rejects. Either divergence would have made this "twin" not
// actually agree with the function it's a twin of, which is exactly what
// #0152's acceptance criteria rule out. Every other codepoint the two
// trim() implementations touch (tab/LF/VT/FF/CR/space/NBSP and the Unicode
// Zs separators U+1680, U+2000-U+200A, U+2028, U+2029, U+202F, U+205F,
// U+3000) already agreed and is unaffected by this switch.
func isJSWhitespace(r rune) bool {
	switch r {
	case 0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x0020, 0x00A0, 0xFEFF,
		0x1680,
		0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200A,
		0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
		return true
	default:
		return false
	}
}

// hasURLScheme reports whether s begins with an RFC 3986-shaped scheme
// (a letter, then any run of letters/digits/"+"/"-"/".", then ":") — the Go
// twin of markdown.ts's HAS_SCHEME regexp (/^[a-z][a-z0-9+.-]*:/i).
func hasURLScheme(s string) bool {
	if s == "" || !isASCIILetter(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == ':' {
			return true
		}
		if !isASCIILetter(c) && !(c >= '0' && c <= '9') && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return false
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// hasSafeLinkScheme reports whether s begins with "http:", "https:", or
// "mailto:", case-insensitively — the Go twin of markdown.ts's
// SAFE_LINK_SCHEME regexp (/^(https?|mailto):/i). Only called once
// hasURLScheme has already confirmed s has a scheme at all.
func hasSafeLinkScheme(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http:") ||
		strings.HasPrefix(lower, "https:") ||
		strings.HasPrefix(lower, "mailto:")
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
