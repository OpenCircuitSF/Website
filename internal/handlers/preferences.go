// GET/PATCH /api/preferences — the token-authenticated preference center
// (PRD §6.4; #0031), reached from every email footer and from a fresh
// confirm (via ConfirmHandler's confirmResponse, which already carries
// everything this GET would otherwise need to fetch — see confirm.go).
//
// # Email masking
//
// GET /api/preferences ALWAYS masks the email (maskEmail below). It is
// deliberately unconditional: there is no server-trusted signal available
// here to distinguish "this token was just handed to the SPA by a confirm"
// from "this is a bookmarked/screenshotted link from three months ago" —
// manage_token itself never changes, so the token alone carries no
// freshness information. The unmasked exception PRD §6.4 and #0030
// describe ("unless the token was just issued by a confirm") is implemented
// entirely on the client: ConfirmSubscription.svelte already holds the
// plaintext email from its own POST /api/subscribe/confirm response (proof
// of fresh mailbox control) and hands it to PreferenceCenter.svelte as a
// prop for that one render, never sending it back to or re-requesting it
// from the server. That keeps this endpoint's contract simple and uniform
// — anyone presenting a bare manage_token always gets the masked form —
// while still satisfying the "unless fresh from confirm" carve-out, because
// the unmasking happens with data the browser already legitimately
// possesses, not with a client-asserted flag this endpoint would have to
// trust.
//
// # PATCH /api/preferences also carries "Unsubscribe from everything"
//
// PRD §6.4's own contract for this route is `{token, interests:[slug,...]}`
// replacing the interest set. #0031's acceptance criteria additionally
// require "a separate, clearly labelled 'Unsubscribe from everything'
// action... alongside" the interest editor. There is no dedicated
// unsubscribe endpoint yet — #0034 (Phase 4, not built) is the one-click
// email-footer-link version of that, and PRD §6.4 does not itself name a
// second route for the preference-center version. Rather than invent a
// third public route for one boolean, this handler accepts an optional
// `unsubscribe: true` field on the SAME PATCH body: when set, it calls
// subscribers.Store.Unsubscribe (source=preferences) and ignores any
// `interests` field in the same request; when absent/false, it behaves
// exactly per PRD §6.4. This keeps the "alongside" requirement literal (two
// distinct buttons in the SPA, each sending a different payload to the same
// route) without expanding the route surface. #0034, when it lands, should
// write the SAME audit.ActionSubscriberUnsubscribed action with
// source=one_click — see that constant's doc comment
// (internal/audit/actions.go) — rather than reintroduce this decision.
package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// maxPreferencesInterests mirrors subscribe.go's maxSubscribeInterests — an
// upper bound on how many slugs a single PATCH may carry, cheap insurance
// against a pathological payload before any per-slug lookup runs.
const maxPreferencesInterests = 64

// complainedContactEmail is named inline in the no-op message below (#0090
// bounce fix) so the "Contact us" sentence actually says how, not just
// that. Same address Footer.svelte, About.svelte and PrivacyPolicy.svelte
// carry as their CONTACT_EMAIL constant (#0075 published it for exactly
// this class of request); this response is plain-text JSON with nowhere to
// put a clickable link, so the address is spelled out in the sentence
// itself instead.
const complainedContactEmail = "hello@opencircuitsf.com"

// preferencesSubscriberStore is the behavior PreferencesHandler needs from
// internal/subscribers.
type preferencesSubscriberStore interface {
	FindByManageToken(ctx context.Context, token string) (subscribers.Subscriber, error)
	InterestIDs(ctx context.Context, subscriberID int64) ([]int64, error)
	SetInterests(ctx context.Context, subscriberID int64, interestIDs []int64) error
	Unsubscribe(ctx context.Context, id int64, source string, now time.Time) (subscribers.Subscriber, error)
}

// preferencesInterestStore is the behavior PreferencesHandler needs from
// internal/interests. GetBySlug is used (not GetByIDs-then-filter) because
// it resolves regardless of the active flag — interests.Store's own doc
// comment on GetBySlug names exactly this use case: "a subscriber's
// preference center needs to resolve a slug that has since been
// deactivated". That matters here because the SPA's checkbox grid only
// offers currently-active interests, but a PATCH that merely re-saves an
// untouched selection must not silently drop an interest the subscriber
// picked before it was deactivated — GetBySlug lets that slug still resolve
// to an id instead of 400ing.
type preferencesInterestStore interface {
	GetByIDs(ctx context.Context, ids []int64) ([]interests.Interest, error)
	GetBySlug(ctx context.Context, slug string) (interests.Interest, error)
	ListActive(ctx context.Context) ([]interests.Interest, error)
}

// PreferencesHandler serves GET and PATCH /api/preferences (PRD §6.4,
// #0031).
type PreferencesHandler struct {
	subs    preferencesSubscriberStore
	ints    preferencesInterestStore
	auditor *audit.Logger
	now     func() time.Time
}

