package outreachconn

import (
	"errors"
	"strings"
	"testing"
)

func TestValidServiceURL(t *testing.T) {
	long := "https://x.org/" + strings.Repeat("a", 2049)
	accept := []string{"https://x.org", "https://x.org/path/", "http://localhost:5173", "http://127.0.0.1:8080"}
	reject := []string{"http://example.com", "http://localhost.evil.com", "ftp://x.org", "https://",
		"https://u:p@x.org", "https://x.org/?q=1", "https://x.org/#f", long, "not a url"}
	for _, s := range accept {
		if err := ValidServiceURL(s); err != nil {
			t.Errorf("ValidServiceURL(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range reject {
		if err := ValidServiceURL(s); err == nil {
			t.Errorf("ValidServiceURL(%.40q) accepted", s)
		}
	}
}

func TestNormalize(t *testing.T) {
	if got, err := Normalize(KeyBlueskyHandle, "  @alice.example.test "); err != nil || got != "alice.example.test" {
		t.Errorf("handle = %q, %v", got, err)
	}
	if _, err := Normalize(KeyBlueskyHandle, "alice example"); err == nil {
		t.Error("a handle with a space must be rejected")
	}
	for _, s := range []string{"to ken", "tok\nen"} {
		if _, err := Normalize(KeyMastodonToken, s); err == nil {
			t.Errorf("secret %q must be rejected", s)
		}
	}
	_, err := Normalize("nope", "x")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Key != "nope" {
		t.Errorf("unknown key error = %v", err)
	}
	if got, err := Normalize(KeyMastodonBaseURL, "https://mastodon.example/"); err != nil || got != "https://mastodon.example" {
		t.Errorf("url = %q, %v", got, err)
	}
	_, err = Normalize(KeyMastodonBaseURL, "http://example.com/secret-looking-value")
	if err == nil || strings.Contains(err.Error(), "secret-looking-value") {
		t.Errorf("error must name the rule, never the value: %v", err)
	}
}
