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
//  2. Indexes only, not full table/column parity. A CREATE/DROP INDEX
//     statement is one self-contained, regexp-extractable unit with no
//     ambiguity about whether it is "shipped" — unlike a column, which
//     requires replaying a CREATE TABLE plus every later ALTER TABLE ADD
//     COLUMN across up to 20 files with no single-statement anchor, and
//     unlike a CHECK constraint, which §6.2 has never attempted to
//     transcribe (PRD.md never lists e.g. interests_slug_format or
//     subscribers_email_normalized — that level of detail lives in
//     docs/database.md's migration-by-migration prose instead). Indexes are
//     also the proven-recurring class here: #0135 and #0142 are two
//     separate drifts on the very same index. #0148's manual sweep also
//     found one column-level drift, email_sends.claimed_at (#0122's
//     OrphanSweep timestamp, added by migrations/000018 but never added to
//     PRD.md's email_sends table) — fixed by hand as part of that issue,
//     not mechanically guarded here. A second column-level drift would be
//     the signal to build that guard too; one instance, with a materially
//     harder extraction problem, was judged not worth it yet.
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
var (
	createIndexPattern = regexp.MustCompile(`(?i)CREATE\s+INDEX\s+(\w+)\s+ON\s+(\w+)\s*(\([^;]*?\))(?:\s+WHERE\s+([^;]*?))?;`)
	dropIndexPattern   = regexp.MustCompile(`(?i)DROP\s+INDEX\s+(?:IF\s+EXISTS\s+)?(\w+)\s*;`)
)

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
		text := string(content)

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
	section := extractPRDSection(t, string(prdBytes), "### 6.2 ")
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

// orNone renders an empty (unconditional) predicate legibly in a failure
// message instead of printing "WHERE " followed by nothing.
func orNone(s string) string {
	if s == "" {
		return "<none>"
	}
	return s
}
