package handlers

import (
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

// #0220: five times now (#0190, #0192, #0196, #0199, #0216) a comment has
// shipped a citation nothing checked — first Go test-function names
// (#0196's guard, in dangling_test_citation_guard_test.go, this same
// package), and then, in #0216's own fix, a citation of a repository PATH
// that does not exist (e.g. "web/HANDOFF", found by #0094's review and
// folded into #0216) and a CLAUDE.md section reference. #0196's guard resolves one
// citation shape (Test* identifiers) against one ground truth (defined Go
// functions). This file resolves two more shapes — a repo-relative PATH,
// and a "CLAUDE.md §N" section reference — against their own ground truths:
// the files actually on disk, and CLAUDE.md's own numbered "## N. " /
// "## Na. " headings. Same technique as #0196 throughout (go/ast comment
// walk, a regexp naming the candidate shape, a set of exclusion rules
// mutation-tested against the real tree, a repo-relative pasteable failure
// message) — deliberately not a new design, per this issue's own framing
// ("follow #0196's shape rather than inventing a second design").
//
// Reuses citedTestScanRoots, walkGoFiles, skipVendoredDir,
// stripCommentMarkers, and toRepoRelativePath from
// dangling_test_citation_guard_test.go (same package, same file set to
// scan, same "_test.go files included" requirement #0196 already
// established the reasoning for) rather than declaring a second copy.
// citation_guard_test.go and dangling_test_citation_guard_test.go are both
// clean in git status as this file is written (checked before starting),
// so reusing their exported-within-package helpers carries none of the
// concurrent-edit risk that made #0196 declare its own copies instead of
// #0181's.
//
// Self-scan: this file is itself a _test.go file the guard below will
// walk, so every "path-shaped" or "CLAUDE.md §N" token in ITS OWN real Go
// comments must resolve. It does — every path cited above and below names
// a file this repo actually has (verified as part of writing this file,
// and re-verified every time TestNoCommentCitesUnresolvedPathOrSection runs
// against the tree with this file included, per its own
// ## Verification). The fictitious examples this file needs to prove the
// guard actually fires (see the "Synthetic" tests below) live in Go STRING
// DATA inside test bodies, never in a real comment token this file's own
// parser would visit — the same technique #0196's file states for itself.

// citationTargetPathExtensions is the fixed set of file extensions a cited
// repository path may end in. Requiring one (rather than matching any
// slash-separated token) is what keeps a bare word like "and/or" or a unit
// like "km/h" out of the candidate set entirely, and is why every citation
// this guard resolves needs at least a "/" plus a real extension — a bare
// filename with no directory ("auth.go") is common enough as ordinary
// prose (`git grep`-style shorthand, "see auth.go") that treating it as a
// citation would be far too noisy; every real path citation surveyed in
// this tree spans at least two segments (see this issue's dry-run notes in
// issues/0220.md's Implementation notes).
//
// "HANDOFF", "README", and "LICENSE" are the one deliberate exception:
// three top-level docs this repo's own comments cite WITHOUT their ".md"
// suffix often enough that the real historical defect this issue reinstates
// (e.g. "web/HANDOFF", #0094/#0216) used exactly that bare form. Extending the
// extension requirement to also accept these three bare names by name
// (not by loosening the pattern generally) is what makes that citation
// resolvable as a candidate at all.
var citationTargetPathPattern = regexp.MustCompile(`\b[A-Za-z0-9_][A-Za-z0-9_.\-]*(?:/[A-Za-z0-9_.\-]+)*/(?:[A-Za-z0-9_.\-]+\.(?:go|ts|tsx|svelte|md|sql|sh|json|ya?ml|css|html|mjs|cjs)|HANDOFF|README|LICENSE)\b`)

// citationTargetFileLikeSegmentPattern recognizes a segment that is ITSELF
// a complete filename with a recognized extension — used only to detect
// the "two full filenames joined by a bare slash" shape (see
// pathCitationIsExcluded), never to match a citation directly. A real
// directory segment never ends in ".go"/".ts"/etc, so this is a structural
// signal, not a name-based allowlist.
var citationTargetFileLikeSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.\-]+\.(?:go|ts|tsx|svelte|md|sql|sh|json|ya?ml|css|html|mjs|cjs)$`)

// citationTargetSectionPattern matches "CLAUDE.md §N" / "CLAUDE.md's §N".
// Deliberately anchored to "CLAUDE.md" appearing immediately before the
// "§" (allowing an ASCII or curly possessive "'s"/"’s" and ordinary
// whitespace in between) rather than matching a bare "§N" and asking
// separately whether CLAUDE.md was mentioned nearby: a dry run of every
// bare "§\d+" in the tree not already preceded by "CLAUDE" or "PRD" within
// 25 characters found 138 hits, and every single one was a citation of a
// COMPLETELY DIFFERENT numbering scheme — an issue's own plan section
// ("#0038 §4", "this issue's plan §11") or a tracker-file section
// ("issues/0045.md §2") — that has no relationship to CLAUDE.md's headings
// at all (issues/0220.md's Implementation notes list the sample). Widening
// this to a bare "§N" would flood the guard with false candidates from
// that unrelated convention; anchoring to "CLAUDE.md" is what keeps it
// scoped to the one citation shape this guard can actually resolve.
var citationTargetSectionPattern = regexp.MustCompile(`CLAUDE\.md(?:['’]s)?\s*§\s*(\d+[A-Za-z]?)`)

// citationTargetHypotheticalMarkers, checked case-insensitively in the text
// immediately preceding a candidate match, is the "deliberately
// hypothetical example" exclusion the acceptance criteria name explicitly.
// #0220's own Description supplies the load-bearing real instance:
// citationGuard.test.ts:76 (web/) reads "issues/NNNN.md (a path to one
// tracker file, e.g." — "e.g." fires this rule, and the NNNN-placeholder
// rule below (citationTargetPlaceholderSegments) closes the same real
// instance a second, structural way. Neither rule alone is redundant with
// the other: "e.g." catches an example introduced by that phrase without
// needing the placeholder to be spelled NNNN specifically, and the
// placeholder rule catches a literal NNNN even mid-sentence with no
// introducing phrase.
var citationTargetHypotheticalMarkers = []string{
	"e.g.", "for example", "such as", "hypothetical", "imagine",
	"not a real path", "does not exist", "no such file", "made up", "placeholder",
}

// citationTargetHistoricalMarkers, checked in the same window as
// citationTargetHypotheticalMarkers, discounts a citation of something the
// comment itself says no longer exists — an honest historical reference,
// not a false present-tense claim. See pathCitationIsExcluded for the two
// real instances this closes.
var citationTargetHistoricalMarkers = []string{
	"deleted in", "the deleted", "was deleted", "removed in", "no longer exists", "used to live at",
}

// citationTargetExternalDependencyMarkers, checked unbounded within the
// current comment group (see pathCitationIsExcluded), discounts a path
// citation that names a third-party dependency's own internal layout
// rather than this repository's.
var citationTargetExternalDependencyMarkers = []string{
	"goldmark",
}

// citationTargetPlaceholderSegments: this repo's own convention (see
// issues/Issues.md's own filing template, "NNNN.md") for "an issue number
// not yet assigned" — a path segment (its extension, if any, stripped)
// that is literally this placeholder is a template example, not a real
// citation. Structural (checked against the literal token, not an
// allowlisted whole path), so it closes every path built from the
// convention, not just the one instance found in the tree today.
var citationTargetPlaceholderSegments = map[string]bool{
	"NNNN": true,
}

// citationTargetBareDocNames names this repo's own well-known top-level
// documents that its comments routinely cite WITHOUT their extension —
// "PRD" for PRD.md throughout the tree ("PRD §6.6" everywhere; #0181's own
// guard treats "PRD" the identical way), and "HANDOFF"/"README"/"LICENSE"
// for the same convention seen less often. Used only to recognize an
// INTERMEDIATE segment of a joined-names citation as "this is also a
// known document, not a directory" (see pathCitationIsExcluded) — the
// real instance, campaign_markdown.go:14's "PRD/CLAUDE.md §9" (meaning
// "PRD §9 and CLAUDE.md §9", not a nested path under a "PRD/" directory
// that does not exist), is what motivates including "PRD" here even
// though citationTargetPathPattern's own bare-suffix alternation
// (HANDOFF|README|LICENSE) does not.
var citationTargetBareDocNames = map[string]bool{
	"HANDOFF": true,
	"README":  true,
	"LICENSE": true,
	"PRD":     true,
}

// pathCitationIsExcluded narrows citationTargetPathPattern's candidates to
// the ones a dry run of the whole tree found are not real path citations —
// mirroring citationIsExcluded's own shape in
// dangling_test_citation_guard_test.go: narrow, structural rules, each
// tied to a real instance, not a blanket loosening of the pattern.
func pathCitationIsExcluded(text string, start, end int) bool {
	cited := text[start:end]

	// A URL: the candidate sits inside a scheme-prefixed token
	// ("https://example.com/docs/guide.md", citation_guard_test.go's own
	// documented false-positive example for #0181's guard, restated here
	// as prose in THIS guard's reasoning too). "://" need only appear
	// somewhere in the run of non-whitespace immediately before the
	// match — checked as a fixed lookback window (Go's RE2 has no
	// lookbehind), wide enough to cover "https://" (8 characters) and
	// then some.
	windowStart := start - 24
	if windowStart < 0 {
		windowStart = 0
	}
	if strings.Contains(text[windowStart:start], "://") {
		return true
	}

	// A deliberately hypothetical example, flagged by an introducing
	// phrase within the preceding 60 characters.
	proseStart := start - 60
	if proseStart < 0 {
		proseStart = 0
	}
	lowerWindow := strings.ToLower(text[proseStart:start])
	for _, marker := range citationTargetHypotheticalMarkers {
		if strings.Contains(lowerWindow, marker) {
			return true
		}
	}

	// A citation of something that legitimately no longer exists,
	// documented honestly as history rather than asserted as present —
	// the same shape #0196's Test*-citation guard discounts for "renamed
	// from". Two real instances: request.go:25's "(deleted in #0002
	// along with the rest of internal/handlers/redirect.go)", and
	// testutil_test.go:7's "Ported alongside clientIP (request.go) from
	// the deleted\ninternal/handlers/url_filters_test.go". Checked in the
	// same 60-character window as the hypothetical markers above — both
	// real instances sit well inside it.
	for _, marker := range citationTargetHistoricalMarkers {
		if strings.Contains(lowerWindow, marker) {
			return true
		}
	}

	// A citation of a NAMED EXTERNAL DEPENDENCY's own source layout, not
	// this repo's — the real instance is internal/mailing
	// /campaign_markdown.go's three references to goldmark's own
	// "renderer/html/html.go" and "renderer/renderer.go", which this
	// repo's tree does not and should not contain (goldmark lives in the
	// module cache, not this checkout). Checked unbounded within the
	// WHOLE comment (text is already one comment group's full joined
	// text — see collectCitationTargetHits), not a fixed window: what
	// licenses reading a subsequent path as "inside that dependency" is
	// the surrounding explanation establishing which dependency is being
	// discussed, not just the adjacent phrase. "goldmark" is the one
	// third-party library this repo's comments cite by internal file
	// path today; extend this list by name, the same way
	// citationTargetBareDocNames is extended by name, if another one
	// turns up.
	for _, dep := range citationTargetExternalDependencyMarkers {
		if strings.Contains(strings.ToLower(text[:start]), dep) {
			return true
		}
	}

	segments := strings.Split(cited, "/")

	// This repo's own NNNN placeholder convention, any segment.
	for _, seg := range segments {
		base := seg
		if dot := strings.LastIndexByte(base, '.'); dot >= 0 {
			base = base[:dot]
		}
		if citationTargetPlaceholderSegments[base] {
			return true
		}
	}

	// Two (or more) whole filenames — or known bare document names, see
	// citationTargetBareDocNames — joined by a bare "/" as a list
	// separator, not a nested path — e.g.
	// "campaigns_test.go/audience_test.go" (a real instance,
	// worker_test_helpers_test.go:2: "following
	// campaigns_test.go/audience_test.go's own conventions"),
	// "Campaigns.svelte/CampaignEditor.svelte" (the equivalent web/
	// shape, found in the parallel dry run behind citationGuard.test.ts),
	// or "PRD/CLAUDE.md" (campaign_markdown.go:14, meaning "PRD §9 and
	// CLAUDE.md §9", not a nested path). Every INTERMEDIATE segment (not
	// the last) already looking like a complete filename with a
	// recognized extension, OR being one of this repo's own bare
	// document names, is what tells the two shapes apart structurally.
	for _, seg := range segments[:len(segments)-1] {
		if citationTargetFileLikeSegmentPattern.MatchString(seg) || citationTargetBareDocNames[seg] {
			return true
		}
	}

	return false
}

// pathCitationResolves reports whether cited names a real file, checking
// (in order): the exact repo-relative path; the same path with ".md"
// appended (the bare "HANDOFF", "README", or "LICENSE" form); a real path that ENDS
// WITH "/" + cited (the shortened-citation shape this tree actually uses —
// "handlers/auth.go" for internal/handlers/auth.go,
// logout_all_test.go:285; "admin/Campaigns.svelte" for
// web/src/views/admin/Campaigns.svelte); and the same suffix check with
// ".md" appended. Then, ONLY if none of those resolves and the first
// segment carries no extension of its own, retries with that first
// segment stripped — closing the "leading bare word before a real,
// independently-resolvable path" shape a real instance produces
// (WorkshopsIndex.svelte:7's "WorkshopCard/lib/workshops.ts", meaning
// "WorkshopCard[.svelte] and lib/workshops.ts", not a nested path under a
// "WorkshopCard/" directory that does not exist). This retry is bounded to
// stripping exactly one leading segment, not iterated — deliberately
// narrower than a general "any suffix of the candidate resolves" rule,
// which would also rescue a genuinely dangling multi-segment citation
// whose tail happened to coincidentally match some unrelated real file.
func pathCitationResolves(paths map[string]bool, cited string) bool {
	if pathExistsDirectly(paths, cited) {
		return true
	}
	segments := strings.Split(cited, "/")
	if len(segments) >= 3 && !strings.Contains(segments[0], ".") {
		remainder := strings.Join(segments[1:], "/")
		if pathExistsDirectly(paths, remainder) {
			return true
		}
	}
	return false
}

func pathExistsDirectly(paths map[string]bool, cited string) bool {
	if paths[cited] || paths[cited+".md"] {
		return true
	}
	suffix := "/" + cited
	suffixMD := suffix + ".md"
	for p := range paths {
		if strings.HasSuffix(p, suffix) || strings.HasSuffix(p, suffixMD) {
			return true
		}
	}
	return false
}

// citationTargetSkipDirs are pruned entirely from the repo-path walk:
// version control internals and the one genuinely large vendored tree.
// Deliberately NOT "dist" (web/dist/): web/dist/index.html is a tracked
// placeholder (CLAUDE.md §1, "the built SPA is embedded via //go:embed"),
// and several real comments cite it as "dist/index.html" or
// "web/dist/index.html" (cmd/opencircuit/main.go, static.go, confirm.go,
// web/embed.go, internal/seo). Pruning "dist" would make every one of
// those legitimate citations unresolvable.
var citationTargetSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
}

// collectRepoPaths walks the WHOLE repository tree (not just .go files —
// a cited path may point at a .ts, .svelte, .md, .sql, or any other file)
// and returns the set of every file's path relative to repoRoot, forward
// slashes, so pathCitationResolves compares like with like regardless of
// host OS.
func collectRepoPaths(t *testing.T, repoRoot string) map[string]bool {
	t.Helper()
	paths := map[string]bool{}
	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if citationTargetSkipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(repoRoot, path)
		if rerr != nil {
			return rerr
		}
		paths[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo tree: %v", err)
	}
	return paths
}

// claudeMDHeadingPattern matches CLAUDE.md's own numbered top-level
// headings ("## 1. What this is", "## 5a. Concurrency …", "## 11. PRD
// section index") — the ground truth for section 2. `##` (not `###`) and a
// leading digit are what keep this from matching an un-numbered "###"
// subsection heading.
var claudeMDHeadingPattern = regexp.MustCompile(`(?m)^##\s+(\d+[A-Za-z]?)\.\s`)

// loadClaudeMDSections reads CLAUDE.md fresh off disk every run — so this
// guard tracks CLAUDE.md's actual current section numbering automatically,
// the same way #0113/#0148's PRD-section-index guard tracks PRD.md, rather
// than hardcoding a section list that would itself drift.
func loadClaudeMDSections(t *testing.T, repoRoot string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	sections := map[string]bool{}
	for _, m := range claudeMDHeadingPattern.FindAllStringSubmatch(string(data), -1) {
		sections[m[1]] = true
	}
	return sections
}

type citationTargetHit struct {
	pos   token.Position
	kind  string // "path" or "section"
	cited string
}

// collectCitationTargetHits walks every comment (both // and doc-comment
// forms, block comments included, _test.go files included — matching
// #0196's own walk) under roots and returns one hit per unresolved
// candidate of either shape.
//
// Each comment GROUP's lines are joined into one text blob (mirroring
// dangling_test_citation_guard_test.go's collectTestCitations' own
// lines/positions/spans construction) rather than matched one physical
// line at a time — a real instance needed it:
// dist_placeholder_guard_test.go's "hashedAssetPattern matches … e.g.\n
// `/assets/index-BDtqW4JY.js` or `/assets/index-TVYljy6F.css`" has its
// "e.g." marker on the line BEFORE the citation it excuses, so a
// per-line-only match would never see it. Joining the group's own lines
// (not the whole file — a citation is excused only by context inside its
// OWN surrounding explanation) is what lets pathCitationIsExcluded's
// window-based checks see across that break, and is also what the
// goldmark exclusion's whole-group lookback depends on.
//
// Not the FURTHER cross-line reconstruction collectTestCitations does for
// an identifier wrapped with no separator at all: a path or "CLAUDE.md
// §N" citation split mid-token across a line break does not occur
// anywhere in the tree today (checked as part of writing this guard).
// Accepted as a residual gap for the same reason #0199 accepted the
// equivalent one for the Go guard's own seam pass — no real instance
// exists to motivate the extra machinery.
func collectCitationTargetHits(t *testing.T, roots []string, paths map[string]bool, sections map[string]bool) []citationTargetHit {
	t.Helper()
	var hits []citationTargetHit
	walkGoFiles(t, roots, func(path string) {
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, group := range file.Comments {
			var lines []string
			var positions []token.Position
			for _, c := range group.List {
				base := fset.Position(c.Slash)
				stripped := stripCommentMarkers(c.Text)
				for j, part := range strings.Split(stripped, "\n") {
					pos := base
					pos.Line += j
					lines = append(lines, part)
					positions = append(positions, pos)
				}
			}

			type span struct {
				start int
				pos   token.Position
			}
			var b strings.Builder
			spans := make([]span, 0, len(lines))
			for i, line := range lines {
				spans = append(spans, span{start: b.Len(), pos: positions[i]})
				b.WriteString(line)
				b.WriteByte('\n')
			}
			text := b.String()

			posAt := func(offset int) token.Position {
				sp := spans[0]
				for _, s := range spans {
					if s.start > offset {
						break
					}
					sp = s
				}
				return sp.pos
			}

			for _, m := range citationTargetPathPattern.FindAllStringIndex(text, -1) {
				start, end := m[0], m[1]
				if pathCitationIsExcluded(text, start, end) {
					continue
				}
				cited := text[start:end]
				if !pathCitationResolves(paths, cited) {
					hits = append(hits, citationTargetHit{pos: posAt(start), kind: "path", cited: cited})
				}
			}

			for _, m := range citationTargetSectionPattern.FindAllStringSubmatchIndex(text, -1) {
				whole := text[m[0]:m[1]]
				section := text[m[2]:m[3]]
				if sections[section] {
					continue
				}
				hits = append(hits, citationTargetHit{pos: posAt(m[0]), kind: "section", cited: whole})
			}
		}
	})
	return hits
}

// TestNoCommentCitesUnresolvedPathOrSection is the guard: it fails if any
// Go comment anywhere in the tree (test files included) cites a
// repository path that does not exist, or a "CLAUDE.md §N" section CLAUDE.md
// itself does not define, after excluding the shapes pathCitationIsExcluded
// discounts.
func TestNoCommentCitesUnresolvedPathOrSection(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	baseDir := filepath.Dir(thisFile)
	repoRoot := filepath.Join(baseDir, "..", "..")

	var roots []string
	for _, rel := range citedTestScanRoots {
		roots = append(roots, filepath.Join(baseDir, rel))
	}

	paths := collectRepoPaths(t, repoRoot)
	sections := loadClaudeMDSections(t, repoRoot)
	if len(sections) == 0 {
		t.Fatal("loadClaudeMDSections found zero numbered headings in CLAUDE.md — the heading pattern itself is broken, not the tree")
	}
	hits := collectCitationTargetHits(t, roots, paths, sections)

	if len(hits) == 0 {
		return
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].pos.Filename != hits[j].pos.Filename {
			return hits[i].pos.Filename < hits[j].pos.Filename
		}
		return hits[i].pos.Line < hits[j].pos.Line
	})
	var b strings.Builder
	b.WriteString("comment cites a repository path or CLAUDE.md section that does not resolve — fix the citation or the target (see #0196, #0216, #0220):\n")
	for _, h := range hits {
		b.WriteString("  " + toRepoRelativePath(repoRoot, h.pos.Filename) + ":" + strconv.Itoa(h.pos.Line) + ": [" + h.kind + "] " + h.cited + "\n")
	}
	t.Error(b.String())
}

