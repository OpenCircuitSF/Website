package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	ocsfauth "github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// The fixture's test operations, shaped like the #0676 and #0677 tools the
// acceptance criteria name: a paginated list, a body create with per-item
// results that posts publicly, a destructive delete, and one that fails
// with a raw error.

type eventItem struct {
	Title string `json:"title"`
}

type listEventsIn struct {
	api.Page
}

type postIn struct {
	Text string `json:"text"`
}

type createPostsIn struct {
	Posts []postIn `json:"posts"`
}

type postResult struct {
	Index int    `json:"index"`
	ID    int64  `json:"id"`
	Text  string `json:"text"`
}

type createPostsOut struct {
	Results []postResult `json:"results"`
}

type deleteIn struct {
	ID int64 `json:"id" path:"id"`
}

type deleteOut struct {
	Deleted bool `json:"deleted"`
}

type emptyIn struct{}

type emptyOut struct{}

// recorder captures what the create op saw.
type recorder struct {
	mu      sync.Mutex
	calls   int
	in      createPostsIn
	tokenID int64
	hasTok  bool
	ip      string
}

func testOps(rec *recorder) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "test_list_events", Method: http.MethodGet, Path: "/api/v1/test-events", Scope: apitokens.ScopeEventsRead,
			Summary: "List test events.",
		}, func(_ context.Context, in listEventsIn) (api.List[eventItem], error) {
			n, err := in.Page.Resolve()
			if err != nil {
				return api.List[eventItem]{}, err
			}
			items := make([]eventItem, n)
			for i := range items {
				items[i] = eventItem{Title: "after:" + in.Cursor}
			}
			next := "next-of-" + in.Cursor
			return api.List[eventItem]{Items: items, NextCursor: &next}, nil
		}),
		api.Define(api.Operation{
			Name: "test_create_posts", Method: http.MethodPost, Path: "/api/v1/test-posts", Scope: apitokens.ScopeGuidePostsWrite,
			Summary: "Create test posts.", PublicEffect: "posts publicly right away", Status: http.StatusCreated, Idempotency: true,
		}, func(ctx context.Context, in createPostsIn) (createPostsOut, error) {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.calls++
			rec.in = in
			tok, ok := middleware.APITokenFromContext(ctx)
			rec.tokenID, rec.hasTok = tok.ID, ok
			rec.ip = api.ClientIPFromContext(ctx)
			out := createPostsOut{Results: []postResult{}}
			for i, p := range in.Posts {
				out.Results = append(out.Results, postResult{Index: i, ID: int64(100 + i), Text: p.Text})
			}
			return out, nil
		}),
		api.Define(api.Operation{
			Name: "test_delete", Method: http.MethodDelete, Path: "/api/v1/test-posts/{id}", Scope: apitokens.ScopeGuidePostsWrite,
			Summary: "Delete a test post.", Destructive: true,
		}, func(_ context.Context, in deleteIn) (deleteOut, error) {
			return deleteOut{Deleted: in.ID > 0}, nil
		}),
		api.Define(api.Operation{
			Name: "test_fail", Method: http.MethodGet, Path: "/api/v1/test-fail", Scope: apitokens.ScopeEventsRead,
			Summary: "Always fails.",
		}, func(context.Context, emptyIn) (emptyOut, error) {
			return emptyOut{}, errors.New("pq: relation secret_table does not exist")
		}),
	}
}

type fixture struct {
	res     *apitest.Resolver
	rec     *recorder
	reg     *api.Registry
	handler http.Handler
	handle  mcpserver.Handle
	url     string
}

func newFixture(t *testing.T, limiter *middleware.RateLimiter) *fixture {
	t.Helper()
	if limiter == nil {
		limiter = middleware.NewAPITokenRateLimiter()
	}
	f := &fixture{res: apitest.NewResolver(), rec: &recorder{}}
	f.reg = api.NewRegistry(api.Config{Version: "test-version", Idempotency: &apitest.MemoryIdempotency{}})
	f.reg.Add(api.CoreOperations("test-version")...)
	f.reg.Add(testOps(f.rec)...)
	f.handler, f.handle = mcpserver.NewTestHandler(mcpserver.Deps{Registry: f.reg, Resolver: f.res, Limiter: limiter})
	f.url = mcptest.Serve(t, f.handler)
	return f
}

