package db

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// #0082: docs/database.md carries a table of every migration and a sentence
// naming the numbered range. It drifted three times before anything caught
// it mechanically — #0009 added 000008 without updating it (caught in
// review), #0023/#0025 added 000009/000010 (caught, updated), and #0026
// added 000011 with no row and stale "000001-000010" prose (only caught by
// this issue). This file is the guard, in the same shape as #0071's
// route-table parity test (internal/handlers/routes_parity_test.go):
// filesystem-only, no database, riding the `go test ./...` every backend
// verification pass already runs.
//
// #0083: #0082's reviewer probed this guard rather than reading it and found
// four drift modes it did not catch, each proved by experiment:
//
//  1. A doc row naming a migration that has been deleted from disk. The
//     original check ran file -> doc only, never doc -> file.
//  2. A numbering gap. Both docs/database.md and CLAUDE.md claim a
//     *contiguous* range; nothing enforced it.
//  3. A *.up.sql with no matching *.down.sql -- listMigrations globbed only
//     *.up.sql, so an up-only migration with a doc row passed green even
//     though a migration with no down is an operational hazard.
//  4. A table row's token appearing inside a fenced code block (or bare
//     prose): the old check was strings.Contains over the *whole file*, with
//     no notion of "this text is a markdown table row". This file now only
//     honors matches found on lines that (a) start with "|" and (b) are not
//     inside a ``` fence -- see docTableRowLines.
//
// CLAUDE.md's migration-range sentence drifted twice on top of that (see
// its "Repository layout" table row) -- the fourth drift of this class
// overall, and across a second file. #0083's fix is one table-driven test
// over both docs/database.md and CLAUDE.md, not a second, near-identical
// test: the two files differ only in wording ("numbered" vs "contiguous")
// and in whether migrations are enumerated per-row at all (CLAUDE.md states
// only the range, docs/database.md also lists every migration). A sibling
// test would duplicate the row/fence parsing and the en-dash handling and
// become a second thing that can itself drift out of step with its twin --
// exactly the failure this issue exists to prevent, one level up.
//
// Design choice: this lives in internal/db (the package that already owns
// "manages the PostgreSQL connection pool and migrations", per its doc
// comment) rather than in internal/handlers, where #0071's parity test
// lives — internal/handlers is unrelated to migrations and, as of this
// writing, has another issue's work in flight.
//
// Placement note (#0083, corrected by #0086): internal/db's TestMain
// acquires testdb's shared advisory lock for the package's whole run, so
// when TEST_DATABASE_URL is set this filesystem-only test queues behind
// every database-backed test in this package even though it touches no
// database itself. That is a real, measurable cost, and it is still not
// fixed here.
//
// #0083 originally justified staying in internal/db by claiming every
// viable target was off limits -- the other lock-gated packages paying the
// same tax (internal/audit, internal/middleware, internal/interests,
// internal/subscribers) for the same reason, and the lock-free ones
// (internal/handlers, internal/mailing, internal/auth) for being other
// subagents' in-flight work. #0086's reviewer found that overstated: a new
// internal/db/docsparity subpackage would hold no advisory lock of its own
// (it would define no TestMain, so testdb.Lock is never called) and
// was squarely in lane the whole time -- internal/db is the package that
// owns migrations, per its own doc comment, and nobody else's work was
// touching it.
//
// The real tradeoff, stated accurately: a lock-free internal/db/docsparity
// subpackage trades away the queueing cost in exchange for a new directory
// and package boundary that exists to hold one test file, a relative path
// one level deeper to migrations/ and docs/ ("../../../..." instead of
// "../../..."), and a second place (beyond internal/db itself) a reader has
// to know about when looking for "the migrations guard." For a
// filesystem-only test whose actual runtime is milliseconds -- the cost is
// queueing behind the DB suite, not the test's own execution -- that
// package-proliferation overhead was judged not worth paying in #0083, and
// still isn't here. internal/db remains the correct call on the merits; the
// alternative was available, considered, and traded off, not absent.
const (
	migrationsDir = "../../migrations"
	docsPath      = "../../docs/database.md"
	claudeMDPath  = "../../CLAUDE.md"
)

