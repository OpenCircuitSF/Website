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

// #0267: #0127 shipped with BuildWelcomeEmail's doc comment run together with
// BuildAdminAlertEmail's — the whole block ended up above func
// BuildWelcomeEmail (attaching WelcomeEmail's real documentation there),
// while BuildAdminAlertEmail was left with no doc comment at all ("go doc
// BuildAdminAlertEmail" returned a bare signature). A contiguous "//" block
// is well-formed Go, so gofmt is silent and go vet does not look — nothing
// in the toolchain could see it. It was caught only because a reviewer ran
// "go doc" on the two specific symbols a bounce had named.
//
// This guard closes exactly that shape and no more: a top-level
// declaration's doc comment whose first word names a DIFFERENT top-level
// symbol declared in the same file. It is deliberately narrow — see this
// issue's own Notes — and does not attempt #0263's job (a doc comment that
// is merely wrong, or accurately worded but attached to the right
// declaration for a stylistic reason a machine can't judge); only review
// catches that.
//
// Same technique as #0196/#0220/#0265 (go/ast walk, a small regexp naming
// the candidate shape, exclusion rules earned by a real hit on this tree, a
// repo-relative pasteable failure message) rather than a new design. Reuses
// walkGoFiles, skipVendoredDir, and toRepoRelativePath from
// dangling_test_citation_guard_test.go and citedTestScanRoots from the same
// file (same package, same file set to scan — including _test.go files,
// since a misattached doc comment can land in a test file exactly as easily
// as production code, and that file was clean in git status when this one
// was written).
//
// Two exclusions, both earned by a real hit this issue's own dry run found
// running the first-cut version of this guard over the whole tree (nine
// real files, zero of them an actual instance of #0267's defect):
//
//   - A doc comment atop a MULTI-member const/var block ("type ( A; B )" or
//     "const ( A; B )" with two or more specs) is not checked at all. This
//     codebase's own established idiom for that shape — "Campaign status
//     constants, verbatim from migration 000017's status CHECK"
//     (internal/mailing/campaigns.go), "Preflight failure codes"
//     (internal/mailing/preflight.go), "Suppression reasons, matching the
//     ... CHECK constraint" (internal/subscribers/suppressions.go),
//     "Unsubscribe source values, matching the ..." (whose members are
//     Source*, not Unsubscribe* — internal/subscribers/store.go), "Workshop
//     status constants" (whose members are Status*, not Workshop* —
//     internal/workshops/store.go), and "newTestWorker constructs a Worker
//     ..." glued directly above its own supporting constant block
//     (internal/mailing/worker_test_helpers_test.go) — opens with a
//     category noun or the function the constants support, not necessarily
//     a name any one member repeats. There is no #0127-shaped defect
//     anywhere in this codebase's real history involving a multi-member
//     group (the actual incident was FuncDecl-to-FuncDecl), and no local
//     signal distinguishes "names the group's own subject" from "names an
//     unrelated symbol that happens to share a category word" — so this
//     guard does not attempt the multi-member case, the same accepted-gap
//     shape as citationIsExcluded's wildcard-family rule. A doc comment on
//     an individual member WITHIN such a block (a per-spec Doc distinct
//     from the block's own Doc) is still fully checked — see
//     TestMisattachedDocCommentGuardStillChecksPerMemberDocInsideAGroup —
//     since that is unambiguously about one specific declaration, the same
//     shape the real incident actually was.
//   - misattachedDocKnownBenignPairs (below) closes the one single-spec
//     case the dry run found that isn't a grouped block: see its own
//     comment.
var misattachedDocFirstWordPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)

