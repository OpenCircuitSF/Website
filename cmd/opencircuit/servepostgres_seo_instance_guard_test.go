package main

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

// TestServePostgres_OneSEOSiteFlowsToEveryConsumer is #0326: an AST guard,
// modelled on TestAdminRoutesRegisteredOnlyViaTable (admin_routes_ast_test.go,
// #0079) and the same family as the citation guards (#0181/#0197/#0120) --
// a runtime test cannot see this defect class because every consumer here is
// nil-tolerant BY DESIGN (#0054's Ruling 1 concession, restated by #0319):
// AdminWorkshopsHandler, AdminCampaignArchiveHandler, and
// newSendWorkerIfEnabled's worker all degrade silently on a nil invalidator,
// so a servePostgres edit that hands one of them nil, or that calls
// buildSEOSite twice and hands out two different *seo.Site instances,
// compiles, boots, and passes every existing test (admin_wiring_test.go and
// worker_wiring_test.go both pass literal nil on purpose, to prove the
// nil-tolerant path) -- it silently restores the 60-second cache staleness
// #0319 removed. Only reading servePostgres's own source proves the SAME
// value reaches every place that needs one.
//
// # Why this belongs in Go, not an external harness (criterion 5)
//
// CLAUDE.md §8's placement rule is the falsifiability test: after a
// mutation, is the assertion still falsifiable by the thing it measures?
// This guard's subject is a specific function's SOURCE TEXT
// (cmd/opencircuit/main.go), and it reads that file fresh with
// parser.ParseFile on every run, exactly like admin_routes_ast_test.go does
// -- there is no compiled/cached form of servePostgres's wiring for a
// mutation to hide behind, and no subprocess boundary that would let a
// stale binary answer instead of the edited source (the trap CLAUDE.md's
// "self-check can start measuring the process instead of the file" entry
// warns about). An external shell harness would have to reimplement Go
// parsing to inspect the same source text, for no falsifiability the
// in-process go/ast form doesn't already have. Kept as a normal Go test
// (not a *_guard_test.sh, and not in scripts/check.sh's `guards` bucket)
// because, unlike the shell guards CLAUDE.md §8 catalogs, nothing here
// re-executes scripts/check.sh itself or risks the fail-open shape those
// guard against -- it is a closed, single-pass AST read.
//
// # What "enumerates consumers" means here (criterion 4)
//
// Rather than hard-coding "NewAdminWorkshopsHandler,
// NewAdminCampaignArchiveHandler, newSendWorkerIfEnabled" by name, this
// walks every function CALLED from servePostgres, resolves each callee's
// own declared parameter types (parsing its defining file, in
// cmd/opencircuit itself for a package-local function like
// newSendWorkerIfEnabled, or in the imported internal/handlers or
// internal/mailing package for a qualified call), and treats a parameter as
// "requires the shared *seo.Site" if its declared type is either literally
// *seo.Site, or a type (local to the callee's own package, possibly
// package-qualified) whose FULL declared method set is exactly one method:
// `Invalidate()` with no parameters and no results -- the exact shape
// *seo.Site's own Invalidate implements (internal/seo/site.go, named
// InvalidateWorkshops until #0325). Any FUTURE function added to
// servePostgres with a parameter of that shape is covered automatically,
// with no edit to this file, because the scan is driven by the callee's own
// signature, not by a name list kept here.
func TestServePostgres_OneSEOSiteFlowsToEveryConsumer(t *testing.T) {
	const thisDir = "."
	fset := token.NewFileSet()

	mainFile, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	servePostgres := findFuncDecl(mainFile, "servePostgres")
	if servePostgres == nil {
		t.Fatal("servePostgres not found in main.go — has it been renamed or moved? " +
			"this guard's whole premise depends on that exact function existing")
	}

	// alias -> local directory, built from main.go's own import block, so a
	// package-qualified call (handlers.NewAdminWorkshopsHandler,
	// mailing.NewCampaignStatsStore, ...) can be traced back to the file
	// that declares it.
	pkgDirs := importDirs(t, mainFile)

	// Step 1: buildSEOSite must be called EXACTLY ONCE in servePostgres, and
	// its first return value's identifier is the one value every consumer
	// below must receive. Zero calls is the "scan is broken" case (#0275's
	// and #0300's lesson: an empty extraction is an error, never evidence of
	// nothing to check) — buildSEOSite is unconditionally called in every
	// servePostgres implementation this test has ever seen, so zero is never
	// legitimate. More than one is exactly the "second buildSEOSite instance
	// handed to a different consumer" mutation criterion 2 names.
	var buildCalls []*ast.CallExpr
	var siteVar string
	ast.Inspect(servePostgres.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range assign.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "buildSEOSite" {
				continue
			}
			buildCalls = append(buildCalls, call)
			if i < len(assign.Lhs) {
				if lhsIdent, ok := assign.Lhs[i].(*ast.Ident); ok {
					siteVar = lhsIdent.Name
				}
			}
		}
		return true
	})
	switch len(buildCalls) {
	case 0:
		t.Fatal("found zero buildSEOSite calls in servePostgres — either this scan is broken " +
			"(fail closed, per CLAUDE.md's #0275/#0300 lesson) or servePostgres no longer builds " +
			"its own *seo.Site, which this whole guard assumes")
	case 1:
		// expected
	default:
		t.Fatalf("found %d buildSEOSite calls in servePostgres, want exactly 1 — a second call "+
			"produces a second, independently-constructed *seo.Site, which is exactly criterion 2's "+
			"'hands out two different *seo.Site instances' mutation, at positions: %s",
			len(buildCalls), positions(fset, buildCalls))
	}
	if siteVar == "" {
		t.Fatal("buildSEOSite's call was found but its result wasn't assigned to a plain identifier " +
			"(e.g. `site, err := buildSEOSite(...)`) — this scan only understands that shape")
	}

	// Step 2: enumerate every call in servePostgres, resolve each callee's
	// own parameter types, and record which (call, argument index) pairs
	// are structurally required to carry a *seo.Site-shaped value —
	// discovered from the callee's signature, never from a name list kept
	// here (criterion 4). seoSiteConsumer is defined at package scope so
	// describeConsumers below can share the same type.
	var consumers []seoSiteConsumer
	var resolvedCalleeCount int

	ast.Inspect(servePostgres.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		funcName, dir, ok := resolveCallee(call, thisDir, pkgDirs)
		if !ok {
			return true
		}
		params, paramNames, found := findFuncParamTypes(t, fset, dir, funcName)
		if !found {
			return true
		}
		resolvedCalleeCount++
		for i, paramType := range params {
			if !typeRequiresSEOSite(fset, dir, pkgDirs, paramType) {
				continue
			}
			consumers = append(consumers, seoSiteConsumer{
				call:     call,
				argIndex: i,
				desc:     fmt.Sprintf("%s param %d (%s)", funcName, i, paramNames[i]),
			})
		}
		return true
	})

	// Fail closed (criterion 3): if the scan never even resolved a single
	// callee's parameter list, or found nothing site-shaped among them,
	// something about this scan broke (a package got moved, a function got
	// renamed) rather than servePostgres genuinely having no consumers —
	// that would be a very different, much bigger issue than #0326.
	if resolvedCalleeCount == 0 {
		t.Fatal("resolved zero callee signatures for any call in servePostgres — this scan's " +
			"package/function resolution is broken (fail closed, never treat this as 'nothing to check')")
	}
	if len(consumers) == 0 {
		t.Fatal("found zero *seo.Site-shaped consumer parameters among every call in servePostgres — " +
			"this scan's type-shape matching is broken (fail closed): as of #0338, " +
			"NewAdminWorkshopsHandler, NewAdminCampaignArchiveHandler, and newSendWorkerIfEnabled " +
			"each have one, and mountAndServe's own site *seo.Site parameter is a fourth")
	}

	// Sanity floor, not a hard-coded name list: as of #0338 there are FOUR
	// known consumers, measured by temporarily raising this constant far
	// above the real count and reading the resulting failure's own
	// enumeration (the same describeConsumers output the failure message
	// below prints): NewAdminWorkshopsHandler param 1, newSendWorkerIfEnabled
	// param 10, NewAdminCampaignArchiveHandler param 2, and mountAndServe
	// param 34 (site) — mountAndServe's own site *seo.Site parameter counts
	// too (typeRequiresSEOSite's first case, a literal *seo.Site), which
	// #0326's original count of three missed. This asserts a COUNT, which is
	// allowed to grow (criterion 4 asks for OPEN-ended coverage of a fifth,
	// not a ceiling) — it exists only to catch this guard's own resolution
	// silently starting to miss consumers it used to find: a degradation
	// from 4 consumers to 3 must FAIL, which a floor of 3 (as #0326 first
	// wrote it) could not catch. Re-measure this constant the same way
	// (temporarily set it above the real count, read the failure's own
	// enumeration) rather than incrementing it by hand, if servePostgres
	// gains or loses a genuine consumer.
	const knownConsumersAsOfWriting = 4
	if len(consumers) < knownConsumersAsOfWriting {
		t.Fatalf("found %d *seo.Site-shaped consumer parameters in servePostgres, want at least %d "+
			"(the known count as of #0338) — this scan may have stopped resolving one of them: %s",
			len(consumers), knownConsumersAsOfWriting, describeConsumers(fset, consumers))
	}

	// Step 3: the actual invariant. Every discovered consumer position must
	// receive the identifier bound by buildSEOSite's own call — not nil,
	// not a second variable, not any other expression.
	var violations []string
	for _, c := range consumers {
		if c.argIndex >= len(c.call.Args) {
			violations = append(violations, fmt.Sprintf(
				"%s: %s expects an argument at index %d but the call only has %d",
				fset.Position(c.call.Pos()), c.desc, c.argIndex, len(c.call.Args)))
			continue
		}
		arg := c.call.Args[c.argIndex]
		ident, ok := arg.(*ast.Ident)
		if !ok || ident.Name != siteVar {
			violations = append(violations, fmt.Sprintf(
				"%s: %s got %s, want the identifier %q from buildSEOSite's own call",
				fset.Position(arg.Pos()), c.desc, exprString(arg), siteVar))
		}
	}

	if len(violations) > 0 {
		t.Fatalf("servePostgres does not hand the SAME *seo.Site (%q) to every consumer that needs "+
			"one:\n%s", siteVar, strings.Join(violations, "\n"))
	}
}

