package testdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// This file implements #0360's live-schema census: a check that a
// database's realised schema actually matches what migrations/ claims to
// have applied, rather than trusting schema_migrations' version stamp.
// See CheckSchema's doc comment for the classification rules, and Lock's
// doc comment in testdb.go for why this runs from there.
//
// Every filesystem-reaching idiom in this package (runtime.Caller,
// the ".."-shaped relative path below) lives in THIS non-test file rather
// than in a _test.go file. CLAUDE.md's repo-wide-guard table only lists
// packages whose _test.go files reach outside their own directory —
// internal/db's parity guard scans _test.go files for exactly that
// pattern — so this package needs no new row there so long as the reach
// stays here.

// migrationsDir resolves <repo>/migrations from this file's own path, the
// same runtime.Caller(0) idiom internal/handlers' citation guards use.
//
// A missing or unresolvable migrations directory is a hard error, not a
// skip: in this repository the only way that can happen is a broken
// resolution (this file moved, or the checkout is incomplete), and a
// silent skip here would recreate exactly the fail-open shape #0476 closed
// in this same package — a caller believing a check ran when it did not.
func migrationsDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("testdb: runtime.Caller(0) failed while resolving migrations/")
	}
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("testdb: resolve migrations directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("testdb: resolved migrations path %s is not a directory", dir)
	}
	return dir, nil
}

// migrationFilePattern matches a golang-migrate up-migration filename, e.g.
// "000010_create_subscribers.up.sql", capturing the leading version number.
var migrationFilePattern = regexp.MustCompile(`^(\d+)_.*\.up\.sql$`)

// migrationFile is one parsed *.up.sql file: its version, base filename
// (the diagnosis a failure names — see CheckSchema), and its text with
// `--` line comments stripped.
type migrationFile struct {
	version int
	name    string
	body    string
}

// loadMigrationFiles reads every *.up.sql file in dir, strips its line
// comments, and returns them sorted ascending by version.
func loadMigrationFiles(dir string) ([]migrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("testdb: read migrations directory %s: %w", dir, err)
	}

	var files []migrationFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("testdb: migration filename %s has a non-numeric version: %w", e.Name(), err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("testdb: read %s: %w", e.Name(), err)
		}
		files = append(files, migrationFile{
			version: version,
			name:    e.Name(),
			body:    stripLineComments(string(raw)),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].version < files[j].version })
	return files, nil
}

// stripLineComments removes a `--` line comment and everything after it
// from each line, without cutting inside a single-quoted string literal.
// This is load-bearing, measured (#0360's plan, Finding 2): scanning
// migrations/ without stripping comments first reported 10 false
// positives on a healthy opencircuit_test, because prose like "the
// CREATE TABLE that owns the table" and a quoted `ADD COLUMN` clause an
// implementer was explaining both read as real DDL to a scanner that
// does not know about comments.
//
// The single-quote tracking is a plain per-line toggle: two single quotes
// in a row (Postgres's escaped-quote-inside-a-string form) toggle twice
// and therefore leave the in-string state exactly as a real escaped quote
// should. No migration file in this tree spans a string literal across a
// newline, so per-line tracking (rather than carrying state across lines)
// is sufficient here.
func stripLineComments(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		inString := false
		cut := -1
		for j := 0; j < len(line); j++ {
			c := line[j]
			if c == '\'' {
				inString = !inString
				continue
			}
			if !inString && c == '-' && j+1 < len(line) && line[j+1] == '-' {
				cut = j
				break
			}
		}
		if cut >= 0 {
			lines[i] = line[:cut]
		}
	}
	return strings.Join(lines, "\n")
}

