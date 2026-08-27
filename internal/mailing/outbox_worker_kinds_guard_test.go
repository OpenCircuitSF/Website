package mailing

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"
)

// TestMailKindsCoversEveryOutboxKind is #0254's review-bounce regression
// coverage for the "mailKinds completeness hole" the reviewer identified
// (issues/0254.md ## Review notes, "What the next pass must change" item
// 3): mailKinds (outbox_worker.go) is a hand-maintained list, and there is
// no compiler check that it stays in sync with internal/outbox's actual
// Kind constants. Before #0254, an unrecognised Kind reaching render's
// switch failed LOUDLY (its default case). After #0254 added ClaimDue's
// kinds filter, a Kind that exists in internal/outbox but is missing from
// this list is claimed by NOBODY — not this worker (ClaimDue never returns
// it), not internal/handlers.SubscribeHandler's recovery poller (which only
// claims outbox.KindSubscribeIntake) — so a row of that kind stalls in
// 'queued' forever, silently, with no error anywhere to notice. See
// mailKinds' own doc comment for the corrected reasoning.
//
// This parses internal/outbox's source for every constant explicitly typed
// Kind, and this package's source for mailKinds' own element list, then
// asserts every declared Kind is EITHER in mailKinds OR is
// outbox.KindSubscribeIntake (the one Kind that is deliberately not an
// email — claimed by a different poller entirely, see that constant's own
// doc comment). A Kind satisfying neither is the exact silent-stall gap
// described above.
//
// Mutation proof: add a new `KindWhatever Kind = "whatever"` constant to
// internal/outbox/store.go without adding outbox.KindWhatever to mailKinds
// (or to the exceptions list below) and this test fails, naming it.
func TestMailKindsCoversEveryOutboxKind(t *testing.T) {
	fset := token.NewFileSet()

	// internal/outbox's own Kind constants — the source of truth. Test
	// files are excluded: a Kind declared only for a test fixture (none
	// exist today) would not be a real production kind any poller needs to
	// claim.
	outboxPkgs, err := parser.ParseDir(fset, "../outbox", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing internal/outbox source: %v", err)
	}

	declaredKinds := map[string]bool{}
	for _, pkg := range outboxPkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					ident, ok := vs.Type.(*ast.Ident)
					if !ok || ident.Name != "Kind" {
						continue
					}
					for _, name := range vs.Names {
						declaredKinds[name.Name] = true
					}
				}
			}
		}
	}
	if len(declaredKinds) == 0 {
		t.Fatal("found zero Kind constants in internal/outbox — the parse likely broke, not a real empty set")
	}

	// This package's own mailKinds var literal.
	mailingPkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing internal/mailing source: %v", err)
	}

	claimedByMailKinds := map[string]bool{}
	var foundMailKindsDecl bool
	for _, pkg := range mailingPkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "mailKinds" {
						continue
					}
					foundMailKindsDecl = true
					if len(vs.Values) != 1 {
						continue
					}
					lit, ok := vs.Values[0].(*ast.CompositeLit)
					if !ok {
						continue
					}
					for _, elt := range lit.Elts {
						sel, ok := elt.(*ast.SelectorExpr)
						if !ok {
							continue
						}
						if pkgIdent, ok := sel.X.(*ast.Ident); ok && pkgIdent.Name == "outbox" {
							claimedByMailKinds[sel.Sel.Name] = true
						}
					}
				}
			}
		}
	}
	if !foundMailKindsDecl {
		t.Fatal("could not find a mailKinds var declaration in package source")
	}
	if len(claimedByMailKinds) == 0 {
		t.Fatal("mailKinds parsed to zero elements — the parse likely broke, not a real empty list")
	}

	// #0254: outbox.KindSubscribeIntake is the one documented exception —
	// not an email, claimed by internal/handlers.SubscribeHandler's own
	// recovery poller instead of this worker. Any OTHER Kind not in
	// mailKinds is the silent-stall gap this test exists to catch.
	const intakeException = "KindSubscribeIntake"

	var uncovered []string
	for kind := range declaredKinds {
		if kind == intakeException || claimedByMailKinds[kind] {
			continue
		}
		uncovered = append(uncovered, kind)
	}
	sort.Strings(uncovered)
	if len(uncovered) > 0 {
		t.Fatalf("outbox.Kind constant(s) claimed by NOBODY — neither mailKinds nor the KindSubscribeIntake exception: %v (a row of this kind would stall in 'queued' forever with no error; add it to mailKinds, or to this test's exceptions if it is deliberately not mail)", uncovered)
	}

	// The reverse direction too: mailKinds must not name something that
	// no longer exists in internal/outbox (a rename left stale, or a typo
	// that would otherwise compile since outbox.Kind is just a string
	// type) — go vet/the compiler already catch a genuinely undefined
	// identifier, so this is redundant with compilation, but cheap and
	// keeps the two directions symmetric.
	for kind := range claimedByMailKinds {
		if !declaredKinds[kind] {
			t.Errorf("mailKinds claims outbox.%s, which is not declared as a Kind constant in internal/outbox", kind)
		}
	}
}
