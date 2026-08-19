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
// Placement note (#0083): internal/db's TestMain acquires testdb's shared
// advisory lock for the package's whole run, so when TEST_DATABASE_URL is
// set this filesystem-only test queues behind every database-backed test in
// this package even though it touches no database itself. That is a real,
// measurable cost. It is not fixed here: every other package that could host
// it without paying the same tax (internal/audit, internal/middleware,
// internal/interests, internal/subscribers) is *also* gated by that same
// lock via its own TestMain, and the packages that plausibly hold no lock at
// all -- internal/handlers, internal/mailing, internal/auth -- are exactly
// the packages #0083's concurrency instructions place off limits for this
// change (other subagents have in-flight, uncommitted work there right now).
// Standing up a brand-new, lock-free package purely to hold this test is a
// bigger structural move than "make the existing guard bidirectional" calls
// for, and would itself be a mid-flight collision risk. Keeping it in
// internal/db is the correct call *for this change*; revisit if a future
// issue frees up a natural, lock-free home.
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
// row -- its trimmed form starts with "|" -- and (b) not inside a ``` fenced
// code block. This is drift mode 4 (#0083): the original check was
// strings.Contains over the *entire file*, so a migration's token sitting in
// a fenced code sample, or in ordinary prose, satisfied it exactly as well
// as a real table row naming that migration. Both the forward check (every
// disk migration has a doc row) and the reverse check (every doc row names a
// real migration) below search only these lines, so neither a fence nor
// prose can stand in for a row.
func docTableRowLines(doc string) []string {
	var rows []string
	inFence := false
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, "|") {
			rows = append(rows, line)
		}
	}
	return rows
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
// case a future edit normalizes it.
var docRangePattern = regexp.MustCompile("(?:numbered|contiguous) `(\\d{6})`[–-]`(\\d{6})`")

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
