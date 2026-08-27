package handlers

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// #0196: a Go doc comment naming a sibling test function is a pointer, and
// nothing checked that the pointer resolved. The shape has appeared three
// times now: the issue that started this one, the one a sweep fixed in the
// same package, and the dangling clause this file's own package review
// found and this issue's phase-2 pass repointed. Each was caught only
// because someone went looking by hand.
//
// #0181 solved the structurally identical problem for admin-facing string
// literals (see citation_guard_test.go), but its walk deliberately EXCLUDES
// _test.go files — a citation of an internal document has no business in a
// string an admin reads, and those only ever live in production code. A
// citation of a *test function*, by construction, only ever appears in a
// doc comment beside other test code — so this guard must scan _test.go
// files, and cannot reuse #0181's walk or its exclusion.
//
// That difference has a consequence #0181 did not have to worry about:
// this guard's own file IS a _test.go file the guard will scan, so any
// "Test<Name>"-shaped token this file writes in a real Go comment becomes
// a citation the guard must itself resolve. This file stays safe by
// construction rather than by care: TestNoCommentCitesUndefinedTestFunction
// (below) is the guard's own tree-wide run, this file included, and it is
// asserted clean as part of this issue's verification (not just left to
// pass by accident) — every "Test"-shaped token this file's comments
// contain is either the name of a real function defined in this same file
// (which resolves the ordinary way), a name on nonTestIdentifierAllowlist,
// or written with the wildcard/emphasis punctuation that
// citationIsExcluded discounts. The genuinely fictitious example names
// used to prove the guard fires live in Go string DATA inside test
// bodies (see TestDanglingTestCitationPatternCatchesSyntheticExample),
// never in a real comment this file's own parser would visit.

// testNameCitationPattern finds an identifier that looks like a cited Go
// test function: "Test" starting its own word, followed by a character
// that is NOT a lowercase ASCII letter, then the rest of the identifier.
// That constraint is `go test`'s own rule for recognizing a test function
// — a name where the rune right after the four-letter prefix is a
// lowercase letter is not treated as a test by the tool itself, only one
// where it is uppercase, a digit, or an underscore is. Two things fall out
// of borrowing that same rule here for free:
//
//   - the *TestPool naming-convention's compound helper names
//     (auditTestPool, signupTestPool, interestsTestPool, and friends —
//     main_test.go's own summary line names six of them) never match at
//     all: the leading \b requires a word boundary immediately before
//     "Test", and there is none between the lowercase letter that
//     precedes "Test" in each of those names and "Test" itself;
//   - the plain English word "Tests" (a section-heading comment,
//     "── Tests ──", in ses_notifications_test.go; ordinary prose,
//     "Tests here instead create rows…", in interests/store_test.go) also
//     never matches: the character right after "Test" is the lowercase
//     "s", which the tightened class excludes. Loosening this back to
//     "any character" reintroduces both false positives; go test's own
//     naming rule is why they don't collide with a real citation --  no
//     defined test function's name has a lowercase letter immediately
//     after "Test".
//
// citationIsExcluded (below) narrows the remaining candidates further for
// the shapes #0192's sweep had to discount by hand that survive both of
// the above.
var testNameCitationPattern = regexp.MustCompile(`\bTest[A-Z0-9_][A-Za-z0-9_]*\b`)

// nonTestIdentifierAllowlist is the discounted class that is a name, not a
// shape: an identifier that matches testNameCitationPattern but names
// something other than a test function.
//
//   - "TestSentAt" is a struct field the mailing subsystem's own comments
//     describe next to its declaration in four separate files
//     (admin_campaigns.go, mailing/campaigns.go, mailing/preflight.go,
//     mailing/worker_store.go — none of them a _test.go file, all of them
//     scanned by this guard's walk regardless).
//
//   - "TestPool" is the bare suffix word the *TestPool naming convention
//     (credsTestPool, interestsTestPool, and friends — see
//     testNameCitationPattern's own comment) is named after. It appears,
//     leading-asterisk-only (a '*' immediately before the name but no
//     closing '*' immediately after it — see below), in exactly three
//     real comments in the tree: main_test.go's summary line and this
//     file's own two uses of the convention name above. #0199 removed a
//     preceding-`*` discount rule that used to cover this by punctuation
//     rather than by name — and that rule was too broad: it silently
//     excluded ANY leading-asterisk-only citation, regardless of name —
//     so a genuinely dangling one written the same way (no closing
//     asterisk immediately after it) was invisible to the guard. That
//     shape is now caught — proved by the emphasisedDanglingName case in
//     TestDanglingTestCitationPatternExcludesDiscountedShapes, whose
//     fictitious name is built from string concatenation rather than
//     spelled out here, per this file's own opening comment on why a
//     genuinely dangling example must never appear as a real comment
//     token in this file. Naming the one word that actually needed it
//     here covers the same three real lines exactly.
//
//     It does NOT close the fully-emphasised markdown form "*Name*"
//     (an asterisk on both sides, right against the name) — a
//     genuinely dangling citation written that way is still missed,
//     because the SEPARATE trailing-`*` wildcard-family rule in
//     citationIsExcluded below fires on the closing asterisk
//     regardless of what removing the preceding-`*` rule did here. See
//     that rule's comment for why this stays open rather than being
//     narrowed.
//
// Extend this list if another such name turns up; don't loosen the
// pattern to admit it structurally, since the pattern's whole value is
// that a real dangling function citation has nowhere to hide behind it.
var nonTestIdentifierAllowlist = map[string]bool{
	"TestSentAt": true,
	"TestPool":   true,
}

