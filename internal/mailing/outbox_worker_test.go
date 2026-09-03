package mailing

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

func outboxTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

func uniqueOutboxRecipient(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("outbox-worker-test-%d@example.com", testdb.Unique())
}

// newTestOutboxWorker builds an OutboxWorker over pool, a RecordingMailer
// (no network — CLAUDE.md §10 item 2), and a fast poll interval so tests
// never wait out a real tick.
func newTestOutboxWorker(t *testing.T, pool *pgxpool.Pool, mailer Mailer) (*OutboxWorker, *outbox.Store) {
	t.Helper()
	store := outbox.NewStore(pool)
	w, err := NewOutboxWorker(OutboxWorkerDeps{
		Store:        store,
		Events:       subscribers.NewStore(pool),
		Mailer:       mailer,
		Settings:     dbSettings{pool: pool},
		BaseURL:      testBaseURL,
		PollInterval: 20 * time.Millisecond,
		BatchSize:    10,
	})
	if err != nil {
		t.Fatalf("NewOutboxWorker: %v", err)
	}
	return w, store
}

func runWorkerUntilStopped(t *testing.T, w *OutboxWorker) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := w.Stop(stopCtx); err != nil {
			t.Errorf("cleanup: w.Stop: %v", err)
		}
		<-done
	})
}

// TestOutboxWorker_DrainsConfirmationRow is the end-to-end proof that a
// queued confirmation row is claimed, rendered with the actual payload
// (token/manage_token), sent through the mailer, and marked sent with its
// payload scrubbed.
func TestOutboxWorker_DrainsConfirmationRow(t *testing.T) {
	pool := outboxTestPool(t)
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)

	recipient := uniqueOutboxRecipient(t)
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:      outbox.KindConfirmation,
		Recipient: recipient,
		Payload: map[string]any{
			"confirm_token": "the-confirm-token",
			"manage_token":  "the-manage-token",
			"ttl_seconds":   604800,
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	deadline := time.Now().Add(2 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("select: %v", err)
		}
		if status == outbox.StatusSent {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != outbox.StatusSent {
		t.Fatalf("status = %q after waiting, want %q", status, outbox.StatusSent)
	}

	sent := mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("mailer.Sent() = %d messages, want 1", len(sent))
	}
	if sent[0].To != recipient {
		t.Errorf("To = %q, want %q", sent[0].To, recipient)
	}
	if !strings.Contains(sent[0].HTMLBody, "the-confirm-token") {
		t.Error("rendered body does not contain the confirm token from the payload")
	}

	var payloadText string
	if err := pool.QueryRow(context.Background(), `SELECT payload::text FROM outbound_queue WHERE id = $1`, id).Scan(&payloadText); err != nil {
		t.Fatalf("select payload: %v", err)
	}
	if payloadText != "{}" {
		t.Errorf("payload = %q after send, want scrubbed to {} (#0126's plan §2)", payloadText)
	}
}

// TestOutboxWorker_MarkSent_RecordsConfirmationSent proves #0126's plan §6
// decision: confirmation_sent is written by MarkSent (a message actually
// LEFT the queue), not at enqueue time.
func TestOutboxWorker_MarkSent_RecordsConfirmationSent(t *testing.T) {
	pool := outboxTestPool(t)
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)

	subStore := subscribers.NewStore(pool)
	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: uniqueOutboxRecipient(t), ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Create's own transaction already enqueued one confirmation row for
	// this subscriber (#0126) — that is exactly the row this test drains,
	// so no separate Enqueue call is needed here.

	var before int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1 AND action = 'confirmation_sent'`, sub.ID,
	).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if before != 0 {
		t.Fatalf("confirmation_sent events before send = %d, want 0", before)
	}

	runWorkerUntilStopped(t, w)

	deadline := time.Now().Add(2 * time.Second)
	var after int
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1 AND action = 'confirmation_sent'`, sub.ID,
		).Scan(&after); err != nil {
			t.Fatalf("count after: %v", err)
		}
		if after == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after != 1 {
		t.Fatalf("confirmation_sent events after send = %d, want 1", after)
	}
	_ = store // silence unused in the (unlikely) event nothing above references it
}

// TestOutboxWorker_DrainsWelcomeRow is #0127's end-to-end proof, mirroring
// TestOutboxWorker_DrainsConfirmationRow: a queued welcome row is claimed,
// rendered with the actual payload (manage_token, interest_names), sent
// through the mailer, and marked sent with its payload scrubbed. Sets
// physical_address explicitly (#0264): the seeded default is blank, and a
// blank address now defers a welcome rather than sending it — see
// TestOutboxWorker_DefersWelcome_MissingPhysicalAddress for that path.
func TestOutboxWorker_DrainsWelcomeRow(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)

	recipient := uniqueOutboxRecipient(t)
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:      outbox.KindWelcome,
		Recipient: recipient,
		Payload: map[string]any{
			"manage_token":   "the-manage-token",
			"interest_names": []string{"Soldering", "Homelab"},
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	deadline := time.Now().Add(2 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("select: %v", err)
		}
		if status == outbox.StatusSent {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != outbox.StatusSent {
		t.Fatalf("status = %q after waiting, want %q", status, outbox.StatusSent)
	}

	sent := mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("mailer.Sent() = %d messages, want 1", len(sent))
	}
	if sent[0].To != recipient {
		t.Errorf("To = %q, want %q", sent[0].To, recipient)
	}
	if !strings.Contains(sent[0].HTMLBody, "Soldering") || !strings.Contains(sent[0].HTMLBody, "Homelab") {
		t.Error("rendered body does not contain the selected interest names from the payload")
	}
	if !strings.Contains(sent[0].TextBody, "the-manage-token") {
		t.Error("rendered body does not contain the manage token from the payload")
	}
	hasListUnsubscribe := false
	for _, h := range sent[0].Headers {
		if h.Name == "List-Unsubscribe" {
			hasListUnsubscribe = true
		}
	}
	if !hasListUnsubscribe {
		t.Error("welcome message rendered by the worker carries no List-Unsubscribe header")
	}

	var payloadText string
	if err := pool.QueryRow(context.Background(), `SELECT payload::text FROM outbound_queue WHERE id = $1`, id).Scan(&payloadText); err != nil {
		t.Fatalf("select payload: %v", err)
	}
	if payloadText != "{}" {
		t.Errorf("payload = %q after send, want scrubbed to {} (#0126's plan §2)", payloadText)
	}
}

