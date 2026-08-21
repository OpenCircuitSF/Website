// GET/POST /api/unsubscribe — RFC 8058 one-click unsubscribe (PRD §6.5 path
// 1; #0034), reached from the List-Unsubscribe email header, not from a
// browser session:
//
//	List-Unsubscribe: <https://opencircuitsf.com/api/unsubscribe?token=MANAGE_TOKEN>, ...
//	List-Unsubscribe-Post: List-Unsubscribe=One-Click
//
// Three things about this endpoint are counter-intuitive and all three are
// load-bearing:
//
// # No session, no CSRF token
//
// The POST arrives from Gmail's or Yahoo's infrastructure autonomously
// acting on List-Unsubscribe-Post, never from a browser holding a session
// cookie or a CSRF token minted for one. Routes registered under
// requireSession/CSRF middleware in cmd/opencircuit/main.go would break this
// path entirely, so Post is wired unauthenticated and unguarded, exactly
// like PreferencesHandler and ConfirmHandler above it in this package.
//
// # GET must never mutate
//
// Mail clients and corporate security scanners prefetch every URL that
// appears in a message, including the href a human would otherwise click.
// Get therefore only ever 302s to the SPA's /unsubscribe confirmation view
// (client-side rendering, no store call) — mirroring ConfirmHandler's GET
// /confirm, which is likewise a pure SPA route with no Go-side mutation.
// Only Post — reachable exclusively via a real one-click POST — touches the
// store.
//
// # Every input answers 200, including garbage
//
// PRD §6.5: "a provider seeing errors on the unsubscribe endpoint downgrades
// sender reputation." Unknown, expired, and replayed tokens (replay is
// exactly what an already-rotated manage_token produces — see
// subscribers.Store.RotateManageToken) all answer the same neutral 200 this
// handler gives a genuinely fresh, valid token. This is the deliberate
// opposite of PreferencesHandler.Get/Patch, which 404 an unknown token —
// the two routes answer to different audiences (a human's browser vs. a
// mail provider's webhook-shaped retry logic) and are not factored
// together, per #0034's carried-in review findings.
//
// # The complained no-op, and why it stays silent
//
// subscribers.Store.Unsubscribe already no-ops on a currently-complained
// row (status, unsubscribed_at, unsubscribe_source all left unchanged — see
// its own doc comment, which is the authoritative answer to "did this
// change anything"). This handler derives noOp from the ROW Unsubscribe
// RETURNS, not a status read before the call (#0104; an earlier version
// read sub.Status before calling Unsubscribe, mirroring
// PreferencesHandler.patchUnsubscribe's original `before := sub.Status` —
// both had a narrow read-then-act window if a complaint landed between the
// SELECT and the UPDATE), and uses that to decide, deliberately in this
// order:
//
//  1. Skip RotateManageToken entirely when the row was already complained.
//     Rotation's purpose is stopping replay of a CONSUMED unsubscribe link;
//     a complained row is already terminal and refused by every mutator, so
//     rotating there would only churn the token on every unattended hit
//     this exact RFC-8058 endpoint is guaranteed to receive from provider
//     retry/prefetch infrastructure — invalidating every live footer link
//     the person holds, for an action that changed nothing.
//  2. Skip the audit write entirely when the row was already complained —
//     reusing audit.ActionSubscriberUnsubscribed for an unsubscribe that
//     never happened would corrupt the record #0038/#0060 read (the same
//     bug #0031's review caught and fixed on the preference-center path).
//  3. Answer the SAME neutral 200 regardless — the no-op has to be silent
//     to the provider, or a complained subscriber's stale footer link would
//     start producing errors exactly where PRD §6.5 forbids them.
//
// # Suppression list: deliberately untouched
//
// #0034 lists #0033 (subscribers.SuppressionStore) as a dependency. This
// handler does not call it. subscribers.suppressionReasons (suppressions.go)
// is a closed set — hard_bounce, complaint, manual, repeated_soft_bounce —
// and pointedly has no "unsubscribed" member: PRD §6.5's state machine adds
// a suppressions row on a hard bounce or a complaint, never on a plain
// unsubscribe, because an unsubscribed (as opposed to suppressed) address
// must still be able to resubscribe through ordinary double opt-in
// (RestartSignup, same table). Suppressing on every one-click hit would
// permanently block that path for a subscriber who never complained,
// contradicting the very table this endpoint is specified by.
//
// # Response schema
//
// #0093 (open, not fixed here) tracks two DIFFERENT no_op JSON schemas
// already living in this package: preferences.go tags NoOp
// `json:"no_op,omitempty"`, admin_subscribers.go tags it plain
// `json:"no_op"`. Rather than add a third shape to that drift, this
// handler's unsubscribeResponse matches admin_subscribers.go's — no
// omitempty, so `false` is always explicit in the body — since that is the
// shape #0093 itself names as correct ("prefer dropping omitempty ...
// matching #0032's original").
package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// unsubscribeSubscriberStore is the behavior UnsubscribeHandler needs from
// internal/subscribers. A narrow interface, matching this package's
// convention (see e.g. preferencesSubscriberStore, confirmSubscriberStore)
// of depending on behavior rather than a concrete store.
type unsubscribeSubscriberStore interface {
	FindByManageToken(ctx context.Context, token string) (subscribers.Subscriber, error)
	Unsubscribe(ctx context.Context, id int64, source string, now time.Time) (subscribers.Subscriber, error)
	RotateManageToken(ctx context.Context, id int64, now time.Time) (subscribers.Subscriber, error)
}