func (f *fixture) connect(t *testing.T, scopes ...string) (*mcp.ClientSession, apitokens.Token) {
	t.Helper()
	secret, tok := f.res.Key(t, "key "+strings.Join(scopes, "+"), scopes...)
	return mcptest.ConnectURL(t, f.url, &mcptest.Transport{Bearer: secret}), tok
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func TestToolsListFilteredByScope(t *testing.T) {
	f := newFixture(t, nil)
	for _, tc := range []struct {
		scopes []string
		want   []string
	}{
		{[]string{apitokens.ScopeEventsRead}, []string{"test_fail", "test_list_events", "whoami"}},
		{[]string{apitokens.ScopeGuidePostsWrite}, []string{"test_create_posts", "test_delete", "whoami"}},
		{[]string{apitokens.ScopeEngagementRead}, []string{"whoami"}},
		{nil, []string{"whoami"}},
	} {
		cs, _ := f.connect(t, tc.scopes...)
		if got := toolNames(t, cs); !slices.Equal(got, tc.want) {
			t.Errorf("scopes %v: tools = %v, want %v", tc.scopes, got, tc.want)
		}
	}
}

func TestToolsListWriteImpliesRead(t *testing.T) {
	f := newFixture(t, nil)
	cs, _ := f.connect(t, apitokens.ScopeEventsWrite)
	if got := toolNames(t, cs); !slices.Contains(got, "test_list_events") {
		t.Errorf("events:write key tools = %v, want test_list_events among them", got)
	}
}

// TestListedToolsMatchRegistry: over the wire, every listed tool's
// description and schemas are the registry's, and token_probe never appears.
func TestListedToolsMatchRegistry(t *testing.T) {
	f := newFixture(t, nil)
	cs, _ := f.connect(t, apitokens.KnownScopes...)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]api.Operation{}
	for _, op := range f.reg.Operations() {
		byName[op.Name] = op
	}
	if len(res.Tools) != len(byName)-1 {
		t.Errorf("listed %d tools, want %d (every op but token_probe)", len(res.Tools), len(byName)-1)
	}
	for _, tool := range res.Tools {
		op, ok := byName[tool.Name]
		if !ok || op.MCPExcluded != "" {
			t.Errorf("listed tool %q is not an exposed operation", tool.Name)
			continue
		}
		wantDesc := op.Describe()
		if op.Idempotency {
			wantDesc += mcpserver.IdempotencyKeyNote
		}
		if tool.Description != wantDesc {
			t.Errorf("%s: description %q, want %q", tool.Name, tool.Description, wantDesc)
		}
		in := withoutIdempotencyKey(t, op, schemaOf(t, tool.InputSchema))
		for name, pair := range map[string][2]any{"input": {in, op.InputSchema()}, "output": {tool.OutputSchema, op.OutputSchema()}} {
			got, _ := json.Marshal(pair[0])
			want, _ := json.Marshal(pair[1])
			if !jsonEqual(t, got, want) {
				t.Errorf("%s: %s schema %s, want %s", tool.Name, name, got, want)
			}
		}
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return string(ja) == string(jb)
}

func TestCallWhoami(t *testing.T) {
	f := newFixture(t, nil)
	cs, _ := f.connect(t, apitokens.ScopeEngagementRead)
	res := mcptest.Call(t, cs, "whoami", nil)
	if res.IsError {
		t.Fatalf("whoami: %s", mcptest.Text(res))
	}
	var who api.WhoAmI
	if err := json.Unmarshal([]byte(mcptest.Text(res)), &who); err != nil {
		t.Fatal(err)
	}
	if who.ServerVersion != "test-version" || !slices.Equal(who.Scopes, []string{apitokens.ScopeEngagementRead}) || who.APIVersion != "v1" {
		t.Errorf("whoami = %+v", who)
	}
}

func TestCallCreateSucceeds(t *testing.T) {
	f := newFixture(t, nil)
	cs, tok := f.connect(t, apitokens.ScopeGuidePostsWrite)
	res := mcptest.Call(t, cs, "test_create_posts", map[string]any{"posts": []map[string]any{{"text": "one"}, {"text": "two"}}})
	if res.IsError {
		t.Fatalf("create: %s", mcptest.Text(res))
	}
	want := createPostsOut{Results: []postResult{{0, 100, "one"}, {1, 101, "two"}}}
	wantJSON, _ := json.Marshal(want)
	if got := mcptest.Text(res); got != string(wantJSON) {
		t.Errorf("text = %s, want %s", got, wantJSON)
	}
	structured, _ := json.Marshal(res.StructuredContent)
	if !jsonEqual(t, structured, wantJSON) {
		t.Errorf("structured content = %s, want %s", structured, wantJSON)
	}
	f.rec.mu.Lock()
	defer f.rec.mu.Unlock()
	if f.rec.calls != 1 || len(f.rec.in.Posts) != 2 || f.rec.in.Posts[1].Text != "two" {
		t.Errorf("op saw %d calls, input %+v", f.rec.calls, f.rec.in)
	}
	// What makes #0674's audit attribution work for tool calls.
	if !f.rec.hasTok || f.rec.tokenID != tok.ID {
		t.Errorf("op context token = %d (present %v), want %d", f.rec.tokenID, f.rec.hasTok, tok.ID)
	}
	if f.rec.ip != "127.0.0.1" {
		t.Errorf("op context client IP = %q, want 127.0.0.1", f.rec.ip)
	}
}

func TestCallListPaginates(t *testing.T) {
	f := newFixture(t, nil)
	cs, _ := f.connect(t, apitokens.ScopeEventsRead)
	res := mcptest.Call(t, cs, "test_list_events", map[string]any{"limit": 3, "cursor": "abc"})
	if res.IsError {
		t.Fatalf("list: %s", mcptest.Text(res))
	}
	var page api.List[eventItem]
	if err := json.Unmarshal([]byte(mcptest.Text(res)), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].Title != "after:abc" || page.NextCursor == nil || *page.NextCursor != "next-of-abc" {
		t.Errorf("page = %+v", page)
	}
	if e := mcptest.ErrorOf(t, mcptest.Call(t, cs, "test_list_events", map[string]any{"limit": 500})); e.Code != api.CodeValidation || e.Fields["limit"] == "" {
		t.Errorf("limit 500 = %+v, want a validation error naming limit", e)
	}
}

func TestCallUnlistedToolIsDenied(t *testing.T) {
	f := newFixture(t, nil)
	cs, _ := f.connect(t, apitokens.ScopeGuidePostsRead)
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "test_create_posts", Arguments: map[string]any{"posts": []any{}}})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("read-only key calling a write tool: err = %v, want unknown tool", err)
	}
	f.rec.mu.Lock()
	defer f.rec.mu.Unlock()
	if f.rec.calls != 0 {
		t.Errorf("the op ran %d times, want 0", f.rec.calls)
	}
}

