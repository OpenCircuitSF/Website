// POST /api/ses/inbound — parses inbound mail sent to
// unsubscribe@lists.opencircuitsf.com and, on a match, unsubscribes the
// sender (PRD §6.5 path 3; #0058). Reached from the SAME SNS-HTTPS-
// subscription shape #0038's /api/ses/notifications uses (#0037's trust
// boundary), but over a DIFFERENT topic: #0057's plan sets
// opencircuit-inbound-mail, distinct from opencircuit-ses-events, precisely
// because this file's dispatch and that one's both key off TopicArn — a
// shared topic would let either endpoint's messages satisfy the other's
// verifier.
//
// # Not an SES "mail content" notification
//
// #0057's plan corrects its own criteria on this point: the receipt rule
// takes a single S3Action with its TopicArn set, not a separate SNS action.
// A standalone SNS action publishes the raw mail content (capped at
// 150 KB) and carries no S3 object key; the S3 action's own notification is
// SES's "Received" event (sesnotify.EventTypeReceived) — an object shaped
// like every other SES event this codebase already parses (mail/receipt),
// naming the bucket and key the S3 action wrote to, not the mail content
// itself. See sesnotify.SESEvent.ObjectKey's doc comment for the field this
// file reads to find the object.
//
// # Fetch -> parse -> match -> act, all before any store write
//
// Unlike #0038's handler, this one needs no cross-table transaction: a
// mailto unsubscribe touches exactly one row (subscribers), through the
// SAME subscribers.Store.Unsubscribe/RotateManageToken methods #0034's
// one-click handler already calls. So this handler holds *subscribers.Store
// through a narrow interface (inboundSubscriberStore below) rather than a
// concrete store plus a transaction-beginner — the "hold concretely"
// exception ses_notifications.go's package doc comment documents does not
// apply here, because nothing here needs that package's unexported querier
// parameter.
//
// # S3 is behind a narrow interface, never a concrete client
//
// inboundObjectStore is this file's seam onto internal/inbound.S3Store —
// CLAUDE.md §1's "narrow interfaces, never a concrete store" applied to AWS
// S3 the same way sesVerifier (ses_notifications.go) already applies it to
// AWS SNS/certificate fetching: no *s3.Client type reaches this file or its
// tests.
//
// # Delete only on a processed message, never on the no-match path
//
// PRD §6.5's diagram states this explicitly ("S3 lifecycle rule deletes
// objects after 30 days" is the ONLY cleanup for anything left behind), and
// #0058's own criteria separate "processed objects deleted from S3" from
// "neither matching -> logged, object left in place for manual review".
// This file treats "processed" as "a subscriber was matched, whether or not
// Unsubscribe changed anything" (an already-complained match is a genuine
// match that correctly does nothing, mirroring #0034's no-op — see
// handleMatch below) and leaves the object in S3 for every other outcome:
// no token and no From match, an unparseable message, and an auto-reply.
// The 30-day lifecycle rule (#0057, A5) is what eventually reclaims those.
//
// # Auto-replies are checked before any lookup at all
//
// #0058's practical hazard: an out-of-office bouncing off a campaign send
// must never be read as an unsubscribe request. internal/inbound.Parse's
// AutoReply field is checked first, before the token/From matching this
// file otherwise tries — see that package's isAutoReply for exactly which
// header signals it checks.
//
// # 200 vs 500 — narrower than #0038's three-way contract
//
// This endpoint has no forgeable action costly enough to need a 403 path of
// its own beyond Verify's (handled identically to ses_notifications.go):
//
//   - Body unreadable, not JSON, unrecognized Type, unparseable S3 event, no
//     resolvable object key, an unparseable fetched message, an auto-reply,
//     or no match at all -> 200. None of these is fixed by SNS retrying the
//     same notification.
//   - Verify fails because the certificate could not be fetched
//     (errors.Is(err, sesnotify.ErrCertUnavailable)) -> 500, matching
//     ses_notifications.go exactly: verification could not be performed.
//   - Verify fails for any other reason -> 403, matching ses_notifications.go.
//   - Fetching the object from S3, or the subscriber-store lookup/mutation on
//     a genuine match, fails operationally -> 500: the object is still in
//     S3 (never deleted before this point), so a retry is safe and is the
//     only thing standing between us and losing a real unsubscribe request.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/inbound"
	"github.com/brennanMKE/OpenCircuitSF/internal/sesnotify"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// maxSESInboundBodyBytes bounds the SNS envelope body, matching
// ses_notifications.go's maxSESNotificationBodyBytes exactly — the envelope
// itself is small regardless of the mail message it references (that's
// fetched separately from S3, bounded by internal/inbound.S3Store's own
// limit).
const maxSESInboundBodyBytes = 512 << 10

