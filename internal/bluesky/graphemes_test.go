package bluesky

import (
	"errors"
	"strings"
	"testing"
)

func TestGraphemeCount(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want int
	}{
		{"ascii", "hello", 5},
		{"precomposed", "café", 4},
		{"combining", "café", 4},
		{"zwj family", "\U0001F469‍\U0001F469‍\U0001F467", 1},
		{"flag", "\U0001F1FA\U0001F1F8", 1},
		{"skin tone", "\U0001F44D\U0001F3FD", 1},
		{"empty", "", 0},
	}
	for _, tc := range cases {
		if got := GraphemeCount(tc.s); got != tc.want {
			t.Errorf("%s: GraphemeCount = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestValidateText_Limits(t *testing.T) {
	if err := ValidateText(strings.Repeat("\U0001F527", 300)); err != nil {
		t.Errorf("300 emoji should pass: %v", err)
	}
	err := ValidateText(strings.Repeat("\U0001F527", 301))
	if !errors.Is(err, ErrTextTooLong) || !IsPermanent(err) {
		t.Errorf("301 emoji: err = %v, want permanent ErrTextTooLong", err)
	}
	// 1001 three-byte CJK runes is 1001 graphemes (over 300) AND 3003 bytes;
	// 300 of them is 900 bytes and fine, so check the byte limit with a
	// string whose grapheme count stays low: combining marks pile onto one
	// base character, so 1000 marks make one grapheme and over 3000 bytes.
	long := "a" + strings.Repeat("́", 1501) // 1 grapheme, 3003 bytes
	if GraphemeCount(long) != 1 || len(long) <= MaxTextBytes {
		t.Fatalf("fixture wrong: %d graphemes, %d bytes", GraphemeCount(long), len(long))
	}
	if err := ValidateText(long); !errors.Is(err, ErrTextTooLong) {
		t.Errorf("byte limit: err = %v, want ErrTextTooLong", err)
	}
	if err := ValidateText(strings.Repeat("京", 1001)); !errors.Is(err, ErrTextTooLong) {
		t.Errorf("1001 CJK runes should fail: %v", err)
	}
	if err := ValidateText("  \n"); !errors.Is(err, ErrInvalid) || !IsPermanent(err) {
		t.Errorf("blank text: err = %v, want permanent ErrInvalid", err)
	}
}
