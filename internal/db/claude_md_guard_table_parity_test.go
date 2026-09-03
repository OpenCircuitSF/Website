package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// #0387: CLAUDE.md §5 carries a table naming the packages that own
// repo-wide guards — tests whose scan roots reach outside their own package
// directory, which a scoped `ISSUE=NNNN scripts/check.sh go
// ./internal/x/...` run cannot see. #0381 built the table after a stale
// scoped run left HEAD red for eighteen minutes: a guard's scan root
// changed, its owning package never ran, and the run reported clean. That
// table itself was unguarded, which is what #0387 is filed against — the
// same silent-drift shape §11's TestPRDSectionIndexParity exists to catch
// for the PRD section index, in the same file.
//
// #0387's plan considered a *full* parity guard — table row, scan roots,
// and all — and declined it. Half the table (the "Reaches into" column) is
// prose written for a human router, e.g. "the whole repo tree" or "every
// `internal/*` package `servePostgres` imports, parsed for `*seo.Site`
// flow" — turning that into something machine-comparable against source
// would mean rewriting it into a path list, discarding the routing guidance
// three review rounds of #0381 tuned. So **this guard checks only the
// table's package column — which packages are listed at all — and leaves
// the "Reaches into" prose, and the routing sentence beneath the table,
// entirely unguarded.** CLAUDE.md §5 itself now says so, in the sentence
// immediately following the table. That asymmetry is deliberate: a stale
// "Reaches into" cell misdescribes *why* a package is listed, but a
// *missing row* is what makes an agent skip a package and conclude clean —
// the #0381 failure. Only the row set carries that failure mode, and only
// the row set is cheap to check.
//
// Two sides, neither a copy of the other (§8's "oracle must not be the same
// bytes as its subject"): the table side reads CLAUDE.md's live markdown;
// the source side parses the live Go AST of every _test.go file under
// ../../internal and ../../cmd. Markdown prose in one file, Go source in
// another, different languages — no single edit satisfies both, unlike a
// quoted-heredoc oracle sitting next to the code it grades. The source side
// is deliberately AST-based rather than regex-based: this doc comment
// itself quotes example paths like "../../PRD.md" to explain the design,
// and a regex oracle scanning raw text would wrongly count those as
// escaping literals in this very file's comments (#0258's self-measurement
// trap, in a new costume — a regex cannot tell a comment from code, an AST
// walk can).
//
// The source side needs two independent detectors, not one:
//
//  1. A STRING literal containing a ".." path segment — the ordinary case
//     (internal/handlers, internal/db, internal/outbox, internal/seo,
//     cmd/opencircuit, internal/mailing all reach out this way).
//  2. A call to runtime.Caller or os.Getwd — internal/subscribers reaches
//     out ONLY this way. Its guard (events_append_only_guard_test.go)
//     resolves the repo root via os.Getwd plus a fixed two-level
//     filepath.Dir walk, with no ".." string literal anywhere in the file.
//     A literal-only scan finds six packages and fails against a table
//     that is, today, correct at seven — this was the first trap #0387's
//     planning pass recorded for whoever implemented it.
//
// The second trap: a scan rooted at the repo root walks into
// .claude/worktrees/ — a full second checkout of this tree that a live git
// worktree leaves on disk, gitignored but present, which would double-count
// every reaching package. The scan below is rooted at ../../internal and
// ../../cmd specifically, never at the repo root, which sidesteps that
// entirely rather than filtering the worktree path out after the fact.
//
// What this guard does NOT establish, stated plainly so a verification
// claim never overstates it: it does not check that a table row's
// "Reaches into" prose still matches what its guard scans (that column is
// hand-maintained, per CLAUDE.md §5's own text), and it does not detect a
// *narrowed or widened* scan root that leaves a package's row present but
// stale. Only "is this package's row present, and does source evidence for
// it still exist" is checked.
const claudeMDGuardTableHeader = "| Package | Reaches into |"

// claudeMDGuardTableRowPattern extracts a backtick-quoted identifier from a
// §5 table row's first cell, e.g. "| `internal/handlers` | ... |" ->
// "internal/handlers".
var claudeMDGuardTableRowPattern = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|")

// claudeMDGuardTableSeparatorPattern matches a markdown table's separator
// row, e.g. "|---|---|", so parseClaudeMDGuardTable can skip it without
// mistaking it for a malformed data row.
var claudeMDGuardTableSeparatorPattern = regexp.MustCompile(`^\|[-\s|]+\|$`)

