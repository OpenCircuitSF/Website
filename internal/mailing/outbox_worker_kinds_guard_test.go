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

// kindExceptions is every outbox.Kind constant TestMailKindsCoversEveryOutboxKind
// (below) accepts as deliberately NOT claimed by mailKinds, keyed by name,
// each entry carrying a written reason as its value — a set, not a single
// `const` compared with `==` (#0296 item 4). Before this fix the exception
// was one bare `const intakeException = "KindSubscribeIntake"`: adding a
// second deliberately-not-mail Kind meant restructuring this into a set
// first, and nothing forced the restructuring to also require a reason —
// an allowlist anyone can append to without argument is a guard with a
// hole (CLAUDE.md's #0280 lesson, cited by this issue for the same
// reason). A map keyed by name makes "is this exempt" an O(1) lookup
// exactly like the old `==` did, while the string value makes a bare
// append (a name with no reason) visibly incomplete at review time.
var kindExceptions = map[string]string{
	"KindSubscribeIntake": "not an email — claimed by internal/handlers.SubscribeHandler's own recovery poller instead of this worker (subscribe_intake.go), not this worker's render switch; see that Kind's own doc comment in internal/outbox/store.go",
}

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
// every declared Kind is EITHER in mailKinds OR named in kindExceptions,
// above (the one entry there today is outbox.KindSubscribeIntake — the one
// Kind that is deliberately not an email, claimed by a different poller
// entirely, see that constant's own doc comment). A Kind satisfying neither
// is the exact silent-stall gap described above.
//
// Mutation proof: add a new `KindWhatever Kind = "whatever"` constant to
// internal/outbox/store.go without adding outbox.KindWhatever to mailKinds
// (or to kindExceptions above) and this test fails, naming it.
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

	var uncovered []string
	for kind := range declaredKinds {
		if _, exempt := kindExceptions[kind]; exempt || claimedByMailKinds[kind] {
			continue
		}
		uncovered = append(uncovered, kind)
	}
	sort.Strings(uncovered)
	if len(uncovered) > 0 {
		t.Fatalf("outbox.Kind constant(s) claimed by NOBODY — neither mailKinds nor kindExceptions: %v (a row of this kind would stall in 'queued' forever with no error; add it to mailKinds, or to kindExceptions with a written reason if it is deliberately not mail)", uncovered)
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
// ordinary assignability rules do the rest). #0296 found a fifth style
// (item 1, below) and an incompleteness in the third (item 3, below).
//
// The five declaration styles this project's Kind constants can appear in,
// and how each is resolved here:
//
//  1. Explicit type annotation — `KindConfirmation Kind = "confirmation"`.
//     Resolved via a pre-pass, collectKindTypeNames: vs.Type is matched
//     against the SET of every type name in the package that resolves to
//     Kind — "Kind" itself, plus any `type X = Kind` alias or `type X
//     Kind` defined type — rather than the single literal spelling "Kind".
//  2. A conversion in the value expression — `KindX = Kind("x")`. vs.Type
//     is nil (there is no annotation), but the value is a call converting
//     a string to Kind. Resolved by isKindConversion, checked against the
//     same collectKindTypeNames set as style 1 (so `KindX =
//     KindAlias("x")` resolves too).
//  3. An untyped member that inherits the block's type via Go's own
//     const-repetition rule: "Within a parenthesized const declaration
//     list the expression list may be omitted from any but the first
//     ConstSpec. Such an empty list is equivalent to the textual
//     substitution of the first preceding non-empty expression list and
//     its type if any" (Go spec, "Constant declarations") — i.e.
//     `KindConfirmation Kind = "confirmation"` followed by a bare `KindY`
//     with no `=` at all. Resolved by propagating the nearest preceding
//     non-empty ConstSpec's resolution forward — BOTH of its fields
//     (#0296 item 2: a pre-#0296 version propagated only isKind, so a
//     repetition of a bare style-4 sibling — `KindA Kind = "a"; KindB =
//     "b"; KindC` — inherited isKind=false from KindB and was never
//     reached by the same-block backfill below either, since that backfill
//     only ever looked at isBare, which was never set for KindC).
//  4. An untyped constant with its OWN value but NO type annotation and no
//     conversion — `KindReviewProbe = "review_probe"`, #0282's own
//     reported example. This is NOT style 3: the expression list is not
//     omitted, so Go's textual-substitution rule never applies, and Go's
//     "Constant declarations" section is explicit that such a constant
//     "remain[s] untyped". That is not a hand-wave: checked empirically
//     with go/types against a fixture identical in shape to this one,
//     the resulting *types.Const's Type() is `untyped string`, not Kind —
//     indistinguishable, by type alone, from any unrelated untyped string
//     constant that happened to be declared nearby (#0296 criterion 6: this
//     is DELIBERATE and preserved — see
//     TestCollectKindConstants_SameBlockFalsePositiveIsDeliberate,
//     outbox_kind_detection_test.go). No type-level analysis (go/types
//     included — see this function's "go/types" note below) can resolve
//     style 4 by typing alone. The only available signal is CONTEXT: it
//     sits in the same `const (...)` block as a constant resolved by
//     styles 1-3, and mailKinds going stale is exactly the failure this
//     guard exists to catch, so a bare, untyped, same-block constant is
//     treated as a Kind. A false positive here just means adding one more
//     name to mailKinds or kindExceptions — cheap and safe. A false
//     negative is the silent 'queued'-forever stall #0254 introduced the
//     possibility of.
//  5. An explicit annotation naming a type ALIAS or DEFINED TYPE of Kind,
//     not the literal spelling — `type KindAlias = Kind; const KindE
//     KindAlias = "e"` (#0296 item 1, #0282's review). Also resolved by
//     the collectKindTypeNames pre-pass under style 1 above: this is not a
//     separate code path, just a name style 1's literal-spelling match
//     alone could not see. Unlike style 4, this one IS a real go/types
//     typing fact — #0282's review measured go/types resolving KindE's
//     Type() to fixture.Kind directly, a case where go/types genuinely
//     outperforms a spelling-based AST match (see the "go/types" note,
//     corrected accordingly by #0296 item 5).
//
// # Why go/ast rather than go/types (#0282 criterion 4, corrected by #0296 item 5)
//
// go/types was considered and rejected, but not for #0261's reason
// (synthetic fixtures importing nothing and therefore failing to
// type-check) — that reasoning does not transfer here, since this guard's
// subject is internal/outbox's real, already-buildable source, not a
// synthetic fixture. #0282's work log originally claimed go/types "buys
// zero marginal precision" here; #0296 found that claim strictly false —
// style 5 above (an alias annotation) IS a case where go/types resolves
// the constant's real type correctly while a naive spelling-based AST
// match does not. The corrected claim: go/types would buy real marginal
// precision for style 5 specifically — precision this file now gets
// WITHOUT go/types, via the collectKindTypeNames pre-pass, which resolves
// an alias or defined type by NAME (a self-contained AST fact: what does
// this `type` declaration say its underlying type is) rather than by full
// type-checking. For style 4, go/types still buys nothing: its Type() is
// `untyped string`, never Kind, so the same same-block heuristic this
// function already needs for style 4 is still required even with go/types
// in place. Net of both corrections, go/types buys NEGLIGIBLE marginal
// precision over the combination of an AST type-name pre-pass plus the
// same-block heuristic — not zero, but not enough to justify go/types'
// real cost here: it needs a working importer to resolve internal/outbox's
// full import graph, a heavier and more fragile dependency than a
// self-contained parse, with its own distinct failure mode (import
// resolution failing, as opposed to a parse failing). The go/ast walk
// below implements Go's own textual-substitution rule (style 3), a
// type-alias/defined-type pre-pass (styles 1 and 5), and conversion
// detection (style 2) directly rather than approximating them, so it is
// exact for 1, 2, 3, and 5, and heuristic only for style 4, where go/types
// would be too.
func collectKindConstants(files []*ast.File) map[string]bool {
	kindTypeNames := collectKindTypeNames(files)
	declared := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			collectKindConstantsInBlock(gd, kindTypeNames, declared)
		}
	}
	return declared
}

