package db

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// #0148: PRD.md §6.2's SQL block is a literal transcription of the mailing
// list and workshop schema, and it fell behind twice on the same index —
// #0135 widened idx_workshops_published's predicate from `status =
// 'published'` to `status <> 'draft'` and #0142 renamed it to
// idx_workshops_visible, and PRD.md:671 kept saying neither. Nothing caught
// it mechanically; TestDatabaseDocMigrationParity (docs_parity_test.go)
// guards docs/database.md and CLAUDE.md against the migrations, but nothing
// played that role for PRD.md.
//
// This guard is deliberately narrower than that one, for a reason that is
// itself the finding #0148 was filed to record: PRD.md is a specification,
// not documentation, and a spec is allowed to lead the implementation. §6.2
// already describes subscribers.source/invited_at/consent_basis/import_id
// (§6.10, subscriber import provenance), subscribers.soft_bounce_streak/
// last_bounce_at/last_delivery_at (§6.9's circuit breaker, which explicitly
// *supersedes* the shipped windowed rule), email_campaigns.slug/
// archive_status/archived_at (§6.8's archive, owned by #0123), and the
// subscriber_imports/subscriber_events/outbound_queue tables outright —
// none of which any migration has shipped yet (Phase 8, #0123-0129). A
// parity guard that fired on all of that would be false-positive noise
// every time the spec did its job; the only way to quiet it would be an
// allowlist of "known future work" that itself needs to be kept in sync,
// which just relocates the drift risk this guard exists to catch.
//
// Two scoping decisions resolve that tension without an allowlist:
//
//  1. One direction only: migrations -> PRD, never PRD -> migrations. This
//     test walks every index that actually exists (replayed from
//     migrations/*.up.sql) and asserts PRD.md §6.2 names it, correctly. It
//     never asks the reverse question ("does every index PRD.md names
//     exist"), so a forward-looking index PRD documents ahead of its
//     migration — idx_email_events_recipient_time, written for §6.9's
//     /admin/deliverability page, which does not exist yet — simply never
//     enters the live-index map this test walks and is never flagged.
//  2. Indexes, and (as of #0154) columns — never a CHECK constraint. §6.2 has
//     never attempted to transcribe those (PRD.md never lists e.g.
//     interests_slug_format or subscribers_email_normalized — that level of
//     detail lives in docs/database.md's migration-by-migration prose
//     instead), and nothing has ever drifted on one, unlike indexes
//     (#0135, #0142 — two separate drifts on the same index) and columns
//     (email_sends.claimed_at, #0122's OrphanSweep timestamp, added by
//     migrations/000018 but missing from PRD.md until #0148 fixed it by
//     hand). #0148 originally deferred the column check as "materially more
//     parsing surface"; #0148's own review prototyped it in ~30 lines during
//     the sweep that found the deferral's stated reason was cost, not
//     difficulty, and #0154 built it: TestPRDWorkshopAndMailingColumnParity
//     below, same one-direction scope, same prdIndexParityTables.
//
// #0154 also closed a silent failure mode in the index guard itself: the
// original createIndexPattern had no match at all for CREATE UNIQUE INDEX,
// CREATE INDEX CONCURRENTLY, CREATE INDEX IF NOT EXISTS, or a statement
// missing its terminating ";" — and a no-match on the migrations side just
// meant the index never entered the live map, silently, with the suite
// staying green. The prefix is now widened to parse the first three forms,
// and checkIndexStatementCoverage below counts "CREATE [UNIQUE] INDEX"
// occurrences against what the pattern actually matched so any remaining
// unparseable form (including a missing ";") fails the test loudly instead
// of vanishing. SQL comments are stripped before any of this matching runs
// (stripSQLComments), so a commented-out `-- CREATE INDEX …` cannot register
// as live on either side.
//
// Placement: same package as TestDatabaseDocMigrationParity for the same
// reason that test gives (internal/db owns migrations), and to reuse its
// listMigrations/migrationsDir rather than duplicating the migration-file
// walk a second time.

const prdPath = "../../PRD.md"

