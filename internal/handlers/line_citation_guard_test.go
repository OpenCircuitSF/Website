package handlers

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// #0356: a `<file>.go:<int>` citation in a Go comment drifts silently. Every
// edit to the CITED file above the cited line moves what the number points
// at, and nothing about such an edit has anything to do with the citation
// itself, so nobody thinks to look — exactly the failure mode `#0352` found
// twice in one file (`internal/outbox/claim_kinds_call_site_guard_test.go`)
// and this guard's own census then found 27 more times across 6 other
// files (`internal/handlers/audit_email_metadata_guard_test.go`,
// `citation_guard_test.go`, `citation_target_guard_test.go`,
// `logging_test.go`; `internal/mailing/templates_test.go`;
// `internal/seo/sitemap_test.go`) — 14 of the 27 already wrong when
// measured, 13 correct only by accident of not yet having been edited.
// §11's PRD section index was rebuilt heading-anchored for the identical
// reason: an identifier is invalidated only by an edit to the thing it
// names, never by an edit to something unrelated above it in the same file.
//
// # Where this belongs: population in Go, shape in the shell harness
//
// This file's own assertion (TestNoCommentCitesGoFileByLineNumber, below)
// is the right home for the POPULATION check: adding a `<file>.go:<int>`
// citation to any Go comment under internal/ or cmd/ moves `got` from 0
// toward 1, so `got == 0` is a non-circular oracle a copy-pasted answer
// cannot satisfy (CLAUDE.md §8's direction-of-comparison rule). But this
// file cannot detect its OWN disarmament — a loosened regex, a weakened
// exemption, or a zeroed floor constant all still compile and still run,
// and an in-package test agreeing with its own edited self is exactly
// `#0258`'s six-pass lesson. That proof lives outside this file, in
// `scripts/go_file_visit_floor_guard_test.sh`, which mutates a COPY of
// this file and asserts failure from outside — see that script's own
// #0356 section for what it mutates and why.
//
// # The one structural exemption, and why it is not an allowlist
//
// `internal/mailing/outbox_kind_detection_test.go` cites itself three times
// inside a `//` + TAB indented block introduced as a captured, verbatim
// `go test` failure transcript ("captured before the fix was applied, not
// reconstructed afterward") — not a citation at all, in the sense this
// guard cares about: rewriting it to match today's line numbers would
// falsify the record it exists to preserve. #0356's planning pass measured
// that accuracy cannot be the discriminator (the transcript's own citation
// is already one line off the real `t.Fatalf` and is CORRECT as it stands),
// so the exemption is a conjunction of three TEXT properties, checked at
// scan time rather than kept in a list anyone has to remember to update:
//
//  1. lineCitationCommentIsIndented — the comment line is `//` immediately
//     followed by a TAB, the one indentation gofmt leaves alone (CLAUDE.md
//     §8's smart-quote/backtick entry is about the same preformatted-block
//     exemption).
//  2. lineCitationIsSelfCitation — the cited path resolves to the file the
//     comment itself is in.
//  3. lineCitationGroupHasTranscriptMarker — the enclosing comment GROUP
//     (the contiguous run of `//` lines the citation sits inside) contains
//     at least one line matching lineCitationTranscriptMarker (`=== RUN `
//     or `--- FAIL/PASS/SKIP: `, also TAB-indented).
//
// All three are required — any one alone is too weak. (1) alone exempts
// any indented block, whatever it contains. (2) alone exempts a citation
// that is stale about ITSELF. (3) alone is spoofable from an ordinary,
// unindented comment. Together they describe "a captured test transcript"
// and nothing else this tree's comments currently contain. There is no
// list of exempt files or names for the tree to drift out from under —
// each predicate is read off the comment text at scan time, the same
// reasoning `#0337`'s bounced `embeddingReviewed` allowlist needed and did
// not have.
//
// Deliberately NOT exempted: a citation framed as history (`db9bff7:` or
// "as of #NNNN", the shape `#0347`'s own adjacent guard family allows for
// its different question). A historical positional citation still drifts
// exactly as silently as a present-tense one — the commit hash or issue
// number pins WHEN the claim was true, not WHERE the line is today — so
// `#0356`'s plan (§8 of issues/0356.md) settled on two guards with
// deliberately incompatible exemption models rather than one guard that
// imports `#0347`'s history allowance into this class and reopens it.
//
// # The honest limit
//
// This guard proves a CONVENTION, not the TRUTH of any comment: that no
// Go comment states a bare `<file>.go:<int>` position. It cannot tell a
// stale citation from an accurate one — doing that for the 27 non-exempt
// citations this issue fixed took reading every cited target by hand, and
// for `internal/handlers/logging_test.go` it additionally took knowing
// that `#0260` deleted the calls the comment described, which no pattern
// over the CITING file could ever reveal. A green run of this guard means
// only "no comment names a line number" — it must never be read as "the
// comments this guard scanned are accurate."
var lineCitationPattern = regexp.MustCompile(`\b[A-Za-z0-9_][A-Za-z0-9_./\-]*\.go:[0-9]+\b`)

