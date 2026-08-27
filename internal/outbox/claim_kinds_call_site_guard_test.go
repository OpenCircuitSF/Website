// claim_kinds_call_site_guard_test.go is #0281's guard: nothing else in
// the compiler or the type system stops a caller from invoking
// Store.ClaimDue or Store.OrphanSweep with no kinds at all, and both
// implement an omitted kinds argument as "cardinality($n) = 0 OR kind =
// ANY(...)" — i.e. EVERY kind, not none. That is exactly the omission
// #0254 bounced on: the intake sweep's OrphanSweep call started out
// unscoped, and its 20s sweep released live mail claims held by
// OutboxWorker, producing a duplicate confirmation email mid-send. #0254's
// fix scoped both live call sites, and its reviewer then enumerated every
// call site by hand and confirmed none of the remaining ones were
// dangerous — but that enumeration was a snapshot, not a standing
// guarantee, and nothing stops the NEXT call site from forgetting. This
// file is that standing guarantee.
//
// # Why internal/outbox and not internal/handlers
//
// Store.ClaimDue and Store.OrphanSweep are this package's own methods, and
// the unscoped default is legitimate and load-bearing WITHIN this
// package's own tests (store_test.go exercises it deliberately, see e.g.
// TestOutbox_ClaimDue_OnlyClaimsQueuedAndDue). The danger is specifically a
// caller OUTSIDE this package forgetting to scope a call — so the guard
// belongs where the methods are declared, scoping the RULE by caller
// package rather than forbidding the variadic signature outright
// (acceptance criterion 1).
package outbox

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// claimKindsGuardScanRoots covers every package that can call this
// package's exported Store methods: ".." is internal/ (every sibling
// package, including this one — outboxSelfDir below excludes it from
// evaluation, not from the walk, so the walk itself stays a real,
// hard-to-narrow-unnoticed population), "../../cmd" is the binary
// entrypoint. web/ is TypeScript, not Go, and is not a caller of a Go
// method, so it is not a root here (unlike the citation guard family in
// internal/handlers, which scans web/ for a different reason).
var claimKindsGuardScanRoots = []string{"..", "../../cmd"}

// claimKindsGuardMinPlausibleCallSiteCount is #0281 criterion 4 (see
// #0275, which this mirrors at the granularity of "call sites found"
// rather than "files visited" — the more direct measurement for THIS
// guard, since a guard whose subject is "did we find any ClaimDue/
// OrphanSweep calls" is not protected by a files-visited floor alone: the
// walk could visit hundreds of files and still find zero matches if the
// method-name check itself broke).
//
// Measured directly, not fitted (a temporary t.Logf counted allSites
// before this comment was written, then was removed): a full scan over
// claimKindsGuardScanRoots today finds 32 call sites named ClaimDue or
// OrphanSweep in total — roughly 20 inside internal/outbox's own
// store_test.go (deliberately exercising the unscoped default, excluded
// from the VIOLATION check by inOwnPkg but still counted here, since they
// are real evidence the scan reached this package) plus 12 outside it
// (this package's own methods called from internal/handlers and
// internal/mailing, and mailing.SendStore's unrelated same-named
// OrphanSweep — see nameMatchesGuardedMethod's doc comment for why that
// coincidence does not need resolving). 5 sits comfortably below that and
// well above what a narrowing to any single file could produce (at most 3,
// in internal/handlers/subscribe_intake.go or
// internal/mailing/outbox_worker.go) — so a scan-roots regression that
// prunes an entire package, not just one file, is what it takes to stay
// above this floor while still being wrong.
const claimKindsGuardMinPlausibleCallSiteCount = 5

// outboxCallSite is one ClaimDue or OrphanSweep call site found by the
// scan: its location, the method named, and how many arguments it passed
// (the signal for whether kinds was omitted).
type outboxCallSite struct {
	file     string
	line     int
	method   string
	argc     int
	inOwnPkg bool
}

// unscoped reports whether this call site omitted kinds entirely. Both
// ClaimDue(ctx, limit int, kinds ...Kind) and OrphanSweep(ctx, staleAfter,
// kinds ...Kind) take exactly two required arguments before the variadic
// tail, so a two-argument call passed no kinds at all — the dangerous
// "every kind" default. Three or more arguments means at least one Kind
// was passed (individually or via a `kinds...` spread — both are exactly
// one *ast.Expr per element syntactically, indistinguishable from a
// non-spread argument by count alone, and both are the safe case this
// guard has no reason to distinguish further).
func (c outboxCallSite) unscoped() bool { return c.argc == 2 }

