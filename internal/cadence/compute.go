package cadence

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// Pacific is the zone rules are stated in. cmd/opencircuit imports
// time/tzdata, so it resolves on any host.
var Pacific = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic("cadence: load America/Los_Angeles: " + err.Error())
	}
	return loc
}()

// Event statuses, as workshops_status_check spells them.
const (
	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusCanceled  = "canceled"
)

// Rule states in a report.
const (
	StateOK      = "ok"
	StateDrafts  = "drafts"
	StateMissing = "missing"
)

// dateLayout is how expected and missing dates are written.
const dateLayout = "2006-01-02"

// Month is one calendar month in Pacific time.
type Month struct {
	Year  int
	Month time.Month
}

var monthPattern = regexp.MustCompile(`^(\d{4})-(\d{2})$`)

// ErrBadMonth is ParseMonth's error for anything but YYYY-MM.
var ErrBadMonth = errors.New("cadence: month must be YYYY-MM")

// ParseMonth reads "2026-12".
func ParseMonth(s string) (Month, error) {
	m := monthPattern.FindStringSubmatch(s)
	if m == nil {
		return Month{}, ErrBadMonth
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	if y < 1 || mo < 1 || mo > 12 {
		return Month{}, ErrBadMonth
	}
	return Month{Year: y, Month: time.Month(mo)}, nil
}

// NextMonth is the month after now's Pacific month.
func NextMonth(now time.Time) Month {
	p := now.In(Pacific)
	n := time.Date(p.Year(), p.Month()+1, 1, 12, 0, 0, 0, Pacific)
	return Month{Year: n.Year(), Month: n.Month()}
}

// String is "2026-12".
func (m Month) String() string { return fmt.Sprintf("%04d-%02d", m.Year, int(m.Month)) }

// Label is "December 2026".
func (m Month) Label() string { return fmt.Sprintf("%s %d", m.Month, m.Year) }

// Bounds is [first midnight of the month, first midnight of the next) in
// Pacific time. The two can have different offsets across a DST change.
func (m Month) Bounds() (from, to time.Time) {
	return time.Date(m.Year, m.Month, 1, 0, 0, 0, 0, Pacific), time.Date(m.Year, m.Month+1, 1, 0, 0, 0, 0, Pacific)
}

// Event is the part of an event the calculation reads.
type Event struct {
	ID          int64
	Slug        string
	Title       string
	Status      string
	StartsAt    time.Time
	EventTypeID int64
	VenueID     *int64
}

// date is e's Pacific calendar date.
func (e Event) date() string { return e.StartsAt.In(Pacific).Format(dateLayout) }

// Report is one month's coverage.
type Report struct {
	Month     Month
	From, To  time.Time
	Rules     []RuleCoverage
	Unmatched []Event
}

// RuleCoverage is one active rule's result for the month.
type RuleCoverage struct {
	Rule Rule
	// ExpectedDates is every <weekday> date in the month for a weekly rule
	// ("2006-01-02"), nil for a monthly one.
	ExpectedDates []string
	// ExpectedCount is len(ExpectedDates) for weekly, PerMonth for monthly.
	ExpectedCount int
	// Events are the matching events that are not canceled, in start order.
	// For a weekly rule an event on another weekday is listed but fills no
	// date.
	Events []Event
	// Canceled are matching canceled events: listed, never counted.
	Canceled []Event
	// MissingDates are expected dates with no published or draft event.
	MissingDates []string
	// DraftOnlyDates are expected dates covered only by drafts.
	DraftOnlyDates []string
	// Drafts are the matching events still in draft.
	Drafts []Event
	// Missing is len(MissingDates) for weekly, max(0, PerMonth -
	// len(Events)) for monthly.
	Missing int
	// State is StateMissing when anything is missing, StateDrafts when
	// covered but partly only by drafts, else StateOK.
	State string
}

// Compute is the coverage of m by events under rules. Inactive rules are
// skipped. An event counts toward every rule it matches; Unmatched is the
// month's non-canceled events that match no active rule. Only events
// starting in m's Pacific bounds are read.
func Compute(m Month, rules []Rule, events []Event) Report {
	from, to := m.Bounds()
	var inMonth []Event
	for _, e := range events {
		if !e.StartsAt.Before(from) && e.StartsAt.Before(to) {
			inMonth = append(inMonth, e)
		}
	}
	slices.SortStableFunc(inMonth, func(a, b Event) int {
		if c := a.StartsAt.Compare(b.StartsAt); c != 0 {
			return c
		}
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})

	rep := Report{Month: m, From: from, To: to}
	matched := make([]bool, len(inMonth))
	for _, r := range rules {
		if !r.Active {
			continue
		}
		rc := RuleCoverage{Rule: r}
		for i, e := range inMonth {
			if !r.Matches(e) {
				continue
			}
			matched[i] = true
			switch e.Status {
			case StatusCanceled:
				rc.Canceled = append(rc.Canceled, e)
				continue
			case StatusDraft:
				rc.Drafts = append(rc.Drafts, e)
			}
			rc.Events = append(rc.Events, e)
		}
		switch r.Frequency {
		case Weekly:
			coverWeekly(&rc, m)
		default:
			rc.ExpectedCount = r.PerMonth
			rc.Missing = max(0, r.PerMonth-len(rc.Events))
			published := len(rc.Events) - len(rc.Drafts)
			switch {
			case rc.Missing > 0:
				rc.State = StateMissing
			case published < r.PerMonth:
				rc.State = StateDrafts
			default:
				rc.State = StateOK
			}
		}
		rep.Rules = append(rep.Rules, rc)
	}
	for i, e := range inMonth {
		if !matched[i] && e.Status != StatusCanceled {
			rep.Unmatched = append(rep.Unmatched, e)
		}
	}
	return rep
}

// coverWeekly fills a weekly rule's expected, missing and draft-only dates.
// Dates are enumerated at noon Pacific, so a DST change never moves one.
func coverWeekly(rc *RuleCoverage, m Month) {
	published, drafted := map[string]bool{}, map[string]bool{}
	for _, e := range rc.Events {
		if e.Status == StatusDraft {
			drafted[e.date()] = true
		} else {
			published[e.date()] = true
		}
	}
	for d := 1; ; d++ {
		t := time.Date(m.Year, m.Month, d, 12, 0, 0, 0, Pacific)
		if t.Month() != m.Month {
			break
		}
		if rc.Rule.Weekday == nil || t.Weekday() != *rc.Rule.Weekday {
			continue
		}
		key := t.Format(dateLayout)
		rc.ExpectedDates = append(rc.ExpectedDates, key)
		switch {
		case published[key]:
		case drafted[key]:
			rc.DraftOnlyDates = append(rc.DraftOnlyDates, key)
		default:
			rc.MissingDates = append(rc.MissingDates, key)
		}
	}
	rc.ExpectedCount = len(rc.ExpectedDates)
	rc.Missing = len(rc.MissingDates)
	switch {
	case rc.Missing > 0:
		rc.State = StateMissing
	case len(rc.DraftOnlyDates) > 0:
		rc.State = StateDrafts
	default:
		rc.State = StateOK
	}
}
