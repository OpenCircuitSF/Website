package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// #0265: #0126's original `## Verification` named three tests that existed
// nowhere in the tree, and a re-review found three more in the same file —
// six false citations, found across two passes, none by a tool. `#0196`,
// `#0199` and `#0220` already guard a Go or TypeScript *comment* that cites a
// `Test…` identifier no function defines. None of them reads Markdown, so an
// issue file's `## Verification` section — precisely where a reviewer looks
// to decide whether a claim was proved — was free of the same check.
//
// # Scope, decided from a measurement, not a guess
//
// Before writing the pattern, this guard's `## Verification` was produced by
// running its candidate extractor (the pattern and discount rules below)
// against the real corpus. **Every figure in this comment is a point-in-time
// measurement, taken at the tree #0268 was implemented against**, not a
// constant: 268 `issues/NNNN.md` files, all the sections listed in
// issueCitationSectionHeaders, 2,796 raw `Test…`-shaped tokens. `#0196`'s
// four rules alone (wildcard family — trailing `*`/`...` — a `renamed from`
// note, and the `TestSentAt`/`TestPool` allowlist) leave 256 unresolved.
// Three more rules this corpus specifically required (below) narrow that
// further: the line-wrap rule takes it to 251, the `-run`-style
// regex-alternation-fragment rule (adjacent `|`, scoped to a genuine
// substring of a defined test name — see below for why) takes it to 199,
// and the go-test-output-marker rule takes it to 161. **All 161 are in
// `resolved` issue files. Zero are in `open` or `in-progress` files** —
// and the zero, not the 161, is what this guard enforces.
//
// The corpus-wide total drifts with ordinary work and is expected to. Over
// this guard's short history it has read 1,742 / 107 / 87 when first
// written; 1,746 / 140 / 108 / 88 after `#0265`'s fix pass corrected a
// misattributed milestone; 1,766 / 146 / 114 / 94 at 03db7ce; and
// 2,783 / 256 / 204 / 166 here. Two of those steps are worth separating,
// because only one of them is anybody's error:
//
//   - **1,746 → 1,766, and 88 → 94, was external.** `#0124` (06fefb3)
//     deleted ten soft-bounce test functions that `issues/0039.md` and
//     `issues/0109.md` cite, and exactly six of those citations thereby
//     became dangling — measured by diffing the set of defined `Test…`
//     functions at the two commits and intersecting it with the dangling
//     set. All six are in `resolved` files. That is precisely the
//     `resolved`-file drift the scope decision below deliberately declines
//     to police.
//
//   - **1,766 → 2,796 is #0268 widening what gets scanned**, not the corpus
//     growing. Admitting `## Fix pass`, `## Review notes` and their earlier
//     spellings roughly doubled the text this guard reads. The enforced
//     `open`/`in-progress` scope was 0 before the widening and is 0 after —
//     but only after the eleven dangling citations #0268 measured in the
//     newly-admitted sections were fixed. It failed loudly on first run,
//     which was the point.
//
// That is not a coincidence of a small sample; it is the project's own
// mutation-testing convention (`CLAUDE.md` §8b, §8a: copy a file aside,
// plant a failure, observe it, restore) working exactly as intended. Issues
// #0199, #0208, #0209, #0211, #0212, #0215, #0251, #0252, #0255, #0258 and
// others document a scratch or plant test file created to *prove a guard
// fires*, exercised once, and deleted — its name genuinely does not exist in
// the tree today, and was never meant to. Scanning `resolved` files
// unconditionally would fail this guard at introduction on ~161 lines of
// exactly that shape, which is precisely the "a guard everyone disables is
// worse than none" failure this issue itself warns against (see
// `issues/0265.md` Notes).
//
// So the guard's fail-scope is **`open` and `in-progress` issue files
// only** — the ones still being actively verified, where a false citation is
// still actionable by the agent that wrote it. This is exactly the window
// `#0126`'s real defect lived in: its false citations existed while the
// issue was `in-progress`, before the fix pass caught two of them and the
// re-review caught the rest, and were gone by the time it was marked
// `resolved`. A guard scoped this way would have caught all six the moment
// `check.sh` next ran against the dirty file, rather than requiring two
// review passes. `resolved`-file drift (acceptance criterion 3's "true when
// written, false now") is real — a handful of the 161 are genuine stale
// citations surviving a later rename (#0028 cites the pre-rename confirmation
// test name that #0196's own guard commentary documents as later renamed —
// not spelled out here, since it is exactly the dangling shape this file
// itself must not cite in a real comment; see #0196's Notes for the name) —
// but it is a minority of the 161, buried in scratch-test noise, and catching it
// is not worth the false-positive rate measured above. If it matters later,
// the fix is a second, opt-in pass over `resolved` files with a stronger
// discount for the scratch/plant/probe naming convention, not widening this
// guard's default scope.
//
// # The pattern, and what it borrows
//
// issueTestCitationPattern is exactly #0196's testNameCitationPattern
// (internal/handlers/dangling_test_citation_guard_test.go): "Test" starting
// its own word, followed by a character that is not a lowercase ASCII
// letter — go test's own rule for recognizing a test function, which for
// free excludes the *TestPool convention's compound helper names and the
// plain English word "Tests"/"Testing".
//
// collectDefinedGoTestFuncs is the same AST walk #0196 uses
// (collectDefinedTestFuncs), just rooted once at the repo root
// ("../.." from internal/db) instead of #0196's three separate roots —
// internal/db's relative position makes one root sufficient to cover
// internal/, cmd/ and web/ together, so there is no reason to split it.
//
// issueCitationExcluded carries #0196's four discount rules (wildcard `*`,
// wildcard `...`, `renamed from` within 20 bytes, and the
// TestSentAt/TestPool allowlist), plus three this corpus specifically
// required and neither #0196 nor #0199 needed, because a markdown evidence
// section quotes shell and go-test output — and wraps long identifiers
// across lines — in ways a Go doc comment never does:
//
//   - **A line break immediately after the match, where the match joined
//     with the identifier characters continuing on the next line names a
//     defined function.** Markdown prose wraps, and a wrapped test name
//     reads to the pattern as a truncated prefix resolving to nothing. See
//     isLineWrappedDefinedTestName for the live instance that prompted this
//     and for why the rule cannot smuggle anything past the guard: it
//     discounts only when the *joined* name is defined, so a wrapped
//     fabrication is still reported.
//
//   - **A `|` immediately before or after the match, AND the match is a
//     substring of some test function the tree actually defines.**
//     `## Verification` routinely quotes a `go test -run 'A|B|C'`
//     invocation, where each name is a regexp alternative matched by
//     substring — go test's `-run` is unanchored, so `-run 'A|B|C'`
//     matches any defined test whose name *contains* A, B, or C, not only
//     one spelled exactly that way (#0007's own -run invocation, naming a
//     construction-path test alongside two others joined by `|`, is the
//     real instance this rule exists for — the first fragment there is
//     itself only a substring of the real function's longer name). That is
//     the actual semantics of `-run`, and it is also the boundary of what
//     this rule may discount: a pipe-adjacent fragment that is not a
//     substring of anything defined is not "standing in for a family", it
//     is just wrong, and this rule must not hide it.
//     **`#0265`'s first fix pass shipped this rule without the
//     substring-of-a-defined-name check** — every name in a multi-name
//     `-run` was exempt unconditionally, which discounted *all four* names
//     in this very file's own headline proof command
//     (`issues/0265.md`, the `## Verification` section's `go test -run
//     'A|B|C|D'` line) regardless of whether they were real, and the
//     reviewing pass planted a fabricated name in that exact shape and
//     watched it pass silently. Narrowing to "substring of a defined name"
//     costs nothing measured: `#0007`'s motivating instance still
//     discounts, the enforced `open`/`in-progress` scope stays at zero
//     failures, and the corpus-wide unresolved count moves by exactly one
//     (a `resolved`, out-of-scope file). This is the markdown-specific
//     sibling of the wildcard-family rule above; it discounts the same
//     intent — "this name stands for a family/fragment, not itself" — but,
//     unlike the wildcard rule, it checks *something*, because unlike a
//     bare trailing `*` a pipe fragment is cheap to verify against the same
//     `defined` set the guard already built.
//
//     **What it checks is weaker than "the claim is true", and the comment
//     said otherwise until #0265's escalated pass.** The substring test is
//     **tree-wide**: it asks whether the fragment occurs inside *any*
//     defined test name anywhere in the repo, not whether it matches
//     anything in the packages the cited command names. So a command of the
//     form `go test ./internal/db/... -run 'A|B'`, where A names something
//     defined only in a different package, is discounted even though that
//     `-run` matches nothing in `internal/db` and the command exits 0
//     having run no test at all. Likewise a short fragment that happens to
//     be a substring of an unrelated name discounts. Both are faithful to
//     `-run`'s unanchored semantics and to the rule as prescribed, so this
//     is a residual rather than a defect — but the rule proves only "this
//     fragment names something real somewhere", never "this command proved
//     what the issue says it proved". Closing it would mean parsing the
//     package pattern out of the cited command and resolving the fragment
//     against only that subtree.
//
//   - **A `--- FAIL:`, `--- PASS:` or `=== RUN` marker within 40 bytes
//     before the match** (go test -v's own three output-line prefixes,
//     `\s` — which matches a line break — allowed between the marker and
//     the name so a wrapped quote of the same shape still discounts). This
//     is quoting the literal, historical output of a test that ran —
//     almost always one of the scratch/plant/probe files above, created,
//     exercised once to prove a guard fires, and deleted — not a claim
//     that the named test exists in the tree now or ever will again.
//     #0258's own scratch proof file forced this rule: its issue file
//     quotes the exact `--- FAIL: …` line that file produced before it was
//     deleted, and without this rule that citation — a true historical
//     record, not a false one — would fail this guard on introduction,
//     against #0258's own still-`in-progress` file. (Not named directly
//     here either, for the same self-reference reason as the rename
//     example above — see #0258's `## Verification` for the line.)
//
//     **Known, accepted blind spot, unlike the pipe rule above: this one is
//     not narrowed, and cannot cheaply be.** Unlike a pipe fragment, a
//     `--- FAIL:`/`--- PASS:`/`=== RUN` marker has no `defined` set to check
//     the name against — the whole reason the marker quotes it is that the
//     test *no longer exists* (it was a scratch/plant file, deleted after
//     proving a guard fired), so "is this name defined today" cannot
//     distinguish a true historical record from a fabricated one dressed
//     the same way. Planting a fictitious name behind a `--- FAIL:` marker
//     and running this guard confirms it: silently accepted, exactly like a
//     genuine quote of #0258's real deleted test would be. That is
//     accepted, not fixed here — #0258's file is a live, in-progress
//     citation that depends on this rule staying permissive, and there is
//     no local signal in the 40 bytes before the match that tells "quoting
//     a test that really ran" apart from "quoting a test that never did".
//     Pinned by name in TestIssueCitationExcludedDiscountRulesAreEachLoadBearing
//     (the case documented there as the marker rule's blind spot), the same
//     shape #0196 uses to pin fullyEmphasisedDanglingName. Two live
//     citations in #0266's `## Verification` sit in exactly this shape
//     today — both real, both consequently unchecked by this rule — and one
//     of the two names, TestErase_RedactsSubscriberEvents_PreservesRows, is
//     itself a name that #0126 originally fabricated, which is the reason
//     this whole guard exists. If the smuggling route this leaves open
//     (dressing a false citation as `--- FAIL:` output defeats the guard)
//     is ever exploited for real, the fix is likely a separate signal
//     entirely — e.g. requiring the marker-quoted block to also carry the
//     issue's own `git cat-file`-verified commit, not a narrower regexp —
//     not something this rule's 40-byte window can provide on its own.
//
// None of the three added rules is hypothetical — removing any one reopens
// real corpus lines (proved directly in
// TestIssueCitationExcludedDiscountRulesAreEachLoadBearing and, for the
// line-wrap rule, in
// TestIssueCitationLineWrappedNameResolvesOnlyWhenJoinedNameIsDefined). But
// they carry different weight in the scope this guard actually enforces, and
// that weight **changed when #0268 widened what gets scanned** — so an earlier
// draft of this paragraph, correct before the widening, is not correct after
// it. Measured by toggling each rule and re-running the scan over the whole
// corpus:
//
//   - **Marker rule: load-bearing.** Removing it takes the enforced
//     `open`/`in-progress` scope from 0 to 5. Before the widening it took it
//     from 0 to 1 — a single in-progress citation quoting the real output of
//     a scratch proof file created, run once and deleted (see #0258's
//     `## Verification`). Widening the scan multiplied what depends on it.
//
//   - **Pipe rule: load-bearing now, but it was not before #0268.** Removing
//     it takes the enforced scope from 0 to 2. Measured before the widening,
//     removing it left the scope at 0, because its motivating line lives in
//     #0007, a `resolved` file. Fix passes and review notes quote `-run`
//     alternations constantly, which is what changed.
//
//   - **Line-wrap rule: not load-bearing today.** Removing it leaves the
//     enforced scope at 0 and moves the corpus-wide count by 5. It is here
//     because the shape it covers was observed producing six false positives
//     at once in a live file during #0268's own implementation, and because
//     it cannot produce a false negative.
//
// The pipe rule is narrow by construction (mechanically checked against
// `defined`, with the tree-wide caveat above); the line-wrap rule likewise,
// and more strictly. The marker rule is not narrow, and is not claimed to
// be — see immediately above for its blind spot and why it stays.