// seoSiteConsumer is one (call, argument position) pair, discovered by
// resolveCallee/findFuncParamTypes/typeRequiresSEOSite below, whose callee
// declares a parameter shaped like *seo.Site — see
// TestServePostgres_OneSEOSiteFlowsToEveryConsumer's doc comment.
type seoSiteConsumer struct {
	call     *ast.CallExpr
	argIndex int
	desc     string // "pkg.Func param N (paramName)" for failure messages
}

func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name.Name != name {
			continue
		}
		return fd
	}
	return nil
}

// importDirs maps every import alias in file to the local repository
// directory it resolves to, for this module's own internal/* packages only
// — third-party and stdlib imports (pgxpool, rate, context, ...) are never
// candidates for declaring a *seo.Site-shaped parameter, so they are simply
// absent from the returned map and every lookup against them naturally
// misses.
func importDirs(t *testing.T, file *ast.File) map[string]string {
	t.Helper()
	const modulePrefix = "github.com/brennanMKE/OpenCircuitSF/"
	dirs := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasPrefix(path, modulePrefix) {
			continue
		}
		rel := strings.TrimPrefix(path, modulePrefix)
		alias := filepath.Base(rel)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		dirs[alias] = filepath.Join("..", "..", rel)
	}
	return dirs
}

