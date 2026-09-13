// TestStaticFallbackCopyMatchesSvelteViews and TestFallbackNavCoversHeaderNav
// are #0519's drift guards: staticPageCopy (fallback.go) is a second, hand
// -typed copy of text that also lives in web/src/views/*.svelte, and
// fallbackNav is a second copy of the links Header.svelte/Footer.svelte
// already render. Nothing enforces the two stay in sync except these tests
// -- both read the real .svelte sources from disk and fail naming exactly
// what drifted, the same shape as internal/db/docs_parity_test.go and
// internal/handlers/routes_parity_test.go's existing content-parity guards.
//
// Not circular (CLAUDE.md §8): the oracle (the .svelte source file, read
// fresh off disk) and the subject (fallback.go's Go tables) are different
// bytes in different files/languages -- an edit to one cannot also satisfy
// the guard by construction the way a copy of the answer stored next to the
// question would.
package seo

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// repoPath resolves a repo-relative path against the repo root, the same
// "../../" convention TestSourceTemplate_EachTokenAppearsExactlyOnce
// (seo_test.go) uses -- `go test`'s working directory for this package is
// always internal/seo/, regardless of where `go test ./...` itself runs
// from.
func repoPath(rel string) string {
	return "../../" + rel
}

var appNamePattern = regexp.MustCompile(`APP_NAME\s*=\s*'([^']+)'`)

// extractAppName reads web/src/lib/branding.ts's own APP_NAME constant, the
// single source of truth staticPageCopy's {APP_NAME} expansion mirrors.
// Fails the test outright (CLAUDE.md §8's self-check rule) if extraction
// comes back empty, rather than silently comparing against "".
func extractAppName(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(repoPath("web/src/lib/branding.ts"))
	if err != nil {
		t.Fatalf("reading branding.ts: %v", err)
	}
	m := appNamePattern.FindSubmatch(data)
	if m == nil {
		t.Fatal("could not find APP_NAME = '...' in web/src/lib/branding.ts")
	}
	name := string(m[1])
	if name == "" {
		t.Fatal("extracted APP_NAME is empty -- branding.ts's own format may have changed")
	}
	return name
}

// normalizeSourceForCopyGuard mirrors staticPageCopy's own "verbatim" rule
// (fallback.go's doc comment): expand {APP_NAME}, drop <strong>/</strong>,
// and collapse whitespace runs (including newlines) to a single space so a
// component's line-wrapped JSX text compares equal to fallback.go's own
// single-line Go string literal.
func normalizeSourceForCopyGuard(src, appName string) string {
	src = strings.ReplaceAll(src, "{APP_NAME}", appName)
	src = strings.ReplaceAll(src, "<strong>", "")
	src = strings.ReplaceAll(src, "</strong>", "")
	return strings.Join(strings.Fields(src), " ")
}

// staticCopyMismatches normalizes source the same way and reports every way
// sc's H1/Blocks fail to appear in it: the H1 must appear as the text
// content of an <h1 ...> element (a regexp anchored on the real tag, not
// just "the string appears somewhere"), and each block's Text must be a
// substring.
func staticCopyMismatches(sc staticCopy, appName string, source []byte) []string {
	normalized := normalizeSourceForCopyGuard(string(source), appName)

	var problems []string
	h1Pattern := regexp.MustCompile(`<h1[^>]*>\s*` + regexp.QuoteMeta(sc.H1) + `\s*<`)
	if !h1Pattern.MatchString(normalized) {
		problems = append(problems, fmt.Sprintf("H1 %q not found as the text content of an <h1> element", sc.H1))
	}
	for _, b := range sc.Blocks {
		if !strings.Contains(normalized, b.Text) {
			problems = append(problems, fmt.Sprintf("block text %q not found as a substring of the normalized source", b.Text))
		}
	}
	return problems
}

// TestStaticFallbackCopyMatchesSvelteViews is fallback_copy_guard_test.go's
// headline guard: every staticPageCopy entry's H1/Blocks must still appear,
// verbatim after normalization, in the .svelte file its Source field names.
func TestStaticFallbackCopyMatchesSvelteViews(t *testing.T) {
	appName := extractAppName(t)

	for path, sc := range staticPageCopy {
		t.Run(path, func(t *testing.T) {
			source, err := os.ReadFile(repoPath(sc.Source))
			if err != nil {
				t.Fatalf("reading %s: %v", sc.Source, err)
			}
			if problems := staticCopyMismatches(sc, appName, source); len(problems) > 0 {
				t.Errorf("%s no longer matches staticPageCopy[%q]: %s", sc.Source, path, strings.Join(problems, "; "))
			}
		})
	}

	// Mutation proof (CLAUDE.md §8): the comparison above must actually be
	// capable of failing, not vacuously pass because of a normalization bug
	// that empties both sides. Change one word in the REAL Home.svelte
	// source and confirm staticCopyMismatches reports it.
	t.Run("mutation: a changed word is caught", func(t *testing.T) {
		home := staticPageCopy["/"]
		source, err := os.ReadFile(repoPath(home.Source))
		if err != nil {
			t.Fatalf("reading %s: %v", home.Source, err)
		}
		const original = "hands-on electronics"
		const mutated = "remote-only electronics"
		if !strings.Contains(string(source), original) {
			t.Fatalf("fixture assumption failed: %q not found in %s -- this test's mutation needs updating", original, home.Source)
		}
		mutatedSource := strings.Replace(string(source), original, mutated, 1)
		if problems := staticCopyMismatches(home, appName, []byte(mutatedSource)); len(problems) == 0 {
			t.Error("expected the mutated source to report a mismatch, got none -- the comparison is not sensitive to its own subject")
		}
	})
}

var headerNavLinkPattern = regexp.MustCompile(`href:\s*'(/[^']*)'`)

// TestFallbackNavCoversHeaderNav proves fallbackNav (fallback.go) is neither
// missing a link a real visitor can reach from Header.svelte's own
// NAV_LINKS, nor inventing one that appears nowhere in the real header or
// footer.
func TestFallbackNavCoversHeaderNav(t *testing.T) {
	header, err := os.ReadFile(repoPath("web/src/lib/Header.svelte"))
	if err != nil {
		t.Fatalf("reading Header.svelte: %v", err)
	}
	footer, err := os.ReadFile(repoPath("web/src/lib/Footer.svelte"))
	if err != nil {
		t.Fatalf("reading Footer.svelte: %v", err)
	}

	matches := headerNavLinkPattern.FindAllSubmatch(header, -1)
	if len(matches) < 3 {
		t.Fatalf("found %d NAV_LINKS hrefs in Header.svelte, want at least 3 -- has Header.svelte's NAV_LINKS shape changed?", len(matches))
	}

	fallbackHrefs := make(map[string]bool, len(fallbackNav))
	for _, l := range fallbackNav {
		fallbackHrefs[l.Href] = true
	}
	for _, m := range matches {
		href := string(m[1])
		if !fallbackHrefs[href] {
			t.Errorf("Header.svelte's NAV_LINKS links to %q, missing from fallbackNav", href)
		}
	}

	for _, l := range fallbackNav {
		singleQuoted := `href: '` + l.Href + `'`
		doubleQuoted := `href="` + l.Href + `"`
		inHeader := strings.Contains(string(header), singleQuoted) || strings.Contains(string(header), doubleQuoted)
		inFooter := strings.Contains(string(footer), singleQuoted) || strings.Contains(string(footer), doubleQuoted)
		if !inHeader && !inFooter {
			t.Errorf("fallbackNav has %q, not found in Header.svelte or Footer.svelte as href=\"...\" or href: '...'", l.Href)
		}
	}
}