// issueTestCitationPattern matches a "Test…" identifier as go test itself
// recognizes one: a word boundary, then "Test", then anything except a
// lowercase ASCII letter. See #0196's testNameCitationPattern for the full
// rationale; this is the identical pattern.
var issueTestCitationPattern = regexp.MustCompile(`\bTest[A-Z0-9_][A-Za-z0-9_]*\b`)

// issueCitationAllowlist mirrors #0196's nonTestIdentifierAllowlist: names
// that match issueTestCitationPattern but are not test functions.
var issueCitationAllowlist = map[string]bool{
	"TestSentAt": true, // a *string struct field, not a function — see #0196
	"TestPool":   true, // the *TestPool naming-convention's own bare name
}

// goTestOutputMarkerPattern recognizes go test -v's own three output-line
// prefixes ("--- FAIL:", "--- PASS:", "=== RUN") so a citation quoting real
// historical test output is not mistaken for a claim that the named test
// exists in the tree today. See the rationale above.
var goTestOutputMarkerPattern = regexp.MustCompile(`(?:---\s+(?:FAIL|PASS)|===\s+RUN)\s*:?\s*$`)

// isPipeAdjacentFragmentOfDefinedTest reports whether name — appearing
// immediately next to a `|` in what is presumed to be a `go test -run
// 'A|B|C'` regexp alternation — is a genuine substring of some function in
// defined. That is what `-run`'s unanchored regexp actually means: A, B and
// C each match any defined test whose name *contains* them, so a pipe
// fragment only stands for a real family when it is in fact a substring of
// one. A fragment that is not a substring of anything defined is not
// discounted by this — see the file-level comment's account of #0265's
// first fix pass, which exempted every pipe-adjacent name unconditionally
// and was proven, by plant, to let a fabricated name through.
func isPipeAdjacentFragmentOfDefinedTest(name string, defined map[string]bool) bool {
	for candidate := range defined {
		if strings.Contains(candidate, name) {
			return true
		}
	}
	return false
}

