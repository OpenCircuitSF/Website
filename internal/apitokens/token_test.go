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
