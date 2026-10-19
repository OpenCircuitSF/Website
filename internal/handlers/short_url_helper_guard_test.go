package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestShortLinkInsertsUseShortURLHelper is #0603's guard. ShortLinks serves
// redirects only at /u/{key}, and this repo once stored base+"/"+key, which
// produced dead links in live posts. Rather than hunt for every spelling of a
// bad concatenation, assert the narrow fact that matters: the short-URL
// argument of every non-test InsertPostLink / InsertFlyerLink call is built by
// shortlinks.ShortURL. The one other accepted shape is a dereferenced
// ShortURL field, which re-records a value an existing row already carries.
func TestShortLinkInsertsUseShortURLHelper(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	fset := token.NewFileSet()
	var viaHelper, viaStored int
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			f, perr := parser.ParseFile(fset, path, src, 0)
			if perr != nil {
				return perr
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				sel, isSel := call.Fun.(*ast.SelectorExpr)
				if !isSel || (sel.Sel.Name != "InsertPostLink" && sel.Sel.Name != "InsertFlyerLink") || len(call.Args) == 0 {
					return true
				}
				last := call.Args[len(call.Args)-1]
				pos := fset.Position(call.Pos())
				switch a := last.(type) {
				case *ast.CallExpr:
					if s, ok := a.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "ShortURL" {
						if x, ok := s.X.(*ast.Ident); ok && x.Name == "shortlinks" {
							viaHelper++
							return true
						}
					}
				case *ast.StarExpr:
					if s, ok := a.X.(*ast.SelectorExpr); ok && s.Sel.Name == "ShortURL" {
						viaStored++
						return true
					}
				}
				t.Errorf("%s: %s's short URL argument must be shortlinks.ShortURL(base, key) (#0603)", pos, sel.Sel.Name)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if viaHelper < 3 {
		t.Errorf("found %d helper-built call sites, want at least 3: the guard is scanning the wrong tree", viaHelper)
	}
}
