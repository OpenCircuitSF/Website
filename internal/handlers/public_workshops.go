// Public workshop read path (PRD §6.2, §7.3, §8; #0051, widened by #0135):
// GET /api/workshops (the index — #0053) and GET /api/workshops/{slug} (the
// detail page — #0054). No session or admin gate.
//
// # Visibility rule (same on both routes, since #0135)
//
// Both routes serve a workshop whose status is 'published' OR 'canceled';
// 'draft' is invisible to both, and a slug with no matching row 404s just
// like a draft (indistinguishable, so probing slugs leaks nothing).
//
//   - GET /api/workshops returns published-or-canceled workshops, split
//     into upcoming and past (workshops.Store.ListVisible).
//   - GET /api/workshops/{slug} returns one published-or-canceled workshop,
//     or 404.
//
// Before #0135 these two routes disagreed: the detail route served canceled
// (deliberately — PRD's Notes, issues/0051.md: "a canceled workshop stays
// visible with a clear cancellation notice — people who saw the
// announcement will come looking, and a 404 tells them nothing"), but the
// index called ListPublished and silently dropped canceled workshops. That
// produced exactly the outcome the detail route's own reasoning exists to
// prevent, one route over: a visitor who bookmarked the index would see a
// canceled workshop vanish with no explanation, while only someone holding
// the direct link learned it was canceled. #0135 widened the index to match,
// so #0053's canceled-badge criterion (web/src/lib/workshops.ts's
// isCanceled/workshopBadgeLabel) can now actually be reached.
package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// publicWorkshopStore is the narrow slice of workshops.Store this handler
// needs. Depending on an interface (rather than the concrete *workshops.Store)
// matches publicInterestStore/campaignStore's pattern elsewhere in this
// package.
type publicWorkshopStore interface {
	ListVisible(ctx context.Context, now time.Time) (upcoming, past []workshops.Workshop, err error)
	GetBySlug(ctx context.Context, slug string) (workshops.Workshop, error)
}

// publicWorkshopInterestStore resolves interest ids to their public view
// (slug/name/description/sort_order) for a workshop's tag list. Depending on
// the same narrow method publicInterestStore already needs keeps this
// handler consistent with PublicInterestsHandler's own read path.
type publicWorkshopInterestStore interface {
	GetByIDs(ctx context.Context, ids []int64) ([]interests.Interest, error)
}

// PublicWorkshopsHandler serves the public, unauthenticated workshop read
// routes:
//
//	GET /api/workshops       — List:   published-or-canceled workshops, split into upcoming/past
//	GET /api/workshops/{slug} — GetBySlug: one published or canceled workshop; draft/missing 404s
type PublicWorkshopsHandler struct {
	store         publicWorkshopStore
	interestStore publicWorkshopInterestStore // may be nil: interest tags are simply omitted
	now           func() time.Time            // overridable for tests; defaults to time.Now in the constructor
}

// NewPublicWorkshopsHandler constructs a PublicWorkshopsHandler over the
// data layer. interestStore may be nil, in which case every workshop view's
// Interests field is always [] (tags simply omitted, never an error) — this
// keeps the handler usable in tests/deployments that don't wire interest
// resolution.
func NewPublicWorkshopsHandler(store publicWorkshopStore, interestStore publicWorkshopInterestStore) *PublicWorkshopsHandler {
	return &PublicWorkshopsHandler{store: store, interestStore: interestStore, now: time.Now}
}

// ── Response shapes ──────────────────────────────────────────────────────────

// publicWorkshopView is the JSON shape for one workshop as shown to an
// anonymous visitor. Deliberately narrower than the admin workshopView: no
// numeric id (the public wire contract identifies a workshop by slug, same
// convention as publicInterestView dropping interests' numeric id).
type publicWorkshopView struct {
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
	// Status is included even though the index only ever contains
	// 'published' rows -- the detail route (GetBySlug) also serves
	// 'canceled', and #0054's frontend view branches on this field to render
	// the cancellation notice PRD's Notes require.
	Status    string               `json:"status"`
	Interests []publicInterestView `json:"interests"`
}

func (h *PublicWorkshopsHandler) toPublicView(ctx context.Context, w workshops.Workshop) publicWorkshopView {
	return publicWorkshopView{
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
		Interests:       h.resolveInterests(ctx, w.InterestIDs),
	}
}

// resolveInterests batch-resolves a workshop's interest ids to their public
// view for the tag list. Returns [] (never nil) when interestStore is nil,
// there are no ids, or the lookup fails -- a missing tag list is a
// degraded-but-safe rendering, not a reason to 500 the whole workshop page.
func (h *PublicWorkshopsHandler) resolveInterests(ctx context.Context, ids []int64) []publicInterestView {
	views := make([]publicInterestView, 0, len(ids))
	if h.interestStore == nil || len(ids) == 0 {
		return views
	}
	found, err := h.interestStore.GetByIDs(ctx, ids)
	if err != nil {
		return views
	}
	for _, it := range found {
		views = append(views, toPublicInterestView(it))
	}
	return views
}

func toPublicWorkshopViews(ctx context.Context, h *PublicWorkshopsHandler, ws []workshops.Workshop) []publicWorkshopView {
	views := make([]publicWorkshopView, 0, len(ws))
	for _, w := range ws {
		views = append(views, h.toPublicView(ctx, w))
	}
	return views
}

type workshopsListResponsePublic struct {
	Upcoming []publicWorkshopView `json:"upcoming"`
	Past     []publicWorkshopView `json:"past"`
}

// ── GET /api/workshops ───────────────────────────────────────────────────────

// List handles GET /api/workshops: published-or-canceled workshops split
// into upcoming and past (workshops.Store.ListVisible), relative to the
// request time. Public, no auth required.
func (h *PublicWorkshopsHandler) List(w http.ResponseWriter, r *http.Request) {
	upcoming, past, err := h.store.ListVisible(r.Context(), h.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, workshopsListResponsePublic{
		Upcoming: toPublicWorkshopViews(r.Context(), h, upcoming),
		Past:     toPublicWorkshopViews(r.Context(), h, past),
	})
}

// ── GET /api/workshops/{slug} ────────────────────────────────────────────────

// GetBySlug handles GET /api/workshops/{slug}: one workshop, if it exists
// and its status is published or canceled. A draft workshop, or a slug with
// no matching row, both 404 -- deliberately indistinguishable to the caller,
// so a signed-out visitor probing slugs learns nothing about the existence
// of an unpublished workshop (mirrors ConfirmHandler's and
// PreferencesHandler's "don't leak existence" posture for token lookups).
func (h *PublicWorkshopsHandler) GetBySlug(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	wk, err := h.store.GetBySlug(r.Context(), slug)
	switch {
	case err == nil:
	case errors.Is(err, workshops.ErrNotFound):
		writeError(w, http.StatusNotFound, "workshop not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if wk.Status == workshops.StatusDraft {
		writeError(w, http.StatusNotFound, "workshop not found")
		return
	}
	writeJSON(w, http.StatusOK, h.toPublicView(r.Context(), wk))
}
