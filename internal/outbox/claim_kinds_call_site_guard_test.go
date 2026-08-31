// claim_kinds_call_site_guard_test.go is #0281's guard, structurally
// updated by #0303: nothing else in the compiler or the type system stops
// a caller from invoking Store.ClaimDue, Store.OrphanSweep, or
// Store.SelectDue with kinds that reach "no kinds" — and every one of
// them implements that as "cardinality($n) = 0 OR kind = ANY(...)" — i.e.
// EVERY kind, not none. That is exactly the omission #0254 bounced on:
// the intake sweep's OrphanSweep call started out unscoped, and its 20s
// sweep released live mail claims held by OutboxWorker, producing a
// duplicate confirmation email mid-send. #0254's fix scoped both live
// call sites, and its reviewer then enumerated every call site by hand
// and confirmed none of the remaining ones were dangerous — but that
// enumeration was a snapshot, not a standing guarantee, and nothing
// stopped the NEXT call site from forgetting. This file is that standing
// guarantee.
//
// #0303 changed WHAT counts as "no kinds": kinds is now a required
// parameter (a plain []Kind, not variadic), so omitting the argument
// entirely — #0254's original defect — is a compile error, not merely a
// guard failure discovered later. Sweeping every kind is still possible
// and still legitimate (this package's own tests exercise it, and it
// remains the correct choice for some callers) but now has to be spelled
// AllKinds (internal/outbox/store.go) — a named, deliberate act. This
// file's job shifted accordingly: instead of watching for an omitted
// argument, it watches for a kinds argument that reaches the same
// dangerous default WITHOUT naming AllKinds — nil, or an empty slice
// literal — a signature change and a guard are complementary, not
// redundant (#0303 criterion 3): the signature stops the omission, this
// guard stops a future overload, wrapper, or plain nil/empty-literal
// call site from reintroducing it.
//
// # Why internal/outbox and not internal/handlers
//
// Store.ClaimDue, Store.OrphanSweep, and Store.SelectDue are this
// package's own methods, and the unscoped (AllKinds) default is
// legitimate and load-bearing WITHIN this package's own tests
// (store_test.go exercises it deliberately, see e.g.
// TestOutbox_ClaimDue_OnlyClaimsQueuedAndDue). The danger is specifically
// a caller OUTSIDE this package reaching that default without naming it —
// so the guard belongs where the methods are declared, scoping the RULE
// by caller package rather than forbidding the sentinel's use outright
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
// OrphanSweep/SelectDue calls" is not protected by a files-visited floor
// alone: the walk could visit hundreds of files and still find zero
// matches if the method-name check itself broke).
//
// Measured directly, not fitted (a temporary t.Logf counted allSites
// before this comment was written, then was removed): a full scan over
// claimKindsGuardScanRoots today finds 36 call sites named ClaimDue,
// OrphanSweep, or SelectDue in total (#0303 re-measured after adding
// SelectDue to nameMatchesGuardedMethod and adding
// internal/outbox/select_due_claim_row_test.go, both landing after #0304
// measured 32) — 24 inside internal/outbox's own tests (store_test.go
// and select_due_claim_row_test.go, deliberately exercising the unscoped
// AllKinds default, excluded from the VIOLATION check by inOwnPkg but
// still counted here, since they are real evidence the scan reached this
// package) plus 12 outside it (this package's own methods called from
// internal/handlers and internal/mailing, and mailing.SendStore's
// unrelated same-named OrphanSweep — see nameMatchesGuardedMethod's doc
// comment for why that coincidence does not need resolving). 5 sits
// comfortably below that and well above what a narrowing to any single
// file could produce (at most 3, in internal/mailing/worker_store_test.go
// — its three mailing.SendStore.OrphanSweep call sites, name-matched the
// same as this package's own; internal/handlers/subscribe_intake.go and
// internal/mailing/outbox_worker.go each hold 2, one OrphanSweep call
// plus one SelectDue call) — so a scan-roots regression that prunes an
// entire package, not just one file, is what it takes to stay above this
// floor while still being wrong.
//
// #0304: THIS FLOOR ONLY PROVES THE WALK REACHED *A* TREE, NOT THE RIGHT
// ONE. 24 of the 36 sites it counts sit inside internal/outbox itself,
// where every call is exempt from the VIOLATION check (inOwnPkg). A
// narrowing of claimKindsGuardScanRoots to []string{"."} leaves this floor
// passing (well above 5, every site inside internal/outbox) while the walk
// never reaches a single caller outside the package — zero of the
// population this guard exists to check
// (TestNonExemptFloorCatchesScanRootsNarrowedToSelf, below, proves this
// permanently). This floor still earns its place (a genuinely empty or
// broken walk trips it), but it is not sufficient alone; see
// claimKindsGuardMinPlausibleNonExemptCallSiteCount below, which is.
//
// #0323: this Go/AST count (36) is NOT the number
// scripts/go_file_visit_floor_guard_test.sh's external oracle measures for
// the identical roots — that harness counts textually with grep rather
// than parsing Go, and measures 42 (#0303 re-measured; #0323 measured 35
// against 32 before SelectDue joined both the guard and the harness's own
// pattern). This is expected and must stay this way (do not "fix" it by
// making the oracle parse Go — its independence from go/ast is the entire
// reason it can catch a regression in THIS file's own parsing logic;
// CLAUDE.md §8, an oracle must not share its method with its subject).
// The six extras all sit inside THIS file, inside internal/outbox, so
// they inflate only the exempt side — the NON-exempt count (12) agrees
// exactly between grep and go/ast, one for one, because none of the
// extras is a non-exempt site. All six are the textual-miscount class
// grep is prone to and go/ast correctly ignores: they are inside
// TestClaimKindsGuardFiresOnFixtureWithNoKinds's raw-string fixtures
// (#0303 added three new fixtures — nil, an empty slice literal, and the
// AllKinds sentinel, the last containing two occurrences — alongside the
// original two) — syntactically real-looking Go text living inside a Go
// string literal, which go/ast never parses as code (they are handed to
// findOutboxCallSitesInFile as an in-memory `src` argument, not
// discovered by walking the tree) and grep cannot tell apart from a real
// call. Deliberately not written as literal guarded-call-syntax prose in
// THIS paragraph (unlike an earlier version of this comment, and of
// nameMatchesGuardedMethod's) — doing so would make this paragraph a
// seventh divergent site of the exact class it describes. The error
// direction is inflation, which only loosens
// outbox_floor_plausible's floor<=population upper bound and never
// tightens it — nothing is under-protected by this gap. Also worth
// knowing generally: grep -c counts matching LINES, not occurrences, so it
// would under-count (not over-count) if two guarded calls ever shared one
// source line.
const claimKindsGuardMinPlausibleCallSiteCount = 5