// nameMatchesGuardedMethod reports whether name is one this guard cares
// about. internal/mailing.SendStore ALSO declares a method named
// OrphanSweep (worker_store.go) with a completely different signature —
// OrphanSweep(ctx, campaignID int64, staleAfter time.Duration), no
// variadic kinds parameter at all, so a call to it always has exactly
// three required arguments and can never satisfy unscoped() (argc==2).
// This guard does not need to resolve which OrphanSweep a given call
// targets (that would need go/types and a full import graph — rejected
// for the same reason outbox_worker_kinds_guard_test.go's own doc comment
// gives for a sibling guard: no marginal precision for real cost) because
// the ONLY method name collision in this codebase happens to be safe by
// construction: SendStore.OrphanSweep is never callable with kinds
// omitted, since it has no kinds parameter to omit. Verified directly, not
// assumed: grepped the whole tree for ".ClaimDue(" and ".OrphanSweep(" —
// worker.go:543 and worker_store_test.go's three OrphanSweep calls are the
// only non-outbox occurrences with argc==3, and none has argc==2.
func nameMatchesGuardedMethod(name string) bool {
	return name == "ClaimDue" || name == "OrphanSweep"
}

// walkClaimKindsGuardFiles calls fn with the path of every .go file
// (test files included — a test file is just as much an "outside the
// package" caller as production code, and #0281's own investigation found
// real unscoped calls living in exactly that shape, in
// internal/handlers/admin_dashboard_test.go and
// internal/handlers/admin_pending_test.go, both fixed alongside this
// guard) under each of roots, recursively, pruning vendored/generated
// directories the same way internal/handlers' citation guard family does.
// Returns the number of files visited.
func walkClaimKindsGuardFiles(t *testing.T, roots []string, fn func(path string)) int {
	t.Helper()
	visited := 0
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "node_modules" || entry.Name() == "dist" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".go") {
				return nil
			}
			visited++
			fn(path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return visited
}

// findOutboxCallSitesInFile parses one file and returns every ClaimDue/
// OrphanSweep call site it contains, tagged with whether the file lives in
// this package's own directory (selfDir). This is the pure, directly
// testable core (CLAUDE.md §8: a guard's oracle must not be the same bytes
// as its subject — see TestClaimKindsGuardFiresOnFixtureWithNoKinds below,
// which calls this exact function against an in-memory fixture rather than
// against real repo files, so the guard's own detection logic is what gets
// proved, independent of anything currently in the tree).
func findOutboxCallSitesInFile(fset *token.FileSet, path string, src any, selfDir string) ([]outboxCallSite, error) {
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	fileDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("resolving directory of %s: %w", path, err)
	}
	inOwnPkg := fileDir == selfDir

	var sites []outboxCallSite
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !nameMatchesGuardedMethod(sel.Sel.Name) {
			return true
		}
		pos := fset.Position(call.Pos())
		sites = append(sites, outboxCallSite{
			file:     pos.Filename,
			line:     pos.Line,
			method:   sel.Sel.Name,
			argc:     len(call.Args),
			inOwnPkg: inOwnPkg,
		})
		return true
	})
	return sites, nil
}

