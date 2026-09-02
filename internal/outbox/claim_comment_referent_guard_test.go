// claim_comment_referent_guard_test.go is #0347's guard. It checks two
// things about every Go comment naming one of the five claim-machinery
// identifiers (ClaimDue, SelectDue, OrphanSweep, ClaimRow, ClaimBatch).
// First: that its own file's code actually makes the call the comment
// claims. Second: that a claimed file-relative position -- earlier in the
// file, versus later in the file -- holds true against that same file's
// code.
//
// # Why this exists
//
// #0342 bounced twice on exactly this class of stale comment -- nine sites
// across two implementation passes and three reviews, each pass finding
// what the last missed, because every prior pass used the same instrument
// (a keyword grep plus a human read). #0342's second review built a
// per-file referential-integrity audit (does a comment-named identifier
// exist in that FILE's own code census) and its third review added a
// four-axis structural sweep (directional, cross-package, arity, file
// referents) that found the remaining sites by construction. This file
// mechanises the two axes #0347 asks for: existence-with-attribution
// (criterion 1, corrected) and direction (the amendment's added axis).
//
// # What "names a call this file's code makes" means here
//
// #0342's third review measured that criterion 1's ORIGINAL present-tense
// framing fires on nine CORRECT comments in
// internal/handlers/subscribe_intake_test.go, all of which mention
// ClaimDue and none of which claim this file's code invokes it: some name
// outbox.Store.ClaimDue or h.intake.ClaimDue as an EXISTING API declared in
// a different package, some narrate a deliberate, reverted worktree
// mutation ("reverting intakePass ... to a whole-batch ClaimDue"), one is
// an explicit negation ("rather than h.intake.ClaimDue"). None of the nine
// claims that intakePass, or anything else in that file, presently calls
// ClaimDue.
//
// So the check below does NOT fire on every bare mention of a target
// identifier in a file with zero code calls to it -- that would repeat
// exactly the false-positive class the amendment describes. It fires only
// when the comment attributes an ACTUAL INVOCATION to something local:
//
//   - a qualified reference "Type.Ident" where Type is a type declared in
//     THIS FILE's OWN PACKAGE (not an external package's type, which is
//     read as "naming an existing API" rather than a claim about this
//     file) AND the word "call"/"calls" appears immediately after the
//     identifier -- the exact shape of the real defect this guard is
//     required to catch (see the falsifiability fixture below:
//     "OutboxWorker.ClaimDue call", where OutboxWorker is declared in this
//     same package);
//   - literal call syntax, Ident(...), inside a comment at all -- which
//     #0347 criterion 7 / #0323 already ban unconditionally, so this is
//     belt-and-suspenders alongside scripts/go_file_visit_floor_guard_test.sh's
//     external, text-based oracle (CLAUDE.md §8: the two homes are not
//     redundant, since that harness cannot distinguish code from comments
//     and this one cannot see files outside claimCommentGuardScanRoots).
//
// A qualified reference to a DIFFERENT package's type (outbox.Store.X from
// inside internal/handlers or internal/mailing), a same-package Type.Ident
// mention with no "call"/"calls" word nearby, or ANY bare (unqualified)
// mention regardless of nearby wording, is read as "naming the
// identifier", not "claiming this file invokes it", and is not flagged.
// Bare mentions are deliberately not checked at all here, even when
// immediately followed by "call"/"calls": measured against the real tree,
// not assumed, an earlier draft flagged exactly that shape and produced
// double-digit false positives in this package's own sibling guard file,
// whose entire job is to describe -- correctly, in bare prose like "a
// ClaimDue, OrphanSweep, or SelectDue call OUTSIDE internal/outbox" -- code
// that lives in OTHER files it scans, not in itself. This is deliberately
// conservative: it will miss some stale comments that describe a call in
// prose without ever qualifying the identifier by this package's own type
// -- see "What this guard cannot do" below.
//
// # The historical-marker convention (criterion 3)
//
// A comment sentence carrying an explicit historical, hypothetical, or
// negating marker -- "since #NNNN", "has not called", "no longer", "used
// to", "the old ", "at the time", "was removed", "removed by", "replaced",
// "predates", "pre-#NNNN", "historical", "rather than", "instead of",
// "would", "reverting"/"revert"/"reverted", "cannot", "never" -- is exempt
// from the EXISTENCE axis below, matching #0342's own round-2 audit's
// convention. This is deliberately broad (a human already read every one
// of #0342's findings and confirmed the correctly-historical ones carry
// language like this); the guard's job is to surface candidates and
// require a human convention to have been followed, not to adjudicate
// prose.
//
// The DIRECTIONAL axis uses a DIFFERENT, narrower marker list --
// claimCommentGuardDirHistoricalRe below, dropping "cannot" and "never" --
// not the same regex reused. #0347's review (bounce 1) measured that
// reusing the broad list here silently exempted BOTH real defects this
// axis exists to catch (#0342 sites A and C -- at commit db9bff7, site A
// is mailKinds' doc comment and site C is the inline comment preceding
// the OrphanSweep call in OutboxWorker.pass, both in
// internal/mailing/outbox_worker.go): both sites'
// clauses contain "never" as an ordinary present-tense behavioural modal
// ("so this worker never claims OR sweeps...", "so this sweep can never
// release a live claim..."), not as a historical reference, and the broad
// exemption swallowed them. Criterion 3's convention is about HISTORY;
// negation and modality are common in completely ordinary, CORRECT
// directional prose ("X below never does Y") and must not be read as
// exempting it. See claimCommentGuardDirHistoricalRe's own doc comment,
// below, for the full account.
//
// # What this guard cannot do
//
// Recorded here because #0342's third review measured the limit and #0347
// requires it not be papered over: this guard finds STRUCTURAL errors --
// an identifier this file's code never calls, or a direction the code does
// not satisfy. A comment that is false about BEHAVIOUR, while every
// identifier it names exists, in the right file, direction, and arity, is
// invisible to it. That was #0342's own original site 1 ("a Stop landing
// anywhere in sendOne still sees it as outstanding") -- it named only real
// things and was simply wrong about what happens. Only a human read found
// that, and only a human read will find the next one like it. A green run
// of this guard means "no comment misattributes a claim-machinery call or
// its position", not "the comments are accurate".
//
// The directional axis is narrower still, and its exact limits are NOT
// enumerated here in prose (#0358). #0347's own C2 doc addition once did
// that -- four window limits, described in a sentence -- and #0347's own
// third review found a FIFTH the same day, which is exactly the failure
// this file's whole subject is about: a sentence claiming more than the
// code beside it delivers. A list here would go stale the next time the
// window logic changes and nothing would notice.
//
// So the calibration is pinned as FIXTURES instead, each one naming, in
// its own comment, which trade it pins and against what opposing error:
// claimCommentGuardDirectionalCalibrationCases, below, and
// TestClaimCommentGuardDirectionalAxisIsCleanOnStoreGoParenScoping, which
// pins the real internal/outbox/store.go false positive the parenthesis
// scoping exists to avoid. Read the table for the trade surfaces that are
// currently pinned, and add a row when you move one. A green
// `go test ./internal/outbox/...` after a window-logic change means every
// surface the table pins still holds -- not that no unpinned surface moved;
// the table is a growing floor under the calibration, not an inventory of
// it.
//
// # Where the oracle lives (criterion 6)
//
// In Go, in-package, per CLAUDE.md §8's rule: the deciding question is
// whether a mutation leaves the assertion falsifiable by the thing it
// measures. Adding a false present-tense claim to a comment -- the
// mutation this guard exists to catch -- changes what the mode-0/
// ParseComments comparison below finds: the code census does not move
// (mode 0 never sees comments, criterion 2), but the comment population
// this test walks does, and a new finding appears where there was none.
// That is a legitimate, non-circular oracle: the comment text cannot
// satisfy the check that measures it, because the check's other input (the
// code census) is built from a parse that structurally excludes comments.
// Compare #0356's plan for its own, sibling guard, which draws the
// identical conclusion for the same reason and reaches the same home.
//
// THIS REASONING COVERS THE FINDINGS ASSERTIONS ONLY -- not the three
// claimCommentGuardMinPlausible* floor constants below. Those are a
// DIFFERENT comparison direction (CLAUDE.md §8: "the deciding question ...
// turns on the direction of the comparison, not the kind of thing
// mutated"), and #0347's review measured, on a private copy, that it does
// NOT hold for two of the three: forcing
// claimCommentGuardMinPlausibleFileCount or
// claimCommentGuardMinPlausibleCommentGroupCount to 0 leaves `go test
// ./internal/outbox/...` green, because a floor mutated DOWNWARD makes
// `got < floor` permanently unfalsifiable by a real, non-negative `got` --
// the identical reasoning scripts/go_file_visit_floor_guard_test.sh's own
// header comment gives for why it, not `go test`, must be the floors'
// oracle. Only claimCommentGuardMinPlausibleCallCensusCount has a genuine
// in-package, non-circular proof
// (TestClaimCommentGuardCensusFloorCatchesRootsWithNoRealPopulation), because
// ITS assertion narrows the SCAN ROOTS rather than the floor constant, which
// does change `got`. The other two floors' only oracle is
// scripts/go_file_visit_floor_guard_test.sh's own "outbox
// claim-comment-referent guard floors" section, mirroring the identical
// treatment its sibling section already gives
// claimKindsGuardMinPlausibleCallSiteCount and
// claimKindsGuardMinPlausibleNonExemptCallSiteCount in
// claim_kinds_call_site_guard_test.go. A green `go test
// ./internal/outbox/...` alone is therefore NOT sufficient evidence that
// these two floors are pinned; `scripts/check.sh guards` (which runs that
// script) is required for that.
package outbox

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// claimCommentGuardScanRoots mirrors claimKindsGuardScanRoots
// (claim_kinds_call_site_guard_test.go, this package): ".." is internal/
// (every sibling package, including this one), "../../cmd" is the binary
// entrypoint. Kept as this guard's own named var, not a reference to its
// sibling's, so the two can be independently grepped and independently
// re-pointed if the two guards' scope ever needs to diverge -- but today
// the values are identical and both cover the only trees that can contain
// a claim-machinery caller or a comment describing one.
var claimCommentGuardScanRoots = []string{"..", "../../cmd"}

// claimCommentGuardTargetIdents is #0347 acceptance criterion 1's exact
// five names.
var claimCommentGuardTargetIdents = []string{"ClaimDue", "SelectDue", "OrphanSweep", "ClaimRow", "ClaimBatch"}

// claimCommentGuardCallSyntaxLiterals is #0347 criterion 7's exact three --
// a subset of the five above. ClaimRow and ClaimBatch are not named by
// that criterion, and scripts/go_file_visit_floor_guard_test.sh's own
// population (claimKindsGuardMinPlausibleCallSiteCount, this package's
// sibling guard) only tracks these three; matching that set here keeps
// this Go-side check aimed at the same population that harness measures,
// rather than silently policing a wider one.
//
// Assembled rather than written as contiguous literals on purpose: the
// external harness (scripts/go_file_visit_floor_guard_test.sh) counts this
// syntax textually and cannot tell code from a real call site, so writing
// them whole would add a seventh divergent site to the population that
// bounds claimKindsGuardMinPlausibleCallSiteCount -- exactly what
// claim_kinds_call_site_guard_test.go's own doc comment deliberately
// declined to do (#0323). Do not "simplify" this back -- #0347's first
// review measured that writing these three whole moved that harness's
// grep-based population 42 -> 43.
var claimCommentGuardCallSyntaxLiterals = []string{
	"." + "ClaimDue" + "(",
	"." + "OrphanSweep" + "(",
	"." + "SelectDue" + "(",
}

func claimCommentGuardIsTargetIdent(name string) bool {
	for _, n := range claimCommentGuardTargetIdents {
		if n == name {
			return true
		}
	}
	return false
}

