// admin_pending.go is the pending-subscriber admin screen (#0128, PRD
// §5.2/§6.3): who signed up but never confirmed, how long they've been
// waiting, whether their token has expired, and a per-subscriber resend —
// the operator's answer to "why didn't they confirm?" now that #0126 gives
// transactional mail a durable trace to read.
//
// GET  /admin/subscribers/pending                    — list, sortable by age
// POST /admin/subscribers/{id}/resend-confirmation    — mint a fresh token, re-enqueue, audited
//
// Both routes are registered through cmd/opencircuit/main.go's adminRoutes
// table, so #0079's structural guard (every /admin route goes through that
// one table) covers them the same way it covers every other admin handler
// in this package.
//
// The expiry sweep itself (#0128's other criterion: "a sweep expires
// pending signups past confirm_expires_at") is NOT in this file — it rides
// internal/mailing.OutboxWorker's existing poll loop
// (subscribers.Store.ExpirePendingSweep, called from outbox_worker.go's
// pass), not a request handler. See that file's doc comment for why.
package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// adminPendingSubscriberStore is the narrow subscribers.Store seam this
// handler needs. *subscribers.Store satisfies it.
type adminPendingSubscriberStore interface {
	ListPending(ctx context.Context, oldestFirst bool) ([]subscribers.Subscriber, error)
	AdminResendConfirmation(ctx context.Context, id int64, now time.Time, cooldown, ttl time.Duration) (subscribers.ResendResult, error)
}

// adminPendingOutboxStore is the narrow internal/outbox seam this handler
// needs to show "delivery failure as a delivery failure, not an unexplained
// non-confirmation" (#0128's criterion). *outbox.Store satisfies it.
type adminPendingOutboxStore interface {
	LatestByRecipients(ctx context.Context, kind outbox.Kind, recipients []string) (map[string]outbox.Row, error)
}

// AdminPendingHandler serves the two routes above.
type AdminPendingHandler struct {
	subs adminPendingSubscriberStore
	// outbox is nil-tolerant (STORAGE=json dev mode has no outbound_queue
	// backing, matching every other outbox-dependent handler in this
	// package) — List simply omits queue_state (reports "unknown") rather
	// than dereferencing it.
	outbox adminPendingOutboxStore
	// auditor records subscriber.resend_confirmation. May be nil in tests
	// that don't assert audit rows, matching every other handler here.
	auditor *audit.Logger
	// cooldown/ttl mirror internal/handlers/subscribe.go's
	// subscribeResendCooldown/subscribeConfirmTTL — the SAME once-per-hour
	// mail-bomb guard and the SAME nominal confirm-link lifetime the public
	// endpoint uses, so an admin-triggered resend behaves exactly like the
	// public "resend" a subscriber could have triggered themselves, not a
	// separately-tuned back door.
	cooldown time.Duration
	ttl      time.Duration
	// now is injectable so timestamps are deterministic in tests; defaults
	// to time.Now, matching every other now-sensitive handler in this
	// package.
	now func() time.Time
}

// NewAdminPendingHandler constructs an AdminPendingHandler. A nil
// outboxStore disables the per-address queue-state figure (STORAGE=json);
// a nil auditor skips the audit write.
func NewAdminPendingHandler(subs adminPendingSubscriberStore, outboxStore adminPendingOutboxStore, auditor *audit.Logger) *AdminPendingHandler {
	return &AdminPendingHandler{
		subs: subs, outbox: outboxStore, auditor: auditor,
		cooldown: subscribeResendCooldown, ttl: subscribeConfirmTTL,
		now: time.Now,
	}
}

// ── Response shapes ──────────────────────────────────────────────────────────

// pendingSubscriberRow is one row of the pending list. Expired and
// AgeSeconds are computed server-side at response time — the client renders
// them, it does not recompute a "now" of its own — so a slow-loading admin
// tab and a fast one agree on whether a row reads as expired.
type pendingSubscriberRow struct {
	ID               int64      `json:"id"`
	Email            string     `json:"email"`
	ConfirmSentAt    *time.Time `json:"confirm_sent_at"`
	ConfirmExpiresAt *time.Time `json:"confirm_expires_at"`
	AgeSeconds       int64      `json:"age_seconds"`
	Expired          bool       `json:"expired"`
	SignupIP         *string    `json:"signup_ip,omitempty"`
	UTMSource        *string    `json:"utm_source,omitempty"`
	UTMMedium        *string    `json:"utm_medium,omitempty"`
	UTMCampaign      *string    `json:"utm_campaign,omitempty"`
	// QueueState is the latest outbound_queue row's status for this
	// address's confirmation mail — "queued" / "sending" / "sent" /
	// "abandoned" — or "unknown" when h.outbox is nil (STORAGE=json) or
	// "none" when no row exists at all (should not happen for a real
	// signup, but the type does not assume it can't).
	QueueState string `json:"queue_state"`
}

