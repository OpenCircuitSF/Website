package cadence

import (
	"strconv"
	"strings"
	"time"
)

// Frequency is how often a rule expects an event.
type Frequency string

// Frequencies, as stored in event_cadence_rules.frequency.
const (
	Weekly  Frequency = "weekly"
	Monthly Frequency = "monthly"
)

// Limits, matching the table's CHECK constraints.
const (
	MaxNameLen   = 120
	MaxPrefixLen = 120
	MaxPerMonth  = 31
)

// Rule is one event_cadence_rules row. TitlePrefix is "" for none; PerMonth
// is 0 for a weekly rule and Weekday nil for a monthly one.
type Rule struct {
	ID          int64
	Name        string
	EventTypeID *int64
	TitlePrefix string
	Frequency   Frequency
	Weekday     *time.Weekday
	PerMonth    int
	VenueID     *int64
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// FieldOrder is the order Validate's fields are reported in, for a caller
// that shows only the first message.
var FieldOrder = []string{"name", "event_type_id", "title_prefix", "frequency", "weekday", "per_month"}

// FirstError returns the first message of a Validate result in FieldOrder,
// prefixed with its field, or "" when there is none.
func FirstError(fields map[string]string) string {
	for _, f := range FieldOrder {
		if msg, ok := fields[f]; ok {
			return f + ": " + msg
		}
	}
	return ""
}

// Normalized returns r with its name and title prefix trimmed.
func (r Rule) Normalized() Rule {
	r.Name = strings.TrimSpace(r.Name)
	r.TitlePrefix = strings.TrimSpace(r.TitlePrefix)
	return r
}

// Validate applies the table's CHECK constraints, with a message per field.
// It is nil when the rule is valid. The admin routes and v1's
// set_cadence_rule both call it; web/src/lib/eventCadence.ts mirrors it.
func (r Rule) Validate() map[string]string {
	bad := map[string]string{}
	name := strings.TrimSpace(r.Name)
	switch {
	case name == "":
		bad["name"] = "is required"
	case len([]rune(name)) > MaxNameLen:
		bad["name"] = "must be " + strconv.Itoa(MaxNameLen) + " characters or fewer"
	}
	prefix := strings.TrimSpace(r.TitlePrefix)
	if len([]rune(prefix)) > MaxPrefixLen {
		bad["title_prefix"] = "must be " + strconv.Itoa(MaxPrefixLen) + " characters or fewer"
	}
	if r.EventTypeID == nil && prefix == "" {
		bad["event_type_id"] = "set an event type, a title prefix, or both"
	}
	switch r.Frequency {
	case Weekly:
		switch {
		case r.Weekday == nil:
			bad["weekday"] = "a weekly rule needs a weekday"
		case *r.Weekday < time.Sunday || *r.Weekday > time.Saturday:
			bad["weekday"] = "must be sunday through saturday"
		}
		if r.PerMonth != 0 {
			bad["per_month"] = "only a monthly rule has a count"
		}
	case Monthly:
		if r.Weekday != nil {
			bad["weekday"] = "only a weekly rule has a weekday"
		}
		if r.PerMonth < 1 || r.PerMonth > MaxPerMonth {
			bad["per_month"] = "must be 1 to " + strconv.Itoa(MaxPerMonth)
		}
	default:
		bad["frequency"] = "must be weekly or monthly"
	}
	if len(bad) == 0 {
		return nil
	}
	return bad
}

// Describe is the rule's frequency in words: "every Tuesday", "1 a month".
func (r Rule) Describe() string {
	switch r.Frequency {
	case Weekly:
		if r.Weekday != nil {
			return "every " + r.Weekday.String()
		}
		return "weekly"
	case Monthly:
		return strconv.Itoa(r.PerMonth) + " a month"
	}
	return string(r.Frequency)
}

// ParseWeekday reads a lowercase English weekday name ("tuesday").
func ParseWeekday(s string) (time.Weekday, bool) {
	for d := time.Sunday; d <= time.Saturday; d++ {
		if s == WeekdayName(d) {
			return d, true
		}
	}
	return 0, false
}

// WeekdayName is d's lowercase English name, as v1 spells it.
func WeekdayName(d time.Weekday) string { return strings.ToLower(d.String()) }

// Matches reports whether e is one of the rule's events: every condition the
// rule sets (event type, title prefix, venue) must hold. It ignores status
// and date; Compute handles those.
func (r Rule) Matches(e Event) bool {
	if r.EventTypeID != nil && e.EventTypeID != *r.EventTypeID {
		return false
	}
	if p := strings.ToLower(strings.TrimSpace(r.TitlePrefix)); p != "" &&
		!strings.HasPrefix(strings.ToLower(strings.TrimSpace(e.Title)), p) {
		return false
	}
	if r.VenueID != nil && (e.VenueID == nil || *e.VenueID != *r.VenueID) {
		return false
	}
	return true
}