var (
	claimCommentGuardIdentRe         = regexp.MustCompile(`\b(ClaimDue|SelectDue|OrphanSweep|ClaimRow|ClaimBatch)\b`)
	claimCommentGuardQualBeforeRe    = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\.\s*$`)
	claimCommentGuardCallWordAfterRe = regexp.MustCompile("^[`'\"(),.;:\\-\\s]{0,8}(call|calls)\\b")
	claimCommentGuardDirWordRe       = regexp.MustCompile(`(?i)\b(below|above)\b`)
	claimCommentGuardHistoricalRe    = regexp.MustCompile(`(?i)\b(before #|since #|has not called|no longer|used to|formerly|the old |at the time|was removed|removed by|replaced|predates|pre-#|historical|rather than|instead of|would|reverting|revert(ed)?|cannot|never)\b`)

	// claimCommentGuardDirHistoricalRe is the DIRECTIONAL axis's own,
	// narrower marker set -- used ONLY in that axis, in place of
	// claimCommentGuardHistoricalRe above. #0347's review (bounce 1, B1)
	// measured that the broad set's negation/modal words ("never",
	// "cannot") are ordinary PRESENT-TENSE behavioural modals, not
	// historical markers, and both real pre-fix defects this axis was
	// built to catch (#0342 sites A and C -- at commit db9bff7, site A is
	// mailKinds' doc comment and site C is the inline comment preceding the
	// OrphanSweep call in OutboxWorker.pass, both in
	// internal/mailing/outbox_worker.go) carry one in
	// the SAME clause as the false directional claim -- "so this worker
	// never claims OR sweeps a row it cannot render" (site A), "so this
	// sweep can never release a live claim" (site C). Under the broad set,
	// both sentences matched "never" and were silently exempted, which is
	// the same over-broad-exemption defect the existence axis's own
	// "rather than" bug (see the ## Fix history in issue #0347) reappearing
	// one clause-word later. Criterion 3's convention is about HISTORY;
	// importing negation and modality into the DIRECTIONAL axis specifically
	// disarms it on completely ordinary prose, since a sentence asserting
	// "X below" very commonly also asserts what X does or does not do in
	// the same breath ("never", "cannot", "always"). The existence axis
	// keeps the broad set unchanged -- several of the nine
	// subscribe_intake_test.go negative-fixture mentions rely on ITS
	// negation words (see that axis's own comment above, and
	// TestClaimCommentGuardIsCleanOnKnownNegativeFixture) -- so this is a
	// second, narrower regex, not an edit to the first.
	claimCommentGuardDirHistoricalRe = regexp.MustCompile(`(?i)\b(before #|since #|has not called|no longer|used to|formerly|the old |at the time|was removed|removed by|replaced|predates|pre-#|historical|rather than|instead of|would|reverting|revert(ed)?)\b`)
)

// claimCommentGuardFileFacts holds what a mode-0 (comment-free) parse of
// one file reveals about that file's own code: how many times it calls
// each target identifier (any CallExpr whose function name, bare or
// selected, matches -- matching claim_kinds_call_site_guard_test.go's own
// name-only convention, see nameMatchesGuardedMethod's doc comment there
// for why a name-only match is the right level of precision), and every
// byte offset at which the identifier appears as any kind of expression
// (used by the directional axis, which cares about POSITION, not call
// syntax specifically).
type claimCommentGuardFileFacts struct {
	pkg          string
	callCount    map[string]int
	identOffsets map[string][]int
}

// claimCommentGuardBuildFileFacts parses src (nil to read path from disk)
// at path with mode 0 -- comments never enter the AST at all, so comment
// text can never contribute to this census (#0347 criterion 2: "a comment
// must not be able to satisfy the check that measures it").
func claimCommentGuardBuildFileFacts(fset *token.FileSet, path string, src any) (*claimCommentGuardFileFacts, error) {
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, fmt.Errorf("mode-0 parse of %s: %w", path, err)
	}
	if len(f.Comments) != 0 {
		return nil, fmt.Errorf("mode-0 parse of %s yielded comments -- the census would be contaminated by comment text", path)
	}
	facts := &claimCommentGuardFileFacts{
		pkg:          f.Name.Name,
		callCount:    map[string]int{},
		identOffsets: map[string][]int{},
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && claimCommentGuardIsTargetIdent(id.Name) {
			facts.identOffsets[id.Name] = append(facts.identOffsets[id.Name], fset.Position(id.Pos()).Offset)
		}
		if call, ok := n.(*ast.CallExpr); ok {
			var name string
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			if claimCommentGuardIsTargetIdent(name) {
				facts.callCount[name]++
			}
		}
		return true
	})
	return facts, nil
}

// claimCommentGuardBuildPackageTypeIndex parses every .go file under roots
// with mode 0 and records, per package NAME (matching #0342's third
// review's own scratchpad audit tool's precedent for this repo -- see
// issue #0347's own ## Notes for that tool's provenance and this session's
// independent re-verification of it), every declared type name. This is
// what lets the existence axis tell "OutboxWorker.X",
// where OutboxWorker is declared in package mailing, from
// "outbox.Store.X", where Store is declared in a DIFFERENT package -- the
// distinction #0342's third review measured is exactly what separates a
// stale local-call claim from a correct reference to another package's
// API.
func claimCommentGuardBuildPackageTypeIndex(t *testing.T, roots []string) (map[string]map[string]bool, int) {
	t.Helper()
	pkgTypes := map[string]map[string]bool{}
	visited := walkClaimKindsGuardFiles(t, roots, func(path string) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			// Not this guard's concern here -- findClaimCommentGuardFindings
			// (below) parses every file it actually scans for findings and
			// will surface a real parse failure for one of THOSE files loudly.
			return
		}
		pkg := f.Name.Name
		if pkgTypes[pkg] == nil {
			pkgTypes[pkg] = map[string]bool{}
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					pkgTypes[pkg][ts.Name.Name] = true
				}
			}
		}
	})
	return pkgTypes, visited
}

// claimCommentFinding is one thing this guard's two axes found.
type claimCommentFinding struct {
	axis    string // "existence" or "directional"
	ident   string
	path    string
	line    int
	detail  string
	snippet string
}

// claimCommentGuardSentence returns the sentence in flat that contains
// [start,end) -- the span between the nearest sentence-ending punctuation
// (".;:!?") before start and at or after end, or the whole string's edges
// if none is found. Used to scope the historical-marker exemption
// (#0347 criterion 3) to the clause actually making the claim, not to the
// whole comment group: a paragraph routinely contains an UNRELATED
// historical/negation marker describing something else entirely, and
// exempting on the group would silently exempt a real defect alongside it
// (measured directly -- see TestClaimCommentGuardFlagsKnownStaleSiteFromFfef8cf's
// own fixture, whose real ffef8cf paragraph contains "rather than" in an
// unrelated clause later in the SAME group).
func claimCommentGuardSentence(flat string, start, end int) string {
	lo := claimCommentGuardLastBoundary(flat[:start])
	hi := end + claimCommentGuardFirstBoundary(flat[end:])
	if lo > hi {
		lo = hi
	}
	return flat[lo:hi]
}

// claimCommentGuardBoundaryRunes is deliberately wider than ".;:!?":
// measured against the real tree, not assumed, a first draft of
// claimCommentGuardSentence used only that set and failed its own
// falsifiability fixture -- ffef8cf's real comment (confirmed byte-for-byte
// via `git show ffef8cf:internal/mailing/outbox_worker_test.go`, not
// retyped from memory) joins its clauses with U+2014 EM DASH ("—"), not a
// period, so "OutboxWorker.ClaimDue call ... — so it must be removed
// explicitly rather than relying on..." was read as ONE sentence and the
// later, unrelated "rather than" silently exempted the real defect. This
// codebase also uses the ASCII double-hyphen "--" the same way (see this
// very file), handled separately below since IndexAny/LastIndexAny only
// match single runes.
const claimCommentGuardBoundaryRunes = ".;:!?—"

func claimCommentGuardLastBoundary(s string) int {
	best := 0
	if k := strings.LastIndexAny(s, claimCommentGuardBoundaryRunes); k >= 0 {
		_, sz := utf8.DecodeRuneInString(s[k:])
		if k+sz > best {
			best = k + sz
		}
	}
	if k := strings.LastIndex(s, "--"); k >= 0 && k+2 > best {
		best = k + 2
	}
	return best
}

func claimCommentGuardFirstBoundary(s string) int {
	best := len(s)
	if k := strings.IndexAny(s, claimCommentGuardBoundaryRunes); k >= 0 && k < best {
		best = k
	}
	if k := strings.Index(s, "--"); k >= 0 && k < best {
		best = k
	}
	return best
}

// claimCommentGuardOpenParen returns the byte offset just after the
// innermost still-open "(" in s, or -1 if none is open. Used by the
// directional axis, defined further down in this file, to scope its
// identifier window to a parenthetical the direction word sits inside, per
// #0347's review (bounce 1, B1, part 3): checking every identifier in the
// window rather than only the nearest (part 2, same review) reintroduces a
// real false positive in store.go, whose real comment names ClaimRow as
// the sentence's own subject and then, in a trailing parenthetical, points
// a directional word at a second, EARLIER identifier -- SelectDue --
// without this scoping, the sentence's OTHER identifier (ClaimRow, well
// outside that parenthetical) would also be checked and, having no
// matching occurrence earlier in the file, falsely fire. A trailing
// parenthetical's directional word binds to what the parenthetical itself
// names, not to the sentence's own subject.
func claimCommentGuardOpenParen(s string) int {
	depth := 0
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ')':
			depth++
		case '(':
			if depth == 0 {
				return i + 1
			}
			depth--
		}
	}
	return -1
}

// claimCommentGuardDirectionalWindowLo computes the left edge of the
// directional axis's backward-looking window for a direction-word match
// starting at byte offset dmStart within flat. Anchor on the nearer of (a)
// 90 chars back, or (b) the last clause boundary claimCommentGuardLastBoundary
// finds -- the SAME boundary set the historical-marker check uses, including
// the em dash ("—") this codebase writes clauses with far more than it
// writes periods. An earlier draft trimmed only on ".;:!?" here (a
// DIFFERENT, narrower set, drifted out of sync from having two copies of
// the same idea) and a left-to-right scan lets an EARLIER word consume a
// LATER direction claim and hide the real one -- measured against the real
// tree, not assumed: a claim-machinery identifier named before an em dash,
// in a clause unrelated to a directional word that follows it, used to leak
// into this window (internal/mailing/outbox_worker_test.go's own "#0297 —
// see this test's own doc comment ABOVE..." was the reproduction: the OTHER
// identifier named earlier in that same paragraph, well before the em dash,
// is what leaked). Also scopes to the innermost still-open parenthesis, when
// the direction word sits inside one -- see claimCommentGuardOpenParen's own
// doc comment, above, for the store.go false positive this closes, which
// checking every identifier in the window would otherwise reintroduce on
// its own (#0347's review, bounce 1, B1, part 3).
//
// Extracted from findClaimCommentGuardFindings's directional axis (#0364),
// byte-identical to what was inline there before -- verified by re-running
// #0358's and #0363's full mutation batteries against the extracted form
// and confirming identical pass/fail results throughout (see #0364's
// ## Verification, issues/0364.md). The extraction exists so
// claimCommentGuardDiagnoseDirectional, defined near the calibration table
// below, calls this SAME function to decide whether a target identifier
// falls inside the window, rather than holding a second, independently
// -drifting copy of this arithmetic -- CLAUDE.md §8: a copy of the answer
// stored next to the question is not a check. #0358 criterion 5 / #0364
// criterion 5 freeze the CALIBRATION this produces, not the act of naming
// it: every constant and every clamp this function makes is unchanged from
// what was inline in the directional axis before this extraction.
func claimCommentGuardDirectionalWindowLo(flat string, dmStart int) int {
	lo := dmStart - 90
	if b := claimCommentGuardLastBoundary(flat[:dmStart]); b > lo {
		lo = b
	}
	if pOpen := claimCommentGuardOpenParen(flat[:dmStart]); pOpen > lo {
		lo = pOpen
	}
	if lo < 0 {
		lo = 0
	}
	return lo
}

// claimCommentGuardSnippet returns context around [start,end) in flat.
// Rune-boundary-safe: #0342's third review's own scratchpad audit tool's
// doc comment records that an earlier draft of this exact kind of helper
// sliced by byte index,
// split a multi-byte rune, and produced invalid UTF-8 that BSD grep then
// silently refused to match (CLAUDE.md §8). Re-derived independently here
// rather than trusting that scratchpad tool's claim.
func claimCommentGuardSnippet(flat string, start, end int) string {
	loB := start - 70
	if loB < 0 {
		loB = 0
	}
	hiB := end + 70
	if hiB > len(flat) {
		hiB = len(flat)
	}
	// Widen outward to the nearest rune boundary rather than narrowing
	// inward, so the snippet never loses the match itself.
	for loB > 0 && !utf8.RuneStart(flat[loB]) {
		loB--
	}
	for hiB < len(flat) && !utf8.RuneStart(flat[hiB]) {
		hiB++
	}
	return flat[loB:hiB]
}

