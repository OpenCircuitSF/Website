package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// #0181: the same defect — an internal document citation (CLAUDE.md, PRD, or
// an issue number) leaking into copy an admin actually reads — was found
// three separate times on three separate axes: #0172 swept web/, #0175's
// review swept Go strings that render verbatim, and #0178's review swept
// backwards from the SPA (api.ts's request() collapses every non-2xx body
// to err?.error ?? err?.message and renders it at ~25 sites; four more
// success-path `message` fields render raw too). Each sweep was thorough
// within its method and blind outside it. This guard closes the class
// instead of relying on a fourth sweep: it fails if ANY string literal in a
// package that can put text into an admin-facing HTTP body cites one of
// those documents.
//
// Scope, verified rather than assumed against the boundary #0178's review
// established: internal/handlers (writeError/writeJSON), internal/middleware
// (auth.go and ratelimit.go write {"error": "<code>"} by hand — machine
// codes, but scanned anyway since it costs nothing), and internal/seo
// (site.go's plain-text http.Error, non-SPA) are the three packages that
// call an HTTP body-writing primitive directly. But the string literal an
// admin actually reads does not always originate in the package that writes
// it: internal/mailing.PreflightFailure.Message is authored entirely in
// internal/mailing/preflight.go ("Message is operator-facing; #0047 renders
// it verbatim" — preflight.go's own doc comment) and is serialized untouched
// by a handler. That is precisely the shape of the #0178 leak this guard
// exists to prevent a repeat of (preflight.go:171's PRD §6.6 citation), so
// internal/mailing is in scope too. No other internal/ package defines an
// exported "Message"-shaped field or otherwise threads a string literal
// into a response body — confirmed by grepping every internal/ package for
// `Message string` and `.Error()` outside these four and finding nothing
// that reaches a handler's response (internal/sesnotify's rawMessage is an
// unrelated parameter name; internal/auth/login.go's one `.Error()` hit is
// inside a comment).
//
// Deliberately AST-based, not a text grep over the file: go/ast walks only
// *ast.BasicLit string-literal nodes, so comments are structurally excluded
// — no allowlist needed, and the same citations remain correct and wanted
// there (#0172 deliberately moved them into comments; #0178 followed suit).
// CAN-SPAM §7704 and the EMAIL_REPLY_TO / EMAIL_LIST_DOMAIN env var names
// need no special-casing either: neither matches citationPattern, so they
// pass by construction rather than by exemption. See
// TestCitationScanExcludesCommentsAndAllowedCitations and
// TestCitationScanCatchesDocCitation for direct, synthetic-source proof of
// both properties, and this file's own accompanying manual proof (recorded
// in issues/0181.md) that restoring one of #0178's pre-fix strings makes
// TestNoAdminFacingStringCitesInternalDocs below fail.
//
// The pattern requires a word boundary after the three digits so it cannot
// match inside a hex color: internal/mailing/templates.go's
// colorButtonText = "#04140a" contains the substring "#041" but "\b" fails
// between the '4' and the '0' that follows it (both word characters), so it
// does not trip the guard. "PRD\s*§" (rather than a single literal space)
// closes the "PRD  §6.6" (two spaces) and "PRD§6.6" (no space) variants —
// both plausible typography a human could produce, both missed by the
// original pattern, and both zero-cost to close (#0181 review, pass 2).
// Issues.md, HANDOFF.md, and docs/*.md join CLAUDE.md and PRD § as
// documents an admin cannot read either — #0178's own whole-tree grep
// already included Issues.md and HANDOFF, and there is no principled
// reason to guard one internal document and not the others.
//
// #0187, item 2: `issues/NNNN.md` (a path to one tracker file, e.g.
// "issues/0138.md") was missing even though docs/*.md was added — an
// asymmetry, not a decision, since a tracker-file path is the least
// readable citation of the whole set to an admin. `issues/[0-9]{4}\.md`
// closes it; the repo's own #0NNN convention already guarantees the
// four-digit form.
//
// #0187, item 3: docs/\S*\.md (and, after this pass, issues/[0-9]{4}\.md)
// would also fire on an external URL that happens to contain a matching
// path segment, e.g. "https://example.com/docs/guide.md". Decided:
// accepted rather than tightened. Go's regexp/syntax (RE2) has no
// lookbehind, so excluding a preceding "scheme://" cannot be expressed in
// this pattern the way it can in the web guard's PCRE-flavored engine
// (V8) — closing it here would mean the two guards no longer share one
// pattern shape, which is a bigger maintenance hazard than the false
// positive it prevents. The false positive is also nil today and
// structurally unlikely to appear: CLAUDE.md §9 forbids external CDNs,
// and the #0181 review's whole-tree sweep found every existing docs/*.md
// occurrence in scanned Go scope was a `//` comment, not a live literal.
var citationPattern = regexp.MustCompile(`CLAUDE\.md|PRD\s*§|#0[0-9]{3}\b|Issues\.md|HANDOFF\.md|docs/\S*\.md|issues/[0-9]{4}\.md`)

