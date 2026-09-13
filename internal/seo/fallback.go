package seo

// #0519: server-rendered fallback content substituted into tokenBody, next
// to #0019's meta injection. See seo.go's tokenBody doc comment for what
// problem this solves (a crawler that never executes JavaScript sees zero
// <h1> tags and an empty body) and this file's own doc comments below for
// the design. web/src/lib/mountTarget.ts's prepareMountTarget is the other
// half: it clears #app before Svelte's mount() appends the real view, so
// this fallback and the SPA's own rendering never coexist in the DOM.
//
// Scope (issue #0519's plan): a first slice. /about and /privacy get their
// intro paragraph and a couple of sections, not their full page text; a
// withheld/draft/unknown/private/token route gets nothing (the empty
// string, same as today). Both are deliberate cuts, not oversights -- see
// the issue's own "Scope" section for the follow-ups filed separately.

import (
	"bytes"
	"html/template"
	"sort"
	"strings"
	"sync"
	"time"

	// Loads the IANA timezone database into the binary so
	// America/Los_Angeles resolves regardless of the host's own zoneinfo --
	// the production binary is cross-compiled with CGO off (CLAUDE.md §7),
	// so it cannot rely on the box's /usr/share/zoneinfo the way a locally
	// built binary can.
	_ "time/tzdata"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// pageKind is which shape of fallback content a route gets. Zero value
// (pageNone) is "no fallback content" -- every private, token-bearing,
// draft/withheld, or unknown route (RouteMeta.page's zero value, since
// defaultFallbackMeta/defaultNotFoundMeta never set it).
type pageKind int

const (
	pageNone pageKind = iota
	pageStatic
	pageWorkshopsIndex
	pageArchiveIndex
	pageWorkshop
	pageArchiveEntry
)

// pageContent is what renderPage needs to build one route's fallback body.
// Plain data, comparable -- deliberately not a func field, so a RouteMeta
// value (which embeds this) stays a plain, comparable struct too.
type pageContent struct {
	kind     pageKind
	path     string // set for pageStatic, keys staticPageCopy
	workshop Workshop
	archive  ArchiveEntry
}

// copyBlock is one paragraph or subheading of static page copy.
type copyBlock struct {
	Heading bool
	Text    string
}

// staticCopy is one static route's fallback content: the source .svelte
// file this was copied from (read by fallback_copy_guard_test.go's drift
// guard), the page's <h1>, and its body blocks.
type staticCopy struct {
	Source string
	H1     string
	Blocks []copyBlock
}

// Static-route copy (#0519's plan §3). "Verbatim" means the exact bytes,
// copied from each component with {APP_NAME} expanded to "Open Circuit SF"
// and inline <strong> tags dropped -- fallback_copy_guard_test.go's
// TestStaticFallbackCopyMatchesSvelteViews proves this table hasn't drifted
// from the .svelte sources it names, normalizing each source file the same
// way before comparing.
//
// /workshops and /archive are NOT pageStatic routes (they're
// pageWorkshopsIndex/pageArchiveIndex, built from live store data below),
// but their static headings/ledes/empty-state strings are entered here too
// so the same drift guard covers them -- renderWorkshopsIndexPage and
// renderArchiveIndexPage reference these same map entries directly rather
// than a second, driftable copy of the same strings.
var staticPageCopy = map[string]staticCopy{
	"/": {
		Source: "web/src/views/Home.svelte",
		H1:     "Hands-on electronics workshops",
		Blocks: []copyBlock{
			{Heading: true, Text: "What this is"},
			{Text: `Open Circuit SF is a San Francisco group running hands-on electronics workshops — soldering, microcontrollers, homelab, home automation, and whatever the room wants to build next.`},
			{Text: `We are deliberately venue-independent. A workshop might run in a makerspace, a co-working room, or somebody's garage. Bring curiosity; we bring the tools.`},
		},
	},
	"/about": {
		Source: "web/src/views/About.svelte",
		H1:     "About Open Circuit SF",
		Blocks: []copyBlock{
			{Text: `Open Circuit SF is a San Francisco group running hands-on electronics workshops — soldering, microcontrollers, homelab, home automation, and whatever the room wants to build next. No experience required, and no membership either — if a session sounds interesting, show up.`},
			{Heading: true, Text: "Venues"},
			{Text: `Open Circuit SF doesn't have a home base. A workshop might happen in a makerspace, a co-working room, or someone's garage — the location moves depending on what's available and who's hosting that month. Wherever it lands, tools and a soldering-safe table are the only requirements.`},
			{Heading: true, Text: "Get involved"},
			{Text: `The easiest way in is Discord — that's where locations get shared and people organize builds between sessions. Upcoming sessions are listed on the Luma calendar, where you can RSVP directly.`},
		},
	},
	"/privacy": {
		Source: "web/src/views/PrivacyPolicy.svelte",
		H1:     "Privacy Policy",
		Blocks: []copyBlock{
			{Text: `Open Circuit SF runs a mailing list so people can hear about upcoming workshops. This page explains what that list collects, why, how long it's kept, and how to leave it. It applies to the workshop mailing list and this website; it does not cover third-party services like Discord or Luma, which have their own privacy policies.`},
		},
	},
	"/subscribe": {
		Source: "web/src/views/Subscribe.svelte",
		H1:     "Subscribe",
		Blocks: []copyBlock{
			{Text: `Get notified about new workshops. Pick the topics you care about — nothing else.`},
		},
	},
	"/workshops": {
		Source: "web/src/views/WorkshopsIndex.svelte",
		H1:     workshopsH1,
		Blocks: []copyBlock{
			{Heading: true, Text: workshopsUpcomingH2},
			{Text: workshopsUpcomingEmpty},
			{Heading: true, Text: workshopsPastH2},
			{Text: workshopsPastEmpty},
		},
	},
	"/archive": {
		Source: "web/src/views/ArchiveIndex.svelte",
		H1:     archiveH1,
		Blocks: []copyBlock{
			{Text: archiveLede},
			{Text: archiveEmpty},
		},
	},
}

// Named constants (rather than literals inline above and again in the
// render functions below) so staticPageCopy's guarded table and the actual
// rendered output are provably the same bytes, not two hand-kept copies.
const (
	workshopsH1            = "Workshops"
	workshopsUpcomingH2    = "Upcoming"
	workshopsUpcomingEmpty = `Nothing scheduled yet — subscribe and we'll email you the moment a new workshop is announced.`
	workshopsPastH2        = "Past workshops"
	workshopsPastEmpty     = `No past workshops yet — this group is just getting started.`

	archiveH1    = "Archive"
	archiveLede  = `Past emails from Open Circuit SF.`
	archiveEmpty = `Nothing archived yet — subscribe and we'll email you the moment there's something to read.`
)

// navLink is one entry in the site nav rendered into every fallback page's
// <header>. fallbackNavCoversHeaderNav (fallback_copy_guard_test.go) proves
// this list is a subset of Header.svelte's own NAV_LINKS plus the handful
// of routes only Footer.svelte links (Home via the brand link, Archive,
// Privacy policy) -- so the fallback nav is never missing a link a real
// visitor can already reach.
type navLink struct {
	Href  string
	Label string
}

// fallbackNav is the nav rendered on every public fallback page (#0519's
// plan §2). Fixed order, independent of $currentRoute -- the fallback has
// no client-side router, so there is no "active route" to mark; every
// visible route is a plain link.
var fallbackNav = []navLink{
	{"/", "Home"},
	{"/workshops", "Workshops"},
	{"/about", "About"},
	{"/subscribe", "Subscribe"},
	{"/archive", "Archive"},
	{"/privacy", "Privacy policy"},
}

// workshopListItem is one row of the /workshops index's Upcoming or Past
// list.
type workshopListItem struct {
	Slug    string
	Title   string
	Meta    string // date label + location label, "Canceled · " prefixed when canceled
	Summary string
}

// archiveListItem is one row of the /archive index.
type archiveListItem struct {
	Slug      string
	Subject   string
	DateAttr  string // ArchiveEntry.UpdatedAt, verbatim, for <time datetime="...">
	DateLabel string // "Jan 2, 2006"
	Preheader string
}

// pageTemplateData is pageTemplate's single input shape. Kind selects which
// branch of the template renders; only the fields that kind actually uses
// are populated by renderPage's callers below -- the zero value of every
// other field (empty string/nil slice/false) is never referenced by a
// branch it doesn't belong to.
type pageTemplateData struct {
	Nav []navLink
	H1  string
	// Kind is a small string tag ("static", "workshopsIndex",
	// "archiveIndex", "workshop", "archiveEntry"), not the pageKind int
	// itself -- html/template's `eq` compares its arguments' dynamic types
	// too, and a plain string tag keeps the template text readable without
	// exporting pageKind's own int constants for template use.
	Kind string

	// pageStatic
	Blocks []copyBlock

	// pageWorkshopsIndex
	UpcomingHeading string
	UpcomingEmpty   string
	UpcomingItems   []workshopListItem
	PastHeading     string
	PastEmpty       string
	PastItems       []workshopListItem

	// pageArchiveIndex
	ArchiveLede  string
	ArchiveEmpty string
	ArchiveItems []archiveListItem

	// pageWorkshop
	Canceled      bool
	DateLabel     string
	StartsAt      string // raw RFC 3339, for <time datetime="...">
	LocationLines []string

	// pageArchiveEntry
	DateAttr string // raw date, for <time datetime="...">

	// pageWorkshop and pageArchiveEntry share this: the rendered body.
	// template.HTML is the ONLY escaping exemption in this whole template
	// (see renderMarkdownOrEmpty's doc comment) -- every other field above
	// is a plain string/[]string/[]T and goes through html/template's
	// ordinary contextual escaping (text content, or URL escaping inside
	// the href="{{.Slug}}"-shaped attributes below).
	BodyHTML template.HTML
}

// pageTemplate is parsed once, at package init, from the single source
// below (#0519's plan §4: "One html/template set, parsed once with
// template.Must at package level"). html/template's contextual autoescaping
// means every {{.Field}} substitution is escaped for the context it sits
// in -- text content is HTML-escaped, and a value inside an href="..."
// attribute is escaped as a URL -- which is what makes a slug safe to drop
// straight into an href without a separate escaping step here.
var pageTemplate = template.Must(template.New("nav").Parse(pageTemplateSource))

const pageTemplateSource = `
{{define "nav"}}<header class="app-shell"><nav aria-label="Site"><ul>{{range .Nav}}<li><a href="{{.Href}}">{{.Label}}</a></li>{{end}}</ul></nav></header>{{end}}
{{define "body"}}{{template "nav" .}}<main id="main-content" class="app-shell">
<h1>{{.H1}}</h1>
{{if eq .Kind "static"}}{{range .Blocks}}{{if .Heading}}<h2>{{.Text}}</h2>{{else}}<p>{{.Text}}</p>{{end}}{{end}}
{{else if eq .Kind "workshopsIndex"}}<h2>{{.UpcomingHeading}}</h2>{{if .UpcomingItems}}<ul>{{range .UpcomingItems}}<li><a href="/workshops/{{.Slug}}">{{.Title}}</a><br>{{.Meta}}{{if .Summary}}<p>{{.Summary}}</p>{{end}}</li>{{end}}</ul>{{else}}<p>{{.UpcomingEmpty}}</p>{{end}}
<h2>{{.PastHeading}}</h2>{{if .PastItems}}<ul>{{range .PastItems}}<li><a href="/workshops/{{.Slug}}">{{.Title}}</a><br>{{.Meta}}{{if .Summary}}<p>{{.Summary}}</p>{{end}}</li>{{end}}</ul>{{else}}<p>{{.PastEmpty}}</p>{{end}}
{{else if eq .Kind "archiveIndex"}}<p>{{.ArchiveLede}}</p>{{if .ArchiveItems}}<ul>{{range .ArchiveItems}}<li><a href="/archive/{{.Slug}}">{{.Subject}}</a> <time datetime="{{.DateAttr}}">{{.DateLabel}}</time>{{if .Preheader}}<p>{{.Preheader}}</p>{{end}}</li>{{end}}</ul>{{else}}<p>{{.ArchiveEmpty}}</p>{{end}}
{{else if eq .Kind "workshop"}}{{if .Canceled}}<p>This workshop has been canceled.</p>{{end}}<dl><dt>When</dt><dd><time datetime="{{.StartsAt}}">{{.DateLabel}}</time></dd><dt>Where</dt><dd>{{if .LocationLines}}{{range .LocationLines}}{{.}}<br>{{end}}{{else}}Location TBA{{end}}</dd></dl>{{if .BodyHTML}}<div class="workshop-body">{{.BodyHTML}}</div>{{end}}<p><a href="/workshops">See all workshops</a></p>
{{else if eq .Kind "archiveEntry"}}<p><time datetime="{{.DateAttr}}">{{.DateLabel}}</time></p>{{if .BodyHTML}}<div class="archive-body">{{.BodyHTML}}</div>{{end}}<p><a href="/archive">See the archive</a></p>
{{end}}</main>{{end}}`

// renderPage builds the HTML substituted into tokenBody for one resolved
// route (#0519's plan §4). pageNone -- every private, token-bearing,
// draft/withheld, or unknown route -- returns "", true: the served HTML
// keeps <div id="app"></div> byte-for-byte, exactly as it did before this
// issue.
//
// cacheable is false when a WorkshopSource/ArchiveSource error, or a
// mailing.RenderMarkdownHTML error, meant a list or body couldn't be
// built -- Render (seo.go) skips storing that degraded body so a transient
// store failure doesn't freeze a list-less page for the whole TTL. The
// <h1>, lede, and nav are still produced in that case (see the pageTemplate
// source above: the <h1> sits outside every per-kind branch).
func (r *Renderer) renderPage(p pageContent) (pageHTML string, cacheable bool) {
	switch p.kind {
	case pageNone:
		return "", true
	case pageStatic:
		copy := staticPageCopy[p.path]
		return r.execTemplate(pageTemplateData{
			Kind: "static", H1: copy.H1, Blocks: copy.Blocks,
		}), true
	case pageWorkshopsIndex:
		all, err := workshopsOrEmpty(r.workshop)
		// #0519's plan §4: workshop filtering/order MUST mirror
		// internal/workshops/store.go's ListVisible -- see
		// buildWorkshopsIndexItems' own doc comment for the exact mirror.
		upcoming, past := buildWorkshopsIndexItems(all, r.now())
		return r.execTemplate(pageTemplateData{
			Kind: "workshopsIndex", H1: workshopsH1,
			UpcomingHeading: workshopsUpcomingH2, UpcomingEmpty: workshopsUpcomingEmpty, UpcomingItems: upcoming,
			PastHeading: workshopsPastH2, PastEmpty: workshopsPastEmpty, PastItems: past,
		}), err == nil
	case pageArchiveIndex:
		all, err := archiveOrEmpty(r.archive)
		items := buildArchiveIndexItems(all)
		return r.execTemplate(pageTemplateData{
			Kind: "archiveIndex", H1: archiveH1,
			ArchiveLede: archiveLede, ArchiveEmpty: archiveEmpty, ArchiveItems: items,
		}), err == nil
	case pageWorkshop:
		w := p.workshop
		bodyHTML, cacheable := renderMarkdownOrEmpty(w.BodyMD)
		return r.execTemplate(pageTemplateData{
			Kind: "workshop", H1: w.Title,
			Canceled:      w.Status == WorkshopCanceled,
			DateLabel:     workshopDateLabel(w.StartsAt, w.EndsAt),
			StartsAt:      w.StartsAt,
			LocationLines: workshopLocationLines(w.LocationName, w.LocationAddress),
			BodyHTML:      bodyHTML,
		}), cacheable
	case pageArchiveEntry:
		e := p.archive
		bodyHTML, cacheable := renderMarkdownOrEmpty(e.BodyMD)
		return r.execTemplate(pageTemplateData{
			Kind: "archiveEntry", H1: e.Subject,
			DateAttr: e.UpdatedAt, DateLabel: archiveDateLabel(e.UpdatedAt),
			BodyHTML: bodyHTML,
		}), cacheable
	default:
		return "", true
	}
}

// execTemplate renders data against pageTemplate's "body" template, always
// with fallbackNav -- every page kind (including pageNone's callers, which
// never reach here) gets the identical nav. A template execution error is a
// programmer error (a template referencing a field pageTemplateData doesn't
// have), never something a request can trigger, so this renders "" rather
// than panicking a live request.
func (r *Renderer) execTemplate(data pageTemplateData) string {
	data.Nav = fallbackNav
	var buf bytes.Buffer
	if err := pageTemplate.ExecuteTemplate(&buf, "body", data); err != nil {
		return ""
	}
	return buf.String()
}

// renderMarkdownOrEmpty renders md through mailing.RenderMarkdownHTML --
// the SAME call PublicArchiveHandler.GetBySlug (#0042) and
// internal/handlers/admin_workshop_preview.go's renderWorkshopBodyHTML make
// -- for a workshop or archive detail page's body (acceptance criterion 2:
// "the two must not diverge"). An empty md renders to "" without calling
// the renderer, matching renderWorkshopBodyHTML's own "empty is not a
// crash" posture.
//
// The returned template.HTML is the ONLY template.HTML conversion anywhere
// in this file (#0519's plan §4). It is safe specifically because goldmark
// runs in safe mode here (no raw HTML, no <script>, no dangerous-scheme
// hrefs -- RenderMarkdownHTML's own doc comment) -- the exact same
// sanitized bytes the SPA already inserts unescaped via Svelte's {@html}.
// Running html/template's own escaping over it a second time would
// HTML-entity-encode the tags this fragment is made of, corrupting it
// rather than protecting anything.
func renderMarkdownOrEmpty(md string) (template.HTML, bool) {
	if md == "" {
		return "", true
	}
	rendered, err := mailing.RenderMarkdownHTML(md)
	if err != nil {
		return "", false
	}
	return template.HTML(rendered), true
}

// workshopsOrEmpty calls source.Workshops(), or returns an empty list with
// no error when source is nil (STORAGE=json, or before any workshop store
// is wired -- WorkshopSource's own doc comment).
func workshopsOrEmpty(source WorkshopSource) ([]Workshop, error) {
	if source == nil {
		return nil, nil
	}
	return source.Workshops()
}

// archiveOrEmpty mirrors workshopsOrEmpty for ArchiveSource.
func archiveOrEmpty(source ArchiveSource) ([]ArchiveEntry, error) {
	if source == nil {
		return nil, nil
	}
	return source.ArchiveEntries()
}

// buildWorkshopsIndexItems mirrors internal/workshops/store.go's
// ListVisible exactly (#0519's plan §3), over the WorkshopSource's full,
// unfiltered list rather than a second SQL query:
//
//   - Visible: Status is published or canceled, AND Published is true.
//   - Upcoming: StartsAt is empty (or unparseable -- treated as empty) OR
//     >= now, sorted by start ascending with empty/unparseable last.
//   - Past: StartsAt is non-empty and parseable AND < now, sorted by start
//     descending.
//   - Ties (including two empty StartsAt values) keep source order --
//     sort.SliceStable, matching ListVisible's own "ORDER BY starts_at ...,
//     id ASC/DESC" tie-break.
func buildWorkshopsIndexItems(all []Workshop, now time.Time) (upcoming, past []workshopListItem) {
	type dated struct {
		w        Workshop
		startsAt time.Time
		hasTime  bool
	}
	var visible []dated
	for _, w := range all {
		if (w.Status != WorkshopPublished && w.Status != WorkshopCanceled) || !w.Published {
			continue
		}
		d := dated{w: w}
		if w.StartsAt != "" {
			if t, err := time.Parse(time.RFC3339, w.StartsAt); err == nil {
				d.startsAt = t
				d.hasTime = true
			}
		}
		visible = append(visible, d)
	}

	var upcomingRaw, pastRaw []dated
	for _, d := range visible {
		if !d.hasTime || !d.startsAt.Before(now) {
			upcomingRaw = append(upcomingRaw, d)
		} else {
			pastRaw = append(pastRaw, d)
		}
	}
	sort.SliceStable(upcomingRaw, func(i, j int) bool {
		a, b := upcomingRaw[i], upcomingRaw[j]
		if !a.hasTime {
			return false // empty/unparseable always sorts last
		}
		if !b.hasTime {
			return true
		}
		return a.startsAt.Before(b.startsAt)
	})
	sort.SliceStable(pastRaw, func(i, j int) bool {
		return pastRaw[i].startsAt.After(pastRaw[j].startsAt)
	})

	for _, d := range upcomingRaw {
		upcoming = append(upcoming, toWorkshopListItem(d.w))
	}
	for _, d := range pastRaw {
		past = append(past, toWorkshopListItem(d.w))
	}
	return upcoming, past
}

// toWorkshopListItem builds one /workshops index row (#0519's plan §3):
// title link, a meta line combining the date label and location label
// (prefixed "Canceled · " when the workshop's status is canceled), and the
// summary if non-empty.
func toWorkshopListItem(w Workshop) workshopListItem {
	meta := workshopDateLabel(w.StartsAt, w.EndsAt) + " · " + workshopLocationLabel(w.LocationName)
	if w.Status == WorkshopCanceled {
		meta = "Canceled · " + meta
	}
	return workshopListItem{Slug: w.Slug, Title: w.Title, Meta: meta, Summary: w.Summary}
}

// workshopLocationLabel mirrors web/src/lib/workshops.ts's
// workshopLocationLabel: the trimmed name, or "Location TBA".
func workshopLocationLabel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Location TBA"
	}
	return name
}

