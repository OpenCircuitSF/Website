package handlers

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// intakeRowStatus reads back a KindSubscribeIntake row's status for
// recipient — this file's own narrow helper, since outboundQueueRowsFor
// (subscribe_test.go) deliberately EXCLUDES this kind.
func intakeRowStatus(t *testing.T, pool *pgxpool.Pool, recipient string) (status string, ok bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT status FROM outbound_queue WHERE kind = $1 AND recipient = $2 ORDER BY id DESC LIMIT 1`,
		string(outbox.KindSubscribeIntake), recipient,
	).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", false
	case err != nil:
		t.Fatalf("reading intake row status for %q: %v", recipient, err)
	}
	return status, true
}

// TestSubscribe_MutationDropped_IntakeRowStaysDurablyQueued is #0254's
// "queue-full is handled deliberately, not silently dropped" proof
// (criterion 4) and half of criterion 1 ("a 202 implies the signup is
// durably recorded"). It forces the exact drop path enqueueMutation's
// closeMu-guarded branch takes — Close() has already run, so h.closed is
// true — and confirms Subscribe still returns its uniform 202 (CLAUDE.md
// §9: an internal drop must never change the response) while a
// KindSubscribeIntake row survives in Postgres, 'queued', for the
// subscriber's own recovery poller (or an operator) to reconcile — never
// silently discarded with nothing to show for the accepted request.
//
// Close() is called BEFORE Subscribe() runs, deliberately: Subscribe's own
// synchronous work (the two fixed reads, and #0254's durability write) does
// not depend on h.sendCtx/h.closed at all — only enqueueMutation's
// channel send does — so this reproduces exactly what a request accepted
// in the narrow window right before shutdown finishes would experience:
// its mutation job is refused, but its intake row was already written.
func TestSubscribe_MutationDropped_IntakeRowStaysDurablyQueued(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	email := subscribeUniqueEmail(t)
	resp := doSubscribeFrom(t, nil, mux, subscribeBody(email, nil, time.Now()), "", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (CLAUDE.md §9: a dropped mutation must not change the response)", resp.StatusCode)
	}

	status, ok := intakeRowStatus(t, pool, email)
	if !ok {
		t.Fatal("no KindSubscribeIntake row exists for this address — an accepted-but-dropped request left no durable record at all")
	}
	if status != outbox.StatusQueued {
		t.Errorf("intake row status = %q, want %q (nothing ever claimed or processed it — mutateQueue was closed and the recovery poller was stopped by the same Close)", status, outbox.StatusQueued)
	}

	// The mutation itself must genuinely have been dropped, not silently
	// processed some other way — confirms this test is actually exercising
	// the drop path it claims to.
	if subscriberExists(t, pool, email) {
		t.Fatal("a subscriber row exists — the mutation was not actually dropped; this test's premise is wrong")
	}
}

// TestSubscribeIntakeWorker_RecoversRowTheFastPathNeverProcessed is #0254's
// criterion 5 at the unit level: a KindSubscribeIntake row nothing has
// processed yet (standing in for one orphaned by a process kill between
// Subscribe's durability write and mutateQueue ever draining it) is fully
// reconciled once the recovery poller reaches it — a real subscribers row
// is created and a real confirmation is queued, with no second request from
// the client and no explicit trigger from this test.
//
// This drives the SAME h returned by subscribeMux, whose NewSubscribeHandler
// call already started runIntakeWorker as a background goroutine (see that
// function's doc comment) — deliberately not called directly: h.intakePass
// is unexported precisely because runIntakeWorker already owns polling it on
// its own ticker, and calling it a second time from the test goroutine would
// race the very goroutine this test means to prove works, occasionally
// losing that race (ClaimDue's SKIP LOCKED simply hands the row to whichever
// caller asks first) and reporting a false negative. Polling for the
// OUTCOME — bounded, well past intakePollInterval — proves the real
// mechanism instead of a hand-invoked stand-in for it.
func TestSubscribeIntakeWorker_RecoversRowTheFastPathNeverProcessed(t *testing.T) {
	pool := subscribeTestPool(t)
	h, _ := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)

	// Bypass Subscribe/mutateQueue entirely: enqueue the row exactly the
	// way Subscribe's durability write does, but with no Delay, standing in
	// for a row whose intakeGraceDelay has already elapsed with nothing
	// having claimed it — the state a genuinely orphaned row is in by the
	// time the recovery poller reaches it for real.
	if _, err := h.intake.Enqueue(context.Background(), outbox.Item{
		Kind:      outbox.KindSubscribeIntake,
		Recipient: email,
		Payload: subscribeIntakePayload{
			ConfirmTTLSeconds: int64(subscribeConfirmTTL.Seconds()),
		},
	}); err != nil {
		t.Fatalf("seeding an orphaned intake row: %v", err)
	}

	deadline := time.Now().Add(intakePollInterval*2 + 5*time.Second)
	for !subscriberExists(t, pool, email) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !subscriberExists(t, pool, email) {
		t.Fatal("no subscriber row appeared — the recovery poller never reconciled the orphaned intake row within the deadline")
	}

	id, status, confirmToken, confirmSentAt := subscriberRow(t, pool, email)
	if status != subscribers.StatusPending {
		t.Errorf("status = %q, want %q — the orphaned intake row was not reconciled into a real signup", status, subscribers.StatusPending)
	}
	if confirmToken == nil || *confirmToken == "" {
		t.Error("confirm_token is nil/empty")
	}
	if confirmSentAt == nil {
		t.Error("confirm_sent_at is nil, want stamped by the recovered Create")
	}

	rows := outboundQueueRowsFor(t, pool, email)
	if len(rows) != 1 {
		t.Fatalf("outbound_queue mail rows for %q = %d, want 1 (the confirmation the recovered signup enqueued)", email, len(rows))
	}
	if rows[0].Kind != string(outbox.KindConfirmation) {
		t.Errorf("kind = %q, want %q", rows[0].Kind, outbox.KindConfirmation)
	}
	if rows[0].SubscriberID == nil || *rows[0].SubscriberID != id {
		t.Errorf("subscriber_id = %v, want %d", rows[0].SubscriberID, id)
	}

	// The intake row itself must eventually be marked done — otherwise a
	// later pass would reprocess it forever. Bounded poll too: MarkSent
	// happens right after dispatchMutation returns, which may be a moment
	// after the subscriber row above became visible.
	intakeDeadline := time.Now().Add(2 * time.Second)
	var intakeStatus string
	var ok bool
	for time.Now().Before(intakeDeadline) {
		intakeStatus, ok = intakeRowStatus(t, pool, email)
		if ok && intakeStatus == outbox.StatusSent {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ok {
		t.Fatal("the intake row itself disappeared — expected it marked 'sent', not deleted")
	}
	if intakeStatus != outbox.StatusSent {
		t.Errorf("intake row status = %q, want %q", intakeStatus, outbox.StatusSent)
	}
}

// TestSubscribeIntakeWorker_OrphanSweepDoesNotTouchOtherKinds is #0254's
// review-bounce regression test (issues/0254.md ## Review notes). Commit
// 4af0ea3 added a kinds filter to outbox.Store.ClaimDue but NOT to
// OrphanSweep, and gave OrphanSweep a second caller — this file's own
// intakePass — with a much shorter staleness window (intakeOrphanStaleAfter
// = 20s) than internal/mailing.OutboxWorker's own sweep uses for the SAME
// method (outboxOrphanStaleAfter = 70s, sized for that worker's legitimate
// hold: sendMessageTimeout 30s + writeStatusTimeout 5s, doubled).
//
// Before the fix, intakePass's unfiltered OrphanSweep(20s) reclaimed ANY
// row stuck 'sending' past 20s — including a confirmation row internal/
// mailing's worker was still legitimately holding mid-send at 25s, well
// inside ITS OWN 70s window. Releasing that live claim is not cosmetic:
// the in-flight send's eventual MarkSent requires status='sending', so it
// affects zero rows (silently discarded by sendOne pre-#0254-review-fix),
// the row stays 'queued', and the next mailing pass claims and sends it a
// SECOND time — a duplicate confirmation, registration magic link, or
// recovery link.
//
// This seeds a confirmation row, puts it into 'sending' with a claimed_at
// 25 seconds old (past intake's 20s window, comfortably inside mailing's
// 70s one — the exact reachable gap the review proved, not an edge case),
// and calls h.intakePass() directly — a deliberate, documented exception to
// this file's usual "poll for the background worker's outcome" discipline
// (see TestSubscribeIntakeWorker_RecoversRowTheFastPathNeverProcessed's own
// doc comment for why that discipline exists for ClaimDue races). It does
// not apply here: OrphanSweep's UPDATE is idempotent and scoped by kind, so
// a concurrent background pass performing the identical, correctly-scoped
// sweep cannot change this test's outcome — there is no row for it to
// race over, only a property ("this row is never touched") to falsify.
//
// A raw SQL UPDATE puts the row into 'sending' directly, rather than
// h.intake.ClaimDue, so this test claims only the ONE row it created and
// cannot interact with any other test's rows in the shared per-agent
// database (this package's tests run sequentially within the package, but
// leftover queued confirmation rows from earlier tests are common and must
// not be disturbed).
func TestSubscribeIntakeWorker_OrphanSweepDoesNotTouchOtherKinds(t *testing.T) {
	pool := subscribeTestPool(t)
	h, _ := subscribeMux(t, pool, nil)
	ctx := context.Background()

	recipient := subscribeUniqueEmail(t)
	id, err := h.intake.Enqueue(ctx, outbox.Item{
		Kind:      outbox.KindConfirmation,
		Recipient: recipient,
	})
	if err != nil {
		t.Fatalf("seeding a confirmation row: %v", err)
	}

	// Simulate internal/mailing.OutboxWorker having claimed this row 25s
	// ago and still being mid-send — a live claim well inside its own 70s
	// staleness window.
	if _, err := pool.Exec(ctx,
		`UPDATE outbound_queue SET status = $2, claimed_at = now() - interval '25 seconds' WHERE id = $1`,
		id, outbox.StatusSending,
	); err != nil {
		t.Fatalf("backdating claimed_at to simulate a live mailing claim: %v", err)
	}

	if _, err := h.intakePass(); err != nil {
		t.Fatalf("intakePass: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("reading status back: %v", err)
	}
	if status != outbox.StatusSending {
		t.Fatalf("status = %q, want %q — the intake poller's OrphanSweep reclaimed a live mail claim it has no business touching (kind=confirmation, not KindSubscribeIntake); this is #0254's review-bounce regression: OrphanSweep must filter by kind exactly as ClaimDue already does, or one poller's staleness window can release the other's in-flight send and cause a duplicate email", status, outbox.StatusSending)
	}
}
