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
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
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
	ClaimConfirmationSend(ctx context.Context, id int64, now time.Time, cooldown time.Duration) (bool, error)
	ReleaseConfirmationClaim(ctx context.Context, id int64, now time.Time) error
	ClaimAlreadySubscribedSend(ctx context.Context, id int64, now time.Time, cooldown time.Duration) (bool, error)
	ReleaseAlreadySubscribedClaim(ctx context.Context, id int64, now time.Time) error
	SetInterests(ctx context.Context, subscriberID int64, interestIDs []int64) error
}

// interestLookup is the behavior SubscribeHandler needs from
// internal/interests: resolving a submitted slug to its id (and confirming
// it is currently active) before it is ever passed to SetInterests.
type interestLookup interface {
	GetBySlug(ctx context.Context, slug string) (interests.Interest, error)
}

// physicalAddressReader is the behavior SubscribeHandler needs to fill in
// the confirmation/already-subscribed email's CAN-SPAM footer address.
// *auth.Store satisfies this via GetSetting; depending on an interface here
// avoids importing internal/auth's concrete type for a single method.
type physicalAddressReader interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// SuppressionChecker reports whether an address is on the global
// suppression list PRD §6.2 describes — the `suppressions` table added by
// #0033, which has not landed yet (#0026 depends on #0025/#0027, not
// #0033; the two issues are independent branches of the tracker). Subscribe
// consults this seam so the "suppressed addresses get 202 and nothing
// sent" acceptance criterion is real and tested now, via a fake in tests,
// and #0033 only has to provide a real implementation — swapped in at the
// single call site in cmd/opencircuit/main.go — for production enforcement
// to switch on. See this issue's Gotchas for the reasoning.
type SuppressionChecker interface {
	IsSuppressed(ctx context.Context, email string) (bool, error)
}

// NoSuppressions is the SuppressionChecker wired in cmd/opencircuit/main.go
// until #0033 lands: every address reports as not suppressed. Exported so
// main.go (outside this package) can construct it.
type NoSuppressions struct{}

// IsSuppressed always reports false, nil.
func (NoSuppressions) IsSuppressed(context.Context, string) (bool, error) { return false, nil }

// SubscribeHandler serves POST /api/subscribe (PRD §6.3).
type SubscribeHandler struct {
	subs        subscriberStore
	interests   interestLookup
	mailer      mailing.Mailer
	suppression SuppressionChecker
	// settings supplies the physical_address setting for the CAN-SPAM
	// footer. May be nil (tests that don't care about the footer address);
	// a nil settings or any GetSetting error is treated as an empty
	// address, matching mailing.BuildConfirmationEmail's documented
	// handling of "" — never fabricated, never fatal to the signup.
	settings physicalAddressReader
	// auditor records subscriber.signup. May be nil in tests that don't
	// assert audit rows.
	auditor *audit.Logger
	baseURL string
	// now is injectable so timestamps are deterministic in tests; defaults
	// to time.Now.
	now func() time.Time
	log *slog.Logger

	// sendQueue and sendWG implement the "mailer.Send off the request path"
	// fix from #0026's review (finding 1) — see the package doc comment.
	// enqueueSend hands a built mailing.Message to this channel; a fixed
	// pool of worker goroutines (started once, in NewSubscribeHandler)
	// drains it and calls h.mailer.Send on sendCtx (see below), so no HTTP
	// response ever waits on a network call. sendWG lets tests
	// deterministically wait for in-flight async sends to finish
	// (waitForSends) instead of sleeping, and lets Close (#0081) wait for
	// every queued/in-flight job to finish being processed.
	sendQueue chan sendJob
	sendWG    sync.WaitGroup

	// sendCtx is the parent context every worker derives its per-job
	// timeout from (processSendJob: context.WithTimeout(h.sendCtx, ...)),
	// instead of context.Background(). sendCancel cancels it, which Close
	// (#0081) uses to interrupt any send that is queued-but-not-yet-started
	// or actively in flight at shutdown time, rather than letting it run to
	// completion or timing out on its own. Set once in NewSubscribeHandler.
	sendCtx    context.Context
	sendCancel context.CancelFunc

	// closeMu/closed guard Close against a concurrent enqueueSend: once
	// closed is true, enqueueSend must not attempt to send on sendQueue
	// (which Close has by then closed — sending on a closed channel
	// panics) and instead releases the claim immediately, the same
	// observable outcome as every other drop path in this file. See
	// Close's doc comment.
	closeMu sync.Mutex
	closed  bool
}

