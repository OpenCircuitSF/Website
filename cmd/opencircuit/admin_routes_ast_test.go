package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestAdminRoutesRegisteredOnlyViaTable is an AST test, not a runtime one —
// that is deliberate and needs explaining before anyone is tempted to delete
// it as over-engineering.
//
// adminRoutes() (main.go) is the single table mountAndServe loops over to
// register every admin-only route behind requireAdmin, and
// TestMountAndServe_AdminRoutesRequireSessionAndAdmin
// (admin_wiring_test.go) drives that exact loop, so a route ADDED TO THE
// TABLE has its guard proven automatically. But nothing stops a route being
// registered directly against mux, beside the loop, bypassing the table (and
// therefore the guard) entirely. #0024's reviewer demonstrated it (#0079):
// adding, anywhere in mountAndServe outside the adminRoutes loop,
//
//	mux.Handle("GET /admin/rogue", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//		w.WriteHeader(http.StatusOK)
//		_, _ = w.Write([]byte(`{"rogue":true,"guard":"none"}`))
//	}))
//
// left `go test ./... -count=1` reporting all 13 packages ok, while a live
// probe showed GET /admin/rogue answering 200 unauthenticated (vs. 403 for
// GET /admin/settings). net/http.ServeMux exposes no way to enumerate its
// registered patterns at runtime, so no runtime test — however thorough —
// can see a pattern string that was never handed to it through a path this
// test also inspects. Only reading the source can.
//
// So this test parses main.go's AST and looks at every call of the form
// mux.Handle(pattern, ...) / mux.HandleFunc(pattern, ...) whose pattern is a
// STRING LITERAL. The one and only legitimate admin registration —
// `mux.Handle(r.method+" "+r.path, requireAdmin(r.handler))` inside the
// adminRoutes loop — builds its pattern from the loop variable, which is a
// concatenation (*ast.BinaryExpr), never a literal, so it is structurally
// exempt without this test needing to know anything about loops or
// scoping. Any OTHER mux.Handle/HandleFunc call whose literal pattern's path
// begins with "/admin" is therefore, by construction, a route that bypassed
// the table — exactly the #0024 reproduction above.
//
// Deliberately /admin-only, not /api or /account too (#0079's acceptance
// criteria ask this decision to be recorded either way): /admin is the only
// prefix with a single table (adminRoutes) a route can be added to OR
// bypass — that specific shape is what makes an AST check worth having.
// /api and /account have no such table: every /api and /account
// mux.Handle/HandleFunc call in mountAndServe already carries its own guard
// (or deliberate absence of one) written directly at the call site —
// requireSession(...) for /account/credentials* and /api/me;
// requireAdmin(...) for /api/events (#0048: its one real consumer, live
// campaign send progress, is admin data — see main.go's route comment for
// why it stays outside this table despite being admin-gated); no
// wrapper at all for the intentionally public /api/interests,
// /api/subscribe, /api/subscribe/confirm, and /api/preferences, each with
// its own comment explaining why. An AST rule there would have to encode
// "which /api routes are meant to be public", a materially different and
// far less mechanical property than "was this route added to the one admin
// table" — and public-vs-authenticated is already legible at each call site
// without one.
func TestAdminRoutesRegisteredOnlyViaTable(t *testing.T) {
	const mainGoPath = "main.go"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainGoPath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", mainGoPath, err)
	}

	var violations []string

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != "mux" {
			return true
		}
		if sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc" {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}

		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			// Not a literal pattern — the adminRoutes loop's
			// `r.method+" "+r.path` lands here. Since #0675 replaced the
			// /api/v1/ loop with one api.Mount call (which registers its
			// patterns inside internal/api, and is
			// TestAPIV1AndMCPMountedOnlyViaRegistry's subject, as is #0679's
			// mcpserver.Mount, which registers /admin/mcp, the one /admin
			// path behind an API key instead of requireAdmin, #0728), that is the only
			// non-literal pattern mux.Handle/HandleFunc is called with in
			// this file (verified by inspection), so it is not further
			// validated here —
			// TestMountAndServe_AdminRoutesRequireSessionAndAdmin
			// (admin_wiring_test.go) is what proves its guard.
			return true
		}

		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("%s: unquote pattern literal %s: %v", fset.Position(lit.Pos()), lit.Value, err)
		}

		path := pattern
		if _, rest, found := strings.Cut(pattern, " "); found {
			path = rest
		}

		if strings.HasPrefix(path, "/admin") {
			violations = append(violations, fmt.Sprintf(
				"%s: mux.%s(%q, ...) registers an /admin route directly, bypassing the adminRoutes table",
				fset.Position(call.Pos()), sel.Sel.Name, pattern,
			))
		}
		return true
	})

	if len(violations) > 0 {
		t.Fatalf(
			"found /admin route(s) registered outside the adminRoutes table — every /admin/* route must be "+
				"added to adminRoutes() (main.go) and registered only by the `for _, r := range adminRoutes(...)` "+
				"loop in mountAndServe, so requireAdmin is applied structurally instead of by hand:\n%s",
			strings.Join(violations, "\n"),
		)
	}
}

