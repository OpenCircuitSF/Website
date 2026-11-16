package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

// OpenAPIPath is the public OpenAPI document's path. No operation may use it.
const OpenAPIPath = "/api/v1/openapi.json"

// anyTokenOps are the only operations that may set AnyToken (#0674's review):
// an empty scope must never authorize anything else, and above all nothing
// that changes data.
var anyTokenOps = map[string]bool{"whoami": true, "token_probe": true}

// Config is what a Registry's adapters need besides the operations.
type Config struct {
	Version     string           // the server version, in whoami and the OpenAPI info
	Idempotency IdempotencyStore // required when any operation sets Idempotency
	Now         func() time.Time // nil = time.Now; idempotency key expiry
}

// Registry holds the v1 operations in the order they were added. Add them at
// startup, then Freeze (NewHTTPRouter and #0679's MCP server both do).
type Registry struct {
	cfg    Config
	ops    []Operation
	names  map[string]bool
	routes map[string]bool
	frozen bool
}

// NewRegistry returns an empty registry.
func NewRegistry(cfg Config) *Registry {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Registry{cfg: cfg, names: map[string]bool{}, routes: map[string]bool{}}
}

// Add registers ops. It panics on any wiring bug: an operation not built by
// Define, a bad name or method, a path outside /api/v1/ or on the reserved
// OpenAPI path, a missing or unknown scope, AnyToken with a scope or on any
// operation but whoami and token_probe or on a non-GET, a non-GET whose
// scope is not a ":write" scope, a duplicate name or method+path, or a call
// after Freeze.
func (r *Registry) Add(ops ...Operation) {
	for _, op := range ops {
		if err := r.check(op); err != nil {
			panic("api: Add " + op.Name + ": " + err.Error())
		}
		r.names[op.Name] = true
		r.routes[op.Method+" "+op.Path] = true
		r.ops = append(r.ops, op)
	}
}

func (r *Registry) check(op Operation) error {
	switch {
	case r.frozen:
		return fmt.Errorf("registry is frozen")
	case op.call == nil:
		return fmt.Errorf("not built with api.Define")
	case !opNameRE.MatchString(op.Name):
		return fmt.Errorf("name must match %s", opNameRE)
	case !strings.HasPrefix(op.Path, "/api/v1/"):
		return fmt.Errorf("path %q is outside /api/v1/", op.Path)
	case op.Path == OpenAPIPath:
		return fmt.Errorf("path %q is reserved", op.Path)
	case strings.TrimSpace(op.Summary) == "":
		return fmt.Errorf("summary is required")
	}
	switch op.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return fmt.Errorf("method %q is not supported", op.Method)
	}
	if op.AnyToken {
		switch {
		case op.Scope != "":
			return fmt.Errorf("AnyToken with scope %q", op.Scope)
		case !anyTokenOps[op.Name]:
			return fmt.Errorf("only whoami and token_probe may set AnyToken")
		case op.Method != http.MethodGet:
			return fmt.Errorf("AnyToken on a %s: an empty scope must never authorize a change", op.Method)
		}
	} else {
		switch {
		case op.Scope == "":
			return fmt.Errorf("scope is required (or AnyToken)")
		case !apitokens.IsKnownScope(op.Scope):
			return fmt.Errorf("unknown scope %q", op.Scope)
		case op.Method != http.MethodGet && !strings.HasSuffix(op.Scope, ":write"):
			return fmt.Errorf("a %s must require a :write scope, not %q", op.Method, op.Scope)
		}
	}
	if r.names[op.Name] {
		return fmt.Errorf("duplicate name")
	}
	if r.routes[op.Method+" "+op.Path] {
		return fmt.Errorf("duplicate route %s %s", op.Method, op.Path)
	}
	if op.Status != 0 && (op.Status < 200 || op.Status > 299) {
		return fmt.Errorf("status %d is not a success status", op.Status)
	}
	return nil
}

// Freeze stops further Adds. It is safe to call more than once.
func (r *Registry) Freeze() { r.frozen = true }

// Operations returns the operations in Add order.
func (r *Registry) Operations() []Operation {
	return append([]Operation(nil), r.ops...)
}

// Version is Config.Version.
func (r *Registry) Version() string { return r.cfg.Version }
