// Subscribe implements POST /api/subscribe — PRD §6.3's double opt-in
// signup, with the anti-abuse controls #0026 requires: a honeypot field, a
// form-timing gate, email syntax + disposable-domain validation, and a
// per-IP rate limiter (wired in cmd/opencircuit/main.go via
// middleware.RateLimiter, matching the pattern used for the auth routes).
//
// # The uniform 202 — CLAUDE.md §9's single most important rule for this file
//
// Every branch through Subscribe ends by calling writeSubscribeUniform202,
// which writes the exact same status code, headers, and JSON body
// (subscribeUniformBody) regardless of which branch ran: a brand-new
// signup, an existing active/pending/unsubscribed/bounced/complained
// subscriber, a suppressed address, a honeypot catch, a failed timing gate,
// or an internal error encountered while trying to act on any of the
// above. There is exactly one call site that writes a 202 for this
// endpoint. Varying the response by branch — even by a single header or a
// few milliseconds of extra work — would turn this endpoint into an
// email-enumeration oracle; see internal/handlers/subscribe_test.go's byte-
// equality assertions.
//
// Only request-shape validation that does NOT depend on whether the
// submitted email is already on the list — malformed JSON, invalid email
// syntax, a disposable domain, an unknown interest slug — is allowed to
// answer with a different status (400). None of those checks ever touch
// the subscribers table, so their outcome cannot leak anything about a
// specific address's subscription state.
//
// # complained never auto-resubscribes
//
// existingSignup's complained case is intentionally empty: no store call,
// no mailer call, nothing but falling through to the same uniform 202 every
// other branch produces. RestartSignup (internal/subscribers) additionally
// guards this at the data layer via statusLockedFromNonAdmin, so even a
// race between this handler's status read and the store's write — a
// complaint landing between the two — cannot move a subscriber out of
// complained. See RestartSignup's doc comment and this issue's Gotchas for
// the #0025 review finding this closes.
//
// # The byte-identical body is not the only observable — #0026's review (bounced)
//
// A phase-3 review measured that even with a structurally byte-identical
// 202, mailer.Send running synchronously inside the request made "was an
// email sent" directly observable as latency: with a realistic ~80ms SES
// call, every branch that sends mail (new signup, restart, resend, already-
// subscribed) took ~80ms longer than every branch that doesn't (suppressed,
// complained, honeypot, cooldown-blocked) — fully disjoint distributions.
// Combined with the "already subscribed" mail having no cooldown at all,
// that produced a two-probe test (submit twice, time the second response)
// that classified list membership at 100% accuracy. The fix has two parts:
//
//  1. mailer.Send never runs on the request goroutine. sendConfirmation and
//     sendAlreadySubscribed hand a built mailing.Message to an in-process
//     queue (sendQueue) drained by a small fixed pool of worker goroutines
//     started in NewSubscribeHandler; Subscribe returns as soon as the
//     fast, non-network claim below succeeds, regardless of whether the
//     send has even started. This removes the whole latency channel.
//  2. Both outbound messages this endpoint can send are gated by an atomic
//     claim in internal/subscribers (ClaimConfirmationSend /
//     ClaimAlreadySubscribedSend) taken BEFORE the send is enqueued — one
//     conditional UPDATE, not a read then a write — so concurrent requests
//     for the same subscriber can't all observe "not in cooldown" and all
//     claim. If the async send then fails, the worker releases the claim
//     (ReleaseConfirmationClaim / ReleaseAlreadySubscribedClaim) so a later
//     request can retry rather than being stuck behind a cooldown anchored
//     to a message nobody received.
//
// A send failure is never silently swallowed: the worker logs it
// (h.log.Error, with the subscriber id and which mail it was) and releases
// the claim, which is itself observable and retryable — the next
// legitimate request for that address attempts the send again. See this
// issue's Gotchas for why no separate status column or metric was added.
//
// # The elapsed-time channel — #0088
//
// #0026's review characterised the residual synchronous INSERT ("brand-new
// signup still does a synchronous write") as weaker than the 80ms mail-send
// channel it had just closed, on the theory that probing a given address
// creates the row, so an attacker only gets one clean observation per
// target. #0088 measured that residual directly and found it total, not
// weaker: with mailer.Send already off the request path, elapsed time alone
// still split every branch into four non-overlapping clusters (honeypot
// fastest, then active, then pending, then a brand-new signup slowest,
// separated by sub-millisecond but statistically total gaps — see
// issues/0088.md). A single observation classified branch membership at
// 100% accuracy, and the honeypot branch was the fastest of all, meaning a
// bot could learn from timing alone that its fill had been detected — the
// one thing a honeypot must never reveal.
//
// The fix generalizes #0026's own remedy one step further: every branch
// that reaches the shared 202 now performs exactly the same *synchronous*
// store work — one SuppressionChecker.IsSuppressed call and one
// subscriberStore.FindByEmail call, always, in that order, regardless of
// whether the honeypot fired, the timing gate failed, or the address is
// new/active/pending/unsubscribed/bounced/complained/suppressed — and every
// branch-dependent WRITE (Create, RestartSignup, SetInterests,
// ClaimConfirmationSend/ClaimAlreadySubscribedSend, the mail enqueue itself,
// and the audit record) moves onto a second async queue (mutateQueue,
// mirroring sendQueue) drained after Subscribe has already written the
// response. Two fixed reads plus a channel send is now the entire
// request-path cost for every branch; the honeypot and timing-gate checks
// no longer short-circuit before those reads, they only change what the
// deferred job does once it runs. See processMutateJob.
//
// Options considered and rejected:
//
//   - A sleep-to-pad constant-time floor (delay every response to the
//     slowest branch's worst case). Rejected: it adds real, unconditional
//     latency to every legitimate signup — including the currently-fastest
//     branches — forever, in exchange for hiding a gap that can instead be
//     closed structurally; it also has to be re-tuned by hand as the
//     slowest branch's cost drifts (a new query added to newSignup silently
//     raises the floor everyone else must now pad to), where the
//     equalize-and-defer fix keeps the fixed cost fixed by construction.
//     Under load it also does the opposite of what padding is meant to
//     buy: sleeping ties up the response goroutine (and, if implemented via
//     a worker-limited path, a semaphore slot) for the padding duration on
//     every request, multiplying concurrent in-flight goroutines directly
//     with traffic rather than draining as fast as the backend allows.
//   - Equalizing work alone (same query count, everything still
//     synchronous). Rejected as insufficient on its own: it would still
//     leave the *value* of "how much work restartSignup/newSignup do"
//     coupled to response latency for anyone willing to pad their own
//     query mix to match, and it does nothing to shrink the request-path
//     cost the way deferring the writes does.
//
// What this costs: a client that gets the 202 no longer has any timing or
// status signal for whether its specific submission was accepted, retried,
// or silently dropped by the async worker (queue-full, shutdown-in-flight)
// — but the endpoint already promised nothing about that (CLAUDE.md §9);
// this removes a side channel that was never a supported guarantee. The
// classification (new/active/pending/complained/suppressed/bot) itself is
// still decided synchronously (the two fixed reads), so it does not
// introduce a new race between "decide what to do" and "the state the
// decision was made against" beyond what already existed (a concurrent
// write between FindByEmail and the deferred job's Create/RestartSignup is
// exactly the same race #0026's atomic claims and Create's ErrEmailExists
// handling already covered when that work ran synchronously — moving it a
// few dozen microseconds later onto a different goroutine does not widen
// that window in any way a client can observe or exploit).
package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

