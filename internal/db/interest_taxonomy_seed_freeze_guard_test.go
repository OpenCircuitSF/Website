package db

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// #0471: migrations/000009_create_interests.up.sql seeds the twelve PRD §6.1
// interests as DML inside a migration that is frozen the moment production
// applies it (CLAUDE.md §1 — re-derived at filing time via the read-only
// `ssh ec2` / `schema_migrations` query, production sat at version 22, well
// past 000009). So 000009 itself can never be edited again, by this project
// or a downstream (`#0125` is the precedent for what happens when a plan
// instructs exactly that edit).
//
// That freeze is fine for the CREATE TABLE — schema doesn't need to keep
// changing — but it would be a real defect for the DATA if the taxonomy had
// no other way to change. It does, on two channels, and this guard exists to
// keep both true going forward without needing 000009 to move at all:
//
//  1. Runtime changes to a LIVE catalog (rename, describe, reorder,
//     deactivate, or hard-delete an unused row) go through the admin CRUD API
//     `internal/interests` already exposes (#0024) — no migration involved,
//     ever. See docs/mailing-list.md's "Changing the taxonomy" note.
//  2. A change to what a FRESH install seeds by default (PRD §6.1's canonical
//     list itself changing) goes in a new, additively-numbered migration,
//     never a 000009 edit — exactly the same "ALTER TABLE in a new file, not
//     an edit to the CREATE TABLE that owns it" shape CLAUDE.md §1 already
//     requires for schema changes, applied here to content.
//
// This test enforces the two invariants any such future migration must
// satisfy so channel 2 can never silently corrupt a production database that
// already holds the twelve rows and that subscribers/workshops/campaigns
// already reference by id (migrations 000010, 000017, 000020):
//
//   - No migration numbered after 000009 may remove an interests row
//     (DELETE/TRUNCATE/DROP). Retiring an interest with history is
//     interests.Store.Deactivate (#0024) at runtime, which preserves the row
//     and its id; a migration that deletes the row instead would either
//     orphan subscriber_interests/workshop_interests/campaign_interests
//     history or cascade it away, and it would renumber nothing back the way
//     it was on a rollback.
//   - Any migration numbered after 000009 that INSERTs into interests must
//     use the same `ON CONFLICT (slug) DO NOTHING` shape 000009 itself
//     uses, so `up` stays idempotent against a database that already has the
//     row — re-running it must be a no-op, not a duplicate-key error and not
//     a second row for the same slug.
//
// interestTaxonomyGuardViolations is deliberately a pure function over an
// in-memory filename->content map rather than one that reads migrationsDir
// directly, so the mutation tests below can prove it actually catches both
// violations (and clears an idempotent one) without touching a single real
// file on disk — the same "prove, don't assert" shape CLAUDE.md §5's guard
// discussion asks for, applied here in Go rather than an external harness
// because the thing being mutated is the scan input, not the checker's own
// source (CLAUDE.md §5's floor/ceiling distinction: this is exactly the case
// where an in-package oracle is legitimate).
func interestTaxonomyGuardViolations(files map[string]string) []string {
	var violations []string

	deleteRe := regexp.MustCompile(`(?is)\b(DELETE\s+FROM|TRUNCATE(\s+TABLE)?|DROP\s+TABLE(\s+IF\s+EXISTS)?)\s+interests\b`)
	insertRe := regexp.MustCompile(`(?is)INSERT\s+INTO\s+interests\b`)
	onConflictRe := regexp.MustCompile(`(?is)ON\s+CONFLICT\s*\(\s*slug\s*\)\s*DO\s+NOTHING`)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		version, ok := migrationFileVersion(name)
		if !ok || version <= 9 {
			// 000009 itself, anything before it, and any name this guard
			// can't parse a version out of, are out of scope — this guard
			// only constrains migrations that come AFTER the frozen seed.
			continue
		}
		body := files[name]
		if deleteRe.MatchString(body) {
			violations = append(violations, fmt.Sprintf(
				"%s: removes rows from interests (DELETE/TRUNCATE/DROP) — retire an interest at "+
					"runtime via interests.Store.Deactivate (#0024), never in a migration; a migration "+
					"delete orphans or cascades away subscriber_interests/workshop_interests/"+
					"campaign_interests history", name))
		}
		if insertRe.MatchString(body) && !onConflictRe.MatchString(body) {
			violations = append(violations, fmt.Sprintf(
				"%s: INSERT INTO interests without ON CONFLICT (slug) DO NOTHING — not idempotent "+
					"against a database that already has the row, unlike migrations/000009's own seed", name))
		}
	}
	return violations
}

// migrationFileVersion extracts the leading 6-digit version from a migration
// filename such as "000012_scope_suppressions_by_reason.up.sql". It returns
// ok=false for anything that doesn't start with exactly 6 digits followed by
// an underscore, which is the same shape migrationVersionName in
// docs_parity_test.go matches for *.up.sql — this helper additionally accepts
// *.down.sql names since a future migration must obey the same rule in
// either direction.
func migrationFileVersion(name string) (int, bool) {
	base := name
	if i := strings.IndexByte(base, '_'); i >= 0 {
		base = base[:i]
	} else {
		return 0, false
	}
	if len(base) != 6 {
		return 0, false
	}
	version := 0
	for _, c := range base {
		if c < '0' || c > '9' {
			return 0, false
		}
		version = version*10 + int(c-'0')
	}
	return version, true
}

