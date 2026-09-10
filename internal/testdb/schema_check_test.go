package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// writeFixture writes name (a *.up.sql filename, e.g. "000001_x.up.sql")
// into dir with the given body, and fails the test on any I/O error.
//
// Every fixture in this file lives under t.TempDir(), and no test in this
// file uses runtime.Caller, os.Getwd, or a ".."-shaped path literal —
// deliberately: CLAUDE.md §5's repo-wide-guard parity test
// (internal/db's TestClaudeMDRepoWideGuardPackageSetParity) recognizes a
// package reaching outside its own directory only via those idioms
// appearing in a _test.go file, and the plan for #0360 keeps that reach
// confined to schema_check.go (a non-test file the parity guard does not
// scan) so this package needs no new row in that table.
func writeFixture(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

// TestParseCreateTables_ConstraintClausesAndCommaInTypeModifier is #0360's
// plan step 1b: a CREATE TABLE body containing a table-level CHECK and a
// table-level UNIQUE constraint, plus a column whose type modifier itself
// contains a comma (NUMERIC(10,2)) — the exact shape that breaks a naive
// comma split. Only the three real columns should be recorded; none of the
// constraint clauses should be mistaken for one.
func TestParseCreateTables_ConstraintClausesAndCommaInTypeModifier(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "000001_create_widgets.up.sql", `
CREATE TABLE widgets (
    id    BIGSERIAL PRIMARY KEY,
    price NUMERIC(10,2) NOT NULL,
    name  TEXT NOT NULL,
    CHECK (price >= 0 AND name <> ''),
    UNIQUE (name)
);
`)
	files, err := loadMigrationFiles(dir)
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	tables, columns, err := expectedObjectsFromFiles(files, 1)
	if err != nil {
		t.Fatalf("expectedObjectsFromFiles: %v", err)
	}

	if len(tables) != 1 || tables[0].name != "widgets" {
		t.Fatalf("expected exactly table widgets, got %+v", tables)
	}

	got := map[string]bool{}
	for _, c := range columns {
		if c.table != "widgets" {
			t.Fatalf("unexpected table on column: %+v", c)
		}
		got[c.column] = true
	}
	want := map[string]bool{"id": true, "price": true, "name": true}
	if len(got) != len(want) {
		t.Fatalf("expected exactly %v, got %v", want, got)
	}
	for col := range want {
		if !got[col] {
			t.Errorf("missing expected column %q; got %v", col, got)
		}
	}
	for col := range got {
		if !want[col] {
			t.Errorf("unexpected column %q recorded from a constraint clause; got %v", col, got)
		}
	}
}

// TestParseAlterTableAddColumns_MultiColumnBlock is #0360's plan step 1b's
// named case: migrations/000023_create_subscriber_imports.up.sql shares
// one ALTER TABLE across several ADD COLUMN clauses, so the table name
// must carry forward across all of them within that one statement.
func TestParseAlterTableAddColumns_MultiColumnBlock(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "000001_create_widgets.up.sql", `
CREATE TABLE widgets (
    id BIGSERIAL PRIMARY KEY
);
`)
	writeFixture(t, dir, "000002_add_widget_columns.up.sql", `
ALTER TABLE widgets
    ADD COLUMN color  TEXT,
    ADD COLUMN weight NUMERIC(5,2);
`)
	files, err := loadMigrationFiles(dir)
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	_, columns, err := expectedObjectsFromFiles(files, 2)
	if err != nil {
		t.Fatalf("expectedObjectsFromFiles: %v", err)
	}

	got := map[string]string{}
	for _, c := range columns {
		got[c.column] = c.file
	}
	for _, col := range []string{"id", "color", "weight"} {
		if _, ok := got[col]; !ok {
			t.Errorf("missing expected column %q; got %v", col, got)
		}
	}
	if got["color"] != "000002_add_widget_columns.up.sql" || got["weight"] != "000002_add_widget_columns.up.sql" {
		t.Errorf("color and weight should both be attributed to 000002_add_widget_columns.up.sql, got %v", got)
	}
}

// TestStripLineComments_ProseDescribingDDLYieldsNoObject is #0360's plan
// Finding 2, pinned as an asserted string literal per CLAUDE.md §8's
// prescription for a citation-guard-shaped example (#0384): without
// comment stripping, a scan of migrations/ reported 10 false positives on
// a healthy opencircuit_test, because prose like this fixture's reads as
// real DDL to a scanner that does not know about comments.
func TestStripLineComments_ProseDescribingDDLYieldsNoObject(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "000001_prose_only.up.sql", `
-- This migration does NOT run a CREATE TABLE ghosts (id INT) statement,
-- nor an ALTER TABLE ghosts ADD COLUMN spirit TEXT clause — both are
-- described here only, to prove a comment-shaped mention of either is not
-- mistaken for the real thing.
SELECT 1;
`)
	files, err := loadMigrationFiles(dir)
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	tables, columns, err := expectedObjectsFromFiles(files, 1)
	if err != nil {
		t.Fatalf("expectedObjectsFromFiles: %v", err)
	}
	if len(tables) != 0 {
		t.Errorf("expected zero tables from a comment-only file, got %+v", tables)
	}
	if len(columns) != 0 {
		t.Errorf("expected zero columns from a comment-only file, got %+v", columns)
	}
}

