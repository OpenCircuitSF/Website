package handlers

import (
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
// something other than a test function — a struct field the mailing
// subsystem's own comments describe next to its declaration in four
// separate files (admin_campaigns.go, mailing/campaigns.go,
// mailing/preflight.go, mailing/worker_store.go — none of them a
// _test.go file, all of them scanned by this guard's walk regardless).
// Extend this list if another such field turns up; don't loosen the
// pattern to admit it structurally, since the pattern's whole value is
// that a real dangling function citation has nowhere to hide behind it.
var nonTestIdentifierAllowlist = map[string]bool{
	"TestSentAt": true,
}

// citationIsExcluded narrows testNameCitationPattern's candidate matches
// down to the ones #0192's phase-3 sweep had to discount by hand, plus one
// more of the same shape this issue's own dry run turned up:
//
//   - a wildcard prefix reference naming a whole family rather than one
//     function — the character (or three characters) immediately after
//     the match is a literal '*' or "...". #0192's sweep found the '*'
//     form twice (seo_test.go, ses_notifications_test.go); this issue's
//     own run of the guard against the tree found the "..." form twice
//     more (audit_test.go's two references to
//     internal/middleware/auth_test.go's pair of collision-probe helpers,
//     each spelled "…Prefix..." rather than "…Prefix*") — same intent,
//     different punctuation, so both are recognised;
//   - the same '*' used the other way, as markdown-style emphasis around
//     the naming convention itself rather than around a family reference
//     — the character immediately BEFORE the match is '*'
//     (main_test.go's own summary comment uses this form once, wrapping
//     the bare suffix word the *TestPool convention is named after). The
//     \b requirement in testNameCitationPattern already keeps every ACTUAL
//     compound helper name (credsTestPool and its five siblings) out of
//     the candidate set on its own, since there is no word boundary
//     immediately before "Test" inside any of them; this second rule
//     exists only for the bare, asterisk-wrapped convention name itself,
//     which is never a defined function either way;
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
	if start > 0 && text[start-1] == '*' {
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
// each of roots, recursively, skipping vendored directories.
func walkGoFiles(t *testing.T, roots []string, fn func(path string)) {
	t.Helper()
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
			fn(path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
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
			lines := make([]string, len(group.List))
			positions := make([]token.Position, len(group.List))
			for i, c := range group.List {
				lines[i] = stripCommentMarkers(c.Text)
				positions[i] = fset.Position(c.Slash)
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

	poolWord := "Test" + "Pool"
	emphasisText := "each of this package's *" + poolWord + " helpers checks that"
	if !testCaseExcluded(t, emphasisText, poolWord) {
		t.Errorf("asterisk-emphasised %q: want excluded, got flagged", poolWord)
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