// #0181 review, pass 2: rather than reasoning a fixed set of directories
// into scope by hand (internal/mailing had to be added that way on the
// first pass, after tracing PreflightFailure.Message back from #0178's
// leak), scanGoRoots walks every non-test .go file under internal/ and
// cmd/ recursively. The reviewer ran the (three-pattern) version of this
// same scan over the whole repo, cmd/ included, and found zero hits outside
// the four packages already in scope — so widening costs nothing today and
// removes the "is the boundary still right?" question for the next package
// that gains an authored-here-serialized-there field, the way
// internal/mailing did.
//
// #0187: the same review's coverage measurement counted 94 non-test .go
// files collected against 94 on disk under internal/ and cmd/ — set
// identical — but noted a 95th non-test .go file exists outside both roots:
// web/embed.go. It authors one panic string about a malformed embedded FS,
// not admin-facing copy, so nothing was exposed; but a guard whose whole
// point is not needing a boundary argument should not itself need one, so
// "../../web" is added rather than documented as a deliberate exclusion.
// Re-measured after adding it: 95 files collected, 95 non-test .go files on
// disk across internal/, cmd/, and web/ (find . -name '*.go' ! -name
// '*_test.go' under those three roots), set-identical.
//
// All three entries are relative to this file's own directory
// (internal/handlers): ".." is internal/ itself (walked recursively, so
// this file's own package and every sibling package are covered in one
// root), "../../cmd" is the repo's cmd/ tree, and "../../web" is the one
// package outside internal/ and cmd/ that has a non-test .go file.
var scanGoRoots = []string{"..", "../../cmd", "../../web"}

type citationViolation struct {
	pos   token.Position
	value string
}

// scanDirForCitations parses every non-test .go file under root — recursing
// into subdirectories, since internal/ and cmd/ both nest packages — and
// returns every string literal that matches citationPattern.
func scanDirForCitations(t *testing.T, root string) []citationViolation {
	t.Helper()

	var violations []citationViolation
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}

		violations = append(violations, scanFileForCitations(fset, file)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return violations
}

// scanFileForCitations walks only *ast.BasicLit string-literal nodes —
// comments are a separate concern in go/ast and are never visited here.
func scanFileForCitations(fset *token.FileSet, file *ast.File) []citationViolation {
	var violations []citationViolation
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			// Not a literal we can decode (shouldn't happen for valid Go
			// source) — skip rather than fail the whole scan on it.
			return true
		}
		if citationPattern.MatchString(value) {
			violations = append(violations, citationViolation{
				pos:   fset.Position(lit.Pos()),
				value: value,
			})
		}
		return true
	})
	return violations
}

// TestNoAdminFacingStringCitesInternalDocs is the actual guard: it fails if
// any string literal in the packages that can put text into an
// admin-facing HTTP body cites CLAUDE.md, a PRD section, or an issue
// number.
func TestNoAdminFacingStringCitesInternalDocs(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	baseDir := filepath.Dir(thisFile)

	var all []citationViolation
	for _, rel := range scanGoRoots {
		dir := filepath.Join(baseDir, rel)
		all = append(all, scanDirForCitations(t, dir)...)
	}

	if len(all) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("admin-facing string literal(s) cite an internal document an admin cannot read — move the citation to a code comment beside the string (see #0172, #0175, #0178, #0181):\n")
	for _, v := range all {
		b.WriteString("  " + v.pos.String() + ": " + strconv.Quote(v.value) + "\n")
	}
	t.Error(b.String())
}

