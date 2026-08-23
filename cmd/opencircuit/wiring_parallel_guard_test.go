package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// #0209: #0165 built registerWiringTest so that a *_wiring_test.go test
// calling truncateAdminWiringTables fails loudly instead of corrupting a
// sibling's fixtures, if this package ever gained t.Parallel(). Its phase-3
// review measured exactly how well that works, on both sides:
//
//   - the four registered callers (admin_wiring_test.go, events_wiring_test.go,
//     seo_wiring_test.go, shutdown_budget_wiring_test.go) fail 5/5 with zero
//     corruption when t.Parallel() is added to two of them;
//   - a FIFTH test that calls truncateAdminWiringTables under t.Parallel()
//     WITHOUT calling registerWiringTest is not protected at all: the review
//     reproduced the original corruption -- spurious 401s in 2 of 3 runs, and
//     "ERROR: deadlock detected (SQLSTATE 40P01)" in the third.
//
// registerWiringTest protects tests that opt in. It cannot protect the
// package against a test that skips it, because it only runs when a test
// calls it. This guard closes that gap the way #0181's citation guard
// closes its own: not by trusting every future test to follow a pattern
// documented in a doc comment, but by reading the source structurally and
// failing the build if the forbidden pattern appears anywhere in the
// package, registered or not.
//
// Why t.Parallel() is forbidden in this package at all, for whoever reaches
// this failure without having read the above: every *_wiring_test.go test
// that calls truncateAdminWiringTables shares the SAME tables (sessions,
// passkey_credentials, audit_log, users, and for the shutdown-budget test
// also subscriber_interests/subscribers) and truncates them wholesale, on
// entry and again in t.Cleanup. That TRUNCATE has no isolation: it deletes
// every row, not just the rows the calling test seeded. Two such tests
// running concurrently means one test's TRUNCATE can fire while the other
// is mid-request, wiping out its seeded user or session and turning a
// clean assertion into a foreign-key violation or a spurious 401 that
// points nowhere near the actual cause. See truncateAdminWiringTables's own
// doc comment (admin_wiring_test.go) for the mechanism, and
// registerWiringTest (also admin_wiring_test.go) for the runtime guard that
// catches this for tests that call it.
//
// Deliberately AST-based, not a text grep over the file: go/ast's
// ast.Inspect walks *ast.CallExpr nodes, so a comment mentioning
// "t.Parallel()" (this doc comment, deliberately, is one) or a string
// literal containing that text is structurally never a CallExpr and cannot
// trip this guard. See TestParallelGuardIgnoresCommentsAndStrings and
// TestParallelGuardCatchesRealCall below for direct, synthetic-source proof
// of both properties, and issues/0209.md's ## Verification for the mutation
// proof against the real tree (added t.Parallel() to a real wiring test,
// confirmed this guard failed naming the file and line, restored the file
// and verified byte-identity with shasum -a 256).
//
// Match rule: any call of the form <ident>.Parallel() with zero arguments,
// regardless of the receiver's name -- not just literal "t.Parallel()" --
// so a test that aliases its *testing.T (e.g. `tt := t`) is still caught.
// This does not attempt to verify the receiver's static type is *testing.T
// or *testing.B (that would need a go/types check, not just go/parser);
// cmd/opencircuit imports "testing" for exactly that purpose and no other
// type in this package exposes a zero-argument Parallel() method (grep -rn
// "\.Parallel(" cmd/opencircuit confirmed zero hits before this guard), so
// the untyped match costs nothing today and only over-fires if some future
// unrelated type in this package is deliberately given a same-named method
// -- a call any reviewer would notice immediately from this guard's
// message naming a file that is not a wiring test.
type parallelViolation struct {
	pos  token.Position
	text string
}

// #0209: mirrors citation_guard_test.go's scanDirForCitations shape --
// WalkDir over the package directory, parse every .go file, and let the
// caller decide what "collected" means. cmd/opencircuit is flat today (no
// subdirectories), but recursing costs nothing and matches the model this
// issue names.
func scanDirForParallelCalls(t *testing.T, root string) []parallelViolation {
	t.Helper()

	var violations []parallelViolation
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}

		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		violations = append(violations, scanFileForParallelCalls(fset, file)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return violations
}