// resolveCallee identifies the (funcName, directory) a call expression's
// callee lives in: either a package-qualified call (pkg.Func, resolved via
// pkgDirs) or a plain identifier, assumed local to selfDir
// (cmd/opencircuit — package-local helpers like newSendWorkerIfEnabled and
// buildSEOSite itself). Any other call shape (a method call, a call through
// a variable holding a func value, ...) returns ok=false and is simply
// skipped — none of this codebase's *seo.Site consumers are called that
// way, and a call this can't classify is not silently treated as
// site-shaped.
func resolveCallee(call *ast.CallExpr, selfDir string, pkgDirs map[string]string) (funcName, dir string, ok bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name, selfDir, true
	case *ast.SelectorExpr:
		pkgIdent, ok := fn.X.(*ast.Ident)
		if !ok {
			return "", "", false
		}
		dir, known := pkgDirs[pkgIdent.Name]
		if !known {
			return "", "", false
		}
		return fn.Sel.Name, dir, true
	default:
		return "", "", false
	}
}

// findFuncParamTypes parses every non-test .go file in dir looking for a
// top-level (non-method) function or type-constructor named funcName, and
// returns its parameter types and names in call order — expanding grouped
// parameters (`a, b Type`) into one entry per name, since that is the shape
// a call's Args slice uses. found is false if no such function exists in
// dir, which is the ordinary case for the vast majority of calls in
// servePostgres (e.g. workshops.NewStore, mailing.NewAudienceStore) — most
// calls are not *seo.Site consumers, and this function returning found=false
// for them is exactly how the caller's scan skips over the ones that don't
// matter without needing to know their names in advance.
func findFuncParamTypes(t *testing.T, fset *token.FileSet, dir, funcName string) (types []ast.Expr, names []string, found bool) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filepath.Join(dir, name), err)
		}
		fd := findFuncDecl(file, funcName)
		if fd == nil {
			continue
		}
		for _, field := range fd.Type.Params.List {
			paramNames := field.Names
			if len(paramNames) == 0 {
				// Unnamed parameter (interface method signature shape,
				// never how this codebase writes a constructor) — give it
				// a synthetic name so failure messages still make sense.
				types = append(types, field.Type)
				names = append(names, "_")
				continue
			}
			for _, n := range paramNames {
				types = append(types, field.Type)
				names = append(names, n.Name)
			}
		}
		return types, names, true
	}
	return nil, nil, false
}

