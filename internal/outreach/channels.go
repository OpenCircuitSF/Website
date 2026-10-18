// Package outreach is the data layer and pure logic behind a workshop's
// Outreach panel (#0544): the fixed channel and wave lists, the draft-text
// and tracked-URL builders, and the store over the outreach and
// outreach_posts tables (migrations 000031 and 000032).
//
// The channel and wave lists live only here and are shipped to the SPA in
// the admin GET response, so there is no second copy to keep in step.
package outreach

import "time"

// LinkToken is the placeholder a draft carries until Prepare substitutes the
// real short URL for it.
const LinkToken = "{link}"

// Channel is one posting destination.
type Channel struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	UTMSource string `json:"utm_source"`
	UTMMedium string `json:"utm_medium"`
	// CharLimit is the post length cap; 0 means none is enforced.
	CharLimit int `json:"char_limit"`
	// Automatic channels are posted by the site (#0545); the rest are copied
	// and posted by hand.
	Automatic bool `json:"automatic"`
}

// Wave is one point in the promotion timeline.
type Wave struct {
	Name  string
	Label string
	// DaysBefore is how long before the workshop starts the wave is
	// suggested; 0 for announce, which has no date.
	DaysBefore int
}

var channels = []Channel{
	{Name: "bluesky", Label: "Bluesky", UTMSource: "bluesky", UTMMedium: "social", CharLimit: 300, Automatic: true},
	{Name: "mastodon", Label: "Mastodon", UTMSource: "mastodon", UTMMedium: "social", CharLimit: 500, Automatic: true},
	{Name: "instagram", Label: "Instagram", UTMSource: "instagram", UTMMedium: "social"},
	{Name: "tiktok", Label: "TikTok", UTMSource: "tiktok", UTMMedium: "video"},
	{Name: "youtube", Label: "YouTube", UTMSource: "youtube", UTMMedium: "video"},
}

var waves = []Wave{
	{Name: "announce", Label: "Announce"},
	{Name: "reminder", Label: "Reminder", DaysBefore: 7},
	{Name: "last-call", Label: "Last call", DaysBefore: 1},
}

// Channels returns a copy of the channel list in display order.
func Channels() []Channel { return append([]Channel(nil), channels...) }

// Waves returns a copy of the wave list in display order.
func Waves() []Wave { return append([]Wave(nil), waves...) }

// ChannelByName looks a channel up by its Name.
func ChannelByName(name string) (Channel, bool) {
	for _, c := range channels {
		if c.Name == name {
			return c, true
		}
	}
	return Channel{}, false
}

// WaveByName looks a wave up by its Name.
func WaveByName(name string) (Wave, bool) {
	for _, w := range waves {
		if w.Name == name {
			return w, true
		}
	}
	return Wave{}, false
}

// DefaultPostHour is the Pacific hour suggested times use when none is set.
const DefaultPostHour = 10

// SuggestedAt is when a wave is suggested for a workshop starting at
// startsAt: nil for a wave with no offset (announce) or an undated workshop.
// It is hour:00 Pacific on the calendar day DaysBefore days before the
// workshop's Pacific date, built with time.Date in the Pacific zone, so a
// daylight-saving change in between never shifts it off that wall-clock hour
// (#0574). An hour outside 0 to 23 falls back to DefaultPostHour.
func SuggestedAt(w Wave, startsAt *time.Time, hour int) *time.Time {
	if w.DaysBefore == 0 || startsAt == nil {
		return nil
	}
	if hour < 0 || hour > 23 {
		hour = DefaultPostHour
	}
	d := startsAt.In(pacific).AddDate(0, 0, -w.DaysBefore)
	t := time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, pacific)
	return &t
}
