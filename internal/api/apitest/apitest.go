// Package apitest helps test /api/v1 operations (#0675) without a database:
// a fake token resolver, an in-memory idempotency store, and a router over
// the operations under test, with the real auth, rate-limit, scope, decode
// and error-envelope path in front of them. #0676–#0678's handler tests use
// it so each does not write its own.
package apitest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// Resolver is an in-memory middleware.APITokenResolver.
type Resolver struct {
	mu     sync.Mutex
	tokens map[string]apitokens.Token
	nextID int64
}

// NewResolver returns an empty Resolver.
func NewResolver() *Resolver { return &Resolver{tokens: map[string]apitokens.Token{}} }

// Key mints a key named name holding scopes and returns its secret and
// token. IDs start at 1 and count up.
func (r *Resolver) Key(t testing.TB, name string, scopes ...string) (string, apitokens.Token) {
	t.Helper()
	secret, err := apitokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	tok := apitokens.Token{ID: r.nextID, Name: name, Prefix: secret[:12], Scopes: scopes, CreatedAt: time.Now()}
	r.tokens[secret] = tok
	return secret, tok
}

// Resolve implements middleware.APITokenResolver.
func (r *Resolver) Resolve(_ context.Context, secret string, now time.Time) (apitokens.Token, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tokens[secret]
	if !ok || (t.ExpiresAt != nil && !now.Before(*t.ExpiresAt)) {
		return apitokens.Token{}, apitokens.ErrNotFound
	}
	return t, nil
}

// MemoryIdempotency is an in-memory api.IdempotencyStore with the Postgres
// store's rules, minus the 24-hour expiry.
type MemoryIdempotency struct {
	mu   sync.Mutex
	rows map[string]*memRow
}

type memRow struct {
	op     string
	hash   []byte
	status int
	body   []byte
}

func memKey(id int64, key string) string { return strconv.FormatInt(id, 10) + "\x00" + key }

// Begin implements api.IdempotencyStore.
func (m *MemoryIdempotency) Begin(_ context.Context, id int64, key, op string, hash []byte, _ time.Time) (*api.StoredResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]*memRow{}
	}
	r, ok := m.rows[memKey(id, key)]
	if !ok {
		m.rows[memKey(id, key)] = &memRow{op: op, hash: hash}
		return nil, nil
	}
	switch {
	case r.op != op || !bytes.Equal(r.hash, hash):
		return nil, api.ErrIdempotencyMismatch
	case r.status == 0:
		return nil, api.ErrIdempotencyInFlight
	}
	return &api.StoredResponse{Status: r.status, Body: r.body}, nil
}

// Complete implements api.IdempotencyStore.
func (m *MemoryIdempotency) Complete(_ context.Context, id int64, key string, status int, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.rows[memKey(id, key)]; r != nil {
		r.status, r.body = status, body
	}
	return nil
}

// Abandon implements api.IdempotencyStore.
func (m *MemoryIdempotency) Abandon(_ context.Context, id int64, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, memKey(id, key))
	return nil
}

// Router is api.NewHTTPRouter over ops (plus api.CoreOperations), with res
// as the resolver, a fresh per-token limiter and MemoryIdempotency.
func Router(t testing.TB, res *Resolver, ops ...api.Operation) http.Handler {
	t.Helper()
	reg := api.NewRegistry(api.Config{Version: "test", Idempotency: &MemoryIdempotency{}})
	reg.Add(api.CoreOperations("test")...)
	reg.Add(ops...)
	return api.NewHTTPRouter(reg, api.RouterDeps{Resolver: res, Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})
}

// Do sends one request to h. A non-empty secret is sent as the bearer; a
// non-empty body is sent as application/json. Extra headers go in hdr as
// name, value pairs.
func Do(t testing.TB, h http.Handler, method, path, secret, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ErrorOf decodes body as the v1 error envelope, failing the test unless it
// is exactly that: one top-level "error" key.
func ErrorOf(t testing.TB, body []byte) api.ErrorDetail {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil || len(top) != 1 || top["error"] == nil {
		t.Fatalf("not a v1 error envelope: %s (%v)", body, err)
	}
	var e api.ErrorBody
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatal(err)
	}
	return e.Error
}
