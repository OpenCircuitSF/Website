package cadence

import (
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	typeID := int64(7)
	good := Rule{Name: "Hack night", EventTypeID: &typeID, Frequency: Weekly, Weekday: ptr(time.Tuesday)}
	if bad := good.Validate(); bad != nil {
		t.Fatalf("valid weekly rule refused: %v", bad)
	}
	if bad := (Rule{Name: "LEDs", TitlePrefix: "Programming LEDs", Frequency: Monthly, PerMonth: 1}).Validate(); bad != nil {
		t.Fatalf("valid monthly rule refused: %v", bad)
	}
	cases := []struct {
		name  string
		edit  func(*Rule)
		field string
	}{
		{"weekly without weekday", func(r *Rule) { r.Weekday = nil }, "weekday"},
		{"weekday out of range", func(r *Rule) { r.Weekday = ptr(time.Weekday(7)) }, "weekday"},
		{"weekly with a count", func(r *Rule) { r.PerMonth = 2 }, "per_month"},
		{"monthly with weekday", func(r *Rule) { r.Frequency = Monthly; r.PerMonth = 1 }, "weekday"},
		{"per_month 0", func(r *Rule) { r.Frequency = Monthly; r.Weekday = nil; r.PerMonth = 0 }, "per_month"},
		{"per_month 32", func(r *Rule) { r.Frequency = Monthly; r.Weekday = nil; r.PerMonth = 32 }, "per_month"},
		{"no type and no prefix", func(r *Rule) { r.EventTypeID = nil; r.TitlePrefix = "  " }, "event_type_id"},
		{"blank name", func(r *Rule) { r.Name = "  " }, "name"},
		{"long name", func(r *Rule) { r.Name = strings.Repeat("x", 121) }, "name"},
		{"long prefix", func(r *Rule) { r.TitlePrefix = strings.Repeat("x", 121) }, "title_prefix"},
		{"unknown frequency", func(r *Rule) { r.Frequency = "daily" }, "frequency"},
	}
	for _, c := range cases {
		r := good
		c.edit(&r)
		bad := r.Validate()
		if bad[c.field] == "" {
			t.Errorf("%s: %v does not name %s", c.name, bad, c.field)
		}
	}
	if got := FirstError(map[string]string{"per_month": "x", "name": "is required"}); got != "name: is required" {
		t.Errorf("FirstError = %q", got)
	}
}

func TestDescribe(t *testing.T) {
	cases := map[string]Rule{
		"every Tuesday": {Frequency: Weekly, Weekday: ptr(time.Tuesday)},
		"1 a month":     {Frequency: Monthly, PerMonth: 1},
		"3 a month":     {Frequency: Monthly, PerMonth: 3},
	}
	for want, r := range cases {
		if got := r.Describe(); got != want {
			t.Errorf("Describe = %q, want %q", got, want)
		}
	}
}

func TestParseWeekday(t *testing.T) {
	for d := time.Sunday; d <= time.Saturday; d++ {
		got, ok := ParseWeekday(WeekdayName(d))
		if !ok || got != d {
			t.Errorf("round trip %v: %v %v", d, got, ok)
		}
	}
	for _, s := range []string{"tues", "Tuesday", "", "funday"} {
		if _, ok := ParseWeekday(s); ok {
			t.Errorf("ParseWeekday(%q) accepted", s)
		}
	}
}