// lineCitationTranscriptMarker recognizes a captured `go test` failure
// line — `=== RUN `, or `--- FAIL: ` / `--- PASS: ` / `--- SKIP: ` — the
// only shape #0356's plan found actually justifies a self-citing, TAB-
// indented block being exempt from the guard below. Anchored at the start
// of the comment body (immediately after the leading "//" is stripped),
// requiring the same TAB predicate 1 checks independently, so a marker
// line that is not itself indented does not, on its own, license anything.
var lineCitationTranscriptMarker = regexp.MustCompile(`^\t(=== RUN |--- (FAIL|PASS|SKIP): )`)

// lineCitationGuardMinPlausibleFileCount is this guard's own #0275-style
// floor, deliberately a fresh constant rather than a reuse of
// citedTestScanRootsMinPlausibleFileCount even though it walks the exact
// same citedTestScanRoots with the exact same walkGoFiles (test files
// included, nothing filtered afterward — this guard's own `got` already
// equals what walkGoFiles yields, same as its siblings). A dedicated
// constant is what lets scripts/go_file_visit_floor_guard_test.sh mutate
// THIS guard's own floor to 0 and assert THIS guard specifically fails,
// independent of whatever the three siblings sharing the other constant
// do. Same value and same justification as citedTestScanRootsMinPlausibleFileCount
// (see dangling_test_citation_guard_test.go): 272 real .go files under
// internal/+cmd/+web/ today (measured directly: `find internal cmd web
// -name '*.go' -not -path '*/node_modules/*' -not -path '*/dist/*' | wc
// -l`), comfortably above 150, low enough that the
// tree can shrink without a false alarm, high enough that narrowing to any
// one of the three roots still trips it.
const lineCitationGuardMinPlausibleFileCount = 150

// lineCitationHit is one non-exempt `<file>.go:<int>` citation found in a
// Go comment.
type lineCitationHit struct {
	pos   token.Position
	cited string
}

// lineCitationIsSelfCitation reports whether citation's path portion (the
// text before the final ":<int>") names the very file the comment
// containing it lives in — checked against both the bare basename (the
// shortened form the three real fixtures in
// internal/mailing/outbox_kind_detection_test.go all use — a bare
// filename, no directory, followed by a colon and a line number) and the
// full repo-relative path (the shape some citations use), plus the same
// suffix form pathCitationResolves uses elsewhere in this package ("a real
// path that ENDS WITH '/' + cited"). Deliberately not spelled out as a
// literal `<file>.go:<int>` example here — this comment is itself scanned
// by the guard below, the same self-referential trap
// citation_target_guard_test.go's own comments avoid (#0323).
func lineCitationIsSelfCitation(citation, selfBasename, selfRepoRelPath string) bool {
	idx := strings.LastIndexByte(citation, ':')
	if idx < 0 {
		return false
	}
	pathPart := citation[:idx]
	if pathPart == selfBasename || pathPart == selfRepoRelPath {
		return true
	}
	return strings.HasSuffix(selfRepoRelPath, "/"+pathPart)
}

// lineCitationIsExempt is the pure conjunction #0356's plan settled on,
// deliberately separated from the collection walk below so it can be
// tested directly against synthetic booleans
// (TestLineCitationIsExemptRequiresAllThreePredicates) as well as through
// the real parse-and-scan path
// (TestCollectLineCitationHitsFixtureExemption) — two different levels of
// the same claim, neither able to fake the other.
func lineCitationIsExempt(commentIsIndented, isSelfCitation, groupHasTranscriptMarker bool) bool {
	return commentIsIndented && isSelfCitation && groupHasTranscriptMarker
}