// inboundObjectStore is the S3 access SESInboundHandler needs. Satisfied by
// *inbound.S3Store; see this file's package doc comment for why it's an
// interface rather than that concrete type.
type inboundObjectStore interface {
	Fetch(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// inboundSubscriberStore is the subscribers.Store behavior this handler
// needs — the same three methods #0034's UnsubscribeHandler already
// depends on (unsubscribeSubscriberStore, unsubscribe.go) plus FindByEmail
// for the From: fallback. *subscribers.Store satisfies this directly.
type inboundSubscriberStore interface {
	FindByManageToken(ctx context.Context, token string) (subscribers.Subscriber, error)
	FindByEmail(ctx context.Context, email string) (subscribers.Subscriber, error)
	Unsubscribe(ctx context.Context, id int64, source string, now time.Time) (subscribers.Subscriber, error)
	RotateManageToken(ctx context.Context, id int64, now time.Time) (subscribers.Subscriber, error)
}

// SESInboundHandler serves POST /api/ses/inbound (PRD §6.5 path 3; #0058).
type SESInboundHandler struct {
	verify  sesVerifier
	objects inboundObjectStore
	subs    inboundSubscriberStore
	auditor *audit.Logger
	now     func() time.Time
	log     *slog.Logger
}

// NewSESInboundHandler constructs a SESInboundHandler. A nil auditor
// disables the audit write on a real match; a nil logger falls back to
// slog.Default(), matching this package's existing convention.
func NewSESInboundHandler(
	verify sesVerifier,
	objects inboundObjectStore,
	subs inboundSubscriberStore,
	auditor *audit.Logger,
	now func() time.Time,
	logger *slog.Logger,
) *SESInboundHandler {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SESInboundHandler{verify: verify, objects: objects, subs: subs, auditor: auditor, now: now, log: logger}
}

// Notify handles POST /api/ses/inbound. See the package doc comment for the
// status-code contract this method implements.
func (h *SESInboundHandler) Notify(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSESInboundBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.log.Warn("ses_inbound: reading request body failed", "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	var msg sesnotify.Message
	if err := json.Unmarshal(body, &msg); err != nil {
		h.log.Warn("ses_inbound: request body is not a valid SNS envelope", "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := h.verify.Verify(r.Context(), &msg); err != nil {
		if errors.Is(err, sesnotify.ErrCertUnavailable) {
			h.log.Error("ses_inbound: could not verify (certificate unavailable)",
				"message_id", msg.MessageId, "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.log.Warn("ses_inbound: verification failed",
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
		h.log.Warn("ses_inbound: unrecognized message Type", "type", msg.Type, "message_id", msg.MessageId)
		w.WriteHeader(http.StatusOK)
	}
}

// handleSubscriptionConfirmation mirrors
// SESNotificationsHandler.handleSubscriptionConfirmation exactly (#0057's
// A12: the inbound-mail topic gets its own HTTPS subscription, which must
// be auto-confirmed the same way). Only reachable after Verify has already
// proven both the signature and the TopicArn allowlist match.
func (h *SESInboundHandler) handleSubscriptionConfirmation(w http.ResponseWriter, r *http.Request, msg *sesnotify.Message) {
	ctx := r.Context()
	if err := h.verify.FetchSubscribeURL(ctx, msg.SubscribeURL); err != nil {
		h.log.Error("ses_inbound: SubscriptionConfirmation SubscribeURL fetch failed",
			"message_id", msg.MessageId, "topic_arn", msg.TopicArn, "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	h.log.Info("ses_inbound: auto-confirmed SNS subscription",
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

// handleUnsubscribeConfirmation mirrors
// SESNotificationsHandler.handleUnsubscribeConfirmation: never fetches
// anything and never acts. This message means inbound mailto: unsubscribes
// are about to silently stop being delivered here.
func (h *SESInboundHandler) handleUnsubscribeConfirmation(w http.ResponseWriter, r *http.Request, msg *sesnotify.Message) {
	h.log.Error("ses_inbound: endpoint was UNSUBSCRIBED from the SNS topic — inbound mailto: unsubscribes will stop arriving",
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

// handleNotification is the Received-event path: resolve the S3 object key,
// fetch it, parse it, and either act on a match or leave it in place. See
// the package doc comment for the full decision table.
func (h *SESInboundHandler) handleNotification(w http.ResponseWriter, r *http.Request, msg *sesnotify.Message) {
	ctx := r.Context()

	ev, err := sesnotify.ParseSESEvent(msg.Message)
	if err != nil {
		h.log.Warn("ses_inbound: SES event payload is not valid JSON", "message_id", msg.MessageId, "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	key := ev.ObjectKey()
	if key == "" {
		h.log.Warn("ses_inbound: notification carried no resolvable S3 object key", "message_id", msg.MessageId)
		w.WriteHeader(http.StatusOK)
		return
	}

	raw, err := h.objects.Fetch(ctx, key)
	if err != nil {
		// Operational failure (network, permissions, the object briefly not
		// yet visible) — the object is still in S3, so retry is safe and is
		// the only thing standing between us and losing this request.
		h.log.Error("ses_inbound: fetching object from S3 failed", "key", key, "message_id", msg.MessageId, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	pm, err := inbound.Parse(raw)
	if err != nil {
		h.log.Warn("ses_inbound: parsing fetched message failed; leaving object for manual review",
			"key", key, "message_id", msg.MessageId, "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	if pm.AutoReply {
		h.log.Info("ses_inbound: ignoring auto-reply/bounce-shaped message; leaving object for manual review",
			"key", key, "message_id", msg.MessageId)
		w.WriteHeader(http.StatusOK)
		return
	}

	sub, matchedVia, err := h.match(ctx, pm)
	if err != nil {
		h.log.Error("ses_inbound: subscriber lookup failed", "key", key, "message_id", msg.MessageId, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if matchedVia == "" {
		h.log.Info("ses_inbound: no subject token or From: match; leaving object for manual review",
			"key", key, "message_id", msg.MessageId)
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := h.handleMatch(ctx, sub, matchedVia, key); err != nil {
		h.log.Error("ses_inbound: acting on match failed", "key", key, "subscriber_id", sub.ID, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// match implements PRD §6.5's precedence exactly: the subject token first
// (pm.Token, matched via manage_token — the same value #0034's one-click
// link carries), falling back to the From: address only when no token was
// present OR the token didn't resolve. matchedVia is "" when neither
// matched at all — the caller's cue to take the no-match path. The From:
// fallback is deliberately never tried when a token WAS present but simply
// didn't resolve to anything current (e.g. already rotated away) — a stale
// or forged token should not fall through to trusting an equally-forgeable
// From: header for the SAME message; see this issue's Notes on why From:
// stays the fallback used only in the token's total absence.
func (h *SESInboundHandler) match(ctx context.Context, pm inbound.ParsedMessage) (sub subscribers.Subscriber, matchedVia string, err error) {
	if pm.Token != "" {
		sub, err = h.subs.FindByManageToken(ctx, pm.Token)
		switch {
		case err == nil:
			return sub, "token", nil
		case errors.Is(err, subscribers.ErrNotFound):
			return subscribers.Subscriber{}, "", nil
		default:
			return subscribers.Subscriber{}, "", err
		}
	}

	if pm.From == "" {
		return subscribers.Subscriber{}, "", nil
	}
	sub, err = h.subs.FindByEmail(ctx, pm.From)
	switch {
	case err == nil:
		return sub, "from", nil
	case errors.Is(err, subscribers.ErrNotFound):
		return subscribers.Subscriber{}, "", nil
	default:
		return subscribers.Subscriber{}, "", err
	}
}

// handleMatch unsubscribes a matched subscriber and deletes the S3 object.
// Mirrors #0034's UnsubscribeHandler.Post exactly for the rotation/audit
// ordering (rotate before audit, both skipped on a complained no-op) — see
// that file's package doc comment for why the no-op check must come first.
// The object is deleted regardless of whether this was a real unsubscribe
// or a complained no-op: either way a subscriber was genuinely identified
// and correctly handled, which is what "processed" means here (see this
// file's package doc comment) — unlike the no-match/auto-reply/unparseable
// paths above, there is nothing left for a human to review.
func (h *SESInboundHandler) handleMatch(ctx context.Context, sub subscribers.Subscriber, matchedVia, key string) error {
	updated, err := h.subs.Unsubscribe(ctx, sub.ID, subscribers.SourceMailto, h.now())
	if err != nil {
		return err
	}

	noOp := updated.Status == subscribers.StatusComplained
	if !noOp {
		if _, err := h.subs.RotateManageToken(ctx, updated.ID, h.now()); err != nil {
			// The unsubscribe itself already committed; a failed rotation
			// is logged only, matching #0034's identical tradeoff — see
			// unsubscribe.go's Post.
			h.log.Error("ses_inbound: RotateManageToken failed", "subscriber_id", updated.ID, "err", err)
		}
		if h.auditor != nil {
			targetID := updated.ID
			h.auditor.Record(ctx, audit.Entry{
				Action:     audit.ActionSubscriberUnsubscribed,
				TargetType: audit.TargetSubscriber,
				TargetID:   &targetID,
				Metadata:   map[string]any{"source": subscribers.SourceMailto, "matched_via": matchedVia},
			})
		}
	}

	return h.objects.Delete(ctx, key)
}
