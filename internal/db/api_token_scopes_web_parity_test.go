package db

import (
	"os"
	"regexp"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

// apiTokensTSPath is the admin UI's scope list (#0674).
const apiTokensTSPath = "../../web/src/lib/apiTokens.ts"

// tsScopeConst matches one exported scope constant in apiTokens.ts, e.g. the
// line declaring SCOPE_EVENTS_READ, capturing its quoted value.
var tsScopeConst = regexp.MustCompile(`(?m)^export const SCOPE_[A-Z0-9_]+ = '([^']+)';`)

// TestAPITokenScopesMatchWeb (#0682) asserts the SCOPE_* constants in
// web/src/lib/apiTokens.ts are exactly apitokens.KnownScopes, so a scope
// added only on the server cannot be missing from the create form. The other
// half, that KNOWN_SCOPES (the form's checkboxes) holds every SCOPE_*
// constant, is apiTokens.test.ts's "KNOWN_SCOPES covers every SCOPE_
// constant" case: together they tie the form to the Go list.
func TestAPITokenScopesMatchWeb(t *testing.T) {
	b, err := os.ReadFile(apiTokensTSPath)
	if err != nil {
		t.Fatal(err)
	}
	var ts []string
	for _, m := range tsScopeConst.FindAllStringSubmatch(string(b), -1) {
		ts = append(ts, m[1])
	}
	if len(ts) == 0 {
		t.Fatalf("%s: no exported SCOPE_* constants found; has the file's shape changed?", apiTokensTSPath)
	}
	if !scopeSetsEqual(apitokens.KnownScopes, ts) {
		t.Fatalf("scope sets differ:\n Go          %v\n apiTokens.ts %v", apitokens.KnownScopes, ts)
	}
}
