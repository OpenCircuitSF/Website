package seo

import (
	"go/ast"
	"go/parser"
	"go/token"
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
// #0325's rename (f7771ed) shortened InvalidateWorkshops to Invalidate on
// *Site's and *Renderer's own methods and on both seams' required method --
// not on *Sitemap. That moved both interfaces onto *Sitemap by accident:
// *Sitemap already had a method of that exact shape under the short name,
// untouched by the rename, so a previously-impossible assignment (passing a
// bare *Sitemap into either seam) went from a compile error to compiling
// silently -- with a real runtime cost, a half-invalidation that clears the
// sitemap cache and leaves the per-path meta cache stale (the meta-cache
// half of #0319's staleness class, not the sitemap staleness #0319 led
// with). #0337 closed that by unexporting Sitemap.invalidate (see its own
// doc comment); this test is what stops it from reopening.
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
// golang.org/x/tools/go/packages with NeedTypes -- source-type-checked, not
// export-data backed (measured, #0349's review: this same package costs
// 1.3s cold / ~560ms warm via NeedTypes, against 5.4s and 240MB of build
// cache cold for an export-data-backed load of the same package, `GOOS=linux
// GOARCH=arm64 go list -export -deps ./internal/seo`) -- not the ~11s
// importer.ForCompiler(fset, "source", nil) both prior reviews used only as
// one-off ground truth. Measured on 2026-08-31, three repeated runs each:
// ~500-600ms on the host (darwin/arm64) and ~500-600ms cross-compiled to
// GOOS=linux/GOARCH=arm64 -- fast enough to run on every `go test`, and the
// reason this guard now targets linux/arm64 explicitly rather than the host
// default (see below).
//
// Deleted with the AST scan: the receiver-form special cases (pointer vs.
// value vs. generic), the embedding tripwire, and the embeddingReviewed
// allowlist. types.Implements resolves promotion (embedded struct and
// interface fields) and generic receivers correctly on its own --
// go/types.Implements works directly on an uninstantiated generic Named
// type and its pointer, with no manual types.Instantiate step, confirmed
// empirically against a synthetic `func (f *FeedCache[T]) Invalidate()`
// during #0349's measurement -- so there is nothing left for a hand-reviewed
// allowlist to guard by declaration or promotion. The one remaining gap,
// found by #0349's own review and closed by #0353: a package-scope alias to
// a type LITERAL (`type FeedCache = struct{ *Site; cached []byte }`) resolves
// to *types.Alias, not *types.Named, under gotypesalias=1, so it is invisible
// to a bare *types.Named assertion. That gap is closed structurally, by a
// types.Unalias branch in the loop below, not by an allowlist.
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

	// got records every named or aliased, non-interface type in this
	// package's scope whose pointer implements interface{ Invalidate() } --
	// by direct method declaration (either receiver form), by promotion
	// from an embedded struct or interface field, or via a generic
	// receiver. See scanInvalidatorSatisfiers' own doc comment for the
	// full shape, including the package-scope alias case (#0353).
	got, concreteTypesChecked := scanInvalidatorSatisfiers(scope, invalidateOnly)

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

// scanInvalidatorSatisfiers is TestInvalidatorSatisfierSet's scanning logic,
// factored out so TestInvalidatorSatisfierSet_AliasToTypeLiteral below can
// pin the alias-to-type-literal branch (#0353) against a small synthetic
// package instead of re-deriving the same assertion by hand -- the same
// function runs in both tests, so a future edit that breaks the alias
// handling breaks both, and the synthetic test is not merely a second copy
// of the same claim (CLAUDE.md §8, "a guard's oracle must not be the same
// bytes as its subject").
//
// It records every named or aliased, non-interface type in scope whose
// pointer implements invalidateOnly -- by direct method declaration (either
// receiver form), by promotion from an embedded struct or interface field,
// by generic receiver, or by a package-scope alias to any of those or to a
// type LITERAL. A pointer's method set is a superset of its base type's, so
// checking only the pointer form is sufficient for a *types.Named target: it
// is also what every real call site passes into either seam (*Site,
// *Renderer, *Sitemap), never a bare value. An alias target that is not
// itself a *types.Named (a type literal) is checked in both forms, since
// promotion from an embedded value field lands only on the literal's
// pointer form, not the value form.
func scanInvalidatorSatisfiers(scope *types.Scope, invalidateOnly *types.Interface) (got map[string]bool, concreteTypesChecked int) {
	got = map[string]bool{}
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}

		if _, isAlias := tn.Type().(*types.Alias); isAlias {
			// Package-scope alias case (#0353): under gotypesalias=1 (the
			// default), scope.Lookup(name).Type() for a `type X = ...`
			// alias declaration returns *types.Alias, not *types.Named, so
			// the *types.Named assertion below silently skips it --
			// reporting `ok` on a real satisfier such as `type FeedCache =
			// struct{ *Site; cached []byte }`, where var _ interface{
			// Invalidate() } = FeedCache{} compiles. types.Unalias resolves
			// to what the alias actually names.
			target := types.Unalias(tn.Type())

			if _, isNamed := target.(*types.Named); isNamed {
				// A new spelling of a type this loop already checks under
				// its own name (`type FeedCache = Renderer`) -- re-checking
				// here would be redundant, not a coverage gap (#0337's
				// third review, N2; #0353 criterion 1).
				continue
			}
			if ptr, isPointer := target.(*types.Pointer); isPointer {
				if _, elemIsNamed := ptr.Elem().(*types.Named); elemIsNamed {
					// `type FeedCache = *Site` -- same reasoning, one level
					// of indirection further.
					continue
				}
			}
			if _, isInterface := target.Underlying().(*types.Interface); isInterface {
				// An alias to a declared interface (`type Invalidator =
				// SomeInterface`) is a seam, not a concrete type joining
				// one -- the same declared-seam rule as the *types.Named
				// branch below (#0337's third review, N1).
				continue
			}

			// A genuine alias to a type LITERAL -- the one construct the
			// retired AST scan's structTypesWithEmbeddedFields tripwire
			// caught and this *types.Named-only oracle could not (#0353).
			// target is not a *types.Named, so checking only the pointer
			// form (as the loop below does) is not sound here, and neither
			// disjunct below is redundant -- each is load-bearing for the
			// opposite reason (#0354): `type X = struct{ Site }` (a VALUE
			// embed of a pointer-receiver type) implements only in the
			// pointer form, since promotion from an embedded value field
			// lands on the literal's pointer method set, not its value
			// method set; `type X = *struct{ *Site }` (an alias to a
			// POINTER literal) implements only in the value form, since
			// target here already IS the pointer type and wrapping it again
			// with types.NewPointer produces `**struct`, whose method set is
			// always empty. Dropping either disjunct silently reopens the
			// alias shape the other one exists to catch --
			// TestInvalidatorSatisfierSet_AliasToTypeLiteral pins both.
			concreteTypesChecked++
			if types.Implements(target, invalidateOnly) || types.Implements(types.NewPointer(target), invalidateOnly) {
				got[name] = true
			}
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
	return got, concreteTypesChecked
}

// TestInvalidatorSatisfierSet_AliasToTypeLiteral is #0353's permanent
// standing regression for the one construct #0349's go/types.Implements
// replacement could not see: a package-scope alias to a type LITERAL
// (`type X = struct{ *Site; ... }`), which resolves to *types.Alias rather
// than *types.Named under gotypesalias=1 and is therefore invisible to a
// bare *types.Named type assertion. #0349's reviewer verified the old,
// retired AST scan caught this construct; this test pins that
// scanInvalidatorSatisfiers (the function TestInvalidatorSatisfierSet itself
// calls, not a re-derivation of it) catches it too.
//
// Built directly with go/types.Config.Check against a small synthetic
// in-memory source file rather than packages.Load against internal/seo's own
// directory -- it runs in milliseconds, needs no importer, and does not
// depend on this package's real contents, so it stays a fast, permanent part
// of `go test ./internal/seo/...` rather than something only exercised in a
// throwaway mutation worktree (#0353 acceptance criteria 3-4).
func TestInvalidatorSatisfierSet_AliasToTypeLiteral(t *testing.T) {
	const src = `package synthetic

type Site struct{}

func (s *Site) Invalidate() {}

// FeedCache is a package-scope alias to an anonymous struct literal that
// promotes Invalidate() from its embedded *Site field -- the exact
// construct #0349's go/types.Implements-over-*types.Named replacement
// missed, and the retired AST scan caught.
type FeedCache = struct {
	*Site
	cached []byte
}

// AliasToNamed is #0337's third review's correct-survival case N2: a new
// spelling of a type already checked under its own name, and must NOT be
// double-counted as a separate satisfier.
type AliasToNamed = Site

// AliasValueEmbed pins the disjunct FeedCache above cannot pin on its own
// (#0354): a VALUE embed of a pointer-receiver type implements
// interface{ Invalidate() } in POINTER form only (types.Implements(target)
// is false; types.Implements(types.NewPointer(target)) is true), because
// promotion from an embedded value field lands on the literal's pointer
// method set. FeedCache implements in both forms and so cannot by itself
// prove the pointer-form disjunct is load-bearing.
type AliasValueEmbed = struct {
	Site
}

// AliasToPointerLiteral pins the other disjunct (#0354): an alias to a
// POINTER literal implements interface{ Invalidate() } in VALUE form only
// (types.Implements(target) is true; types.Implements(types.NewPointer(target))
// is false), because target here already IS the pointer type, and wrapping
// it again produces **struct, whose method set is always empty.
type AliasToPointerLiteral = *struct {
	*Site
}

// DeclaredSeam is a declared (named) interface, present so the doc comments
// below can point at a concrete counter-example: aliasing to IT, i.e. type X
// = DeclaredSeam, would NOT exercise the declared-interface continue below,
// because types.Unalias(tn.Type()) on such an alias resolves to a
// *types.Named (DeclaredSeam itself), so the alias-to-*types.Named continue
// (already pinned by AliasToNamed above) fires first and consumes it. Only
// an alias to an interface LITERAL, with no declared name of its own,
// reaches the declared-interface continue -- see AliasToIfaceLiteral below.
// DeclaredSeam itself is not aliased anywhere in this fixture; it exists to
// make that contrast nameable rather than hypothetical (#0355).
type DeclaredSeam interface{ Invalidate() }

// AliasToPtrNamed pins the second of the alias branch's three continues
// (#0355): an alias to a POINTER-TO-*types.Named type, i.e. type X = *Site,
// distinct from AliasToNamed's direct type X = Site, is a new spelling of a
// type already checked -- under *Site's own pointer form -- so it must not
// be double-counted as its own satisfier.
type AliasToPtrNamed = *Site

// AliasToIfaceLiteral pins the third of the alias branch's three continues
// (#0355): an alias to an interface LITERAL (not a declared interface name)
// resolves, via types.Unalias, directly to a *types.Interface -- it is not a
// *types.Named (so the AliasToNamed continue does not fire) and not a
// *types.Pointer (so the AliasToPtrNamed continue does not fire either) --
// so it is the declared-interface continue itself, and only this shape,
// that must skip it. DeclaredSeam above is the trap: aliasing to a NAMED
// interface takes an entirely different path through the *types.Named
// continue and would prove nothing about this one.
type AliasToIfaceLiteral = interface{ Invalidate() }
`

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("parse synthetic source: %v", err)
	}

	conf := types.Config{}
	pkg, err := conf.Check("synthetic", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatalf("type-check synthetic source: %v", err)
	}

	invalidateOnly := types.NewInterfaceType([]*types.Func{
		types.NewFunc(0, pkg, "Invalidate", types.NewSignatureType(nil, nil, nil, nil, nil, false)),
	}, nil).Complete()

	got, concreteTypesChecked := scanInvalidatorSatisfiers(pkg.Scope(), invalidateOnly)

	// Fail closed: prove the harness itself found something before trusting
	// its negative assertions below (CLAUDE.md §8's fail-open lesson).
	if concreteTypesChecked == 0 {
		t.Fatal("fail closed: scanInvalidatorSatisfiers found zero concrete types in the synthetic " +
			"package -- the harness itself is broken, not evidence there is nothing to check")
	}

	if !got["FeedCache"] {
		t.Error("scanInvalidatorSatisfiers did not report FeedCache (a package-scope alias to a struct " +
			"literal promoting Invalidate() from an embedded *Site) as a satisfier -- #0353's fix has " +
			"regressed and this construct is invisible to the guard again")
	}
	if got["AliasToNamed"] {
		t.Error("scanInvalidatorSatisfiers reported AliasToNamed (an alias to the already-checked Site " +
			"type) as its own satisfier -- it should be skipped as a redundant spelling of Site " +
			"(#0337's third review, N2), and double-counting it risks masking a real regression under " +
			"an unrelated name")
	}

	// #0354: FeedCache alone cannot discriminate which disjunct of
	// `types.Implements(target, ...) || types.Implements(types.NewPointer(target), ...)`
	// is doing the work, because it happens to implement in both forms.
	// These two pin each disjunct separately -- dropping either one from the
	// `||` leaves FeedCache (and TestInvalidatorSatisfierSet) green while
	// silently missing one of these.
	if !got["AliasValueEmbed"] {
		t.Error("scanInvalidatorSatisfiers did not report AliasValueEmbed (a value embed of a " +
			"pointer-receiver type, which implements interface{ Invalidate() } in POINTER form only) as " +
			"a satisfier -- the pointer-form disjunct of the `||` is missing or broken")
	}
	if !got["AliasToPointerLiteral"] {
		t.Error("scanInvalidatorSatisfiers did not report AliasToPointerLiteral (an alias to a pointer " +
			"literal, which implements interface{ Invalidate() } in VALUE form only) as a satisfier -- " +
			"the value-form disjunct of the `||` is missing or broken")
	}

	// #0355: the alias branch has three `continue` exclusions in total.
	// AliasToNamed above already pins the first (alias directly to a
	// *types.Named); these two pin the remaining two, which #0354's review
	// found unpinned by anything in the tree.
	if got["AliasToPtrNamed"] {
		t.Error("scanInvalidatorSatisfiers reported AliasToPtrNamed (an alias to a pointer-to-*types.Named " +
			"type, *Site) as its own satisfier -- it should be skipped as a redundant spelling of Site's " +
			"own pointer form (same reasoning as AliasToNamed, one indirection further), and " +
			"double-counting it means the alias-to-pointer-to-*types.Named `continue` is missing or broken")
	}
	if got["AliasToIfaceLiteral"] {
		t.Error("scanInvalidatorSatisfiers reported AliasToIfaceLiteral (an alias to the interface " +
			"LITERAL interface{ Invalidate() }) as a satisfier -- an alias to a seam is not a concrete " +
			"type joining one, and double-counting it means the declared-interface `continue` is missing " +
			"or broken")
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
