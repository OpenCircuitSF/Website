package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/crt"
)

type fakeCrtCommandStore struct {
	rows  []crt.Command
	err   error
	calls int
}

func (f *fakeCrtCommandStore) ListActive(context.Context) ([]crt.Command, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func getCrtSession(t *testing.T, h *PublicCrtSessionHandler) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/crt-session", nil))
	return rec.Code, rec.Body.String()
}

// The public shape must carry exactly cmd/out/source — no id, no active
// flag, no timestamps — asserted against the encoded JSON bytes rather than
// the Go struct, per #0393's acceptance criteria (a field added to the view
// struct later must fail this test, not just go unnoticed).
func TestPublicCrtSession_ResponseShapeCarriesOnlyCmdOutSource(t *testing.T) {
	now := time.Now()
	store := &fakeCrtCommandStore{rows: []crt.Command{
		{ID: 42, Slug: "whoami", Command: "whoami", Output: "line one\nline two", Source: crt.SourceStatic, SortOrder: 10, Active: true, CreatedAt: now, UpdatedAt: &now},
	}}
	h := NewPublicCrtSessionHandler(store)
	code, body := getCrtSession(t, h)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", code, body)
	}

	var raw struct {
		Commands []map[string]any `json:"commands"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if len(raw.Commands) != 1 {
		t.Fatalf("got %d commands, want 1 (body=%s)", len(raw.Commands), body)
	}
	entry := raw.Commands[0]
	if len(entry) != 3 {
		t.Fatalf("entry has %d fields (%v), want exactly cmd/out/source", len(entry), entry)
	}
	for _, forbidden := range []string{"id", "active", "created_at", "updated_at", "sort_order", "slug"} {
		if _, present := entry[forbidden]; present {
			t.Errorf("public crt session entry carries admin-only field %q: %v", forbidden, entry)
		}
	}
	for _, required := range []string{"cmd", "out", "source"} {
		if _, present := entry[required]; !present {
			t.Errorf("public crt session entry missing %q: %v", required, entry)
		}
	}
	if out, ok := entry["out"].([]any); !ok || len(out) != 2 {
		t.Errorf(`entry["out"] = %v, want a 2-element array ["line one","line two"]`, entry["out"])
	}
}

// Active rows come back in sort_order (ListActive's own ordering
// contract) — this test proves the handler preserves whatever order the
// store returned rather than re-sorting or reversing it.
func TestPublicCrtSession_PreservesStoreOrder(t *testing.T) {
	store := &fakeCrtCommandStore{rows: []crt.Command{
		{Command: "first", Output: "a", Source: crt.SourceStatic},
		{Command: "second", Output: "b", Source: crt.SourceStatic},
		{Command: "third", Output: "c", Source: crt.SourceStatic},
	}}
	h := NewPublicCrtSessionHandler(store)
	_, body := getCrtSession(t, h)
	var raw struct {
		Commands []struct {
			Cmd string `json:"cmd"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	want := []string{"first", "second", "third"}
	if len(raw.Commands) != len(want) {
		t.Fatalf("got %d commands, want %d", len(raw.Commands), len(want))
	}
	for i, w := range want {
		if raw.Commands[i].Cmd != w {
			t.Errorf("commands[%d].cmd = %q, want %q", i, raw.Commands[i].Cmd, w)
		}
	}
}

// The cache is the same 60s-TTL convention PublicListStatsHandler uses, and
// this endpoint is hit by every home-page visitor.
func TestPublicCrtSession_CachesWithinTTL(t *testing.T) {
	store := &fakeCrtCommandStore{rows: []crt.Command{{Command: "x", Output: "y", Source: crt.SourceStatic}}}
	h := NewPublicCrtSessionHandler(store)
	now := time.Now()
	h.now = func() time.Time { return now }

	getCrtSession(t, h)
	getCrtSession(t, h)
	if store.calls != 1 {
		t.Fatalf("store queried %d times within the TTL, want 1", store.calls)
	}

	now = now.Add(crtSessionTTL + time.Second)
	getCrtSession(t, h)
	if store.calls != 2 {
		t.Fatalf("store queried %d times after the TTL expired, want 2", store.calls)
	}
}

// A decorative endpoint should degrade, not fail: once it has a value, a
// later store error serves the stale one rather than a 503 — matching
// PublicListStatsHandler's own convention exactly.
func TestPublicCrtSession_ServesStaleRatherThanFailingOnceWarm(t *testing.T) {
	store := &fakeCrtCommandStore{rows: []crt.Command{{Command: "warm", Output: "y", Source: crt.SourceStatic}}}
	h := NewPublicCrtSessionHandler(store)
	now := time.Now()
	h.now = func() time.Time { return now }

	if code, body := getCrtSession(t, h); code != http.StatusOK {
		t.Fatalf("warm-up: code %d body %q", code, body)
	}

	store.err = errors.New("database down")
	now = now.Add(crtSessionTTL + time.Second)
	code, body := getCrtSession(t, h)
	if code != http.StatusOK {
		t.Fatalf("status %d (%q), want the stale value served", code, body)
	}
	if !containsCmd(body, "warm") {
		t.Errorf("body %q does not carry the stale \"warm\" command", body)
	}
}

// Cold, with no value ever cached, an error answers 503 rather than a
// fabricated empty session -- this is precisely the STORAGE=json / pre-seed
// deploy shape the SPA's CRT_SESSION fallback exists for; the handler itself
// must not paper over it with a fake 200.
func TestPublicCrtSession_ColdStoreErrorReturns503(t *testing.T) {
	store := &fakeCrtCommandStore{err: errors.New("database down")}
	h := NewPublicCrtSessionHandler(store)
	code, body := getCrtSession(t, h)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status %d (%q), want 503", code, body)
	}
}

func containsCmd(body, cmd string) bool {
	var raw struct {
		Commands []struct {
			Cmd string `json:"cmd"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return false
	}
	for _, c := range raw.Commands {
		if c.Cmd == cmd {
			return true
		}
	}
	return false
}
