package outreach

import (
	"testing"
	"time"
)

func TestSuggestedAt(t *testing.T) {
	start := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC) // Mon Oct 19, 7:00 PM PDT
	if got := SuggestedAt(Wave{Name: "announce"}, &start, 10); got != nil {
		t.Errorf("announce = %v, want nil", got)
	}
	if got := SuggestedAt(Wave{DaysBefore: 7}, nil, 10); got != nil {
		t.Errorf("undated = %v, want nil", got)
	}
	got := SuggestedAt(Wave{DaysBefore: 1}, &start, 10)
	if want := time.Date(2026, 10, 18, 17, 0, 0, 0, time.UTC); got == nil || !got.Equal(want) {
		t.Errorf("last call = %v, want %v", got, want)
	}
	if got := SuggestedAt(Wave{DaysBefore: 1}, &start, 99); got == nil || got.In(pacific).Hour() != DefaultPostHour {
		t.Errorf("bad hour should fall back to %d: %v", DefaultPostHour, got)
	}
	if got := SuggestedAt(Wave{DaysBefore: 1}, &start, 7); got == nil || got.In(pacific).Hour() != 7 {
		t.Errorf("configured hour not used: %v", got)
	}
}

// Daylight saving ends 2026-11-01. A suggested time is the same Pacific
// wall-clock hour on the day N days before, whichever side of the change it
// lands on, and is never one hour off the way plain subtraction would be.
func TestSuggestedAtAcrossDST(t *testing.T) {
	// Thu Nov 5, 6:30 PM PST.
	start := time.Date(2026, 11, 6, 2, 30, 0, 0, time.UTC)
	rem := SuggestedAt(Wave{DaysBefore: 7}, &start, 10)
	last := SuggestedAt(Wave{DaysBefore: 1}, &start, 10)
	if want := time.Date(2026, 10, 29, 17, 0, 0, 0, time.UTC); !rem.Equal(want) { // 10:00 PDT
		t.Errorf("reminder = %v, want %v", rem.UTC(), want)
	}
	if want := time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC); !last.Equal(want) { // 10:00 PST
		t.Errorf("last call = %v, want %v", last.UTC(), want)
	}
	for _, got := range []*time.Time{rem, last} {
		if h, m := got.In(pacific).Hour(), got.In(pacific).Minute(); h != 10 || m != 0 {
			t.Errorf("%v is %d:%02d Pacific, want 10:00", got, h, m)
		}
	}
	// A workshop the evening of Nov 1 itself, reminder across the change.
	nov1 := time.Date(2026, 11, 2, 2, 0, 0, 0, time.UTC) // Nov 1, 6:00 PM PST
	if got := SuggestedAt(Wave{DaysBefore: 1}, &nov1, 10); !got.Equal(time.Date(2026, 10, 31, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("day before the change = %v", got.UTC())
	}
}
