package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

type fakeListStatsStore struct {
	counts map[string]int64
	err    error
	calls  int
}

func (f *fakeListStatsStore) StatusCounts(context.Context) (map[string]int64, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.counts, nil
}

func getStats(t *testing.T, h *PublicListStatsHandler) (int, listStatsResponse, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Stats(rec, httptest.NewRequest(http.MethodGet, "/api/list-stats", nil))
	var out listStatsResponse
	body := rec.Body.String()
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("decoding %q: %v", body, err)
		}
	}
	return rec.Code, out, body
}

// The oracle mitigation, pinned. #0274: a live EXACT pending count would let
// someone submit an address and poll to learn whether it was already known --
// a narrow reopening of what POST /api/subscribe's uniform 202 exists to
// prevent (CLAUDE.md §9). Bucketing pending is what stops one submission from
// moving the reported number.
func TestListStats_PendingIsBucketedSoOneSignupDoesNotMoveIt(t *testing.T) {
	for _, pending := range []int64{5, 6, 7, 8, 9} {
		store := &fakeListStatsStore{counts: map[string]int64{
			subscribers.StatusActive:  42,
			subscribers.StatusPending: pending,
		}}
		h := NewPublicListStatsHandler(store)
		code, got, body := getStats(t, h)
		if code != http.StatusOK {
			t.Fatalf("pending=%d: status %d, body %q", pending, code, body)
		}
		if got.Pending != 5 {
			t.Errorf("pending=%d reported as %d, want 5 — a single signup must not move the reported value", pending, got.Pending)
		}
	}
}

// confirmed is exact and must stay exact: an attacker cannot move it for an
// address they do not control, because confirming requires clicking a link in
// an email only the owner receives.
func TestListStats_ConfirmedIsExact(t *testing.T) {
	store := &fakeListStatsStore{counts: map[string]int64{
		subscribers.StatusActive:  43,
		subscribers.StatusPending: 0,
	}}
	code, got, body := getStats(t, NewPublicListStatsHandler(store))
	if code != http.StatusOK {
		t.Fatalf("status %d, body %q", code, body)
	}
	if got.Confirmed != 43 {
		t.Errorf("confirmed = %d, want exactly 43", got.Confirmed)
	}
}

// The response must never carry anything that could identify a signup. This
// asserts on the serialised bytes rather than the struct, so adding a field
// later fails here rather than shipping.
func TestListStats_ResponseCarriesOnlyTwoAggregateFields(t *testing.T) {
	store := &fakeListStatsStore{counts: map[string]int64{
		subscribers.StatusActive:     42,
		subscribers.StatusPending:    10,
		subscribers.StatusBounced:    3,
		subscribers.StatusComplained: 1,
	}}
	_, _, body := getStats(t, NewPublicListStatsHandler(store))
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decoding %q: %v", body, err)
	}
	if len(raw) != 2 {
		t.Fatalf("response has %d fields (%q), want exactly confirmed and pending", len(raw), body)
	}
	for _, k := range []string{"confirmed", "pending"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing %q in %q", k, body)
		}
	}
	for _, forbidden := range []string{"@", "email", "address", "id", "created", "bounced", "complained"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("response %q contains %q — this endpoint is public and must stay aggregate-only", body, forbidden)
		}
	}
}

// The cache is the second half of the mitigation and keeps a per-visitor page
// from becoming a per-visitor query.
func TestListStats_CachesWithinTTL(t *testing.T) {
	store := &fakeListStatsStore{counts: map[string]int64{subscribers.StatusActive: 1}}
	h := NewPublicListStatsHandler(store)
	now := time.Now()
	h.now = func() time.Time { return now }

	getStats(t, h)
	getStats(t, h)
	if store.calls != 1 {
		t.Fatalf("store queried %d times within the TTL, want 1", store.calls)
	}

	now = now.Add(listStatsTTL + time.Second)
	getStats(t, h)
	if store.calls != 2 {
		t.Fatalf("store queried %d times after the TTL expired, want 2", store.calls)
	}
}

// A decorative endpoint should degrade, not fail: once it has a value, a later
// store error serves the stale one rather than a 503.
func TestListStats_ServesStaleRatherThanFailingOnceWarm(t *testing.T) {
	store := &fakeListStatsStore{counts: map[string]int64{subscribers.StatusActive: 7}}
	h := NewPublicListStatsHandler(store)
	now := time.Now()
	h.now = func() time.Time { return now }

	if code, got, _ := getStats(t, h); code != http.StatusOK || got.Confirmed != 7 {
		t.Fatalf("warm-up: code %d confirmed %d", code, got.Confirmed)
	}

	store.err = errors.New("database down")
	now = now.Add(listStatsTTL + time.Second)
	code, got, body := getStats(t, h)
	if code != http.StatusOK {
		t.Fatalf("status %d (%q), want the stale value served", code, body)
	}
	if got.Confirmed != 7 {
		t.Errorf("confirmed = %d, want the stale 7", got.Confirmed)
	}
}

// Cold, with no value ever cached, an error is honest rather than a fabricated
// zero — "0 confirmed" would be a claim, not an absence.
func TestListStats_ColdStoreErrorIsNotReportedAsZero(t *testing.T) {
	store := &fakeListStatsStore{err: errors.New("database down")}
	code, _, body := getStats(t, NewPublicListStatsHandler(store))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status %d (%q), want 503", code, body)
	}
	if strings.Contains(body, "confirmed") {
		t.Errorf("cold error body %q reports counts; it must not fabricate them", body)
	}
}
