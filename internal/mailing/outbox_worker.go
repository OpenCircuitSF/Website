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
// not silently retune the other's staleness window. The two derivations
// are NOT identical (#0284), and #0295 checked why in detail rather than
// assuming the two workers match: this worker's claimed_at is stamped
// ONCE per whole batch (ClaimDue's single UPDATE) while pass sends that
// batch SERIALLY, one row at a time, so a predecessor's real processing
// time genuinely delays how long the LAST row's claim has been held. A
// predecessor's contribution is bounded by ITS OWN worst-case send time,
// not by the rate limiter's interval — the limiter only ever makes a send
// wait LONGER than the interval, never shorter, so it cannot be the
// dominant term once a single row's own bound exceeds a second (it does:
// 35s). outboxOrphanStaleAfter's own doc comment, below, has the
// batch-aware arithmetic and says why the rate limiter drops out of it
// entirely.
//
// worker.go's *Worker does NOT share this shape, and #0295's finding —
// checked against the code, not assumed from this worker's defect — is
// that it never did: *Worker's ClaimBatch never claims a row or touches
// claimed_at at all; SendStore.ClaimRow performs the atomic claim
// per-recipient, individually, at the exact moment that recipient's own
// send begins (worker.go's orphanStaleAfter doc comment has the full
// story). So batch size never enters worker.go's bound, and there was no
// batching flaw there for #0295 to fix — the two orphanStaleAfter
// derivations differ because the two workers' claim mechanisms differ,
// not because one implementer's judgment call diverged from the other's.
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
// #0284: the original derivation, 2 * (sendMessageTimeout +
// writeStatusTimeout), sized itself against ONE message's worst case. But
// ClaimDue's single UPDATE stamps claimed_at ONCE for the WHOLE batch (up
// to outboxDefaultBatchSize rows), and pass then sends that batch
// SERIALLY, one at a time, behind rate.Limiter (outboxEffectiveSendRate).
// So the last row claimed has already sat 'sending' for however long its
// predecessors took, not just to be rate-limited, but to actually run
// their own sends to completion — the two are not the same thing.
//
// A first correction (this issue's first, bounced pass) charged each
// predecessor the rate limiter's interval alone, reasoning that the
// limiter enforces a MINIMUM gap between sends. That is true but
// irrelevant to this bound: the limiter's interval is a floor on pacing,
// never a ceiling on a row's cost, so it is never the dominant term. A
// send that takes longer than the interval — which every real send does,
// since a single row's own worst case (sendMessageTimeout +
// writeStatusTimeout = 35s) vastly exceeds any interval the enforced
// >= 1 msg/sec rate floor can produce (<= 1s) — makes the interval
// disappear from the max(interval, rowCost) that actually governs how
// long the NEXT row waits. So the rate limiter drops out of this
// derivation entirely; charging it was the defect, not the fix.
//
// The bound instead charges every one of the outboxDefaultBatchSize rows
// in a full batch — including the last row itself — the full delay a
// predecessor contributes to the NEXT row's start, not just the time it
// holds its own claim. A row holds its claim for sendMessageTimeout +
// writeStatusTimeout = 35s, released at MarkSent, but RecordEvent (see
// sendOne below) runs AFTER MarkSent and still delays when the next row's
// send can begin — so a predecessor's real contribution to the next row's
// wait is sendMessageTimeout + 2*writeStatusTimeout = 40s, not 35s. This
// bound charges that 40s figure to every row in the batch, tail row
// included:
//
//	outboxDefaultBatchSize * (sendMessageTimeout + 2*writeStatusTimeout)
//	= 20 * (30s + 2*5s) = 800s today.
//
// The true worst case is 19 predecessors at 40s each plus the tail row's
// own 35s claim-hold: 19*40s + 35s = 795s. Charging every row — tail
// included — the full 40s predecessor rate rather than trying to be exact
// about the tail's shorter 35s contribution leaves 5s of real slack
// instead of none. That is what this bound is: one that charges each
// predecessor its full delay contribution, including the RecordEvent
// tail — not a "deliberately simple" or "generous" figure relative to
// some tighter one, since the previous 700s value here was BELOW the 795s
// true worst case, not above it. What does still justify a larger window
// over a smaller one is the cost asymmetry, unchanged from before: this
// window being too LARGE only costs recovery latency on the crash path —
// Stop already releases every claim on a graceful shutdown, so
// OrphanSweep exists purely for a hard kill — while too SMALL costs a
// duplicate send, #0254's failure mode. Choose too large.
//
// Even 800s is not, and cannot be made, a STRICT upper bound from these
// constants alone. OutboxWorker.Run is started as
// `go outboxWorker.Run(context.Background())` (cmd/opencircuit/main.go),
// and pass's calls to store.ClaimDue and outboxEffectiveSendRate both run
// on that context with no deadline of their own — including ClaimDue's
// RETURNING scan, which executes after its UPDATE has already committed
// claimed_at server-side. That is real, unbounded elapsed time between
// the stamp and row 1's send even starting, so no expression over
// outboxDefaultBatchSize, sendMessageTimeout, and writeStatusTimeout can
// ever be a strict bound on claim age. Closing that gap is #0297's
// territory (a per-row claimed_at re-stamp in internal/outbox's shared
// claim path), not a bigger multiplier here.
//
// Expressed from the real package constants, not a hand-computed
// literal, so raising outboxDefaultBatchSize or either timeout moves this
// window automatically. See TestOutboxOrphanStaleAfterCoversFullBatch
// (outbox_orphan_stale_after_test.go) for the invariant this maintains,
// checked against an INDEPENDENTLY expressed worst case.
//
// A var, not a const, so a test can shrink it; NOT sized against measured
// machine load (CLAUDE.md §5).
var outboxOrphanStaleAfter = time.Duration(outboxDefaultBatchSize) * (sendMessageTimeout + 2*writeStatusTimeout)