// TestOutboxWorker_MarkSent_RecordsWelcomeSent proves #0127's precedent
// (mirroring confirmation_sent, #0126's plan §6): welcome_sent is written
// by MarkSent — "a welcome message LEFT the queue" — not at enqueue time.
// This is the load-bearing call site
// TestSubscriberEventActions_EveryConstantHasCallSiteOrOwner
// (internal/subscribers) checks for ActionWelcomeSent, closing the gap
// #0126 left with #0127 named as the owner.
//
// Calls subscribers.Store.Confirm to activate the subscriber — required
// since #0365's sendGate now refuses a KindWelcome row for anything but an
// 'active' subscriber, so the "stays pending, enqueue a welcome row
// directly" shape this test used before #0365 can no longer reach a real
// send at all. Confirm's own transaction already enqueues one welcome row
// for the newly-active subscriber (#0127's producer, internal/subscribers'
// Confirm) — this test drains THAT row rather than enqueueing a second one,
// the same "find the auto-enqueued row" pattern
// TestOutboxWorker_MarkSent_RecordsConfirmationSent (above) already uses
// for Create's auto-enqueued confirmation row, and for the identical
// reason: a second explicit Enqueue would leave two welcome rows for one
// subscriber, both eligible to send, making welcome_sent's count
// nondeterministic (1 or 2 depending on timing) rather than exactly 1.
//
// The resulting active subscriber row is deleted in t.Cleanup before any
// other test in this package can observe it via an AudienceAll-scoped
// query, following audience_test.go's and
// TestOutboxWorker_DefersWelcome_MissingPhysicalAddress's established
// pattern for the same shared-database constraint (TestMain truncates once
// per package run, not per test).
func TestOutboxWorker_MarkSent_RecordsWelcomeSent(t *testing.T) {
	pool := outboxTestPool(t)
	// #0264: a blank physical_address (the seeded default) now defers a
	// welcome instead of sending it, which would leave welcome_sent
	// unwritten and stall this test's whole point.
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	mailer := &RecordingMailer{}
	w, _ := newTestOutboxWorker(t, pool, mailer)

	subStore := subscribers.NewStore(pool)
	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: uniqueOutboxRecipient(t), ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, sub.ID)
	})
	if sub.ConfirmToken == nil {
		t.Fatal("Create returned a subscriber with no confirm_token")
	}
	sub, err = subStore.Confirm(context.Background(), *sub.ConfirmToken, time.Now())
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	var id int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM outbound_queue WHERE subscriber_id = $1 AND kind = $2`, sub.ID, string(outbox.KindWelcome),
	).Scan(&id); err != nil {
		t.Fatalf("finding Confirm's auto-enqueued welcome row: %v", err)
	}

	var before int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1 AND action = 'welcome_sent'`, sub.ID,
	).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if before != 0 {
		t.Fatalf("welcome_sent events before send = %d, want 0", before)
	}

	runWorkerUntilStopped(t, w)

	deadline := time.Now().Add(2 * time.Second)
	var after int
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1 AND action = 'welcome_sent'`, sub.ID,
		).Scan(&after); err != nil {
			t.Fatalf("count after: %v", err)
		}
		if after == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after != 1 {
		t.Fatalf("welcome_sent events after send = %d, want 1", after)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("select status: %v", err)
	}
	if status != outbox.StatusSent {
		t.Errorf("outbound_queue status = %q, want %q", status, outbox.StatusSent)
	}
}

// TestOutboxWorker_DefersWelcome_MissingPhysicalAddress is #0264's proof, in
// both directions: with settings.physical_address unset, a genuine Confirm()
// call still activates the subscriber and enqueues the welcome row, but the
// worker never sends it — then, once the address is set, the SAME row sends
// without being re-enqueued.
//
// Confirm() is used deliberately here (unlike
// TestOutboxWorker_MarkSent_RecordsWelcomeSent's direct Enqueue, which exists
// precisely to avoid activating a subscriber in this package's shared,
// truncated-once-per-run test database — see that test's own doc comment):
// #0264's acceptance criterion is explicit ("confirm with physical_address
// empty, assert the subscriber is confirmed"), so proving it needs a real
// Confirm call. The subscriber row this test creates is deleted in
// t.Cleanup before any other test in this package can observe it via an
// AudienceAll-scoped query, following audience_test.go's established pattern
// for the same shared-database constraint.
func TestOutboxWorker_DefersWelcome_MissingPhysicalAddress(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "")

	mailer := &RecordingMailer{}
	w, _ := newTestOutboxWorker(t, pool, mailer)

	// subscribers.Store.Create (below) also enqueues an ordinary
	// 'confirmation' row for the same subscriber/recipient, which the
	// worker sends normally regardless of physical_address (render's own
	// doc comment) — so "nothing went out" below is scoped to the WELCOME
	// message specifically, by subject, not to mailer.Sent()'s raw count.
	const welcomeSubject = "Welcome to Open Circuit SF"
	countWelcomeSent := func() int {
		n := 0
		for _, m := range mailer.Sent() {
			if m.Subject == welcomeSubject {
				n++
			}
		}
		return n
	}

	subStore := subscribers.NewStore(pool)
	email := uniqueOutboxRecipient(t)
	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: email, ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, sub.ID)
	})
	if sub.ConfirmToken == nil {
		t.Fatal("Create returned a subscriber with no confirm_token")
	}

	confirmed, err := subStore.Confirm(context.Background(), *sub.ConfirmToken, time.Now())
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if confirmed.Status != subscribers.StatusActive {
		t.Fatalf("status after Confirm = %q, want %q — a missing physical_address must not block confirmation", confirmed.Status, subscribers.StatusActive)
	}

	var welcomeID int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM outbound_queue WHERE recipient = $1 AND kind = 'welcome'`, email,
	).Scan(&welcomeID); err != nil {
		t.Fatalf("expected a welcome row enqueued by Confirm: %v", err)
	}

	runWorkerUntilStopped(t, w)

	// Wait until the worker has claimed and deferred the row at least once
	// (attempts >= 1), then assert it is queued again — not sent, not
	// abandoned — and that nothing went out.
	deadline := time.Now().Add(2 * time.Second)
	var status string
	var attempts int
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(), `SELECT status, attempts FROM outbound_queue WHERE id = $1`, welcomeID).Scan(&status, &attempts); err != nil {
			t.Fatalf("select: %v", err)
		}
		// Wait for a full claim-then-defer cycle to settle back to
		// 'queued', not just for attempts to tick up — 'sending' is a
		// real intermediate state between ClaimRow's UPDATE and
		// deferMissingPhysicalAddress's, and reading it there is a race
		// in this test, not a defect in the worker.
		if attempts >= 1 && status == outbox.StatusQueued {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if attempts < 1 {
		t.Fatal("worker never attempted the welcome row within the deadline")
	}
	if status != outbox.StatusQueued {
		t.Fatalf("status = %q after a deferred attempt, want %q (deferred, not abandoned/sent)", status, outbox.StatusQueued)
	}
	if n := countWelcomeSent(); n != 0 {
		t.Fatalf("welcome messages sent = %d, want 0 — no welcome should go out with physical_address unset", n)
	}

	// Now set the address and force the row due immediately — the backoff
	// schedule's first step is 1 minute (Backoff), which this test should
	// not have to wait out, the same technique
	// TestOutboxWorker_AbandonsAtMaxRetries_RetainsLastError uses.
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	if _, err := pool.Exec(context.Background(),
		`UPDATE outbound_queue SET next_attempt_at = now() WHERE id = $1 AND status = $2`,
		welcomeID, outbox.StatusQueued,
	); err != nil {
		t.Fatalf("forcing due: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, welcomeID).Scan(&status); err != nil {
			t.Fatalf("select status: %v", err)
		}
		if status == outbox.StatusSent {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != outbox.StatusSent {
		t.Fatalf("status = %q after setting physical_address, want %q", status, outbox.StatusSent)
	}
	var welcomeSent []Message
	for _, m := range mailer.Sent() {
		if m.Subject == welcomeSubject {
			welcomeSent = append(welcomeSent, m)
		}
	}
	if len(welcomeSent) != 1 {
		t.Fatalf("welcome messages sent after the address was set = %d, want exactly 1 (the same row, not re-enqueued)", len(welcomeSent))
	}
	if welcomeSent[0].To != email {
		t.Errorf("To = %q, want %q", welcomeSent[0].To, email)
	}
}

// TestOutboxWorker_Pass_ExpiresPendingSignups proves #0128's sweep
// integration: OutboxWorker's own poll loop (pass) drives
// subscribers.Store.ExpirePendingSweep, not just a standalone call to that
// method (already covered directly in internal/subscribers/pending_test.go)
// — this is the wiring test for the "ride the existing poll loop" decision
// disclosed in outbox_worker.go's pass doc comment.
//
// The subscriber row is inserted directly via SQL rather than through
// subscribers.Store.Create — Create also enqueues a 'confirmation' row,
// and this package's shared test database is NOT truncated between tests
// (see main_test.go's TestMain doc comment: once per package run). A
// confirmation row this test never drains would sit 'queued' and be
// available for the NEXT test's worker to opportunistically claim,
// corrupting an unrelated test's RecordingMailer assertions — exactly the
// failure this comment is here to prevent a future edit from
// reintroducing.
func TestOutboxWorker_Pass_ExpiresPendingSignups(t *testing.T) {
	pool := outboxTestPool(t)
	mailer := &RecordingMailer{}
	w, _ := newTestOutboxWorker(t, pool, mailer)

	var subscriberID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO subscribers (email, status, confirm_token, confirm_sent_at, confirm_expires_at, manage_token, created_at, updated_at)
		 VALUES ($1, 'pending', $2, $3, $3, $4, $3, $3)
		 RETURNING id`,
		uniqueOutboxRecipient(t), "expiry-sweep-test-token", time.Now().Add(-time.Hour), "expiry-sweep-test-manage-token",
	).Scan(&subscriberID); err != nil {
		t.Fatalf("inserting a pending subscriber directly: %v", err)
	}

	runWorkerUntilStopped(t, w)

	deadline := time.Now().Add(2 * time.Second)
	var tokenCleared bool
	for time.Now().Before(deadline) {
		var token *string
		if err := pool.QueryRow(context.Background(), `SELECT confirm_token FROM subscribers WHERE id = $1`, subscriberID).Scan(&token); err != nil {
			t.Fatalf("select confirm_token: %v", err)
		}
		if token == nil {
			tokenCleared = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !tokenCleared {
		t.Fatal("confirm_token was not cleared by the worker's own poll loop within the deadline")
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM subscribers WHERE id = $1`, subscriberID).Scan(&status); err != nil {
		t.Fatalf("select status: %v", err)
	}
	if status != subscribers.StatusPending {
		t.Errorf("status = %q after sweep, want %q (left pending, not deleted)", status, subscribers.StatusPending)
	}
}

// TestOutboxWorker_AbandonsAtMaxRetries_RetainsLastError drives a mailer
// that always fails and asserts the row reaches 'abandoned' with the last
// error retained, without hanging or looping forever.
func TestOutboxWorker_AbandonsAtMaxRetries_RetainsLastError(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, "queue_max_retries", "2")

	failing := &RecordingMailer{}
	failing.SetError(fmt.Errorf("simulated SES outage"))
	w, store := newTestOutboxWorker(t, pool, failing)
	w.pollInterval = 10 * time.Millisecond

	recipient := uniqueOutboxRecipient(t)
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:      outbox.KindConfirmation,
		Recipient: recipient,
		Payload:   map[string]any{"confirm_token": "t", "manage_token": "m", "ttl_seconds": 3600},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	deadline := time.Now().Add(3 * time.Second)
	var status string
	var lastErr *string
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(), `SELECT status, error FROM outbound_queue WHERE id = $1`, id).Scan(&status, &lastErr); err != nil {
			t.Fatalf("select: %v", err)
		}
		if status == outbox.StatusAbandoned {
			break
		}
		// Force the next attempt due immediately, since the backoff
		// schedule's first step (1 minute) would otherwise make this test
		// wait a full minute per retry.
		if _, err := pool.Exec(context.Background(), `UPDATE outbound_queue SET next_attempt_at = now() WHERE id = $1 AND status = 'queued'`, id); err != nil {
			t.Fatalf("forcing due: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != outbox.StatusAbandoned {
		t.Fatalf("status = %q after waiting, want %q", status, outbox.StatusAbandoned)
	}
	if lastErr == nil || *lastErr == "" {
		t.Fatal("error column empty after abandonment, want the last send error retained")
	}
	// Abandoned rows keep their payload — the diagnostic, per #0126's plan §2.
	var payloadText string
	if err := pool.QueryRow(context.Background(), `SELECT payload::text FROM outbound_queue WHERE id = $1`, id).Scan(&payloadText); err != nil {
		t.Fatalf("select payload: %v", err)
	}
	if payloadText == "{}" {
		t.Error("payload was scrubbed on an abandoned row, want it retained as the diagnostic")
	}
}

// TestOutbox_Release_RequeuesClaimedRow is the store-level unit test for
// outbox.Store.Release itself, independent of OutboxWorker — it does not
// exercise Stop's release path (see TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued
// below for that; this issue's phase-3 review, defect 2, found this test
// under the OutboxWorker_Stop_* name calling Store.Release directly and
// never constructing a worker or calling Stop at all, so it is renamed to
// what it actually proves).
func TestOutbox_Release_RequeuesClaimedRow(t *testing.T) {
	pool := outboxTestPool(t)
	store := outbox.NewStore(pool)

	recipient := uniqueOutboxRecipient(t)
	id, err := store.Enqueue(context.Background(), outbox.Item{Kind: outbox.KindConfirmation, Recipient: recipient, Payload: map[string]any{"confirm_token": "t", "manage_token": "m", "ttl_seconds": 3600}})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// This test deliberately ends with the row back in 'queued' — a
	// due-now row no real worker ever finishes sending. Left behind, it
	// is claimable by ANY later OutboxWorker in this same package/run
	// (e.g. TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued, which depends on
	// claiming its OWN two rows first) — so it must be removed
	// explicitly, the same reasoning as that test's own cleanup.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE id = $1`, id)
	})

	// Simulate a worker that claimed this row and then the process was
	// asked to stop before it finished sending — exactly the state
	// OutboxWorker.Stop's release path exists for. Scoped to the one kind
	// enqueued above (#0281) — an unscoped ClaimDue outside internal/outbox
	// claims every kind by default, #0254's failure mode.
	if _, err := store.ClaimDue(context.Background(), 10, []outbox.Kind{outbox.KindConfirmation}); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("select: %v", err)
	}
	if status != outbox.StatusSending {
		t.Fatalf("status after claim = %q, want %q (test setup invalid)", status, outbox.StatusSending)
	}

	done, err := store.Release(context.Background(), id)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !done {
		t.Fatal("Release reported done=false")
	}

	if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("select after release: %v", err)
	}
	if status != outbox.StatusQueued {
		t.Fatalf("status after release = %q, want %q", status, outbox.StatusQueued)
	}
}

// blockingMailer is a Mailer whose Send blocks until release is closed,
// then delegates to an embedded RecordingMailer. It exists only to make
// TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued deterministic: a way to
// hold a real OutboxWorker's pass loop paused mid-send on the FIRST row it
// reaches, so the SECOND row (selected but not yet claimed — #0297) is
// still 'queued' at the exact moment Stop is called — no sleeps, no timing
// luck.
type blockingMailer struct {
	inner   *RecordingMailer
	started chan struct{}
	release chan struct{}

	mu          sync.Mutex
	startedOnce bool
}

func (m *blockingMailer) Send(ctx context.Context, msg Message) (string, error) {
	m.mu.Lock()
	if !m.startedOnce {
		m.startedOnce = true
		close(m.started)
	}
	m.mu.Unlock()
	<-m.release
	return m.inner.Send(ctx, msg)
}

var _ Mailer = (*blockingMailer)(nil)

// TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued is #0126's phase-3 review
// defect 2's regression test, rewritten for #0297. It constructs a REAL
// *OutboxWorker, runs it, and calls Stop, reproducing the review's original
// scenario: two rows in one batch, the worker mid-send on one when Stop is
// called. It still asserts the review's outcome — Stop never leaves a row
// abandoned in 'sending' for the orphan sweep alone to find — but HOW that
// holds changed.
//
// Renamed from TestOutboxWorker_Stop_ReleasesClaimedRow by #0297: before
// that issue, ClaimDue's single UPDATE claimed the WHOLE batch atomically up front, so
// the row not yet reached when Stop fired was genuinely 'sending', and
// Stop's releaseAll (outbox_worker.go) existed to put it back to 'queued'
// rather than leave it for outboxOrphanStaleAfter to expire.
//
// #0297 moved this worker onto internal/outbox's select-then-per-row-claim
// path (SelectDue/ClaimRow): a row not yet reached is simply still
// 'queued' — SelectDue never claims anything. pass's own stopCh check
// (before each row's ClaimRow) is what stops a second row from ever being
// claimed once Stop has been called, and the one row genuinely mid-send
// when Stop fires always runs to completion (sendOne has no internal
// stopCh check of its own) before Run can return and doneCh can close — so
// w.claimed is provably empty by the time releaseAll would run.
// releaseAll (and the trackClaimed/untrackClaimed/claimedMu machinery
// behind it) is kept as a defensive safety net rather than removed — see
// releaseAll's own doc comment — and
// TestOutboxWorker_ReleaseAll_ConcurrentCallsDoNotRace (#0266) still
// exercises it directly against a fabricated w.claimed, so its own
// correctness stays covered even though THIS test can no longer observe it
// doing real work.
func TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued(t *testing.T) {
	pool := outboxTestPool(t)

	mailer := &blockingMailer{
		inner:   &RecordingMailer{},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	w, store := newTestOutboxWorker(t, pool, mailer)
	w.batchSize = 10

	id1, err := store.Enqueue(context.Background(), outbox.Item{
		Kind: outbox.KindConfirmation, Recipient: uniqueOutboxRecipient(t),
		Payload: map[string]any{"confirm_token": "t", "manage_token": "m", "ttl_seconds": 3600},
	})
	if err != nil {
		t.Fatalf("Enqueue 1: %v", err)
	}
	id2, err := store.Enqueue(context.Background(), outbox.Item{
		Kind: outbox.KindConfirmation, Recipient: uniqueOutboxRecipient(t),
		Payload: map[string]any{"confirm_token": "t", "manage_token": "m", "ttl_seconds": 3600},
	})
	if err != nil {
		t.Fatalf("Enqueue 2: %v", err)
	}
	// The whole point of this test is that id2 is deliberately left
	// 'queued': pass's stopCh check runs before its ClaimRow, so id2 is
	// never claimed at all (#0297 — see this test's own doc comment
	// above, and the post-Stop assertions below). A 'queued', due-now
	// confirmation row left behind after this test returns is picked up
	// by ANY later OutboxWorker pass sharing this database — including
	// other tests in this same package/run — so it must be removed
	// explicitly rather than relying on ending in a terminal status the
	// way every other test in this file does.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE id = ANY($1)`, []int64{id1, id2})
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		w.Run(runCtx)
		close(runDone)
	}()

	// Wait for the worker to claim the batch and block mid-send on row 1 —
	// deterministic, no sleep-and-hope.
	select {
	case <-mailer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("mailer.Send was never called; the worker never claimed the batch")
	}

	// Exactly one of the two rows is claimed now — the one blocked inside
	// sendOne — and the other is still plain 'queued', never having
	// reached ClaimRow yet: SelectDue selects both ids up front, but
	// ClaimRow only claims one row at a time, immediately before ITS OWN
	// send (#0297), so the second row's claim genuinely has not happened.
	// Under #0297's SelectDue (ORDER BY next_attempt_at, id) plus pass's
	// own in-order loop over the returned ids, which row that is is now
	// deterministic — id1, sorted first — not a race: contrast the
	// earlier ClaimDue-based version of this pass, whose UPDATE …
	// RETURNING gave no ordering guarantee at all. This checkpoint still
	// asserts on the PAIR, not on id1/id2 by name, because pinning a
	// specific id would encode the loop's current iteration order rather
	// than the property this test exists to pin, and a later legitimate
	// change to claim order should not have to rewrite this assertion.
	// See the post-Stop assertions below, which check "exactly one sent,
	// one queued" rather than assuming which id is which — #0264's review
	// found an earlier version of this test assuming id1 specifically
	// after a change elsewhere in this file altered the shared test
	// database's physical layout enough to flip it, back when claim order
	// genuinely was not guaranteed.
	var status1, status2 string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id1).Scan(&status1); err != nil {
		t.Fatalf("select id1 before stop: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id2).Scan(&status2); err != nil {
		t.Fatalf("select id2 before stop: %v", err)
	}
	sendingBefore, queuedBefore := 0, 0
	for _, s := range []string{status1, status2} {
		switch s {
		case outbox.StatusSending:
			sendingBefore++
		case outbox.StatusQueued:
			queuedBefore++
		}
	}
	if sendingBefore != 1 || queuedBefore != 1 {
		t.Fatalf("status before Stop = (id1=%q, id2=%q), want exactly one %q (blocked in sendOne) and one %q (not yet reached — test setup invalid)",
			status1, status2, outbox.StatusSending, outbox.StatusQueued)
	}

	// Call Stop concurrently — it must block on <-w.doneCh, which cannot
	// close until pass returns, which cannot happen until the row blocked
	// in sendOne unblocks. This is the exact ordering the review's proof
	// used: Stop is already in flight, THEN the send is released.
	stopErrCh := make(chan error, 1)
	go func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		stopErrCh <- w.Stop(stopCtx)
	}()

	// Give Stop's goroutine a moment to reach <-w.doneCh before unblocking
	// the send, so the release genuinely happens on Stop's path rather
	// than by accident before Stop was even called.
	time.Sleep(50 * time.Millisecond)
	close(mailer.release)

	select {
	case err := <-stopErrCh:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop never returned")
	}
	<-runDone

	if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id1).Scan(&status1); err != nil {
		t.Fatalf("select id1 after stop: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id2).Scan(&status2); err != nil {
		t.Fatalf("select id2 after stop: %v", err)
	}
	// The row that was mid-send finished normally (unblocked, then sent to
	// completion — sendOne has no internal stopCh check, so Stop cannot
	// interrupt it) and the row never reached is still plain 'queued' —
	// never claimed at all, so there was nothing for Stop to release
	// (#0297; contrast the pre-#0297 doc comment above). Either way, the
	// review's original defect — a row abandoned in 'sending' for the
	// orphan sweep alone to find — does not reproduce. See the pre-Stop
	// checkpoint's comment for why this does not assume which id is
	// which.
	sentCount, queuedCount := 0, 0
	for _, s := range []string{status1, status2} {
		switch s {
		case outbox.StatusSent:
			sentCount++
		case outbox.StatusQueued:
			queuedCount++
		}
	}
	if sentCount != 1 || queuedCount != 1 {
		t.Fatalf("status after Stop = (id1=%q, id2=%q), want exactly one %q (finished before Stop returned) and one %q (never claimed, not abandoned mid-'sending' — defect 2)",
			status1, status2, outbox.StatusSent, outbox.StatusQueued)
	}
}

// TestOutboxWorker_ReleaseAll_ConcurrentCallsDoNotRace is #0266 item 3's
// mutation proof: claimedMu's doc comment now says the mutex is what makes
// Stop's "safe to call more than once" promise hold under CONCURRENT calls,
// not merely belt-and-braces once <-w.doneCh has fired. This exercises that
// directly — two goroutines calling releaseAll() at the same instant,
// against a populated w.claimed — rather than through the fuller Run/Stop
// timing dance TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued above already
// covers for the single-caller case.
//
// The ids in w.claimed are fabricated (never enqueued), so both calls to
// store.Release resolve to "0 rows affected, no error" — this test is about
// the race on w.claimed itself, not on any real outbound_queue row, and
// leaves nothing to clean up.
//
// Run under `go test -race`: with the mutex removed from releaseAll (proved
// by hand while writing this test — see issues/0266.md's ## Verification
// for the reinstatement), this fails with "DATA RACE" on the w.claimed
// field, both goroutines' stacks pointing at the same range/assignment this
// method makes. With the mutex in place, it is clean.
func TestOutboxWorker_ReleaseAll_ConcurrentCallsDoNotRace(t *testing.T) {
	pool := outboxTestPool(t)
	w, _ := newTestOutboxWorker(t, pool, &RecordingMailer{})

	w.claimed = map[int64]struct{}{
		-9001: {}, -9002: {}, -9003: {}, -9004: {}, -9005: {},
	}

	start := make(chan struct{})
	done := make(chan struct{}, 2)
	for range 2 {
		go func() {
			<-start
			w.releaseAll()
			done <- struct{}{}
		}()
	}
	close(start)
	for range 2 {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("releaseAll goroutine never returned")
		}
	}
}

// TestOutbox_Enqueue_AdminAlert_DrainsAndSends is #0126's plan §7 proof for
// #0124's alert path: an admin_alert row enqueues and drains like any
// other kind, rendering with BuildAdminAlertEmail.
func TestOutbox_Enqueue_AdminAlert_DrainsAndSends(t *testing.T) {
	pool := outboxTestPool(t)
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)

	recipient := uniqueOutboxRecipient(t)
	_, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:      outbox.KindAdminAlert,
		Recipient: recipient,
		Payload: map[string]any{
			"subject": "Test alert",
			"lines":   []string{"line one", "line two"},
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(mailer.Sent()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	sent := mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("mailer.Sent() = %d messages, want 1", len(sent))
	}
	if sent[0].Subject != "Test alert" {
		t.Errorf("Subject = %q, want %q", sent[0].Subject, "Test alert")
	}
}

// TestOutboxWorker_DrainsImportInviteRow is #0129's end-to-end proof,
// mirroring TestOutboxWorker_DrainsWelcomeRow: a queued import_invite row is
// claimed, rendered with the actual payload (confirm_token, provenance
// fields), sent through the mailer, and marked sent with its payload
// scrubbed — and, distinctly from every other kind this worker renders,
// carries the mandatory provenance sentence and the RFC 8058 headers.
func TestOutboxWorker_DrainsImportInviteRow(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)

	recipient := uniqueOutboxRecipient(t)
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:      outbox.KindImportInvite,
		Recipient: recipient,
		Payload: map[string]any{
			"confirm_token": "the-confirm-token",
			"manage_token":  "the-manage-token",
			"ttl_seconds":   int64(7 * 24 * 3600),
			"import_source": "luma",
			"source_detail": "Intro to Soldering",
			"collected_at":  "2026-05-12T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	deadline := time.Now().Add(2 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("select: %v", err)
		}
		if status == outbox.StatusSent {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != outbox.StatusSent {
		t.Fatalf("status = %q after waiting, want %q", status, outbox.StatusSent)
	}

	sent := mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("mailer.Sent() = %d messages, want 1", len(sent))
	}
	if sent[0].To != recipient {
		t.Errorf("To = %q, want %q", sent[0].To, recipient)
	}
	if !strings.Contains(sent[0].TextBody, "Intro to Soldering") {
		t.Error("rendered body does not contain source_detail from the payload")
	}
	if !strings.Contains(sent[0].TextBody, "the-confirm-token") {
		t.Error("rendered body does not contain the confirm token from the payload")
	}
	hasListUnsubscribe := false
	for _, h := range sent[0].Headers {
		if h.Name == "List-Unsubscribe" {
			hasListUnsubscribe = true
		}
	}
	if !hasListUnsubscribe {
		t.Error("import invite message rendered by the worker carries no List-Unsubscribe header")
	}

	var payloadText string
	if err := pool.QueryRow(context.Background(), `SELECT payload::text FROM outbound_queue WHERE id = $1`, id).Scan(&payloadText); err != nil {
		t.Fatalf("select payload: %v", err)
	}
	if payloadText != "{}" {
		t.Errorf("payload = %q after send, want scrubbed to {}", payloadText)
	}
}