// misattachedDocKnownBenignPairs is a small, individually-verified allowlist
// of (repo-relative file, first word) pairs this guard's tree-wide run
// flagged and a human confirmed are NOT #0267's defect shape — the same
// role nonTestIdentifierAllowlist plays for the sibling guard
// (dangling_test_citation_guard_test.go), for the same reason: no local,
// structural signal distinguishes these from a real corruption, so naming
// the specific instance is the honest fix, not loosening the pattern.
//
// internal/handlers/public_list_stats.go: "PublicListStatsHandler serves
// GET /api/list-stats: aggregate mailing-list counts for the home page's
// live CRT screen (#0274)." opens const pendingBucket's doc comment, and
// const pendingBucket = 5 is a SINGLE-spec declaration, so the multi-member
// group exclusion above does not reach it. Verified by reading the whole
// comment (not just its first sentence): every paragraph after the opening
// one is substantively and exclusively about pendingBucket specifically —
// why it exists, what it defends against, and it names "pendingBucket" by
// its own identifier partway through ("`pending` is therefore rounded DOWN
// to a multiple of pendingBucket"). That is the opposite of #0127's real
// defect, where the corrupted block glued onto the wrong declaration
// contained NOTHING about the declaration it was actually attached to. The
// one-sentence opener naming the handler is context for why the constant's
// value matters, not evidence the paragraph belongs somewhere else.
//
// Deliberately keyed by file, not by word alone: "PublicListStatsHandler"
// is a 22-character CamelCase identifier unlikely to collide anywhere else
// in the tree, but scoping to its one real file keeps this allowlist from
// silently excusing an unrelated future doc comment that happens to open
// with the same word in a different file. Extend this map — one entry per
// verified-benign instance — if the dry run turns up another; don't widen
// either exclusion rule to admit it structurally.
var misattachedDocKnownBenignPairs = map[string]string{
	"internal/handlers/public_list_stats.go": "PublicListStatsHandler",
}

// declDoc is one top-level declaration's own leading doc comment, paired
// with the set of names it legitimately belongs to in this file.
type declDoc struct {
	doc *ast.CommentGroup
	own map[string]bool
	pos token.Position
}

// addValueSpecNames adds spec's declared names to names ("_" excluded, since
// the blank identifier can't be "the wrong declaration" — nothing can cite
// it).
func addValueSpecNames(names map[string]bool, spec *ast.ValueSpec) {
	for _, n := range spec.Names {
		if n.Name != "_" {
			names[n.Name] = true
		}
	}
}

// collectFileTopLevelNames returns every top-level identifier this file
// declares: function and method names, and the names introduced by
// package-level const/var/type declarations. import declarations are
// skipped — an imported package's local name is not a "symbol declared in
// the same file" in the sense #0267 means.
func collectFileTopLevelNames(file *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name != "_" {
				names[d.Name.Name] = true
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					addValueSpecNames(names, s)
				case *ast.TypeSpec:
					if s.Name.Name != "_" {
						names[s.Name.Name] = true
					}
				}
			}
		}
	}
	return names
}

// collectDeclDocs returns one declDoc per doc comment this guard checks in
// file: a *ast.FuncDecl's own Doc (own = just that function); a
// *ast.GenDecl's Doc ONLY when it has exactly one Spec (own = that spec's
// own names) — a GenDecl with two or more Specs is a genuinely grouped
// block, and this file's header explains why its group-level Doc is not
// checked at all; and, independently of the group-size rule, any
// individual Spec's own Doc when it carries one distinct from the group's
// (own = that one member alone, since a comment written directly above one
// line of a block is documenting that line, not the block).
func collectDeclDocs(fset *token.FileSet, file *ast.File) []declDoc {
	var out []declDoc
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Doc != nil {
				out = append(out, declDoc{
					doc: d.Doc,
					own: map[string]bool{d.Name.Name: true},
					pos: fset.Position(d.Doc.Pos()),
				})
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			if d.Doc != nil && len(d.Specs) == 1 {
				own := map[string]bool{}
				switch s := d.Specs[0].(type) {
				case *ast.ValueSpec:
					addValueSpecNames(own, s)
				case *ast.TypeSpec:
					if s.Name.Name != "_" {
						own[s.Name.Name] = true
					}
				}
				out = append(out, declDoc{doc: d.Doc, own: own, pos: fset.Position(d.Doc.Pos())})
			}
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					if s.Doc != nil && s.Doc != d.Doc {
						own := map[string]bool{}
						addValueSpecNames(own, s)
						out = append(out, declDoc{doc: s.Doc, own: own, pos: fset.Position(s.Doc.Pos())})
					}
				case *ast.TypeSpec:
					if s.Doc != nil && s.Doc != d.Doc {
						out = append(out, declDoc{
							doc: s.Doc,
							own: map[string]bool{s.Name.Name: true},
							pos: fset.Position(s.Doc.Pos()),
						})
					}
				}
			}
		}
	}
	return out
}

// misattachedDoc is one confirmed hit: a declaration's doc comment (at pos)
// opens with firstWord, which names a real top-level symbol in the same
// file that is NOT among own — the declaration(s) this comment is actually
// attached to.
type misattachedDoc struct {
	pos       token.Position
	firstWord string
	own       []string
}

