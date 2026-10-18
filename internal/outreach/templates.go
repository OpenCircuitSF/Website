package outreach

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
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
	// Summary is the workshop's trimmed summary; Tagline is the outreach
	// flyer's tagline, used when there is no summary.
	Summary string
	Tagline string
	// Hashtags is the merged hashtag line appended to drafts. It is not a
	// fingerprint input: a settings change must not mark every draft stale.
	Hashtags string
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

// SiteURL is the public site, named in the description blocks drafted for the
// video channels.
const SiteURL = "https://www.opencircuitsf.com"

// sampleLinkURL stands in for the short link while a draft is measured: the
// real one is only known at Prepare, and Mastodon counts any URL as 23.
const sampleLinkURL = "https://go.opencircuitsf.com/aBcDeF"

// manualCaptionLimit bounds the summary kept in a caption for channels with
// no enforced limit (Instagram's own cap is 2,200).
const manualCaptionLimit = 2200

// Draft generates the post text (#0570): a sentence that carries the date and
// venue (never the clock time, #0579), the workshop summary cut at a sentence boundary to fit the channel,
// the channel's call to action, then the hashtag line. Bluesky and Instagram
// drafts carry no link (the Bluesky card holds it; Instagram links the bio);
// the rest carry LinkToken, which Prepare replaces with the real short URL.
// Only workshop fields are used, so a draft never claims anything the
// workshop does not say.
func Draft(ch Channel, w Wave, f Facts) string {
	lead, cta, keepSummary := draftParts(ch, w, f)
	summary := f.paragraph()
	if !keepSummary {
		summary = ""
	}
	assemble := func(sum string) string {
		var parts []string
		parts = append(parts, lead)
		if sum != "" {
			parts = append(parts, sum)
		}
		if cta != "" {
			parts = append(parts, cta)
		}
		if tags := strings.TrimSpace(f.Hashtags); tags != "" {
			parts = append(parts, tags)
		}
		return strings.Join(parts, "\n\n")
	}
	if summary == "" {
		return assemble("")
	}
	limit := ch.CharLimit
	if limit <= 0 {
		limit = manualCaptionLimit
	}
	fits := func(sum string) bool {
		body := strings.ReplaceAll(assemble(sum), LinkToken, sampleLinkURL)
		if ch.Name == "bluesky" && len(body) > bluesky.MaxTextBytes {
			return false
		}
		return Length(ch, body) <= limit
	}
	sentences := splitSentences(summary)
	if ch.Name == "tiktok" || ch.Name == "youtube" {
		sentences = sentences[:1]
	}
	for n := len(sentences); n >= 1; n-- {
		if cand := strings.Join(sentences[:n], " "); fits(cand) {
			return assemble(cand)
		}
	}
	// Even the first sentence is too long: cut it at a word.
	words := strings.Fields(sentences[0])
	for n := len(words) - 1; n >= 3; n-- {
		cand := strings.TrimRight(strings.Join(words[:n], " "), ",;:-") + "\u2026"
		if fits(cand) {
			return assemble(cand)
		}
	}
	return assemble("")
}

