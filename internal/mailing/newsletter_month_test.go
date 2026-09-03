// Unit tests for newsletter_month.go: parsing, rejection, and the zero
// value, none of which touch a database. #0405, PRD §6.8.
package mailing

import (
	"errors"
	"testing"
)

func TestParseNewsletterMonth_Valid(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2026-11", "11-2026"},
		{"2026-12", "12-2026"},
		{"2026-01", "01-2026"}, // zero padding is the whole point of the format
	}
	for _, c := range cases {
		nm, err := ParseNewsletterMonth(c.in)
		if err != nil {
			t.Fatalf("ParseNewsletterMonth(%q): unexpected error: %v", c.in, err)
		}
		if got := nm.Slug(); got != c.want {
			t.Errorf("ParseNewsletterMonth(%q).Slug() = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParseNewsletterMonth_Rejects pins the property this file's doc
// comment names: a value in the transposed MM-YYYY slug shape must be
// rejected by the wire-format parser (YYYY-MM) rather than silently parsed
// as some other month, because the pattern anchors four digits first. The
// transposed case below uses "03-2031" — the MM-YYYY form of this package's
// own 2031-03 test month (campaign_newsletter_slug_test.go) — deliberately,
// so no test in this repository ever spells out #0404's real production
// slug (issues/0405.md §6).
func TestParseNewsletterMonth_Rejects(t *testing.T) {
	cases := []string{
		"",
		"2026-1",
		"2026-00",
		"2026-13",
		"2026-9x",
		"03-2031", // the transposed form — must not parse as anything
	}
	for _, in := range cases {
		if _, err := ParseNewsletterMonth(in); !errors.Is(err, ErrInvalidNewsletterMonth) {
			t.Errorf("ParseNewsletterMonth(%q) err = %v, want ErrInvalidNewsletterMonth", in, err)
		}
	}
}

func TestNewsletterMonth_ZeroValueIsInvalid(t *testing.T) {
	var nm NewsletterMonth
	if nm.valid() {
		t.Errorf("NewsletterMonth{}.valid() = true, want false")
	}
}