// TestCitationTargetPathPatternCatchesSyntheticExample proves the path
// pattern + resolver actually fire, against a synthetic source string
// rather than a live dangling citation (mirrors
// TestDanglingTestCitationPatternCatchesSyntheticExample's own reasoning
// in dangling_test_citation_guard_test.go). Built from string
// concatenation, never a bare "segment" + "/" + "name.ext"-shaped literal token, so
// this fixture itself does not become a citation
// TestNoCommentCitesUnresolvedPathOrSection would see when it scans this
// file's own real comments.
func TestCitationTargetPathPatternCatchesSyntheticExample(t *testing.T) {
	fictitiousPath := "internal/handlers/" + "fictitious_file_for_guard_self_proof.go"
	src := "package fixture\n\n" +
		"// see " + fictitiousPath + " for the rationale\n" +
		"func Example() {}\n"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	var found []string
	for _, group := range file.Comments {
		for _, m := range citationTargetPathPattern.FindAllString(group.Text(), -1) {
			found = append(found, m)
		}
	}
	if len(found) != 1 || found[0] != fictitiousPath {
		t.Fatalf("expected exactly one citation of %q, got %v", fictitiousPath, found)
	}
	// This exact path does not exist in the real tree (confirmed as part
	// of writing this fixture), so a tree-wide run that included this
	// file would correctly report it missing — which is exactly why it
	// lives in a string, not a real comment.
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot = filepath.Join(repoRoot, "..", "..")
	paths := collectRepoPaths(t, repoRoot)
	if pathCitationResolves(paths, fictitiousPath) {
		t.Fatalf("fixture setup: %q unexpectedly resolves against the real tree — pick a different fictitious name", fictitiousPath)
	}
}

