package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// fakeResolver maps a secret to its token.
type fakeResolver map[string]apitokens.Token

func (f fakeResolver) Resolve(_ context.Context, secret string, _ time.Time) (apitokens.Token, error) {
	if t, ok := f[secret]; ok {
		return t, nil
	}
	return apitokens.Token{}, apitokens.ErrNotFound
}

// memIdem is an in-memory IdempotencyStore with the Postgres store's rules.
type memIdem struct {
	mu   sync.Mutex
	rows map[string]*memIdemRow
}

type memIdemRow struct {
	op     string
	hash   []byte
	status int
	body   []byte
}

func (m *memIdem) k(id int64, key string) string { return strconv.FormatInt(id, 10) + "\x00" + key }

func (m *memIdem) Begin(_ context.Context, id int64, key, op string, hash []byte, _ time.Time) (*StoredResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]*memIdemRow{}
	}
	r, ok := m.rows[m.k(id, key)]
	if !ok {
		m.rows[m.k(id, key)] = &memIdemRow{op: op, hash: hash}
		return nil, nil
	}
	switch {
	case r.op != op || !bytes.Equal(r.hash, hash):
		return nil, ErrIdempotencyMismatch
	case r.status == 0:
		return nil, ErrIdempotencyInFlight
	}
	return &StoredResponse{Status: r.status, Body: r.body}, nil
}

func (m *memIdem) Complete(_ context.Context, id int64, key string, status int, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rows[m.k(id, key)]
	r.status, r.body = status, body
	return nil
}

func (m *memIdem) Abandon(_ context.Context, id int64, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, m.k(id, key))
	return nil
}

type item struct {
	ID int `json:"id"`
}

type listIn struct {
	Page
}

type createIn struct {
	Title string `json:"title"`
	Fail  string `json:"fail,omitempty"`
}

type createOut struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

type getIn struct {
	ID int64 `json:"id" path:"id"`
}

// env is a router over test ops: test_list (120 items, keyset cursor),
// test_create (POST, idempotent, counts calls), test_get ({id}), and
// test_empty (an empty list).
type env struct {
	h        http.Handler
	readKey  string
	writeKey string
	creates  int
	listN    int
}

func newEnv(t *testing.T, limiter *middleware.RateLimiter) *env {
	t.Helper()
	e := &env{listN: 120}
	e.readKey, _ = apitokens.Generate()
	e.writeKey, _ = apitokens.Generate()
	otherKey, _ := apitokens.Generate()
	res := fakeResolver{
		e.readKey:  {ID: 1, Name: "reader", Scopes: []string{apitokens.ScopeGuidePostsRead}},
		e.writeKey: {ID: 2, Name: "writer", Scopes: []string{apitokens.ScopeGuidePostsWrite}},
		otherKey:   {ID: 3, Name: "other", Scopes: []string{apitokens.ScopeVenuesRead}},
	}
	reg := NewRegistry(Config{Version: "9.9.9", Idempotency: &memIdem{}})
	reg.Add(
		Define(Operation{Name: "test_list", Method: http.MethodGet, Path: "/api/v1/items", Scope: apitokens.ScopeGuidePostsRead, Summary: "List."},
			func(_ context.Context, in listIn) (List[item], error) {
				limit, err := in.Resolve()
				if err != nil {
					return List[item]{}, err
				}
				var after struct {
					ID int `json:"id"`
				}
				if in.Cursor != "" {
					if err := DecodeCursor(in.Cursor, &after); err != nil {
						return List[item]{}, err
					}
				}
				var items []item
				for id := after.ID + 1; id <= e.listN && len(items) < limit; id++ {
					items = append(items, item{ID: id})
				}
				var next *string
				if len(items) == limit && items[len(items)-1].ID < e.listN {
					c := EncodeCursor(map[string]int{"id": items[len(items)-1].ID})
					next = &c
				}
				return NewList(items, next), nil
			}),
		Define(Operation{Name: "test_create", Method: http.MethodPost, Path: "/api/v1/items", Scope: apitokens.ScopeGuidePostsWrite,
			Summary: "Create.", Idempotency: true, Status: http.StatusCreated},
			func(_ context.Context, in createIn) (createOut, error) {
				e.creates++
				switch in.Fail {
				case "conflict":
					return createOut{}, Conflict("already exists")
				case "validation":
					return createOut{}, Validation(map[string]string{"title": "is taken"})
				case "sql":
					return createOut{}, errors.New(`pq: relation "secret_table" does not exist`)
				}
				return createOut{ID: e.creates, Title: in.Title}, nil
			}),
		Define(Operation{Name: "test_get", Method: http.MethodGet, Path: "/api/v1/items/{id}", Scope: apitokens.ScopeGuidePostsRead, Summary: "Get."},
			func(_ context.Context, in getIn) (item, error) {
				if in.ID > 120 {
					return item{}, NotFound("item")
				}
				return item{ID: int(in.ID)}, nil
			}),
	)
	reg.Add(CoreOperations("9.9.9")...)
	if limiter == nil {
		limiter = middleware.NewAPITokenRateLimiter()
	}
	e.h = NewHTTPRouter(reg, RouterDeps{Resolver: res, Limiter: limiter, BaseURL: "https://example.test"})
	return e
}