// draftParts is the channel and wave specific text: the lead sentence, the
// call to action, and whether the summary belongs in the post.
func draftParts(ch Channel, w Wave, f Facts) (lead, cta string, keepSummary bool) {
	title := strings.TrimSpace(f.Title)
	venue := ""
	if f.LocationName != nil {
		venue = strings.TrimSpace(*f.LocationName)
	}
	at, on := "", ""
	if venue != "" {
		at = " at " + venue
	}
	if f.StartsAt != nil {
		t := f.StartsAt.In(pacific)
		on = t.Format("Monday, Jan 2")
	}
	// sentence joins a clause and, when known, "on <date and time>".
	sentence := func(head string) string {
		s := head + at
		if on != "" {
			s += " on " + on
		}
		return s + "."
	}
	switch ch.Name {
	case "bluesky":
		keepSummary = w.Name != "last-call"
		switch w.Name {
		case "reminder":
			lead = sentence("One week out: " + title + " is happening")
			cta = "A few seats may still be open."
		case "last-call":
			if f.StartsAt != nil {
				lead = "Tomorrow: " + title + at + ". Last call to sign up."
			} else {
				lead = sentence("Last call: " + title + " is happening")
			}
		default:
			lead = sentence("We're running " + title)
		}
	case "mastodon":
		keepSummary = w.Name != "last-call"
		switch w.Name {
		case "reminder":
			lead = sentence("One week to go until " + title)
			cta = "Sign up here: " + LinkToken
		case "last-call":
			lead = "Last call for " + title + at
			if f.StartsAt != nil {
				lead += ", tomorrow"
			}
			lead += "."
			cta = "Sign up here: " + LinkToken
		default:
			lead = sentence("Join us for " + title)
			cta = "Spots are limited, so you can sign up here: " + LinkToken
		}
	case "instagram":
		keepSummary = true
		lead = titleWithDetails(title, on, venue)
		cta = "Sign up at the link in our bio."
	default: // tiktok, youtube: a description block to paste under a video
		keepSummary = true
		lead = titleWithDetails(title, on, venue)
		cta = "Sign up: " + LinkToken + "\n\nOpen Circuit SF: " + SiteURL
	}
	return lead, cta, keepSummary
}

// titleWithDetails is "Title: Monday, Oct 19 at Venue.", leaving
// out whichever of the date and venue is unknown.
func titleWithDetails(title, on, venue string) string {
	details := on
	switch {
	case venue != "" && on != "":
		details += " at " + venue
	case venue != "":
		details = venue
	}
	if details == "" {
		return title + "."
	}
	return title + ": " + details + "."
}

// paragraph is the workshop's own description: its summary, or the flyer
// tagline when there is no summary, on one line.
func (f Facts) paragraph() string {
	for _, s := range []string{f.Summary, f.Tagline} {
		if t := strings.Join(strings.Fields(s), " "); t != "" {
			return t
		}
	}
	return ""
}

// splitSentences cuts s after each sentence-ending mark followed by a space,
// except after an abbreviation ("e.g.", "Dr."), so a summary is never cut to
// a dangling fragment.
func splitSentences(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	for i, r := range rs {
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(rs) || rs[i+1] == ' ') && !(r == '.' && i+1 < len(rs) && abbreviationAt(rs, i)) {
			if seg := strings.TrimSpace(string(rs[start : i+1])); seg != "" {
				out = append(out, seg)
			}
			start = i + 1
		}
	}
	if tail := strings.TrimSpace(string(rs[start:])); tail != "" {
		out = append(out, tail)
	}
	if len(out) == 0 {
		out = []string{s}
	}
	return out
}

// abbreviationAt reports whether the "." at rs[i] closes an abbreviation: a
// word with an inner dot ("e.g.", "U.S.") or a common title.
func abbreviationAt(rs []rune, i int) bool {
	j := i
	for j > 0 && rs[j-1] != ' ' {
		j--
	}
	word := strings.ToLower(string(rs[j:i]))
	if strings.Contains(word, ".") {
		return true
	}
	switch strings.Trim(word, "(\"'") {
	case "mr", "mrs", "ms", "dr", "st", "vs", "jr", "sr", "approx":
		return true
	}
	return false
}

// Fingerprint hashes the facts a draft is generated from, so a later change
// to title, date, place, summary or tagline marks the draft stale (#0570: the
// templates read the summary). Body edits are not inputs.
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
	sum := sha256.Sum256([]byte(strings.Join([]string{f.Title, start, deref(f.LocationName), deref(f.LocationAddress), f.paragraphInputs()}, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// paragraphInputs is the text the draft body reads, normalised so
// whitespace-only edits do not mark drafts stale.
func (f Facts) paragraphInputs() string {
	return strings.Join(strings.Fields(f.Summary), " ") + "\x1e" + strings.Join(strings.Fields(f.Tagline), " ")
}