// prdIndexParityTables is the exact set of tables PRD.md §6.2 specifies with
// a literal CREATE TABLE statement — everything that section transcribes in
// full, as opposed to the "copied auth tables" line at the end of §6.2 that
// names users/passkey_credentials/webauthn_challenges/pending_registrations/
// sessions/settings/audit_log without detailing them. An index on one of
// those auth tables (e.g. idx_audit_log_action) is correctly absent from
// §6.2 and must never be flagged here.
//
// subscriber_imports, subscriber_events, and outbound_queue are also fully
// specified in §6.2 but have no migration yet (Phase 8, #0123-0129) — they
// are listed here for completeness of intent, but in practice contribute no
// entries to the migrations-derived live index map below, so their presence
// or absence in this set has no effect until that phase ships.
var prdIndexParityTables = map[string]bool{
	"subscribers":          true,
	"interests":            true,
	"subscriber_interests": true,
	"suppressions":         true,
	"email_campaigns":      true,
	"campaign_interests":   true,
	"email_sends":          true,
	"email_events":         true,
	"subscriber_imports":   true,
	"subscriber_events":    true,
	"outbound_queue":       true,
	"workshops":            true,
	"workshop_interests":   true,
}

// createIndexPattern and dropIndexPattern extract CREATE/DROP INDEX
// statements from raw SQL, whether migration file or PRD.md prose. Both are
// deliberately narrow rather than a real SQL parser — every CREATE INDEX in
// this project's migrations and PRD.md has an unparenthesized column list
// (no nested parens before the list's own closing paren), so a non-greedy
// "first close-paren wins" match is safe. The WHERE predicate is delimited
// only by the statement's terminating ";" (not by paren-balancing), which is
// what lets idx_email_events_soft_bounce's predicate — itself containing a
// nested `(bounce_subtype IS NULL OR ...)` — match correctly without any
// paren-depth tracking.
//
// The prefix accepts UNIQUE, CONCURRENTLY, and IF NOT EXISTS, in Postgres's
// required order (CREATE [UNIQUE] INDEX [CONCURRENTLY] [IF NOT EXISTS]
// name ON table …) — #0154: none of the three exist in this tree today, but
// the first one anyone writes must not be invisible to this guard the way it
// was before. Capture group numbering is unaffected: the three added clauses
// are non-capturing, so group 1 is still the index name, 2 the table, 3 the
// column list, 4 the predicate — every caller indexing loc[2:3]/[4:5]/[6:7]/
// [8:9] is unchanged.
var (
	createIndexPattern     = regexp.MustCompile(`(?i)CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:CONCURRENTLY\s+)?(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s+ON\s+(\w+)\s*(\([^;]*?\))(?:\s+WHERE\s+([^;]*?))?;`)
	dropIndexPattern       = regexp.MustCompile(`(?i)DROP\s+INDEX\s+(?:IF\s+EXISTS\s+)?(\w+)\s*;`)
	looseIndexStartPattern = regexp.MustCompile(`(?i)CREATE\s+(?:UNIQUE\s+)?INDEX\b`)
	sqlCommentPattern      = regexp.MustCompile(`--[^\n]*`)
)

// stripSQLComments removes every SQL line comment (`-- ...` to end of line)
// from text before any pattern in this file runs over it. Applied uniformly
// to both sides of every comparison so (a) a commented-out
// `-- CREATE INDEX …`/`-- CREATE TABLE …` can never register as a live
// definition, and (b) checkIndexStatementCoverage's loose count and
// createIndexPattern's strict count are counting the same text.
func stripSQLComments(s string) string {
	return sqlCommentPattern.ReplaceAllString(s, "")
}