const (
	// subscribeConfirmTTL is the confirm token's validity window (PRD §6.3:
	// "7-day confirm token"). Passed as a nominal constant to every
	// mailing.BuildConfirmationEmail call — new signup, restart, AND resend
	// — never as a computed remaining-time-until-expiry. #0028's review
	// found that mailing.formatDuration renders a ragged duration (e.g. a
	// resend computed as "10079 minutes remaining") instead of the clean
	// "7 days" it renders for the nominal constant; passing the constant on
	// every branch avoids that drift entirely rather than special-casing it.
	subscribeConfirmTTL = 7 * 24 * time.Hour

	// subscribeResendCooldown is how often a pending subscriber's
	// confirmation email may be resent (PRD §6.3: "rate-limited to once per
	// hour").
	subscribeResendCooldown = time.Hour

	// subscribeAlreadySubscribedCooldown is how often the "you're already
	// subscribed" email may be sent to the same address. PRD §6.3 doesn't
	// name a value for this branch (only the confirmation resend is given
	// one) — #0026's review (finding 1) required SOME cooldown to close the
	// mail-amplification/timing-oracle finding, and reusing the resend
	// value is a judgment call: it is the only cooldown PRD §6.3 defines
	// for this endpoint, and the two emails share the same abuse shape
	// (repeated submission of one address), so there is no principled
	// reason for them to differ. Flagging for the reviewer in case a
	// different value is wanted.
	subscribeAlreadySubscribedCooldown = time.Hour

	// subscribeTimingGateMinDwell is the minimum time PRD §6.3 requires
	// between the signup form rendering and the submission arriving.
	subscribeTimingGateMinDwell = 2 * time.Second

	// maxSubscribeInterests caps the number of interest slugs a single
	// request may submit. Not itself an acceptance criterion, but a modest
	// defense against a request forcing many synchronous
	// interests.GetBySlug round trips; the taxonomy has twelve seeded rows
	// (PRD §6.1) so this leaves generous headroom for it to grow.
	maxSubscribeInterests = 64
)

// subscriberStore is the behavior SubscribeHandler needs from
// internal/subscribers. Depending on an interface (rather than the concrete
// *subscribers.Store) keeps the handler unit-testable with a fake, matching
// AuthHandler's registrar/authenticator/recoverer pattern.
type subscriberStore interface {
	Create(ctx context.Context, in subscribers.NewSignup, now time.Time) (subscribers.Subscriber, error)
	FindByEmail(ctx context.Context, email string) (subscribers.Subscriber, error)
	RestartSignup(ctx context.Context, id int64, in subscribers.RestartSignupInput, now time.Time) (subscribers.Subscriber, error)
	// ClaimAndEnqueueConfirmation / ClaimAndEnqueueAlreadySubscribed
	// (#0126) replace the pre-#0126 Claim*Send/Release*Claim four-method
	// pair: the claim and the outbound_queue enqueue now happen inside one
	// internal/subscribers transaction, so a claim can no longer be
	// stamped for a send that then fails to be queued — the queue itself
	// retries on its own, so the old release-on-failure compensating
	// action has no job anymore. See this file's package doc comment.
	ClaimAndEnqueueConfirmation(ctx context.Context, sub subscribers.Subscriber, now time.Time, cooldown time.Duration, ttl time.Duration) (bool, error)
	ClaimAndEnqueueAlreadySubscribed(ctx context.Context, sub subscribers.Subscriber, now time.Time, cooldown time.Duration) (bool, error)
	SetInterests(ctx context.Context, subscriberID int64, interestIDs []int64) error
}

// interestLookup is the behavior SubscribeHandler needs from
// internal/interests: resolving a submitted slug to its id (and confirming
// it is currently active) before it is ever passed to SetInterests.
type interestLookup interface {
	GetBySlug(ctx context.Context, slug string) (interests.Interest, error)
}

// SuppressionChecker reports whether an address is on the global
// suppression list PRD §6.2 describes — the `suppressions` table
// (migrations/000012, widened to key on (email, reason) by migrations/000013,
// #0100). Subscribe consults this seam (an interface, not the concrete
// *subscribers.SuppressionStore) so the "suppressed addresses get 202 and
// nothing sent" acceptance criterion is testable via a fake without this
// package importing internal/subscribers; production wires the real
// *subscribers.SuppressionStore at the single call site in
// cmd/opencircuit/main.go. Deliberately reason-blind (#0100 §3): any
// suppressions row of any reason blocks, since "may we mail this address"
// does not depend on why it was suppressed.
type SuppressionChecker interface {
	IsSuppressed(ctx context.Context, email string) (bool, error)
}

