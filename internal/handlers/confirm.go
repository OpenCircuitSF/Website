// POST /api/subscribe/confirm — completes double opt-in (PRD §6.3; #0030).
//
// Deliberately a NEW handler/file rather than an addition to subscribe.go:
// #0081 is concurrently reviewing that file, and this route shares nothing
// with Subscribe's uniform-202 machinery — a confirm token is either valid
// or it isn't, and telling the caller which is not an email-enumeration
// oracle the way Subscribe's branches are (see subscribe.go's package doc
// comment). Reaching this handler at all already requires possessing a
// 32-byte random secret that was only ever placed in one inbox, so a
// distinguishing response here reveals nothing an attacker didn't already
// need to know to get here.
//
// # GET must never mutate
//
// The route table (cmd/opencircuit/main.go) only ever wires this file's
// Confirm method to POST /api/subscribe/confirm. GET /confirm (no /api
// prefix) is a client-side SPA route handled entirely by the catch-all
// "GET /" handler (web/dist/index.html, no Go logic runs) — mail clients,
// link scanners, and corporate security proxies prefetch every URL in a
// message, and a confirming GET would auto-confirm addresses that never
// clicked, destroying the consent evidence double opt-in exists to produce.
// ConfirmSubscription.svelte fires this endpoint's POST from JavaScript only
// after the page has actually loaded and run — a prefetcher that fetches the
// HTML without executing script (the entire point of "prefetch-safe") never
// triggers it.
//
// # Token comparison: no application-level constant-time compare
//
// subscribers.Store.Confirm resolves confirm_token via a single indexed SQL
// equality lookup (`WHERE confirm_token = $1 FOR UPDATE`) — never a manual
// byte-by-byte Go comparison, which is the actual pattern
// crypto/subtle.ConstantTimeCompare exists to guard against (an early-exit
// loop that leaks how many leading bytes matched via timing). There is no
// non-secret column to fetch a candidate row by first (unlike a
// selector/verifier token split), so this handler has no two in-memory
// values to compare — the comparison happens inside Postgres's own index
// scan, the same mechanism already used and reviewed for manage_token,
// session tokens, and the register/recover magic-link tokens elsewhere in
// this codebase, none of which layer a Go-level constant-time compare on
// top either. A selector/verifier redesign would close this gap for real if
// ever prioritized, but is a schema change out of scope here — see this
// issue's Gotchas.
package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// confirmSubscriberStore is the behavior ConfirmHandler needs from
// internal/subscribers. Depending on an interface keeps the handler
// unit-testable with a fake, matching the rest of this package's pattern.
type confirmSubscriberStore interface {
	Confirm(ctx context.Context, token string, now time.Time) (subscribers.Subscriber, error)
	InterestIDs(ctx context.Context, subscriberID int64) ([]int64, error)
}

// confirmInterestStore is the behavior ConfirmHandler needs from
// internal/interests: resolving the subscriber's currently-selected interest
// ids back to slugs, and listing the active taxonomy for the response's
// active_interests (so the SPA can render the preference-center checkbox
// grid inline without a second round trip to GET /api/interests).
type confirmInterestStore interface {
	GetByIDs(ctx context.Context, ids []int64) ([]interests.Interest, error)
	ListActive(ctx context.Context) ([]interests.Interest, error)
}

// ConfirmHandler serves POST /api/subscribe/confirm (PRD §6.3, #0030).
type ConfirmHandler struct {
	subs    confirmSubscriberStore
	ints    confirmInterestStore
	auditor *audit.Logger
	now     func() time.Time
	log     *slog.Logger
}

// NewConfirmHandler constructs a ConfirmHandler. A nil auditor disables
// audit writes; a nil logger defaults to slog.Default(), matching
// SubscribeHandler's (NewSubscribeHandler) nil-tolerance convention.
func NewConfirmHandler(subs confirmSubscriberStore, ints confirmInterestStore, auditor *audit.Logger, logger *slog.Logger) *ConfirmHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ConfirmHandler{subs: subs, ints: ints, auditor: auditor, now: time.Now, log: logger}
}