// TestOutboxWorker_MarkSent_RecordsInviteSent proves #0129's mirror of
// #0127's welcome_sent precedent: invite_sent is written by MarkSent — "an
// import invitation LEFT the queue" — not at ImportStore.Commit's enqueue
// time. This is the load-bearing call site
// TestSubscriberEventActions_EveryConstantHasCallSiteOrOwner
// (internal/subscribers) checks for ActionInviteSent, closing the gap
// #0125's groundwork left with #0129 named as the owner.
func TestOutboxWorker_MarkSent_RecordsInviteSent(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)

	subStore := subscribers.NewStore(pool)
	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: uniqueOutboxRecipient(t), ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// sub stays 'pending' via the ordinary website-signup path — this test
	// never calls ImportStore.Commit — so it is invisible to any
	// AudienceAll (status='active') query elsewhere in this package's
	// shared test database, mirroring TestOutboxWorker_MarkSent_RecordsWelcomeSent's
	// own reasoning for the identical choice.
	if sub.ConfirmToken == nil {
		t.Fatal("Create returned a subscriber with no confirm_token")
	}

	// #0340: the payload's confirm_token must be the subscriber's actual
	// LIVE token, not a fabricated placeholder — sendGate's new token
	// predicate now correctly withholds a row whose payload token was
	// never the live one (this test's fixture used a literal placeholder
	// before #0340, which this fix's own token check would otherwise, and
	// correctly, skip rather than send).
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:         outbox.KindImportInvite,
		Recipient:    sub.Email,
		SubscriberID: &sub.ID,
		Payload: map[string]any{
			"confirm_token": *sub.ConfirmToken,
			"manage_token":  sub.ManageToken,
			"ttl_seconds":   int64(7 * 24 * 3600),
			"import_source": "luma",
			"source_detail": "Intro to Soldering",
			"collected_at":  "2026-05-12T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var before int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1 AND action = 'invite_sent'`, sub.ID,
	).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if before != 0 {
		t.Fatalf("invite_sent events before send = %d, want 0", before)
	}

	runWorkerUntilStopped(t, w)

	deadline := time.Now().Add(2 * time.Second)
	var after int
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1 AND action = 'invite_sent'`, sub.ID,
		).Scan(&after); err != nil {
			t.Fatalf("count after: %v", err)
		}
		if after == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after != 1 {
		t.Fatalf("invite_sent events after send = %d, want 1", after)
	}
	_ = id
}

// TestOutboxWorker_DefersImportInvite_MissingPhysicalAddress mirrors
// TestOutboxWorker_DefersWelcome_MissingPhysicalAddress (#0264) for #0129's
// identical gate: a blank physical_address defers an import_invite row
// indefinitely (never abandons it) rather than sending it without a
// required CAN-SPAM §7704 address line.
func TestOutboxWorker_DefersImportInvite_MissingPhysicalAddress(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "")

	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)

	recipient := uniqueOutboxRecipient(t)
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:      outbox.KindImportInvite,
		Recipient: recipient,
		Payload: map[string]any{
			"confirm_token": "the-confirm-token",
			"manage_token":  "the-manage-token",
			"ttl_seconds":   int64(7 * 24 * 3600),
			"import_source": "luma",
			"source_detail": "Intro to Soldering",
			"collected_at":  "2026-05-12T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	// import_source is the enqueue-time payload key; the invitation
	// subject is what mailer.Sent() records — scope the "nothing sent"
	// assertion to it below rather than the raw slice length, since this
	// package's worker drains the whole shared outbound_queue and other
	// tests can leave their own rows in it.
	const inviteSubject = "You're invited to the Open Circuit SF mailing list"
	countInviteSent := func() int {
		n := 0
		for _, m := range mailer.Sent() {
			if m.Subject == inviteSubject {
				n++
			}
		}
		return n
	}

	deadline := time.Now().Add(2 * time.Second)
	var status string
	var attempts int
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(),
			`SELECT status, attempts FROM outbound_queue WHERE id = $1`, id,
		).Scan(&status, &attempts); err != nil {
			t.Fatalf("select: %v", err)
		}
		// Wait for a full claim-then-defer cycle to settle back to
		// 'queued', not just for attempts to tick up — 'sending' is a
		// real intermediate state between ClaimRow's UPDATE and
		// deferMissingPhysicalAddress's, and reading it there is a race
		// in this test, not a defect in the worker (mirrors
		// TestOutboxWorker_DefersWelcome_MissingPhysicalAddress, #0264).
		if attempts >= 1 && status == outbox.StatusQueued {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if attempts < 1 {
		t.Fatal("row was never claimed (attempts stayed 0)")
	}
	if status != outbox.StatusQueued {
		t.Errorf("status = %q, want %q — a missing physical_address must defer, not abandon or send", status, outbox.StatusQueued)
	}
	if n := countInviteSent(); n != 0 {
		t.Errorf("invitation messages sent = %d, want 0 — nothing should go out without a physical_address", n)
	}
}

// TestOutboxWorker_AdminResendInvitation_RendersSameProvenanceAsFirstInvitation
// is #0312 criterion 1's end-to-end oracle: the message
// subscribers.Store.AdminResendInvitation enqueues, once actually rendered
// by THIS package's real OutboxWorker (never a copy of
// BuildImportInviteEmail's template logic, and never the payload this test
// wrote — the oracle is the RENDERED body), carries the identical
// provenance sentence the first invitation carried — built from the SAME
// subscriber_imports row's source_detail/collected_at — and is the
// invitation template, never the generic confirmation.
//
// Mutation M1 (#0312's plan): make AdminResendInvitation enqueue
// outbox.KindConfirmation instead of outbox.KindImportInvite. The re-send
// would then render as "Confirm your Open Circuit SF subscription" with no
// source_detail anywhere in the body — both assertions below on the
// re-send fail.
func TestOutboxWorker_AdminResendInvitation_RendersSameProvenanceAsFirstInvitation(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	mailer := &RecordingMailer{}
	w, _ := newTestOutboxWorker(t, pool, mailer)

	importStore := subscribers.NewImportStore(pool)
	subStore := subscribers.NewStore(pool)

	email := uniqueOutboxRecipient(t)
	collectedAt := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	const sourceDetail = "Intro to Soldering (admin resend provenance test)"
	const inviteSubject = "You're invited to the Open Circuit SF mailing list"

	if _, err := importStore.Commit(context.Background(), subscribers.CommitInput{
		Source:       subscribers.ImportSourceLuma,
		SourceDetail: sourceDetail,
		ConsentMode:  subscribers.ConsentModeInvite,
		ConsentNote:  "collected via Luma RSVP export, attested by the organizer",
		CollectedAt:  collectedAt,
		Filename:     "attendees.csv",
		Rows:         []subscribers.ImportRow{{Email: email}},
	}, time.Now()); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	sub, err := subStore.FindByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}

	// Scoped by recipient throughout, not by slice length or index — this
	// package's worker drains the WHOLE shared outbound_queue, and other
	// tests can leave their own rows in it (the exact defect #0129's
	// blocker 1 traced a false failure to).
	sentToRecipient := func() []Message {
		var out []Message
		for _, m := range mailer.Sent() {
			if m.To == email {
				out = append(out, m)
			}
		}
		return out
	}

	runWorkerUntilStopped(t, w)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(sentToRecipient()) < 1 {
		time.Sleep(20 * time.Millisecond)
	}
	first := sentToRecipient()
	if len(first) != 1 {
		t.Fatalf("messages sent to %s after the first invitation = %d, want 1", email, len(first))
	}
	if first[0].Subject != inviteSubject {
		t.Fatalf("first message Subject = %q, want %q", first[0].Subject, inviteSubject)
	}
	if !strings.Contains(first[0].TextBody, sourceDetail) {
		t.Fatalf("first invitation body does not contain source_detail %q", sourceDetail)
	}

	if _, err := subStore.AdminResendInvitation(context.Background(), sub.ID, time.Now(), time.Hour); err != nil {
		t.Fatalf("AdminResendInvitation: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(sentToRecipient()) < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	sent := sentToRecipient()
	if len(sent) != 2 {
		t.Fatalf("messages sent to %s after the admin re-send = %d, want 2", email, len(sent))
	}
	resend := sent[1]
	if resend.Subject != inviteSubject {
		t.Errorf("re-send Subject = %q, want %q (the invitation, not the generic confirmation)", resend.Subject, inviteSubject)
	}
	if !strings.Contains(resend.TextBody, sourceDetail) {
		t.Errorf("re-send body does not contain source_detail %q — criterion 1 (same provenance as the first invitation)", sourceDetail)
	}
	if strings.Contains(resend.Subject, "Confirm your Open Circuit SF subscription") {
		t.Error("re-send Subject reads like the generic confirmation, not the invitation")
	}
}

// TestAdminResendInvitation_PhysicalAddressGateNotBypassable
// is #0312 criterion 6 / CLAUDE.md §9's "must not be bypassable from the
// UI" — proved by calling the STORE method directly (never through
// internal/handlers.AdminPendingHandler.ResendInvitation, whose
// physical_address pre-check is explicitly advisory, not the gate — see
// that handler's own doc comment) against a blank physical_address, then
// draining with a real OutboxWorker. The pre-existing
// TestOutboxWorker_DefersImportInvite_MissingPhysicalAddress (#0129) is the
// second, independent oracle proving the SAME gate for a first invitation;
// this one proves it for a re-send, which enqueues through a different
// code path (AdminResendInvitation, not ImportStore.Commit).
//
// Mutation M4 (#0312's plan): delete render's KindImportInvite blank-
// address refusal in internal/mailing/outbox_worker.go. Both this test and
// TestOutboxWorker_DefersImportInvite_MissingPhysicalAddress must fail.
func TestAdminResendInvitation_PhysicalAddressGateNotBypassable(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "") // #0312's advisory handler-side pre-check is not exercised here at all

	mailer := &RecordingMailer{}
	w, _ := newTestOutboxWorker(t, pool, mailer)

	importStore := subscribers.NewImportStore(pool)
	subStore := subscribers.NewStore(pool)

	email := uniqueOutboxRecipient(t)
	if _, err := importStore.Commit(context.Background(), subscribers.CommitInput{
		Source:       subscribers.ImportSourceLuma,
		SourceDetail: "Intro to Soldering (gate-not-bypassable test)",
		ConsentMode:  subscribers.ConsentModeInvite,
		ConsentNote:  "collected via Luma RSVP export, attested by the organizer",
		CollectedAt:  time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		Filename:     "attendees.csv",
		Rows:         []subscribers.ImportRow{{Email: email}},
	}, time.Now()); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	sub, err := subStore.FindByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}

	// Drain the FIRST invitation too — it also defers (blank
	// physical_address from the start), so it never leaves a live
	// confirm_sent_at cooldown behind for the re-send call below.
	runWorkerUntilStopped(t, w)

	if _, err := subStore.AdminResendInvitation(context.Background(), sub.ID, time.Now(), time.Hour); err != nil {
		t.Fatalf("AdminResendInvitation (store call, no handler pre-check involved): %v", err)
	}

	sentToRecipient := func() int {
		n := 0
		for _, m := range mailer.Sent() {
			if m.To == email {
				n++
			}
		}
		return n
	}

	// Polls the RE-SENT row specifically (highest id for this subscriber's
	// import_invite rows — the original, first-invitation row also exists
	// and also never sends, but is not what this test is about). No status
	// filter in the query itself: if the gate were bypassed the row would
	// reach 'sent', and the query must still find it so the assertions
	// below can say so, rather than failing on "no rows" for the wrong
	// reason.
	deadline := time.Now().Add(2 * time.Second)
	var status string
	var attempts int
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(),
			`SELECT status, attempts FROM outbound_queue
			  WHERE subscriber_id = $1 AND kind = 'import_invite'
			  ORDER BY id DESC LIMIT 1`,
			sub.ID,
		).Scan(&status, &attempts); err != nil {
			t.Fatalf("select: %v", err)
		}
		if attempts >= 1 && status != outbox.StatusSending {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if attempts < 1 {
		t.Fatal("re-sent row was never claimed (attempts stayed 0)")
	}
	if status != outbox.StatusQueued {
		t.Errorf("status = %q, want %q — a missing physical_address must defer the re-send too, not abandon or send it", status, outbox.StatusQueued)
	}
	if n := sentToRecipient(); n != 0 {
		t.Errorf("messages sent to %s = %d, want 0 — nothing should go out without a physical_address, for a re-send any more than a first send", email, n)
	}
}

// --- #0365: send-time eligibility gate ---
//
// TestOutboxWorker_DrainsConfirmationRow and its siblings above enqueue
// rows with a NIL SubscriberID, so they never exercise sendGate at all —
// gatedKinds[row.Kind] only matters once row.SubscriberID is set, and
// their staying green after this change is not evidence the gate works.
// Every test below sets SubscriberID explicitly via a real subscribers row.

// waitForQueueStatus polls outbound_queue.status for id until it leaves
// 'queued'/'sending' or the deadline passes, returning the terminal value
// observed. Shared by every #0365 test below so each one asserts against
// the SAME terminal-state polling convention the pre-existing
// TestOutboxWorker_DrainsConfirmationRow uses inline.
func waitForQueueStatus(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("select status: %v", err)
		}
		if status != outbox.StatusQueued && status != outbox.StatusSending {
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}
	return status
}

// sentTo counts mailer's recorded sends addressed to recipient — scoped
// rather than mailer.Sent()'s raw length, matching this file's own
// established convention (see TestOutboxWorker_ResendInvitation_*'s
// countInviteSent, above): this package's worker drains the WHOLE shared
// outbound_queue, so a leftover row from an earlier test's own subscriber
// could in principle also be selected by a later test's fresh worker, and
// scoping by recipient keeps each test's assertion about mail sent to ITS
// OWN address regardless.
func sentTo(mailer *RecordingMailer, recipient string) int {
	n := 0
	for _, m := range mailer.Sent() {
		if m.To == recipient {
			n++
		}
	}
	return n
}

// TestOutboxWorker_SkipsConfirmationForComplainedSubscriber is #0365's
// primary proof: a complaint that lands AFTER a confirmation row is
// enqueued but BEFORE the worker drains it must not result in a delivered
// confirmation (CLAUDE.md §9 — "complained subscribers never
// auto-resubscribe").
func TestOutboxWorker_SkipsConfirmationForComplainedSubscriber(t *testing.T) {
	pool := outboxTestPool(t)
	mailer := &RecordingMailer{}
	w, _ := newTestOutboxWorker(t, pool, mailer)
	subStore := subscribers.NewStore(pool)

	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: uniqueOutboxRecipient(t), ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Create's own transaction already enqueued one confirmation row for
	// this subscriber (#0126) — find it rather than enqueueing a second.
	var id int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM outbound_queue WHERE subscriber_id = $1 AND kind = $2`, sub.ID, string(outbox.KindConfirmation),
	).Scan(&id); err != nil {
		t.Fatalf("finding auto-enqueued confirmation row: %v", err)
	}

	// The complaint lands AFTER the claim committed (Create, above) but
	// BEFORE the worker drains the row — exactly #0365's window.
	if _, err := subStore.MarkComplained(context.Background(), sub.ID, time.Now()); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}

	runWorkerUntilStopped(t, w)

	status := waitForQueueStatus(t, pool, id)
	if status != outbox.StatusSkipped {
		t.Fatalf("status = %q after waiting, want %q", status, outbox.StatusSkipped)
	}

	var errText *string
	var payloadText string
	if err := pool.QueryRow(context.Background(),
		`SELECT error, payload::text FROM outbound_queue WHERE id = $1`, id,
	).Scan(&errText, &payloadText); err != nil {
		t.Fatalf("select error/payload: %v", err)
	}
	if errText == nil || !strings.Contains(*errText, `"complained"`) {
		t.Errorf("error = %v, want a reason naming the complained status", errText)
	}
	if payloadText != "{}" {
		t.Errorf("payload = %q after skip, want scrubbed to {}", payloadText)
	}

	if got := sentTo(mailer, sub.Email); got != 0 {
		t.Fatalf("messages sent to %s = %d, want 0 — a complained subscriber must never receive a confirmation", sub.Email, got)
	}
}