// parseClaudeMDGuardTable extracts the set of package identifiers named in
// CLAUDE.md §5's repo-wide-guard table. Two structural checks, both from
// §8: the header must appear exactly once (a second copy could let a decoy
// table shadow the real one — GUARD-0208's failure shape), and the parse
// must yield at least one row (an empty extraction is an error, never an
// input — the #0258 fail-open).
func parseClaudeMDGuardTable(t *testing.T, claudeMD string) map[string]bool {
	t.Helper()

	if n := strings.Count(claudeMD, claudeMDGuardTableHeader); n != 1 {
		t.Fatalf("CLAUDE.md: expected the §5 guard-table header %q to appear exactly once, found %d — a second copy could let a decoy table shadow the real one", claudeMDGuardTableHeader, n)
	}

	lines := strings.Split(claudeMD, "\n")
	headerIdx := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == claudeMDGuardTableHeader {
			headerIdx = i
			break
		}
	}
	if headerIdx == -1 {
		t.Fatalf("CLAUDE.md: header text %q was found by strings.Count but not as its own trimmed line — has the table's markdown shape changed?", claudeMDGuardTableHeader)
	}

	pkgs := map[string]bool{}
	for i := headerIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "|") {
			break // blank line (or prose) ends the table
		}
		if claudeMDGuardTableSeparatorPattern.MatchString(trimmed) {
			continue
		}
		m := claudeMDGuardTableRowPattern.FindStringSubmatch(trimmed)
		if m == nil {
			t.Fatalf("CLAUDE.md:%d: a line inside the §5 guard table does not match the expected `| `package` | ... |` row shape: %q", i+1, trimmed)
		}
		pkgs[m[1]] = true
	}

	if len(pkgs) == 0 {
		t.Fatalf("CLAUDE.md: found the §5 guard-table header but parsed zero data rows — has the table's shape changed?")
	}
	return pkgs
}

// repoWideGuardScanRoots is where the source-side scan looks for _test.go
// files that might reach outside their own package directory. Deliberately
// ../../internal and ../../cmd, never the repo root — see this file's
// package doc comment for why a repo-root scan double-counts under a live
// git worktree.
var repoWideGuardScanRoots = []string{"../../internal", "../../cmd"}

// Measured at HEAD when this guard was written (#0387): 157 _test.go files
// across 17 test-holding directories under internal/ and cmd/. Both floors
// sit comfortably below that and are meant to stay there — the repo's test
// suite only grows, so a scan that visits fewer than either floor has had
// its roots emptied or narrowed, not merely started smaller than today.
const (
	repoWideGuardScanFileFloor = 100
	repoWideGuardScanDirFloor  = 12
)

// repoWideGuardScanResult is what scanRepoWideGuardSourcePackages found.
type repoWideGuardScanResult struct {
	// packages holds one entry per test-holding directory, relative to the
	// repo root with forward slashes (e.g. "internal/handlers"), whose
	// _test.go files contain either an escaping string literal or the
	// runtime.Caller/os.Getwd repo-root idiom.
	packages map[string]bool
	// filesVisited and dirsVisited are the raw walk counts, checked against
	// the floors above before packages is trusted for anything (#0275's
	// shape: a scan that silently walks nothing must fail closed, not
	// report an empty-but-plausible-looking result).
	filesVisited int
	dirsVisited  int
}