type resp struct {
	code int
	hdr  http.Header
	body []byte
}

func (e *env) do(t *testing.T, method, path, key, body string, hdr ...string) resp {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return resp{rec.Code, rec.Header(), rec.Body.Bytes()}
}

// errBody decodes the envelope, failing on any other top-level key.
func errBody(t *testing.T, b []byte) ErrorDetail {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil || len(top) != 1 || top["error"] == nil {
		t.Fatalf("not an error envelope: %s (%v)", b, err)
	}
	var e ErrorBody
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	return e.Error
}

func quietLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestRouterErrorCodes(t *testing.T) {
	logs := quietLogs(t)
	e := newEnv(t, nil)
	unknown, _ := apitokens.Generate()
	cookieReq := func() resp {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/items", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "a-session"})
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return resp{rec.Code, rec.Header(), rec.Body.Bytes()}
	}
	cases := []struct {
		name   string
		r      resp
		status int
		code   string
	}{
		{"no token", e.do(t, "GET", "/api/v1/items", "", ""), 401, CodeUnauthorized},
		{"malformed token", e.do(t, "GET", "/api/v1/items", "not-a-token", ""), 401, CodeUnauthorized},
		{"unknown token", e.do(t, "GET", "/api/v1/items", unknown, ""), 401, CodeUnauthorized},
		{"session cookie only", cookieReq(), 401, CodeUnauthorized},
		{"missing scope", e.do(t, "POST", "/api/v1/items", e.readKey, `{"title":"x"}`), 403, CodeForbidden},
		{"unknown path", e.do(t, "GET", "/api/v1/nope", e.readKey, ""), 404, CodeNotFound},
		{"unknown path, no token", e.do(t, "GET", "/api/v1/nope", "", ""), 404, CodeNotFound},
		{"wrong method", e.do(t, "DELETE", "/api/v1/items", e.writeKey, ""), 405, CodeMethodNotAllowed},
		{"validation", e.do(t, "POST", "/api/v1/items", e.writeKey, `{"titel":"x"}`), 400, CodeValidation},
		{"op validation", e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x","fail":"validation"}`), 400, CodeValidation},
		{"not found", e.do(t, "GET", "/api/v1/items/500", e.readKey, ""), 404, CodeNotFound},
		{"conflict", e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x","fail":"conflict"}`), 409, CodeConflict},
		{"raw error", e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x","fail":"sql"}`), 500, CodeInternal},
	}
	for _, c := range cases {
		if c.r.code != c.status {
			t.Errorf("%s: status %d, want %d (%s)", c.name, c.r.code, c.status, c.r.body)
			continue
		}
		if got := errBody(t, c.r.body); got.Code != c.code || got.Message == "" {
			t.Errorf("%s: %+v, want code %s", c.name, got, c.code)
		}
		if c.status == 401 && c.r.hdr.Get("WWW-Authenticate") == "" {
			t.Errorf("%s: no WWW-Authenticate", c.name)
		}
		if c.r.hdr.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", c.name, c.r.hdr.Get("Cache-Control"))
		}
	}
	if d := errBody(t, cases[8].r.body); d.Fields["titel"] != "unknown field" {
		t.Errorf("validation fields %v", d.Fields)
	}
	if allow := cases[7].r.hdr.Get("Allow"); allow != "GET, POST" {
		t.Errorf("Allow %q", allow)
	}
	raw := cases[len(cases)-1].r.body
	if bytes.Contains(raw, []byte("pq:")) || bytes.Contains(raw, []byte("secret_table")) {
		t.Errorf("raw error leaked: %s", raw)
	}
	if !strings.Contains(logs.String(), "secret_table") {
		t.Error("raw error was not logged")
	}
	// Write implies read.
	if r := e.do(t, "GET", "/api/v1/items/3", e.writeKey, ""); r.code != 200 {
		t.Errorf("write scope on a read op: %d %s", r.code, r.body)
	}
	// Over the rate limit.
	rl := middleware.NewRateLimiter(rate.Every(time.Hour), 1)
	lim := newEnv(t, rl)
	lim.do(t, "GET", "/api/v1/items/1", lim.readKey, "")
	r := lim.do(t, "GET", "/api/v1/items/1", lim.readKey, "")
	if r.code != 429 || errBody(t, r.body).Code != CodeRateLimited || r.hdr.Get("Retry-After") == "" {
		t.Errorf("rate limit: %d %s %v", r.code, r.body, r.hdr)
	}
}

func TestRouterBodyCap(t *testing.T) {
	e := newEnv(t, nil)
	pad := func(n int) string {
		const head, tail = `{"title":"`, `"}`
		return head + strings.Repeat("a", n-len(head)-len(tail)) + tail
	}
	if r := e.do(t, "POST", "/api/v1/items", e.writeKey, pad(MaxBodyBytes)); r.code != 201 {
		t.Fatalf("64 KiB: %d %s", r.code, r.body)
	}
	r := e.do(t, "POST", "/api/v1/items", e.writeKey, pad(MaxBodyBytes+1))
	if r.code != 413 {
		t.Fatalf("64 KiB + 1: %d", r.code)
	}
	if d := errBody(t, r.body); d.Code != CodeValidation || d.Fields["body"] == "" {
		t.Fatalf("413 body %+v", d)
	}
}

func TestRouterRequiresJSONContentType(t *testing.T) {
	e := newEnv(t, nil)
	r := e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x"}`, "Content-Type", "text/plain")
	if r.code != 415 {
		t.Fatalf("text/plain: %d", r.code)
	}
	if d := errBody(t, r.body); d.Code != CodeValidation || d.Fields["Content-Type"] == "" {
		t.Fatalf("415 body %+v", d)
	}
	if r := e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x"}`, "Content-Type", "application/json; charset=utf-8"); r.code != 201 {
		t.Fatalf("json with charset: %d", r.code)
	}
	// An empty body needs no Content-Type.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/items", nil)
	req.Header.Set("Authorization", "Bearer "+e.writeKey)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("empty body: %d %s", rec.Code, rec.Body)
	}
}