// citationIsExcluded narrows testNameCitationPattern's candidate matches
// down to the ones #0192's phase-3 sweep had to discount by hand, plus one
// more of the same shape this issue's own dry run turned up. #0199 removed
// a fourth rule that used to live here — a punctuation-based markdown-
// emphasis discount — and replaced its one real use with a name on
// nonTestIdentifierAllowlist instead; see that variable's comment for why.
//
//   - a wildcard prefix reference naming a whole family rather than one
//     function — the character (or three characters) immediately after
//     the match is a literal '*' or "...". #0192's sweep found the '*'
//     form twice (seo_test.go, ses_notifications_test.go); this issue's
//     own run of the guard against the tree found the "..." form twice
//     more (audit_test.go's two references to
//     internal/middleware/auth_test.go's pair of collision-probe helpers,
//     each spelled "…Prefix..." rather than "…Prefix*") — same intent,
//     different punctuation, so both are recognised. The "..." form has a
//     blind spot with the same shape as the emphasis rule #0199 removed,
//     at much lower stakes: a placeholder like "// TODO: write
//     TestNotYetWritten... later" reads identically to a genuine
//     family-wildcard reference and is silently accepted (planted and
//     confirmed missed as part of #0199 — see #0199's implementation
//     notes). No instance of this exists in the tree today (checked as
//     part of this guard's own verification). It is not narrowed here,
//     because there is no local signal — punctuation or otherwise — that
//     tells "wildcard family reference" and "unwritten placeholder" apart;
//     if a real instance ever turns up, the fix is the same shape as
//     always: name it on nonTestIdentifierAllowlist, don't loosen this
//     rule. The same closing-'*' match has a second, unrelated
//     consequence: it also discounts genuine markdown emphasis "*Name*"
//     (an asterisk on both sides of the name), because at the point
//     this rule looks — one character past the match — that shape is
//     indistinguishable from the family-wildcard shape "Name*". A
//     dangling citation written that way is therefore silently accepted
//     too — pinned by the fullyEmphasisedDanglingName case in
//     TestDanglingTestCitationPatternExcludesDiscountedShapes, whose
//     fictitious name is likewise built from string concatenation
//     rather than spelled out here. This is
//     not narrowed either, for the same reason as the "..." case above:
//     nothing local distinguishes "closes a wildcard family" from
//     "closes markdown emphasis", and #0196 mutation-tested this rule
//     as narrow and load-bearing (TestFetchSubscribeURLReal_*,
//     TestRender_*), so it is not the rule to change.
//     nonTestIdentifierAllowlist's TestPool entry closes only the
//     LEADING-asterisk-only shape (no closing '*' right after the
//     name) — this fully-emphasised "*Name*" shape is a distinct,
//     still-open gap;
//   - a name the comment honestly documents as retired — the phrase
//     "renamed from" anywhere in the twenty characters immediately before
//     the match (case-insensitive; templates_test.go carries the one
//     instance #0192's sweep found). The phrase and the name it
//     introduces there sit on adjacent comment lines, not the same one —
//     the twenty-character window is taken from the joined multi-line
//     text built in collectTestCitations, so the line break between them
//     doesn't hide it;
//   - a name on nonTestIdentifierAllowlist.
//
// Subtests are a deliberate non-decision made unnecessary rather than an
// oversight: a comment citing a parent function's name followed by a
// slash and a subtest name (e.g.
// "TestDanglingTestCitationPatternExcludesDiscountedShapes/some_case")
// still matches testNameCitationPattern on the parent function's name
// alone, because '/' is not an identifier character and stops the match
// there — so a subtest reference resolves correctly whenever its parent
// function does, with no special case. What this guard does NOT attempt
// is validating that the subtest half after the slash actually exists:
// subtest names are frequently built at run time (t.Run(c.name, ...) over
// a table, go test's own space-to-underscore substitution) rather than
// being static identifiers, so there is no fixed set to check them
// against the way there is for top-level functions. A comment citing a
// bare subtest name that happens to start with "Test" but is not itself a
// parent function's name (no instance of this exists in the tree today —
// checked as part of this guard's own verification) would be a false
// positive this guard does not yet discount; if one appears, the fix is a
// new entry in this list, not a change to the pattern.
func citationIsExcluded(text string, start, end int) bool {
	name := text[start:end]
	if nonTestIdentifierAllowlist[name] {
		return true
	}
	if end < len(text) && text[end] == '*' {
		return true
	}
	if end+3 <= len(text) && text[end:end+3] == "..." {
		return true
	}
	windowStart := start - 20
	if windowStart < 0 {
		windowStart = 0
	}
	if strings.Contains(strings.ToLower(text[windowStart:start]), "renamed from") {
		return true
	}
	return false
}