// migrationVersionName matches a migration file's stem: a 6-digit version
// followed by an underscore and a snake_case name, e.g.
// "000011_add_subscribers_already_subscribed_sent_at". This is exactly the
// token docs/database.md wraps in backticks as the first cell of each
// migration table row, so the same regex identifies both a migration file's
// identity and its row in the doc.
var migrationVersionName = regexp.MustCompile(`^(\d{6})_([a-z0-9_]+)\.up\.sql$`)

// migrationStem is a single migrations/*.up.sql file's identity: its
// 6-digit version and its full "NNNNNN_name" stem, the token expected
// verbatim (backtick-wrapped) in docs/database.md.
type migrationStem struct {
	version int
	stem    string // e.g. "000011_add_subscribers_already_subscribed_sent_at"
}

// listMigrations returns every migrations/*.up.sql file's stem, sorted by
// version. Fails the test outright (not skips) if the directory can't be
// read or no .up.sql files are found — either means this test's assumptions
// about the repo layout have themselves drifted, which is worth surfacing
// loudly rather than passing vacuously.
func listMigrations(t *testing.T) []migrationStem {
	t.Helper()
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("read %s: %v", migrationsDir, err)
	}

	var stems []migrationStem
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationVersionName.FindStringSubmatch(e.Name())
		if m == nil {
			continue // *.down.sql and anything else non-matching
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("migration %s: version %q is not a plain integer: %v", e.Name(), m[1], err)
		}
		stems = append(stems, migrationStem{
			version: version,
			stem:    strings.TrimSuffix(e.Name(), ".up.sql"),
		})
	}
	if len(stems) == 0 {
		t.Fatalf("found no migrations/*.up.sql files in %s — has the repo layout changed?", migrationsDir)
	}
	sort.Slice(stems, func(i, j int) bool { return stems[i].version < stems[j].version })
	return stems
}

// verifyDownPairs is drift mode 3 (#0083): every migrations/*.up.sql file
// must have a sibling *.down.sql on disk. listMigrations only globs
// *.up.sql, so this is a distinct filesystem check, not something the doc
// comparison below can express — a migration with no down is an operational
// hazard (nothing can roll it back), not a documentation gap.
func verifyDownPairs(t *testing.T, stems []migrationStem) {
	t.Helper()
	for _, m := range stems {
		downPath := migrationsDir + "/" + m.stem + ".down.sql"
		if _, err := os.Stat(downPath); err != nil {
			t.Errorf(
				"migrations/%s.up.sql has no matching migrations/%s.down.sql — "+
					"every migration must be reversible; add the missing down file — see #0083.",
				m.stem, m.stem,
			)
		}
	}
}

// verifyContiguous is drift mode 2 (#0083): both docs/database.md and
// CLAUDE.md claim the on-disk migrations are a *contiguous* numbered range
// starting at 000001. Nothing previously enforced that a version wasn't
// skipped -- moving 000009 off disk while leaving 000008 and 000010 in place
// left every prior assertion green.
func verifyContiguous(t *testing.T, stems []migrationStem) {
	t.Helper()
	for i, m := range stems {
		want := i + 1
		if m.version != want {
			t.Errorf(
				"migrations/ is not a contiguous 000001-prefixed range: expected version "+
					"%06d at position %d (0-indexed) but found %06d (%s) — a migration is "+
					"missing, duplicated, or misnumbered between here and its neighbors. "+
					"Renumber or restore the missing migration file — see #0083.",
				want, i, m.version, m.stem,
			)
			return // one gap reported is enough; further positions only cascade from it
		}
	}
}