// TestOutboxWorker_SkipsWelcomeForSuppressedAddress proves the suppression
// half of the gate is independently load-bearing: the subscriber's status
// stays 'active' the whole time — only a suppressions row exists — so a
// status-only check would miss this.
func TestOutboxWorker_SkipsWelcomeForSuppressedAddress(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)
	subStore := subscribers.NewStore(pool)
	suppStore := subscribers.NewSuppressionStore(pool)

	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: uniqueOutboxRecipient(t), ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sub.ConfirmToken == nil {
		t.Fatalf("Create did not return a confirm token")
	}
	if _, err := subStore.Confirm(context.Background(), *sub.ConfirmToken, time.Now()); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	if _, err := suppStore.Add(context.Background(), subscribers.NewSuppression{
		Email: sub.Email, Reason: subscribers.SuppressionReasonManual,
	}, time.Now()); err != nil {
		t.Fatalf("Add suppression: %v", err)
	}

	subID := sub.ID
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:         outbox.KindWelcome,
		Recipient:    sub.Email,
		SubscriberID: &subID,
		Payload: map[string]any{
			"manage_token":   sub.ManageToken,
			"interest_names": []string{"Soldering"},
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	status := waitForQueueStatus(t, pool, id)
	if status != outbox.StatusSkipped {
		t.Fatalf("status = %q after waiting, want %q", status, outbox.StatusSkipped)
	}

	var errText *string
	if err := pool.QueryRow(context.Background(), `SELECT error FROM outbound_queue WHERE id = $1`, id).Scan(&errText); err != nil {
		t.Fatalf("select error: %v", err)
	}
	if errText == nil || !strings.Contains(*errText, "suppressed") {
		t.Errorf("error = %v, want a reason naming the suppression", errText)
	}

	if got := sentTo(mailer, sub.Email); got != 0 {
		t.Fatalf("messages sent to %s = %d, want 0", sub.Email, got)
	}

	// The subscriber's own status is untouched — this is a suppression,
	// not a status mutation, and the two predicates must be independently
	// provable (this test proves the suppression one).
	live, err := subStore.GetByID(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if live.Status != subscribers.StatusActive {
		t.Errorf("subscriber status = %q, want unchanged %q", live.Status, subscribers.StatusActive)
	}
}

// TestOutboxWorker_SkipsImportInviteForDeclinedInvitee mirrors the
// invite-decline path's status transition (pending -> unsubscribed, the
// same UPDATE internal/subscribers.Store.Unsubscribe performs when an
// invited row declines) — the gate cares about the subscriber's LIVE
// status, not the specific call that produced it.
func TestOutboxWorker_SkipsImportInviteForDeclinedInvitee(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)
	subStore := subscribers.NewStore(pool)

	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: uniqueOutboxRecipient(t), ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := subStore.Unsubscribe(context.Background(), sub.ID, subscribers.SourceOneClick, time.Now()); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	subID := sub.ID
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:         outbox.KindImportInvite,
		Recipient:    sub.Email,
		SubscriberID: &subID,
		Payload: map[string]any{
			"confirm_token": "the-confirm-token",
			"manage_token":  sub.ManageToken,
			"ttl_seconds":   604800,
			"import_source": "csv",
			"source_detail": "test-import.csv",
			"collected_at":  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	status := waitForQueueStatus(t, pool, id)
	if status != outbox.StatusSkipped {
		t.Fatalf("status = %q after waiting, want %q", status, outbox.StatusSkipped)
	}

	var errText *string
	if err := pool.QueryRow(context.Background(), `SELECT error FROM outbound_queue WHERE id = $1`, id).Scan(&errText); err != nil {
		t.Fatalf("select error: %v", err)
	}
	if errText == nil || !strings.Contains(*errText, `"unsubscribed"`) {
		t.Errorf("error = %v, want a reason naming the unsubscribed status", errText)
	}

	if got := sentTo(mailer, sub.Email); got != 0 {
		t.Fatalf("messages sent to %s = %d, want 0", sub.Email, got)
	}
}

// TestOutboxWorker_SkipsAlreadySubscribedForComplainedSubscriber is the
// fourth gated kind.
func TestOutboxWorker_SkipsAlreadySubscribedForComplainedSubscriber(t *testing.T) {
	pool := outboxTestPool(t)
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)
	subStore := subscribers.NewStore(pool)

	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: uniqueOutboxRecipient(t), ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sub.ConfirmToken == nil {
		t.Fatalf("Create did not return a confirm token")
	}
	if _, err := subStore.Confirm(context.Background(), *sub.ConfirmToken, time.Now()); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := subStore.MarkComplained(context.Background(), sub.ID, time.Now()); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}

	subID := sub.ID
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:         outbox.KindAlreadySubscribed,
		Recipient:    sub.Email,
		SubscriberID: &subID,
		Payload:      map[string]any{"manage_token": sub.ManageToken},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	status := waitForQueueStatus(t, pool, id)
	if status != outbox.StatusSkipped {
		t.Fatalf("status = %q after waiting, want %q", status, outbox.StatusSkipped)
	}
	if got := sentTo(mailer, sub.Email); got != 0 {
		t.Fatalf("messages sent to %s = %d, want 0", sub.Email, got)
	}
}

