package handlers

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// #0241: TestCampaignProgressPublisher_PublishesJSONFrameToBroker (in
// campaign_progress_test.go) already pins the SSE payload's key set --- but
// only in the Go -> wire direction. It builds its `want` map by hand from
// mailing.CampaignProgress's json tags, so a field added to that struct
// without also updating `want` fails loudly; a field added only to
// web/src/lib/campaignProgress.ts's CampaignProgress interface is invisible
// to it, because that test never reads the TypeScript file at all. #0095's
// 2026-08-22 amendment named this gap and #0241 is the issue that closes it;
// #0095 itself deliberately declined to (see that issue's `## Fix`), because
// comparing a hand-typed Go struct against a TypeScript `interface` is a
// structurally different (and larger) parsing problem than the string-
// constant parity #0095 was built for.
//
// This file is that guard, in the same shape as #0071's route-table parity
// test (routes_parity_test.go) and #0095's own copy parity test
// (complained_copy_parity_test.go): filesystem-only, no database, and it
// reads both sides from their own source rather than restating either by
// hand.
//
//   - The Go side is read via reflection on the live mailing.CampaignProgress
//     type, not by parsing worker.go as text. Unlike router.ts's or
//     preferences.go's Go halves (plain string literals routes_parity_test.go
//     and complained_copy_parity_test.go have no choice but to regex out of
//     source text), CampaignProgress is an importable Go type: reflect.Type's
//     json struct tags are the actual field names encoding/json uses, always
//     in sync with the compiled program by construction, and immune to the
//     gofmt-reformat-breaks-the-regex failure mode routes_parity_test.go's own
//     doc comment describes. There is nothing more authoritative to parse.
//   - The TypeScript side has no such shortcut -- an `interface` is erased at
//     build time, so this reads web/src/lib/campaignProgress.ts's
//     `CampaignProgress` interface declaration directly, the same technique
//     #0095 used for subscribe.ts's string constants.
//
// Only the KEY SET is compared, matching #0241's acceptance criteria ("a
// field added on either side alone fails") -- not field types. A name match
// is not a type match (CLAUDE.md's #0282 note), but this guard doesn't need
// go/types to avoid that trap: it never claims type equivalence in the first
// place, only "does this name appear as a key on both sides," which a plain
// string-set comparison answers honestly.

// campaignProgressTSPath is relative to this package's directory
// (internal/handlers), which is also `go test`'s working directory for this
// package regardless of where the `go test ./...` invocation itself runs
// from.
const campaignProgressTSPath = "../../web/src/lib/campaignProgress.ts"

// campaignProgressTSParseError distinguishes "could not parse
// campaignProgress.ts" from "the two key sets disagree" -- the same
// distinction routes_parity_test.go's routerTSParseError and
// complained_copy_parity_test.go's complainedCopyParseError make, so a
// reformat of the TypeScript file reads as "fix the parser," not as a false
// drift alarm.
type campaignProgressTSParseError struct {
	what string
	err  error
}

func (e *campaignProgressTSParseError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("could not parse %s from %s: %v", e.what, campaignProgressTSPath, e.err)
	}
	return fmt.Sprintf("could not parse %s from %s", e.what, campaignProgressTSPath)
}

func readCampaignProgressTS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(campaignProgressTSPath)
	if err != nil {
		t.Fatalf("could not read %s: %v (campaign progress parity test and %s have drifted apart on disk layout)", campaignProgressTSPath, err, campaignProgressTSPath)
	}
	return string(b)
}

// campaignProgressInterfaceBlockRe captures the body of
// campaignProgress.ts's `export interface CampaignProgress { ... }`
// declaration, from its opening brace to the first line-start closing
// brace. Anchored on the exact declaration text (no `extends`, no generic
// parameters) the way staticRoutesBlockRe anchors on STATIC_ROUTES's type
// annotation -- narrow on purpose, and it fails via
// campaignProgressTSParseError rather than silently matching nothing if the
// declaration is ever reshaped.
var campaignProgressInterfaceBlockRe = regexp.MustCompile(`(?s)export interface CampaignProgress \{(.*?)\n\}`)