// claimCommentGuardFlattenComment collapses a parsed comment group's text
// (ast.CommentGroup.Text(), which keeps line breaks and leading "// ") down
// to single-spaced prose, the shape every regex and offset computation in
// this file operates on. #0366 (non-blocking residual, noted by #0364's
// review): this exact expression -- strings.Join(strings.Fields(text), " ")
// -- used to live twice, once inline in findClaimCommentGuardFindings's
// per-comment-group loop and once inline in
// claimCommentGuardDiagnoseDirectional, the one fragment where the two could
// silently diverge (CLAUDE.md §8: a copy of the answer stored next to the
// question is not a check -- this applies just as much to two copies of a
// TRANSFORM as to two copies of an ASSERTION). Folded into one function both
// call; behaviour-preserving, since it is textually the same expression
// moved, not rewritten.
//
// #0374 (recorded, not closed -- CLAUDE.md §8's #0258 caution binds on this
// file, six in-file mechanisms already, so a seventh is not the default
// answer to a gap this cheap to state): the calibration table cannot detect
// this function doing nothing. Every row in
// claimCommentGuardDirectionalCalibrationCases is a SINGLE-LINE comment
// fixture, so this function's body is a semantic no-op on all 24 of them --
// replacing it with "return text" leaves not just the calibration table but
// the WHOLE internal/outbox package green (measured, not the table alone).
// Pre-existing, not introduced by the #0366 extraction: the identical
// mutation applied to both of the pre-#0366 inline copies is equally green.
// A fixture that discriminates would need a genuine multi-line comment --
// new fixture surface of the #0330/#0347/#0356 "record, not close" shape
// those three families ended by refusing.
//
// That gap is in the TABLE, not in the TREE -- this function is not dead
// code. Measured directly: real, checked-in multi-line comments run through
// it today and come out changed, not identical. Two examples, deliberately
// PARAPHRASED rather than quoted -- #0347's own history records that
// quoting a real defect sentence verbatim in a doc comment trips this
// guard's directional axis against itself (rephrase, don't delete, is that
// issue's own stated convention, and this file scans itself). Both examples
// are named by STABLE IDENTIFIER rather than by file:line -- #0352's settled
// rule, enforced repo-wide by internal/handlers' TestNoCommentCitesGoFileByLineNumber
// (#0356), which this record's first draft tripped from another package.
// The ClaimRow doc comment in internal/outbox/store.go names a sibling
// identifier inside a trailing parenthetical whose direction word sits alone
// at the start of the following physical line (the parenthetical
// claimCommentGuardStoreGoParenSentinelRe, below, is pinned against) --
// joined, the two lines become one sentence. The inline comment preceding
// the OrphanSweep call in OutboxWorker.pass, in
// internal/mailing/outbox_worker.go -- the site #0342 corrected from a
// misnamed identifier -- has the identical shape: the identifier ends one
// physical line and the direction word that names its position starts the
// next, and joining is what makes the two lines read as one claim about
// where that identifier is. (#0342's own file does not say flattening was
// how that site was originally found, so that causal claim is not repeated
// here -- only the shape, which is verified against both files as they read
// today.)
//
// One further check so this record does not overstate the other way: on
// BOTH of those two real sites, `go test ./internal/outbox/...` as a WHOLE
// -- not only the calibration table -- also stays green under the "return
// text" mutation, because the window/regex computations downstream of this
// function tolerate a bare "\n" much like a space for these two comments'
// particular shapes. So: this function is not a no-op on real input, but no
// test in this package would currently catch its removal via a real file
// either. Both gaps are real, and neither is closed by this comment --
// closing them is exactly the fixture-adding move this issue declined.
func claimCommentGuardFlattenComment(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// findClaimCommentGuardFindings is the pure, directly testable core
// (CLAUDE.md §8: a guard's oracle must not be the same bytes as its
// subject -- this is exercised against in-memory fixtures below, not only
// against whatever the repo currently contains, matching
// findOutboxCallSitesInFile's own precedent in this package). It parses
// path/src twice -- mode 0 for the code census, ParseComments for the
// comment population -- and returns every existence and directional
// finding.
func findClaimCommentGuardFindings(path string, src any, allPkgTypes map[string]map[string]bool) ([]claimCommentFinding, error) {
	factsFset := token.NewFileSet()
	facts, err := claimCommentGuardBuildFileFacts(factsFset, path, src)
	if err != nil {
		return nil, err
	}
	pkgTypes := allPkgTypes[facts.pkg]

	commentFset := token.NewFileSet()
	f, err := parser.ParseFile(commentFset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("comment parse of %s: %w", path, err)
	}

	var findings []claimCommentFinding
	for _, cg := range f.Comments {
		text := cg.Text()
		if text == "" {
			continue
		}
		flat := claimCommentGuardFlattenComment(text)
		startOff := commentFset.Position(cg.Pos()).Offset
		endOff := commentFset.Position(cg.End()).Offset
		line := commentFset.Position(cg.Pos()).Line

		// --- existence axis (criterion 1, corrected) ---
		for _, m := range claimCommentGuardIdentRe.FindAllStringIndex(flat, -1) {
			mStart, mEnd := m[0], m[1]
			ident := flat[mStart:mEnd]

			if facts.callCount[ident] > 0 {
				continue // this file's code really does call it -- never stale by definition
			}
			// #0347 criterion 3's convention -- scoped to the SENTENCE
			// containing this occurrence, not the whole comment group/
			// paragraph. An earlier draft checked the whole group and was
			// caught, by this file's own falsifiability fixture, exempting
			// the real ffef8cf defect because an UNRELATED "rather than"
			// later in the same paragraph ("...so it must be removed
			// explicitly rather than relying on...") matched: a paragraph
			// is not a claim, a sentence is, and the marker must qualify
			// the SAME claim it is being read to excuse.
			if claimCommentGuardHistoricalRe.MatchString(claimCommentGuardSentence(flat, mStart, mEnd)) {
				continue
			}

			before := flat[:mStart]
			after := flat[mEnd:]
			callAdjacent := claimCommentGuardCallWordAfterRe.MatchString(after)

			if qm := claimCommentGuardQualBeforeRe.FindStringSubmatch(before); qm != nil {
				qualifier := qm[1]
				r, _ := utf8.DecodeRuneInString(qualifier)
				if unicode.IsUpper(r) && pkgTypes[qualifier] && callAdjacent {
					findings = append(findings, claimCommentFinding{
						axis: "existence", ident: ident, path: path, line: line,
						detail: fmt.Sprintf(
							"comment claims %s.%s is called (this file's own code has zero calls to %s, and %s IS declared in this file's own package %q -- see this file's other %s.%s references for what an EXTERNAL package's type would look like instead)",
							qualifier, ident, ident, qualifier, facts.pkg, qualifier, ident),
						snippet: claimCommentGuardSnippet(flat, mStart, mEnd),
					})
				}
				// else: qualifier names an external package's type, or a
				// same-package type mentioned with no "call"/"calls" word
				// nearby -- read as naming an existing API, not claiming a
				// local invocation.
				continue
			}

			// Bare (unqualified) identifiers are deliberately NOT flagged
			// by "call"/"calls" adjacency alone. Measured against the real
			// tree, not assumed: a first draft of this axis flagged this
			// exact rule, and it produced double-digit false positives in
			// claim_kinds_call_site_guard_test.go and store.go alone --
			// "the intake sweep's OrphanSweep call started out unscoped",
			// "one OrphanSweep call plus one SelectDue call", "a ClaimDue,
			// OrphanSweep, or SelectDue call OUTSIDE internal/outbox" --
			// every one of them a file whose own job is to DESCRIBE calls
			// that happen in OTHER files it scans, not to make them
			// itself. A bare "X call" is common, correct prose for that
			// shape of file and is indistinguishable, by text alone, from
			// a stale local-invocation claim; #0347's own amendment
			// records that this guard family has a structural limit, and
			// this is it. The qualified, same-package Type.Method form
			// above remains flagged because it names THIS package's own
			// type as the actor, which a file merely describing another
			// file's calls has no reason to do.
		}

		// --- directional axis ---
		for _, dm := range claimCommentGuardDirWordRe.FindAllStringSubmatchIndex(flat, -1) {
			// #0347's review (bounce 1, B1, part 1): use the DIRECTIONAL-
			// only marker set here, not the existence axis's broader
			// claimCommentGuardHistoricalRe -- see
			// claimCommentGuardDirHistoricalRe's own doc comment above for
			// why "never"/"cannot" in the broad set silently exempted both
			// real sites (#0342 sites A and C) this axis exists to catch.
			if claimCommentGuardDirHistoricalRe.MatchString(claimCommentGuardSentence(flat, dm[0], dm[1])) {
				continue // #0347 criterion 3's convention, sentence-scoped -- see the existence axis's identical fix, above
			}
			dir := flat[dm[2]:dm[3]]
			// #0363: claimCommentGuardDirWordRe carries (?i), so dir may be
			// capitalised ("Below"/"Above"). The resolution comparison below
			// must be case-insensitive or it can never be satisfied, and the
			// axis degenerates from "invisible to capitalised direction words"
			// (the bug this issue fixes) into "flags every capitalised
			// direction word unconditionally" -- a false positive on CORRECT
			// prose, which is strictly worse. Pinned by the resolution rows in
			// claimCommentGuardDirectionalCalibrationCases.
			dirLower := strings.ToLower(dir)
			// Window left edge -- see claimCommentGuardDirectionalWindowLo's
			// own doc comment (defined earlier in this file, right after
			// claimCommentGuardOpenParen) for the full account of the
			// 90-char cap, the boundary clamp, and the paren scoping this
			// combines. Extracted there (#0364) so
			// claimCommentGuardDiagnoseDirectional, near the calibration
			// table below, shares this exact computation rather than
			// holding a second, independently-drifting copy of it.
			lo := claimCommentGuardDirectionalWindowLo(flat, dm[0])
			window := flat[lo:dm[0]]
			// Check EVERY DISTINCT target identifier in the window, not
			// only the nearest. #0347's review (bounce 1, B1, part 2)
			// measured that nearest-only masks a real site: in #0342 site
			// A (mailKinds' doc comment in
			// internal/mailing/outbox_worker.go at commit db9bff7), the
			// nearest identifier to the direction word is OrphanSweep,
			// which DOES occur later in that same file -- so a
			// nearest-only check resolves and goes silent, while the
			// false claim in the SAME clause is about ClaimDue, which has
			// ZERO occurrences anywhere in that file. A nearer identifier
			// resolving true must not suppress a farther one in the same
			// window that doesn't.
			seen := map[string]bool{}
			for _, im := range claimCommentGuardIdentRe.FindAllString(window, -1) {
				if seen[im] {
					continue
				}
				seen[im] = true
				offs := facts.identOffsets[im]
				ok := false
				for _, o := range offs {
					if dirLower == "below" && o > endOff {
						ok = true
					}
					if dirLower == "above" && o < startOff {
						ok = true
					}
				}
				if !ok {
					findings = append(findings, claimCommentFinding{
						axis: "directional", ident: im, path: path, line: line,
						detail: fmt.Sprintf(
							"comment says %q is %s, but this file's code has no %s occurrence %s the comment (occurrences anywhere in file: %d)",
							im, dir, im, dir, len(offs)),
						snippet: claimCommentGuardSnippet(flat, dm[0], dm[1]),
					})
				}
			}
		}
	}
	return findings, nil
}

// claimCommentGuardMinPlausibleFileCount and its two siblings below are
// this guard's #0275-family floors (#0347 criterion 5 / CLAUDE.md §8): a
// broken walk, an emptied scan-roots list, or a method-name check that
// silently stopped matching must fail loudly rather than report an
// all-clear built on an empty population. Measured directly against
// claimCommentGuardScanRoots, not fitted -- TestNoCommentClaimsClaimMachineryCallFileCodeLacks,
// below, both enforces and re-measures these floors on every run, and
// issue #0347's ## Verification records the exact numbers observed for
// this session.
const claimCommentGuardMinPlausibleFileCount = 150

// claimCommentGuardMinPlausibleCallCensusCount is the floor on the SUM,
// across every file and every target identifier, of
// claimCommentGuardFileFacts.callCount -- i.e. real code calls to
// ClaimDue/SelectDue/OrphanSweep/ClaimRow/ClaimBatch found anywhere under
// claimCommentGuardScanRoots. If this ever reaches 0 the mode-0 census
// itself is broken (a parser regression, a renamed AST field) and every
// "file's own code has zero calls to X" finding this guard could report
// would be meaningless -- indistinguishable from "the census never
// worked". A floor here is what turns that into a loud failure instead of
// a guard that always agrees the code is silent.
const claimCommentGuardMinPlausibleCallCensusCount = 15

// claimCommentGuardMinPlausibleCommentGroupCount is the floor on how many
// comment groups the ParseComments walk actually visited. #0342's second
// review measured 4963 comment groups over its own (wider) scan; this
// guard's scan roots are the same trees, so a healthy run should clear
// this floor by two orders of magnitude. Set low deliberately (CLAUDE.md
// §8's #0300 lesson: a floor set close to today's population fails
// permanently on ordinary drift) -- its job is only to catch "the
// ParseComments walk visited nothing".
const claimCommentGuardMinPlausibleCommentGroupCount = 500

func claimCommentGuardFloorImplausible(guardName string, roots []string, got, floor int) string {
	if len(roots) == 0 {
		return fmt.Sprintf("%s: scan roots are empty -- this guard would silently check nothing (#0275)", guardName)
	}
	if got < floor {
		return fmt.Sprintf("%s: measured %d against a floor of %d under %v -- the walk or the census may have silently broken (#0275)", guardName, got, floor, roots)
	}
	return ""
}

// TestNoCommentClaimsClaimMachineryCallFileCodeLacks is #0347's guard
// proper (criteria 1, 2, 3, 5, 6 -- see this file's own top-of-file doc
// comment for the reasoning behind each) plus the directional axis the
// amendment added. It walks claimCommentGuardScanRoots exactly once,
// parsing each file twice (mode 0 for the census, ParseComments for the
// population), and fails naming every finding from both axes.
func TestNoCommentClaimsClaimMachineryCallFileCodeLacks(t *testing.T) {
	if len(claimCommentGuardScanRoots) == 0 {
		t.Fatal("claimCommentGuardScanRoots is empty -- this guard would silently check nothing (#0275)")
	}

	pkgTypes, typeIndexVisited := claimCommentGuardBuildPackageTypeIndex(t, claimCommentGuardScanRoots)
	if reason := claimCommentGuardFloorImplausible("TestNoCommentClaimsClaimMachineryCallFileCodeLacks (package-type index)", claimCommentGuardScanRoots, typeIndexVisited, claimCommentGuardMinPlausibleFileCount); reason != "" {
		t.Fatal(reason)
	}
	if len(pkgTypes) == 0 {
		t.Fatal("FAIL-CLOSED: package-type index is empty across a non-empty walk -- the type declaration scan is broken")
	}

	var findings []claimCommentFinding
	totalCallCensus := 0
	commentGroups := 0

	visited := walkClaimKindsGuardFiles(t, claimCommentGuardScanRoots, func(path string) {
		fs, err := findClaimCommentGuardFindings(path, nil, pkgTypes)
		if err != nil {
			t.Fatalf("scanning %s: %v", path, err)
		}
		findings = append(findings, fs...)

		// Independently accumulate the two population floors from a THIRD
		// parse pass (mode 0 for the call census, ParseComments count for
		// groups) rather than threading counters out of
		// findClaimCommentGuardFindings -- keeps the floor measurement
		// visibly independent of the function whose output it is meant to
		// bound (CLAUDE.md §8: an oracle must not share its method with
		// its subject).
		fset := token.NewFileSet()
		facts, err := claimCommentGuardBuildFileFacts(fset, path, nil)
		if err == nil {
			for _, c := range facts.callCount {
				totalCallCensus += c
			}
		}
		cfset := token.NewFileSet()
		if cf, err := parser.ParseFile(cfset, path, nil, parser.ParseComments); err == nil {
			commentGroups += len(cf.Comments)
		}
	})

	if reason := claimCommentGuardFloorImplausible("TestNoCommentClaimsClaimMachineryCallFileCodeLacks", claimCommentGuardScanRoots, visited, claimCommentGuardMinPlausibleFileCount); reason != "" {
		t.Fatal(reason)
	}
	if reason := claimCommentGuardFloorImplausible("TestNoCommentClaimsClaimMachineryCallFileCodeLacks (call census)", claimCommentGuardScanRoots, totalCallCensus, claimCommentGuardMinPlausibleCallCensusCount); reason != "" {
		t.Fatal(reason)
	}
	if reason := claimCommentGuardFloorImplausible("TestNoCommentClaimsClaimMachineryCallFileCodeLacks (comment groups)", claimCommentGuardScanRoots, commentGroups, claimCommentGuardMinPlausibleCommentGroupCount); reason != "" {
		t.Fatal(reason)
	}

	if len(findings) == 0 {
		return
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].path != findings[j].path {
			return findings[i].path < findings[j].path
		}
		return findings[i].line < findings[j].line
	})
	var b strings.Builder
	fmt.Fprintf(&b, "%d comment(s) claim a claim-machinery call or position their own file's code does not satisfy:\n", len(findings))
	for _, fdg := range findings {
		fmt.Fprintf(&b, "[%s] %s:%d %s\n    …%s…\n", fdg.axis, fdg.path, fdg.line, fdg.detail, fdg.snippet)
	}
	t.Fatal(b.String())
}