// collectKindTypeNames returns every type name declared in files that
// resolves to outbox.Kind — "Kind" itself, plus any `type X = Kind` alias
// or `type X Kind` defined type, transitively (an alias of an alias also
// resolves, though nothing in this project's real source chains that
// deep today). #0296 item 1/style 5's pre-pass: collectKindConstantsInBlock
// matches a ConstSpec's vs.Type against this SET rather than the single
// literal spelling "Kind", so `type KindAlias = Kind; const KindE
// KindAlias = "e"` resolves the same as the literal spelling would.
//
// Both `type X = Kind` (a TRUE alias — X and Kind are the IDENTICAL type)
// and `type X Kind` (a DEFINED type — X's underlying type is Kind's
// underlying type, string, but X and Kind are NOT identical types, and a
// constant of type X needs an explicit conversion before it is usable
// where a Kind is expected) are collected here and treated the same way.
// That is deliberately more generous than Go's own type-identity rules:
// this guard's design already accepts a false positive (one more name
// added to mailKinds or kindExceptions, cheap) over a false negative (a
// silent 'queued'-forever stall, #0254) for style 4 above, and the same
// trade-off applies to a defined type of Kind, which is at minimum a
// strong signal of intent even where it is not a strict typing fact.
func collectKindTypeNames(files []*ast.File) map[string]bool {
	names := map[string]bool{"Kind": true}
	type aliasCandidate struct {
		name string
		base string
	}
	var candidates []aliasCandidate
	for _, file := range files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				id, ok := ts.Type.(*ast.Ident)
				if !ok {
					continue
				}
				candidates = append(candidates, aliasCandidate{name: ts.Name.Name, base: id.Name})
			}
		}
	}
	// Fixed point rather than one pass, so a chain (an alias of an alias)
	// resolves regardless of declaration order.
	for changed := true; changed; {
		changed = false
		for _, c := range candidates {
			if names[c.base] && !names[c.name] {
				names[c.name] = true
				changed = true
			}
		}
	}
	return names
}

