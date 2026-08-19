// Public interest-taxonomy read path (PRD §6.1, §7.3; #0029). Serves the
// checkbox grid SubscribeForm.svelte renders — and, once #0031 lands, the
// preference center's "topics you can pick from" list too.
//
// # Why this exists — a carried-in finding from #0024's review
//
// #0024's admin CRUD screen tells operators that deactivating an interest
// "removes this interest from the public signup form", but #0026's
// subscribe endpoint enforces that only at validation time: it rejects an
// inactive slug with a 400. Before this file, nothing served the public a
// list to build checkboxes FROM in the first place — the only existing read
// path was AdminInterestsHandler.List (GET /admin/interests), which is
// admin-only (RequireSession + RequireAdmin) and calls ListAll, the wrong
// method for this purpose: it would show an inactive interest as a
// selectable checkbox, and submitting it would then 400. This handler is
// deliberately new and deliberately thin — it must never be reachable
// without a session (that route stays admin-only, unchanged) and it must
// only ever call ListActive, never ListAll, so the taxonomy an anonymous
// visitor can see is exactly the taxonomy #0026 will accept.
package handlers

import (
	"context"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
)

// publicInterestStore is the narrow slice of interests.Store this handler
// needs. Depending on an interface (rather than the concrete *interests.Store)
// matches interestStore/adminSubscriberStore's pattern elsewhere in this
// package and keeps the handler unit-testable with a fake.
type publicInterestStore interface {
	ListActive(ctx context.Context) ([]interests.Interest, error)
}

// PublicInterestsHandler serves GET /api/interests: the public, unauthenticated
// read of the active interest taxonomy. No session or admin check — this is
// the one interests read path a signed-out visitor is meant to reach.
type PublicInterestsHandler struct {
	store publicInterestStore
}

// NewPublicInterestsHandler constructs a PublicInterestsHandler over the data
// layer.
func NewPublicInterestsHandler(store publicInterestStore) *PublicInterestsHandler {
	return &PublicInterestsHandler{store: store}
}

// publicInterestView is the JSON shape for one interests row as shown to an
// anonymous visitor. Deliberately narrower than AdminInterestsHandler's
// interestView: no id (the public wire contract identifies an interest by
// slug, per #0026's subscribeRequest.Interests), no subscriber_count (an
// operational detail, not a visitor's concern), and no active (this list is
// ListActive's output — every row in it is active by construction).
type publicInterestView struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	SortOrder   int     `json:"sort_order"`
}

func toPublicInterestView(it interests.Interest) publicInterestView {
	return publicInterestView{
		Slug:        it.Slug,
		Name:        it.Name,
		Description: it.Description,
		SortOrder:   it.SortOrder,
	}
}

// publicInterestsResponse is the GET /api/interests body: {"interests":[{...}]}.
// The list is always present (never null), matching interestsResponse's
// convention.
type publicInterestsResponse struct {
	Interests []publicInterestView `json:"interests"`
}

// List handles GET /api/interests. Returns every ACTIVE interest, ordered by
// sort_order then name (interests.Store.ListActive) — never ListAll. Public,
// no auth required.
func (h *PublicInterestsHandler) List(w http.ResponseWriter, r *http.Request) {
	all, err := h.store.ListActive(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views := make([]publicInterestView, 0, len(all))
	for _, it := range all {
		views = append(views, toPublicInterestView(it))
	}
	writeJSON(w, http.StatusOK, publicInterestsResponse{Interests: views})
}
