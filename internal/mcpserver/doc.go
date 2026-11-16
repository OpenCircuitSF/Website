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
package mcpserver