// mailKinds is every outbox.Kind this worker's render switch knows how to
// build a message for — i.e. every Kind EXCEPT outbox.KindSubscribeIntake
// (#0254), which is not an email and is claimed separately by
// internal/handlers.SubscribeHandler's own recovery poller. Passed to
// outbox.Store.ClaimDue's and OrphanSweep's kinds filters in pass, below,
// so this worker never claims OR sweeps a row it cannot render.
//
// A hand-maintained list, deliberately, and #0254's review bounce corrected
// an earlier version of this comment that overstated what that buys: it
// claimed a Kind added here without updating render's switch would "fail
// loudly in render's default case" — true only of a Kind mistakenly ADDED
// to this list, whose render call reaches the default case and abandons
// after retries. It says nothing about the opposite mistake — a Kind added
// to internal/outbox and forgotten HERE — and that omission is silent, not
// loud: ClaimDue(mailKinds...) never claims such a row at all, so render's
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
	// claimed holds the outbound_queue ids from the most recent batch that
	// this worker holds claimed ('sending') but has not yet finished
	// sending (MarkSent/MarkRetryOrAbandon). A row is added the instant
	// ClaimDue returns it and removed the instant sendOne finishes with
	// it — so anything still present when Stop reads this set is exactly
	// the set Stop's doc comment promises to release.
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

// Stop signals Run to stop claiming new work, releases any row this
// process currently holds claimed-but-unsent back to 'queued' (so a
// restart, or another process's worker, picks it up immediately instead of
// waiting out outboxOrphanStaleAfter), and blocks until Run has returned or
// ctx's deadline elapses. Safe to call more than once — including
// concurrently: see claimedMu's doc comment for why that specific promise
// depends on the mutex, not merely on the doneCh ordering below.
//
// The release only runs once <-w.doneCh confirms Run has fully returned —
// deliberately, not from inside pass/Run itself. Run's for loop calls pass
// synchronously and only closes doneCh (via its own defer) after pass has
// returned, so by the time Stop observes doneCh closed, no goroutine is
// still mutating w.claimed: trackClaimed/untrackClaimed have finished for
// this batch, and whatever ids remain are exactly the rows pass's stopCh
// check left claimed (see pass's own comment). If ctx's deadline elapses
// first — Run genuinely still mid-send — Stop returns ctx.Err() WITHOUT
// releasing anything, because a row a live send might still complete for
// must not be raced back to 'queued' out from under it; the orphan sweep
// covers that case once outboxOrphanStaleAfter passes, same as before this
// fix existed.
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
// ClaimDue returns them, before any row in the batch is sent, so a Stop
// landing between the claim and the first send still sees every row in
// w.claimed.
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
// sendOne finishes with a row (sent, retried, or abandoned), so a
// concurrent Stop no longer considers it outstanding.
func (w *OutboxWorker) untrackClaimed(id int64) {
	w.claimedMu.Lock()
	defer w.claimedMu.Unlock()
	delete(w.claimed, id)
}