// confirmRequest is the POST /api/subscribe/confirm body: {"token":"..."}.
type confirmRequest struct {
	Token string `json:"token"`
}

// confirmResponse is the 200 body on success. manage_token is what lets the
// SPA show the preference center inline without a second confirmation email
// (#0030's acceptance criterion) — the caller just proved fresh control of
// the mailbox by presenting a single-use confirm token, so returning the
// plaintext email here (unlike GET /api/preferences, which masks by
// default) is safe: nobody who lacks that same secret can reach this
// response. interests/active_interests carry everything
// PreferenceCenter.svelte needs to render immediately, matching what GET
// /api/preferences returns for the standalone entry point.
type confirmResponse struct {
	Message         string               `json:"message"`
	ManageToken     string               `json:"manage_token"`
	Email           string               `json:"email"`
	Interests       []string             `json:"interests"`
	ActiveInterests []publicInterestView `json:"active_interests"`
}

// Confirm handles POST /api/subscribe/confirm. See the package doc comment
// for why a non-uniform response here does not reintroduce #0026's
// enumeration concern.
func (h *ConfirmHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	var req confirmRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "invalid or expired confirmation link")
		return
	}

	sub, err := h.subs.Confirm(r.Context(), req.Token, h.now())
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, subscribers.ErrTokenInvalid):
		// Covers three cases the store cannot distinguish once a token is
		// consumed (confirm_token is cleared on success, single-use by
		// design — see subscribers.Store.Confirm): a token that never
		// existed, one that expired, and a replay of one already used. The
		// SPA's copy (ConfirmSubscription.svelte) is worded to cover all
		// three gracefully rather than asserting any one of them.
		writeError(w, http.StatusBadRequest, "invalid or expired confirmation link")
		return
	case errors.Is(err, subscribers.ErrComplainedLocked):
		writeError(w, http.StatusBadRequest, "this subscription can't be reactivated automatically; contact us if you believe this is a mistake")
		return
	default:
		h.log.Error("confirm: store.Confirm failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		targetID := sub.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    nil,
			Action:     audit.ActionSubscriberConfirmed,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata:   map[string]any{"email": sub.Email},
			IP:         clientIP(r),
		})
	}

	slugs, activeViews, err := h.loadInterestState(r.Context(), sub.ID)
	if err != nil {
		// The subscriber IS confirmed at this point (Confirm already
		// committed) — a failure here must not be reported as a failed
		// confirmation. Degrade to empty interest lists rather than 500;
		// the SPA can still show the preference center with the manage
		// token, and a save there re-fetches/resets state anyway.
		h.log.Error("confirm: loading interest state failed", "subscriber_id", sub.ID, "err", err)
		slugs = []string{}
		activeViews = []publicInterestView{}
	}

	writeJSON(w, http.StatusOK, confirmResponse{
		Message:         "You're confirmed.",
		ManageToken:     sub.ManageToken,
		Email:           sub.Email,
		Interests:       slugs,
		ActiveInterests: activeViews,
	})
}

// loadInterestState resolves a subscriber's currently-selected interest ids
// to slugs and loads the full active taxonomy, for the confirmResponse and
// (via preferences.go's identical helper) GET /api/preferences.
func (h *ConfirmHandler) loadInterestState(ctx context.Context, subscriberID int64) ([]string, []publicInterestView, error) {
	ids, err := h.subs.InterestIDs(ctx, subscriberID)
	if err != nil {
		return nil, nil, err
	}
	selected, err := h.ints.GetByIDs(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	slugs := make([]string, 0, len(selected))
	for _, it := range selected {
		slugs = append(slugs, it.Slug)
	}
	active, err := h.ints.ListActive(ctx)
	if err != nil {
		return nil, nil, err
	}
	views := make([]publicInterestView, 0, len(active))
	for _, it := range active {
		views = append(views, toPublicInterestView(it))
	}
	return slugs, views, nil
}