// TestNoClaimMachineryCallSyntaxLiteralInAnyComment is #0347 criterion 7 /
// #0323 enforced in Go, as a lexer-level check (go/scanner with
// ScanComments emits a COMMENT token for exactly the comment spans, so a
// string or raw literal that happens to contain "//" or these three
// substrings can never be mistaken for a comment -- the same reasoning
// #0342's third review's own scratchpad scanner tool used). This
// complements, and does not
// duplicate, scripts/go_file_visit_floor_guard_test.sh's external harness:
// that harness greps file TEXT (comments included) and is the guard
// against loosening or deleting the check itself (CLAUDE.md §8: a guard
// living in the file it guards is new mutable surface); this one runs
// in-package on every `go test` and is the guard against a comment
// introducing the literal in the first place, independent of whether the
// shell harness happens to be run in the same session.
func TestNoClaimMachineryCallSyntaxLiteralInAnyComment(t *testing.T) {
	if len(claimCommentGuardScanRoots) == 0 {
		t.Fatal("claimCommentGuardScanRoots is empty -- this guard would silently check nothing (#0275)")
	}
	nFiles, nComments := 0, 0
	var hits []string
	visited := walkClaimKindsGuardFiles(t, claimCommentGuardScanRoots, func(path string) {
		nFiles++
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		fset := token.NewFileSet()
		file := fset.AddFile(path, fset.Base(), len(src))
		var s scanner.Scanner
		s.Init(file, src, nil, scanner.ScanComments)
		for {
			pos, tok, lit := s.Scan()
			if tok == token.EOF {
				break
			}
			if tok != token.COMMENT {
				continue
			}
			nComments++
			for _, banned := range claimCommentGuardCallSyntaxLiterals {
				if strings.Contains(lit, banned) {
					hits = append(hits, fmt.Sprintf("%s: %q in comment: %s", fset.Position(pos), banned, strings.Join(strings.Fields(lit), " ")))
				}
			}
		}
	})
	if reason := claimCommentGuardFloorImplausible("TestNoClaimMachineryCallSyntaxLiteralInAnyComment", claimCommentGuardScanRoots, visited, claimCommentGuardMinPlausibleFileCount); reason != "" {
		t.Fatal(reason)
	}
	if nComments == 0 {
		t.Fatalf("FAIL-CLOSED: visited %d files but found zero comment tokens -- the scanner is not seeing comments", nFiles)
	}
	if len(hits) > 0 {
		t.Fatalf("%d call-syntax literal(s) found in Go comments (#0347 criterion 7 / #0323):\n%s", len(hits), strings.Join(hits, "\n"))
	}
}

// TestGoFileVisitFloorImplausibleGuardFiresOnEmptyOrLowCount is #0347
// criterion 5's direct proof (CLAUDE.md §8: separated from the two tests
// above so a synthetic failure does not require a real *testing.T standing
// in for one -- claimCommentGuardFloorImplausible returns "" or a reason,
// the same shape dangling_test_citation_guard_test.go's own
// goFileVisitCountImplausible uses, for the identical reason given there).
// The numbers here are chosen for THIS test alone, not copied from any
// guard's own floor constant (CLAUDE.md §8: an oracle must not be the same
// bytes as its subject) -- a change to
// claimCommentGuardMinPlausibleFileCount cannot make this test agree with
// itself regardless of whether claimCommentGuardFloorImplausible still
// works.
func TestGoFileVisitFloorImplausibleGuardFiresOnEmptyOrLowCount(t *testing.T) {
	if reason := claimCommentGuardFloorImplausible("Example", nil, 0, 150); reason == "" {
		t.Fatal("expected empty roots to be reported implausible, got no reason")
	}
	if reason := claimCommentGuardFloorImplausible("Example", []string{"../../cmd"}, 3, 150); reason == "" {
		t.Fatal("expected a count under the floor to be reported implausible, got no reason")
	}
	if reason := claimCommentGuardFloorImplausible("Example", []string{"..", "../../cmd"}, 150, 150); reason != "" {
		t.Fatalf("expected a count meeting the floor with non-empty roots to be plausible, got: %s", reason)
	}
}

// TestClaimCommentGuardCensusFloorCatchesRootsWithNoRealPopulation is
// #0347 criterion 5's END-TO-END proof, one step beyond the pure-function
// proof above: it runs the REAL walk-and-census pipeline
// (walkClaimKindsGuardFiles + claimCommentGuardBuildFileFacts, the exact
// calls TestNoCommentClaimsClaimMachineryCallFileCodeLacks makes) against
// internal/audit -- a real, unmodified package under this repo's tree,
// confirmed by a direct grep immediately above this comment's own review
// to contain zero mentions of any of the five target identifiers, in code
// or in comments. This is a roots NARROWING, not a mutation of any shared
// file (CLAUDE.md §8b): nothing in the tree is edited, and no other
// session's work is touched, matching claim_kinds_call_site_guard_test.go's
// own TestNonExemptFloorCatchesScanRootsNarrowedToSelf precedent in this
// same package.
//
// If this ever finds a real call or comment mention, that is a change
// worth noticing (internal/audit would have grown claim-machinery
// involvement) -- not a reason to raise the floor without looking.
func TestClaimCommentGuardCensusFloorCatchesRootsWithNoRealPopulation(t *testing.T) {
	narrowRoots := []string{"../audit"}
	totalCallCensus := 0
	commentGroups := 0
	visited := walkClaimKindsGuardFiles(t, narrowRoots, func(path string) {
		fset := token.NewFileSet()
		facts, err := claimCommentGuardBuildFileFacts(fset, path, nil)
		if err != nil {
			t.Fatalf("scanning %s: %v", path, err)
		}
		for _, c := range facts.callCount {
			totalCallCensus += c
		}
		cfset := token.NewFileSet()
		if cf, err := parser.ParseFile(cfset, path, nil, parser.ParseComments); err == nil {
			commentGroups += len(cf.Comments)
		}
	})
	if visited == 0 {
		t.Fatal("FAIL-CLOSED premise broken: internal/audit has no .go files to walk -- re-point this test at a different zero-population package")
	}
	if totalCallCensus != 0 {
		t.Fatalf("FAIL-CLOSED premise broken: internal/audit now has %d real claim-machinery call(s) -- re-verify with grep and re-point this test, or drop it if the class has spread there deliberately", totalCallCensus)
	}
	// The real pipeline, pointed at a real but claim-machinery-empty tree,
	// must be reported implausible against this guard's own real floor --
	// proving the floor actually gates the real measurement path, not only
	// the pure function in isolation.
	if reason := claimCommentGuardFloorImplausible("TestClaimCommentGuardCensusFloorCatchesRootsWithNoRealPopulation", narrowRoots, totalCallCensus, claimCommentGuardMinPlausibleCallCensusCount); reason == "" {
		t.Fatalf("expected a zero-call-census narrowing to internal/audit to be reported implausible against claimCommentGuardMinPlausibleCallCensusCount=%d, got no reason (call census=%d, comment groups=%d, files visited=%d)",
			claimCommentGuardMinPlausibleCallCensusCount, totalCallCensus, commentGroups, visited)
	}
}