// pass sweeps orphans, expires overdue pending signups (#0128), claims one
// batch, and sends it. Returns processed=true if it did any real work
// (claimed at least one row), so Run can skip the poll wait, matching
// worker.go's "don't busy-loop on an empty pass" discipline (#0122).
func (w *OutboxWorker) pass(ctx context.Context) (bool, error) {
	// #0254's review bounce: scoped to mailKinds, the same set ClaimDue
	// below is scoped to, so this sweep can never release a live claim
	// belonging to internal/handlers.SubscribeHandler's own recovery
	// poller (KindSubscribeIntake) — see OrphanSweep's doc comment for the
	// duplicate-send chain an unfiltered sweep produced.
	sweepCtx, sweepCancel := context.WithTimeout(ctx, writeStatusTimeout)
	swept, err := w.store.OrphanSweep(sweepCtx, outboxOrphanStaleAfter, mailKinds...)
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

	// #0266 item 4, pre-existing and unchanged by this issue: ClaimDue is a
	// single `UPDATE ... RETURNING` (internal/outbox/store.go), which
	// commits server-side as soon as Postgres executes it — before this
	// process has scanned a single row of the result set. If scanning the
	// RETURNING rows then fails (a network error mid-stream, a decode
	// error), ClaimDue returns (nil, err) here: the rows it just claimed
	// are genuinely 'sending' in the database, but w.trackClaimed is never
	// called for them, because there is nothing in `rows` to pass it. This
	// worker cannot release what it was never told it claimed. Nothing is
	// lost: OrphanSweep (this pass's first step, above) reclaims any row
	// stuck at 'sending' past outboxOrphanStaleAfter regardless of whether
	// this process's own w.claimed ever knew about it — the same safety
	// net that covers a hard crash mid-batch. Recorded here so this gap is
	// not rediscovered as new; fixing it would mean ClaimDue running the
	// UPDATE and the scan inside one explicit transaction it can still roll
	// back, which is a change to internal/outbox/store.go's shared claim
	// path, out of this issue's scope.
	// #0254: mailKinds (below) scopes this claim to the email kinds this
	// worker's render switch actually knows how to build — outbound_queue
	// now also holds outbox.KindSubscribeIntake rows, a non-email kind
	// internal/handlers.SubscribeHandler's own recovery poller claims and
	// processes separately (see that Kind's doc comment). Without this
	// filter this worker would occasionally claim an intake row it cannot
	// render, hitting render's default case and eventually abandoning a row
	// that has nothing to do with mail.
	rows, err := w.store.ClaimDue(ctx, w.batchSize, mailKinds...)
	if err != nil {
		return false, fmt.Errorf("mailing: claiming outbound_queue batch: %w", err)
	}
	if len(rows) == 0 {
		return false, nil
	}
	// Recorded the instant the claim succeeds, before any row is sent, so
	// a Stop landing anywhere in the loop below sees every row of this
	// batch as outstanding — see trackClaimed's doc comment.
	w.trackClaimed(rows)

	sendRate := w.outboxEffectiveSendRate(ctx)
	limiter := rate.NewLimiter(rate.Limit(sendRate), 1)

	for _, row := range rows {
		select {
		case <-w.stopCh:
			// Leave this and every remaining row of the batch claimed in
			// w.claimed; Stop's own release (releaseAll, called from the
			// goroutine that invoked Stop, after Run — and therefore this
			// pass — has fully returned) or, failing that, the orphan
			// sweep will reclaim them.
			return true, nil
		default:
		}
		if err := limiter.Wait(ctx); err != nil {
			return true, nil // context cancelled/deadline — shutdown, not an error to log
		}
		w.sendOne(row)
		w.untrackClaimed(row.ID)
	}
	return true, nil
}

// sendOne renders and sends a single claimed row, on a context detached
// from the caller's (so a SIGTERM's ctx cancellation doesn't abort a send
// already accepted by SES before the status write commits — the same
// precedent worker.go's package doc comment sets), and records the
// terminal state (sent, retried, or abandoned).
func (w *OutboxWorker) sendOne(row outbox.Row) {
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
