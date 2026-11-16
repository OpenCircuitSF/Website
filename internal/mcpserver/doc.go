// Package mcpserver serves the /api/v1 operation registry (#0675) as an MCP
// server at /mcp (#0679), so Claude Code and other MCP clients can call the
// same operations the REST adapter serves.
//
// It is the second adapter over internal/api's Registry. Every tool is an
// api.Operation: its name, description, input and output schemas come from
// the registry, and a call runs the operation's own Decode and Call in
// process. No tool is written by hand, and nothing calls back over HTTP.
//
// The transport is the official SDK's Streamable HTTP handler, stateless and
// answering with application/json only, so there are no sessions and no SSE
// for a proxy to buffer. In front of it sit the same RequireAPIToken and
// per-token rate limit as /api/v1/, so a missing or invalid key gets the v1
// 401 and a key has one request budget across both. The tool list follows
// the key: each distinct scope set gets its own cached server holding only
// the tools those scopes allow.
//
// Idempotency (#0685). Every operation with Idempotency set (a create) gets
// an optional idempotency_key argument, its MCP equivalent of REST's
// Idempotency-Key header. The adapter removes the key before Decode and runs
// the call through api.RunIdempotent on the registry's IdempotencyStore: the
// same table, the same per-API-key scope, the same 24-hour retention and
// abandoned-key takeover. A retry with the same key and arguments replays
// the first result, different arguments or a call still running is a
// conflict, and a failure forgets the key. The request hash is MCP's own,
// covering the operation name and canonicalised arguments. A key used over
// REST and then over MCP is therefore a conflict, not a replay. An
// operation registered later with Idempotency set gets all of this without
// any change here.
//
// Cancellation (#0685). The handler sets PropagateRequestCancellation, so a
// tool call's context ends with its HTTP request. Each call then runs on
// api.CallContext, which REST shares: a read (GET) stops when the client
// cancels or disconnects. A write runs on a context that is never cancelled,
// so a create, send or publish that has started always finishes, and its
// audit entries and idempotency result are recorded. Running a write to
// completion is the safe choice for an operation with a PublicEffect, where
// a half-finished send could not be retried cleanly. For other writes it
// costs nothing.
package mcpserver