// TestExpectedObjectsFromFiles_MaxVersionTruncation is #0360's plan step
// 1d's censusMax behavior: an object declared in a migration above the cap
// must not be expected.
func TestExpectedObjectsFromFiles_MaxVersionTruncation(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "000001_create_widgets.up.sql", `
CREATE TABLE widgets (
    id BIGSERIAL PRIMARY KEY
);
`)
	writeFixture(t, dir, "000002_create_gadgets.up.sql", `
CREATE TABLE gadgets (
    id BIGSERIAL PRIMARY KEY
);
`)
	files, err := loadMigrationFiles(dir)
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}

	tables, _, err := expectedObjectsFromFiles(files, 1)
	if err != nil {
		t.Fatalf("expectedObjectsFromFiles: %v", err)
	}
	if len(tables) != 1 || tables[0].name != "widgets" {
		t.Fatalf("maxVersion=1 should only expect widgets, got %+v", tables)
	}

	tables, _, err = expectedObjectsFromFiles(files, 2)
	if err != nil {
		t.Fatalf("expectedObjectsFromFiles: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("maxVersion=2 should expect both tables, got %+v", tables)
	}
}

// TestExpectedObjectsFromFiles_RefusesDropColumn is #0360's plan step 1c:
// the cumulative census depends on no migration ever dropping, or
// renaming, an object an earlier migration declared (Finding 3). If that
// premise stops holding, this must fail closed rather than silently start
// emitting false positives.
func TestExpectedObjectsFromFiles_RefusesDropColumn(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "000001_create_widgets.up.sql", `
CREATE TABLE widgets (
    id    BIGSERIAL PRIMARY KEY,
    color TEXT
);
`)
	writeFixture(t, dir, "000002_drop_widget_color.up.sql", `
ALTER TABLE widgets DROP COLUMN color;
`)
	files, err := loadMigrationFiles(dir)
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	_, _, err = expectedObjectsFromFiles(files, 2)
	if err == nil {
		t.Fatal("expected expectedObjectsFromFiles to refuse a DROP COLUMN migration, got nil error")
	}
	if !strings.Contains(err.Error(), "000002_drop_widget_color.up.sql") {
		t.Errorf("error should name the offending file, got: %v", err)
	}
}

// TestExpectedObjectsFromFiles_RefusesDropTableAndRename covers the other
// two destructive shapes step 1c names, so DROP COLUMN is not the only one
// exercised.
func TestExpectedObjectsFromFiles_RefusesDropTableAndRename(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"drop table", "DROP TABLE widgets;"},
		{"rename", "ALTER TABLE widgets RENAME TO gizmos;"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "000001_create_widgets.up.sql", `
CREATE TABLE widgets (
    id BIGSERIAL PRIMARY KEY
);
`)
			writeFixture(t, dir, "000002_destructive.up.sql", tc.sql)
			files, err := loadMigrationFiles(dir)
			if err != nil {
				t.Fatalf("loadMigrationFiles: %v", err)
			}
			if _, _, err := expectedObjectsFromFiles(files, 2); err == nil {
				t.Fatalf("expected a refusal for %q, got nil error", tc.sql)
			}
		})
	}
}

// TestClassifyVersion_Dirty, _Behind, _Ahead, and _Equal are #0360's plan
// step 1d's four classifier cases. classifyVersion is a pure function
// (Postgres is never involved), so these run unconditionally regardless
// of TEST_DATABASE_URL.
func TestClassifyVersion_Dirty(t *testing.T) {
	_, _, err := classifyVersion("somedb", 27, 27, true)
	if err == nil {
		t.Fatal("expected an error for dirty=true")
	}
	if !strings.Contains(err.Error(), "dirty") {
		t.Errorf("error should name the dirty verdict, got: %v", err)
	}
}

func TestClassifyVersion_Behind(t *testing.T) {
	_, _, err := classifyVersion("somedb", 25, 27, false)
	if err == nil {
		t.Fatal("expected an error when dbVersion < diskVersion")
	}
	if !strings.Contains(err.Error(), "behind") {
		t.Errorf("error should name the behind verdict, got: %v", err)
	}
}