// isLineWrappedDefinedTestName reports whether the match ending at end is the
// first half of a test-function name that markdown prose broke across a line,
// where the two halves joined name a function the tree really defines.
//
// #0268 widened this guard to read `## Fix pass` and `## Review notes`, which
// are long-form wrapped prose full of long identifiers, and a name split at a
// line break reads to issueTestCitationPattern as a truncated prefix that
// resolves to nothing. This was observed live: while this pass was running,
// another agent's in-progress issue file briefly carried six such names in its
// `## Verification` section — a section this guard already scanned before
// #0268 — and every one was reported as dangling although each joined into a
// real defined function.
//
// The rule cannot smuggle anything past the guard, because it discounts only
// when the *joined* name is in defined. A wrapped name whose halves do not
// join into something real is still reported. Blockquote markers are skipped
// along with indentation, since review notes are frequently quoted.
func isLineWrappedDefinedTestName(text string, end int, name string, defined map[string]bool) bool {
	if end >= len(text) || text[end] != '\n' {
		return false
	}
	i := end + 1
	for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '>') {
		i++
	}
	j := i
	for j < len(text) && isGoIdentByte(text[j]) {
		j++
	}
	if j == i {
		return false
	}
	return defined[name+text[i:j]]
}

// isGoIdentByte reports whether b may appear in a Go identifier after the
// first character, restricted to the ASCII subset test function names use.
func isGoIdentByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// issueCitationExcluded reports whether the Test-shaped match at
// text[start:end] should be discounted rather than resolved against
// defined, the set of test functions the tree actually defines. See the
// file-level comment for what each rule is for, the real corpus line it
// exists to cover, and — for the pipe and marker rules specifically — the
// blind spot each one does or does not have.
func issueCitationExcluded(text string, start, end int, defined map[string]bool) bool {
	name := text[start:end]
	if issueCitationAllowlist[name] {
		return true
	}
	if end < len(text) && text[end] == '*' {
		return true
	}
	if end+3 <= len(text) && text[end:end+3] == "..." {
		return true
	}
	if isLineWrappedDefinedTestName(text, end, name, defined) {
		return true
	}
	pipeAdjacent := (end < len(text) && text[end] == '|') || (start > 0 && text[start-1] == '|')
	if pipeAdjacent && isPipeAdjacentFragmentOfDefinedTest(name, defined) {
		return true
	}
	windowStart := start - 20
	if windowStart < 0 {
		windowStart = 0
	}
	if strings.Contains(strings.ToLower(text[windowStart:start]), "renamed from") {
		return true
	}
	markerWindowStart := start - 40
	if markerWindowStart < 0 {
		markerWindowStart = 0
	}
	if goTestOutputMarkerPattern.MatchString(text[markerWindowStart:start]) {
		return true
	}
	return false
}