// typeRequiresSEOSite reports whether expr's declared type is *seo.Site
// itself, or a type — local to dir, or reached through one more
// package-qualified hop resolved via pkgDirs — whose FULL declared method
// set is exactly one method, `Invalidate()`, taking no parameters and
// returning nothing: the exact shape *seo.Site.Invalidate implements
// (internal/seo/site.go). That second case is what lets this guard
// recognize handlers.seoCacheInvalidator (named workshopCacheInvalidator
// until #0335 — a plain Ident, local to dir=internal/handlers, where the
// function declaring it lives) and
// mailing.ArchiveCacheInvalidator (a SelectorExpr, resolved via pkgDirs —
// newSendWorkerIfEnabled is declared in cmd/opencircuit itself, so its
// param types are written using the SAME import aliases main.go's own
// import block establishes, which is exactly what pkgDirs was built from)
// as *seo.Site-shaped without either name ever appearing in this file — any
// interface anywhere pkgDirs can reach, with that same one-method shape, is
// recognized the same way. A SelectorExpr whose package alias is not a key
// in pkgDirs (e.g. it belongs to some other file's own, differently-scoped
// import block) is not resolved and returns false rather than guessing —
// every real consumer parameter in this codebase resolves through pkgDirs,
// built once from main.go, since every *seo.Site-shaped parameter in
// servePostgres's own call graph is either declared in cmd/opencircuit
// itself or referenced from there using cmd/opencircuit's own import
// aliases.
func typeRequiresSEOSite(fset *token.FileSet, dir string, pkgDirs map[string]string, expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		if sel, ok := star.X.(*ast.SelectorExpr); ok {
			if pkgIdent, ok := sel.X.(*ast.Ident); ok && pkgIdent.Name == "seo" && sel.Sel.Name == "Site" {
				return true
			}
		}
		// A pointer to some other named type is never how this codebase's
		// invalidator seam is declared (both known interfaces are used by
		// value, not by pointer) — fall through to false rather than
		// unwrapping further, so a *Foo parameter is never mistaken for a
		// value-typed Foo interface.
		return false
	}

	switch t2 := expr.(type) {
	case *ast.Ident:
		return interfaceIsInvalidateOnly(fset, dir, t2.Name)
	case *ast.SelectorExpr:
		pkgIdent, ok := t2.X.(*ast.Ident)
		if !ok {
			return false
		}
		resolvedDir, known := pkgDirs[pkgIdent.Name]
		if !known {
			return false
		}
		return interfaceIsInvalidateOnly(fset, resolvedDir, t2.Sel.Name)
	default:
		return false
	}
}

// interfaceIsInvalidateOnly reports whether dir declares
// `type name interface { Invalidate() }` — exactly one method, named
// Invalidate, no parameters, no results — searching every non-test .go file
// in dir. #0338: until then this checked parameters only, so an interface
// shaped `Invalidate() error` (which does NOT structurally match
// *seo.Site.Invalidate's own zero-result signature, and therefore should
// NOT be mistaken for a *seo.Site-shaped consumer) would have incorrectly
// passed — no live interface has ever had that shape, so it was never
// observed, but the doc comment already claimed "no results" was checked.
// The function now checks results too, closing that gap rather than
// weakening the comment to match the old behavior.
func interfaceIsInvalidateOnly(fset *token.FileSet, dir, name string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		fname := e.Name()
		if e.IsDir() || !strings.HasSuffix(fname, ".go") || strings.HasSuffix(fname, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, fname), nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != name {
					continue
				}
				iface, ok := ts.Type.(*ast.InterfaceType)
				if !ok || len(iface.Methods.List) != 1 {
					return false
				}
				m := iface.Methods.List[0]
				if len(m.Names) != 1 || m.Names[0].Name != "Invalidate" {
					return false
				}
				ft, ok := m.Type.(*ast.FuncType)
				if !ok {
					return false
				}
				noParams := ft.Params == nil || len(ft.Params.List) == 0
				noResults := ft.Results == nil || len(ft.Results.List) == 0
				return noParams && noResults
			}
		}
	}
	return false
}

func positions(fset *token.FileSet, calls []*ast.CallExpr) string {
	var out []string
	for _, c := range calls {
		out = append(out, fset.Position(c.Pos()).String())
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func describeConsumers(fset *token.FileSet, consumers []seoSiteConsumer) string {
	var out []string
	for _, c := range consumers {
		out = append(out, fmt.Sprintf("%s (%s)", fset.Position(c.call.Pos()), c.desc))
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	default:
		return fmt.Sprintf("%T", e)
	}
}
