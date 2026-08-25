// Admin deliverability screen (#0124, PRD §6.9): the read side of the
// consecutive-streak repeated-soft-bounce rule and the per-address history
// behind it.
//
//	GET  /admin/deliverability                — addresses with bounce activity, sorted by streak then recency
//	GET  /admin/deliverability/{email}         — one address's full email_events history
//	POST /admin/deliverability/{email}/reset-streak — clear the streak, audited
//
// # Three logs, one screen
//
// This screen reads email_events (internal/sesnotify, SES's own words,
// verbatim) for the per-address history, and subscribers.soft_bounce_streak
// for the live decision variable — never subscriber_events (#0126), which
// is the address's own activity log rendered on the subscriber detail
// drawer (#0032), a DIFFERENT screen for a DIFFERENT audience. See
// internal/subscribers/events.go's package doc comment for the three-logs
// argument this mirrors.
//
// # Suppression actions live elsewhere
//
// This issue's acceptance criteria say "the admin view links to the
// existing #0100 suppression add/remove endpoints rather than duplicating
// them" — so this file has no add/remove-suppression handler of its own.
// The client calls POST /admin/suppressions/remove (admin_suppressions.go)
// directly; removing a suppression there already resets the streak
// (internal/subscribers/suppressions.go's Remove) as a side effect of that
// existing endpoint, not something this screen re-implements.
package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/sesnotify"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// deliverabilityLister is the narrow read dependency the list endpoint
// needs. *subscribers.Store satisfies it via ListBounceActivity.
type deliverabilityLister interface {
	ListBounceActivity(ctx context.Context) ([]subscribers.BounceActivityItem, error)
}

// deliverabilitySubscriberReader is the narrow read dependency the detail
// endpoint needs to resolve {email} to its current streak/status.
// *subscribers.Store satisfies it via FindByEmail.
type deliverabilitySubscriberReader interface {
	FindByEmail(ctx context.Context, email string) (subscribers.Subscriber, error)
}

// deliverabilityStreakResetter is the narrow write dependency the
// reset-streak endpoint needs. *subscribers.Store satisfies it via
// ResetSoftBounceStreakByEmail.
type deliverabilityStreakResetter interface {
	ResetSoftBounceStreakByEmail(ctx context.Context, email string, now time.Time) error
}

// deliverabilityEventReader is the narrow read dependency the detail
// endpoint needs for the per-address email_events history.
// *sesnotify.Store satisfies it via ByRecipient.
type deliverabilityEventReader interface {
	ByRecipient(ctx context.Context, recipient string) ([]sesnotify.EmailEvent, error)
}

// deliverabilityCampaignResolver is the narrow read dependency the detail
// endpoint needs to resolve a history row's "originating campaign" (a
// ses_message_id -> campaign_id lookup). *mailing.CampaignStatsStore
// satisfies it via CampaignIDsByMessageIDs.
type deliverabilityCampaignResolver interface {
	CampaignIDsByMessageIDs(ctx context.Context, sesMessageIDs []string) (map[string]int64, error)
}

// AdminDeliverabilityHandler serves the three routes above.
type AdminDeliverabilityHandler struct {
	list      deliverabilityLister
	subs      deliverabilitySubscriberReader
	reset     deliverabilityStreakResetter
	events    deliverabilityEventReader
	campaigns deliverabilityCampaignResolver
	auditor   *audit.Logger
	now       func() time.Time
}

// NewAdminDeliverabilityHandler constructs an AdminDeliverabilityHandler. A
// nil auditor disables audit writes, matching every other admin handler's
// nil-tolerance.
func NewAdminDeliverabilityHandler(
	list deliverabilityLister,
	subs deliverabilitySubscriberReader,
	reset deliverabilityStreakResetter,
	events deliverabilityEventReader,
	campaigns deliverabilityCampaignResolver,
	auditor *audit.Logger,
) *AdminDeliverabilityHandler {
	return &AdminDeliverabilityHandler{
		list: list, subs: subs, reset: reset, events: events, campaigns: campaigns,
		auditor: auditor, now: time.Now,
	}
}

// ── Response shapes ──────────────────────────────────────────────────────────

type deliverabilityListItemView struct {
	SubscriberID       int64    `json:"subscriber_id"`
	Email              string   `json:"email"`
	Status             string   `json:"status"`
	SoftBounceStreak   int      `json:"soft_bounce_streak"`
	LastBounceAt       *string  `json:"last_bounce_at,omitempty"`
	LastDeliveryAt     *string  `json:"last_delivery_at,omitempty"`
	Suppressed         bool     `json:"suppressed"`
	SuppressionReasons []string `json:"suppression_reasons"`
}

func toDeliverabilityListItemView(item subscribers.BounceActivityItem) deliverabilityListItemView {
	reasons := item.SuppressionReasons
	if reasons == nil {
		reasons = []string{}
	}
	return deliverabilityListItemView{
		SubscriberID:       item.SubscriberID,
		Email:              item.Email,
		Status:             item.Status,
		SoftBounceStreak:   item.SoftBounceStreak,
		LastBounceAt:       formatTimePtr(item.LastBounceAt),
		LastDeliveryAt:     formatTimePtr(item.LastDeliveryAt),
		Suppressed:         item.Suppressed,
		SuppressionReasons: reasons,
	}
}

type deliverabilityListResponse struct {
	Items []deliverabilityListItemView `json:"items"`
}