// NewPreferencesHandler constructs a PreferencesHandler over the data layer.
// A nil auditor disables audit writes. A nil now defaults to time.Now,
// matching the constructor conventions elsewhere in this package.
func NewPreferencesHandler(subs preferencesSubscriberStore, ints preferencesInterestStore, auditor *audit.Logger, now func() time.Time) *PreferencesHandler {
	if now == nil {
		now = time.Now
	}
	return &PreferencesHandler{subs: subs, ints: ints, auditor: auditor, now: now}
}

// preferencesResponse is the shared response shape for both GET and PATCH.
type preferencesResponse struct {
	Message         string               `json:"message,omitempty"`
	Email           string               `json:"email"`
	Status          string               `json:"status"`
	Interests       []string             `json:"interests"`
	ActiveInterests []publicInterestView `json:"active_interests"`
	Unsubscribed    bool                 `json:"unsubscribed"`
	// NoOp is true only on a PATCH {unsubscribe: true} against an already-
	// complained row: Store.Unsubscribe silently no-ops there (see its doc
	// comment), so Unsubscribed above is false and Message explains why
	// nothing changed instead of implying success (#0031 review finding 2 —
	// mirrors AdminSubscribersHandler.Suppress's no_op field, #0032).
	NoOp bool `json:"no_op,omitempty"`
}

// Get handles GET /api/preferences?token=. Invalid/unknown/rotated tokens
// answer 404 with a plain {"error":...} body — the SPA is responsible for
// rendering a neutral "this link doesn't work, subscribe again" state
// rather than falling through to a generic not-found view (#0031's
// acceptance criteria: "never a raw 404" describes the rendered PAGE, not
// forbidding the HTTP status; PreferenceCenter.svelte special-cases
// ApiError(404) from this route specifically).
func (h *PreferencesHandler) Get(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		writeError(w, http.StatusNotFound, "invalid or expired preferences link")
		return
	}

	sub, err := h.subs.FindByManageToken(r.Context(), token)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, subscribers.ErrNotFound):
		writeError(w, http.StatusNotFound, "invalid or expired preferences link")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	slugs, activeViews, err := h.loadInterestState(r.Context(), sub.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, preferencesResponse{
		Email:           maskEmail(sub.Email),
		Status:          sub.Status,
		Interests:       slugs,
		ActiveInterests: activeViews,
	})
}

// patchPreferencesRequest is the PATCH /api/preferences body. Unsubscribe,
// when true, takes priority over Interests (which is ignored in that case) —
// see the package doc comment for why this one route carries both actions.
type patchPreferencesRequest struct {
	Token       string   `json:"token"`
	Interests   []string `json:"interests"`
	Unsubscribe bool     `json:"unsubscribe,omitempty"`
}

// Patch handles PATCH /api/preferences. See the package doc comment for the
// Unsubscribe branch.
func (h *PreferencesHandler) Patch(w http.ResponseWriter, r *http.Request) {
	var req patchPreferencesRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		writeError(w, http.StatusNotFound, "invalid or expired preferences link")
		return
	}

	sub, err := h.subs.FindByManageToken(r.Context(), token)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, subscribers.ErrNotFound):
		writeError(w, http.StatusNotFound, "invalid or expired preferences link")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if req.Unsubscribe {
		h.patchUnsubscribe(w, r, sub)
		return
	}
	h.patchInterests(w, r, sub, req.Interests)
}

func (h *PreferencesHandler) patchUnsubscribe(w http.ResponseWriter, r *http.Request, sub subscribers.Subscriber) {
	updated, err := h.subs.Unsubscribe(r.Context(), sub.ID, subscribers.SourcePreferences, h.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Decided from the row Store.Unsubscribe actually returned, not a
	// status read before the call (#0104). Store.Unsubscribe's only no-op
	// case is a row that was ALREADY complained (its doc comment, which is
	// the authoritative answer to "did this change anything") — deriving
	// noOp from updated.Status rather than sub.Status closes the narrow
	// window where a complaint lands between the FindByManageToken read
	// above and this call, which would otherwise make the pre-call read
	// stale and let the audit-write/response logic below believe a real
	// unsubscribe happened when the guarded UPDATE actually refused it.
	noOp := updated.Status == subscribers.StatusComplained

	// Skip the audit write entirely on a no-op (#0031 review finding 2):
	// the audit log is the consent-evidence record PRD §6.3 promises and
	// #0038/#0060 will read, so a subscriber.unsubscribed row for someone
	// who in fact complained and was never unsubscribed would corrupt it —
	// worse than the misleading message alone. Unlike Suppress (which
	// still records the admin's action attempt with no_op:true in its
	// metadata), there is no analogous "action someone took" worth
	// recording here: this route no-ops on the subscriber's own request,
	// not an admin's, and nothing happened to log.
	if !noOp && h.auditor != nil {
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    nil,
			Action:     audit.ActionSubscriberUnsubscribed,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata:   map[string]any{"source": subscribers.SourcePreferences},
			IP:         clientIP(r),
		})
	}

	slugs, activeViews, err := h.loadInterestState(r.Context(), updated.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	message := "You've been unsubscribed from everything."
	if noOp {
		// #0090 bounce fix: says how to reach a human, not just that one
		// exists — see complainedContactEmail's doc comment above.
		message = "No change: this address is marked as having complained about a previous email, and complained addresses can't be unsubscribed or resubscribed from this page. Contact us at " + complainedContactEmail + " if you believe this is a mistake."
	}

	writeJSON(w, http.StatusOK, preferencesResponse{
		Message:         message,
		Email:           maskEmail(updated.Email),
		Status:          updated.Status,
		Interests:       slugs,
		ActiveInterests: activeViews,
		Unsubscribed:    !noOp,
		NoOp:            noOp,
	})
}