// kindSpecInfo is one ConstSpec's resolution within a single const block.
type kindSpecInfo struct {
	names  []string
	isKind bool // resolved via styles 1, 2, 3, or 5 (an actual Go typing fact)
	isBare bool // style 4 candidate — untyped, own value, no annotation/conversion
}

// collectKindConstantsInBlock resolves every ConstSpec in one `const (...)`
// GenDecl and adds every name that resolves to Kind (styles 1, 2, 3, 5, or
// style 4 when the block declares at least one style-1/2/3/5 Kind — see
// collectKindConstants' doc comment) to declared.
func collectKindConstantsInBlock(gd *ast.GenDecl, kindTypeNames map[string]bool, declared map[string]bool) {
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
		case isKindTypeIdent(vs.Type, kindTypeNames):
			// Styles 1 and 5.
			info.isKind = true
		case vs.Type == nil && !nonEmpty:
			// Style 3: Go's own repetition rule — inherit the nearest
			// preceding non-empty ConstSpec's resolution, BOTH fields
			// (#0296 item 2 — isBare must propagate here too, not just
			// isKind, or a repetition of a bare style-4 sibling is missed).
			if lastNonEmpty != nil {
				info.isKind = lastNonEmpty.isKind
				info.isBare = lastNonEmpty.isBare
			}
		case vs.Type == nil && isKindConversion(vs.Values, kindTypeNames):
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

// isKindTypeIdent reports whether expr is a bare identifier naming a type
// in kindTypeNames (collectKindTypeNames) — styles 1 and 5.
func isKindTypeIdent(expr ast.Expr, kindTypeNames map[string]bool) bool {
	id, ok := expr.(*ast.Ident)
	return ok && kindTypeNames[id.Name]
}

// isKindConversion reports whether values is exactly one expression shaped
// like `<T>(<arg>)`, where T is a name in kindTypeNames — style 2 (and its
// style-5 counterpart, a conversion through an alias or defined type of
// Kind rather than the literal spelling).
func isKindConversion(values []ast.Expr, kindTypeNames map[string]bool) bool {
	if len(values) != 1 {
		return false
	}
	call, ok := values[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	return isKindTypeIdent(call.Fun, kindTypeNames)
}
