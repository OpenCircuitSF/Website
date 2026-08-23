package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
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
// does not trip the guard.
//
// #0193: "PRD\s*§" used to mean two different things in the two guards.
// Go's regexp/syntax \s is exactly [\t\n\f\r ] — five ASCII characters, and
// notably not \v (vertical tab, U+000B). JavaScript's \s is Unicode-aware
// by spec (ECMA-262's WhiteSpace and LineTerminator productions) regardless
// of the u/v flag, and additionally matches \v, NBSP (U+00A0), Ogham space
// mark (U+1680), the eleven general-punctuation spaces U+2000-U+200A
// (including thin space, U+2009), the line/paragraph separators U+2028 and
// U+2029, narrow no-break space (U+202F), medium mathematical space
// (U+205F), ideographic space (U+3000), and ZWNBSP/BOM (U+FEFF). So a
// non-breaking space before "§" — exactly what pasting out of a word
// processor produces — was caught by the web half and silently missed by
// this one, the half that covers handler code (#0187's phase-3 review,
// item 1; filed as #0193).
//
// Decided: widen Go to match web, rather than narrow both to a smaller
// explicit set. The web guard's broader coverage was already correct and
// live; narrowing it to make the comparison easier would be removing real
// coverage, not fixing a bug. The class below spells out, by codepoint,
// every character JavaScript's \s matches, so this pattern and the web
// guard's (web/src/lib/citationGuard.test.ts) now say the identical thing
// rather than one leaning on regexp/syntax's \s and the other on V8's —
// verified by running both patterns over the same codepoint cases (see the
// whitespace-shape tests in this file and in citationGuard.test.ts). "PRD
// followed by whitespace then §" (rather than a single literal space)
// originally closed the "PRD  §6.6" (two spaces) and "PRD§6.6" (no space)
// variants (#0181 review, pass 2); the explicit class still closes both,
// plus everything listed above.
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
// #0187, item 3: docs/\S*\.md (and issues/[0-9]{4}\.md) also fire on an
// external URL that happens to contain a matching path segment, e.g.
// "https://example.com/docs/guide.md". Decided: accepted rather than
// tightened (TestCitationPatternAcceptsDocsURLFalsePositive pins it).
//
// #0193 corrects the reasoning recorded here for that decision. It used to
// say only V8 could express a scheme exclusion because RE2 has no
// lookbehind. RE2 lacking lookbehind is true; the inference is false — a
// scheme exclusion does not need lookbehind, only a consumed leading
// boundary, e.g. `(?:^|[^:/\w])docs/\S*\.md`, which compiles and matches
// correctly in both RE2 and V8 verbatim (#0187's phase-3 review built and
// ran it, 13/13 on intent, in both engines — not adopted here; #0193
// reports it as a filing candidate rather than taking on that change). The
// actual reason to accept, once the RE2 claim falls away, is what that
// review gave: the false positive is nil in the tree today, and the cost
// of tightening the pattern is real against that nil risk. It is not
// "tightening would diverge the two guards" — the PRD-whitespace fix above
// exists precisely because the two guards were already diverged on a
// different term, so "the guards must share one pattern shape" was never a
// fact this decision could rest on.
var citationPattern = regexp.MustCompile(`CLAUDE\.md|PRD[\t\n\v\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}]*§|#0[0-9]{3}\b|Issues\.md|HANDOFF\.md|docs/\S*\.md|issues/[0-9]{4}\.md`)

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

// #0194: adding "../../web" to scanGoRoots (#0187, item 1) made this walk
// descend into web/node_modules and web/dist for the first time. Neither
// holds a .go file today (the 95/95 measurement in #0187's review confirms
// it), but both are vendored/build output nobody here authored: an npm
// dependency that happens to ship Go source, or a build artifact, would be
// parsed by scanDirForCitations below, and a parse failure there is a
// t.Fatalf — a routine `npm install` could take this guard down pointing at
// a file nobody in this project wrote. skipVendoredDirNames names every
// directory this walk must never descend into, checked by exact directory
// name (not by path prefix) so it applies uniformly under any of the three
// roots, not just web/. Proof this is live rather than inert: see this
// issue's ## Verification — a .go file citing PRD §6.6 was placed under
// web/node_modules, the guard stayed green (SkipDir took effect before the
// parser ever saw it), and the same file one level up under web/ made the
// guard fail, confirming the skip is scoped to the vendored directory and
// not swallowing web/ itself.
var skipVendoredDirNames = map[string]bool{
	"node_modules": true, // npm dependencies — none of this project's code
	"dist":         true, // Vite build output — generated, not authored
}

