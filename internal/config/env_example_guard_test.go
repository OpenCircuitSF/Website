package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
	// see the doc comment; those two figures are #0428's own review-time
	// measurements and are not re-derived here. The constant has climbed
	// twice since #0428 set it at 20: #0433's MEDIA_DIR took the real count
	// to 21 without a matching bump, and #0402 raised the constant itself to
	// 22 after its implementer re-derived the count from this same AST walk
	// rather than trust the doc comment that used to sit here. That comment
	// asserted config.go "reads exactly 22 variables today" — an exact-count
	// claim a floor can never keep true, since growth clears a `<` check
	// silently by design. #0483 removed the claim, not the floor: the floor
	// did its job throughout, catching nothing because nothing shrank, and
	// the stale prose beside it is what misled #0402's planning pass toward
	// prescribing the wrong number. This comment therefore names no current
	// count on purpose; the constant below is the only number that has to
	// stay accurate. Falling below the floor means the walk stopped seeing
	// reads it used to see, not that config.go reads fewer variables —
	// assert this before trusting the result, per CLAUDE.md §8's "assert the
	// extraction produced something before hashing/using it" rule. Lower
	// this constant only when a variable is genuinely and deliberately
	// removed from config.go — a loud, reviewed edit — never to make a
	// shrink go quiet.
	const minLoaderVariables = 22

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

// envAssignmentLine matches a KEY=VALUE line as either parser would recognize
// it as the start of an assignment: an optional "export " prefix (godotenv
// strips it per joho/godotenv v1.5.1's locateKeyName; systemd's
// EnvironmentFile= has no such keyword, so the same line is either a syntax
// error there or is read as a literal key literally named "export FOO") and
// then a name shaped like envVarNameLiteral above.
var envAssignmentLine = regexp.MustCompile(`^(export[ \t]+)?([A-Z][A-Z0-9_]*)=(.*)$`)

// envTrailingComment matches the #0429 shape: a "#" preceded by whitespace,
// anywhere in the value. This mirrors godotenv's own backward scan in
// extractVarValue (parser.go: "line[i] == charComment && i > 0 &&
// isSpace(line[i-1])") rather than a hand-rolled guess at what counts as a
// trailing comment, so the shape this test flags is exactly the shape
// godotenv strips. A bare "#" with no preceding space (e.g. inside a URL
// fragment or a password) is NOT this shape and is not flagged — godotenv
// itself would not treat it as a comment either, so there is no divergence.
var envTrailingComment = regexp.MustCompile(`[ \t]#`)