// TestOutboxWorker_SendsRegistrationToSuppressedAddress is the ungated
// half, end to end: auth mail is not blocked by list suppression.
// CLAUDE.md §9's "complained subscribers never auto-resubscribe" is about
// LIST mail, and this worker's registration kind reaches a users row, not
// a subscribers row (Row.SubscriberID is always nil for it in production —
// verified across every producer in internal/auth, see gatedKinds' doc
// comment), so an admin whose personal address happens to carry a
// suppression must still be able to complete registration.
//
// NOT a regression guard for "someone moves registration into gatedKinds"
// — corrected by #0365's review, which measured it. Because a registration
// row's SubscriberID is nil, sendGate's nil-SubscriberID fail-open returns
// send-anyway BEFORE any map lookup can matter, so this test stays green
// with KindRegistration moved into gatedKinds (mutation-checked). The
// mutation that actually catches that is
// TestOutboxWorker_SendGate_UngatedKindsNeverSkip, below, which drives
// sendGate directly with a non-nil SubscriberID so the map decision is the
// only thing under test.
func TestOutboxWorker_SendsRegistrationToSuppressedAddress(t *testing.T) {
	pool := outboxTestPool(t)
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)
	suppStore := subscribers.NewSuppressionStore(pool)

	recipient := uniqueOutboxRecipient(t)
	if _, err := suppStore.Add(context.Background(), subscribers.NewSuppression{
		Email: recipient, Reason: subscribers.SuppressionReasonManual,
	}, time.Now()); err != nil {
		t.Fatalf("Add suppression: %v", err)
	}

	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:      outbox.KindRegistration,
		Recipient: recipient,
		Payload:   map[string]any{"token": "the-registration-token", "ttl_seconds": 3600},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	status := waitForQueueStatus(t, pool, id)
	if status != outbox.StatusSent {
		t.Fatalf("status = %q after waiting, want %q — auth mail must not be blocked by list suppression (CLAUDE.md §9)", status, outbox.StatusSent)
	}
	if got := sentTo(mailer, recipient); got != 1 {
		t.Fatalf("messages sent to %s = %d, want 1", recipient, got)
	}
}

// TestOutboxWorker_SkipWritesNoSubscriberEvent is #0365's plan §8
// criterion 3: no subscriber_events row is written for a skip.
func TestOutboxWorker_SkipWritesNoSubscriberEvent(t *testing.T) {
	pool := outboxTestPool(t)
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)
	subStore := subscribers.NewStore(pool)

	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: uniqueOutboxRecipient(t), ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := subStore.MarkComplained(context.Background(), sub.ID, time.Now()); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}

	// Captured AFTER Create (which writes signup_requested) and
	// MarkComplained (which writes complained) — both are exactly the
	// facts #0365's plan §3.4 says already explain the skip, timestamped
	// earlier by the SES event handler's real-world equivalent. This test
	// isolates whether the SKIP ITSELF adds anything further.
	var before int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1`, sub.ID,
	).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	subID := sub.ID
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:         outbox.KindConfirmation,
		Recipient:    sub.Email,
		SubscriberID: &subID,
		Payload:      map[string]any{"confirm_token": "t", "manage_token": "m", "ttl_seconds": 3600},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	status := waitForQueueStatus(t, pool, id)
	if status != outbox.StatusSkipped {
		t.Fatalf("status = %q after waiting, want %q", status, outbox.StatusSkipped)
	}

	var after int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1`, sub.ID,
	).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Errorf("subscriber_events count changed from %d to %d — a skip must write no new event (#0365's plan §3.4)", before, after)
	}
}

// TestOutboxWorker_EligibilityReadFailureRetriesRatherThanSends is #0365's
// plan §6 row 2: a transient eligibility-read error must fail CLOSED (an
// ordinary retry/backoff), never fail open into a send.
//
// This unit-tests sendGate directly rather than driving it through the
// full Run/pass/sendOne loop: the shared per-package test pool
// (outboxTestPool) cannot be torn down mid-test without breaking every
// other test sharing it (#0365 obstacle scope — no fixture here can
// manufacture a genuine network/DB fault on demand without doing that), so
// a context already canceled before the read is the practical way to
// force pool.QueryRow to return a real error rather than pgx.ErrNoRows.
// sendOne's own dispatch order — `if gateErr != nil { finishFailed...;
// return }` runs BEFORE the `if skip` branch (outbox_worker.go) — is what
// makes proving sendGate's own contract here (err != nil, skip == false)
// sufficient: an error can never reach finishSkipped by construction.
func TestOutboxWorker_EligibilityReadFailureRetriesRatherThanSends(t *testing.T) {
	pool := outboxTestPool(t)
	mailer := &RecordingMailer{}
	w, _ := newTestOutboxWorker(t, pool, mailer)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled — forces the pool round trip to fail, not return ErrNoRows

	subID := int64(1)
	row := outbox.Row{
		ID:           1,
		Kind:         outbox.KindConfirmation,
		Recipient:    "does-not-matter@example.com",
		SubscriberID: &subID,
	}

	skip, reason, err := w.sendGate(ctx, row)
	if err == nil {
		t.Fatalf("sendGate with an already-canceled context returned no error (skip=%v reason=%q) — a read failure must be reported as an error, not resolved silently either way", skip, reason)
	}
	if skip {
		t.Errorf("sendGate reported skip=true alongside a read error — a read failure must not be mistaken for a decided skip; the caller must route it to finishFailed, never finishSkipped")
	}
}

// TestGatedKindsPartitionEveryMailKind is #0365's plan §8 criterion 5:
// every mailKinds element appears in EXACTLY ONE of gatedKinds/ungatedKinds
// — no gap (a kind claimed by neither would silently skip the recheck with
// nothing to notice), no overlap (ambiguous about whether it's checked) —
// and every ungatedKinds value is non-empty (#0296 item 4: an allowlist
// anyone can append to without an argument is a guard with a hole).
//
// This lives IN the package rather than an external `scripts/check.sh
// guards` harness — CLAUDE.md §8's rule on where a guard's proof belongs
// turns on whether mutating the SUBJECT changes what the guard itself
// observes. Adding a kind to mailKinds, or deleting an entry from
// gatedKinds/ungatedKinds, changes THIS test's own inputs directly, so it
// is a non-circular oracle; the external-harness rule exists for
// floor/ceiling constants a test could trivially satisfy by mutating
// itself, which this is not.
//
// Mutation proof (#0365's ## Verification): add outbox.KindWhatever to
// internal/outbox and to mailKinds without classifying it in gatedKinds or
// ungatedKinds, and this test fails, naming it.
func TestGatedKindsPartitionEveryMailKind(t *testing.T) {
	for _, k := range mailKinds {
		_, inGated := gatedKinds[k]
		_, inUngated := ungatedKinds[k]
		switch {
		case inGated && inUngated:
			t.Errorf("outbox.Kind %q is in BOTH gatedKinds and ungatedKinds — must be exactly one", k)
		case !inGated && !inUngated:
			t.Errorf("outbox.Kind %q (in mailKinds) is in NEITHER gatedKinds nor ungatedKinds — a send-time recheck decision was never made for it", k)
		}
	}

	for k, reason := range ungatedKinds {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("ungatedKinds[%q] has an empty or whitespace-only reason — an exemption from the send-time recheck must be deliberate, not silent", k)
		}
	}

	// Reverse direction: gatedKinds/ungatedKinds must not name a kind that
	// mailKinds itself does not — TestMailKindsCoversEveryOutboxKind
	// already guards mailKinds against drifting away from internal/outbox;
	// this guards these two maps against drifting away from mailKinds.
	inMailKinds := map[outbox.Kind]bool{}
	for _, k := range mailKinds {
		inMailKinds[k] = true
	}
	for k := range gatedKinds {
		if !inMailKinds[k] {
			t.Errorf("gatedKinds names outbox.Kind %q, which is not in mailKinds", k)
		}
	}
	for k := range ungatedKinds {
		if !inMailKinds[k] {
			t.Errorf("ungatedKinds names outbox.Kind %q, which is not in mailKinds", k)
		}
	}
}

// TestOutboxWorker_SkipsWhenRecipientDiffersFromLiveEmail pins #0365's
// THIRD predicate — the live-email drift check copied from
// RecheckEligibleTx (audience.go) — which shipped with no test of its own
// (#0365's review; deleting `case elig.Email != row.Recipient` from
// sendGate left the whole suite green).
//
// The predicate exists so a suppression/status check is never evaluated
// against one address while the message goes to another: here the
// subscriber is 'active' and unsuppressed, so status and suppression both
// say "send" and ONLY the drift predicate can stop it.
func TestOutboxWorker_SkipsWhenRecipientDiffersFromLiveEmail(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)
	subStore := subscribers.NewStore(pool)

	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: uniqueOutboxRecipient(t), ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sub.ConfirmToken == nil {
		t.Fatal("Create returned a subscriber with no confirm_token")
	}
	if _, err := subStore.Confirm(context.Background(), *sub.ConfirmToken, time.Now()); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	// Confirm activates the subscriber, so delete it before any
	// AudienceAll-scoped test elsewhere in this package's SHARED database
	// can observe it — the same pattern
	// TestOutboxWorker_MarkSent_RecordsWelcomeSent uses.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, sub.ID)
	})

	// The queue row names a DIFFERENT address than the live
	// subscribers.email it carries a subscriber_id for.
	other := uniqueOutboxRecipient(t)
	subID := sub.ID
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:         outbox.KindWelcome,
		Recipient:    other,
		SubscriberID: &subID,
		Payload: map[string]any{
			"manage_token":   sub.ManageToken,
			"interest_names": []string{"Soldering"},
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	status := waitForQueueStatus(t, pool, id)
	if status != outbox.StatusSkipped {
		t.Fatalf("status = %q after waiting, want %q — a row whose recipient is not the live subscribers.email must be skipped", status, outbox.StatusSkipped)
	}
	if got := sentTo(mailer, other); got != 0 {
		t.Fatalf("messages sent to %s = %d, want 0 — mail must never go to an address the eligibility check never evaluated", other, got)
	}
}