// The fixtures below are #0347 criterion 4's falsifiability proof, built
// as in-memory reconstructions rather than a live `git show ffef8cf`
// checkout -- criterion 4 explicitly allows "the ffef8cf snapshot (or an
// equivalent reconstruction)", and an in-memory fixture is what
// findOutboxCallSitesInFile's own sibling test
// (TestClaimKindsGuardFiresOnFixtureWithNoKinds) already does in this
// package, for the same CLAUDE.md §8 reason: the guard's own detection
// logic gets proved, independent of whatever the tree currently contains.
//
// This session ALSO independently confirmed the fixture text matches the
// real ffef8cf/HEAD trees, out of band, before trusting it (CLAUDE.md §8b:
// the scratchpad -- and by the same caution, any inherited claim about
// what a historical commit contains -- is not to be trusted unverified):
//
//	git show ffef8cf:internal/mailing/outbox_worker_test.go | sed -n '743,749p'
//	git show HEAD:internal/mailing/outbox_worker_test.go   | sed -n '742,750p'
//
// confirmed the pre-fix text reads "...picked up by ANY later
// OutboxWorker.ClaimDue call sharing this database..." and the current
// tree reads "...picked up by ANY later OutboxWorker pass sharing this
// database..." -- exactly what the two fixtures below reproduce.

const claimCommentGuardStaleFixtureSrc = `package mailing

// OutboxWorker is this file's own worker type -- the fixture's stand-in
// for internal/mailing/outbox_worker.go's real declaration.
type OutboxWorker struct{}

// TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued reproduces the exact
// pre-fix comment from ffef8cf's outbox_worker_test.go:743-749, byte-for-byte
// (git show ffef8cf:internal/mailing/outbox_worker_test.go).
func TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued() {
	// The whole point of this test is that id2 is deliberately left
	// 'queued' by Stop's release. A 'queued', due-now confirmation row
	// left behind after this test returns is picked up by ANY later
	// OutboxWorker.ClaimDue call sharing this database — including other
	// tests in this same package/run — so it must be removed explicitly
	// rather than relying on ending in a terminal status the way every
	// other test in this file does.
}
`

const claimCommentGuardFixedFixtureSrc = `package mailing

// OutboxWorker is this file's own worker type -- the fixture's stand-in
// for internal/mailing/outbox_worker.go's real declaration.
type OutboxWorker struct{}

// TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued reproduces the exact
// post-fix comment from HEAD's outbox_worker_test.go, byte-for-byte
// (git show HEAD:internal/mailing/outbox_worker_test.go).
func TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued() {
	// The whole point of this test is that id2 is deliberately left
	// 'queued': pass's stopCh check runs before its ClaimRow, so id2 is
	// never claimed at all (#0297 — see this test's own doc comment
	// above, and the post-Stop assertions below). A 'queued', due-now
	// confirmation row left behind after this test returns is picked up
	// by ANY later OutboxWorker pass sharing this database — including
	// other tests in this same package/run — so it must be removed
	// explicitly rather than relying on ending in a terminal status the
	// way every other test in this file does.
}
`

// TestClaimCommentGuardFlagsKnownStaleSiteFromFfef8cf is #0347 criterion
// 4's proof: pointed at a reconstruction of the ffef8cf snapshot, the
// existence axis flags the t.Cleanup preamble comment in
// TestOutboxWorker_Stop_LeavesUnclaimedRowsQueued's "OutboxWorker.ClaimDue
// call"; pointed at the fixed tree's actual current wording, it does not.
func TestClaimCommentGuardFlagsKnownStaleSiteFromFfef8cf(t *testing.T) {
	pkgTypes := map[string]map[string]bool{"mailing": {"OutboxWorker": true}}

	staleFindings, err := findClaimCommentGuardFindings("outbox_worker_test.go", claimCommentGuardStaleFixtureSrc, pkgTypes)
	if err != nil {
		t.Fatalf("scanning stale fixture: %v", err)
	}
	found := false
	for _, f := range staleFindings {
		if f.axis == "existence" && f.ident == "ClaimDue" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the pre-fix (ffef8cf) fixture's \"OutboxWorker.ClaimDue call\" to be flagged by the existence axis; findings: %+v", staleFindings)
	}

	fixedFindings, err := findClaimCommentGuardFindings("outbox_worker_test.go", claimCommentGuardFixedFixtureSrc, pkgTypes)
	if err != nil {
		t.Fatalf("scanning fixed fixture: %v", err)
	}
	for _, f := range fixedFindings {
		if f.ident == "ClaimDue" || f.ident == "ClaimRow" {
			t.Fatalf("expected the post-fix fixture (HEAD's real wording, \"OutboxWorker pass\") to be clean; got: %+v", f)
		}
	}
}

// The two fixtures below are #0347's review (bounce 1, B1, part 4): a
// standing directional-axis falsifiability regression, mirroring
// TestClaimCommentGuardFlagsKnownStaleSiteFromFfef8cf above but for the
// DIRECTIONAL axis specifically, which had NO falsifiability proof at all
// before this bounce -- exactly how it reached zero-findings-on-HEAD
// without anyone noticing it detected almost nothing (that review's own
// words). Built from #0342 sites A and C
// (site A is mailKinds' doc comment, site C is the inline comment
// preceding the OrphanSweep call in OutboxWorker.pass, both in
// internal/mailing/outbox_worker.go at commit db9bff7 -- the two real
// pre-fix defects the directional axis was added to catch), extracted via
// `git show db9bff7:internal/mailing/outbox_worker.go` and the real HEAD
// file, not retyped -- CLAUDE.md §8's backslash-escape gotcha generalizes
// to "don't retype punctuation a human wrote, either", the same discipline
// the ffef8cf fixtures above already follow, including the real U+2014 em
// dash both sites' comments contain.
//
// Both site comments name two other target identifiers alongside the false
// ClaimDue claim. Each fixture's own function body, defined next in this
// file, deliberately DOES call the two OTHER identifiers those comments
// name -- reproducing the exact shape that let ClaimDue's absence hide
// behind a nearer, correctly-resolving identifier before this bounce's
// part-2 fix (check every identifier in the window, not only the
// nearest).
//
// The calls below are deliberately BARE package-level functions, not
// methods called through a receiver, on purpose: findClaimCommentGuardFileFacts
// registers any *ast.Ident matching a target name regardless of call
// shape, so a bare call proves the census identically -- but, unlike a
// receiver-qualified call, it never places a period directly before the
// identifier name and an open paren directly after, which is the exact
// three-part shape claimCommentGuardCallSyntaxLiterals bans and
// scripts/go_file_visit_floor_guard_test.sh's external, text-based
// population oracle also matches on. That harness cannot distinguish a
// real call from fixture text (#0323's own accepted-inflation precedent,
// restated in claimCommentGuardCallSyntaxLiterals' own doc comment above),
// so writing these calls in the qualified shape would needlessly re-inflate
// the SAME population #0347's B2 fix just brought back down -- avoidable
// here, unlike that sibling guard's own raw-string fixtures, which must
// use the qualified shape for an unrelated reason (matching a real
// production call SHAPE) at deliberate cost.

const claimCommentGuardDirectionalStaleFixtureSrc = `package mailing

// mailKinds is every outbox.Kind this worker's render switch knows how to
// build a message for — i.e. every Kind EXCEPT outbox.KindSubscribeIntake
// (#0254), which is not an email and is claimed separately by
// internal/handlers.SubscribeHandler's own recovery poller. Passed to
// outbox.Store.ClaimDue's and OrphanSweep's kinds filters in pass, below,
// so this worker never claims OR sweeps a row it cannot render.
var mailKinds = []int{}

func OrphanSweep(staleAfter int, kinds []int) (int, error) {
	return 0, nil
}

type OutboxWorker struct{}

func (w *OutboxWorker) pass() (bool, error) {
	// #0254's review bounce: scoped to mailKinds, the same set ClaimDue
	// below is scoped to, so this sweep can never release a live claim
	// belonging to internal/handlers.SubscribeHandler's own recovery
	// poller (KindSubscribeIntake) — see OrphanSweep's doc comment for the
	// duplicate-send chain an unfiltered sweep produced.
	swept, err := OrphanSweep(0, mailKinds)
	_ = swept
	_ = err
	return false, nil
}
`

const claimCommentGuardDirectionalFixedFixtureSrc = `package mailing

// mailKinds is every outbox.Kind this worker's render switch knows how to
// build a message for — i.e. every Kind EXCEPT outbox.KindSubscribeIntake
// (#0254), which is not an email and is claimed separately by
// internal/handlers.SubscribeHandler's own recovery poller. Passed to
// outbox.Store.SelectDue's and OrphanSweep's kinds filters in pass, below
// — #0254 scoped ClaimDue, which this worker has not called since #0297;
// ClaimRow takes no kinds and only ever claims an id SelectDue already
// filtered — so this worker never selects OR sweeps a row it cannot
// render.
var mailKinds = []int{}

func SelectDue(kinds []int) ([]int, error) {
	return nil, nil
}

func OrphanSweep(staleAfter int, kinds []int) (int, error) {
	return 0, nil
}

type OutboxWorker struct{}

func (w *OutboxWorker) pass() (bool, error) {
	// #0254's review bounce: scoped to mailKinds, the same set SelectDue
	// below is scoped to (#0254 scoped ClaimDue; this pass has not called
	// it since #0297), so this sweep can never release a live claim
	// belonging to internal/handlers.SubscribeHandler's own recovery
	// poller (KindSubscribeIntake) — see OrphanSweep's doc comment for the
	// duplicate-send chain an unfiltered sweep produced.
	ids, err := SelectDue(mailKinds)
	_ = ids
	_ = err
	swept, err2 := OrphanSweep(0, mailKinds)
	_ = swept
	_ = err2
	return false, nil
}
`

// TestClaimCommentGuardDirectionalAxisFlagsKnownStaleSitesFromDb9bff7 is
// #0347's review (bounce 1, B1, part 4)'s proof: pointed at a
// reconstruction of #0342 sites A and C (db9bff7's real wording), the
// directional axis flags ClaimDue at both; pointed at the fixed tree's
// actual current wording (HEAD's real SelectDue-based rewrite), it flags
// nothing.
func TestClaimCommentGuardDirectionalAxisFlagsKnownStaleSitesFromDb9bff7(t *testing.T) {
	pkgTypes := map[string]map[string]bool{"mailing": {"OutboxWorker": true}}

	staleFindings, err := findClaimCommentGuardFindings("outbox_worker.go", claimCommentGuardDirectionalStaleFixtureSrc, pkgTypes)
	if err != nil {
		t.Fatalf("scanning stale directional fixture: %v", err)
	}
	claimDueDirectional := 0
	for _, f := range staleFindings {
		if f.axis == "directional" && f.ident == "ClaimDue" {
			claimDueDirectional++
		}
	}
	if claimDueDirectional < 2 {
		t.Fatalf("expected the pre-fix (db9bff7) fixture to produce at least 2 directional ClaimDue findings (one from site A's \"below\" claim, one from site C's), got %d; findings: %+v", claimDueDirectional, staleFindings)
	}

	fixedFindings, err := findClaimCommentGuardFindings("outbox_worker.go", claimCommentGuardDirectionalFixedFixtureSrc, pkgTypes)
	if err != nil {
		t.Fatalf("scanning fixed directional fixture: %v", err)
	}
	for _, f := range fixedFindings {
		t.Fatalf("expected the post-fix fixture (HEAD's real wording) to be entirely clean; got: %+v", f)
	}
}

// claimCommentGuardDirectionalReason is #0364's discriminant: WHY a
// calibration row gets the ClaimDue count it gets, not merely what that
// count is. A row asserting wantClaimDueFindings alone cannot show whether
// it is silent (or flagged) for the reason its own comment names --
// #0358 shipped a row pinning nothing this way (its comment named the
// backwards-only window; its real cause, at the time, was that the
// direction-word regex never matched a capitalised "Below" at all, so the
// axis was never entered), and #0363 shipped the mirror image (a row
// pinned only entry, blind to whether the axis then RESOLVED correctly --
// #0363's own shipped bug flagged every capitalised direction word
// unconditionally, a false positive that row could not see). Both were
// invisible to a table comparing only counts.
//
// dirWordMatched, dirWord, markerExempt, and identInWindow are RE-DERIVED
// from the axis's own shared primitives -- claimCommentGuardDirWordRe,
// claimCommentGuardDirHistoricalRe, claimCommentGuardSentence,
// claimCommentGuardDirectionalWindowLo, claimCommentGuardIdentRe -- the
// same vars and functions findClaimCommentGuardFindings itself calls when
// it makes its own decision, not a second, independently-drifting copy of
// it (CLAUDE.md §8: a copy of the answer stored next to the question is
// not a check). resolved is deliberately NOT computed by re-implementing
// the "below"/"above" resolution comparison a third time -- that exact
// comparison's case-sensitivity trap is #0363's whole subject, and a
// hand-written duplicate of it here would inherit the identical risk of
// silently drifting from whatever the real comparison does. Instead
// resolved asks findClaimCommentGuardFindings itself, the REAL
// computation, whether a directional ClaimDue finding actually resulted --
// so a mutation to the real comparison is observed through its real
// output, not missed by a stale copy standing in for it.
type claimCommentGuardDirectionalReason struct {
	dirWordMatched bool
	dirWord        string
	markerExempt   bool
	identInWindow  bool
	resolved       bool // only meaningful when identInWindow is true
}

