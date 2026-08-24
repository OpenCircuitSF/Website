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
// bare `{` `}` `(` `)` `+` `?` `|` `^` `$` in a disallowed position, or an
// escaped `\(` `\)` — naming the row and the construct, rather than
// translating it wrong and silently passing. A row whose START pattern is
// safe but matches nothing in PRD.md still fails loudly by name too.
// sedPatternUnsafeConstruct does not model bracket expressions, so `[{]` and
// `[+]` are rejected even though they are literal (and safe) in both
// dialects there — a deliberate fail-safe over-rejection (#0230), not a gap
// worth bracket-expression parsing to close.
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

// oddPrecedingBackslashes reports whether the byte at index i in s is itself
// escaped by counting the run of '\\' bytes immediately before it: an *odd*
// count means i is escaped (the last backslash of the run pairs with i and
// has no partner of its own), an *even* count (including zero) means it
// isn't — the preceding backslashes pair off with each other and i is
// unescaped. This is parity, not "is the one immediately preceding byte a
// backslash": `a\\{b` has two backslashes before `{` (even → `{` is
// unescaped, a real literal-brace-in-BRE case), while `a\{b` has one (odd →
// `{` is escaped, a BRE interval). Backslash is single-byte in UTF-8 and
// never a continuation byte of a multi-byte rune, so byte indexing here
// agrees with rune indexing for any input.
//
// #0230: parseSedRangeCmd already implemented exactly this scan (to tell an
// escaped literal `/` in START's own pattern text apart from the real
// `/,/` separator) before sedPatternUnsafeConstruct existed; the two were
// never unified, and sedPatternUnsafeConstruct grew the simpler, wrong
// `runes[i-1] == '\\'` check independently — a metacharacter preceded by an
// even number (≥2) of backslashes was wrongly treated as escaped there.
func oddPrecedingBackslashes(s string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		n++
	}
	return n%2 != 0
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
// whose leading `/` is not itself escaped, via oddPrecedingBackslashes.
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
		if oddPrecedingBackslashes(body, i) {
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
//   - a mid-pattern bare `^`  — literal in BRE anywhere but the pattern's
//     first position; an anchor in RE2 wherever it appears, so `a^b` reads
//     the literal three characters in BRE but never matches anything as RE2
//     (#0230: an END pattern built this way would silently under-count,
//     since countPRDSection's terminator would never be found and the range
//     would run to EOF or fail loudly some other way, never partially)
//   - a mid-pattern bare `$`  — the same divergence as `^`, mirrored: literal
//     in BRE anywhere but the pattern's last position, an anchor in RE2
//     wherever it appears. A *trailing* `$` is an anchor in both and is not
//     flagged.
//
// `\+` `\?` `\|` are omitted: they are literal on this repo's sed (BSD) and
// remain literal, unrewritten, in RE2 — agreement, not divergence. They only
// diverge under GNU sed, which this project does not use (CLAUDE.md §5). A
// leading `^` and a trailing `$` are omitted for the same reason: both
// dialects treat them as anchors there.
//
// Backreferences (`\1`) are also omitted: an unrewritten `\1` is not valid
// RE2 syntax, so regexp.Compile already rejects it loudly in countPRDSection
// — no silent-pass risk to guard against here.
//
// #0230 fix: "escaped" now means an *odd* number of immediately preceding
// backslashes (oddPrecedingBackslashes, the same scan parseSedRangeCmd
// already used), not "the immediately preceding rune is a backslash". The
// old check treated a metacharacter preceded by an *even* number (≥2) of
// backslashes as escaped, so `a\\{b` and `a\\+b` mistranslated silently —
// BSD sed reads a literal `a\{b` (the doubled backslash is one literal
// backslash, and `{` is unescaped and literal in BRE either way), RE2 read
// an interval.
//
// What this function does *not* catch (#0230): `[{]` and `[+]` are literal
// in both dialects inside a bracket expression, but this scan has no notion
// of bracket-expression state, so it flags the bare metacharacter anyway.
// That is a deliberate, accepted over-rejection — it fails safe (a loud
// refusal on a pattern that would actually translate fine), never silent —
// rather than adding bracket-expression parsing for a case none of the
// current 48 rows use.
func sedPatternUnsafeConstruct(pat string) (construct string, safe bool) {
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		escaped := oddPrecedingBackslashes(pat, i)
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
		case '^':
			if !escaped && i != 0 {
				return "a mid-pattern `^` (literal in sed's BRE except at the pattern's start, an RE2 anchor wherever it appears)", false
			}
		case '$':
			if !escaped && i != len(pat)-1 {
				return "a mid-pattern `$` (literal in sed's BRE except at the pattern's end, an RE2 anchor wherever it appears)", false
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

// TestSedPatternUnsafeConstruct exercises sedPatternUnsafeConstruct directly
// against shapes #0230's review found undercovered by the 48 rows in
// CLAUDE.md today. "unsafe" cases must be rejected (safe == false); "safe"
// cases must pass through untouched (safe == true) so a legitimate pattern
// isn't refused.
func TestSedPatternUnsafeConstruct(t *testing.T) {
	t.Run("unsafe", func(t *testing.T) {
		cases := []struct {
			name string
			pat  string
		}{
			{
				// #0230 bug 1: two backslashes before `{` is an *even*
				// count -- the backslashes pair with each other, so `{` is
				// unescaped and literal in BRE, an untranslated RE2
				// interval. The old `runes[i-1] == '\\'` check saw the
				// immediately preceding rune was a backslash and wrongly
				// called this escaped, letting it pass silently.
				name: `a\\{b (even backslashes -- bare, unsafe brace)`,
				pat:  `a\\{b`,
			},
			{
				// Same bug, the `+` form named in the issue.
				name: `a\\+b (even backslashes -- bare, unsafe plus)`,
				pat:  `a\\+b`,
			},
			{
				// #0230 bug 2: `^` anywhere but the pattern's first
				// position is a literal in BRE but an RE2 anchor wherever
				// it appears, so an untranslated `a^b` never matches as
				// RE2 even though BRE reads it as three literal characters.
				name: "a^b (mid-pattern caret)",
				pat:  "a^b",
			},
			{
				// The `$` mirror of the case above -- literal in BRE
				// anywhere but the pattern's last position.
				name: "a$b (mid-pattern dollar)",
				pat:  "a$b",
			},
			{
				// #0230 criterion 4: sedPatternUnsafeConstruct does not
				// model bracket expressions, so a literal `{` or `+` inside
				// one is still rejected -- a deliberate, documented,
				// fail-safe over-rejection, not a bug this issue closes.
				name: "[{] (over-rejected on purpose -- see doc comment)",
				pat:  "[{]",
			},
			{
				name: "[+] (over-rejected on purpose -- see doc comment)",
				pat:  "[+]",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if construct, safe := sedPatternUnsafeConstruct(tc.pat); safe {
					t.Errorf("sedPatternUnsafeConstruct(%q) = (%q, true), want safe == false", tc.pat, construct)
				}
			})
		}
	})

	t.Run("safe", func(t *testing.T) {
		cases := []struct {
			name string
			pat  string
		}{
			{
				// A single backslash before `{` is an *odd* count -- `{`
				// is genuinely escaped, a real BRE interval, exactly what
				// sedBREToGoRegex translates.
				name: `a\{b (odd backslashes -- a real BRE interval)`,
				pat:  `a\{b`,
			},
			{
				name: `a\+b (odd backslashes -- literal plus, agreement)`,
				pat:  `a\+b`,
			},
			{
				name: "^ab (leading caret is an anchor in both dialects)",
				pat:  "^ab",
			},
			{
				name: "ab$ (trailing dollar is an anchor in both dialects)",
				pat:  "ab$",
			},
			{
				name: "^ab$ (both, at their only permitted positions)",
				pat:  "^ab$",
			},
			{
				// The exact shape every current §11 row uses.
				name: `^### 4\.2 `,
				pat:  `^### 4\.2 `,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if construct, safe := sedPatternUnsafeConstruct(tc.pat); !safe {
					t.Errorf("sedPatternUnsafeConstruct(%q) = (%q, false), want safe == true", tc.pat, construct)
				}
			})
		}
	})
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
