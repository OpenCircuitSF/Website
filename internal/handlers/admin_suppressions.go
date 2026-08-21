// Admin suppression-list screen (PRD §6.2; #0100): list every row of the
// suppressions table (#0033) with the subscriber status alongside it, and
// let an admin reverse one with a required justification note.
//
// # A separate handler, not more AdminSubscribersHandler methods
//
// A suppression is keyed by email and can exist with NO subscribers row at
// all — hard deletion (#0060), or a suppression added before any signup —
// so it is not a subscriber sub-resource the way Suppress/ClearComplaint
// are. It gets its own file, its own handler type, and its own narrow store
// seams (suppressionLister, suppressionSubscriberLookup) rather than being
// bolted onto AdminSubscribersHandler.
//
// # Why POST .../remove rather than DELETE /admin/suppressions/{email}
//
// Email addresses make poor path segments ('+', '%', '.' all need
// percent-encoding), the action requires a justification note carried in a
// body, and every other mutating admin action in this codebase is a POST
// (Suppress, ClearComplaint, the interests CRUD's own POST/PATCH shape) —
// this follows Suppress's shape deliberately.
//
// # Reason-scoped removal, and the one refusal
//
// #0100's whole point is that a suppression is scoped by (email, reason):
// an address can carry more than one independent reason to be blocked, and
// removing one must not touch another. Removing a reason="complaint" row is
// refused (409) whenever a subscribers row still exists for that address —
// AdminSubscribersHandler.ClearComplaint is the one sanctioned, audited path
// that touches a complaint while a subscriber record exists (CLAUDE.md §9's
// concentration of authority over the `complained` state), and this handler
// deliberately does not open a second one. The refusal lifts only for an
// ORPHANED complaint suppression (no subscribers row at all), since
// ClearComplaint is keyed by subscriber id and would otherwise leave that
// row permanently unremovable.
//
// # Reversal never touches subscriber status
//
// Removing a suppression — any reason, including manual — never changes a
// subscriber's status. A manual suppress leaves the row `unsubscribed`;
// un-suppressing leaves it `unsubscribed`. The address is no longer blocked
// at the send gate, but it is still not a subscriber: it returns only
// through double opt-in (subscribers.Store.RestartSignup already handles
// `unsubscribed`). Silently flipping anything to `active` here would
// manufacture consent that was never given, and nothing in this handler
// can be used as an oblique route to un-complain an address either — the
// 409 above already forecloses that.
package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// suppressionLister is the behavior AdminSuppressionsHandler needs from
// subscribers.SuppressionStore: the full admin listing (with subscriber
// status joined in), per-address survivors after a removal, and the removal
// itself. *subscribers.SuppressionStore satisfies it directly.
type suppressionLister interface {
	List(ctx context.Context) ([]subscribers.SuppressionListItem, error)
	ListByEmail(ctx context.Context, email string) ([]subscribers.Suppression, error)
	Remove(ctx context.Context, email, reason string) (subscribers.Suppression, error)
}

// suppressionSubscriberLookup is the one piece of behavior this handler
// needs from internal/subscribers.Store: deciding whether a subscribers row
// exists for the address being un-suppressed, which drives the
// reason=complaint refusal above and the audit row's target/subscriber
// fields. *subscribers.Store satisfies it via FindByEmail.
type suppressionSubscriberLookup interface {
	FindByEmail(ctx context.Context, email string) (subscribers.Subscriber, error)
}

// suppressionValidReasons is the closed set of legal reason values,
// mirroring subscribers.suppressionReasons (unexported in that package) —
// duplicated here rather than exported solely for this one call site, since
// SuppressionStore.Remove already re-validates and returns
// ErrInvalidSuppressionReason regardless; this copy only lets the handler
// give that 400 before doing any lookup.
var suppressionValidReasons = map[string]bool{
	subscribers.SuppressionReasonHardBounce:         true,
	subscribers.SuppressionReasonComplaint:          true,
	subscribers.SuppressionReasonManual:             true,
	subscribers.SuppressionReasonRepeatedSoftBounce: true,
}

// AdminSuppressionsHandler serves the admin-only suppression-list routes
// (PRD §5.2/§6.2; #0100):
//
//	GET  /admin/suppressions          — every row, joined to subscriber status
//	POST /admin/suppressions/remove   — reverse one (email, reason) with a required note
//
// Both routes MUST be mounted behind middleware.RequireSession then
// middleware.RequireAdmin, exactly like every other admin handler in this
// package — see cmd/opencircuit/main.go's adminRoutes.
type AdminSuppressionsHandler struct {
	suppressions suppressionLister
	subs         suppressionSubscriberLookup
	auditor      *audit.Logger
}

// NewAdminSuppressionsHandler constructs an AdminSuppressionsHandler. A nil
// auditor disables audit writes, matching AdminSubscribersHandler's own
// nil-tolerance.
func NewAdminSuppressionsHandler(suppressions suppressionLister, subs suppressionSubscriberLookup, auditor *audit.Logger) *AdminSuppressionsHandler {
	return &AdminSuppressionsHandler{suppressions: suppressions, subs: subs, auditor: auditor}
}

// ── Response shapes ──────────────────────────────────────────────────────────

