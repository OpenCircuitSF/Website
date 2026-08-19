package handlers

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// #0071: routes.go's knownStaticRoutes and workshopDetailPattern are a
// hand-typed mirror of web/src/lib/router.ts's STATIC_ROUTES and
// WORKSHOP_DETAIL. Nothing previously kept the two honest -- a route added to
// either table alone passed `go build`, `go vet`, `go test ./...`,
// `npm run check` and `npm test` with no complaint (proven both directions in
// #0071's originating review). This file is the guard: it parses the
// TypeScript source directly (deliberately, narrowly -- see the parse
// functions below) so a one-sided edit fails *this* package's test suite,
// which every backend verification run already executes.
//
// Design choice (see issues/0071.md "Approach"): asserting parity from Go by
// parsing router.ts, rather than a generated file or a shared JSON manifest.
// It rides `go test ./...`, which every backend verification pass already
// runs, and needs no new build step or toolchain. The cost is that the parser
// is coupled to router.ts's current formatting; parse failures are reported
// distinctly from parity failures (see routerTSParseError below) so a
// reformat that breaks the regex reads as "fix the parser", not as "the
// tables disagree".

// routerTSPath is relative to this package's directory (internal/handlers),
// which is also `go test`'s working directory for this package regardless of
// where the `go test ./...` invocation itself runs from.
const routerTSPath = "../../web/src/lib/router.ts"

// routerTSParseError distinguishes "could not parse router.ts" from "the two
// tables disagree" -- the two failure modes acceptance criteria and the
// issue's Approach section both call out as needing to stay distinguishable,
// since a reformat of router.ts should not be mistaken for a real drift.
type routerTSParseError struct {
	what string
	err  error
}

func (e *routerTSParseError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("could not parse %s from %s: %v", e.what, routerTSPath, e.err)
	}
	return fmt.Sprintf("could not parse %s from %s", e.what, routerTSPath)
}

func readRouterTS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(routerTSPath)
	if err != nil {
		t.Fatalf("could not read %s: %v (parity test and %s have drifted apart on disk layout)", routerTSPath, err, routerTSPath)
	}
	return string(b)
}

// staticRoutesBlockRe captures the body of router.ts's STATIC_ROUTES object
// literal, from its opening brace to its closing "};". Narrow and format
// -sensitive on purpose (see the file doc comment): it anchors on the
// declaration's type annotation, which is far less likely to be reformatted
// away than the entries themselves.
var staticRoutesBlockRe = regexp.MustCompile(`(?s)const STATIC_ROUTES:[^=]*=\s*\{(.*?)\n\};`)

// staticRoutesEntryRe matches one whole, trimmed 'path': 'RouteName' entry
// line from the block staticRoutesBlockRe captures. Anchored (^...$) on
// purpose: parseStaticRoutesFromRouterTS applies it per line, not as a scan
// over the whole block, precisely so a line that *doesn't* match -- rather
// than being silently skipped -- is treated as a parse failure. See the
// per-line walk below and issues/0071.md's "Review notes" for why a
// scan-and-skip parse (the pre-fix behaviour) is unsafe: it can parse
// "successfully" with one or more routes silently missing.
var staticRoutesEntryRe = regexp.MustCompile(`^'([^']+)':\s*'[^']*',?$`)

// isBraceOnlyLine reports whether a trimmed line is nothing but a stray
// brace -- defensive, since staticRoutesBlockRe's capture group is the
// interior of the literal and should not itself contain a bare "{" or "}",
// but a future reformat could change that.
func isBraceOnlyLine(line string) bool {
	return line == "{" || line == "}"
}

// parseStaticRoutesFromRouterTS extracts the path set from router.ts's
// STATIC_ROUTES literal -- the TypeScript-side twin of knownStaticRoutes.
//
// It walks the captured block line by line and requires every non-blank,
// non-comment, non-brace line to match staticRoutesEntryRe, t.Fatal-ing (via
// a routerTSParseError) on the first line that doesn't. This is the
// structural fix #0071's review asked for: the original version scanned the
// block for entry-shaped substrings and only errored when it found *zero*
// entries, so a single divergent line -- a double-quoted entry among
// single-quoted neighbours, or a route composed in via `...EXTRA_ROUTES,`
// spread -- was silently skipped rather than failing, and the resulting
// missing route looked exactly like agreement. Erroring on any unrecognized
// line closes that gap for the two demonstrated cases and for any future
// syntax this parser doesn't understand, because the failure mode becomes "I
// do not understand this line" rather than "I found nothing here".
func parseStaticRoutesFromRouterTS(src string) (map[string]bool, error) {
	block := staticRoutesBlockRe.FindStringSubmatch(src)
	if block == nil {
		return nil, &routerTSParseError{what: "the STATIC_ROUTES literal"}
	}

	out := make(map[string]bool)
	for _, rawLine := range strings.Split(block[1], "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "//") || isBraceOnlyLine(line) {
			continue
		}
		m := staticRoutesEntryRe.FindStringSubmatch(line)
		if m == nil {
			return nil, &routerTSParseError{what: fmt.Sprintf("STATIC_ROUTES entry: `%s`", line)}
		}
		out[m[1]] = true
	}
	if len(out) == 0 {
		return nil, &routerTSParseError{what: "any STATIC_ROUTES entries"}
	}
	return out, nil
}

// workshopDetailLineRe captures the pattern source between WORKSHOP_DETAIL's
// `= /` and the line-final `/;` -- i.e. the same text that appears between
// the slashes of a JS regex literal, with no flags. router.ts declares it
// with no flags today (`/^\/workshops\/([^/]+)$/`); a future `/gi` etc. would
// need this regex extended, which is exactly the kind of format-sensitivity
// the file doc comment calls out.
var workshopDetailLineRe = regexp.MustCompile(`(?m)^const WORKSHOP_DETAIL = /(.*)/;\s*$`)

