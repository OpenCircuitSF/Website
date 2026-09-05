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

// envVarNameLiteral matches the shape of an environment variable name, not
// any particular way of writing one down. See the #0428 doc block below for
// why the match is on shape rather than on which function is called.
var envVarNameLiteral = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// TestEnvExampleCoversLoaderVariables guards against .env.example silently
// falling behind config.go's loadFromFile — the exact defect #0423 was filed
// over. docs/deployment.md's install step is `sudo cp .env.example
// /etc/opencircuit/config.env`, so .env.example is not a reference; it is
// what production gets. STORAGE and SES_EVENTS_TOPIC_ARN were both read by
// loadFromFile and absent from .env.example before #0423, and nothing caught
// it — this test is that catch.
//
// The oracle is config.go itself, re-derived by parsing its AST for every
// call whose first argument is a string literal shaped like an environment
// variable name (envVarNameLiteral: `^[A-Z][A-Z0-9_]*$`) — not a hand-copied
// list of names living next to the thing it checks (CLAUDE.md §8: "a guard's
// oracle must not be the same bytes as its subject"). Mutating config.go's
// scan root — adding a new call reading a variable — changes what this test
// extracts and so changes `got`, which is exactly the shape CLAUDE.md §8
// calls a legitimate, non-circular in-package Go check ("mutate the scan
// roots and `got` itself changes").
//
// #0428: the extraction used to key off the CALLEE's name (matching only
// "Getenv", "getInt", "getBool"), which let a same-package refactor shrink
// `got` without changing what it means to read a variable. Routing the 14
// direct string reads through a one-line wrapper —
//
//	func env(name string) string { return os.Getenv(name) }
//
// — is a plausible refactor that is a pure rename from the loader's point of
// view, and it dropped the callee-name match from 20 extracted names to 6
// (only getInt/getBool survived), after which five variables — including
// EMAIL_FROM and SESSION_SECRET — could be deleted from .env.example with
// this test still exiting 0. Reproduced in a throwaway worktree per the
// issue's acceptance criterion 1; see its `## Verification`.
//
// The fix is not a floor on len(names): a floor is a number that can itself
// drift out of sync with config.go and only ever tells you extraction
// shrank, not why. Instead, matching moved off the callee identifier
// entirely and onto the ARGUMENT's shape — any call, regardless of what it
// is named, whose first argument is a string literal that looks like an
// environment variable name. `env("EMAIL_FROM")`, `os.Getenv("EMAIL_FROM")`,
// and `someFutureHelper("EMAIL_FROM")` all extract identically, because none
// of them can rename away the one thing that has to stay a literal for
// loadFromFile to compile: the name being read. This does not require a
// second mechanism (CLAUDE.md §8's #0258 warning) — it is the same single
// AST walk with its match condition changed, and it makes the measured
// fail-open unrepresentable rather than merely detecting it after the fact
// (CLAUDE.md's "prefer making the bad state unrepresentable" guidance, per
// #0400). It does not cover every conceivable refactor — a table-driven
// rewrite that assembles names outside any call argument (e.g. as elements
// of a slice literal) would still evade both the old extraction and this
// one — but that is a materially different, more invasive rewrite than the
// one #0423's review actually produced, and the original guard never covered
// it either, so this is not a regression in what is checked.
//
// This only proves loader ⊆ .env.example (every variable the loader reads
// appears as a `NAME=` line somewhere in the file) — it says nothing about
// whether the shipped value works, is required, or is a secret; those are
// judgment calls #0423's issue file and docs/configuration.md's table make
// by hand, deliberately not automated (CLAUDE.md §8's ceiling-vs-floor
// distinction: whether a value is safe to publish is not a fact `got` can
// observe). It also does not examine the VALUE side of a `NAME=value` line
// at all — whether that value parses the same way under both systemd's
// EnvironmentFile= and godotenv is a different gap, found by #0429's review
// and left to that issue's own judgment call rather than folded in here.
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

		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil || !envVarNameLiteral.MatchString(name) {
			return true
		}
		names[name] = true
		return true
	})

	// An empty extraction means the AST walk or the envVarNameLiteral match
	// broke, not that config.go reads nothing — assert this before trusting
	// the result, per CLAUDE.md §8's "assert the extraction produced
	// something before hashing/using it" rule.
	if len(names) == 0 {
		t.Fatal("extracted zero environment variable names from config.go — the AST walk or the envVarNameLiteral match is broken, not evidence config.go reads nothing")
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