// findMisattachedDocsInFile is the check itself: for each doc comment
// collectDeclDocs considers, take its first word; if that word is a real
// top-level name in this file and is not one of the names the comment is
// actually attached to, it's a hit. A first word that matches nothing
// declared in the file at all (ordinary English, an issue-number citation,
// a name declared in some OTHER file or package) is not checked — #0267
// scopes this to symbols "declared in the same file", which is the one case
// this guard can actually resolve without false accusations. This function
// does not know about misattachedDocKnownBenignPairs — that filter is
// applied by the caller, which has the repo-relative path this function
// deliberately doesn't need.
func findMisattachedDocsInFile(fset *token.FileSet, file *ast.File) []misattachedDoc {
	allNames := collectFileTopLevelNames(file)
	var found []misattachedDoc
	for _, dd := range collectDeclDocs(fset, file) {
		text := strings.TrimSpace(dd.doc.Text())
		word := misattachedDocFirstWordPattern.FindString(text)
		if word == "" {
			continue
		}
		if dd.own[word] {
			continue
		}
		if !allNames[word] {
			continue
		}
		own := make([]string, 0, len(dd.own))
		for n := range dd.own {
			own = append(own, n)
		}
		sort.Strings(own)
		found = append(found, misattachedDoc{pos: dd.pos, firstWord: word, own: own})
	}
	return found
}

// TestNoDocCommentNamesADifferentDeclarationInSameFile is the guard: it
// fails if any top-level declaration anywhere in the tree (test files
// included — see this file's header) carries a doc comment whose first
// word names a different top-level symbol declared in the same file, after
// excluding the shapes documented at the top of this file.
func TestNoDocCommentNamesADifferentDeclarationInSameFile(t *testing.T) {
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

	var found []misattachedDoc
	walkGoFiles(t, roots, func(path string) {
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		relPath := toRepoRelativePath(repoRoot, path)
		for _, m := range findMisattachedDocsInFile(fset, file) {
			if misattachedDocKnownBenignPairs[relPath] == m.firstWord {
				continue
			}
			found = append(found, m)
		}
	})
	if len(found) == 0 {
		return
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].pos.Filename != found[j].pos.Filename {
			return found[i].pos.Filename < found[j].pos.Filename
		}
		return found[i].pos.Line < found[j].pos.Line
	})
	var b strings.Builder
	b.WriteString("doc comment opens by naming a different declaration in the same file — fix the doc comment or move it (see #0127, #0267):\n")
	for _, m := range found {
		b.WriteString("  " + toRepoRelativePath(repoRoot, m.pos.Filename) + ":" + strconv.Itoa(m.pos.Line) +
			": doc says \"" + m.firstWord + "\" but is attached to " + strings.Join(m.own, ", ") + "\n")
	}
	t.Error(b.String())
}

// TestMisattachedDocCommentGuardCatchesThe0127Shape is a direct, isolated
// proof against a throwaway package on disk (t.TempDir(), not this repo's
// own tree), reproducing #0267's own motivating incident with the real
// symbol names it names: a func with no doc comment of its own
// (BuildAdminAlertEmail, matching "go doc BuildAdminAlertEmail returned a
// bare signature") immediately preceded by a doc comment that actually
// describes a DIFFERENT, later function (BuildWelcomeEmail). Using the
// historical names here is safe and not circular: neither name is a real
// declaration in THIS file, so this guard's own tree-wide run
// (TestNoDocCommentNamesADifferentDeclarationInSameFile above) never sees
// them as anything but ordinary prose in a doc comment that isn't attached
// to anything named that.
func TestMisattachedDocCommentGuardCatchesThe0127Shape(t *testing.T) {
	dir := t.TempDir()
	const src = `package fixture

// BuildWelcomeEmail builds the message sent once a subscriber confirms.
func BuildAdminAlertEmail(to string) string { return to }

func BuildWelcomeEmail(to string) string { return to }
`
	writeFixtureGoFile(t, dir, "fixture.go", src)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(dir, "fixture.go"), nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	found := findMisattachedDocsInFile(fset, file)
	if len(found) != 1 {
		t.Fatalf("expected exactly one hit, got %d: %+v", len(found), found)
	}
	if found[0].firstWord != "BuildWelcomeEmail" {
		t.Errorf("expected the hit to name BuildWelcomeEmail as the misplaced doc's subject, got %q", found[0].firstWord)
	}
	if len(found[0].own) != 1 || found[0].own[0] != "BuildAdminAlertEmail" {
		t.Errorf("expected the hit to report it's attached to BuildAdminAlertEmail, got %v", found[0].own)
	}
}