// docTableRowLines returns every line of doc that is (a) a markdown table
// row -- its trimmed form starts with "|" -- and (b) not inside a fenced or
// indented code block or an HTML comment. This is drift mode 4 (#0083): the
// original check was strings.Contains over the *entire file*, so a
// migration's token sitting in a fenced code sample, or in ordinary prose,
// satisfied it exactly as well as a real table row naming that migration.
// Both the forward check (every disk migration has a doc row) and the
// reverse check (every doc row names a real migration) below search only
// these lines, so none of fence, indent, comment, or prose can stand in for
// a row.
//
// #0083 closed the ``` fence case only. Its reviewer proved three more forms
// still satisfied the row check unguarded, each by a probe, not by
// inference: a "|"-leading line inside a 4-space indented code block, inside
// a multi-line HTML comment, and inside a "~~~" tilde fence (#0086). This is
// a small state machine over those four states -- fence (``` or ~~~,
// matched to the character that opened it), HTML comment, and indented code
// -- deliberately not a markdown parser: see TestDocTableRowLines for the
// forms it must reject and the forms (tight pipes, alignment colons, a
// table nested in a list item, a table inside <details>) it must keep
// accepting.
//
// #0089 closed two more residuals in the same two states, still without
// taking on a parser:
//
//  1. The indent check below used to count only leading ' ' bytes
//     (strings.TrimLeft(line, " ")), so a line indented with a leading tab
//     measured indent 0 and fell straight through to the "|" check even
//     though CommonMark counts a tab as advancing to the next 4-column tab
//     stop -- four columns of indentation from a single leading tab.
//     leadingIndentWidth expands tabs the same way.
//  2. The fence-close check used a fixed 3-character prefix
//     (strings.HasPrefix(trimmed, "```")), so a 4-backtick (or 4-tilde)
//     fence was closed early by an inner 3-character run of the same
//     character. CommonMark requires the closing fence be at least as long
//     as the opener; fenceLen now records the opener's run length and the
//     close check requires the same-or-longer run.
//
// #0092 closed two further residuals in the close check specifically --
// #0089's fix required the closer's run to be *long enough*, but never
// checked the closer's own indentation or what followed the run:
//
//  1. A closer indented 4+ columns still closed the fence, because the run
//     length was measured against trimmed (strings.TrimSpace already
//     dropped the indentation) rather than against the raw line. CommonMark
//     caps a closing fence at 3 columns of indentation; at 4+ columns the
//     line is code content, not a closer. Now checked with
//     leadingIndentWidth(line) <= 3, the same helper and the same threshold
//     the indented-code-block check below already uses.
//  2. A closer followed by trailing text (e.g. "``` nope") still closed the
//     fence, because leadingRunLength(trimmed, fenceChar) >= fenceLen only
//     checks a prefix -- it never confirmed nothing else followed the run.
//     CommonMark permits only whitespace after a closing fence's run, and
//     trimmed already had trailing whitespace stripped by TrimSpace, so
//     "the run is all of trimmed" (leadingRunLength(trimmed, fenceChar) ==
//     len(trimmed)) is exactly that check.
//
// #0224 found the same defect class -- #0092's review, on the *opposite*
// branch: the fence *opener* had no indentation cap of its own, checking
// only leadingRunLength(trimmed, ch) >= 3 and discarding indentation exactly
// as the closer branch used to. Two divergences, both silent (an incorrect
// open, not a compile error or a loud rejection):
//
//  1. An opener indented 4+ columns (including a single leading tab, which
//     leadingIndentWidth counts as 4 columns under CommonMark's tab-stop
//     rule) still opened a fence. Fixed the same way the closer branch was
//     in #0092: leadingIndentWidth(line) <= 3 gates the open.
//  2. A backtick inside a backtick fence's info string (e.g. "``` foo`bar")
//     still opened a fence. CommonMark forbids this specifically for
//     backtick fences -- a backtick in the info string would be
//     indistinguishable from the start of an inline code span -- but not for
//     tilde fences, where a backtick can never be confused with the fence
//     marker. Fixed by rejecting a backtick anywhere in the text following
//     the opening run when, and only when, that run is backticks.
//
// Individually each fix only ever turns an incorrect open into a correct
// non-open (the safe direction: content that should be normal prose stops
// being mistaken for code). But #0224's review found the *composition*
// dangerous: a spurious open from an indented opener flips fence parity,
// masking real rows as code until some later line coincidentally satisfies
// the closer branch's own conditions and flips parity back -- and a line
// that fails to open (e.g. the info-string case) is exactly such a
// candidate, because it is not itself masked and is evaluated by the
// (already-correct) closer branch once state is wrongly "inside". A doc row
// naming a migration that does not exist on disk, sitting inside such a
// wrongly-open span, was silently dropped from rowLines entirely -- never
// seen by verifyDocRowsHaveMigration's reverse check at all, rather than
// being flagged as stale. See
// TestDocTableRowLinesParityFlipMaskingRegression for the reproduction.
func docTableRowLines(doc string) []string {
	var rows []string
	var fenceChar byte // 0 outside a fence; '`' or '~' while inside one
	var fenceLen int   // the opening fence's run length; a closer must be >= this
	inComment := false

	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)

		// Fenced code blocks. CommonMark requires the closing fence use the
		// same character as the opener (a ``` block isn't closed by ~~~ and
		// vice versa) and be at least as long as the opener (#0089: a
		// 4-backtick opener isn't closed by a 3-backtick line), so track
		// both which character opened it and how long that opening run was
		// rather than toggling a single boolean.
		if fenceChar == 0 {
			// #0224: an opener is capped at 3 columns of indentation, the
			// same threshold and the same helper the closer branch uses
			// (below) -- a single leading tab counts as 4 columns. A
			// backtick-run opener additionally may not have a backtick
			// anywhere in its info string (the text after the run);
			// CommonMark does not extend that restriction to a tilde-run
			// opener.
			indented := leadingIndentWidth(line) > 3
			switch {
			case !indented && leadingRunLength(trimmed, '`') >= 3 &&
				!strings.Contains(trimmed[leadingRunLength(trimmed, '`'):], "`"):
				fenceChar = '`'
				fenceLen = leadingRunLength(trimmed, '`')
				continue
			case !indented && leadingRunLength(trimmed, '~') >= 3:
				fenceChar = '~'
				fenceLen = leadingRunLength(trimmed, '~')
				continue
			}
		} else {
			// #0092: a closer must (a) run at least as long as the opener
			// (#0089's rule, unchanged), (b) be nothing but that run once
			// trimmed -- trimmed already dropped surrounding whitespace, so
			// requiring the run to account for the *whole* trimmed line is
			// how "only whitespace may follow the run" gets checked -- and
			// (c) sit at 3 columns of indentation or less on the raw,
			// untrimmed line.
			run := leadingRunLength(trimmed, fenceChar)
			if run >= fenceLen && run == len(trimmed) && leadingIndentWidth(line) <= 3 {
				fenceChar = 0
				fenceLen = 0
			}
			continue // every line while a fence is open is code, including its closer
		}

		// Multi-line HTML comments (<!-- ... --> spanning one or more
		// lines). A single-line comment wrapping a row on one line, e.g.
		// "<!-- | 000003 | stale | -->", needs no state here: its trimmed
		// form starts with "<!--", not "|", so the final prefix check below
		// already rejects it without ever seeing this branch.
		if inComment {
			if strings.Contains(line, "-->") {
				inComment = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "<!--") && !strings.Contains(trimmed, "-->") {
			inComment = true
			continue
		}

		// Indented code block (CommonMark: 4+ columns of leading
		// indentation). A table nested inside a list item is indented by
		// the marker's own width -- 2 spaces for "- ", 3 for "1. " -- not 4
		// columns, so this does not reject that case; see
		// TestDocTableRowLines's accept cases. leadingIndentWidth expands a
		// leading tab to the next 4-column tab stop (#0089) rather than
		// counting only ' ' bytes, so a tab-indented row is caught the same
		// way a 4-space-indented one already was.
		if leadingIndentWidth(line) >= 4 {
			continue
		}

		if strings.HasPrefix(trimmed, "|") {
			rows = append(rows, line)
		}
	}
	return rows
}