// destructiveDDLPattern matches the three DDL shapes that would break a
// purely cumulative census: dropping a column or table removes an object
// an earlier migration's expectations still name, and a RENAME changes an
// object's identity out from under them. #0360's plan Finding 3 verified
// none of these appears anywhere in migrations/ today (checked across all
// 27 files); assertNoDestructiveDDL is what stops that premise from going
// stale silently, per the plan's step 1c.
var destructiveDDLPattern = regexp.MustCompile(`(?i)\bDROP\s+COLUMN\b|\bDROP\s+TABLE\b|\bRENAME\b`)

// assertNoDestructiveDDL fails closed — rather than let the census start
// silently emitting false positives — the moment any scanned migration
// contains DROP COLUMN, DROP TABLE, or RENAME. f.body has already had its
// comments stripped, so a match here is real DDL, not prose describing it.
// The error string deliberately does not cite an issue number: it can
// surface to whoever is running the tests, not only a developer who can
// open issues/ (CLAUDE.md §8, TestNoAdminFacingStringCitesInternalDocs).
func assertNoDestructiveDDL(f migrationFile) error {
	if loc := destructiveDDLPattern.FindString(f.body); loc != "" {
		return fmt.Errorf("testdb: %s contains %q — the cumulative schema census "+
			"assumes migrations/ never drops, renames, or removes a column, table, or "+
			"relation an earlier migration declared; that assumption no longer holds "+
			"and this census needs revisiting before it can be trusted", f.name, loc)
	}
	return nil
}

// createTablePattern locates a CREATE TABLE statement's opening paren,
// capturing the (optionally quoted) table name.
var createTablePattern = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?("?[A-Za-z_][A-Za-z0-9_]*"?)\s*\(`)

// alterTableHeadPattern captures the (optionally quoted) table name a
// single ALTER TABLE statement (one of the ';'-delimited chunks produced
// by splitting a file's body) applies to.
var alterTableHeadPattern = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+(?:ONLY\s+)?("?[A-Za-z_][A-Za-z0-9_]*"?)`)

// addColumnPattern finds every ADD COLUMN clause within one ALTER TABLE
// statement, including the multi-column form migrations/000023 uses
// (several ADD COLUMN clauses sharing one ALTER TABLE), which is why this
// is a FindAll over the whole statement text rather than a single match.
var addColumnPattern = regexp.MustCompile(`(?i)ADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?("?[A-Za-z_][A-Za-z0-9_]*"?)`)

// constraintKeywords are the table-level (not column-level) constraint
// introducers #0360's plan step 1b names: an item inside a CREATE TABLE
// body whose first word is one of these is a constraint clause, not a
// column declaration, and must not be recorded as one.
var constraintKeywords = map[string]bool{
	"PRIMARY":    true,
	"UNIQUE":     true,
	"CHECK":      true,
	"FOREIGN":    true,
	"CONSTRAINT": true,
	"EXCLUDE":    true,
	"LIKE":       true,
}

// expectedTable is one relation a migration's CREATE TABLE declares.
type expectedTable struct {
	name string
	file string
}

// expectedColumn is one column a migration declares, either in a CREATE
// TABLE body or an ALTER TABLE ... ADD COLUMN clause.
type expectedColumn struct {
	table  string
	column string
	file   string
}

// unquoteIdent strips one pair of enclosing double quotes, if present, and
// folds the result to lower case — Postgres folds unquoted identifiers to
// lower case, the same hazard #0208 hit in name_for, so an identifier read
// from SQL text must be folded the same way before it is compared against
// anything information_schema reports.
func unquoteIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, `"`)
	s = strings.TrimSuffix(s, `"`)
	return strings.ToLower(s)
}

// firstWord returns the leading identifier-shaped token of s (after
// trimming whitespace and an optional leading quote), used both to name a
// CREATE TABLE body item's column and to test it against
// constraintKeywords.
func firstWord(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, `"`)
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			i++
			continue
		}
		break
	}
	return s[:i]
}