// suppressionView is the JSON shape for one suppressions row on the admin
// screen. subscriber_status is nil when no subscribers row exists for the
// address (an orphan) — the deliberate addition beyond the four raw columns
// that makes the two-layer picture ("blocked at the suppression list, the
// subscriber row, or both?") legible and lets the client pre-disable a
// complaint removal before the server 409s.
type suppressionView struct {
	Email            string  `json:"email"`
	Reason           string  `json:"reason"`
	Note             *string `json:"note,omitempty"`
	CreatedAt        string  `json:"created_at"`
	SubscriberStatus *string `json:"subscriber_status"`
}

func toSuppressionView(item subscribers.SuppressionListItem) suppressionView {
	return suppressionView{
		Email:            item.Email,
		Reason:           item.Reason,
		Note:             item.Note,
		CreatedAt:        item.CreatedAt.UTC().Format(time.RFC3339),
		SubscriberStatus: item.SubscriberStatus,
	}
}

func toSuppressionViewFromRow(sup subscribers.Suppression) suppressionView {
	return suppressionView{
		Email:     sup.Email,
		Reason:    sup.Reason,
		Note:      sup.Note,
		CreatedAt: sup.CreatedAt.UTC().Format(time.RFC3339),
	}
}

type suppressionsListResponse struct {
	Suppressions []suppressionView `json:"suppressions"`
	Count        int               `json:"count"`
}

// ── GET /admin/suppressions ──────────────────────────────────────────────────

// List handles GET /admin/suppressions: every suppression row, joined to
// its subscriber's status when one exists. No pagination or filtering — see
// the package doc comment on internal/subscribers/suppressions.go's List
// for why.
func (h *AdminSuppressionsHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.suppressions.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views := make([]suppressionView, 0, len(items))
	for _, item := range items {
		views = append(views, toSuppressionView(item))
	}
	writeJSON(w, http.StatusOK, suppressionsListResponse{Suppressions: views, Count: len(views)})
}

// ── POST /admin/suppressions/remove ──────────────────────────────────────────

type removeSuppressionRequest struct {
	Email  string `json:"email"`
	Reason string `json:"reason"`
	Note   string `json:"note"`
}

type removeSuppressionResponse struct {
	Removed   suppressionView `json:"removed"`
	Remaining []string        `json:"remaining"`
	Message   string          `json:"message"`
}

// Remove handles POST /admin/suppressions/remove: reverse one (email,
// reason) suppression with a required justification note. See the package
// doc comment for the reason=complaint refusal and why reversal never
// changes subscriber status.
func (h *AdminSuppressionsHandler) Remove(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	var req removeSuppressionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		writeError(w, http.StatusBadRequest, "note is required")
		return
	}
	if !suppressionValidReasons[req.Reason] {
		writeError(w, http.StatusBadRequest, "invalid reason")
		return
	}

	ctx := r.Context()

	sub, err := h.subs.FindByEmail(ctx, email)
	subscriberExists := err == nil
	if err != nil && !errors.Is(err, subscribers.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// The one refusal (package doc comment): a `complaint` row is removable
	// here only when it is orphaned. While a subscribers row exists,
	// AdminSubscribersHandler.ClearComplaint is the sole sanctioned,
	// audited path (CLAUDE.md §9).
	if req.Reason == subscribers.SuppressionReasonComplaint && subscriberExists {
		writeError(w, http.StatusConflict,
			"a subscriber record exists for this address; use Subscribers → Clear complaint instead of removing this suppression directly")
		return
	}

	removed, err := h.suppressions.Remove(ctx, email, req.Reason)
	switch {
	case errors.Is(err, subscribers.ErrSuppressionNotFound):
		writeError(w, http.StatusNotFound, "no suppression found for this email and reason")
		return
	case errors.Is(err, subscribers.ErrInvalidSuppressionReason):
		// Defensive: suppressionValidReasons already rejected this above,
		// so the store should never disagree, but a mismatch would be a
		// caller bug worth surfacing accurately rather than as a 500.
		writeError(w, http.StatusBadRequest, "invalid reason")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	remainingRows, err := h.suppressions.ListByEmail(ctx, email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	remaining := make([]string, 0, len(remainingRows))
	for _, sup := range remainingRows {
		remaining = append(remaining, sup.Reason)
	}

	if h.auditor != nil {
		actorID := actor.ID
		var targetID *int64
		var subscriberStatus *string
		if subscriberExists {
			id := sub.ID
			targetID = &id
			status := sub.Status
			subscriberStatus = &status
		}
		h.auditor.Record(ctx, audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSuppressionRemoved,
			TargetType: audit.TargetSubscriber,
			TargetID:   targetID,
			Metadata: map[string]any{
				"email":                       email,
				"reason":                      req.Reason,
				"note":                        note,
				"removed_note":                removed.Note,
				"removed_created_at":          removed.CreatedAt,
				"suppressions_remaining":      remaining,
				"subscriber_status":           subscriberStatus,
				"subscriber_status_unchanged": true,
			},
			IP: clientIP(r),
		})
	}

	message := "Suppression removed; this does not resubscribe the address — it must opt in again."
	if len(remaining) > 0 {
		message += " This address is still suppressed for: " + strings.Join(remaining, ", ") + "."
	}

	writeJSON(w, http.StatusOK, removeSuppressionResponse{
		Removed:   toSuppressionViewFromRow(removed),
		Remaining: remaining,
		Message:   message,
	})
}
