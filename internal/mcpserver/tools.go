package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// IdempotencyKeyArg is the optional argument every tool for an operation
// with Idempotency set accepts (#0685): MCP's Idempotency-Key header.
const IdempotencyKeyArg = "idempotency_key"

// IdempotencyKeyNote is appended to such a tool's description.
const IdempotencyKeyNote = " To retry safely, pass " + IdempotencyKeyArg + ", a unique string (1 to 255 printable ASCII characters), " +
	"and reuse it on every retry of this call. A retry with the same key returns the first call's result instead of creating a duplicate. " +
	"A key reused with different arguments, or while the first call is still running, is a conflict. Keys are kept 24 hours per API key."

// toolFor is op as an MCP tool. Everything comes from the registry: the
// description already leads with DESTRUCTIVE or PUBLIC when that applies and
// ends with the scope the call needs. An idempotent operation's tool also
// takes IdempotencyKeyArg, and its description ends with IdempotencyKeyNote.
func toolFor(op api.Operation) *mcp.Tool {
	destructive := op.Destructive
	openWorld := op.PublicEffect != ""
	desc, in := op.Describe(), op.InputSchema()
	if op.Idempotency {
		desc += IdempotencyKeyNote
		if in.Properties == nil {
			in.Properties = map[string]*jsonschema.Schema{}
		}
		minLen, maxLen := 1, 255
		in.Properties[IdempotencyKeyArg] = &jsonschema.Schema{
			Type:        "string",
			MinLength:   &minLen,
			MaxLength:   &maxLen,
			Description: "Optional. A unique key for this call; a retry with the same key returns the first result instead of creating a duplicate.",
		}
	}
	return &mcp.Tool{
		Name:         op.Name,
		Description:  desc,
		InputSchema:  in,
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
//
// When op takes an idempotency key and the call carries IdempotencyKeyArg,
// the key is removed before Decode (so the operation's unknown-field check
// still applies to everything else) and the call runs through
// api.RunIdempotent on idem, the same store and rules as REST's
// Idempotency-Key. The request hash covers the operation and the remaining
// arguments, canonicalised, so key order and spacing do not matter.
//
// The operation runs on api.CallContext: a read stops when the client
// cancels, and a write runs to completion.
func handlerFor(op api.Operation, idem api.IdempotencyStore, now func() time.Time) mcp.ToolHandler {
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
		var key string
		if op.Idempotency {
			var e *api.Error
			if key, raw, e = takeIdempotencyKey(raw); e != nil {
				return errorResult(e), nil
			}
		}
		in, err := op.Decode(raw)
		if err != nil {
			return errorResult(api.AsError(op.Name, err)), nil
		}
		call := func(ctx context.Context) (int, []byte, *api.Error) {
			out, err := op.Call(api.CallContext(ctx, op), in)
			if err != nil {
				return 0, nil, api.AsError(op.Name, err)
			}
			text, err := json.Marshal(out)
			if err != nil {
				return 0, nil, api.AsError(op.Name, err)
			}
			return http.StatusOK, text, nil
		}
		var (
			text []byte
			e    *api.Error
		)
		if key != "" && idem != nil {
			canon, err := canonicalArgs(raw)
			if err != nil {
				return errorResult(api.AsError(op.Name, err)), nil
			}
			sum := sha256.Sum256([]byte("mcp\n" + op.Name + "\n" + string(canon)))
			_, text, _, e = api.RunIdempotent(ctx, idem, now(), tok.ID, key, op.Name, sum[:], call)
		} else {
			_, text, e = call(ctx)
		}
		if e != nil {
			return errorResult(e), nil
		}
		return &mcp.CallToolResult{
			StructuredContent: json.RawMessage(text),
			Content:           []mcp.Content{&mcp.TextContent{Text: string(text)}},
		}, nil
	}
}

// takeIdempotencyKey removes IdempotencyKeyArg from raw, a tool's arguments,
// and returns it with the arguments that remain. Arguments that are not a
// JSON object are returned untouched for Decode to reject. A key that is
// not a string, or is not 1 to 255 printable ASCII characters, is a
// validation error naming the argument.
func takeIdempotencyKey(raw []byte) (string, []byte, *api.Error) {
	var obj map[string]json.RawMessage
	if raw == nil || json.Unmarshal(raw, &obj) != nil || obj == nil {
		return "", raw, nil
	}
	v, ok := obj[IdempotencyKeyArg]
	if !ok {
		return "", raw, nil
	}
	bad := api.Validation(map[string]string{IdempotencyKeyArg: "must be 1 to 255 printable ASCII characters"})
	var key string
	if err := json.Unmarshal(v, &key); err != nil || !api.ValidIdempotencyKey(key) {
		return "", nil, bad
	}
	delete(obj, IdempotencyKeyArg)
	rest, err := json.Marshal(obj)
	if err != nil {
		return "", nil, bad
	}
	return key, rest, nil
}

// canonicalArgs is raw re-encoded with sorted object keys and no
// insignificant whitespace, numbers kept exactly as sent: the bytes the
// request hash covers. Nil or empty raw is "{}".
func canonicalArgs(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("{}"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// errorResult is e as a tool error. Its text is the v1 error envelope, the
// same body a REST call would get. It carries no StructuredContent, so a
// client never validates an error against the tool's success OutputSchema.
func errorResult(e *api.Error) *mcp.CallToolResult {
	text, _ := json.Marshal(e.Body())
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(text)}}}
}
