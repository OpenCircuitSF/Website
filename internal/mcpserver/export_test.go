package mcpserver

import (
	"net/http"
	"net/http/httptest"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// Test hooks for the external mcpserver_test package (which cannot be an
// internal test: it imports mcptest, which imports this package).
var (
	ToolFor    = toolFor
	HandlerFor = handlerFor
)

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
