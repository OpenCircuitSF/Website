// Package publicapi is the shared definition of the public, read-only API
// (#0729): the operations anyone may call with no account or key. Each is
// an api.Operation, so bind, decode, schema and error rules are the v1
// API's, but the Registry here is a separate type from api.Registry and
// inverts its checks: a public operation has no scope, is a GET under
// Prefix, and has no destructive or public effect. An admin operation
// therefore cannot be added to it.
//
// internal/publicmcp serves this registry as MCP tools at /mcp; #0734 adds
// the REST router and OpenAPI document under Prefix. Both call operations
// through Registry.Invoke, the single call path.
package publicapi
