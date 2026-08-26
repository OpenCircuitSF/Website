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
// §1): the same aggregate query the admin dashboard uses, nothing more.
type ListStatsStore interface {
	StatusCounts(ctx context.Context) (map[string]int64, error)
}

// listStatsResponse is deliberately two integers. No addresses, no ids, no
// timestamps — nothing that could identify a signup, which is what makes this
// endpoint safe to serve publicly (#0274).
type listStatsResponse struct {
	Confirmed int64 `json:"confirmed"`
	Pending   int64 `json:"pending"`
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

	resp := listStatsResponse{
		Confirmed: maxInt64(counts[subscribers.StatusActive], 0),
		Pending:   bucketDown(counts[subscribers.StatusPending], pendingBucket),
	}

	h.mu.Lock()
	h.cached = resp
	h.cachedAt = h.now()
	h.haveOnce = true
	h.mu.Unlock()
	return resp, nil
}
