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
// of vanishing. SQL comments, string literals, and dollar-quoted bodies are
// all blanked before any of this matching runs (prepareSQLText/tokenizeSQL,
// #0164), so a commented-out `-- CREATE INDEX …`, one written inside a
// string literal, or one inside a dollar-quoted DO block cannot register as
// live on either side.
//
// Placement: same package as TestDatabaseDocMigrationParity for the same
// reason that test gives (internal/db owns migrations), and to reuse its
// listMigrations/migrationsDir rather than duplicating the migration-file
// walk a second time.
//
// #0164: this file was widened three times (#0148 built it, #0154 added
// coverage checks and column parity, #0159 added multi-clause ADD and a
// dollar-quote lexer), and each round's reviewer found the next round's gap
// one layer down — because each new scanner (stripSQLComments, splitTopLevel,
// columnsFromCreateTable's close-paren finder) re-derived "am I inside a
// string / dollar-quote / comment right now" independently, so a gap fixed
// in one was still open in the others. tokenizeSQL below is the single scan
// that answers that question once; prepareSQLText derives the two blanked
// views (commentsBlanked, blanked) everything else in this file now reads
// instead of tracking quote state itself. This closed #0159's two residual
// gaps: a ';' inside a string literal used to truncate an ALTER TABLE
// statement (alterTableStmtPattern's terminator ran over unblanked text, so
// an embedded ';' looked real); and a CREATE TABLE/ADD COLUMN inside a
// dollar-quoted body used to enter the live map as a phantom (its text
// survived stripSQLComments unblanked, so the strict patterns still matched
// it). Both are now structurally impossible: blanked has no punctuation or
// keywords left inside any string/dollar/comment span for a pattern to
// match, so a statement can't be cut short by one and a phantom can't be
// found inside one — see tokenizeSQL and blankSpans for the mechanism.

// prdPath is a var, not a const, so a proof of the column-parity guard's
// failure mode can point it at a scratch copy of PRD.md instead of editing
// the project's actual specification in place (#0154's phase-3 review had to
// do the latter, copy the real file aside, and restore it byte-identically —
// see issues/0154.md's "Gotchas"/"What would clear the bounce").
var prdPath = "../../PRD.md"

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
)

// dollarQuoteOpenPattern matches a Postgres dollar-quote tag opener at the
// very start of the text it's run against: `$$` (the untagged form) or
// `$tag$`, where tag follows Postgres's own identifier rule (a letter or
// underscore, then any run of letters/digits/underscores) rather than a bare
// `\w*`, so a stray `$1` (a positional parameter, not a dollar-quote) is
// never mistaken for one.
var dollarQuoteOpenPattern = regexp.MustCompile(`^\$([A-Za-z_]\w*)?\$`)

// dollarQuoteEnd reports whether a dollar-quoted string starts at text[i]
// (the caller has already checked text[i] == '$') and, if so, the index one
// past its matching closing tag. Postgres dollar-quoting is not
// nesting-aware — the *first* later occurrence of the identical tag text
// closes it, full stop, even if a differently-tagged (or untagged) `$...$`
// pair appears textually inside — which is exactly what searching for the
// next literal occurrence of the opening tag gives for free, without having
// to recursively parse whatever is between the tags.
//
// #0159: this is what fixes dollar quoting's exposure to the quote-aware
// scanners below. Before this, a `'` inside a dollar-quoted body (an odd
// number of them, as in `$x$it's fine$x$`) was indistinguishable from a
// genuine `'...'` string literal opening, so a scanner that had never heard
// of dollar-quoting flipped into "inside a string" for the remainder of the
// file — silently eating every later `--` comment, comma, and paren. Jumping
// straight from the opening tag to its matching closer, without ever
// inspecting what's inside for quotes of its own, removes that exposure
// entirely: nothing inside a dollar-quoted span is treated as SQL structure,
// which is also exactly how Postgres itself treats it.
func dollarQuoteEnd(text string, i int) (end int, ok bool) {
	loc := dollarQuoteOpenPattern.FindStringIndex(text[i:])
	if loc == nil {
		return 0, false
	}
	tag := text[i : i+loc[1]] // e.g. "$$" or "$x$"
	closeIdx := strings.Index(text[i+len(tag):], tag)
	if closeIdx == -1 {
		return 0, false // unterminated — caller falls back to treating '$' as a literal byte
	}
	return i + len(tag) + closeIdx + len(tag), true
}