// TestCitationTargetSectionPatternCatchesSyntheticExample is the section-
// citation half of the same proof: a "CLAUDE.md §N" reference to a section
// number CLAUDE.md does not define.
func TestCitationTargetSectionPatternCatchesSyntheticExample(t *testing.T) {
	src := "package fixture\n\n" +
		"// see CLAUDE.md §" + "9999" + " for the rationale\n" +
		"func Example() {}\n"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	var found []string
	for _, group := range file.Comments {
		for _, m := range citationTargetSectionPattern.FindAllStringSubmatch(group.Text(), -1) {
			found = append(found, m[1])
		}
	}
	if len(found) != 1 || found[0] != "9999" {
		t.Fatalf("expected exactly one section citation of %q, got %v", "9999", found)
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot = filepath.Join(repoRoot, "..", "..")
	sections := loadClaudeMDSections(t, repoRoot)
	if sections["9999"] {
		t.Fatalf("fixture setup: CLAUDE.md unexpectedly has a section %q — pick a different fictitious number", "9999")
	}
}

// TestCitationTargetPathPatternExcludesDiscountedShapes proves
// pathCitationIsExcluded's classes directly, one assertion per class, plus
// one control case that must NOT be excluded — mirroring
// TestDanglingTestCitationPatternExcludesDiscountedShapes's own shape in
// dangling_test_citation_guard_test.go.
func TestCitationTargetPathPatternExcludesDiscountedShapes(t *testing.T) {
	testCase := func(text string) (start, end int) {
		loc := citationTargetPathPattern.FindStringIndex(text)
		if loc == nil {
			t.Fatalf("fixture text %q: pattern did not match at all", text)
		}
		return loc[0], loc[1]
	}

	// URL-embedded — the exact real instance in citation_guard_test.go's
	// own reasoning comment ("https://example.com/docs/guide.md").
	urlText := `see "https://example.com/docs/guide.md" for the rationale`
	s, e := testCase(urlText)
	if !pathCitationIsExcluded(urlText, s, e) {
		t.Errorf("URL-embedded path %q: want excluded, got flagged", urlText[s:e])
	}

	// Hypothetical example, introduced by "e.g." — the real instance in
	// web/'s citationGuard.test.ts:76.
	hypoText := "issues/NNNN.md (a path to one tracker file, e.g. issues/handlers/example.go)"
	// NNNN itself is also excluded by the placeholder rule below; assert
	// the "e.g." rule alone on the SECOND candidate in this text, which
	// carries no NNNN segment.
	matches := citationTargetPathPattern.FindAllStringIndex(hypoText, -1)
	if len(matches) < 2 {
		t.Fatalf("fixture text %q: expected at least 2 candidates, got %d", hypoText, len(matches))
	}
	secondStart, secondEnd := matches[1][0], matches[1][1]
	if !pathCitationIsExcluded(hypoText, secondStart, secondEnd) {
		t.Errorf("hypothetical example %q: want excluded by 'e.g.', got flagged", hypoText[secondStart:secondEnd])
	}

	// The NNNN placeholder convention — the real instance,
	// citationGuard.test.ts:76's "issues/NNNN.md".
	placeholderText := "see issues/NNNN.md for the template"
	s, e = testCase(placeholderText)
	if !pathCitationIsExcluded(placeholderText, s, e) {
		t.Errorf("NNNN placeholder %q: want excluded, got flagged", placeholderText[s:e])
	}

	// Two whole filenames joined by a bare "/" — the real instance,
	// worker_test_helpers_test.go:2's
	// "campaigns_test.go/audience_test.go".
	joinedText := "following " + "campaigns_test.go" + "/" + "audience_test.go" + "'s own conventions"
	s, e = testCase(joinedText)
	if !pathCitationIsExcluded(joinedText, s, e) {
		t.Errorf("joined-filenames shape %q: want excluded, got flagged", joinedText[s:e])
	}

	// Control: a genuine, non-discounted candidate must NOT be excluded.
	controlText := "internal/handlers/" + "genuinely_undefined_control_case.go"
	s, e = testCase(controlText)
	if pathCitationIsExcluded(controlText, s, e) {
		t.Errorf("plain citation %q: want NOT excluded, got excluded", controlText[s:e])
	}
}

// TestPathCitationResolvesShortenedAndLeadingWordForms proves
// pathCitationResolves' two non-exact-match rules directly, against the
// real tree's own path set (not a synthetic one), using the exact real
// instances that motivated each rule.
func TestPathCitationResolvesShortenedAndLeadingWordForms(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	paths := collectRepoPaths(t, repoRoot)

	// Exact repo-relative match.
	if !pathCitationResolves(paths, "internal/handlers/citation_guard_test.go") {
		t.Error("exact repo-relative path did not resolve")
	}

	// Shortened form (drops "internal/") — logout_all_test.go:285's real
	// "handlers/auth.go".
	if !pathCitationResolves(paths, "handlers/auth.go") {
		t.Error("shortened suffix path \"handlers/auth.go\" did not resolve against internal/handlers/auth.go")
	}

	// Leading bare word before an independently-resolvable remainder —
	// WorkshopsIndex.svelte:7's real "WorkshopCard/lib/workshops.ts".
	if !pathCitationResolves(paths, "WorkshopCard/lib/workshops.ts") {
		t.Error("leading-bare-word shape \"WorkshopCard/lib/workshops.ts\" did not resolve against web/src/lib/workshops.ts")
	}

	// Bare "HANDOFF"/"README"/"LICENSE" form with ".md" appended — the
	// mutation this issue reinstates below is the ONE real historical
	// instance that did NOT resolve; a real, valid one must.
	if !pathCitationResolves(paths, "docs/README") {
		t.Skip("docs/README.md does not exist in this checkout — nothing to assert (see assets/logo/README.md instead)")
	}

	// The actual reinstated historical defect must NOT resolve.
	if pathCitationResolves(paths, "web/HANDOFF") {
		t.Error("\"web/HANDOFF\" unexpectedly resolves — web/ has no HANDOFF(.md) file; this was #0216's real dangling citation")
	}
}