func claimCommentGuardReasonNoDirectionWordMatch() claimCommentGuardDirectionalReason {
	return claimCommentGuardDirectionalReason{}
}

func claimCommentGuardReasonMarkerExempt(dirWord string) claimCommentGuardDirectionalReason {
	return claimCommentGuardDirectionalReason{dirWordMatched: true, dirWord: dirWord, markerExempt: true}
}

func claimCommentGuardReasonWindowExcludes(dirWord string) claimCommentGuardDirectionalReason {
	return claimCommentGuardDirectionalReason{dirWordMatched: true, dirWord: dirWord}
}

func claimCommentGuardReasonResolved(dirWord string) claimCommentGuardDirectionalReason {
	return claimCommentGuardDirectionalReason{dirWordMatched: true, dirWord: dirWord, identInWindow: true, resolved: true}
}

func claimCommentGuardReasonUnresolved(dirWord string) claimCommentGuardDirectionalReason {
	return claimCommentGuardDirectionalReason{dirWordMatched: true, dirWord: dirWord, identInWindow: true, resolved: false}
}

// claimCommentGuardDiagnoseDirectional re-derives WHY the directional axis
// produces the count it does for ident in src, for
// TestClaimCommentGuardDirectionalAxisCalibrationTable to check against
// each row's declared claimCommentGuardDirectionalReason (#0364). It
// assumes exactly one non-empty comment group and exactly one
// direction-word match within it -- every calibration fixture below is
// deliberately written to that shape, matching what each row's own count
// assertion already assumes -- and fails loudly, rather than guessing,
// if a fixture doesn't hold to it.
//
// ident is a parameter, not a hardcoded "ClaimDue", since #0366: the
// calibration table's own count assertion counted only
// f.ident == "ClaimDue" findings, and this diagnostic INHERITED that scope
// by hardcoding the same literal in its own window check and findings
// filter -- so the one row whose stated purpose is a SECOND identifier
// (every-distinct-identifier's OrphanSweep) had no diagnostic covering it.
// Parameterising here is the fix: every existing call site names
// "ClaimDue" explicitly now, and the every-distinct-identifier row's new
// secondIdent pair (below) is the one call site that names "OrphanSweep"
// instead. This is a change to the DIAGNOSTIC's argument list, not to the
// axis it diagnoses -- claimCommentGuardDirWordRe, claimCommentGuardDirHistoricalRe,
// claimCommentGuardDirectionalWindowLo, claimCommentGuardIdentRe, and
// findClaimCommentGuardFindings itself are all untouched, so #0358
// criterion 4's freeze on window logic, marker set, clamp, and
// every-identifier behaviour holds.
func claimCommentGuardDiagnoseDirectional(src string, ident string) (claimCommentGuardDirectionalReason, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "diagnose.go", src, parser.ParseComments)
	if err != nil {
		return claimCommentGuardDirectionalReason{}, fmt.Errorf("parsing fixture: %w", err)
	}
	var flat string
	nonEmpty := 0
	for _, cg := range f.Comments {
		text := cg.Text()
		if text == "" {
			continue
		}
		nonEmpty++
		flat = claimCommentGuardFlattenComment(text)
	}
	if nonEmpty != 1 {
		return claimCommentGuardDirectionalReason{}, fmt.Errorf("fixture has %d non-empty comment group(s); this diagnostic assumes exactly one", nonEmpty)
	}

	matches := claimCommentGuardDirWordRe.FindAllStringSubmatchIndex(flat, -1)
	if len(matches) == 0 {
		return claimCommentGuardReasonNoDirectionWordMatch(), nil
	}
	if len(matches) != 1 {
		return claimCommentGuardDirectionalReason{}, fmt.Errorf("fixture has %d direction-word matches; this diagnostic assumes exactly one", len(matches))
	}
	dm := matches[0]
	dirWord := strings.ToLower(flat[dm[2]:dm[3]])

	if claimCommentGuardDirHistoricalRe.MatchString(claimCommentGuardSentence(flat, dm[0], dm[1])) {
		return claimCommentGuardReasonMarkerExempt(dirWord), nil
	}

	lo := claimCommentGuardDirectionalWindowLo(flat, dm[0])
	window := flat[lo:dm[0]]
	identInWindow := false
	for _, im := range claimCommentGuardIdentRe.FindAllString(window, -1) {
		if im == ident {
			identInWindow = true
		}
	}
	if !identInWindow {
		return claimCommentGuardReasonWindowExcludes(dirWord), nil
	}

	findings, err := findClaimCommentGuardFindings("diagnose.go", src, map[string]map[string]bool{})
	if err != nil {
		return claimCommentGuardDirectionalReason{}, fmt.Errorf("scanning fixture: %w", err)
	}
	for _, fdg := range findings {
		if fdg.axis == "directional" && fdg.ident == ident {
			return claimCommentGuardReasonUnresolved(dirWord), nil
		}
	}
	return claimCommentGuardReasonResolved(dirWord), nil
}

// #0358's calibration table (job 1: pin the trade surfaces; job 2: retire
// the prose list above in favour of this table -- see "What this guard
// cannot do" above for the reasoning).
//
// claimCommentGuardDirectionalCalibrationCase is one measured trade
// surface of the directional axis. Every fixture's ClaimDue mentions are
// genuinely FALSE -- none of these source strings ever CALLS or even
// mentions ClaimDue in code, only in the comment under test -- so
// wantClaimDueFindings == 0 always means "silently exempt" (a false
// negative this calibration accepts as the price of closing a real false
// positive elsewhere), never "correctly recognised as true", and
// wantClaimDueFindings == 1 always means "flagged" (a false positive this
// calibration accepts, or the real defect this axis exists to catch).
//
// wantReason is #0364's addition: each row also declares WHICH of the
// axis's mechanisms produces that count -- the direction word never
// matched at all, the sentence's marker exempted it, the identifier fell
// outside the computed window, or the identifier was in the window and
// either resolved (silent) or did not (flagged). See
// claimCommentGuardDirectionalReason's own doc comment for why this is
// re-derived from the axis's shared primitives rather than a second,
// driftable copy of the answer.
//
// #0366 DECISION (criterion 1): this table's count and reason assertions
// scope to a SINGLE named identifier, "ClaimDue", by construction --
// wantClaimDueFindings and wantReason above, and
// claimCommentGuardDiagnoseDirectional before #0366, both hardcoded it.
// #0364's review measured the consequence: forcing every NON-ClaimDue
// identifier in the window to flag unconditionally leaves this entire
// table green, including the every-distinct-identifier row below, whose
// STATED purpose is that a nearer, resolving OrphanSweep must not mask a
// farther, unresolved ClaimDue -- a purpose this table asserted only one
// half of. Verified by re-running that exact mutation (issue #0366's
// ## Verification records the throwaway-worktree run): the table's own
// go test output stayed 100% green under it, confirming the hole was real,
// though `go test ./internal/outbox/...` as a whole was NOT green. Three
// other tests in this package fail under that mutation, and #0366's review
// measured that they are NOT all of one kind:
//
// TestNoCommentClaimsClaimMachineryCallFileCodeLacks (the whole-tree scan,
// which does not filter by identifier) and
// TestClaimCommentGuardDirectionalAxisIsCleanOnStoreGoParenScoping both
// read real files from disk, so their catch is contingent on what those
// files' comments happen to say today, and an unrelated comment edit could
// silently remove it.
//
// TestClaimCommentGuardDirectionalAxisFlagsKnownStaleSitesFromDb9bff7 does
// not. It hands claimCommentGuardDirectionalStaleFixtureSrc and
// claimCommentGuardDirectionalFixedFixtureSrc to findClaimCommentGuardFindings
// as src, and go/parser never opens the named path when src is non-nil, so
// that test reads no file at all -- it is a frozen db9bff7 snapshot held in
// two consts in THIS file, as durable as a calibration row. Its "the fixed
// fixture must be entirely clean" assertion is identifier-agnostic, which
// is why it catches this mutation.
//
// So this table was not the only durable instrument that would have noticed
// the mutation. It was the only place THIS ROW's own stated trade -- a
// nearer, resolving OrphanSweep against a farther, unresolved ClaimDue --
// was pinned at all, and that, not fragility of the other catchers, is the
// gap option 1 closes (see the "growing floor, not an inventory" framing in
// TestClaimCommentGuardDirectionalAxisCalibrationTable's own doc comment,
// below).
//
// CHOSE OPTION 1 (add a second, narrowly-scoped pair): the fix is a single
// row's addition (secondIdent/wantSecondIdentFindings/wantSecondIdentReason
// below) plus parameterising claimCommentGuardDiagnoseDirectional's
// hardcoded ident (its own doc comment records that change) -- neither
// touches the axis itself (#0358 criterion 4's freeze holds: no window
// logic, marker set, clamp, or every-identifier change), and the pair is
// proved to discriminate by the SAME mutation that found the hole (#0366's
// ## Verification). Given that low a cost against a real, already-measured
// gap on the one row whose whole point is a second identifier, extending
// the row is more direct than a doc-comment-only "record and stop" (the
// #0330/#0347/#0356 precedent this issue considered, and could equally have
// chosen) -- that precedent fits a gap that is expensive or architecturally
// awkward to close; this one was neither. Every OTHER row in this table
// still asserts only its ClaimDue half by design -- generalising every row
// to check every mentioned identifier is explicitly NOT what this decision
// does, and no other row gained a secondIdent pair.
type claimCommentGuardDirectionalCalibrationCase struct {
	name                 string
	src                  string
	wantClaimDueFindings int
	wantReason           claimCommentGuardDirectionalReason

	// secondIdent, wantSecondIdentFindings, and wantSecondIdentReason are
	// #0366's addition: when secondIdent is non-empty, the test loop below
	// ALSO asserts a count and a re-derived reason for that identifier,
	// exactly as it does for ClaimDue -- the second half of a trade this
	// table otherwise pins only one half of. Every row except
	// every-distinct-identifier leaves secondIdent "" and this second check
	// is skipped entirely; that row is the only one whose stated purpose is
	// a second, distinct identifier's resolution.
	//
	// #0375 (recorded, not closed -- see #0374's doc comment above for why a
	// seventh in-file check is not the default answer here either):
	// wantSecondIdentFindings and wantSecondIdentReason are ONLY asserted
	// when secondIdent is non-empty. A future row that sets
	// wantSecondIdentFindings (and/or wantSecondIdentReason) while leaving
	// secondIdent "" asserts NOTHING: the `if c.secondIdent != ""` guard in
	// TestClaimCommentGuardDirectionalAxisCalibrationTable, below, skips the
	// whole block, both fields go unread, and the row PASSES regardless of
	// what they say. Measured, not assumed: a throwaway row with
	// secondIdent: "" and wantSecondIdentFindings: 1 passes cleanly next to
	// the 24 real rows. Latent today, not live -- no such row exists, and
	// the one row that does set secondIdent has both of its assertions live
	// (each independently fails under its own targeted mutation). If you add
	// a row with a secondIdent pair, set secondIdent FIRST: an empty
	// secondIdent here is not "no claim about a second identifier", it is a
	// claim that silently goes unchecked.
	secondIdent             string
	wantSecondIdentFindings int
	wantSecondIdentReason   claimCommentGuardDirectionalReason
}

