package db

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// #0113: CLAUDE.md §11 indexes every PRD.md section with a stated line count
// and the exact `sed` command that extracts it — the table an orchestrator
// consults instead of reading PRD.md's ~1,800 lines whole. Three rows had
// drifted from the file when this issue was filed, and nothing caught it:
// grep -rln 'PRD.md' --include='*_test.go' . found prd_index_parity_test.go,
// but that file (despite its name) guards §6.2's SQL schema against
// migrations/ — it never reads §11's table. docs_parity_test.go guards
// docs/database.md and CLAUDE.md's migration-range sentence, not this table
// either. So the table was hand-maintained, and it drifted twice on the same
// three-row shape: once before #0113 was filed, and again after its first
// fix, when #0148 widened §6.2 by 30 lines (and its §6 parent along with it)
// without touching this table — proof that a "recomputed by hand, once" note
// does not stay true past the next PRD.md edit.
//
// This test recomputes every row itself rather than shelling out to `sed`
// (no test in this package invokes an external process — see prdPath's
// sibling guards in prd_index_parity_test.go and docs_parity_test.go, both
// pure Go). It parses each row's own Extract cell and reimplements exactly
// the range semantics `sed -n '/START/,/END/p'` has: begin at the first line
// matching START, print through and including the first *subsequent* line
// matching END, or to end-of-file if END never matches again (true only for
// §14, the last section — CLAUDE.md §11 documents this and says not to
// "fix" it into an off-by-one). That also means this test is the mechanical
// check on the extraction commands themselves, not just the numbers: a row
// whose START pattern no longer matches PRD.md fails loudly by name rather
// than silently extracting nothing.
const prdSectionIndexHeading = "## 11. PRD section index"

// prdIndexRow is one data row of §11's table.
type prdIndexRow struct {
	label      string
	statedLine int // 1-based line number in CLAUDE.md, for error messages
	stated     int
	startPat   string
	endPat     string
}

// parsePRDIndexTable extracts §11's table rows from CLAUDE.md, scoped to the
// text between the "## 11. PRD section index" heading and the next "## "
// heading (or EOF), so a table row shape appearing anywhere else in the file
// can never be mistaken for one of §11's rows.
func parsePRDIndexTable(t *testing.T, claudeMD string) []prdIndexRow {
	t.Helper()

	lines := strings.Split(claudeMD, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == prdSectionIndexHeading {
			start = i
			break
		}
	}
	if start == -1 {
		t.Fatalf("CLAUDE.md: heading %q not found — has §11 been renamed or renumbered? Update prdSectionIndexHeading.", prdSectionIndexHeading)
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}

	rowPattern := regexp.MustCompile("^`(.+)`$")
	var rows []prdIndexRow
	for i := start + 1; i < end; i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) != 5 {
			continue
		}
		label := strings.TrimSpace(fields[1])
		statedStr := strings.TrimSpace(fields[2])
		extractCell := strings.TrimSpace(fields[3])

		stated, err := strconv.Atoi(statedStr)
		if err != nil {
			// Header ("Section | Lines | Extract") and separator
			// ("---|---|---") rows both land here; every real data row's
			// middle cell is a bare integer.
			continue
		}

		m := rowPattern.FindStringSubmatch(extractCell)
		if m == nil {
			t.Errorf("CLAUDE.md §11 row %q: Extract cell %q is not a single backtick-quoted command", label, extractCell)
			continue
		}
		startPat, endPat, ok := parseSedRangeCmd(m[1])
		if !ok {
			t.Errorf("CLAUDE.md §11 row %q: Extract command %q does not match the expected `sed -n '/START/,/END/p' PRD.md` shape", label, m[1])
			continue
		}

		rows = append(rows, prdIndexRow{
			label:      label,
			statedLine: i + 1,
			stated:     stated,
			startPat:   startPat,
			endPat:     endPat,
		})
	}

	if len(rows) == 0 {
		t.Fatalf("CLAUDE.md §11: found the heading but parsed zero table rows — has the table's shape changed?")
	}
	return rows
}

// parseSedRangeCmd splits `sed -n '/START/,/END/p' PRD.md` into its START
// and END patterns. Every §11 row uses exactly this shape (verified by the
// caller rejecting anything else), so a plain prefix/suffix/separator split
// is enough — no need for a general sed-argument parser.
func parseSedRangeCmd(cmd string) (startPat, endPat string, ok bool) {
	const prefix = "sed -n '/"
	const mid = "/,/"
	const suffix = "/p' PRD.md"
	if !strings.HasPrefix(cmd, prefix) || !strings.HasSuffix(cmd, suffix) {
		return "", "", false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(cmd, prefix), suffix)
	idx := strings.Index(body, mid)
	if idx == -1 {
		return "", "", false
	}
	return body[:idx], body[idx+len(mid):], true
}