// TestOutboxWorker_SendGate_UngatedKindsNeverSkip pins the
// gatedKinds/ungatedKinds MEMBERSHIP decision itself, which
// TestOutboxWorker_SendsRegistrationToSuppressedAddress does not (see that
// test's own corrected doc comment): it drives sendGate directly with a
// NON-nil SubscriberID, so the nil-SubscriberID fail-open cannot mask the
// map lookup, and every ungated kind must still report skip=false against
// a subscriber that is both 'complained' AND suppressed — the worst state
// an address can be in.
//
// The gated control arm is what makes this falsifiable rather than
// vacuous: the SAME subscriber must produce skip=true for a gated kind, so
// a fixture that had silently stopped being ineligible would fail here
// rather than passing every ungated assertion for the wrong reason.
//
// What this DOES catch, measured rather than asserted (#0365's review):
// sendGate losing its `if !gated { return }` early return — the literal
// "someone simplifies the gate to cover all kinds" regression — fails here
// naming all five ungated kinds.
//
// What it does NOT catch, and what no in-package test can: a kind
// DELIBERATELY moved from ungatedKinds into gatedKinds. This test iterates
// ungatedKinds, so such a move removes the kind from the test's own inputs
// — CLAUDE.md §8's "a guard's oracle must not be the same bytes as its
// subject". That is a policy change rather than silent drift, and the
// protection for it is ungatedKinds' written reason plus review.
// TestGatedKindsPartitionEveryMailKind covers the silent case (a kind in
// NEITHER map), which is the gap that can actually open by accident.
func TestOutboxWorker_SendGate_UngatedKindsNeverSkip(t *testing.T) {
	pool := outboxTestPool(t)
	mailer := &RecordingMailer{}
	w, _ := newTestOutboxWorker(t, pool, mailer)
	subStore := subscribers.NewStore(pool)
	suppStore := subscribers.NewSuppressionStore(pool)

	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: uniqueOutboxRecipient(t), ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := subStore.MarkComplained(context.Background(), sub.ID, time.Now()); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}
	if _, err := suppStore.Add(context.Background(), subscribers.NewSuppression{
		Email: sub.Email, Reason: subscribers.SuppressionReasonManual,
	}, time.Now()); err != nil {
		t.Fatalf("Add suppression: %v", err)
	}

	subID := sub.ID
	row := func(kind outbox.Kind) outbox.Row {
		return outbox.Row{ID: 1, Kind: kind, Recipient: sub.Email, SubscriberID: &subID}
	}

	// Control: a GATED kind against this same subscriber must skip. If
	// this arm ever stops failing the send, every assertion below is
	// vacuous.
	skip, _, err := w.sendGate(context.Background(), row(outbox.KindConfirmation))
	if err != nil {
		t.Fatalf("sendGate (gated control): %v", err)
	}
	if !skip {
		t.Fatalf("sendGate reported skip=false for a GATED kind against a complained+suppressed subscriber — the fixture is not producing an ineligible subscriber, so this test's ungated assertions prove nothing")
	}

	for kind := range ungatedKinds {
		skip, reason, err := w.sendGate(context.Background(), row(kind))
		if err != nil {
			t.Errorf("sendGate(%q): %v", kind, err)
			continue
		}
		if skip {
			t.Errorf("sendGate(%q) reported skip=true (%q) — %q is in ungatedKinds and must never be withheld on list state; gating auth/staff mail on a marketing suppression is an availability failure introduced by a safety feature (#0365's plan §2)", kind, reason, kind)
		}
	}
}

// TestOutboxWorker_SkipsDeferredImportInviteWhoseConfirmTokenWasSwept is
// #0340's primary mutation proof: an import_invite row deferred by
// deferMissingPhysicalAddress must not be delivered once its confirm_token
// has been swept out from under it by a REAL
// subscribers.Store.ExpirePendingSweep — which clears confirm_token to NULL
// while deliberately leaving the subscriber 'pending' (#0128), so the
// status predicate alone cannot see this. Modeled on
// TestOutboxWorker_DefersImportInvite_MissingPhysicalAddress (the
// claim-then-defer poll) and TestOutboxWorker_DefersWelcome_MissingPhysicalAddress
// (forcing next_attempt_at due rather than waiting out Backoff's 1-minute
// first step). The rotation goes through the REAL production path, never a
// raw UPDATE of confirm_token itself (CLAUDE.md §8b), so this proves the
// fix against the exact mechanism that produces the hazard in production.
//
// Mutation (#0340's plan §8, M1): against the un-fixed code (no
// tokenGatedKinds predicate in sendGate), this row is delivered 'sent',
// carrying a confirm_token that resolves to nothing — every assertion below
// after the sweep fails.
func TestOutboxWorker_SkipsDeferredImportInviteWhoseConfirmTokenWasSwept(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "")

	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)
	subStore := subscribers.NewStore(pool)

	email := uniqueOutboxRecipient(t)
	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: email, ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, sub.ID)
	})
	if sub.ConfirmToken == nil {
		t.Fatal("Create returned a subscriber with no confirm_token")
	}
	liveToken := *sub.ConfirmToken

	// subStore.Create also enqueues an ordinary 'confirmation' row for the
	// same subscriber/recipient (drained normally by this test's own
	// worker below, before the sweep below rotates anything — see
	// TestOutboxWorker_DefersWelcome_MissingPhysicalAddress's identical
	// comment), so "nothing invited went out" is scoped to the invitation
	// subject specifically, not to mailer.Sent()'s raw count.
	const inviteSubject = "You're invited to the Open Circuit SF mailing list"
	countInviteSent := func() int {
		n := 0
		for _, m := range mailer.Sent() {
			if m.To == email && m.Subject == inviteSubject {
				n++
			}
		}
		return n
	}

	subID := sub.ID
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:         outbox.KindImportInvite,
		Recipient:    email,
		SubscriberID: &subID,
		Payload: map[string]any{
			"confirm_token": liveToken,
			"manage_token":  sub.ManageToken,
			"ttl_seconds":   int64(7 * 24 * 3600),
			"import_source": "csv",
			"source_detail": "test-import.csv",
			"collected_at":  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	// Wait for a full claim-then-defer cycle to settle back to 'queued' —
	// proving this row is genuinely DEFERRED (#0340's premise), not merely
	// unclaimed. Mirrors TestOutboxWorker_DefersImportInvite_MissingPhysicalAddress's
	// own comment on why 'sending' is a real intermediate state here.
	deadline := time.Now().Add(2 * time.Second)
	var status string
	var attempts int
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(),
			`SELECT status, attempts FROM outbound_queue WHERE id = $1`, id,
		).Scan(&status, &attempts); err != nil {
			t.Fatalf("select: %v", err)
		}
		if attempts >= 1 && status == outbox.StatusQueued {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if attempts < 1 {
		t.Fatal("worker never attempted the import-invite row within the deadline")
	}
	if status != outbox.StatusQueued {
		t.Fatalf("status = %q after a deferred attempt, want %q (deferred, not abandoned/sent)", status, outbox.StatusQueued)
	}
	if n := countInviteSent(); n != 0 {
		t.Fatalf("invitation messages sent = %d, want 0 before physical_address is configured", n)
	}

	// Rotate through the REAL production path: back-date
	// confirm_expires_at, then run the real sweep. The row stays 'pending';
	// confirm_token goes NULL (ExpirePendingSweep's own doc comment).
	if _, err := pool.Exec(context.Background(),
		`UPDATE subscribers SET confirm_expires_at = $1 WHERE id = $2`,
		time.Now().Add(-time.Minute), sub.ID,
	); err != nil {
		t.Fatalf("back-dating confirm_expires_at: %v", err)
	}
	if _, err := subStore.ExpirePendingSweep(context.Background(), time.Now()); err != nil {
		t.Fatalf("ExpirePendingSweep: %v", err)
	}
	var liveNow *string
	if err := pool.QueryRow(context.Background(), `SELECT confirm_token FROM subscribers WHERE id = $1`, sub.ID).Scan(&liveNow); err != nil {
		t.Fatalf("select confirm_token: %v", err)
	}
	if liveNow != nil {
		t.Fatalf("confirm_token = %q after ExpirePendingSweep, want NULL", *liveNow)
	}

	// The row is otherwise fully deliverable now — physical_address is set,
	// so the defer is no longer the reason anything is withheld — and
	// force it due immediately rather than waiting out Backoff's 1-minute
	// first step (the same technique
	// TestOutboxWorker_DefersWelcome_MissingPhysicalAddress uses).
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	if _, err := pool.Exec(context.Background(),
		`UPDATE outbound_queue SET next_attempt_at = now() WHERE id = $1 AND status = $2`,
		id, outbox.StatusQueued,
	); err != nil {
		t.Fatalf("forcing due: %v", err)
	}

	finalStatus := waitForQueueStatus(t, pool, id)
	if finalStatus != outbox.StatusSkipped {
		t.Fatalf("status = %q after waiting, want %q — a swept confirm_token must withhold the deferred row rather than let it send", finalStatus, outbox.StatusSkipped)
	}
	var errText *string
	if err := pool.QueryRow(context.Background(), `SELECT error FROM outbound_queue WHERE id = $1`, id).Scan(&errText); err != nil {
		t.Fatalf("select error: %v", err)
	}
	if errText == nil || !strings.Contains(*errText, "confirm token") {
		t.Errorf("error = %v, want a reason naming the confirm token", errText)
	}
	if n := countInviteSent(); n != 0 {
		t.Fatalf("invitation messages sent = %d, want 0 — a link built from a swept token must never be delivered", n)
	}
}

// TestOutboxWorker_SkipsConfirmationWhoseConfirmTokenWasRotated is #0340's
// second mutation proof, and does double duty: it proves BOTH halves of
// criterion 1 at once — the stale pre-resend row must be withheld, AND the
// legitimate replacement subscribers.Store.AdminResendConfirmation enqueues
// must still be delivered. A careless "cancel the whole subscriber's
// confirmation mail on any rotation" implementation would pass the first
// half and fail the second; this test would catch that.
//
// Mutation (#0340's plan §8, M2): against the un-fixed code, BOTH rows send
// and the address receives two confirmations — the second assertion and the
// exactly-one-message assertion both fail.
func TestOutboxWorker_SkipsConfirmationWhoseConfirmTokenWasRotated(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	mailer := &RecordingMailer{}
	w, _ := newTestOutboxWorker(t, pool, mailer)
	subStore := subscribers.NewStore(pool)

	email := uniqueOutboxRecipient(t)
	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: email, ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, sub.ID)
	})

	// AdminResendConfirmation mints a fresh confirm_token and enqueues a
	// SECOND outbox.KindConfirmation row in its own transaction, leaving
	// the FIRST (Create's) row's payload carrying the now-stale token.
	// cooldown=0 because Create already stamped confirm_sent_at
	// (ErrResendCooldownActive otherwise).
	if _, err := subStore.AdminResendConfirmation(context.Background(), sub.ID, time.Now(), 0, time.Hour); err != nil {
		t.Fatalf("AdminResendConfirmation: %v", err)
	}

	var ids []int64
	rows, err := pool.Query(context.Background(),
		`SELECT id FROM outbound_queue WHERE kind = $1 AND subscriber_id = $2 ORDER BY id`,
		outbox.KindConfirmation, sub.ID,
	)
	if err != nil {
		t.Fatalf("query outbound_queue rows: %v", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("outbound_queue confirmation rows for subscriber %d = %d, want 2 (Create's original plus the admin resend)", sub.ID, len(ids))
	}
	staleID, freshID := ids[0], ids[1]

	runWorkerUntilStopped(t, w)

	staleStatus := waitForQueueStatus(t, pool, staleID)
	if staleStatus != outbox.StatusSkipped {
		t.Errorf("stale (pre-resend) row status = %q after waiting, want %q — its payload confirm_token no longer matches the live one", staleStatus, outbox.StatusSkipped)
	}
	var staleErr *string
	if err := pool.QueryRow(context.Background(), `SELECT error FROM outbound_queue WHERE id = $1`, staleID).Scan(&staleErr); err != nil {
		t.Fatalf("select stale row error: %v", err)
	}
	if staleErr == nil || !strings.Contains(*staleErr, "confirm token") {
		t.Errorf("stale row error = %v, want a reason naming the confirm token", staleErr)
	}

	freshStatus := waitForQueueStatus(t, pool, freshID)
	if freshStatus != outbox.StatusSent {
		t.Errorf("fresh (resend) row status = %q after waiting, want %q — the legitimate resend must still be delivered", freshStatus, outbox.StatusSent)
	}

	if got := sentTo(mailer, email); got != 1 {
		t.Errorf("messages sent to %s = %d, want exactly 1 — the stale row must be withheld while the fresh resend still goes out, never both", email, got)
	}
}