func TestRouterPagination(t *testing.T) {
	e := newEnv(t, nil)
	page := func(q string) (List[item], resp) {
		t.Helper()
		r := e.do(t, "GET", "/api/v1/items"+q, e.readKey, "")
		var l List[item]
		if r.code == 200 {
			if err := json.Unmarshal(r.body, &l); err != nil {
				t.Fatal(err)
			}
		}
		return l, r
	}
	l, r := page("")
	if r.code != 200 || len(l.Items) != 50 || l.NextCursor == nil {
		t.Fatalf("default: %d items, next %v", len(l.Items), l.NextCursor)
	}
	l, _ = page("?limit=200")
	if len(l.Items) != 120 || l.NextCursor != nil || !bytes.Contains(e.do(t, "GET", "/api/v1/items?limit=200", e.readKey, "").body, []byte(`"next_cursor":null`)) {
		t.Fatalf("limit=200: %d items, next %v", len(l.Items), l.NextCursor)
	}
	for _, q := range []string{"?limit=0", "?limit=201", "?limit=x"} {
		if _, r := page(q); r.code != 400 || errBody(t, r.body).Fields["limit"] == "" {
			t.Errorf("%s: %d %s", q, r.code, r.body)
		}
	}
	seen := map[int]int{}
	q := "?limit=7"
	for n := 0; n < 100; n++ {
		l, r := page(q)
		if r.code != 200 {
			t.Fatalf("walk: %d %s", r.code, r.body)
		}
		for _, it := range l.Items {
			seen[it.ID]++
		}
		if l.NextCursor == nil {
			break
		}
		q = "?limit=7&cursor=" + *l.NextCursor
	}
	if len(seen) != 120 {
		t.Fatalf("walk saw %d ids", len(seen))
	}
	for id, c := range seen {
		if c != 1 {
			t.Fatalf("id %d seen %d times", id, c)
		}
	}
	if _, r := page("?cursor=garbage!"); r.code != 400 || errBody(t, r.body).Fields["cursor"] == "" {
		t.Errorf("garbage cursor: %d %s", r.code, r.body)
	}
	e.listN = 0
	if r := e.do(t, "GET", "/api/v1/items", e.readKey, ""); !bytes.Contains(r.body, []byte(`"items":[]`)) {
		t.Errorf("empty list: %s", r.body)
	}
}

