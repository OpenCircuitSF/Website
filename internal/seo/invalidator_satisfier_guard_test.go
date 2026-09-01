package seo

import (
	"go/types"
	"os"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestInvalidatorSatisfierSet is #0337's regression guard: two callers
// outside this package -- handlers.seoCacheInvalidator (named
// workshopCacheInvalidator until #0335) and mailing.ArchiveCacheInvalidator
// -- are both single-method interfaces requiring exactly `Invalidate()`,
// with no parameters and no results. Go interface satisfaction is
// structural, so ANY type in this package with a method matching that exact
// shape satisfies both, whether or not that was intended.
//
// #0325's rename (InvalidateWorkshops -> Invalidate, f7771ed) moved those
// two interfaces onto *Sitemap by accident: *Sitemap already had a method
// of that shape under its old, longer name, and shortening it made a
// previously-impossible assignment (passing a bare *Sitemap into either
// seam) compile silently instead of failing -- with a real runtime cost, a
// half-invalidation that clears the sitemap cache and leaves the per-path
// meta cache stale (the exact failure #0319 was filed to fix). #0337 closed
// that by unexporting Sitemap.invalidate (see its own doc comment); this
// test is what stops it from reopening.
//
// #0337's guard answered this with a go/ast scan of method DECLARATIONS,
// which is a sound proxy for the type-level property (method SETS) only
// while no type in this package acquires Invalidate() by PROMOTION from an
// embedded field -- so three review passes each closed one AST special case
// (value receivers, generic receivers) and then had to add a
// refuse-to-answer tripwire for the one case (embedding) an AST scan cannot
// model at all, plus a name-keyed allowlist to escape it that #0337's third
// review proved does not stay true (allowlist a type while it embeds a
// non-satisfier, then let an unrelated later edit swap the embedded type for
// a real satisfier, and go/types reports a new satisfier while the AST scan
// stays green with no edit to the guard file). #0349 replaces the whole
// scan with the actual oracle: go/types.Implements against every named
// type's real method set, computed by the type checker itself via
// golang.org/x/tools/go/packages with NeedTypes (export-data backed, not
// the ~11s importer.ForCompiler(fset, "source", nil) both prior reviews used
// only as one-off ground truth). Measured on 2026-08-31, three repeated runs
// each: ~500-600ms on the host (darwin/arm64) and ~500-600ms cross-compiled
// to GOOS=linux/GOARCH=arm64 -- fast enough to run on every `go test`, and
// the reason this guard now targets linux/arm64 explicitly rather than the
// host default (see below).
//
// Deleted with the AST scan: the receiver-form special cases (pointer vs.
// value vs. generic), the embedding tripwire, and the embeddingReviewed
// allowlist. types.Implements resolves promotion (embedded struct and
// interface fields) and generic receivers correctly on its own --
// go/types.Implements works directly on an uninstantiated generic Named
// type and its pointer, with no manual types.Instantiate step, confirmed
// empirically against a synthetic `func (f *FeedCache[T]) Invalidate()`
// during #0349's measurement -- so there is nothing left for a hand-reviewed
// allowlist to guard.
//
// Build-tag decision (#0349, from #0337's third review N11): the AST scan
// was build-tag-BLIND in the safe direction -- it read every .go file
// regardless of //go:build constraints, so a `//go:build linux` file
// declaring an accidental Invalidate() was caught even on a developer's
// mac. A naive go/types replacement running under the host's default
// GOOS/GOARCH would lose that: production is linux/arm64 (CLAUDE.md §7), so
// a linux-only satisfier would be invisible to a darwin type-check. This
// guard closes that gap deliberately rather than losing it silently: its
// packages.Config.Env pins GOOS=linux and GOARCH=arm64, matching production
// exactly, so the type-check this guard performs is the one that matters --
// what actually ships. (A hypothetical darwin-only or other-GOOS-only
// satisfier is out of scope: this codebase does not ship to any target
// other than linux/arm64, so checking additional host targets would only
// buy coverage for platforms nothing here ever builds for, at the cost of
// another ~500ms Load per target.)
//
// This guard cannot reference handlers.seoCacheInvalidator or
// mailing.ArchiveCacheInvalidator directly: the former is unexported
// (package-private to internal/handlers by design -- see
// admin_workshops.go's doc comment), and internal/seo already imports
// internal/handlers for route helpers (seo.go's metaFor), so an import in
// the other direction would be a cycle regardless of visibility. Instead it
// builds an ad hoc `interface{ Invalidate() }` -- the exact shared shape of
// both real seams -- and asks go/types whether each named type in this
// package's own compiled type information implements it.
//
// The known correct answer, current as of #0337: *Site and *Renderer are
// the intended satisfiers (only *Site is ever actually passed into either
// seam in production, but *Renderer already had a matching method before
// #0325 and closing that pre-existing case is explicitly out of #0337's
// scope -- see its acceptance criterion 4). *Sitemap must NOT be a
// satisfier.
func TestInvalidatorSatisfierSet(t *testing.T) {
	pkg := loadSeoPackageForProductionTarget(t)

	// The shared shape both handlers.seoCacheInvalidator and
	// mailing.ArchiveCacheInvalidator require: exactly one method, named
	// Invalidate, no parameters, no results.
	invalidateOnly := types.NewInterfaceType([]*types.Func{
		types.NewFunc(0, pkg.Types, "Invalidate", types.NewSignatureType(nil, nil, nil, nil, nil, false)),
	}, nil).Complete()

	scope := pkg.Types.Scope()
	names := scope.Names()
	if len(names) == 0 {
		t.Fatal("fail closed: internal/seo's package scope has zero names -- packages.Load returned an " +
			"empty or broken type-checked package, not evidence there is nothing to check")
	}

	// got records every named, non-interface type in this package whose
	// pointer implements interface{ Invalidate() } -- by direct method
	// declaration (either receiver form), by promotion from an embedded
	// struct or interface field, or via a generic receiver. A pointer's
	// method set is a superset of its base type's, so checking only the
	// pointer form is sufficient: it is also what every real call site
	// passes into either seam (*Site, *Renderer, *Sitemap), never a bare
	// value.
	got := map[string]bool{}
	concreteTypesChecked := 0
	for _, name := range names {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		named, ok := tn.Type().(*types.Named)
		if !ok {
			continue
		}
		if _, isInterface := named.Underlying().(*types.Interface); isInterface {
			// A declared interface in this package is a seam, not a
			// concrete type that could accidentally join one -- declaring
			// `type Invalidator interface{ Invalidate() }` makes nothing
			// new passable that was not already (#0337's third review, N1).
			continue
		}
		concreteTypesChecked++
		if types.Implements(types.NewPointer(named), invalidateOnly) {
			got[name] = true
		}
	}

	// Fail closed (CLAUDE.md §8 / #0275's lesson): finding zero named
	// concrete types is never legitimate evidence there is nothing to
	// check -- it means the scan itself broke.
	if concreteTypesChecked == 0 {
		t.Fatal("fail closed: found zero named concrete (non-interface) types in internal/seo -- this " +
			"oracle is broken, not evidence nothing needs checking")
	}

	want := map[string]bool{
		"Site":     true,  // intended satisfier -- the only type actually passed into either seam
		"Renderer": true,  // pre-existing, harmless accidental satisfier, explicitly scoped out by #0337
		"Sitemap":  false, // #0337's fix: must NOT satisfy handlers.seoCacheInvalidator or mailing.ArchiveCacheInvalidator
	}

	// Fail closed the other direction: finding zero satisfiers at all would
	// mean the oracle itself is broken, since *Site and *Renderer have
	// always had a matching method since before #0325.
	if len(got) == 0 {
		t.Fatal("fail closed: go/types.Implements found zero satisfiers of interface{ Invalidate() } in " +
			"internal/seo -- this oracle is broken (fail closed, never treat this as 'nothing to check'): " +
			"as of #0337, *Site and *Renderer both satisfy it")
	}

	for typeName, wantSatisfies := range want {
		if got[typeName] != wantSatisfies {
			t.Errorf("*%s implements interface{ Invalidate() }: got %v, want %v -- this changes whether "+
				"*%s structurally satisfies handlers.seoCacheInvalidator and mailing.ArchiveCacheInvalidator, "+
				"both single-method interfaces requiring exactly Invalidate() (see this test's doc comment)",
				typeName, got[typeName], wantSatisfies, typeName)
		}
	}

	// The actual regression this guard exists to catch: an UNINTENDED type
	// joining the satisfier set. The loop above only re-checks the three
	// types known when this guard was written, so on its own it cannot see
	// a new type acquiring Invalidate() by any means -- declaration,
	// promotion, or generic instantiation -- which is precisely the "the
	// satisfier set grew, silently" failure #0337 was filed about.
	unexpected := make([]string, 0, len(got))
	for typeName := range got {
		if _, known := want[typeName]; known {
			continue
		}
		unexpected = append(unexpected, typeName)
	}
	sort.Strings(unexpected)
	for _, typeName := range unexpected {
		t.Errorf("%s structurally implements interface{ Invalidate() } (by declaration, promotion, or "+
			"generic instantiation) but is not in this guard's intended satisfier set, so it now "+
			"structurally satisfies handlers.seoCacheInvalidator and mailing.ArchiveCacheInvalidator -- "+
			"either it is a deliberate new satisfier (add it to `want` with a comment saying why) or it "+
			"is #0337's defect regrowing (unexport the method, or remove/change the embedding)", typeName)
	}
}

// loadSeoPackageForProductionTarget type-checks internal/seo (this
// package's own directory) via golang.org/x/tools/go/packages, targeting
// production's GOOS/GOARCH (linux/arm64, CLAUDE.md §7) rather than the
// host's default, so a build-tag-gated file that would actually ship is not
// invisible to this guard on a developer's mac. It runs with
// GOFLAGS=-mod=readonly explicitly forced (never -mod=mod): a type-checking
// loader run with -mod=mod against this module has been observed to
// silently promote an unrelated indirect dependency to direct in go.mod
// (#0349's own filed trap, hit for real by #0346's reviewer against
// github.com/aws/smithy-go) -- readonly mode cannot mutate go.mod at all.
func loadSeoPackageForProductionTarget(t *testing.T) *packages.Package {
	t.Helper()

	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "GOOS="),
			strings.HasPrefix(kv, "GOARCH="),
			strings.HasPrefix(kv, "GOFLAGS="):
			continue // overridden explicitly below
		}
		env = append(env, kv)
	}
	env = append(env, "GOOS=linux", "GOARCH=arm64", "GOFLAGS=-mod=readonly")

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax | packages.NeedDeps,
		Env:  env,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		t.Fatalf("packages.Load(internal/seo, GOOS=linux, GOARCH=arm64): %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("packages.Load reported package errors type-checking internal/seo for linux/arm64 -- " +
			"fail closed rather than checking a broken load (this is also how a mutation that breaks " +
			"the package, e.g. removing an intended satisfier's method, is caught: the package fails " +
			"to build and this test cannot report green)")
	}
	if len(pkgs) != 1 {
		t.Fatalf("fail closed: expected exactly 1 package loaded for internal/seo, got %d", len(pkgs))
	}
	if pkgs[0].Types == nil {
		t.Fatal("fail closed: packages.Load returned no type information for internal/seo")
	}
	return pkgs[0]
}
