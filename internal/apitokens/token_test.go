package apitokens

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerate_FormatAndLength(t *testing.T) {
	tok, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, TokenPrefix) || len(tok) != TokenLength {
		t.Fatalf("token %d chars, prefix ok=%v", len(tok), strings.HasPrefix(tok, TokenPrefix))
	}
	b, err := base64.RawURLEncoding.DecodeString(tok[len(TokenPrefix):])
	if err != nil || len(b) != 32 {
		t.Fatalf("body decodes to %d bytes, err %v", len(b), err)
	}
}

func TestGenerate_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
	}
}

func TestHash_IsSHA256OfWholeSecret(t *testing.T) {
	tok, _ := Generate()
	want := sha256.Sum256([]byte(tok))
	if got := Hash(tok); string(got) != string(want[:]) || len(got) != 32 {
		t.Fatal("Hash is not sha256 of the whole token")
	}
}

func TestLooksLikeToken(t *testing.T) {
	valid, _ := Generate()
	body := valid[len(TokenPrefix):]
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid", valid, true},
		{"session-shaped 43 chars", body, false},
		{"wrong prefix", "ocsx_" + body, false},
		{"length 47", valid[:47], false},
		{"length 49", valid + "A", false},
	}
	for _, c := range cases {
		if got := LooksLikeToken(c.in); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestScopeSatisfies(t *testing.T) {
	cases := []struct {
		granted  []string
		required string
		want     bool
	}{
		{[]string{ScopeEventsRead}, ScopeEventsRead, true},
		{[]string{ScopeEventsWrite}, ScopeEventsRead, true},      // write implies read
		{[]string{ScopeEventsRead}, ScopeEventsWrite, false},     // read never implies write
		{[]string{ScopeGuidePostsWrite}, ScopeGuidesRead, false}, // a different resource
		{[]string{ScopeGuidePostsWrite}, ScopeGuidePostsRead, true},
		{[]string{ScopeEventsWrite}, ScopeVenuesRead, false},
		{nil, ScopeEngagementRead, false},
		{nil, "", true},
		{[]string{":write"}, ":read", false}, // an empty resource never matches
	}
	for _, c := range cases {
		if got := ScopeSatisfies(c.granted, c.required); got != c.want {
			t.Errorf("ScopeSatisfies(%v, %q) = %v, want %v", c.granted, c.required, got, c.want)
		}
	}
}

func TestKnownScopes_ShapeAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range KnownScopes {
		if seen[s] {
			t.Errorf("duplicate scope %q", s)
		}
		seen[s] = true
		if !strings.HasSuffix(s, ":read") && !strings.HasSuffix(s, ":write") {
			t.Errorf("scope %q is not resource:read or resource:write", s)
		}
		if !IsKnownScope(s) {
			t.Errorf("IsKnownScope(%q) = false", s)
		}
	}
	if IsKnownScope("admin:write") {
		t.Error("IsKnownScope(admin:write) = true")
	}
}