func TestRouterIdempotentReplay(t *testing.T) {
	e := newEnv(t, nil)
	a := e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x"}`, "Idempotency-Key", "k1")
	b := e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x"}`, "Idempotency-Key", "k1")
	if a.code != 201 || b.code != a.code || !bytes.Equal(a.body, b.body) {
		t.Fatalf("replay: %d %s / %d %s", a.code, a.body, b.code, b.body)
	}
	if b.hdr.Get("Idempotent-Replayed") != "true" || a.hdr.Get("Idempotent-Replayed") != "" {
		t.Fatalf("Idempotent-Replayed %q / %q", a.hdr.Get("Idempotent-Replayed"), b.hdr.Get("Idempotent-Replayed"))
	}
	if e.creates != 1 {
		t.Fatalf("op ran %d times", e.creates)
	}
	// No key: every call runs.
	e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x"}`)
	if e.creates != 2 {
		t.Fatalf("keyless call did not run")
	}
	// A bad key is a validation error naming the header.
	if r := e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x"}`, "Idempotency-Key", strings.Repeat("k", 256)); r.code != 400 || errBody(t, r.body).Fields["Idempotency-Key"] == "" {
		t.Fatalf("long key: %d %s", r.code, r.body)
	}
}

func TestRouterIdempotencyMismatch409(t *testing.T) {
	e := newEnv(t, nil)
	e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x"}`, "Idempotency-Key", "k1")
	r := e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"y"}`, "Idempotency-Key", "k1")
	if r.code != 409 || errBody(t, r.body).Code != CodeConflict || e.creates != 1 {
		t.Fatalf("mismatch: %d %s, creates %d", r.code, r.body, e.creates)
	}
}

func TestRouterIdempotencyFailureNotReplayed(t *testing.T) {
	e := newEnv(t, nil)
	body := `{"title":"x","fail":"conflict"}`
	if r := e.do(t, "POST", "/api/v1/items", e.writeKey, body, "Idempotency-Key", "k1"); r.code != 409 {
		t.Fatalf("first: %d", r.code)
	}
	r := e.do(t, "POST", "/api/v1/items", e.writeKey, body, "Idempotency-Key", "k1")
	if r.code != 409 || r.hdr.Get("Idempotent-Replayed") != "" || e.creates != 2 {
		t.Fatalf("retry after failure: %d replayed %q creates %d", r.code, r.hdr.Get("Idempotent-Replayed"), e.creates)
	}
}

func TestRouterNoStoreHeader(t *testing.T) {
	e := newEnv(t, nil)
	for _, r := range []resp{
		e.do(t, "GET", "/api/v1/items", e.readKey, ""),
		e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x"}`),
		e.do(t, "POST", "/api/v1/items", e.writeKey, `{"bad":1}`),
		e.do(t, "GET", "/api/v1/nope", "", ""),
		e.do(t, "GET", "/api/v1/items", "", ""),
	} {
		if r.hdr.Get("Cache-Control") != "no-store" {
			t.Errorf("%d %s: Cache-Control %q", r.code, r.body, r.hdr.Get("Cache-Control"))
		}
	}
	rec := httptest.NewRecorder()
	Serve(getOp("a", "/api/v1/a", apitokens.ScopeEngagementRead)).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/a", nil))
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("Serve does not set no-store")
	}
}

