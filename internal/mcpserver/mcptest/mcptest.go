// Package mcptest helps test the /admin/mcp adapter (#0679) and the tools #0676,
// #0677 and #0678 add: it connects the official SDK client to a handler over
// a real HTTP server, so the auth, rate-limit and transport path in front of
// the tools is the one production runs.
package mcptest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// Transport adds a bearer key, and optionally overrides Host, on every
// request it sends through Base (nil = http.DefaultTransport).
type Transport struct {
	Bearer string
	Host   string
	Base   http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if t.Bearer != "" {
		r.Header.Set("Authorization", "Bearer "+t.Bearer)
	}
	if t.Host != "" {
		r.Host = t.Host
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}

// Serve starts an httptest server for h, closed at cleanup, and returns its
// MCP endpoint URL (mcpserver.Path).
func Serve(t testing.TB, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL + mcpserver.Path
}

// ConnectURL connects the SDK client to endpoint through tr and returns the
// session, closed at cleanup. It fails the test if the connection fails;
// use Dial to see the error instead.
func ConnectURL(t testing.TB, endpoint string, tr *Transport) *mcp.ClientSession {
	t.Helper()
	cs, err := Dial(endpoint, tr)
	if err != nil {
		t.Fatalf("mcptest: connect %s: %v", endpoint, err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// Dial connects the SDK client to endpoint through tr.
func Dial(endpoint string, tr *Transport) (*mcp.ClientSession, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "mcptest", Version: "test"}, nil)
	return c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: tr},
	}, nil)
}

// Connect serves h and connects the SDK client to it with bearer as the key.
// Both are closed at cleanup.
func Connect(t testing.TB, h http.Handler, bearer string) *mcp.ClientSession {
	t.Helper()
	return ConnectURL(t, Serve(t, h), &Transport{Bearer: bearer})
}

// Handler is mcpserver.NewHandler over ops (plus api.CoreOperations), with
// res as the resolver and a fresh per-token limiter: the MCP twin of
// apitest.Router. Mint keys with res.Key.
func Handler(t testing.TB, res *apitest.Resolver, ops ...api.Operation) http.Handler {
	t.Helper()
	reg := api.NewRegistry(api.Config{Version: "test", Idempotency: &apitest.MemoryIdempotency{}})
	reg.Add(api.CoreOperations("test")...)
	reg.Add(ops...)
	return mcpserver.NewHandler(mcpserver.Deps{Registry: reg, Resolver: res, Limiter: middleware.NewAPITokenRateLimiter()})
}

// Call calls tool name with args (nil = no arguments) and fails the test on
// a protocol error. A tool error comes back as a result with IsError set.
func Call(t testing.TB, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("mcptest: call %s: %v", name, err)
	}
	return res
}

// Text is the result's first content block's text, or "" if there is none.
func Text(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// ErrorOf decodes a tool error's text as the v1 error envelope, failing the
// test unless res is a tool error carrying exactly that.
func ErrorOf(t testing.TB, res *mcp.CallToolResult) api.ErrorDetail {
	t.Helper()
	if !res.IsError {
		t.Fatalf("mcptest: want a tool error, got success: %s", Text(res))
	}
	return apitest.ErrorOf(t, []byte(Text(res)))
}
