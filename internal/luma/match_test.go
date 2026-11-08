package luma

import (
	"reflect"
	"testing"
	"time"
)

const siteAddr = "1736 18th Street, San Francisco, CA 94107"

// The three address forms production's workshops carry, read 2026-11-07.
const (
	prodAddrNoComma   = "1736 18th Street, San Francisco CA 94107"
	prodAddrStNoComma = "1736 18th St San Francisco, CA 94107"
	prodAddrZip4      = "1736 18th Street, San Francisco CA 94107-2343"
)

func pt(mo time.Month, d, h, m int) time.Time {
	return time.Date(2026, mo, d, h, m, 0, 0, pacific)
}

func ws(id int64, title string, start, end time.Time) Workshop {
	return Workshop{ID: id, Title: title, Start: start, End: &end, Address: siteAddr}
}

func production() []Workshop {
	at := func(w Workshop, addr string) Workshop { w.Address = addr; return w }
	return []Workshop{
		at(ws(1, "Introduction to Soldering", pt(9, 3, 18, 30), pt(9, 3, 21, 0)), prodAddrNoComma),
		at(ws(2, "Programming LEDs", pt(9, 17, 18, 30), pt(9, 17, 20, 30)), prodAddrStNoComma),
		at(ws(3, "Introduction to Soldering", pt(10, 1, 18, 30), pt(10, 1, 21, 0)), prodAddrNoComma),
		at(ws(4, "Programming LEDs", pt(10, 19, 18, 30), pt(10, 19, 19, 30)), prodAddrStNoComma),
		at(ws(5, "Hack Night", pt(11, 9, 19, 0), pt(11, 9, 22, 0)), prodAddrZip4),
		at(ws(6, "Programming LEDs", pt(11, 16, 18, 30), pt(11, 16, 20, 30)), prodAddrStNoComma),
		at(ws(7, "Hack Night", pt(11, 16, 19, 0), pt(11, 16, 22, 0)), prodAddrZip4),
	}
}

func byID(rs []Result) map[int64]Result {
	m := map[int64]Result{}
	for _, r := range rs {
		m[r.WorkshopID] = r
	}
	return m
}

