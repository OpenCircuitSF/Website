package seo

import "encoding/json"

// This file builds the schema.org Event JSON-LD block for a workshop detail
// page (#0055, PRD §7.4) -- structured data that makes an individual
// workshop eligible for a Google Rich Result, distinct from the %%OC_OG_*%%
// social-card tags seo.go already injects.
//
// # Which workshops get a block at all
//
// Only WorkshopPublished and WorkshopCanceled workshops (matching
// internal/handlers/public_workshops.go's own "published OR canceled" public
// visibility rule) that ALSO have both a start date and a location are
// eligible. Draft workshops, an unknown slug, and a published/canceled
// workshop still missing a date or venue all render "" -- no <script> tag at
// all -- rather than a block with a fabricated or empty required field.
// Google's Event guidance treats name, startDate, and location (for a
// non-virtual event) as required; emitting a block that's missing one of
// those would fail Rich Results validation, which is worse than omitting
// structured data entirely for a workshop whose details aren't finalized
// yet.
//
// # Canceled workshops: JSON-LD diverges from the social-card fallback
//
// seo.go's workshopRouteMeta still serves a CANCELED workshop's <title>/
// og:* tags from the generic site fallback, not the workshop's own -- #0135's
// review deliberately left internal/seo's social-card/sitemap behavior
// untouched so a canceled workshop stays out of link-preview cards and the
// sitemap. JSON-LD does the opposite on purpose: schema.org's
// eventStatus/EventCancelled value exists specifically so a search result
// can say "this was scheduled and got canceled" instead of either pretending
// the event never existed or silently vanishing it. Hiding a canceled
// workshop's JSON-LD behind the generic fallback here would defeat the one
// field schema.org designed for exactly this case, and the workshop detail
// page itself (#0054) already renders a real, visible cancellation notice
// for anyone holding the link -- the structured data should tell the same
// story a crawler that never runs JavaScript would otherwise miss.
//
// # No online/virtual event concept exists in this schema
//
// eventAttendanceMode is always "https://schema.org/OfflineEventAttendanceMode"
// and location is always a schema.org Place: Open Circuit SF is an in-person
// workshop space (PRD's own description), and internal/workshops.Workshop has
// no is-virtual flag or online-meeting-link field for a workshop to declare
// itself remote. If that ever changes, this is the place to branch on it and
// emit OnlineEventAttendanceMode + a VirtualLocation (schema.org supports
// both a Place and a VirtualLocation on the same Event for a hybrid session)
// -- today there is nothing in the data model to branch on.

// eventStatus / eventAttendanceMode are schema.org enumeration values, given
// as their canonical https://schema.org/... URLs (Google's own examples use
// this form, not the bare token).
const (
	eventStatusScheduled   = "https://schema.org/EventScheduled"
	eventStatusCancelled   = "https://schema.org/EventCancelled"
	eventAttendanceOffline = "https://schema.org/OfflineEventAttendanceMode"
)

// eventPlace is a schema.org Place, used for Event.location. Address is a
// plain string (schema.org's address property accepts Text, not only a
// structured PostalAddress) since internal/workshops.Workshop stores the
// venue address as a single free-text field, not separate
// street/city/region/postal components.
type eventPlace struct {
	Type    string `json:"@type"`
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
}

// eventOrganizer is a schema.org Organization, used for Event.organizer.
// Always Open Circuit SF itself -- #0055's acceptance criterion "Organizer is
// Open Circuit SF with the site URL — no venue is named as organizer" is
// exactly why this is a fixed value built from baseURL, never derived from
// the workshop's own location fields.
type eventOrganizer struct {
	Type string `json:"@type"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// eventLD is the top-level schema.org Event object, marshaled directly to
// the JSON-LD payload. Field order here is the field order Marshal emits,
// chosen to match #0055's acceptance-criteria list (name, startDate,
// endDate, eventStatus, eventAttendanceMode, location, description,
// organizer, image, url) purely for human readability of the emitted
// markup -- schema.org and JSON itself are both order-independent.
type eventLD struct {
	Context             string         `json:"@context"`
	Type                string         `json:"@type"`
	Name                string         `json:"name"`
	StartDate           string         `json:"startDate"`
	EndDate             string         `json:"endDate,omitempty"`
	EventStatus         string         `json:"eventStatus"`
	EventAttendanceMode string         `json:"eventAttendanceMode"`
	Location            eventPlace     `json:"location"`
	Description         string         `json:"description,omitempty"`
	Organizer           eventOrganizer `json:"organizer"`
	Image               string         `json:"image,omitempty"`
	URL                 string         `json:"url"`
}

// eventLocation builds Event.location from a workshop's location fields.
// LocationName and LocationAddress are independently optional in the source
// data; ok=false only when NEITHER is set, meaning there's no real venue to
// describe yet (see this file's package-level doc comment). When only one is
// set, the other is simply omitted rather than treated as missing data --
// e.g. an address with no separately-given venue name still produces a valid
// Place (address doubles as the display name).
func eventLocation(w Workshop) (eventPlace, bool) {
	if w.LocationName == "" && w.LocationAddress == "" {
		return eventPlace{}, false
	}
	name := w.LocationName
	if name == "" {
		name = w.LocationAddress
	}
	return eventPlace{Type: "Place", Name: name, Address: w.LocationAddress}, true
}

// buildEvent decides whether w carries enough data for a spec-valid
// schema.org Event and, if so, builds it. See this file's package-level doc
// comment for the full reasoning behind each gate.
func buildEvent(w Workshop, baseURL string) (eventLD, bool) {
	if w.Status != WorkshopPublished && w.Status != WorkshopCanceled {
		return eventLD{}, false
	}
	if w.StartsAt == "" {
		return eventLD{}, false // startDate is required; never fabricate one
	}
	loc, ok := eventLocation(w)
	if !ok {
		return eventLD{}, false // location is required for a non-virtual Event
	}

	status := eventStatusScheduled
	if w.Status == WorkshopCanceled {
		status = eventStatusCancelled
	}

	image := absoluteURL(baseURL, w.CoverImage)
	if image == "" {
		image = baseURL + "/og-default.png"
	}

	return eventLD{
		Context:             "https://schema.org",
		Type:                "Event",
		Name:                w.Title,
		StartDate:           w.StartsAt,
		EndDate:             w.EndsAt,
		EventStatus:         status,
		EventAttendanceMode: eventAttendanceOffline,
		Location:            loc,
		Description:         w.Summary,
		Organizer:           eventOrganizer{Type: "Organization", Name: "Open Circuit SF", URL: baseURL + "/"},
		Image:               image,
		URL:                 baseURL + "/workshops/" + w.Slug,
	}, true
}

// eventJSONLD returns the complete
// `<script type="application/ld+json">...</script>` block for w, or "" if w
// doesn't qualify (see buildEvent). json.Marshal HTML-escapes <, >, and & by
// default (it does not need html.EscapeString or a custom Encoder to do
// this), which is what makes embedding its output directly inside a
// <script> element safe against a workshop title/summary containing
// "</script>" or similar -- #0055's own acceptance criterion: "a title
// containing quotes does not break the block".
func eventJSONLD(w Workshop, baseURL string) string {
	ev, ok := buildEvent(w, baseURL)
	if !ok {
		return ""
	}
	body, err := json.Marshal(ev)
	if err != nil {
		// eventLD is a fixed struct of strings -- no channel, func, or cyclic
		// field that could ever make Marshal fail. Treated the same as "not
		// enough data" rather than panicking or serving a broken tag, purely
		// as defense in depth.
		return ""
	}
	return `<script type="application/ld+json">` + string(body) + `</script>`
}