// TestHandlerRechecksScope reaches the handler's own scope check, which the
// per-scope server normally makes unreachable, and its missing-token check.
func TestHandlerRechecksScope(t *testing.T) {
	rec := &recorder{}
	var create api.Operation
	for _, op := range testOps(rec) {
		if op.Name == "test_create_posts" {
			create = op
		}
	}
	h := mcpserver.HandlerFor(create)
	call := func(extra map[string]any) *mcp.CallToolResult {
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: create.Name, Arguments: json.RawMessage(`{"posts":[]}`)}}
		if extra != nil {
			req.Extra = &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{Extra: extra}}
		}
		res, err := h(context.Background(), req)
		if err != nil {
			t.Fatalf("handler returned a Go error: %v", err)
		}
		return res
	}
	readOnly := apitokens.Token{ID: 7, Scopes: []string{apitokens.ScopeGuidePostsRead}}
	if e := mcptest.ErrorOf(t, call(map[string]any{mcpserver.ExtraToken: readOnly})); e.Code != api.CodeForbidden || !strings.Contains(e.Message, apitokens.ScopeGuidePostsWrite) {
		t.Errorf("read-only token = %+v, want forbidden naming guide_posts:write", e)
	}
	if e := mcptest.ErrorOf(t, call(nil)); e.Code != api.CodeUnauthorized {
		t.Errorf("no token info = %+v, want unauthorized", e)
	}
	if rec.calls != 0 {
		t.Errorf("the op ran %d times, want 0", rec.calls)
	}
	writer := apitokens.Token{ID: 8, Scopes: []string{apitokens.ScopeGuidePostsWrite}}
	if res := call(map[string]any{mcpserver.ExtraToken: writer, mcpserver.ExtraClientIP: "203.0.113.9"}); res.IsError || rec.ip != "203.0.113.9" || rec.tokenID != 8 {
		t.Errorf("writer: IsError %v, ip %q, token %d", res.IsError, rec.ip, rec.tokenID)
	}
}

