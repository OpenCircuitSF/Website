package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"sync"
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

// intakeQueueDiagnostic renders, for a failure message, what outbound_queue
// actually holds for ids right now plus how many KindSubscribeIntake rows are
// queued and due across the whole table, and how many intake pollers are
// alive to compete for them.
//
// It exists because the two ways intakePass can decline to process a row are
// both SILENT: SelectDue returning no ids at all, and ClaimRow reporting
// claimed=false because the row was no longer queued. Neither logs anything,
// so a test that waits on the poller and times out had, before this, no
// evidence at all about which of the two happened, what state the rows were
// left in, or whether some other handler's poller got there first. Four
// separate review passes each paid that diagnostic cost and reached the same
// dead end, which is what #0470 was filed against; the poller census is the
// line that names the real cause when it recurs.
//
// Best-effort by construction: it runs on a failure path, so a query error is
// folded into the returned text rather than failing the test a second time
// and replacing the real diagnosis with a secondary one.
func intakeQueueDiagnostic(pool *pgxpool.Pool, ids ...int64) string {
	ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
	defer cancel()

	var b strings.Builder
	for _, id := range ids {
		var status string
		var attempts int
		var claimedAt *time.Time
		var dueIn float64
		err := pool.QueryRow(ctx,
			`SELECT status, attempts, claimed_at, extract(epoch from (next_attempt_at - now()))
			   FROM outbound_queue WHERE id = $1`, id,
		).Scan(&status, &attempts, &claimedAt, &dueIn)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			fmt.Fprintf(&b, "\n  id=%d: ROW IS GONE (deleted or truncated out from under the poller)", id)
		case err != nil:
			fmt.Fprintf(&b, "\n  id=%d: could not be read back: %v", id, err)
		default:
			fmt.Fprintf(&b, "\n  id=%d: status=%q attempts=%d claimed_at=%v due_in=%.2fs",
				id, status, attempts, claimedAt, dueIn)
		}
	}

	var queuedDue int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM outbound_queue
		  WHERE kind = $1 AND status = $2 AND next_attempt_at <= now()`,
		string(outbox.KindSubscribeIntake), outbox.StatusQueued,
	).Scan(&queuedDue); err != nil {
		fmt.Fprintf(&b, "\n  queued-and-due %s rows: could not be counted: %v", outbox.KindSubscribeIntake, err)
	} else {
		fmt.Fprintf(&b, "\n  queued-and-due %s rows table-wide: %d (SelectDue would return this many)",
			outbox.KindSubscribeIntake, queuedDue)
	}
	pollers, parked := goroutinesIn(frameName((*SubscribeHandler).runIntakeWorker))
	fmt.Fprintf(&b, "\n  live intake pollers (goroutines in runIntakeWorker): %d", pollers)
	for _, g := range parked {
		fmt.Fprintf(&b, "\n    parked: %s", g)
	}
	fmt.Fprintf(&b, "\n  goroutines inside processIntakeRow: %d", countGoroutinesIn(frameName((*SubscribeHandler).processIntakeRow)))
	fmt.Fprintf(&b, "\n  goroutines inside blockingSuppressionChecker.IsSuppressed: %d",
		countGoroutinesIn(frameName((*blockingSuppressionChecker).IsSuppressed)))
	return b.String()
}

// countGoroutinesIn is goroutinesIn without the parked-at sample, for the
// call sites that only need the number.
func countGoroutinesIn(frame string) int {
	n, _ := goroutinesIn(frame)
	return n
}

// frameName returns the exact fully-qualified stack-frame name of fn,
// as the runtime prints it in a goroutine dump. Deriving it from the
// function value instead of writing the string by hand is what keeps
// goroutinesIn's callers honest: goroutinesIn reports zero both when
// nothing is in the frame and when the name it was handed matches
// nothing at all, and TestNoLeakedIntakePollers PASSES on zero — so a
// hand-written literal that goes stale disarms the guard silently,
// which is CLAUDE.md §8's "an empty result must be an error, never an
// input". Measured during #0470's review: with 32 leaked pollers
// provably alive, renaming runIntakeWorker made the guard pass in
// 0.00s and the package go green. Written this way the same rename is
// a compile error instead.
func frameName(fn any) string {
	return runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
}

// goroutinesIn returns how many live goroutines are executing frame right
// now, plus the header line of the first few, so a failure can show where
// they are parked rather than only how many there are. frame must be the
// exact fully-qualified stack-frame name a goroutine dump prints — pass the
// result of frameName(...) rather than writing it by hand, so a rename of
// the watched method is a compile error instead of silently disarming the
// match. A goroutine dump prints one such line per goroutine that is inside
// the frame, so counting the frame is exact where splitting the dump into
// per-goroutine blocks is not -- the block separator is not reliably
// distinct from blank lines the runtime emits inside a block, which
// over-counts badly on a dump with many goroutines.
func goroutinesIn(frame string) (int, []string) {
	buf := make([]byte, 4<<20)
	dump := string(buf[:runtime.Stack(buf, true)])
	n := strings.Count(dump, frame+"(")
	var sample []string
	for _, g := range strings.Split(dump, "\ngoroutine ") {
		if !strings.Contains(g, frame+"(") {
			continue
		}
		lines := strings.SplitN(g, "\n", 3)
		if len(sample) < 3 {
			sample = append(sample, strings.Join(lines[:min(2, len(lines))], " | "))
		}
	}
	return n, sample
}

// TestNoLeakedIntakePollers is #0470's regression pin, and it must stay the
// FIRST test in this file: it asserts that no SubscribeHandler built by any
// earlier test in this package is still polling outbound_queue.
//
// Since #0254, NewSubscribeHandler with a non-nil intake store starts
// runIntakeWorker, which claims KindSubscribeIntake rows every
// intakePollInterval until Close stops it. A handler built without a
// matching Close therefore does not merely leak a goroutine — it leaks a
// competitor for the rows every other test in this package seeds, for the
// remainder of the test binary. #0470 measured 33 such pollers alive by the
// time this file ran, all from newTestSubscribeHandler
// (admin_subscribers_test.go), which took no *testing.T and so could not
// register a cleanup. See that helper's doc comment for the full chain and
// for the failure it produced.
//
// The oracle is the live goroutine dump rather than a counter this package
// maintains, so it cannot be satisfied by a bookkeeping mistake: a handler
// whose poller is genuinely still running shows up here whether or not
// anything recorded that it was created.
//
// A short settle window, not a deadline: Close returns once runIntakeWorker
// has closed its done channel, but the goroutine itself needs a moment more
// to leave the frame, and a preceding test's cleanup may still be in
// flight. This waits for the count to reach zero rather than sampling once,
// so an in-flight shutdown is not misreported as a leak. It is NOT sized
// against machine load (CLAUDE.md §5) — a genuine leak never reaches zero,
// however long the window, and a clean shutdown reaches it in microseconds.
func TestNoLeakedIntakePollers(t *testing.T) {
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; no DB-backed test has built a SubscribeHandler")
	}
	frame := frameName((*SubscribeHandler).runIntakeWorker)
	deadline := time.Now().Add(time.Second)
	for {
		n, parked := goroutinesIn(frame)
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d intake poller goroutine(s) from earlier tests are still running; every SubscribeHandler must be Closed by the test that built it, or its poller keeps claiming outbound_queue rows out from under every later test (#0470). Parked at:\n  %s",
				n, strings.Join(parked, "\n  "))
		}
		// 25ms, not a tighter spin: each probe takes a full goroutine
		// dump, so polling faster only burns allocation on a run that is
		// already failing.
		time.Sleep(25 * time.Millisecond)
	}
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
// losing that race (SelectDue's SKIP LOCKED and ClaimRow's atomic claim
// simply hand the row to whichever caller reaches ClaimRow first — #0297)
// and reporting a false negative. Polling for the
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
// intakePass. At the time of that bug, intakePass's staleness window
// (intakeOrphanStaleAfter) was 20s, much shorter than
// internal/mailing.OutboxWorker's own sweep window for the SAME method
// (outboxOrphanStaleAfter, 70s then). #0294 raised intakeOrphanStaleAfter
// to (intakeBatchSize+1)*intakeRowTimeout = 210s and #0284 raised
// outboxOrphanStaleAfter to 800s, for unrelated reasons (both windows
// were derived from one row's bound rather than a batch's); #0297 has
// since collapsed both back down to a single-row bound —
// intakeOrphanStaleAfter is 30s and outboxOrphanStaleAfter is 70s today —
// see intakeOrphanStaleAfter's own doc comment (subscribe_intake.go).
// The numbers below are historical, describing the reachable gap as it
// existed at the time of the #0254 bug; the mechanism this test guards —
// OrphanSweep must filter by kind, because ClaimDue already does — does
// not depend on either window's current value, only on kind filtering,
// which is what this test actually exercises.
//
// Before the fix, intakePass's unfiltered OrphanSweep(20s) reclaimed ANY
// row stuck 'sending' past 20s — including a confirmation row internal/
// mailing's worker was still legitimately holding mid-send at 25s, well
// inside ITS OWN then-70s window. Releasing that live claim is not
// cosmetic: the in-flight send's eventual MarkSent requires
// status='sending', so it affects zero rows (silently discarded by
// sendOne pre-#0254-review-fix), the row stays 'queued', and the next
// mailing pass claims and sends it a SECOND time — a duplicate
// confirmation, registration magic link, or recovery link.
//
// This seeds a confirmation row and puts it into 'sending' with a
// claimed_at 25 seconds old — the same backdate the original review used,
// kept for continuity with the bug's own reproduction rather than any
// current relationship to either window's value (25s is now comfortably
// inside BOTH windows; the fix under test is the kind filter, not the
// staleness comparison) — and calls h.intakePass() directly, a deliberate,
// documented exception to this file's usual "poll for the background
// worker's outcome" discipline
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

// blockingSuppressionChecker wraps a SuppressionChecker and blocks the
// FIRST call to IsSuppressed until release is closed, then delegates every
// call (including that first one, once unblocked) to inner. It exists only
// to make TestSubscribeIntakeWorker_ClaimsRowsIndividuallyNotAsBatch
// deterministic — the exact role blockingMailer
// (internal/mailing/outbox_worker_test.go) plays for
// TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued, adapted to this
// package's own natural blocking seam: processIntakeRow calls
// h.suppression.IsSuppressed fresh, on the real per-row context, before
// anything else — so blocking it holds ClaimRow's claim open on exactly
// one row without needing a fake Mailer at all.
//
// once, not a counter or channel-close on every call, so only the row
// racing to be processed FIRST blocks; every row after it (including the
// second row this test seeds) proceeds at full speed once release is
// closed, matching how a real suppression lookup never blocks in
// production.
type blockingSuppressionChecker struct {
	inner   SuppressionChecker
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingSuppressionChecker) IsSuppressed(ctx context.Context, email string) (bool, error) {
	b.once.Do(func() {
		close(b.started)
		<-b.release
	})
	return b.inner.IsSuppressed(ctx, email)
}

// TestSubscribeIntakeWorker_ClaimsRowsIndividuallyNotAsBatch is #0297's
// review-bounce defect 1 fix (issues/0297.md ## Review notes): the
// decisive gap the review found was that nothing in this package pinned
// intakePass's per-row claim the way
// TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued
// (internal/mailing/outbox_worker_test.go) pins OutboxWorker.pass's. The
// review proved this by reverting intakePass to a whole-batch ClaimDue,
// in a throwaway worktree, while leaving intakeOrphanStaleAfter at its new,
// much smaller per-row-derived window — reinstating #0254's exact failure
// mode with a window 10.5x under the honest batch-derived bound — and the
// ENTIRE suite (handlers, outbox, mailing, db, cmd) still passed green.
//
// This test closes that gap the same way the outbox worker's own test
// does: seed two due rows, block the poller mid-processing of the first
// via a fake dependency (blockingSuppressionChecker, above, standing in
// for blockingMailer — this package's processIntakeRow has no Mailer to
// block, but IsSuppressed is called just as early and just as
// unconditionally), and assert on the PAIR while blocked. Under the
// real per-row claim model (SelectDue then ClaimRow per id,
// intakePass, subscribe_intake.go), exactly one row is 'sending' — the
// one blocked inside processIntakeRow — and the other is still plain
// 'queued', claimed_at IS NULL, attempts = 0: SelectDue selected both
// ids up front but claimed neither, and ClaimRow has not yet reached the
// second one. A whole-batch ClaimDue CANNOT satisfy that assertion: its
// single UPDATE stamps status='sending', claimed_at=now() on every row in
// the batch atomically, before either row's own processing begins, so
// both rows would read 'sending' with a non-NULL claimed_at the instant
// the batch is claimed — which is exactly why this test fails immediately
// against the mutation the review used, restoring the exact regression
// pin criterion 5 asked for.
//
// Mutation proof performed for this fix, in a throwaway worktree
// (`git worktree add`, own scratch database), git-diff confirmed against
// the main tree afterward with no changes left behind: reverting
// intakePass's claim loop to one whole-batch outbox.Store.ClaimDue call
// up front (instead of SelectDue then per-id ClaimRow) made this test FAIL
// with "status while blocked = (id1=\"sending\", id2=\"sending\"), want
// exactly one \"sending\" ... and one \"queued\"" — both rows already
// 'sending' the moment the batch-wide claim committed, before
// processIntakeRow for either had even started. Restored the mutated
// files from the worktree's own git history (not copied back into the
// main tree) and confirmed `git status` in the main tree stayed clean
// throughout — the main tree's intakePass was never touched by the proof.
func TestSubscribeIntakeWorker_ClaimsRowsIndividuallyNotAsBatch(t *testing.T) {
	pool := subscribeTestPool(t)
	checker := &blockingSuppressionChecker{
		inner:   NoSuppressions{},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	h, _ := subscribeMux(t, pool, checker)
	ctx := context.Background()

	email1 := subscribeUniqueEmail(t)
	email2 := subscribeUniqueEmail(t)

	id1, err := h.intake.Enqueue(ctx, outbox.Item{
		Kind:      outbox.KindSubscribeIntake,
		Recipient: email1,
		Payload: subscribeIntakePayload{
			ConfirmTTLSeconds: int64(subscribeConfirmTTL.Seconds()),
		},
	})
	if err != nil {
		t.Fatalf("seeding intake row 1: %v", err)
	}
	id2, err := h.intake.Enqueue(ctx, outbox.Item{
		Kind:      outbox.KindSubscribeIntake,
		Recipient: email2,
		Payload: subscribeIntakePayload{
			ConfirmTTLSeconds: int64(subscribeConfirmTTL.Seconds()),
		},
	})
	if err != nil {
		t.Fatalf("seeding intake row 2: %v", err)
	}
	// Both rows must end 'sent' (or be force-cleaned) before this test
	// returns, or a leftover 'queued' KindSubscribeIntake row would be
	// claimable by any later test in this package/run — the same
	// discipline TestSubscribeIntakeWorker_OrphanSweepDoesNotTouchOtherKinds
	// and TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued both document.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE id = ANY($1)`, []int64{id1, id2})
	})

	// Wait for the real background poller (started by NewSubscribeHandler
	// inside subscribeMux) to reach the first row's IsSuppressed call and
	// block there — bounded past a full intakePollInterval cycle, since
	// the poller's very first pass (before these rows existed) found
	// nothing and is asleep until its next tick.
	select {
	case <-checker.started:
	case <-time.After(intakePollInterval*2 + 5*time.Second):
		t.Fatalf("blockingSuppressionChecker.IsSuppressed was never called; the intake poller never reached either seeded row.%s",
			intakeQueueDiagnostic(pool, id1, id2))
	}

	// Exactly one of the two rows is claimed now — the one blocked inside
	// processIntakeRow — and the other is still plain 'queued', never
	// having reached ClaimRow: SelectDue selected both ids up front but
	// claimed neither, and ClaimRow only claims one row at a time,
	// immediately before THAT row's own reprocessing. Assert on the PAIR,
	// not on a specific id, since SelectDue's ORDER BY next_attempt_at, id
	// combined with intakePass's own loop order determines which id is
	// claimed first, not the order these rows were enqueued.
	var status1, status2 string
	var claimedAt1, claimedAt2 *time.Time
	var attempts1, attempts2 int
	if err := pool.QueryRow(ctx, `SELECT status, claimed_at, attempts FROM outbound_queue WHERE id = $1`, id1).Scan(&status1, &claimedAt1, &attempts1); err != nil {
		t.Fatalf("select id1 while blocked: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status, claimed_at, attempts FROM outbound_queue WHERE id = $1`, id2).Scan(&status2, &claimedAt2, &attempts2); err != nil {
		t.Fatalf("select id2 while blocked: %v", err)
	}

	sendingCount, queuedCount := 0, 0
	for i, s := range []string{status1, status2} {
		switch s {
		case outbox.StatusSending:
			sendingCount++
			claimedAt := []*time.Time{claimedAt1, claimedAt2}[i]
			attempts := []int{attempts1, attempts2}[i]
			if claimedAt == nil {
				t.Errorf("row %d status=%q but claimed_at is NULL — ClaimRow's own UPDATE always stamps it", i+1, s)
			}
			if attempts != 1 {
				t.Errorf("row %d status=%q but attempts=%d, want 1 (ClaimRow's single per-row UPDATE)", i+1, s, attempts)
			}
		case outbox.StatusQueued:
			queuedCount++
			claimedAt := []*time.Time{claimedAt1, claimedAt2}[i]
			attempts := []int{attempts1, attempts2}[i]
			if claimedAt != nil {
				t.Errorf("row %d status=%q but claimed_at is non-NULL — SelectDue must claim nothing", i+1, s)
			}
			if attempts != 0 {
				t.Errorf("row %d status=%q but attempts=%d, want 0 — SelectDue must not touch attempts", i+1, s, attempts)
			}
		}
	}
	if sendingCount != 1 || queuedCount != 1 {
		t.Fatalf("status while blocked = (id1=%q, id2=%q), want exactly one %q (blocked in processIntakeRow) and one %q (not yet reached by ClaimRow) — a whole-batch ClaimDue would show both rows %q here, which is the exact regression #0297's review found untested",
			status1, status2, outbox.StatusSending, outbox.StatusQueued, outbox.StatusSending)
	}

	close(checker.release)

	// Both rows must reach 'sent' once unblocked — bounded poll, no
	// arbitrary sleep. Once the first row finishes, intakePass's caller
	// (runIntakeWorker) sees processed=true and loops immediately without
	// sleeping, so the second row is claimed and finished promptly too.
	deadline := time.Now().Add(5 * time.Second)
	for {
		s1, ok1 := intakeRowStatus(t, pool, email1)
		s2, ok2 := intakeRowStatus(t, pool, email2)
		if ok1 && ok2 && s1 == outbox.StatusSent && s2 == outbox.StatusSent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rows never both reached %q after unblocking: id1=%q(ok=%v) id2=%q(ok=%v)", outbox.StatusSent, s1, ok1, s2, ok2)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
