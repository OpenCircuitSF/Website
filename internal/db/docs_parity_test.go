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
// Design choice: this lives in internal/db (the package that already owns
// "manages the PostgreSQL connection pool and migrations", per its doc
// comment) rather than in internal/handlers, where #0071's parity test
// lives — internal/handlers is unrelated to migrations and, as of this
// writing, has another issue's work in flight.
//
// migrationsDir and docsPath are relative to this package's directory
// (internal/db), which is also `go test`'s working directory for this
// package regardless of where the `go test ./...` invocation itself runs
// from.
const (
	migrationsDir = "../../migrations"
	docsPath      = "../../docs/database.md"
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

// docRangePattern extracts the "`000001`–`000011`" range prose from
// docs/database.md's opening paragraph. \x{2013} is an en dash (the
// character actually used in the doc, confirmed against the file's bytes),
// tolerating a plain hyphen too in case a future edit normalizes it.
var docRangePattern = regexp.MustCompile("numbered `(\\d{6})`[–-]`(\\d{6})`")

// TestDatabaseDocMigrationParity is #0082's guard: every migrations/*.up.sql
// file must have a corresponding row in docs/database.md (its "NNNNNN_name"
// stem appearing backtick-wrapped somewhere in the file), and the doc's
// stated "numbered 000001-NNNNNN" range must end at the actual highest
// migration version on disk.
func TestDatabaseDocMigrationParity(t *testing.T) {
	stems := listMigrations(t)

	docBytes, err := os.ReadFile(docsPath)
	if err != nil {
		t.Fatalf("read %s: %v", docsPath, err)
	}
	doc := string(docBytes)

	var missing []string
	for _, m := range stems {
		token := "`" + m.stem + "`"
		if !strings.Contains(doc, token) {
			missing = append(missing, m.stem)
		}
	}
	if len(missing) > 0 {
		t.Errorf(
			"docs/database.md is missing a row for %d migration(s): %s\n"+
				"Edit docs/database.md and add a table row explaining each one — see #0082.",
			len(missing), strings.Join(missing, ", "),
		)
	}

	highest := stems[len(stems)-1].version
	rangeMatch := docRangePattern.FindStringSubmatch(doc)
	if rangeMatch == nil {
		t.Fatalf(
			"could not find the \"numbered `NNNNNN`–`NNNNNN`\" range sentence in " +
				"docs/database.md — has its wording changed? Update docRangePattern " +
				"in this test to match, or restore the sentence.",
		)
	}
	statedHigh, err := strconv.Atoi(rangeMatch[2])
	if err != nil {
		t.Fatalf("range sentence's upper bound %q is not a plain integer: %v", rangeMatch[2], err)
	}
	if statedHigh != highest {
		t.Errorf(
			"docs/database.md's migration range says `%s`–`%06d` but the highest "+
				"migration on disk is %06d (%s)\n"+
				"Edit docs/database.md and correct the \"numbered `000001`–`NNNNNN`\" "+
				"sentence — see #0082.",
			rangeMatch[1], statedHigh, highest, stems[len(stems)-1].stem,
		)
	}
}