// campaignProgressFieldLineRe matches one trimmed `name: type;` or
// `name?: type;` field line from the interface body. Anchored (^...$) like
// staticRoutesEntryRe, and for the identical reason: parseCampaignProgressTS
// applies it per non-comment, non-blank line, so a line that doesn't match
// is a parse failure rather than a silently skipped field.
var campaignProgressFieldLineRe = regexp.MustCompile(`^(\w+)\??:\s*[^;]+;$`)

// parseCampaignProgressTS extracts the field-name set from
// campaignProgress.ts's CampaignProgress interface -- the TypeScript-side
// twin of mailing.CampaignProgress's json tags.
//
// It walks the captured block line by line, tracking whether it is inside a
// `/** ... */` JSDoc comment (the `status` field carries one, spanning
// several lines) so those lines are skipped without needing to match the
// field regex. Every other non-blank line must match
// campaignProgressFieldLineRe or the walk fails closed via
// campaignProgressTSParseError -- the same structural fix #0071's review
// asked of parseStaticRoutesFromRouterTS: a scan-and-skip parser can "parse
// successfully" with a field silently missing, which reads exactly like
// agreement. Failing on the first unrecognized line, and separately failing
// if the walk finds zero fields at all, closes both the "found the wrong
// thing" and the "found nothing" (#0275) shapes of that gap.
//
// #0241's review bounce: a block comment's open and/or close can share a
// physical line with real field text --
// `/** Estimated seconds remaining. */ eta_seconds: number;` or
// `*/ eta_seconds: number;` -- and the original walk `continue`d on the
// whole line in both cases, silently dropping the field with no error. Both
// branches below now take the remainder of the line after the comment
// closes (the first "*/", since block comments cannot nest) and require it
// to be empty or a valid field line, same as any other line -- there is no
// third option that skips it silently.
func parseCampaignProgressTS(src string) (map[string]bool, error) {
	block := campaignProgressInterfaceBlockRe.FindStringSubmatch(src)
	if block == nil {
		return nil, &campaignProgressTSParseError{what: "the CampaignProgress interface declaration"}
	}

	out := make(map[string]bool)
	inBlockComment := false
	for _, rawLine := range strings.Split(block[1], "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if inBlockComment {
			idx := strings.Index(line, "*/")
			if idx < 0 {
				// Still inside the comment; nothing on this line to check.
				continue
			}
			inBlockComment = false
			line = strings.TrimSpace(line[idx+len("*/"):])
			if line == "" {
				continue
			}
			// Fall through: whatever follows "*/" on this line is live code
			// and must be matched or rejected like any other line, not
			// dropped by the `continue` this branch used to take
			// unconditionally.
		} else if strings.HasPrefix(line, "/**") {
			idx := strings.Index(line, "*/")
			if idx < 0 {
				// Opens a comment that does not also close on this line.
				inBlockComment = true
				continue
			}
			line = strings.TrimSpace(line[idx+len("*/"):])
			if line == "" {
				continue
			}
			// Same fall-through as above: a same-line "/** ... */ field;"
			// leaves real code after the close that must not be dropped.
		} else if strings.HasPrefix(line, "//") {
			continue
		}
		m := campaignProgressFieldLineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, &campaignProgressTSParseError{what: fmt.Sprintf("CampaignProgress field line: `%s`", line)}
		}
		out[m[1]] = true
	}
	if len(out) == 0 {
		return nil, &campaignProgressTSParseError{what: "any CampaignProgress field lines"}
	}
	return out, nil
}