type pendingListResponse struct {
	Pending []pendingSubscriberRow `json:"pending"`
}

func toPendingSubscriberRow(sub subscribers.Subscriber, now time.Time, queueState string) pendingSubscriberRow {
	row := pendingSubscriberRow{
		ID: sub.ID, Email: sub.Email,
		ConfirmSentAt: sub.ConfirmSentAt, ConfirmExpiresAt: sub.ConfirmExpiresAt,
		SignupIP: sub.SignupIP, UTMSource: sub.UTMSource, UTMMedium: sub.UTMMedium, UTMCampaign: sub.UTMCampaign,
		QueueState: queueState,
	}
	if sub.ConfirmSentAt != nil {
		row.AgeSeconds = int64(now.Sub(*sub.ConfirmSentAt).Seconds())
	}
	if sub.ConfirmExpiresAt != nil {
		row.Expired = !sub.ConfirmExpiresAt.After(now)
	}
	return row
}

// List handles GET /admin/subscribers/pending?sort=age_asc|age_desc
// (default age_asc — oldest first, PRD §5.2's own framing and #0128's
// criterion). synthetic rows are excluded by ListPending itself.
func (h *AdminPendingHandler) List(w http.ResponseWriter, r *http.Request) {
	oldestFirst := r.URL.Query().Get("sort") != "age_desc"

	rows, err := h.subs.ListPending(r.Context(), oldestFirst)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	queueByRecipient := map[string]outbox.Row{}
	haveOutbox := h.outbox != nil
	if haveOutbox && len(rows) > 0 {
		recipients := make([]string, len(rows))
		for i, sub := range rows {
			recipients[i] = sub.Email
		}
		queueByRecipient, err = h.outbox.LatestByRecipients(r.Context(), outbox.KindConfirmation, recipients)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}

	now := h.now()
	out := make([]pendingSubscriberRow, 0, len(rows))
	for _, sub := range rows {
		state := "unknown"
		if haveOutbox {
			state = "none"
			if r, ok := queueByRecipient[sub.Email]; ok {
				state = r.Status
			}
		}
		out = append(out, toPendingSubscriberRow(sub, now, state))
	}

	writeJSON(w, http.StatusOK, pendingListResponse{Pending: out})
}

type resendConfirmationResponse struct {
	ID               int64     `json:"id"`
	ConfirmSentAt    time.Time `json:"confirm_sent_at"`
	ConfirmExpiresAt time.Time `json:"confirm_expires_at"`
}

// Resend handles POST /admin/subscribers/{id}/resend-confirmation.
func (h *AdminPendingHandler) Resend(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseSubscriberID(w, r)
	if !ok {
		return
	}

	result, err := h.subs.AdminResendConfirmation(r.Context(), id, h.now(), h.cooldown, h.ttl)
	switch {
	case errors.Is(err, subscribers.ErrPendingSubscriberNotFound):
		writeError(w, http.StatusNotFound, "subscriber not found")
		return
	case errors.Is(err, subscribers.ErrNotPending):
		writeError(w, http.StatusConflict, "subscriber is not pending — nothing to resend")
		return
	case errors.Is(err, subscribers.ErrResendSuppressed):
		writeError(w, http.StatusConflict, "this address is suppressed and cannot be mailed")
		return
	case errors.Is(err, subscribers.ErrResendCooldownActive):
		writeError(w, http.StatusTooManyRequests, "a confirmation was sent to this address recently — try again later")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := result.Subscriber.ID
		metadata := map[string]any{}
		if result.PreviousConfirmSentAt != nil {
			metadata["previous_confirm_sent_at"] = result.PreviousConfirmSentAt.UTC().Format(time.RFC3339)
		}
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSubscriberResendConfirmation,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata:   metadata,
			IP:         clientIP(r),
		})
	}

	resp := resendConfirmationResponse{ID: result.Subscriber.ID}
	if result.Subscriber.ConfirmSentAt != nil {
		resp.ConfirmSentAt = *result.Subscriber.ConfirmSentAt
	}
	if result.Subscriber.ConfirmExpiresAt != nil {
		resp.ConfirmExpiresAt = *result.Subscriber.ConfirmExpiresAt
	}
	writeJSON(w, http.StatusOK, resp)
}
