package handlers

import (
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
			if strings.Contains(line, "*/") {
				inBlockComment = false
			}
			continue
		}
		if strings.HasPrefix(line, "/**") {
			if !strings.Contains(line, "*/") {
				// Opens a comment that does not also close on this line.
				inBlockComment = true
			}
			continue
		}
		if strings.HasPrefix(line, "//") {
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
