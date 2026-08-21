// POST /api/ses/notifications — ingests SES bounce/complaint/delivery
// events delivered by an SNS HTTPS subscription (PRD §6.7; #0038).
//
// # Message-type dispatch is this file's job, not internal/sesnotify's
//
// #0037 built internal/sesnotify's trust boundary — Verify proves a
// message's signature AND its TopicArn — and deliberately stopped there:
// its plan states the package "must have no database dependency and no
// HTTP handler". Everything downstream of "this message may be trusted" —
// auto-confirming a SubscriptionConfirmation, alerting on an
// UnsubscribeConfirmation, and turning a verified Notification into
// email_events/suppressions/subscribers writes — is this file's half. See
// issues/0038.md's "Carried in from #0037's review" section, which is
// binding here.
//
// # One transaction per SNS message (#0033's hard block)
//
// A single incoming Notification can touch three tables: email_events (one
// row per recipient), suppressions (AddTx), and subscribers
// (MarkBouncedTx/MarkComplainedTx). All of it commits or rolls back
// together. Splitting these into separate pool writes would reopen exactly
// the crash window #0033 was built to close: a crash between the
// email_events insert and the suppressions insert would leave the event
// recorded, so SNS's retry gets deduped away by email_events' own UNIQUE
// constraint, and the suppression never lands — a hard-bounced address that
// keeps receiving mail with nothing to indicate it. See
// internal/subscribers/suppressions.go's package doc comment.
//
// # 200 vs 403 vs 500 — the line this handler must get right
//
// SNS retries aggressively on anything other than 2xx, so the status code
// choice is a deliberate contract, not an HTTP-semantics afterthought:
//
//   - Body unreadable, not JSON, or an unrecognized Type -> 200, logged.
//     Retrying doesn't fix a parsing bug; it turns one into a retry storm
//     that buries the real events (PRD §6.7 item 5, criterion 7).
//   - Verify fails because the certificate could not be FETCHED
//     (errors.Is(err, sesnotify.ErrCertUnavailable)) -> 500. Verification
//     could not be PERFORMED; SNS should retry and the event is not lost.
//   - Verify fails for every other reason (bad signature, wrong TopicArn,
//     hostile SigningCertURL) -> 403. Verification FAILED; an attacker must
//     not be able to induce a retry.
//   - The message is trustworthy but the TRANSACTION fails (DB down,
//     constraint violation, serialization failure) -> 500. This is the one
//     place "always 200" is wrong: if the raw payload could not even be
//     recorded, there is nothing to recover from later, and SNS's retry is
//     the only thing standing between us and a silently lost bounce.
//
// # Store dependencies are held concretely, not behind narrow interfaces
//
// CLAUDE.md §1 asks for narrow store interfaces "never a concrete store".
// The three stores below that participate in the shared transaction
// (events, subs, suppr) are a deliberate, documented exception: each one's
// …Tx method takes that PACKAGE's own unexported querier interface
// (mirroring internal/audit's Write/WriteTx split), and a Go interface
// method's parameter type is part of its identity for the purpose of
// satisfying some OTHER interface — a locally-declared interface in this
// package could never structurally match a method whose parameter type is
// unexported in another package. internal/auth's RegistrationService and
// LoginService hold *audit.Logger concretely for the identical reason (they
// call WriteTx). subscriberLookup below stays a real interface because
// FindByEmail takes no querier — nothing stops that one from being narrow.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/sesnotify"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// maxSESNotificationBodyBytes bounds the request body, matching #0037's
// plan §6: SNS caps a message at 256 KB; 512 KB is a comfortable ceiling
// that still refuses a resource-exhaustion body.
const maxSESNotificationBodyBytes = 512 << 10

// sesVerifier is the trust boundary this handler depends on
// (internal/sesnotify, #0037). Narrow because it takes no querier
// parameter — see the package doc comment's note on which dependencies
// stay behind an interface and which don't.
type sesVerifier interface {
	// Verify checks m's signature and TopicArn. A nil return means m may be
	// trusted.
	Verify(ctx context.Context, m *sesnotify.Message) error
	// FetchSubscribeURL host-validates and fetches a SubscriptionConfirmation's
	// SubscribeURL exactly once, discarding the body.
	FetchSubscribeURL(ctx context.Context, subscribeURL string) error
}