// citedTestScanRoots mirrors #0181's scanGoRoots (internal/handlers's own
// package via ".." plus cmd/ and web/, so every package that could hold a
// doc comment is covered in one pass) but is declared separately in this
// file rather than reused from citation_guard_test.go: that file is being
// edited concurrently for #0193, and this guard has a different walk
// (_test.go files included, since a test-name citation only ever appears
// beside other test code) so sharing the variable would not save anything
// and would only create a collision.
var citedTestScanRoots = []string{"..", "../../cmd", "../../web"}

// citedTestScanRootsMinPlausibleFileCount is the #0275 floor for every
// guard that walks citedTestScanRoots and parses everything walkGoFiles
// yields it (test files included, nothing skipped afterward — unlike the
// audit-metadata guard, so walkGoFiles' own return value already IS the
// parsed count for these three; see criterion 4a). Measured directly, not
// fitted: `find .. ../../cmd ../../web -name '*.go' -not -path
// '*/node_modules/*' -not -path '*/dist/*'` counts 255 files under these
// roots today, and the single largest package under them (internal/handlers
// itself, this guard's own directory) has 96 — cmd/ alone has 18, web/
// alone has 1. 150 sits comfortably below the real total (room for the
// tree to shrink without a false alarm) while still tripping if the roots
// were narrowed to any one of the three, which is the "a real narrowing
// would trip it" bar #0275 criterion 3 sets. Reproduced directly before
// adding this floor: emptying citedTestScanRoots made all three guards
// that share it (TestNoCommentCitesUndefinedTestFunction,
// TestNoCommentCitesUnresolvedPathOrSection,
// TestNoDocCommentNamesADifferentDeclarationInSameFile) PASS in under
// 0.01s each, having examined nothing.
const citedTestScanRootsMinPlausibleFileCount = 150

// skipVendoredDir reports whether dirName should be pruned from the walk
// entirely. #0194 is open against #0181's guard for descending into
// web/node_modules and web/dist, where a parse failure is a t.Fatalf that
// can take the whole guard down over a vendored or generated file this
// guard has no business scanning. Pruned from the start here rather than
// filtered after the fact.
func skipVendoredDir(dirName string) bool {
	return dirName == "node_modules" || dirName == "dist"
}

