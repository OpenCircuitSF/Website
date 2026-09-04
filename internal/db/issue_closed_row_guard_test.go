package db

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// #0420: `issues/Issues.md`'s own close-out procedure says a status move to
// `resolved` *or* `closed` adds a `**Closed**` row recording the date — the
// date the issue was judged done, not the date bytes happened to land in
// git. Measured directly against the live corpus (not trusted from any
// prior report — see this issue's criterion 1): 380 `issues/*.md` files
// carry `**Status** | resolved`, and 24 of those have no `**Closed**` row
// anywhere in the file. `#0412` needed one of those 24 dates (`#0293`'s) for
// a single sentence and had to reconstruct it from `git log` on the
// resolution commit, corroborated against a same-day review commit, before
// it could be trusted — exactly the inference this convention exists to
// make unnecessary.
//
// # Order chosen: guard first, backfill second
//
// `#0420` flags a real sequencing choice: backfilling the 24 first means the
// guard is written against an already-clean corpus and its only proof of
// discrimination is a synthetic mutation. This implementation wrote the
// guard first instead, ran it red against the real, unmodified 24-file
// corpus (a live specimen, not a fixture), and only then backfilled — so
// the guard's discriminating power was proved twice: once against real
// history, once against the planted-fixture control below. The backfill
// commit that followed is cleanup, exactly as this issue's "the guard
// matters more than the backfill" framing asks for.
//
// # Scope: resolved and closed, not wontfix
//
// `issues/Issues.md` states the requirement for exactly two statuses:
// "When status moves to `resolved` or `closed`, add a `**Closed**` row with
// the date." It never says this for `wontfix`. Measured against the live
// corpus: all 7 `closed`-status issues already carry a `**Closed**` row, so
// widening this guard's scope from `resolved` alone (as #0420's title
// states it) to `{resolved, closed}` (matching Issues.md's own words)
// changes nothing about today's pass/fail outcome — it only closes the gap
// that would open the next time an issue is marked `closed` directly rather
// than through `resolved`. `wontfix` is deliberately left unguarded: 2 of
// the 3 live `wontfix` issues (`#0096`, `#0099` — both named in CLAUDE.md §5
// as legitimate `wontfix` closures) have no `**Closed**` row, and
// Issues.md's text never asks for one there. Enforcing it here would invent
// a requirement the project's own procedure does not state and immediately
// fail two issues nobody asked this guard to touch — worth a separate
// decision (and possibly a separate issue updating Issues.md's own text)
// rather than being smuggled into this guard's scope. `open` and
// `in-progress` are excluded for the obvious reason: neither is a terminal
// state, so neither has a resolution date to record yet.
//
// # Why an in-package go test is a sound oracle here
//
// This guard reads issues/*.md from disk — external data, not the bytes of
// this test file itself — so it is the "mutate the scan roots and got
// itself changes" shape CLAUDE.md §8 describes as a legitimate, non-circular
// oracle for an in-package test, in the shape of docs_parity_test.go and
// prd_section_index_test.go rather than the scripts/*_guard_test.sh
// external-harness shape those same notes reserve for a checker that could
// be edited to agree with itself.

// issueClosedRowPattern matches an issue file's `**Closed**` metadata row.
var issueClosedRowPattern = regexp.MustCompile(`(?m)^\|\s*\*\*Closed\*\*\s*\|`)

// issueRequiresClosedRow reports whether status is one Issues.md's own
// procedure requires to carry a **Closed** row: resolved or closed.
// wontfix, open, and in-progress do not — see the design note above.
func issueRequiresClosedRow(status string) bool {
	return status == "resolved" || status == "closed"
}

// scanIssueDirForMissingClosedRow walks dir's issues/NNNN.md files and
// returns the relative path of every file whose **Status** satisfies
// issueRequiresClosedRow but carries no **Closed** metadata row, sorted for
// a stable, pasteable report.
func scanIssueDirForMissingClosedRow(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var failures []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("read %s: %v", path, rerr)
		}
		text := string(raw)
		if !issueRequiresClosedRow(issueStatus(text)) {
			continue
		}
		if !issueClosedRowPattern.MatchString(text) {
			failures = append(failures, path)
		}
	}
	sort.Strings(failures)
	return failures
}