// claimKindsGuardMinPlausibleNonExemptCallSiteCount is #0304's fix for the
// gap claimKindsGuardMinPlausibleCallSiteCount's own comment now documents:
// a floor on the TOTAL call-site count is satisfied by the exempt
// (in-package) population alone, so it cannot detect a scan-roots
// regression that keeps internal/outbox's own tree but drops every actual
// caller. This floor instead applies to the NON-exempt subset — sites
// where inOwnPkg is false, exactly the sites the VIOLATION loop below
// evaluates — so it can only be satisfied by the walk having actually
// reached code outside this package.
//
// Measured the same way as the floor above, from the same scan: 12 sites
// outside internal/outbox today, broken down by PACKAGE rather than just
// file, because #0322's review showed the file-level breakdown alone hides
// the narrowing that matters most. Re-measured for #0303 (which added
// SelectDue to the guarded set): the count per package is UNCHANGED — each
// of the two files that used to make one of its two outbox.Store calls
// via the now-renamed selection method still makes exactly two calls, one
// old-named and one new-named:
//
//	internal/handlers  5  (subscribe_intake.go x2 — one OrphanSweep, one
//	                       SelectDue, was one OrphanSweep + one ClaimDue
//	                       before #0303 — admin_dashboard_test.go x2,
//	                       admin_pending_test.go x1)
//	internal/mailing   7  (outbox_worker.go x2 — outbox.Store's, one
//	                       OrphanSweep + one SelectDue (was ClaimDue
//	                       before #0303), worker.go x1 and
//	                       worker_store_test.go x3 — FOUR sites total,
//	                       all mailing.SendStore's unrelated same-named
//	                       OrphanSweep, counted here because
//	                       nameMatchesGuardedMethod matches on name only,
//	                       same as the total floor above —,
//	                       outbox_worker_test.go x1 — outbox.Store's
//	                       ClaimDue, unchanged)
//
// #0304 first set this to 6 (>= population/2, mirroring #0300's file-count
// margin) and its own review then measured which scan-roots narrowings
// that actually catches:
//
//	narrowing                        non-exempt left   caught by floor 6?
//	{"."}                             0                 yes
//	cmd only                          0                 yes
//	internal/handlers only            5                 yes
//	worst single file                 3                 yes
//	internal/mailing only             7                 NO
//
// The one 6 misses is the one that matters most: internal/mailing/only
// drops internal/handlers/subscribe_intake.go — the production caller this
// guard exists for, since #0254's duplicate-send defect came from exactly
// that file's unscoped OrphanSweep call. #0322 raised the floor to 8
// (8 > 7, closing that gap; 8 <= 12, still below the real population) —
// deliberately, not as an arithmetic default, and re-checked against the
// same table: 8 catches every row above, including internal/mailing only
// (7 < 8 now fails, as it must).
//
// The #0275 failure mode weighed explicitly, not assumed away: raising a
// floor above its population makes the guard fail permanently even with
// nothing wrong (the reason #0300's file-count floors came down from 150 to
// 80 and from an earlier value to 150). At 8 against 12 there are 4 sites
// of headroom on paper — but FOUR of the 12, not three, are
// mailing.SendStore's unrelated same-named OrphanSweep: worker.go:575's
// call to it (through Worker.store, declared *SendStore) plus
// worker_store_test.go's three, all counted
// only because this guard matches by method name, not by declaring
// type (see nameMatchesGuardedMethod's doc comment, which already had
// this right). A rename or removal of that unrelated method —
// plausible, since it is a different signature on a different type
// that merely shares a name — would drop the population to 8, not 9,
// leaving ZERO sites of headroom, not one: the floor would sit exactly
// AT the population rather than one below it. Equivalently: 8 is
// exactly the count of genuine, non-collision outbox call sites
// outside this package today (internal/handlers' 5 plus
// internal/mailing's 3 real outbox.Store calls — outbox_worker.go's
// two and outbox_worker_test.go's one); this floor's entire margin is
// currently supplied by that name collision, not by real slack. That
// risk is accepted here, not hidden: it is smaller than the risk 8
// closes (a real, reachable narrowing silently passing), and a shrink
// is visible the moment it happens, since `go test` fails loudly
// rather than silently underprotecting.
//
// The growth direction is not loud the same way, and is worth
// recording for the same reason (measured in a throwaway worktree, not
// asserted): narrow claimKindsGuardScanRoots to internal/mailing alone
// and add one ordinary, correctly-scoped guarded-method call (ClaimDue,
// OrphanSweep, or SelectDue) inside internal/mailing — nothing wrong
// with the call itself — and
// internal/mailing's own non-exempt count reaches 8, so the
// mailing-only narrowing passes again with `go test` green, dropping
// subscribe_intake.go exactly as before with nothing to signal it. So
// 8 protects against the mailing-only narrowing only while
// internal/mailing holds exactly 7 non-exempt sites today; growing
// that package's own legitimate call count by one silently reopens the
// gap #0322 was filed to close. internal/mailing is the package most
// likely to gain outbox callers, since it already owns OutboxWorker.
// This is not a reason to reject 8 — it remains strictly better than
// 6, which fails today rather than only after a hypothetical future
// commit — but it sharpens criterion 4's point: the margin above is
// pinned to a distribution that can change in either direction without
// anything noticing on the growth side, only on the shrink side.
//
// A count-based floor cannot close this robustly in general, and that
// limit is inherent, not a defect in the number chosen: any single integer
// threshold is blind to WHICH sites it counted, so a narrowing that
// coincidentally leaves >= floor non-exempt sites — none of them
// subscribe_intake.go — would still pass. A stronger check exists and is
// deliberately NOT built here (scope of #0322 was the floor, not a rework
// of the guard's shape): assert that specific known production call sites
// — starting with internal/handlers/subscribe_intake.go's two — are
// present in the non-exempt population, which is a presence check rather
// than a count and so cannot be fooled by a narrowing that merely
// preserves enough OTHER sites. Left as a recommendation, not implemented,
// per #0322's own acceptance criteria.
//
// Unlike the floor above, THIS floor is what actually breaks under the
// {"."} narrowing #0304 measured: with claimKindsGuardScanRoots narrowed to
// the package's own directory, every site found is inOwnPkg, so the
// non-exempt count is 0 — well below 8 — and
// TestNoUnscopedOutboxClaimCallOutsidePackage now fails closed instead of
// reporting PASS with nothing evaluated. See
// TestNonExemptFloorCatchesScanRootsNarrowedToSelf below, which proves
// exactly that, permanently, against the real repo tree.
const claimKindsGuardMinPlausibleNonExemptCallSiteCount = 8

