package luma

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Event is one VEVENT from a Luma calendar feed. End is nil when the feed
// gives no DTEND.
type Event struct {
	UID, Summary, Location, Description, URL string
	Start                                    time.Time
	End                                      *time.Time
}

// ErrMalformed reports a feed that is not a readable iCalendar document.
var ErrMalformed = errors.New("luma: malformed calendar feed")

var lumaLink = regexp.MustCompile(`https://(?:luma\.com|lu\.ma)/[A-Za-z0-9_-]+`)

// ParseICS parses an iCalendar document and returns its events in feed order.
// It keeps only what the comparison needs and rejects anything it cannot read
// with an error wrapping ErrMalformed.
func ParseICS(r io.Reader) ([]Event, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64*1024), len(text)+1)
	for sc.Scan() {
		l := sc.Text()
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	first := ""
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			first = strings.TrimSpace(l)
			break
		}
	}
	if first != "BEGIN:VCALENDAR" {
		return nil, fmt.Errorf("%w: no BEGIN:VCALENDAR", ErrMalformed)
	}
	ended := false
	var events []Event
	var cur map[string]prop
	for _, l := range lines {
		name, params, value, ok := splitLine(l)
		if !ok {
			continue
		}
		switch {
		case name == "BEGIN" && value == "VEVENT":
			cur = map[string]prop{}
		case name == "END" && value == "VEVENT" && cur != nil:
			ev, err := buildEvent(cur)
			if err != nil {
				return nil, err
			}
			events = append(events, ev)
			cur = nil
		case name == "END" && value == "VCALENDAR":
			ended = true
		case cur != nil:
			switch name {
			case "UID", "SUMMARY", "LOCATION", "DESCRIPTION", "URL", "DTSTART", "DTEND":
				cur[name] = prop{params: params, value: value}
			}
		}
	}
	if !ended {
		return nil, fmt.Errorf("%w: no END:VCALENDAR", ErrMalformed)
	}
	return events, nil
}

type prop struct {
	params map[string]string
	value  string
}

// splitLine splits a content line at the first colon outside a quoted
// parameter value. The name is upper-cased; parameter names too.
func splitLine(l string) (name string, params map[string]string, value string, ok bool) {
	inQuote := false
	colon := -1
	for i := 0; i < len(l); i++ {
		switch l[i] {
		case '"':
			inQuote = !inQuote
		case ':':
			if !inQuote {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon < 0 {
		return "", nil, "", false
	}
	head, value := l[:colon], l[colon+1:]
	parts := strings.Split(head, ";")
	name = strings.ToUpper(strings.TrimSpace(parts[0]))
	params = map[string]string{}
	for _, p := range parts[1:] {
		k, v, _ := strings.Cut(p, "=")
		params[strings.ToUpper(k)] = strings.Trim(v, `"`)
	}
	return name, params, value, true
}

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
			case 'n', 'N':
				b.WriteByte('\n')
			case ',':
				b.WriteByte(',')
			case ';':
				b.WriteByte(';')
			default:
				b.WriteByte(s[i])
				continue
			}
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func buildEvent(p map[string]prop) (Event, error) {
	ev := Event{
		UID:         unescape(p["UID"].value),
		Summary:     unescape(p["SUMMARY"].value),
		Location:    unescape(p["LOCATION"].value),
		Description: unescape(p["DESCRIPTION"].value),
		URL:         strings.TrimSpace(p["URL"].value),
	}
	st, ok := p["DTSTART"]
	if !ok {
		return Event{}, fmt.Errorf("%w: event %q has no DTSTART", ErrMalformed, ev.UID)
	}
	start, err := parseTime(st)
	if err != nil {
		return Event{}, fmt.Errorf("%w: event %q: %v", ErrMalformed, ev.UID, err)
	}
	ev.Start = start
	if en, ok := p["DTEND"]; ok {
		end, err := parseTime(en)
		if err != nil {
			return Event{}, fmt.Errorf("%w: event %q: %v", ErrMalformed, ev.UID, err)
		}
		ev.End = &end
	}
	if ev.URL == "" {
		ev.URL = lumaLink.FindString(ev.Description)
	}
	return ev, nil
}

func parseTime(p prop) (time.Time, error) {
	v := strings.TrimSpace(p.value)
	if p.params["VALUE"] == "DATE" || len(v) == 8 {
		return time.ParseInLocation("20060102", v, pacific)
	}
	if strings.HasSuffix(v, "Z") {
		return time.Parse("20060102T150405Z", v)
	}
	tz := p.params["TZID"]
	if tz == "" {
		return time.Time{}, fmt.Errorf("time %q has no zone", v)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("unknown zone %q", tz)
	}
	return time.ParseInLocation("20060102T150405", v, loc)
}
