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
// against the real corpus: all 266 `issues/NNNN.md` files, both
// `## Verification` and `## Implementation notes` sections, 1,742 raw
// `Test…`-shaped tokens. After the discount rules — the wildcard family
// (`#0196`'s trailing `*`/`...`), a `renamed from` note, a `-run`-style
// regex-alternation fragment (adjacent `|`), the `TestSentAt`/`TestPool`
// allowlist, and one new rule this corpus required (below) — 87 citations
// remained unresolved against the tree. **All 87 are in `resolved` issue
// files. Zero are in `open` or `in-progress` files.**
//
// That is not a coincidence of a small sample; it is the project's own
// mutation-testing convention (`CLAUDE.md` §8b, §8a: copy a file aside,
// plant a failure, observe it, restore) working exactly as intended. Issues
// #0199, #0208, #0209, #0211, #0212, #0215, #0251, #0252, #0255, #0258 and
// others document a scratch or plant test file created to *prove a guard
// fires*, exercised once, and deleted — its name genuinely does not exist in
// the tree today, and was never meant to. Scanning `resolved` files
// unconditionally would fail this guard at introduction on ~87 lines of
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
// written, false now") is real — a handful of the 87 are genuine stale
// citations surviving a later rename (#0028 cites the pre-rename confirmation
// test name that #0196's own guard commentary documents as later renamed —
// not spelled out here, since it is exactly the dangling shape this file
// itself must not cite in a real comment; see #0196's Notes for the name) —
// but it is a minority of the 87, buried in scratch-test noise, and catching it
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
// TestSentAt/TestPool allowlist), plus two this corpus specifically
// required and neither #0196 nor #0199 needed, because a markdown
// `## Verification` section quotes shell and go-test output in ways a Go
// doc comment never does:
//
//   - **A `|` immediately before or after the match.** `## Verification`
//     routinely quotes a `go test -run 'A|B|C'` invocation, where each
//     name is a regexp alternative matched by substring, not a claim that
//     A, B and C are themselves defined functions (#0007's own -run
//     invocation, naming a construction-path test alongside two others
//     joined by `|`, is the real instance this rule exists for — the first
//     fragment there is itself only a substring of the real function's
//     name, matched by go test's unanchored -run regexp, never a citation
//     of a function by that exact shorter name). This is the
//     markdown-specific sibling of the wildcard-family rule above; it
//     discounts the same intent — "this name stands for a family/fragment,
//     not itself" — written with different punctuation because it's
//     quoting a shell command rather than prose.
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
// Neither new rule is hypothetical — both were added because the measured
// corpus above required them to reach zero false positives in the
// unresolved scope this guard actually enforces, and removing either
// reopens exactly the real line that motivated it (proved directly in
// TestIssueCitationExcludedDiscountRulesAreEachLoadBearing).

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

// issueCitationExcluded reports whether the Test-shaped match at
// text[start:end] should be discounted rather than resolved against the
// tree. See the file-level comment for what each rule is for and the real
// corpus line it exists to cover.
func issueCitationExcluded(text string, start, end int) bool {
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
	if end < len(text) && text[end] == '|' {
		return true
	}
	if start > 0 && text[start-1] == '|' {
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

// issueCitationSectionHeaders are the two headers this guard resolves
// citations under, per #0265's acceptance criteria. "## Plan" is not
// included even though the issue's own Description names a false "(existing)"
// citation found there — the acceptance criteria scope this guard to the two
// sections that assert a test was run, not one that proposes running it; a
// planned test naturally may not exist yet.
var issueCitationSectionHeaders = map[string]bool{
	"## Verification":         true,
	"## Implementation notes": true,
}

// extractNamedSections returns, for each occurrence of a level-2 heading in
// issueCitationSectionHeaders, the heading and the text between it and the
// next level-2 heading (or EOF) — plus startLine, the 1-based line number of
// the first line of that body, so a citation's position inside the returned
// text can be translated back into a real line number in the source file.
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
		if !issueCitationSectionHeaders[line] {
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
// cited in dir's *.md files' `## Verification`/`## Implementation notes`
// sections against defined, restricted to files whose `**Status**` row
// satisfies includeStatus. It returns one formatted "path:line: Identifier"
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
				if issueCitationExcluded(section.text, loc[0], loc[1]) {
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
// `## Verification` and `## Implementation notes` sections must cite only
// Test… identifiers the tree actually defines. See the file-level comment
// for why resolved/closed/wontfix files are out of scope.
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
// the status filter is what keeps the 87-citation resolved-file corpus (see
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
// issueCitationExcluded's six rules against a synthetic example built from
// string concatenation (never spelling a fictitious "Test…" name directly
// in a real comment in this file, matching #0196's own self-reference
// discipline — this file is not a _test.go file this guard itself scans,
// since it only scans issues/*.md, but the discipline costs nothing to
// keep). Each case additionally records the real corpus line the rule
// exists to cover, per the file-level comment's measurement.
func TestIssueCitationExcludedDiscountRulesAreEachLoadBearing(t *testing.T) {
	fictitious := "Test" + "Rv0265DoesNotExist"
	cases := []struct {
		name string
		text string
	}{
		{"allowlisted TestSentAt", "the field `TestSentAt` is documented here"},
		{"allowlisted TestPool", "see the *TestPool convention"},
		{"trailing wildcard *", "`" + fictitious + "*` covers the family — real line: #0192's TestSPA*"},
		{"trailing wildcard ...", "`" + fictitious + "...` covers the family — real line: #0025's TestDB_*"},
		{"trailing pipe (regexp alternation)", "-run '" + fictitious + "|TestOther'"},
		{"leading pipe (regexp alternation)", "-run 'TestOther|" + fictitious + "'"},
		{"renamed from", "renamed from " + fictitious + "; the replacement covers it"},
		{"go test FAIL marker", "printed `--- FAIL: " + fictitious + "`, then exit 0"},
		{"go test PASS marker", "printed `--- PASS: " + fictitious + "`"},
		{"go test RUN marker, wrapped", "printed `=== RUN\n  " + fictitious + "`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			loc := issueTestCitationPattern.FindStringIndex(c.text)
			if loc == nil {
				t.Fatalf("pattern did not even match in %q", c.text)
			}
			if !issueCitationExcluded(c.text, loc[0], loc[1]) {
				t.Errorf("expected %q to be excluded by a discount rule, it was not", c.text)
			}
		})
	}

	// The pattern must still fire on the same fictitious name written
	// plainly, with none of the above shapes — this is the guard's whole
	// value, and every rule above must stay narrow enough not to swallow it.
	plain := "`" + fictitious + "` was cited and does not exist"
	loc := issueTestCitationPattern.FindStringIndex(plain)
	if loc == nil {
		t.Fatalf("pattern did not match the plain citation %q", plain)
	}
	if issueCitationExcluded(plain, loc[0], loc[1]) {
		t.Errorf("plain citation %q must not be excluded by any discount rule", plain)
	}
}