// splitTopLevel splits body (a CREATE TABLE's parenthesized column-and-
// constraint list) on commas that sit at paren depth zero relative to
// body, so a comma inside a type modifier (NUMERIC(10,2)) or a CHECK
// clause's own parens does not split an item in two.
func splitTopLevel(body string) []string {
	var items []string
	depth := 0
	last := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				items = append(items, body[last:i])
				last = i + 1
			}
		}
	}
	items = append(items, body[last:])
	return items
}

// extractParenBody, given the index of a '(' in s, returns the text
// strictly between it and its matching ')' (balancing nested parens), plus
// the index of that closing ')'.
func extractParenBody(s string, openIdx int) (body string, closeIdx int, err error) {
	depth := 0
	start := -1
	for i := openIdx; i < len(s); i++ {
		switch s[i] {
		case '(':
			if depth == 0 {
				start = i + 1
			}
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[start:i], i, nil
			}
			if depth < 0 {
				return "", 0, fmt.Errorf("unbalanced ')' at offset %d", i)
			}
		}
	}
	return "", 0, fmt.Errorf("unterminated '(' starting at offset %d", openIdx)
}

// parseCreateTables scans body for every CREATE TABLE statement and
// returns the table it declares plus every column in its body — skipping
// table-level constraint clauses per constraintKeywords.
func parseCreateTables(fileName, body string) ([]expectedTable, []expectedColumn, error) {
	var tables []expectedTable
	var columns []expectedColumn

	matches := createTablePattern.FindAllStringSubmatchIndex(body, -1)
	for _, m := range matches {
		tableName := unquoteIdent(body[m[2]:m[3]])
		openIdx := m[1] - 1 // the '(' the pattern's own match ends on
		inner, _, err := extractParenBody(body, openIdx)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: CREATE TABLE %s: %w", fileName, tableName, err)
		}

		tables = append(tables, expectedTable{name: tableName, file: fileName})

		for _, item := range splitTopLevel(inner) {
			word := firstWord(item)
			if word == "" {
				continue
			}
			if constraintKeywords[strings.ToUpper(word)] {
				continue
			}
			columns = append(columns, expectedColumn{
				table:  tableName,
				column: strings.ToLower(word),
				file:   fileName,
			})
		}
	}
	return tables, columns, nil
}

// parseAlterTableAddColumns scans body for ALTER TABLE statements and
// returns every column an ADD COLUMN clause within one adds to that
// statement's table — including the multi-column form one ALTER TABLE
// shares across several ADD COLUMN clauses (migrations/000023).
func parseAlterTableAddColumns(fileName, body string) []expectedColumn {
	var columns []expectedColumn
	for _, stmt := range strings.Split(body, ";") {
		head := alterTableHeadPattern.FindStringSubmatch(stmt)
		if head == nil {
			continue
		}
		tableName := unquoteIdent(head[1])
		for _, add := range addColumnPattern.FindAllStringSubmatch(stmt, -1) {
			columns = append(columns, expectedColumn{
				table:  tableName,
				column: unquoteIdent(add[1]),
				file:   fileName,
			})
		}
	}
	return columns
}

// expectedObjectsFromFiles walks files (already sorted ascending by
// version) up to and including maxVersion, and returns every table and
// column those migrations declare, each tagged with the migration file
// that owns it — the diagnosis a failure names, per #0360's plan step 1e.
// A table or column declared more than once (there are none today) keeps
// only its first-seen owner.
//
// assertNoDestructiveDDL runs over every scanned file first: this census
// is purely additive, and Finding 3 is what makes that sound.
func expectedObjectsFromFiles(files []migrationFile, maxVersion int) ([]expectedTable, []expectedColumn, error) {
	var tables []expectedTable
	var columns []expectedColumn
	seenTable := map[string]bool{}
	seenColumn := map[string]bool{}

	for _, f := range files {
		if f.version > maxVersion {
			continue
		}
		if err := assertNoDestructiveDDL(f); err != nil {
			return nil, nil, err
		}

		fTables, fColumns, err := parseCreateTables(f.name, f.body)
		if err != nil {
			return nil, nil, err
		}
		fColumns = append(fColumns, parseAlterTableAddColumns(f.name, f.body)...)

		for _, t := range fTables {
			if seenTable[t.name] {
				continue
			}
			seenTable[t.name] = true
			tables = append(tables, t)
		}
		for _, c := range fColumns {
			key := c.table + "." + c.column
			if seenColumn[key] {
				continue
			}
			seenColumn[key] = true
			columns = append(columns, c)
		}
	}
	return tables, columns, nil
}

