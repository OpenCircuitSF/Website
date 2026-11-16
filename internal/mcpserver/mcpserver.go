package mcpserver

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// Path is where the MCP endpoint is mounted.
const Path = "/mcp"

// MaxBodyBytes caps an MCP request body, the same as a v1 REST body.
const MaxBodyBytes = api.MaxBodyBytes

// Extra keys the verifier puts in auth.TokenInfo.Extra for tool handlers.
// The SDK copies TokenInfo into every call's req.Extra; the handler's ctx is
// not guaranteed to carry this request's context values.
const (
	extraToken    = "token"     // apitokens.Token
	extraClientIP = "client_ip" // string, middleware.ClientIP
)

// instructions is sent to every client on initialize.
const instructions = `Open Circuit SF admin tools. Times are RFC 3339 with an offset; the site's time zone is America/Los_Angeles. ` +
	`The tools listed are exactly the ones this API key's scopes allow. Call whoami to check the connection and see the key's scopes. ` +
	`A tool whose description starts with PUBLIC acts publicly right away, and one starting with DESTRUCTIVE cannot be undone: confirm with the user before calling either.`

// Deps is what NewHandler needs.
type Deps struct {
	Registry *api.Registry
	Resolver middleware.APITokenResolver
	// Limiter is the per-token limiter. Pass the same instance the /api/v1/
	// router uses, so a key has one budget across REST and MCP.
	Limiter *middleware.RateLimiter
	Logger  *slog.Logger // nil = slog.Default()
}

// handler holds the registry's exposed operations and one cached
// *mcp.Server per distinct scope set.
type handler struct {
	ops     []api.Operation // MCPExcluded == "", in registry order
	version string
	logger  *slog.Logger
	servers sync.Map // sorted scopes joined by "," -> *mcp.Server
}

// NewHandler freezes d.Registry and returns the /mcp handler. The chain,
// outermost first: middleware.RequireAPIToken (the v1 401 with
// WWW-Authenticate), the per-token rate limit, the SDK's RequireBearerToken
// (whose verifier reads the token RequireAPIToken resolved, never resolving
// it twice), and the SDK's stateless, JSON-only Streamable HTTP handler.
// It panics on a nil registry, resolver or limiter.
func NewHandler(d Deps) http.Handler {
	_, h := newHandler(d)
	return h
}

// newHandler is NewHandler, also returning the handler state for tests.
func newHandler(d Deps) (*handler, http.Handler) {
	if d.Registry == nil || d.Resolver == nil || d.Limiter == nil {
		panic("mcpserver: NewHandler needs a Registry, a Resolver and a Limiter")
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	// The SDK logs several INFO lines per stateless request (a session
	// connecting and disconnecting each time); keep only its warnings.
	d.Logger = slog.New(warnAndAbove{d.Logger.Handler()})
	d.Registry.Freeze()
	h := &handler{version: d.Registry.Version(), logger: d.Logger}
	for _, op := range d.Registry.Operations() {
		if op.MCPExcluded == "" {
			h.ops = append(h.ops, op)
		}
	}
	stream := mcp.NewStreamableHTTPHandler(h.serverFor, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// Apache proxies with ProxyPreserveHost On, so a production request
		// arrives on 127.0.0.1 with Host www.opencircuitsf.com, which the
		// SDK's DNS-rebinding guard would 403. Every request here already
		// carries a valid bearer key, and no cookie authenticates /mcp.
		// TestMCPAcceptsProxiedHost pins this.
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        MaxBodyBytes,
		Logger:                     d.Logger,
	})
	bearer := auth.RequireBearerToken(verifyFromContext, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})
	return h, middleware.RequireAPIToken(d.Resolver, "")(middleware.APITokenRateLimit(d.Limiter)(bearer(stream)))
}

// Mount registers h for Path on mux, one pattern per method the Streamable
// HTTP transport uses. A bare "/mcp" pattern cannot be used: it conflicts
// with the SPA's "GET /" catch-all in Go's ServeMux. Call it once.
func Mount(mux *http.ServeMux, h http.Handler) {
	for _, m := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		mux.Handle(m+" "+Path, h)
	}
}

// verifyFromContext is the SDK's TokenVerifier. RequireAPIToken has already
// resolved the bearer and put the token in the request context, so this
// only translates it into an auth.TokenInfo.
func verifyFromContext(_ context.Context, _ string, r *http.Request) (*auth.TokenInfo, error) {
	tok, ok := middleware.APITokenFromContext(r.Context())
	if !ok {
		return nil, auth.ErrInvalidToken
	}
	info := &auth.TokenInfo{
		Scopes: tok.Scopes,
		UserID: "token:" + strconv.FormatInt(tok.ID, 10),
		Extra:  map[string]any{extraToken: tok, extraClientIP: middleware.ClientIP(r)},
	}
	if tok.ExpiresAt != nil {
		info.Expiration = *tok.ExpiresAt
	}
	return info, nil
}

// scopeKey is the cache key for a scope set: order-insensitive.
func scopeKey(scopes []string) string {
	s := slices.Clone(scopes)
	slices.Sort(s)
	return strings.Join(slices.Compact(s), ",")
}

// serverFor returns the cached server for the request's key's scope set,
// building it on first use. The SDK calls it more than once per request, so
// it must be cheap after the first time.
func (h *handler) serverFor(r *http.Request) *mcp.Server {
	tok, ok := middleware.APITokenFromContext(r.Context())
	if !ok {
		return nil // unreachable behind RequireAPIToken; the SDK answers 400
	}
	key := scopeKey(tok.Scopes)
	if s, ok := h.servers.Load(key); ok {
		return s.(*mcp.Server)
	}
	s, _ := h.servers.LoadOrStore(key, h.newServer(tok.Scopes))
	return s.(*mcp.Server)
}

// allowed reports whether a key holding scopes may call op.
func allowed(op api.Operation, scopes []string) bool {
	return op.AnyToken || apitokens.ScopeSatisfies(scopes, op.Scope)
}

// newServer builds a server holding exactly the tools scopes allow. No tool
// handler closes over a key (each reads it from the call), so one server is
// shared by every key with the same scopes.
func (h *handler) newServer(scopes []string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "opencircuitsf", Title: "Open Circuit SF", Version: h.version},
		&mcp.ServerOptions{Instructions: instructions, Logger: h.logger})
	for _, op := range h.ops {
		if allowed(op, scopes) {
			s.AddTool(toolFor(op), handlerFor(op))
		}
	}
	return s
}

// warnAndAbove drops records below slog.LevelWarn.
type warnAndAbove struct{ slog.Handler }

func (w warnAndAbove) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelWarn && w.Handler.Enabled(ctx, l)
}

func (w warnAndAbove) WithAttrs(a []slog.Attr) slog.Handler {
	return warnAndAbove{w.Handler.WithAttrs(a)}
}

func (w warnAndAbove) WithGroup(n string) slog.Handler { return warnAndAbove{w.Handler.WithGroup(n)} }