// sendQueueCapacity bounds the async send queue. Sized generously relative
// to the per-IP rate limit this endpoint sits behind in production
// (5/min, burst 3, cmd/opencircuit/main.go) — the queue existing at all is
// a defense against a burst of legitimate concurrent signups outrunning the
// worker pool for a moment, not a capacity this project expects to fill in
// steady state.
const sendQueueCapacity = 256

// sendWorkerCount is the number of goroutines draining sendQueue. A small
// fixed pool, not one goroutine per request: bounds how many concurrent SES
// calls this process makes regardless of how many signups arrive at once,
// which matters because #0026's own rate limit (5/min, burst 3 per IP) does
// nothing to bound TOTAL concurrency across many different IPs.
const sendWorkerCount = 4

// sendJob is one outbound email queued by sendConfirmation or
// sendAlreadySubscribed: the already-built message, plus the compensating
// action to run if the send fails (releasing the atomic claim that was
// taken before the job was enqueued, so a later request can retry).
type sendJob struct {
	subscriberID int64
	label        string // "confirmation" | "already-subscribed" — for logs only
	msg          mailing.Message
	release      func(ctx context.Context) error
}

// NewSubscribeHandler constructs a SubscribeHandler and starts its async
// send worker pool (see sendJob and the package doc comment). A nil
// suppression checker defaults to NoSuppressions; a nil logger defaults to
// slog.Default(), matching AuthHandler's nil-tolerance convention.
func NewSubscribeHandler(
	subs subscriberStore,
	il interestLookup,
	mailer mailing.Mailer,
	suppression SuppressionChecker,
	settings physicalAddressReader,
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
		subs: subs, interests: il, mailer: mailer, suppression: suppression,
		settings: settings, auditor: auditor, baseURL: baseURL,
		now: time.Now, log: logger,
		sendQueue:  make(chan sendJob, sendQueueCapacity),
		sendCtx:    sendCtx,
		sendCancel: sendCancel,
	}
	for i := 0; i < sendWorkerCount; i++ {
		go h.runSendWorker()
	}
	return h
}

// sendJobTimeout bounds a single async send attempt so a hung network call
// can't pin a worker goroutine forever.
const sendJobTimeout = 30 * time.Second

// runSendWorker drains h.sendQueue until Close (#0081) closes it, at which
// point the loop exits and this goroutine returns — one of sendWorkerCount
// goroutines started by NewSubscribeHandler, and the mechanism that makes
// repeated handler construction (every test in this package, every
// #0045-shaped future caller) not leak them forever.
func (h *SubscribeHandler) runSendWorker() {
	for job := range h.sendQueue {
		h.processSendJob(job)
	}
}

// processSendJob performs one queued send on a context detached from any
// HTTP request (the request that enqueued this job has already returned)
// but derived from h.sendCtx, not context.Background() — so Close (#0081)
// cancelling h.sendCtx interrupts this call rather than letting it run for
// the full sendJobTimeout, or to completion, during shutdown. A failed send
// — including one that failed because sendCtx was cancelled — is logged,
// never silently dropped, and its claim released so a subsequent legitimate
// request can retry.
func (h *SubscribeHandler) processSendJob(job sendJob) {
	defer h.sendWG.Done()

	ctx, cancel := context.WithTimeout(h.sendCtx, sendJobTimeout)
	defer cancel()

	if _, err := h.mailer.Send(ctx, job.msg); err != nil {
		h.log.Error("subscribe: async send failed", "subscriber_id", job.subscriberID, "kind", job.label, "err", err)
		h.releaseSendClaim(job)
		return
	}
}

