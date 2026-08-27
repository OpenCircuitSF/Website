package mailing

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// outbox_kind_detection_test.go is #0282's regression coverage for
// collectKindConstants (outbox_worker_kinds_guard_test.go): synthetic,
// self-contained fixtures (import nothing, so no importer/build-graph
// dependency — see collectKindConstants' "go/ast rather than go/types"
// note for why that distinction matters here) exercising each of the four
// declaration styles that function's doc comment enumerates, independently
// of internal/outbox's real source.
//
// # Reproducing the gap before closing it (#0282 criterion 2)
//
// Before this fix, collectKindConstantsInBlock's switch had only one case
// — `isIdent(vs.Type, "Kind")` — mirroring the pre-#0282
// TestMailKindsCoversEveryOutboxKind's `vs.Type.(*ast.Ident)` match
// exactly. Run against allStylesFixture below with only that case present
// (every other case, and the isBare/same-block backfill, deleted), this is
// the verbatim output — captured before the fix was applied, not
// reconstructed afterward:
//
//	=== RUN   TestCollectKindConstants_AllDeclarationStyles
//	    outbox_kind_detection_test.go:107: collectKindConstants missed
//	        [KindConversion KindRepeated KindUntypedSibling]; found only
//	        [KindAnnotated]
//	--- FAIL: TestCollectKindConstants_AllDeclarationStyles (0.00s)
//
// i.e. exactly styles 2, 3, and 4 — every style except the one explicit
// annotation — were invisible, reproducing #0254's reviewer's finding that
// the guard passed with an uncovered kind present. Restoring the other
// switch cases (collectKindConstantsInBlock, as shipped in
// outbox_worker_kinds_guard_test.go) is what turns this green — confirmed
// by re-running immediately after restoring them. See this issue's Work
// log for the full transcript of both runs.
const allStylesFixture = `package fixture

type Kind string

const (
	KindAnnotated  Kind = "annotated"  // style 1: explicit annotation
	KindConversion      = Kind("conv") // style 2: conversion, no annotation
	KindRepeated                       // style 3: Go's own const-repetition rule — inherits KindConversion's type+value
	KindUntypedSibling  = "sibling"    // style 4: untyped, own value, no annotation — same block as a resolved Kind above

	OtherTyped SomeOtherType = "not-a-kind" // must NOT be picked up
)

type SomeOtherType string
`

// unrelatedBlockFixture: an untyped, own-value constant that shares NO
// const block with anything resolving to Kind — style 4's same-block
// heuristic must not sweep it in, since nothing here identifies it as a
// Kind at all.
const unrelatedBlockFixture = `package fixture

type Kind string

const (
	KindAnnotated Kind = "annotated"
)

const (
	UnrelatedUntyped = "not-a-kind-either"
)
`

func mustParseFixture(t *testing.T, src string) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("parsing fixture source: %v", err)
	}
	return f
}

func TestCollectKindConstants_AllDeclarationStyles(t *testing.T) {
	file := mustParseFixture(t, allStylesFixture)
	got := collectKindConstants([]*ast.File{file})

	want := map[string]bool{
		"KindAnnotated":      true, // style 1
		"KindConversion":     true, // style 2
		"KindRepeated":       true, // style 3
		"KindUntypedSibling": true, // style 4
	}

	var missed []string
	for name := range want {
		if !got[name] {
			missed = append(missed, name)
		}
	}
	sort.Strings(missed)
	if len(missed) > 0 {
		var found []string
		for name := range got {
			found = append(found, name)
		}
		sort.Strings(found)
		t.Fatalf("collectKindConstants missed %v; found only %v", missed, found)
	}

	if got["OtherTyped"] {
		t.Errorf("collectKindConstants wrongly claimed OtherTyped (a different, explicitly-typed constant) as a Kind")
	}
	if len(got) != len(want) {
		t.Errorf("collectKindConstants returned %d names, want exactly %d (%v) — got %v", len(got), len(want), want, got)
	}
}