// TestMisattachedDocCommentGuardCleanOnceReattached is the same fixture as
// above with the historical corruption fixed the same way #0127's own fix
// pass fixed it — each function gets its own doc comment naming itself —
// and asserts the guard reports nothing. This is the "revert, confirm
// clean" half of #0267's mutation-proof criterion.
func TestMisattachedDocCommentGuardCleanOnceReattached(t *testing.T) {
	dir := t.TempDir()
	const src = `package fixture

// BuildAdminAlertEmail builds the operator-facing alert.
func BuildAdminAlertEmail(to string) string { return to }

// BuildWelcomeEmail builds the message sent once a subscriber confirms.
func BuildWelcomeEmail(to string) string { return to }
`
	writeFixtureGoFile(t, dir, "fixture.go", src)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(dir, "fixture.go"), nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	found := findMisattachedDocsInFile(fset, file)
	if len(found) != 0 {
		t.Errorf("expected no hits once each doc comment names its own function, got %+v", found)
	}
}

// TestMisattachedDocCommentGuardSkipsMultiMemberGroupLevelDoc pins the
// grouped-declaration exclusion this file's header documents against the
// real shape that earned it: internal/mailing/campaigns.go's actual
// "Campaign status constants, verbatim from migration 000017's status
// CHECK." atop a multi-member const block whose members are
// CampaignStatus*, not Campaign — reproduced here with fictitious names so
// this proof doesn't depend on that file's wording staying put. Without the
// exclusion this would flag "Widget" as naming a different declaration
// (the type), even though the doc is legitimately describing the block as
// a whole.
func TestMisattachedDocCommentGuardSkipsMultiMemberGroupLevelDoc(t *testing.T) {
	dir := t.TempDir()
	const src = `package fixture

type Widget string

// Widget values recognized by the catalog.
const (
	WidgetOne Widget = "one"
	WidgetTwo Widget = "two"
)
`
	writeFixtureGoFile(t, dir, "fixture.go", src)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(dir, "fixture.go"), nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	found := findMisattachedDocsInFile(fset, file)
	if len(found) != 0 {
		t.Errorf("expected no hits — a two-member block's own group-level doc is not checked, got %+v", found)
	}
}

// TestMisattachedDocCommentGuardStillChecksPerMemberDocInsideAGroup proves
// the multi-member exclusion above does not also blind the guard to a
// misattachment WITHIN a group: a comment written directly above one
// member of a const block, naming a DIFFERENT member of that same block, is
// exactly as real a #0267 instance as the top-level FuncDecl case — no
// grouped block anywhere in this tree's real history needed this
// exclusion, and this pins that the exclusion doesn't accidentally cover
// it either.
func TestMisattachedDocCommentGuardStillChecksPerMemberDocInsideAGroup(t *testing.T) {
	dir := t.TempDir()
	const src = `package fixture

const (
	// Bar explains something that belongs to the other constant.
	Foo = 1
	Bar = 2
)
`
	writeFixtureGoFile(t, dir, "fixture.go", src)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(dir, "fixture.go"), nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	found := findMisattachedDocsInFile(fset, file)
	if len(found) != 1 {
		t.Fatalf("expected exactly one hit (Foo's own per-member doc naming sibling Bar), got %d: %+v", len(found), found)
	}
	if found[0].firstWord != "Bar" {
		t.Errorf("expected the hit to name Bar, got %q", found[0].firstWord)
	}
	if len(found[0].own) != 1 || found[0].own[0] != "Foo" {
		t.Errorf("expected the hit to report it's attached to Foo, got %v", found[0].own)
	}
}

// TestMisattachedDocCommentGuardIgnoresOrdinaryProseOpeners proves the
// guard's deliberate scope limit: a doc comment that opens with an ordinary
// English word, or a name that isn't declared ANYWHERE in the file, is left
// alone — this guard can only resolve a citation against this file's own
// ground truth, and has no business guessing at prose it can't check.
func TestMisattachedDocCommentGuardIgnoresOrdinaryProseOpeners(t *testing.T) {
	dir := t.TempDir()
	const src = `package fixture

// This helper normalizes the input before handing it to Second.
func First(s string) string { return s }

// Unrelated is a name this file never declares anywhere.
func Second(s string) string { return s }
`
	writeFixtureGoFile(t, dir, "fixture.go", src)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(dir, "fixture.go"), nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	found := findMisattachedDocsInFile(fset, file)
	if len(found) != 0 {
		t.Errorf("expected no hits — neither opener names a symbol this file actually declares elsewhere, got %+v", found)
	}
}

// writeFixtureGoFile is a small shared helper for this file's fixture-based
// tests above: it writes src to name under dir, t.Fatal-ing on any error.
func writeFixtureGoFile(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}
