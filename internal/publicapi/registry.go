package publicapi

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

// Prefix is the path every public operation lives under.
const Prefix = "/api/public/v1/"

// OpenAPIPath is reserved for the public OpenAPI document (#0734), so no
// operation may take it.
const OpenAPIPath = "/api/public/v1/openapi.json"

// Op is a public operation: an api.Operation built with api.Define, plus a
// one-line summary of a result for clients that show text.
type Op struct {
	api.Operation
	Summarize func(out any) string
}

// Define is api.Define plus a typed summariser.
func Define[In, Out any](op api.Operation, fn func(context.Context, In) (Out, error), summarize func(Out) string) Op {
	o := Op{Operation: api.Define(op, fn)}
	if summarize != nil {
		o.Summarize = func(out any) string {
			v, _ := out.(Out)
			return summarize(v)
		}
	}
	return o
}

// Describe is the tool and OpenAPI description: the summary, the detail,
// and a note that no key is needed.
func (o Op) Describe() string {
	return strings.TrimSpace(o.Summary+" "+o.Description) + " Read-only; no account or key needed."
}

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Registry holds the public operations. It is not an api.Registry.
type Registry struct {
	version string
	ops     []Op
	names   map[string]bool
	paths   map[string]bool
	frozen  bool
}

// NewRegistry returns an empty registry reporting version.
func NewRegistry(version string) *Registry {
	return &Registry{version: version, names: map[string]bool{}, paths: map[string]bool{}}
}

// Add registers ops. It panics ("publicapi: Add <name>: <reason>") on
// anything that is not a public, read-only operation: a frozen registry,
// an op not built with Define, no summariser, a bad name, a method other
// than GET, a path outside Prefix or equal to OpenAPIPath, a scope or
// AnyToken, Destructive, PublicEffect, Idempotency, MCPExcluded, a
// non-default Status, an empty Summary, or a duplicate name or path.
// Every admin operation has a scope or AnyToken and an /api/v1/ path, so
// none can be added.
func (r *Registry) Add(ops ...Op) {
	for _, op := range ops {
		if reason := r.reject(op); reason != "" {
			panic("publicapi: Add " + op.Name + ": " + reason)
		}
		r.names[op.Name] = true
		r.paths[op.Path] = true
		r.ops = append(r.ops, op)
	}
}

func (r *Registry) reject(op Op) string {
	switch {
	case r.frozen:
		return "registry is frozen"
	case !op.Defined():
		return "not built with Define"
	case op.Summarize == nil:
		return "no Summarize"
	case !nameRE.MatchString(op.Name):
		return "name must match " + nameRE.String()
	case op.Method != http.MethodGet:
		return "method must be GET, not " + op.Method
	case !strings.HasPrefix(op.Path, Prefix):
		return "path must start with " + Prefix
	case op.Path == OpenAPIPath:
		return "path is reserved for the OpenAPI document"
	case op.Scope != "" || op.AnyToken:
		return "a public operation takes no scope or token"
	case op.Destructive:
		return "a public operation is not destructive"
	case op.PublicEffect != "":
		return "a public operation has no public effect"
	case op.Idempotency:
		return "a public operation takes no idempotency key"
	case op.MCPExcluded != "":
		return "every public operation is an MCP tool"
	case op.Status != 0:
		return "a public operation answers 200"
	case strings.TrimSpace(op.Summary) == "":
		return "empty Summary"
	case r.names[op.Name]:
		return "duplicate name"
	case r.paths[op.Path]:
		return "duplicate path " + op.Path
	}
	return ""
}

// Freeze stops further Adds.
func (r *Registry) Freeze() { r.frozen = true }

// Ops returns a copy of the operations in the order added.
func (r *Registry) Ops() []Op { return append([]Op(nil), r.ops...) }

// Version is the version the registry was built with.
func (r *Registry) Version() string { return r.version }

// Invoke runs op on in, a value op.Decode (or #0734's REST binder)
// returned. It is the single call path every adapter uses, so logging and
// caching (#0730) belong here.
func (r *Registry) Invoke(ctx context.Context, op Op, in any) (any, error) {
	return op.Call(ctx, in)
}