// campaignProgressGoJSONKeys reads mailing.CampaignProgress's field set via
// reflection on its `json` struct tags -- the wire-facing names
// encoding/json actually emits, read from the live compiled type rather than
// restated by hand. Every field is required to carry an explicit, non-"-"
// json tag with a non-empty name; a field that doesn't is itself a fatal
// error, since a tag-less field would fall back to encoding/json's Go-name
// default and silently change the wire shape this test exists to pin.
func campaignProgressGoJSONKeys(t *testing.T) map[string]bool {
	t.Helper()
	typ := reflect.TypeOf(mailing.CampaignProgress{})

	out := make(map[string]bool)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag, ok := f.Tag.Lookup("json")
		if !ok || tag == "-" {
			t.Fatalf(
				"mailing.CampaignProgress.%s has no usable `json` tag (tag=%q) -- "+
					"campaign progress parity test requires every field to carry an explicit json tag so the wire key set is knowable from reflection alone",
				f.Name, tag,
			)
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			t.Fatalf("mailing.CampaignProgress.%s's json tag has an empty name (tag=%q)", f.Name, tag)
		}
		out[name] = true
	}
	if len(out) == 0 {
		// Fail closed (#0275): an empty result here almost certainly means
		// reflection found the wrong type or the struct changed shape out
		// from under this test, not that CampaignProgress is genuinely
		// fieldless -- never treat "found nothing" as "nothing to compare".
		t.Fatal("reflect.TypeOf(mailing.CampaignProgress{}) reports zero fields -- campaign progress parity test found nothing to compare on the Go side, which likely means the type changed shape rather than that it is genuinely empty")
	}
	return out
}

// TestCampaignProgressParity_KeySet fails when mailing.CampaignProgress's
// json keys and campaignProgress.ts's CampaignProgress interface fields
// disagree in either direction -- closing #0241, the gap #0095's 2026-08-22
// amendment named: TestCampaignProgressPublisher_PublishesJSONFrameToBroker
// (campaign_progress_test.go) only ever catches a Go-side addition, because
// its `want` map is hand-typed rather than read from campaignProgress.ts.
func TestCampaignProgressParity_KeySet(t *testing.T) {
	tsKeys, err := parseCampaignProgressTS(readCampaignProgressTS(t))
	if err != nil {
		t.Fatal(err)
	}
	goKeys := campaignProgressGoJSONKeys(t)

	// Sanity floor: both sides should have found a plausible number of
	// keys, not just a nonzero one -- #0241's own text names 7 fields.
	// A parser that silently degrades to matching one stray field would
	// otherwise pass len(out) == 0's check while still having found almost
	// nothing.
	const minPlausibleKeys = 5
	if len(tsKeys) < minPlausibleKeys {
		t.Fatalf("campaign progress parity: parsed only %d field(s) from %s's CampaignProgress interface, want at least %d -- the parser likely broke rather than the interface genuinely shrinking this far", len(tsKeys), campaignProgressTSPath, minPlausibleKeys)
	}
	if len(goKeys) < minPlausibleKeys {
		t.Fatalf("campaign progress parity: reflection found only %d json-tagged field(s) on mailing.CampaignProgress, want at least %d -- something is wrong with the reflection walk rather than the struct genuinely shrinking this far", len(goKeys), minPlausibleKeys)
	}

	var onlyInTS, onlyInGo []string
	for k := range tsKeys {
		if !goKeys[k] {
			onlyInTS = append(onlyInTS, k)
		}
	}
	for k := range goKeys {
		if !tsKeys[k] {
			onlyInGo = append(onlyInGo, k)
		}
	}
	sort.Strings(onlyInTS)
	sort.Strings(onlyInGo)

	if len(onlyInTS) > 0 {
		t.Errorf(
			"campaign progress parity: %d key(s) present in %s's CampaignProgress interface but absent from mailing.CampaignProgress's json tags: %s\n"+
				"either add the missing field(s) to internal/mailing.CampaignProgress (worker.go), or the client is reading a key the server never sends",
			len(onlyInTS), campaignProgressTSPath, strings.Join(onlyInTS, ", "),
		)
	}
	if len(onlyInGo) > 0 {
		t.Errorf(
			"campaign progress parity: %d key(s) present in mailing.CampaignProgress's json tags but absent from %s's CampaignProgress interface: %s\n"+
				"add the missing field(s) to web/src/lib/campaignProgress.ts's CampaignProgress interface",
			len(onlyInGo), campaignProgressTSPath, strings.Join(onlyInGo, ", "),
		)
	}
}