// classifyVersion is CheckSchema's pure classification step (#0360's plan
// step 1d), separated out so it is testable without a database. It
// returns a non-empty aheadWarning when dbVersion is ahead of diskVersion
// (a legitimate, recurring state per #0339 — never a failure on its own),
// and always returns the version the census should run through
// (min(dbVersion, diskVersion)) when it does not return an error.
//
//	dirty                    -> error (fail closed)
//	dbVersion > diskVersion  -> "ahead": aheadWarning set, census still runs
//	dbVersion < diskVersion  -> "behind": error (fail closed)
//	dbVersion == diskVersion -> "in agreement": census runs, no warning
func classifyVersion(dbName string, dbVersion, diskVersion int, dirty bool) (aheadWarning string, censusMax int, err error) {
	if dirty {
		return "", 0, fmt.Errorf("testdb: schema check for database %q FAILED (dirty) — "+
			"schema_migrations reports version=%d dirty=true, meaning a previous "+
			"migration run was interrupted mid-application. `migrate up` cannot repair "+
			"a dirty database on its own. Remedy: drop and recreate — "+
			"`scripts/testdb.sh create <id>`, or `scripts/db-reset.sh --force %s` for a "+
			"shared database", dbName, dbVersion, dbName)
	}

	if dbVersion < diskVersion {
		return "", 0, fmt.Errorf("testdb: schema check for database %q FAILED (behind) — "+
			"schema_migrations reports version=%d, but this checkout's migrations/ has "+
			"%d migrations on disk. Remedy: drop and recreate — "+
			"`scripts/testdb.sh create <id>`, or `scripts/db-reset.sh --force %s` for a "+
			"shared database", dbName, dbVersion, diskVersion, dbName)
	}

	censusMax = dbVersion
	if diskVersion < censusMax {
		censusMax = diskVersion
	}

	if dbVersion > diskVersion {
		// This is a legitimate, recurring state, not a defect: a shared
		// template can carry a migration a sibling worktree added on a
		// branch this checkout does not have (#0339). The message below
		// deliberately does not cite that issue number — it can surface to
		// whoever is running the tests, not only a developer who can open
		// issues/ (CLAUDE.md §8, TestNoAdminFacingStringCitesInternalDocs).
		aheadWarning = fmt.Sprintf("testdb: database %q schema_migrations version (%d) is "+
			"AHEAD of this checkout's migrations/ (highest on disk: %d) — built from a "+
			"migrations/ newer than this tree. That is a legitimate, recurring state, "+
			"not a defect: a shared template can carry a migration a sibling worktree "+
			"added on a branch this checkout does not have. Census over the shared "+
			"range (migrations ≤ v%d) came back clean — every object this checkout's "+
			"migrations/ declares is present.", dbName, dbVersion, diskVersion, censusMax)
	}

	return aheadWarning, censusMax, nil
}