// TestServeUnavailableAndClientIP: Unavailable is a 503 "unavailable", and
// the op sees the caller's IP (for its audit entry).
func TestServeUnavailableAndClientIP(t *testing.T) {
	var gotIP string
	op := Define(Operation{Name: "u", Method: http.MethodGet, Path: "/api/v1/u", Scope: apitokens.ScopeEngagementRead, Summary: "s", MayBeUnavailable: true},
		func(ctx context.Context, _ emptyIn) (okOut, error) {
			gotIP = ClientIPFromContext(ctx)
			return okOut{}, Unavailable("ShortLinks is not responding")
		})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/u", nil)
	req.RemoteAddr = "203.0.113.9:4242"
	rec := httptest.NewRecorder()
	Serve(op).ServeHTTP(rec, req)
	if rec.Code != 503 || errBody(t, rec.Body.Bytes()).Code != CodeUnavailable {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if gotIP != "203.0.113.9" {
		t.Fatalf("client IP %q", gotIP)
	}
}

// TestRouterIdempotencyQueryIsPartOfRequest (#0675 review): the request hash
// covers the query string, so a reused key with a different query is a
// mismatch rather than a replay of the first response.
func TestRouterIdempotencyQueryIsPartOfRequest(t *testing.T) {
	e := newEnv(t, nil)
	e.do(t, "POST", "/api/v1/items", e.writeKey, `{"title":"x"}`, "Idempotency-Key", "k1")
	r := e.do(t, "POST", "/api/v1/items?dry=1", e.writeKey, `{"title":"x"}`, "Idempotency-Key", "k1")
	if r.code != 409 || r.hdr.Get("Idempotent-Replayed") != "" || e.creates != 1 {
		t.Fatalf("query changed: %d replayed %q creates %d", r.code, r.hdr.Get("Idempotent-Replayed"), e.creates)
	}
}

// TestAsErrorTypedNilAndBadStatus (#0675 review): a typed-nil *Error or one
// without an error status is an operation bug; it becomes the 500, never a
// nil dereference or a WriteHeader panic.
func TestAsErrorTypedNilAndBadStatus(t *testing.T) {
	quietLogs(t)
	var typedNil *Error
	for name, err := range map[string]error{
		"typed nil":   typedNil,
		"zero status": &Error{Code: CodeConflict, Message: "x"},
		"2xx status":  &Error{Status: 200, Code: CodeConflict},
	} {
		if got := AsError("t", err); got == nil || got.Status != 500 || got.Code != CodeInternal {
			t.Errorf("%s: AsError = %+v, want the 500", name, got)
		}
	}
	if got := AsError("t", Conflict("x")); got.Status != 409 {
		t.Errorf("a real *Error: %+v", got)
	}
}

// TestDecodeCursorRejectsTrailingData (#0675 review): a cursor is exactly
// one JSON value this API encoded.
func TestDecodeCursorRejectsTrailingData(t *testing.T) {
	var v struct {
		ID int `json:"id"`
	}
	if err := DecodeCursor(EncodeCursor(map[string]int{"id": 4}), &v); err != nil || v.ID != 4 {
		t.Fatalf("round trip: %v %d", err, v.ID)
	}
	forged := base64.RawURLEncoding.EncodeToString([]byte(`{"id":4}{"id":9}`))
	if err := DecodeCursor(forged, &v); err == nil {
		t.Fatal("trailing data accepted")
	}
}