// scanFileForParallelCalls walks only *ast.CallExpr nodes shaped like
// <ident>.Parallel() with no arguments. Comments and string literals are
// different node kinds entirely and are never visited here.
func scanFileForParallelCalls(fset *token.FileSet, file *ast.File) []parallelViolation {
	var violations []parallelViolation
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Parallel" {
			return true
		}
		recv := "<expr>"
		if ident, ok := sel.X.(*ast.Ident); ok {
			recv = ident.Name
		}
		violations = append(violations, parallelViolation{
			pos:  fset.Position(call.Pos()),
			text: recv + ".Parallel()",
		})
		return true
	})
	return violations
}

// TestNoWiringTestUsesParallel is the actual guard: it fails if any file
// under cmd/opencircuit contains a real t.Parallel() call.
func TestNoWiringTestUsesParallel(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	baseDir := filepath.Dir(thisFile)

	violations := scanDirForParallelCalls(t, baseDir)
	if len(violations) == 0 {
		return
	}
	sort.Slice(violations, func(i, j int) bool {
		return violations[i].pos.String() < violations[j].pos.String()
	})

	var b strings.Builder
	b.WriteString("cmd/opencircuit must not call t.Parallel(): every *_wiring_test.go " +
		"test that calls truncateAdminWiringTables (admin_wiring_test.go) shares " +
		"the same sessions/passkey_credentials/audit_log/users tables and TRUNCATEs " +
		"them wholesale on entry and in t.Cleanup, with no per-test isolation. Running " +
		"two such tests concurrently lets one test's TRUNCATE wipe a sibling's rows " +
		"mid-test, producing a foreign-key violation or a spurious 401 that points " +
		"nowhere near the real cause (#0165's phase-3 review reproduced both, 13/13). " +
		"registerWiringTest(t) (admin_wiring_test.go) catches this at runtime for " +
		"tests that call it, but a test that calls truncateAdminWiringTables under " +
		"t.Parallel() WITHOUT calling registerWiringTest(t) is not protected -- " +
		"reproduced empirically in #0209 (spurious 401s in 2 of 3 runs, ERROR: " +
		"deadlock detected (SQLSTATE 40P01) in the third). Remove t.Parallel() " +
		"from:\n")
	for _, v := range violations {
		b.WriteString("  " + v.pos.String() + ": " + v.text + "\n")
	}
	t.Error(b.String())
}

// TestParallelGuardIgnoresCommentsAndStrings proves the two exclusions this
// guard must respect, against a synthetic source file: a comment
// mentioning "t.Parallel()" and a string literal containing that exact
// text must not trip it, since neither is a CallExpr.
func TestParallelGuardIgnoresCommentsAndStrings(t *testing.T) {
	const src = `package fixture

// This comment says t.Parallel() is forbidden -- fine, it is not a call.
func Message() string {
	return "do not call t.Parallel() in this package"
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if got := scanFileForParallelCalls(fset, file); len(got) != 0 {
		t.Fatalf("expected zero violations on the comment/string fixture, got %d: %+v", len(got), got)
	}
}

// TestParallelGuardCatchesRealCall proves the guard actually fires: a real
// t.Parallel() call must be reported, and TestParallelGuardCatchesAliasedReceiver
// proves the match is not limited to a receiver literally named "t".
func TestParallelGuardCatchesRealCall(t *testing.T) {
	const src = `package fixture

import "testing"

func TestSomething(t *testing.T) {
	t.Parallel()
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	got := scanFileForParallelCalls(fset, file)
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %d: %+v", len(got), got)
	}
	if got[0].text != "t.Parallel()" {
		t.Fatalf("violation text = %q, want %q", got[0].text, "t.Parallel()")
	}
}

func TestParallelGuardCatchesAliasedReceiver(t *testing.T) {
	const src = `package fixture

import "testing"

func TestSomething(t *testing.T) {
	tt := t
	tt.Parallel()
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	got := scanFileForParallelCalls(fset, file)
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %d: %+v", len(got), got)
	}
	if got[0].text != "tt.Parallel()" {
		t.Fatalf("violation text = %q, want %q", got[0].text, "tt.Parallel()")
	}
}