// List handles GET /admin/deliverability.
func (h *AdminDeliverabilityHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.list.ListBounceActivity(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views := make([]deliverabilityListItemView, 0, len(items))
	for _, item := range items {
		views = append(views, toDeliverabilityListItemView(item))
	}
	writeJSON(w, http.StatusOK, deliverabilityListResponse{Items: views})
}

type deliverabilityEventView struct {
	EventType      string  `json:"event_type"`
	BounceType     *string `json:"bounce_type,omitempty"`
	BounceSubtype  *string `json:"bounce_subtype,omitempty"`
	DiagnosticCode string  `json:"diagnostic_code,omitempty"`
	CampaignID     *int64  `json:"campaign_id,omitempty"`
	Timestamp      string  `json:"timestamp"`
}

type deliverabilityDetailResponse struct {
	Email            string                    `json:"email"`
	SubscriberID     *int64                    `json:"subscriber_id,omitempty"`
	Status           *string                   `json:"status,omitempty"`
	SoftBounceStreak *int                      `json:"soft_bounce_streak,omitempty"`
	LastBounceAt     *string                   `json:"last_bounce_at,omitempty"`
	LastDeliveryAt   *string                   `json:"last_delivery_at,omitempty"`
	Events           []deliverabilityEventView `json:"events"`
}

// Detail handles GET /admin/deliverability/{email}: the full email_events
// history for one address, newest first (sesnotify.Store.ByRecipient's own
// ordering), each row enriched with its originating campaign (resolved in
// one batched query, not one per row — CampaignIDsByMessageIDs' own doc
// comment) and, for a Bounce event, the SES diagnostic code for exactly
// this recipient (SESEvent.DiagnosticCodeFor — a Bounce event's payload can
// carry several recipients, so this is not simply "the event's own
// diagnostic code").
//
// Answers 200 with an empty events array (never 404) for an address with no
// subscribers row and no email_events history — an admin typing an
// arbitrary address to check is a normal use of this screen, not an error.
func (h *AdminDeliverabilityHandler) Detail(w http.ResponseWriter, r *http.Request) {
	email := strings.ToLower(strings.TrimSpace(r.PathValue("email")))
	if email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}

	resp := deliverabilityDetailResponse{Email: email, Events: []deliverabilityEventView{}}

	if h.subs != nil {
		sub, err := h.subs.FindByEmail(r.Context(), email)
		switch {
		case err == nil:
			id := sub.ID
			status := sub.Status
			streak := sub.SoftBounceStreak
			resp.SubscriberID = &id
			resp.Status = &status
			resp.SoftBounceStreak = &streak
			resp.LastBounceAt = formatTimePtr(sub.LastBounceAt)
			resp.LastDeliveryAt = formatTimePtr(sub.LastDeliveryAt)
		case errors.Is(err, subscribers.ErrNotFound):
			// No subscribers row — resp stays at its zero-value subscriber
			// fields; the event history below may still be non-empty.
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}

	if h.events == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	rows, err := h.events.ByRecipient(r.Context(), email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	campaignByMessageID := map[string]int64{}
	if h.campaigns != nil {
		var ids []string
		for _, row := range rows {
			if row.SESMessageID != nil && *row.SESMessageID != "" {
				ids = append(ids, *row.SESMessageID)
			}
		}
		if len(ids) > 0 {
			resolved, cerr := h.campaigns.CampaignIDsByMessageIDs(r.Context(), ids)
			if cerr != nil {
				writeError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			campaignByMessageID = resolved
		}
	}

	events := make([]deliverabilityEventView, 0, len(rows))
	for _, row := range rows {
		view := deliverabilityEventView{
			EventType:     row.EventType,
			BounceType:    row.BounceType,
			BounceSubtype: row.BounceSubtype,
			Timestamp:     row.ReceivedAt.UTC().Format(time.RFC3339),
		}
		if row.EventType == sesnotify.EventTypeBounce {
			if ev, perr := sesnotify.ParseSESEvent(string(row.Payload)); perr == nil {
				view.DiagnosticCode = ev.DiagnosticCodeFor(email)
			}
		}
		if row.SESMessageID != nil {
			if campaignID, ok := campaignByMessageID[*row.SESMessageID]; ok {
				id := campaignID
				view.CampaignID = &id
			}
		}
		events = append(events, view)
	}
	resp.Events = events
	writeJSON(w, http.StatusOK, resp)
}

type resetStreakResponse struct {
	Email string `json:"email"`
	Reset bool   `json:"reset"`
}

// ResetStreak handles POST /admin/deliverability/{email}/reset-streak: an
// explicit admin override, distinct from the automatic reset a Delivery
// event or a suppression removal perform — an operator who has confirmed
// (by phone, by re-verifying the address by hand) that a flagged address is
// actually fine may clear its streak directly rather than waiting for a
// Delivery event that may never come if the address is never mailed again.
func (h *AdminDeliverabilityHandler) ResetStreak(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PathValue("email")))
	if email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}

	// Looked up BEFORE the reset only to attach a target_id to the audit
	// row when one exists — the reset itself (below) does not need it and
	// still succeeds (as a no-op) for an address with no subscribers row,
	// per ResetSoftBounceStreakByEmail's own doc comment.
	var targetID *int64
	if h.subs != nil {
		if sub, serr := h.subs.FindByEmail(r.Context(), email); serr == nil {
			id := sub.ID
			targetID = &id
		}
	}

	if err := h.reset.ResetSoftBounceStreakByEmail(r.Context(), email, h.now()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSubscriberSoftBounceStreakReset,
			TargetType: audit.TargetSubscriber,
			TargetID:   targetID,
			Metadata:   map[string]any{"email": email},
			IP:         clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, resetStreakResponse{Email: email, Reset: true})
}