var claimCommentGuardDirectionalCalibrationCases = []claimCommentGuardDirectionalCalibrationCase{
	// FALSE NEGATIVE -- the direction word sits inside a parenthetical the
	// identifier is outside of. TRADE: pinned against the OPPOSING error it
	// exists to prevent -- without claimCommentGuardOpenParen's scoping,
	// internal/outbox/store.go's ClaimRow doc comment produces a false
	// positive (see TestClaimCommentGuardDirectionalAxisIsCleanOnStoreGoParenScoping,
	// below, which pins that side of the SAME trade against the real
	// file). #0347's review (bounce 1, B1 part 3) measured both sides.
	{
		name:                 "paren-scoping false negative, trades against store.go ClaimRow's false positive",
		src:                  "package outbox\n\n// ClaimDue's kinds filter (applied in pass, below) so this pass narrows what gets claimed.\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonWindowExcludes("below"),
	},
	// FALSE NEGATIVE -- the identifier is named AFTER the direction word.
	// TRADE: the window only ever looks BACKWARDS from a direction word
	// (flat[lo:dm[0]]), by construction -- looking forward too would
	// require its own width calibration, symmetric to the 90-char limit
	// below, for a shape none of #0342's or #0347's reviews ever measured
	// as a real defect.
	{
		name:                 "identifier-after-direction-word false negative",
		src:                  "package outbox\n\n// The row is claimed below by ClaimDue and marked sending.\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonWindowExcludes("below"),
	},
	// REGRESSION PIN (#0363) -- claimCommentGuardDirWordRe used to be
	// case-SENSITIVE, so a capitalised, sentence-initial direction word
	// ("Below,"/"Above," -- the natural way to write a forward reference at
	// the start of a Go sentence) was invisible to the axis entirely and its
	// window logic was never reached. #0358's review measured this as a
	// non-deliberate gap (unlike every other row in this table, it buys
	// nothing against an opposing error) and recorded it rather than fixing
	// it, per that issue's criterion 4 freeze on the window logic; #0363
	// added the `(?i)` flag to fix it. This pair now pins the FIXED
	// behaviour: both rows carry the identical sentence and both must flag,
	// so the pair specifically pins case-insensitivity and nothing else.
	// Proved by mutation (#0363): reverting claimCommentGuardDirWordRe to
	// `\b(below|above)\b` (dropping `(?i)`) makes ONLY the second row below
	// fail, want 1 got 0 -- nothing else in this table, the db9bff7 sites
	// test, or the rest of the package moves.
	{
		name:                 "case-sensitivity control: lowercase direction word flags",
		src:                  "package outbox\n\n// ClaimDue claims the row below.\n",
		wantClaimDueFindings: 1,
		wantReason:           claimCommentGuardReasonUnresolved("below"),
	},
	{
		name:                 "case-insensitivity regression pin: capitalised direction word flags too",
		src:                  "package outbox\n\n// ClaimDue claims the row Below.\n",
		wantClaimDueFindings: 1,
		wantReason:           claimCommentGuardReasonUnresolved("below"),
	},
	// RESOLUTION PIN (#0363, second bounce) -- the pair above proves only
	// that a capitalised direction word ENTERS the axis; both fixtures carry
	// no code, so facts.identOffsets["ClaimDue"] is empty and ok stays false
	// on either the case-sensitive or case-insensitive comparison path. That
	// leaves the pair blind to a second, opposite-sign bug the first #0363
	// pass introduced: `dir == "below"`/`dir == "above"` stayed
	// case-sensitive after `(?i)` was added to the regex, so a capitalised
	// "Below"/"Above" could never satisfy either comparison and the axis
	// flagged EVERY target identifier in the window unconditionally --
	// including on comments that are correct. These three rows carry real
	// code, so they pin RESOLUTION, not merely entry: a lowercase control
	// and one pin per comparison line (the "below" and "above" branches are
	// separate lines, so a partial fix lowercasing only one would still pass
	// a single combined row).
	{
		name:                 "case-insensitivity resolution control: lowercase direction word resolves against a real declaration below",
		src:                  "package outbox\n\n// ClaimDue claims the row below.\nfunc ClaimDue() {}\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonResolved("below"),
	},
	{
		name:                 "case-insensitivity resolution pin: capitalised direction word must RESOLVE, not merely enter the axis",
		src:                  "package outbox\n\n// ClaimDue claims the row Below.\nfunc ClaimDue() {}\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonResolved("below"),
	},
	{
		name:                 "case-insensitivity resolution pin: capitalised Above must resolve too -- the other, separately-written comparison",
		src:                  "package outbox\n\nfunc ClaimDue() {}\n\n// ClaimDue is declared Above.\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonResolved("above"),
	},
	// FALSE NEGATIVE -- a clause boundary separates the identifier from the
	// direction word. TRADE: the boundary set (claimCommentGuardBoundaryRunes
	// plus ASCII "--") is what stops an EARLIER, unrelated clause's
	// identifier from leaking into a LATER direction word's window (see
	// claimCommentGuardLastBoundary's own doc comment for the real
	// outbox_worker_test.go reproduction) -- the same boundary, applied in
	// the other temporal direction, also cuts off a genuinely related
	// identifier that happens to sit just before one. Three punctuation
	// forms, since this codebase uses all three as clause separators.
	{
		name:                 "clause-boundary false negative (em dash)",
		src:                  "package outbox\n\n// ClaimDue narrows the kinds filter — the row is processed below.\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonWindowExcludes("below"),
	},
	{
		name:                 "clause-boundary false negative (colon)",
		src:                  "package outbox\n\n// ClaimDue narrows the kinds filter: the row is processed below.\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonWindowExcludes("below"),
	},
	{
		name:                 "clause-boundary false negative (ASCII double hyphen)",
		src:                  "package outbox\n\n// ClaimDue narrows the kinds filter -- the row is processed below.\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonWindowExcludes("below"),
	},
	// FALSE NEGATIVE -- more than 90 characters separate the identifier
	// from the direction word, with no boundary between them. TRADE: the
	// 90-char cap is what stops the window from reading arbitrarily far
	// back through an entire multi-sentence comment group looking for a
	// direction word's true referent; #0347's reviews never measured a
	// real defect wider than this, and widening the cap re-opens the
	// existence axis's own "whole group" over-exemption class one level
	// down (see claimCommentGuardSentence's own doc comment).
	{
		name:                 ">90-characters false negative, no boundary",
		src:                  "package outbox\n\n// ClaimDue determines which due rows this particular render pass considers eligible before the sweep that runs immediately below.\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonWindowExcludes("below"),
	},
	// FALSE NEGATIVE -- the marker-exemption surface (#0347's third
	// review). TRADE: claimCommentGuardDirHistoricalRe's four
	// non-modal markers ("rather than", "instead of", "would", "has not
	// called") exempt a genuinely stale directional claim whenever one
	// appears in the SAME SENTENCE as the direction word -- but dropping
	// them (as #0347's second bounce briefly did) fails correct prose
	// internal/mailing/outbox_worker.go writes on HEAD today. The control
	// row below carries the identical false claim with NO marker, so it
	// stays flagged -- proving the exemption is about the marker, not
	// about the sentence shape.
	{
		name:                 "marker-exemption control (no marker, same false claim, stays flagged)",
		src:                  "package outbox\n\n// So ClaimDue below claims the row.\n",
		wantClaimDueFindings: 1,
		wantReason:           claimCommentGuardReasonUnresolved("below"),
	},
	{
		name:                 "marker-exemption: rather than",
		src:                  "package outbox\n\n// ...rather than a bare loop, so ClaimDue below claims the row.\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonMarkerExempt("below"),
	},
	{
		name:                 "marker-exemption: instead of",
		src:                  "package outbox\n\n// ...instead of a batch, and ClaimDue below marks it sending.\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonMarkerExempt("below"),
	},
	{
		name:                 "marker-exemption: would",
		src:                  "package outbox\n\n// A retry would be wasteful here, so ClaimDue below claims the row...\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonMarkerExempt("below"),
	},
	{
		name:                 "marker-exemption: has not called",
		src:                  "package outbox\n\n// The intake has not called the sweep yet, so ClaimDue below is what claims...\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonMarkerExempt("below"),
	},
	// FALSE POSITIVE -- non-positional "above"/"below". TRADE: the axis
	// cannot tell a positional direction word from an ordinary spatial or
	// numeric one ("the table below", "stays below eight attempts") --
	// disambiguating that would require actual sentence parsing, which
	// this guard deliberately does not attempt (see the top-of-file doc
	// comment's "what this guard cannot do"). This is the accepted
	// direction for the error to run: a build break is loud and
	// rephrasable, unlike a silent miss.
	{
		name:                 "false positive: non-positional \"below\" (table)",
		src:                  "package outbox\n\n// ClaimDue semantics matter here, and the table below explains why.\n",
		wantClaimDueFindings: 1,
		wantReason:           claimCommentGuardReasonUnresolved("below"),
	},
	{
		name:                 "false positive: non-positional \"below\" (numeric)",
		src:                  "package outbox\n\n// ClaimDue's retry budget stays below eight attempts.\n",
		wantClaimDueFindings: 1,
		wantReason:           claimCommentGuardReasonUnresolved("below"),
	},
	// FALSE POSITIVE -- a genuinely historical comment whose ONLY marker is
	// "never" or "cannot". TRADE: those two words are dropped from
	// claimCommentGuardDirHistoricalRe (unlike the broader
	// claimCommentGuardHistoricalRe the existence axis uses) because
	// they're ordinary present-tense behavioural modals in directional
	// prose ("X below never does Y") -- which is exactly how #0342 sites A
	// and C survived under the broader marker set (see
	// claimCommentGuardDirHistoricalRe's own doc comment). No comment of
	// this exact shape exists in the tree today; the house convention of
	// writing "since #NNNN" alongside "never"/"cannot" rescues it, as the
	// fourth row here shows.
	{
		name:                 "false positive: historical comment, only marker is \"never\"",
		src:                  "package outbox\n\n// ClaimDue below was never used by this worker.\n",
		wantClaimDueFindings: 1,
		wantReason:           claimCommentGuardReasonUnresolved("below"),
	},
	{
		name:                 "false positive: historical comment, \"never\" plus an unrelated #-reference",
		src:                  "package outbox\n\n// This worker never called ClaimDue below; #0297 removed that path entirely.\n",
		wantClaimDueFindings: 1,
		wantReason:           claimCommentGuardReasonUnresolved("below"),
	},
	{
		name:                 "false positive: historical comment, only marker is \"cannot\"",
		src:                  "package outbox\n\n// ClaimDue below cannot be reached from here any more.\n",
		wantClaimDueFindings: 1,
		wantReason:           claimCommentGuardReasonUnresolved("below"),
	},
	{
		name:                 "rescued: \"never\" alongside \"since #\" is exempt after all",
		src:                  "package outbox\n\n// This worker has never called ClaimDue below since #0297.\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonMarkerExempt("below"),
	},
	// TRADE (not a false negative -- the one shape where checking every
	// identifier is load-bearing): a nearer identifier that RESOLVES must
	// not suppress a farther one in the same window that does not. #0347's
	// review (bounce 1, B1 part 2) measured that nearest-only masks #0342
	// site A, where OrphanSweep sits nearer the direction word and does
	// occur later in that file, while the false claim is about ClaimDue,
	// which occurs nowhere in it. Pinned here in-table as well as by
	// TestClaimCommentGuardDirectionalAxisFlagsKnownStaleSitesFromDb9bff7,
	// which pins it against the real db9bff7 file.
	//
	// secondIdent (#0366): the row's own name says a NEARER, RESOLVING
	// identifier must not mask a farther, false one -- until #0366 nothing
	// here checked the "resolving" half; only ClaimDue's count and reason
	// were asserted, so a mutation forcing every non-ClaimDue identifier to
	// flag unconditionally (which would make OrphanSweep flag too, the
	// opposite of "resolving") moved nothing in this row. secondIdent closes
	// that: OrphanSweep occurs once in this fixture's code
	// (`var _ = OrphanSweep`), after the direction word, so it resolves and
	// contributes 0 directional findings for itself.
	{
		name:                 "every-distinct-identifier: a nearer, resolving identifier must not mask a farther, false one",
		src:                  "package outbox\n\n// ClaimDue and OrphanSweep below claim the row.\n\nvar _ = OrphanSweep\n",
		wantClaimDueFindings: 1,
		wantReason:           claimCommentGuardReasonUnresolved("below"),

		secondIdent:             "OrphanSweep",
		wantSecondIdentFindings: 0,
		wantSecondIdentReason:   claimCommentGuardReasonResolved("below"),
	},
	// #0364 -- gives claimCommentGuardDirectionalReason's
	// no-direction-word-match bucket a real, reachable exemplar under
	// NORMAL (unmutated) conditions, not only under a hypothetical mutation.
	// This is exactly the shape #0358's original capitalisation row secretly
	// fell into while its comment claimed a different cause (a window
	// limit): before #0363 added the case-insensitive flag, a capitalised
	// positional word never matched claimCommentGuardDirWordRe at all, so
	// the window logic was never reached, and a count-only assertion could
	// not tell that apart from a genuine window exclusion.
	//
	// NOTE ON THIS COMMENT'S OWN WORDING: deliberately does not spell either
	// positional word literally anywhere near "ClaimDue" in this doc
	// comment -- an earlier draft did, describing the fixture as mentioning
	// "neither X nor Y", and that description sentence was ITSELF flagged
	// by the directional axis scanning this very file (ClaimDue preceding
	// both quoted words, no code anywhere in this file resolving either) --
	// the same "this guard scans its own file" class #0358's own history
	// records. Rephrased rather than deleted, per that issue's convention.
	// The fixture below carries neither positional word at all, so
	// claimCommentGuardDirWordRe.FindAllStringSubmatchIndex returns zero
	// matches on it and the directional axis's per-match loop body never
	// executes -- deliberately, not as an accident of wording.
	{
		name:                 "no-direction-word-match: ClaimDue mentioned with neither \"below\" nor \"above\"",
		src:                  "package outbox\n\n// ClaimDue is scoped to mailKinds in this pass.\n",
		wantClaimDueFindings: 0,
		wantReason:           claimCommentGuardReasonNoDirectionWordMatch(),
	},
}