// workshopLocationLines mirrors web/src/lib/workshopDetail.ts's
// workshopLocationLines for the fields seo.Workshop carries (location_note
// is not part of that struct -- see workshop.go's own doc comment; adding
// it is #0519's follow-up 1's kind of scope, not this slice's).
func workshopLocationLines(name, address string) []string {
	var lines []string
	if n := strings.TrimSpace(name); n != "" {
		lines = append(lines, n)
	}
	if a := strings.TrimSpace(address); a != "" {
		lines = append(lines, a)
	}
	return lines
}

// buildArchiveIndexItems filters all to Published entries, keeping source
// order (#0519's plan §3: the adapter's ArchiveEntries already returns
// ListArchived's archived_at DESC order; ArchiveSource's own doc comment
// says the same defence-in-depth filter sitemap.go's Build already applies
// -- this mirrors that, not a new rule).
func buildArchiveIndexItems(all []ArchiveEntry) []archiveListItem {
	var items []archiveListItem
	for _, e := range all {
		if !e.Published {
			continue
		}
		items = append(items, archiveListItem{
			Slug: e.Slug, Subject: e.Subject,
			DateAttr: e.UpdatedAt, DateLabel: archiveDateLabel(e.UpdatedAt),
			Preheader: e.Preheader,
		})
	}
	return items
}