// kindsArgShape classifies the syntactic shape of a guarded call's kinds
// argument (#0303: ClaimDue/OrphanSweep/SelectDue's third, now-required
// parameter — call.Args[2]).
type kindsArgShape int

const (
	// kindsArgTooFewArgs means the call has fewer than three arguments —
	// impossible for real code once kinds became a required parameter
	// (that call site fails to compile, per #0303 criterion 5), but
	// checked anyway for robustness against a hand-written fixture and
	// treated the same as the old variadic shape's dangerous default.
	kindsArgTooFewArgs kindsArgShape = iota
	// kindsArgSentinel is the bare identifier AllKinds, or a qualified
	// outbox.AllKinds from outside the package — the sanctioned, deliberate
	// spelling of "every kind" (#0303).
	kindsArgSentinel
	// kindsArgUnnamedEmpty is `nil` or an empty slice composite literal
	// (`[]Kind{}` / `[]outbox.Kind{}`) — reaches the IDENTICAL "every
	// kind" SQL behavior as kindsArgSentinel (store.go's
	// `cardinality($n) = 0` clause treats them the same), but without
	// naming that as a deliberate act. This is the shape #0303 exists to
	// flag: the same dangerous default the old variadic shape defaulted
	// to, reached a different way now that omitting the argument entirely
	// is a compile error.
	kindsArgUnnamedEmpty
	// kindsArgOther is anything else: a named variable, a non-empty slice
	// literal, a function call, a spread of a named slice, or an argument
	// belonging to an unrelated same-named method (see
	// nameMatchesGuardedMethod's doc comment for the SendStore.OrphanSweep
	// collision this classification cannot mistake for a kinds argument,
	// because staleAfter's own expressions never happen to look like nil
	// or an empty composite literal). Presumed scoped — this guard cannot
	// prove a named variable is non-empty at compile time any more than
	// the pre-#0303 variadic-count check could prove one was, which is
	// the same "no marginal precision for real cost" limit
	// nameMatchesGuardedMethod's own doc comment accepts elsewhere in this
	// file.
	kindsArgOther
)