// literalEscapesPackageDir reports whether a Go string literal's *value*
// (raw is the literal's source text, quotes included, exactly as
// go/ast.BasicLit.Value carries it for either an interpreted "..." string
// or a raw `...` string — strconv.Unquote handles both) contains a genuine
// ".." path segment. This is a segment match, not a substring match: a
// literal like "..." (three dots, seen in this package's own test data as
// an ellipsis) has one path segment, "...", which is not ".." — a
// substring check would wrongly flag it, a segment check does not.
func literalEscapesPackageDir(raw string) bool {
	value, err := strconv.Unquote(raw)
	if err != nil {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(value), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// isRepoRootIdiomCall reports whether call is `runtime.Caller(...)` or
// `os.Getwd()` — the two calls internal/subscribers' guard uses (in
// combination with a fixed filepath.Dir walk) to resolve the repository
// root without ever writing a ".." string literal. Any use of either call
// in a _test.go file under the scan roots is treated as a reach signal;
// measured across the whole tree while writing this guard, only the seven
// table packages' _test.go files call either one at all, so this is not
// currently a source of false positives — see the package doc comment.
func isRepoRootIdiomCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	switch {
	case pkgIdent.Name == "runtime" && sel.Sel.Name == "Caller":
		return true
	case pkgIdent.Name == "os" && sel.Sel.Name == "Getwd":
		return true
	}
	return false
}

// scanRepoWideGuardSourcePackages walks repoWideGuardScanRoots for every
// directory holding at least one _test.go file, parses each such file with
// go/parser, and records which directories show either reach signal.
func scanRepoWideGuardSourcePackages(t *testing.T) repoWideGuardScanResult {
	t.Helper()

	dirsWithTests := map[string]bool{}
	for _, root := range repoWideGuardScanRoots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "dist" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(d.Name(), "_test.go") {
				dirsWithTests[filepath.Dir(path)] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	result := repoWideGuardScanResult{packages: map[string]bool{}}

	var dirs []string
	for d := range dirsWithTests {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		result.dirsVisited++

		relDir, err := filepath.Rel("../..", dir)
		if err != nil {
			t.Fatalf("relativizing %s against ../..: %v", dir, err)
		}
		relSlash := filepath.ToSlash(relDir)

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading dir %s: %v", dir, err)
		}

		reached := false
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			result.filesVisited++

			path := filepath.Join(dir, e.Name())
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}

			ast.Inspect(f, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.BasicLit:
					if v.Kind == token.STRING && literalEscapesPackageDir(v.Value) {
						reached = true
					}
				case *ast.CallExpr:
					if isRepoRootIdiomCall(v) {
						reached = true
					}
				}
				return true
			})
		}

		if reached {
			result.packages[relSlash] = true
		}
	}

	return result
}

// assertRepoWideGuardScanPlausible is this guard's #0275-shaped floor
// check: a scan whose roots were silently emptied or narrowed must fail
// closed rather than report an innocuous-looking empty (or small) result.
func assertRepoWideGuardScanPlausible(t *testing.T, result repoWideGuardScanResult) {
	t.Helper()
	if result.filesVisited < repoWideGuardScanFileFloor {
		t.Fatalf("the source-side scan of %v visited only %d _test.go file(s) — expected at least %d; the scan roots may have been emptied or narrowed, which would silently disarm this guard rather than fail it", repoWideGuardScanRoots, result.filesVisited, repoWideGuardScanFileFloor)
	}
	if result.dirsVisited < repoWideGuardScanDirFloor {
		t.Fatalf("the source-side scan of %v visited only %d test-holding director(y/ies) — expected at least %d", repoWideGuardScanRoots, result.dirsVisited, repoWideGuardScanDirFloor)
	}
}

// TestClaudeMDRepoWideGuardPackageSetParity is #0387's guard: the set of
// packages named in CLAUDE.md §5's repo-wide-guard table must match the set
// this scan finds actually reaching outside their own package directory.
// Neither direction of mismatch is silently ignored — a package reaching
// out with no row would leave an agent's scoped verification unable to see
// it (the #0381 failure this table exists to prevent), and a row with no
// remaining source evidence means the reach it once described may have
// been removed and the row is now misleading.
func TestClaudeMDRepoWideGuardPackageSetParity(t *testing.T) {
	claudeBytes, err := os.ReadFile(claudeMDPath)
	if err != nil {
		t.Fatalf("reading %s: %v", claudeMDPath, err)
	}
	tablePkgs := parseClaudeMDGuardTable(t, string(claudeBytes))

	result := scanRepoWideGuardSourcePackages(t)
	assertRepoWideGuardScanPlausible(t, result)

	var missingFromTable, missingFromSource []string
	for pkg := range result.packages {
		if !tablePkgs[pkg] {
			missingFromTable = append(missingFromTable, pkg)
		}
	}
	for pkg := range tablePkgs {
		if !result.packages[pkg] {
			missingFromSource = append(missingFromSource, pkg)
		}
	}
	sort.Strings(missingFromTable)
	sort.Strings(missingFromSource)

	for _, pkg := range missingFromTable {
		t.Errorf("%s has a _test.go file with an escaping string literal or the runtime.Caller/os.Getwd repo-root idiom, but CLAUDE.md's §5 repo-wide-guard table has no row for it — add a row naming what it reaches", pkg)
	}
	for _, pkg := range missingFromSource {
		t.Errorf("CLAUDE.md's §5 repo-wide-guard table has a row for %s, but this scan found no escaping literal or repo-root idiom left in its _test.go files — the reach that row described may have been removed; drop the row, or verify by hand if this is a detector gap", pkg)
	}
}