func TestCollectKindConstants_DoesNotLeakAcrossUnrelatedBlocks(t *testing.T) {
	file := mustParseFixture(t, unrelatedBlockFixture)
	got := collectKindConstants([]*ast.File{file})

	if !got["KindAnnotated"] {
		t.Fatalf("collectKindConstants missed KindAnnotated (style 1, unambiguous) — got %v", got)
	}
	if got["UnrelatedUntyped"] {
		t.Errorf("collectKindConstants wrongly swept UnrelatedUntyped (%v) into the Kind set — it shares no const block with any resolved Kind", got)
	}
}

func TestCollectKindConstants_EmptySourceYieldsEmptySet(t *testing.T) {
	file := mustParseFixture(t, "package fixture\n")
	got := collectKindConstants([]*ast.File{file})
	if len(got) != 0 {
		t.Fatalf("collectKindConstants on a file with no const declarations returned %v, want empty — TestMailKindsCoversEveryOutboxKind relies on an empty result here to trip its own \"parse likely broke\" fatal check", got)
	}
}

// aliasKindFixture is #0296 item 1's reproduction: a fifth declaration
// style #0282's review found, not covered by any of the original four.
// `type KindAlias = Kind` is a TRUE Go alias — KindAlias and Kind are the
// IDENTICAL type, not merely convertible — so `const KindE KindAlias =
// "e"` really does have type Kind, a fact go/types confirms directly
// (#0282's review measured Type() == fixture.Kind for this exact shape).
// Before #0296's fix, collectKindConstantsInBlock's style-1 case matched
// only the literal spelling isIdent(vs.Type, "Kind"), so a non-nil
// vs.Type naming "KindAlias" never reached that branch (nor the
// bare/style-4 branch, since it has an explicit annotation) —
// detected=false for a constant every real Kind call site accepts.
//
// Reproduced against the pre-#0296 detector (collapsing the style-1 case
// back to the literal-spelling match, in a scratch copy, exactly as
// #0282's own methodology did for its four styles):
//
//	=== RUN   TestCollectKindConstants_AliasAnnotatedKind
//	    outbox_kind_detection_test.go:192: collectKindConstants missed
//	        [KindE]; found only [KindAnnotated]
//	--- FAIL: TestCollectKindConstants_AliasAnnotatedKind (0.00s)
//
// Restoring the type-name pre-pass (collectKindTypeNames,
// outbox_worker_kinds_guard_test.go) turns this green.
const aliasKindFixture = `package fixture

type Kind string
type KindAlias = Kind

const (
	KindAnnotated Kind      = "annotated"
	KindE         KindAlias = "e" // #0296 item 1: alias-annotated, not the literal "Kind"
)
`

func TestCollectKindConstants_AliasAnnotatedKind(t *testing.T) {
	file := mustParseFixture(t, aliasKindFixture)
	got := collectKindConstants([]*ast.File{file})

	want := map[string]bool{"KindAnnotated": true, "KindE": true}
	var missed []string
	for name := range want {
		if !got[name] {
			missed = append(missed, name)
		}
	}
	sort.Strings(missed)
	if len(missed) > 0 {
		var found []string
		for name := range got {
			found = append(found, name)
		}
		sort.Strings(found)
		t.Fatalf("collectKindConstants missed %v; found only %v — an alias-annotated Kind (type KindAlias = Kind) must resolve the same as the literal spelling (#0296 item 1)", missed, found)
	}
	if len(got) != len(want) {
		t.Errorf("collectKindConstants returned %d names, want exactly %d (%v) — got %v", len(got), len(want), want, got)
	}
}

