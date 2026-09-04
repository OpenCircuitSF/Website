// #0141: web/dist/index.html is a tracked placeholder that `npm run build`
// rewrites in place -- .gitignore deliberately un-ignores this one file (see
// web/embed.go's doc comment and the comment above `web/dist/*` /
// `!web/dist/index.html` in .gitignore) so `go build ./...` has something to
// //go:embed before any npm build has run. Nothing previously enforced that
// the committed copy stayed the minimal placeholder rather than becoming a
// real build.
//
// This already happened twice in one day (#0054's review, then #0140):
// ShortLinks' original token-free placeholder shipped silently, so this
// package had nothing to substitute, and that defect masked a second one --
// #0054's implementer saw its own wiring test pass only because a local
// `npm run build` had overwritten this file out from under it. #0141 was
// filed to make that failure loud instead of silent.
//
// Placement (#0141's second AC: "runs somewhere it cannot be skipped"):
// lives in internal/seo, not in package web where web/dist/index.html
// itself sits, and not in a standalone test binary. Two reasons. First, the
// default per-issue `ISSUE=NNNN scripts/check.sh` (no subcommand) only runs
// `go test ./internal/... ./cmd/...` (scripts/check.sh's `*)` case) --
// `./web/...` is exercised only by `scripts/check.sh all`, which CLAUDE.md
// §4 calls out as "a batch's single review pass, not for every
// implementer". A guard placed in package web would in practice run far
// less often than intended, exactly the gap this test exists to close.
// internal/seo sits under the default's `./internal/...`, matching this
// issue's own AC wording ("a Go test alongside the existing doc-parity
// guards" -- internal/db/docs_parity_test.go, internal/handlers's
// routes_parity_test.go -- "is the cheapest home, since scripts/check.sh
// already runs them"). Second, this package already defines the nine
// tokenTitle … tokenJSONLD constants below it substitutes into
// web/dist/index.html at request time (seo.go); reusing them here means the
// "what counts as a placeholder token" list has exactly one owner instead of
// a second, driftable copy.
//
// It is filesystem-only -- no TEST_DATABASE_URL, no advisory lock -- so it
// rides every `go test ./internal/...` run for free, and (unlike a
// TestMain-gated package) is never skipped when TEST_DATABASE_URL is unset.
//
// Design choice -- guard vs. removing the collision outright (#0141's
// acceptance criteria ask for this to be decided and recorded, not just the
// cheap fix applied): the durable option would stop tracking a file
// `npm run build` also writes -- e.g. generate the placeholder from
// web/index.html at `go generate` time, or embed from a path npm never
// touches, so web/dist/ has no committed content at all. That is a bigger,
// structural change: `//go:embed all:dist` (web/embed.go) needs the
// directory to exist and contain *something* at compile time from a clean
// checkout, so removing the collision means either committing a
// differently-named seed file and copying it into place before both
// `go build` and `npm run build` (a new pre-build step in scripts/ and,
// plausibly, CI), or generating web/dist/index.html from web/index.html
// mechanically instead of hand-maintaining a second near-duplicate copy of
// its <head> block. Either is worth doing, but it touches scripts/ call
// sites that #0208 is already contesting (see #0141's own scope note) and
// the go:embed wiring every backend build depends on -- too large a change
// to fold into this guard's implementation, and not needed to close #0141's
// actual failure mode. Recorded here rather than done: a follow-up issue for
// "stop tracking web/dist/index.html, generate it instead" is the right next
// step if the guard proves insufficient in practice.
//
// So the fix landed here is the second, cheaper option #0141 names: a Go
// test that fails when the committed placeholder looks like build output,
// in the same shape as this repo's other content-parity guards
// (internal/db/docs_parity_test.go's #0082/#0083/#0086/#0089,
// internal/handlers/routes_parity_test.go's #0071).
//
// Two independent failure signals, matched to the two ways this file has
// actually gone wrong in this repo's history:
//
//  1. Missing a %%OC_*%% token: ShortLinks' original placeholder (#0054
//     finding 1) had none of the nine tokens this package's Render
//     substitutes. A placeholder missing even one means there is nothing
//     there to replace on that field.
//  2. A hashed /assets/*.js or *.css reference: real `npm run build` output
//     (#0054 finding 3, reproduced live while writing this guard by running
//     `npm run build` in a throwaway git worktree, never in this shared
//     checkout -- see CLAUDE.md §8b) emits
//     `<script type="module" crossorigin src="/assets/index-<hash>.js">`
//     and a matching stylesheet `<link>`. Note that a real build still
//     contains every %%OC_*%% token (Vite does not touch literal text it
//     doesn't recognize as a token), so the token check alone would not
//     have caught this case -- both checks are load-bearing.
package seo

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// distIndexPath is relative to this package's directory (internal/seo/),
// which is also `go test`'s working directory for this package regardless of
// where the `go test ./...` invocation itself runs from -- matching
// internal/handlers/routes_parity_test.go's routerTSPath convention and
// internal/db/docs_parity_test.go's migrationsDir/docsPath convention.
const distIndexPath = "../../web/dist/index.html"

