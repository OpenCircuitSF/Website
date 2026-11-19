// Package publicapitest holds test helpers for the public API packages
// (#0729). Only _test.go files import it.
package publicapitest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// bannedImports are the admin-auth packages a public package must never
// import. A prefix match, so internal/mcpserver/mcptest counts too.
var bannedImports = []string{
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver",
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens",
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers",
	"github.com/modelcontextprotocol/go-sdk/auth",
}

// bannedSelectors are the token middleware's names.
var bannedSelectors = map[string]bool{
	"RequireAPIToken": true, "APITokenRateLimit": true, "APIAuthFailureLimit": true,
	"APITokenFromContext": true, "WithAPIToken": true,
}

// AdminAuthViolations parses every non-test .go file directly in dir and
// lists each banned import and each middleware.<banned> selector. An
// unreadable or unparsable file is itself reported.
func AdminAuthViolations(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	files := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			out = append(out, err.Error())
			continue
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			for _, b := range bannedImports {
				if p == b || strings.HasPrefix(p, b+"/") {
					out = append(out, fmt.Sprintf("%s imports %s", fset.Position(imp.Pos()), p))
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "middleware" && bannedSelectors[sel.Sel.Name] {
				out = append(out, fmt.Sprintf("%s uses middleware.%s", fset.Position(sel.Pos()), sel.Sel.Name))
			}
			return true
		})
	}
	if files == 0 {
		out = append(out, "no non-test .go files in "+dir)
	}
	return out
}
