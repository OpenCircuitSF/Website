package seo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
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
// This guard cannot reference handlers.seoCacheInvalidator or
// mailing.ArchiveCacheInvalidator directly: the former is unexported
// (package-private to internal/handlers by design -- see
// admin_workshops.go's doc comment), and internal/seo already imports
// internal/handlers for route helpers (seo.go's metaFor), so an import in
// the other direction would be a cycle regardless of visibility. Instead it
// reads THIS package's own source with go/ast/go/parser -- the same
// technique cmd/opencircuit/servepostgres_seo_instance_guard_test.go's
// interfaceIsInvalidateOnly uses for the mirror-image problem (recognizing
// an Invalidate()-only interface from its declaration) -- and asks the
// equivalent question about a METHOD declaration instead of an interface
// declaration: does *Site / *Renderer / *Sitemap have a method named
// exactly "Invalidate", taking no parameters and returning nothing? Reading
// method declarations is equivalent to asking about method SETS only while no
// type in this package acquires Invalidate() by promotion from an embedded
// field, which is true today (site.go, seo.go, sitemap.go all declare plain,
// non-embedding structs) -- and this test no longer merely assumes it: the
// structTypesWithEmbeddedFields check below fails the moment any struct here
// grows an embedded field, because a promoted method is invisible to the
// scan. Per CLAUDE.md
// §8's placement rule, this is a legitimate in-package oracle rather than
// one requiring an external harness: the guard's subject is this package's
// own method declarations, and a mutation to those declarations (renaming,
// adding, removing Invalidate) changes what the scan finds -- there is
// nothing here for an edit to the subject to hide behind.
//
// The known correct answer, current as of #0337: *Site and *Renderer are
// the intended satisfiers (only *Site is ever actually passed into either
// seam in production, but *Renderer already had a matching method before
// #0325 and closing that pre-existing case is explicitly out of #0337's
// scope -- see its acceptance criterion 4). *Sitemap must NOT be a
// satisfier.
func TestInvalidatorSatisfierSet(t *testing.T) {
	fset := token.NewFileSet()
	methods := invalidateOnlyMethodReceivers(t, fset, ".")

	// Fail closed (CLAUDE.md §8 / #0275's lesson): finding zero
	// Invalidate()-only method receivers is never legitimate evidence that
	// nothing needs checking -- it means the scan itself broke (the
	// directory moved, every .go file failed to parse). At least one
	// receiver type in this package has always had a method matching this
	// exact shape since before #0325.
	satisfiers := 0
	for _, has := range methods {
		if has {
			satisfiers++
		}
	}
	if satisfiers == 0 {
		t.Fatal("found zero Invalidate()-only method receivers in internal/seo -- this scan is broken " +
			"(fail closed, never treat this as 'nothing to check'): as of #0337, *Site and *Renderer " +
			"both declare one")
	}

	want := map[string]bool{
		"Site":     true,  // intended satisfier -- the only type actually passed into either seam
		"Renderer": true,  // pre-existing, harmless accidental satisfier, explicitly scoped out by #0337
		"Sitemap":  false, // #0337's fix: must NOT satisfy handlers.seoCacheInvalidator or mailing.ArchiveCacheInvalidator
	}
	for typeName, wantSatisfies := range want {
		gotSatisfies := methods[typeName]
		if gotSatisfies != wantSatisfies {
			t.Errorf("*%s has an Invalidate()-only method: got %v, want %v -- this changes whether "+
				"*%s structurally satisfies handlers.seoCacheInvalidator and mailing.ArchiveCacheInvalidator, "+
				"both single-method interfaces requiring exactly Invalidate() (see this test's doc comment)",
				typeName, gotSatisfies, wantSatisfies, typeName)
		}
	}

	// Criterion 5's actual requirement: fail when an UNINTENDED type joins
	// the satisfier set. The loop above only re-checks the three types known
	// when this guard was written, so on its own it cannot see a new type
	// acquiring Invalidate() -- which is precisely the "the satisfier set
	// grew, silently" failure #0337 was filed about.
	for typeName, gotSatisfies := range methods {
		if !gotSatisfies {
			continue
		}
		if _, known := want[typeName]; known {
			continue
		}
		t.Errorf("%s declares an Invalidate()-only method but is not in this guard's intended "+
			"satisfier set, so it now structurally satisfies handlers.seoCacheInvalidator and "+
			"mailing.ArchiveCacheInvalidator -- either it is a deliberate new satisfier (add it "+
			"to `want` with a comment saying why) or it is #0337's defect regrowing (unexport "+
			"the method, as Sitemap.invalidate is)", typeName)
	}

	// Fail closed on the one construct this scan cannot model. Everything
	// above reads METHOD DECLARATIONS, which is a sound proxy for "satisfies
	// an Invalidate()-only interface" only while no type in this package
	// acquires the method by PROMOTION. Embedding an existing satisfier
	// (`struct{ *Site }`) or an interface that has the method
	// (`struct{ cacheInvalidator }`) gives the outer type Invalidate() with
	// no method declaration anywhere for the scan to find: #0337's second
	// review proved go/types then reports the outer type as a satisfier of
	// both seams while this guard stayed green. Reimplementing Go's
	// method-set rules here would be a type checker, so instead this refuses
	// to answer at all once the premise breaks. internal/seo declares no
	// embedded fields today; a type that grows one must be checked by hand
	// with go/types.Implements and then listed in embeddingReviewed.
	embeddingReviewed := map[string]bool{}
	for _, typeName := range structTypesWithEmbeddedFields(t, fset, ".") {
		if embeddingReviewed[typeName] {
			continue
		}
		t.Errorf("%s is a struct with an embedded field, so it may satisfy "+
			"handlers.seoCacheInvalidator and mailing.ArchiveCacheInvalidator through a PROMOTED "+
			"Invalidate() that this guard's method-declaration scan cannot see -- check it with "+
			"go/types.Implements against interface{ Invalidate() }, then either remove the "+
			"embedding, unexport the promoted method's source, or add %q to embeddingReviewed with "+
			"a comment recording that check", typeName, typeName)
	}
}