// walkGoFiles calls fn with the path of every .go file (test files
// included — the one difference from #0181's scanDirForCitations) under
// each of roots, recursively, skipping vendored directories. It returns the
// number of .go files it yielded to fn — #0275: a guard whose pass
// condition is "the findings slice is empty" cannot on its own tell "the
// tree is clean" apart from "the walk visited nothing" (an empty roots
// list is not an error to filepath.WalkDir, it is simply nothing to do), so
// every caller that wants to guard against that must have this count
// available. See assertGoFileVisitCountPlausible below, which is what
// actually turns the count into a failure.
func walkGoFiles(t *testing.T, roots []string, fn func(path string)) int {
	t.Helper()
	visited := 0
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if skipVendoredDir(entry.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".go") {
				return nil
			}
			visited++
			fn(path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return visited
}

// goFileVisitCountImplausible is the pure #0275 check, deliberately
// separated from assertGoFileVisitCountPlausible below so it can be tested
// directly (TestGoFileVisitCountGuardFiresOnEmptyOrLowCount) without a real
// *testing.T standing in for a synthetic failure: calling t.Fatalf from a
// helper and then asserting the OUTER test still passed is not something a
// *testing.T supports cleanly, so the failure decision lives here as an
// ordinary function returning "" (plausible) or a reason (not plausible),
// and assertGoFileVisitCountPlausible just reports whatever this returns.
//
// Two conditions, matching #0275's two acceptance criteria: roots itself
// must be non-empty (criterion 4 — an emptied scan-roots variable is a
// hard failure, not a walk over nothing that reports "no findings"), and
// got — the number of files the CALLER ACTUALLY EXAMINED, not merely
// walked past — must clear floor (criterion 3). Per criterion 4a, callers
// that filter what they parse after walkGoFiles yields a path (today, only
// the audit-metadata guard, which skips _test.go) must pass the count of
// what they parsed, not walkGoFiles' own raw yield count, or the floor is
// sized against a population larger than the coverage it protects — this
// is exactly the gap #0261's review measured (247 walked vs. 108 parsed)
// and this issue exists to close.
func goFileVisitCountImplausible(guardName string, roots []string, got, floor int) string {
	if len(roots) == 0 {
		return fmt.Sprintf("%s: scan roots are empty — this guard would silently check nothing (#0275)", guardName)
	}
	if got < floor {
		return fmt.Sprintf("%s: the walk over %v examined only %d .go file(s) — expected at least %d; the scan roots may have been emptied or narrowed, which would silently disarm this guard rather than fail it (#0275)", guardName, roots, got, floor)
	}
	return ""
}

// assertGoFileVisitCountPlausible is the shared #0275 guard every one of
// this file's five siblings (this guard, TestNoCommentCitesUnresolvedPathOrSection,
// TestNoDocCommentNamesADifferentDeclarationInSameFile,
// TestNoAdminFacingStringCitesInternalDocs, and
// TestAuditEntryEmailMetadataMatchesKnownSites) calls before trusting
// anything its own scan reports — the one place, per #0275 criterion 4,
// that a scan-roots variable's emptiness is checked, so five copies of the
// same check do not drift out of the same shared place the way the walk
// itself already is shared.
func assertGoFileVisitCountPlausible(t *testing.T, guardName string, roots []string, got, floor int) {
	t.Helper()
	if reason := goFileVisitCountImplausible(guardName, roots, got, floor); reason != "" {
		t.Fatal(reason)
	}
}

// TestGoFileVisitCountGuardFiresOnEmptyOrLowCount is #0275 criterion 5's
// proof: it calls goFileVisitCountImplausible directly (the "via the
// helper's parameter" option the criterion names) with an empty roots list
// and with a plausible-looking roots list paired with a too-low count, and
// asserts each is reported implausible — then asserts a count that clears
// the floor is reported plausible, so this test cannot pass by having the
// function always return a non-empty string. Per CLAUDE.md §8, the oracle
// here is not a copy of any guard's own floor constant — the numbers below
// (0, 3, 150) are chosen for this test alone, so a change to e.g.
// auditEmailMetadataMinPlausibleFileCount cannot make this test agree with
// itself regardless of whether goFileVisitCountImplausible still works.
func TestGoFileVisitCountGuardFiresOnEmptyOrLowCount(t *testing.T) {
	if reason := goFileVisitCountImplausible("ExampleGuard", nil, 0, 150); reason == "" {
		t.Fatal("expected an empty roots list to be reported implausible, got no reason")
	}
	if reason := goFileVisitCountImplausible("ExampleGuard", []string{"../../cmd"}, 3, 150); reason == "" {
		t.Fatal("expected a visited count under the floor to be reported implausible, got no reason")
	}
	if reason := goFileVisitCountImplausible("ExampleGuard", []string{"..", "../../cmd", "../../web"}, 150, 150); reason != "" {
		t.Fatalf("expected a visited count meeting the floor with non-empty roots to be plausible, got: %s", reason)
	}
}

// collectDefinedTestFuncs returns the set of top-level (no receiver)
// function names starting with "Test" defined anywhere under roots. This
// is the tree's actual ground truth: a citation resolves if and only if
// its name is in this set.
func collectDefinedTestFuncs(t *testing.T, roots []string) map[string]bool {
	t.Helper()
	defined := map[string]bool{}
	walkGoFiles(t, roots, func(path string) {
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
	})
	return defined
}

type testCitation struct {
	pos  token.Position
	name string
}

// stripCommentMarkers removes the "//" or "/* */" markers from one
// *ast.Comment's raw Text, leaving the content with its original spacing
// intact (unlike ast.CommentGroup.Text(), which is not used here because
// it drops the per-line position information this guard's failure message
// needs).
func stripCommentMarkers(text string) string {
	if strings.HasPrefix(text, "//") {
		return strings.TrimPrefix(text, "//")
	}
	if strings.HasPrefix(text, "/*") {
		return strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
	}
	return text
}

// collectTestCitations returns every testNameCitationPattern match found
// in a comment anywhere under roots, minus the shapes citationIsExcluded
// discounts, given the tree's ground truth (defined, from
// collectDefinedTestFuncs) to resolve one ambiguity a text-only pass
// cannot: whether a line break inside a comment falls INSIDE a compound
// identifier that continues on the next line with no separator (main.go
// and admin_campaign_preview_test.go each wrap a real function name this
// way; seo_test.go wraps a third one mid-camel-word, "…UnknownPathsShare"
// / "OneCacheEntry…", with no underscore or other punctuation marking the
// seam at all) or is an ordinary doc-comment sentence that happens to
// wrap right after a complete function name ("…NotARealSubscriber" ending
// a line, "is the recipient…" starting the next — joining THAT with zero
// separation would read as one word that cites nothing). There is no
// punctuation-based rule that tells the two apart — this issue's own dry
// run tried one (a trailing '_') and it correctly reconstructed the first
// two real cases while missing the third, which doesn't end in '_'. So
// this asks the only question that actually distinguishes them: does the
// zero-separator join produce a name the tree defines? If yes, it is
// accepted as the real citation and the truncated line-i fragment the
// primary pass would otherwise also report is suppressed (that fragment
// was never written as its own name — it is the first half of one). If
// no, the join is discarded entirely and the primary pass's own reading
// of each line stands, which is what correctly leaves
// "…NotARealSubscriber" resolved on its own.
//
// This makes the seam pass's answer depend on what the tree currently
// defines, not on anything local to the comment being read — and #0199
// found the one place that coupling actually bites: when the line-i
// fragment of a wrap is ITSELF a name defined[] already recognises (not
// the full wrapped name — just the piece up to the seam), the "does the
// join produce a defined name" question above is moot, because the
// primary pass below has already resolved that fragment as a complete
// citation on its own. The seam pass never runs, the line-i+1 remainder is
// never looked at, and a citation that was genuinely dangling disappears
// with no punctuation difference from an ordinary caught one — proved by
// planting one (see #0199's implementation notes). This needs a defined
// function name that is a strict prefix of another defined function's
// name, wrapped exactly at that boundary, which is contrived today — but
// six such strict-prefix pairs already exist among the tree's defined test
// functions (e.g. TestDeleteSessionsForUser is a strict prefix of
// TestDeleteSessionsForUser_RemovesAllAndIsIdempotent), so the count of
// candidates only grows as the tree does; nothing here re-checks it. The
// alternatives are worse and already tried: a punctuation-based seam rule
// (a trailing '_') failed on seo_test.go's real mid-camel-word wrap, and
// merging unconditionally produces the "…NotARealSubscriber" + "is" false
// positive above. So the coupling stays, and this paragraph exists so a
// future reader does not have to rediscover why.
func collectTestCitations(t *testing.T, roots []string, defined map[string]bool) []testCitation {
	t.Helper()
	var citations []testCitation
	walkGoFiles(t, roots, func(path string) {
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, group := range file.Comments {
			// A "//" *ast.Comment always holds exactly one physical line,
			// so one entry in group.List already means one entry here. A
			// "/* ... */" *ast.Comment can span many physical lines in a
			// single Text, all reported at one Slash position — #0199: a
			// citation on any line but the block's first was reported at
			// the block's OPENING line, off by however many lines separate
			// them. Splitting each comment's stripped text on its own
			// internal newlines and advancing the line number once per
			// split fixes both shapes with the same code: a "//" comment's
			// stripped text has no "\n" in it, so the split is a no-op and
			// nothing changes there.
			var lines []string
			var positions []token.Position
			for _, c := range group.List {
				base := fset.Position(c.Slash)
				stripped := stripCommentMarkers(c.Text)
				for j, part := range strings.Split(stripped, "\n") {
					pos := base
					pos.Line += j
					lines = append(lines, part)
					positions = append(positions, pos)
				}
			}

			// suppressFragment[i] holds the byte offset, within lines[i],
			// where an accepted cross-line merge's fragment begins — the
			// primary pass skips a match that starts there and runs to
			// the end of line i, since the seam pass below already
			// reports the merge's full, real name instead.
			suppressFragment := make(map[int]int)
			for i := 0; i+1 < len(lines); i++ {
				left := lines[i]
				right := strings.TrimPrefix(lines[i+1], " ")
				merged := left + right
				seam := len(left)
				for _, m := range testNameCitationPattern.FindAllStringIndex(merged, -1) {
					start, end := m[0], m[1]
					if start >= seam || end <= seam {
						continue // wholly on one side of the break — the primary pass already reads this correctly
					}
					name := merged[start:end]
					if !defined[name] {
						continue // not a real name once joined — an ordinary sentence wrap, not a split identifier
					}
					if citationIsExcluded(merged, start, end) {
						continue
					}
					citations = append(citations, testCitation{pos: positions[i], name: name})
					suppressFragment[i] = start
				}
			}

			type span struct {
				start int
				pos   token.Position
				index int
			}
			var b strings.Builder
			spans := make([]span, 0, len(lines))
			for i, line := range lines {
				spans = append(spans, span{start: b.Len(), pos: positions[i], index: i})
				b.WriteString(line)
				b.WriteByte('\n')
			}
			text := b.String()

			for _, m := range testNameCitationPattern.FindAllStringIndex(text, -1) {
				start, end := m[0], m[1]
				if citationIsExcluded(text, start, end) {
					continue
				}
				sp := spans[0]
				for _, s := range spans {
					if s.start > start {
						break
					}
					sp = s
				}
				relStart := start - sp.start
				if fragStart, ok := suppressFragment[sp.index]; ok && relStart == fragStart && end < len(text) && text[end] == '\n' {
					continue
				}
				citations = append(citations, testCitation{pos: sp.pos, name: text[start:end]})
			}
		}
	})
	return citations
}

// toRepoRelativePath matches #0187's fix to the other guard's failure
// message: a form every reader can paste into a search regardless of
// where the repo is checked out, not the absolute path token.Position
// produces on its own.
func toRepoRelativePath(repoRoot, absPath string) string {
	rel, err := filepath.Rel(repoRoot, absPath)
	if err != nil {
		return absPath
	}
	return filepath.ToSlash(rel)
}

// TestNoCommentCitesUndefinedTestFunction is the guard: it fails if any Go
// comment anywhere in the tree (test files included) cites a
// "Test<Name>"-shaped identifier that no top-level function in the tree
// defines, after excluding the shapes citationIsExcluded discounts.
func TestNoCommentCitesUndefinedTestFunction(t *testing.T) {
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

	// #0275: assert the walk actually visited a plausible number of files
	// BEFORE trusting anything collectDefinedTestFuncs/collectTestCitations
	// report below — an empty or narrowed citedTestScanRoots must be a hard
	// failure here, never silently read as "no dangling citations found".
	// This guard parses every file walkGoFiles yields (no _test.go skip, by
	// design — see this file's header), so a dedicated counting pass over
	// the same roots measures exactly the population collectDefinedTestFuncs
	// and collectTestCitations go on to parse.
	visited := walkGoFiles(t, roots, func(path string) {})
	assertGoFileVisitCountPlausible(t, "TestNoCommentCitesUndefinedTestFunction", citedTestScanRoots, visited, citedTestScanRootsMinPlausibleFileCount)

	defined := collectDefinedTestFuncs(t, roots)
	citations := collectTestCitations(t, roots, defined)

	var missing []testCitation
	for _, c := range citations {
		if !defined[c.name] {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].pos.Filename != missing[j].pos.Filename {
			return missing[i].pos.Filename < missing[j].pos.Filename
		}
		return missing[i].pos.Line < missing[j].pos.Line
	})
	var b strings.Builder
	b.WriteString("comment cites a test function that no function in the tree defines — fix the citation or the function (see #0190, #0192, #0196):\n")
	for _, m := range missing {
		b.WriteString("  " + toRepoRelativePath(repoRoot, m.pos.Filename) + ":" + strconv.Itoa(m.pos.Line) + ": " + m.name + "\n")
	}
	t.Error(b.String())
}