// issueGuardScanRoots is where issue-tracker citations are resolved
// against: one root suffices from internal/db's position, since "../.."
// (the repo root) already contains internal/, cmd/ and web/.
var issueGuardScanRoots = []string{"../.."}

// issueGuardSkipDir reports whether dirName should be pruned from a walk
// rooted at the repo root — vendored/generated trees (#0194's lesson) plus
// .git, which #0196's narrower per-package roots never had to consider.
func issueGuardSkipDir(dirName string) bool {
	switch dirName {
	case "node_modules", "dist", ".git":
		return true
	}
	return false
}

// collectDefinedGoTestFuncs returns the set of top-level (no receiver)
// function names starting with "Test" defined anywhere under roots. Same
// approach as #0196's collectDefinedTestFuncs.
func collectDefinedGoTestFuncs(t *testing.T, roots []string) map[string]bool {
	t.Helper()
	defined := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if issueGuardSkipDir(entry.Name()) {
					return filepath.SkipDir
				}
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
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil {
					continue
				}
				name := fn.Name.Name
				if strings.HasPrefix(name, "Test") && len(name) > len("Test") {
					defined[name] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return defined
}

// issueCitationSectionHeaders are the level-2 sections this guard resolves
// citations under, keyed by *normalized* title (see issueSectionTitle) so a
// dated or annotated heading matches the same entry as a bare one.
//
// # Which sections, decided from a measurement — #0268
//
// #0265 shipped this guard admitting exactly two headings, and #0268 measured
// what that missed. Counting dangling citations per section across the 23
// open/in-progress issue files at b12fed1, rolled up to the enclosing level-2
// heading:
//
//	dangling  raw tokens  section
//	       5          26  ## Fix pass
//	       4          10  ## Description
//	       2          23  ## Review notes
//	       0          11  ## Verification          (already scanned)
//	       0           2  ## Implementation notes  (already scanned)
//
// Every other level-2 heading present in an open or in-progress file —
// ## Relation, ## Notes, ## Acceptance criteria, ## Work log, ## Open
// questions, ## Files changed, ## Root cause, ## Fix and a scattering of
// one-off narrative headings — carries no Test-shaped token at all there.
//
// So the guard was enforcing a rule over the two cleanest sections in the
// corpus while the sections where implementers and reviewers actually assert
// evidence went unchecked. Both of the factual defects that bounced #0265 a
// second time, and the dangling citation one of them was about, sat in those
// unscanned sections. That is what drives the additions below; the sections
// with a measured zero are not admitted on the strength of a guess.
//
// A fix pass writes its evidence under a `### Verification` *nested inside*
// `## Fix pass` — three hashes, so the old level-2 match never saw it. There
// is deliberately no separate rule for that nesting: a section runs from its
// heading to the next level-2 heading, so admitting `## Fix pass` admits
// everything nested under it, which is also what has always happened for
// `## Verification`.
//
// # What stays out, and why it is not an oversight
//
//   - **## Description and ## Notes.** These *quote* fabricated names as
//     subject matter. #0126's six false citations are reproduced verbatim in
//     several issue files precisely so the defect can be discussed, and four
//     of the eleven dangling citations measured above are exactly that, in
//     this guard's own motivating issue. Scanning them would fail the guard
//     on the very text that documents why the guard exists. The line #0268
//     draws is between prose that *asserts* evidence and prose that
//     *discusses* it.
//
//   - **## Plan and ## Acceptance criteria.** These propose work. A planned
//     or required test may legitimately not exist yet — #0265's original
//     reasoning for excluding ## Plan, unchanged.
//
//   - **## Fix, ## Root cause, ## Files changed, ## Gotchas.** Zero dangling
//     and zero tokens in the enforced scope today, so the measurement gives
//     no support for admitting them; and the resolved corpus shows each
//     carrying legitimate quotes of deleted scratch tests and pre-rename
//     names (a ## Root cause naming a SIGTERM reproduction file that was
//     created, run once and deleted; a ## Gotchas naming a pre-rename
//     construction-seam test). Admitting them would import exactly the
//     day-one noise #0265 scoped away from. Revisit if a measurement ever
//     says otherwise.
//
//   - **## Manual verification required.** Zero in the enforced scope. Its
//     two dangling citations in resolved files are both a bare `-run` prefix
//     standing in for a real family — a shape the pipe rule does not cover
//     because it is not pipe-adjacent (see the residual noted there).
//
//   - **## Work log.** Token and cost accounting, not evidence about tests.
var issueCitationSectionHeaders = map[string]bool{
	"verification":          true,
	"implementation notes":  true,
	"fix pass":              true, // #0268: 5 of the 11 measured dangling citations
	"review notes":          true, // #0268: 2 of the 11
	"review verification":   true, // earlier passes' spelling of "review notes"
	"review findings":       true, // ditto
	"bounce fix":            true, // earlier passes' spelling of "fix pass"
	"bounce-fix completion": true, // ditto
}

// issueSectionTitle normalizes a level-2 heading line to the bare section
// title used as the key in issueCitationSectionHeaders: it strips the "## "
// marker, drops any trailing annotation introduced by an em-dash or an
// opening parenthesis (whichever comes first), trims, and lowercases. That is
// what lets one entry match every real spelling in the corpus — for example a
// review-notes heading carrying a phase and a date, or a fix-pass heading
// carrying a date and a parenthetical. It returns "" for a line that is not a
// level-2 heading.
func issueSectionTitle(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "## ") {
		return ""
	}
	title := strings.TrimSpace(line[len("## "):])
	cut := len(title)
	if i := strings.Index(title, " — "); i >= 0 && i < cut {
		cut = i
	}
	if i := strings.Index(title, " ("); i >= 0 && i < cut {
		cut = i
	}
	return strings.ToLower(strings.TrimSpace(title[:cut]))
}

