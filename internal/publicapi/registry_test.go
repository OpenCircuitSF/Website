package publicapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

type tIn struct{}
type tOut struct {
	OK bool `json:"ok"`
}

func good() api.Operation {
	return api.Operation{Name: "get_x", Method: http.MethodGet, Path: Prefix + "x", Summary: "Get x."}
}

func def(op api.Operation) Op {
	return Define(op, func(context.Context, tIn) (tOut, error) { return tOut{OK: true}, nil }, func(tOut) string { return "x" })
}

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("%s: Add did not panic", name)
		} else if !strings.HasPrefix(r.(string), "publicapi: Add ") {
			t.Errorf("%s: panic %v", name, r)
		}
	}()
	f()
}

func TestAddRejectsNonPublicOperations(t *testing.T) {
	with := func(f func(*api.Operation)) Op { op := good(); f(&op); return def(op) }
	cases := map[string]func(r *Registry){
		"frozen":       func(r *Registry) { r.Freeze(); r.Add(def(good())) },
		"not defined":  func(r *Registry) { r.Add(Op{Operation: good(), Summarize: func(any) string { return "" }}) },
		"no summarize": func(r *Registry) { op := def(good()); op.Summarize = nil; r.Add(op) },
		"bad name":     func(r *Registry) { r.Add(with(func(o *api.Operation) { o.Name = "Get-X" })) },
		"post": func(r *Registry) {
			r.Add(Op{Operation: api.Define(api.Operation{Name: "x", Method: http.MethodPost, Path: Prefix + "x", Summary: "x"}, func(context.Context, tIn) (tOut, error) { return tOut{}, nil }), Summarize: func(any) string { return "" }})
		},
		"admin path":     func(r *Registry) { r.Add(with(func(o *api.Operation) { o.Path = "/api/v1/x" })) },
		"openapi path":   func(r *Registry) { r.Add(with(func(o *api.Operation) { o.Path = OpenAPIPath })) },
		"scope":          func(r *Registry) { r.Add(with(func(o *api.Operation) { o.Scope = "events:read" })) },
		"any token":      func(r *Registry) { r.Add(with(func(o *api.Operation) { o.AnyToken = true })) },
		"destructive":    func(r *Registry) { r.Add(with(func(o *api.Operation) { o.Destructive = true })) },
		"public effect":  func(r *Registry) { r.Add(with(func(o *api.Operation) { o.PublicEffect = "posts" })) },
		"idempotency":    func(r *Registry) { r.Add(with(func(o *api.Operation) { o.Idempotency = true })) },
		"mcp excluded":   func(r *Registry) { r.Add(with(func(o *api.Operation) { o.MCPExcluded = "no" })) },
		"status":         func(r *Registry) { r.Add(with(func(o *api.Operation) { o.Status = 201 })) },
		"empty summary":  func(r *Registry) { r.Add(with(func(o *api.Operation) { o.Summary = " " })) },
		"duplicate name": func(r *Registry) { r.Add(def(good()), with(func(o *api.Operation) { o.Path = Prefix + "y" })) },
		"duplicate path": func(r *Registry) { r.Add(def(good()), with(func(o *api.Operation) { o.Name = "get_y" })) },
	}
	for name, f := range cases {
		mustPanic(t, name, func() { f(NewRegistry("t")) })
	}
}

func TestAddAcceptsAPublicGet(t *testing.T) {
	r := NewRegistry("v")
	r.Add(def(good()))
	r.Freeze()
	ops := r.Ops()
	if len(ops) != 1 || r.Version() != "v" {
		t.Fatalf("ops %d, version %q", len(ops), r.Version())
	}
	out, err := r.Invoke(context.Background(), ops[0], tIn{})
	if err != nil || out.(tOut) != (tOut{OK: true}) || ops[0].Summarize(out) != "x" {
		t.Fatalf("invoke = %v, %v", out, err)
	}
	if d := ops[0].Describe(); d != "Get x. Read-only; no account or key needed." {
		t.Errorf("Describe = %q", d)
	}
}

func TestClampLimit(t *testing.T) {
	p := func(n int) *int { return &n }
	for _, c := range []struct {
		in   *int
		want int
	}{{nil, 10}, {p(0), 10}, {p(-5), 10}, {p(7), 7}, {p(50), 50}, {p(51), 50}, {p(10000), 50}} {
		if got := ClampLimit(c.in); got != c.want {
			t.Errorf("ClampLimit(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCapQuery(t *testing.T) {
	if got := CapQuery(strings.Repeat("a", 500)); len(got) != 200 {
		t.Errorf("ascii len = %d", len(got))
	}
	got := CapQuery(strings.Repeat("é", 300))
	if utf8.RuneCountInString(got) != 200 || !utf8.ValidString(got) {
		t.Errorf("runes = %d, valid %v", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
	if got := CapQuery("  esp32  "); got != "esp32" {
		t.Errorf("trim = %q", got)
	}
}
