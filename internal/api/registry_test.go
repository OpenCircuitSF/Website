package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

type emptyIn struct{}
type okOut struct {
	OK bool `json:"ok"`
}

func okFn[In any](context.Context, In) (okOut, error) { return okOut{OK: true}, nil }

func getOp(name, path, scope string) Operation {
	return Define(Operation{Name: name, Method: http.MethodGet, Path: path, Scope: scope, Summary: "s"}, okFn[emptyIn])
}

type bodyIn struct {
	Title string `json:"title"`
}

func writeOp(method, scope string) Operation {
	return Define(Operation{Name: "write_thing", Method: method, Path: "/api/v1/things", Scope: scope, Summary: "s"}, okFn[bodyIn])
}

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: no panic", name)
		}
	}()
	f()
}

func TestAddPanicsOnWiringBugs(t *testing.T) {
	read := apitokens.ScopeEngagementRead
	cases := []struct {
		name string
		ops  []Operation
	}{
		{"outside /api/v1/", []Operation{getOp("a", "/api/x", read)}},
		{"unknown scope", []Operation{getOp("a", "/api/v1/a", "admin:write")}},
		{"empty scope without AnyToken", []Operation{getOp("a", "/api/v1/a", "")}},
		{"AnyToken with a scope", []Operation{Define(Operation{Name: "whoami", Method: http.MethodGet, Path: "/api/v1/a", Scope: read, AnyToken: true, Summary: "s"}, okFn[emptyIn])}},
		{"AnyToken on another name", []Operation{Define(Operation{Name: "list_all", Method: http.MethodGet, Path: "/api/v1/a", AnyToken: true, Summary: "s"}, okFn[emptyIn])}},
		{"AnyToken on a POST", []Operation{Define(Operation{Name: "whoami", Method: http.MethodPost, Path: "/api/v1/a", AnyToken: true, Summary: "s"}, okFn[bodyIn])}},
		{"duplicate name", []Operation{getOp("a", "/api/v1/a", read), getOp("a", "/api/v1/b", read)}},
		{"duplicate method+path", []Operation{getOp("a", "/api/v1/a", read), getOp("b", "/api/v1/a", read)}},
		{"bad name", []Operation{getOp("Bad-Name", "/api/v1/a", read)}},
		{"reserved openapi path", []Operation{getOp("a", OpenAPIPath, read)}},
		{"not built by Define", []Operation{{Name: "a", Method: http.MethodGet, Path: "/api/v1/a", Scope: read, Summary: "s"}}},
		{"no summary", []Operation{Define(Operation{Name: "a", Method: http.MethodGet, Path: "/api/v1/a", Scope: read}, okFn[emptyIn])}},
	}
	for _, c := range cases {
		mustPanic(t, c.name, func() { NewRegistry(Config{}).Add(c.ops...) })
	}
	frozen := NewRegistry(Config{})
	frozen.Freeze()
	mustPanic(t, "Add after Freeze", func() { frozen.Add(getOp("a", "/api/v1/a", read)) })
}

// TestWriteOperationRequiresWriteScope (#0674's review): an empty or read
// scope never authorizes anything that changes data.
func TestWriteOperationRequiresWriteScope(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		for _, scope := range []string{"", apitokens.ScopeGuidePostsRead, apitokens.ScopeEngagementRead} {
			mustPanic(t, m+" with "+scope, func() { NewRegistry(Config{}).Add(writeOp(m, scope)) })
		}
	}
	mustPanic(t, "DELETE with a read scope", func() {
		NewRegistry(Config{}).Add(Define(Operation{Name: "d", Method: http.MethodDelete, Path: "/api/v1/d/{id}", Scope: apitokens.ScopeGuidePostsRead, Summary: "s"},
			okFn[struct {
				ID int64 `json:"id" path:"id"`
			}]))
	})
	NewRegistry(Config{}).Add(writeOp(http.MethodPost, apitokens.ScopeGuidePostsWrite)) // the allowed shape
}

func TestDefinePanicsOnBadShapes(t *testing.T) {
	mustPanic(t, "non-struct Out", func() {
		Define(Operation{Name: "a", Method: http.MethodGet, Path: "/api/v1/a"}, func(context.Context, emptyIn) ([]int, error) { return nil, nil })
	})
	mustPanic(t, "non-struct In", func() {
		Define(Operation{Name: "a", Method: http.MethodGet, Path: "/api/v1/a"}, func(context.Context, int) (okOut, error) { return okOut{}, nil })
	})
	mustPanic(t, "GET with a body field", func() {
		Define(Operation{Name: "a", Method: http.MethodGet, Path: "/api/v1/a"}, okFn[bodyIn])
	})
	mustPanic(t, "path tag missing from Path", func() {
		Define(Operation{Name: "a", Method: http.MethodGet, Path: "/api/v1/a"}, okFn[struct {
			ID int64 `json:"id" path:"id"`
		}])
	})
	mustPanic(t, "{x} with no field", func() {
		Define(Operation{Name: "a", Method: http.MethodGet, Path: "/api/v1/a/{id}"}, okFn[emptyIn])
	})
	mustPanic(t, "field without a json tag", func() {
		Define(Operation{Name: "a", Method: http.MethodPost, Path: "/api/v1/a"}, okFn[struct{ Title string }])
	})
	mustPanic(t, "unsupported URL type", func() {
		Define(Operation{Name: "a", Method: http.MethodGet, Path: "/api/v1/a"}, okFn[struct {
			IDs []int `json:"ids,omitempty" query:"ids"`
		}])
	})
}

func TestSchemaTimeIsDateTime(t *testing.T) {
	type in struct {
		At    time.Time  `json:"at"`
		Until *time.Time `json:"until,omitempty"`
	}
	op := Define(Operation{Name: "a", Method: http.MethodPost, Path: "/api/v1/a"}, okFn[in])
	s := op.InputSchema()
	if p := s.Properties["at"]; p == nil || p.Type != "string" || p.Format != "date-time" {
		t.Fatalf("at schema %+v", p)
	}
	if p := s.Properties["until"]; p == nil || p.Format != "date-time" {
		t.Fatalf("until schema %+v", p)
	}
	if s.AdditionalProperties == nil {
		t.Fatal("input schema allows additional properties")
	}
}

func TestDescribePutsWarningsFirst(t *testing.T) {
	op := Define(Operation{Name: "a", Method: http.MethodPost, Path: "/api/v1/a", Scope: apitokens.ScopeGuidePostsWrite,
		Summary: "Post it.", Destructive: true, PublicEffect: "posts publicly to Bluesky right away"}, okFn[bodyIn])
	d := op.Describe()
	if want := "DESTRUCTIVE: PUBLIC: posts publicly to Bluesky right away. Post it."; !strings.HasPrefix(d, want) {
		t.Fatalf("Describe %q, want prefix %q", d, want)
	}
	if !strings.HasSuffix(d, "Requires the guide_posts:write scope.") {
		t.Fatalf("Describe %q lacks the scope", d)
	}
	plain := getOp("b", "/api/v1/b", apitokens.ScopeEngagementRead).Describe()
	if strings.Contains(plain, "DESTRUCTIVE") || strings.Contains(plain, "PUBLIC") {
		t.Fatalf("plain op warns: %q", plain)
	}
}
