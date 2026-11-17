package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/eventtypes"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/venues"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// Narrow lookup seams for the #0677 operations; the real stores satisfy them.
type venueLister interface {
	ListAll(ctx context.Context) ([]venues.Venue, error)
}

type eventTypeLister interface {
	ListAll(ctx context.Context) ([]eventtypes.EventType, error)
}

type interestLister interface {
	ListAll(ctx context.Context) ([]interests.Interest, error)
}

// workshopStatuses are workshops_status_check's values, in v1's spelling
// (one L in canceled, as the database has it).
var workshopStatuses = []string{workshops.StatusDraft, workshops.StatusPublished, workshops.StatusCanceled}

// inPacific returns t in America/Los_Angeles, so it marshals with a Pacific
// offset (2026-12-02T18:00:00-08:00). nil stays nil. #0678 reuses it.
func inPacific(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	p := t.In(announceLocation)
	return &p
}

// eventOperations are the eleven /api/v1 event and lookup operations
// (#0677). Building them never touches d's stores: the closures read them
// only when called, so a parity test may pass V1Deps{}. Every write runs
// AdminWorkshopsHandler's transport-free createWorkshop or patchWorkshop,
// the same code as /admin/workshops, so validation, cache invalidation and
// audit cannot differ.
func eventOperations(d V1Deps) []api.Operation {
	return append(eventOps(d), lookupOperations(d)...)
}

// workshopAPIError maps a workshop error to the v1 envelope, from the same
// table the admin routes use (workshopErrInfo). An unrecognised error is
// returned as is, so api.AsError logs it and answers 500.
func workshopAPIError(err error) error {
	if err == nil {
		return nil
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return err
	}
	status, field, msg := workshopErrInfo(err)
	switch status {
	case http.StatusNotFound:
		return api.NotFound("event")
	case http.StatusConflict:
		return api.Conflict(msg)
	case http.StatusBadRequest:
		if field == "" {
			field = "body"
		}
		return api.Validation(map[string]string{field: msg})
	}
	return err
}

// ── shapes ───────────────────────────────────────────────────────────────────

type eventTypeRefV1 struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type eventVenueRefV1 struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Address *string `json:"address"`
}