// scanFileForLineCitations applies the guard's regex and exemption rule to
// one already-parsed file. Block comments (/* ... */) can never satisfy
// predicate 1 (the comment line must start "//" then TAB), so every
// citation inside one is reported unconditionally — this tree has none
// today, but the logic does not assume that.
func scanFileForLineCitations(fset *token.FileSet, f *ast.File, selfBasename, selfRepoRelPath string) []lineCitationHit {
	var hits []lineCitationHit
	for _, group := range f.Comments {
		groupHasMarker := false
		for _, c := range group.List {
			if !strings.HasPrefix(c.Text, "//") {
				continue
			}
			if lineCitationTranscriptMarker.MatchString(strings.TrimPrefix(c.Text, "//")) {
				groupHasMarker = true
				break
			}
		}
		for _, c := range group.List {
			if strings.HasPrefix(c.Text, "//") {
				body := strings.TrimPrefix(c.Text, "//")
				indented := strings.HasPrefix(body, "\t")
				pos := fset.Position(c.Slash)
				for _, m := range lineCitationPattern.FindAllString(c.Text, -1) {
					self := lineCitationIsSelfCitation(m, selfBasename, selfRepoRelPath)
					if lineCitationIsExempt(indented, self, groupHasMarker) {
						continue
					}
					hits = append(hits, lineCitationHit{pos: pos, cited: m})
				}
				continue
			}
			// A /* ... */ comment: never TAB-indented in this guard's
			// sense, so never exempt. May itself span several lines, so
			// position each match against the right offset within it.
			basePos := fset.Position(c.Slash)
			for li, raw := range strings.Split(c.Text, "\n") {
				for _, m := range lineCitationPattern.FindAllString(raw, -1) {
					hits = append(hits, lineCitationHit{
						pos:   token.Position{Filename: basePos.Filename, Line: basePos.Line + li},
						cited: m,
					})
				}
			}
		}
	}
	return hits
}

// collectLineCitationHits walks roots (test files included, nothing
// filtered after the walk — the same shape as this package's other
// citedTestScanRoots-based guards) and returns every non-exempt hit plus
// the number of .go files visited, so the caller can assert that count
// plausible before trusting an empty hits slice (#0275's rule, applied
// here the same way TestNoCommentCitesUnresolvedPathOrSection applies it).
func collectLineCitationHits(t *testing.T, repoRoot string, roots []string) ([]lineCitationHit, int) {
	t.Helper()
	var hits []lineCitationHit
	visited := walkGoFiles(t, roots, func(path string) {
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		selfRepoRelPath := toRepoRelativePath(repoRoot, path)
		hits = append(hits, scanFileForLineCitations(fset, file, filepath.Base(path), selfRepoRelPath)...)
	})
	return hits, visited
}

// TestNoCommentCitesGoFileByLineNumber is the guard: it fails if any Go
// comment anywhere under internal/ or cmd/ (test files included) cites a
// `<file>.go:<int>` position, unless lineCitationIsExempt's three-predicate
// conjunction holds. The assertion is zero non-exempt citations —
// deliberately not a count compared against a retained population: #0356's
// plan rejected grandfathering the 13 citations that were accurate when
// measured, on the same reasoning CLAUDE.md §8 gives for why
// `#0337`'s `embeddingReviewed` allowlist did not stay true — a retained
// population is an allowlist wearing a number.
func TestNoCommentCitesGoFileByLineNumber(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	baseDir := filepath.Dir(thisFile)
	repoRoot := filepath.Join(baseDir, "..", "..")

	var roots []string
	for _, rel := range citedTestScanRoots {
		roots = append(roots, filepath.Join(baseDir, rel))
	}

	hits, visited := collectLineCitationHits(t, repoRoot, roots)

	// #0275: assert the walk actually visited a plausible number of files
	// BEFORE trusting an empty hits slice — an empty or narrowed
	// citedTestScanRoots must be a hard failure here, never silently read
	// as "no positional citations found".
	assertGoFileVisitCountPlausible(t, "TestNoCommentCitesGoFileByLineNumber", citedTestScanRoots, visited, lineCitationGuardMinPlausibleFileCount)

	if len(hits) == 0 {
		return
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].pos.Filename != hits[j].pos.Filename {
			return hits[i].pos.Filename < hits[j].pos.Filename
		}
		return hits[i].pos.Line < hits[j].pos.Line
	})
	var b strings.Builder
	for _, h := range hits {
		rel := toRepoRelativePath(repoRoot, h.pos.Filename)
		fmt.Fprintf(&b, "\n  %s:%d: cites %q by line number — replace with a stable identifier (a declaration, function, or test name), not a corrected number (#0356; #0352's settled rule)", rel, h.pos.Line, h.cited)
	}
	t.Errorf("%d Go comment(s) cite a <file>.go:<int> position, which drifts silently on any unrelated edit to the cited file:%s", len(hits), b.String())
}