// NoSuppressions is the nil-default SuppressionChecker NewSubscribeHandler
// substitutes when its suppression argument is nil, and the stand-in tests
// use directly: every address reports as not suppressed. Production wires
// the real *subscribers.SuppressionStore instead — see
// cmd/opencircuit/main.go. Exported so main.go (outside this package) can
// construct it.
type NoSuppressions struct{}

// IsSuppressed always reports false, nil.
func (NoSuppressions) IsSuppressed(context.Context, string) (bool, error) { return false, nil }

// SubscribeHandler serves POST /api/subscribe (PRD §6.3).
type SubscribeHandler struct {
	subs        subscriberStore
	interests   interestLookup
	suppression SuppressionChecker
	// auditor records subscriber.signup. May be nil in tests that don't
	// assert audit rows.
	auditor *audit.Logger
	baseURL string
	// now is injectable so timestamps are deterministic in tests; defaults
	// to time.Now.
	now func() time.Time
	log *slog.Logger

	// #0126 removed sendQueue/sendWG/h.mailer entirely: mailer.Send used to
	// run on a small worker pool drained from a channel this handler owned
	// (the "mailer.Send off the request path" fix from #0026's review,
	// finding 1 — see the package doc comment for the history). Sending is
	// now internal/mailing.OutboxWorker's job, over internal/outbox's
	// durable queue, wired independently in cmd/opencircuit/main.go — this
	// handler's entire remaining job past the two fixed reads is enqueueing
	// a mutateJob and claiming-and-enqueueing on internal/subscribers,
	// which already commits the outbound_queue row inside its own
	// transaction (subscribers.Store.ClaimAndEnqueueConfirmation /
	// ClaimAndEnqueueAlreadySubscribed / Create). Removing sendQueue does
	// NOT reopen #0026's or #0088's timing-oracle findings: no branch here
	// gains or loses request-path work by this change, because the
	// request-path cost was already just the two fixed reads plus a
	// channel send (mutateQueue, below) before #0126 and still is after.

	// mutateQueue and mutateWG are #0088's async subscription-mutation
	// queue: enqueueMutation hands a mutateJob (everything Subscribe
	// learned synchronously — the honeypot/timing-gate verdict, the two
	// fixed reads' results, the validated email/interests/evidence) to this
	// channel; a fixed pool of worker goroutines (started once, in
	// NewSubscribeHandler) drains it and runs processMutateJob, which is
	// where newSignup/existingSignup/restartSignup — and therefore every
	// Create/RestartSignup/SetInterests/ClaimAndEnqueue*/audit call — now
	// actually happen. See the package doc comment, "The elapsed-time
	// channel — #0088". mutateWG lets tests deterministically wait for
	// in-flight mutations to finish (waitForSends) instead of sleeping, and
	// lets Close (#0081) wait for every queued/in-flight job to finish.
	mutateQueue chan mutateJob
	mutateWG    sync.WaitGroup

	// sendCtx is the parent context every mutate-worker goroutine derives
	// its per-job timeout from (processMutateJob: context.WithTimeout(h.sendCtx,
	// ...)), instead of context.Background(). Named sendCtx from before
	// #0126 removed the send-worker pool that originally justified the
	// name — kept rather than renamed so this diff stays reviewable; it is
	// still exactly what Close (#0081) cancels to interrupt a
	// queued-but-not-yet-started or actively in-flight mutation job at
	// shutdown time, rather than letting it run to completion or time out
	// on its own. Set once in NewSubscribeHandler.
	sendCtx    context.Context
	sendCancel context.CancelFunc

	// closeMu/closed guard Close against a concurrent enqueueMutation: once
	// closed is true, it may not send on mutateQueue (which Close has by
	// then closed — sending on a closed channel panics) and instead falls
	// back to its own drop path, the same observable outcome as every
	// other drop path in this file. See Close's doc comment.
	closeMu sync.Mutex
	closed  bool
}

// mutateQueueCapacity bounds the async subscription-mutation queue. Sized
// generously relative to the per-IP rate limit this endpoint sits behind in
// production (5/min, burst 3, cmd/opencircuit/main.go) — a defense against
// a burst of legitimate concurrent signups outrunning the worker pool for a
// moment, not a capacity expected to fill in steady state. A mutateJob is
// enqueued for EVERY accepted request (bot traffic included, per #0088),
// not only the subset that ends up sending mail.
const mutateQueueCapacity = 256

// mutateWorkerCount is the number of goroutines draining mutateQueue. A
// small fixed pool, not one goroutine per request: bounds how many
// concurrent DB round trips this process makes regardless of how many
// signups arrive at once, which matters because #0026's own rate limit
// (5/min, burst 3 per IP) does nothing to bound TOTAL concurrency across
// many different IPs.
const mutateWorkerCount = 4

// mutateJob carries everything Subscribe learned synchronously — before
// #0088, this same information drove newSignup/existingSignup inline, on
// the request goroutine. Now it is handed to processMutateJob instead,
// which reproduces that exact same dispatch (see existingSignup's switch)
// from a worker goroutine. suppressed/suppErr and existing/findErr are the
// two fixed reads' *raw results*, not yet interpreted — interpretation
// (what suppressed=true means, what findErr wrapping ErrNotFound means)
// happens in processMutateJob, unchanged from how Subscribe used to
// interpret them inline, so moving this off the request path could not
// silently change what any branch decides to do, only when.
type mutateJob struct {
	isBot       bool
	email       string
	interestIDs []int64
	evidence    subscribers.RestartSignupInput
	now         time.Time

	suppressed bool
	suppErr    error
	existing   subscribers.Subscriber
	findErr    error
}