// TestAPIV1AndMCPMountedOnlyViaRegistry (#0675, #0679) is the /api/v1 and
// /admin/mcp (#0728) twin of the test above. Every v1 route comes from the operation
// registry (handlers.BuildV1Registry), and api.NewHTTPRouter puts each behind
// RequireAPIToken, the per-token rate limit and the scope check; /admin/mcp is the
// same registry served by mcpserver.NewHandler, behind the same token and
// rate-limit middleware. A route mounted on mux directly, beside either,
// would get none of that. So: no mux.Handle/HandleFunc in main.go may have
// a literal pattern whose path starts with /api/v1, /admin/mcp or /mcp; api.Mount must
// be called exactly once, with mux and a call to api.NewHTTPRouter; and
// mcpserver.Mount exactly once, with mux and a call to
// mcpserver.NewHandler. (Both Mounts register one "<METHOD> <path>" pattern
// per method, because a pattern with no method conflicts with the SPA's
// "GET /" in Go's ServeMux.)
func TestAPIV1AndMCPMountedOnlyViaRegistry(t *testing.T) {
	if v := v1MountViolations(t, "main.go"); len(v) > 0 {
		t.Fatalf("/api/v1 and /admin/mcp must each be mounted once, as api.Mount(mux, api.NewHTTPRouter(...)) and mcpserver.Mount(mux, mcpserver.NewHandler(...)):\n%s", strings.Join(v, "\n"))
	}
}