// TestResolvedOrClosedIssueCarriesClosedRow is the guard: every issues/*.md
// file whose Status is resolved or closed must carry a **Closed** row. See
// the file-level comment for why those two statuses and not others.
func TestResolvedOrClosedIssueCarriesClosedRow(t *testing.T) {
	failures := scanIssueDirForMissingClosedRow(t, "../../issues")
	if len(failures) > 0 {
		t.Fatalf("issue(s) resolved or closed with no **Closed** row — add one recording the actual resolution date, derived from git and stated, not guessed (see #0420):\n%s",
			strings.Join(failures, "\n"))
	}
}

// TestIssueClosedRowGuardCatchesPlantedMissingRowAndSparesRowPresent proves
// the guard discriminates (#0420 criterion 3): a resolved fixture with no
// Closed row fails, naming the file; the identical fixture with a Closed
// row added passes — the passing control. Both fixtures live in
// t.TempDir(), never the tracked issues/ tree.
func TestIssueClosedRowGuardCatchesPlantedMissingRowAndSparesRowPresent(t *testing.T) {
	dir := t.TempDir()
	missing := "# 9997 — scratch fixture for #0420's mutation proof\n\n" +
		"| | |\n|---|---|\n| **Status** | resolved |\n| **Commit** | `deadbee` |\n\n" +
		"## Description\n\nFixture only.\n"
	fixturePath := filepath.Join(dir, "9997.md")
	if err := os.WriteFile(fixturePath, []byte(missing), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	failures := scanIssueDirForMissingClosedRow(t, dir)
	if len(failures) != 1 {
		t.Fatalf("expected exactly one failure for the planted fixture, got %d: %v", len(failures), failures)
	}
	if !strings.Contains(failures[0], "9997.md") {
		t.Fatalf("expected the failure to name 9997.md, got %q", failures[0])
	}

	withClosedRow := "# 9997 — scratch fixture for #0420's mutation proof\n\n" +
		"| | |\n|---|---|\n| **Status** | resolved |\n| **Closed** | 2026-09-03 |\n| **Commit** | `deadbee` |\n\n" +
		"## Description\n\nFixture only.\n"
	if err := os.WriteFile(fixturePath, []byte(withClosedRow), 0o600); err != nil {
		t.Fatalf("rewrite fixture: %v", err)
	}
	failures = scanIssueDirForMissingClosedRow(t, dir)
	if len(failures) != 0 {
		t.Fatalf("expected clean after adding the Closed row, got %v", failures)
	}
}

// TestIssueClosedRowGuardExemptsWontfixOpenAndInProgress pins the scope
// decision above: fixtures with no Closed row in wontfix, open, and
// in-progress states must not be reported.
func TestIssueClosedRowGuardExemptsWontfixOpenAndInProgress(t *testing.T) {
	dir := t.TempDir()
	statuses := []string{"wontfix", "open", "in-progress"}
	for i, status := range statuses {
		name := "999" + string(rune('0'+i)) + ".md"
		text := "# scratch — #0420 exemption fixture\n\n" +
			"| | |\n|---|---|\n| **Status** | " + status + " |\n\n" +
			"## Description\n\nFixture only.\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	failures := scanIssueDirForMissingClosedRow(t, dir)
	if len(failures) != 0 {
		t.Fatalf("expected wontfix/open/in-progress fixtures to be exempt, got %v", failures)
	}
}

// TestIssueClosedRowGuardHonorsClosedStatusToo pins the wider scope
// decision: a closed-status fixture with no Closed row must be reported,
// not just a resolved one.
func TestIssueClosedRowGuardHonorsClosedStatusToo(t *testing.T) {
	dir := t.TempDir()
	text := "# 9998 — scratch fixture, closed status\n\n" +
		"| | |\n|---|---|\n| **Status** | closed |\n\n" +
		"## Description\n\nFixture only.\n"
	if err := os.WriteFile(filepath.Join(dir, "9998.md"), []byte(text), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	failures := scanIssueDirForMissingClosedRow(t, dir)
	if len(failures) != 1 || !strings.Contains(failures[0], "9998.md") {
		t.Fatalf("expected the closed-status fixture to be reported, got %v", failures)
	}
}