// NewSubscribeHandler constructs a SubscribeHandler and starts its async
// mutation worker pool (see mutateJob and the package doc comment). A nil
// suppression checker defaults to NoSuppressions; a nil logger defaults to
// slog.Default(), matching AuthHandler's nil-tolerance convention.
//
// #0126 removed the mailer and settings parameters this constructor used to
// take: mailer.Send and the physical_address footer lookup both moved to
// internal/mailing.OutboxWorker, which renders at send time from
// outbound_queue.payload (template inputs, not rendered MIME) rather than
// this handler building a mailing.Message inline. Production wires the
// worker separately in cmd/opencircuit/main.go.
func NewSubscribeHandler(
	subs subscriberStore,
	il interestLookup,
	suppression SuppressionChecker,
	auditor *audit.Logger,
	baseURL string,
	logger *slog.Logger,
) *SubscribeHandler {
	if suppression == nil {
		suppression = NoSuppressions{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	sendCtx, sendCancel := context.WithCancel(context.Background())
	h := &SubscribeHandler{
		subs: subs, interests: il, suppression: suppression,
		auditor: auditor, baseURL: baseURL,
		now: time.Now, log: logger,
		mutateQueue: make(chan mutateJob, mutateQueueCapacity),
		sendCtx:     sendCtx,
		sendCancel:  sendCancel,
	}
	for i := 0; i < mutateWorkerCount; i++ {
		go h.runMutateWorker()
	}
	return h
}

// runMutateWorker drains h.mutateQueue until Close closes it — one of
// mutateWorkerCount goroutines started by NewSubscribeHandler, and the
// mechanism that makes repeated handler construction (every test in this
// package) not leak them forever.
func (h *SubscribeHandler) runMutateWorker() {
	for job := range h.mutateQueue {
		h.processMutateJob(job)
	}
}

// mutateJobTimeout bounds a single async mutation job's DB work so a hung
// connection can't pin a worker goroutine forever. Generous relative to
// ordinary local query latency (sub-millisecond, per #0088's own
// measurements) but well short of sendJobTimeout, since this job's own
// slowest step is a handful of local Postgres round trips, not a network
// call to a third party like SES.
const mutateJobTimeout = 10 * time.Second

// processMutateJob is where #0088 moved every branch-dependent write:
// Create, RestartSignup, SetInterests, ClaimAndEnqueueConfirmation/
// ClaimAndEnqueueAlreadySubscribed (which, since #0126, commit the
// outbound_queue enqueue inside the same transaction as the claim itself),
// and the audit record. It
// reproduces exactly the dispatch Subscribe used to run inline — suppressed
// check, then FindByEmail's ErrNotFound/error/found three-way switch — the
// only difference is it interprets job's ALREADY-CAPTURED suppressed/
// suppErr/existing/findErr fields (the two fixed reads Subscribe performed
// synchronously) instead of calling IsSuppressed/FindByEmail itself, and it
// checks job.isBot first so a honeypot-caught or timing-gate-failed
// submission takes no action at all — same as before #0088, just decided
// here instead of by not reaching this code in the first place.
//
// ctx is derived from h.sendCtx, not the (already-returned) HTTP request's
// context, so Close (#0081) cancelling it interrupts a queued-but-not-yet-
// started or in-flight mutation job at shutdown — see the package doc
// comment and Close.
func (h *SubscribeHandler) processMutateJob(job mutateJob) {
	defer h.mutateWG.Done()

	ctx, cancel := context.WithTimeout(h.sendCtx, mutateJobTimeout)
	defer cancel()

	if job.isBot {
		// Honeypot or timing gate. Deliberately no action and no log line
		// distinguishing this from any other silently-dropped branch below
		// — logging it differently would just move the oracle from
		// response timing into log volume/timing, which is exactly the
		// leak #0088 exists to close.
		return
	}

	if subscribers.IsReservedTestEmail(job.email) {
		// #0046's review, finding B: campaign-test+admin-<id>@
		// internal.opencircuitsf.test is the deterministic, now-public
		// test-send recipient anchor admin_campaign_preview.go creates.
		// Its domain is RFC 2606-reserved and guaranteed to never resolve,
		// so mailing ANY address there is a certain hard bounce, and small
		// enumerable admin ids make it trivially probeable. Silently
		// no-op, exactly like isBot/suppressed above — Subscribe already
		// wrote the uniform 202 to the (long-since-returned) HTTP response
		// before this async job ever ran, so this check cannot introduce
		// any observable branch in the endpoint's behavior. Checked
		// regardless of whether a subscribers row already exists at this
		// address (findErr/existing below), since the enumeration risk is
		// in the domain itself, not in any particular row's state.
		return
	}

	if job.suppErr != nil {
		h.log.Error("subscribe: suppression check failed", "err", job.suppErr)
		return
	}
	if job.suppressed {
		return
	}

	switch {
	case errors.Is(job.findErr, subscribers.ErrNotFound):
		h.newSignup(ctx, job.email, job.interestIDs, job.evidence, job.now)
	case job.findErr != nil:
		h.log.Error("subscribe: lookup failed", "err", job.findErr)
	default:
		h.existingSignup(ctx, job.existing, job.interestIDs, job.evidence, job.now)
	}
}

// enqueueMutation hands job to the async mutation worker pool without
// blocking the HTTP response — the #0088 fix's other half of "two fixed
// reads plus a channel send is the entire request-path cost". closeMu is
// held from the closed check through mutateWG.Add(1) and the select, and
// Add is only ever called after that check has observed closed==false, so
// this can never race Close's own mutateWG.Wait()/close(h.mutateQueue).
// sync.WaitGroup's documented misuse is "calls with a positive delta that
// start when the counter is zero must happen before a Wait" — it is not
// enough for an Add to merely be gated by some condition; it must be
// sequenced (happens-before, via a shared lock) ahead of the Wait that
// could observe the counter reaching zero. Here that ordering is real:
// Close's critical section sets closed=true while holding closeMu, and
// only spawns its mutateWG.Wait() goroutine after releasing closeMu. So
// either this call's closeMu.Lock() happens entirely before Close's
// (closed is still false, Add is called, and that Add happens-before
// Close's own Wait() via the mutex handoff plus the go statement's own
// happens-before edge), or entirely after (closed is already true, and Add
// is never called at all — the drop path below runs without ever touching
// mutateWG). #0081's review reproduced a panic against an earlier version
// of the equivalent send-side function (enqueueSend, removed by #0126 along
// with the send-worker pool it fed) that called Add(1) unconditionally at
// entry, before taking closeMu at all — this function was written to the
// same fixed shape from the start.
func (h *SubscribeHandler) enqueueMutation(job mutateJob) {
	h.closeMu.Lock()
	if h.closed {
		h.closeMu.Unlock()
		h.log.Error("subscribe: handler is shutting down, dropping subscription mutation")
		return
	}
	h.mutateWG.Add(1)
	select {
	case h.mutateQueue <- job:
		h.closeMu.Unlock()
	default:
		h.closeMu.Unlock()
		h.log.Error("subscribe: mutation queue full, dropping subscription mutation")
		h.mutateWG.Done()
	}
}

// waitForSends blocks until every mutation job enqueued so far has been
// fully processed by the async mutate workers. Test-only:
// internal/handlers/subscribe_test.go calls this after doSubscribe so
// assertions on subscriber rows / outbound_queue rows are deterministic,
// without an arbitrary sleep, despite #0088's review having moved the
// actual account mutation off the request/response path. Named waitForSends
// from before #0126 removed the separate send-worker pool this used to also
// wait on (mutateWG then sendWG) — kept for the test-file diff's sake; it
// now waits on mutateWG alone, which is sufficient because #0126 folded the
// enqueue itself INTO the same transaction as the mutation
// (subscribers.Store.ClaimAndEnqueueConfirmation and friends), so by the
// time a mutate job's goroutine returns, both the mutation and its
// outbound_queue row (if any) have already committed together.
func (h *SubscribeHandler) waitForSends() {
	h.mutateWG.Wait()
}

// Close implements #0081's graceful-shutdown fix: no new mutation is ever
// attempted after this is called (enqueueMutation's closeMu-guarded check),
// the shared context is cancelled so a job that is queued-but-not-yet-
// started or actively in flight is interrupted rather than left to run to
// completion or its own mutateJobTimeout, and h.mutateQueue is closed so
// every runMutateWorker goroutine exits once it has drained (fixing the
// goroutine leak: before Close existed, NewSubscribeHandler's
// mutateWorkerCount goroutines per construction had no way to ever stop).
//
// Close is bounded by ctx and idempotent (a second call is a no-op) and
// safe to call concurrently with in-flight Subscribe requests still calling
// enqueueMutation. It returns ctx.Err() if the bound is exceeded before
// every queued/in-flight job finishes being processed.
//
// cmd/opencircuit/main.go's mountAndServe calls this from its
// SIGTERM/SIGINT handler, after http.Server.Shutdown — that ordering
// guarantees no request goroutine can still be inside enqueueMutation ONLY
// when Shutdown returns nil (every in-flight handler, including Subscribe,
// genuinely returned before Close is called). If Shutdown instead returns
// context.DeadlineExceeded (its own budget consumed by a still-running
// handler — e.g. a long-lived SSE stream, which does not block on a
// mutation but does keep Shutdown from returning early — see #0087), that
// guarantee does not hold: a request goroutine may still be executing
// concurrently with this call. That is harmless, not incidental:
// enqueueMutation's closeMu-guarded check still refuses a send after closed
// flips true, and Close (below) holds that same lock while setting
// closed=true, so the two can safely overlap regardless of whether Shutdown
// finished draining first. #0045's campaign send worker needs the same
// close-then-bounded-wait shape; reuse this pattern (a cancellable shared
// context plus a closed work queue plus a WaitGroup) rather than inventing
// a second one — and #0126's OutboxWorker.Stop follows it too, for the
// worker that took over the actual mail send this handler used to run.
//
// #0126 removed the send-worker pool (sendQueue/sendWG) this used to also
// close and wait on alongside mutateQueue/mutateWG — see the struct's field
// comments for why that removal does not reopen #0081's goroutine-leak
// fix: mutateQueue/mutateWG is the only pool left, and it is closed/waited
// exactly as before.
func (h *SubscribeHandler) Close(ctx context.Context) error {
	h.closeMu.Lock()
	if h.closed {
		h.closeMu.Unlock()
		return nil
	}
	h.closed = true
	h.sendCancel()
	close(h.mutateQueue)
	h.closeMu.Unlock()

	done := make(chan struct{})
	go func() {
		h.mutateWG.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// subscribeRequest is the POST /api/subscribe body. This is the wire
// contract #0029's SubscribeForm component must produce:
//
//   - email: the address to subscribe.
//   - interests: zero or more taxonomy slugs (PRD §6.1); an absent or empty
//     array is valid and selects general-announcements-only.
//   - website: the honeypot field. A real form never shows or fills this
//     input; any non-empty value is treated as a bot.
//   - rendered_at: unix milliseconds, client-captured at the moment the
//     form first rendered. The server requires at least
//     subscribeTimingGateMinDwell to have elapsed before the submission is
//     accepted as human.
//   - utm_source / utm_medium / utm_campaign: attribution captured from the
//     landing URL, per PRD §6.3's "SPA stores utm_* in sessionStorage on
//     first paint".
type subscribeRequest struct {
	Email       string   `json:"email"`
	Interests   []string `json:"interests"`
	Website     string   `json:"website"`
	RenderedAt  int64    `json:"rendered_at"`
	UTMSource   string   `json:"utm_source"`
	UTMMedium   string   `json:"utm_medium"`
	UTMCampaign string   `json:"utm_campaign"`
}

// subscribeResponse is the uniform 202 body every branch of Subscribe
// returns, verbatim from PRD §6.3.
type subscribeResponse struct {
	Message string `json:"message"`
}

// subscribeUniformBody is the single value ever passed to writeJSON for
// this endpoint's success path. Using one package-level value (rather than
// constructing an equivalent-looking literal at each call site) makes byte
// divergence across branches structurally impossible rather than merely
// untested.
var subscribeUniformBody = subscribeResponse{Message: "Check your email to confirm."}

// writeSubscribeUniform202 is the ONLY place this file writes a 202. Every
// branch of Subscribe that must not be distinguishable from any other calls
// this and nothing else.
func writeSubscribeUniform202(w http.ResponseWriter) {
	writeJSON(w, http.StatusAccepted, subscribeUniformBody)
}

// errUnknownInterest is returned by resolveInterestIDs when a submitted
// slug does not resolve to a currently-active interest.
var errUnknownInterest = errors.New("handlers: unknown interest slug")

// Subscribe handles POST /api/subscribe. See the package doc comment above
// for the uniform-202 security property this handler exists to preserve,
// and "The elapsed-time channel — #0088" specifically for why this function
// no longer branches on the honeypot or timing gate before doing the same
// work every other request does.
func (h *SubscribeHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	var req subscribeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	now := h.now()

	// #0088: isBot records the honeypot/timing-gate verdict but does NOT
	// return early. Before #0088, a honeypot fill or a failed timing gate
	// short-circuited straight to the uniform 202 without doing any of the
	// validation or store work every other branch does — which made this
	// the cheapest, fastest, and therefore most timing-distinguishable
	// branch of all four measured, precisely the property a honeypot must
	// never reveal. isBot is consulted only inside processMutateJob, which
	// decides to do nothing at all for a bot-caught submission — same
	// externally-observable outcome as before, just decided after the same
	// fixed-cost synchronous work every other branch also pays for.
	isBot := req.Website != "" || !passesTimingGate(req.RenderedAt, now)

	// Structural request-shape validation. Per the package doc comment,
	// none of this depends on whether the submitted email is already on
	// the list, so answering 400 here (unlike everything below) cannot
	// leak account state — and #0088 runs it unconditionally, honeypot or
	// not, so that a bot's synchronous cost matches a human's regardless
	// of how many interest slugs either one submits (resolveInterestIDs is
	// the one validation step whose cost scales with the request rather
	// than being O(1), so it must not be skippable by isBot).
	email := strings.TrimSpace(req.Email)
	if !validEmailSyntax(email) {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if isDisposableDomain(email) {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}

	if len(req.Interests) > maxSubscribeInterests {
		writeError(w, http.StatusBadRequest, "too many interests")
		return
	}
	interestIDs, err := h.resolveInterestIDs(r.Context(), req.Interests)
	switch {
	case errors.Is(err, errUnknownInterest):
		writeError(w, http.StatusBadRequest, "unknown interest")
		return
	case err != nil:
		h.log.Error("subscribe: resolving interests failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Every check from here on MUST end in writeSubscribeUniform202,
	// including every error path: none of the remaining work can be
	// allowed to produce an observably different response, or the endpoint
	// becomes an email-enumeration oracle.
	ctx := r.Context()

	// #0088's fixed cost: exactly these two reads, always, for every
	// accepted request regardless of branch — see the package doc comment.
	// Their results are not interpreted here; that happens in
	// processMutateJob, unchanged from how this function used to interpret
	// them inline before #0088 moved the interpretation off the request
	// path along with everything it used to drive.
	suppressed, suppErr := h.suppression.IsSuppressed(ctx, email)
	existing, findErr := h.subs.FindByEmail(ctx, email)

	evidence := subscribers.RestartSignupInput{
		SignupIP:        clientIP(r),
		SignupUserAgent: r.UserAgent(),
		UTMSource:       req.UTMSource,
		UTMMedium:       req.UTMMedium,
		UTMCampaign:     req.UTMCampaign,
		ConfirmTTL:      subscribeConfirmTTL,
	}

	h.enqueueMutation(mutateJob{
		isBot:       isBot,
		email:       email,
		interestIDs: interestIDs,
		evidence:    evidence,
		now:         now,
		suppressed:  suppressed,
		suppErr:     suppErr,
		existing:    existing,
		findErr:     findErr,
	})

	writeSubscribeUniform202(w)
}

// newSignup handles a genuinely new address: FindByEmail returned
// ErrNotFound.
func (h *SubscribeHandler) newSignup(ctx context.Context, email string, interestIDs []int64, evidence subscribers.RestartSignupInput, now time.Time) {
	sub, err := h.subs.Create(ctx, subscribers.NewSignup{
		Email:           email,
		SignupIP:        evidence.SignupIP,
		SignupUserAgent: evidence.SignupUserAgent,
		UTMSource:       evidence.UTMSource,
		UTMMedium:       evidence.UTMMedium,
		UTMCampaign:     evidence.UTMCampaign,
		ConfirmTTL:      evidence.ConfirmTTL,
	}, now)
	if err != nil {
		if errors.Is(err, subscribers.ErrEmailExists) {
			// Lost a race with a concurrent signup for the same address
			// between FindByEmail and Create (e.g. a double-submit or two
			// tabs). The request that won the race already owns sending a
			// confirmation; do nothing further here rather than risk a
			// second email or a second token.
			return
		}
		h.log.Error("subscribe: creating subscriber failed", "err", err)
		return
	}

	if err := h.subs.SetInterests(ctx, sub.ID, interestIDs); err != nil {
		h.log.Error("subscribe: setting interests failed", "subscriber_id", sub.ID, "err", err)
		// Continue anyway: the subscriber row and confirm token are valid,
		// and losing the interest selections is recoverable later via the
		// preference center (#0031) — but losing the ability to confirm at
		// all would not be.
	}

	// #0126: NOT h.sendConfirmation(ctx, sub, now) here. Create's own
	// transaction (internal/subscribers.Store.Create) already claimed
	// confirm_sent_at and enqueued the confirmation atomically with the
	// INSERT — that is the "a committed signup can never have an unsent
	// confirmation" property this issue exists to establish. Calling
	// sendConfirmation here would just find confirm_sent_at already
	// stamped (cooldown active from the claim Create just made) and
	// no-op — harmless, but redundant and confusing to read.
	h.auditSignup(ctx, sub, evidence.SignupIP, "new")
}

// existingSignup handles every branch where FindByEmail found a row,
// dispatching on status per PRD §6.3's table.
func (h *SubscribeHandler) existingSignup(ctx context.Context, existing subscribers.Subscriber, interestIDs []int64, evidence subscribers.RestartSignupInput, now time.Time) {
	switch existing.Status {
	case subscribers.StatusActive:
		h.sendAlreadySubscribed(ctx, existing, now)

	case subscribers.StatusPending:
		// Resend the existing confirm link, rate-limited to once per hour
		// (PRD §6.3). sendConfirmation's atomic claim IS the rate limit —
		// no separate cooldown check is needed here, unlike the pre-review
		// version of this handler (see #0026's review, finding 3).
		h.sendConfirmation(ctx, existing, now)

	case subscribers.StatusUnsubscribed:
		h.restartSignup(ctx, existing, interestIDs, evidence, now)

	case subscribers.StatusBounced:
		// Not enumerated by #0026's acceptance-criteria table (only
		// active/pending/unsubscribed/complained are). Deliberately
		// conservative default rather than silence-by-omission: a bounced
		// address had a real delivery problem and this handler cannot
		// distinguish a stale hard bounce from a transient soft one, so it
		// takes the same "route back through double opt-in" path as
		// unsubscribed instead of guessing. #0033/#0038 (suppression list,
		// SES bounce/complaint ingestion) will refine this once bounce
		// classification exists; until then, requiring a fresh confirm
		// click before any mail resumes is the safe direction to err in.
		// See this issue's Gotchas.
		h.restartSignup(ctx, existing, interestIDs, evidence, now)

	case subscribers.StatusComplained:
		// CLAUDE.md §9 / PRD notes: complained never auto-resubscribes.
		// No store call, no mailer call — falls straight through to the
		// same uniform 202 every other branch produces.

	default:
		h.log.Error("subscribe: subscriber in unrecognized status", "subscriber_id", existing.ID, "status", existing.Status)
	}
}

// restartSignup handles the "unsubscribed → treat as new signup; fresh
// confirm token" branch (and, per existingSignup's comment, bounced too).
func (h *SubscribeHandler) restartSignup(ctx context.Context, existing subscribers.Subscriber, interestIDs []int64, evidence subscribers.RestartSignupInput, now time.Time) {
	sub, err := h.subs.RestartSignup(ctx, existing.ID, evidence, now)
	if err != nil {
		h.log.Error("subscribe: restarting signup failed", "subscriber_id", existing.ID, "err", err)
		return
	}
	if sub.Status != subscribers.StatusPending {
		// RestartSignup's statusLockedFromNonAdmin guard fired: a complaint
		// landed between this handler's FindByEmail read and the store's
		// UPDATE. Treat exactly like the complained branch — nothing sent.
		return
	}

	if err := h.subs.SetInterests(ctx, sub.ID, interestIDs); err != nil {
		h.log.Error("subscribe: setting interests failed", "subscriber_id", sub.ID, "err", err)
	}

	h.sendConfirmation(ctx, sub, now)
	h.auditSignup(ctx, sub, evidence.SignupIP, "restarted")
}

// sendConfirmation claims and enqueues the double opt-in confirmation email
// for a subscriber with a live confirm_token — a restarted signup, or a
// resend to an already-pending one (a brand-new signup's own confirmation
// is already claimed and enqueued inside Create's own transaction — see
// newSignup — so this is never called for that case). Both remaining call
// sites are identical from this point on, which is deliberate: #0026's
// review (finding 3) traced a duplicate-send bug to the resend branch
// having its OWN, separately-timed cooldown check instead of sharing this
// one atomic claim.
//
// The claim-and-enqueue (internal/subscribers.Store.
// ClaimAndEnqueueConfirmation, #0126) is synchronous, fast, and
// non-network — it is what enforces PRD §6.3's once-per-hour resend limit
// AND what makes concurrent double-submits of the same address send at
// most once, and it commits the outbound_queue row in the SAME transaction
// as the claim, so a claim can no longer be stamped for a send that then
// fails to be queued. Only a successful claim enqueues an actual send;
// losing the claim (cooldown active, or a concurrent request claimed
// first) is not an error, just this request declining to send. Rendering
// and the actual mailer call happen later, off this goroutine entirely, in
// internal/mailing.OutboxWorker.
func (h *SubscribeHandler) sendConfirmation(ctx context.Context, sub subscribers.Subscriber, now time.Time) {
	if sub.ConfirmToken == nil {
		h.log.Error("subscribe: subscriber has no confirm token", "subscriber_id", sub.ID)
		return
	}
	if _, err := h.subs.ClaimAndEnqueueConfirmation(ctx, sub, now, subscribeResendCooldown, subscribeConfirmTTL); err != nil {
		h.log.Error("subscribe: claiming/enqueueing confirmation send failed", "subscriber_id", sub.ID, "err", err)
	}
	// claimed=false is not an error — cooldown active, or a concurrent
	// request already claimed this send — just this request declining to
	// send, same as before #0126.
}

// sendAlreadySubscribed handles the active branch: notify the submitter the
// address is already on the list, with the preference-center link. No
// status mutation — nothing about the subscriber's own state changes — but
// the send itself is claimed exactly like sendConfirmation, gated by its
// own cooldown column (already_subscribed_sent_at). #0026's review (finding
// 1) measured 20 sequential submits of one active subscriber's address
// producing 20 emails to that person before this claim existed: an
// unauthenticated mail-amplification vector, and half of a two-probe
// enumeration oracle once mailer.Send was also off the request path but
// this branch still sent unconditionally.
func (h *SubscribeHandler) sendAlreadySubscribed(ctx context.Context, existing subscribers.Subscriber, now time.Time) {
	if _, err := h.subs.ClaimAndEnqueueAlreadySubscribed(ctx, existing, now, subscribeAlreadySubscribedCooldown); err != nil {
		h.log.Error("subscribe: claiming/enqueueing already-subscribed send failed", "subscriber_id", existing.ID, "err", err)
	}
}

// auditSignup records subscriber.signup. kind ("new" or "restarted")
// distinguishes the two audit-worthy branches in the metadata rather than
// via separate action constants, since both represent the same event from
// an audit-trail perspective: a person consented and a confirm email went
// out. actor is NULL — signup is a pre-auth, anonymous action, matching
// ActionAccountRegistrationStarted's convention.
func (h *SubscribeHandler) auditSignup(ctx context.Context, sub subscribers.Subscriber, ip, kind string) {
	if h.auditor == nil {
		return
	}
	h.auditor.Record(ctx, audit.Entry{
		Action:     audit.ActionSubscriberSignup,
		TargetType: audit.TargetSubscriber,
		TargetID:   &sub.ID,
		Metadata:   map[string]any{"kind": kind},
		IP:         ip,
	})
}

// resolveInterestIDs resolves each submitted slug to its interest id,
// rejecting the whole request with errUnknownInterest if any slug does not
// resolve to a currently-active interest (PRD §6.1: "Interest slugs
// validated against active interests; unknown slugs rejected"). A nil or
// empty slugs returns (nil, nil) — a subscriber with zero interests is
// valid and expected, never an error.
func (h *SubscribeHandler) resolveInterestIDs(ctx context.Context, slugs []string) ([]int64, error) {
	if len(slugs) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(slugs))
	for _, slug := range slugs {
		it, err := h.interests.GetBySlug(ctx, slug)
		switch {
		case errors.Is(err, interests.ErrNotFound):
			return nil, errUnknownInterest
		case err != nil:
			return nil, err
		case !it.Active:
			return nil, errUnknownInterest
		}
		ids = append(ids, it.ID)
	}
	return ids, nil
}

// passesTimingGate reports whether renderedAtMS — a client-declared
// unix-millisecond timestamp of when the signup form first rendered, per
// subscribeRequest's documented wire contract — is at least
// subscribeTimingGateMinDwell before now. A missing, zero, or future-dated
// value fails the gate: a real browser always sends a positive value
// somewhat in the past.
//
// This trusts a client-declared time, which a sophisticated bot can fake
// trivially — that is expected. PRD §6.3 describes the gate itself as
// "optional; cheap and effective" against the common case of a bot that
// fills and submits a form with no rendering delay at all, not as a hard
// security boundary; the honeypot and the per-IP rate limiter are what
// carry the real weight.
func passesTimingGate(renderedAtMS int64, now time.Time) bool {
	if renderedAtMS <= 0 {
		return false
	}
	renderedAt := time.UnixMilli(renderedAtMS)
	if renderedAt.After(now) {
		return false
	}
	return now.Sub(renderedAt) >= subscribeTimingGateMinDwell
}

// validEmailSyntax reports whether email is a syntactically valid, single,
// undecorated address (no display name, no comments).
//
// An earlier version of this function rejected any non-ASCII byte outright,
// to sidestep a real disagreement between Go's strings.ToLower (full
// Unicode case folding) and Postgres's lower() (locale-dependent) on how to
// lowercase certain runes — normalizeEmail used to run in Go, so an address
// that passed this check but disagreed with the database's own lower()
// could trip the subscribers_email_normalized CHECK and turn into a 500
// from an endpoint that must always answer 202. #0026's review found that
// restriction too broad: the actual disagreement was narrow (one
// titlecase-digraph codepoint class, ǅ U+01C5, across a wide battery of
// case-folding hazards), while RFC 6531 (SMTPUTF8) local parts and
// internationalized domains are legitimate addresses a San Francisco
// community group's subscribers can plausibly have. The narrower fix,
// applied in internal/subscribers (Create/RestartSignup/FindByEmail), is to
// stop normalizing in Go at all — SQL's lower(trim($1)) is now the only
// place normalization happens, so this function no longer needs to predict
// it and the ASCII restriction is gone. net/mail.ParseAddress already
// accepts UTF-8 local parts and domains (verified: RFC 6532 aware), so
// removing the byte-range check is the entire change.
func validEmailSyntax(email string) bool {
	if email == "" || len(email) > 254 {
		return false
	}

	addr, err := mail.ParseAddress(email)
	if err != nil {
		return false
	}
	if addr.Address != email {
		// mail.ParseAddress accepts "Display Name <a@b.com>" and similar
		// decorated forms; a signup field must be exactly the address.
		return false
	}

	at := strings.LastIndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return false
	}
	domain := email[at+1:]
	if !strings.Contains(domain, ".") {
		return false
	}
	return true
}

// disposableEmailDomains is a small, deliberately non-exhaustive blocklist
// of well-known disposable/temporary-address providers (PRD §6.3: "reject
// disposable-domain list"). Not a substitute for a maintained third-party
// list — #0026's scope is "apply *a* blocklist", not "build the
// authoritative one" — but enough to stop the common, low-effort case.
var disposableEmailDomains = map[string]bool{
	"mailinator.com":     true,
	"guerrillamail.com":  true,
	"guerrillamail.info": true,
	"10minutemail.com":   true,
	"tempmail.com":       true,
	"temp-mail.org":      true,
	"throwawaymail.com":  true,
	"yopmail.com":        true,
	"trashmail.com":      true,
	"getnada.com":        true,
	"sharklasers.com":    true,
	"dispostable.com":    true,
	"fakeinbox.com":      true,
	"maildrop.cc":        true,
	"mintemail.com":      true,
	"mailnesia.com":      true,
	"spamgourmet.com":    true,
	"moakt.com":          true,
	"emailondeck.com":    true,
	"discard.email":      true,
}

// isDisposableDomain reports whether email's domain is on
// disposableEmailDomains. Only meaningful after validEmailSyntax has
// confirmed exactly one "@". strings.ToLower's Unicode case-folding hazard
// (see internal/subscribers' package doc comment) doesn't apply here: every
// key in disposableEmailDomains is a plain ASCII hostname, so a non-ASCII
// domain simply never matches — a false negative (misses a hypothetical
// IDN disposable provider), never a false positive, and not a bypass of
// anything this blocklist is relied on to enforce.
func isDisposableDomain(email string) bool {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return false
	}
	return disposableEmailDomains[strings.ToLower(email[at+1:])]
}
