package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// schemaOf is s, a tool's input schema in whatever form it arrived, as a
// *jsonschema.Schema.
func schemaOf(t *testing.T, s any) *jsonschema.Schema {
	t.Helper()
	if js, ok := s.(*jsonschema.Schema); ok {
		return js
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var js jsonschema.Schema
	if err := json.Unmarshal(b, &js); err != nil {
		t.Fatal(err)
	}
	return &js
}

// withoutIdempotencyKey checks that the tool's input schema has
// idempotency_key, an optional string, exactly when op takes one. It returns
// the schema with that property removed, which must then equal the
// registry's own.
func withoutIdempotencyKey(t *testing.T, op api.Operation, in any) *jsonschema.Schema {
	t.Helper()
	s := schemaOf(t, in)
	p, has := s.Properties[mcpserver.IdempotencyKeyArg]
	if has != op.Idempotency {
		t.Errorf("%s: %s in the input schema = %v, want %v (Idempotency)", op.Name, mcpserver.IdempotencyKeyArg, has, op.Idempotency)
	}
	if !has {
		return s
	}
	if p.Type != "string" {
		t.Errorf("%s: %s type %q, want string", op.Name, mcpserver.IdempotencyKeyArg, p.Type)
	}
	for _, r := range s.Required {
		if r == mcpserver.IdempotencyKeyArg {
			t.Errorf("%s: %s is required, want optional", op.Name, mcpserver.IdempotencyKeyArg)
		}
	}
	c := *s
	c.Properties = map[string]*jsonschema.Schema{}
	for k, v := range s.Properties {
		if k != mcpserver.IdempotencyKeyArg {
			c.Properties[k] = v
		}
	}
	return &c
}

func posts(texts ...string) []map[string]any {
	var out []map[string]any
	for _, s := range texts {
		out = append(out, map[string]any{"text": s})
	}
	return out
}

// TestIdempotencyKeyRetryReplays: a retry with the same key and arguments
// returns the first result byte for byte and does not run the operation
// again, even with the arguments in another order.
func TestIdempotencyKeyRetryReplays(t *testing.T) {
	f := newFixture(t, nil)
	cs, _ := f.connect(t, apitokens.ScopeGuidePostsWrite)
	args := map[string]any{"posts": posts("one", "two"), mcpserver.IdempotencyKeyArg: "retry-0685"}
	first := mcptest.Call(t, cs, "test_create_posts", args)
	if first.IsError {
		t.Fatalf("first call: %s", mcptest.Text(first))
	}
	raw := json.RawMessage(`{"idempotency_key": "retry-0685", "posts": [{"text":"one"},{"text":"two"}]}`)
	second := mcptest.Call(t, cs, "test_create_posts", raw)
	if second.IsError {
		t.Fatalf("retry: %s", mcptest.Text(second))
	}
	if mcptest.Text(first) != mcptest.Text(second) {
		t.Errorf("retry text %s, want the first result %s", mcptest.Text(second), mcptest.Text(first))
	}
	a, _ := json.Marshal(first.StructuredContent)
	b, _ := json.Marshal(second.StructuredContent)
	if !jsonEqual(t, a, b) {
		t.Errorf("retry structured %s, want %s", b, a)
	}
	if f.rec.calls != 1 {
		t.Errorf("the op ran %d times, want 1", f.rec.calls)
	}
	// The key was removed before Decode: the op never saw it, and its
	// unknown-field check did not reject it.
	if len(f.rec.in.Posts) != 2 {
		t.Errorf("op input %+v", f.rec.in)
	}
}

func TestIdempotencyKeyConflicts(t *testing.T) {
	f := newFixture(t, nil)
	cs, _ := f.connect(t, apitokens.ScopeGuidePostsWrite)
	if r := mcptest.Call(t, cs, "test_create_posts", map[string]any{"posts": posts("one"), mcpserver.IdempotencyKeyArg: "k1"}); r.IsError {
		t.Fatalf("first: %s", mcptest.Text(r))
	}
	d := mcptest.ErrorOf(t, mcptest.Call(t, cs, "test_create_posts", map[string]any{"posts": posts("different"), mcpserver.IdempotencyKeyArg: "k1"}))
	if d.Code != api.CodeConflict || !strings.Contains(d.Message, "different request") {
		t.Errorf("same key, different arguments = %+v, want a conflict", d)
	}
	if f.rec.calls != 1 {
		t.Errorf("the op ran %d times, want 1", f.rec.calls)
	}
	// The key is per API key: another key may use the same string.
	other, _ := f.connect(t, apitokens.ScopeGuidePostsWrite)
	if r := mcptest.Call(t, other, "test_create_posts", map[string]any{"posts": posts("different"), mcpserver.IdempotencyKeyArg: "k1"}); r.IsError {
		t.Errorf("another API key, same idempotency key: %s", mcptest.Text(r))
	}
}

func TestIdempotencyKeyAbsentOrInvalid(t *testing.T) {
	f := newFixture(t, nil)
	cs, _ := f.connect(t, apitokens.ScopeGuidePostsWrite)
	for i := 0; i < 2; i++ {
		if r := mcptest.Call(t, cs, "test_create_posts", map[string]any{"posts": posts("one")}); r.IsError {
			t.Fatalf("no key: %s", mcptest.Text(r))
		}
	}
	if f.rec.calls != 2 {
		t.Errorf("without a key the op ran %d times, want 2", f.rec.calls)
	}
	for name, key := range map[string]any{"empty": "", "number": 7, "control": "a\nb", "long": strings.Repeat("k", 256)} {
		d := mcptest.ErrorOf(t, mcptest.Call(t, cs, "test_create_posts", map[string]any{"posts": posts("one"), mcpserver.IdempotencyKeyArg: key}))
		if d.Code != api.CodeValidation || d.Fields[mcpserver.IdempotencyKeyArg] == "" {
			t.Errorf("%s key = %+v, want a validation error naming %s", name, d, mcpserver.IdempotencyKeyArg)
		}
	}
	if f.rec.calls != 2 {
		t.Errorf("an invalid key ran the op (%d calls)", f.rec.calls)
	}
	// Unknown fields other than the key are still rejected.
	d := mcptest.ErrorOf(t, mcptest.Call(t, cs, "test_create_posts", map[string]any{"posts": posts("one"), mcpserver.IdempotencyKeyArg: "k", "bogus": 1}))
	if d.Code != api.CodeValidation || d.Fields["bogus"] == "" {
		t.Errorf("unknown field beside a key = %+v", d)
	}
	// A tool whose operation takes no key rejects the argument.
	read, _ := f.connect(t, apitokens.ScopeEventsRead)
	d = mcptest.ErrorOf(t, mcptest.Call(t, read, "test_list_events", map[string]any{mcpserver.IdempotencyKeyArg: "k"}))
	if d.Code != api.CodeValidation || d.Fields[mcpserver.IdempotencyKeyArg] == "" {
		t.Errorf("key on a non-idempotent tool = %+v", d)
	}
}

// TestIdempotencyFailureForgetsKey: an operation that fails releases its
// key, so the retry runs again (REST's Abandon).
func TestIdempotencyFailureForgetsKey(t *testing.T) {
	res := apitest.NewResolver()
	var calls atomic.Int32
	op := api.Define(api.Operation{
		Name: "test_flaky_create", Method: http.MethodPost, Path: "/api/v1/test-flaky", Scope: apitokens.ScopeGuidePostsWrite,
		Summary: "Create, failing the first time.", Idempotency: true, Status: http.StatusCreated,
	}, func(context.Context, postIn) (deleteOut, error) {
		if calls.Add(1) == 1 {
			return deleteOut{}, api.Unavailable("try again")
		}
		return deleteOut{Deleted: true}, nil
	})
	secret, _ := res.Key(t, "flaky", apitokens.ScopeGuidePostsWrite)
	cs := mcptest.Connect(t, mcptest.Handler(t, res, op), secret)
	args := map[string]any{"text": "x", mcpserver.IdempotencyKeyArg: "flaky-1"}
	if r := mcptest.Call(t, cs, "test_flaky_create", args); !r.IsError {
		t.Fatalf("first call succeeded: %s", mcptest.Text(r))
	}
	if r := mcptest.Call(t, cs, "test_flaky_create", args); r.IsError {
		t.Fatalf("retry after a failure: %s", mcptest.Text(r))
	}
	if r := mcptest.Call(t, cs, "test_flaky_create", args); r.IsError || calls.Load() != 2 {
		t.Fatalf("third call: %s, %d calls, want a replay and 2 calls", mcptest.Text(r), calls.Load())
	}
}

// TestIdempotencyInFlightConflicts: a retry while the first call is still
// running is a conflict, not a second run.
func TestIdempotencyInFlightConflicts(t *testing.T) {
	res := apitest.NewResolver()
	started, release := make(chan struct{}), make(chan struct{})
	op := api.Define(api.Operation{
		Name: "test_slow_create", Method: http.MethodPost, Path: "/api/v1/test-slow", Scope: apitokens.ScopeGuidePostsWrite,
		Summary: "Create slowly.", Idempotency: true,
	}, func(context.Context, postIn) (deleteOut, error) {
		close(started)
		<-release
		return deleteOut{Deleted: true}, nil
	})
	secret, _ := res.Key(t, "slow", apitokens.ScopeGuidePostsWrite)
	url := mcptest.Serve(t, mcptest.Handler(t, res, op))
	first := mcptest.ConnectURL(t, url, &mcptest.Transport{Bearer: secret})
	second := mcptest.ConnectURL(t, url, &mcptest.Transport{Bearer: secret})
	args := map[string]any{"text": "x", mcpserver.IdempotencyKeyArg: "slow-1"}
	done := make(chan *mcp.CallToolResult, 1)
	go func() {
		r, _ := first.CallTool(context.Background(), &mcp.CallToolParams{Name: "test_slow_create", Arguments: args})
		done <- r
	}()
	<-started
	d := mcptest.ErrorOf(t, mcptest.Call(t, second, "test_slow_create", args))
	close(release)
	if d.Code != api.CodeConflict || !strings.Contains(d.Message, "still in progress") {
		t.Errorf("retry while in flight = %+v, want a conflict", d)
	}
	if r := <-done; r == nil || r.IsError {
		t.Errorf("first call result %v", r)
	}
}

// TestIdempotentOpWithoutStorePanics: like api.NewHTTPRouter, the MCP
// handler refuses an idempotent operation with no store to keep keys in.
func TestIdempotentOpWithoutStorePanics(t *testing.T) {
	reg := api.NewRegistry(api.Config{Version: "v"})
	reg.Add(testOps(&recorder{})...)
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "IdempotencyStore") {
			t.Errorf("recover() = %v, want a panic naming IdempotencyStore", r)
		}
	}()
	mcpserver.NewHandler(mcpserver.Deps{Registry: reg, Resolver: apitest.NewResolver(), Limiter: middleware.NewAPITokenRateLimiter()})
}