// dbConn is the minimal surface CheckSchema needs from a live connection.
// Both *pgx.Conn (what Lock already has open) and *pgxpool.Pool satisfy
// it, so CheckSchema can run against whichever the caller already holds
// without opening a second connection.
type dbConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// actualTables returns the set of relation names Postgres actually has in
// schema "public".
func actualTables(ctx context.Context, conn dbConn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'`)
	if err != nil {
		return nil, fmt.Errorf("testdb: query information_schema.tables: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("testdb: scan information_schema.tables row: %w", err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("testdb: iterate information_schema.tables: %w", err)
	}
	return out, nil
}

// actualColumns returns the set of "table.column" pairs Postgres actually
// has in schema "public".
func actualColumns(ctx context.Context, conn dbConn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, `SELECT table_name, column_name FROM information_schema.columns WHERE table_schema = 'public'`)
	if err != nil {
		return nil, fmt.Errorf("testdb: query information_schema.columns: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, fmt.Errorf("testdb: scan information_schema.columns row: %w", err)
		}
		out[table+"."+column] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("testdb: iterate information_schema.columns: %w", err)
	}
	return out, nil
}

// CheckSchema is #0360's live-schema census. It classifies conn's
// schema_migrations state against dir's migrations (see classifyVersion),
// then — in every surviving case, including "ahead" — compares what
// migrations/ ≤ the shared version claims to have applied against what
// conn's information_schema actually reports. A version stamp cannot see
// a migration recorded as applied whose DDL never took effect (#0360's
// Description); only this comparison against the live catalog can.
//
// Running the census in the "ahead" case too is deliberate and cheap: a
// database ahead of this checkout still claims every migration this
// checkout has, so every object those files declare must still exist in
// it — the only way phantom DDL is caught on a database built from a
// newer migrations/ than this tree.
//
// On success, CheckSchema returns nil and — only in the "ahead" case —
// writes one warning line to stderr; it never writes anything on the
// ordinary "in agreement" path. On failure it returns an error naming the
// database, the recorded and on-disk versions, the verdict, every missing
// object as "table.column (declared in <file>)", and the remedy — see
// classifyVersion and the phantom-DDL message below for the exact text.
func CheckSchema(ctx context.Context, conn dbConn, dir string) error {
	var dbName string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		return fmt.Errorf("testdb: query current_database(): %w", err)
	}

	var dbVersion int
	var dirty bool
	if err := conn.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&dbVersion, &dirty); err != nil {
		return fmt.Errorf("testdb: read schema_migrations from database %q: %w", dbName, err)
	}

	files, err := loadMigrationFiles(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("testdb: no *.up.sql migration files found in %s", dir)
	}
	diskVersion := files[len(files)-1].version

	aheadWarning, censusMax, err := classifyVersion(dbName, dbVersion, diskVersion, dirty)
	if err != nil {
		return err
	}

	tables, columns, err := expectedObjectsFromFiles(files, censusMax)
	if err != nil {
		return err
	}

	existingTables, err := actualTables(ctx, conn)
	if err != nil {
		return err
	}
	existingColumns, err := actualColumns(ctx, conn)
	if err != nil {
		return err
	}

	var missing []string
	for _, t := range tables {
		if !existingTables[t.name] {
			missing = append(missing, fmt.Sprintf("table %s (declared in %s)", t.name, t.file))
		}
	}
	for _, c := range columns {
		if !existingTables[c.table] {
			// Already reported as a missing table above; don't also list
			// every one of its columns as a separate finding.
			continue
		}
		if !existingColumns[c.table+"."+c.column] {
			missing = append(missing, fmt.Sprintf("%s.%s (declared in %s)", c.table, c.column, c.file))
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("testdb: schema check for database %q FAILED (phantom DDL) — census of "+
			"migrations/ ≤ v%d found %d object(s) missing from the live schema though "+
			"schema_migrations records their owning migration as already applied: %s. "+
			"`migrate up` will exit 0 and fix nothing — golang-migrate correctly skips a "+
			"migration it already believes applied. Remedy: drop and recreate — "+
			"`scripts/testdb.sh create <id>`, or `scripts/db-reset.sh --force %s` for a "+
			"shared database", dbName, censusMax, len(missing), strings.Join(missing, "; "), dbName)
	}

	if aheadWarning != "" {
		fmt.Fprintln(os.Stderr, aheadWarning)
	}
	return nil
}
