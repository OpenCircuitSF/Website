package luma

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	// The tests must not depend on the host's zoneinfo.
	_ "time/tzdata"
)

func loadFixture(t *testing.T) []Event {
	t.Helper()
	f, err := os.Open("testdata/luma.ics")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	evs, err := ParseICS(f)
	if err != nil {
		t.Fatalf("ParseICS: %v", err)
	}
	return evs
}

func TestParseICSFixture(t *testing.T) {
	evs := loadFixture(t)
	if len(evs) != 6 {
		t.Fatalf("events = %d, want 6", len(evs))
	}
	e := evs[0]
	if e.Summary != "Intro to Soldering" {
		t.Errorf("summary = %q", e.Summary)
	}
	if !e.Start.Equal(time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("start = %v", e.Start)
	}
	if e.End == nil || !e.End.Equal(time.Date(2026, 9, 4, 4, 0, 0, 0, time.UTC)) {
		t.Errorf("end = %v", e.End)
	}
	if e.URL != "https://luma.com/cjr6l17d" {
		t.Errorf("url = %q", e.URL)
	}
	if want := "Sequoia Fabrica Community Workshop, 1736 18th St, San Francisco, CA 94107, USA"; e.Location != want {
		t.Errorf("location = %q, want %q", e.Location, want)
	}
	if e.UID != "evt-3QwoCvTioeeY4k9@events.lu.ma" {
		t.Errorf("uid = %q", e.UID)
	}
}

func TestParseICSUnfoldsLines(t *testing.T) {
	src := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:u1\r\nDTSTART:20310101T200000Z\r\nSUMMARY:Open \r\n Build\r\n\t Night\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	evs, err := ParseICS(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Summary != "Open Build Night" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestParseICSUnescapesText(t *testing.T) {
	src := `BEGIN:VCALENDAR
BEGIN:VEVENT
UID:u1
DTSTART:20310101T200000Z
SUMMARY:Solder\, Code\; Ship
LOCATION:Room 1\NFloor 2
DESCRIPTION:Line one\nSee https://lu.ma/abc123 back\\slash
END:VEVENT
END:VCALENDAR
`
	if !strings.Contains(src, string([]byte{92, ','})) {
		t.Fatal("fixture lost its literal backslash-comma")
	}
	src = strings.ReplaceAll(src, "\n", "\r\n")
	evs, err := ParseICS(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	e := evs[0]
	if e.Summary != "Solder, Code; Ship" {
		t.Errorf("summary = %q", e.Summary)
	}
	if e.Location != "Room 1\nFloor 2" {
		t.Errorf("location = %q", e.Location)
	}
	if want := "Line one\nSee https://lu.ma/abc123 back" + string([]byte{92}) + "slash"; e.Description != want {
		t.Errorf("description = %q, want %q", e.Description, want)
	}
	if e.URL != "https://lu.ma/abc123" {
		t.Errorf("url = %q", e.URL)
	}
}

func TestParseICSTimes(t *testing.T) {
	src := "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:z\nDTSTART:20310101T200000Z\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nUID:tz\nDTSTART;TZID=America/Los_Angeles:20310301T183000\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nUID:d\nDTSTART;VALUE=DATE:20310401\nEND:VEVENT\nEND:VCALENDAR\n"
	evs, err := ParseICS(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{
		time.Date(2031, 1, 1, 20, 0, 0, 0, time.UTC),
		time.Date(2031, 3, 2, 2, 30, 0, 0, time.UTC),
		time.Date(2031, 4, 1, 7, 0, 0, 0, time.UTC),
	}
	for i, w := range want {
		if !evs[i].Start.Equal(w) {
			t.Errorf("event %d start = %v, want %v", i, evs[i].Start.UTC(), w)
		}
		if evs[i].End != nil {
			t.Errorf("event %d end = %v, want nil", i, evs[i].End)
		}
	}
}

func TestParseICSMalformed(t *testing.T) {
	ev := func(body string) string {
		return "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:x\n" + body + "END:VEVENT\nEND:VCALENDAR\n"
	}
	cases := map[string]string{
		"html page":        "<!doctype html><html><body>Not found</body></html>",
		"no END:VCALENDAR": "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:x\nDTSTART:20310101T200000Z\nEND:VEVENT\n",
		"no DTSTART":       ev("SUMMARY:x\n"),
		"DTSTART tomorrow": ev("DTSTART:tomorrow\n"),
		"unknown TZID":     ev("DTSTART;TZID=Mars/Olympus:20310101T200000\n"),
	}
	for name, src := range cases {
		if _, err := ParseICS(strings.NewReader(src)); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: err = %v, want ErrMalformed", name, err)
		}
	}
}