// envDollarExpansion matches $VAR, ${VAR}, and $(VAR) syntax in a value. This
// is #0453's extension of #0450's widening, not a fifth shape: it is the same
// "godotenv expands, systemd doesn't" divergence, just admitting one more
// prefix — `\(?` — into the same character-class-and-structure story, exactly
// as #0450 widened the name class itself. The violation site below still
// covers it inside the existing $VAR/${VAR} check, not a fifth branch.
//
// The `\(?` mirrors joho/godotenv v1.5.1's own expandVarRegex verbatim
// (parser.go: `(\\)?(\$)(\()?\{?([A-Z0-9_]+)?\}?`) — an optional backslash,
// the dollar, an optional open paren, an optional open brace, the name class
// (uppercase letters, digits, underscore, digit-led permitted), an optional
// close brace. Before #0453 this pattern started matching only at `\$\{?`,
// so a value with a literal `(` between the dollar and the name — the
// `$(FOO)` shape — was invisible to the scan entirely: `\{?` only makes the
// brace optional, it does not also make an intervening `(` optional.
//
// $(FOO) looks like it ought to be a no-op to both parsers (neither this
// project's config.go nor systemd's EnvironmentFile= documents any command
// substitution), but godotenv v1.5.1 has a bug that makes it silently
// misparse: expandVariables (parser.go) is meant to leave the `$(…)` form
// alone, and tries to detect it with `submatch[2] == "("`. But in
// expandVarRegex's own group numbering, group 2 is the **dollar** capture —
// always literally "$", never "(" — and the paren is group 3, which nothing
// tests. That branch is therefore unreachable dead code, so `$(FOO` falls
// through to ordinary variable expansion, reads "FOO" as a variable name
// (substituting the empty string if undefined, same as any other unknown
// $VAR), and leaves any trailing `)` as literal text — which is why
// `PRICE=cost$(FOO)` loads as `cost)` rather than `cost` or `cost$(FOO)`.
// Verified by probing godotenv.Unmarshal directly, in a throwaway module
// against the real v1.5.1 dependency (never by reasoning from the regex
// alone — CLAUDE.md §5, and the exact way the group-numbering bug was
// missed the first time):
//
//	value              godotenv loads   diverges from systemd's literal text?
//	cost$(FOO)         cost)            yes
//	cost$(FOO          cost             yes
//	cost$(FOO}         cost             yes
//	cost$(FOO})        cost)            yes
//	cost$(5)           cost)            yes (digit-led name, same as #0450)
//	cost${FOO)         cost)            yes (already caught pre-#0453: the
//	                                    optional `{` sits directly after `$`
//	                                    with no intervening `(`, so the
//	                                    pre-#0453 pattern already matched
//	                                    "${FOO")
//	cost$()            cost$()          no  (no name characters after the
//	                                    parenthesis; expandVariables returns
//	                                    the unexpanded text unchanged, and the
//	                                    widened pattern's required
//	                                    `[A-Z0-9_]+` correctly does not match
//	                                    here either)
//	cost$((FOO))       cost$((FOO))     no  (the SECOND "(" is not a valid
//	                                    name character, so godotenv's own
//	                                    regex fails to match starting at the
//	                                    first "$" at all; the widened pattern
//	                                    agrees and does not flag it)
//
// #0457 widens this once more — not by making the leading backslash optional
// on the existing alternative, but by adding a second alternative to the same
// variable: `\\\$`, a backslash immediately followed by a dollar sign. A
// prefix form — splicing an optional `\\?` onto the existing
// `\$\(?\{?[A-Z0-9_]+\}?` alternative — was considered and rejected: it would
// still require a variable name to follow the dollar sign, so it would miss
// the bare backslash-dollar case entirely and reproduce the pre-#0457 counts
// exactly. This is not a fifth shape or a second pattern: still the "godotenv
// treats a dollar sign specially, systemd doesn't" mechanism, still one
// FindString call at the one violation site below. This closes the family
// #0453's reviewer measured and reported but did not fix: a backslash sitting
// directly in front of a "$" anywhere in the value.
//
// This is distinct from the trailing-backslash CONTINUATION check later in
// this file (the very last check in scanEnvExampleValueShapes, which tests
// whether value itself ends in a single backslash character): that one
// fires on a backslash at the END of the value with no dollar sign
// involved, and it is systemd doing something special there (line
// continuation) while godotenv does nothing. This one
// fires on a backslash immediately BEFORE a dollar sign, anywhere in the
// value, and it is godotenv doing something special (silently stripping the
// backslash) while systemd does nothing. Neither shape subsumes the other —
// a value can trip one, both, or neither — so the two stay two separate
// checks with two separate messages, and each message names its own parser
// as the one behaving unusually rather than leaving an operator to guess
// which backslash is meant.
//
// The mechanism, read from expandVariables (parser.go):
//
//	if submatch[1] == "\\" || submatch[2] == "(" {
//		return submatch[0][1:]
//	} else if submatch[4] != "" {
//		return m[submatch[4]]
//	}
//	return s
//
// submatch[1] is the optional leading backslash (expandVarRegex's first
// group). Whenever ANY match of expandVarRegex has that group present,
// expandVariables strips exactly the one matched backslash character and
// returns the rest of the match completely unchanged — no substitution
// happens even when a valid variable name follows, and this fires even for
// a bare backslash-dollar with nothing after it at all, since every group
// after the dollar is independently optional. That unconditional behavior
// is why the widened pattern below does not also require a name to follow a
// backslash-prefixed dollar, unlike the plain (non-backslash) alternative,
// which still does: a bare "$" with no name after it does not diverge
// (#0450's review), so keeping the name required there is still correct.
//
// Verified by probing godotenv.Unmarshal directly, in a throwaway module
// against the real v1.5.1 dependency (never by reasoning from the regex
// alone — CLAUDE.md §5):
//
//	value          godotenv loads  diverges?
//	cost\$FOO      cost$FOO        yes (backslash stripped; unlike the
//	                               no-backslash "cost$FOO" case above, the
//	                               "FOO" is left literal, NOT substituted)
//	cost\${FOO}    cost${FOO}      yes (brace form, same strip-only result)
//	cost\$(FOO)    cost$(FOO)      yes (the backslash SUPPRESSES the
//	                               $(...) misparse bug documented above —
//	                               submatch[1] is checked before
//	                               submatch[2], so this strips and stops
//	                               rather than also misparsing)
//	cost\$5        cost$5          yes (digit-led name, same as #0450)
//	cost\$         cost$           yes (bare backslash-dollar, no name
//	                               characters at all, still diverges — the
//	                               case the required-name half of the
//	                               pattern must NOT apply to)
//	cost\n         cost\n          no  (a literal backslash followed by the
//	                               letter "n", not a newline; the backslash
//	                               is not immediately before a "$", so
//	                               expandVarRegex — which requires a literal
//	                               "$" right after its optional backslash —
//	                               never matches here at all)
//	cost\\$FOO     cost\$FOO       yes (only the backslash directly
//	                               adjacent to the dollar sign is consumed;
//	                               an earlier, non-adjacent backslash is
//	                               untouched — still diverges, and still
//	                               caught, since the pattern only needs to
//	                               find one qualifying pair)
//
// Re-derived over the exact 111,110-value corpus #0453's reviewer built (the
// alphabet `$ ( ) { } A 5 _ \ x`, every combination of length 1 through 5,
// each appended to a "cost" prefix, each run through the real
// godotenv.Unmarshal and compared against the literal text systemd would
// keep): the pre-#0457 pattern flags 14,637 of them with zero false
// positives, and of the 3,024 that genuinely diverge and go unflagged, every
// one contains a backslash — #0453's finding, reproduced here rather than
// trusted. The widened pattern below flags all 17,661 true divergences in
// that same corpus (14,637 + the 3,024 it was missing) with zero false
// positives and zero remaining misses.
var envDollarExpansion = regexp.MustCompile(`\\\$|\$\(?\{?[A-Z0-9_]+\}?`)