// TestDanglingTestCitationPatternCatchesSyntheticExample proves the guard
// actually fires, against a synthetic source string rather than the real
// tree — so this proof does not depend on there being a live dangling
// citation to point at (there is deliberately never one committed). Both
// the citing comment and the fictitious cited name below are built from
// string concatenation of ordinary words, not written as one
// "Test"-prefixed literal token — so this fixture is itself a self-check:
// TestNoCommentCitesUndefinedTestFunction never sees this string, because
// it is Go string DATA inside this _test.go file's source, not a comment
// this file's own parser will visit when the real guard runs. The
// resulting name (see synthesized below) does not collide with any real
// function in the tree.
func TestDanglingTestCitationPatternCatchesSyntheticExample(t *testing.T) {
	fictitiousName := "Test" + "FictitiousExampleForGuardSelfProof"
	src := "package fixture\n\n" +
		"// see " + fictitiousName + "'s doc comment for the rationale\n" +
		"func Example() {}\n"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	var found []string
	for _, group := range file.Comments {
		for _, m := range testNameCitationPattern.FindAllString(group.Text(), -1) {
			found = append(found, m)
		}
	}
	if len(found) != 1 || found[0] != fictitiousName {
		t.Fatalf("expected exactly one citation of %q, got %v", fictitiousName, found)
	}
	// The fixture never defines fictitiousName as a function, so a
	// tree-wide run that included this file would correctly report it
	// missing — which is exactly why the fixture lives in a string, not a
	// real comment.
}

