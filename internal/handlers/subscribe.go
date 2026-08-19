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
package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
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
	MarkConfirmationSent(ctx context.Context, id int64, now time.Time) (subscribers.Subscriber, error)
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
}

// NewSubscribeHandler constructs a SubscribeHandler. A nil suppression
// checker defaults to NoSuppressions; a nil logger defaults to
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
	return &SubscribeHandler{
		subs: subs, interests: il, mailer: mailer, suppression: suppression,
		settings: settings, auditor: auditor, baseURL: baseURL,
		now: time.Now, log: logger,
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
		h.sendAlreadySubscribed(ctx, existing)

	case subscribers.StatusPending:
		h.resendConfirmation(ctx, existing, now)

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

// resendConfirmation handles the pending branch: resend the existing
// confirm link, rate-limited to once per hour per PRD §6.3. It reuses the
// existing confirm_token rather than minting a new one, so a
// still-unopened earlier email keeps working.
func (h *SubscribeHandler) resendConfirmation(ctx context.Context, existing subscribers.Subscriber, now time.Time) {
	if existing.ConfirmSentAt != nil && now.Sub(*existing.ConfirmSentAt) < subscribeResendCooldown {
		return // rate-limited; silently do nothing
	}
	if existing.ConfirmToken == nil {
		// Should be unreachable for a genuinely pending row (Create and
		// RestartSignup always populate it), but guard rather than
		// dereference a nil pointer if data ever drifts.
		h.log.Error("subscribe: pending subscriber has no confirm token", "subscriber_id", existing.ID)
		return
	}

	msg := mailing.BuildConfirmationEmail(existing.Email, h.baseURL, *existing.ConfirmToken, existing.ManageToken, subscribeConfirmTTL, h.physicalAddress(ctx))
	if _, err := h.mailer.Send(ctx, msg); err != nil {
		h.log.Error("subscribe: resending confirmation email failed", "subscriber_id", existing.ID, "err", err)
		return // do not stamp confirm_sent_at on a failed send
	}
	if _, err := h.subs.MarkConfirmationSent(ctx, existing.ID, now); err != nil {
		h.log.Error("subscribe: marking confirmation sent failed", "subscriber_id", existing.ID, "err", err)
	}
}

// sendConfirmation builds and sends the double opt-in confirmation email
// for a subscriber that now has a live confirm_token — a new signup or a
// restarted one — and stamps confirm_sent_at only after the send succeeds.
func (h *SubscribeHandler) sendConfirmation(ctx context.Context, sub subscribers.Subscriber, now time.Time) {
	if sub.ConfirmToken == nil {
		h.log.Error("subscribe: subscriber has no confirm token", "subscriber_id", sub.ID)
		return
	}
	msg := mailing.BuildConfirmationEmail(sub.Email, h.baseURL, *sub.ConfirmToken, sub.ManageToken, subscribeConfirmTTL, h.physicalAddress(ctx))
	if _, err := h.mailer.Send(ctx, msg); err != nil {
		h.log.Error("subscribe: sending confirmation email failed", "subscriber_id", sub.ID, "err", err)
		return // do not stamp confirm_sent_at on a failed send
	}
	if _, err := h.subs.MarkConfirmationSent(ctx, sub.ID, now); err != nil {
		h.log.Error("subscribe: marking confirmation sent failed", "subscriber_id", sub.ID, "err", err)
	}
}

// sendAlreadySubscribed handles the active branch: notify the submitter the
// address is already on the list, with the preference-center link. No
// store mutation — nothing about the subscriber's own state changes.
func (h *SubscribeHandler) sendAlreadySubscribed(ctx context.Context, existing subscribers.Subscriber) {
	msg := mailing.BuildAlreadySubscribedEmail(existing.Email, h.baseURL, existing.ManageToken, h.physicalAddress(ctx))
	if _, err := h.mailer.Send(ctx, msg); err != nil {
		h.log.Error("subscribe: sending already-subscribed email failed", "subscriber_id", existing.ID, "err", err)
	}
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
// undecorated address (no display name, no comments) using only ASCII
// characters.
//
// The ASCII-only restriction is deliberate, not an accidental
// simplification: #0026's review carried in the observation that
// normalizeEmail (Go's strings.ToLower, full Unicode case folding) and the
// subscribers_email_normalized CHECK constraint (Postgres's lower(),
// locale-dependent) can disagree on how to lowercase a non-ASCII rune. For
// pure ASCII input the two case-fold identically, so an address that
// passes this check can never trip that CHECK constraint and turn into a
// 500 from an endpoint that must always answer 202. Rejecting here is
// indistinguishable to the caller from any other syntax error — same
// generic "invalid email" message — so it adds no new oracle.
func validEmailSyntax(email string) bool {
	if email == "" || len(email) > 254 {
		return false
	}
	for i := 0; i < len(email); i++ {
		if email[i] > 127 {
			return false
		}
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
// confirmed exactly one "@" and an ASCII-only address; strings.ToLower is
// therefore safe (see validEmailSyntax's doc comment on the ASCII/Unicode
// case-folding hazard this sidesteps entirely).
func isDisposableDomain(email string) bool {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return false
	}
	return disposableEmailDomains[strings.ToLower(email[at+1:])]
}
