package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/sesnotify"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

const inboundTestTopicArn = "arn:aws:sns:us-west-2:123456789012:opencircuit-inbound-mail"

// fakeInboundVerifier is a minimal sesVerifier double. #0037's real
// signature verification is already proven by internal/sesnotify's own
// tests and reused end-to-end by ses_notifications_test.go's fixture; this
// file's job is #0058's own logic (S3 fetch, net/mail parsing, token/From
// matching, auto-reply detection, act-or-leave-in-place), so Verify's
// outcome is injected directly rather than re-deriving a signed envelope.
type fakeInboundVerifier struct {
	verifyErr       error
	certUnavailable bool

	subscribeURLErr  error
	subscribeURLHits int
}

func (f *fakeInboundVerifier) Verify(ctx context.Context, m *sesnotify.Message) error {
	if f.certUnavailable {
		return fmt.Errorf("wrap: %w", sesnotify.ErrCertUnavailable)
	}
	return f.verifyErr
}

func (f *fakeInboundVerifier) FetchSubscribeURL(ctx context.Context, subscribeURL string) error {
	f.subscribeURLHits++
	return f.subscribeURLErr
}

// fakeInboundObjectStore is an in-memory inboundObjectStore double — no S3
// client, no network, matching CLAUDE.md §10's "develop against mocks" for
// #0057's not-yet-created bucket.
type fakeInboundObjectStore struct {
	objects   map[string][]byte
	fetchErr  error
	deleteErr error
	deleted   []string
}

func (f *fakeInboundObjectStore) Fetch(ctx context.Context, key string) ([]byte, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	body, ok := f.objects[key]
	if !ok {
		return nil, errors.New("no such key")
	}
	return body, nil
}

func (f *fakeInboundObjectStore) Delete(ctx context.Context, key string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, key)
	delete(f.objects, key)
	return nil
}

// inboundRawMessage builds a minimal RFC 5322 message body for these tests —
// mirrors internal/inbound's own test helper, duplicated locally since that
// package's is unexported and this file is proving the HANDLER's behavior,
// not internal/inbound.Parse's (already covered by that package's own
// tests).
func inboundRawMessage(headers map[string]string, body string) []byte {
	raw := ""
	for k, v := range headers {
		raw += k + ": " + v + "\r\n"
	}
	raw += "\r\n" + body
	return []byte(raw)
}

// inboundNotificationBody builds the SNS envelope body Notify expects: a
// "Received" SES event (the S3 action's own TopicArn notification, #0057's
// plan) naming objectKey, wrapped in the standard SNS Message envelope.
func inboundNotificationBody(t *testing.T, objectKey string) []byte {
	t.Helper()
	inner := map[string]any{
		"notificationType": "Received",
		"mail":             map[string]any{"messageId": "ses-msg-1"},
		"receipt": map[string]any{
			"action": map[string]any{
				"type":       "S3",
				"bucketName": "opencircuitsf-inbound",
				"objectKey":  objectKey,
			},
		},
	}
	innerBytes, err := json.Marshal(inner)
	if err != nil {
		t.Fatalf("marshal inner SES event: %v", err)
	}
	env := sesnotify.Message{
		Type:      sesnotify.TypeNotification,
		MessageId: "sns-msg-1",
		TopicArn:  inboundTestTopicArn,
		Message:   string(innerBytes),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	envBytes, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal SNS envelope: %v", err)
	}
	return envBytes
}

func doPostSESInbound(h *SESInboundHandler, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/ses/inbound", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Notify(rr, req)
	return rr
}

