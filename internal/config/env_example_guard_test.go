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
// Matching moved off the callee identifier entirely and onto the ARGUMENT's
// shape — any call, regardless of what it is named, whose first argument is
// a string literal that looks like an environment variable name.
// `env("EMAIL_FROM")`, `os.Getenv("EMAIL_FROM")`, and
// `someFutureHelper("EMAIL_FROM")` all extract identically. This closes the
// wrapper-rename case above without a second mechanism (CLAUDE.md §8's
// #0258 warning) — it is the same single AST walk with its match condition
// changed.
//
// It does NOT make the fail-open unrepresentable, and an earlier version of
// this comment claimed it did; #0428's review measured that claim false. A
// named constant compiles and stops being a literal call argument: hoisting
// five of the deleted-in-review variable names into a const block (config.go
// already has one, for its numeric defaults) and reading
// os.Getenv(envEmailFrom) drops the extraction from 20 names to 15, compiles,
// is gofmt/vet-clean, and leaves EMAIL_FROM, SESSION_SECRET, STORAGE,
// SES_EVENTS_TOPIC_ARN, and ADMIN_EMAIL free to vanish from .env.example
// undetected. A second shape — assembling a subset of names as elements of a
// []string{...} literal and reading them in a loop — drops the extraction
// 20 -> 18 the same way, with no missing variable required at all. Both
// compile and are gofmt/vet-clean; neither is the "materially different,
// more invasive rewrite" an earlier version of this comment used to wave off
// the slice-literal shape.
//
// What actually closes both, and any other shape that shrinks the
// extraction rather than just these two, is minLoaderVariables below: a
// floor on len(names), scored by go test's exit code, that fails whenever
// the walk sees fewer literal-shaped calls than config.go is known to make
// today. A floor was originally rejected in favor of the argument-shape
// predicate alone, on the reasoning that a floor is "one more constant that
// can drift" and only ever detects a shrink after the fact. That reasoning
// was not wrong about the floor; it was incomplete about the predicate,
// which narrows the set of evading shapes rather than eliminating it. The
// floor and the predicate are not redundant: the predicate is what makes a
// same-argument rename (the wrapper) extract identically to the original;
// the floor is what catches every OTHER way of shrinking got — including the
// const-hoist and slice-literal shapes above, and any shape neither this
// comment nor #0428's review anticipated — because it observes the count
// directly instead of trying to enumerate syntax.
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

	// minLoaderVariables replaces the old "extraction produced literally
	// nothing" check with a real threshold, added by #0428's review after the
	// argument-shape predicate above was shown to still evade under a
	// const-identifier hoist (20 -> 15) and a slice-literal loop (20 -> 18) —
	// see the doc comment. config.go reads exactly 20 variables today,
	// verified in #0428's review by enumerating every CallExpr in the file
	// whose first argument is a string literal (the 20 counted here, plus two
	// Errorf formats, three Sprintf formats, and loadFromFile(".env"...),
	// none of which match envVarNameLiteral). Falling below the floor means
	// the walk stopped seeing reads it used to see, not that config.go reads
	// fewer variables — assert this before trusting the result, per
	// CLAUDE.md §8's "assert the extraction produced something before
	// hashing/using it" rule. Lower this constant only when a variable is
	// genuinely and deliberately removed from config.go — a loud, reviewed
	// edit — never to make a shrink go quiet.
	const minLoaderVariables = 20

	if len(names) < minLoaderVariables {
		t.Fatalf("extracted %d environment variable name(s) from config.go, below the floor of %d — the AST walk or the envVarNameLiteral match stopped seeing reads it used to see, which is not evidence config.go reads fewer variables", len(names), minLoaderVariables)
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
