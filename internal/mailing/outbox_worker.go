// outbox_worker.go is #0126's second send worker: it drains
// internal/outbox's outbound_queue (transactional mail — confirmation,
// already-subscribed, welcome, goodbye, admin alerts, registration,
// recovery, sessions-revoked, import invites) exactly as worker.go's
// *Worker drains email_sends (campaign mail), but as a SEPARATE type with
// its own claim/backoff/orphan-sweep, not an extension of *Worker.
//
// # A new worker, not an extension of *Worker — why
//
// *Worker's claim unit is a campaign, and it drains one campaign to
// completion before returning to its ticker (worker.go's Run doc comment:
// "there is no concurrency across campaigns within one Worker"). Folding
// transactional mail into it would put a confirmation email behind a
// five-thousand-recipient campaign drain — the exact failure #0126 exists
// to close, reintroduced with a different cause. OutboxWorker reuses the
// SHAPE — atomic claim, staleness-gated orphan sweep (#0122), detached
// contexts bounded by sendMessageTimeout/writeStatusTimeout, signal-based
// Stop — and none of the code path.
//
// # orphanStaleAfter is recomputed here, not shared with worker.go's
//
// outboxOrphanStaleAfter is its own var, not shared with worker.go's
// orphanStaleAfter — a future change to one worker's timeout budget must
// not silently retune the other's staleness window, even though (#0297) the
// two now evaluate to the SAME expression.
//
// # History: batch-wide claim (#0284), then per-row claim (#0297)
//
// Through #0284/#0295, the two workers' derivations genuinely differed:
// this worker claimed a whole batch atomically in one UPDATE (ClaimDue)
// and then drained it serially, so the LAST row's claim age included every
// predecessor's real processing time — #0284's batch-derived window
// (ultimately 800s). worker.go never had that shape at all: its
// SendStore.ClaimBatch only selects, and SendStore.ClaimRow claims one
// recipient at a time, individually, right as that recipient's own send
// begins (#0295) — which is what let worker.go's orphanStaleAfter stay a
// simple single-row bound (70s) the whole time.
//
// #0297 closed that gap structurally rather than tuning outboxOrphanStaleAfter
// further: this worker's pass now uses outbox.Store's own SelectDue/ClaimRow
// pair — select the batch's ids WITHOUT claiming anything, then claim each
// row individually, immediately before that row's own send begins — the
// exact shape worker.go's ClaimBatch/ClaimRow already had. A row waiting its
// turn in pass's loop is therefore still plain 'queued', never 'sending',
// and is never at risk from OrphanSweep at all; only the row currently being
// sent is ever claimed. Batch size has dropped out of this bound entirely,
// the same way it always was out of worker.go's — see
// outboxOrphanStaleAfter's own doc comment, below, for the resulting
// arithmetic.
//
// # Rate limiting is this worker's own, not shared with *Worker's
//
// OutboxWorker carries its own rate.Limiter at the existing max_send_rate
// setting. Two workers can therefore momentarily exceed that combined rate
// while a campaign is draining at the same time transactional mail is
// flowing. Accepted deliberately: transactional volume here is a handful of
// messages a minute, SES's account-level rate is the real ceiling, and
// CLAUDE.md §5 is explicit that this project has no performance
// requirement to protect against that overlap.
package mailing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

const (
	outboxDefaultPollInterval = 2 * time.Second
	outboxDefaultBatchSize    = 20

	// settingQueueMaxRetries is migrations/000021's seeded settings row.
	// #0126's plan §9 item 3: not "queue.max_retries" (dotted keys don't
	// exist in this project's settings table).
	settingQueueMaxRetries = "queue_max_retries"
)

// outboxOrphanStaleAfter bounds how long a claimed-but-unfinished
// outbound_queue row is treated as still legitimately in flight before
// OrphanSweep (#0122) releases it back to 'queued'. Releasing a row that
// is still genuinely being sent is the #0254 failure mode: the worker
// finishes the send it still holds, then a later pass reclaims the same
// row and sends it again.
//
// # #0297 — collapsed to a per-row bound
//
// Through #0284, this bound was derived from a full outboxDefaultBatchSize
// batch (ultimately 800s: 20 * (sendMessageTimeout + 2*writeStatusTimeout)),
// because ClaimDue stamped ONE claimed_at for the whole batch and pass then
// drained it serially — so the LAST row's claim age included every
// predecessor's real processing time, not just its own.
//
// #0297 changed pass, below, to claim per row instead: SelectDue picks the
// batch's ids without claiming anything, and ClaimRow claims exactly one
// row, individually, immediately before THAT row's own send begins (the
// same shape worker.go's SendStore.ClaimBatch/ClaimRow always had — see
// #0295). A row waiting its turn in pass's loop is still plain 'queued'
// until ClaimRow reaches it, so it carries no claim age to inherit from its
// predecessors, and batch size drops out of this bound entirely — there is
// only ever, at most, ONE row 'sending' under this worker at a time.
//
// The single-row bound: a live claim can be held for at most
// sendMessageTimeout (render + the detached, bounded sendCtx SES call) plus
// writeStatusTimeout (MarkSent's own detached, bounded writeCtx) = 35s,
// before MarkSent's UPDATE moves the row OUT of 'sending'. RecordEvent
// (sendOne, below) runs AFTER that transition has already committed, so it
// cannot make this row look more stale than it is, and — because there is
// no batch-wide claim any more — it cannot delay a "next row" either. On
// top of that, ClaimRow's own round trip is now itself bounded (pass wraps
// it in a bounded context.WithTimeout(ctx, writeStatusTimeout) — deliberately
// NOT detached from ctx the way sendOne's writes are, since a claim that
// cannot complete before shutdown should not be taken at all; see pass,
// below): if THAT round trip alone hangs, up to writeStatusTimeout elapses
// after claimed_at is committed server-side before this process even learns
// the claim succeeded, which is real elapsed claim age too. Worst case:
//
//	sendMessageTimeout + 2*writeStatusTimeout = 30s + 2*5s = 40s.
//
// outboxOrphanStaleAfter doubles the legitimate single-row hold
// (sendMessageTimeout + writeStatusTimeout = 35s), not the 40s figure that
// also charges the ClaimRow round-trip residual, giving 70s — the same
// expression and value as worker.go's own orphanStaleAfter. 70s still
// covers the fuller 40s worst case with 30s of real margin, comfortably
// more than the 35s margin worker.go's own window carries over its 35s
// figure, so doubling the simpler term rather than the fuller one is not a
// shortfall — it happens to land with slightly MORE headroom, not less.
//
// # Still not a STRICT bound
//
// pass's own SelectDue call, and outboxEffectiveSendRate's settings read,
// still run on OutboxWorker.Run's ambient ctx
// (`go outboxWorker.Run(context.Background())`, cmd/opencircuit/main.go) —
// unbounded. Neither holds any row's claim open (SelectDue claims nothing;
// outboxEffectiveSendRate runs before a row is claimed), so a hang there
// delays the NEXT claim rather than extending an existing one — it cannot
// make outboxOrphanStaleAfter false the way the old batch-wide gap could.
// What remains unclosed, the same CLASS of residual #0294's final comment
// names: this assumes context cancellation actually interrupts a stuck
// DB/network call within its own deadline — a TCP read the OS cannot be
// made to abandon on cancellation is not something any Go-level constant
// formula can close.
//
// Expressed from the real package constants, not a hand-computed literal,
// so raising either timeout moves this window automatically — batch size
// (outboxDefaultBatchSize) is deliberately NOT a term any more; see
// TestOutboxOrphanStaleAfterCoversSingleRowHold
// (outbox_orphan_stale_after_test.go) for the invariant this maintains.
//
// A var, not a const, so a test can shrink it; NOT sized against measured
// machine load (CLAUDE.md §5).
var outboxOrphanStaleAfter = 2 * (sendMessageTimeout + writeStatusTimeout)