// TestDanglingTestCitationPatternExcludesDiscountedShapes proves
// citationIsExcluded's four discount classes directly, one assertion per
// class, using the exact wording #0192's sweep found in the real tree
// (reproduced as literal strings here, not read from the files, so this
// test does not depend on that wording staying put) plus one control case
// that must NOT be excluded.
func TestDanglingTestCitationPatternExcludesDiscountedShapes(t *testing.T) {
	wildcardPrefix := "Test" + "Render_"
	wildcardText := "the placeholder-template-only tests above (" + wildcardPrefix + "*) cannot"
	if !testCaseExcluded(t, wildcardText, wildcardPrefix) {
		t.Errorf("wildcard-suffixed reference %q*: want excluded, got flagged", wildcardPrefix)
	}

	// #0199: this used to be excluded by a preceding-'*' punctuation rule
	// that discounted ANY markdown-emphasised citation. That rule is gone;
	// "TestPool" is now excluded because it is on nonTestIdentifierAllowlist
	// by name, not because of the asterisks around it.
	poolWord := "Test" + "Pool"
	emphasisText := "each of this package's *" + poolWord + " helpers checks that"
	if !testCaseExcluded(t, emphasisText, poolWord) {
		t.Errorf("allowlisted %q written with markdown emphasis: want excluded, got flagged", poolWord)
	}

	// #0199, the other direction: a DIFFERENT name written with the same
	// single-leading-asterisk emphasis main_test.go's real TestPool use has
	// (no closing '*' immediately after the name — a trailing '*' right
	// after the match is the SEPARATE, still-live wildcard-family rule
	// above, and would exclude it regardless of this one) is no longer
	// swallowed by punctuation alone. This is the dangling citation the old
	// preceding-'*' rule used to hide.
	emphasisedDanglingName := "Test" + "RenderSomethingThatGotRenamedAway"
	emphasisedDanglingText := "each of this package's *" + emphasisedDanglingName + " helpers checks that"
	if testCaseExcluded(t, emphasisedDanglingText, emphasisedDanglingName) {
		t.Errorf("markdown-emphasised dangling citation %q: want NOT excluded now that the preceding-'*' rule is gone, got excluded", emphasisedDanglingName)
	}

	// #0199's bounce: the FULLY-emphasised form "*Name*" (an asterisk on
	// BOTH sides of the name, unlike emphasisedDanglingName above which has
	// only a leading one) is a distinct case the allowlist change above does
	// NOT close. It stays excluded — genuinely dangling or not — because the
	// trailing-'*' wildcard-family rule tested at the top of this function
	// fires on the closing asterisk regardless of what removed the
	// preceding-'*' rule. This pins that gap rather than only describing it;
	// see citationIsExcluded's wildcard-rule comment for why it is not
	// narrowed.
	fullyEmphasisedDanglingName := "Test" + "RenderSomethingElseThatGotRenamedAway"
	fullyEmphasisedDanglingText := "see *" + fullyEmphasisedDanglingName + "* for the rationale"
	if !testCaseExcluded(t, fullyEmphasisedDanglingText, fullyEmphasisedDanglingName) {
		t.Errorf("fully markdown-emphasised dangling citation %q (asterisks on both sides): want excluded by the trailing-'*' wildcard rule, got flagged", fullyEmphasisedDanglingName)
	}

	renamedName := "Test" + "ConfirmationAndAlreadySubscribed_NoCampaignHeaders"
	renamedText := "the predecessor version of this test (renamed from\n" + renamedName + ") checked"
	if !testCaseExcluded(t, renamedText, renamedName) {
		t.Errorf("renamed-from-documented name %q: want excluded, got flagged", renamedName)
	}

	if !testCaseExcluded(t, "field is "+"TestSentAt"+" on the campaign row", "TestSentAt") {
		t.Errorf("allowlisted identifier %q: want excluded, got flagged", "TestSentAt")
	}

	controlName := "Test" + "GenuinelyUndefinedControlCase"
	controlText := "see " + controlName + "'s doc comment"
	if testCaseExcluded(t, controlText, controlName) {
		t.Errorf("plain citation %q: want NOT excluded, got excluded", controlName)
	}
}