// TestOutboxWorker_TokenGateDoesNotShortCircuitPhysicalAddressDefer pins
// #0340's plan §6 / acceptance criterion 3: a row carrying a VALID,
// unrotated confirm_token must still take render's ordinary
// deferMissingPhysicalAddress path when physical_address is blank — it must
// settle back to 'queued' with attempts climbing, never 'skipped', never
// sent. #0045/#0264's CAN-SPAM refusal (CLAUDE.md §9) is a restricted rule
// and this issue's token predicate must not be able to weaken it.
//
// Unlike the pre-existing TestOutboxWorker_DefersImportInvite_MissingPhysicalAddress
// (which enqueues with a nil SubscriberID and so never reaches
// SendEligibility at all — see #0365's own "trap" note in its Verification
// section), this test sets a real SubscriberID, so it is the first test in
// this file to drive sendGate's FULL chain — status, suppression, email,
// AND the token predicate — at the same time render's physical-address
// defer fires.
//
// Mutation (#0340's plan §8, M3): make tokenGate skip unconditionally
// (ignore the token comparison) and this row becomes 'skipped' instead of
// staying 'queued' — failing this test. The pre-existing
// TestOutboxWorker_DefersImportInvite_MissingPhysicalAddress,
// TestOutboxWorker_DefersWelcome_MissingPhysicalAddress, and
// TestAdminResendInvitation_PhysicalAddressGateNotBypassable all pass
// UNMODIFIED alongside this one — see #0340's ## Verification.
func TestOutboxWorker_TokenGateDoesNotShortCircuitPhysicalAddressDefer(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "")

	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)
	subStore := subscribers.NewStore(pool)

	email := uniqueOutboxRecipient(t)
	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: email, ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, sub.ID)
	})
	if sub.ConfirmToken == nil {
		t.Fatal("Create returned a subscriber with no confirm_token")
	}

	const inviteSubject = "You're invited to the Open Circuit SF mailing list"
	countInviteSent := func() int {
		n := 0
		for _, m := range mailer.Sent() {
			if m.To == email && m.Subject == inviteSubject {
				n++
			}
		}
		return n
	}

	subID := sub.ID
	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:         outbox.KindImportInvite,
		Recipient:    email,
		SubscriberID: &subID,
		Payload: map[string]any{
			"confirm_token": *sub.ConfirmToken, // the LIVE token — unrotated
			"manage_token":  sub.ManageToken,
			"ttl_seconds":   int64(7 * 24 * 3600),
			"import_source": "csv",
			"source_detail": "test-import.csv",
			"collected_at":  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	runWorkerUntilStopped(t, w)

	deadline := time.Now().Add(2 * time.Second)
	var status string
	var attempts int
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(),
			`SELECT status, attempts FROM outbound_queue WHERE id = $1`, id,
		).Scan(&status, &attempts); err != nil {
			t.Fatalf("select: %v", err)
		}
		if attempts >= 1 && status == outbox.StatusQueued {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if attempts < 1 {
		t.Fatal("worker never attempted the row within the deadline")
	}
	if status != outbox.StatusQueued {
		t.Fatalf("status = %q, want %q — a valid, unrotated token must still take the physical_address defer path, never %q", status, outbox.StatusQueued, outbox.StatusSkipped)
	}
	if n := countInviteSent(); n != 0 {
		t.Fatalf("invitation messages sent = %d, want 0", n)
	}
}

// TestOutboxWorker_SkipsImportInviteAfterRestartSignup is #0400's second
// half: the fourth of #0340's plan's four stale-row sites,
// (*subscribers.Store).RestartSignup, which M1/M2/M3 above do not cover.
// It is the sharpest of the four because it moves status BACK to 'pending'
// while minting a fresh confirm_token — re-opening gatedKinds' own status
// check (sendGate's first predicate) on a row that still carries the
// PRE-restart token in its payload. Without #0340's tokenGate predicate,
// the status check alone would see 'pending' and wave the stale row
// through.
//
// Sequence: create (pending, confirm_token A) -> enqueue an import_invite
// row carrying A -> Unsubscribe (status -> unsubscribed) -> RestartSignup
// (status -> pending again, confirm_token -> B) -> run the worker -> the
// queued row, which still carries A, must be 'skipped', never delivered.
//
// Mutation proof (recorded in ## Verification): disabling tokenGate's
// elig.ConfirmToken comparison — the same Mutation B shape #0340's review
// applied to M1/M2 — delivers this row 'sent' with the stale token A
// instead of 'skipped', in a git-archive export against a private scratch
// database (CLAUDE.md §8b), never the tracked tree.
func TestOutboxWorker_SkipsImportInviteAfterRestartSignup(t *testing.T) {
	pool := outboxTestPool(t)
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")

	mailer := &RecordingMailer{}
	w, store := newTestOutboxWorker(t, pool, mailer)
	subStore := subscribers.NewStore(pool)

	email := uniqueOutboxRecipient(t)
	sub, err := subStore.Create(context.Background(), subscribers.NewSignup{
		Email: email, ConfirmTTL: time.Hour,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, sub.ID)
	})
	if sub.ConfirmToken == nil {
		t.Fatal("Create returned a subscriber with no confirm_token")
	}
	preRestart := *sub.ConfirmToken

	subID := sub.ID
	inviteID, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:         outbox.KindImportInvite,
		Recipient:    email,
		SubscriberID: &subID,
		Payload: map[string]any{
			"confirm_token": preRestart, // the PRE-restart token — must go stale
			"manage_token":  sub.ManageToken,
			"ttl_seconds":   int64(7 * 24 * 3600),
			"import_source": "csv",
			"source_detail": "restart-signup-test.csv",
			"collected_at":  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if _, err := subStore.Unsubscribe(context.Background(), sub.ID, "one_click", time.Now()); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	restarted, err := subStore.RestartSignup(context.Background(), sub.ID,
		subscribers.RestartSignupInput{ConfirmTTL: time.Hour}, time.Now())
	if err != nil {
		t.Fatalf("RestartSignup: %v", err)
	}
	if restarted.Status != subscribers.StatusPending {
		t.Fatalf("status after RestartSignup = %q, want %q", restarted.Status, subscribers.StatusPending)
	}
	if restarted.ConfirmToken == nil || *restarted.ConfirmToken == preRestart {
		t.Fatal("RestartSignup did not mint a fresh confirm_token")
	}

	runWorkerUntilStopped(t, w)

	// #0401's review of #0400: this delivery check previously sat AFTER
	// the status Fatalf below, so it could never be the assertion that
	// fired — a wrong status always stopped the test first, on exactly the
	// scenario this loop exists to catch. Checked first and independently
	// instead, as t.Errorf so the status assertion below still runs (and
	// still reports) even if this one already failed.
	const inviteSubject = "You're invited to the Open Circuit SF mailing list"
	for _, m := range mailer.Sent() {
		if m.To == email && m.Subject == inviteSubject {
			t.Errorf("an invitation carrying the pre-restart confirm_token was delivered")
		}
	}

	got := waitForQueueStatus(t, pool, inviteID)
	if got != outbox.StatusSkipped {
		t.Fatalf("import_invite row status = %q, want %q — RestartSignup re-opened the status gate on a row still carrying the PRE-restart confirm_token", got, outbox.StatusSkipped)
	}
	var errText *string
	if err := pool.QueryRow(context.Background(), `SELECT error FROM outbound_queue WHERE id = $1`, inviteID).Scan(&errText); err != nil {
		t.Fatalf("select error: %v", err)
	}
	// #0401's review of #0400: %v on a *string prints the pointer address,
	// not the text it points to — a failure here used to read like
	// `error = 0x70b3dcca0500`. Dereference so a failure shows the actual
	// reason.
	switch {
	case errText == nil:
		t.Errorf("error = <nil>, want a reason naming the confirm token")
	case !strings.Contains(*errText, "confirm token"):
		t.Errorf("error = %q, want a reason naming the confirm token", *errText)
	}

	// CLAUDE.md §8b: never let a real token value land in a committed
	// artifact (outbound_queue.error is read by admins and can end up
	// pasted into an issue file).
	if errText != nil && (strings.Contains(*errText, preRestart) || strings.Contains(*errText, *restarted.ConfirmToken)) {
		t.Fatalf("outbound_queue.error leaked a confirm_token value")
	}
}

// TestTokenGatedKindsPartitionEveryMailKind is #0340's plan §8 criterion 4's
// coverage guard, a direct sibling of TestGatedKindsPartitionEveryMailKind
// above: every mailKinds element must appear in EXACTLY ONE of
// tokenGatedKinds/tokenUngatedKinds — no gap, no overlap — and every
// tokenUngatedKinds value must be non-empty (#0296 item 4's
// kindExceptions convention). Lives in-package for the identical reason
// its sibling does (CLAUDE.md §8: mutating mailKinds/tokenGatedKinds/
// tokenUngatedKinds changes THIS test's own inputs directly, a non-circular
// oracle).
//
// Mutation (#0340's plan §8, M4): delete outbox.KindImportInvite from both
// maps — this test fails, naming it.
func TestTokenGatedKindsPartitionEveryMailKind(t *testing.T) {
	for _, k := range mailKinds {
		_, inGated := tokenGatedKinds[k]
		_, inUngated := tokenUngatedKinds[k]
		switch {
		case inGated && inUngated:
			t.Errorf("outbox.Kind %q is in BOTH tokenGatedKinds and tokenUngatedKinds — must be exactly one", k)
		case !inGated && !inUngated:
			t.Errorf("outbox.Kind %q (in mailKinds) is in NEITHER tokenGatedKinds nor tokenUngatedKinds — a token-rotation recheck decision was never made for it", k)
		}
	}

	for k, reason := range tokenUngatedKinds {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("tokenUngatedKinds[%q] has an empty or whitespace-only reason — an exemption from the token recheck must be deliberate, not silent", k)
		}
	}

	// Reverse direction: tokenGatedKinds/tokenUngatedKinds must not name a
	// kind mailKinds itself does not, mirroring
	// TestGatedKindsPartitionEveryMailKind's own reverse check.
	inMailKinds := map[outbox.Kind]bool{}
	for _, k := range mailKinds {
		inMailKinds[k] = true
	}
	for k := range tokenGatedKinds {
		if !inMailKinds[k] {
			t.Errorf("tokenGatedKinds names outbox.Kind %q, which is not in mailKinds", k)
		}
	}
	for k := range tokenUngatedKinds {
		if !inMailKinds[k] {
			t.Errorf("tokenUngatedKinds names outbox.Kind %q, which is not in mailKinds", k)
		}
	}
}