// TestNoUnscopedOutboxClaimCallOutsidePackage is #0281's guard proper: a
// ClaimDue or OrphanSweep call OUTSIDE internal/outbox that passes no
// kinds is exactly #0254's failure shape waiting to happen again — every
// kind, claimed or swept, when the caller almost certainly meant one.
//
// Reproduced as a real (not hypothetical) finding while building this
// guard: internal/handlers/admin_dashboard_test.go (two call sites),
// internal/handlers/admin_pending_test.go, and
// internal/mailing/outbox_worker_test.go all called ClaimDue unscoped from
// OUTSIDE internal/outbox — #0254's reviewer's enumeration covered
// production call sites, not every test file, and these four were the gap.
// None was live-traffic dangerous (each test enqueues exactly one kind, so
// the unscoped claim happened to behave like a scoped one), but each was
// exactly the pattern #0254 warns about: it works today because nothing
// else is queued, and silently stops working the moment that stops being
// true. All four now pass their kind explicitly, fixed alongside this
// guard landing, not left for a future bounce.
func TestNoUnscopedOutboxClaimCallOutsidePackage(t *testing.T) {
	if len(claimKindsGuardScanRoots) == 0 {
		t.Fatal("claimKindsGuardScanRoots is empty — this guard would silently check nothing (#0275)")
	}

	selfDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving internal/outbox's own directory: %v", err)
	}

	fset := token.NewFileSet()
	var allSites []outboxCallSite
	visited := walkClaimKindsGuardFiles(t, claimKindsGuardScanRoots, func(path string) {
		sites, err := findOutboxCallSitesInFile(fset, path, nil, selfDir)
		if err != nil {
			t.Fatalf("%v", err)
		}
		allSites = append(allSites, sites...)
	})
	if visited == 0 {
		t.Fatalf("walked zero .go files under %v — scan roots may have been narrowed to nothing (#0275)", claimKindsGuardScanRoots)
	}

	// Criterion 4: fail closed if the scan found no call sites at all —
	// otherwise a broken method-name check or an accidentally-narrowed
	// root set would report "no violations" for the wrong reason (nothing
	// was ever examined) and this guard would look green while checking
	// nothing, the exact #0275 failure mode.
	if len(allSites) < claimKindsGuardMinPlausibleCallSiteCount {
		t.Fatalf("found only %d ClaimDue/OrphanSweep call site(s) under %v, want at least %d — the scan roots may have been narrowed or the method-name check broken, which would silently disarm this guard rather than fail it (#0275)",
			len(allSites), claimKindsGuardScanRoots, claimKindsGuardMinPlausibleCallSiteCount)
	}

	var violations []string
	for _, site := range allSites {
		if site.inOwnPkg || !site.unscoped() {
			continue
		}
		violations = append(violations, fmt.Sprintf("%s:%d: %s called with no kinds — an empty kinds argument means EVERY kind (outbox.Store's cardinality($n)=0 OR kind=ANY(...) filter), not none; this is #0254's failure mode (an unscoped OrphanSweep released a live mail claim mid-send, causing a duplicate confirmation email) reached from a caller outside internal/outbox",
			site.file, site.line, site.method))
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("found %d unscoped outbox claim call(s) outside internal/outbox:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

// TestClaimKindsGuardFiresOnFixtureWithNoKinds is #0281 criterion 3: a
// fixture call site with no kinds must fail the guard. This calls
// findOutboxCallSitesInFile directly against an in-memory synthetic source
// string — never written into any directory the real guard scans — so it
// proves the DETECTION LOGIC fires on the dangerous shape, independent of
// anything currently true about the real tree (CLAUDE.md §8: an oracle
// must not be the same bytes as its subject — this fixture is hand-written
// prose describing the dangerous call, not a copy of any of the four real
// violations this issue found and fixed).
func TestClaimKindsGuardFiresOnFixtureWithNoKinds(t *testing.T) {
	const fixtureSrc = `package fixture

import "context"

func sweepEverything(ctx context.Context, s *Store) {
	// This call passes no kinds at all — the dangerous default.
	_, _ = s.OrphanSweep(ctx, 0)
}
`
	fset := token.NewFileSet()
	// selfDir deliberately does NOT match the fixture's synthetic path, so
	// the fixture is evaluated as an "outside internal/outbox" caller —
	// exactly the case this guard exists to catch.
	sites, err := findOutboxCallSitesInFile(fset, "fixture_outside_pkg.go", fixtureSrc, "/nonexistent/self-dir")
	if err != nil {
		t.Fatalf("findOutboxCallSitesInFile: %v", err)
	}
	var found bool
	for _, site := range sites {
		if site.method == "OrphanSweep" && site.unscoped() && !site.inOwnPkg {
			found = true
		}
	}
	if !found {
		t.Fatalf("findOutboxCallSitesInFile did not flag the fixture's unscoped OrphanSweep(ctx, 0) call as an outside-package violation: %+v", sites)
	}

	// Companion check: a SCOPED call in the same shape must NOT be
	// flagged — otherwise this guard would fail every legitimate call
	// site in the codebase, not just the dangerous one.
	const scopedFixtureSrc = `package fixture

import "context"

func sweepOneKind(ctx context.Context, s *Store) {
	_, _ = s.OrphanSweep(ctx, 0, KindConfirmation)
}
`
	scopedSites, err := findOutboxCallSitesInFile(fset, "fixture_scoped.go", scopedFixtureSrc, "/nonexistent/self-dir")
	if err != nil {
		t.Fatalf("findOutboxCallSitesInFile (scoped fixture): %v", err)
	}
	for _, site := range scopedSites {
		if site.unscoped() {
			t.Fatalf("scoped fixture call OrphanSweep(ctx, 0, KindConfirmation) was flagged as unscoped: %+v", site)
		}
	}

	// And a call INSIDE this package's own directory must never be
	// flagged, scoped or not (criterion 1: the package's own tests must
	// still be able to exercise the unscoped default). "fixture_own_pkg.go"
	// is a bare relative filename, so findOutboxCallSitesInFile resolves
	// its directory to the current working directory (this test binary
	// runs from internal/outbox) — passing that SAME resolved directory as
	// selfDir is what makes inOwnPkg true, exactly mirroring how the real
	// guard computes selfDir via filepath.Abs(".").
	ownDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving cwd: %v", err)
	}
	ownPkgSites, err := findOutboxCallSitesInFile(fset, "fixture_own_pkg.go", fixtureSrc, ownDir)
	if err != nil {
		t.Fatalf("findOutboxCallSitesInFile (own-package fixture): %v", err)
	}
	for _, site := range ownPkgSites {
		if !site.inOwnPkg {
			t.Fatalf("fixture parsed with selfDir matching its own directory was not marked inOwnPkg: %+v", site)
		}
	}
}