// releaseSendClaim runs job.release on its own short-lived, detached
// context — never the (possibly already-expired) context the failed send
// itself used — so a release attempt is not defeated by the same timeout
// that may have doomed the send.
func (h *SubscribeHandler) releaseSendClaim(job sendJob) {
	if job.release == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := job.release(ctx); err != nil {
		h.log.Error("subscribe: releasing send claim after failed send failed", "subscriber_id", job.subscriberID, "kind", job.label, "err", err)
	}
}

// enqueueSend hands job to the async worker pool without blocking the HTTP
// response. If the queue is saturated (sendQueueCapacity jobs already
// waiting — see that constant's doc comment on why this is not expected in
// steady state) OR the handler is shutting down (#0081's Close has already
// been called), it does NOT block and does NOT send on sendQueue: blocking
// here would silently reopen the exact request-latency channel #0026's
// review closed, and sending on sendQueue after Close has closed it would
// panic. Instead it logs loudly and releases the claim immediately, exactly
// as if the send itself had failed, so the drop is both observable (in
// logs) and retryable (the claim is released, so the next legitimate
// request tries again — immediately, not after subscribeResendCooldown).
//
// closeMu is held from the closed check through sendWG.Add(1) and the
// select, and Add is only ever called AFTER that check has observed
// closed==false, so this can never race Close's own
// sendWG.Wait()/close(h.sendQueue). sync.WaitGroup's documented misuse is
// "calls with a positive delta that start when the counter is zero must
// happen before a Wait" — it is not enough for an Add to merely be gated by
// some condition; it must be sequenced (happens-before, via a shared lock or
// similar) ahead of the Wait that could observe the counter reaching zero.
// Here that ordering is real: Close's critical section sets closed=true
// while holding closeMu, and only spawns its sendWG.Wait() goroutine after
// releasing closeMu. So either this call's closeMu.Lock() happens entirely
// before Close's (closed is still false, Add is called, and that Add
// happens-before Close's own Lock()/closed=true/Unlock()/go-statement/Wait()
// via the mutex handoff plus the go statement's own happens-before edge —
// i.e. strictly before Close ever calls Wait), or entirely after (closed is
// already true, and Add is never called at all — the drop-and-release path
// below runs without ever touching sendWG, since a job that was never
// queued was never "in flight" from Close's point of view and needs no
// WaitGroup accounting). #0081's review reproduced a panic at ~5,500 rounds
// against an earlier version of this function that called Add(1)
// unconditionally at entry, before taking closeMu at all: that Add had no
// happens-before relationship to Close's Wait whatsoever. A version that
// gated the closed-check's Add/Done pair under closeMu but still called Add
// before checking closed also failed at ~5,000 rounds, for a subtler
// reason: the intermediate Add(1)+Done() pair on the closed-drop path still
// raced Close's Wait() call itself, since Wait() is not called while
// closeMu is held — only the DECISION to add needs to happen-before Wait,
// which requires Add to happen only in the branch that runs strictly before
// Close's critical section, not merely "under the same mutex at some
// point". This version's Add is called only in the branch that provably
// precedes Close, so no drop path (closed or queue-full) ever calls
// Add/Done for a job Close could already be waiting on.
func (h *SubscribeHandler) enqueueSend(job sendJob) {
	h.closeMu.Lock()
	if h.closed {
		h.closeMu.Unlock()
		h.log.Error("subscribe: handler is shutting down, dropping send", "subscriber_id", job.subscriberID, "kind", job.label)
		h.releaseSendClaim(job)
		return
	}
	h.sendWG.Add(1)
	select {
	case h.sendQueue <- job:
		h.closeMu.Unlock()
	default:
		h.closeMu.Unlock()
		h.log.Error("subscribe: send queue full, dropping send", "subscriber_id", job.subscriberID, "kind", job.label)
		h.releaseSendClaim(job)
		h.sendWG.Done()
	}
}