// invalidateOnlyMethodReceivers parses every non-test .go file in dir and
// returns, for each receiver type declared there, whether it has a method
// named exactly "Invalidate" with zero parameters and zero results -- the
// shape *seo.Site.Invalidate implements (site.go) and every single-method
// invalidator seam in this codebase requires. Both receiver forms are
// scanned, pointer and value, because a value-receiver method matters just
// as much as a pointer-receiver one: Go's method-set rule makes *T's method
// set include every value-receiver method declared on T, so
// `func (s Sitemap) Invalidate() {}` would make *Sitemap satisfy both seams
// exactly as surely as a pointer receiver does (#0337's review proved this
// with go/types).
func invalidateOnlyMethodReceivers(t *testing.T, fset *token.FileSet, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}

	result := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, dir+string(os.PathSeparator)+name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 {
				continue
			}
			// Both receiver forms matter: the method set of *T includes
			// value-receiver methods, so `func (s Sitemap) Invalidate()` makes
			// *Sitemap satisfy both seams just as surely as a pointer receiver
			// does (#0337's review proved this with go/types).
			recvType := fd.Recv.List[0].Type
			if star, ok := recvType.(*ast.StarExpr); ok {
				recvType = star.X
			}
			// A generic receiver is spelled T[P] or T[P1, P2], which parse as
			// *ast.IndexExpr / *ast.IndexListExpr wrapping the base type's
			// identifier. Unwrap to that identifier: *FeedCache[int] satisfies
			// an Invalidate()-only interface exactly as *FeedCache would
			// (#0337's second review proved this with types.Instantiate), and
			// without this unwrap the whole declaration is skipped.
			switch idx := recvType.(type) {
			case *ast.IndexExpr:
				recvType = idx.X
			case *ast.IndexListExpr:
				recvType = idx.X
			}
			ident, ok := recvType.(*ast.Ident)
			if !ok {
				continue
			}
			typeName := ident.Name
			if _, seen := result[typeName]; !seen {
				result[typeName] = false
			}
			if fd.Name.Name != "Invalidate" {
				continue
			}
			if fd.Type.Params != nil && len(fd.Type.Params.List) > 0 {
				continue
			}
			if fd.Type.Results != nil && len(fd.Type.Results.List) > 0 {
				continue
			}
			result[typeName] = true
		}
	}
	return result
}

// structTypesWithEmbeddedFields parses every non-test .go file in dir and
// returns, sorted, the name of every struct type declared there that has at
// least one embedded (anonymous) field. It exists because
// invalidateOnlyMethodReceivers reads method declarations, and an embedded
// field can supply Invalidate() with no declaration to read: `struct{ *Site }`
// and `struct{ cacheInvalidator }` both make the outer type satisfy every
// Invalidate()-only seam. Deciding whether a particular embedding does that
// needs full type information (the embedded type may come from another
// package), so this reports the construct rather than trying to resolve it --
// the caller fails closed on anything not explicitly reviewed. Interface type
// declarations are deliberately not reported: an interface in this package
// whose method set is Invalidate() is a declared seam, not a concrete type
// accidentally joining one.
func structTypesWithEmbeddedFields(t *testing.T, fset *token.FileSet, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}

	var found []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, dir+string(os.PathSeparator)+name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ts, ok := node.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				if len(field.Names) == 0 {
					found = append(found, ts.Name.Name)
					return true
				}
			}
			return true
		})
	}
	sort.Strings(found)
	return found
}