// leadingRunLength returns how many bytes at the start of s equal ch --
// docTableRowLines's way of measuring a fence marker's length (```` is 4,
// ``` is 3) rather than only checking for a fixed-length prefix (#0089).
func leadingRunLength(s string, ch byte) int {
	n := 0
	for n < len(s) && s[n] == ch {
		n++
	}
	return n
}

// leadingIndentWidth returns a line's leading indentation in columns,
// expanding a tab to the next 4-column tab stop the way CommonMark does
// (#0089) rather than counting only leading ' ' bytes. Starting at column 0,
// a single leading tab alone already reaches column 4 -- the same threshold
// four leading spaces reaches -- so a tab-indented line is recognized as an
// indented code block exactly like a space-indented one.
func leadingIndentWidth(line string) int {
	col := 0
	for _, r := range line {
		switch r {
		case ' ':
			col++
		case '\t':
			col += 4 - (col % 4)
		default:
			return col
		}
	}
	return col
}

// docStemToken finds every backtick-wrapped "NNNNNN_name" token -- the exact
// shape a migration's stem takes in a docs/database.md table row -- so the
// reverse check (drift mode 1) can walk every migration a doc row *names*,
// not just confirm the ones on disk are named somewhere.
var docStemToken = regexp.MustCompile("`(\\d{6}_[a-z0-9_]+)`")

