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
// Deliberately does NOT go through subscribers.Store.Confirm to produce the
// welcome row (that atomicity — Confirm enqueueing welcome inside its own
// transaction — is proven in internal/subscribers/store_test.go instead):
// Confirm activates the subscriber, and internal/mailing's own
// worker_store_test.go has several AudienceAll-scoped preflight tests that
// assert "no subscribers seeded" against this package's SHARED test
// database (TestMain truncates once per package run, not per test — see
// its own doc comment). An active subscriber left behind by this test would
// silently pollute those counts depending on file-order execution. Enqueuing
// the welcome row directly, against a subscriber left in 'pending', proves
// the exact same OutboxWorker.sendOne wiring (kind=welcome,
// row.SubscriberID != nil → RecordEvent ActionWelcomeSent) without that
// side effect.
func TestOutboxWorker_MarkSent_RecordsWelcomeSent(t *testing.T) {
	pool := outboxTestPool(t)
	// #0264: a blank physical_address (the seeded default) now defers a
	// welcome instead of sending it, which would leave welcome_sent
	// unwritten and stall this test's whole point.
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
	// sub stays 'pending' — this test never calls Confirm — so it is
	// invisible to any AudienceAll (status='active') query elsewhere in
	// this package's shared test database.

	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:         outbox.KindWelcome,
		Recipient:    sub.Email,
		SubscriberID: &sub.ID,
		Payload:      map[string]any{"manage_token": sub.ManageToken, "interest_names": []string{}},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
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

	id, err := store.Enqueue(context.Background(), outbox.Item{
		Kind:         outbox.KindImportInvite,
		Recipient:    sub.Email,
		SubscriberID: &sub.ID,
		Payload: map[string]any{
			"confirm_token": "the-confirm-token",
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
