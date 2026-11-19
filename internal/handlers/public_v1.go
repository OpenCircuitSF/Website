package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// PublicDeps is what BuildPublicRegistry's operations read, only when
// called, so PublicDeps{} is enough to list them.
type PublicDeps struct {
	Workshops *PublicWorkshopsHandler
	Archive   *PublicArchiveHandler
	// About returns the About page's copy (main.go passes seo.About;
	// internal/seo imports this package, so it cannot be called directly).
	About   func() publicapi.AboutText
	BaseURL string
	Version string
}

// ── Output shapes ────────────────────────────────────────────────────────────

// PublicEvent is one event as the public tools return it. id equals slug:
// the public API exposes no numeric ids.
type PublicEvent struct {
	ID              string     `json:"id" jsonschema:"the event's id, which is its slug"`
	Slug            string     `json:"slug"`
	Title           string     `json:"title"`
	Summary         *string    `json:"summary" jsonschema:"a one-line summary, or null"`
	StartsAt        *time.Time `json:"starts_at" jsonschema:"start, RFC 3339 in America/Los_Angeles; null when not yet announced"`
	EndsAt          *time.Time `json:"ends_at" jsonschema:"end, RFC 3339 in America/Los_Angeles, or null"`
	LocationName    *string    `json:"location_name"`
	Address         *string    `json:"address"`
	Status          string     `json:"status" jsonschema:"scheduled or cancelled"`
	RegistrationURL *string    `json:"registration_url" jsonschema:"where to RSVP, or null"`
	URL             string     `json:"url" jsonschema:"the event's public page; link people to it"`
	EventType       string     `json:"event_type"`
	Tags            []string   `json:"tags" jsonschema:"interest slugs"`
}

// PublicEventDetail is get_event's result.
type PublicEventDetail struct {
	PublicEvent
	LocationNote *string `json:"location_note"`
	Description  string  `json:"description" jsonschema:"the event's description as plain text"`
}

// PublicEventList is a list of events.
type PublicEventList struct {
	Events []PublicEvent `json:"events"`
}

