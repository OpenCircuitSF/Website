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
// # A redelivered message id is a no-op (#0501)
//
// #0058 commits the unsubscribe before deleting the S3 object, so a failed
// delete leaves the object in place and SNS redelivers the notification
// (#0501). Replay used to write a duplicate subscriber.unsubscribed audit
// row on the From: path (and could still re-run the whole match on the
// token path when the first delivery's token rotation had failed). The fix
// stamps the SES message id — notification.mail.messageId, carried out of
// the already-parsed event payload — on the audit row it writes, and
// handleMatch checks for that id BEFORE anything else: a message id that
// already has its unsubscribed row is a no-op (skip the unsubscribe, the
// rotation, and the audit row), leaving only the object delete to run.
// The commit-before-delete ordering is untouched — the check is a read
// that happens before both, and it is described with handleMatch below.
//
// # Parked messages get a durable record, not just a log line (#0499)
//
// #0058 criterion 5 says an unmatched message is "logged ... left in place
// for manual review", but a log line on the box is not itself a review
// mechanism, and the object it describes is gone in 30 days regardless.
// Each of the three left-in-place outcomes named just above additionally
// calls recordParked, which writes an audit.ActionInboundMailParked row
// (audit_log has no expiry, unlike the S3 object) carrying the S3 key, the
// SNS message id, the From: address and Subject when known, and why the
// message was left — never the message body. An admin finds these at GET
// /admin/audit filtered to target_type=inbound_mail, with no SSH access to
// the box required; see docs/unsubscribe.md. recordParked is a no-op when
// h.auditor is nil, and audit.Logger.Record itself logs-and-swallows any
// insert failure — a failure to WRITE this record must never turn this
// handler's 200 into a 500, since that would make SNS redeliver the SAME
// message and loop. A fourth caller exists since #0500: an S3 fetch
// answering NoSuchKey parks the NOTIFICATION (there is no object to leave
// in place, but the failed request is exactly what #0499's record exists
// to make queryable) with reason object_missing.
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
//     resolvable object key, an S3 fetch answering NoSuchKey (#0500: the key
//     names an object that does not exist, and retrying cannot create it),
//     an unparseable fetched message, an auto-reply, or no match at all ->
//     200. None of these is fixed by SNS retrying the same notification.
//   - Verify fails because the certificate could not be fetched
//     (errors.Is(err, sesnotify.ErrCertUnavailable)) -> 500, matching
//     ses_notifications.go exactly: verification could not be performed.
//   - Verify fails for any other reason -> 403, matching ses_notifications.go.
//   - Fetching the object from S3 fails with anything OTHER than NoSuchKey
//     (network, permissions — the NoSuchKey case is parked above), or the
//     subscriber-store lookup/mutation on a genuine match, fails
//     operationally -> 500: the object is still in S3 (never deleted before
//     this point), so a retry is safe and is the only thing standing between
//     us and losing a real unsubscribe request.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
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

	// observeShape is the TEMPORARY #0500 observation gate — see
	// sesInboundObservationLogEnv and logReceiptActionShape. Read once
	// here in the constructor (the user-approved shape: a single boolean
	// env var, no config plumbing, so the whole mechanism is one
	// delete-shaped block); default OFF.
	observeShape bool
}

// sesInboundObservationLogEnv enables the TEMPORARY #0500 observation log
// (logReceiptActionShape): the first live inbound notification is the only
// oracle for whether SES writes receipt.action.objectKey fully prefixed and
// whether objectKeyPrefix appears at all, and no other oracle on the box
// could recover that shape (see the issue's Observation section). Go boolean
// syntax ("1", "true", ...) enables it; unset, empty, or unparseable leaves
// it OFF, so production is silent until an operator deliberately sets it.
// This constant, the flag, the level passthrough below, and the log line
// itself all die together once the observation is recorded in the issue —
// removal is #0500's acceptance path.
const sesInboundObservationLogEnv = "SES_INBOUND_OBSERVATION_LOG"