// verifyDocRowsHaveMigration is drift mode 1 (#0083), the reverse direction
// of the original #0082 check: every migration named in a doc's table rows
// must still exist on disk. Moving 000009's files off disk while leaving its
// docs/database.md row behind previously left the suite green, because the
// original check only ever asked "is every disk file mentioned in the doc",
// never "does every doc mention correspond to a real disk file".
func verifyDocRowsHaveMigration(t *testing.T, path string, rowLines []string, onDisk map[string]bool) {
	t.Helper()
	seen := map[string]bool{}
	for _, line := range rowLines {
		for _, m := range docStemToken.FindAllStringSubmatch(line, -1) {
			stem := m[1]
			if seen[stem] {
				continue
			}
			seen[stem] = true
			if !onDisk[stem] {
				t.Errorf(
					"%s has a table row naming migration `%s`, but no migrations/%s.up.sql "+
						"exists on disk — edit %s and remove the stale row, or restore the "+
						"migration file — see #0083.",
					path, stem, stem, path,
				)
			}
		}
	}
}

// docRangePattern extracts the "`000001`–`000011`" range prose from a doc's
// migration-range sentence. docs/database.md phrases it "numbered
// `NNNNNN`-`NNNNNN`"; CLAUDE.md's "Repository layout" table row phrases the
// same fact "contiguous `NNNNNN`-`NNNNNN`" -- the loosened
// "(?:numbered|contiguous)" alternation (#0083) is what lets one
// table-driven test cover both files' wording rather than needing a sibling
// test per file. \x{2013} is an en dash (the character actually used in both
// files, confirmed against their bytes), tolerating a plain hyphen too in
// case a future edit normalizes it. The leading \b (#0413) keeps the
// alternation from matching a word that merely ends in "numbered" --
// "renumbered", which PRD.md's §3.1 row carries -- since without it the
// match starts mid-word rather than requiring numbered/contiguous as a
// whole word.
var docRangePattern = regexp.MustCompile("\\b(?:numbered|contiguous) `(\\d{6})`[–-]`(\\d{6})`")

// docParityCase is one file this test holds to the same migration-range
// claim. checkRows gates the per-migration row checks (drift modes 1 and 4,
// plus the forward check #0082 originally added): docs/database.md
// enumerates every migration as its own table row, so all three apply.
// CLAUDE.md's "Repository layout" table states only the aggregate range, not
// a per-migration row, so only the range check (drift mode 2's contiguity
// aside, which is disk-only and runs once above) applies to it.
type docParityCase struct {
	path      string
	checkRows bool
}

