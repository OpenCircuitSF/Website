// Public CRT-session read path (#0393): GET /api/crt-session, the ordered,
// active-only rows the home page's live CRT screen (#0270, #0274) types out.
// Deliberately new and deliberately thin, mirroring public_interests.go's
// own convention: it must only ever call ListActive, never List, so an
// anonymous visitor never learns a deactivated row's id or that it exists at
// all.
//
// STORAGE=json (CLAUDE.md §5) has no crt_commands-table backing —
// internal/devstore does not implement crtCommandStore — so this handler is
// nil under STORAGE=json (see cmd/opencircuit/main.go's serveDevMode) and
// GET /api/crt-session is simply absent from the route table in that mode
// (mountAndServe only registers it when non-nil, matching
// publicInterestsH's own convention). The SPA's fallback for that case —
// and for a 404/500 from this handler, and for a pre-seed deploy where the
// migration has run but no admin has edited anything yet — is the
// compiled-in CRT_SESSION constant in web/src/lib/crtScreen.ts; see that
// file's own doc comment.
package handlers

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/crt"
)

// publicCrtCommandStore is the narrow slice of crt.Store this handler needs.
// Depending on an interface (rather than the concrete *crt.Store) matches
// publicInterestStore's own pattern and keeps the handler unit-testable with
// a fake.
type publicCrtCommandStore interface {
	ListActive(ctx context.Context) ([]crt.Command, error)
}

// publicCrtCommandView is the JSON shape for one crt_commands row as shown
// to an anonymous visitor. Deliberately narrower than crtCommandView: no id,
// no active (this list is ListActive's output — every row in it is active by
// construction), no created_at/updated_at, no slug (the SPA has no need to
// address a row individually; #0393's acceptance criteria require this be
// asserted against the encoded JSON bytes, not the Go struct, precisely
// because a field added here later would otherwise ship unnoticed).
type publicCrtCommandView struct {
	Cmd    string   `json:"cmd"`
	Out    []string `json:"out"`
	Source string   `json:"source"`
}

func toPublicCrtCommandView(c crt.Command) publicCrtCommandView {
	lines := c.Lines()
	// out must never be null in the response even when Output somehow
	// splits to zero lines (a row with empty stored output would violate the
	// NOT NULL/format the admin form enforces, but the wire contract stays
	// defensive rather than trusting that invariant all the way through).
	if lines == nil {
		lines = []string{}
	}
	return publicCrtCommandView{Cmd: c.Command, Out: lines, Source: c.Source}
}

// publicCrtSessionResponse is the GET /api/crt-session body:
// {"commands":[{...}]}, matching publicInterestsResponse's "always present,
// never null" convention.
type publicCrtSessionResponse struct {
	Commands []publicCrtCommandView `json:"commands"`
}

// crtSessionTTL and the handler's in-process cache mirror
// PublicListStatsHandler's own convention exactly (same rationale: this
// endpoint is hit by every home-page visitor, and the data changes only when
// an admin edits it). #0393's Design §1 table calls out the same 60s TTL and
// Cache-Control value as list-stats.
const crtSessionTTL = 60 * time.Second

// PublicCrtSessionHandler serves GET /api/crt-session: the public,
// unauthenticated read of the active CRT command rows, in order.
type PublicCrtSessionHandler struct {
	store publicCrtCommandStore
	now   func() time.Time

	mu       sync.Mutex
	cached   publicCrtSessionResponse
	cachedAt time.Time
	haveOnce bool
}

// NewPublicCrtSessionHandler constructs a PublicCrtSessionHandler over the
// data layer.
func NewPublicCrtSessionHandler(store publicCrtCommandStore) *PublicCrtSessionHandler {
	return &PublicCrtSessionHandler{store: store, now: time.Now}
}

// List handles GET /api/crt-session.
func (h *PublicCrtSessionHandler) List(w http.ResponseWriter, r *http.Request) {
	resp, err := h.value(r.Context())
	if err != nil {
		http.Error(w, "crt session unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, resp)
}

func (h *PublicCrtSessionHandler) value(ctx context.Context) (publicCrtSessionResponse, error) {
	h.mu.Lock()
	if h.haveOnce && h.now().Sub(h.cachedAt) < crtSessionTTL {
		resp := h.cached
		h.mu.Unlock()
		return resp, nil
	}
	h.mu.Unlock()

	rows, err := h.store.ListActive(ctx)
	if err != nil {
		// Serve a stale value rather than an error if we ever had one — same
		// "decorative endpoint degrades, never fails" convention as
		// PublicListStatsHandler.value. The home page's own fallback to the
		// compiled-in CRT_SESSION constant only engages on a genuine
		// non-OK/network failure; serving stale rows here is strictly better
		// than forcing that fallback over a transient DB blip.
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.haveOnce {
			return h.cached, nil
		}
		return publicCrtSessionResponse{}, err
	}

	views := make([]publicCrtCommandView, 0, len(rows))
	for _, c := range rows {
		views = append(views, toPublicCrtCommandView(c))
	}
	resp := publicCrtSessionResponse{Commands: views}

	h.mu.Lock()
	h.cached = resp
	h.cachedAt = h.now()
	h.haveOnce = true
	h.mu.Unlock()
	return resp, nil
}
