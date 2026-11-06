package outreach

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func la(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestNewsletterDestinationURL(t *testing.T) {
	got := NewsletterDestinationURL("https://site.example/", "intro-soldering", "issue-slug")
	want := "https://site.example/events/intro-soldering?utm_campaign=intro-soldering&utm_content=issue-slug&utm_medium=email&utm_source=newsletter"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"utm_source", "utm_medium", "utm_campaign", "utm_content"} {
		if u.Query().Get(k) == "" {
			t.Errorf("missing %s", k)
		}
	}
}

func TestMonthWindow_CurrentMonthStartsNow(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, la(t))
	from, to, label, err := MonthWindow("2026-11", now)
	if err != nil {
		t.Fatal(err)
	}
	if !from.Equal(now) {
		t.Errorf("from = %v, want now", from)
	}
	if !to.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, la(t))) {
		t.Errorf("to = %v", to)
	}
	if label != "November 2026" {
		t.Errorf("label = %q", label)
	}
}

func TestMonthWindow_FutureMonth(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, la(t))
	from, to, label, err := MonthWindow("2027-01", now)
	if err != nil {
		t.Fatal(err)
	}
	if !from.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, la(t))) || !to.Equal(time.Date(2027, 2, 1, 0, 0, 0, 0, la(t))) {
		t.Errorf("window = %v .. %v", from, to)
	}
	if label != "January 2027" {
		t.Errorf("label = %q", label)
	}
}

func TestMonthWindow_LosAngelesBoundary(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	from, to, _, err := MonthWindow("2026-11", now)
	if err != nil {
		t.Fatal(err)
	}
	in := func(x time.Time) bool { return !x.Before(from) && x.Before(to) }
	late := time.Date(2026, 11, 30, 23, 30, 0, 0, la(t))
	early := time.Date(2026, 12, 1, 0, 30, 0, 0, la(t))
	if !in(late) {
		t.Errorf("23:30 PST on Nov 30 should be inside")
	}
	if in(early) {
		t.Errorf("00:30 PST on Dec 1 should be outside")
	}
}

func TestMonthWindow_Malformed(t *testing.T) {
	for _, m := range []string{"", "2026-13", fmt.Sprintf("%02d-%04d", 11, 2026), "2026-1", "2026/11", "abcd-ef"} {
		if _, _, _, err := MonthWindow(m, time.Now()); !errors.Is(err, ErrInvalidMonth) {
			t.Errorf("MonthWindow(%q) err = %v", m, err)
		}
	}
}

func TestNewsletterEventsMarkdown_Format(t *testing.T) {
	items := []NewsletterItem{
		{Title: "Intro to Soldering", URL: "https://go.example/u/abc", Venue: "Sequoia Fabrica", StartsAt: time.Date(2026, 11, 15, 21, 0, 0, 0, time.UTC)},
		{Title: "Second", URL: "https://x.example/e", Venue: "Hall", StartsAt: time.Date(2026, 11, 20, 18, 30, 0, 0, la(t))},
	}
	got := NewsletterEventsMarkdown("November 2026", items)
	want := "## Upcoming events in November 2026\n\n" +
		"- **[Intro to Soldering](https://go.example/u/abc)**: Sunday, Nov 15 at 1:00 PM · Sequoia Fabrica\n" +
		"- **[Second](https://x.example/e)**: Friday, Nov 20 at 6:30 PM · Hall\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNewsletterEventsMarkdown_EscapesTitle(t *testing.T) {
	got := NewsletterEventsMarkdown("M", []NewsletterItem{{Title: `a[b]*c_d\e`, URL: "u", StartsAt: time.Now()}})
	if !strings.Contains(got, `[a\[b\]\*c\_d\\e](u)`) {
		t.Fatalf("title not escaped: %q", got)
	}
}

func TestNewsletterEventsMarkdown_OmitsEmptyVenue(t *testing.T) {
	got := NewsletterEventsMarkdown("M", []NewsletterItem{{Title: "T", URL: "u", Venue: " ", StartsAt: time.Now()}})
	if strings.Contains(got, "·") || !strings.HasSuffix(got, "PM\n") && !strings.HasSuffix(got, "AM\n") {
		t.Fatalf("got %q", got)
	}
}

func TestNewsletterEventsMarkdown_EmptyIsEmpty(t *testing.T) {
	if got := NewsletterEventsMarkdown("M", nil); got != "" {
		t.Fatalf("got %q", got)
	}
}
