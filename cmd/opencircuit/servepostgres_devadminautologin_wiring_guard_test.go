package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestServePostgres_WiresDevAdminAutoLoginIntoMountAndServe is #0492:
// servePostgres has the identical unpinned wiring gap #0490 found and fixed
// on the json path. Confirmed independently three times before this test
// existed -- by #0490's implementer, by its reviewer, and by the
// orchestrator -- that grepping every _test.go file in this package for
// servePostgres( returns nothing, so servePostgres's own
//
//	devAdminAutoLogin, err := newDevAdminAutoLogin(ctx, cfg, store, pool, slog.Default())
//	...
//	return mountAndServe(cfg, pool, ..., requireSession, requireAdmin, devAdminAutoLogin, nil)
//
// was connected by exactly one argument position with nothing pinning it.
// TestNewDevAdminAutoLogin_RealRouteTable (devadmin_test.go, #0484) builds
// devAdminAutoLogin itself with a direct newDevAdminAutoLogin call in the
// test body and hands it to its own startDevAdminWiringServer helper, which
// calls mountAndServe directly -- so it proves the middleware heals a
// session once wired, never that servePostgres itself does the wiring.
// mountAndServe's own doc comment says a nil (or wrong) outerMiddleware
// means "no wrapper" BY DESIGN, so the failure here is silent exactly the
// way #0490 described: the server starts, serves Postgres traffic, and
// simply never logs a developer in.
//
// # Same shape as #0490, reusing its helpers directly (criterion 1)
//
// This is modelled on TestServeDevMode_WiresDevAutoLoginIntoMountAndServe
// (servedevmode_devautologin_wiring_guard_test.go, #0490), itself modelled
// on TestServePostgres_OneSEOSiteFlowsToEveryConsumer
// (servepostgres_seo_instance_guard_test.go, #0326): re-parse main.go fresh
// on every run, resolve mountAndServe's outerMiddleware parameter position
// by name via paramIndexByName (never a hardcoded index), find the
// construction inside the caller's own body, find the caller's own call to
// mountAndServe, and assert the argument at the resolved position is
// exactly the identifier the construction bound. findFuncDecl and
// exprString (both from servepostgres_seo_instance_guard_test.go) and
// paramIndexByName (from #0490's file) are reused as-is -- nothing here
// redefines them, since Go test files in one package share top-level scope
// and a redefinition would be a duplicate-declaration compile error.
//
// # Two real shape differences from #0490, both handled deliberately
//
// serveDevMode's construction is `devAutoLogin :=
// middleware.DevAutoLogin(...)` -- a package-qualified selector call bound
// to a single identifier. servePostgres's is
// `devAdminAutoLogin, err := newDevAdminAutoLogin(...)` -- a bare,
// package-local identifier call (newDevAdminAutoLogin lives in this same
// package, so there is no middleware.-style selector to match) bound to
// TWO identifiers via a two-value assignment. Both differences are handled
// explicitly below rather than by silently widening #0490's own matcher
// (which stays untouched) to accept either shape:
//
//  1. The call-target match below tests call.Fun.(*ast.Ident) with
//     Name == "newDevAdminAutoLogin", not a *ast.SelectorExpr -- the
//     opposite AST shape from #0490's pkgIdent.Name == "middleware" check,
//     because there is no package qualifier on a same-package call.
//  2. The two-value assignment is handled by the same "index into Rhs, not
//     Lhs" logic #0490's scan already uses, which -- read closely -- does
//     the right thing here without any change: go/ast represents
//     `a, b := f(...)` as ONE AssignStmt with Rhs == []ast.Expr{f(...)}
//     (a single call expression, however many values it returns) and
//     Lhs == []ast.Expr{a, b}. Ranging over Rhs therefore visits the call
//     exactly once at i == 0, and assign.Lhs[i] is assign.Lhs[0] -- the
//     FIRST assigned name, which by this codebase's own (err last)
//     convention is the middleware value, not the error. So indexing by
//     the call's position in Rhs, rather than by "the last name in Lhs" or
//     "the only name in Lhs", is what makes the same scan shape correct for
//     both a single-value and a two-value construction; it was not
//     rewritten for this file, only reused. Nothing here inspects or
//     asserts on the `err` identifier at all -- whether servePostgres
//     checks that error correctly is #0484's and #0402's concern, not this
//     wiring guard's.
//
// # Why one guard per call site, not a shared generalisation (criterion 4)
//
// The two shape differences above -- selector call vs. bare identifier
// call, and which of two matchers applies -- are exactly the kind of extra
// branching a shared helper would need to carry permanently, for two call
// sites that will essentially never gain a third sibling. Folding both into
// one parameterised function means every future reader of the ONE surviving
// wiring guard has to hold both shapes in mind to change either call site's
// check, and a mistake in the shared branching (matching the wrong shape
// for the wrong call site) would silently pass both. Two small, direct
// tests -- each reading exactly one function's source for exactly one
// shape -- cost roughly this file's length in duplication and buy back
// exactly the property CLAUDE.md §8 keeps naming as worth paying for:
// a mutation to one call site can only ever be caught or missed by the test
// that reads that call site, never by a shared mechanism neither reviewer
// has separately proved against both shapes. #0490's guard is proved
// (mutated three ways, restored byte-identically, re-verified independently
// by its reviewer) and is not touched here at all -- not its assertions,
// not its helpers' bodies, nothing. The cost actually paid is duplication
// of this doc comment's shape and of the two ast.Inspect walks below;
// findFuncDecl, exprString, and paramIndexByName are shared code, not
// shared prose, and are reused rather than re-derived.
//
// # Why this is not a §8/#0337 method-set question (criterion 3, inherited)
//
// Exactly as #0490's own doc comment records for its sibling: this asks
// whether one literal call-argument identifier equals another assignment's
// left-hand identifier, a purely syntactic go/ast question with no
// interface, no method set, and no receiver form anywhere in it. go/types
// buys nothing over go/ast here for the same reason it buys nothing there.
//
// # What this guard does not catch (criterion 6)
//
// It does not catch newDevAdminAutoLogin returning nil -- unlike
// middleware.DevAutoLogin, that is the EXPECTED value on every production
// boot where an operator has not explicitly set DEV_ADMIN_LOGIN=true
// (newDevAdminAutoLogin's own doc comment, #0402), so "nil" is not itself a
// defect signal here the way #0490 could treat a refusal-panic as making a
// runtime nil impossible. This guard is agnostic to what devAdminAutoLogin
// holds at runtime; it only proves servePostgres hands mountAndServe
// whatever newDevAdminAutoLogin actually returned, under whatever name it
// was bound to, rather than a different identifier, a bare nil literal, or
// nothing. Nor does it catch an error arising from newDevAdminAutoLogin
// being mishandled -- that is #0402's and #0484's concern. And, inherited
// directly from #0490: it understands exactly one construction shape (a
// plain two-value `a, b := f(...)` binding, generalised from #0490's
// one-value form as described above) -- a refactor that inlined the
// newDevAdminAutoLogin call directly into mountAndServe's argument list
// would fail this guard even though the wiring stayed correct, the same
// deliberate fail-closed maintenance cost #0490 accepted.
//
// # Fail-closed shape (per #0275/#0300/#0326/#0490's own precedent)
//
// Both counting steps below (exactly one newDevAdminAutoLogin(...) call,
// exactly one mountAndServe(...) call in servePostgres) fail loudly on zero
// or on more than one, rather than silently skipping -- an empty scan is
// evidence the scan itself broke (a rename, a move), never evidence there
// is nothing to check.
func TestServePostgres_WiresDevAdminAutoLoginIntoMountAndServe(t *testing.T) {
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

	servePostgresDecl := findFuncDecl(mainFile, "servePostgres")
	if servePostgresDecl == nil {
		t.Fatal("servePostgres not found in main.go — has it been renamed or moved? " +
			"this guard's whole premise depends on that exact function existing")
	}

	// Step 1: find servePostgres's own construction of the dev-admin
	// auto-login middleware -- devAdminAutoLogin, err :=
	// newDevAdminAutoLogin(...) -- and record which identifier its FIRST
	// result (the middleware, not the error) is bound to. See the doc
	// comment above for why indexing by the call's position in Rhs, rather
	// than by counting Lhs names, is what makes this correct for a
	// two-value assignment without any change to the scan shape itself.
	var devAdminAutoLoginCalls []*ast.CallExpr
	devAdminAutoLoginVar := ""
	ast.Inspect(servePostgresDecl.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range assign.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "newDevAdminAutoLogin" {
				continue
			}
			devAdminAutoLoginCalls = append(devAdminAutoLoginCalls, call)
			if i < len(assign.Lhs) {
				if lhsIdent, ok := assign.Lhs[i].(*ast.Ident); ok {
					devAdminAutoLoginVar = lhsIdent.Name
				}
			}
		}
		return true
	})
	switch len(devAdminAutoLoginCalls) {
	case 0:
		t.Fatal("found zero newDevAdminAutoLogin(...) calls in servePostgres — either this scan " +
			"is broken (fail closed) or servePostgres no longer constructs the dev-admin auto-login " +
			"middleware at all, which this whole guard assumes")
	case 1:
		// expected
	default:
		t.Fatalf("found %d newDevAdminAutoLogin(...) calls in servePostgres, want exactly 1 — "+
			"this guard only understands a single construction bound to a single identifier",
			len(devAdminAutoLoginCalls))
	}
	if devAdminAutoLoginVar == "" {
		t.Fatal("newDevAdminAutoLogin's call was found but its first result wasn't assigned to a " +
			"plain identifier (e.g. `devAdminAutoLogin, err := newDevAdminAutoLogin(...)`) — this " +
			"scan only understands that shape")
	}

	// Step 2: find servePostgres's own call to mountAndServe and check the
	// argument at outerMiddleware's position is exactly that identifier —
	// not nil, not some other variable, not missing outright.
	var mountCalls []*ast.CallExpr
	ast.Inspect(servePostgresDecl.Body, func(n ast.Node) bool {
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
		t.Fatal("found zero mountAndServe(...) calls in servePostgres — either this scan is broken " +
			"(fail closed) or servePostgres no longer starts the server through mountAndServe at all")
	case 1:
		// expected
	default:
		t.Fatalf("found %d mountAndServe(...) calls in servePostgres, want exactly 1", len(mountCalls))
	}

	call := mountCalls[0]
	if outerIdx >= len(call.Args) {
		t.Fatalf("%s: servePostgres's mountAndServe call has only %d argument(s), want at least %d "+
			"(mountAndServe's outerMiddleware position) — the argument appears to have been dropped",
			fset.Position(call.Pos()), len(call.Args), outerIdx+1)
	}
	arg := call.Args[outerIdx]
	ident, ok := arg.(*ast.Ident)
	if !ok || ident.Name != devAdminAutoLoginVar {
		t.Fatalf("%s: servePostgres passes %s at mountAndServe's outerMiddleware position (index %d), "+
			"want the identifier %q bound by newDevAdminAutoLogin's own call — mountAndServe's "+
			"doc comment says a nil (or wrong) outerMiddleware here means \"no wrapper\" BY DESIGN, "+
			"so this is silent: the server starts, serves, and never logs a developer in (#0492)",
			fset.Position(arg.Pos()), exprString(arg), outerIdx, devAdminAutoLoginVar)
	}
}
