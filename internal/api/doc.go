// Package api is the /api/v1 operation registry (#0675).
//
// An Operation, built with Define, is one API call: its name, method, path,
// required scope, input and output types, and the function that runs it. A
// Registry holds every operation. Two adapters serve one registry:
//
//   - NewHTTPRouter (http.go) serves each operation as a REST route under
//     /api/v1/, behind middleware.RequireAPIToken and the per-token rate
//     limit, plus the public OpenAPI document at OpenAPIPath, which
//     is generated from the same registry so it cannot drift from it.
//   - The MCP server (#0679) exposes each operation as a tool, using
//     Operation.InputSchema, OutputSchema, Describe, Decode and Call.
//
// Both adapters decode input through one set of rules (bind.go): unknown
// fields are rejected and a type mismatch names its field. Every error is the
// ErrorBody envelope, and the only place an underlying error's text goes is
// the log (AsError), never a response.
//
// This package is generic: it knows about tokens, scopes, JSON, schemas and
// idempotency, and nothing about guides or events. The domain operations live
// in internal/handlers (BuildV1Registry), beside the admin handlers whose
// stores they reuse. This package must not import internal/handlers.
package api
