package handlers

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// #0095: web/src/lib/subscribe.ts's COMPLAINED_NO_RESUBSCRIBE_MESSAGE_LEAD /
// _TAIL / COMPLAINED_CONTACT_EMAIL are meant to compose the same sentence
// this package's patchUnsubscribe (preferences.go) returns for a no-op PATCH
// against an already-complained row, modulo a documented "No change: "
// lead-in and the recapitalisation that drops it entails. Three separate doc
// comments asserted that guarantee and nothing checked it: #0090's bounce fix
// added " at hello@opencircuitsf.com" to the server string and left the
// client string unchanged, and the divergence was invisible to every test
// this repository ran until a reviewer read both strings character by
// character.
//
// This file is the guard, in the same shape as #0071's route-table parity
// test (routes_parity_test.go) and #0082/#0083's docs parity test
// (internal/db/docs_parity_test.go): filesystem-only, no database, parsing
// the TypeScript source directly and comparing it against the Go source it
// must mirror. It rides `go test ./...`, which every backend verification
// pass already runs.
//
// Deliberately narrow, not a generalised cross-language string-constant
// diff: this is the one pair #0095 was filed over, and #0095's Notes ask
// explicitly whether a broader guard is warranted and say "if not, say so
// and keep it narrow." A generalised guard would need to discover which Go
// string literals have TypeScript twins at all -- there is no marker for
// that today -- so it would either need a manifest (a new place for the same
// kind of drift to happen) or a much fuzzier heuristic. Keeping this test
// narrow and adding a sibling when the next pair drifts is cheaper than
// guessing at that shape now.

// complainedCopyTSPath and complainedCopyGoPath are relative to this
// package's directory (internal/handlers), which is also `go test`'s
// working directory for this package regardless of where the
// `go test ./...` invocation itself runs from.
const complainedCopyTSPath = "../../web/src/lib/subscribe.ts"
const complainedCopyGoPath = "preferences.go"

// complainedCopyParseError distinguishes "could not parse the expected
// shape out of the source" from "the two sides disagree" -- see
// routes_parity_test.go's routerTSParseError for why that distinction
// matters: a reformat should read as "fix the parser", not as "the copy
// drifted".
type complainedCopyParseError struct {
	what string
	path string
	err  error
}

func (e *complainedCopyParseError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("could not parse %s from %s: %v", e.what, e.path, e.err)
	}
	return fmt.Sprintf("could not parse %s from %s", e.what, e.path)
}

func readComplainedCopySource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read %s: %v (parity test and %s have drifted apart on disk layout)", path, err, path)
	}
	return string(b)
}

// tsStringLiteralRe matches one single- or double-quoted JS/TS string
// literal, allowing backslash-escaped characters (including an escaped
// version of its own delimiter) inside. Used to pull the literal segments
// out of a declaration's right-hand side without caring which quote style
// each segment uses -- subscribe.ts mixes both on purpose (LEAD's second
// segment is double-quoted because it contains an apostrophe in "can't").
var tsStringLiteralRe = regexp.MustCompile(`'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"`)

// parseTSStringConst extracts the string value of an
// `export const NAME = <literal>[ + <literal>...];` declaration from
// subscribe.ts, concatenating every quoted literal segment on the
// right-hand side in order. It does not evaluate JS escape sequences beyond
// stripping the surrounding quotes -- none of the three constants this file
// reads contain any, and a future one that did would need this extended
// (which would fail loudly here as a content mismatch, not silently).
func parseTSStringConst(t *testing.T, src, name string) string {
	t.Helper()
	declRe := regexp.MustCompile(`(?s)export const ` + regexp.QuoteMeta(name) + `(?::\s*string)? =(.*?);`)
	m := declRe.FindStringSubmatch(src)
	if m == nil {
		t.Fatal(&complainedCopyParseError{what: "the " + name + " declaration", path: complainedCopyTSPath})
	}
	rhs := m[1]
	literals := tsStringLiteralRe.FindAllString(rhs, -1)
	if len(literals) == 0 {
		t.Fatal(&complainedCopyParseError{what: "any string literal in the " + name + " declaration", path: complainedCopyTSPath})
	}
	var sb strings.Builder
	for _, lit := range literals {
		// Strip the one leading/trailing quote character (either ' or ")
		// and unescape only the delimiter's own escaped form -- \' or \" --
		// which is all these three constants ever need.
		inner := lit[1 : len(lit)-1]
		inner = strings.ReplaceAll(inner, `\'`, `'`)
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		sb.WriteString(inner)
	}
	return sb.String()
}