func TestCallValidationErrorIsToolError(t *testing.T) {
	f := newFixture(t, nil)
	cs, _ := f.connect(t, apitokens.ScopeGuidePostsWrite)
	e := mcptest.ErrorOf(t, mcptest.Call(t, cs, "test_create_posts", map[string]any{"posts": []any{}, "bogus": 1}))
	if e.Code != api.CodeValidation || e.Fields["bogus"] == "" {
		t.Errorf("unknown argument = %+v, want validation naming bogus", e)
	}
	e = mcptest.ErrorOf(t, mcptest.Call(t, cs, "test_delete", nil))
	if e.Code != api.CodeValidation || e.Fields["id"] == "" {
		t.Errorf("missing path field = %+v, want validation naming id", e)
	}
}

func TestCallInternalErrorHidesText(t *testing.T) {
	f := newFixture(t, nil)
	cs, _ := f.connect(t, apitokens.ScopeEventsRead)
	res := mcptest.Call(t, cs, "test_fail", nil)
	if e := mcptest.ErrorOf(t, res); e.Code != api.CodeInternal {
		t.Errorf("code = %q, want internal", e.Code)
	}
	if txt := mcptest.Text(res); strings.Contains(txt, "pq:") || strings.Contains(txt, "secret_table") {
		t.Errorf("tool error leaks the raw error: %s", txt)
	}
	if res.StructuredContent != nil {
		t.Errorf("tool error carries structured content: %v", res.StructuredContent)
	}
}