func TestSESInboundHandler_SubjectTokenMatch_UnsubscribesRotatesAuditsAndDeletes(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	originalToken := created.ManageToken

	raw := inboundRawMessage(map[string]string{
		"From":    "spoofed-someone-else@example.com",
		"Subject": "unsubscribe:" + originalToken,
	}, "please unsubscribe me\r\n")
	objects := &fakeInboundObjectStore{objects: map[string][]byte{"unsubscribe/msg-1": raw}}
	verifier := &fakeInboundVerifier{}
	auditor := audit.New(pool)
	h := NewSESInboundHandler(verifier, objects, subs, auditor, nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-1"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	after, err := subs.FindByEmail(ctx, created.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if after.Status != subscribers.StatusUnsubscribed {
		t.Errorf("Status = %q, want %q", after.Status, subscribers.StatusUnsubscribed)
	}
	if after.UnsubscribeSource == nil || *after.UnsubscribeSource != subscribers.SourceMailto {
		t.Errorf("UnsubscribeSource = %v, want %q", after.UnsubscribeSource, subscribers.SourceMailto)
	}
	if after.ManageToken == originalToken {
		t.Error("ManageToken was not rotated")
	}

	// The token match must win even though a plausible-looking (but
	// unrelated) From: address is present — the subject token is the
	// preferred path (PRD §6.5; this issue's Notes).
	if _, err := subs.FindByEmail(ctx, "spoofed-someone-else@example.com"); !errors.Is(err, subscribers.ErrNotFound) {
		t.Errorf("spoofed From: address must not have been created/matched, got err=%v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 1 {
		t.Errorf("audit_log rows for subscriber.unsubscribed = %d, want 1", count)
	}
	var source, matchedVia string
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'source', metadata->>'matched_via' FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&source, &matchedVia); err != nil {
		t.Fatalf("query audit_log metadata: %v", err)
	}
	if source != subscribers.SourceMailto {
		t.Errorf("audit metadata source = %q, want %q", source, subscribers.SourceMailto)
	}
	if matchedVia != "token" {
		t.Errorf("audit metadata matched_via = %q, want %q", matchedVia, "token")
	}

	if len(objects.deleted) != 1 || objects.deleted[0] != "unsubscribe/msg-1" {
		t.Errorf("deleted = %v, want [unsubscribe/msg-1] — a processed message must be deleted from S3", objects.deleted)
	}
}

func TestSESInboundHandler_FromAddressFallback_Match(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	raw := inboundRawMessage(map[string]string{
		"From":    created.Email,
		"Subject": "please take me off your list",
	}, "no token here\r\n")
	objects := &fakeInboundObjectStore{objects: map[string][]byte{"unsubscribe/msg-2": raw}}
	verifier := &fakeInboundVerifier{}
	auditor := audit.New(pool)
	h := NewSESInboundHandler(verifier, objects, subs, auditor, nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-2"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	after, err := subs.FindByEmail(ctx, created.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if after.Status != subscribers.StatusUnsubscribed {
		t.Errorf("Status = %q, want %q", after.Status, subscribers.StatusUnsubscribed)
	}

	var matchedVia string
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'matched_via' FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&matchedVia); err != nil {
		t.Fatalf("query audit_log metadata: %v", err)
	}
	if matchedVia != "from" {
		t.Errorf("audit metadata matched_via = %q, want %q", matchedVia, "from")
	}
	if len(objects.deleted) != 1 {
		t.Errorf("deleted = %v, want exactly one object deleted", objects.deleted)
	}
}