// TestLineCitationIsExemptRequiresAllThreePredicates proves the exemption
// is a genuine conjunction, at the level of the pure boolean combiner: no
// single true predicate, nor any two, is enough on its own. Per CLAUDE.md
// §8, the want values below are chosen for this test alone, not copied
// from lineCitationIsExempt's own body, so an edit that weakens the
// conjunction (e.g. "||" in place of "&&") is what this test exists to
// catch, not merely restate.
func TestLineCitationIsExemptRequiresAllThreePredicates(t *testing.T) {
	cases := []struct {
		name                                      string
		indented, self, hasTranscriptMarker, want bool
	}{
		{"all three true", true, true, true, true},
		{"missing indent", false, true, true, false},
		{"missing self-citation", true, false, true, false},
		{"missing transcript marker", true, true, false, false},
		{"only indent", true, false, false, false},
		{"only self-citation", false, true, false, false},
		{"only transcript marker", false, false, true, false},
		{"none", false, false, false, false},
	}
	for _, tc := range cases {
		if got := lineCitationIsExempt(tc.indented, tc.self, tc.hasTranscriptMarker); got != tc.want {
			t.Errorf("%s: lineCitationIsExempt(%v, %v, %v) = %v, want %v", tc.name, tc.indented, tc.self, tc.hasTranscriptMarker, got, tc.want)
		}
	}
}

// TestCollectLineCitationHitsFixtureExemption is criterion 7's synthetic
// proof at the OTHER level: real source, parsed with go/parser, run
// through the actual scanFileForLineCitations the shipped guard calls —
// not just the boolean combiner above, which could pass while the wiring
// between predicate-computation and the regex match were wrong. Two
// fixtures, both self-citing "fixture_test.go" and both TAB-indented:
// one carries a real transcript marker and must NOT be flagged; the other
// looks identical except for the missing marker and MUST be flagged —
// proving the exemption is the three-way conjunction, not any single
// predicate (indentation and self-citation alone are present in both).
func TestCollectLineCitationHitsFixtureExemption(t *testing.T) {
	const withMarker = `package fixture

// Captured transcript, self-citing, indented, WITH a marker line — exempt.
//
//	=== RUN   TestSomething
//	    fixture_test.go:42: boom
//	--- FAIL: TestSomething (0.00s)
func F() {}
`
	const withoutMarker = `package fixture

// Looks like a transcript line, self-citing, indented, but the enclosing
// group carries NO === RUN / --- FAIL|PASS|SKIP: marker anywhere — must
// still be flagged.
//
//	fixture_test.go:42: this is not a real captured transcript.
func F() {}
`
	scan := func(src string) []lineCitationHit {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "fixture_test.go", src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse fixture: %v", err)
		}
		return scanFileForLineCitations(fset, f, "fixture_test.go", "fixture_test.go")
	}

	if hits := scan(withMarker); len(hits) != 0 {
		t.Errorf("withMarker: want 0 hits (exempt: indented, self-citing, real transcript marker), got %d: %+v", len(hits), hits)
	}
	if hits := scan(withoutMarker); len(hits) != 1 {
		t.Errorf("withoutMarker: want exactly 1 hit (not exempt: no transcript marker in the group), got %d: %+v", len(hits), hits)
	} else if hits[0].cited != "fixture_test.go:42" {
		t.Errorf("withoutMarker: want the hit to be fixture_test.go:42, got %q", hits[0].cited)
	}
}

// TestLineCitationPatternExcludesNonGoExtensions proves the regex is
// deliberately narrower than #0356's planning-pass census pattern (which
// intentionally matched .md/.ts/.svelte too, to prove those extensions
// were the only subtraction between "37 citations of any extension" and
// "30 .go"): this guard's own pattern must match only a `.go:<int>`
// citation, never PRD.md:671, citationGuard.test.ts:76, or
// WorkshopsIndex.svelte:7 — all three real, pre-existing citations in this
// package's own files today, which would falsely inflate this guard's
// population if the pattern were the wider one.
func TestLineCitationPatternExcludesNonGoExtensions(t *testing.T) {
	text := "see PRD.md:671, citationGuard.test.ts:76, WorkshopsIndex.svelte:7, and worker.go:625"
	got := lineCitationPattern.FindAllString(text, -1)
	want := []string{"worker.go:625"}
	if len(got) != len(want) || (len(got) > 0 && got[0] != want[0]) {
		t.Errorf("lineCitationPattern.FindAllString(%q) = %v, want %v", text, got, want)
	}
}
