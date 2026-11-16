package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// Test hooks for the external mcpserver_test package (which cannot be an
// internal test: it imports mcptest, which imports this package).
var ToolFor = toolFor

// HandlerFor is op's tool handler with no idempotency store.
func HandlerFor(op api.Operation) mcp.ToolHandler { return handlerFor(op, nil, time.Now) }

// ExtraToken and ExtraClientIP are the TokenInfo.Extra keys.
const (
	ExtraToken    = extraToken
	ExtraClientIP = extraClientIP
)

// Handle exposes a handler's server cache.
type Handle struct{ h *handler }

// NewTestHandler is NewHandler, also returning a Handle on its state.
func NewTestHandler(d Deps) (http.Handler, Handle) {
	h, chain := newHandler(d)
	return chain, Handle{h}
}

// ServerFor is the server a request authenticated as tok would get.
func (x Handle) ServerFor(tok apitokens.Token) *mcp.Server {
	r := httptest.NewRequest(http.MethodPost, Path, nil)
	return x.h.serverFor(r.WithContext(middleware.WithAPIToken(r.Context(), tok)))
}