type keyClashIn struct {
	Key string `json:"idempotency_key"`
}

func TestIdempotencyKeyFieldClashPanics(t *testing.T) {
	reg := api.NewRegistry(api.Config{Version: "v", Idempotency: &apitest.MemoryIdempotency{}})
	reg.Add(api.Define(api.Operation{
		Name: "test_clash", Method: http.MethodPost, Path: "/api/v1/test-clash", Scope: apitokens.ScopeGuidePostsWrite,
		Summary: "Clash.", Idempotency: true,
	}, func(context.Context, keyClashIn) (emptyOut, error) { return emptyOut{}, nil }))
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), mcpserver.IdempotencyKeyArg) {
			t.Errorf("recover() = %v, want a panic naming %s", r, mcpserver.IdempotencyKeyArg)
		}
	}()
	mcpserver.NewHandler(mcpserver.Deps{Registry: reg, Resolver: apitest.NewResolver(), Limiter: middleware.NewAPITokenRateLimiter()})
}

// cancelProbe records, per HTTP request, when the request's own context
// ends, so a test knows the server saw the client give up.
type cancelProbe struct {
	mu        sync.Mutex
	cancelled chan struct{}
}

func (p *cancelProbe) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		ch := p.cancelled
		p.mu.Unlock()
		if ch != nil {
			go func() {
				<-r.Context().Done()
				if errors.Is(r.Context().Err(), context.Canceled) {
					select {
					case <-ch:
					default:
						close(ch)
					}
				}
			}()
		}
		h.ServeHTTP(w, r)
	})
}