// sedBREToGoRegex translates the one BRE-specific escape CLAUDE.md's §11
// `sed` commands use into Go's RE2 syntax. Sed's basic regular expressions
// require `\{...\}` to write an interval expression — a bare `{`/`}` is
// literal — which is exactly backwards from RE2, where a bare `{n,m}` is the
// interval and `\{`/`\}` escape a literal brace. Compiling `^#\{2,3\} [0-9]`
// unmodified as a Go regexp does not error — it silently means "the literal
// text `#{2,3} ` followed by a digit", which never matches anything in
// PRD.md and makes every subsection row look like it has no terminator (the
// first version of this test failed exactly this way, against every §3.x,
// §4.x, §5.x, §6.x, §7.x, and §10.x row, before this fix). No other escape
// these patterns use differs between BRE and RE2: `\.` means a literal dot
// in both.
func sedBREToGoRegex(pat string) string {
	pat = strings.ReplaceAll(pat, `\{`, `{`)
	pat = strings.ReplaceAll(pat, `\}`, `}`)
	return pat
}

// countPRDSection reimplements `sed -n '/startPat/,/endPat/p' | wc -l`
// against prdLines, and applies §11's stated convention: a row's count is
// one less than the raw line count when a terminator line was found (the
// range is inclusive of the following section's heading), or the raw count
// unchanged when no terminator was found (only true at end-of-file, i.e.
// PRD.md's final section).
func countPRDSection(t *testing.T, prdLines []string, startPat, endPat string) (actual int, found bool) {
	t.Helper()

	startRe, err := regexp.Compile(sedBREToGoRegex(startPat))
	if err != nil {
		t.Errorf("start pattern %q does not compile as a Go regexp: %v", startPat, err)
		return 0, false
	}
	endRe, err := regexp.Compile(sedBREToGoRegex(endPat))
	if err != nil {
		t.Errorf("end pattern %q does not compile as a Go regexp: %v", endPat, err)
		return 0, false
	}

	startIdx := -1
	for i, l := range prdLines {
		if startRe.MatchString(l) {
			startIdx = i
			break
		}
	}
	if startIdx == -1 {
		return 0, false
	}

	endIdx := -1
	for j := startIdx + 1; j < len(prdLines); j++ {
		if endRe.MatchString(prdLines[j]) {
			endIdx = j
			break
		}
	}

	if endIdx == -1 {
		// No terminator: sed prints through end-of-file, and §11's
		// convention counts that raw (documented exception for §14).
		return len(prdLines) - startIdx, true
	}
	raw := endIdx - startIdx + 1
	return raw - 1, true
}

// TestPRDSectionIndexParity is #0113's guard: every row of CLAUDE.md §11
// must match what its own Extract command actually produces against the
// current PRD.md.
func TestPRDSectionIndexParity(t *testing.T) {
	claudeBytes, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("reading %s: %v", claudeMDPath, err)
	}
	claudeMD := string(claudeBytes)

	prdBytes, err := os.ReadFile(prdPath)
	if err != nil {
		t.Fatalf("reading %s: %v", prdPath, err)
	}
	prdContent := string(prdBytes)
	prdLines := strings.Split(prdContent, "\n")
	if len(prdLines) > 0 && prdLines[len(prdLines)-1] == "" {
		prdLines = prdLines[:len(prdLines)-1]
	}

	rows := parsePRDIndexTable(t, claudeMD)
	for _, row := range rows {
		actual, found := countPRDSection(t, prdLines, row.startPat, row.endPat)
		if !found {
			t.Errorf("CLAUDE.md:%d §11 row %q: start pattern /%s/ matches nothing in PRD.md — has this section moved, been renamed, or been deleted? The row's Extract command is now broken, not just its count.", row.statedLine, row.label, row.startPat)
			continue
		}
		if actual != row.stated {
			t.Errorf("CLAUDE.md:%d §11 row %q: stated %d lines, PRD.md's current text is %d — run `%s` (with the trailing terminator line, if any, one more than this count) and correct the row.", row.statedLine, row.label, row.stated, actual, "sed -n '/"+row.startPat+"/,/"+row.endPat+"/p' PRD.md")
		}
	}
}

// prdMDLineCountPattern matches §11's preamble sentence, e.g.
// "`PRD.md` is 1,798 lines (~21k tokens)." — the one number in this table
// not attached to a row.
var prdMDLineCountPattern = regexp.MustCompile("`PRD\\.md` is ([0-9,]+) lines")

// TestPRDSectionIndexTotalMatchesFile guards the one number in §11 that
// isn't a table row: the preamble's stated total line count for PRD.md.
func TestPRDSectionIndexTotalMatchesFile(t *testing.T) {
	claudeBytes, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("reading %s: %v", claudeMDPath, err)
	}
	m := prdMDLineCountPattern.FindStringSubmatch(string(claudeBytes))
	if m == nil {
		t.Fatalf("CLAUDE.md §11: preamble sentence \"`PRD.md` is N,NNN lines\" not found — has the wording changed? Update prdMDLineCountPattern.")
	}
	stated, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	if err != nil {
		t.Fatalf("CLAUDE.md §11: preamble line count %q does not parse as an integer: %v", m[1], err)
	}

	prdBytes, err := os.ReadFile(prdPath)
	if err != nil {
		t.Fatalf("reading %s: %v", prdPath, err)
	}
	lines := strings.Split(string(prdBytes), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	actual := len(lines)

	if stated != actual {
		t.Errorf("CLAUDE.md §11 preamble states PRD.md is %d lines; `wc -l PRD.md` says %d. Update the preamble sentence.", stated, actual)
	}
}