// repetitionAfterBareSiblingFixture is #0296 item 2's reproduction.
// KindC is a style-3 repetition (Go's own const-repetition rule) of
// KindB, a style-4 bare/untyped constant swept in only by the same-block
// heuristic. Before #0296's fix, style 3 propagated ONLY isKind forward
// (`info.isKind = lastNonEmpty.isKind`), and a style-4 candidate carries
// isKind == false by construction (it is resolved to Kind only later, by
// the same-block backfill pass) — so KindC inherited isKind=false and was
// never reached by that backfill either, since the backfill only
// considers info.isBare, which KindC never had set. go/types reports
// "untyped string" for both KindB and KindC (#0282's review measured
// this directly), so this is heuristic incompleteness, not something
// go/types would already fix.
//
// Reproduced against the pre-#0296 detector (only propagating isKind in
// the style-3 case, in a scratch copy):
//
//	=== RUN   TestCollectKindConstants_RepetitionAfterBareSibling
//	    outbox_kind_detection_test.go:251: collectKindConstants missed
//	        [KindC]; found only [KindA KindB]
//	--- FAIL: TestCollectKindConstants_RepetitionAfterBareSibling (0.00s)
//
// Propagating isBare alongside isKind in the style-3 case turns this
// green.
const repetitionAfterBareSiblingFixture = `package fixture

type Kind string

const (
	KindA Kind = "a"
	KindB      = "b" // style 4: bare, swept in by the same-block heuristic
	KindC          // style 3: repeats KindB, not KindA — #0296 item 2
)
`

func TestCollectKindConstants_RepetitionAfterBareSibling(t *testing.T) {
	file := mustParseFixture(t, repetitionAfterBareSiblingFixture)
	got := collectKindConstants([]*ast.File{file})

	want := map[string]bool{"KindA": true, "KindB": true, "KindC": true}
	var missed []string
	for name := range want {
		if !got[name] {
			missed = append(missed, name)
		}
	}
	sort.Strings(missed)
	if len(missed) > 0 {
		var found []string
		for name := range got {
			found = append(found, name)
		}
		sort.Strings(found)
		t.Fatalf("collectKindConstants missed %v; found only %v — a repetition (KindC) of a bare style-4 sibling (KindB) must inherit isBare, not just isKind (#0296 item 2)", missed, found)
	}
	if len(got) != len(want) {
		t.Errorf("collectKindConstants returned %d names, want exactly %d (%v) — got %v", len(got), len(want), want, got)
	}
}

// sameBlockFalsePositiveFixture proves #0296 criterion 6: the same-block
// heuristic's false positive on an UNRELATED untyped constant that shares
// a const block with a resolved Kind is DELIBERATE and must be preserved,
// not "fixed" by weakening the detector. defaultKindNote carries no Kind
// semantics at all — it is prose, not a queue kind — but it is untyped,
// carries its own value, and sits in the same const block as
// KindConfirmation (an isKind block), so style 4's same-block rule sweeps
// it in exactly as it would sweep in a genuine, forgotten Kind constant.
// #0282's review measured this exact false positive against internal/
// outbox's real shape (a synthetic KindReviewProbe-style addition) and
// judged it acceptable: it fails LOUD, in the safe direction (a name
// wrongly flagged as needing an entry in mailKinds or kindExceptions,
// cheap to add), and the message points at the exception list rather than
// at the detector. internal/outbox has no bare constant sharing the Kind
// block today, so this never fires in the real guard — this test exists
// purely to pin the behavior so a future "fix" does not quietly narrow
// style 4 and reintroduce #0254's silent-stall gap for a real bare Kind.
const sameBlockFalsePositiveFixture = `package fixture

type Kind string

const (
	KindConfirmation Kind = "confirmation"
	defaultKindNote       = "kinds are declared above" // #0296 criterion 6: untyped, NOT a Kind, still swept in
)
`

func TestCollectKindConstants_SameBlockFalsePositiveIsDeliberate(t *testing.T) {
	file := mustParseFixture(t, sameBlockFalsePositiveFixture)
	got := collectKindConstants([]*ast.File{file})

	if !got["KindConfirmation"] {
		t.Fatalf("collectKindConstants missed KindConfirmation (style 1, unambiguous) — got %v", got)
	}
	if !got["defaultKindNote"] {
		t.Fatalf("collectKindConstants no longer sweeps in an unrelated untyped constant sharing the Kind block — this is a DELIBERATE false positive (#0296 criterion 6, #0282's review), not a defect: do not narrow style 4 to fix this, doing so risks a real Kind going undetected instead")
	}
}