// extractNamedSections returns, for each level-2 heading whose normalized
// title is in issueCitationSectionHeaders, the heading and the text between it
// and the next level-2 heading (or EOF) — plus startLine, the 1-based line
// number of the first line of that body, so a citation's position inside the
// returned text can be translated back into a real line number in the source
// file. Nested level-3 headings are part of the section they sit under, which
// is how a fix pass's nested Verification subsection gets scanned.
func extractNamedSections(fileText string) []struct {
	header    string
	startLine int
	text      string
} {
	lines := strings.Split(fileText, "\n")
	var sections []struct {
		header    string
		startLine int
		text      string
	}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !issueCitationSectionHeaders[issueSectionTitle(line)] {
			continue
		}
		start := i + 1
		j := start
		for j < len(lines) && !strings.HasPrefix(lines[j], "## ") {
			j++
		}
		sections = append(sections, struct {
			header    string
			startLine int
			text      string
		}{header: line, startLine: start + 1, text: strings.Join(lines[start:j], "\n")})
		i = j - 1
	}
	return sections
}

// issueStatusPattern extracts an issue file's `**Status**` metadata row.
var issueStatusPattern = regexp.MustCompile(`(?m)^\|\s*\*\*Status\*\*\s*\|\s*([a-z-]+)\s*\|`)

// issueStatus returns the status recorded in an issues/NNNN.md file's
// metadata table, or "" if the row is missing or malformed.
func issueStatus(fileText string) string {
	m := issueStatusPattern.FindStringSubmatch(fileText)
	if m == nil {
		return ""
	}
	return m[1]
}

// scanIssueDirForDanglingTestCitations resolves every Test… identifier
// cited in dir's *.md files' evidence-asserting sections — those named in
// issueCitationSectionHeaders, including everything nested under them —
// against defined, restricted to files whose `**Status**` row satisfies
// includeStatus. It returns one formatted "path:line: Identifier"
// string per dangling citation, sorted for a stable, pasteable report.
func scanIssueDirForDanglingTestCitations(t *testing.T, dir string, defined map[string]bool, includeStatus func(status string) bool) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var failures []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("read %s: %v", path, rerr)
		}
		text := string(raw)
		if !includeStatus(issueStatus(text)) {
			continue
		}
		for _, section := range extractNamedSections(text) {
			for _, loc := range issueTestCitationPattern.FindAllStringIndex(section.text, -1) {
				if issueCitationExcluded(section.text, loc[0], loc[1], defined) {
					continue
				}
				name := section.text[loc[0]:loc[1]]
				if defined[name] {
					continue
				}
				line := section.startLine + strings.Count(section.text[:loc[0]], "\n")
				failures = append(failures, path+":"+itoa(line)+": "+name)
			}
		}
	}
	sort.Strings(failures)
	return failures
}

// itoa avoids importing strconv solely for one call site's int-to-string
// conversion in a hot loop; %d via fmt.Sprintf is unnecessary machinery for
// a value that's always a small positive line number.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestNoOpenIssueVerificationCitesUndefinedTestFunction is this guard's
// real-corpus run: every open or in-progress issues/NNNN.md file's
// evidence-asserting sections (issueCitationSectionHeaders, and everything
// nested under them) must cite only Test… identifiers the tree actually
// defines. See the file-level comment for why resolved/closed/wontfix files
// are out of scope, and for the per-section measurement #0268 used to decide
// which sections those are.
func TestNoOpenIssueVerificationCitesUndefinedTestFunction(t *testing.T) {
	defined := collectDefinedGoTestFuncs(t, issueGuardScanRoots)
	failures := scanIssueDirForDanglingTestCitations(t, "../../issues", defined, func(status string) bool {
		return status == "open" || status == "in-progress"
	})
	if len(failures) > 0 {
		t.Fatalf("issue file cites a test function that no function in the tree defines — fix the citation or the function (see #0126, #0265):\n  %s",
			strings.Join(failures, "\n  "))
	}
}