// sesTxBeginner is the pool seam this handler needs to own a transaction
// spanning email_events, suppressions, and subscribers writes for one SNS
// message. *pgxpool.Pool satisfies it via its own Begin method.
type sesTxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// SESNotificationsHandler serves POST /api/ses/notifications.
type SESNotificationsHandler struct {
	verify sesVerifier
	begin  sesTxBeginner
	events *sesnotify.Store
	subs   *subscribers.Store
	suppr  *subscribers.SuppressionStore

	auditor *audit.Logger
	now     func() time.Time
	log     *slog.Logger
}

// NewSESNotificationsHandler constructs a SESNotificationsHandler. A nil
// auditor disables audit writes for the SubscriptionConfirmation/
// UnsubscribeConfirmation/state-change paths; a nil logger falls back to
// slog.Default(), matching this package's existing nil-tolerance
// convention (ConfirmHandler, SubscribeHandler).
func NewSESNotificationsHandler(
	verify sesVerifier,
	begin sesTxBeginner,
	events *sesnotify.Store,
	subs *subscribers.Store,
	suppr *subscribers.SuppressionStore,
	auditor *audit.Logger,
	logger *slog.Logger,
) *SESNotificationsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SESNotificationsHandler{
		verify: verify, begin: begin, events: events, subs: subs, suppr: suppr,
		auditor: auditor, now: time.Now, log: logger,
	}
}