// TestSESInboundHandler_StaleTokenDoesNotFallBackToFrom pins match's
// security-relevant rule: a subject token that is present but does not
// resolve (stale or forged) must not fall through to trusting the From:
// address on the SAME message, even when From: is the subscriber's own,
// genuine email address. Falling through here would let a forged or
// out-of-date token in the Subject line be silently upgraded to a
// successful unsubscribe via an equally-forgeable header.
func TestSESInboundHandler_StaleTokenDoesNotFallBackToFrom(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// A token that does NOT resolve to anything current — the subscriber's
	// own manage token with a character appended — alongside a From: that
	// IS this subscriber's own, genuine address.
	raw := inboundRawMessage(map[string]string{
		"From":    created.Email,
		"Subject": "unsubscribe:" + created.ManageToken + "x",
	}, "please unsubscribe me\r\n")
	objects := &fakeInboundObjectStore{objects: map[string][]byte{"unsubscribe/msg-stale": raw}}
	verifier := &fakeInboundVerifier{}
	h := NewSESInboundHandler(verifier, objects, subs, audit.New(pool), nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-stale"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	after, err := subs.FindByEmail(ctx, created.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if after.Status != subscribers.StatusActive {
		t.Errorf("Status = %q, want %q — a stale token must not fall back to the From: address", after.Status, subscribers.StatusActive)
	}
	if len(objects.deleted) != 0 {
		t.Errorf("deleted = %v, want none — a stale token is left for manual review, not deleted", objects.deleted)
	}
	if _, ok := objects.objects["unsubscribe/msg-stale"]; !ok {
		t.Error("object was removed from the store on a stale-token no-match — it must be left in place")
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 0 {
		t.Errorf("audit_log rows for subscriber.unsubscribed = %d, want 0", count)
	}

	// #0499: this is the "stale token" no-match reason — the token was
	// present but did not resolve, so it must never be reported as
	// "from_not_found" (which would misleadingly suggest the From: address
	// was the problem, when the whole point of this test is that From: was
	// valid and deliberately not tried).
	var reason string
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'reason' FROM audit_log WHERE action = $1 AND metadata->>'key' = $2`,
		audit.ActionInboundMailParked, "unsubscribe/msg-stale",
	).Scan(&reason); err != nil {
		t.Fatalf("query audit_log for parked record: %v", err)
	}
	if reason != "token_not_found" {
		t.Errorf("metadata reason = %q, want %q", reason, "token_not_found")
	}
}

func TestSESInboundHandler_NoMatch_LeavesObjectInPlace(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()

	fromAddr := journeyUniqueEmail(t) // guaranteed not to exist as a subscriber
	raw := inboundRawMessage(map[string]string{
		"From":    fromAddr,
		"Subject": "please stop emailing me",
	}, "body\r\n")
	objects := &fakeInboundObjectStore{objects: map[string][]byte{"unsubscribe/msg-3": raw}}
	verifier := &fakeInboundVerifier{}
	h := NewSESInboundHandler(verifier, objects, subs, audit.New(pool), nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-3"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if len(objects.deleted) != 0 {
		t.Errorf("deleted = %v, want none — a no-match object must be left for manual review", objects.deleted)
	}
	if _, ok := objects.objects["unsubscribe/msg-3"]; !ok {
		t.Error("object was removed from the store on a no-match — it must be left in place")
	}

	// #0499: a no-match must leave a durable audit_log record, not only the
	// log line — this is what lets the object's own 30-day S3 expiry
	// (#0057 A5) happen without also destroying the only evidence the
	// request ever arrived.
	var targetType, reason, from, subject string
	if err := pool.QueryRow(ctx,
		`SELECT target_type, metadata->>'reason', metadata->>'from', metadata->>'subject'
		   FROM audit_log WHERE action = $1 AND metadata->>'key' = $2`,
		audit.ActionInboundMailParked, "unsubscribe/msg-3",
	).Scan(&targetType, &reason, &from, &subject); err != nil {
		t.Fatalf("query audit_log for parked record: %v", err)
	}
	if targetType != audit.TargetInboundMail {
		t.Errorf("target_type = %q, want %q", targetType, audit.TargetInboundMail)
	}
	if reason != "from_not_found" {
		t.Errorf("metadata reason = %q, want %q", reason, "from_not_found")
	}
	if from != fromAddr {
		t.Errorf("metadata from = %q, want %q", from, fromAddr)
	}
	if subject != "please stop emailing me" {
		t.Errorf("metadata subject = %q, want %q", subject, "please stop emailing me")
	}
}

// TestSESInboundHandler_NoTokenNoFrom_RecordsParkedWithNoSignalReason pins
// noMatchReason's third branch: neither a subject token nor a parseable
// From: address at all (as opposed to TestSESInboundHandler_NoMatch_
// LeavesObjectInPlace's "from_not_found", where a From: was present but
// didn't resolve to a subscriber).
func TestSESInboundHandler_NoTokenNoFrom_RecordsParkedWithNoSignalReason(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()

	raw := inboundRawMessage(map[string]string{
		"Subject": "hello",
	}, "no From: header at all\r\n")
	objects := &fakeInboundObjectStore{objects: map[string][]byte{"unsubscribe/msg-3b": raw}}
	h := NewSESInboundHandler(&fakeInboundVerifier{}, objects, subs, audit.New(pool), nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-3b"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	var reason string
	var hasFrom bool
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'reason', metadata ? 'from'
		   FROM audit_log WHERE action = $1 AND metadata->>'key' = $2`,
		audit.ActionInboundMailParked, "unsubscribe/msg-3b",
	).Scan(&reason, &hasFrom); err != nil {
		t.Fatalf("query audit_log for parked record: %v", err)
	}
	if reason != "no_identifying_signal" {
		t.Errorf("metadata reason = %q, want %q", reason, "no_identifying_signal")
	}
	if hasFrom {
		t.Error(`metadata has key "from", want it omitted — the message carried no From: header`)
	}
}