// mailKinds is every outbox.Kind this worker's render switch knows how to
// build a message for — i.e. every Kind EXCEPT outbox.KindSubscribeIntake
// (#0254), which is not an email and is claimed separately by
// internal/handlers.SubscribeHandler's own recovery poller. Passed to
// outbox.Store.SelectDue's and OrphanSweep's kinds filters in pass, below
// — #0254 scoped ClaimDue, which this worker has not called since #0297;
// ClaimRow takes no kinds and only ever claims an id SelectDue already
// filtered — so this worker never selects OR sweeps a row it cannot
// render.
//
// A hand-maintained list, deliberately, and #0254's review bounce corrected
// an earlier version of this comment that overstated what that buys: it
// claimed a Kind added here without updating render's switch would "fail
// loudly in render's default case" — true only of a Kind mistakenly ADDED
// to this list, whose render call reaches the default case and abandons
// after retries. It says nothing about the opposite mistake — a Kind added
// to internal/outbox and forgotten HERE — and that omission is silent, not
// loud: SelectDue, scoped to mailKinds, never selects such a row at all
// (#0297; before that it was ClaimDue's own kinds filter), so render's
// default case never runs; the row simply stalls in 'queued' forever, with
// no error anywhere to notice. TestMailKindsCoversEveryOutboxKind
// (outbox_worker_kinds_guard_test.go) closes that gap by parsing
// internal/outbox's Kind constants and failing on any constant that is
// neither in this list nor outbox.KindSubscribeIntake.
var mailKinds = []outbox.Kind{
	outbox.KindConfirmation,
	outbox.KindAlreadySubscribed,
	outbox.KindWelcome,
	outbox.KindGoodbye,
	outbox.KindAdminAlert,
	outbox.KindRegistration,
	outbox.KindRecovery,
	outbox.KindSessionsRevoked,
	outbox.KindImportInvite,
}

// gatedKinds is #0365's send-time re-check whitelist: every outbox.Kind
// sendGate re-checks against live subscriber state immediately before
// sending, mapped to the ONE subscribers.status a message of that kind is
// valid to send under. Positive (whitelist) rather than negative
// (!= complained), matching RecheckEligibleTx's own
// status == subscribers.StatusActive (audience.go) and #0341's claim
// predicate AND status = 'pending' — #0341's own plan defends that shape
// in so many words as "strictly stronger than <> 'complained'". A
// whitelist also blocks 'unsubscribed'/'bounced' addresses and catches the
// invite-decline path (internal/subscribers.Store's pending -> unsubscribed
// transition) for free, with no extra code.
//
// welcome and import_invite carry the widest window in the system: render
// (below) defers either one indefinitely via deferMissingPhysicalAddress
// when settings.physical_address is blank, so a row of either kind can sit
// 'queued' for a long time with nothing else re-checking the subscriber in
// the meantime — see #0365's plan §2.
//
// TestGatedKindsPartitionEveryMailKind asserts every mailKinds element
// appears in exactly one of this map or ungatedKinds, below.
var gatedKinds = map[outbox.Kind]string{
	outbox.KindConfirmation:      subscribers.StatusPending,
	outbox.KindAlreadySubscribed: subscribers.StatusActive,
	outbox.KindWelcome:           subscribers.StatusActive,
	outbox.KindImportInvite:      subscribers.StatusPending,
}

// ungatedKinds is every mailKinds member sendGate deliberately does NOT
// re-check, each carrying a written reason — the kindExceptions shape
// (outbox_worker_kinds_guard_test.go, #0296 item 4: an allowlist anyone
// can append to without an argument is a guard with a hole).
// TestGatedKindsPartitionEveryMailKind asserts every value here is
// non-empty, and that gatedKinds+ungatedKinds together partition mailKinds
// exactly (no overlap, no gap).
var ungatedKinds = map[outbox.Kind]string{
	outbox.KindRegistration:    "staff auth mail to a users row, not a subscribers row — Row.SubscriberID is always nil; gating it would let a marketing suppression lock an admin out of the console",
	outbox.KindRecovery:        "same as registration — a passkey recovery link must reach an admin regardless of any list state",
	outbox.KindSessionsRevoked: "same as registration — a security notice, not list mail",
	outbox.KindAdminAlert:      "operational alert to staff, not to a subscriber",
	outbox.KindGoodbye:         "no producer yet, and a status whitelist would be WRONG for it when one lands: goodbye is sent precisely BECAUSE the address stopped being active. Whoever adds the producer must decide its own predicate rather than inherit one of the four above",
}