// testCaseExcluded is a small helper for the table above: it locates name
// inside text via testNameCitationPattern and reports whether
// citationIsExcluded discounts that occurrence.
func testCaseExcluded(t *testing.T, text, name string) bool {
	t.Helper()
	loc := testNameCitationPattern.FindStringIndex(text)
	if loc == nil {
		t.Fatalf("fixture text %q: pattern did not match %q at all", text, name)
	}
	if text[loc[0]:loc[1]] != name {
		t.Fatalf("fixture text %q: pattern matched %q, want %q", text, text[loc[0]:loc[1]], name)
	}
	return citationIsExcluded(text, loc[0], loc[1])
}

// TestDanglingTestCitationPatternReconstructsGenuineLineWrap is a direct,
// isolated proof of collectTestCitations' seam pass — the mechanism that
// resolves main.go's, admin_campaign_preview_test.go's, and seo_test.go's
// real citations — against a throwaway package on disk (t.TempDir(), not
// this repo's own tree, so this proof does not depend on those three
// comments staying worded the way they are today). One function's name is
// split across a line break with no separator at the seam and must
// resolve as its full, whole name; a second function's doc comment wraps
// an ordinary sentence right after its own complete name and must resolve
// as that name ALONE, not merged with the next line's leading word.
func TestDanglingTestCitationPatternReconstructsGenuineLineWrap(t *testing.T) {
	dir := t.TempDir()
	const src = `package fixture

// TestSplitAcrossTheLine_
// ContinuesHereWithNoSeparator is cited below by its whole, correctly
// reconstructed name: TestSplitAcrossTheLine_
// ContinuesHereWithNoSeparator.
func TestSplitAcrossTheLine_ContinuesHereWithNoSeparator(t *T) {}

// TestOrdinaryDocWrap_EndsCleanly is an ordinary Go doc comment whose
// sentence happens to wrap right after the complete function name — the
// next line begins a normal English continuation, not the rest of an
// identifier.
func TestOrdinaryDocWrap_EndsCleanly(t *T) {}

type T struct{}
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	roots := []string{dir}
	defined := collectDefinedTestFuncs(t, roots)
	if !defined["TestSplitAcrossTheLine_ContinuesHereWithNoSeparator"] {
		t.Fatalf("fixture setup: expected the split name to be defined, got %v", defined)
	}
	if !defined["TestOrdinaryDocWrap_EndsCleanly"] {
		t.Fatalf("fixture setup: expected the wrapped-doc name to be defined, got %v", defined)
	}

	citations := collectTestCitations(t, roots, defined)
	names := map[string]bool{}
	for _, c := range citations {
		names[c.name] = true
	}

	if !names["TestSplitAcrossTheLine_ContinuesHereWithNoSeparator"] {
		t.Errorf("expected the seam pass to reconstruct the split name; got citations %v", names)
	}
	if names["TestSplitAcrossTheLine_"] {
		t.Errorf("the truncated line-1 fragment of the split name should have been suppressed, not reported on its own; got citations %v", names)
	}
	if !names["TestOrdinaryDocWrap_EndsCleanly"] {
		t.Errorf("expected the ordinary wrapped doc comment's complete name to resolve on its own; got citations %v", names)
	}
	for name := range names {
		if strings.HasPrefix(name, "TestOrdinaryDocWrap_EndsCleanly") && name != "TestOrdinaryDocWrap_EndsCleanly" {
			t.Errorf("the ordinary doc-comment wrap must not merge with the next line's leading word; got %q", name)
		}
	}

	var missing []string
	for name := range names {
		if !defined[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		t.Errorf("fixture should resolve cleanly end to end, found undefined citations: %v", missing)
	}
}

// TestDanglingTestCitationPatternReportsBlockCommentLineNotItsOpeningLine is
// a direct, isolated proof of #0199's fix to a "/* ... */" line-number bug:
// before the fix, a citation anywhere inside a multi-line block comment was
// reported at the comment's OPENING line (the *ast.Comment's single Slash
// position), no matter which physical line inside the block actually held
// the citation — because collectTestCitations built one lines[]/positions[]
// entry per *ast.Comment, and a whole multi-line block comment is a single
// *ast.Comment. Latent in the real tree today — five multi-line block
// comments exist there and none carries a citation — but the failure
// message exists to be pasted into a search (#0187 established that for
// the sibling guard), so it must name the citation's own line, not the
// block's.
func TestDanglingTestCitationPatternReportsBlockCommentLineNotItsOpeningLine(t *testing.T) {
	dir := t.TempDir()
	citedName := "Test" + "BlockCommentPlantedCitation"
	const blockOpeningLine = 3   // the line the "/*" itself sits on
	const citationLine = 6       // the line the planted name actually sits on
	src := "package fixture\n" + // line 1
		"\n" + // line 2
		"/*\n" + // line 3 — blockOpeningLine
		"Block comment opening line.\n" + // line 4
		"Second line, still prose.\n" + // line 5
		"Citation on this very line: " + citedName + " is dangling.\n" + // line 6 — citationLine
		"Fourth line, wraps up.\n" + // line 7
		"*/\n" + // line 8
		"func Example() {}\n" // line 9
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	roots := []string{dir}
	// citedName is deliberately never defined anywhere in this fixture —
	// this test only cares about the LINE a citation of it is reported at,
	// which collectTestCitations reports regardless of whether the name
	// resolves.
	defined := collectDefinedTestFuncs(t, roots)
	citations := collectTestCitations(t, roots, defined)

	var found *testCitation
	for i := range citations {
		if citations[i].name == citedName {
			found = &citations[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a citation of %q, got %v", citedName, citations)
	}
	if found.pos.Line == blockOpeningLine {
		t.Fatalf("citation %q reported at the block's opening line (%d) instead of its own line (%d) — the #0199 bug is back", citedName, blockOpeningLine, citationLine)
	}
	if found.pos.Line != citationLine {
		t.Errorf("citation %q: want line %d, got %d", citedName, citationLine, found.pos.Line)
	}
}