// scanDirForCitations parses every non-test .go file under root — recursing
// into subdirectories, since internal/ and cmd/ both nest packages — and
// returns every string literal that matches citationPattern.
//
// #0200: it also returns the path of every file it collected (parsed),
// alongside the violations. This is instrumentation on the shipped
// collection step, not a second implementation of it — the same WalkDir
// call, the same skipVendoredDirNames check, the same suffix filter — so
// TestScanDirForCitationsCollectsExactlyDiskFiles below can compare what
// the real guard walked against what is actually on disk, and a change to
// the skip logic here is automatically visible to that test without also
// needing to be mirrored into it.
func scanDirForCitations(t *testing.T, root string) (violations []citationViolation, collected []string) {
	t.Helper()

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if skipVendoredDirNames[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		collected = append(collected, path)

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
	return violations, collected
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
		v, _ := scanDirForCitations(t, dir)
		all = append(all, v...)
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

// TestCitationPatternMatchesWebWhitespaceShapes proves #0193's fix: the
// five whitespace shapes the phase-3 review of #0187 found caught by the
// web guard's Unicode-aware \s and missed by this file's Go \s (NBSP, thin
// space, VT, ZWNBSP, ideographic space) all match here now, plus the rest
// of the codepoints JavaScript's \s matches by spec that this pattern was
// widened to include. Built from rune codepoints, not pasted characters, so
// what the test asserts is provably what it contains rather than what was
// typed and might not have landed (#0193's own instruction, taken
// seriously: see this file's git history for a Go-only guard that, before
// this fix, missed exactly these cases).
func TestCitationPatternMatchesWebWhitespaceShapes(t *testing.T) {
	section := string(rune(0x00A7)) // §

	mustMatch := []struct {
		name string
		cp   rune
	}{
		{"NBSP", 0x00A0},
		{"thin space", 0x2009},
		{"vertical tab", 0x000B},
		{"ZWNBSP/BOM", 0xFEFF},
		{"ideographic space", 0x3000},
		{"ogham space mark", 0x1680},
		{"line separator", 0x2028},
		{"paragraph separator", 0x2029},
		{"narrow no-break space", 0x202F},
		{"medium mathematical space", 0x205F},
		{"en quad", 0x2000},
		{"hair space", 0x200A},
		{"tab", 0x0009},
		{"newline", 0x000A},
		{"carriage return", 0x000D},
		{"form feed", 0x000C},
		{"regular space", 0x0020},
	}
	for _, c := range mustMatch {
		s := "PRD" + string(c.cp) + section + "6.6"
		if !citationPattern.MatchString(s) {
			t.Errorf("citationPattern did not match PRD + %s (U+%04X) + section 6.6", c.name, c.cp)
		}
	}

	if !citationPattern.MatchString("PRD" + section + "6.6") {
		t.Error("citationPattern did not match PRD immediately followed by section (zero whitespace)")
	}
	if !citationPattern.MatchString("PRD  " + section + "6.6") {
		t.Error("citationPattern did not match PRD followed by two spaces then section")
	}
	if citationPattern.MatchString("PRDA" + section + "6.6") {
		t.Error("citationPattern matched PRD followed by a non-whitespace letter before section; the class must not include ordinary letters")
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

// TestScanDirForCitationsCollectsExactlyDiskFiles is #0200: the 95/95
// walk-coverage invariant, pinned as a permanent test instead of being
// re-derived by scratch probe and thrown away a sixth time (#0181 review
// pass 2, #0187's implementation, #0187's review, #0194's implementation,
// #0194's review all did this and kept none of it).
//
// It runs against the shipped scanDirForCitations — the same function
// TestNoAdminFacingStringCitesInternalDocs above uses for the real guard,
// now also returning the paths it collected — rather than re-implementing
// its walk predicate; a replica could not notice the shipped walk
// narrowing, which is exactly the failure mode this test exists to catch
// (see #0197 on this tracker's history of that mistake).
//
// "on disk" is a second, independent enumeration of the same three
// scanGoRoots that excludes the two vendored trees by ANCHORED PATH PREFIX
// (baseDir-joined "../../web/node_modules" and "../../web/dist") rather
// than reusing skipVendoredDirNames' bare-name check. #0201: the two are
// not interchangeable, and the distinction is the whole point of this
// test. skipVendoredDirNames matches a directory by name at ANY depth, so
// a genuine, non-vendored directory that happens to be named
// "node_modules" or "dist" anywhere under scanGoRoots (see that map's own
// doc comment on internal/dist as the live example) is swallowed by the
// shipped walk today with nothing failing. Reusing that same name-based
// check here would swallow it identically in the ground truth too, and the
// mismatch this test exists to catch would never surface — a circular,
// worthless comparison. A path-prefix exclusion has no such blind spot:
// internal/dist/ does not share a path prefix with web/node_modules or
// web/dist, so it is never excluded from "on disk" no matter how deep a
// name-based skip elsewhere hides it, while a real npm dependency shipping
// a non-test .go file under web/node_modules — the prospective hazard
// #0200's review reported — is excluded from both sides identically and no
// longer fails this test.
//
// Both directions are checked and reported separately, but they are not
// symmetric hazards. A file present on disk but not collected means the
// shipped walk is narrowing beyond the two anchored vendored prefixes — the
// internal/dist scenario above, and the actual bug this test exists to
// catch. A file collected but not on disk is, by construction, unreachable
// for any static filesystem state: "collected" is exactly the shipped
// walk's output minus skipVendoredDirNames' SkipDir, and "on disk" is a
// superset of that same walk (it additionally counts name-matched
// directories that lie outside the two anchored prefixes — internal/dist,
// internal/node_modules, and web/src/node_modules — since the anchoring
// does not exclude them), so collected is always a subset of onDisk.
// #0200's review tried every candidate cause by direct probe — a
// *.go symlink lands in both walks identically since WalkDir lstats; a
// symlinked directory is followed by neither walk; a dangling symlink or a
// directory named *.go dies earlier at the shipped walk's parse
// t.Fatalf — and found none of them can populate this set. The only thing
// that could is a file vanishing between the two walks of the same root
// within a single test run: a filesystem race, not a code path a static
// tree can hit. The branch is kept rather than deleted (temporarily
// narrowing the ground truth in that review correctly turned it on), but
// its message says what it actually guards instead of naming causes that
// cannot produce it.
func TestScanDirForCitationsCollectsExactlyDiskFiles(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	baseDir := filepath.Dir(thisFile)

	// #0201: anchored to baseDir, not matched by bare name — see the
	// doc comment above for why that distinction is load-bearing.
	excludedPrefixes := []string{
		filepath.Join(baseDir, "../../web/node_modules"),
		filepath.Join(baseDir, "../../web/dist"),
	}
	excluded := func(path string) bool {
		for _, prefix := range excludedPrefixes {
			if path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}

	collected := map[string]bool{}
	onDisk := map[string]bool{}
	for _, rel := range scanGoRoots {
		dir := filepath.Join(baseDir, rel)

		_, files := scanDirForCitations(t, dir)
		for _, p := range files {
			collected[p] = true
		}

		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if excluded(path) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				onDisk[path] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	var uncollected, extra []string
	for p := range onDisk {
		if !collected[p] {
			uncollected = append(uncollected, p)
		}
	}
	for p := range collected {
		if !onDisk[p] {
			extra = append(extra, p)
		}
	}
	if len(uncollected) == 0 && len(extra) == 0 {
		return
	}
	sort.Strings(uncollected)
	sort.Strings(extra)

	var b strings.Builder
	b.WriteString("scanDirForCitations's collected file set does not match the non-test .go files on disk under scanGoRoots:\n")
	if len(uncollected) > 0 {
		b.WriteString("  present on disk but NOT collected (skipVendoredDirNames may be swallowing a real directory):\n")
		for _, p := range uncollected {
			b.WriteString("    " + p + "\n")
		}
	}
	if len(extra) > 0 {
		// #0201: this branch is unreachable for any static filesystem state
		// (see the doc comment above the test) — reaching it at all means a
		// file vanished between the shipped walk and this second walk of
		// the same root, within this one run.
		b.WriteString("  collected but NOT present on disk (a file vanished between the two walks of the same root during this run):\n")
		for _, p := range extra {
			b.WriteString("    " + p + "\n")
		}
	}
	t.Fatal(b.String())
}