// checkIndexStatementCoverage is the guard for the guard (#0154). It counts
// every "CREATE [UNIQUE] INDEX" occurrence in text (already comment-stripped)
// and compares it against how many statements createIndexPattern actually
// matched. Before #0154, a form the pattern couldn't parse — CREATE UNIQUE
// INDEX, CREATE INDEX CONCURRENTLY, CREATE INDEX IF NOT EXISTS, or a
// statement missing its terminating ";" — simply produced no match and no
// error, so the index vanished from the live map in total silence. The first
// three are now parsed by the widened prefix above; this check is what makes
// any *remaining* unparseable form (a missing ";", or some fifth form nobody
// has written yet) fail loudly instead of vanishing the same way.
func checkIndexStatementCoverage(t *testing.T, text, label string) {
	t.Helper()
	found := len(looseIndexStartPattern.FindAllStringIndex(text, -1))
	matched := len(createIndexPattern.FindAllStringIndex(text, -1))
	if found != matched {
		t.Errorf(
			"%s: found %d \"CREATE [UNIQUE] INDEX\" occurrence(s) but only "+
				"parsed %d as a complete statement — one of them uses a SQL form "+
				"createIndexPattern can't parse (check for a missing terminating "+
				"';', or a form not yet handled). A silent skip here means the "+
				"index is invisible to this guard — fix the statement or widen "+
				"createIndexPattern, don't leave the mismatch. See #0154.",
			label, found, matched,
		)
	}
}

// indexDef is one CREATE INDEX statement's parsed shape, from either side of
// the comparison (a migration file or PRD.md's §6.2 section).
type indexDef struct {
	table      string
	columns    string // including the parens, e.g. "(starts_at DESC)"
	predicate  string // text after WHERE, before ";"; "" if unconditional
	sourceFile string // migration stem, for error messages; unset for PRD-side defs
}

// liveIndexesFromMigrations replays every CREATE/DROP INDEX statement across
// migrations/*.up.sql — in version order, and in the order each statement
// appears within a file — to compute each index's *current* definition. Most
// index changes in this codebase land as an in-place edit to the
// still-unapplied migration file itself (CLAUDE.md §1's greenfield
// exception: idx_workshops_published's rename and predicate widen both
// happened this way, directly inside migrations/000020), so simply reading
// the final file content already gives the live definition for those. The
// replay exists for the one index that instead went through a real
// DROP+CREATE pair across two files — idx_email_events_soft_bounce, created
// by 000014 and redefined by 000016 dropping and recreating it with a wider
// predicate — where only the DROP-then-CREATE ordering, not "read the file
// once", gives the right final answer.
func liveIndexesFromMigrations(t *testing.T) map[string]indexDef {
	t.Helper()
	stems := listMigrations(t)
	live := map[string]indexDef{}

	type op struct {
		pos  int
		drop bool
		name string
		def  indexDef
	}

	for _, m := range stems {
		path := migrationsDir + "/" + m.stem + ".up.sql"
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := stripSQLComments(string(content))
		checkIndexStatementCoverage(t, text, "migrations/"+m.stem+".up.sql")

		var ops []op
		for _, loc := range dropIndexPattern.FindAllStringSubmatchIndex(text, -1) {
			ops = append(ops, op{pos: loc[0], drop: true, name: text[loc[2]:loc[3]]})
		}
		for _, loc := range createIndexPattern.FindAllStringSubmatchIndex(text, -1) {
			name := text[loc[2]:loc[3]]
			def := indexDef{
				table:      text[loc[4]:loc[5]],
				columns:    text[loc[6]:loc[7]],
				sourceFile: m.stem,
			}
			if loc[8] != -1 {
				def.predicate = text[loc[8]:loc[9]]
			}
			ops = append(ops, op{pos: loc[0], drop: false, name: name, def: def})
		}
		sort.Slice(ops, func(i, j int) bool { return ops[i].pos < ops[j].pos })
		for _, o := range ops {
			if o.drop {
				delete(live, o.name)
			} else {
				live[o.name] = o.def
			}
		}
	}
	return live
}

// prdIndexesFromSection parses every CREATE INDEX statement out of a slice
// of PRD.md text (§6.2's section, already extracted by extractPRDSection).
func prdIndexesFromSection(section string) map[string]indexDef {
	defs := map[string]indexDef{}
	for _, loc := range createIndexPattern.FindAllStringSubmatchIndex(section, -1) {
		name := section[loc[2]:loc[3]]
		def := indexDef{
			table:   section[loc[4]:loc[5]],
			columns: section[loc[6]:loc[7]],
		}
		if loc[8] != -1 {
			def.predicate = section[loc[8]:loc[9]]
		}
		defs[name] = def
	}
	return defs
}

