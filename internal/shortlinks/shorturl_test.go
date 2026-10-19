package shortlinks

import "testing"

func TestShortURL(t *testing.T) {
	cases := []struct{ base, key, want string }{
		{"https://go.example", "abc123", "https://go.example/u/abc123"},
		{"https://go.example/", "abc123", "https://go.example/u/abc123"},
		{"https://go.example///", "k", "https://go.example/u/k"},
		{"https://go.example", "a b/c?d", "https://go.example/u/a%20b%2Fc%3Fd"},
	}
	for _, c := range cases {
		if got := ShortURL(c.base, c.key); got != c.want {
			t.Errorf("ShortURL(%q,%q) = %q, want %q", c.base, c.key, got, c.want)
		}
	}
}