// UnsubscribeHandler serves GET/POST /api/unsubscribe (PRD §6.5 path 1,
// RFC 8058; #0034). See the package doc comment for why GET and POST behave
// so differently from each other.
type UnsubscribeHandler struct {
	subs    unsubscribeSubscriberStore
	auditor *audit.Logger
	now     func() time.Time
	log     *slog.Logger
}

// NewUnsubscribeHandler constructs an UnsubscribeHandler. A nil auditor
// disables audit writes (matching every other handler in this package); a
// nil now defaults to time.Now; a nil logger defaults to slog.Default().
func NewUnsubscribeHandler(subs unsubscribeSubscriberStore, auditor *audit.Logger, now func() time.Time, logger *slog.Logger) *UnsubscribeHandler {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &UnsubscribeHandler{subs: subs, auditor: auditor, now: now, log: logger}
}

// unsubscribeResponse is the uniform 200 body for POST /api/unsubscribe.
// See the package doc comment's "Response schema" section for why NoOp
// carries no `omitempty`.
type unsubscribeResponse struct {
	Message string `json:"message"`
	NoOp    bool   `json:"no_op"`
}

// Get handles GET /api/unsubscribe. It NEVER touches the store — see the
// package doc comment's "GET must never mutate" section — and always 302s
// to the SPA's /unsubscribe confirmation view, carrying the token along so
// that view (#0034 blocks #0036, which builds it) has what it needs to
// render or to fire its own POST from JavaScript, the same prefetch-safe
// shape ConfirmHandler's package doc comment describes for GET /confirm.
func (h *UnsubscribeHandler) Get(w http.ResponseWriter, r *http.Request) {
	target := "/unsubscribe"
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		target += "?token=" + url.QueryEscape(token)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// Post handles POST /api/unsubscribe — the actual RFC 8058 one-click action.
// token arrives as a URL query parameter (matching the
// List-Unsubscribe header's <https://.../api/unsubscribe?token=...> form);
// the request body (List-Unsubscribe=One-Click) is deliberately never
// parsed or validated — RFC 8058 defines it as a fixed, content-free marker,
// and rejecting a body that fails to parse would violate "every input
// answers 200" for a request shape this handler is required to accept.
//
// Every path through this method ends in http.StatusOK; see the package
// doc comment for why.
func (h *UnsubscribeHandler) Post(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		h.writeNeutral(w, true, "If this address was on our list, it has been unsubscribed.")
		return
	}

	sub, err := h.subs.FindByManageToken(r.Context(), token)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, subscribers.ErrNotFound):
		// Unknown, garbage, or replayed token (a replay of an
		// already-processed one-click link IS exactly this case: the
		// first hit rotated manage_token, so the old value this retry
		// carries no longer resolves to anyone). Neutral 200, no store
		// call, nothing to audit.
		h.writeNeutral(w, true, "If this address was on our list, it has been unsubscribed.")
		return
	default:
		// A transient store failure must still answer 200 (PRD §6.5) —
		// there is no error response this endpoint is allowed to give a
		// mail provider. Log server-side so the failure is visible to an
		// operator even though the caller never sees it.
		h.log.Error("unsubscribe: FindByManageToken failed", "err", err)
		h.writeNeutral(w, true, "If this address was on our list, it has been unsubscribed.")
		return
	}

	updated, err := h.subs.Unsubscribe(r.Context(), sub.ID, subscribers.SourceOneClick, h.now())
	if err != nil {
		h.log.Error("unsubscribe: Store.Unsubscribe failed", "subscriber_id", sub.ID, "err", err)
		h.writeNeutral(w, true, "If this address was on our list, it has been unsubscribed.")
		return
	}

	// Decided from the row Store.Unsubscribe actually returned, not a
	// status read before the call (#0104) — that row is the guarded
	// UPDATE's own atomic answer to "did this change anything", so it can't
	// go stale the way a pre-call read can if a complaint lands between the
	// SELECT above and this UPDATE. See the package doc comment's
	// "complained no-op" section for why the rotation and audit decisions
	// below must still happen after this check and before either write.
	noOp := updated.Status == subscribers.StatusComplained

	if noOp {
		// Do nothing further at all: no rotation, no audit row. See the
		// package doc comment's "complained no-op" section.
		h.writeNeutral(w, true,
			"No change: this address is marked as having complained about a previous email and can't be unsubscribed again from this link.")
		return
	}

	if _, err := h.subs.RotateManageToken(r.Context(), updated.ID, h.now()); err != nil {
		// The unsubscribe itself already succeeded and committed; a failed
		// rotation is a smaller, logged-only problem (a stale link stays
		// live a little longer than intended), not a reason to report
		// failure for a request PRD §6.5 requires this endpoint to answer
		// 200 to regardless.
		h.log.Error("unsubscribe: RotateManageToken failed", "subscriber_id", updated.ID, "err", err)
	}

	if h.auditor != nil {
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    nil,
			Action:     audit.ActionSubscriberUnsubscribed,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata:   map[string]any{"source": subscribers.SourceOneClick},
			IP:         clientIP(r),
		})
	}

	h.writeNeutral(w, false, "You've been unsubscribed.")
}

// writeNeutral writes the uniform 200 body every path through Post ends in.
// Named to make the "always 200" invariant impossible to miss at each call
// site above — see the package doc comment.
func (h *UnsubscribeHandler) writeNeutral(w http.ResponseWriter, noOp bool, message string) {
	writeJSON(w, http.StatusOK, unsubscribeResponse{Message: message, NoOp: noOp})
}