// TestCitationScanExcludesCommentsAndAllowedCitations proves the three
// exclusions this guard must respect, against a synthetic source file so
// the proof does not depend on the current state of the real tree:
//
//  1. code comments citing PRD §/CLAUDE.md/an issue number do not trip it
//     (that is exactly where #0172 and #0178 moved these citations to);
//  2. CAN-SPAM §7704 — a statute an admin can look up — does not trip it;
//  3. the EMAIL_REPLY_TO / EMAIL_LIST_DOMAIN env var names do not trip it.
func TestCitationScanExcludesCommentsAndAllowedCitations(t *testing.T) {
	const src = `package fixture

// This comment cites PRD §6.6, CLAUDE.md §9, and #0181 — all fine, since
// comments are for the next engineer, not for the admin reading the UI.
func Messages() []string {
	return []string{
		"Physical mailing address is not set. CAN-SPAM §7704 requires it in every campaign email.",
		"Reply-To address is not configured (EMAIL_REPLY_TO). Set it to an address someone reads.",
		"EMAIL_LIST_DOMAIN is not configured; the one-click unsubscribe header would be malformed.",
	}
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if got := scanFileForCitations(fset, file); len(got) != 0 {
		t.Fatalf("expected zero violations on the exclusion fixture, got %d: %+v", len(got), got)
	}
}

// TestCitationScanCatchesDocCitation proves the guard actually fires: a
// string literal (not a comment) citing PRD §6.6 must be reported.
func TestCitationScanCatchesDocCitation(t *testing.T) {
	const src = `package fixture

func Message() string {
	return "Reply-To address is not configured. PRD §6.6 requires a monitored reply address, not noreply@."
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	got := scanFileForCitations(fset, file)
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].value, "PRD §6.6") {
		t.Fatalf("violation value = %q, want it to contain %q", got[0].value, "PRD §6.6")
	}
}

// TestCitationPatternIgnoresHexColors proves the word-boundary tightening:
// internal/mailing/templates.go's colorButtonText = "#04140a" must not
// trip the guard even though "#041" is a substring.
func TestCitationPatternIgnoresHexColors(t *testing.T) {
	if citationPattern.MatchString("#04140a") {
		t.Fatal("citationPattern matched a hex color; the \\b boundary should have prevented this")
	}
	if !citationPattern.MatchString("see #0181 for context") {
		t.Fatal("citationPattern failed to match a genuine issue reference")
	}
}

// TestCitationPatternCatchesIssueFilePath proves #0187 item 2: a string
// literal citing a tracker file path (as opposed to the "#0181" form) trips
// the guard.
func TestCitationPatternCatchesIssueFilePath(t *testing.T) {
	if !citationPattern.MatchString("see issues/0138.md for the acceptance criteria") {
		t.Fatal("citationPattern failed to match an issues/NNNN.md path")
	}
	if citationPattern.MatchString("see the issues tracker for details") {
		t.Fatal("citationPattern matched ordinary prose containing the word \"issues\"")
	}
}

// TestCitationPatternAcceptsDocsURLFalsePositive documents #0187 item 3's
// decision: docs/\S*\.md is not tightened to exclude a scheme-prefixed
// path, so an external URL containing a matching segment does fire. This
// test exists so a future change to citationPattern that accidentally
// closes (or widens) the gap is visible in a diff rather than silent.
func TestCitationPatternAcceptsDocsURLFalsePositive(t *testing.T) {
	if !citationPattern.MatchString("see https://example.com/docs/guide.md for details") {
		t.Fatal("citationPattern no longer matches the accepted external-URL false positive — update the #0187 comment above citationPattern if this was deliberate")
	}
}
