package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// toolFor is op as an MCP tool. Everything comes from the registry: the
// description already leads with DESTRUCTIVE or PUBLIC when that applies and
// ends with the scope the call needs.
func toolFor(op api.Operation) *mcp.Tool {
	destructive := op.Destructive
	openWorld := op.PublicEffect != ""
	return &mcp.Tool{
		Name:         op.Name,
		Description:  op.Describe(),
		InputSchema:  op.InputSchema(),
		OutputSchema: op.OutputSchema(),
		Annotations: &mcp.ToolAnnotations{
			Title:           op.Summary,
			ReadOnlyHint:    op.Method == http.MethodGet,
			DestructiveHint: &destructive,
			IdempotentHint:  op.Method == http.MethodGet || op.Method == http.MethodDelete,
			OpenWorldHint:   &openWorld,
		},
	}
}

// handlerFor runs op for one tools/call: the same Decode and Call as the
// REST adapter, in process. Every failure is a tool error (IsError, with
// the v1 error envelope as its text) rather than a JSON-RPC error, so the
// model can read it and act on it.
func handlerFor(op api.Operation) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var extra map[string]any
		if req.Extra != nil && req.Extra.TokenInfo != nil {
			extra = req.Extra.TokenInfo.Extra
		}
		tok, ok := extra[extraToken].(apitokens.Token)
		if !ok {
			return errorResult(&api.Error{Status: http.StatusUnauthorized, Code: api.CodeUnauthorized, Message: "a valid API key is required"}), nil
		}
		// The cached server already holds only the tools these scopes
		// allow; this is the belt to that pair of braces.
		if !allowed(op, tok.Scopes) {
			return errorResult(api.Forbidden(op.Scope)), nil
		}
		// What the REST adapter's middleware puts in context: the token, for
		// whoami and middleware.AttributeAPIToken's audit attribution, and
		// the client IP for audit entries.
		ctx = middleware.WithAPIToken(ctx, tok)
		if ip, _ := extra[extraClientIP].(string); ip != "" {
			ctx = api.WithClientIP(ctx, ip)
		}
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
		out, err := op.Call(ctx, in)
		if err != nil {
			return errorResult(api.AsError(op.Name, err)), nil
		}
		text, err := json.Marshal(out)
		if err != nil {
			return errorResult(api.AsError(op.Name, err)), nil
		}
		return &mcp.CallToolResult{
			StructuredContent: json.RawMessage(text),
			Content:           []mcp.Content{&mcp.TextContent{Text: string(text)}},
		}, nil
	}
}

// errorResult is e as a tool error. Its text is the v1 error envelope, the
// same body a REST call would get. It carries no StructuredContent, so a
// client never validates an error against the tool's success OutputSchema.
func errorResult(e *api.Error) *mcp.CallToolResult {
	text, _ := json.Marshal(e.Body())
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(text)}}}
}
