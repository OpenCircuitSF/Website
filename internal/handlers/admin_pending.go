// admin_pending.go is the pending-subscriber admin screen (#0128, PRD
// §5.2/§6.3): who signed up but never confirmed, how long they've been
// waiting, whether their token has expired, and a per-subscriber resend —
// the operator's answer to "why didn't they confirm?" now that #0126 gives
// transactional mail a durable trace to read.
//
// GET  /admin/subscribers/pending                    — list, sortable by age
// POST /admin/subscribers/{id}/resend-confirmation    — mint a fresh token, re-enqueue, audited
// POST /admin/subscribers/{id}/resend-invitation      — #0312: re-send an
//
//	unaccepted import invitation, at most once per address ever (the
//	bounded, user-approved PRD §6.10.1 deviation — see
//	AdminResendInvitation's own doc comment, internal/subscribers/pending.go)
//
// All three routes are registered through cmd/opencircuit/main.go's
// adminRoutes table, so #0079's structural guard (every /admin route goes
// through that one table) covers them the same way it covers every other
// admin handler in this package.
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
	"strings"
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
	// AdminResendInvitation (#0312) has no ttl parameter — it always uses
	// the invitation's own importInviteConfirmTTL, never the confirmation's
	// subscribeConfirmTTL. See that method's own doc comment.
	AdminResendInvitation(ctx context.Context, id int64, now time.Time, cooldown time.Duration) (subscribers.ResendResult, error)
}

// adminPendingSettingsReader is the narrow settings seam ResendInvitation
// needs for its advisory physical_address pre-check — see that method's own
// doc comment for why this is a usability nicety and NOT the §9 gate.
// *auth.Store satisfies it (the same SettingsReader shape
// internal/mailing.SettingsReader already establishes).
type adminPendingSettingsReader interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// adminPendingOutboxStore is the narrow internal/outbox seam this handler
// needs to show "delivery failure as a delivery failure, not an unexplained
// non-confirmation" (#0128's criterion). *outbox.Store satisfies it.
type adminPendingOutboxStore interface {
	LatestByRecipients(ctx context.Context, kind outbox.Kind, recipients []string) (map[string]outbox.Row, error)
}

// AdminPendingHandler serves the three routes above.
type AdminPendingHandler struct {
	subs adminPendingSubscriberStore
	// outbox is nil-tolerant (STORAGE=json dev mode has no outbound_queue
	// backing, matching every other outbox-dependent handler in this
	// package) — List simply omits queue_state (reports "unknown") rather
	// than dereferencing it.
	outbox adminPendingOutboxStore
	// auditor records subscriber.resend_confirmation / subscriber.resend_invitation. May be nil in tests
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
	// settings backs ResendInvitation's advisory physical_address pre-check
	// (#0312) — nil-tolerant, matching outbox above (STORAGE=json / a test
	// that doesn't care): a nil settings simply skips the pre-check, since
	// the real gate lives in OutboxWorker.render regardless (see
	// ResendInvitation's own doc comment).
	settings adminPendingSettingsReader
	// now is injectable so timestamps are deterministic in tests; defaults
	// to time.Now, matching every other now-sensitive handler in this
	// package.
	now func() time.Time
}