// TestCancellationPolicy pins #0685's policy end to end: when the client
// cancels a call, a read's context is cancelled, and a write runs to
// completion on a live context and records its idempotency result.
func TestCancellationPolicy(t *testing.T) {
	res := apitest.NewResolver()
	readStarted, writeStarted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	readErr, writeErr := make(chan error, 1), make(chan error, 1)
	var writes atomic.Int32
	ops := []api.Operation{
		api.Define(api.Operation{
			Name: "test_slow_read", Method: http.MethodGet, Path: "/api/v1/test-slow-read", Scope: apitokens.ScopeGuidePostsRead,
			Summary: "Read slowly.",
		}, func(ctx context.Context, _ emptyIn) (emptyOut, error) {
			close(readStarted)
			select {
			case <-ctx.Done():
				readErr <- ctx.Err()
			case <-time.After(10 * time.Second):
				readErr <- errors.New("the read's context was never cancelled")
			}
			return emptyOut{}, ctx.Err()
		}),
		api.Define(api.Operation{
			Name: "test_slow_send", Method: http.MethodPost, Path: "/api/v1/test-slow-send", Scope: apitokens.ScopeGuidePostsWrite,
			Summary: "Send slowly.", PublicEffect: "posts publicly right away", Idempotency: true,
		}, func(ctx context.Context, _ postIn) (deleteOut, error) {
			if writes.Add(1) == 1 {
				close(writeStarted)
				<-release
				// Give a cancellation that is still propagating time to
				// arrive; a live context waits out the timer.
				select {
				case <-ctx.Done():
				case <-time.After(300 * time.Millisecond):
				}
				writeErr <- ctx.Err()
			}
			return deleteOut{Deleted: true}, ctx.Err()
		}),
	}
	secret, _ := res.Key(t, "cancel", apitokens.ScopeGuidePostsWrite)
	probe := &cancelProbe{}
	srv := httptest.NewServer(probe.wrap(mcptest.Handler(t, res, ops...)))
	t.Cleanup(srv.Close)
	cs := mcptest.ConnectURL(t, srv.URL+mcpserver.Path, &mcptest.Transport{Bearer: secret})

	// A read: cancelling the call cancels the operation.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "test_slow_read"}) }()
	<-readStarted
	cancel()
	if err := <-readErr; !errors.Is(err, context.Canceled) {
		t.Errorf("read after the client cancelled: ctx.Err() = %v, want context.Canceled", err)
	}

	// A write: the client cancels, the server sees its request end, and the
	// operation still finishes on a live context.
	probe.mu.Lock()
	probe.cancelled = make(chan struct{})
	seen := probe.cancelled
	probe.mu.Unlock()
	ctx, cancel = context.WithCancel(context.Background())
	args := map[string]any{"text": "x", mcpserver.IdempotencyKeyArg: "send-1"}
	go func() { _, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "test_slow_send", Arguments: args}) }()
	<-writeStarted
	cancel()
	select {
	case <-seen:
	case <-time.After(10 * time.Second):
		t.Fatal("the server never saw the write's request cancelled")
	}
	close(release)
	if err := <-writeErr; err != nil {
		t.Errorf("write after the client cancelled: ctx.Err() = %v, want nil (runs to completion)", err)
	}
	// Its result was recorded, so the retry replays rather than resending.
	var r *mcp.CallToolResult
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if r = mcptest.Call(t, cs, "test_slow_send", args); !r.IsError {
			break
		}
	}
	if r.IsError || writes.Load() != 1 {
		t.Errorf("retry after the cancelled write: %s, %d sends, want a replay and 1 send", mcptest.Text(r), writes.Load())
	}
}