type interestRefV1 struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type eventV1 struct {
	ID          int64            `json:"id"`
	Slug        string           `json:"slug"`
	Title       string           `json:"title"`
	Summary     *string          `json:"summary"`
	Status      string           `json:"status" jsonschema:"draft, published or canceled"`
	Type        eventTypeRefV1   `json:"type"`
	StartsAt    *time.Time       `json:"starts_at" jsonschema:"Pacific time; null when not scheduled"`
	EndsAt      *time.Time       `json:"ends_at"`
	PublishedAt *time.Time       `json:"published_at"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	Venue       *eventVenueRefV1 `json:"venue" jsonschema:"null means no location"`
	Capacity    *int             `json:"capacity"`
	SignupURL   *string          `json:"signup_url"`
	Interests   []interestRefV1  `json:"interests"`
	PublicURL   string           `json:"public_url"`
	Public      bool             `json:"public" jsonschema:"shown on the public site: published, or canceled after being published"`
	LumaURL     *string          `json:"luma_url" jsonschema:"signup_url when it is a Luma link, else null"`
}

type eventDetailV1 struct {
	eventV1
	BodyMD       *string `json:"body_md"`
	LocationNote *string `json:"location_note" jsonschema:"the venue's note"`
	CoverImage   *string `json:"cover_image"`
	CoverAlt     *string `json:"cover_alt"`
}

// lumaURL is signup when its host is lu.ma, luma.com or a subdomain of
// either.
func lumaURL(signup *string) *string {
	if signup == nil {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(*signup))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	for _, d := range []string{"lu.ma", "luma.com"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			s := *signup
			return &s
		}
	}
	return nil
}

func (d V1Deps) interestNames(ctx context.Context) (map[int64]interests.Interest, error) {
	all, err := d.Interests.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]interests.Interest, len(all))
	for _, i := range all {
		m[i.ID] = i
	}
	return m, nil
}

func (d V1Deps) toEventV1(w workshops.Workshop, names map[int64]interests.Interest) eventV1 {
	ev := eventV1{
		ID: w.ID, Slug: w.Slug, Title: w.Title, Summary: w.Summary, Status: w.Status,
		Type:        eventTypeRefV1{ID: w.EventTypeID, Slug: w.EventTypeSlug, Name: w.EventTypeName},
		StartsAt:    inPacific(w.StartsAt),
		EndsAt:      inPacific(w.EndsAt),
		PublishedAt: inPacific(w.PublishedAt),
		CreatedAt:   *inPacific(&w.CreatedAt),
		UpdatedAt:   *inPacific(&w.UpdatedAt),
		Capacity:    w.Capacity,
		SignupURL:   w.SignupURL,
		Interests:   []interestRefV1{},
		PublicURL:   strings.TrimRight(d.BaseURL, "/") + "/events/" + w.Slug,
		Public:      w.PublishedAt != nil && w.Status != workshops.StatusDraft,
		LumaURL:     lumaURL(w.SignupURL),
	}
	if w.VenueID != nil {
		v := eventVenueRefV1{ID: *w.VenueID, Address: w.LocationAddress}
		if w.LocationName != nil {
			v.Name = *w.LocationName
		}
		ev.Venue = &v
	}
	for _, id := range w.InterestIDs {
		i := names[id]
		ev.Interests = append(ev.Interests, interestRefV1{ID: id, Slug: i.Slug, Name: i.Name})
	}
	return ev
}

func (d V1Deps) toEventDetailV1(ctx context.Context, w workshops.Workshop) (eventDetailV1, error) {
	names, err := d.interestNames(ctx)
	if err != nil {
		return eventDetailV1{}, err
	}
	return eventDetailV1{eventV1: d.toEventV1(w, names), BodyMD: w.BodyMD, LocationNote: w.LocationNote, CoverImage: w.CoverImage, CoverAlt: w.CoverAlt}, nil
}

// ── inputs ───────────────────────────────────────────────────────────────────

type eventIDIn struct {
	ID int64 `json:"id" path:"id"`
}

type listEventsIn struct {
	api.Page
	From   *time.Time `json:"from,omitempty" query:"from" jsonschema:"only events starting at or after this; undated events drop out"`
	To     *time.Time `json:"to,omitempty" query:"to" jsonschema:"only events starting before this; undated events drop out"`
	Status *string    `json:"status,omitempty" query:"status" jsonschema:"draft, published or canceled"`
	Type   *string    `json:"type,omitempty" query:"type" jsonschema:"an event type slug (list_event_types)"`
	Venue  *int64     `json:"venue,omitempty" query:"venue" jsonschema:"a venue id (list_venues)"`
}

// eventCursor is list_events' cursor: the last row's start (RFC 3339 UTC,
// "" when undated) and id.
type eventCursor struct {
	S  string `json:"s"`
	ID int64  `json:"id"`
}

type createEventIn struct {
	Title       string  `json:"title"`
	Summary     *string `json:"summary,omitempty"`
	BodyMD      *string `json:"body_md,omitempty" jsonschema:"Markdown"`
	StartsAt    *string `json:"starts_at,omitempty" jsonschema:"RFC 3339 with an offset, e.g. 2026-12-02T18:00:00-08:00"`
	EndsAt      *string `json:"ends_at,omitempty" jsonschema:"RFC 3339; must be after starts_at"`
	SignupURL   *string `json:"signup_url,omitempty" jsonschema:"http(s), mailto: or a site path"`
	CoverImage  *string `json:"cover_image,omitempty" jsonschema:"a site path under /media"`
	CoverAlt    *string `json:"cover_alt,omitempty" jsonschema:"the cover's image description (alt text), up to 1500 characters; empty clears it. Mastodon posts attach the cover only when this is set"`
	Capacity    *int    `json:"capacity,omitempty" jsonschema:"1 or more; omit for no limit"`
	InterestIDs []int64 `json:"interest_ids,omitempty" jsonschema:"ids from list_interests"`
	EventTypeID *int64  `json:"event_type_id,omitempty" jsonschema:"an id from list_event_types; omit for the default type"`
	VenueID     *int64  `json:"venue_id,omitempty" jsonschema:"an id from list_venues; omit both this and no_venue for the default venue"`
	NoVenue     bool    `json:"no_venue,omitempty" jsonschema:"true for an event with no location"`
}

type updateEventIn struct {
	ID            int64    `json:"id" path:"id"`
	Title         *string  `json:"title,omitempty"`
	Summary       *string  `json:"summary,omitempty" jsonschema:"empty clears it"`
	BodyMD        *string  `json:"body_md,omitempty" jsonschema:"empty clears it"`
	StartsAt      *string  `json:"starts_at,omitempty" jsonschema:"RFC 3339; empty clears it"`
	EndsAt        *string  `json:"ends_at,omitempty" jsonschema:"RFC 3339; empty clears it"`
	SignupURL     *string  `json:"signup_url,omitempty" jsonschema:"empty clears it"`
	CoverImage    *string  `json:"cover_image,omitempty" jsonschema:"empty clears it; changing it without cover_alt clears the old alt text"`
	CoverAlt      *string  `json:"cover_alt,omitempty" jsonschema:"the cover's image description (alt text), up to 1500 characters; empty clears it. Mastodon posts attach the cover only when this is set"`
	Capacity      *int     `json:"capacity,omitempty"`
	ClearCapacity bool     `json:"clear_capacity,omitempty" jsonschema:"true removes the capacity limit"`
	InterestIDs   *[]int64 `json:"interest_ids,omitempty" jsonschema:"replaces the list; [] clears it"`
	EventTypeID   *int64   `json:"event_type_id,omitempty"`
	VenueID       *int64   `json:"venue_id,omitempty"`
	NoVenue       bool     `json:"no_venue,omitempty" jsonschema:"true removes the location"`
}

type duplicateEventIn struct {
	ID       int64   `json:"id" path:"id"`
	StartsAt *string `json:"starts_at,omitempty" jsonschema:"RFC 3339 start for the copy; the end keeps the source's duration. Omit to keep the source's times"`
}

// venueOptional maps venue_id/no_venue onto the admin's Optional venue:
// neither is absent, no_venue is null, venue_id is that venue.
func venueOptional(id *int64, none bool) (Optional[int64], error) {
	switch {
	case id != nil && none:
		return Optional[int64]{}, api.Validation(map[string]string{"venue_id": "send venue_id or no_venue, not both"})
	case none:
		return Optional[int64]{Present: true}, nil
	case id != nil:
		return Optional[int64]{Present: true, Valid: true, Value: *id}, nil
	}
	return Optional[int64]{}, nil
}

// ── operations ───────────────────────────────────────────────────────────────

func eventOps(d V1Deps) []api.Operation {
	transition := func(name, path, summary, publicEffect, to string, allowed func(string) bool, conflict string) api.Operation {
		return api.Define(api.Operation{
			Name: name, Method: http.MethodPost, Path: path,
			Scope:        apitokens.ScopeEventsWrite,
			Summary:      summary,
			PublicEffect: publicEffect,
		}, func(ctx context.Context, in eventIDIn) (eventDetailV1, error) {
			cur, err := d.Workshops.store.GetByID(ctx, in.ID)
			if err != nil {
				return eventDetailV1{}, workshopAPIError(err)
			}
			if !allowed(cur.Status) {
				return eventDetailV1{}, api.Conflict(conflict)
			}
			_, w, err := d.Workshops.patchWorkshop(ctx, "", nil, in.ID, patchWorkshopRequest{Status: &to})
			if err != nil {
				return eventDetailV1{}, workshopAPIError(err)
			}
			return d.toEventDetailV1(ctx, w)
		})
	}
	return []api.Operation{
		api.Define(api.Operation{
			Name: "list_events", Method: http.MethodGet, Path: "/api/v1/events",
			Scope:       apitokens.ScopeEventsRead,
			Summary:     "List events (workshops and other listings), soonest first, filtered by time, status, type or venue.",
			Description: "Undated events come last. Times are Pacific. get_event adds the Markdown body.",
		}, func(ctx context.Context, in listEventsIn) (api.List[eventV1], error) {
			return d.listEvents(ctx, in)
		}),
		api.Define(api.Operation{
			Name: "get_event", Method: http.MethodGet, Path: "/api/v1/events/{id}",
			Scope:   apitokens.ScopeEventsRead,
			Summary: "One event, with its Markdown body.",
		}, func(ctx context.Context, in eventIDIn) (eventDetailV1, error) {
			w, err := d.Workshops.store.GetByID(ctx, in.ID)
			if err != nil {
				return eventDetailV1{}, workshopAPIError(err)
			}
			return d.toEventDetailV1(ctx, w)
		}),
		api.Define(api.Operation{
			Name: "create_event", Method: http.MethodPost, Path: "/api/v1/events",
			Scope:       apitokens.ScopeEventsWrite,
			Summary:     "Create a draft event; the slug comes from the title.",
			Description: "Validated exactly as the admin form is. Nothing is public until publish_event, and no email is sent.",
			Idempotency: true,
			Status:      http.StatusCreated,
		}, func(ctx context.Context, in createEventIn) (eventDetailV1, error) {
			venue, err := venueOptional(in.VenueID, in.NoVenue)
			if err != nil {
				return eventDetailV1{}, err
			}
			w, err := d.Workshops.createWorkshop(ctx, "", nil, createWorkshopRequest{
				Title: in.Title, Summary: in.Summary, BodyMD: in.BodyMD, StartsAt: in.StartsAt, EndsAt: in.EndsAt,
				Capacity: in.Capacity, SignupURL: in.SignupURL, CoverImage: in.CoverImage, CoverAlt: in.CoverAlt,
				InterestIDs: in.InterestIDs, EventTypeID: in.EventTypeID, VenueID: venue,
			}, 0)
			if err != nil {
				return eventDetailV1{}, workshopAPIError(err)
			}
			return d.toEventDetailV1(ctx, w)
		}),
		api.Define(api.Operation{
			Name: "update_event", Method: http.MethodPatch, Path: "/api/v1/events/{id}",
			Scope:        apitokens.ScopeEventsWrite,
			Summary:      "Change an event's fields; only the fields sent change.",
			Description:  "Status changes go through publish_event, unpublish_event and cancel_event.",
			PublicEffect: "on a published event, the changes show on the public site right away",
		}, func(ctx context.Context, in updateEventIn) (eventDetailV1, error) {
			req, err := in.patchRequest()
			if err != nil {
				return eventDetailV1{}, err
			}
			_, w, err := d.Workshops.patchWorkshop(ctx, "", nil, in.ID, req)
			if err != nil {
				return eventDetailV1{}, workshopAPIError(err)
			}
			return d.toEventDetailV1(ctx, w)
		}),
		transition("publish_event", "/api/v1/events/{id}/publish",
			"Publish an event, as the admin's Publish button does.",
			"makes the event visible on the public site and in the sitemap right away",
			workshops.StatusPublished, func(s string) bool { return s != workshops.StatusPublished }, "event is already published"),
		transition("unpublish_event", "/api/v1/events/{id}/unpublish",
			"Return a published event to draft, taking it off the public site.", "",
			workshops.StatusDraft, func(s string) bool { return s == workshops.StatusPublished }, "only a published event can be unpublished"),
		transition("cancel_event", "/api/v1/events/{id}/cancel",
			"Cancel an event.",
			"a published event stays on the public site, marked canceled",
			workshops.StatusCanceled, func(s string) bool { return s != workshops.StatusCanceled }, "event is already canceled"),
		api.Define(api.Operation{
			Name: "duplicate_event", Method: http.MethodPost, Path: "/api/v1/events/{id}/duplicate",
			Scope:       apitokens.ScopeEventsWrite,
			Summary:     "Copy an event to a new draft, optionally on a new date: the fast way to set up a repeat workshop.",
			Description: "Copies what the admin's Clone does (title, summary, body, type, venue, capacity, signup URL, cover image, interests). The slug gets the next -2, -3 suffix.",
			Idempotency: true,
			Status:      http.StatusCreated,
		}, func(ctx context.Context, in duplicateEventIn) (eventDetailV1, error) {
			return d.duplicateEvent(ctx, in)
		}),
	}
}

// patchRequest maps update_event's input onto the admin PATCH request.
func (in updateEventIn) patchRequest() (patchWorkshopRequest, error) {
	venue, err := venueOptional(in.VenueID, in.NoVenue)
	if err != nil {
		return patchWorkshopRequest{}, err
	}
	var capacity Optional[int]
	switch {
	case in.Capacity != nil && in.ClearCapacity:
		return patchWorkshopRequest{}, api.Validation(map[string]string{"capacity": "send capacity or clear_capacity, not both"})
	case in.ClearCapacity:
		capacity = Optional[int]{Present: true}
	case in.Capacity != nil:
		capacity = Optional[int]{Present: true, Valid: true, Value: *in.Capacity}
	}
	req := patchWorkshopRequest{
		Title: in.Title, Summary: in.Summary, BodyMD: in.BodyMD, StartsAt: in.StartsAt, EndsAt: in.EndsAt,
		Capacity: capacity, SignupURL: in.SignupURL, CoverImage: in.CoverImage, CoverAlt: in.CoverAlt,
		InterestIDs: in.InterestIDs, EventTypeID: in.EventTypeID, VenueID: venue,
	}
	if req.Title == nil && req.Summary == nil && req.BodyMD == nil && req.StartsAt == nil && req.EndsAt == nil &&
		!req.Capacity.Present && req.SignupURL == nil && req.CoverImage == nil && req.CoverAlt == nil && req.InterestIDs == nil &&
		req.EventTypeID == nil && !req.VenueID.Present {
		return patchWorkshopRequest{}, api.Validation(map[string]string{"body": "send at least one field"})
	}
	return req, nil
}

func (d V1Deps) listEvents(ctx context.Context, in listEventsIn) (api.List[eventV1], error) {
	bad := map[string]string{}
	if in.Status != nil && !slices.Contains(workshopStatuses, *in.Status) {
		bad["status"] = "must be one of " + strings.Join(workshopStatuses, ", ")
	}
	if in.From != nil && in.To != nil && !in.From.Before(*in.To) {
		bad["to"] = "must be after from"
	}
	if len(bad) > 0 {
		return api.List[eventV1]{}, api.Validation(bad)
	}
	if in.Cursor != "" {
		var c eventCursor
		if err := api.DecodeCursor(in.Cursor, &c); err != nil {
			return api.List[eventV1]{}, err
		}
		if c.S != "" {
			if _, err := time.Parse(time.RFC3339Nano, c.S); err != nil {
				return api.List[eventV1]{}, api.Validation(map[string]string{"cursor": "is not a cursor this API issued"})
			}
		}
	}
	all, err := d.Workshops.store.List(ctx)
	if err != nil {
		return api.List[eventV1]{}, err
	}
	var rows []workshops.Workshop
	for _, w := range all {
		switch {
		case in.Status != nil && w.Status != *in.Status,
			in.Type != nil && w.EventTypeSlug != *in.Type,
			in.Venue != nil && (w.VenueID == nil || *w.VenueID != *in.Venue),
			(in.From != nil || in.To != nil) && w.StartsAt == nil,
			in.From != nil && w.StartsAt.Before(*in.From),
			in.To != nil && !w.StartsAt.Before(*in.To):
			continue
		}
		rows = append(rows, w)
	}
	slices.SortFunc(rows, func(a, b workshops.Workshop) int { return compareEventKey(a.StartsAt, a.ID, b.StartsAt, b.ID) })
	names, err := d.interestNames(ctx)
	if err != nil {
		return api.List[eventV1]{}, err
	}
	items := make([]eventV1, 0, len(rows))
	for _, w := range rows {
		items = append(items, d.toEventV1(w, names))
	}
	return paginate(items, in.Page,
		func(e eventV1) eventCursor {
			c := eventCursor{ID: e.ID}
			if e.StartsAt != nil {
				c.S = e.StartsAt.UTC().Format(time.RFC3339Nano)
			}
			return c
		},
		func(e eventV1, c eventCursor) bool {
			var cs *time.Time
			if c.S != "" {
				t, _ := time.Parse(time.RFC3339Nano, c.S)
				cs = &t
			}
			return compareEventKey(e.StartsAt, e.ID, cs, c.ID) > 0
		})
}

// compareEventKey orders events by start, undated last, then id.
func compareEventKey(as *time.Time, aid int64, bs *time.Time, bid int64) int {
	switch {
	case as != nil && bs == nil:
		return -1
	case as == nil && bs != nil:
		return 1
	case as != nil && bs != nil && !as.Equal(*bs):
		return as.Compare(*bs)
	}
	switch {
	case aid < bid:
		return -1
	case aid > bid:
		return 1
	}
	return 0
}

// duplicateEvent copies the source onto a new draft through createWorkshop,
// copying what the admin's Clone copies (cloneWorkshopFields in
// web/src/lib/workshopAdmin.ts). A new start keeps the source's duration,
// as an absolute length, so a DST change between the dates does not stretch
// or shrink it.
func (d V1Deps) duplicateEvent(ctx context.Context, in duplicateEventIn) (eventDetailV1, error) {
	src, err := d.Workshops.store.GetByID(ctx, in.ID)
	if err != nil {
		return eventDetailV1{}, workshopAPIError(err)
	}
	newStart, err := parseOptionalTime(in.StartsAt, "starts_at")
	if err != nil {
		return eventDetailV1{}, workshopAPIError(err)
	}
	starts, ends := src.StartsAt, src.EndsAt
	if newStart != nil {
		starts, ends = newStart, nil
		if src.StartsAt != nil && src.EndsAt != nil {
			if dur := src.EndsAt.Sub(*src.StartsAt); dur > 0 {
				e := newStart.Add(dur)
				ends = &e
			}
		}
	}
	format := func(t *time.Time) *string {
		if t == nil {
			return nil
		}
		s := t.Format(time.RFC3339Nano)
		return &s
	}
	venue := Optional[int64]{Present: true}
	if src.VenueID != nil {
		venue = Optional[int64]{Present: true, Valid: true, Value: *src.VenueID}
	}
	typeID := src.EventTypeID
	w, err := d.Workshops.createWorkshop(ctx, "", nil, createWorkshopRequest{
		Title: src.Title, Summary: src.Summary, BodyMD: src.BodyMD,
		StartsAt: format(starts), EndsAt: format(ends),
		Capacity: src.Capacity, SignupURL: src.SignupURL, CoverImage: src.CoverImage, CoverAlt: src.CoverAlt,
		InterestIDs: append([]int64(nil), src.InterestIDs...), EventTypeID: &typeID, VenueID: venue,
	}, src.ID)
	if err != nil {
		return eventDetailV1{}, workshopAPIError(err)
	}
	return d.toEventDetailV1(ctx, w)
}