func TestClassifyVersion_Ahead(t *testing.T) {
	warning, censusMax, err := classifyVersion("somedb", 28, 27, false)
	if err != nil {
		t.Fatalf("ahead must not be an error, got: %v", err)
	}
	if warning == "" {
		t.Fatal("expected a non-empty ahead warning")
	}
	if !strings.Contains(warning, "AHEAD") {
		t.Errorf("warning should say AHEAD, got: %s", warning)
	}
	if censusMax != 27 {
		t.Errorf("censusMax should be min(28, 27) = 27, got %d", censusMax)
	}
}

func TestClassifyVersion_Equal(t *testing.T) {
	warning, censusMax, err := classifyVersion("somedb", 27, 27, false)
	if err != nil {
		t.Fatalf("equal must not be an error, got: %v", err)
	}
	if warning != "" {
		t.Errorf("equal must not warn, got: %s", warning)
	}
	if censusMax != 27 {
		t.Errorf("censusMax should be 27, got %d", censusMax)
	}
}

// TestCheckSchema_PrivateDatabase_DroppedColumnsDetected is #0360's own
// reproduction, DB-backed, against a private database (CLAUDE.md §8b —
// never the shared opencircuit_test or opencircuit). It migrates a fresh
// private database all the way up from this checkout's real migrations/
// (via migrate up, the same tool scripts/testdb.sh uses), confirms
// CheckSchema passes silently against that healthy database, then
// reproduces the exact defect #0360 was filed over — a migration recorded
// as applied whose DDL never took effect — by dropping the three columns
// migrations/000010_create_subscribers.up.sql declares, and asserts
// CheckSchema's error names all three and attributes them to that file.
func TestCheckSchema_PrivateDatabase_DroppedColumnsDetected(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if _, err := exec.LookPath("migrate"); err != nil {
		t.Skip("migrate not on PATH (CLAUDE.md §5b)")
	}
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}

	adminCfg := cfg.Copy()
	adminCfg.Database = "postgres"
	adminConn, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		t.Fatalf("connect to postgres maintenance db: %v", err)
	}

	// zz_ prefix keeps this outside the opencircuit_test_% pool
	// scripts/testdb.sh's gc sweeps (CLAUDE.md §5a), matching the
	// convention TestDiagnoseLockHolders_IgnoresLocksInOtherDatabase
	// already uses in this package.
	dbName := fmt.Sprintf("zz_testdb_0360_%d", Unique())

	t.Cleanup(func() {
		bg := context.Background()
		_, _ = adminConn.Exec(bg, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()", dbName)
		_, _ = adminConn.Exec(bg, "DROP DATABASE IF EXISTS "+dbName)
		_ = adminConn.Close(bg)
	})

	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("CREATE DATABASE %s: %v — does the connecting role have CREATEDB? (CLAUDE.md §5b)", dbName, err)
	}

	dir, err := migrationsDir()
	if err != nil {
		t.Fatalf("migrationsDir: %v", err)
	}

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL as a URL: %v", err)
	}
	u.Path = "/" + dbName
	scratchDSN := u.String()

	migrateOut, err := exec.Command("migrate", "-path", dir, "-database", scratchDSN, "up").CombinedOutput()
	if err != nil {
		t.Fatalf("migrate up against %s failed: %v\n%s", dbName, err, migrateOut)
	}

	scratchCfg := cfg.Copy()
	scratchCfg.Database = dbName
	conn, err := pgx.ConnectConfig(ctx, scratchCfg)
	if err != nil {
		t.Fatalf("connect to %s: %v", dbName, err)
	}
	defer func() { _ = conn.Close(ctx) }()

	// Sanity: a database freshly migrated all the way up from this
	// checkout's own migrations/ must pass silently — this is the "healthy
	// database" half of #0360's target, alongside the drifted half below.
	if err := CheckSchema(ctx, conn, dir); err != nil {
		t.Fatalf("CheckSchema on a freshly migrated database should pass, got: %v", err)
	}

	// Reproduce #0360's own defect: migrations/000010_create_subscribers.up.sql
	// is the migration whose DDL no longer holds, even though the version
	// table still records it as applied.
	if _, err := conn.Exec(ctx, "ALTER TABLE subscribers "+
		"DROP COLUMN soft_bounce_streak, "+
		"DROP COLUMN last_bounce_at, "+
		"DROP COLUMN last_delivery_at"); err != nil {
		t.Fatalf("drop reproduction columns: %v", err)
	}

	err = CheckSchema(ctx, conn, dir)
	if err == nil {
		t.Fatal("expected CheckSchema to fail after dropping subscribers' three columns, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"subscribers.soft_bounce_streak",
		"subscribers.last_bounce_at",
		"subscribers.last_delivery_at",
		"000010_create_subscribers.up.sql",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected failure message to contain %q; got: %s", want, msg)
		}
	}
}