// NewAdminPendingHandler constructs an AdminPendingHandler. A nil
// outboxStore disables the per-address queue-state figure (STORAGE=json); a
// nil auditor skips the audit write; a nil settings disables
// ResendInvitation's advisory physical_address pre-check (the §9 gate
// itself lives in OutboxWorker.render and is unaffected).
func NewAdminPendingHandler(subs adminPendingSubscriberStore, outboxStore adminPendingOutboxStore, settings adminPendingSettingsReader, auditor *audit.Logger) *AdminPendingHandler {
	return &AdminPendingHandler{
		subs: subs, outbox: outboxStore, settings: settings, auditor: auditor,
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
	// Invited is true for a still-pending row an import invited (#0129:
	// import_id set, consent_basis still NULL — the same row-state test
	// internal/subscribers.Store.Confirm/Unsubscribe use to recognize an
	// invitation elsewhere) — #0129's acceptance criterion "the pending
	// screen distinguishes an invited address from a website signup
	// awaiting confirmation".
	Invited bool `json:"invited"`
	// InviteResendAvailable is true exactly when Invited && the row has not
	// already used its one admin re-send (#0312: invited &&
	// invite_resent_at == nil). The SPA uses this, not Invited alone, to
	// decide whether to render an enabled "Resend invitation" action —
	// Invited stays true forever on an invited row (it names how the row
	// was created), while this flips to false the moment the one-ever
	// re-send is spent.
	InviteResendAvailable bool `json:"invite_resend_available"`
	// InviteResentAt is nil until an admin has used the one re-send this
	// address will ever get (issues/0312.md's approved PRD §6.10.1
	// deviation); once set, it is the reason the button above is disabled.
	InviteResentAt *time.Time `json:"invite_resent_at,omitempty"`
	// QueueState is the latest outbound_queue row's status for this
	// address's confirmation/invitation mail — "queued" / "sending" /
	// "sent" / "skipped" / "abandoned" — or "unknown" when h.outbox is nil
	// (STORAGE=json) or "none" when no row exists at all (should not
	// happen for a real signup, but the type does not assume it can't).
	// "skipped" (#0365, #0378) means the system correctly withheld the
	// message — the subscriber's live state made it ineligible to send by
	// the time the worker reached it, or a queued import invitation was
	// superseded by that same address's own signup — never a delivery
	// failure; only "abandoned" means that. "failed" is a value the
	// database's CHECK constraint permits but nothing in this codebase
	// ever writes, so it does not appear here. Looked up against
	// outbox.KindImportInvite for an Invited row and
	// outbox.KindConfirmation otherwise — the two kinds are mutually
	// exclusive per address (see List's own comment).
	QueueState string `json:"queue_state"`
}

type pendingListResponse struct {
	Pending []pendingSubscriberRow `json:"pending"`
}

func toPendingSubscriberRow(sub subscribers.Subscriber, now time.Time, queueState string) pendingSubscriberRow {
	invited := sub.ImportID != nil && sub.ConsentBasis == nil
	row := pendingSubscriberRow{
		ID: sub.ID, Email: sub.Email,
		ConfirmSentAt: sub.ConfirmSentAt, ConfirmExpiresAt: sub.ConfirmExpiresAt,
		SignupIP: sub.SignupIP, UTMSource: sub.UTMSource, UTMMedium: sub.UTMMedium, UTMCampaign: sub.UTMCampaign,
		InviteResendAvailable: invited && sub.InviteResentAt == nil,
		InviteResentAt:        sub.InviteResentAt,
		Invited:               invited,
		QueueState:            queueState,
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

	// Two independent LatestByRecipients calls, one per Kind — #0129: an
	// invited row's confirmation mail is queued as outbox.KindImportInvite,
	// never outbox.KindConfirmation, so a single lookup would report
	// "none" for every invited address regardless of its real queue state.
	// The two maps never both hold an entry for the same address (a
	// subscribers row is either import-invited or not, for the lifetime of
	// its pending status), so merging by "first hit wins" below is safe.
	confirmationByRecipient := map[string]outbox.Row{}
	inviteByRecipient := map[string]outbox.Row{}
	haveOutbox := h.outbox != nil
	if haveOutbox && len(rows) > 0 {
		recipients := make([]string, len(rows))
		for i, sub := range rows {
			recipients[i] = sub.Email
		}
		confirmationByRecipient, err = h.outbox.LatestByRecipients(r.Context(), outbox.KindConfirmation, recipients)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		inviteByRecipient, err = h.outbox.LatestByRecipients(r.Context(), outbox.KindImportInvite, recipients)
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
			if r, ok := confirmationByRecipient[sub.Email]; ok {
				state = r.Status
			} else if r, ok := inviteByRecipient[sub.Email]; ok {
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
	case errors.Is(err, subscribers.ErrResendNotForInvited):
		writeError(w, http.StatusConflict, "this address is an unaccepted import invitation — use POST /admin/subscribers/{id}/resend-invitation instead (available once)")
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

type resendInvitationResponse struct {
	ID               int64     `json:"id"`
	ConfirmSentAt    time.Time `json:"confirm_sent_at"`
	ConfirmExpiresAt time.Time `json:"confirm_expires_at"`
	InviteResentAt   time.Time `json:"invite_resent_at"`
}

// ResendInvitation handles POST /admin/subscribers/{id}/resend-invitation
// (#0312) — Resend's twin for a row this package's Invited test identifies
// as an unaccepted import invitation. The bounded, user-approved deviation
// from PRD §6.10.1 ("one invitation per address, ever") this method exists
// to serve is recorded in issues/0312.md's "Decision" section (approved
// 2026-08-31): at most ONE admin-triggered re-send, ever, per address, on
// top of the automated import path's unconditional cap.
//
// # The physical_address pre-check below is advisory, NOT the §9 gate
//
// If physical_address is unset, a re-send would still enqueue successfully
// — OutboxWorker.render's KindImportInvite arm defers rather than sends
// (errImportInviteMissingPhysicalAddress) — but doing so would burn the
// one-and-only invite_resent_at on a message that will sit deferred
// indefinitely, with no way for the admin to get a second try. So this
// handler reads the setting and answers 409 BEFORE calling the store at
// all, purely so the admin sees a useful, immediate reason instead of a
// silently-stuck queue row.
//
// This is a usability nicety layered IN FRONT of the gate, never a
// substitute for it: removing this whole pre-check changes nothing about
// whether a message can actually be sent with a blank physical_address,
// because AdminResendInvitation still only ENQUEUES (never sends directly)
// and OutboxWorker.render still refuses to build the message regardless of
// how the row got into the queue. CLAUDE.md §9's "must not be bypassable
// from the UI" is satisfied by the worker, not by this — see
// TestAdminResendInvitation_PhysicalAddressGateNotBypassable, which proves
// the gate holds with this pre-check removed entirely.
func (h *AdminPendingHandler) ResendInvitation(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseSubscriberID(w, r)
	if !ok {
		return
	}

	// Advisory only — see this method's own doc comment. h.settings is
	// nil-tolerant (STORAGE=json / a test with no settings backing): the
	// pre-check is simply skipped, and the store's own enqueue-then-defer
	// path is still what actually protects CAN-SPAM §7704 compliance.
	if h.settings != nil {
		addr, err := h.settings.GetSetting(r.Context(), settingPhysicalAddress)
		if err != nil || strings.TrimSpace(addr) == "" {
			writeError(w, http.StatusConflict, "physical_address is not set — set it in settings before resending this invitation, since the one-and-only re-send would otherwise be deferred indefinitely")
			return
		}
	}

	result, err := h.subs.AdminResendInvitation(r.Context(), id, h.now(), h.cooldown)
	switch {
	case errors.Is(err, subscribers.ErrPendingSubscriberNotFound):
		writeError(w, http.StatusNotFound, "subscriber not found")
		return
	case errors.Is(err, subscribers.ErrNotPending):
		writeError(w, http.StatusConflict, "subscriber is not pending — nothing to resend")
		return
	case errors.Is(err, subscribers.ErrResendNotAnInvitation):
		writeError(w, http.StatusConflict, "this address is not an unaccepted import invitation — use POST /admin/subscribers/{id}/resend-confirmation instead")
		return
	case errors.Is(err, subscribers.ErrInviteImportRevoked):
		writeError(w, http.StatusConflict, "the import batch that invited this address has been revoked")
		return
	case errors.Is(err, subscribers.ErrInviteAlreadyResent):
		writeError(w, http.StatusConflict, "this address has already received its one invitation re-send")
		return
	case errors.Is(err, subscribers.ErrResendSuppressed):
		writeError(w, http.StatusConflict, "this address is suppressed and cannot be mailed")
		return
	case errors.Is(err, subscribers.ErrResendCooldownActive):
		writeError(w, http.StatusTooManyRequests, "an invitation was sent to this address recently — try again later")
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
		if result.Subscriber.ImportID != nil {
			metadata["import_id"] = *result.Subscriber.ImportID
		}
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSubscriberResendInvitation,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata:   metadata,
			IP:         clientIP(r),
		})
	}

	resp := resendInvitationResponse{ID: result.Subscriber.ID}
	if result.Subscriber.ConfirmSentAt != nil {
		resp.ConfirmSentAt = *result.Subscriber.ConfirmSentAt
	}
	if result.Subscriber.ConfirmExpiresAt != nil {
		resp.ConfirmExpiresAt = *result.Subscriber.ConfirmExpiresAt
	}
	if result.Subscriber.InviteResentAt != nil {
		resp.InviteResentAt = *result.Subscriber.InviteResentAt
	}
	writeJSON(w, http.StatusOK, resp)
}