// TestClaimCommentGuardDirectionalAxisCalibrationTable is #0358 criterion
// 1: it asserts the CURRENT calibration of every trade surface measured
// across #0347's three reviews, so a future change to the window logic
// discovers, in a specific failing sub-test naming the trade, which
// calibration it moved -- rather than either silently widening a false
// positive back open or silently narrowing a false negative back into
// existence with nothing noticing either way (this issue's own root
// cause). The two real sites this axis exists to catch (#0342 sites A and
// C) are pinned separately, above, by
// TestClaimCommentGuardDirectionalAxisFlagsKnownStaleSitesFromDb9bff7 --
// not duplicated here.
//
// #0358 criterion 3's mutation proof (run by hand against a throwaway
// export, per that criterion and CLAUDE.md §8b -- not committed as code,
// since a test cannot mutate this file's own production logic without
// becoming exactly the in-file guard CLAUDE.md §8's #0258 entry warns
// against): reverting the directional marker set to the broad
// claimCommentGuardHistoricalRe fails this table's three `never`/`cannot`
// false-positive rows (the four marker-exemption rows stay green -- a
// broader marker set only exempts more) AND
// TestClaimCommentGuardDirectionalAxisFlagsKnownStaleSitesFromDb9bff7;
// reverting "every distinct identifier" to "nearest identifier only" fails
// that same falsifiability test (site A drops from 2 findings to 1);
// removing claimCommentGuardOpenParen's clamp entirely fails this table's
// paren-scoping row (0 -> 1) AND
// TestClaimCommentGuardDirectionalAxisIsCleanOnStoreGoParenScoping, below
// (0 -> 1, reproducing the exact pre-#0347-bounce-1 false positive). All
// three reproduced and recorded in issue #0358's own ## Verification.
func TestClaimCommentGuardDirectionalAxisCalibrationTable(t *testing.T) {
	for _, c := range claimCommentGuardDirectionalCalibrationCases {
		t.Run(c.name, func(t *testing.T) {
			findings, err := findClaimCommentGuardFindings("calibration.go", c.src, map[string]map[string]bool{})
			if err != nil {
				t.Fatalf("parsing calibration fixture: %v", err)
			}
			got := 0
			for _, f := range findings {
				if f.axis == "directional" && f.ident == "ClaimDue" {
					got++
				}
			}
			if got != c.wantClaimDueFindings {
				t.Fatalf("directional ClaimDue findings = %d, want %d; all findings: %+v", got, c.wantClaimDueFindings, findings)
			}

			// #0364 -- a matching count is not enough; re-derive WHY this row
			// gets that count (from the axis's own shared primitives, via
			// claimCommentGuardDiagnoseDirectional) and confirm it matches
			// the mechanism the row's own name and comment claim. This is
			// what #0358's identifier-after-direction-word row and #0363's
			// first-pass resolution pin both lacked: each got its declared
			// count for a DIFFERENT reason than the one it named, and
			// nothing but hand-instrumentation caught it.
			reason, err := claimCommentGuardDiagnoseDirectional(c.src, "ClaimDue")
			if err != nil {
				t.Fatalf("diagnosing WHY this row gets its count (#0364): %v", err)
			}
			if reason != c.wantReason {
				t.Fatalf("row got %d finding(s) for a DIFFERENT mechanism than its declared wantReason (#0364) -- want %+v, got %+v", got, c.wantReason, reason)
			}

			// #0366 -- when a row also declares a secondIdent, assert ITS
			// count and reason too. Every row except every-distinct-identifier
			// leaves secondIdent "" and this block is skipped; that row's
			// stated purpose is a SECOND identifier's resolution, which
			// wantClaimDueFindings/wantReason alone cannot see (see
			// claimCommentGuardDirectionalCalibrationCase's own doc comment,
			// "#0366 DECISION", for why this row and only this row gets one).
			if c.secondIdent != "" {
				got2 := 0
				for _, f := range findings {
					if f.axis == "directional" && f.ident == c.secondIdent {
						got2++
					}
				}
				if got2 != c.wantSecondIdentFindings {
					t.Fatalf("directional %s findings = %d, want %d; all findings: %+v", c.secondIdent, got2, c.wantSecondIdentFindings, findings)
				}

				reason2, err := claimCommentGuardDiagnoseDirectional(c.src, c.secondIdent)
				if err != nil {
					t.Fatalf("diagnosing WHY this row's %s count is what it is (#0366): %v", c.secondIdent, err)
				}
				if reason2 != c.wantSecondIdentReason {
					t.Fatalf("row's %s got %d finding(s) for a DIFFERENT mechanism than its declared wantSecondIdentReason (#0366) -- want %+v, got %+v", c.secondIdent, got2, c.wantSecondIdentReason, reason2)
				}
			}
		})
	}
}

// claimCommentGuardStoreGoParenSentinelRe is #0358's review (B5): the
// PREVIOUS sentinel here was a bare strings.Contains(src, "see SelectDue"),
// which did not require the direction word itself -- rewording store.go's
// parenthetical to "(see SelectDue, declared earlier)" left this test green
// while pinning nothing, a silent fail-open in a test whose only job is to
// pin a trade. store.go also wraps the parenthetical across two comment
// lines ("see SelectDue\n// above)"), so a plain "see SelectDue above"
// substring check would NOT match today's file either -- this pattern
// tolerates that wrap (a run of whitespace-or-"/" between the two words)
// while still requiring the direction word to survive. Verified against
// the real file both ways: matches today's store.go, stops matching once
// "above" is reworded away.
var claimCommentGuardStoreGoParenSentinelRe = regexp.MustCompile(`see SelectDue[\s/]+above`)

// TestClaimCommentGuardDirectionalAxisIsCleanOnStoreGoParenScoping is
// #0358 criterion 2's explicit ask: name store.go's false positive as the
// reason the parenthesis scoping exists, pinned against the REAL file
// (extraction, not a retyped fixture -- CLAUDE.md §8), not a frozen copy of
// it. internal/outbox/store.go's real doc comment for ClaimRow names it as
// its own sentence's subject, then, in a trailing parenthetical, points a
// positional word at SelectDue, a second, different identifier declared
// earlier in this same file -- deliberately paraphrased here, not quoted,
// since #0347's own history records that quoting a real defect sentence
// verbatim in a doc comment trips this guard's two tests against
// themselves (rephrase, don't delete, is that issue's own stated
// convention).
//
// #0382 -- this test pins TWO identifiers at zero, and paren scoping is
// responsible for only one of them; instrumented directly against the real
// file rather than assumed. SelectDue sits INSIDE the paren-scoped window
// (the clamp that sets the window's left edge here is the open-paren one,
// not the 90-char or clause-boundary ones), so it is checked, and it comes
// back zero because it genuinely resolves -- SelectDue really is declared
// earlier in this file, in the direction the positional word claims.
// ClaimRow sits OUTSIDE that parenthetical, well before the window's own
// left edge, so it is never checked at all -- excluding it from the window
// is claimCommentGuardOpenParen's actual job here. Without that scoping,
// ClaimRow -- with zero earlier occurrences anywhere in this file -- would
// enter the (then wider) window too and falsely fire; #0347's review
// (bounce 1, B1 part 3) measured exactly this false positive before the
// scoping existed. Reading the real file, rather than a copy, means a
// future edit to store.go's own comment is checked against the real trade
// rather than a fixture that can drift out of sync with it.
func TestClaimCommentGuardDirectionalAxisIsCleanOnStoreGoParenScoping(t *testing.T) {
	const path = "store.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v -- this test's premise (a real, checked-in file with the ClaimRow/SelectDue parenthetical comment for ClaimRow) no longer holds and must be re-pointed, not skipped", path, err)
	}
	if !claimCommentGuardStoreGoParenSentinelRe.MatchString(string(src)) {
		t.Fatal("FAIL-CLOSED: store.go no longer contains the parenthetical \"(see SelectDue ... above)\" -- WITH the direction word itself surviving -- this test pins against; re-point it at whatever comment now exercises the same paren-scoping trade, or drop it if the shape no longer exists in this file")
	}

	pkgTypes, visited := claimCommentGuardBuildPackageTypeIndex(t, claimCommentGuardScanRoots)
	if visited == 0 {
		t.Fatal("FAIL-CLOSED: package-type index walk visited zero files")
	}

	findings, err := findClaimCommentGuardFindings(path, nil, pkgTypes)
	if err != nil {
		t.Fatalf("scanning %s: %v", path, err)
	}
	for _, f := range findings {
		if f.axis == "directional" && (f.ident == "ClaimRow" || f.ident == "SelectDue") {
			t.Fatalf("store.go's real ClaimRow parenthetical comment must produce zero directional findings for both ClaimRow (excluded from the window by paren scoping, #0382) and SelectDue (inside the window, resolves genuinely) -- one of those two now fires and the trade this test pins is broken; got: %+v", f)
		}
	}
}

// TestClaimCommentGuardIsCleanOnKnownNegativeFixture is #0342's third
// review's own measurement, re-proved here as a standing regression test:
// internal/handlers/subscribe_intake_test.go mentions ClaimDue nine times
// and calls it zero times, and every one of the nine is correct (see this
// file's own top-of-file doc comment for why). This test parses that REAL
// file, from disk, and asserts the existence axis reports nothing for it
// -- a correct implementation of this guard must produce zero findings on
// this file, permanently, and a future change to the detection heuristics
// that starts flagging any of the nine must fail this test before it can
// fail a real `go test ./internal/handlers/...` run.
func TestClaimCommentGuardIsCleanOnKnownNegativeFixture(t *testing.T) {
	path := filepath.Join("..", "handlers", "subscribe_intake_test.go")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("negative fixture file not found at %s: %v -- this test's premise (a real, checked-in file with nine correct ClaimDue mentions) no longer holds and must be re-pointed, not skipped", path, err)
	}

	pkgTypes, visited := claimCommentGuardBuildPackageTypeIndex(t, claimCommentGuardScanRoots)
	if visited == 0 {
		t.Fatal("FAIL-CLOSED: package-type index walk visited zero files")
	}

	findings, err := findClaimCommentGuardFindings(path, nil, pkgTypes)
	if err != nil {
		t.Fatalf("scanning %s: %v", path, err)
	}

	mentions := 0
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	mentions = strings.Count(string(src), "ClaimDue")
	if mentions == 0 {
		t.Fatal("FAIL-CLOSED: this negative fixture's premise (it mentions ClaimDue) no longer holds -- a zero-mention file proves nothing about false positives and this test must be re-pointed")
	}

	var existence []claimCommentFinding
	for _, f := range findings {
		if f.axis == "existence" {
			existence = append(existence, f)
		}
	}
	if len(existence) != 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%s is a named negative fixture (#0347, per #0342's third review): its ClaimDue mentions are all correct and must produce zero existence-axis findings. Got %d:\n", path, len(existence))
		for _, f := range existence {
			fmt.Fprintf(&b, "%s:%d %s\n    …%s…\n", f.path, f.line, f.detail, f.snippet)
		}
		t.Fatal(b.String())
	}
}