func TestSESInboundHandler_AutoReply_IgnoredEvenWithAValidToken(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// A valid token is present (an out-of-office bouncing off the exact
	// campaign that carried the mailto: unsubscribe form, quoting the
	// original subject) — it must still be ignored because Auto-Submitted
	// says this was generated automatically.
	raw := inboundRawMessage(map[string]string{
		"From":           created.Email,
		"Subject":        "Automatic reply: unsubscribe:" + created.ManageToken,
		"Auto-Submitted": "auto-replied",
	}, "I am out of the office\r\n")
	objects := &fakeInboundObjectStore{objects: map[string][]byte{"unsubscribe/msg-4": raw}}
	verifier := &fakeInboundVerifier{}
	h := NewSESInboundHandler(verifier, objects, subs, audit.New(pool), nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-4"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	after, err := subs.FindByEmail(ctx, created.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if after.Status != subscribers.StatusActive {
		t.Errorf("Status = %q, want %q — an auto-reply must not unsubscribe the sender", after.Status, subscribers.StatusActive)
	}
	if len(objects.deleted) != 0 {
		t.Errorf("deleted = %v, want none — an auto-reply is left for manual review, not deleted", objects.deleted)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 0 {
		t.Errorf("audit_log rows for subscriber.unsubscribed = %d, want 0", count)
	}

	// #0499: an auto-reply-classified message is exactly the shape #0498
	// warned can carry a genuine subscriber's own address (an
	// out-of-office bouncing off a real campaign), so it must leave a
	// parked record too — reason "auto_reply", distinguishing it from a
	// no-match at the metadata level.
	var reason, from, subject string
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'reason', metadata->>'from', metadata->>'subject'
		   FROM audit_log WHERE action = $1 AND metadata->>'key' = $2`,
		audit.ActionInboundMailParked, "unsubscribe/msg-4",
	).Scan(&reason, &from, &subject); err != nil {
		t.Fatalf("query audit_log for parked record: %v", err)
	}
	if reason != "auto_reply" {
		t.Errorf("metadata reason = %q, want %q", reason, "auto_reply")
	}
	if from != created.Email {
		t.Errorf("metadata from = %q, want %q", from, created.Email)
	}
	if subject != "Automatic reply: unsubscribe:"+created.ManageToken {
		t.Errorf("metadata subject = %q, want %q", subject, "Automatic reply: unsubscribe:"+created.ManageToken)
	}
}

// TestSESInboundHandler_PrecedenceList_HumanReplyStillUnsubscribes pins
// #0498's fix: a message whose only conventional auto-reply signal is
// Precedence: list must NOT be treated as machine-generated, because that
// header only marks mail as having passed through a list manager (a group
// alias, a corporate distribution list, a mailing-list relay) and says
// nothing about whether a person wrote it. A human replying to an Open
// Circuit campaign through such a relay, with a valid unsubscribe token,
// must still be unsubscribed — the exact scenario #0498's Description
// describes as silently parking a legitimate request.
func TestSESInboundHandler_PrecedenceList_HumanReplyStillUnsubscribes(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// The message's ONLY conventional auto-reply signal is Precedence:
	// list — a corporate distribution list relaying a genuine reply, not an
	// out-of-office or a bounce. No Auto-Submitted, no X-Autoreply, no null
	// Return-Path, no delivery-status Content-Type.
	raw := inboundRawMessage(map[string]string{
		"From":       created.Email,
		"Subject":    "unsubscribe:" + created.ManageToken,
		"Precedence": "list",
	}, "please take me off this list\r\n")
	objects := &fakeInboundObjectStore{objects: map[string][]byte{"unsubscribe/msg-precedence-list": raw}}
	verifier := &fakeInboundVerifier{}
	auditor := audit.New(pool)
	h := NewSESInboundHandler(verifier, objects, subs, auditor, nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-precedence-list"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	after, err := subs.FindByEmail(ctx, created.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if after.Status != subscribers.StatusUnsubscribed {
		t.Errorf("Status = %q, want %q — Precedence: list alone must not be read as an auto-reply", after.Status, subscribers.StatusUnsubscribed)
	}
	if after.UnsubscribeSource == nil || *after.UnsubscribeSource != subscribers.SourceMailto {
		t.Errorf("UnsubscribeSource = %v, want %q", after.UnsubscribeSource, subscribers.SourceMailto)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 1 {
		t.Errorf("audit_log rows for subscriber.unsubscribed = %d, want 1", count)
	}
	var source string
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'source' FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&source); err != nil {
		t.Fatalf("query audit_log metadata: %v", err)
	}
	if source != subscribers.SourceMailto {
		t.Errorf("audit metadata source = %q, want %q", source, subscribers.SourceMailto)
	}

	if len(objects.deleted) != 1 || objects.deleted[0] != "unsubscribe/msg-precedence-list" {
		t.Errorf("deleted = %v, want [unsubscribe/msg-precedence-list] — a processed message must be deleted from S3", objects.deleted)
	}
}

func TestSESInboundHandler_ComplainedMatch_NoOpButStillDeletesObject(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := subs.MarkComplained(ctx, created.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}
	complained, err := subs.FindByEmail(ctx, created.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}

	raw := inboundRawMessage(map[string]string{
		"From":    "irrelevant@example.com",
		"Subject": "unsubscribe:" + complained.ManageToken,
	}, "body\r\n")
	objects := &fakeInboundObjectStore{objects: map[string][]byte{"unsubscribe/msg-5": raw}}
	h := NewSESInboundHandler(&fakeInboundVerifier{}, objects, subs, audit.New(pool), nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-5"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	after, err := subs.FindByEmail(ctx, created.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if after.Status != subscribers.StatusComplained {
		t.Errorf("Status = %q, want %q — a complained row must stay locked", after.Status, subscribers.StatusComplained)
	}
	if after.ManageToken != complained.ManageToken {
		t.Error("ManageToken was rotated on a complained no-op — it must not be")
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 0 {
		t.Errorf("audit_log rows for subscriber.unsubscribed = %d, want 0 — a complained no-op must not write one", count)
	}

	// This is still "processed": a genuine subscriber was identified and
	// correctly handled (do nothing to the row), so nothing is left for
	// manual review — see ses_inbound.go's package doc comment.
	if len(objects.deleted) != 1 {
		t.Errorf("deleted = %v, want the object deleted even on a complained no-op match", objects.deleted)
	}
}

func TestSESInboundHandler_VerifyFails_Returns403AndNeverFetches(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	objects := &fakeInboundObjectStore{objects: map[string][]byte{"unsubscribe/msg-6": []byte("irrelevant")}}
	h := NewSESInboundHandler(&fakeInboundVerifier{verifyErr: errors.New("bad signature")}, objects, subs, nil, nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-6"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if len(objects.deleted) != 0 {
		t.Error("object must never be fetched/deleted when verification fails")
	}
}

func TestSESInboundHandler_CertUnavailable_Returns500(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	objects := &fakeInboundObjectStore{}
	h := NewSESInboundHandler(&fakeInboundVerifier{certUnavailable: true}, objects, subs, nil, nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-7"))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (SNS should retry)", rr.Code)
	}
}

func TestSESInboundHandler_UnresolvableObjectKey_Returns200(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	objects := &fakeInboundObjectStore{}
	h := NewSESInboundHandler(&fakeInboundVerifier{}, objects, subs, nil, nil, nil)

	env := sesnotify.Message{
		Type:      sesnotify.TypeNotification,
		MessageId: "sns-msg-no-key",
		TopicArn:  inboundTestTopicArn,
		Message:   `{"notificationType":"Received","receipt":{"action":{"type":"S3"}}}`,
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rr := doPostSESInbound(h, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (nothing to retry)", rr.Code)
	}
}

func TestSESInboundHandler_FetchError_Returns500(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	objects := &fakeInboundObjectStore{fetchErr: errors.New("s3 unavailable")}
	h := NewSESInboundHandler(&fakeInboundVerifier{}, objects, subs, nil, nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-8"))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (object still in S3; safe to retry)", rr.Code)
	}
}

func TestSESInboundHandler_UnparseableMessage_Returns200AndLeavesObject(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	objects := &fakeInboundObjectStore{objects: map[string][]byte{"unsubscribe/msg-9": []byte("not a valid RFC 5322 message")}}
	// A nil auditor here doubles as #0499's nil-safety proof: recordParked
	// must be a no-op rather than a nil-pointer panic when no auditor is
	// configured (NewSESInboundHandler's own doc comment says a nil
	// auditor "disables the audit write").
	h := NewSESInboundHandler(&fakeInboundVerifier{}, objects, subs, nil, nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-9"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(objects.deleted) != 0 {
		t.Error("an unparseable message must be left in place, not deleted")
	}
}

// TestSESInboundHandler_UnparseableMessage_RecordsParkedWithoutFromOrSubject
// pins #0499's third left-in-place outcome: net/mail failed before any
// field could be extracted at all, so the parked record still exists (key,
// message id, reason) but genuinely has neither a From: nor a Subject to
// report — recordParked must omit those metadata keys rather than store
// empty strings (see its own doc comment).
func TestSESInboundHandler_UnparseableMessage_RecordsParkedWithoutFromOrSubject(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()
	objects := &fakeInboundObjectStore{objects: map[string][]byte{"unsubscribe/msg-9b": []byte("not a valid RFC 5322 message")}}
	h := NewSESInboundHandler(&fakeInboundVerifier{}, objects, subs, audit.New(pool), nil, nil)

	rr := doPostSESInbound(h, inboundNotificationBody(t, "unsubscribe/msg-9b"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var targetType, reason string
	var hasFrom, hasSubject bool
	if err := pool.QueryRow(ctx,
		`SELECT target_type, metadata->>'reason', metadata ? 'from', metadata ? 'subject'
		   FROM audit_log WHERE action = $1 AND metadata->>'key' = $2`,
		audit.ActionInboundMailParked, "unsubscribe/msg-9b",
	).Scan(&targetType, &reason, &hasFrom, &hasSubject); err != nil {
		t.Fatalf("query audit_log for parked record: %v", err)
	}
	if targetType != audit.TargetInboundMail {
		t.Errorf("target_type = %q, want %q", targetType, audit.TargetInboundMail)
	}
	if reason != "unparseable" {
		t.Errorf("metadata reason = %q, want %q", reason, "unparseable")
	}
	if hasFrom {
		t.Error(`metadata has key "from", want it omitted — Parse never returned a From: address`)
	}
	if hasSubject {
		t.Error(`metadata has key "subject", want it omitted — Parse never returned a Subject`)
	}
}

func TestSESInboundHandler_SubscriptionConfirmation_AutoConfirms(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	verifier := &fakeInboundVerifier{}
	h := NewSESInboundHandler(verifier, &fakeInboundObjectStore{}, subs, nil, nil, nil)

	env := sesnotify.Message{
		Type:         sesnotify.TypeSubscriptionConfirmation,
		MessageId:    "sub-confirm-1",
		TopicArn:     inboundTestTopicArn,
		SubscribeURL: "https://sns.us-west-2.amazonaws.com/?Action=ConfirmSubscription",
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rr := doPostSESInbound(h, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if verifier.subscribeURLHits != 1 {
		t.Errorf("FetchSubscribeURL hits = %d, want 1", verifier.subscribeURLHits)
	}
}

func TestSESInboundHandler_UnsubscribeConfirmation_NeverFetchesOrActs(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	verifier := &fakeInboundVerifier{}
	h := NewSESInboundHandler(verifier, &fakeInboundObjectStore{}, subs, nil, nil, nil)

	env := sesnotify.Message{
		Type:      sesnotify.TypeUnsubscribeConfirmation,
		MessageId: "unsub-confirm-1",
		TopicArn:  inboundTestTopicArn,
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rr := doPostSESInbound(h, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if verifier.subscribeURLHits != 0 {
		t.Error("UnsubscribeConfirmation must never fetch anything")
	}
}