func (h *PreferencesHandler) patchInterests(w http.ResponseWriter, r *http.Request, sub subscribers.Subscriber, slugs []string) {
	if len(slugs) > maxPreferencesInterests {
		writeError(w, http.StatusBadRequest, "too many interests")
		return
	}

	ids := make([]int64, 0, len(slugs))
	for _, slug := range slugs {
		it, err := h.ints.GetBySlug(r.Context(), slug)
		switch {
		case err == nil:
			ids = append(ids, it.ID)
		case errors.Is(err, interests.ErrNotFound):
			writeError(w, http.StatusBadRequest, "unknown interest")
			return
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}

	if err := h.subs.SetInterests(r.Context(), sub.ID, ids); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		targetID := sub.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    nil,
			Action:     audit.ActionSubscriberPreferencesUpdated,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata:   map[string]any{"interest_count": len(ids)},
			IP:         clientIP(r),
		})
	}

	respSlugs, activeViews, err := h.loadInterestState(r.Context(), sub.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	message := preferencesSaveMessage(sub.Status, respSlugs)

	writeJSON(w, http.StatusOK, preferencesResponse{
		Message:         message,
		Email:           maskEmail(sub.Email),
		Status:          sub.Status,
		Interests:       respSlugs,
		ActiveInterests: activeViews,
	})
}

// loadInterestState resolves a subscriber's currently-selected interest ids
// to slugs and loads the full active taxonomy. Mirrors ConfirmHandler's
// method of the same name (confirm.go) — kept as a separate copy rather than
// a shared free function so the two handlers' dependencies stay fully
// decoupled (no risk of one file's edit changing the other's behavior).
func (h *PreferencesHandler) loadInterestState(ctx context.Context, subscriberID int64) ([]string, []publicInterestView, error) {
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

// preferencesSaveMessage composes the honest confirmation message for a
// successful interests PATCH (#0031 review finding 1). SetInterests itself
// has no status guard and always writes (deliberately — see this package's
// preferencesInterestStore doc comment and CLAUDE.md §9's "complained never
// auto-resubscribes": the interest edit is real and takes effect the moment
// status becomes active again), so this is never about whether the save
// happened. It is about not asserting an active subscription that does not
// exist: the previous unconditional "You're subscribed to general
// announcements only." told a non-active subscriber (unsubscribed,
// complained, bounced, or — the one truly-in-progress case — still pending)
// they were receiving mail. Only an active subscriber gets that claim.
func preferencesSaveMessage(status string, respSlugs []string) string {
	if status != subscribers.StatusActive {
		return "Your interests were saved, but you're not currently subscribed (status: " + status + "), so no emails will be sent. Resubscribe to start receiving them again."
	}
	if len(respSlugs) == 0 {
		return "Preferences saved. You're subscribed to general announcements only."
	}
	return "Preferences saved."
}

// maskEmail renders an address as "b•••••n@gmail.com" (PRD §6.4): first and
// last character of the local part kept, everything between replaced with a
// run of five bullets regardless of the original length (so the mask itself
// never leaks the local part's length), domain untouched. A local part of
// zero or one characters is masked outright (a single "•") rather than
// risking a degenerate reveal of the entire local part.
//
// Operates on runes, not bytes (#0031 review, minor finding): #0026
// deliberately accepts RFC 6531 non-ASCII local parts and SubscribeForm.svelte
// deliberately does not filter them, so a byte slice on local[:1]/local[len-1:]
// could land mid-codepoint and mask to invalid UTF-8 (renders as U+FFFD after
// JSON encoding). '@' itself is always a single ASCII byte, so splitting the
// domain off by byte index is still safe — only the local part needs runes.
func maskEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at <= 0 {
		// No '@' or empty local part — not a well-formed address (should
		// never happen for a stored subscriber), mask everything to be safe.
		return "•••••"
	}
	localRunes := []rune(email[:at])
	domain := email[at:]
	if len(localRunes) <= 1 {
		return "•" + domain
	}
	first := string(localRunes[0])
	last := string(localRunes[len(localRunes)-1])
	return first + "•••••" + last + domain
}