// waitForSends blocks until every send job enqueued so far has been fully
// processed (sent, failed-and-released, or dropped-and-released) by the
// async workers. Test-only: internal/handlers/subscribe_test.go calls this
// after doSubscribe so assertions on mailer state or subscriber rows are
// deterministic, without an arbitrary sleep, despite #0026's review moving
// the actual send off the request/response path.
func (h *SubscribeHandler) waitForSends() {
	h.sendWG.Wait()
}

// Close implements #0081's graceful-shutdown fix: no new send is ever
// attempted after this is called (enqueueSend's closeMu-guarded check), the
// shared send context is cancelled so a job that is queued-but-not-yet-
// started or actively in flight is interrupted rather than left to run to
// completion or its own sendJobTimeout, and h.sendQueue is closed so every
// runSendWorker goroutine exits once it has drained (fixing the goroutine
// leak: before Close existed, NewSubscribeHandler's sendWorkerCount
// goroutines per construction had no way to ever stop).
//
// Every interrupted send takes processSendJob's ordinary failed-send path —
// logged, claim released via releaseSendClaim's own short-lived, detached
// 5s context (deliberately NOT derived from sendCtx, so a release attempt
// is never itself defeated by the same cancellation that doomed the send it
// is compensating for). That is the actual fix for defect 1: a visitor who
// saw the 202 can retry immediately after a SIGTERM-driven shutdown,
// instead of being silenced for subscribeResendCooldown by a claim nobody
// ever released.
//
// Close is bounded by ctx and idempotent (a second call is a no-op) and
// safe to call concurrently with in-flight Subscribe requests still calling
// enqueueSend. It returns ctx.Err() if the bound is exceeded before every
// queued/in-flight job finishes being processed — expected only if the
// mailer itself ignores context cancellation, since sendCtx's cancellation
// otherwise makes every remaining job fail (and release) almost
// immediately rather than running out its full timeout.
//
// cmd/opencircuit/main.go's mountAndServe calls this from its
// SIGTERM/SIGINT handler, after http.Server.Shutdown has stopped accepting
// new connections and drained in-flight ones — Subscribe itself never
// blocks on a send, so that step is fast and by the time it returns no
// request goroutine can still be inside enqueueSend. #0045's campaign send
// worker needs the same close-then-bounded-wait shape; reuse this pattern
// (a cancellable shared context plus a closed work queue plus a WaitGroup)
// rather than inventing a second one.
func (h *SubscribeHandler) Close(ctx context.Context) error {
	h.closeMu.Lock()
	if h.closed {
		h.closeMu.Unlock()
		return nil
	}
	h.closed = true
	h.sendCancel()
	close(h.sendQueue)
	h.closeMu.Unlock()

	done := make(chan struct{})
	go func() {
		h.sendWG.Wait()
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
// for the uniform-202 security property this handler exists to preserve.
func (h *SubscribeHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	var req subscribeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Honeypot: a real visitor never sees or fills this field. Bots fill
	// every input. Discard silently behind the same uniform response used
	// for success, so a bot probing the endpoint can't tell "caught" from
	// "accepted".
	if req.Website != "" {
		writeSubscribeUniform202(w)
		return
	}

	now := h.now()

	// Timing gate: reject a submission that arrived faster than a human
	// could plausibly have read the form and clicked submit. Also silent —
	// same reasoning as the honeypot.
	if !passesTimingGate(req.RenderedAt, now) {
		writeSubscribeUniform202(w)
		return
	}

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

	suppressed, err := h.suppression.IsSuppressed(ctx, email)
	if err != nil {
		// An infra failure here must not leak into a different response.
		// Treating it as "proceed as if suppressed" (send nothing) is the
		// safe default: worst case a legitimate signup doesn't get an
		// email this one time and the visitor can simply try again, which
		// is far cheaper than the alternative of possibly mailing a
		// suppressed address because the check couldn't run.
		h.log.Error("subscribe: suppression check failed", "err", err)
		writeSubscribeUniform202(w)
		return
	}
	if suppressed {
		writeSubscribeUniform202(w)
		return
	}

	evidence := subscribers.RestartSignupInput{
		SignupIP:        clientIP(r),
		SignupUserAgent: r.UserAgent(),
		UTMSource:       req.UTMSource,
		UTMMedium:       req.UTMMedium,
		UTMCampaign:     req.UTMCampaign,
		ConfirmTTL:      subscribeConfirmTTL,
	}

	existing, err := h.subs.FindByEmail(ctx, email)
	switch {
	case errors.Is(err, subscribers.ErrNotFound):
		h.newSignup(ctx, email, interestIDs, evidence, now)
	case err != nil:
		h.log.Error("subscribe: lookup failed", "err", err)
	default:
		h.existingSignup(ctx, existing, interestIDs, evidence, now)
	}

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

	h.sendConfirmation(ctx, sub, now)
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

// sendConfirmation builds and enqueues the double opt-in confirmation email
// for a subscriber with a live confirm_token — a brand-new signup, a
// restarted one, or a resend to an already-pending one; all three call
// sites are identical from this point on, which is deliberate: #0026's
// review (finding 3) traced a duplicate-send bug to the resend branch
// having its OWN, separately-timed cooldown check instead of sharing this
// one atomic claim.
//
// The claim (internal/subscribers.ClaimConfirmationSend) is synchronous,
// fast, and non-network — it is what enforces PRD §6.3's once-per-hour
// resend limit AND what makes concurrent double-submits of the same
// address send at most once. Only a successful claim enqueues an actual
// send; losing the claim (cooldown active, or a concurrent request claimed
// first) is not an error, just this request declining to send. The mailer
// call itself happens later, off this goroutine — see enqueueSend and the
// package doc comment.
func (h *SubscribeHandler) sendConfirmation(ctx context.Context, sub subscribers.Subscriber, now time.Time) {
	if sub.ConfirmToken == nil {
		h.log.Error("subscribe: subscriber has no confirm token", "subscriber_id", sub.ID)
		return
	}
	claimed, err := h.subs.ClaimConfirmationSend(ctx, sub.ID, now, subscribeResendCooldown)
	if err != nil {
		h.log.Error("subscribe: claiming confirmation send failed", "subscriber_id", sub.ID, "err", err)
		return
	}
	if !claimed {
		return // cooldown active, or a concurrent request already claimed this send
	}

	msg := mailing.BuildConfirmationEmail(sub.Email, h.baseURL, *sub.ConfirmToken, sub.ManageToken, subscribeConfirmTTL, h.physicalAddress(ctx))
	h.enqueueSend(sendJob{
		subscriberID: sub.ID,
		label:        "confirmation",
		msg:          msg,
		release: func(ctx context.Context) error {
			return h.subs.ReleaseConfirmationClaim(ctx, sub.ID, h.now())
		},
	})
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
	claimed, err := h.subs.ClaimAlreadySubscribedSend(ctx, existing.ID, now, subscribeAlreadySubscribedCooldown)
	if err != nil {
		h.log.Error("subscribe: claiming already-subscribed send failed", "subscriber_id", existing.ID, "err", err)
		return
	}
	if !claimed {
		return
	}

	msg := mailing.BuildAlreadySubscribedEmail(existing.Email, h.baseURL, existing.ManageToken, h.physicalAddress(ctx))
	h.enqueueSend(sendJob{
		subscriberID: existing.ID,
		label:        "already-subscribed",
		msg:          msg,
		release: func(ctx context.Context) error {
			return h.subs.ReleaseAlreadySubscribedClaim(ctx, existing.ID, h.now())
		},
	})
}

// physicalAddress reads settings.physical_address for the email footer. A
// nil settings dependency or any read error is treated as an empty address
// rather than failing the signup — mailing.BuildConfirmationEmail already
// documents "" as simply omitting the address line, and #0045's send
// worker (not this endpoint) is where an empty physical_address is
// actually enforced (CLAUDE.md §9).
func (h *SubscribeHandler) physicalAddress(ctx context.Context) string {
	if h.settings == nil {
		return ""
	}
	value, err := h.settings.GetSetting(ctx, "physical_address")
	if err != nil {
		return ""
	}
	return value
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