func TestCompareFixtureAgainstProduction(t *testing.T) {
	rs, left := Compare(production(), loadFixture(t), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if len(rs) != 7 {
		t.Fatalf("results = %d", len(rs))
	}
	for i, r := range rs {
		if r.WorkshopID != int64(i+1) {
			t.Fatalf("results out of order: %+v", rs)
		}
	}
	m := byID(rs)
	want := map[int64][]Diff{
		1: {{"start", "Start", "6:30 PM", "6:00 PM"}},
		2: {{"start", "Start", "6:30 PM", "7:00 PM"}, {"end", "End", "8:30 PM", "8:00 PM"}},
		3: {{"start", "Start", "6:30 PM", "6:00 PM"}},
	}
	for id, d := range want {
		if m[id].State != StateDiffers || !reflect.DeepEqual(m[id].Diffs, d) {
			t.Errorf("workshop %d = %s %+v, want differs %+v", id, m[id].State, m[id].Diffs, d)
		}
	}
	if m[4].State != StateNotOnLuma || m[4].Event != nil || m[4].Diffs == nil {
		t.Errorf("workshop 4 = %+v", m[4])
	}
	for id, u := range map[int64]string{5: "k9aszuib", 6: "6ssnei53", 7: "3y6a2mnm"} {
		r := m[id]
		if r.State != StateOnLuma || r.Event == nil || r.Event.URL != "https://luma.com/"+u || len(r.Diffs) != 0 {
			t.Errorf("workshop %d = %+v", id, r)
		}
	}
	if len(left) != 0 {
		t.Errorf("leftovers = %+v", left)
	}
}

func TestCompareTitleWordingAlone(t *testing.T) {
	rs, _ := Compare([]Workshop{ws(1, "Introduction to Soldering", pt(9, 3, 18, 0), pt(9, 3, 21, 0))}, loadFixture(t), time.Time{})
	r := rs[0]
	if r.State != StateOnLuma || r.Event.Summary != "Intro to Soldering" || len(r.Diffs) != 0 {
		t.Fatalf("result = %+v", r)
	}
}

func TestCompareSameDayPicksByTitle(t *testing.T) {
	all := production()
	rs, _ := Compare([]Workshop{all[6], all[5]}, loadFixture(t), time.Time{})
	m := byID(rs)
	if m[7].Event == nil || m[7].Event.Summary != "Hack Night" || m[7].State != StateOnLuma {
		t.Errorf("hack night = %+v", m[7])
	}
	if m[6].Event == nil || m[6].Event.Summary != "Programming LEDs" || m[6].State != StateOnLuma {
		t.Errorf("programming leds = %+v", m[6])
	}
}

func TestCompareRenamedAloneOnItsDay(t *testing.T) {
	rs, _ := Compare([]Workshop{ws(1, "Open Build Night", pt(11, 9, 19, 0), pt(11, 9, 22, 0))}, loadFixture(t), time.Time{})
	if rs[0].Event == nil || rs[0].Event.Summary != "Hack Night" || rs[0].State != StateOnLuma {
		t.Fatalf("result = %+v", rs[0])
	}
}

func TestCompareLeftovers(t *testing.T) {
	_, left := Compare(production()[4:5], loadFixture(t), time.Date(2026, 11, 7, 22, 0, 0, 0, time.UTC))
	if len(left) != 2 || left[0].Summary != "Programming LEDs" || left[1].Summary != "Hack Night" {
		t.Fatalf("leftovers = %+v", left)
	}
}

func TestCompareLocation(t *testing.T) {
	start := time.Date(2031, 1, 1, 20, 0, 0, 0, time.UTC)
	cases := []struct {
		name, site, luma string
		diff             bool
	}{
		{"formatting only", siteAddr, "Sequoia Fabrica Community Workshop, 1736 18th St., San Francisco, CA 94107, USA", false},
		{"other number", siteAddr, "1740 18th St, San Francisco, CA 94107", true},
		{"no site address", "", "1740 18th St, San Francisco, CA 94107", false},
		{"no Luma street", siteAddr, "Sequoia Fabrica, San Francisco", false},
		{"production, no comma after St", prodAddrStNoComma, lumaFixtureAddr, false},
		{"production, ZIP+4", prodAddrZip4, lumaFixtureAddr, false},
		{"production form, other number", "1740 18th St San Francisco, CA 94107", lumaFixtureAddr, true},
	}
	for _, c := range cases {
		rs, _ := Compare([]Workshop{{ID: 1, Title: "Hack Night", Start: start, Address: c.site}},
			[]Event{{UID: "u", Summary: "Hack Night", Start: start, Location: c.luma}}, time.Time{})
		got := rs[0].Diffs
		if !c.diff {
			if len(got) != 0 {
				t.Errorf("%s: diffs = %+v", c.name, got)
			}
			continue
		}
		want := []Diff{{"location", "Location", c.site, c.luma}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: diffs = %+v, want %+v", c.name, got, want)
		}
	}
}

// lumaFixtureAddr is the location text the captured Luma feed carries.
const lumaFixtureAddr = "Sequoia Fabrica Community Workshop, 1736 18th St, San Francisco, CA 94107, USA"

func TestStreetPart(t *testing.T) {
	for in, want := range map[string]string{
		prodAddrNoComma:       "1736 18th st",
		prodAddrStNoComma:     "1736 18th st",
		prodAddrZip4:          "1736 18th st",
		lumaFixtureAddr:       "1736 18th st",
		"100 St Francis Blvd": "100 st francis blvd",
	} {
		if got := streetPart(in); got != want {
			t.Errorf("streetPart(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompareEndOnlyWhenBothHaveOne(t *testing.T) {
	start := time.Date(2031, 1, 1, 20, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	other := start.Add(3 * time.Hour)
	rs, _ := Compare([]Workshop{{ID: 1, Title: "Hack Night", Start: start}},
		[]Event{{UID: "u", Summary: "Hack Night", Start: start, End: &end}}, time.Time{})
	if len(rs[0].Diffs) != 0 {
		t.Errorf("site without end: %+v", rs[0].Diffs)
	}
	rs, _ = Compare([]Workshop{{ID: 1, Title: "Hack Night", Start: start, End: &other}},
		[]Event{{UID: "u", Summary: "Hack Night", Start: start}}, time.Time{})
	if len(rs[0].Diffs) != 0 {
		t.Errorf("Luma without end: %+v", rs[0].Diffs)
	}
	rs, _ = Compare([]Workshop{{ID: 1, Title: "Hack Night", Start: start, End: &other}},
		[]Event{{UID: "u", Summary: "Hack Night", Start: start, End: &end}}, time.Time{})
	if len(rs[0].Diffs) != 1 || rs[0].Diffs[0].Field != "end" {
		t.Errorf("both ends: %+v", rs[0].Diffs)
	}
}

func TestTitleScore(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"Intro to Soldering", "Introduction to Soldering", 1},
		{"Learn to Program LEDs", "Programming LEDs", 1},
		{"Programming LEDs", "Hack Night", 0},
		{"Hack Night", "Hack Night", 1},
		{"The", "Hack Night", 0},
		{"Hack Night", "Hack Day", 0.5},
	}
	for _, c := range cases {
		if got := titleScore(c.a, c.b); got != c.want {
			t.Errorf("titleScore(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