// columnDef is one column's provenance, from either side of the column
// parity comparison. Only the migrations side ever populates sourceFile —
// the PRD side needs no error message pointer to itself.
type columnDef struct {
	sourceFile string // migration stem; unset for PRD-side defs
}

// tableConstraintKeywords are the leading tokens that mark a CREATE TABLE
// body's top-level comma-separated segment as a table-level constraint
// (PRIMARY KEY (...), UNIQUE (...), CHECK (...), FOREIGN KEY (...),
// CONSTRAINT name ..., EXCLUDE (...)) rather than a column definition. No
// column in this schema is ever named one of these bare reserved words, so
// checking a segment's first token is sufficient without a real SQL parser
// — the same "narrow, not a parser" tradeoff createIndexPattern already
// makes.
var tableConstraintKeywords = map[string]bool{
	"PRIMARY":    true,
	"UNIQUE":     true,
	"CHECK":      true,
	"FOREIGN":    true,
	"CONSTRAINT": true,
	"EXCLUDE":    true,
}

// createTableStart matches "CREATE TABLE name (" and its match end lands
// exactly one byte past the opening paren, which is what lets callers derive
// the paren's own index as loc[1]-1.
var createTableStart = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(\w+)\s*\(`)

// columnAddPattern matches the one ALTER TABLE form this project's
// migrations use to grow a table after its CREATE TABLE: a single-column
// `ALTER TABLE t ADD COLUMN c ...;` (CLAUDE.md §1's append-only-migrations
// rule plus its greenfield exception — e.g. migrations/000011, 000018,
// 000019).
var columnAddPattern = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+(\w+)\s+ADD\s+COLUMN\s+(\w+)`)

// splitTopLevel splits s on commas that are not nested inside parens, so a
// column definition like "import_id BIGINT REFERENCES
// subscriber_imports(id)" is never mistaken for two segments by the comma
// that doesn't exist there, and — more importantly — so a table constraint
// like "PRIMARY KEY (subscriber_id, interest_id)" is treated as the single
// segment it is rather than split apart by its own internal comma.
func splitTopLevel(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// columnsFromCreateTable extracts every column name from one
// "CREATE TABLE name ( ... )" body, given the index of its opening paren
// (openParen, as produced by createTableStart's match end minus one). It
// finds the matching close paren by depth-tracking rather than assuming a
// single unnested pair — column definitions here nest parens freely
// (REFERENCES t(id), DEFAULT now()) — then splits the body on top-level
// commas and takes each segment's first token as the column name, skipping
// any segment whose first token is a table constraint keyword.
func columnsFromCreateTable(text string, openParen int) (cols []string, bodyEnd int) {
	depth := 0
	end := -1
	for i := openParen; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end != -1 {
			break
		}
	}
	if end == -1 {
		end = len(text)
	}
	body := text[openParen+1 : end]
	for _, seg := range splitTopLevel(body) {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		if tableConstraintKeywords[strings.ToUpper(fields[0])] {
			continue
		}
		cols = append(cols, fields[0])
	}
	return cols, end
}