// TestIssueVerificationCitationGuardCatchesPlantedFalseCitation is #0265's
// mutation proof, run against an isolated t.TempDir() fixture rather than a
// real issues/*.md file — per CLAUDE.md §8a a shared file another agent may
// be mid-edit on is not something to plant a failure into, even temporarily,
// and an isolated fixture proves the same mechanism without that risk.
func TestIssueVerificationCitationGuardCatchesPlantedFalseCitation(t *testing.T) {
	dir := t.TempDir()
	defined := map[string]bool{"TestRv0265PlantRealFunction": true}

	planted := "# 9999 — scratch fixture for #0265's mutation proof\n\n" +
		"| | |\n|---|---|\n| **Status** | open |\n\n" +
		"## Verification\n\n" +
		"`go test -run TestRv0265PlantRealFunction` passed. `TestRv0265PlantFictitious` was also cited here, and does not exist.\n"
	fixturePath := filepath.Join(dir, "9999.md")
	if err := os.WriteFile(fixturePath, []byte(planted), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	failures := scanIssueDirForDanglingTestCitations(t, dir, defined, func(status string) bool { return status == "open" })
	if len(failures) != 1 {
		t.Fatalf("expected exactly one dangling citation, got %d: %v", len(failures), failures)
	}
	if !strings.Contains(failures[0], "TestRv0265PlantFictitious") {
		t.Fatalf("expected the failure to name TestRv0265PlantFictitious, got %q", failures[0])
	}
	if !strings.Contains(failures[0], "9999.md:9:") {
		t.Fatalf("expected the failure to point at 9999.md:9, got %q", failures[0])
	}

	corrected := strings.Replace(planted, "TestRv0265PlantFictitious", "TestRv0265PlantRealFunction", 1)
	if err := os.WriteFile(fixturePath, []byte(corrected), 0o600); err != nil {
		t.Fatalf("write corrected fixture: %v", err)
	}
	failures = scanIssueDirForDanglingTestCitations(t, dir, defined, func(status string) bool { return status == "open" })
	if len(failures) != 0 {
		t.Fatalf("expected clean after correcting the citation, got %v", failures)
	}
}

// TestIssueVerificationCitationGuardExcludesResolvedFilesByDefault proves
// the status filter is what keeps the 161-citation resolved-file corpus (see
// the file-level comment) from failing this guard: a fixture with a
// dangling citation and Status resolved is not reported when includeStatus
// only admits open/in-progress, and the exact same fixture with Status
// changed to open is.
func TestIssueVerificationCitationGuardExcludesResolvedFilesByDefault(t *testing.T) {
	dir := t.TempDir()
	defined := map[string]bool{}
	body := "## Verification\n\n`TestRv0265PlantResolvedOnly` was cited but does not exist.\n"

	resolved := "# 9998 — scratch fixture\n\n| | |\n|---|---|\n| **Status** | resolved |\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, "9998.md"), []byte(resolved), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	failures := scanIssueDirForDanglingTestCitations(t, dir, defined, func(status string) bool {
		return status == "open" || status == "in-progress"
	})
	if len(failures) != 0 {
		t.Fatalf("resolved-status fixture should be out of scope, got %v", failures)
	}

	open := "# 9998 — scratch fixture\n\n| | |\n|---|---|\n| **Status** | open |\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, "9998.md"), []byte(open), 0o600); err != nil {
		t.Fatalf("rewrite fixture: %v", err)
	}
	failures = scanIssueDirForDanglingTestCitations(t, dir, defined, func(status string) bool {
		return status == "open" || status == "in-progress"
	})
	if len(failures) != 1 {
		t.Fatalf("open-status fixture with the same dangling citation should be in scope, got %v", failures)
	}
}

