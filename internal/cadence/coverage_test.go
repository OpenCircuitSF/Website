package cadence

import (
	"slices"
	"testing"
	"time"
)

// Pure tests of Compute (#0678): no database, clock or network.

func la(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func ptr[T any](v T) *T { return &v }

func weekly(id int64, typeID int64, d time.Weekday) Rule {
	return Rule{ID: id, Name: "weekly", EventTypeID: ptr(typeID), Frequency: Weekly, Weekday: ptr(d), Active: true}
}

func monthly(id int64, typeID int64, n int) Rule {
	return Rule{ID: id, Name: "monthly", EventTypeID: ptr(typeID), Frequency: Monthly, PerMonth: n, Active: true}
}

func ev(id int64, typeID int64, status string, at time.Time) Event {
	return Event{ID: id, Title: "Event", Status: status, StartsAt: at, EventTypeID: typeID}
}

func evening(t *testing.T, y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 18, 0, 0, 0, la(t))
}

func mustRFC(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func one(t *testing.T, rep Report) RuleCoverage {
	t.Helper()
	if len(rep.Rules) != 1 {
		t.Fatalf("%d rules in report, want 1", len(rep.Rules))
	}
	return rep.Rules[0]
}

func TestWeeklyRuleFiveTuesdaysDecember2026(t *testing.T) {
	m := Month{2026, time.December}
	evs := []Event{
		ev(1, 7, StatusPublished, evening(t, 2026, 12, 1)),
		ev(2, 7, StatusPublished, evening(t, 2026, 12, 8)),
		ev(3, 7, StatusPublished, evening(t, 2026, 12, 22)),
	}
	rc := one(t, Compute(m, []Rule{weekly(1, 7, time.Tuesday)}, evs))
	want := []string{"2026-12-01", "2026-12-08", "2026-12-15", "2026-12-22", "2026-12-29"}
	if !slices.Equal(rc.ExpectedDates, want) || rc.ExpectedCount != 5 {
		t.Fatalf("expected %v (%d), want %v", rc.ExpectedDates, rc.ExpectedCount, want)
	}
	if !slices.Equal(rc.MissingDates, []string{"2026-12-15", "2026-12-29"}) || rc.Missing != 2 || rc.State != StateMissing {
		t.Errorf("missing %v (%d) state %s", rc.MissingDates, rc.Missing, rc.State)
	}
	if len(rc.Events) != 3 {
		t.Errorf("%d events", len(rc.Events))
	}
}

func TestWeeklyRuleFourTuesdaysFebruary2027(t *testing.T) {
	rc := one(t, Compute(Month{2027, time.February}, []Rule{weekly(1, 7, time.Tuesday)}, nil))
	want := []string{"2027-02-02", "2027-02-09", "2027-02-16", "2027-02-23"}
	if !slices.Equal(rc.ExpectedDates, want) || rc.Missing != 4 {
		t.Errorf("expected %v missing %d", rc.ExpectedDates, rc.Missing)
	}
}

func TestMonthlyRuleCount(t *testing.T) {
	m := Month{2026, time.December}
	r := monthly(1, 7, 2)
	rc := one(t, Compute(m, []Rule{r}, []Event{ev(1, 7, StatusPublished, evening(t, 2026, 12, 3))}))
	if rc.ExpectedDates != nil || rc.ExpectedCount != 2 || rc.Missing != 1 || rc.State != StateMissing {
		t.Errorf("one of two: %+v", rc)
	}
	rc = one(t, Compute(m, []Rule{r}, []Event{
		ev(1, 7, StatusPublished, evening(t, 2026, 12, 3)),
		ev(2, 7, StatusPublished, evening(t, 2026, 12, 17)),
	}))
	if rc.Missing != 0 || rc.State != StateOK {
		t.Errorf("two of two: %+v", rc)
	}
}

func TestCanceledDoesNotCount(t *testing.T) {
	m := Month{2026, time.December}
	canceled := ev(1, 7, StatusCanceled, evening(t, 2026, 12, 1))
	rc := one(t, Compute(m, []Rule{weekly(1, 7, time.Tuesday)}, []Event{canceled}))
	if !slices.Contains(rc.MissingDates, "2026-12-01") || len(rc.Canceled) != 1 || len(rc.Events) != 0 {
		t.Errorf("weekly canceled: missing %v canceled %v events %v", rc.MissingDates, rc.Canceled, rc.Events)
	}
	rc = one(t, Compute(m, []Rule{monthly(1, 7, 1)}, []Event{canceled}))
	if rc.Missing != 1 || rc.State != StateMissing || len(rc.Canceled) != 1 {
		t.Errorf("monthly canceled: %+v", rc)
	}
}

func TestDraftsFlagged(t *testing.T) {
	m := Month{2026, time.December}
	var evs []Event
	for i, d := range []int{1, 8, 15, 22, 29} {
		status := StatusPublished
		if d == 15 {
			status = StatusDraft
		}
		evs = append(evs, ev(int64(i+1), 7, status, evening(t, 2026, 12, d)))
	}
	rc := one(t, Compute(m, []Rule{weekly(1, 7, time.Tuesday)}, evs))
	if !slices.Equal(rc.DraftOnlyDates, []string{"2026-12-15"}) || len(rc.MissingDates) != 0 || rc.State != StateDrafts {
		t.Errorf("weekly: draft-only %v missing %v state %s", rc.DraftOnlyDates, rc.MissingDates, rc.State)
	}
	if len(rc.Drafts) != 1 || rc.Drafts[0].ID != 3 {
		t.Errorf("drafts %v", rc.Drafts)
	}

	rc = one(t, Compute(m, []Rule{monthly(1, 7, 2)}, []Event{
		ev(1, 7, StatusPublished, evening(t, 2026, 12, 3)),
		ev(2, 7, StatusDraft, evening(t, 2026, 12, 17)),
	}))
	if rc.Missing != 0 || rc.State != StateDrafts || len(rc.Drafts) != 1 || rc.Drafts[0].ID != 2 {
		t.Errorf("monthly: %+v", rc)
	}
}

func TestDSTNovember2026(t *testing.T) {
	m := Month{2026, time.November}
	from, to := m.Bounds()
	if got := from.Format(time.RFC3339); got != "2026-11-01T00:00:00-07:00" {
		t.Errorf("from %s", got)
	}
	if got := to.Format(time.RFC3339); got != "2026-12-01T00:00:00-08:00" {
		t.Errorf("to %s", got)
	}
	r := weekly(1, 7, time.Sunday)
	before := ev(1, 7, StatusPublished, mustRFC(t, "2026-11-01T01:30:00-07:00"))
	repeated := ev(2, 7, StatusPublished, mustRFC(t, "2026-11-01T01:30:00-08:00"))
	for _, e := range []Event{before, repeated} {
		rc := one(t, Compute(m, []Rule{r}, []Event{e}))
		if rc.ExpectedDates[0] != "2026-11-01" || slices.Contains(rc.MissingDates, "2026-11-01") {
			t.Errorf("%s: expected %v missing %v", e.StartsAt, rc.ExpectedDates, rc.MissingDates)
		}
	}
	late := ev(3, 7, StatusPublished, mustRFC(t, "2026-11-30T23:30:00-08:00"))
	if rep := Compute(m, nil, []Event{late}); len(rep.Unmatched) != 1 {
		t.Errorf("Nov 30 23:30 PST not in November: %v", rep.Unmatched)
	}
	if rep := Compute(Month{2026, time.December}, nil, []Event{late}); len(rep.Unmatched) != 0 {
		t.Errorf("Nov 30 23:30 PST counted in December: %v", rep.Unmatched)
	}
}

func TestDSTMarch2027(t *testing.T) {
	m := Month{2027, time.March}
	from, to := m.Bounds()
	if from.Format(time.RFC3339) != "2027-03-01T00:00:00-08:00" || to.Format(time.RFC3339) != "2027-04-01T00:00:00-07:00" {
		t.Errorf("bounds %s – %s", from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	rc := one(t, Compute(m, []Rule{weekly(1, 7, time.Sunday)}, nil))
	want := []string{"2027-03-07", "2027-03-14", "2027-03-21", "2027-03-28"}
	if !slices.Equal(rc.ExpectedDates, want) {
		t.Errorf("sundays %v, want %v", rc.ExpectedDates, want)
	}
	// 10:00 PDT on the change day is still Mar 14.
	rc = one(t, Compute(m, []Rule{weekly(1, 7, time.Sunday)}, []Event{ev(1, 7, StatusPublished, mustRFC(t, "2027-03-14T10:00:00-07:00"))}))
	if slices.Contains(rc.MissingDates, "2027-03-14") {
		t.Errorf("Mar 14 missing: %v", rc.MissingDates)
	}
}

func TestMonthBoundsArePacific(t *testing.T) {
	m := Month{2026, time.December}
	lastNov := ev(1, 7, StatusPublished, mustRFC(t, "2026-12-01T07:59:00Z"))
	firstDec := ev(2, 7, StatusPublished, mustRFC(t, "2026-12-01T08:00:00Z"))
	rep := Compute(m, nil, []Event{lastNov, firstDec})
	if len(rep.Unmatched) != 1 || rep.Unmatched[0].ID != 2 {
		t.Errorf("unmatched %v, want only id 2", rep.Unmatched)
	}
}

func TestMatchTypePrefixVenue(t *testing.T) {
	venue := int64(40)
	other := int64(41)
	e := Event{ID: 1, Title: "Programming LEDs: intro", Status: StatusPublished, EventTypeID: 7, VenueID: &venue}
	cases := []struct {
		name string
		rule Rule
		want bool
	}{
		{"type only", Rule{EventTypeID: ptr(int64(7))}, true},
		{"wrong type", Rule{EventTypeID: ptr(int64(8))}, false},
		{"prefix only, case-insensitive", Rule{TitlePrefix: "programming leds"}, true},
		{"prefix with spaces", Rule{TitlePrefix: "  Programming  "}, true},
		{"prefix miss", Rule{TitlePrefix: "hack night"}, false},
		{"type and prefix", Rule{EventTypeID: ptr(int64(7)), TitlePrefix: "Programming"}, true},
		{"type right, prefix wrong", Rule{EventTypeID: ptr(int64(7)), TitlePrefix: "Hack"}, false},
		{"prefix right, type wrong", Rule{EventTypeID: ptr(int64(8)), TitlePrefix: "Programming"}, false},
		{"venue match", Rule{EventTypeID: ptr(int64(7)), VenueID: &venue}, true},
		{"venue narrows", Rule{EventTypeID: ptr(int64(7)), VenueID: &other}, false},
	}
	for _, c := range cases {
		if got := c.rule.Matches(e); got != c.want {
			t.Errorf("%s: Matches = %v, want %v", c.name, got, c.want)
		}
	}
	noVenue := e
	noVenue.VenueID = nil
	if (Rule{EventTypeID: ptr(int64(7)), VenueID: &venue}).Matches(noVenue) {
		t.Error("a venue rule matched an event with no venue")
	}

	// One event counts toward both rules it matches.
	m := Month{2026, time.December}
	e.StartsAt = evening(t, 2026, 12, 1)
	rules := []Rule{weekly(1, 7, time.Tuesday), {ID: 2, Name: "leds", TitlePrefix: "Programming LEDs", Frequency: Monthly, PerMonth: 1, Active: true}}
	rep := Compute(m, rules, []Event{e})
	if len(rep.Rules) != 2 || len(rep.Rules[0].Events) != 1 || len(rep.Rules[1].Events) != 1 || rep.Rules[1].State != StateOK {
		t.Errorf("two rules: %+v", rep.Rules)
	}
	if len(rep.Unmatched) != 0 {
		t.Errorf("unmatched %v", rep.Unmatched)
	}
}

func TestUnmatchedAndInactive(t *testing.T) {
	m := Month{2026, time.December}
	inactive := weekly(1, 7, time.Tuesday)
	inactive.Active = false
	evs := []Event{
		ev(1, 7, StatusPublished, evening(t, 2026, 12, 1)),
		ev(2, 8, StatusDraft, evening(t, 2026, 12, 2)),
		ev(3, 8, StatusCanceled, evening(t, 2026, 12, 3)),
	}
	rep := Compute(m, []Rule{inactive}, evs)
	if len(rep.Rules) != 0 {
		t.Errorf("inactive rule reported: %+v", rep.Rules)
	}
	var ids []int64
	for _, e := range rep.Unmatched {
		ids = append(ids, e.ID)
	}
	if !slices.Equal(ids, []int64{1, 2}) {
		t.Errorf("unmatched %v, want [1 2] (canceled excluded, inactive rule matches nothing)", ids)
	}
}

func TestParseMonthAndNextMonth(t *testing.T) {
	if m, err := ParseMonth("2026-12"); err != nil || m != (Month{2026, time.December}) || m.String() != "2026-12" || m.Label() != "December 2026" {
		t.Errorf("ParseMonth(2026-12) = %v, %v", m, err)
	}
	for _, s := range []string{"2026-13", "2026-00", "26-12", "", "2026-1", "2026-12-01"} {
		if _, err := ParseMonth(s); err == nil {
			t.Errorf("ParseMonth(%q) accepted", s)
		}
	}
	if got := NextMonth(mustRFC(t, "2026-12-31T23:30:00-08:00")); got != (Month{2027, time.January}) {
		t.Errorf("NextMonth(Dec 31 PST) = %v", got)
	}
	if got := NextMonth(mustRFC(t, "2027-01-01T07:30:00Z")); got != (Month{2027, time.January}) {
		t.Errorf("NextMonth(Jan 1 07:30Z, still Dec 31 Pacific) = %v", got)
	}
	if got := NextMonth(mustRFC(t, "2026-11-15T12:00:00-08:00")); got != (Month{2026, time.December}) {
		t.Errorf("NextMonth(Nov 15) = %v", got)
	}
}