// liveColumnsFromMigrations replays every CREATE TABLE and
// ALTER TABLE ADD COLUMN statement across migrations/*.up.sql — in version
// order and by byte position within each file, the same ordering
// liveIndexesFromMigrations uses and for the same reason: today every column
// change in this tree is a monotonic addition (CREATE TABLE's own list, or a
// later ADD COLUMN — migrations/000011, 000018, 000019), so a plain union
// gives the right answer, but the position-ordered replay is what keeps that
// true if a future migration ever DROPs and recreates a table or drops a
// column, exactly as idx_email_events_soft_bounce's DROP+CREATE pair is what
// makes the *index* replay's ordering load-bearing rather than incidental.
func liveColumnsFromMigrations(t *testing.T) map[string]map[string]columnDef {
	t.Helper()
	stems := listMigrations(t)
	live := map[string]map[string]columnDef{}

	type op struct {
		pos     int
		table   string
		columns []string
	}

	for _, m := range stems {
		path := migrationsDir + "/" + m.stem + ".up.sql"
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := stripSQLComments(string(content))

		var ops []op
		for _, loc := range createTableStart.FindAllStringSubmatchIndex(text, -1) {
			table := text[loc[2]:loc[3]]
			openParen := loc[1] - 1
			cols, _ := columnsFromCreateTable(text, openParen)
			ops = append(ops, op{pos: loc[0], table: table, columns: cols})
		}
		for _, loc := range columnAddPattern.FindAllStringSubmatchIndex(text, -1) {
			table := text[loc[2]:loc[3]]
			col := text[loc[4]:loc[5]]
			ops = append(ops, op{pos: loc[0], table: table, columns: []string{col}})
		}
		sort.Slice(ops, func(i, j int) bool { return ops[i].pos < ops[j].pos })
		for _, o := range ops {
			if live[o.table] == nil {
				live[o.table] = map[string]columnDef{}
			}
			for _, c := range o.columns {
				live[o.table][c] = columnDef{sourceFile: m.stem}
			}
		}
	}
	return live
}

// prdColumnsFromSection parses every CREATE TABLE's column list out of a
// slice of PRD.md text (§6.2's section, already extracted by
// extractPRDSection and comment-stripped by the caller).
func prdColumnsFromSection(section string) map[string]map[string]columnDef {
	tables := map[string]map[string]columnDef{}
	for _, loc := range createTableStart.FindAllStringSubmatchIndex(section, -1) {
		table := section[loc[2]:loc[3]]
		openParen := loc[1] - 1
		cols, _ := columnsFromCreateTable(section, openParen)
		set := tables[table]
		if set == nil {
			set = map[string]columnDef{}
			tables[table] = set
		}
		for _, c := range cols {
			set[c] = columnDef{}
		}
	}
	return tables
}

// normalizeSQL collapses whitespace runs (including newlines) to single
// spaces and trims the ends, so a predicate or column list wrapped across
// several lines in one source and written on one line in the other still
// compares equal. It does not touch case, punctuation, or token order — a
// genuine drift (a renamed column, a widened predicate, `<>` vs `=`) still
// fails.
func normalizeSQL(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// extractPRDSection returns the text between a heading line matching
// "^headingPrefix" and the next "## N" or "### N" heading, mirroring
// CLAUDE.md §11's extraction command for this exact section —
// `sed -n '/^### 6\.2 /,/^#{2,3} [0-9]/p' PRD.md` — so this test reads
// precisely the slice an agent following that instruction would read, never
// PRD.md's 1,769 lines whole.
func extractPRDSection(t *testing.T, doc, headingPrefix string) string {
	t.Helper()
	startRe := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(headingPrefix))
	loc := startRe.FindStringIndex(doc)
	if loc == nil {
		t.Fatalf("PRD.md: heading %q not found — has §6.2 moved or been renamed? Update extractPRDSection's caller — see #0148.", headingPrefix)
	}
	rest := doc[loc[0]:]
	firstNewline := strings.IndexByte(rest, '\n')
	if firstNewline == -1 {
		return rest
	}
	afterHeading := rest[firstNewline+1:]
	endRe := regexp.MustCompile(`(?m)^#{2,3} \d`)
	endLoc := endRe.FindStringIndex(afterHeading)
	if endLoc == nil {
		return rest
	}
	return rest[:firstNewline+1+endLoc[0]]
}

