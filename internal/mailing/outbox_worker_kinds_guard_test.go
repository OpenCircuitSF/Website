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
// This parses internal/outbox's source for every constant that resolves to
// type Kind (collectKindConstants, below — see #0282 for why "resolves to
// type Kind" is not simply "carries an explicit `Kind` type annotation"),
// and this package's source for mailKinds' own element list, then asserts
// every declared Kind is EITHER in mailKinds OR is outbox.KindSubscribeIntake
// (the one Kind that is deliberately not an email — claimed by a different
// poller entirely, see that constant's own doc comment). A Kind satisfying
// neither is the exact silent-stall gap described above.
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

	declaredKinds := collectKindConstants(filesOf(outboxPkgs))
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

// filesOf flattens parser.ParseDir's map[filename]*ast.Package result into
// the []*ast.File collectKindConstants (and its synthetic-fixture tests in
// outbox_kind_detection_test.go) both operate on.
func filesOf(pkgs map[string]*ast.Package) []*ast.File {
	var files []*ast.File
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			files = append(files, file)
		}
	}
	return files
}

// collectKindConstants is #0282's fix for the gap #0254's reviewer found in
// the ORIGINAL version of this test: matching only `vs.Type.(*ast.Ident)`
// sees a Kind constant carrying an explicit type annotation
// (`KindX Kind = "x"`) and nothing else, so any of Go's other three ways to
// declare one — see below — was invisible to it while being fully usable
// as a Kind at every call site (outbox.Kind is just a string type, so Go's
// ordinary assignability rules do the rest).
//
// The four declaration styles this project's Kind constants can appear in,
// and how each is resolved here:
//
//  1. Explicit type annotation — `KindConfirmation Kind = "confirmation"`.
//     Resolved directly: isIdent(vs.Type, "Kind").
//  2. A conversion in the value expression — `KindX = Kind("x")`. vs.Type
//     is nil (there is no annotation), but the value is a call converting
//     a string to Kind. Resolved by isKindConversion.
//  3. An untyped member that inherits the block's type via Go's own
//     const-repetition rule: "Within a parenthesized const declaration
//     list the expression list may be omitted from any but the first
//     ConstSpec. Such an empty list is equivalent to the textual
//     substitution of the first preceding non-empty expression list and
//     its type if any" (Go spec, "Constant declarations") — i.e.
//     `KindConfirmation Kind = "confirmation"` followed by a bare `KindY`
//     with no `=` at all. Resolved by propagating the nearest preceding
//     non-empty ConstSpec's resolution forward.
//  4. An untyped constant with its OWN value but NO type annotation and no
//     conversion — `KindReviewProbe = "review_probe"`, #0282's own
//     reported example. This is NOT style 3: the expression list is not
//     omitted, so Go's textual-substitution rule never applies, and Go's
//     "Constant declarations" section is explicit that such a constant
//     "remain[s] untyped". That is not a hand-wave: checked empirically
//     with go/types against a fixture identical in shape to this one,
//     the resulting *types.Const's Type() is `untyped string`, not Kind —
//     indistinguishable, by type alone, from any unrelated untyped string
//     constant that happened to be declared nearby. No type-level analysis
//     (go/types included — see this function's "go/types" note below) can
//     resolve style 4 by typing alone. The only available signal is
//     CONTEXT: it sits in the same `const (...)` block as a constant
//     resolved by styles 1-3, and mailKinds going stale is exactly the
//     failure this guard exists to catch, so a bare, untyped, same-block
//     constant is treated as a Kind. A false positive here just means
//     adding one more name to mailKinds or its exceptions — cheap and
//     safe. A false negative is the silent 'queued'-forever stall #0254
//     introduced the possibility of.
//
// # Why go/ast rather than go/types (#0282 criterion 4)
//
// go/types was considered and rejected, but not for #0261's reason
// (synthetic fixtures importing nothing and therefore failing to
// type-check) — that reasoning does not transfer here, since this guard's
// subject is internal/outbox's real, already-buildable source, not a
// synthetic fixture. The actual reason is narrower and was established
// empirically, not assumed: go/types resolves styles 1, 2, and 3 exactly
// as precisely as the go/ast walk below (all three ARE real typing facts),
// but it resolves style 4 no better — its Type() is `untyped string`,
// never Kind, so the same same-block heuristic this function already needs
// for style 4 would still be required even with go/types in place. Given
// go/types buys no additional precision here, and does add a real cost —
// it needs a working importer to resolve internal/outbox's full import
// graph, a heavier and more fragile dependency than a self-contained parse,
// with its own distinct failure mode (import resolution failing, as
// opposed to a parse failing) — it was not worth adding for zero marginal
// coverage. The go/ast walk below implements Go's own textual-substitution
// rule (style 3) and conversion detection (style 2) directly rather than
// approximating them, so it is exact for 1-3 and heuristic only where
// go/types would be too.
func collectKindConstants(files []*ast.File) map[string]bool {
	declared := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			collectKindConstantsInBlock(gd, declared)
		}
	}
	return declared
}

// kindSpecInfo is one ConstSpec's resolution within a single const block.
type kindSpecInfo struct {
	names  []string
	isKind bool // resolved via styles 1-3 (an actual Go typing fact)
	isBare bool // style 4 candidate — untyped, own value, no annotation/conversion
}

// collectKindConstantsInBlock resolves every ConstSpec in one `const (...)`
// GenDecl and adds every name that resolves to Kind (styles 1-3, or style 4
// when the block declares at least one style-1/2/3 Kind — see
// collectKindConstants' doc comment) to declared.
func collectKindConstantsInBlock(gd *ast.GenDecl, declared map[string]bool) {
	var infos []kindSpecInfo
	var lastNonEmpty *kindSpecInfo
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		info := kindSpecInfo{}
		for _, n := range vs.Names {
			info.names = append(info.names, n.Name)
		}
		nonEmpty := len(vs.Values) > 0
		switch {
		case isIdent(vs.Type, "Kind"):
			// Style 1.
			info.isKind = true
		case vs.Type == nil && !nonEmpty:
			// Style 3: Go's own repetition rule — inherit the nearest
			// preceding non-empty ConstSpec's resolution.
			if lastNonEmpty != nil {
				info.isKind = lastNonEmpty.isKind
			}
		case vs.Type == nil && isKindConversion(vs.Values):
			// Style 2.
			info.isKind = true
		case vs.Type == nil && nonEmpty:
			// Style 4 candidate — resolved after the loop, once it is
			// known whether this block declares a Kind at all.
			info.isBare = true
		}
		infos = append(infos, info)
		if nonEmpty {
			last := info
			lastNonEmpty = &last
		}
	}

	isKindBlock := false
	for _, info := range infos {
		if info.isKind {
			isKindBlock = true
			break
		}
	}
	for _, info := range infos {
		if info.isKind || (isKindBlock && info.isBare) {
			for _, name := range info.names {
				declared[name] = true
			}
		}
	}
}

func isIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

// isKindConversion reports whether values is exactly one expression shaped
// like `Kind(<arg>)` — style 2.
func isKindConversion(values []ast.Expr) bool {
	if len(values) != 1 {
		return false
	}
	call, ok := values[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	return isIdent(call.Fun, "Kind")
}