// distPlaceholderTokens is every %%OC_*%% token web/dist/index.html must
// still carry -- reusing this package's own tokenTitle … tokenJSONLD
// constants (seo.go) rather than a second, driftable literal list, since
// they are exactly the tokens Render substitutes into this same file at
// request time.
var distPlaceholderTokens = []string{
	tokenTitle,
	tokenDescription,
	tokenOGTitle,
	tokenOGDescription,
	tokenOGImage,
	tokenOGURL,
	tokenOGType,
	tokenTwitterCard,
	tokenTwitterTitle,
	tokenTwitterDescription,
	tokenJSONLD,
}

// hashedAssetPattern matches a Vite-emitted hashed asset reference, e.g.
// `/assets/index-BDtqW4JY.js` or `/assets/index-TVYljy6F.css` -- the
// signature of real `npm run build` output, which the placeholder must never
// contain since no built asset exists until npm has run.
var hashedAssetPattern = regexp.MustCompile(`/assets/[^"'\s]+\.(?:js|css)\b`)

// placeholderMarker is the literal comment text web/dist/index.html carries
// and no real `npm run build` output does (verified: 1 occurrence in the
// committed file, 0 in build output). #0212: the hashedAssetPattern check
// above keys on `/assets/…\.(js|css)`, which is config-dependent -- a build
// with a non-default `base` or `assetsDir` in web/vite.config.ts
// (`/static/…`, a relative `assets/…`, or `.mjs`) would slip past it. Neither
// is set today, so that gap is currently unreachable, but asserting this
// marker is present catches regeneration regardless of Vite config, for one
// extra line.
const placeholderMarker = "This is a MINIMAL PLACEHOLDER, not real build output"

// validateDistPlaceholder returns every problem found in content, or nil if
// content still looks like the minimal placeholder. Split out from the test
// function so both TestDistIndexPlaceholder and a mutation proof (run by
// hand against build output, see #0141's Verification notes) can call it
// directly.
func validateDistPlaceholder(content string) []string {
	var problems []string

	var missing []string
	for _, tok := range distPlaceholderTokens {
		if !strings.Contains(content, tok) {
			missing = append(missing, tok)
		}
	}
	if len(missing) > 0 {
		problems = append(problems, "missing placeholder token(s): "+strings.Join(missing, ", "))
	}

	if m := hashedAssetPattern.FindAllString(content, -1); len(m) > 0 {
		problems = append(problems, "references hashed build asset(s): "+strings.Join(m, ", "))
	}

	if !strings.Contains(content, placeholderMarker) {
		problems = append(problems, "missing placeholder marker comment: "+placeholderMarker)
	}

	return problems
}

// TestDistIndexPlaceholder is #0141's guard: web/dist/index.html is a
// tracked placeholder `npm run build` rewrites in place (see this file's
// package-level doc comment for the two prior incidents). It fails loudly,
// naming exactly what's wrong and what to do, instead of letting a real
// build silently ship as the committed file the way it did twice already.
func TestDistIndexPlaceholder(t *testing.T) {
	content, err := os.ReadFile(distIndexPath)
	if err != nil {
		t.Fatalf("read %s: %v -- has the repo layout changed?", distIndexPath, err)
	}

	if problems := validateDistPlaceholder(string(content)); len(problems) > 0 {
		t.Errorf(
			"%s no longer looks like the minimal placeholder it must stay as (%s).\n"+
				"This almost certainly means a real `npm run build` was committed over it "+
				"(#0141) -- restore the placeholder with "+
				"`git checkout HEAD -- web/dist/index.html` (only if you did not "+
				"intentionally edit it this session; if you did, restore its %%%%OC_*%%%% "+
				"tokens by hand instead) and do NOT commit `npm run build` output over this "+
				"file. See this file's package doc comment and CLAUDE.md §8a.",
			distIndexPath, strings.Join(problems, "; "),
		)
	}
}