// TestDocTableRowLines exercises docTableRowLines directly against markdown
// forms that don't occur in the real docs today (#0086's reviewer proved
// each by probe against synthetic input, not against docs/database.md or
// CLAUDE.md). "rejects" is every form that must NOT satisfy the row check
// even though it contains a "|"-leading line somewhere; "accepts" is every
// form docTableRowLines correctly handled before this fix and must keep
// handling afterward.
func TestDocTableRowLines(t *testing.T) {
	t.Run("rejects", func(t *testing.T) {
		cases := []struct {
			name string
			doc  string
		}{
			{
				name: "ordinary prose",
				doc:  "This sentence mentions `000003_add_x` in passing, not as a table row.",
			},
			{
				name: "bullet list",
				doc:  "- `000003_add_x` -- stale bullet, not a table row",
			},
			{
				name: "blockquoted row",
				doc:  "> | `000003_add_x` | stale |",
			},
			{
				name: "single-line HTML comment",
				doc:  "<!-- | `000003_add_x` | stale | -->",
			},
			{
				// #0086 drift mode: a "|"-leading line indented 4+ raw
				// spaces is a CommonMark indented code block, not a table
				// row, even though the old check's only state was "inside a
				// ``` fence or not".
				name: "4-space indented code block",
				doc:  "Some paragraph.\n\n    | `000003_add_x` | stale |\n\nMore prose.",
			},
			{
				// #0086 drift mode: the row line itself starts with "|"
				// after trimming, so the old fence-only check counted it —
				// it never tracked HTML comment state at all.
				name: "multi-line HTML comment",
				doc:  "<!--\n| `000003_add_x` | stale |\n-->\n",
			},
			{
				// #0086 drift mode: the old check toggled on any line
				// starting with "```", so a "~~~"-fenced block's content
				// was never recognized as code in the first place.
				name: "tilde fence",
				doc:  "~~~\n| `000003_add_x` | stale |\n~~~\n",
			},
			{
				// #0089 residual 1: a tab is 4 columns of CommonMark
				// indentation, but strings.TrimLeft(line, " ") counts only
				// ' ' bytes, so a tab-indented "|"-leading line measured
				// indent 0 and satisfied the row check.
				name: "tab-indented code block",
				doc:  "Some paragraph.\n\n\t| `000003_add_x` | stale |\n\nMore prose.",
			},
			{
				// #0089 residual 2: a 4-backtick opener must be closed by a
				// run of 4+ backticks. The old check treated any 3-backtick
				// line as a closer, so the inner 3-backtick line here closed
				// the fence early and the row on the following line satisfied
				// the row check.
				name: "short closer does not end a longer backtick fence",
				doc:  "````\n```\n| `000003_add_x` | stale |\n````\n",
			},
			{
				// #0089 residual 2, tilde form: a 4-tilde opener must be
				// closed by a run of 4+ tildes; a 3-tilde line inside it is
				// fence content, not a closer.
				name: "short closer does not end a longer tilde fence",
				doc:  "~~~~\n~~~\n| `000003_add_x` | stale |\n~~~~\n",
			},
			{
				// #0092 residual 1: CommonMark caps a closing fence at 3
				// columns of indentation. The old check measured the run
				// length against trimmed, which had already discarded the
				// indentation, so a closer indented 4+ columns still closed
				// the fence.
				name: "closer indented 4+ spaces does not close the fence",
				doc:  "```\n| `000003_add_x` | stale |\n    ```\n| `000003_add_x` | stale |\n```\n",
			},
			{
				// #0092 residual 2: CommonMark permits only whitespace after
				// a closer's run. The old check only tested a prefix, so
				// "``` nope" (a leading run of 3 backticks) still closed a
				// 3-backtick fence even though "nope" follows it.
				name: "closer with trailing text does not close the fence",
				doc:  "```\n| `000003_add_x` | stale |\n``` nope\n| `000003_add_x` | stale |\n```\n",
			},
			{
				// #0224: unlike a backtick fence, CommonMark does not forbid
				// a backtick in a tilde fence's info string -- a backtick can
				// never be mistaken for part of a "~~~" marker. This proves
				// the opener fix didn't over-correct into rejecting tilde
				// opens generally.
				name: "tilde opener with backtick in info string still opens",
				doc:  "~~~ foo`bar\n| `000003_add_x` | stale |\n~~~\n",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := docTableRowLines(tc.doc); len(got) != 0 {
					t.Errorf("docTableRowLines(%q) = %v, want no rows", tc.doc, got)
				}
			})
		}
	})

	t.Run("accepts", func(t *testing.T) {
		cases := []struct {
			name     string
			doc      string
			wantRows int
		}{
			{
				name:     "tight pipes",
				doc:      "|`000003_add_x`|stale|",
				wantRows: 1,
			},
			{
				name: "alignment colons",
				doc: "| Migration | Description |\n" +
					"|:----------|------------:|\n" +
					"| `000003_add_x` | stale |",
				wantRows: 3,
			},
			{
				// Indented 2 spaces, matching a "- " bullet's content
				// indent -- not the 4+ spaces that would make it an
				// indented code block.
				name: "nested table in a list item",
				doc: "- Some list item describing migrations:\n" +
					"  | `000003_add_x` | stale |\n" +
					"  |---|---|",
				wantRows: 2,
			},
			{
				name: "table inside <details>",
				doc: "<details>\n" +
					"<summary>Migrations</summary>\n" +
					"\n" +
					"| `000003_add_x` | stale |\n" +
					"|---|---|\n" +
					"\n" +
					"</details>",
				wantRows: 2,
			},
			{
				// #0224 opener residual 1: CommonMark caps a fence *opener*
				// at 3 columns of indentation, the same as a closer. Before
				// the fix, the opener check discarded indentation entirely
				// (it ran against trimmed), so a 4-space-indented "```" still
				// opened a fence and masked the real row that follows.
				name:     "opener indented 4+ spaces does not open a fence",
				doc:      "    ```\n| `000003_add_x` | stale |\n",
				wantRows: 1,
			},
			{
				// #0224 opener residual 1, tab form: a single leading tab is
				// 4 columns under CommonMark's tab-stop rule, using the same
				// leadingIndentWidth the closer branch and the indented-code
				// check already depend on.
				name:     "tab-indented opener does not open a fence",
				doc:      "\t```\n| `000003_add_x` | stale |\n",
				wantRows: 1,
			},
			{
				// #0224 opener residual 2: CommonMark forbids a backtick
				// anywhere in a backtick fence's info string -- it would be
				// indistinguishable from the start of an inline code span.
				// Before the fix, the opener check never looked past the
				// run, so "``` foo`bar" still opened a fence and masked the
				// real row that follows.
				name:     "backtick in backtick fence info string does not open a fence",
				doc:      "``` foo`bar\n| `000003_add_x` | stale |\n",
				wantRows: 1,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := docTableRowLines(tc.doc)
				if len(got) != tc.wantRows {
					t.Errorf("docTableRowLines(%q) returned %d row(s) %v, want %d", tc.doc, len(got), got, tc.wantRows)
				}
			})
		}
	})
}