// OutboxWorker drains internal/outbox's outbound_queue. Construct with
// NewOutboxWorker; call Run in its own goroutine, Stop to shut down.
type OutboxWorker struct {
	store    *outbox.Store
	events   *subscribers.Store // RecordEvent for confirmation_sent/welcome_sent — nil-tolerant, matching every other handler
	mailer   Mailer
	settings SettingsReader

	baseURL string
	// listDomain is config.Config.EmailListDomain, threaded through to
	// BuildWelcomeEmail (#0127) for the RFC 8058 List-Id/mailto: form —
	// see CampaignHeaders' own doc comment for why a blank value degrades
	// to the HTTPS-only header form rather than emitting an invalid
	// mailto: URI.
	listDomain     string
	envMaxSendRate int
	batchSize      int

	pollInterval time.Duration
	sleep        func(context.Context, time.Duration) error
	log          *slog.Logger

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once

	// claimedMu guards claimed, below. Written only by the goroutine
	// running Run (via trackClaimed/untrackClaimed); read only by Stop
	// (via releaseAll), and only after <-w.doneCh confirms Run has
	// returned — see Stop's and releaseAll's doc comments for why that
	// ordering means Run's own writes can never race a call to releaseAll.
	//
	// #0266: that is not the whole story, and calling this mutex
	// "belt-and-braces" (as an earlier version of this comment did)
	// understated it. Stop's doc comment promises it is safe to call more
	// than once, and Stop enforces no first-caller-wins ordering of its
	// own — stopOnce only makes closing stopCh idempotent, not the rest of
	// the method. Two goroutines calling Stop concurrently both block on
	// the same <-w.doneCh and, once it closes, BOTH proceed to call
	// releaseAll at the same time. Without this mutex, both goroutines
	// would range over and nil out the same w.claimed map concurrently —
	// a genuine data race (Go's race detector flags it; see this issue's
	// mutation proof), not merely a double `store.Release` call. This
	// mutex is exactly what makes concurrent Stop calls safe, i.e. what
	// makes Stop's repeat-call promise true — the same class of
	// undersold guard as #0263's FOR UPDATE on AdminResendConfirmation's
	// cooldown check.
	claimedMu sync.Mutex
	// claimed holds the outbound_queue id(s) this worker currently holds
	// claimed ('sending') but has not yet finished sending
	// (MarkSent/MarkRetryOrAbandon). A row is added the instant ClaimRow
	// returns it and removed the instant sendOne finishes with it — under
	// #0297's per-row claim model, at most one id is ever present at a
	// time. See trackClaimed's and Stop's own doc comments for why
	// anything still present by the time Stop reads this set (releaseAll)
	// is a defensive net rather than the ordinary case.
	claimed map[int64]struct{}
}

// OutboxWorkerDeps is NewOutboxWorker's construction argument.
type OutboxWorkerDeps struct {
	Store    *outbox.Store
	Events   *subscribers.Store // nil disables subscriber_events writes (confirmation_sent/welcome_sent)
	Mailer   Mailer
	Settings SettingsReader

	BaseURL string
	// ListDomain is config.Config.EmailListDomain (#0127) — see
	// OutboxWorker.listDomain's doc comment. Empty is tolerated the same
	// way CampaignHeaders tolerates it.
	ListDomain string
	// EnvMaxSendRate is the same MAX_SEND_RATE ceiling worker.go's Worker
	// reads — see effectiveSendRate/outboxEffectiveSendRate.
	EnvMaxSendRate int
	BatchSize      int
	PollInterval   time.Duration
	Log            *slog.Logger
}

// NewOutboxWorker constructs an OutboxWorker. It does not start it — call
// Run. See worker.go's NewWorker doc comment for why this runs as an
// in-process goroutine rather than a separate binary; the same reasoning
// applies unchanged.
func NewOutboxWorker(deps OutboxWorkerDeps) (*OutboxWorker, error) {
	if deps.Store == nil {
		return nil, fmt.Errorf("mailing: outbox worker requires an outbox.Store")
	}
	if deps.Mailer == nil {
		return nil, fmt.Errorf("mailing: outbox worker requires a Mailer")
	}
	if deps.Settings == nil {
		return nil, fmt.Errorf("mailing: outbox worker requires a SettingsReader")
	}

	pollInterval := deps.PollInterval
	if pollInterval <= 0 {
		pollInterval = outboxDefaultPollInterval
	}
	batchSize := deps.BatchSize
	if batchSize <= 0 {
		batchSize = outboxDefaultBatchSize
	}
	envMaxSendRate := deps.EnvMaxSendRate
	if envMaxSendRate <= 0 {
		envMaxSendRate = defaultMaxSendRate
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}

	return &OutboxWorker{
		store:          deps.Store,
		events:         deps.Events,
		mailer:         deps.Mailer,
		settings:       deps.Settings,
		baseURL:        deps.BaseURL,
		listDomain:     deps.ListDomain,
		envMaxSendRate: envMaxSendRate,
		batchSize:      batchSize,
		pollInterval:   pollInterval,
		sleep:          sleepWithContext,
		log:            log,
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
	}, nil
}

