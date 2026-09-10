package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestServeDevMode_WiresDevAutoLoginIntoMountAndServe is #0490: no test in
// this package ever referenced devAutoLogin before this one (confirmed by
// grepping every _test.go file in cmd/opencircuit for that identifier), so
// serveDevMode's line
//
//	devAutoLogin := middleware.DevAutoLogin(ds, ds, cfg.DevMode())
//	...
//	return mountAndServe(cfg, ds, ..., requireSession, requireAdmin, devAutoLogin, nil)
//
// was connected by exactly one argument position with nothing pinning it.
// mountAndServe's own doc comment says a nil outerMiddleware means "no
// wrapper" BY DESIGN (it is servePostgres's default in every ordinary
// configuration, #0402), so dropping the argument, reordering it, or
// swapping in a literal nil at that position compiles, boots, and serves —
// it just never logs a developer in. That silence is exactly why this is a
// wiring guard and not a behavior test: DevAutoLogin's own behavior is
// already pinned directly by TestDevAutoLogin_StaleCookieHeals and
// TestDevAutoLogin_GarbageCookieHeals (internal/middleware/devauth_test.go)
// -- what those tests cannot see is whether serveDevMode ever hands its
// constructed middleware to mountAndServe at all.
//
// # Why an AST call-site read, not a server (criterion 1)
//
// #0484's reviewer named the proportionate form directly: a wiring guard,
// not a whole HTTP server standing up STORAGE=json's internal/devstore. This
// guard detects the defect the same way
// TestServePostgres_OneSEOSiteFlowsToEveryConsumer
// (servepostgres_seo_instance_guard_test.go, #0326) already does for a
// structurally identical shape -- one function's source builds a value and
// must hand that SAME value to another function's specific parameter -- by
// re-parsing main.go fresh on every run and reading serveDevMode's actual
// call to mountAndServe, positionally, against mountAndServe's own current
// parameter list (looked up by name, never a hardcoded index, so a future
// parameter inserted before outerMiddleware does not stale this guard). A
// server-based test proves DevAutoLogin heals a session when it is present
// in the chain; it does not distinguish "present because serveDevMode wired
// it" from "present because the test harness wired it directly" -- the
// latter is exactly the shape TestNewDevAdminAutoLogin_RealRouteTable uses
// for the Postgres sibling (see the doc comment below on criterion 5), and
// this issue exists because that shape does not, in fact, prove the former.
//
// # Why this is not a §8/#0337 method-set question (criterion 3)
//
// #0337's guard had a receiver-form hole because it asked "does this type
// satisfy that interface", which is a go/types question a pointer-receiver-
// only AST walk cannot answer soundly (a value-receiver method still widens
// *T's method set). This guard asks a different, purely syntactic question:
// "is the identifier at this literal argument position the same identifier
// middleware.DevAutoLogin's own call result was bound to". No interface, no
// method set, and no receiver form is involved anywhere in this check, so
// go/types buys nothing here that go/ast does not already give directly --
// there is no satisfier set to widen. If a future version of this file ever
// needs to reason about which types satisfy some outerMiddleware-shaped
// interface, that reasoning should use go/types per §8, but that is not what
// this guard does.
//
// # Fail-closed shape (per #0275/#0300/#0326's own precedent)
//
// Both counting steps below (exactly one middleware.DevAutoLogin(...) call,
// exactly one mountAndServe(...) call in serveDevMode) fail loudly on zero or
// on more than one, rather than silently skipping -- an empty scan is
// evidence the scan itself broke (a rename, a move), never evidence there is
// nothing to check.
func TestServeDevMode_WiresDevAutoLoginIntoMountAndServe(t *testing.T) {
	fset := token.NewFileSet()
	mainFile, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	mountAndServeDecl := findFuncDecl(mainFile, "mountAndServe")
	if mountAndServeDecl == nil {
		t.Fatal("mountAndServe not found in main.go — has it been renamed or moved? " +
			"this guard's whole premise depends on that exact function existing")
	}
	outerIdx, found := paramIndexByName(mountAndServeDecl, "outerMiddleware")
	if !found {
		t.Fatal("mountAndServe has no parameter named outerMiddleware — has it been renamed? " +
			"fail closed rather than guessing a position")
	}

	serveDevModeDecl := findFuncDecl(mainFile, "serveDevMode")
	if serveDevModeDecl == nil {
		t.Fatal("serveDevMode not found in main.go — has it been renamed or moved? " +
			"this guard's whole premise depends on that exact function existing")
	}

	// Step 1: find serveDevMode's own construction of the dev auto-login
	// middleware -- devAutoLogin := middleware.DevAutoLogin(...) -- and
	// record which identifier its result is bound to. This is the value the
	// call site below must hand to mountAndServe; anything else (a second,
	// unrelated identifier, or a value never assigned from this call at all)
	// is exactly what this guard exists to catch.
	var devAutoLoginCalls []*ast.CallExpr
	devAutoLoginVar := ""
	ast.Inspect(serveDevModeDecl.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range assign.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok || pkgIdent.Name != "middleware" || sel.Sel.Name != "DevAutoLogin" {
				continue
			}
			devAutoLoginCalls = append(devAutoLoginCalls, call)
			if i < len(assign.Lhs) {
				if lhsIdent, ok := assign.Lhs[i].(*ast.Ident); ok {
					devAutoLoginVar = lhsIdent.Name
				}
			}
		}
		return true
	})
	switch len(devAutoLoginCalls) {
	case 0:
		t.Fatal("found zero middleware.DevAutoLogin(...) calls in serveDevMode — either this scan " +
			"is broken (fail closed) or serveDevMode no longer constructs the dev auto-login " +
			"middleware at all, which this whole guard assumes")
	case 1:
		// expected
	default:
		t.Fatalf("found %d middleware.DevAutoLogin(...) calls in serveDevMode, want exactly 1 — "+
			"this guard only understands a single construction bound to a single identifier",
			len(devAutoLoginCalls))
	}
	if devAutoLoginVar == "" {
		t.Fatal("middleware.DevAutoLogin's call was found but its result wasn't assigned to a plain " +
			"identifier (e.g. `devAutoLogin := middleware.DevAutoLogin(...)`) — this scan only " +
			"understands that shape")
	}

	// Step 2: find serveDevMode's own call to mountAndServe and check the
	// argument at outerMiddleware's position is exactly that identifier —
	// not nil, not some other variable, not missing outright.
	var mountCalls []*ast.CallExpr
	ast.Inspect(serveDevModeDecl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "mountAndServe" {
			return true
		}
		mountCalls = append(mountCalls, call)
		return true
	})
	switch len(mountCalls) {
	case 0:
		t.Fatal("found zero mountAndServe(...) calls in serveDevMode — either this scan is broken " +
			"(fail closed) or serveDevMode no longer starts the server through mountAndServe at all")
	case 1:
		// expected
	default:
		t.Fatalf("found %d mountAndServe(...) calls in serveDevMode, want exactly 1", len(mountCalls))
	}

	call := mountCalls[0]
	if outerIdx >= len(call.Args) {
		t.Fatalf("%s: serveDevMode's mountAndServe call has only %d argument(s), want at least %d "+
			"(mountAndServe's outerMiddleware position) — the argument appears to have been dropped",
			fset.Position(call.Pos()), len(call.Args), outerIdx+1)
	}
	arg := call.Args[outerIdx]
	ident, ok := arg.(*ast.Ident)
	if !ok || ident.Name != devAutoLoginVar {
		t.Fatalf("%s: serveDevMode passes %s at mountAndServe's outerMiddleware position (index %d), "+
			"want the identifier %q bound by middleware.DevAutoLogin's own call — mountAndServe's "+
			"doc comment says a nil (or wrong) outerMiddleware here means \"no wrapper\" BY DESIGN, "+
			"so this is silent: the server starts, serves, and never logs a developer in (#0490)",
			fset.Position(arg.Pos()), exprString(arg), outerIdx, devAutoLoginVar)
	}
}

// paramIndexByName returns the zero-based position of the parameter named
// name in fd's flattened parameter list (grouped parameters, `a, b Type`,
// expand to one slot per name, matching the order a call's Args slice
// uses) — the same lookup findFuncParamTypes
// (servepostgres_seo_instance_guard_test.go) does by name rather than by a
// hardcoded index, so a future parameter inserted before outerMiddleware
// does not silently stale this guard's position.
func paramIndexByName(fd *ast.FuncDecl, name string) (int, bool) {
	idx := 0
	for _, field := range fd.Type.Params.List {
		if len(field.Names) == 0 {
			idx++
			continue
		}
		for _, n := range field.Names {
			if n.Name == name {
				return idx, true
			}
			idx++
		}
	}
	return 0, false
}