// classifyKindsArg inspects args[2] — ClaimDue/OrphanSweep/SelectDue's
// kinds parameter, post-#0303 — and returns its shape. Never resolves an
// identifier's VALUE (that would need go/types), only its SYNTAX: the
// bare token AllKinds/outbox.AllKinds is the only spelling this treats as
// deliberate.
func classifyKindsArg(args []ast.Expr) kindsArgShape {
	if len(args) < 3 {
		return kindsArgTooFewArgs
	}
	switch e := args[2].(type) {
	case *ast.Ident:
		switch e.Name {
		case "nil":
			return kindsArgUnnamedEmpty
		case "AllKinds":
			return kindsArgSentinel
		}
	case *ast.SelectorExpr:
		if e.Sel.Name == "AllKinds" {
			return kindsArgSentinel
		}
	case *ast.CompositeLit:
		if len(e.Elts) == 0 {
			return kindsArgUnnamedEmpty
		}
	}
	return kindsArgOther
}

// outboxCallSite is one ClaimDue, OrphanSweep, or SelectDue call site
// found by the scan: its location, the method named, how many arguments
// it passed, and the classified shape of its kinds argument.
type outboxCallSite struct {
	file     string
	line     int
	method   string
	argc     int
	kindsArg kindsArgShape
	inOwnPkg bool
}

