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
// through the mailer, and marked sent with its payload scrubbed.
func TestOutboxWorker_DrainsWelcomeRow(t *testing.T) {
	pool := outboxTestPool(t)
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
// exercise Stop's release path (see TestOutboxWorker_Stop_ReleasesClaimedRow
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
	// (e.g. TestOutboxWorker_Stop_ReleasesClaimedRow, which depends on
	// claiming its OWN two rows first) — so it must be removed
	// explicitly, the same reasoning as that test's own cleanup.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE id = $1`, id)
	})

	// Simulate a worker that claimed this row and then the process was
	// asked to stop before it finished sending — exactly the state
	// OutboxWorker.Stop's release path exists for.
	if _, err := store.ClaimDue(context.Background(), 10); err != nil {
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
// TestOutboxWorker_Stop_ReleasesClaimedRow deterministic: a way to hold a
// real OutboxWorker's pass loop paused mid-send on the FIRST row of a
// two-row batch, so the SECOND (already claimed, never started) row is
// still 'sending' at the exact moment Stop is called — no sleeps, no
// timing luck.
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

// TestOutboxWorker_Stop_ReleasesClaimedRow is #0126's phase-3 review defect
// 2's regression test: it constructs a REAL *OutboxWorker, runs it, and
// calls Stop — unlike the old same-named test (renamed above to
// TestOutbox_Release_RequeuesClaimedRow), which never did either. It
// reproduces the review's executed proof ("after Stop: sending=1
// queued=0") and then asserts the fix: after Stop returns, the row that
// was still claimed when it was called is back to 'queued', not left
// 'sending' for the orphan sweep to find up to outboxOrphanStaleAfter
// later.
func TestOutboxWorker_Stop_ReleasesClaimedRow(t *testing.T) {
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
	// 'queued' by Stop's release. A 'queued', due-now confirmation row
	// left behind after this test returns is picked up by ANY later
	// OutboxWorker.ClaimDue call sharing this database — including other
	// tests in this same package/run — so it must be removed explicitly
	// rather than relying on ending in a terminal status the way every
	// other test in this file does.
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

	// Both rows are claimed now: row 1 is blocked inside sendOne (past
	// ClaimDue, past trackClaimed), row 2 is still waiting in pass's loop,
	// never having reached its own stopCh check yet.
	var status2 string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id2).Scan(&status2); err != nil {
		t.Fatalf("select id2 before stop: %v", err)
	}
	if status2 != outbox.StatusSending {
		t.Fatalf("row 2 status before Stop = %q, want %q (test setup invalid)", status2, outbox.StatusSending)
	}

	// Call Stop concurrently — it must block on <-w.doneCh, which cannot
	// close until pass returns, which cannot happen until row 1's blocked
	// Send unblocks. This is the exact ordering the review's proof used:
	// Stop is already in flight, THEN the send is released.
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

	var status1 string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id1).Scan(&status1); err != nil {
		t.Fatalf("select id1 after stop: %v", err)
	}
	if status1 != outbox.StatusSent {
		t.Fatalf("row 1 (unblocked and finished before Stop returned) status = %q, want %q", status1, outbox.StatusSent)
	}

	if err := pool.QueryRow(context.Background(), `SELECT status FROM outbound_queue WHERE id = $1`, id2).Scan(&status2); err != nil {
		t.Fatalf("select id2 after stop: %v", err)
	}
	if status2 != outbox.StatusQueued {
		t.Fatalf("row 2 (claimed but never sent) status after Stop = %q, want %q — Stop must release claimed-but-unsent rows, not leave them for the orphan sweep (defect 2)", status2, outbox.StatusQueued)
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
