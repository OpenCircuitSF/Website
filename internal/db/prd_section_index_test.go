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
// pure Go). It parses each row's own Extract cell and reimplements the range
// semantics `sed -n '/START/,/END/p'` has: begin at the first line matching
// START, print through and including the first *subsequent* line matching
// END, or to end-of-file if END never matches again (true only for §14, the
// last section — CLAUDE.md §11 documents this and says not to "fix" it into
// an off-by-one).
//
// What this actually checks, and what it does not (#0218): it verifies the
// *arithmetic* — that each row's stated count matches what its own START/END
// patterns produce against the current PRD.md — for the subset of sed's BRE
// the 48 rows currently use (literal text, `\.`, character classes, and the
// `\{n,m\}` interval escape). It is not a general BRE-vs-RE2 validator, and
// it does not itself check that an Extract cell is valid BRE. sedBREToGoRegex
// translates exactly one escape (`\{...\}`); every other regex metacharacter
// means something different in BRE than in RE2, in the direction that makes
// a bad translation compile silently rather than error. So this test rejects
// (via sedPatternUnsafeConstruct, before ever compiling a pattern) any
// Extract cell containing a construct it cannot translate faithfully — a
// bare `{` `}` `(` `)` `+` `?` `|`, or an escaped `\(` `\)` — naming the row
// and the construct, rather than translating it wrong and silently passing.
// A row whose START pattern is safe but matches nothing in PRD.md still
// fails loudly by name too.
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

		// #0218: reject anything sedBREToGoRegex cannot translate faithfully
		// before it is ever compiled — see sedPatternUnsafeConstruct.
		if construct, safe := sedPatternUnsafeConstruct(startPat); !safe {
			t.Errorf("CLAUDE.md:%d §11 row %q: START pattern /%s/ contains %s — this cannot be translated from sed's BRE to Go's RE2 faithfully, so the row's count cannot be trusted. Rewrite the pattern to avoid it, or teach sedBREToGoRegex and sedPatternUnsafeConstruct together.", i+1, label, startPat, construct)
			continue
		}
		if construct, safe := sedPatternUnsafeConstruct(endPat); !safe {
			t.Errorf("CLAUDE.md:%d §11 row %q: END pattern /%s/ contains %s — this cannot be translated from sed's BRE to Go's RE2 faithfully, so the row's count cannot be trusted. Rewrite the pattern to avoid it, or teach sedBREToGoRegex and sedPatternUnsafeConstruct together.", i+1, label, endPat, construct)
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
// caller rejecting anything else).
//
// #0218 fix: an earlier version found the separator with a plain
// strings.Index(body, "/,/"), which mis-splits if START's own pattern text
// contains an escaped literal slash (`\/`) followed by a comma and slash —
// the naive search has no way to tell that `/` apart from the real
// separator. No current row's pattern contains a `/` at all, so this was
// latent, not triggered — but it is cheap to do correctly: scan for a `/,/`
// whose leading `/` is not itself escaped (an even number of immediately
// preceding backslashes), rather than trusting the first textual match.
func parseSedRangeCmd(cmd string) (startPat, endPat string, ok bool) {
	const prefix = "sed -n '/"
	const suffix = "/p' PRD.md"
	if !strings.HasPrefix(cmd, prefix) || !strings.HasSuffix(cmd, suffix) {
		return "", "", false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(cmd, prefix), suffix)

	for i := 0; i+2 < len(body); i++ {
		if body[i] != '/' || body[i+1] != ',' || body[i+2] != '/' {
			continue
		}
		backslashes := 0
		for j := i - 1; j >= 0 && body[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 != 0 {
			// This '/' is escaped — part of START's own pattern text, not
			// the START/END separator. Keep scanning.
			continue
		}
		return body[:i], body[i+3:], true
	}
	return "", "", false
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
// §4.x, §5.x, §6.x, §7.x, and §10.x row, before this fix). `\.` and character
// classes mean the same thing in both.
//
// This function does not itself detect or reject any other BRE/RE2
// divergence — callers must run sedPatternUnsafeConstruct on a pattern first
// (parsePRDIndexTable does) and refuse to call this on one it flagged.
// #0113 fixed the `\{...\}` direction; #0218 found and closed the inverse
// (a bare `{n,m}`, literal in BRE, silently becomes an RE2 interval here)
// and the same class of bug for `(` `)` `+` `?` `|` — see
// sedPatternUnsafeConstruct for the full list.
func sedBREToGoRegex(pat string) string {
	pat = strings.ReplaceAll(pat, `\{`, `{`)
	pat = strings.ReplaceAll(pat, `\}`, `}`)
	return pat
}

// sedPatternUnsafeConstruct scans a sed BRE pattern — one half of a §11
// Extract cell — for a construct sedBREToGoRegex cannot translate faithfully
// into Go's RE2, and returns a human-readable name for the first one it
// finds. sedBREToGoRegex rewrites only the interval escape `\{...\}`; every
// other regex metacharacter means something different in BRE than in RE2,
// and in every case below the difference is silent rather than a compile
// error:
//
//   - bare `{` `}`      — literal text in BRE; RE2's interval syntax if left
//     untranslated (the #0218 bug: `#{2,3} ` compiles fine and quietly means
//     "the literal `#` repeated 2-3 times, followed by a space")
//   - bare `(` `)`      — literal text in BRE; RE2's group syntax if left
//     untranslated
//   - `\(` `\)`         — a BRE group; sedBREToGoRegex does not rewrite it,
//     so RE2 reads the escaped form as a literal parenthesis instead — the
//     grouping is lost either way a pattern could plausibly use it
//   - bare `+` `?` `|`  — literal text in BRE; RE2 metacharacters (one-or-
//     more, optional, alternation) if left untranslated
//
// `\+` `\?` `\|` are omitted: they are literal on this repo's sed (BSD) and
// remain literal, unrewritten, in RE2 — agreement, not divergence. They only
// diverge under GNU sed, which this project does not use (CLAUDE.md §5).
//
// Backreferences (`\1`) are also omitted: an unrewritten `\1` is not valid
// RE2 syntax, so regexp.Compile already rejects it loudly in countPRDSection
// — no silent-pass risk to guard against here.
func sedPatternUnsafeConstruct(pat string) (construct string, safe bool) {
	runes := []rune(pat)
	for i, c := range runes {
		escaped := i > 0 && runes[i-1] == '\\'
		switch c {
		case '{', '}':
			if !escaped {
				return "a bare `" + string(c) + "` (literal in sed's BRE, an RE2 interval character if untranslated)", false
			}
		case '(', ')':
			if escaped {
				return "`\\" + string(c) + "` (a BRE group; sedBREToGoRegex does not translate it, so RE2 reads it as a literal parenthesis)", false
			}
			return "a bare `" + string(c) + "` (literal in sed's BRE, an RE2 group character if untranslated)", false
		case '+', '?', '|':
			if !escaped {
				return "a bare `" + string(c) + "` (literal in sed's BRE, an RE2 metacharacter if untranslated)", false
			}
		}
	}
	return "", true
}

// countPRDSection reimplements `sed -n '/startPat/,/endPat/p' | wc -l`
// against prdLines, and applies §11's stated convention: a row's count is
// one less than the raw line count when a terminator line was found (the
// range is inclusive of the following section's heading), or the raw count
// unchanged when no terminator was found (only true at end-of-file, i.e.
// PRD.md's final section).
//
// Structural divergence from real `sed`, documented rather than fixed
// (#0218): `sed -n '/START/,/END/p'` restarts a new range every time START
// matches again after a range's END, printing those lines too; this function
// only ever computes the *first* START/END range. Every current row's START
// pattern is a unique heading anchor that matches exactly once in PRD.md, so
// the two behaviours agree on all 48 rows today (confirmed by the caller's
// mayRestart check below, not just asserted here) — but a future row whose
// START pattern is looser would silently under-count instead of erroring.
// Rather than reimplement sed's restart loop for a case nothing exercises,
// this function detects it — does startRe match again anywhere past the
// first range? — and reports that back so the caller can fail loudly by row
// and pattern instead of leaving the gap latent in a source comment only.
func countPRDSection(t *testing.T, prdLines []string, startPat, endPat string) (actual int, found bool, mayRestart bool) {
	t.Helper()

	startRe, err := regexp.Compile(sedBREToGoRegex(startPat))
	if err != nil {
		t.Errorf("start pattern %q does not compile as a Go regexp: %v", startPat, err)
		return 0, false, false
	}
	endRe, err := regexp.Compile(sedBREToGoRegex(endPat))
	if err != nil {
		t.Errorf("end pattern %q does not compile as a Go regexp: %v", endPat, err)
		return 0, false, false
	}

	startIdx := -1
	for i, l := range prdLines {
		if startRe.MatchString(l) {
			startIdx = i
			break
		}
	}
	if startIdx == -1 {
		return 0, false, false
	}

	endIdx := -1
	for j := startIdx + 1; j < len(prdLines); j++ {
		if endRe.MatchString(prdLines[j]) {
			endIdx = j
			break
		}
	}

	// Scan past the computed range (or past startIdx, if it ran to EOF —
	// unreachable in that case since there is nothing left to scan, but
	// kept for symmetry) for a further START match real sed would restart
	// on.
	scanFrom := startIdx + 1
	if endIdx != -1 {
		scanFrom = endIdx + 1
	}
	for j := scanFrom; j < len(prdLines); j++ {
		if startRe.MatchString(prdLines[j]) {
			mayRestart = true
			break
		}
	}

	if endIdx == -1 {
		// No terminator: sed prints through end-of-file, and §11's
		// convention counts that raw (documented exception for §14).
		return len(prdLines) - startIdx, true, mayRestart
	}
	raw := endIdx - startIdx + 1
	return raw - 1, true, mayRestart
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
		actual, found, mayRestart := countPRDSection(t, prdLines, row.startPat, row.endPat)
		if !found {
			t.Errorf("CLAUDE.md:%d §11 row %q: start pattern /%s/ matches nothing in PRD.md — has this section moved, been renamed, or been deleted? The row's Extract command is now broken, not just its count.", row.statedLine, row.label, row.startPat)
			continue
		}
		if mayRestart {
			t.Errorf("CLAUDE.md:%d §11 row %q: START pattern /%s/ matches PRD.md again after this row's computed range ends — real `sed` would restart a new START/END range there and print more lines, but countPRDSection only computes the first range (known limit, #0218), so the count below cannot be trusted for this row. Narrow the START pattern to match exactly once, or verify this row's count by hand.", row.statedLine, row.label, row.startPat)
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