// TestInterestTaxonomyMigrationGuardPassesOnRealMigrations runs the guard
// against every real migrations/*.up.sql and *.down.sql file on disk. It
// currently passes vacuously — no migration after 000009 touches the
// interests table at all — and stays that way until a future migration adds
// or retires a default interest; at that point this is the test that catches
// a non-idempotent INSERT or a destructive DELETE before it ever reaches
// production.
func TestInterestTaxonomyMigrationGuardPassesOnRealMigrations(t *testing.T) {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("read %s: %v", migrationsDir, err)
	}

	files := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".up.sql") && !strings.HasSuffix(e.Name(), ".down.sql") {
			continue
		}
		body, err := os.ReadFile(migrationsDir + "/" + e.Name())
		if err != nil {
			t.Fatalf("read %s/%s: %v", migrationsDir, e.Name(), err)
		}
		files[e.Name()] = string(body)
	}
	if len(files) == 0 {
		t.Fatalf("found no migrations/*.sql files in %s — has the repo layout changed?", migrationsDir)
	}

	if violations := interestTaxonomyGuardViolations(files); len(violations) != 0 {
		t.Fatalf("interest-taxonomy migration guard violations:\n%s", strings.Join(violations, "\n"))
	}
}

// TestInterestTaxonomyMigrationGuardCatchesDestructiveDelete proves the guard
// actually fires for a later migration that deletes an interests row —
// exactly the "orphan an existing interest" failure #0471 criterion 4 forbids
// — rather than asserting the regex looks right and trusting it.
func TestInterestTaxonomyMigrationGuardCatchesDestructiveDelete(t *testing.T) {
	files := map[string]string{
		"000009_create_interests.up.sql": "INSERT INTO interests (slug, name, sort_order) VALUES " +
			"('beginner', 'Absolute Beginner Sessions', 120) ON CONFLICT (slug) DO NOTHING;",
		"000030_retire_beginner.up.sql": "DELETE FROM interests WHERE slug = 'beginner';",
	}
	violations := interestTaxonomyGuardViolations(files)
	if len(violations) == 0 {
		t.Fatal("expected a violation for a post-000009 migration deleting an interests row, got none")
	}
	if !strings.Contains(violations[0], "000030_retire_beginner.up.sql") {
		t.Fatalf("expected the violation to name the offending file, got: %v", violations)
	}
}

// TestInterestTaxonomyMigrationGuardCatchesNonIdempotentInsert proves the
// guard fires for a later migration that adds a default interest without the
// idempotent ON CONFLICT shape — the "re-running up must be a no-op" half of
// criterion 4.
func TestInterestTaxonomyMigrationGuardCatchesNonIdempotentInsert(t *testing.T) {
	files := map[string]string{
		"000031_add_ai_ml_interest.up.sql": "INSERT INTO interests (slug, name, sort_order) " +
			"VALUES ('ai-ml', 'AI & Machine Learning', 130);",
	}
	violations := interestTaxonomyGuardViolations(files)
	if len(violations) == 0 {
		t.Fatal("expected a violation for a post-000009 INSERT INTO interests missing ON CONFLICT, got none")
	}
}

// TestInterestTaxonomyMigrationGuardAllowsIdempotentInsert proves the guard
// does NOT fire on the pattern it is meant to permit — a future migration
// that adds a default interest the same idempotent way 000009 already does.
// Without this test, a guard that always fails would look identical to one
// that correctly discriminates.
func TestInterestTaxonomyMigrationGuardAllowsIdempotentInsert(t *testing.T) {
	files := map[string]string{
		"000032_add_ai_ml_interest.up.sql": "INSERT INTO interests (slug, name, sort_order) " +
			"VALUES ('ai-ml', 'AI & Machine Learning', 130) ON CONFLICT (slug) DO NOTHING;",
	}
	if violations := interestTaxonomyGuardViolations(files); len(violations) != 0 {
		t.Fatalf("expected no violations for an idempotent INSERT, got: %v", violations)
	}
}

// TestInterestTaxonomyMigrationGuardIgnores000009Itself proves the guard
// never inspects 000009 — the file this issue forbids editing — even though
// 000009's own seed statement has no reason to trip either rule (it already
// uses ON CONFLICT and never deletes). The version-gate (version <= 9) is
// the mechanism; this test pins that a file literally named
// "000009_create_interests.down.sql" containing a bare DROP TABLE (000009's
// real down migration) is correctly out of scope rather than flagged.
func TestInterestTaxonomyMigrationGuardIgnores000009Itself(t *testing.T) {
	files := map[string]string{
		"000009_create_interests.down.sql": "DROP TABLE interests;",
	}
	if violations := interestTaxonomyGuardViolations(files); len(violations) != 0 {
		t.Fatalf("expected 000009's own down migration to be out of scope, got: %v", violations)
	}
}
