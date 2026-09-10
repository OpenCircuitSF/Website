package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// ListStatsStore is the narrow store interface this handler needs (CLAUDE.md
// §1): the same aggregate query the admin dashboard uses, plus the
// per-interest active-subscriber breakdown #0393 adds below.
type ListStatsStore interface {
	StatusCounts(ctx context.Context) (map[string]int64, error)
	ActiveInterestCounts(ctx context.Context) ([]subscribers.InterestCount, error)
}

// listStatsInterestView is one entry of the interests array below: a slug,
// its display name, and the exact count of active subscribers who currently
// have it selected. No id — the public wire contract has never needed one
// (matching publicInterestView's own convention), and this is the one field
// set #0393 adds to what was otherwise a fixed two-field response.
type listStatsInterestView struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// listStatsResponse. `confirmed` and `pending` are the original two
// integers; no addresses, no ids, no timestamps — nothing that could
// identify a signup, which is what makes this endpoint safe to serve
// publicly (#0274). `interests` (#0393) is the one addition: per-interest
// active-subscriber counts, always present as a list (never null, matching
// every other list endpoint's convention) though it may be empty. See
// value()'s call into subscribers.Store.ActiveInterestCounts for why these
// counts are exact rather than bucketed like pending — the reasoning is
// recorded on that method, not duplicated here.
type listStatsResponse struct {
	Confirmed int64                   `json:"confirmed"`
	Pending   int64                   `json:"pending"`
	Interests []listStatsInterestView `json:"interests"`
}

// PublicListStatsHandler serves GET /api/list-stats: aggregate mailing-list
// counts for the home page's live CRT screen (#0274).
//
// # Why pending is bucketed and confirmed is not
//
// CLAUDE.md §9 forbids weakening POST /api/subscribe's uniform 202, which
// exists so the endpoint cannot be used to test whether an address is already
// on the list. A live, exact `pending` count reopens a narrow version of that:
// submit an address, poll the count, and a +1 says the address was new while
// no change says it was already known. On a busy list the signal drowns; this
// list is quiet, which is the worst case.
//
// `confirmed` carries no such risk and is exact. Confirming requires clicking a
// link in an email only the address's owner receives, so an attacker cannot
// move that number for an address they do not control, and watching it move
// tells them nothing about any address they might be probing.
//
// `pending` is therefore rounded DOWN to a multiple of pendingBucket. One
// submission usually does not move the reported value at all, and when it does
// the boundary is not attributable to any particular submission. Combined with
// the cache TTL below, a submit-then-poll cannot attribute a change.
//
// # Why the per-interest counts (#0393) are exact too
//
// `interests` carries the same exactness as `confirmed`, for the same reason:
// moving an interest's count requires confirming a subscription with that
// interest selected, which requires clicking a link in an email only the
// address's owner receives. An attacker cannot move an interest's count for an
// address they do not control, and selecting an interest is not itself an
// oracle for "is this address already on the list" — the thing the uniform 202
// protects, and the thing bucketing `pending` above closes off. Only `pending`
// needed the bucket; `interests`, like `confirmed`, deliberately does not
// apply one. See subscribers.Store.ActiveInterestCounts's own doc comment for
// the full statement of this argument against the query it actually runs.
//
// The screen this feeds is decorative; single-address precision buys it
// nothing, so the mitigation costs nothing real.
const pendingBucket = 5

// listStatsTTL keeps the endpoint cheap — it is polled by every visitor's home
// page — and is the second half of the oracle mitigation above.
const listStatsTTL = 60 * time.Second

type PublicListStatsHandler struct {
	store ListStatsStore
	now   func() time.Time

	mu       sync.Mutex
	cached   listStatsResponse
	cachedAt time.Time
	haveOnce bool
}

func NewPublicListStatsHandler(store ListStatsStore) *PublicListStatsHandler {
	return &PublicListStatsHandler{store: store, now: time.Now}
}

func bucketDown(n int64, size int64) int64 {
	if n <= 0 || size <= 1 {
		return maxInt64(n, 0)
	}
	return (n / size) * size
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Stats handles GET /api/list-stats.
func (h *PublicListStatsHandler) Stats(w http.ResponseWriter, r *http.Request) {
	resp, err := h.value(r.Context())
	if err != nil {
		http.Error(w, "list stats unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *PublicListStatsHandler) value(ctx context.Context) (listStatsResponse, error) {
	h.mu.Lock()
	if h.haveOnce && h.now().Sub(h.cachedAt) < listStatsTTL {
		resp := h.cached
		h.mu.Unlock()
		return resp, nil
	}
	h.mu.Unlock()

	counts, err := h.store.StatusCounts(ctx)
	if err != nil {
		// Serve a stale value rather than an error if we ever had one: the
		// screen degrading to old counts is better than it degrading to
		// nothing, and this endpoint is decorative.
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.haveOnce {
			return h.cached, nil
		}
		return listStatsResponse{}, err
	}
	interestCounts, err := h.store.ActiveInterestCounts(ctx)
	if err != nil {
		// Same degrade-to-stale-or-error convention as the StatusCounts
		// error above, rather than silently reporting an empty interests
		// array over a real store error — an empty array is itself a claim
		// ("no interest currently has an active subscriber"), and this
		// endpoint must not fabricate one.
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.haveOnce {
			return h.cached, nil
		}
		return listStatsResponse{}, err
	}

	interestViews := make([]listStatsInterestView, 0, len(interestCounts))
	for _, ic := range interestCounts {
		interestViews = append(interestViews, listStatsInterestView{Slug: ic.Slug, Name: ic.Name, Count: ic.Count})
	}
	resp := listStatsResponse{
		Confirmed: maxInt64(counts[subscribers.StatusActive], 0),
		Pending:   bucketDown(counts[subscribers.StatusPending], pendingBucket),
		Interests: interestViews,
	}

	h.mu.Lock()
	h.cached = resp
	h.cachedAt = h.now()
	h.haveOnce = true
	h.mu.Unlock()
	return resp, nil
}