// unscoped reports whether this call site's kinds argument reaches the
// dangerous "every kind" default WITHOUT naming that as deliberate
// (#0303) — either the argument is missing outright (kindsArgTooFewArgs,
// the pre-#0303 shape, now a compile error in real code) or it is present
// but spelled as nil / an empty slice literal instead of the AllKinds
// sentinel (kindsArgUnnamedEmpty). kindsArgSentinel (AllKinds, spelled
// out) and kindsArgOther (anything else — presumed to carry real Kind
// values) are both scoped, by construction and by the guard's own
// precision limits respectively — see kindsArgShape's own doc comment.
func (c outboxCallSite) unscoped() bool {
	return c.kindsArg == kindsArgTooFewArgs || c.kindsArg == kindsArgUnnamedEmpty
}

// nameMatchesGuardedMethod reports whether name is one this guard cares
// about: ClaimDue, OrphanSweep, or SelectDue (#0297 added SelectDue
// alongside the per-row claim path — see internal/outbox/store.go's own
// doc comment — and it carries the identical unscoped-default risk, so
// #0303 folded it into this guard rather than leaving it uncovered).
// internal/mailing.SendStore ALSO declares a method named OrphanSweep
// (worker_store.go) with a completely different signature —
// OrphanSweep(ctx, campaignID int64, staleAfter time.Duration), no kinds
// parameter of any kind — so a call to it always has exactly three
// required arguments, matching this guard's own new argument-count
// expectation, but its third argument is staleAfter (a time.Duration
// expression), never a value that happens to spell nil or AllKinds or an
// empty composite literal. This guard does not need to resolve which
// OrphanSweep a given call targets (that would need go/types and a full
// import graph — rejected for the same reason
// outbox_worker_kinds_guard_test.go's own doc comment gives for a sibling
// guard: no marginal precision for real cost) because the ONLY method
// name collision in this codebase happens to be safe by construction:
// every real SendStore.OrphanSweep call site in the tree passes a literal
// duration, a named duration variable, or a signed duration expression as
// its third argument — none of the AST shapes classifyKindsArg treats as
// "no kinds" — verified directly, not assumed, by reading all four real
// call sites (worker.go:625, worker_store_test.go's three) rather than by
// re-deriving the same reasoning the pre-#0303 argc-based version of this
// comment gave.
func nameMatchesGuardedMethod(name string) bool {
	return name == "ClaimDue" || name == "OrphanSweep" || name == "SelectDue"
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
			kindsArg: classifyKindsArg(call.Args),
			inOwnPkg: inOwnPkg,
		})
		return true
	})
	return sites, nil
}

