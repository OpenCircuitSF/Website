package outreach

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata" // America/Los_Angeles must resolve on a box without zoneinfo
)

// Facts is the slice of a workshop that drafts and fingerprints are built
// from.
type Facts struct {
	Slug            string
	Title           string
	StartsAt        *time.Time
	LocationName    *string
	LocationAddress *string
}

var pacific = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// DestinationURL is the workshop's public URL with the UTM query baked in.
// Keys are sorted (url.Values.Encode), so the result is deterministic, which
// ShortLinks' de-duplication on destination depends on. An empty siteBaseURL
// yields a relative URL.
func DestinationURL(siteBaseURL string, f Facts, ch Channel, w Wave) string {
	q := url.Values{}
	q.Set("utm_source", ch.UTMSource)
	q.Set("utm_medium", ch.UTMMedium)
	q.Set("utm_campaign", f.Slug)
	q.Set("utm_content", w.Name)
	return strings.TrimRight(siteBaseURL, "/") + "/workshops/" + f.Slug + "?" + q.Encode()
}

// Draft generates the post text. The body carries LinkToken, which Prepare
// replaces with the real short URL.
func Draft(ch Channel, w Wave, f Facts) string {
	var lead string
	switch w.Name {
	case "reminder":
		lead = "One week to go: " + f.Title
	case "last-call":
		lead = "Tomorrow: " + f.Title
	default:
		lead = "New workshop: " + f.Title
	}
	date := "Date TBA"
	if f.StartsAt != nil {
		date = f.StartsAt.In(pacific).Format("Mon, Jan 2 · 3:04 PM")
	}
	place := "Location TBA"
	if f.LocationName != nil && strings.TrimSpace(*f.LocationName) != "" {
		place = *f.LocationName
	}
	cta := "Details and signup: " + LinkToken
	if ch.Name == "instagram" {
		cta = "Link in bio — " + LinkToken
	}
	return lead + "\n" + date + " · " + place + "\n\n" + cta
}

// Fingerprint hashes the facts a draft is generated from, so a later change
// to title, date or place marks the draft stale. Summary and body edits are
// not inputs.
func Fingerprint(f Facts) string {
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	start := ""
	if f.StartsAt != nil {
		start = f.StartsAt.UTC().Format(time.RFC3339)
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{f.Title, start, deref(f.LocationName), deref(f.LocationAddress)}, "\x1f")))
	return hex.EncodeToString(sum[:])
}