// TestDocTableRowLinesParityFlipMaskingRegression reproduces #0224's review
// scenario -- the composition its title refers to, and "the criterion that
// matters" per the issue: each opener residual is individually loud (a
// spurious open only ever turns real rows into masked code, which a missing
// doc row already surfaces), but composed with the already-correct closer
// branch (#0092) they cancel out silently.
//
// Line 2 is a "```" indented 4 columns -- pre-#0224, this satisfied
// leadingRunLength(trimmed, '`') >= 3 with indentation discarded, so it
// spuriously opened a fence (opener residual 1) even though CommonMark
// treats a 4-column-indented line as an indented code block, not a fence
// opener. Line 4, "``` bogus`info", never gets a chance to demonstrate
// opener residual 2 on its own -- by the time the loop reaches it, fenceChar
// is already (spuriously) non-zero from line 2, so it is evaluated by the
// closer branch instead of the opener branch. The already-correct closer
// logic (#0092) requires the run to be all of trimmed, and "``` bogus`info"
// has trailing text, so it correctly refuses to close -- which means the
// fence pre-#0224 never closes at all for the rest of the document, and
// every line from line 2 onward -- including a doc row naming a migration
// that does not exist on disk -- is masked out of rowLines entirely. That
// row never reaches verifyDocRowsHaveMigration's reverse check, so the
// parity guard reports ok despite the document actually containing a stale
// reference. Verified by hand against the pre-#0224 docTableRowLines (see
// this issue's `## Verification`): it returns only the first line as a row;
// this test asserts the post-#0224 function returns all three.
func TestDocTableRowLinesParityFlipMaskingRegression(t *testing.T) {
	doc := "| `000003_add_x` | real row one |\n" +
		"    ```\n" +
		"| `000999_stale_phantom` | should surface for the reverse check |\n" +
		"``` bogus`info\n" +
		"| `000003_add_x` | real row two |\n"

	want := []string{
		"| `000003_add_x` | real row one |",
		"| `000999_stale_phantom` | should surface for the reverse check |",
		"| `000003_add_x` | real row two |",
	}

	got := docTableRowLines(doc)
	if len(got) != len(want) {
		t.Fatalf("docTableRowLines(%q) returned %d row(s) %v, want %d row(s) %v", doc, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("docTableRowLines(%q) row %d = %q, want %q", doc, i, got[i], want[i])
		}
	}
}

