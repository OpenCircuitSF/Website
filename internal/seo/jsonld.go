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

// #0530: structured data for the newsletter archive and for the
// organisation itself. Two blocks, same builder discipline as eventJSONLD
// above (fixed structs, encoding/json.Marshal's own HTML escaping, "" when
// the data doesn't qualify), so database content can never break out of the
// <script> element.
//
// # Article, not BlogPosting
//
// An archive page is an article -- a headline, a publication date, a
// publisher -- and Article is the schema.org type that states exactly that
// much. BlogPosting is its subtype for blog posts; a newsletter issue is
// not a blog post, and the more general type makes no claim the data
// can't back.
//
// # Which archive entries get a block at all
//
// Only published entries with an archived_at. archiveRouteMeta already
// gates on Published before this is ever called, but buildArticle re-checks
// it (defence in depth, matching sitemap.go's Build) and, like buildEvent's
// missing-startDate gate, treats a missing ArchivedAt as "not enough data":
// datePublished is required for Article rich results and is never
// fabricated.

// articleLD is the top-level schema.org Article object for one published
// archive entry. Publisher reuses eventOrganizer's {Organization, name,
// url} shape -- the same fixed Open Circuit SF identity #0055's Event
// carries as organizer, so both blocks always agree on who published.
type articleLD struct {
	Context       string         `json:"@context"`
	Type          string         `json:"@type"`
	Headline      string         `json:"headline"`
	DatePublished string         `json:"datePublished"`
	Publisher     eventOrganizer `json:"publisher"`
	Image         string         `json:"image"`
	Description   string         `json:"description,omitempty"`
	URL           string         `json:"url"`
}

// buildArticle decides whether e carries enough data for a spec-valid
// Article and, if so, builds it. The image is #0532's generated card -- the
// same URL archiveRouteMeta sets og:image to -- and the description is
// #0528's text, so a page's structured data and its social card always
// agree.
func buildArticle(e ArchiveEntry, baseURL string) (articleLD, bool) {
	if !e.Published || e.ArchivedAt == "" {
		return articleLD{}, false
	}
	return articleLD{
		Context:       "https://schema.org",
		Type:          "Article",
		Headline:      e.Subject,
		DatePublished: e.ArchivedAt,
		Publisher:     eventOrganizer{Type: "Organization", Name: "Open Circuit SF", URL: baseURL + "/"},
		Image:         baseURL + "/archive/" + e.Slug + "/og.png",
		Description:   archiveDescription(e),
		URL:           baseURL + "/archive/" + e.Slug,
	}, true
}

// articleJSONLD is eventJSONLD's archive twin: the complete JSON-LD script
// block for e, or "" when e doesn't qualify (buildArticle's gates).
func articleJSONLD(e ArchiveEntry, baseURL string) string {
	a, ok := buildArticle(e, baseURL)
	if !ok {
		return ""
	}
	body, err := json.Marshal(a)
	if err != nil {
		// articleLD is a fixed struct of strings -- see eventJSONLD's note.
		return ""
	}
	return `<script type="application/ld+json">` + string(body) + `</script>`
}

// # The organisation and the WebSite (#0530 criterion 2)
//
// The home page states, in machine-readable form, that opencircuitsf.com is
// Open Circuit SF, what its logo is, and which profiles belong to it -- the
// inputs a knowledge panel draws from. One <script> block with an @graph
// holding both nodes, because the two describe one site and sharing the
// block keeps them from drifting.
//
// The profiles are the organisation's real public ones, mirrored from the
// only place the site itself links them: web/src/lib/links.ts (Discord,
// Luma calendar) plus the Bluesky profile whose handle IS the domain --
// /.well-known/atproto-did (kept on disk ahead of the proxy, per AGENTS.md
// §7) binds did:plc:olbggqhj2rwqv56ik53kvwfx to opencircuitsf.com, which
// makes https://bsky.app/profile/opencircuitsf.com the profile's canonical
// URL. The logo is /mark-green-512.png -- the opaque brand mark the
// webmanifest itself declares, not the og-default.png social card.

// organizationSameAs is the site's sameAs list, in a fixed order for
// deterministic output.
var organizationSameAs = []string{
	"https://bsky.app/profile/opencircuitsf.com",
	"https://discord.gg/Fq9ug6QXV3",
	"https://luma.com/opencircuitsf",
}

// organizationNode is the schema.org Organization for Open Circuit SF.
type organizationNode struct {
	Type   string   `json:"@type"`
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Logo   string   `json:"logo"`
	SameAs []string `json:"sameAs"`
}

// websiteNode is the schema.org WebSite node for the site itself.
type websiteNode struct {
	Type string `json:"@type"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// graphLD wraps the two home-page nodes in one @graph under a single
// @context. Graph holds *organizationNode/*websiteNode values directly --
// each carries its own @type, so one Marshal call emits the heterogeneous
// nodes.
type graphLD struct {
	Context string `json:"@context"`
	Graph   []any  `json:"@graph"`
}

// homeJSONLD returns the complete JSON-LD block for the home page: an
// Organization and a WebSite node in one @graph. The inputs are all fixed
// values (brand constants, baseURL, static asset paths), so this cannot
// fail for data reasons -- only Marshal, which cannot fail on a fixed
// struct of strings, could, and that "" mirrors eventJSONLD's defense in
// depth.
func homeJSONLD(baseURL string) string {
	g := graphLD{
		Context: "https://schema.org",
		Graph: []any{
			&organizationNode{
				Type:   "Organization",
				Name:   "Open Circuit SF",
				URL:    baseURL + "/",
				Logo:   baseURL + "/mark-green-512.png",
				SameAs: organizationSameAs,
			},
			&websiteNode{
				Type: "WebSite",
				Name: "Open Circuit SF",
				URL:  baseURL + "/",
			},
		},
	}
	body, err := json.Marshal(g)
	if err != nil {
		return ""
	}
	return `<script type="application/ld+json">` + string(body) + `</script>`
}
