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
// from both axes below, matching #0342's own round-2 audit's convention.
// This is deliberately broad (a human already read every one of #0342's
// findings and confirmed the correctly-historical ones carry language like
// this); the guard's job is to surface candidates and require a human
// convention to have been followed, not to adjudicate prose.
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
var claimCommentGuardCallSyntaxLiterals = []string{".ClaimDue(", ".OrphanSweep(", ".SelectDue("}

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
	claimCommentGuardDirWordRe       = regexp.MustCompile(`\b(below|above)\b`)
	claimCommentGuardHistoricalRe    = regexp.MustCompile(`(?i)\b(before #|since #|has not called|no longer|used to|formerly|the old |at the time|was removed|removed by|replaced|predates|pre-#|historical|rather than|instead of|would|reverting|revert(ed)?|cannot|never)\b`)
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
		flat := strings.Join(strings.Fields(text), " ")
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
			if claimCommentGuardHistoricalRe.MatchString(claimCommentGuardSentence(flat, dm[0], dm[1])) {
				continue // #0347 criterion 3's convention, sentence-scoped -- see the existence axis's identical fix, above
			}
			dir := flat[dm[2]:dm[3]]
			// Anchor on the nearer of (a) 90 chars back, or (b) the last
			// clause boundary claimCommentGuardLastBoundary finds -- the
			// SAME boundary set the historical-marker check uses two
			// paragraphs up, including the em dash ("—") this codebase
			// writes clauses with far more than it writes periods. An
			// earlier draft trimmed only on ".;:!?" here (a DIFFERENT,
			// narrower set, drifted out of sync from having two copies of
			// the same idea) and a left-to-right scan lets an EARLIER word
			// consume a LATER direction claim and hide the real one --
			// measured against the real tree, not assumed: a claim-machinery
			// identifier named before an em dash, in a clause unrelated to
			// a directional word that follows it, used to leak into this
			// window (internal/mailing/outbox_worker_test.go's own "#0297
			// — see this test's own doc comment ABOVE..." was the
			// reproduction: the OTHER identifier named earlier in that
			// same paragraph, well before the em dash, is what leaked).
			lo := dm[0] - 90
			if b := claimCommentGuardLastBoundary(flat[:dm[0]]); b > lo {
				lo = b
			}
			if lo < 0 {
				lo = 0
			}
			window := flat[lo:dm[0]]
			// Take only the NEAREST target identifier to the direction
			// word, not every one in the window. Measured against the
			// real tree, not assumed: an earlier draft took every
			// identifier in the window and flagged one real, correct
			// doc comment in this package's own store.go -- a sentence
			// naming one method as this file's own subject and, in a
			// trailing parenthetical, pointing a directional word at a
			// SECOND, different method declared earlier in the same
			// file. That directional word grammatically binds to the
			// method named immediately before it inside the
			// parenthetical, not to the sentence's own subject, 60+
			// characters earlier. Taking the nearest identifier is the
			// same simplification English word order makes: a direction
			// word almost always modifies whatever it immediately
			// follows, not an earlier noun phrase.
			matches := claimCommentGuardIdentRe.FindAllString(window, -1)
			if len(matches) == 0 {
				continue
			}
			im := matches[len(matches)-1]
			offs := facts.identOffsets[im]
			ok := false
			for _, o := range offs {
				if dir == "below" && o > endOff {
					ok = true
				}
				if dir == "above" && o < startOff {
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
// existence axis flags outbox_worker_test.go:746's "OutboxWorker.ClaimDue
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