// Run blocks, polling every pollInterval, until Stop is called or ctx is
// done. Each pass sweeps orphans, claims a batch, sends it, then waits for
// the next tick.
func (w *OutboxWorker) Run(ctx context.Context) {
	defer close(w.doneCh)
	for {
		select {
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		processed, err := w.pass(ctx)
		if err != nil {
			w.log.Error("mailing: outbox worker pass failed", "err", err)
		}
		if processed {
			continue
		}

		select {
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		case <-time.After(w.pollInterval):
		}
	}
}

// Stop signals Run to stop claiming new work and blocks until Run has
// returned or ctx's deadline elapses. Safe to call more than once —
// including concurrently: see claimedMu's doc comment for why that
// specific promise depends on the mutex, not merely on the doneCh
// ordering below.
//
// #0297's review: under the per-row claim model (SelectDue then ClaimRow
// per id, pass, below), releaseAll — called only on the <-w.doneCh
// branch, after Run has fully returned — provably has nothing to release
// on every path that reaches it. pass's stopCh check runs before each
// ClaimRow, so a row not yet reached is still plain 'queued', never
// claimed; and the one row genuinely mid-send always runs to completion
// before pass (and therefore Run) can return, because sendOne has no
// internal stopCh check. So Stop no longer actually releases anything in
// ordinary operation — releaseAll is kept only as a defensive net, not
// because the current pass ever leaves it work to do by the time Stop
// reaches it. See TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued
// (outbox_worker_test.go) for the proof.
//
// If ctx's deadline elapses first — Run genuinely still mid-send — Stop
// returns ctx.Err() without calling releaseAll at all; the orphan sweep
// covers recovery in that case once outboxOrphanStaleAfter passes, exactly
// as if Stop had never been called.
func (w *OutboxWorker) Stop(ctx context.Context) error {
	w.stopOnce.Do(func() { close(w.stopCh) })
	select {
	case <-w.doneCh:
		w.releaseAll()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// releaseAll releases every row this worker still holds claimed ('sending')
// back to 'queued', via outbox.Store.Release — called from Stop only, only
// after <-w.doneCh confirms Run has exited (see Stop's and claimedMu's doc
// comments for what that ordering does and does not make safe on its own).
// Best-effort: a Release failure is logged, not returned — Stop's contract
// is "Run has stopped," not "every row was released immediately," and a row
// this misses is still reclaimed by the orphan sweep after
// outboxOrphanStaleAfter, exactly as it was before this method existed.
//
// #0266: takes no context. An earlier version accepted one from Stop and
// never used it — every Store.Release call below already builds its own
// bounded context, independent of the caller's (which may be at or near its
// own deadline by the time Stop's doneCh case fires), matching every other
// post-send status write in this file. Dropped the parameter rather than
// leaving it unread: `go vet` does not flag an unused parameter, and a
// reader passing a context here expecting it to bound something, or to
// carry cancellation through, would be wrong.
func (w *OutboxWorker) releaseAll() {
	w.claimedMu.Lock()
	ids := make([]int64, 0, len(w.claimed))
	for id := range w.claimed {
		ids = append(ids, id)
	}
	w.claimed = nil
	w.claimedMu.Unlock()

	for _, id := range ids {
		releaseCtx, cancel := context.WithTimeout(context.Background(), writeStatusTimeout)
		if _, err := w.store.Release(releaseCtx, id); err != nil {
			w.log.Error("mailing: releasing claimed outbound_queue row on stop failed", "id", id, "err", err)
		}
		cancel()
	}
}

// trackClaimed records rows' ids as claimed-but-unsent — called the instant
// ClaimRow returns one, before it is sent. #0297's review: Stop cannot
// actually observe this mid-send — it blocks on <-w.doneCh, which only
// closes once Run (and so pass, and so sendOne) has fully returned, so by
// the time Stop ever reads w.claimed, untrackClaimed (below) has already
// removed this row on every ordinary path (see Stop's own doc comment).
// Tracking still happens on this schedule anyway: it is what makes
// releaseAll's defensive-net role meaningful — a row genuinely still
// present in w.claimed by the time Stop reaches it, however that comes
// about, is exactly what releaseAll exists to release. Kept as a
// slice-taking method (rather than narrowed to a single id) for symmetry
// with untrackClaimed and to leave the map-based implementation below
// unchanged; #0297 changed every call site to pass a single-row slice,
// since at most one row is ever claimed-but-unsent under this worker at a
// time now.
func (w *OutboxWorker) trackClaimed(rows []outbox.Row) {
	w.claimedMu.Lock()
	defer w.claimedMu.Unlock()
	if w.claimed == nil {
		w.claimed = make(map[int64]struct{}, len(rows))
	}
	for _, r := range rows {
		w.claimed[r.ID] = struct{}{}
	}
}

// untrackClaimed removes id from the claimed set — called the instant
// sendOne finishes with a row (sent, retried, or abandoned). A
// concurrent Stop cannot observe the set before this runs — it blocks
// on <-w.doneCh — so this is what keeps releaseAll's defensive net
// accurate, not what prevents a live race; see trackClaimed's and
// Stop's own doc comments.
func (w *OutboxWorker) untrackClaimed(id int64) {
	w.claimedMu.Lock()
	defer w.claimedMu.Unlock()
	delete(w.claimed, id)
}

// pass sweeps orphans, expires overdue pending signups (#0128), selects one
// batch of candidate ids, and drains it — claiming and sending each row
// individually (#0297), not the whole batch at once. Returns processed=true
// if it did any real work (claimed at least one row), so Run can skip the
// poll wait, matching worker.go's "don't busy-loop on an empty pass"
// discipline (#0122).
func (w *OutboxWorker) pass(ctx context.Context) (bool, error) {
	// #0254's review bounce: scoped to mailKinds, the same set SelectDue
	// below is scoped to (#0254 scoped ClaimDue; this pass has not called
	// it since #0297), so this sweep can never release a live claim
	// belonging to internal/handlers.SubscribeHandler's own recovery
	// poller (KindSubscribeIntake) — see OrphanSweep's doc comment for the
	// duplicate-send chain an unfiltered sweep produced.
	sweepCtx, sweepCancel := context.WithTimeout(ctx, writeStatusTimeout)
	swept, err := w.store.OrphanSweep(sweepCtx, outboxOrphanStaleAfter, mailKinds)
	sweepCancel()
	if err != nil {
		return false, fmt.Errorf("mailing: outbox orphan sweep: %w", err)
	}
	if swept > 0 {
		w.log.Warn("mailing: outbox orphan sweep reclaimed rows", "count", swept)
	}

	// #0128: expiring pending signups past confirm_expires_at rides this
	// worker's existing poll loop rather than standing up a fourth
	// dedicated ticker in main.go — it is cheap (one indexed UPDATE ...
	// RETURNING per pass, idempotent, near-zero rows in the common case)
	// and this worker already holds the one *subscribers.Store this
	// package is given (w.events, also used for confirmation_sent/
	// welcome_sent). A failure here is logged and does not abort the
	// pass — mail still needs to go out even if the expiry sweep hiccups.
	// nil-tolerant: w.events may be nil (see OutboxWorkerDeps' doc
	// comment), matching every other w.events-gated branch in this file.
	if w.events != nil {
		expireCtx, expireCancel := context.WithTimeout(ctx, writeStatusTimeout)
		expired, expireErr := w.events.ExpirePendingSweep(expireCtx, time.Now())
		expireCancel()
		if expireErr != nil {
			w.log.Error("mailing: expire-pending-signups sweep failed", "err", expireErr)
		} else if expired > 0 {
			w.log.Info("mailing: expired pending signups past confirm_expires_at", "count", expired)
		}
	}

	// #0297: SelectDue only SELECTs — it claims nothing, so a network error
	// or decode failure mid-scan here leaves every candidate row exactly as
	// it was found ('queued'), unlike the old ClaimDue-based version of
	// this pass, whose UPDATE committed claimed_at server-side before this
	// process had scanned a single RETURNING row (that residual, and the
	// #0266 item 4 reasoning about it, now applies to ClaimRow below
	// instead — one row's worth of blast radius, not a whole batch's).
	// #0254: mailKinds (below) scopes this selection to the email kinds
	// this worker's render switch actually knows how to build —
	// outbound_queue now also holds outbox.KindSubscribeIntake rows, a
	// non-email kind internal/handlers.SubscribeHandler's own recovery
	// poller claims and processes separately (see that Kind's doc
	// comment). Without this filter this worker would occasionally select
	// an intake row it cannot render, hitting render's default case and
	// eventually abandoning a row that has nothing to do with mail.
	selectCtx, selectCancel := context.WithTimeout(ctx, writeStatusTimeout)
	ids, err := w.store.SelectDue(selectCtx, w.batchSize, mailKinds)
	selectCancel()
	if err != nil {
		return false, fmt.Errorf("mailing: selecting due outbound_queue rows: %w", err)
	}
	if len(ids) == 0 {
		return false, nil
	}

	sendRate := w.outboxEffectiveSendRate(ctx)
	limiter := rate.NewLimiter(rate.Limit(sendRate), 1)

	processed := false
	for _, id := range ids {
		select {
		case <-w.stopCh:
			// Every id not yet reached is still plain 'queued' (SelectDue
			// claimed nothing) — nothing to release for them; only the row
			// currently claimed (if any — see below) is w.claimed's
			// concern, and it is released the same way it always was.
			return processed, nil
		default:
		}
		if err := limiter.Wait(ctx); err != nil {
			return processed, nil // context cancelled/deadline — shutdown, not an error to log
		}

		// #0297: the atomic claim happens HERE, immediately before this
		// row's own send — not once for the whole batch up front — so
		// claimed_at reflects when THIS row's own work began. A bounded
		// context derived FROM ctx (the caller's ambient one, not
		// detached from it the way sendOne's writes below are) so a
		// shutdown in progress stops a new claim from being taken at all,
		// rather than racing to grab one anyway; a hung round trip still
		// cannot hold this claim attempt open past writeStatusTimeout —
		// see outboxOrphanStaleAfter's doc comment for the resulting bound.
		claimCtx, claimCancel := context.WithTimeout(ctx, writeStatusTimeout)
		row, claimed, err := w.store.ClaimRow(claimCtx, id)
		claimCancel()
		if err != nil {
			w.log.Error("mailing: claiming outbound_queue row failed", "id", id, "err", err)
			continue
		}
		if !claimed {
			// Lost the race: a concurrent poller claimed it first, or an
			// orphan sweep already reclaimed it while it waited its turn
			// in this batch (only possible if it had been sitting
			// 'sending' from an EARLIER pass — SelectDue itself never
			// claims). Nothing to release; the row is exactly where it
			// should be.
			continue
		}
		// Recorded the instant the claim succeeds, before this row is
		// sent. Stop cannot actually observe it here mid-send — it blocks
		// on <-w.doneCh, which only closes once sendOne has fully
		// returned, so untrackClaimed below has already removed it on
		// every ordinary path by the time Stop ever reads w.claimed — see
		// trackClaimed's doc comment. At most one row is ever in this set
		// at a time now (#0297): the rest of the batch is still 'queued',
		// not this worker's to release.
		w.trackClaimed([]outbox.Row{row})
		processed = true
		w.sendOne(row)
		w.untrackClaimed(row.ID)
	}
	return processed, nil
}

// sendOne re-checks eligibility (#0365), renders, and sends a single
// claimed row, on a context detached from the caller's (so a SIGTERM's ctx
// cancellation doesn't abort a send already accepted by SES before the
// status write commits — the same precedent worker.go's package doc
// comment sets), and records the terminal state (sent, skipped, retried,
// or abandoned).
//
// sendGate runs as the very first statement, before render — #0365's plan
// §5. That ordering keeps three things true: the #0045/#0264
// physical-address gates inside render are untouched (an ineligible row
// returns before ever reaching them, and nothing here is reachable from
// the UI — CLAUDE.md §9); a welcome/import-invite row parked indefinitely
// by deferMissingPhysicalAddress still gets re-checked on every later
// reclaim, which is how this closes that kind's much wider window; and
// nothing is rendered for a message that will not be sent.
//
// Deliberately no new transaction around the gate. RecheckEligibleTx's own
// doc comment requires its recheck to sit inside the transaction holding
// the claim; here the worker already holds row in 'sending' via ClaimRow,
// and ClaimRow/MarkSent/MarkRetryOrAbandon/MarkSkipped all require the row
// to already be 'sending' (or, for ClaimRow, 'queued') — so no other actor
// can move this row while sendGate runs. The residual window (a complaint
// committing between sendGate's read and mailer.Send returning) is
// microseconds and unclosable in principle, since SES accepts
// asynchronously: a complaint can always land a moment after Send returns.
// What this closes is queue latency — seconds, ordinarily, up to
// indefinite for a deferred welcome/import-invite row.
func (w *OutboxWorker) sendOne(row outbox.Row) {
	gateCtx, gateCancel := context.WithTimeout(context.Background(), writeStatusTimeout)
	skip, reason, gateErr := w.sendGate(gateCtx, row)
	gateCancel()
	if gateErr != nil {
		// A transient read error is exactly when we do NOT know whether
		// this row is eligible — retrying via the ordinary backoff
		// schedule is safe; sending on an unknown state is not (#0365's
		// plan §6: "must not fail open").
		w.finishFailed(row, fmt.Errorf("checking send eligibility for kind %q: %w", row.Kind, gateErr))
		return
	}
	if skip {
		w.finishSkipped(row, reason)
		return
	}

	sendCtx, sendCancel := context.WithTimeout(context.Background(), sendMessageTimeout)
	msg, err := w.render(sendCtx, row)
	if err != nil {
		sendCancel()
		// #0264: a welcome row refused for a missing physical_address is not
		// an ordinary render/send failure — it is a policy gate the row must
		// clear automatically once an admin sets the address, so it takes a
		// dedicated path that never abandons it. See
		// deferMissingPhysicalAddress and errWelcomePhysicalAddressRequired.
		// #0129's import invitation is refused on the identical gate — see
		// errImportInviteMissingPhysicalAddress's own doc comment.
		if errors.Is(err, errWelcomePhysicalAddressRequired) || errors.Is(err, errImportInviteMissingPhysicalAddress) {
			w.deferMissingPhysicalAddress(row, err.Error())
			return
		}
		w.finishFailed(row, fmt.Errorf("rendering kind %q: %w", row.Kind, err))
		return
	}

	messageID, sendErr := w.mailer.Send(sendCtx, msg)
	sendCancel()
	if sendErr != nil {
		w.finishFailed(row, sendErr)
		return
	}

	writeCtx, writeCancel := context.WithTimeout(context.Background(), writeStatusTimeout)
	defer writeCancel()
	sentOK, err := w.store.MarkSent(writeCtx, row.ID, messageID)
	if err != nil {
		w.log.Error("mailing: marking outbound_queue row sent failed", "id", row.ID, "kind", row.Kind, "err", err)
		return
	}
	if !sentOK {
		// #0254's review bounce: MarkSent requires status='sending', so
		// affecting zero rows means something else already moved this row
		// out from under this send — an id that no longer exists, a
		// concurrent claim, or (the proven case) an orphan sweep that had
		// no business touching it. The message was already accepted by SES
		// at this point; this is not a reason to retry or abandon it, only
		// to say so loudly. Logged rather than silently discarded, because
		// discarding it silently is exactly what let #0254's sweep bug
		// masquerade as a healthy pass instead of surfacing the duplicate
		// send it was producing.
		w.log.Warn("mailing: MarkSent affected no rows — this row's claim was lost before the send it just completed was recorded; the row may be re-sent by a later pass", "id", row.ID, "kind", row.Kind)
	}

	// #0126's plan §6: confirmation_sent is written here — "a confirmation
	// message LEFT the outbound queue" — not at enqueue time. #0127 adds
	// welcome_sent on the identical precedent ("a welcome message LEFT the
	// outbound queue"), not at Confirm's enqueue time — see
	// internal/subscribers.Store.Confirm's own comment on why it does NOT
	// write welcome_sent itself. Every other kind either has no
	// subscriber_events action yet (admin alerts, auth mail) or isn't in
	// the closed set at all (already_subscribed — see events.go's package
	// doc comment, #0126's plan §9 item 6).
	var sentAction subscribers.Action
	switch row.Kind {
	case outbox.KindConfirmation:
		sentAction = subscribers.ActionConfirmationSent
	case outbox.KindWelcome:
		sentAction = subscribers.ActionWelcomeSent
	case outbox.KindImportInvite:
		// #0129: invite_sent means "an import invitation LEFT the outbound
		// queue" — the identical precedent confirmation_sent/welcome_sent
		// already establish, not "was enqueued" (ImportStore.Commit does
		// NOT write this action at enqueue time — see that method's own
		// comment).
		sentAction = subscribers.ActionInviteSent
	}
	if w.events != nil && sentAction != "" && row.SubscriberID != nil {
		eventCtx, eventCancel := context.WithTimeout(context.Background(), writeStatusTimeout)
		if err := w.events.RecordEvent(eventCtx, subscribers.Event{
			SubscriberID: row.SubscriberID,
			Email:        row.Recipient,
			Action:       sentAction,
		}); err != nil {
			w.log.Error("mailing: recording sent event failed", "id", row.ID, "action", sentAction, "subscriber_id", *row.SubscriberID, "err", err)
		}
		eventCancel()
	}
}

// finishFailed records a send/render failure via MarkRetryOrAbandon,
// logging the outcome either way.
func (w *OutboxWorker) finishFailed(row outbox.Row, sendErr error) {
	writeCtx, writeCancel := context.WithTimeout(context.Background(), writeStatusTimeout)
	defer writeCancel()
	maxRetries := w.effectiveMaxRetries(writeCtx)
	if _, err := w.store.MarkRetryOrAbandon(writeCtx, row.ID, row.Attempts, sendErr.Error(), maxRetries); err != nil {
		w.log.Error("mailing: recording outbound_queue failure failed", "id", row.ID, "kind", row.Kind, "send_err", sendErr, "err", err)
		return
	}
	if row.Attempts >= maxRetries {
		w.log.Error("mailing: outbound_queue row abandoned after max retries", "id", row.ID, "kind", row.Kind, "attempts", row.Attempts, "err", sendErr)
	} else {
		w.log.Warn("mailing: outbound_queue send failed, will retry", "id", row.ID, "kind", row.Kind, "attempts", row.Attempts, "err", sendErr)
	}
}

// sendGate is #0365's send-time re-check: called as the first statement of
// sendOne, it decides whether row should still be sent. Returns
// skip=false, err=nil immediately for any kind not in gatedKinds — the
// ungatedKinds map exists purely so TestGatedKindsPartitionEveryMailKind
// can assert that omission is deliberate, not an accident of scope.
//
// row.SubscriberID == nil on a gated kind is impossible by construction —
// every producer of every gatedKinds kind sets it (#0365's plan §2's
// table) — so this treats it as a defensive fail-OPEN (Warn, then send),
// matching the convention of every other nil-tolerant seam in this file:
// a defensive branch guarding against a state the code cannot actually
// reach must not be able to silently kill real mail. w.events == nil gets
// the identical fail-open treatment, for the identical reason
// (OutboxWorkerDeps.Events is documented nil-tolerant; production always
// wires it).
//
// A genuine eligibility-read error is different and MUST fail CLOSED: it
// is returned to the caller, which routes it to finishFailed's ordinary
// retry/backoff rather than sending on an unknown state (#0365's plan §6).
func (w *OutboxWorker) sendGate(ctx context.Context, row outbox.Row) (skip bool, reason string, err error) {
	wantStatus, gated := gatedKinds[row.Kind]
	if !gated {
		return false, "", nil
	}

	if row.SubscriberID == nil {
		w.log.Warn("mailing: gated outbound_queue row has no subscriber_id — sending without a send-time recheck", "id", row.ID, "kind", row.Kind)
		return false, "", nil
	}

	if w.events == nil {
		w.log.Warn("mailing: outbox worker has no subscribers.Store wired — sending gated row without a send-time recheck", "id", row.ID, "kind", row.Kind)
		return false, "", nil
	}

	elig, eligErr := w.events.SendEligibility(ctx, *row.SubscriberID)
	if eligErr != nil {
		return false, "", fmt.Errorf("reading subscriber %d's send eligibility: %w", *row.SubscriberID, eligErr)
	}

	switch {
	case !elig.Found:
		// Positively learned there is no subscriber — different from the
		// nil-SubscriberID case above, which is a defensive branch for a
		// state that cannot occur. Unreachable in practice
		// (outbound_queue.subscriber_id is ON DELETE CASCADE), so reaching
		// it means an inconsistent database; mailing on that basis is not
		// defensible (#0365's plan §6).
		return true, "subscriber row no longer exists", nil
	case elig.Status != wantStatus:
		// Phrasing mirrors recheckReason (audience.go) exactly.
		return true, fmt.Sprintf("subscriber status is now %q, not %q", elig.Status, wantStatus), nil
	case elig.Suppressed:
		return true, "address is now suppressed", nil
	case elig.Email != row.Recipient:
		return true, "subscriber's email changed since enqueue", nil
	default:
		return false, "", nil
	}
}

// finishSkipped records a send-time eligibility failure via
// outbox.Store.MarkSkipped (#0365) and logs it. Unlike finishFailed — an
// ordinary send/render failure, which retries on backoff — a skip is a
// decision, not a fault: the row terminates immediately, matching
// RecheckEligibleTx/MarkSkippedTx's identical one-shot semantics for
// campaign mail (audience.go).
//
// No subscriber_events row is written here, no Action constant exists for
// this, and no admin alert fires — #0365's plan §3.4: nothing NEW happened
// to the address. The facts that caused the skip ('complained',
// 'suppressed') are already in that log, timestamped earlier, by the SES
// event handler; a third row saying "and therefore we withheld a
// confirmation" would duplicate evidence the log already holds.
//
// The Warn deliberately omits the recipient address — #0277's lesson
// (CLAUDE.md §8b): a prior pass published a live token into a committed
// issue transcript, and this file's logs get pasted into issue files the
// same way. id/kind/reason are enough to diagnose from outbound_queue
// directly.
func (w *OutboxWorker) finishSkipped(row outbox.Row, reason string) {
	writeCtx, writeCancel := context.WithTimeout(context.Background(), writeStatusTimeout)
	defer writeCancel()
	ok, err := w.store.MarkSkipped(writeCtx, row.ID, reason)
	if err != nil {
		w.log.Error("mailing: marking outbound_queue row skipped failed", "id", row.ID, "kind", row.Kind, "err", err)
		return
	}
	if !ok {
		// MarkSkipped requires status='sending'; affecting zero rows means
		// something else already moved this row out from under this
		// decision — matching MarkSent's own "false means the claim was
		// lost" convention (sendOne's !sentOK branch, above).
		w.log.Warn("mailing: MarkSkipped affected no rows — this row's claim was lost before the skip decision was recorded", "id", row.ID, "kind", row.Kind)
	}
	w.log.Warn("mailing: outbound_queue send skipped — recipient no longer eligible", "id", row.ID, "kind", row.Kind, "reason", reason)
}

// welcomeAddressDeferMaxRetries is the maxRetries deferMissingPhysicalAddress
// passes to MarkRetryOrAbandon. MarkRetryOrAbandon abandons a row once
// attempts >= maxRetries; a welcome deferred for a missing physical_address
// (#0264) must NEVER reach that terminal state on its own — unlike an
// ordinary send failure, this is a policy gate, and an abandoned row is not
// automatically retried once the address is set. A value this large keeps
// the "attempts >= maxRetries" comparison permanently false, so the row
// always takes MarkRetryOrAbandon's 'queued' branch and backs off along the
// ordinary schedule (Backoff, clamped to 24h) — reusing that existing,
// tested mechanism rather than inventing a second requeue path.
const welcomeAddressDeferMaxRetries = math.MaxInt32

// errWelcomePhysicalAddressRequired is render's signal that a welcome
// message (outbox.KindWelcome) could not be built because
// settings.physical_address is unset. #0264: #0127's welcome carries a
// workshops CTA and RFC 8058 one-click unsubscribe headers — #0127's own
// phase-3 review judged that this makes it commercial rather than
// transactional, so CAN-SPAM §7704's physical-address requirement applies
// to it the same way #0045's CLAUDE.md §9 rule applies to the campaign
// worker. sendOne recognizes this specific sentinel and routes it to
// deferMissingPhysicalAddress instead of the ordinary finishFailed path.
// The literal carries no issue citation (TestNoAdminFacingStringCitesInternalDocs,
// #0172/#0175/#0178/#0181): this error's text lands in outbound_queue.error,
// which the admin pending/dashboard screens can surface, and an admin
// cannot read issues/0264.md.
var errWelcomePhysicalAddressRequired = errors.New("mailing: welcome email refused: physical_address is not set")

// errImportInviteMissingPhysicalAddress is render's identical signal for
// outbox.KindImportInvite (#0129): an import invitation solicits consent
// from someone who never asked, and carries a one-click decline — the same
// two facts that make BuildWelcomeEmail commercial rather than
// transactional under #0264's reasoning apply here too, so CAN-SPAM §7704's
// physical-address requirement applies the same way. sendOne recognizes
// this sentinel alongside errWelcomePhysicalAddressRequired and routes both
// to deferMissingPhysicalAddress. See that sentinel's own doc comment for
// why the literal carries no issue citation.
var errImportInviteMissingPhysicalAddress = errors.New("mailing: import invite email refused: physical_address is not set")

// deferMissingPhysicalAddress requeues row — a welcome or import-invite
// message render refused to build because settings.physical_address is
// unset — using the ordinary backoff schedule and
// welcomeAddressDeferMaxRetries so it is never abandoned. Logged at Warn,
// not Error: this is expected operational state (CLAUDE.md §10 item 3 —
// the physical mailing address is not yet configured), not a bug. The
// state that produced this row already succeeded and is not at risk:
// neither internal/subscribers.Store.Confirm nor ImportStore.Commit reads
// physical_address, so a refusal here only defers the one email, never the
// state change that queued it. errMsg is whichever of
// errWelcomePhysicalAddressRequired / errImportInviteMissingPhysicalAddress
// render actually returned, so outbound_queue.error names the right
// message for whichever kind this row is.
func (w *OutboxWorker) deferMissingPhysicalAddress(row outbox.Row, errMsg string) {
	writeCtx, writeCancel := context.WithTimeout(context.Background(), writeStatusTimeout)
	defer writeCancel()
	if _, err := w.store.MarkRetryOrAbandon(writeCtx, row.ID, row.Attempts, errMsg, welcomeAddressDeferMaxRetries); err != nil {
		w.log.Error("mailing: deferring send for missing physical_address failed", "id", row.ID, "kind", row.Kind, "err", err)
		return
	}
	subscriberID := int64(-1)
	if row.SubscriberID != nil {
		subscriberID = *row.SubscriberID
	}
	w.log.Warn("mailing: send deferred pending physical_address configuration", "id", row.ID, "kind", row.Kind, "subscriber_id", subscriberID)
}

// effectiveMaxRetries reads settings.queue_max_retries, falling back to
// outbox.DefaultMaxRetries on a missing row or an unparseable/non-positive
// value — the same degrade-gracefully convention worker.go's
// effectiveSendRate and internal/handlers/soft_bounce.go both establish for
// their own settings-backed constants.
func (w *OutboxWorker) effectiveMaxRetries(ctx context.Context) int {
	raw, err := w.settings.GetSetting(ctx, settingQueueMaxRetries)
	if err != nil {
		return outbox.DefaultMaxRetries
	}
	n, perr := strconv.Atoi(strings.TrimSpace(raw))
	if perr != nil || n <= 0 {
		return outbox.DefaultMaxRetries
	}
	return n
}

// outboxEffectiveSendRate mirrors worker.go's effectiveSendRate exactly
// (settings.max_send_rate, clamped to envMaxSendRate, falling back to
// envMaxSendRate on any read/parse failure) — kept as this worker's own
// method rather than shared, matching this file's "own rate limiter"
// decision in the package doc comment.
func (w *OutboxWorker) outboxEffectiveSendRate(ctx context.Context) int {
	raw, err := w.settings.GetSetting(ctx, settingMaxSendRate)
	if err != nil {
		return w.envMaxSendRate
	}
	n, perr := strconv.Atoi(strings.TrimSpace(raw))
	if perr != nil || n <= 0 {
		return w.envMaxSendRate
	}
	if n > w.envMaxSendRate {
		return w.envMaxSendRate
	}
	return n
}

// --- payload shapes and rendering ---
//
// Every payload struct below is the JSONB contract between a producer
// (internal/subscribers.Store, internal/auth's services) and this
// renderer — matched by JSON field name, not by a shared Go type, since
// producer and consumer live in different packages and outbox.Item.Payload
// is deliberately `any`. See internal/outbox's package doc comment for why
// payload holds template inputs, not rendered MIME.

type confirmationPayload struct {
	ConfirmToken string `json:"confirm_token"`
	ManageToken  string `json:"manage_token"`
	TTLSeconds   int64  `json:"ttl_seconds"`
}

type alreadySubscribedPayload struct {
	ManageToken string `json:"manage_token"`
}

type registrationPayload struct {
	Token      string `json:"token"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type recoveryPayload struct {
	Token      string `json:"token"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type sessionsRevokedPayload struct {
	At time.Time `json:"at"`
}

type adminAlertPayload struct {
	Subject string   `json:"subject"`
	Lines   []string `json:"lines"`
}

// welcomePayload is outbound_queue.payload's shape for outbox.KindWelcome
// (#0127) — mirrors internal/subscribers.welcomePayload field-for-field
// (matched by JSON field name, not a shared Go type, per this file's own
// "producer and consumer live in different packages" convention above).
// InterestNames is the subscriber's selection AT CONFIRM TIME, not re-read
// at send time — see BuildWelcomeEmail's doc comment.
type welcomePayload struct {
	ManageToken   string   `json:"manage_token"`
	InterestNames []string `json:"interest_names"`
}

// importInvitePayload is outbound_queue.payload's shape for
// outbox.KindImportInvite (#0129) — mirrors
// internal/subscribers.importInvitePayload field-for-field (matched by JSON
// field name, not a shared Go type, per this file's own "producer and
// consumer live in different packages" convention above).
// ImportSource/SourceDetail/CollectedAt are the subscriber_imports row's
// values AT ENQUEUE TIME, not re-read at send time — see
// BuildImportInviteEmail's doc comment.
type importInvitePayload struct {
	ConfirmToken string    `json:"confirm_token"`
	ManageToken  string    `json:"manage_token"`
	TTLSeconds   int64     `json:"ttl_seconds"`
	ImportSource string    `json:"import_source"`
	SourceDetail string    `json:"source_detail"`
	CollectedAt  time.Time `json:"collected_at"`
}

// render resolves physical_address at SEND time (not enqueue time) — #0126's
// plan §3: "a small improvement: an address set between enqueue and send
// now produces a correct footer, where today it would not." This worker
// deliberately does NOT refuse to send confirmation, already_subscribed,
// registration, recovery, sessions_revoked, or admin_alert for a missing
// physical_address the way #0045's CAMPAIGN send worker refuses to start a
// campaign (CLAUDE.md §9's rule is scoped to that worker;
// BuildConfirmationEmail/BuildAlreadySubscribedEmail already document "" as
// simply omitting the line) — refusing here would turn a cosmetic gap into a
// broken signup flow, and none of those six messages is commercial.
//
// KindWelcome is the one deliberate exception (#0264, correcting the earlier
// version of this comment that predated the welcome email and did not
// consider it). #0127's welcome carries a workshops CTA and RFC 8058
// one-click unsubscribe headers — #0127's own phase-3 review judged that
// this makes it commercial rather than transactional, so CAN-SPAM §7704's
// address requirement applies to it the same way it does to a campaign. A
// blank physical_address there returns errWelcomePhysicalAddressRequired
// instead of building the message; sendOne recognizes that sentinel and
// requeues the row indefinitely (deferMissingPhysicalAddress) rather than
// treating it as an ordinary send failure, so a refusal never costs the
// subscriber their confirmation — Confirm already committed independently
// of this check — and the welcome sends automatically once an admin sets
// the address.
func (w *OutboxWorker) render(ctx context.Context, row outbox.Row) (Message, error) {
	switch row.Kind {
	case outbox.KindConfirmation:
		var p confirmationPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildConfirmationEmail(row.Recipient, w.baseURL, p.ConfirmToken, p.ManageToken, time.Duration(p.TTLSeconds)*time.Second, w.physicalAddress(ctx)), nil

	case outbox.KindAlreadySubscribed:
		var p alreadySubscribedPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildAlreadySubscribedEmail(row.Recipient, w.baseURL, p.ManageToken, w.physicalAddress(ctx)), nil

	case outbox.KindRegistration:
		var p registrationPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildRegistrationEmail(row.Recipient, w.baseURL, p.Token, time.Duration(p.TTLSeconds)*time.Second), nil

	case outbox.KindRecovery:
		var p recoveryPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildRecoveryEmail(row.Recipient, w.baseURL, p.Token, time.Duration(p.TTLSeconds)*time.Second), nil

	case outbox.KindSessionsRevoked:
		var p sessionsRevokedPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildSessionsRevokedEmail(row.Recipient, w.baseURL, p.At), nil

	case outbox.KindAdminAlert:
		var p adminAlertPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildAdminAlertEmail(row.Recipient, w.baseURL, p.Subject, p.Lines), nil

	case outbox.KindWelcome:
		var p welcomePayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		// #0264: unlike every other kind in this switch, a blank
		// physical_address here is a refusal, not an omission — see this
		// function's doc comment.
		addr := w.physicalAddress(ctx)
		if strings.TrimSpace(addr) == "" {
			return Message{}, errWelcomePhysicalAddressRequired
		}
		return BuildWelcomeEmail(row.Recipient, w.baseURL, w.listDomain, p.ManageToken, p.InterestNames, addr), nil

	case outbox.KindImportInvite:
		var p importInvitePayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		// #0129, mirroring KindWelcome above: a blank physical_address here
		// is a refusal, not an omission — see BuildImportInviteEmail's and
		// errImportInviteMissingPhysicalAddress's doc comments.
		addr := w.physicalAddress(ctx)
		if strings.TrimSpace(addr) == "" {
			return Message{}, errImportInviteMissingPhysicalAddress
		}
		return BuildImportInviteEmail(row.Recipient, w.baseURL, w.listDomain, p.ConfirmToken, p.ManageToken,
			p.ImportSource, p.SourceDetail, p.CollectedAt, time.Duration(p.TTLSeconds)*time.Second, addr), nil

	default:
		// goodbye: no producer yet. Returning an error routes through the
		// ordinary MarkRetryOrAbandon path — it will retry a few times,
		// then land on 'abandoned' with this error retained, rather than
		// crashing the worker or silently dropping the row.
		return Message{}, fmt.Errorf("mailing: no renderer for outbound_queue kind %q", row.Kind)
	}
}

// physicalAddress reads settings.physical_address for the email footer,
// treating a nil dependency or any read error as an empty address — the
// same nil-tolerant convention SubscribeHandler used before #0126 moved
// this resolution here.
func (w *OutboxWorker) physicalAddress(ctx context.Context) string {
	if w.settings == nil {
		return ""
	}
	value, err := w.settings.GetSetting(ctx, settingPhysicalAddress)
	if err != nil {
		return ""
	}
	return value
}