// envQuotedValuePrefix is #0461: it matches a value whose first
// non-whitespace character is a single or double quote — the exact
// condition joho/godotenv v1.5.1's hasQuotePrefix (parser.go) checks, on the
// same already-leading-whitespace-trimmed text locateKeyName hands it, to
// decide whether to parse a value as QUOTED at all rather than as the plain
// unquoted text the three checks below (trailing comment, dollar expansion,
// trailing backslash) are written against and were proved against.
//
// #0457's reviewer swept an extended alphabet — this file's original ten
// characters ($ ( ) { } A 5 _ \ x) plus ', ", a space, #, and a lowercase
// letter — through the real library and the scan: still zero false
// positives, and a further set of genuine, unflagged divergences. #0461
// re-derived that sweep independently (see issues/0461.md's ## Work log for
// the corpus construction and counts, which differ from the earlier
// review's own count because that review's exact corpus is not reproducible
// from its write-up alone — CLAUDE.md §8's "every number inherited without
// measurement in this lineage has been wrong at least once" is exactly why
// this was re-run rather than copied). That finding is oracle-dependent, and
// the dependence is the point. Against a systemd model that keeps the raw
// bytes — the model the four shapes above were proved against — the
// unflagged divergences over the extended alphabet are dominated by
// whitespace rather than quoting, and this fifth shape closes none of them.
// Against a systemd model that trims leading and trailing whitespace the way
// godotenv's unquoted branch does, every remaining unflagged divergence is
// quote-led and this shape closes all of them. Which model is right is a
// systemd question this environment cannot settle — the same reason #0461
// chose refusal over modelling. Both counts, and the whitespace family's
// disposition, are in issues/0461.md.
//
// Read from extractVarValue (parser.go) and confirmed by probing
// godotenv.Unmarshal directly — never by reasoning from the source alone
// (CLAUDE.md §5) — a leading quote changes the parsing path entirely:
//
//   - Only the quote character itself is trimmed from each end, not
//     whitespace. Interior whitespace, including a space adjacent to either
//     quote, survives untouched: `'  x  '` loads as `"  x  "`, not `"x"` —
//     unlike the unquoted path a few lines below, which also trims
//     whitespace at both ends of the value.
//   - A single-quoted value gets NO escape processing and NO variable
//     expansion at all: `'cost$FOO'` loads as the literal `cost$FOO` —
//     unlike the identical text unquoted, which the dollar-expansion check
//     below would correctly flag as expanding.
//   - A double-quoted value runs expandEscapes before expandVariables:
//     `"cost\n"` loads with an actual newline byte, not the two characters
//     backslash-then-n; `"cost\t"` loads as the single literal character
//     `t` (unescapeCharsRegex strips the backslash from any escape that
//     is not `\n`, `\r`, or `\$`); and a literal `$` still interacts with a
//     preceding backslash exactly as #0457 documented, since
//     expandVariables still runs afterward on the already-unescaped text.
//   - An unterminated quote is not a silent divergence at all — it is a
//     hard parse error (Unmarshal fails outright), the boot-failure half of
//     this bug class (#0429) rather than the silently-wrong-value half the
//     rest of this file targets.
//
// None of this can be assumed to match whatever systemd's EnvironmentFile=
// does with the same quote characters. systemd's quoting rules are a
// separate grammar — not the one #0450's, #0453's and #0457's reviews
// verified is at least internally consistent for godotenv's own expansion
// and escaping — and this repository has no systemd parser to probe the way
// CLAUDE.md §5 and this issue's own criterion 3 require for godotenv.
// Modelling both grammars and keeping them in lockstep is exactly the shape
// that has produced five issues in this lineage (#0441, #0450, #0453,
// #0457, #0461) for ONE mechanism (dollar expansion) alone. #0461 decided
// not to build a sixth: quoted values are forbidden outright in this file,
// the same policy #0441 already chose for "export " (CLAUDE.md §8, #0258 —
// no second mechanism where a blanket rule closes the whole family).
//
// This is deliberately a FIFTH shape, not a widening of one of the other
// four — unlike #0453's and #0457's extensions, it shares no mechanism with
// "godotenv expands a bare $, systemd doesn't"; it is "godotenv parses a
// quoted value under rules this scan does not encode, and systemd's rules
// for the same bytes are unverified and likely different." Forbidding it
// outright means the other three value-side checks never again need to
// reason about quoting: a quoted value is caught and reported on here,
// before it can reach them (see the early continue at the call site) — so
// their own matching logic keeps meaning exactly what its own doc comments
// already say it means, which was written assuming unquoted text.
var envQuotedValuePrefix = regexp.MustCompile(`^[ \t]*(['"])`)