// archiveDateLabel formats ArchiveEntry.UpdatedAt (a bare "2006-01-02" date
// -- cmd/opencircuit/campaign_archive_seo_source.go's toSEOArchiveEntry's
// own format) as "Jan 2, 2006". Empty or unparseable input returns "".
func archiveDateLabel(updatedAt string) string {
	if updatedAt == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", updatedAt)
	if err != nil {
		return ""
	}
	return t.Format("Jan 2, 2006")
}

// losAngelesLocation loads America/Los_Angeles once (time.LoadLocation is
// not free -- it parses a zoneinfo file, here via the time/tzdata blank
// import above rather than the host's own copy). A load failure is not
// expected -- time/tzdata bundles this zone -- but falls back to UTC rather
// than panicking a live request; workshopDateLabel's own zone-abbreviation
// suffix would then read "UTC" instead of "PDT"/"PST", a degraded label,
// not a crash.
var (
	workshopDateLocOnce sync.Once
	workshopDateLoc     *time.Location
)

func losAngelesLocation() *time.Location {
	workshopDateLocOnce.Do(func() {
		loc, err := time.LoadLocation("America/Los_Angeles")
		if err != nil {
			loc = time.UTC
		}
		workshopDateLoc = loc
	})
	return workshopDateLoc
}

