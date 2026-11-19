package ical

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// bs is one backslash, built at runtime so no typed escape can land as
// something else on disk (AGENTS.md section 8).
var bs = string(rune(92))

func unfold(s string) string { return strings.ReplaceAll(s, "\r\n ", "") }

func TestEscapeText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a,b", "a" + bs + ",b"},
		{"a;b", "a" + bs + ";b"},
		{"a" + bs + "b", "a" + bs + bs + "b"},
		{"a\nb", "a" + bs + "nb"},
		{"a\r\nb", "a" + bs + "nb"},
		{"a\rb", "a" + bs + "nb"},
		{bs + ",", bs + bs + bs + ","},
	}
	for _, c := range cases {
		if got := EscapeText(c.in); got != c.want {
			t.Errorf("EscapeText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func checkFolded(t *testing.T, in, out string) []string {
	t.Helper()
	phys := strings.Split(out, "\r\n")
	for i, p := range phys {
		if len(p) > 75 {
			t.Errorf("physical line %d is %d octets: %q", i, len(p), p)
		}
		if i > 0 && (!strings.HasPrefix(p, " ") || strings.HasPrefix(p, "  ")) {
			t.Errorf("continuation %d does not start with exactly one space: %q", i, p)
		}
		if !utf8.ValidString(p) {
			t.Errorf("physical line %d is not valid UTF-8: %q", i, p)
		}
	}
	if got := unfold(out); got != in {
		t.Errorf("unfold round-trip mismatch:\n got %q\nwant %q", got, in)
	}
	return phys
}

func TestFoldLine_ShortLineUnchanged(t *testing.T) {
	in := strings.Repeat("a", 75)
	if got := FoldLine(in); got != in {
		t.Errorf("75-octet line was folded: %q", got)
	}
}

func TestFoldLine_ASCIIAt75Octets(t *testing.T) {
	in := strings.Repeat("abcdefghij", 20)
	phys := checkFolded(t, in, FoldLine(in))
	if len(phys) < 3 || len(phys[0]) != 75 {
		t.Errorf("unexpected fold: %d lines, first %d octets", len(phys), len(phys[0]))
	}
}

func TestFoldLine_MultiByteAtBoundary(t *testing.T) {
	prefix := strings.Repeat("x", 74)
	for _, r := range []string{"é", "€", "😀"} {
		in := prefix + r + strings.Repeat("y", 100) + r + "z"
		phys := checkFolded(t, in, FoldLine(in))
		if phys[0] != prefix {
			t.Errorf("rune %q: first physical line = %q, want the 74 ASCII bytes only", r, phys[0])
		}
	}
}

// TestFoldLine_InvalidUTF8Terminates pins that a window holding no rune
// start (only continuation octets) is cut at the limit instead of making the
// back-off reach zero and loop forever.
func TestFoldLine_InvalidUTF8Terminates(t *testing.T) {
	in := strings.Repeat(string([]byte{0x80}), 200)
	done := make(chan string, 1)
	go func() { done <- FoldLine(in) }()
	select {
	case out := <-done:
		for i, p := range strings.Split(out, "\r\n") {
			if len(p) > 75 {
				t.Errorf("physical line %d is %d octets", i, len(p))
			}
		}
		if unfold(out) != in {
			t.Error("unfold round-trip mismatch")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FoldLine did not return on invalid UTF-8")
	}
}

func sample() Event {
	end := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	return Event{
		UID:     "event-1@www.example.test",
		Start:   time.Date(2026, 7, 10, 1, 0, 0, 0, time.UTC),
		End:     &end,
		Stamp:   time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC),
		Summary: "Solder, flux; and you",
		URL:     "https://www.example.test/events/a,b;c",
	}
}

func TestCalendar_StructureAndCRLF(t *testing.T) {
	out := string(Calendar("Open Circuit SF", []Event{sample(), sample()}))
	if !strings.HasSuffix(out, "\r\n") {
		t.Fatal("document does not end in CRLF")
	}
	if strings.Count(out, "\n") != strings.Count(out, "\r\n") {
		t.Error("bare LF present")
	}
	for _, blk := range []string{"VCALENDAR", "VTIMEZONE", "VEVENT", "DAYLIGHT", "STANDARD"} {
		if b, e := strings.Count(out, "BEGIN:"+blk+"\r\n"), strings.Count(out, "END:"+blk+"\r\n"); b != e || b == 0 {
			t.Errorf("%s BEGIN=%d END=%d", blk, b, e)
		}
	}
	if strings.Count(out, "BEGIN:VTIMEZONE") != 1 {
		t.Error("VTIMEZONE must appear exactly once")
	}
	for _, want := range []string{"TZID:America/Los_Angeles\r\n", "BYMONTH=3;BYDAY=2SU", "BYMONTH=11;BYDAY=1SU"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestCalendar_EventTimesInLosAngeles(t *testing.T) {
	summer := sample()
	winter := sample()
	winter.Start = time.Date(2026, 12, 5, 2, 0, 0, 0, time.UTC)
	out := string(Calendar("x", []Event{summer, winter}))
	for _, want := range []string{
		"DTSTART;TZID=America/Los_Angeles:20260709T180000\r\n",
		"DTSTART;TZID=America/Los_Angeles:20261204T180000\r\n",
		"DTEND;TZID=America/Los_Angeles:20260709T200000\r\n",
		"DTSTAMP:20260601T123000Z\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestCalendar_OmitsDTENDWhenNoEnd(t *testing.T) {
	e := sample()
	e.End = nil
	if out := string(Calendar("x", []Event{e})); strings.Contains(out, "DTEND") {
		t.Error("DTEND emitted with no end")
	}
}

func TestCalendar_CancelledStatus(t *testing.T) {
	c := sample()
	c.Cancelled = true
	out := string(Calendar("x", []Event{c}))
	if !strings.Contains(out, "STATUS:CANCELLED\r\n") || strings.Contains(out, "STATUS:CONFIRMED") {
		t.Errorf("cancelled event status wrong:\n%s", out)
	}
	out = string(Calendar("x", []Event{sample()}))
	if !strings.Contains(out, "STATUS:CONFIRMED\r\n") {
		t.Error("confirmed status missing")
	}
}

func TestCalendar_EscapesTextFieldsNotURL(t *testing.T) {
	out := unfold(string(Calendar("x", []Event{sample()})))
	if !strings.Contains(out, "SUMMARY:Solder"+bs+", flux"+bs+"; and you\r\n") {
		t.Errorf("summary not escaped:\n%s", out)
	}
	if !strings.Contains(out, "URL:https://www.example.test/events/a,b;c\r\n") {
		t.Errorf("URL not verbatim:\n%s", out)
	}
}
