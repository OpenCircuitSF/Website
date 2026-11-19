// Package publicmcp is the public, read-only MCP server at /mcp (#0729).
// It serves a publicapi.Registry as tools over the SDK's stateless
// Streamable HTTP handler, with no authentication of any kind: it reads
// no Authorization header, never answers 401, and serves no OAuth
// metadata. It is a separate *mcp.Server from the admin one at /admin/mcp
// (internal/mcpserver), and shares no server, registry or middleware with
// it; TestNoAdminAuthImports keeps it that way.
package publicmcp
