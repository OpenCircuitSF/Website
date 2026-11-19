// Package ical writes RFC 5545 iCalendar documents (#0732): TEXT escaping,
// 75-octet line folding that never splits a UTF-8 sequence, a fixed
// America/Los_Angeles VTIMEZONE, and CRLF line endings. It is pure, so its
// byte-level behaviour is tested without a database.
package ical

import (
	"strings"
	"time"
	_ "time/tzdata" // embed the zone database so TZID always resolves
	"unicode/utf8"
)

// TZID is the time zone every DTSTART/DTEND is expressed in.
const TZID = "America/Los_Angeles"

var la = func() *time.Location {
	loc, err := time.LoadLocation(TZID)
	if err != nil {
		panic(err)
	}
	return loc
}()

// textEscaper escapes backslash first, so the escapes added after it are not
// themselves escaped again.
var textEscaper = strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, "\n", `\n`)

// EscapeText applies RFC 5545 section 3.3.11 TEXT escaping. CRLF and lone CR
// are normalised to LF before escaping.
func EscapeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return textEscaper.Replace(s)
}

const maxOctets = 75

// FoldLine splits one content line into physical lines of at most 75 octets
// each (a continuation's leading space counts), joined by CRLF plus a space,
// with no trailing CRLF. A cut never lands inside a UTF-8 sequence.
func FoldLine(line string) string {
	if len(line) <= maxOctets {
		return line
	}
	var b strings.Builder
	limit := maxOctets
	first := true
	for len(line) > limit {
		i := limit
		for i > 0 && !utf8.RuneStart(line[i]) {
			i--
		}
		if !first {
			b.WriteString("\r\n ")
		}
		b.WriteString(line[:i])
		line = line[i:]
		first = false
		limit = maxOctets - 1
	}
	if !first {
		b.WriteString("\r\n ")
	}
	b.WriteString(line)
	return b.String()
}

// Event is one VEVENT. End is optional (RFC 5545 section 3.6.1).
type Event struct {
	UID         string
	Start       time.Time
	End         *time.Time
	Stamp       time.Time
	Summary     string
	Location    string
	Description string
	URL         string
	Cancelled   bool
}

const vtimezone = "BEGIN:VTIMEZONE\nTZID:America/Los_Angeles\nBEGIN:DAYLIGHT\nTZOFFSETFROM:-0800\nTZOFFSETTO:-0700\nTZNAME:PDT\nDTSTART:19700308T020000\nRRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=2SU\nEND:DAYLIGHT\nBEGIN:STANDARD\nTZOFFSETFROM:-0700\nTZOFFSETTO:-0800\nTZNAME:PST\nDTSTART:19701101T020000\nRRULE:FREQ=YEARLY;BYMONTH=11;BYDAY=1SU\nEND:STANDARD\nEND:VTIMEZONE"

const localLayout = "20060102T150405"

// Calendar renders a complete VCALENDAR holding events, every line folded
// and terminated by CRLF.
func Calendar(name string, events []Event) []byte {
	var b strings.Builder
	line := func(s string) {
		b.WriteString(FoldLine(s))
		b.WriteString("\r\n")
	}
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Open Circuit SF//Events//EN")
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	line("X-WR-CALNAME:" + EscapeText(name))
	line("X-WR-TIMEZONE:" + TZID)
	for _, l := range strings.Split(vtimezone, "\n") {
		line(l)
	}
	for _, e := range events {
		line("BEGIN:VEVENT")
		line("UID:" + e.UID)
		line("DTSTAMP:" + e.Stamp.UTC().Format(localLayout) + "Z")
		line("DTSTART;TZID=" + TZID + ":" + e.Start.In(la).Format(localLayout))
		if e.End != nil {
			line("DTEND;TZID=" + TZID + ":" + e.End.In(la).Format(localLayout))
		}
		line("SUMMARY:" + EscapeText(e.Summary))
		if e.Location != "" {
			line("LOCATION:" + EscapeText(e.Location))
		}
		if e.Description != "" {
			line("DESCRIPTION:" + EscapeText(e.Description))
		}
		if e.URL != "" {
			line("URL:" + e.URL)
		}
		if e.Cancelled {
			line("STATUS:CANCELLED")
		} else {
			line("STATUS:CONFIRMED")
		}
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return []byte(b.String())
}