// TestAPIV1MountGuardCatchesRogueRoute proves the guard above fails: copies
// of main.go with a rogue literal v1 or MCP route, with the router or MCP
// handler swapped for another call, and with a second mount of either each
// report a violation. The copies live in t.TempDir() and are only parsed,
// never compiled; main.go itself is never touched.
func TestAPIV1MountGuardCatchesRogueRoute(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	const mount = `api.Mount(mux, api.NewHTTPRouter(`
	const mcpMount = `mcpserver.Mount(mux, mcpserver.NewHandler(`
	for _, m := range []string{mount, mcpMount} {
		if strings.Count(string(src), m) != 1 {
			t.Fatalf("main.go does not hold exactly one %q", m)
		}
	}
	cases := map[string]string{
		"rogue": strings.Replace(string(src), mount,
			`mux.Handle("GET /api/v1/rogue", http.NotFoundHandler())`+"\n\t\t"+mount, 1),
		"bypassed": strings.Replace(string(src), mount, `api.Mount(mux, handlers.NotARouter(`, 1),
		"twice":    strings.Replace(string(src), mount, `api.Mount(mux, api.NewHTTPRouter(t.V1(), api.RouterDeps{}))`+"\n\t\t"+mount, 1),
		"mcp rogue": strings.Replace(string(src), mcpMount,
			`mux.Handle("POST /mcp", http.NotFoundHandler())`+"\n\t\t"+mcpMount, 1),
		"admin mcp rogue": strings.Replace(string(src), mcpMount,
			`mux.Handle("POST /admin/mcp", http.NotFoundHandler())`+"\n\t\t"+mcpMount, 1),
		"mcp subpath": strings.Replace(string(src), mcpMount,
			`mux.HandleFunc("/mcp/x", nil)`+"\n\t\t"+mcpMount, 1),
		"mcp bypassed": strings.Replace(string(src), mcpMount, `mcpserver.Mount(mux, handlers.NotAnMCPHandler(`, 1),
		"mcp twice":    strings.Replace(string(src), mcpMount, `mcpserver.Mount(mux, mcpserver.NewHandler(mcpserver.Deps{}))`+"\n\t\t"+mcpMount, 1),
		"mcp missing":  strings.Replace(string(src), mcpMount, `noMount(mux, mcpserver.NewHandler(`, 1),
	}
	for name, body := range cases {
		p := filepath.Join(t.TempDir(), "main.go")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if v := v1MountViolations(t, p); len(v) == 0 {
			t.Errorf("%s: the guard reported nothing", name)
		}
	}
}

// v1MountViolations parses path and lists every /api/v1, /admin/mcp or /mcp mux
// registration that is not the one registry mount of each.
func v1MountViolations(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var violations []string
	mounts, mcpMounts := 0, 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		pos := fset.Position(call.Pos())
		switch {
		case recv.Name == "api" && sel.Sel.Name == "Mount":
			mounts++
			if m, ok := call.Args[0].(*ast.Ident); !ok || m.Name != "mux" || !isPkgCall(call.Args[1], "api", "NewHTTPRouter") {
				violations = append(violations, fmt.Sprintf("%s: api.Mount must be given mux and api.NewHTTPRouter(...)", pos))
			}
		case recv.Name == "mcpserver" && sel.Sel.Name == "Mount":
			mcpMounts++
			if m, ok := call.Args[0].(*ast.Ident); !ok || m.Name != "mux" || !isPkgCall(call.Args[1], "mcpserver", "NewHandler") {
				violations = append(violations, fmt.Sprintf("%s: mcpserver.Mount must be given mux and mcpserver.NewHandler(...)", pos))
			}
		case recv.Name == "mux" && (sel.Sel.Name == "Handle" || sel.Sel.Name == "HandleFunc"):
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			pattern, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("%s: unquote %s: %v", fset.Position(lit.Pos()), lit.Value, err)
			}
			p := pattern
			if _, rest, found := strings.Cut(pattern, " "); found {
				p = rest
			}
			if strings.HasPrefix(p, "/api/v1") {
				violations = append(violations, fmt.Sprintf("%s: mux.%s(%q, ...) bypasses the v1 registry", pos, sel.Sel.Name, pattern))
			}
			// #0729 must revisit this line: it will take /mcp for the public server.
			if p == "/mcp" || strings.HasPrefix(p, "/mcp/") || p == "/admin/mcp" || strings.HasPrefix(p, "/admin/mcp/") {
				violations = append(violations, fmt.Sprintf("%s: mux.%s(%q, ...) bypasses mcpserver.Mount", pos, sel.Sel.Name, pattern))
			}
		}
		return true
	})
	if mounts != 1 {
		violations = append(violations, fmt.Sprintf("%s: api.Mount is called %d times, want 1", path, mounts))
	}
	if mcpMounts != 1 {
		violations = append(violations, fmt.Sprintf("%s: mcpserver.Mount is called %d times, want 1", path, mcpMounts))
	}
	return violations
}

// isPkgCall reports whether e is a call pkg.fn(...).
func isPkgCall(e ast.Expr, pkg, fn string) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != fn {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}