// sqlSpanKind classifies one contiguous run of SQL source text as
// tokenizeSQL walks it: either it's ordinary SQL text (kindText), or one of
// the four non-text constructs a real SQL scanner has to recognize before
// any character inside it can be trusted to mean what it looks like — a
// `--` line comment, a `/* */` block comment, a single-quoted string
// literal (Postgres escapes an embedded quote by doubling it: 'it”s'), or
// a Postgres dollar-quoted body ($$...$$ / $tag$...$tag$).
type sqlSpanKind int

const (
	kindText sqlSpanKind = iota
	kindLineComment
	kindBlockComment
	kindString
	kindDollar
)

// sqlSpan is one contiguous byte range of a tokenizeSQL scan, tagged with
// its kind. start/end are byte offsets into the exact string tokenizeSQL was
// given ([start, end), half-open).
type sqlSpan struct {
	kind       sqlSpanKind
	start, end int
}

// tokenizeSQL is #0164's single scanning pass: it partitions s, left to
// right, into sqlSpans that together cover the whole string with no gaps
// and no overlaps, alternating kindText with whichever of the four non-text
// constructs it recognizes. This replaces three separate hand-rolled
// quote/comment scanners #0154 and #0159 built one at a time — inside the
// old stripSQLComments, inside splitTopLevel, and inside
// columnsFromCreateTable's close-paren finder — each independently
// re-deriving "am I inside a string / dollar-quote / comment right now",
// which is exactly why a gap fixed in one kept turning up in the others a
// round later. Everything downstream now reads this one scan's answer
// instead (see prepareSQLText and blankSpans).
//
// Precedence and fallback behavior are carried over unchanged from the
// scanners this replaces: a '$' is checked for a dollar-quote opener before
// anything else (dollarQuoteEnd's own contract — an unmatched opener, e.g. a
// stray positional parameter like $1, falls back to being an ordinary
// byte); an unterminated string runs to end of s (nothing after it can be
// trusted to be outside the string); an unterminated block comment likewise
// runs to end of s ("nothing after it is SQL"); a line comment ends at the
// next newline or end of s, whichever comes first, and the newline itself
// is left as ordinary text.
//
// #0173 (declined — recorded here, not just in the issue, since this is
// where the next reader looking at string-quoting behavior will land): an
// unpaired ' reaching this function while it is in ordinary text state opens
// a kindString span that, having no closing ' to find, truncates to end of s
// exactly like an unterminated block comment does above — silently, since a
// span that runs to EOF still parses as "a string, just a long one" rather
// than as an error. #0159's classification table called the identical two
// inputs (an unterminated dollar-quote, and mismatched dollar-quote tags —
// both end in a stray ' once the tag machinery gives up and falls back to a
// literal byte) "right"; under this tokenizer they are "wrong-but-silent"
// instead, so those two rows now behave differently than #0159 recorded.
// That regression was examined, not overlooked, and left as-is:
//   - three of the four ways to trigger it (an unterminated dollar-quote, a
//     mismatched dollar-quote tag pair, an unterminated string) are SQL
//     Postgres itself rejects — a migration containing one fails
//     scripts/testdb.sh template / golang-migrate before this guard is ever
//     reached, and truncate-to-EOF for an unclosed construct is the same
//     policy #0154 already accepted as correct for an unterminated block
//     comment, now applied to strings for consistency rather than
//     inconsistently to just one construct;
//   - the only way to trigger it with SQL Postgres *does* accept is a
//     double-quoted identifier containing an apostrophe (e.g. "it's") or an
//     E'...' backslash-escape string, and this tokenizer has no state for
//     either — grep over migrations/*.up.sql and PRD.md's fenced SQL finds
//     neither form anywhere in this tree, and this project does not write
//     quoted identifiers.
//
// A kindQuotedIdent case (~10 lines: open on a bare `"`, close on the next
// unescaped `"`, `""` doubles like a string's `”` does) would close this for
// good and is worth adding the day either form shows up in a migration or in
// PRD.md — until then it is unreachable, so it stays undone rather than
// carrying untested code for an input that cannot occur.
func tokenizeSQL(s string) []sqlSpan {
	var spans []sqlSpan
	n := len(s)
	textStart := 0

	flushText := func(end int) {
		if end > textStart {
			spans = append(spans, sqlSpan{kind: kindText, start: textStart, end: end})
		}
	}

	for i := 0; i < n; {
		c := s[i]
		switch {
		case c == '\'':
			flushText(i)
			j := i + 1
			for j < n {
				if s[j] == '\'' {
					if j+1 < n && s[j+1] == '\'' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			spans = append(spans, sqlSpan{kind: kindString, start: i, end: j})
			i, textStart = j, j

		case c == '$':
			if end, ok := dollarQuoteEnd(s, i); ok {
				flushText(i)
				spans = append(spans, sqlSpan{kind: kindDollar, start: i, end: end})
				i, textStart = end, end
				continue
			}
			i++

		case c == '-' && i+1 < n && s[i+1] == '-':
			flushText(i)
			j := i
			for j < n && s[j] != '\n' {
				j++
			}
			spans = append(spans, sqlSpan{kind: kindLineComment, start: i, end: j})
			i, textStart = j, j

		case c == '/' && i+1 < n && s[i+1] == '*':
			flushText(i)
			j := i + 2
			// #0173 (declined): closes at the *first* "*/", so unlike
			// Postgres — whose block comments nest — a nested
			// "/* /* */ */" leaves everything after the inner "*/" live SQL
			// again. Harmless unless a phantom statement inside that tail
			// collides with a guarded table name, in which case it's a
			// loud false "PRD does not mention X", never a silent miss.
			// No block comment of any kind exists in migrations/ or in
			// PRD.md's §6.2 fenced SQL today, so there is nothing in this
			// tree for it to bite; nesting support is a few lines (track a
			// depth counter instead of stopping at the first close) worth
			// adding the day a nested block comment actually appears.
			for j+1 < n && !(s[j] == '*' && s[j+1] == '/') {
				j++
			}
			end := n
			if j+1 < n {
				end = j + 2
			}
			spans = append(spans, sqlSpan{kind: kindBlockComment, start: i, end: end})
			i, textStart = end, end

		default:
			i++
		}
	}
	flushText(n)
	return spans
}

// blankSpans returns s with every span in spans whose kind appears in kinds
// replaced by ASCII spaces, byte for byte — so the result is exactly
// len(s), and any byte offset into it names the same span of s (and of
// whatever s itself was blanked from) it always did. A newline inside a
// blanked span is left as '\n' rather than turned to a space, purely so a
// multi-line comment or dollar-quoted body still blanks to the same number
// of lines it occupied — cosmetic, nothing here depends on it.
//
// This is the operation that closes both of #0159's residual gaps, by
// construction rather than by a new special case for either: called with
// kindString and kindDollar included, a ';' that was inside a string
// literal or dollar-quoted body is now a space in the text every statement
// pattern in this file actually matches against, so it can no longer
// terminate an ALTER TABLE statement early (the semicolon-in-a-DEFAULT-
// literal gap); and a CREATE TABLE or ALTER TABLE keyword sequence that was
// inside one of those spans is now spaces too, so it can never match
// createTableStart/alterTableStmtPattern and enter the live map as a
// phantom (the dollar-quoted-body gap).
func blankSpans(s string, spans []sqlSpan, kinds ...sqlSpanKind) string {
	blank := make(map[sqlSpanKind]bool, len(kinds))
	for _, k := range kinds {
		blank[k] = true
	}
	b := []byte(s)
	for _, sp := range spans {
		if !blank[sp.kind] {
			continue
		}
		for i := sp.start; i < sp.end; i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	return string(b)
}

// prepareSQLText tokenizes raw exactly once (tokenizeSQL) and derives the
// two views the rest of this file needs, both exactly len(raw) bytes so a
// byte offset means the same thing in either one (and in raw itself):
//
//   - commentsBlanked has only comments blanked. Quoted literal content
//     (e.g. an index predicate's 'draft') survives, so a capture group that
//     needs real text — an index's WHERE predicate or column list — can
//     recover it from here instead of from the fully-blanked view, without
//     risking comment text leaking into it either.
//   - blanked additionally blanks string literals and dollar-quoted bodies.
//     This is the text every pattern and coverage check in this file
//     matches against; see blankSpans for why that closes #0159's two
//     residual gaps.
func prepareSQLText(raw string) (blanked, commentsBlanked string) {
	spans := tokenizeSQL(raw)
	commentsBlanked = blankSpans(raw, spans, kindLineComment, kindBlockComment)
	blanked = blankSpans(commentsBlanked, spans, kindString, kindDollar)
	return blanked, commentsBlanked
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
		text, orig := prepareSQLText(string(content))
		checkIndexStatementCoverage(t, text, "migrations/"+m.stem+".up.sql")

		var ops []op
		for _, loc := range dropIndexPattern.FindAllStringSubmatchIndex(text, -1) {
			ops = append(ops, op{pos: loc[0], drop: true, name: text[loc[2]:loc[3]]})
		}
		for _, loc := range createIndexPattern.FindAllStringSubmatchIndex(text, -1) {
			name := text[loc[2]:loc[3]]
			// Match positions come from the fully-blanked text (safe from a
			// stray ';'/'('/')' inside a literal ever mis-shaping the match),
			// but columns/predicate are recovered from orig (comments-only
			// blanked) so real literal content — e.g. idx_workshops_visible's
			// WHERE status <> 'draft' — survives for the parity comparison
			// below instead of coming back as blanked spaces (#0164).
			def := indexDef{
				table:      text[loc[4]:loc[5]],
				columns:    orig[loc[6]:loc[7]],
				sourceFile: m.stem,
			}
			if loc[8] != -1 {
				def.predicate = orig[loc[8]:loc[9]]
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
// of PRD.md text (§6.2's section, already extracted by extractPRDSection and
// run through prepareSQLText by the caller — section is the blanked view,
// origSection the comments-only-blanked view columns/predicate are
// recovered from, for the identical reason liveIndexesFromMigrations does).
func prdIndexesFromSection(section, origSection string) map[string]indexDef {
	defs := map[string]indexDef{}
	for _, loc := range createIndexPattern.FindAllStringSubmatchIndex(section, -1) {
		name := section[loc[2]:loc[3]]
		def := indexDef{
			table:   section[loc[4]:loc[5]],
			columns: origSection[loc[6]:loc[7]],
		}
		if loc[8] != -1 {
			def.predicate = origSection[loc[8]:loc[9]]
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
// body's top-level comma-separated segment (or an ALTER TABLE ADD's target)
// as a table-level constraint (PRIMARY KEY (...), UNIQUE (...), CHECK (...),
// FOREIGN KEY (...), CONSTRAINT name ...) rather than a column definition.
// Every one of these is reserved in Postgres, so a bare column can never
// legally be named one of them and checking a segment's first token is
// sufficient without a real SQL parser — the same "narrow, not a parser"
// tradeoff createIndexPattern already makes.
//
// EXCLUDE is deliberately not in this map (#0154, phase-3 review, attack 11):
// unlike the other five, EXCLUDE is *non-reserved* in Postgres, so
// `exclude BOOLEAN` is a legal bare column name — a table naming one would
// have had it silently dropped by a blanket keyword match. isTableConstraintSegment
// below only treats EXCLUDE as a constraint when it is immediately followed
// by USING, which is EXCLUDE's only valid constraint syntax
// (`EXCLUDE USING <method> (...)`).
var tableConstraintKeywords = map[string]bool{
	"PRIMARY":    true,
	"UNIQUE":     true,
	"CHECK":      true,
	"FOREIGN":    true,
	"CONSTRAINT": true,
}

// isTableConstraintSegment reports whether a CREATE TABLE body segment or an
// ALTER TABLE ADD's target — given its first token and, if present, its
// second — is a table-level constraint rather than a column (or column-add).
// Shared by columnsFromCreateTable (table-body segments) and
// liveColumnsFromMigrations/the column-add coverage check (ALTER TABLE ADD's
// captured tokens), so the two paths can never disagree about what counts as
// a constraint.
func isTableConstraintSegment(first, second string) bool {
	first = strings.ToUpper(first)
	if first == "EXCLUDE" {
		return strings.ToUpper(second) == "USING"
	}
	return tableConstraintKeywords[first]
}

// createTableStart matches "CREATE TABLE [IF NOT EXISTS] name (" and its
// match end lands exactly one byte past the opening paren, which is what
// lets callers derive the paren's own index as loc[1]-1.
//
// #0154's phase-3 review (attack 15) found the original pattern — no IF NOT
// EXISTS clause — simply didn't match `CREATE TABLE IF NOT EXISTS workshops
// (`, and a non-match here means the *entire table*, all of its columns,
// silently never enters the live map: the same silent-skip shape the index
// guard was widened to close, reintroduced on the table side. checkTableStatementCoverage
// below is what catches any remaining form this prefix still can't parse
// (e.g. a quoted table name), instead of letting it vanish the same way.
var createTableStart = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s*\(`)

// looseTableStartPattern is createTableStart's coverage ceiling (#0154,
// mirroring looseIndexStartPattern/checkIndexStatementCoverage): every
// "CREATE TABLE" occurrence, however it's spelled, so checkTableStatementCoverage
// can catch a table createTableStart's own prefix still can't parse — a
// quoted name, or a future form nobody has written yet.
var looseTableStartPattern = regexp.MustCompile(`(?i)CREATE\s+TABLE\b`)

// alterTableStmtPattern matches one whole ALTER TABLE statement — table name
// through the terminating ";" — rather than a single ADD clause. #0159 found
// the previous single-clause pattern (matching "ALTER TABLE t ADD ..." once
// per statement) was blind to two things: a second or later comma-joined
// clause in a multi-clause ALTER (`ALTER TABLE t ADD COLUMN a …, ADD COLUMN
// b …;` — this project already writes multi-clause ALTERs, e.g.
// migrations/000007's `ALTER TABLE sessions ALTER COLUMN …, ALTER COLUMN …,
// …`), and the `ALTER TABLE ONLY t` / `ALTER TABLE "t"` forms, whose table
// name never matched the old pattern's bare `\w+` slot at all. Capturing the
// whole clause list as one blob (group 2) and splitting it into individual
// clauses afterward (splitTopLevel, reused from the CREATE TABLE column-list
// case) is what lets every clause — not just the first — be classified on
// its own.
//
// The table name accepts an optional ONLY and optional double-quoting
// (unwrapped, not captured as part of the name), matching Postgres's actual
// grammar; a schema-qualified name (`public.t`) is deliberately not handled
// here, the same policy createTableStart uses for CREATE TABLE — it becomes
// wrong-but-loud via checkAlterTableStatementCoverage below, not silent.
var alterTableStmtPattern = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+(?:ONLY\s+)?"?(\w+)"?\s+([^;]*);`)

// looseAlterTableStmtPattern is alterTableStmtPattern's coverage ceiling —
// bare "ALTER TABLE " (note the trailing \s+, not just \b: PRD.md §6.2 has
// several prose mentions of "`ALTER TABLE`" in backtick-quoted text with no
// following whitespace at all, e.g. "the FK added in a later `ALTER
// TABLE`." — requiring real whitespace after "TABLE" is what keeps those
// out of the count on both sides without needing an allowlist) — so
// checkAlterTableStatementCoverage can catch a form alterTableStmtPattern
// still can't parse (a schema-qualified name, a missing ";") instead of
// letting it, and every column it adds, vanish silently — the same failure
// shape looseTableStartPattern/checkTableStatementCoverage already closed on
// the CREATE TABLE side.
var looseAlterTableStmtPattern = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+`)

// checkAlterTableStatementCoverage is the statement-level half of the
// ALTER-TABLE-ADD coverage check (#0159): it catches an ALTER TABLE
// statement alterTableStmtPattern can't parse at all — before any of its
// clauses are even examined. checkColumnAddCoverage below is the
// clause-level half, for a statement that *did* parse but contains an ADD
// clause it can't classify.
//
// found can never come out less than matched: every alterTableStmtPattern
// match begins with literal "ALTER TABLE " text, which looseAlterTableStmtPattern
// also matches at the identical starting position, so a strict match always
// has a corresponding loose match backing it.
func checkAlterTableStatementCoverage(t *testing.T, text, label string) {
	t.Helper()
	found := len(looseAlterTableStmtPattern.FindAllStringIndex(text, -1))
	matched := len(alterTableStmtPattern.FindAllStringIndex(text, -1))
	if found != matched {
		t.Errorf(
			"%s: found %d \"ALTER TABLE\" occurrence(s) but only parsed %d as a "+
				"complete statement — one of them uses a SQL form alterTableStmtPattern "+
				"can't parse (a schema-qualified name, a missing terminating ';', or a "+
				"form not yet handled). A silent skip here means every column that "+
				"statement adds is invisible to this guard — fix the statement or widen "+
				"alterTableStmtPattern, don't leave the mismatch. See #0159.",
			label, found, matched,
		)
	}
}

// clauseAddPattern matches one ALTER TABLE clause — already isolated from
// its statement's clause list by splitTopLevel, so it never has to account
// for a *later* clause's own commas — that adds a single column: an
// optional COLUMN keyword, optional IF NOT EXISTS, the column's identifier,
// and (optionally) its type's first token. Anchored at the start (^) because
// it is matched against one already-split clause, never scanned across a
// whole file. Group 1 is a capturing "COLUMN " literal (empty/unmatched when
// the keyword is absent) rather than non-capturing, so classifyColumnAdd can
// tell "the COLUMN keyword said so" apart from "no keyword — check whether
// what follows ADD is actually a constraint, not a column name" without a
// second pattern (#0154, attacks 17/18 — carried over unchanged by #0159's
// statement/clause split).
var clauseAddPattern = regexp.MustCompile(`(?i)^\s*ADD\s+(COLUMN\s+)?(?:IF\s+NOT\s+EXISTS\s+)?(\w+)(?:\s+(\w+))?`)

// looseClauseAddPattern is clauseAddPattern's coverage ceiling within one
// already-split clause: does this clause even claim to be an ADD at all,
// regardless of whether clauseAddPattern can parse what follows. Unlike
// clauseAddPattern it requires nothing after "ADD" — no token, not even a
// trailing word — so a clause so malformed that no identifier follows it at
// all still counts on the loose side, producing a genuine, catchable
// mismatch instead of the two patterns being structurally unable to
// disagree (#0154's own retrospective on its first, too-close-to-strict
// draft).
var looseClauseAddPattern = regexp.MustCompile(`(?i)^\s*ADD\b`)

// classifyColumnAdd interprets one ADD clause's tokens — the "COLUMN "
// literal if clauseAddPattern matched it, and the identifier/next-word that
// follow — and reports the column name being added, or ok=false if this is
// actually a table-level constraint addition (ADD CONSTRAINT …, ADD PRIMARY
// KEY (...), …) rather than a column. An explicit COLUMN keyword is
// unambiguous on its own; its absence means identifier/nextWord must be
// checked against isTableConstraintSegment to tell "ADD c TEXT" (a column)
// apart from "ADD CONSTRAINT c CHECK (...)" (not one).
func classifyColumnAdd(columnKeyword, identifier, nextWord string) (name string, ok bool) {
	if columnKeyword != "" {
		return identifier, true
	}
	if isTableConstraintSegment(identifier, nextWord) {
		return "", false
	}
	return identifier, true
}

// matchGroup returns submatch group i (as produced by
// FindAllStringSubmatchIndex, so loc[i]/loc[i+1]) of s, or "" if the group
// didn't participate in the match.
func matchGroup(s string, loc []int, i int) string {
	if loc[i] == -1 {
		return ""
	}
	return s[loc[i]:loc[i+1]]
}

// peekTwoWords returns the first two whitespace-separated tokens of s (each
// "" if absent), used by checkColumnAddCoverage to classify what follows a
// looseClauseAddPattern match without requiring the loose pattern itself to
// capture anything — the same isTableConstraintSegment test clauseAddPattern's
// own tokens go through, applied to whatever text (if any) actually follows
// "ADD".
func peekTwoWords(s string) (first, second string) {
	fields := strings.Fields(s)
	if len(fields) > 0 {
		first = fields[0]
	}
	if len(fields) > 1 {
		second = fields[1]
	}
	return first, second
}

// columnAddsInClauses walks one ALTER TABLE statement's already-split
// clauses (splitTopLevel(blob), where blob is alterTableStmtPattern's group
// 2) and returns the column name added by each clause that genuinely adds a
// column — never a clause that isn't an ADD at all (ALTER COLUMN, DROP
// COLUMN, …) and never a table-level constraint add (ADD CONSTRAINT, ADD
// PRIMARY KEY, ADD UNIQUE, ADD FOREIGN KEY, ADD EXCLUDE USING). Shared by
// liveColumnsFromMigrations (extraction) and, via the same
// clauseAddPattern/classifyColumnAdd, checkColumnAddCoverage (counting), so
// the two can never disagree about what counts as a column.
func columnAddsInClauses(clauses []string) []string {
	var cols []string
	for _, clause := range clauses {
		loc := clauseAddPattern.FindStringSubmatchIndex(clause)
		if loc == nil {
			continue
		}
		if col, ok := classifyColumnAdd(matchGroup(clause, loc, 2), matchGroup(clause, loc, 4), matchGroup(clause, loc, 6)); ok {
			cols = append(cols, col)
		}
	}
	return cols
}

// checkTableStatementCoverage, checkAlterTableStatementCoverage (above), and
// checkColumnAddCoverage (below) are checkIndexStatementCoverage's
// column-side counterparts (#0154, phase-3 review — "a coverage check for
// the column half... is what makes attacks 15-18 loud instead of silent";
// #0159 split the ADD half into a statement tier and a clause tier so a
// multi-clause ALTER's second and later clauses are no longer invisible to
// both). Each counts a loose, maximally permissive pattern's occurrences
// against how many the corresponding strict pattern actually parsed into a
// table/column, and t.Errorf's on a mismatch naming the file. That is what
// turns any CREATE TABLE or ALTER TABLE ADD form none of these patterns
// anticipate into a loud test failure instead of a silent absence from the
// live map — exactly the property checkIndexStatementCoverage already gives
// the index half.
func checkTableStatementCoverage(t *testing.T, text, label string) {
	t.Helper()
	found := len(looseTableStartPattern.FindAllStringIndex(text, -1))
	matched := len(createTableStart.FindAllStringIndex(text, -1))
	if found != matched {
		t.Errorf(
			"%s: found %d \"CREATE TABLE\" occurrence(s) but only parsed %d as a "+
				"complete statement — one of them uses a SQL form createTableStart "+
				"can't parse (check for a quoted table name, or a form not yet "+
				"handled). A silent skip here means the whole table — every column "+
				"it defines — is invisible to this guard — fix the statement or "+
				"widen createTableStart, don't leave the mismatch. See #0154.",
			label, found, matched,
		)
	}
}

// checkColumnAddCoverage is the clause-level half of the ALTER-TABLE-ADD
// coverage check (#0159). It walks every ALTER TABLE statement
// checkAlterTableStatementCoverage above already confirmed alterTableStmtPattern
// can parse, splits its clause list on top-level commas, and — clause by
// clause — counts a loose "does this even claim to be an ADD (and isn't a
// constraint add)?" test against how many clauseAddPattern actually parsed
// into a column. A mismatch here means one comma-separated clause in a
// (possibly multi-clause) ALTER TABLE statement uses a form clauseAddPattern
// can't parse — including a second or later ADD clause that the pre-#0159
// single-shot-per-statement pattern could never even see.
//
// found can never come out less than matched, for the same structural
// reason checkColumnStatementCoverage's other checks can't: every clause
// clauseAddPattern successfully parses necessarily starts with "ADD" (its
// own pattern requires that literal prefix), so looseClauseAddPattern always
// matches there too; and both sides classify "is this actually a constraint
// add, not a column" through the identical isTableConstraintSegment call —
// the loose side on the clause's first token(s) after "ADD", the strict side
// on classifyColumnAdd's identifier/nextWord, which is the same token
// whenever no COLUMN keyword is present, and never a constraint at all
// whenever one is.
func checkColumnAddCoverage(t *testing.T, text, label string) {
	t.Helper()
	found := 0
	matched := 0
	for _, sloc := range alterTableStmtPattern.FindAllStringSubmatchIndex(text, -1) {
		blob := matchGroup(text, sloc, 4)
		for _, clause := range splitTopLevel(blob) {
			rest := looseClauseAddPattern.FindString(clause)
			if rest == "" {
				continue // not an ADD clause at all — ALTER COLUMN, DROP COLUMN, ...
			}
			first, second := peekTwoWords(clause[len(rest):])
			if isTableConstraintSegment(first, second) {
				continue // ADD CONSTRAINT / ADD PRIMARY KEY / ... — classified out identically on both sides
			}
			found++
			if aloc := clauseAddPattern.FindStringSubmatchIndex(clause); aloc != nil {
				if _, ok := classifyColumnAdd(matchGroup(clause, aloc, 2), matchGroup(clause, aloc, 4), matchGroup(clause, aloc, 6)); ok {
					matched++
				}
			}
		}
	}
	if found != matched {
		t.Errorf(
			"%s: found %d \"ALTER TABLE ... ADD\" column clause(s) (excluding "+
				"constraint adds, counted per comma-separated clause across every "+
				"ALTER TABLE statement) but only parsed %d as a complete ADD COLUMN "+
				"clause — one of them uses a SQL form clauseAddPattern can't parse. A "+
				"silent skip here means the column is invisible to this guard — fix "+
				"the statement or widen clauseAddPattern, don't leave the mismatch. "+
				"See #0159.",
			label, found, matched,
		)
	}
}

// checkColumnStatementCoverage runs every column-side coverage check
// (checkTableStatementCoverage, checkAlterTableStatementCoverage, and
// checkColumnAddCoverage) against one piece of text, mirroring
// checkIndexStatementCoverage's single call site per migration file / PRD
// section.
func checkColumnStatementCoverage(t *testing.T, text, label string) {
	t.Helper()
	checkTableStatementCoverage(t, text, label)
	checkAlterTableStatementCoverage(t, text, label)
	checkColumnAddCoverage(t, text, label)
}

// splitTopLevel splits s on commas that are not nested inside parens, so a
// column definition like "import_id BIGINT REFERENCES
// subscriber_imports(id)" is never mistaken for two segments by the comma
// that doesn't exist there, and — more importantly — so a table constraint
// like "PRIMARY KEY (subscriber_id, interest_id)" is treated as the single
// segment it is rather than split apart by its own internal comma.
//
// #0164: no longer quote-aware itself. #0154 and #0159 each gave it its own
// string/dollar-quote tracking (a comma or paren inside a literal, e.g.
// `DEFAULT 'x,y'`, used to be indistinguishable from real structure) — now
// unnecessary, because every caller passes s already run through
// prepareSQLText's blanked view, where a string literal or dollar-quoted
// body has had its content replaced with spaces upstream (tokenizeSQL,
// blankSpans). There is no comma or paren left inside one for this function
// to mis-split on, so a plain paren-depth counter is sufficient.
func splitTopLevel(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
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
// commas (splitTopLevel) and takes each segment's first token as the column
// name, skipping any segment that isTableConstraintSegment identifies as a
// table-level constraint rather than a column.
//
// #0164: the close-paren finder is no longer quote-aware itself, for the
// identical reason splitTopLevel isn't anymore — text is always
// prepareSQLText's blanked view by the time it reaches here, so a `)` or `(`
// that used to hide inside a string literal or dollar-quoted body (#0154
// attack 13; #0159's dollar-quote lexer) has already been blanked to a
// space upstream and can no longer close the body early or open a phantom
// nesting level.
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
		second := ""
		if len(fields) > 1 {
			second = fields[1]
		}
		if isTableConstraintSegment(fields[0], second) {
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
		text, _ := prepareSQLText(string(content)) // column names are never quoted content; the blanked view alone is enough
		checkColumnStatementCoverage(t, text, "migrations/"+m.stem+".up.sql")

		var ops []op
		for _, loc := range createTableStart.FindAllStringSubmatchIndex(text, -1) {
			table := text[loc[2]:loc[3]]
			openParen := loc[1] - 1
			cols, _ := columnsFromCreateTable(text, openParen)
			ops = append(ops, op{pos: loc[0], table: table, columns: cols})
		}
		for _, loc := range alterTableStmtPattern.FindAllStringSubmatchIndex(text, -1) {
			table := matchGroup(text, loc, 2)
			blob := matchGroup(text, loc, 4)
			cols := columnAddsInClauses(splitTopLevel(blob))
			if len(cols) > 0 {
				ops = append(ops, op{pos: loc[0], table: table, columns: cols})
			}
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
// extractPRDSection and run through prepareSQLText's blanked view by the
// caller).
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

// sqlFencePattern matches a markdown fenced code block explicitly labeled
// ```sql, capturing its body. #6.2's schema listing is written as one such
// block; nothing outside a fence in that section is SQL, no matter how much
// it may look like it (a backtick-quoted identifier in prose, a semicolon
// ending a sentence, an apostrophe in a contraction).
var sqlFencePattern = regexp.MustCompile("(?s)```sql\r?\n(.*?)\r?\n```")

// extractFencedSQL narrows a PRD.md section down to only the SQL inside its
// ```sql fenced code block(s), before any of it reaches tokenizeSQL.
//
// #0173: every prior pass over this guard fed extractPRDSection's whole
// output — markdown prose and all — straight into prepareSQLText, which
// tokenizes it as if every byte were SQL. That is usually harmless (prose
// rarely contains SQL punctuation in a way that confuses the tokenizer) but
// not reliably so: §6.2 has one unpaired apostrophe in an ordinary sentence
// ("...CLAUDE.md §1's append-only rule..." — the contraction pairs with none
// of the section's SQL string literals), which opens a runaway kindString
// span that runs to the next apostrophe found anywhere later in the section,
// blanking 867 of §6.2's 18,660 bytes including part of unrelated prose nowhere
// near a schema statement. Nothing is lost today — the blanked span happens to
// land between statements — but that is luck, not design: a schema line
// landing inside a future blanked span would fail with "PRD does not mention
// X", and the real cause (an apostrophe in an unrelated sentence, possibly in
// a completely different paragraph) would not be obvious from that message.
//
// The fix removes the whole class rather than this one instance: prose can
// contain arbitrarily many unpaired quotes, comment markers, or semicolons,
// and none of it is ever fed to the tokenizer, because none of it is SQL.
// Multiple fenced blocks are joined with a blank-line separator so a
// tokenizeSQL run can never read the tail of one fence and the head of the
// next as one continuous statement (relevant if a second ```sql block is ever
// added to this section; today there is exactly one).
func extractFencedSQL(t *testing.T, section string) string {
	t.Helper()
	matches := sqlFencePattern.FindAllStringSubmatch(section, -1)
	if len(matches) == 0 {
		t.Fatalf("PRD.md: no ```sql fenced code block found in this section — has §6.2's schema listing moved out of a fenced block, or lost its \"sql\" language tag? See #0173.")
	}
	var b strings.Builder
	for _, m := range matches {
		b.WriteString(m[1])
		b.WriteString("\n\n")
	}
	return b.String()
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
	sqlOnly := extractFencedSQL(t, extractPRDSection(t, string(prdBytes), "### 6.2 "))
	section, origSection := prepareSQLText(sqlOnly)
	checkIndexStatementCoverage(t, section, "PRD.md §6.2")
	prdIndexes := prdIndexesFromSection(section, origSection)

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
	sqlOnly := extractFencedSQL(t, extractPRDSection(t, string(prdBytes), "### 6.2 "))
	section, _ := prepareSQLText(sqlOnly) // column names only — blanked view is enough
	checkColumnStatementCoverage(t, section, "PRD.md §6.2")
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
