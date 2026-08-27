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
