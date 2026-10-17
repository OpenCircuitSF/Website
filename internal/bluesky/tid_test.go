package bluesky

import (
	"regexp"
	"testing"
)

var tidRE = regexp.MustCompile(`^[234567abcdefghij][2-7a-z]{12}$`)

func TestTID(t *testing.T) {
	// Hand-computed vectors: zero is thirteen lowest characters; clock 1 sets
	// the lowest bit, which is index 1 of the alphabet ("3") in the last
	// place; micros 1 shifts left ten bits = 1024 = 32*32, so the third
	// character from the end becomes "3".
	if got := TID(0, 0); got != "2222222222222" {
		t.Errorf("TID(0,0) = %q", got)
	}
	if got := TID(0, 1); got != "2222222222223" {
		t.Errorf("TID(0,1) = %q", got)
	}
	if got := TID(1, 0); got != "2222222222322" {
		t.Errorf("TID(1,0) = %q", got)
	}
	a, b := TID(1_700_000_000_000_000, 5), TID(1_700_000_000_000_001, 5)
	if !tidRE.MatchString(a) || !tidRE.MatchString(b) {
		t.Errorf("shape: %q %q", a, b)
	}
	if !(a < b) {
		t.Errorf("TIDs must sort with time: %q !< %q", a, b)
	}
	if TID(5, 1) == TID(5, 2) {
		t.Error("two clocks at the same micro must differ")
	}
	if TID(5, 1023) != TID(5, 2047) {
		t.Error("clock is masked to ten bits")
	}
}