// workshopDateLabel mirrors web/src/lib/workshops.ts's formatWorkshopDate,
// but fixed to America/Los_Angeles rather than the viewer's own browser
// timezone -- there is no browser, and no per-request timezone, at HTML
// generation time (#0519's plan §3). "" or an unparseable StartsAt returns
// "Date TBA". Otherwise: "Jan 2, 2006, 3:04 PM" in that zone; with a
// parseable EndsAt, "-3:04 PM" is appended (an en dash in real output --
// this comment stays ASCII per CLAUDE.md §8's backslash-escape gotcha); the
// whole label always ends with a space and the zone abbreviation from that
// same instant's own "MST"-layout format (e.g. "PDT" in September), e.g.
// "Sep 12, 2026, 11:00 AM-12:30 PM PDT".
func workshopDateLabel(startsAt, endsAt string) string {
	if startsAt == "" {
		return "Date TBA"
	}
	start, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		return "Date TBA"
	}
	loc := losAngelesLocation()
	startLocal := start.In(loc)
	label := startLocal.Format("Jan 2, 2006, 3:04 PM")
	if endsAt != "" {
		if end, err := time.Parse(time.RFC3339, endsAt); err == nil {
			label += "–" + end.In(loc).Format("3:04 PM")
		}
	}
	return label + " " + startLocal.Format("MST")
}