// parseWorkshopDetailPatternFromRouterTS extracts router.ts's WORKSHOP_DETAIL
// regex source and compiles it as a Go regexp. JS and Go's RE2 regex dialects
// agree closely enough for this specific pattern (character class, capture
// group, anchors, and a backslash-escaped literal slash, which Go's regexp
// accepts the same as an unescaped one) that no translation is needed --
// verified by hand against the current pattern.
func parseWorkshopDetailPatternFromRouterTS(src string) (*regexp.Regexp, error) {
	m := workshopDetailLineRe.FindStringSubmatch(src)
	if m == nil {
		return nil, &routerTSParseError{what: "the WORKSHOP_DETAIL regex literal"}
	}
	re, err := regexp.Compile(m[1])
	if err != nil {
		return nil, &routerTSParseError{what: "the WORKSHOP_DETAIL regex literal", err: err}
	}
	return re, nil
}

// TestRouteTableParity_StaticRoutes fails when router.ts's STATIC_ROUTES and
// routes.go's knownStaticRoutes disagree in either direction -- a path
// present in one and absent from the other. This is the test #0071 exists to
// add: proven (in the issue's originating review, in a throwaway worktree) to
// catch both a TS-only addition (/privacy) and a Go-only addition
// (/sponsors), neither of which any prior check caught.
func TestRouteTableParity_StaticRoutes(t *testing.T) {
	tsRoutes, err := parseStaticRoutesFromRouterTS(readRouterTS(t))
	if err != nil {
		t.Fatal(err)
	}

	var onlyInTS, onlyInGo []string
	for path := range tsRoutes {
		if !knownStaticRoutes[path] {
			onlyInTS = append(onlyInTS, path)
		}
	}
	for path := range knownStaticRoutes {
		if !tsRoutes[path] {
			onlyInGo = append(onlyInGo, path)
		}
	}
	sort.Strings(onlyInTS)
	sort.Strings(onlyInGo)

	if len(onlyInTS) > 0 {
		t.Errorf(
			"route table parity: %d path(s) present in router.ts's STATIC_ROUTES but missing from routes.go's knownStaticRoutes: %s\n"+
				"add the missing entry/entries to internal/handlers/routes.go's knownStaticRoutes",
			len(onlyInTS), strings.Join(onlyInTS, ", "),
		)
	}
	if len(onlyInGo) > 0 {
		t.Errorf(
			"route table parity: %d path(s) present in routes.go's knownStaticRoutes but missing from router.ts's STATIC_ROUTES: %s\n"+
				"add the missing entry/entries to web/src/lib/router.ts's STATIC_ROUTES",
			len(onlyInGo), strings.Join(onlyInGo, ", "),
		)
	}
}

// workshopDetailPatternParityCases are the paths exercised against both the
// TypeScript-sourced pattern and routes.go's workshopDetailPattern. It
// mirrors -- deliberately, not by re-parsing the source -- the trailing-slash
// normalization rule both sides implement independently (isKnownRoute here,
// parsePath in router.ts: "strip one trailing slash on a path longer than
// '/'"), via normalizeTrailingSlashForParity below. That rule is a two-line,
// independently-documented invariant already covered on each side by its own
// tests (TestIsKnownRoute_TrailingSlash here, router.test.ts's equivalent);
// re-deriving it from source here would buy little and add another format
// -sensitive parse. What this test actually verifies is that the two
// *regexes* -- the part that is not otherwise cross-checked -- accept and
// reject the same normalized paths.
var workshopDetailPatternParityCases = []string{
	"/workshops/solder-101",
	"/workshops/solder-101/", // trailing slash, single-segment slug
	"/workshops/x",
	"/workshops/kicad-night-2026",
	"/workshops",   // the index, not a detail slug
	"/workshops/",  // index with trailing slash
	"/workshops//", // double slash: normalizes to "/workshops/", still a miss
	"/about",
	"/about/",
	"/workshops/a/b", // two segments after "workshops": not the detail pattern
	"/",
}

// normalizeTrailingSlashForParity mirrors the single-strip trailing-slash
// rule both isKnownRoute (routes.go) and parsePath (router.ts) implement --
// see workshopDetailPatternParityCases's comment for why this is hand-mirrored
// rather than parsed.
func normalizeTrailingSlashForParity(path string) string {
	if len(path) > 1 && path[len(path)-1] == '/' {
		return path[:len(path)-1]
	}
	return path
}

// TestRouteTableParity_WorkshopDetailPattern fails when router.ts's
// WORKSHOP_DETAIL and routes.go's workshopDetailPattern disagree on whether a
// given (normalized) path is a workshop-detail path, for either direction of
// disagreement.
func TestRouteTableParity_WorkshopDetailPattern(t *testing.T) {
	tsPattern, err := parseWorkshopDetailPatternFromRouterTS(readRouterTS(t))
	if err != nil {
		t.Fatal(err)
	}

	for _, raw := range workshopDetailPatternParityCases {
		path := normalizeTrailingSlashForParity(raw)
		tsMatch := tsPattern.MatchString(path)
		goMatch := workshopDetailPattern.MatchString(path)
		if tsMatch != goMatch {
			t.Errorf(
				"route table parity: %q (normalized from %q) -- router.ts's WORKSHOP_DETAIL matched=%v, routes.go's workshopDetailPattern matched=%v",
				path, raw, tsMatch, goMatch,
			)
		}
	}
}