// sesInboundDebugHandler is the TEMPORARY #0500 level passthrough installed
// by NewSESInboundHandler when the observation flag is on. The service
// logger is slog.Default(), whose floor is slog.Info — a literal Debug line
// would be dropped on the box even with the flag set, so the observation
// pass would see nothing and could misread that silence as "the payload
// carried no receipt action". Enabled answers true for every level, raising
// just this handler's ceiling to Debug; Handle, WithAttrs and WithGroup
// delegate unchanged, so every other line keeps the wrapped handler's own
// format and destination.
type sesInboundDebugHandler struct{ inner slog.Handler }

func (h sesInboundDebugHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h sesInboundDebugHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.inner.Handle(ctx, r)
}

func (h sesInboundDebugHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return sesInboundDebugHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h sesInboundDebugHandler) WithGroup(name string) slog.Handler {
	return sesInboundDebugHandler{inner: h.inner.WithGroup(name)}
}

// NewSESInboundHandler constructs a SESInboundHandler. A nil auditor
// disables the audit write on a real match; a nil logger falls back to
// slog.Default(), matching this package's existing convention.
//
// When the SES_INBOUND_OBSERVATION_LOG env var is truthy, the returned
// handler additionally logs one DEBUG-level, explicitly-labeled shape
// observation per inbound notification (#0500 — TEMPORARY, removed once the
// issue's Observation section records the real payload). The default is OFF.
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
	// TEMPORARY (#0500): read once at construction, per the user-approved
	// plan — no config plumbing, so the whole mechanism is one removable
	// block. Unparseable values fail safe to OFF (ParseBool's error is
	// deliberately discarded: an operator typo must not crash the endpoint,
	// and a silently-off observation is recoverable by reading this comment).
	observe, _ := strconv.ParseBool(os.Getenv(sesInboundObservationLogEnv))
	if observe {
		logger = slog.New(sesInboundDebugHandler{inner: logger.Handler()})
		// #0500: the issue citation stays in this comment, not in the
		// admin-facing string itself (the citation guard's rule).
		logger.Info("ses_inbound: OBSERVATION log ENABLED — temporary, removed with the observation code",
			"env", sesInboundObservationLogEnv)
	}
	return &SESInboundHandler{verify: verify, objects: objects, subs: subs, auditor: auditor, now: now, log: logger, observeShape: observe}
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

	// TEMPORARY (#0500): the shape observation — removed with the rest of
	// the mechanism once the issue's Observation section records the real
	// payload. Silent unless the env flag was on at construction.
	h.logReceiptActionShape(msg, ev)

	key := ev.ObjectKey()
	if key == "" {
		h.log.Warn("ses_inbound: notification carried no resolvable S3 object key", "message_id", msg.MessageId)
		w.WriteHeader(http.StatusOK)
		return
	}

	raw, err := h.objects.Fetch(ctx, key)
	if err != nil {
		if errors.Is(err, inbound.ErrNoSuchKey) {
			// #0500 criterion 4: S3 reported the key does not exist. SES
			// writes the object BEFORE publishing this notification and S3
			// reads are strongly consistent, so a missing object is not a
			// transient condition a retry could fix — and SNS's redelivery
			// policy would retry the same doomed notification to exhaustion
			// before dropping it. Park instead: 200 (no redelivery loop)
			// plus the #0499-style durable audit row (reason
			// object_missing), which is what makes the failure queryable
			// rather than a log line that rotates away. Nothing was
			// fetched, so no From:/Subject exists to record.
			h.log.Warn("ses_inbound: S3 reported the object does not exist; parking instead of retrying",
				"key", key, "message_id", msg.MessageId, "err", err)
			h.recordParked(ctx, key, msg.MessageId, parkedReasonObjectMissing, "", "")
			w.WriteHeader(http.StatusOK)
			return
		}
		// Operational failure (network, permissions — the missing-object
		// case above is handled separately) — the object is still in S3, so
		// retry is safe and is the only thing standing between us and
		// losing this request.
		h.log.Error("ses_inbound: fetching object from S3 failed", "key", key, "message_id", msg.MessageId, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	pm, err := inbound.Parse(raw)
	if err != nil {
		h.log.Warn("ses_inbound: parsing fetched message failed; leaving object for manual review",
			"key", key, "message_id", msg.MessageId, "err", err)
		h.recordParked(ctx, key, msg.MessageId, parkedReasonUnparseable, "", "")
		w.WriteHeader(http.StatusOK)
		return
	}

	if pm.AutoReply {
		h.log.Info("ses_inbound: ignoring auto-reply/bounce-shaped message; leaving object for manual review",
			"key", key, "message_id", msg.MessageId)
		h.recordParked(ctx, key, msg.MessageId, parkedReasonAutoReply, pm.From, pm.Subject)
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
		h.recordParked(ctx, key, msg.MessageId, noMatchReason(pm), pm.From, pm.Subject)
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := h.handleMatch(ctx, sub, matchedVia, key, ev.Mail.MessageID); err != nil {
		h.log.Error("ses_inbound: acting on match failed", "key", key, "subscriber_id", sub.ID, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// logReceiptActionShape is the TEMPORARY #0500 observation log: one
// explicitly-labeled DEBUG line per inbound notification, recording the
// parsed receipt action's shape — whether objectKey is present in the
// payload and its full value, whether objectKeyPrefix appears at all, and
// the bucket — plus the SNS envelope's message id, matching every other log
// line in this file. It never logs the raw notification body, only these
// envelope-level fields. This exists because criterion 1 of #0500 needs the
// real payload's shape and every oracle on the box came up empty (service
// logs, the audit row, CloudWatch — see the issue's Observation section);
// the whole mechanism dies once that observation is recorded.
//
// Presence is read by re-parsing msg.Message into the raw probe below
// rather than from the already-parsed event: an unmarshalled string field
// cannot distinguish a field that is absent from one that is present but
// empty, and that distinction is exactly what #0500 asks the observation to
// settle. The same Message string just parsed successfully into ev, so the
// probe cannot realistically fail on real bytes; on the impossible path the
// probe stays zero and both fields report absent.
func (h *SESInboundHandler) logReceiptActionShape(msg *sesnotify.Message, ev sesnotify.SESEvent) {
	if !h.observeShape {
		return
	}
	var probe struct {
		Receipt *struct {
			Action struct {
				ObjectKey       json.RawMessage `json:"objectKey"`
				ObjectKeyPrefix json.RawMessage `json:"objectKeyPrefix"`
			} `json:"action"`
		} `json:"receipt"`
	}
	_ = json.Unmarshal([]byte(msg.Message), &probe)

	hasReceipt := ev.Receipt != nil
	var objectKey, objectKeyPrefix, bucket string
	if hasReceipt {
		objectKey = ev.Receipt.Action.ObjectKey
		objectKeyPrefix = ev.Receipt.Action.ObjectKeyPrefix
		bucket = ev.Receipt.Action.BucketName
	}
	// #0500: the issue citation stays in this comment, not in the
	// admin-facing string itself (the citation guard's rule).
	h.log.Debug("ses_inbound: OBSERVATION parsed receipt action shape",
		"message_id", msg.MessageId,
		"receipt_present", hasReceipt,
		"object_key_present", probe.Receipt != nil && probe.Receipt.Action.ObjectKey != nil,
		"object_key", objectKey,
		"object_key_prefix_present", probe.Receipt != nil && probe.Receipt.Action.ObjectKeyPrefix != nil,
		"object_key_prefix", objectKeyPrefix,
		"bucket", bucket,
	)
}

// Reason values recordParked writes into its audit row's metadata.reason —
// see that function's doc comment and noMatchReason below for which
// left-in-place outcome produces which value; object_missing is the fourth
// (#0500's, for a notification whose key S3 reports as absent — not a
// left-in-place outcome at all, since there was never an object).
const (
	parkedReasonUnparseable = "unparseable"
	parkedReasonAutoReply   = "auto_reply"
	parkedReasonTokenStale  = "token_not_found"
	parkedReasonFromUnknown = "from_not_found"
	parkedReasonNoSignal    = "no_identifying_signal"
	// parkedReasonObjectMissing marks the NoSuchKey fetch outcome (#0500
	// criterion 4): the notification named a key S3 says does not exist, so
	// instead of retrying to exhaustion the request is parked with reason
	// object_missing — the row carries the key and message id, and neither
	// from nor subject, since nothing was ever fetched or parsed.
	parkedReasonObjectMissing = "object_missing"
)

// recordParked writes the durable audit record #0499 added for a message
// handleNotification is about to leave in S3 for manual review — see this
// file's package doc comment ("Parked messages get a durable record, not
// just a log line") for why this exists and where an admin finds the
// result. #0500 added a fourth caller: an S3 fetch answering NoSuchKey,
// where nothing was left in S3 because nothing was ever there — the record
// documents the failed request instead. from and subject are passed through
// as-is and may be "" (an unparseable message has neither, and neither was
// ever fetched on the object_missing path); the metadata omits an empty one
// rather than storing an empty string, so a reader can distinguish "known
// to be blank" from "not available for this outcome".
//
// A nil h.auditor is a no-op, matching every other audit call site in this
// file. audit.Logger.Record itself logs-and-swallows any insert failure, so
// this can never turn the caller's 200 into a 500 — see the constraint
// named in the package doc comment.
func (h *SESInboundHandler) recordParked(ctx context.Context, key, messageID, reason, from, subject string) {
	if h.auditor == nil {
		return
	}
	meta := map[string]any{
		"key":        key,
		"message_id": messageID,
		"reason":     reason,
	}
	if from != "" {
		meta["from"] = from
	}
	if subject != "" {
		meta["subject"] = subject
	}
	h.auditor.Record(ctx, audit.Entry{
		Action:     audit.ActionInboundMailParked,
		TargetType: audit.TargetInboundMail,
		Metadata:   meta,
	})
}

// noMatchReason explains, for recordParked's metadata.reason, why match
// returned matchedVia == "" — reconstructed from pm alone (rather than
// widening match's own return values) since match's three no-match
// branches map onto pm.Token/pm.From one-to-one: a present Token that
// didn't resolve never falls back to From (match's own stale-token rule,
// see its doc comment), so its absence here means Token was either absent
// or, if present, already handled by the first case below.
func noMatchReason(pm inbound.ParsedMessage) string {
	switch {
	case pm.Token != "":
		return parkedReasonTokenStale
	case pm.From == "":
		return parkedReasonNoSignal
	default:
		return parkedReasonFromUnknown
	}
}

// match implements PRD §6.5's precedence exactly: the subject token first
// (pm.Token, matched via manage_token — the same value #0034's one-click
// link carries), falling back to the From: address only when no token was
// present at all. matchedVia is "" when neither matched at all — the
// caller's cue to take the no-match path. The From: fallback is
// deliberately never tried when a token WAS present but simply didn't
// resolve to anything current (e.g. already rotated away) — a stale or
// forged token should not fall through to trusting an equally-forgeable
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
//
// sesMessageID is the inbound message's SES message id
// (notification.mail.messageId, threaded from the event payload this file
// already parsed — #0501). Before anything else it drives the redelivery
// check: an audit row for this id and action already existing means the
// FIRST delivery of this same message already unsubscribed its sender
// (#0058's commit-before-delete ordering left the object behind when its
// delete failed, so SNS is redelivering) — the correct response is a
// no-op: skip the unsubscribe, the rotation, and the audit row, and only
// attempt the object delete again, which is the second chance that failed
// delete needed. A nil auditor, or an empty id (a payload without
// mail.messageId cannot key the check), falls back to the pre-#0501
// behavior unchanged. A failed check is failed-open — the ordinary path
// runs, degrading to the duplicate audit row #0501 exists to prevent
// rather than ever blocking or retrying a real unsubscribe.
func (h *SESInboundHandler) handleMatch(ctx context.Context, sub subscribers.Subscriber, matchedVia, key, sesMessageID string) error {
	if h.auditor != nil {
		if seen, err := h.auditor.MessageProcessed(ctx, audit.ActionSubscriberUnsubscribed, sesMessageID); err != nil {
			h.log.Warn("ses_inbound: redelivery check failed; processing normally",
				"key", key, "message_id", sesMessageID, "err", err)
		} else if seen {
			h.log.Info("ses_inbound: redelivery of an already-processed message id; no-op",
				"key", key, "message_id", sesMessageID)
			return h.objects.Delete(ctx, key)
		}
	}

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
				MessageID:  sesMessageID,
			})
		}
	}

	return h.objects.Delete(ctx, key)
}