// goPatchUnsubscribeNoOpMessageRe captures the two literal halves either
// side of `+ complainedContactEmail +` in preferences.go's patchUnsubscribe
// no-op message assignment. Anchored on the exact current line shape (see
// routes_parity_test.go's file doc comment for why this is a deliberate,
// format-sensitive choice): a reformat that breaks this regex should read as
// a parse failure, not as the two sides agreeing by accident on an empty
// diff.
var goPatchUnsubscribeNoOpMessageRe = regexp.MustCompile(`(?m)^\s*message = "((?:[^"\\]|\\.)*)" \+ complainedContactEmail \+ "((?:[^"\\]|\\.)*)"$`)

// goComplainedContactEmailConstRe captures preferences.go's
// complainedContactEmail const value.
var goComplainedContactEmailConstRe = regexp.MustCompile(`(?m)^const complainedContactEmail = "((?:[^"\\]|\\.)*)"$`)

// lowerFirst lowercases the first rune of s, leaving the rest untouched. It
// mirrors the recapitalisation subscribe.ts's doc comment documents:
// preferences.go's no-op message drops the "No change: " lead-in's
// grammatical need for a capital and re-lowercases "This" to "this".
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// TestComplainedCopyParity_LeadTailComposeToServerMessage fails when
// subscribe.ts's COMPLAINED_NO_RESUBSCRIBE_MESSAGE_LEAD /
// COMPLAINED_NO_RESUBSCRIBE_MESSAGE_TAIL no longer compose (modulo the
// documented "No change: " lead-in and its recapitalisation) to the exact
// sentence preferences.go's patchUnsubscribe returns for a no-op PATCH
// against an already-complained row. Fails in either direction: an edit to
// the Go string alone, or to either TypeScript constant alone.
func TestComplainedCopyParity_LeadTailComposeToServerMessage(t *testing.T) {
	tsSrc := readComplainedCopySource(t, complainedCopyTSPath)
	goSrc := readComplainedCopySource(t, complainedCopyGoPath)

	tsLead := parseTSStringConst(t, tsSrc, "COMPLAINED_NO_RESUBSCRIBE_MESSAGE_LEAD")
	tsTail := parseTSStringConst(t, tsSrc, "COMPLAINED_NO_RESUBSCRIBE_MESSAGE_TAIL")

	m := goPatchUnsubscribeNoOpMessageRe.FindStringSubmatch(goSrc)
	if m == nil {
		t.Fatal(&complainedCopyParseError{what: "the patchUnsubscribe no-op message assignment", path: complainedCopyGoPath})
	}
	goBeforeEmail, goAfterEmail := m[1], m[2]

	wantBeforeEmail := "No change: " + lowerFirst(tsLead)
	if goBeforeEmail != wantBeforeEmail {
		t.Errorf(
			"complained-copy parity: preferences.go's no-op message text before the contact address does not equal \"No change: \" + lowerFirst(subscribe.ts's COMPLAINED_NO_RESUBSCRIBE_MESSAGE_LEAD)\n  preferences.go:   %q\n  computed from TS: %q",
			goBeforeEmail, wantBeforeEmail,
		)
	}
	if goAfterEmail != tsTail {
		t.Errorf(
			"complained-copy parity: preferences.go's no-op message text after the contact address does not equal subscribe.ts's COMPLAINED_NO_RESUBSCRIBE_MESSAGE_TAIL\n  preferences.go: %q\n  subscribe.ts:   %q",
			goAfterEmail, tsTail,
		)
	}
}

// TestComplainedCopyParity_ContactEmail fails when preferences.go's
// complainedContactEmail and subscribe.ts's COMPLAINED_CONTACT_EMAIL name
// different addresses -- the two constants that make the mailto: anchor in
// PreferenceCenter.svelte and the plain-text address in the JSON response
// point at the same inbox.
func TestComplainedCopyParity_ContactEmail(t *testing.T) {
	tsSrc := readComplainedCopySource(t, complainedCopyTSPath)
	goSrc := readComplainedCopySource(t, complainedCopyGoPath)

	tsEmail := parseTSStringConst(t, tsSrc, "COMPLAINED_CONTACT_EMAIL")

	m := goComplainedContactEmailConstRe.FindStringSubmatch(goSrc)
	if m == nil {
		t.Fatal(&complainedCopyParseError{what: "the complainedContactEmail const", path: complainedCopyGoPath})
	}
	goEmail := m[1]

	if goEmail != tsEmail {
		t.Errorf(
			"complained-copy parity: preferences.go's complainedContactEmail (%q) does not match subscribe.ts's COMPLAINED_CONTACT_EMAIL (%q)",
			goEmail, tsEmail,
		)
	}
}