// TestParseCampaignProgressTS is #0241's review-bounce fixture: a
// table-driven test over parseCampaignProgressTS itself, independent of
// TestCampaignProgressParity_KeySet's read of the real
// web/src/lib/campaignProgress.ts. It exists because the two shapes that
// exposed the silent-drop bug -- a field sharing a physical line with a
// block comment's open-and-close, or with just its close -- can no longer be
// demonstrated by mutating the real file once the parser is fixed (the fix
// makes that mutation fail, which is the point), so the closure has to be
// pinned here instead. It also locks in every shape the review's own
// measurement table confirmed already failed closed, so a future edit to
// this parser can't quietly reopen one of them without tripping a test.
//
// Every fixture is built from double-quoted Go string literals joined with
// explicit "\n" separators, never a backtick multi-line raw string, per
// CLAUDE.md §8's "a backslash escape you type may land as the real
// character" / malformed-fixture gotcha: the failure mode there is a
// fixture that looks right, parses as "0 matches" because it is malformed,
// and reports a false pass. Building line-by-line with explicit "\n" keeps
// every fixture's exact bytes visible in the diff and leaves no room for
// invisible or misplaced whitespace to change which branch of the parser is
// exercised. The well-formedness of each fixture is enforced by the
// assertions themselves, not taken on faith: a non-error case's produced key
// set is compared for exact equality against wantKeys via reflect.DeepEqual,
// so a fixture that (by construction error) parsed to an empty or partial
// set fails loudly here rather than silently passing an "at least these
// keys were found" check.
func TestParseCampaignProgressTS(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// wantErr: true if parseCampaignProgressTS must return a
		// *campaignProgressTSParseError. wantKeys is only checked when
		// wantErr is false.
		wantErr  bool
		wantKeys []string
	}{
		{
			// The exact shape the review bounce demonstrated on the real
			// file: a JSDoc comment that opens AND closes on the same
			// physical line as a field. Before the fix this field was
			// silently dropped with no error; the fix must capture it.
			name: "same-line JSDoc open-and-close before a field",
			src: "export interface CampaignProgress {\n" +
				"  campaign_id: number;\n" +
				"  /** Estimated seconds remaining. */ eta_seconds: number;\n" +
				"}\n",
			wantKeys: []string{"campaign_id", "eta_seconds"},
		},
		{
			// The second shape the review bounce demonstrated: a multi-line
			// JSDoc whose closing "*/" shares a physical line with a field.
			// Before the fix the whole line (including the field) was
			// dropped via the inBlockComment branch's unconditional
			// continue.
			name: "multi-line JSDoc closing on the same line as a field",
			src: "export interface CampaignProgress {\n" +
				"  campaign_id: number;\n" +
				"  /**\n" +
				"   * Estimated seconds remaining.\n" +
				"   */ eta_seconds: number;\n" +
				"}\n",
			wantKeys: []string{"campaign_id", "eta_seconds"},
		},
		{
			// Baseline control: plain field lines, no comments at all.
			name: "control: plain field lines",
			src: "export interface CampaignProgress {\n" +
				"  campaign_id: number;\n" +
				"  status: string;\n" +
				"}\n",
			wantKeys: []string{"campaign_id", "status"},
		},
		{
			// Control for the shape that was already correct: a multi-line
			// JSDoc whose closing "*/" is alone on its own line (nothing
			// after it), immediately followed by the field on the next
			// line -- matches how the real status field is documented in
			// campaignProgress.ts today.
			name: "control: multi-line JSDoc closes on its own line, field follows",
			src: "export interface CampaignProgress {\n" +
				"  /**\n" +
				"   * The campaign's status.\n" +
				"   */\n" +
				"  status: string;\n" +
				"  campaign_id: number;\n" +
				"}\n",
			wantKeys: []string{"status", "campaign_id"},
		},
		{
			// A plain (non-JSDoc) block comment -- "/*", not "/**" -- is
			// not recognized as a suppressible comment at all by this
			// parser (campaignProgress.ts only ever uses "/** ... */"), so
			// its opening line must fail to match a field line and raise a
			// parse error rather than being silently absorbed.
			name: "plain /* */ block comment (not JSDoc) fails closed",
			src: "export interface CampaignProgress {\n" +
				"  campaign_id: number;\n" +
				"  /*\n" +
				"   ghost: number;\n" +
				"   */\n" +
				"}\n",
			wantErr: true,
		},
		{
			name: "two fields on one physical line",
			src: "export interface CampaignProgress {\n" +
				"  campaign_id: number; status: string;\n" +
				"}\n",
			wantErr: true,
		},
		{
			name: "multi-line object type",
			src: "export interface CampaignProgress {\n" +
				"  meta: {\n" +
				"    foo: string;\n" +
				"  };\n" +
				"}\n",
			wantErr: true,
		},
		{
			name: "multi-line union type",
			src: "export interface CampaignProgress {\n" +
				"  statusish:\n" +
				"    | 'a'\n" +
				"    | 'b';\n" +
				"}\n",
			wantErr: true,
		},
		{
			name: "comma separator instead of a semicolon",
			src: "export interface CampaignProgress {\n" +
				"  campaign_id: number,\n" +
				"}\n",
			wantErr: true,
		},
		{
			// The declaration-level regex is anchored on the exact text
			// "export interface CampaignProgress {" -- an "extends" clause
			// changes what comes before the brace, so the block regex
			// itself must fail to find the interface at all.
			name: "extends clause on the declaration line",
			src: "export interface CampaignProgress extends Base {\n" +
				"  campaign_id: number;\n" +
				"}\n",
			wantErr: true,
		},
		{
			// A decoy declaration-looking string sitting inside a
			// preceding block comment. The declaration regex has no
			// comment-awareness, so it can latch onto the decoy text; the
			// per-line field walk then fails closed on the resulting
			// garbage rather than quietly matching the real interface.
			name: "decoy 'export interface CampaignProgress {' inside a preceding comment",
			src: "/*\n" +
				" * See also: export interface CampaignProgress { foo: string; }\n" +
				" */\n" +
				"export interface CampaignProgress {\n" +
				"  campaign_id: number;\n" +
				"}\n",
			wantErr: true,
		},
		{
			name: "string-literal key",
			src: "export interface CampaignProgress {\n" +
				"  'campaign-id': number;\n" +
				"}\n",
			wantErr: true,
		},
		{
			name: "index signature",
			src: "export interface CampaignProgress {\n" +
				"  [k: string]: unknown;\n" +
				"}\n",
			wantErr: true,
		},
		{
			name: "readonly modifier",
			src: "export interface CampaignProgress {\n" +
				"  readonly campaign_id: number;\n" +
				"}\n",
			wantErr: true,
		},
		{
			name: "trailing line comment after a field",
			src: "export interface CampaignProgress {\n" +
				"  campaign_id: number; // the id\n" +
				"}\n",
			wantErr: true,
		},
		{
			name: "missing export keyword",
			src: "interface CampaignProgress {\n" +
				"  campaign_id: number;\n" +
				"}\n",
			wantErr: true,
		},
		{
			// Optional-field marker control: a plain, already-passing
			// shape kept here so a regression in the "?" handling would
			// show up next to the comment-line fixtures it sits beside.
			name: "control: optional field marker",
			src: "export interface CampaignProgress {\n" +
				"  eta_seconds?: number;\n" +
				"}\n",
			wantKeys: []string{"eta_seconds"},
		},
		{
			// Full-line "//" comment control: already-correct behavior,
			// kept as a regression guard next to the block-comment cases.
			name: "control: full-line // comment is skipped",
			src: "export interface CampaignProgress {\n" +
				"  // campaign_id is intentionally omitted here\n" +
				"  status: string;\n" +
				"}\n",
			wantKeys: []string{"status"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCampaignProgressTS(tt.src)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseCampaignProgressTS(%q) = %v, <nil>; want a *campaignProgressTSParseError", tt.src, got)
				}
				var perr *campaignProgressTSParseError
				if !errors.As(err, &perr) {
					t.Fatalf("parseCampaignProgressTS(%q) error = %v (%T); want a *campaignProgressTSParseError", tt.src, err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCampaignProgressTS(%q) unexpected error: %v", tt.src, err)
			}
			gotKeys := make([]string, 0, len(got))
			for k := range got {
				gotKeys = append(gotKeys, k)
			}
			sort.Strings(gotKeys)
			wantKeys := append([]string(nil), tt.wantKeys...)
			sort.Strings(wantKeys)
			if !reflect.DeepEqual(gotKeys, wantKeys) {
				t.Fatalf("parseCampaignProgressTS(%q) keys = %v, want %v (a fixture that parsed to fewer/other keys than expected is a broken fixture or a broken parser, not a pass)", tt.src, gotKeys, wantKeys)
			}
		})
	}
}