// TestIssueCitationExcludedDiscountRulesAreEachLoadBearing pins each of
// issueCitationExcluded's discount rules against a synthetic example built
// from string concatenation (never spelling a fictitious "Test…" name
// directly in a real comment in this file, matching #0196's own
// self-reference discipline — this file is not a _test.go file this guard
// itself scans, since it only scans issues/*.md, but the discipline costs
// nothing to keep). Each case additionally records the real corpus line the
// rule exists to cover, per the file-level comment's measurement.
//
// The two pipe cases and the two "narrowed pipe rule" cases together pin
// #0265's fix pass: a pipe-adjacent fragment is excluded only when it is a
// genuine substring of a defined test, never unconditionally. The three
// marker cases are, deliberately, still unconditional — see "go test FAIL
// marker (also the guard's known blind spot)" below and the file-level
// comment's account of why that one is not narrowed.
func TestIssueCitationExcludedDiscountRulesAreEachLoadBearing(t *testing.T) {
	fictitious := "Test" + "Rv0265DoesNotExist"
	// realSuperset stands in for #0007's real corpus line: a pipe fragment
	// that is a genuine substring of a longer defined function's name (not
	// spelled out here — see the file-level comment's pipe-rule bullet for
	// the real names). Using the same fictitious base keeps this fixture
	// from accidentally colliding with anything actually defined in the
	// tree.
	realSuperset := fictitious + "_RealSuffix"
	definedSet := map[string]bool{realSuperset: true}

	cases := []struct {
		name    string
		text    string
		target  string // the specific citation under test; several fixtures below contain more than one Test-shaped token (a second, unrelated name sharing the string with the target, e.g. in a -run alternation), so the case names its own target rather than relying on issueTestCitationPattern.FindStringIndex's first match, which is not necessarily the one the case means to exercise
		defined map[string]bool
	}{
		{"allowlisted TestSentAt", "the field `TestSentAt` is documented here", "TestSentAt", nil},
		{"allowlisted TestPool", "see the *TestPool convention", "TestPool", nil},
		{"trailing wildcard *", "`" + fictitious + "*` covers the family — real line: #0192's TestSPA*", fictitious, nil},
		{"trailing wildcard ...", "`" + fictitious + "...` covers the family — real line: #0025's TestDB_*", fictitious, nil},
		{"trailing pipe, substring of a defined test (narrowed rule's positive case)", "-run '" + fictitious + "|TestOther'", fictitious, definedSet},
		{"leading pipe, substring of a defined test (narrowed rule's positive case)", "-run 'TestOther|" + fictitious + "'", fictitious, definedSet},
		{"renamed from", "renamed from " + fictitious + "; the replacement covers it", fictitious, nil},
		{"go test FAIL marker (also the guard's known blind spot: this fictitious, undefined name is discounted here exactly as a real deleted scratch test's name would be — see the file-level comment's account of why this rule, unlike the pipe rule above, is not narrowed)", "printed `--- FAIL: " + fictitious + "`, then exit 0", fictitious, nil},
		{"go test PASS marker", "printed `--- PASS: " + fictitious + "`", fictitious, nil},
		{"go test RUN marker, wrapped", "printed `=== RUN\n  " + fictitious + "`", fictitious, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idx := strings.Index(c.text, c.target)
			if idx < 0 {
				t.Fatalf("target %q not found in %q", c.target, c.text)
			}
			start, end := idx, idx+len(c.target)
			if m := issueTestCitationPattern.FindStringIndex(c.text[start:]); m == nil || m[0] != 0 || start+m[1] != end {
				t.Fatalf("issueTestCitationPattern does not recognize %q at its position in %q as this exact span", c.target, c.text)
			}
			if !issueCitationExcluded(c.text, start, end, c.defined) {
				t.Errorf("expected %q to be excluded by a discount rule, it was not", c.text)
			}
		})
	}

	// #0265's fix pass: a pipe-adjacent fragment that is NOT a substring of
	// anything defined must no longer be excluded. Before the fix this was
	// discounted unconditionally — proved missed by planting exactly this
	// shape and confirming it passed silently; this case pins the fix.
	t.Run("pipe-adjacent but not a substring of any defined test (narrowed rule's real fix, was previously a silent miss)", func(t *testing.T) {
		text := "-run '" + fictitious + "|TestOtherReal'"
		idx := strings.Index(text, fictitious)
		if idx < 0 {
			t.Fatalf("target %q not found in %q", fictitious, text)
		}
		start, end := idx, idx+len(fictitious)
		if issueCitationExcluded(text, start, end, map[string]bool{"TestOtherReal": true}) {
			t.Errorf("expected %q to NOT be excluded — %q is not a substring of any defined test, so the narrowed pipe rule must not discount it", text, fictitious)
		}
	})

	// The mirror of the case above, with the fictitious fragment on the
	// LEFT of the pipe instead of the right, so both adjacency directions
	// are proved independently rather than assuming symmetry.
	t.Run("leading-pipe-adjacent but not a substring of any defined test (narrowed rule's real fix, other direction)", func(t *testing.T) {
		text := "-run 'TestOtherReal|" + fictitious + "'"
		idx := strings.Index(text, fictitious)
		if idx < 0 {
			t.Fatalf("target %q not found in %q", fictitious, text)
		}
		start, end := idx, idx+len(fictitious)
		if issueCitationExcluded(text, start, end, map[string]bool{"TestOtherReal": true}) {
			t.Errorf("expected %q to NOT be excluded — %q is not a substring of any defined test, so the narrowed pipe rule must not discount it", text, fictitious)
		}
	})

	// The pattern must still fire on the same fictitious name written
	// plainly, with none of the above shapes — this is the guard's whole
	// value, and every rule above must stay narrow enough not to swallow it.
	plain := "`" + fictitious + "` was cited and does not exist"
	loc := issueTestCitationPattern.FindStringIndex(plain)
	if loc == nil {
		t.Fatalf("pattern did not match the plain citation %q", plain)
	}
	if issueCitationExcluded(plain, loc[0], loc[1], nil) {
		t.Errorf("plain citation %q must not be excluded by any discount rule", plain)
	}
}