// PublicNewsletterIssue is one public newsletter issue.
type PublicNewsletterIssue struct {
	ID          string     `json:"id" jsonschema:"the issue's id, which is its slug"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Summary     *string    `json:"summary"`
	PublishedAt *time.Time `json:"published_at" jsonschema:"RFC 3339 in America/Los_Angeles"`
	URL         string     `json:"url" jsonschema:"the issue's public page; link people to it"`
}

// PublicNewsletterIssueDetail is get_newsletter_issue's result.
type PublicNewsletterIssueDetail struct {
	PublicNewsletterIssue
	Body string `json:"body" jsonschema:"the issue's body as plain text"`
}

// PublicNewsletterList is a list of issues.
type PublicNewsletterList struct {
	Issues []PublicNewsletterIssue `json:"issues"`
}

// PublicAboutSection is one section of the About page.
type PublicAboutSection struct {
	Heading string `json:"heading"`
	Text    string `json:"text"`
}

// PublicAbout is get_about's result.
type PublicAbout struct {
	Name                 string               `json:"name"`
	Description          string               `json:"description"`
	Sections             []PublicAboutSection `json:"sections"`
	WebsiteURL           string               `json:"website_url"`
	EventsURL            string               `json:"events_url"`
	NewsletterArchiveURL string               `json:"newsletter_archive_url"`
	SubscribeURL         string               `json:"subscribe_url"`
	ContactEmail         string               `json:"contact_email"`
	DiscordURL           string               `json:"discord_url"`
	CalendarURL          string               `json:"calendar_url"`
}

// ── Inputs ───────────────────────────────────────────────────────────────────

const dateForms = "an RFC 3339 time or a YYYY-MM-DD date in America/Los_Angeles"

type publicListEventsIn struct {
	Limit  *int    `json:"limit,omitempty" query:"limit" jsonschema:"how many events, default 10, max 50"`
	After  *string `json:"after,omitempty" query:"after" jsonschema:"only events starting at or after this: an RFC 3339 time or a YYYY-MM-DD date in America/Los_Angeles"`
	Before *string `json:"before,omitempty" query:"before" jsonschema:"only events starting at or before this (a date includes the whole day): an RFC 3339 time or a YYYY-MM-DD date in America/Los_Angeles"`
}

type publicSearchEventsIn struct {
	Query       string  `json:"query,omitempty" query:"query" jsonschema:"words to find in the title, description and tags; every word must match; cut to 200 characters; empty matches all"`
	From        *string `json:"from,omitempty" query:"from" jsonschema:"only events starting at or after this: an RFC 3339 time or a YYYY-MM-DD date in America/Los_Angeles"`
	To          *string `json:"to,omitempty" query:"to" jsonschema:"only events starting at or before this (a date includes the whole day): an RFC 3339 time or a YYYY-MM-DD date in America/Los_Angeles"`
	IncludePast bool    `json:"include_past,omitempty" query:"include_past" jsonschema:"also search past events; default false"`
	Limit       *int    `json:"limit,omitempty" query:"limit" jsonschema:"how many events, default 10, max 50"`
}

type publicSlugIn struct {
	Slug string `json:"slug" path:"slug" jsonschema:"the id (its slug), from a list or search result"`
}

type publicLimitIn struct {
	Limit *int `json:"limit,omitempty" query:"limit" jsonschema:"how many issues, default 10, max 50"`
}

type publicNoIn struct{}

// ── Registry ─────────────────────────────────────────────────────────────────

// BuildPublicRegistry returns the six public, read-only operations
// (#0729), served as MCP tools at /mcp and, by #0734, as REST under
// publicapi.Prefix. Every read goes through the public handlers' own
// stores and is re-checked with workshopPageStatus or archivePageStatus,
// so nothing the public website hides can come back.
func BuildPublicRegistry(d PublicDeps) *publicapi.Registry {
	base := strings.TrimRight(d.BaseURL, "/")
	reg := publicapi.NewRegistry(d.Version)
	reg.Add(
		publicapi.Define(api.Operation{
			Name: "list_upcoming_events", Method: http.MethodGet, Path: publicapi.Prefix + "events",
			Summary:     "List upcoming Open Circuit SF events, soonest first.",
			Description: "Events with no date yet come last.",
		}, func(ctx context.Context, in publicListEventsIn) (PublicEventList, error) {
			from, to, err := publicDateRange("after", in.After, "before", in.Before)
			if err != nil {
				return PublicEventList{}, err
			}
			upcoming, _, err := d.Workshops.visible(ctx)
			if err != nil {
				return PublicEventList{}, err
			}
			return PublicEventList{Events: d.Workshops.publicEvents(ctx, base, filterByDate(upcoming, from, to), publicapi.ClampLimit(in.Limit))}, nil
		}, func(out PublicEventList) string { return countSummary(len(out.Events), "upcoming event") }),

		publicapi.Define(api.Operation{
			Name: "search_events", Method: http.MethodGet, Path: publicapi.Prefix + "events/search",
			Summary:     "Search Open Circuit SF events by words in the title, description and tags.",
			Description: "Upcoming events come first, soonest first; with include_past, past events follow, most recent first.",
		}, func(ctx context.Context, in publicSearchEventsIn) (PublicEventList, error) {
			from, to, err := publicDateRange("from", in.From, "to", in.To)
			if err != nil {
				return PublicEventList{}, err
			}
			upcoming, past, err := d.Workshops.visible(ctx)
			if err != nil {
				return PublicEventList{}, err
			}
			rows := upcoming
			if in.IncludePast {
				rows = append(append([]workshops.Workshop(nil), upcoming...), past...)
			}
			rows = filterByDate(rows, from, to)
			tokens := strings.Fields(strings.ToLower(publicapi.CapQuery(in.Query)))
			var hits []workshops.Workshop
			for _, w := range rows {
				if d.Workshops.matches(ctx, w, tokens) {
					hits = append(hits, w)
				}
			}
			return PublicEventList{Events: d.Workshops.publicEvents(ctx, base, hits, publicapi.ClampLimit(in.Limit))}, nil
		}, func(out PublicEventList) string { return countSummary(len(out.Events), "matching event") }),

		publicapi.Define(api.Operation{
			Name: "get_event", Method: http.MethodGet, Path: publicapi.Prefix + "events/{slug}",
			Summary:     "Get one Open Circuit SF event by its id (its slug), from list_upcoming_events or search_events.",
			Description: "Includes the description and location note.",
		}, func(ctx context.Context, in publicSlugIn) (PublicEventDetail, error) {
			h := d.Workshops
			w, err := h.store.GetBySlug(ctx, in.Slug)
			if errors.Is(err, workshops.ErrNotFound) || (err == nil && workshopPageStatus(w) != http.StatusOK) {
				return PublicEventDetail{}, api.NotFound("event")
			}
			if err != nil {
				return PublicEventDetail{}, err
			}
			desc := ""
			if w.BodyMD != nil {
				if desc, err = plainText(*w.BodyMD); err != nil {
					return PublicEventDetail{}, err
				}
			}
			return PublicEventDetail{PublicEvent: h.publicEvent(ctx, base, w), LocationNote: w.LocationNote, Description: desc}, nil
		}, func(out PublicEventDetail) string {
			when := "date TBA"
			if out.StartsAt != nil {
				when = out.StartsAt.In(announceLocation).Format("Mon Jan 2 2006 3:04 PM MST")
			}
			return out.Title + " — " + when + " — " + out.Status + " — " + out.URL
		}),

		publicapi.Define(api.Operation{
			Name: "list_newsletter_issues", Method: http.MethodGet, Path: publicapi.Prefix + "newsletter",
			Summary: "List Open Circuit SF newsletter issues, newest first.",
		}, func(ctx context.Context, in publicLimitIn) (PublicNewsletterList, error) {
			cs, err := d.Archive.store.ListArchived(ctx)
			if err != nil {
				return PublicNewsletterList{}, err
			}
			limit := publicapi.ClampLimit(in.Limit)
			out := PublicNewsletterList{Issues: []PublicNewsletterIssue{}}
			for _, c := range cs {
				if archivePageStatus(c) != http.StatusOK {
					continue
				}
				if len(out.Issues) == limit {
					break
				}
				out.Issues = append(out.Issues, publicIssue(base, c))
			}
			return out, nil
		}, func(out PublicNewsletterList) string { return countSummary(len(out.Issues), "newsletter issue") }),

		publicapi.Define(api.Operation{
			Name: "get_newsletter_issue", Method: http.MethodGet, Path: publicapi.Prefix + "newsletter/{slug}",
			Summary:     "Get one Open Circuit SF newsletter issue by its id (its slug), from list_newsletter_issues.",
			Description: "The body is plain text.",
		}, func(ctx context.Context, in publicSlugIn) (PublicNewsletterIssueDetail, error) {
			c, err := d.Archive.store.GetBySlug(ctx, in.Slug)
			// Withheld (410 on the web page) is not found here too.
			if errors.Is(err, mailing.ErrCampaignNotFound) || (err == nil && archivePageStatus(c) != http.StatusOK) {
				return PublicNewsletterIssueDetail{}, api.NotFound("newsletter issue")
			}
			if err != nil {
				return PublicNewsletterIssueDetail{}, err
			}
			body, err := plainText(c.BodyMD)
			if err != nil {
				return PublicNewsletterIssueDetail{}, err
			}
			return PublicNewsletterIssueDetail{PublicNewsletterIssue: publicIssue(base, c), Body: body}, nil
		}, func(out PublicNewsletterIssueDetail) string { return out.Title + " — " + out.URL }),

		publicapi.Define(api.Operation{
			Name: "get_about", Method: http.MethodGet, Path: publicapi.Prefix + "about",
			Summary:     "What Open Circuit SF is, and how to join, subscribe or get in touch.",
			Description: "The About page's own copy, with the site's links.",
		}, func(ctx context.Context, _ publicNoIn) (PublicAbout, error) {
			if d.About == nil {
				return PublicAbout{}, errors.New("handlers: get_about has no About source")
			}
			a := d.About()
			out := PublicAbout{
				Name:                 publicSiteName,
				Description:          strings.Join(a.Intro, "\n\n"),
				Sections:             make([]PublicAboutSection, 0, len(a.Sections)),
				WebsiteURL:           base + "/",
				EventsURL:            base + "/events",
				NewsletterArchiveURL: base + "/archive",
				SubscribeURL:         base + "/subscribe",
				ContactEmail:         strings.TrimPrefix(a.Links["email"], "mailto:"),
				DiscordURL:           a.Links["discord"],
				CalendarURL:          a.Links["luma"],
			}
			for _, s := range a.Sections {
				out.Sections = append(out.Sections, PublicAboutSection{Heading: s.Heading, Text: s.Text})
			}
			return out, nil
		}, func(out PublicAbout) string {
			first, _, _ := strings.Cut(out.Description, ". ")
			return out.Name + ": " + strings.TrimSuffix(first, ".") + "."
		}),
	)
	return reg
}

// publicSiteName is seo.SiteName (which this package cannot import);
// cmd/opencircuit's public MCP wiring test pins the two together.
const publicSiteName = "Open Circuit SF"

// ── Helpers ──────────────────────────────────────────────────────────────────

// publicEventURL and publicArchiveURL are the canonical public pages.
func publicEventURL(base, slug string) string   { return base + "/events/" + slug }
func publicArchiveURL(base, slug string) string { return base + "/archive/" + slug }

// visible is ListVisible at h.now(), re-filtered by workshopPageStatus.
func (h *PublicWorkshopsHandler) visible(ctx context.Context) (upcoming, past []workshops.Workshop, err error) {
	up, pa, err := h.store.ListVisible(ctx, h.now())
	if err != nil {
		return nil, nil, err
	}
	keep := func(ws []workshops.Workshop) []workshops.Workshop {
		var out []workshops.Workshop
		for _, w := range ws {
			if workshopPageStatus(w) == http.StatusOK {
				out = append(out, w)
			}
		}
		return out
	}
	return keep(up), keep(pa), nil
}

// publicEvents maps at most limit rows; never nil.
func (h *PublicWorkshopsHandler) publicEvents(ctx context.Context, base string, ws []workshops.Workshop, limit int) []PublicEvent {
	out := make([]PublicEvent, 0, min(len(ws), limit))
	for _, w := range ws {
		if len(out) == limit {
			break
		}
		out = append(out, h.publicEvent(ctx, base, w))
	}
	return out
}

func (h *PublicWorkshopsHandler) publicEvent(ctx context.Context, base string, w workshops.Workshop) PublicEvent {
	status := "scheduled"
	if w.Status == workshops.StatusCanceled {
		status = "cancelled"
	}
	var reg *string
	if w.SignupURL != nil && IsSafeLinkHref(*w.SignupURL) {
		reg = w.SignupURL
	}
	tags := []string{}
	for _, it := range h.resolveInterests(ctx, w.InterestIDs) {
		tags = append(tags, it.Slug)
	}
	return PublicEvent{
		ID: w.Slug, Slug: w.Slug, Title: w.Title, Summary: w.Summary,
		StartsAt: inPacific(w.StartsAt), EndsAt: inPacific(w.EndsAt),
		LocationName: w.LocationName, Address: w.LocationAddress,
		Status: status, RegistrationURL: reg, URL: publicEventURL(base, w.Slug),
		EventType: w.EventTypeName, Tags: tags,
	}
}

// matches reports whether every token appears in w's title, summary,
// body, event type name, or interest slugs and names.
func (h *PublicWorkshopsHandler) matches(ctx context.Context, w workshops.Workshop, tokens []string) bool {
	if len(tokens) == 0 {
		return true
	}
	parts := []string{w.Title, w.EventTypeName}
	if w.Summary != nil {
		parts = append(parts, *w.Summary)
	}
	if w.BodyMD != nil {
		parts = append(parts, *w.BodyMD)
	}
	for _, it := range h.resolveInterests(ctx, w.InterestIDs) {
		parts = append(parts, it.Slug, it.Name)
	}
	hay := strings.ToLower(strings.Join(parts, "\n"))
	for _, t := range tokens {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

func publicIssue(base string, c mailing.Campaign) PublicNewsletterIssue {
	return PublicNewsletterIssue{
		ID: c.Slug, Slug: c.Slug, Title: c.Subject, Summary: c.Preheader,
		PublishedAt: inPacific(c.ArchivedAt), URL: publicArchiveURL(base, c.Slug),
	}
}

// plainText is md as the plain text the web page shows: raw HTML and
// images omitted, LF line endings.
func plainText(md string) (string, error) {
	s, err := mailing.RenderMarkdownText(md)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(s, "\r\n", "\n"), nil
}

// publicDateRange parses an optional inclusive lower and upper bound. A
// YYYY-MM-DD upper bound covers that whole Pacific day. The upper bound
// returned is exclusive. A zero time means unbounded.
func publicDateRange(fromName string, from *string, toName string, to *string) (lo, hi time.Time, err error) {
	bad := map[string]string{}
	if from != nil && strings.TrimSpace(*from) != "" {
		var ok bool
		if lo, _, ok = parsePublicDate(*from); !ok {
			bad[fromName] = "must be " + dateForms
		}
	}
	if to != nil && strings.TrimSpace(*to) != "" {
		t, isDate, ok := parsePublicDate(*to)
		switch {
		case !ok:
			bad[toName] = "must be " + dateForms
		case isDate:
			hi = t.AddDate(0, 0, 1)
		default:
			hi = t.Add(time.Nanosecond)
		}
	}
	if len(bad) > 0 {
		return time.Time{}, time.Time{}, api.Validation(bad)
	}
	return lo, hi, nil
}

func parsePublicDate(s string) (t time.Time, isDate, ok bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, false, true
	}
	if t, err := time.ParseInLocation("2006-01-02", s, announceLocation); err == nil {
		return t, true, true
	}
	return time.Time{}, false, false
}

// filterByDate keeps rows starting in [lo, hi). When either bound is set,
// a row with no start is dropped.
func filterByDate(ws []workshops.Workshop, lo, hi time.Time) []workshops.Workshop {
	if lo.IsZero() && hi.IsZero() {
		return ws
	}
	var out []workshops.Workshop
	for _, w := range ws {
		if w.StartsAt == nil {
			continue
		}
		if !lo.IsZero() && w.StartsAt.Before(lo) {
			continue
		}
		if !hi.IsZero() && !w.StartsAt.Before(hi) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func countSummary(n int, noun string) string {
	switch n {
	case 0:
		return "No " + noun + "s."
	case 1:
		return "1 " + noun + "."
	}
	return fmt.Sprintf("%d %ss.", n, noun)
}
