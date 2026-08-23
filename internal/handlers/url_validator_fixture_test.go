package handlers

// #0157: nothing in the build previously guarded the Go<->TypeScript
// isSafeCoverImage/isSafeLinkHref twins against drifting apart. Parity was
// established three separate times (#0138 x2, #0152) by throwaway sweeps
// that were deleted right after -- one of those found a live bypass
// (strings.TrimSpace vs JS trim() disagreeing on U+FEFF) invisible to every
// test that existed at the time.
//
// testdata/url_validators.json is that sweep, made permanent. It holds 3,138
// {rule, value, want} cases generated from ground truth, not hand-listed:
//   - every codepoint 0x00-0xFF, C0∪{DEL} included, swept in interior,
//     leading, doubled-leading, and trailing position against both rules
//   - the exact ECMA-262 whitespace set, computed by asking V8 (Node
//     v26.7.0) for every codepoint 0x0000-0x10FFFF whether
//     `(c+"Z").trim()` differs from `c+"Z"` -- the same method #0152's
//     reviewer used, independently reproduced here (25 codepoints: TAB, LF,
//     VT, FF, CR, SPACE, NBSP, U+1680, U+2000-U+200A, LS, PS, U+202F,
//     U+205F, U+3000, U+FEFF), plus notable near-misses V8 does NOT trim
//     (U+0085 NEL, U+180E, U+200B, U+2060, U+00AD, U+3164) confirmed absent
//   - scheme shapes (javascript:, mailto:, data:, vbscript:, mixed case,
//     percent-encoded), protocol-relative and backslash forms, and the
//     legitimate cases that must keep passing (query, fragment, mid-path
//     //, .., %5C, %2F%2F, non-ASCII, a bare /, a 10,000+ char path)
//
// Every "want" was computed by executing the REAL functions -- the actual
// web/src/lib/linkSafety.ts isSafeLinkHref and workshopAdmin.ts
// isSafeCoverImage under Node 26 (which runs .ts natively), cross-checked
// against these same Go functions at generation time (0 mismatches across
// all 3,138). See issues/0157.md's `## Fix` for the generation script and
// the dedup-vs-fixture reasoning.
//
// This file is the Go side of that check: TestURLValidatorFixture and
// urlValidatorFixture_test.ts (web/src/lib/) both read the identical file,
// so an edit to either isSafeCoverImage/isSafeLinkHref that changes behavior
// on any of these 3,138 payloads is a red build in that language, and an
// edit that breaks Go<->TS agreement specifically requires BOTH files to be
// updated in lockstep -- there is no way to "fix" only one side's test.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestURLValidatorFixture(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "url_validators.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var cases []fixtureCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("fixture is empty")
	}

	bySource := map[string]int{}
	for _, c := range cases {
		bySource[c.Rule+":"+c.Source]++
		t.Run(c.ID, func(t *testing.T) {
			var got bool
			switch c.Rule {
			case "link_href":
				got = isSafeLinkHref(c.Value)
			case "cover_image":
				got = isSafeCoverImage(c.Value)
			default:
				t.Fatalf("unknown rule %q", c.Rule)
			}
			if got != c.Want {
				t.Errorf("rule=%s value=%q: got %v, want %v", c.Rule, c.Value, got, c.Want)
			}
		})
	}
	t.Logf("fixture: %d cases across %d (rule, source) groups", len(cases), len(bySource))
}

// fixtureCase mirrors one entry of testdata/url_validators.json. Kept small
// deliberately -- the fixture itself carries no Go-specific typing, so this
// struct is the only place that decodes it.
type fixtureCase struct {
	ID     string `json:"id"`
	Rule   string `json:"rule"`
	Source string `json:"source"`
	Value  string `json:"value"`
	Want   bool   `json:"want"`
}