// rawPost sends one JSON-RPC body to url with the given headers.
func rawPost(t *testing.T, method, url, body string, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

const toolsListBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`

func TestInvalidKeyGets401(t *testing.T) {
	f := newFixture(t, nil)
	unknown, err := apitokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	for name, hdr := range map[string][]string{
		"no key":      nil,
		"malformed":   {"Authorization", "Bearer not-a-key"},
		"unknown key": {"Authorization", "Bearer " + unknown},
	} {
		resp, b := rawPost(t, http.MethodPost, f.url, toolsListBody, hdr...)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
			continue
		}
		if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("%s: WWW-Authenticate %q, want Bearer …", name, got)
		}
		if e := apitest.ErrorOf(t, b); e.Code != api.CodeUnauthorized {
			t.Errorf("%s: code %q, want unauthorized", name, e.Code)
		}
	}
	if _, err := mcptest.Dial(f.url, &mcptest.Transport{Bearer: unknown}); err == nil {
		t.Error("the SDK client connected with an unknown key")
	}
}

func TestSessionCookieIsNotAccepted(t *testing.T) {
	f := newFixture(t, nil)
	resp, _ := rawPost(t, http.MethodPost, f.url, toolsListBody, "Cookie", ocsfauth.SessionCookieName+"=some-admin-session-token")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("session cookie only: status %d, want 401", resp.StatusCode)
	}
}

// TestMCPAcceptsProxiedHost: Apache proxies with ProxyPreserveHost On, so
// production requests reach 127.0.0.1 with Host www.opencircuitsf.com. The
// SDK's DNS-rebinding guard 403s that unless DisableLocalhostProtection is
// set; the control below proves the guard would fire on this very request.
func TestMCPAcceptsProxiedHost(t *testing.T) {
	f := newFixture(t, nil)
	secret, _ := f.res.Key(t, "proxied", apitokens.ScopeEngagementRead)
	tr := &mcptest.Transport{Bearer: secret, Host: "www.opencircuitsf.com"}
	cs := mcptest.ConnectURL(t, f.url, tr)
	if got := toolNames(t, cs); !slices.Equal(got, []string{"whoami"}) {
		t.Errorf("tools = %v", got)
	}

	// Control: the SDK's handler with its default protection, same Host.
	plain := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcp.NewServer(&mcp.Implementation{Name: "control", Version: "0"}, nil)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	srv := httptest.NewServer(plain)
	defer srv.Close()
	if _, err := mcptest.Dial(srv.URL, tr); err == nil {
		t.Error("control: the SDK's default handler accepted a proxied Host; the test no longer proves anything")
	}
}

func TestResponsesAreJSONNotSSE(t *testing.T) {
	f := newFixture(t, nil)
	secret, _ := f.res.Key(t, "json", apitokens.ScopeEngagementRead)
	resp, b := rawPost(t, http.MethodPost, f.url, toolsListBody, "Authorization", "Bearer "+secret)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tools/list: status %d %s", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type %q, want application/json", ct)
	}
	if !strings.Contains(string(b), `"whoami"`) {
		t.Errorf("tools/list body lacks whoami: %s", b)
	}
	resp, _ = rawPost(t, http.MethodGet, f.url, "", "Authorization", "Bearer "+secret)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET with a valid key: status %d, want 405", resp.StatusCode)
	}
}

func TestRateLimitAppliesToMCP(t *testing.T) {
	f := newFixture(t, middleware.NewRateLimiter(rate.Every(time.Hour), 2))
	secret, _ := f.res.Key(t, "limited", apitokens.ScopeEngagementRead)
	for i := 1; i <= 3; i++ {
		resp, b := rawPost(t, http.MethodPost, f.url, toolsListBody, "Authorization", "Bearer "+secret)
		switch {
		case i < 3 && resp.StatusCode != http.StatusOK:
			t.Fatalf("request %d: status %d %s, want 200", i, resp.StatusCode, b)
		case i == 3:
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("request 3: status %d, want 429", resp.StatusCode)
			}
			if e := apitest.ErrorOf(t, b); e.Code != api.CodeRateLimited {
				t.Errorf("request 3: code %q, want rate_limited", e.Code)
			}
		}
	}
	// Another key still has its own budget.
	other, _ := f.res.Key(t, "other", apitokens.ScopeEngagementRead)
	if resp, _ := rawPost(t, http.MethodPost, f.url, toolsListBody, "Authorization", "Bearer "+other); resp.StatusCode != http.StatusOK {
		t.Errorf("another key: status %d, want 200", resp.StatusCode)
	}
}

func TestServerCacheReusesPerScopeSet(t *testing.T) {
	f := newFixture(t, nil)
	a := f.handle.ServerFor(apitokens.Token{ID: 1, Scopes: []string{apitokens.ScopeEventsRead, apitokens.ScopeGuidesRead}})
	b := f.handle.ServerFor(apitokens.Token{ID: 2, Scopes: []string{apitokens.ScopeGuidesRead, apitokens.ScopeEventsRead}})
	c := f.handle.ServerFor(apitokens.Token{ID: 3, Scopes: []string{apitokens.ScopeEventsRead}})
	if a == nil || a != b {
		t.Errorf("same scopes in a different order got different servers")
	}
	if a == c {
		t.Errorf("different scopes got the same server")
	}
}

func TestOversizedBodyRefused(t *testing.T) {
	f := newFixture(t, nil)
	secret, _ := f.res.Key(t, "big", apitokens.ScopeEngagementRead)
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"pad":"` + strings.Repeat("x", mcpserver.MaxBodyBytes) + `"}}`
	if resp, _ := rawPost(t, http.MethodPost, f.url, big, "Authorization", "Bearer "+secret); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("a body over 64 KB: status %d, want 413", resp.StatusCode)
	}
}

// TestSDKInfoLogsAreDropped: the SDK logs a session connecting and
// disconnecting on every stateless request; only its warnings reach the
// journal.
func TestSDKInfoLogsAreDropped(t *testing.T) {
	var buf bytes.Buffer
	res := apitest.NewResolver()
	secret, _ := res.Key(t, "quiet", apitokens.ScopeEngagementRead)
	reg := api.NewRegistry(api.Config{Version: "v"})
	reg.Add(api.CoreOperations("v")...)
	h := mcpserver.NewHandler(mcpserver.Deps{Registry: reg, Resolver: res, Limiter: middleware.NewAPITokenRateLimiter(),
		Logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	cs := mcptest.Connect(t, h, secret)
	if r := mcptest.Call(t, cs, "whoami", nil); r.IsError {
		t.Fatalf("whoami: %s", mcptest.Text(r))
	}
	if strings.Contains(buf.String(), "level=INFO") || strings.Contains(buf.String(), "level=DEBUG") {
		t.Errorf("SDK info logs reached the logger:\n%s", buf.String())
	}
}