// TestIssueSectionTitleNormalizesRealCorpusHeadings pins issueSectionTitle
// against the heading spellings the tracker actually uses. Every "want"
// below that is non-empty is a key in issueCitationSectionHeaders, so this
// test is what makes the map's eight entries cover the corpus's many
// decorated variants rather than only their bare forms. The inputs are
// transcribed from real headings; a bare-form-only matcher would have missed
// every one of the decorated ones, which is how a fix pass's evidence went
// unscanned in the first place (#0268).
func TestIssueSectionTitleNormalizesRealCorpusHeadings(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"## Verification", "verification"},
		{"## Implementation notes", "implementation notes"},
		{"## Fix pass — 2026-08-25 (addressing the phase-3 bounce)", "fix pass"},
		{"## Review notes — phase 3 (Opus, 2026-08-24)", "review notes"},
		{"## Review notes — phase 3, second pass (Opus, 2026-08-25)", "review notes"},
		{"## Review verification — 2026-08-19 (phase 3, approved)", "review verification"},
		{"## Review findings (2026-08-18) — approved, with one correction", "review findings"},
		{"## Bounce fix (2026-08-23, claude-sonnet-5)", "bounce fix"},
		{"## Bounce-fix completion (third pass, 2026-08-19 — escalated to Opus)", "bounce-fix completion"},
		// Deliberately not admitted — see the file-level comment.
		{"## Description", "description"},
		{"## Notes", "notes"},
		{"## Plan", "plan"},
		{"## Manual verification required", "manual verification required"},
		// Not a level-2 heading at all.
		{"### Verification", ""},
		{"# 0265 — Issue files can cite tests that do not exist", ""},
		{"some prose", ""},
	}
	for _, c := range cases {
		if got := issueSectionTitle(c.in); got != c.want {
			t.Errorf("issueSectionTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, c := range cases {
		admitted := issueCitationSectionHeaders[issueSectionTitle(c.in)]
		switch c.want {
		case "description", "notes", "plan", "manual verification required", "":
			if admitted {
				t.Errorf("%q must not be scanned", c.in)
			}
		default:
			if !admitted {
				t.Errorf("%q must be scanned", c.in)
			}
		}
	}
}

// TestIssueCitationGuardScansEvidenceSectionsNotSubjectMatter is #0268's
// mechanism proof, on an isolated t.TempDir() fixture (CLAUDE.md §8a: never
// plant a failure into a shared file another agent may be mid-edit on). It
// asserts the two halves of #0268's scope decision at once:
//
//   - a fabricated name in a `### Verification` nested inside `## Fix pass`,
//     and one in a dated `## Review notes`, are both reported — neither was
//     visible to the guard before #0268;
//   - fabricated names in `## Description`, `## Notes` and `## Plan` are not
//     reported, because those sections quote and propose rather than assert.
//     Real issue files reproduce #0126's six false citations verbatim in
//     exactly that way.
func TestIssueCitationGuardScansEvidenceSectionsNotSubjectMatter(t *testing.T) {
	dir := t.TempDir()
	defined := map[string]bool{}

	fixture := "# 9997 — scratch fixture for #0268\n\n" +
		"| | |\n|---|---|\n| **Status** | in-progress |\n\n" +
		"## Description\n\nQuoting the bad citation as subject matter: `TestRv0268SubjectMatter`.\n\n" +
		"## Plan\n\nWill add `TestRv0268Proposed`, which does not exist yet.\n\n" +
		"## Fix pass — 2026-08-25 (a dated heading)\n\n" +
		"### Verification\n\nRan `TestRv0268NestedUnderFixPass` and it passed.\n\n" +
		"## Review notes — phase 3 (Opus, 2026-08-25)\n\nRe-ran `TestRv0268InReviewNotes` myself.\n\n" +
		"## Notes\n\nSee also `TestRv0268InNotes`.\n"
	if err := os.WriteFile(filepath.Join(dir, "9997.md"), []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	failures := scanIssueDirForDanglingTestCitations(t, dir, defined, func(status string) bool {
		return status == "open" || status == "in-progress"
	})
	joined := strings.Join(failures, "\n")
	for _, want := range []string{"TestRv0268NestedUnderFixPass", "TestRv0268InReviewNotes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %s to be reported, got:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{"TestRv0268SubjectMatter", "TestRv0268Proposed", "TestRv0268InNotes"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("%s sits in an unscanned section and must not be reported, got:\n%s", unwanted, joined)
		}
	}
	if len(failures) != 2 {
		t.Fatalf("expected exactly 2 dangling citations, got %d:\n%s", len(failures), joined)
	}
}

// TestIssueCitationLineWrappedNameResolvesOnlyWhenJoinedNameIsDefined pins
// isLineWrappedDefinedTestName in both directions: a name broken across a
// line is discounted when its halves join into a real function, and is still
// reported when they do not. The second half is what keeps the rule from
// being a hole — a wrapped fabrication stays visible.
func TestIssueCitationLineWrappedNameResolvesOnlyWhenJoinedNameIsDefined(t *testing.T) {
	defined := map[string]bool{"TestRv0268Wrapped_SecondHalfExists": true}

	cases := []struct {
		name         string
		text         string
		target       string
		wantExcluded bool
	}{
		{
			name:         "wrapped, joined name is defined",
			text:         "ran `TestRv0268Wrapped_\n  SecondHalfExists` and it passed",
			target:       "TestRv0268Wrapped_",
			wantExcluded: true,
		},
		{
			name:         "wrapped inside a blockquote, joined name is defined",
			text:         "> ran `TestRv0268Wrapped_\n>   SecondHalfExists` and it passed",
			target:       "TestRv0268Wrapped_",
			wantExcluded: true,
		},
		{
			name:         "wrapped, joined name is NOT defined",
			text:         "ran `TestRv0268Wrapped_\n  SomethingElseEntirely` and it passed",
			target:       "TestRv0268Wrapped_",
			wantExcluded: false,
		},
		{
			name:         "not wrapped at all",
			text:         "ran `TestRv0268Wrapped_` and it passed",
			target:       "TestRv0268Wrapped_",
			wantExcluded: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start := strings.Index(c.text, c.target)
			if start < 0 {
				t.Fatalf("fixture does not contain %q", c.target)
			}
			end := start + len(c.target)
			loc := issueTestCitationPattern.FindStringIndex(c.text[start:])
			if loc == nil || loc[0] != 0 || start+loc[1] != end {
				t.Fatalf("pattern does not match %q at the expected span in %q", c.target, c.text)
			}
			if got := issueCitationExcluded(c.text, start, end, defined); got != c.wantExcluded {
				t.Errorf("issueCitationExcluded = %v, want %v", got, c.wantExcluded)
			}
		})
	}
}