// Notify handles POST /api/ses/notifications. See the package doc comment
// for the status-code contract this method implements.
func (h *SESNotificationsHandler) Notify(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSESNotificationBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.log.Warn("ses_notifications: reading request body failed", "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	var msg sesnotify.Message
	if err := json.Unmarshal(body, &msg); err != nil {
		h.log.Warn("ses_notifications: request body is not a valid SNS envelope", "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := h.verify.Verify(r.Context(), &msg); err != nil {
		if errors.Is(err, sesnotify.ErrCertUnavailable) {
			h.log.Error("ses_notifications: could not verify (certificate unavailable)",
				"message_id", msg.MessageId, "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.log.Warn("ses_notifications: verification failed",
			"message_id", msg.MessageId, "topic_arn", msg.TopicArn, "err", err)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	switch msg.Type {
	case sesnotify.TypeSubscriptionConfirmation:
		h.handleSubscriptionConfirmation(w, r, &msg)
	case sesnotify.TypeUnsubscribeConfirmation:
		h.handleUnsubscribeConfirmation(w, r, &msg)
	case sesnotify.TypeNotification:
		h.handleNotification(w, r, &msg)
	default:
		// A signature-valid message with an unrecognized Type. Recorded in
		// the log only (there is no email_events row without a parsed SES
		// event to attach it to) — treated the same as any other
		// uninterpretable payload: 200, never retried.
		h.log.Warn("ses_notifications: unrecognized message Type", "type", msg.Type, "message_id", msg.MessageId)
		w.WriteHeader(http.StatusOK)
	}
}

// handleSubscriptionConfirmation auto-confirms an SNS subscription. Only
// reachable after Verify has already proven both the signature and the
// TopicArn allowlist match (#0037's plan §5): auto-confirming BEFORE that
// pin would hand this endpoint to any AWS account that asked, since a valid
// signature only proves SNS relayed the message, not that our topic sent
// it.
func (h *SESNotificationsHandler) handleSubscriptionConfirmation(w http.ResponseWriter, r *http.Request, msg *sesnotify.Message) {
	ctx := r.Context()
	if err := h.verify.FetchSubscribeURL(ctx, msg.SubscribeURL); err != nil {
		// The message itself is genuine (Verify already passed); a fetch
		// failure here is operational (bad host, network, non-200), not
		// forgery. Still 200: SNS has no different outcome to retry into,
		// and the operator sees this in the log.
		h.log.Error("ses_notifications: SubscriptionConfirmation SubscribeURL fetch failed",
			"message_id", msg.MessageId, "topic_arn", msg.TopicArn, "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	h.log.Info("ses_notifications: auto-confirmed SNS subscription",
		"message_id", msg.MessageId, "topic_arn", msg.TopicArn)
	if h.auditor != nil {
		h.auditor.Record(ctx, audit.Entry{
			Action:     audit.ActionSESSubscriptionConfirmed,
			TargetType: audit.TargetSNSTopic,
			Metadata:   map[string]any{"topic_arn": msg.TopicArn, "message_id": msg.MessageId},
		})
	}
	w.WriteHeader(http.StatusOK)
}

// handleUnsubscribeConfirmation never fetches anything and never acts —
// this message means SNS has unsubscribed this endpoint from the events
// topic, so bounces and complaints are about to silently stop arriving.
// Logged at ERROR and audited, since nothing else in the system would
// otherwise notice.
func (h *SESNotificationsHandler) handleUnsubscribeConfirmation(w http.ResponseWriter, r *http.Request, msg *sesnotify.Message) {
	h.log.Error("ses_notifications: endpoint was UNSUBSCRIBED from the SNS topic — bounce/complaint events will stop arriving",
		"message_id", msg.MessageId, "topic_arn", msg.TopicArn)
	if h.auditor != nil {
		h.auditor.Record(r.Context(), audit.Entry{
			Action:     audit.ActionSESUnsubscribeConfirmation,
			TargetType: audit.TargetSNSTopic,
			Metadata:   map[string]any{"topic_arn": msg.TopicArn, "message_id": msg.MessageId},
		})
	}
	w.WriteHeader(http.StatusOK)
}

// rawPayloadJSON returns raw as JSONB-safe bytes for email_events.payload:
// unchanged when it already IS valid JSON (the overwhelmingly common case —
// SES events are JSON), or wrapped as {"raw": "..."} when it is not, so the
// column (JSONB NOT NULL) can always accept it and the original bytes are
// never discarded even when they can't be interpreted. PRD §6.7 item 3: the
// raw payload is recorded before interpretation, unconditionally.
func rawPayloadJSON(raw string) []byte {
	if json.Valid([]byte(raw)) {
		return []byte(raw)
	}
	wrapped, err := json.Marshal(map[string]string{"raw": raw})
	if err != nil {
		// map[string]string of a single field cannot fail to marshal.
		return []byte(`{"raw":""}`)
	}
	return wrapped
}

// handleNotification is the Bounce/Complaint/Delivery/... path: parse the
// inner SES event, record one email_events row per recipient inside a
// single transaction, then — only if this SNS message was not already
// fully processed by an earlier delivery — apply #0038 §4's state mapping
// per recipient and commit.
func (h *SESNotificationsHandler) handleNotification(w http.ResponseWriter, r *http.Request, msg *sesnotify.Message) {
	ctx := r.Context()

	// ev's zero value (on a parse failure) has Type() == "" and
	// Recipients() falling through to mail.destination (empty) and then to
	// a single "" recipient — so a genuinely unparseable inner payload is
	// handled by the SAME code path below as "valid JSON, unrecognized
	// eventType", rather than a separate branch. Either way the raw bytes
	// are still recorded (rawPayloadJSON) — PRD §6.7 item 3.
	ev, parseErr := sesnotify.ParseSESEvent(msg.Message)
	if parseErr != nil {
		h.log.Warn("ses_notifications: SES event payload is not valid JSON",
			"message_id", msg.MessageId, "err", parseErr)
	}

	tx, err := h.begin.Begin(ctx)
	if err != nil {
		h.log.Error("ses_notifications: begin transaction failed", "message_id", msg.MessageId, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := h.now()
	payload := rawPayloadJSON(msg.Message)
	eventAt, _ := sesnotify.ParseEventTimestamp(ev.Mail.Timestamp)
	var eventAtPtr *time.Time
	if !eventAt.IsZero() {
		eventAtPtr = &eventAt
	}

	recipients := ev.Recipients()
	anyInserted := false
	for _, recipient := range recipients {
		_, inserted, err := h.events.InsertTx(ctx, tx, sesnotify.NewEmailEvent{
			SNSMessageID:  msg.MessageId,
			SESMessageID:  ev.Mail.MessageID,
			EventType:     ev.Type(),
			BounceType:    ev.BounceType(),
			BounceSubtype: ev.BounceSubType(),
			Recipient:     recipient,
			EventAt:       eventAtPtr,
			Payload:       payload,
		})
		if err != nil {
			h.log.Error("ses_notifications: inserting email event failed",
				"message_id", msg.MessageId, "recipient", recipient, "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if inserted {
			anyInserted = true
		}
	}

	if !anyInserted {
		// Every recipient's (sns_message_id, recipient) row already existed
		// — a pure SNS redelivery of a message this handler already fully
		// processed and committed. Nothing left to apply; commit the no-op
		// (there is nothing to roll back) and answer 200.
		if err := tx.Commit(ctx); err != nil {
			h.log.Error("ses_notifications: commit (redelivery no-op) failed", "message_id", msg.MessageId, "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, recipient := range recipients {
		if err := h.applyRecipient(ctx, tx, ev, recipient, now); err != nil {
			h.log.Error("ses_notifications: applying event state failed",
				"message_id", msg.MessageId, "recipient", recipient, "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("ses_notifications: commit failed", "message_id", msg.MessageId, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// applyRecipient is #0038's entire state-mapping surface for one recipient
// of one event, run inside tx. #0039 (repeated soft-bounce suppression)
// hangs its one addition off applyBounce's "Transient" arm below — see that
// function's comment.
func (h *SESNotificationsHandler) applyRecipient(ctx context.Context, tx pgx.Tx, ev sesnotify.SESEvent, recipient string, now time.Time) error {
	if recipient == "" {
		// No address to look up or suppress — this event carried no
		// recipient list at all (already recorded above).
		return nil
	}

	sub, err := h.subs.FindByEmail(ctx, recipient)
	hasSub := true
	switch {
	case errors.Is(err, subscribers.ErrNotFound):
		hasSub = false
	case err != nil:
		return fmt.Errorf("finding subscriber for %q: %w", recipient, err)
	}

	// §3 staleness guard: a genuinely OLD event (e.g. a week-late SNS retry)
	// arriving after the address was legitimately restored — admin cleared
	// a complaint or suppression, the person re-signed-up and re-confirmed —
	// must not undo that. Terminal states are already monotone in the store
	// (setStatusTx never leaves complained), so the only remaining hazard is
	// this one, and it only applies when a subscriber row exists to compare
	// against.
	if hasSub && sub.ConfirmedAt != nil {
		if eventAt, ok := sesnotify.ParseEventTimestamp(ev.Mail.Timestamp); ok && eventAt.Before(*sub.ConfirmedAt) {
			return nil
		}
	}

	switch ev.Type() {
	case sesnotify.EventTypeBounce:
		return h.applyBounce(ctx, tx, ev, recipient, hasSub, sub, now)
	case sesnotify.EventTypeComplaint:
		return h.applyComplaint(ctx, tx, ev, recipient, hasSub, sub, now)
	case sesnotify.EventTypeRenderingFailure:
		h.log.Error("ses_notifications: SES reported a rendering failure", "recipient", recipient, "message_id", ev.Mail.MessageID)
		return nil
	default:
		// Delivery, Reject, DeliveryDelay, Send, and anything SES adds
		// later: already recorded above (InsertTx), nothing else changes.
		return nil
	}
}

// applyBounce is #0038 §4's Bounce mapping.
func (h *SESNotificationsHandler) applyBounce(ctx context.Context, tx pgx.Tx, ev sesnotify.SESEvent, recipient string, hasSub bool, sub subscribers.Subscriber, now time.Time) error {
	switch ev.BounceType() {
	case sesnotify.BounceTypePermanent:
		note := "SES permanent bounce"
		if st := ev.BounceSubType(); st != "" {
			note = fmt.Sprintf("%s (%s)", note, st)
		}
		// AddTx runs unconditionally, per #0038's "Carried in from #0100's
		// plan": the suppressions key is (email, reason), so a later
		// complaint for this same address inserts a SECOND row rather than
		// no-opping — never skip this call because the address is "already
		// suppressed".
		if _, err := h.suppr.AddTx(ctx, tx, subscribers.NewSuppression{
			Email: recipient, Reason: subscribers.SuppressionReasonHardBounce, Note: note,
		}, now); err != nil {
			return fmt.Errorf("adding hard_bounce suppression for %q: %w", recipient, err)
		}
		if !hasSub {
			return nil
		}
		// setStatusTx returns (Subscriber, nil) unchanged when sub is
		// already complained — success, not failure (carried in from
		// #0025's review). The suppression write above already happened
		// regardless, so that criterion holds by construction here.
		if _, err := h.subs.MarkBouncedTx(ctx, tx, sub.ID, now); err != nil {
			return fmt.Errorf("marking %q bounced: %w", recipient, err)
		}
		if h.auditor != nil {
			targetID := sub.ID
			if err := h.auditor.WriteTx(ctx, tx, audit.Entry{
				Action:     audit.ActionSubscriberBounced,
				TargetType: audit.TargetSubscriber,
				TargetID:   &targetID,
				Metadata: map[string]any{
					"email": recipient, "source": "ses",
					"bounce_type": ev.BounceType(), "bounce_subtype": ev.BounceSubType(),
				},
			}); err != nil {
				return fmt.Errorf("writing bounced audit row for %q: %w", recipient, err)
			}
		}
		return nil
	case sesnotify.BounceTypeTransient:
		// #0039 owns counting these toward the soft-bounce threshold. #0038
		// does no counting whatsoever: the row is already recorded above
		// (with bounce_type='Transient' and bounce_subtype faithfully
		// carried), and nothing else changes here. #0039 adds, inside this
		// same transaction, a CountRecentTransientBounces(ctx, tx,
		// recipient, window) call against email_events (the partial index
		// migrations/000014 already provides for it) and, on threshold, the
		// same AddTx(ReasonRepeatedSoftBounce) + MarkBouncedTx pair the
		// Permanent case above already uses.
		return nil
	default:
		// "Undetermined", or anything else SES might report — recorded,
		// unhandled. Whether Undetermined should count toward #0039's
		// threshold is explicitly left to that issue (#0038 §8).
		return nil
	}
}

// applyComplaint is #0038 §4's Complaint mapping. complained is terminal —
// nothing here (or anywhere in this handler) ever moves a subscriber OUT of
// complained; only an admin can (CLAUDE.md §9, subscribers.AdminClearComplaint).
func (h *SESNotificationsHandler) applyComplaint(ctx context.Context, tx pgx.Tx, ev sesnotify.SESEvent, recipient string, hasSub bool, sub subscribers.Subscriber, now time.Time) error {
	if ev.ComplaintFeedbackType() == sesnotify.ComplaintFeedbackTypeNotSpam {
		// A feedback-loop report that a message was moved OUT of spam — the
		// opposite of a complaint. Suppressing on it would unsubscribe
		// someone for rescuing our mail (#0038 §4). Already recorded above;
		// nothing else changes.
		return nil
	}

	if _, err := h.suppr.AddTx(ctx, tx, subscribers.NewSuppression{
		Email: recipient, Reason: subscribers.SuppressionReasonComplaint, Note: "SES complaint",
	}, now); err != nil {
		return fmt.Errorf("adding complaint suppression for %q: %w", recipient, err)
	}
	if !hasSub {
		return nil
	}
	if _, err := h.subs.MarkComplainedTx(ctx, tx, sub.ID, now); err != nil {
		return fmt.Errorf("marking %q complained: %w", recipient, err)
	}
	if h.auditor != nil {
		targetID := sub.ID
		if err := h.auditor.WriteTx(ctx, tx, audit.Entry{
			Action:     audit.ActionSubscriberComplained,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata:   map[string]any{"email": recipient, "source": "ses"},
		}); err != nil {
			return fmt.Errorf("writing complained audit row for %q: %w", recipient, err)
		}
	}
	return nil
}