// scanEnvExampleValueShapes walks .env.example-shaped content line by line
// and reports every KEY=VALUE line whose shape the two parsers named in
// #0441 — systemd's EnvironmentFile= and joho/godotenv v1.5.1 (the library
// config.go's loadFromFile actually uses) — would read differently. It
// returns the number of assignment-shaped lines it recognized (for the
// caller's own fail-closed floor check, CLAUDE.md §8) and one formatted
// violation string per offending line, naming the line number, the variable,
// and which parser would disagree and how.
//
// This function does not itself decide what a "plausible" scanned count is —
// see minScannedEnvExampleAssignments at the call site — so it stays a pure,
// directly-testable scan with no threshold baked in, and
// TestEnvExampleValueShapeFloorFailsClosedOnEmptyExtraction can drive it with
// deliberately empty content without that threshold getting in the way.
func scanEnvExampleValueShapes(content []byte) (scanned int, violations []string) {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")

	for i, raw := range lines {
		lineNo := i + 1
		trimmed := strings.TrimLeft(raw, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue // blank line or a full-line comment, not an assignment
		}

		m := envAssignmentLine.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		scanned++

		exportPrefix, name, value := m[1], m[2], m[3]

		if exportPrefix != "" {
			violations = append(violations, fmt.Sprintf(
				"line %d (%s): starts with %q — godotenv accepts and strips an \"export \" prefix (joho/godotenv v1.5.1 locateKeyName), while systemd's EnvironmentFile= has no such keyword and either rejects the line outright or treats \"export %s\" as the literal key name; drop the \"export \" prefix",
				lineNo, name, strings.TrimRight(exportPrefix, " \t")+" ", name))
		}

		if qm := envQuotedValuePrefix.FindStringSubmatch(value); qm != nil {
			quoteKind := "double"
			if qm[1] == "'" {
				quoteKind = "single"
			}
			violations = append(violations, fmt.Sprintf(
				"line %d (%s): value %q begins with a %s-quote character — joho/godotenv v1.5.1 (parser.go's hasQuotePrefix/extractVarValue) parses a quoted value under its own rules (stripping only the quote characters rather than surrounding whitespace, skipping escape processing and expansion entirely for a single-quoted value, and running escape processing before expansion for a double-quoted one), while systemd's EnvironmentFile= applies its own, separate quoting grammar to the same bytes; this scan does not model either grammar closely enough to say whether the two would agree, so quoted values are forbidden here rather than modeled (#0461) — write %s unquoted",
				lineNo, name, value, quoteKind, name))
			continue // #0461: the three checks below assume the UNQUOTED
			// parsing path (see envQuotedValuePrefix's doc comment) — running
			// them against a quoted value's raw text would either mis-describe
			// why it diverges or flag a divergence that quoting itself already
			// fully explains.
		}

		if envTrailingComment.MatchString(value) {
			violations = append(violations, fmt.Sprintf(
				"line %d (%s): value %q has a trailing \"<space>#\" comment on the same line — systemd's EnvironmentFile= has no mid-line comment syntax and includes everything after '=' verbatim, so the comment becomes part of %s's value, while godotenv strips it; move the comment to its own preceding line (the #0429 fix for MAX_SEND_RATE/SEND_WORKER_ENABLED)",
				lineNo, name, value, name))
		}

		if match := envDollarExpansion.FindString(value); match != "" {
			switch {
			case strings.HasPrefix(match, `\`):
				// #0457: a backslash sitting directly in front of a "$",
				// anywhere in the value. Worded separately from both cases
				// below, and from the trailing-backslash CONTINUATION check
				// further down in this function — see envDollarExpansion's
				// doc comment for why none of the three is the other. Here
				// godotenv is the one behaving unusually (silently
				// dropping the backslash and substituting nothing), so the
				// message names godotenv's behavior first rather than
				// leading with "expansion", which this case never performs.
				violations = append(violations, fmt.Sprintf(
					"line %d (%s): value %q has a backslash immediately before a \"$\" — joho/godotenv v1.5.1 (parser.go's expandVariables, via expandVarRegex's optional leading backslash group) silently strips exactly that one backslash and leaves the dollar sign it precedes as literal text with no substitution for that occurrence, while systemd's EnvironmentFile= has no backslash-escaping at all and keeps the backslash byte-for-byte; this is NOT the trailing-backslash line-continuation shape (that one is about a backslash at the END of the value, not one sitting in front of a \"$\") — the two parsers would assign different values to %s; remove the backslash",
					lineNo, name, value, name))
			case strings.Contains(match, "("):
				// #0453: the $(VAR) shape. Worded separately from the
				// plain $VAR/${VAR} case below because the fact is more
				// surprising and the fix is different — this isn't a
				// documented feature being misused, it is a library bug
				// (see envDollarExpansion's doc comment for the exact
				// group-numbering defect in godotenv's expandVariables).
				violations = append(violations, fmt.Sprintf(
					"line %d (%s): value %q contains $(...) syntax, which looks like it should be a no-op but is NOT one — joho/godotenv v1.5.1 has a bug (parser.go's expandVariables checks the wrong regex capture group to detect the parenthesis) that makes it silently read the text after '(' as a variable name instead, substituting its value (empty if undefined elsewhere in this file) and leaving any trailing ')' as literal text, while systemd's EnvironmentFile= performs no expansion at all and keeps the value exactly as written; the two parsers would load completely different values for %s — remove the $(...) syntax",
					lineNo, name, value, name))
			default:
				violations = append(violations, fmt.Sprintf(
					"line %d (%s): value %q contains $VAR/${VAR} syntax — joho/godotenv v1.5.1 (parser.go's expandVariables) expands this against variables assigned earlier in the SAME file (silently substituting an empty string if the name isn't yet defined there), while systemd's EnvironmentFile= performs no expansion at all and keeps it as a literal dollar sign; the two parsers would load different values for %s",
					lineNo, name, value, name))
			}
		}

		if strings.HasSuffix(value, `\`) {
			violations = append(violations, fmt.Sprintf(
				"line %d (%s): value %q ends in a trailing backslash — systemd's EnvironmentFile= treats this as a line continuation and merges it with the following line, while godotenv reads only to end of line and keeps the backslash as part of the value; the two parsers would assign completely different values to %s",
				lineNo, name, value, name))
		}
	}

	return scanned, violations
}

// minScannedEnvExampleAssignments is the fail-closed floor for
// scanEnvExampleValueShapes (CLAUDE.md §8: "assert the scan saw a plausible
// number of lines before concluding anything" — the exact lesson #0258 and
// #0428 both taught the hard way, the latter in this same package). Its
// prose used to carry the same defect #0483 found and removed from
// minLoaderVariables above: it asserted .env.example "carries exactly 20
// 'NAME=' lines today," an exact-count claim beside this same shape of
// floor. #0433's MEDIA_DIR and #0402's DEV_ADMIN_LOGIN each added a line
// without a matching bump here, so that claim had already drifted false the
// same way — undetected only because a floor of 20 still passes at the real
// count. This sentence names no current count on purpose; a scan that
// recognizes fewer than the constant below stopped seeing lines it used to
// see, not evidence the file shrank. Lower it only when a variable is
// genuinely and deliberately removed from .env.example — a loud, reviewed
// edit — never to make a shrink go quiet.
const minScannedEnvExampleAssignments = 20

// checkScannedFloor is minScannedEnvExampleAssignments's assertion, pulled
// out as a plain function returning an error rather than inlined as a
// t.Fatalf call. That is what lets
// TestEnvExampleValueShapeFloorFailsClosedOnEmptyExtraction below prove the
// floor fails closed on a zero extraction WITHOUT going through
// *testing.T — a subtest that calls t.Fatalf on purpose marks its PARENT
// test failed too (Go's subtest semantics propagate upward regardless of
// what the parent's own code does afterward), so driving the proof through
// t.Run would make this file permanently fail `go test`, not merely
// demonstrate the failure-closed behavior once.
func checkScannedFloor(scanned, floor int) error {
	if scanned < floor {
		return fmt.Errorf("scanned %d KEY=VALUE line(s), below the floor of %d — the line scan stopped seeing assignments it used to see, which is not evidence .env.example shrank", scanned, floor)
	}
	return nil
}

// TestEnvExampleValuesAvoidDivergentParserShapes is #0441: it scans
// .env.example's VALUE side (the part after "=") for the four shapes
// systemd's EnvironmentFile= and joho/godotenv read differently — see the
// table in issues/0441.md and the doc comments on scanEnvExampleValueShapes
// above. TestEnvExampleCoversLoaderVariables above proves loader ⊆
// .env.example on the NAME side only; its own doc comment says the value
// side "is a different gap ... left to that issue's own judgment call rather
// than folded in here" — this test is that judgment call.
//
// #0461 added a fifth shape, quoted values (envQuotedValuePrefix's doc
// comment), so this test now scans for five shapes rather than #0441's
// original four. The paragraphs below describing #0441's own four criteria
// are left as the historical record of that issue's own decisions; they are
// not restated for the fifth.
//
// #0441 criterion 3 asked whether this duplicates #0429's
// TestLoad_EnvExampleValuesAreSystemdParseable (config_test.go). It does
// not, and that test was widened rather than replaced (see its own doc
// comment, updated by #0441): that test proves loadFromFile's getInt/getBool
// genuinely REJECT the literal value systemd's EnvironmentFile= would have
// produced from the pre-#0429 lines — a behavioral proof about config.go's
// own parsing, exercised via t.Setenv, that says nothing about what
// .env.example's text currently contains. This test proves the opposite
// half: that .env.example's CURRENT text contains none of the four
// divergent shapes, for every line, not just the two #0429 fixed. Per
// CLAUDE.md §8 ("the two homes are not exclusive"), a shape worth guarding
// against a specific past incident is worth guarding both as text-shape
// (here) and as loader-behavior (there).
//
// #0441 criterion 4: this test does not scan production's own
// /etc/opencircuit/config.env. Reading it needs sudo on a secrets file that
// #0415 separately owns the contents of, and .env.example is the thing this
// repo can see and is the documented install source (CLAUDE.md §5's
// internal/config row, #0423) — a production drift from .env.example is a
// different, out-of-band failure mode this repo's test suite has no way to
// observe at all, sudo or not.
//
// #0441 criterion 5: all four shapes are treated as outright failures
// (t.Errorf), not warnings. .env.example is the literal production template
// (#0423) and every one of the four shapes either fails to boot or silently
// loads a different value than the one written down — there is no currently
// known legitimate use for any of them in this file today. "export " in
// particular has no legitimate use here at all (config.go never expects a
// shell-exported name). $VAR/${VAR} is the one shape with a conceivable
// future legitimate use (one value templated from another), but until a
// variable genuinely needs it, forbidding it outright is the safer default;
// widening the scan for one specific, deliberate line is a smaller and more
// visible change than softening the check globally.
func TestEnvExampleValuesAvoidDivergentParserShapes(t *testing.T) {
	envExample, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatalf("reading .env.example: %v", err)
	}

	scanned, violations := scanEnvExampleValueShapes(envExample)
	if err := checkScannedFloor(scanned, minScannedEnvExampleAssignments); err != nil {
		t.Fatal(err)
	}

	if len(violations) > 0 {
		t.Errorf(".env.example has %d value(s) shaped so systemd's EnvironmentFile= and godotenv would parse them differently (docs/deployment.md's install step copies .env.example verbatim onto production, sudo cp .env.example /etc/opencircuit/config.env):\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

// TestEnvExampleValueShapeFloorFailsClosedOnEmptyExtraction is #0441
// criterion 2's direct proof (a floor is the legitimate in-package case per
// CLAUDE.md §8's floor-vs-ceiling distinction): it feeds
// scanEnvExampleValueShapes content that is deliberately not .env.example —
// all comments and blank lines, zero assignment-shaped lines — and asserts
// that checkScannedFloor returns a non-nil error for that result, calling it
// directly rather than through *testing.T so the intentionally-failing case
// this test exists to prove does not itself fail `go test` (see
// checkScannedFloor's doc comment for why t.Run's parent-propagation rules
// out that approach). If this ever observes a nil error, the floor stopped
// doing its job and TestEnvExampleValuesAvoidDivergentParserShapes could see
// a shrunk-to-nothing extraction and still report success — exactly the
// #0258 / #0428 bug class this floor exists to close.
func TestEnvExampleValueShapeFloorFailsClosedOnEmptyExtraction(t *testing.T) {
	emptyContent := []byte("# nothing but comments here\n\n   \n# and more comments\n")

	scanned, violations := scanEnvExampleValueShapes(emptyContent)
	if scanned != 0 {
		t.Fatalf("test setup bug: expected scanEnvExampleValueShapes to recognize zero assignment lines in comment-only content, got %d", scanned)
	}
	if len(violations) != 0 {
		t.Fatalf("test setup bug: expected zero violations from comment-only content, got %d: %v", len(violations), violations)
	}

	err := checkScannedFloor(scanned, minScannedEnvExampleAssignments)
	if err == nil {
		t.Fatal("expected checkScannedFloor to fail closed when the scan sees zero assignment lines, but it returned nil — an empty extraction must never read as a clean .env.example")
	}
	t.Logf("floor check correctly failed closed: %v", err)
}

// TestScanEnvExampleValueShapes_DetectsEachDivergentShape is #0441
// criterion 1's proof for each of the four shapes individually, built from
// synthetic content rather than the real .env.example (so it cannot be
// affected by whatever .env.example currently contains, and stays a stable
// regression pin). Each case injects exactly one bad line into an otherwise
// clean block of minScannedEnvExampleAssignments assignment lines and
// asserts the scan names the right line and mentions both parsers.
//
// #0458: the three cases below named "dollar expansion — ..." are not a
// fifth, sixth, or seventh shape. They are regression pins for the three
// widenings envDollarExpansion's own doc comment already documents
// (#0450's digit-leading name class, #0453's $(...) admission, and #0457's
// backslash-before-dollar alternative) — each is a fact envDollarExpansion
// alone already established, not a new fact this file is asserting. Each
// badLine below is chosen so that reverting exactly one widening (and no
// other) changes this test's own result for that one case: #0453's
// reviewer measured that, before this issue, reverting any of the three
// left the whole package green. Confirmed in a throwaway worktree per
// this issue's own criterion 3 (see issues/0458.md's ## Work log for the
// three revert transcripts) rather than assumed from reading the pattern.
//
// #0461: the "quoted value" case below IS a genuine fifth shape (see
// envQuotedValuePrefix's doc comment for why), not another regression pin
// in the #0458 sense above — there is no earlier widening of an existing
// shape to name here, since nothing in this file checked value-quoting
// before #0461 added it.
func TestScanEnvExampleValueShapes_DetectsEachDivergentShape(t *testing.T) {
	// cleanLines is a minimal, entirely well-shaped block big enough to clear
	// minScannedEnvExampleAssignments on its own, so each case below only has
	// to add ONE bad line to also prove the floor is satisfied throughout.
	var cleanLines []string
	for i := 0; i < minScannedEnvExampleAssignments; i++ {
		cleanLines = append(cleanLines, fmt.Sprintf("VAR_%02d=fine", i))
	}
	clean := strings.Join(cleanLines, "\n") + "\n"

	cases := []struct {
		name       string
		badLine    string
		wantSubstr []string // must all appear in the one violation message
	}{
		{
			name:       "trailing comment",
			badLine:    "MAX_SEND_RATE=10          # messages/second, keep below the SES quota",
			wantSubstr: []string{"MAX_SEND_RATE", "systemd", "godotenv", "trailing"},
		},
		{
			name:       "dollar expansion",
			badLine:    "EMAIL_FROM=Open Circuit SF <contact@$EMAIL_LIST_DOMAIN>",
			wantSubstr: []string{"EMAIL_FROM", "systemd", "godotenv", "expand"},
		},
		{
			name:       "export prefix",
			badLine:    "export SEND_BATCH_SIZE=50",
			wantSubstr: []string{"SEND_BATCH_SIZE", "systemd", "godotenv", "export"},
		},
		{
			name:       "trailing backslash continuation",
			badLine:    `BASE_URL=https://www.opencircuitsf.com\`,
			wantSubstr: []string{"BASE_URL", "systemd", "godotenv", "continuation"},
		},
		{
			// #0450's widening: a digit-leading variable name. Pinned with
			// $5 rather than $ABC because $5 is the exact shape #0450 added
			// coverage for — godotenv's expandVarRegex uses [A-Z0-9_]+,
			// digits permitted to lead, while the pre-#0450 class required
			// a leading letter or underscore and missed it.
			name:       "dollar expansion - digit-leading name (#0450)",
			badLine:    "PRICE=cost$5",
			wantSubstr: []string{"PRICE", "systemd", "godotenv", "expand"},
		},
		{
			// #0453's widening: the $(...) shape, which godotenv v1.5.1
			// misparses as a variable named FOO due to a group-numbering
			// bug in its own expandVariables (see envDollarExpansion's doc
			// comment). Pinned with the literal "$(...)" substring, which
			// only this case's dedicated message contains — the plain
			// $VAR/${VAR} message never mentions parens.
			name:       "dollar expansion - $(...) form (#0453)",
			badLine:    "PRICE=cost$(FOO)",
			wantSubstr: []string{"PRICE", "systemd", "godotenv", "$(...)"},
		},
		{
			// #0457's widening: a backslash immediately before a "$", with
			// no variable name required to follow it. Pinned with the bare
			// "cost\$" form specifically (rather than "cost\$FOO") because
			// it has no name after the dollar sign at all — the one case
			// the plain $VAR/${VAR} alternative can never match on its own,
			// so reverting #0457 drops this case straight to zero
			// violations rather than merely changing which message fires.
			name:       "dollar expansion - backslash before dollar (#0457)",
			badLine:    `PRICE=cost\$`,
			wantSubstr: []string{"PRICE", "systemd", "godotenv", "backslash"},
		},
		{
			// #0461's fifth shape: a value beginning with a quote character.
			// Unlike the three cases immediately above, this is NOT a
			// regression pin for a widening of the dollar-expansion
			// mechanism — envQuotedValuePrefix is its own check, with its
			// own violation message, that fires and "continue"s BEFORE the
			// dollar-expansion check below ever runs. A single-quoted value
			// containing a "$" (rather than an empty one) would prove
			// nothing about the ordering, since godotenv does not expand
			// inside single quotes at all (envQuotedValuePrefix's doc
			// comment) — 'shh' has no "$" in it at all, so this case cannot
			// be satisfied by the dollar-expansion check misfiring on raw
			// text; only the quote check can produce it.
			name:       "quoted value (#0461)",
			badLine:    `SECRET='shh'`,
			wantSubstr: []string{"SECRET", "systemd", "godotenv", "quote"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			content := []byte(clean + c.badLine + "\n")

			scanned, violations := scanEnvExampleValueShapes(content)
			if scanned < minScannedEnvExampleAssignments {
				t.Fatalf("scanned %d assignment line(s), below the floor of %d — test fixture bug", scanned, minScannedEnvExampleAssignments)
			}
			if len(violations) != 1 {
				t.Fatalf("got %d violation(s) for %q, want exactly 1: %v", len(violations), c.badLine, violations)
			}

			got := violations[0]
			for _, substr := range c.wantSubstr {
				if !strings.Contains(got, substr) {
					t.Errorf("violation message %q does not mention %q", got, substr)
				}
			}
		})
	}
}