// TestValidateDistPlaceholder exercises validateDistPlaceholder directly
// against synthetic inputs, so the two failure signals are pinned
// independently of whatever the real committed file currently contains.
func TestValidateDistPlaceholder(t *testing.T) {
	const goodPlaceholder = `<!doctype html>
<html><head>
<title>%%OC_TITLE%%</title>
<!-- This is a MINIMAL PLACEHOLDER, not real build output -->
<meta name="description" content="%%OC_DESCRIPTION%%" />
<meta property="og:title" content="%%OC_OG_TITLE%%" />
<meta property="og:description" content="%%OC_OG_DESCRIPTION%%" />
<meta property="og:image" content="%%OC_OG_IMAGE%%" />
<meta property="og:url" content="%%OC_OG_URL%%" />
<meta property="og:type" content="%%OC_OG_TYPE%%" />
<meta name="twitter:card" content="%%OC_TWITTER_CARD%%" />
<meta name="twitter:title" content="%%OC_TWITTER_TITLE%%" />
<meta name="twitter:description" content="%%OC_TWITTER_DESCRIPTION%%" />
%%OC_JSONLD%%
</head><body><div id="app"></div></body></html>`

	t.Run("accepts a minimal placeholder", func(t *testing.T) {
		if got := validateDistPlaceholder(goodPlaceholder); len(got) != 0 {
			t.Errorf("validateDistPlaceholder(good) = %v, want no problems", got)
		}
	})

	t.Run("rejects a real build (hashed assets, tokens still present)", func(t *testing.T) {
		// Vite doesn't touch literal text it doesn't recognize as a token,
		// so a real build still contains every %%OC_*%% token -- this case
		// pins that the hashed-asset check alone catches it (#0141: proven
		// live against actual `npm run build` output in a throwaway
		// worktree, which looked exactly like this).
		built := goodPlaceholder + `
<script type="module" crossorigin src="/assets/index-Cs2-8eM1.js"></script>
<link rel="stylesheet" crossorigin href="/assets/index-TVYljy6F.css">`
		got := validateDistPlaceholder(built)
		if len(got) == 0 {
			t.Fatal("validateDistPlaceholder(built) = no problems, want the hashed-asset signal")
		}
		found := false
		for _, p := range got {
			if strings.Contains(p, "hashed build asset") {
				found = true
			}
		}
		if !found {
			t.Errorf("validateDistPlaceholder(built) = %v, want a hashed build asset problem", got)
		}
	})

	t.Run("rejects a token-free placeholder", func(t *testing.T) {
		// ShortLinks' original placeholder (#0054 finding 1): no %%OC_*%%
		// tokens at all, and (unlike a real build) no hashed assets either --
		// pins that the token check is a genuinely independent signal, not
		// redundant with the asset check.
		tokenFree := `<!doctype html><html><head><title>Open Circuit SF</title></head><body><div id="app"></div></body></html>`
		got := validateDistPlaceholder(tokenFree)
		if len(got) == 0 {
			t.Fatal("validateDistPlaceholder(tokenFree) = no problems, want the missing-token signal")
		}
		found := false
		for _, p := range got {
			if strings.Contains(p, "missing placeholder token") {
				found = true
			}
		}
		if !found {
			t.Errorf("validateDistPlaceholder(tokenFree) = %v, want a missing placeholder token problem", got)
		}
	})

	t.Run("rejects a build with tokens but no marker (non-default base/assetsDir)", func(t *testing.T) {
		// #0212: hashedAssetPattern keys on `/assets/…\.(js|css)`, which is
		// config-dependent -- a build with a non-default `base` or
		// `assetsDir` in web/vite.config.ts would emit an asset path this
		// pattern does not match (e.g. `/static/index-XXXX.js`), while every
		// %%OC_*%% token still survives (Vite does not touch literal text it
		// doesn't recognize). Mutation proof that the marker check alone
		// catches that case: same tokens as goodPlaceholder, no marker
		// comment, and an asset reference the hashed-asset pattern misses.
		nonDefaultBase := `<!doctype html>
<html><head>
<title>%%OC_TITLE%%</title>
<meta name="description" content="%%OC_DESCRIPTION%%" />
<meta property="og:title" content="%%OC_OG_TITLE%%" />
<meta property="og:description" content="%%OC_OG_DESCRIPTION%%" />
<meta property="og:image" content="%%OC_OG_IMAGE%%" />
<meta property="og:url" content="%%OC_OG_URL%%" />
<meta property="og:type" content="%%OC_OG_TYPE%%" />
<meta name="twitter:card" content="%%OC_TWITTER_CARD%%" />
<meta name="twitter:title" content="%%OC_TWITTER_TITLE%%" />
<meta name="twitter:description" content="%%OC_TWITTER_DESCRIPTION%%" />
%%OC_JSONLD%%
<script type="module" crossorigin src="/static/index-Cs2-8eM1.js"></script>
</head><body><div id="app"></div></body></html>`
		got := validateDistPlaceholder(nonDefaultBase)
		if len(got) == 0 {
			t.Fatal("validateDistPlaceholder(nonDefaultBase) = no problems, want the missing-marker signal (hashedAssetPattern does not match a non-default base)")
		}
		found := false
		for _, p := range got {
			if strings.Contains(p, "missing placeholder marker") {
				found = true
			}
		}
		if !found {
			t.Errorf("validateDistPlaceholder(nonDefaultBase) = %v, want a missing placeholder marker problem", got)
		}
	})
}
