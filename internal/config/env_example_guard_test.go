package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

// TestEnvExampleCoversLoaderVariables guards against .env.example silently
// falling behind config.go's loadFromFile — the exact defect #0423 was filed
// over. docs/deployment.md's install step is `sudo cp .env.example
// /etc/opencircuit/config.env`, so .env.example is not a reference; it is
// what production gets. STORAGE and SES_EVENTS_TOPIC_ARN were both read by
// loadFromFile and absent from .env.example before #0423, and nothing caught
// it — this test is that catch.
//
// The oracle is config.go itself, re-derived by parsing its AST for every
// os.Getenv/getInt/getBool call whose first argument is a string literal,
// not a hand-copied list of names living next to the thing it checks
// (CLAUDE.md §8: "a guard's oracle must not be the same bytes as its
// subject"). Mutating config.go's scan root — adding a new call reading a
// variable — changes what this test extracts and so changes `got`, which is
// exactly the shape CLAUDE.md §8 calls a legitimate, non-circular in-package
// Go check ("mutate the scan roots and `got` itself changes").
//
// This only proves loader ⊆ .env.example (every variable the loader reads
// appears as a `NAME=` line somewhere in the file) — it says nothing about
// whether the shipped value works, is required, or is a secret; those are
// judgment calls #0423's issue file and docs/configuration.md's table make
// by hand, deliberately not automated (CLAUDE.md §8's ceiling-vs-floor
// distinction: whether a value is safe to publish is not a fact `got` can
// observe).
func TestEnvExampleCoversLoaderVariables(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "config.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing config.go: %v", err)
	}

	names := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}

		var fname string
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			fname = fn.Name
		case *ast.SelectorExpr:
			fname = fn.Sel.Name
		default:
			return true
		}
		// loadFromFile reads every variable through one of these three
		// entry points: os.Getenv directly for string fields, getInt/getBool
		// for typed ones with a default. See config.go.
		if fname != "Getenv" && fname != "getInt" && fname != "getBool" {
			return true
		}

		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		names[name] = true
		return true
	})

	// An empty extraction means the AST walk or the Getenv/getInt/getBool
	// name match broke, not that config.go reads nothing — assert this
	// before trusting the result, per CLAUDE.md §8's "assert the extraction
	// produced something before hashing/using it" rule.
	if len(names) == 0 {
		t.Fatal("extracted zero environment variable names from config.go — the AST walk or the Getenv/getInt/getBool match is broken, not evidence config.go reads nothing")
	}

	envExample, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatalf("reading .env.example: %v", err)
	}

	varLine := regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]*)=`)
	present := map[string]bool{}
	for _, m := range varLine.FindAllSubmatch(envExample, -1) {
		present[string(m[1])] = true
	}

	var missing []string
	for name := range names {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("config.go's loader reads %d variable(s) not present in .env.example: %v — docs/deployment.md's install step copies .env.example verbatim onto production (sudo cp .env.example /etc/opencircuit/config.env), so an omission here ships silently", len(missing), missing)
	}
}
