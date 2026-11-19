package publicmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
)

// Path is where the public MCP endpoint is mounted.
const Path = "/mcp"

// instructions is sent to every client on initialize.
const instructions = `Read-only public information about Open Circuit SF, a San Francisco hands-on electronics workshop group: its events and its newsletter issues. ` +
	`No account or key is needed. Times are RFC 3339 with an offset in America/Los_Angeles. ` +
	`Every event and newsletter result carries its public url; link people to it. get_about says what the group is and how to join.`

// NewHandler freezes reg and returns the /mcp handler: one *mcp.Server
// holding every operation in reg as a tool, behind the SDK's stateless,
// JSON-only Streamable HTTP handler, with nothing wrapped around it. The
// stateless handler answers GET and DELETE with 405. logger nil means
// slog.Default().
func NewHandler(reg *publicapi.Registry, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	// The SDK logs several INFO lines per stateless request; keep only its
	// warnings.
	logger = slog.New(warnAndAbove{logger.Handler()})
	reg.Freeze()
	s := mcp.NewServer(&mcp.Implementation{Name: "opencircuitsf", Title: "Open Circuit SF", Version: reg.Version()},
		&mcp.ServerOptions{Instructions: instructions, Logger: logger})
	for _, op := range reg.Ops() {
		s.AddTool(toolFor(op), handlerFor(reg, op))
	}
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// Apache proxies with ProxyPreserveHost On, so a production request
		// arrives on 127.0.0.1 with Host www.opencircuitsf.com, which the
		// SDK's DNS-rebinding guard would 403. No cookie or key
		// authenticates /mcp, so DNS rebinding gains an attacker nothing.
		// TestProxiedHostAccepted pins this.
		DisableLocalhostProtection:   true,
		MaxRequestBodyBytes:          api.MaxBodyBytes,
		PropagateRequestCancellation: true,
		Logger:                       logger,
	})
}

// Mount registers h for Path on mux, one pattern per method the Streamable
// HTTP transport uses. A pattern with no method cannot be used: it
// conflicts with the SPA's "GET /" catch-all in Go's ServeMux. Call it once.
func Mount(mux *http.ServeMux, h http.Handler) {
	for _, m := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		mux.Handle(m+" "+Path, h)
	}
}

// toolFor is op as a read-only, idempotent, closed-world tool.
func toolFor(op publicapi.Op) *mcp.Tool {
	f := false
	return &mcp.Tool{
		Name:         op.Name,
		Description:  op.Describe(),
		InputSchema:  op.InputSchema(),
		OutputSchema: op.OutputSchema(),
		Annotations: &mcp.ToolAnnotations{
			Title:           op.Summary,
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: &f,
			OpenWorldHint:   &f,
		},
	}
}

// handlerFor runs op for one tools/call through reg.Invoke. A success
// carries the JSON as structured content, plus two text blocks: the
// one-line summary, then the JSON for clients that ignore structured
// content. Every failure is a tool error whose text is the v1 error
// envelope.
func handlerFor(reg *publicapi.Registry, op publicapi.Op) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var raw []byte
		if req.Params != nil {
			raw = req.Params.Arguments
		}
		if t := bytes.TrimSpace(raw); len(t) == 0 || bytes.Equal(t, []byte("null")) {
			raw = nil
		}
		in, err := op.Decode(raw)
		if err != nil {
			return errorResult(api.AsError(op.Name, err)), nil
		}
		out, err := reg.Invoke(ctx, op, in)
		if err != nil {
			return errorResult(api.AsError(op.Name, err)), nil
		}
		b, err := json.Marshal(out)
		if err != nil {
			return errorResult(api.AsError(op.Name, err)), nil
		}
		return &mcp.CallToolResult{
			StructuredContent: json.RawMessage(b),
			Content:           []mcp.Content{&mcp.TextContent{Text: op.Summarize(out)}, &mcp.TextContent{Text: string(b)}},
		}, nil
	}
}

// errorResult is e as a tool error with no structured content.
func errorResult(e *api.Error) *mcp.CallToolResult {
	text, _ := json.Marshal(e.Body())
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(text)}}}
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