// TestNoUnscopedOutboxClaimCallOutsidePackage is #0281's guard proper: a
// ClaimDue, OrphanSweep, or SelectDue call OUTSIDE internal/outbox that passes no
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
	// nothing, the exact #0275 failure mode. This floor alone proves only
	// that the walk reached SOME tree containing ClaimDue/OrphanSweep/SelectDue
	// calls — see the const's own doc comment and #0304 below for why
	// that is not the same as reaching the population this guard checks.
	if len(allSites) < claimKindsGuardMinPlausibleCallSiteCount {
		t.Fatalf("found only %d ClaimDue/OrphanSweep/SelectDue call site(s) under %v, want at least %d — the scan roots may have been narrowed or the method-name check broken, which would silently disarm this guard rather than fail it (#0275)",
			len(allSites), claimKindsGuardScanRoots, claimKindsGuardMinPlausibleCallSiteCount)
	}

	// #0304: a SECOND floor, on the non-exempt subset only (sites outside
	// internal/outbox — exactly what the VIOLATION loop below evaluates).
	// The floor above is satisfied by internal/outbox's own store_test.go
	// sites alone, so it cannot detect a scan-roots regression that keeps
	// this package's own tree but drops every actual caller — which is
	// exactly what narrowing claimKindsGuardScanRoots to []string{"."}
	// does (proven permanently in
	// TestNonExemptFloorCatchesScanRootsNarrowedToSelf, below). This one
	// can only be satisfied by the walk having actually left
	// internal/outbox.
	nonExemptCount := 0
	for _, site := range allSites {
		if !site.inOwnPkg {
			nonExemptCount++
		}
	}
	if nonExemptCount < claimKindsGuardMinPlausibleNonExemptCallSiteCount {
		t.Fatalf("found only %d ClaimDue/OrphanSweep/SelectDue call site(s) OUTSIDE internal/outbox under %v (of %d total, the rest exempt as internal/outbox's own), want at least %d — the scan roots may have been narrowed to exclude every real caller while still finding internal/outbox's own exempt sites, which would silently disarm this guard's VIOLATION check rather than fail it (#0304, the same population-mismatch shape #0275 closed for the sibling guards)",
			nonExemptCount, claimKindsGuardScanRoots, len(allSites), claimKindsGuardMinPlausibleNonExemptCallSiteCount)
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

// TestClaimKindsGuardFiresOnFixtureWithNoKinds is #0281 criterion 3 (kept
// current for #0303's shape change): a fixture call site with no kinds
// must fail the guard. This calls findOutboxCallSitesInFile directly
// against in-memory synthetic source strings — never written into any
// directory the real guard scans — so it proves the DETECTION LOGIC fires
// on each dangerous shape, independent of anything currently true about
// the real tree (CLAUDE.md §8: an oracle must not be the same bytes as
// its subject — these fixtures are hand-written prose describing the
// dangerous calls, not a copy of any real violation this or #0281 found
// and fixed).
//
// #0303 changed what "no kinds" looks like in real code: omitting the
// argument entirely is now a compile error (kindsArgTooFewArgs — kept as
// a fixture below for robustness, since a hand-written or generated
// source could still produce that shape even though nothing in this repo
// compiles it), and the LIVE risk shifted to a caller writing `nil` or an
// empty `[]Kind{}` literal instead of naming the AllKinds sentinel. Both
// new shapes get their own fixture here, alongside the original.
func TestClaimKindsGuardFiresOnFixtureWithNoKinds(t *testing.T) {
	const fixtureSrc = `package fixture

import "context"

func sweepEverything(ctx context.Context, s *Store) {
	// This call passes no kinds at all — the dangerous default, and
	// (post-#0303) not even valid Go against the real signature; kept as
	// a fixture for robustness against a hand-written source that still
	// produces this shape.
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

	// #0303: the two NEW dangerous shapes — nil and an empty slice
	// literal, spelled instead of the AllKinds sentinel — each real code
	// CAN produce (unlike the arity-based fixture above, which no longer
	// compiles).
	const nilFixtureSrc = `package fixture

import "context"

func sweepEverythingViaNil(ctx context.Context, s *Store) {
	_, _ = s.OrphanSweep(ctx, 0, nil)
}
`
	nilSites, err := findOutboxCallSitesInFile(fset, "fixture_nil.go", nilFixtureSrc, "/nonexistent/self-dir")
	if err != nil {
		t.Fatalf("findOutboxCallSitesInFile (nil fixture): %v", err)
	}
	found = false
	for _, site := range nilSites {
		if site.unscoped() && !site.inOwnPkg {
			found = true
		}
	}
	if !found {
		t.Fatalf("findOutboxCallSitesInFile did not flag the fixture's OrphanSweep(ctx, 0, nil) call as an outside-package violation: %+v", nilSites)
	}

	const emptyLiteralFixtureSrc = `package fixture

import "context"

func sweepEverythingViaEmptyLiteral(ctx context.Context, s *Store) {
	_, _ = s.OrphanSweep(ctx, 0, []Kind{})
}
`
	emptyLiteralSites, err := findOutboxCallSitesInFile(fset, "fixture_empty_literal.go", emptyLiteralFixtureSrc, "/nonexistent/self-dir")
	if err != nil {
		t.Fatalf("findOutboxCallSitesInFile (empty-literal fixture): %v", err)
	}
	found = false
	for _, site := range emptyLiteralSites {
		if site.unscoped() && !site.inOwnPkg {
			found = true
		}
	}
	if !found {
		t.Fatalf("findOutboxCallSitesInFile did not flag the fixture's OrphanSweep(ctx, 0, []Kind{}) call as an outside-package violation: %+v", emptyLiteralSites)
	}

	// The sanctioned spelling — AllKinds, named — must NOT be flagged,
	// even though it reaches the identical SQL behavior as the two shapes
	// above. This is the entire point of #0303: the safe case is the one
	// that requires spelling something out.
	const sentinelFixtureSrc = `package fixture

import "context"

func sweepEverythingDeliberately(ctx context.Context, s *Store) {
	_, _ = s.OrphanSweep(ctx, 0, AllKinds)
}

func sweepEverythingDeliberatelyQualified(ctx context.Context, s *outbox.Store) {
	_, _ = s.OrphanSweep(ctx, 0, outbox.AllKinds)
}
`
	sentinelSites, err := findOutboxCallSitesInFile(fset, "fixture_sentinel.go", sentinelFixtureSrc, "/nonexistent/self-dir")
	if err != nil {
		t.Fatalf("findOutboxCallSitesInFile (sentinel fixture): %v", err)
	}
	if len(sentinelSites) != 2 {
		t.Fatalf("findOutboxCallSitesInFile (sentinel fixture) found %d site(s), want 2", len(sentinelSites))
	}
	for _, site := range sentinelSites {
		if site.unscoped() {
			t.Fatalf("AllKinds-sentinel fixture call was flagged as unscoped: %+v", site)
		}
	}

	// Companion check: a SCOPED call — the realistic post-#0303 shape,
	// kinds wrapped in a slice literal — must NOT be flagged either,
	// otherwise this guard would fail every legitimate call site in the
	// codebase, not just the dangerous ones.
	const scopedFixtureSrc = `package fixture

import "context"

func sweepOneKind(ctx context.Context, s *Store) {
	_, _ = s.OrphanSweep(ctx, 0, []Kind{KindConfirmation})
}
`
	scopedSites, err := findOutboxCallSitesInFile(fset, "fixture_scoped.go", scopedFixtureSrc, "/nonexistent/self-dir")
	if err != nil {
		t.Fatalf("findOutboxCallSitesInFile (scoped fixture): %v", err)
	}
	for _, site := range scopedSites {
		if site.unscoped() {
			t.Fatalf("scoped fixture call OrphanSweep(ctx, 0, []Kind{KindConfirmation}) was flagged as unscoped: %+v", site)
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

// TestNonExemptFloorCatchesScanRootsNarrowedToSelf is #0304 criterion 3's
// standing proof, run every time `go test` runs rather than argued once
// and left undemonstrated. It does NOT mutate claimKindsGuardScanRoots
// (a package-level var read by the real guard test) — instead it walks the
// real repo tree with roots=[]string{"."} passed directly to
// walkClaimKindsGuardFiles/findOutboxCallSitesInFile, the exact narrowing
// #0304's reviewer used, and exercises the SAME production functions the
// real guard calls, just with roots it controls. Since this test also runs
// from internal/outbox, "." resolves to this package's own directory —
// identical to what claimKindsGuardScanRoots={"."} would walk.
//
// It proves BOTH halves of #0304's finding, permanently:
//
//   - THE BEFORE STATE (what #0281's guard, before this fix, actually did):
//     the OLD floor alone (claimKindsGuardMinPlausibleCallSiteCount, which
//     counts every site regardless of inOwnPkg) is satisfied by this
//     narrowing — proving the population-mismatch bug was real, not just
//     asserted in the issue.
//   - THE FIX: the NEW floor (claimKindsGuardMinPlausibleNonExemptCallSiteCount)
//     is NOT satisfied — the non-exempt count drops to 0, since every site
//     the narrowed walk finds lives inside internal/outbox itself.
//
// If internal/outbox's own call-site count ever drops so low that the old
// floor stops being satisfied here too, that is fine — it only means this
// test's "before" assertion needs its own floor lowered to match, not that
// the finding stops being real; the "fix" assertion (nonExemptCount == 0)
// holds regardless of internal/outbox's own site count, since {"."} can
// never reach a file outside this package's directory.
func TestNonExemptFloorCatchesScanRootsNarrowedToSelf(t *testing.T) {
	selfDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving internal/outbox's own directory: %v", err)
	}

	fset := token.NewFileSet()
	var narrowedSites []outboxCallSite
	visited := walkClaimKindsGuardFiles(t, []string{"."}, func(path string) {
		sites, err := findOutboxCallSitesInFile(fset, path, nil, selfDir)
		if err != nil {
			t.Fatalf("%v", err)
		}
		narrowedSites = append(narrowedSites, sites...)
	})
	if visited == 0 {
		t.Fatal("walked zero .go files under \".\" from internal/outbox — this test's own premise (this package has .go files) is broken, not the guard's")
	}

	// BEFORE: the old, total-count-only floor would have reported PASS —
	// the exact bug #0304 found. If this assertion itself starts failing,
	// it means internal/outbox's own call-site count has dropped below
	// claimKindsGuardMinPlausibleCallSiteCount, not that the finding this
	// test documents is no longer real.
	if len(narrowedSites) < claimKindsGuardMinPlausibleCallSiteCount {
		t.Fatalf("this test's own premise broke: narrowing to \".\" now finds only %d call site(s) inside internal/outbox, below the old floor of %d — the old floor would (correctly, by coincidence) fail here too, so this test can no longer demonstrate the population-mismatch bug; adjust the premise rather than deleting the proof",
			len(narrowedSites), claimKindsGuardMinPlausibleCallSiteCount)
	}

	nonExemptCount := 0
	for _, site := range narrowedSites {
		if !site.inOwnPkg {
			nonExemptCount++
		}
	}

	// AFTER: the new floor correctly rejects this narrowing — zero sites
	// outside internal/outbox were ever reached, well below
	// claimKindsGuardMinPlausibleNonExemptCallSiteCount, which is exactly
	// what makes TestNoUnscopedOutboxClaimCallOutsidePackage fail closed
	// under this narrowing instead of reporting PASS with nothing
	// evaluated (#0304).
	if nonExemptCount != 0 {
		t.Fatalf("expected narrowing scan roots to \".\" to find zero call sites outside internal/outbox (every file reachable from this package's own directory is inside it), found %d — this test's assumption about what \".\" resolves to may be stale",
			nonExemptCount)
	}
	if nonExemptCount >= claimKindsGuardMinPlausibleNonExemptCallSiteCount {
		t.Fatalf("REGRESSION: nonExemptCount=%d unexpectedly satisfies claimKindsGuardMinPlausibleNonExemptCallSiteCount=%d under the \".\" narrowing — the new floor no longer catches the #0304 regression it exists for",
			nonExemptCount, claimKindsGuardMinPlausibleNonExemptCallSiteCount)
	}
}
