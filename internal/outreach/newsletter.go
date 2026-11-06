package outreach

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// Newsletter link UTM values (#0643). utm_campaign is the event's slug, as
// Outreach's own links use; utm_content is the newsletter's slug, so
// ShortLinks shows which issue drove a click.
const (
	NewsletterUTMSource = "newsletter"
	NewsletterUTMMedium = "email"
	NewsletterPlacement = "newsletter"
)

// ErrInvalidMonth is returned by MonthWindow for a value that is not YYYY-MM.
var ErrInvalidMonth = errors.New("outreach: month must be YYYY-MM")

// newsletterZone is the zone the month window and the event times are read
// in: the events are in San Francisco.
var newsletterZone = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// NewsletterDestinationURL is the event page with the newsletter UTM values,
// used both as a ShortLink's destination and as the plain fallback link.
func NewsletterDestinationURL(siteBaseURL, workshopSlug, newsletterSlug string) string {
	q := url.Values{}
	q.Set("utm_source", NewsletterUTMSource)
	q.Set("utm_medium", NewsletterUTMMedium)
	q.Set("utm_campaign", workshopSlug)
	q.Set("utm_content", newsletterSlug)
	return strings.TrimRight(siteBaseURL, "/") + "/events/" + workshopSlug + "?" + q.Encode()
}

// MonthWindow parses month (YYYY-MM) and returns the half-open window of
// events still to come in it: from the later of now and the first of the
// month, to the first of the next month, both midnight in Los Angeles. label
// is the month's display name ("January 2027" style).
func MonthWindow(month string, now time.Time) (from, to time.Time, label string, err error) {
	if len(month) != 7 {
		return time.Time{}, time.Time{}, "", ErrInvalidMonth
	}
	start, perr := time.ParseInLocation("2006-01", month, newsletterZone)
	if perr != nil {
		return time.Time{}, time.Time{}, "", ErrInvalidMonth
	}
	to = start.AddDate(0, 1, 0)
	from = start
	if now.After(from) {
		from = now
	}
	return from, to, start.Format("January 2006"), nil
}

// NewsletterItem is one event in the newsletter's upcoming-events block.
type NewsletterItem struct {
	Title    string
	URL      string
	Venue    string
	StartsAt time.Time
}

var markdownTitleEscaper = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`, `*`, `\*`, `_`, `\_`)

// NewsletterEventsMarkdown renders the block the campaign editor appends to a
// newsletter's body: a heading, then one bullet per event with its linked
// title, Los Angeles date and time, and venue. No items renders "".
func NewsletterEventsMarkdown(label string, items []NewsletterItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Upcoming events in " + label + "\n\n")
	for _, it := range items {
		b.WriteString("- **[" + markdownTitleEscaper.Replace(it.Title) + "](" + it.URL + ")**: ")
		b.WriteString(it.StartsAt.In(newsletterZone).Format("Monday, Jan 2 at 3:04 PM"))
		if v := strings.TrimSpace(it.Venue); v != "" {
			b.WriteString(" · " + v)
		}
		b.WriteString("\n")
	}
	return b.String()
}