// TestPRDWorkshopAndMailingIndexParity is #0148's guard — see the package
// comment above for why it is scoped to indexes and to one direction
// (migrations -> PRD.md) only.
func TestPRDWorkshopAndMailingIndexParity(t *testing.T) {
	live := liveIndexesFromMigrations(t)

	prdBytes, err := os.ReadFile(prdPath)
	if err != nil {
		t.Fatalf("read %s: %v", prdPath, err)
	}
	section := stripSQLComments(extractPRDSection(t, string(prdBytes), "### 6.2 "))
	checkIndexStatementCoverage(t, section, "PRD.md §6.2")
	prdIndexes := prdIndexesFromSection(section)

	var names []string
	for name, def := range live {
		if prdIndexParityTables[def.table] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		want := live[name]
		got, ok := prdIndexes[name]
		if !ok {
			t.Errorf(
				"PRD.md §6.2 does not mention index %q (table %s), defined in "+
					"migrations/%s.up.sql — PRD.md's schema listing has fallen behind "+
					"the migration. Add or correct the CREATE INDEX line in PRD.md §6.2 — see #0148.",
				name, want.table, want.sourceFile,
			)
			continue
		}
		if normalizeSQL(got.columns) != normalizeSQL(want.columns) ||
			normalizeSQL(got.predicate) != normalizeSQL(want.predicate) {
			t.Errorf(
				"PRD.md §6.2's %q index has drifted from migrations/%s.up.sql:\n"+
					"  PRD.md:    ON %s %s WHERE %s\n"+
					"  migration: ON %s %s WHERE %s\n"+
					"Correct PRD.md §6.2's CREATE INDEX line to match — see #0148.",
				name, want.sourceFile,
				got.table, normalizeSQL(got.columns), orNone(normalizeSQL(got.predicate)),
				want.table, normalizeSQL(want.columns), orNone(normalizeSQL(want.predicate)),
			)
		}
	}
}

// TestPRDWorkshopAndMailingColumnParity is #0154's column-parity guard,
// deferred by #0148 and built here. Same one-direction scope as the index
// guard above, for the identical reason (see the package comment): every
// column a migration has actually created on a prdIndexParityTables table
// must appear, by name, in PRD.md §6.2's CREATE TABLE for that table. It
// never asks the reverse question, so the Phase 8 forward-declared columns
// (subscribers.source/invited_at/consent_basis/import_id/
// soft_bounce_streak/last_bounce_at/last_delivery_at,
// email_campaigns.slug/archive_status/archived_at, and the
// subscriber_imports/subscriber_events/outbound_queue tables outright — all
// owned by open Phase 8 issues #0123-0129) are correctly never flagged: none
// of them have a migration yet, so they never enter the live map this test
// walks.
//
// Column *names* are the assertion, not types or defaults. #0148's review
// prototyped a type/default comparison too during its independent sweep and
// found it false-positive-free against this tree, but shipping it isn't
// required to reproduce the one known drift (email_sends.claimed_at, a name
// difference) and it adds a second axis of "how similar is similar enough"
// that a name-only check doesn't need — see #0154's acceptance criteria.
func TestPRDWorkshopAndMailingColumnParity(t *testing.T) {
	live := liveColumnsFromMigrations(t)

	prdBytes, err := os.ReadFile(prdPath)
	if err != nil {
		t.Fatalf("read %s: %v", prdPath, err)
	}
	section := stripSQLComments(extractPRDSection(t, string(prdBytes), "### 6.2 "))
	prdColumns := prdColumnsFromSection(section)

	var tables []string
	for table := range live {
		if prdIndexParityTables[table] {
			tables = append(tables, table)
		}
	}
	sort.Strings(tables)

	for _, table := range tables {
		prdCols := prdColumns[table]

		var cols []string
		for col := range live[table] {
			cols = append(cols, col)
		}
		sort.Strings(cols)

		for _, col := range cols {
			def := live[table][col]
			if _, ok := prdCols[col]; !ok {
				t.Errorf(
					"PRD.md §6.2's CREATE TABLE %s does not mention column %q, added "+
						"by migrations/%s.up.sql — PRD.md's schema listing has fallen "+
						"behind the migration. Add the column to PRD.md §6.2's "+
						"CREATE TABLE %s — see #0154.",
					table, col, def.sourceFile, table,
				)
			}
		}
	}
}

// orNone renders an empty (unconditional) predicate legibly in a failure
// message instead of printing "WHERE " followed by nothing.
func orNone(s string) string {
	if s == "" {
		return "<none>"
	}
	return s
}