// TestDatabaseDocMigrationParity is #0082's guard, extended by #0083 to be
// bidirectional, contiguity-aware, down-file-aware, and row-structure-aware,
// and to cover CLAUDE.md's migration-range sentence in the same table as
// docs/database.md's rather than in a sibling test.
func TestDatabaseDocMigrationParity(t *testing.T) {
	stems := listMigrations(t)
	onDisk := make(map[string]bool, len(stems))
	for _, m := range stems {
		onDisk[m.stem] = true
	}

	verifyDownPairs(t, stems)
	verifyContiguous(t, stems)

	lowest := stems[0].version
	highest := stems[len(stems)-1].version

	cases := []docParityCase{
		{path: docsPath, checkRows: true},
		{path: claudeMDPath, checkRows: false},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			docBytes, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			doc := string(docBytes)

			if tc.checkRows {
				rowLines := docTableRowLines(doc)

				// Forward: every migration on disk has a doc row (#0082,
				// tightened by #0083 to require an actual table row rather
				// than any substring match anywhere in the file).
				var missing []string
				for _, m := range stems {
					token := "`" + m.stem + "`"
					found := false
					for _, line := range rowLines {
						if strings.Contains(line, token) {
							found = true
							break
						}
					}
					if !found {
						missing = append(missing, m.stem)
					}
				}
				if len(missing) > 0 {
					t.Errorf(
						"%s is missing a table row for %d migration(s): %s\n"+
							"Edit %s and add a table row explaining each one — see #0082.",
						tc.path, len(missing), strings.Join(missing, ", "), tc.path,
					)
				}

				// Reverse (#0083 drift mode 1): every migration a doc row
				// names still exists on disk.
				verifyDocRowsHaveMigration(t, tc.path, rowLines, onDisk)
			}

			rangeMatch := docRangePattern.FindStringSubmatch(doc)
			if rangeMatch == nil {
				t.Fatalf(
					"could not find the \"numbered/contiguous `NNNNNN`–`NNNNNN`\" range "+
						"sentence in %s — has its wording changed? Update docRangePattern "+
						"in this test to match, or restore the sentence.",
					tc.path,
				)
			}
			// #0086 drift mode: only the upper bound was ever compared.
			// rangeMatch[1] was captured and echoed in messages but never
			// asserted, so a stale lower bound (e.g. a leftover "`000003`"
			// left behind after 000001/000002 were renumbered or restored)
			// passed green in both files -- the same class of unenforced
			// documented claim this guard exists to catch, sitting inside
			// the guard itself.
			statedLow, err := strconv.Atoi(rangeMatch[1])
			if err != nil {
				t.Fatalf("%s: range sentence's lower bound %q is not a plain integer: %v", tc.path, rangeMatch[1], err)
			}
			if statedLow != lowest {
				t.Errorf(
					"%s's migration range says `%06d`-`%s` but the lowest migration on "+
						"disk is %06d (%s)\n"+
						"Edit %s and correct the \"`NNNNNN`-`NNNNNN`\" range sentence — see #0086.",
					tc.path, statedLow, rangeMatch[2], lowest, stems[0].stem, tc.path,
				)
			}

			statedHigh, err := strconv.Atoi(rangeMatch[2])
			if err != nil {
				t.Fatalf("%s: range sentence's upper bound %q is not a plain integer: %v", tc.path, rangeMatch[2], err)
			}
			if statedHigh != highest {
				t.Errorf(
					"%s's migration range says `%s`–`%06d` but the highest migration on "+
						"disk is %06d (%s)\n"+
						"Edit %s and correct the \"`000001`–`NNNNNN`\" range sentence — see #0082.",
					tc.path, rangeMatch[1], statedHigh, highest, stems[len(stems)-1].stem, tc.path,
				)
			}
		})
	}
}
