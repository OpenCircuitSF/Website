package handlers

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/cadence"
	"github.com/brennanMKE/OpenCircuitSF/internal/eventtypes"
	"github.com/brennanMKE/OpenCircuitSF/internal/luma"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/venues"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// The event coverage service (#0678): cadence rules and the "are we missing
// any events next month?" report. EventCadenceHandler holds the one
// transport-free implementation; admin_event_cadence.go serves it to the
// admin session and v1_coverage.go to API tokens and MCP, so the two cannot
// differ.

// cadenceRuleStore is the rule table the service reads and writes;
// *cadence.Store satisfies it.
type cadenceRuleStore interface {
	List(ctx context.Context) ([]cadence.Rule, error)
	Get(ctx context.Context, id int64) (cadence.Rule, error)
	Create(ctx context.Context, r cadence.Rule) (cadence.Rule, error)
	Update(ctx context.Context, id int64, r cadence.Rule) (cadence.Rule, error)
	Delete(ctx context.Context, id int64) error
}

// workshopLister reads every event; *workshops.Store satisfies it.
type workshopLister interface {
	List(ctx context.Context) ([]workshops.Workshop, error)
}

// EventCadenceHandler is the coverage service. luma may be nil (status
// "off"); main.go passes the same *luma.Checker the outreach Luma route uses,
// so the two share one cache and one fetch.
type EventCadenceHandler struct {
	rules   cadenceRuleStore
	events  workshopLister
	types   eventTypeLister
	venues  venueLister
	luma    lumaFeed
	auditor *audit.Logger
	baseURL string
	now     func() time.Time
}

// NewEventCadenceHandler builds the coverage service.
func NewEventCadenceHandler(rules cadenceRuleStore, events workshopLister, types eventTypeLister, venueList venueLister, feed lumaFeed, auditor *audit.Logger, baseURL string) *EventCadenceHandler {
	return &EventCadenceHandler{rules: rules, events: events, types: types, venues: venueList, luma: feed, auditor: auditor, baseURL: strings.TrimRight(baseURL, "/"), now: time.Now}
}

// cadenceFieldError is a rule or month the service refuses, by field.
type cadenceFieldError struct {
	fields map[string]string
}

func (e *cadenceFieldError) Error() string { return "cadence: invalid " + cadence.FirstError(e.fields) }

// message is the first field's message, for the admin's plain 400.
func (e *cadenceFieldError) message() string {
	if m := cadence.FirstError(e.fields); m != "" {
		return m
	}
	for f, m := range e.fields {
		return f + ": " + m
	}
	return "invalid request"
}

// ── shapes ───────────────────────────────────────────────────────────────────

type cadenceVenueRefV1 struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type cadenceRuleV1 struct {
	ID          int64              `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description" jsonschema:"the frequency in words, e.g. every Tuesday or 2 a month"`
	EventType   *eventTypeRefV1    `json:"event_type" jsonschema:"null: any type"`
	TitlePrefix *string            `json:"title_prefix" jsonschema:"case-insensitive; null: any title"`
	Frequency   string             `json:"frequency" jsonschema:"weekly or monthly"`
	Weekday     *string            `json:"weekday" jsonschema:"weekly only: sunday … saturday"`
	PerMonth    *int               `json:"per_month" jsonschema:"monthly only"`
	Venue       *cadenceVenueRefV1 `json:"venue" jsonschema:"null: any venue"`
	Active      bool               `json:"active"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

type coverageEventV1 struct {
	ID        int64     `json:"id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Status    string    `json:"status" jsonschema:"draft, published or canceled"`
	StartsAt  time.Time `json:"starts_at" jsonschema:"Pacific time"`
	AdminURL  string    `json:"admin_url"`
	PublicURL string    `json:"public_url"`
}

type coverageExpectedV1 struct {
	Dates []string `json:"dates" jsonschema:"weekly: every expected date (YYYY-MM-DD, Pacific); monthly: empty"`
	Count int      `json:"count"`
}

type ruleCoverageV1 struct {
	Rule           cadenceRuleV1      `json:"rule"`
	Expected       coverageExpectedV1 `json:"expected"`
	Events         []coverageEventV1  `json:"events" jsonschema:"matching events that are not canceled, drafts included"`
	Canceled       []coverageEventV1  `json:"canceled" jsonschema:"matching canceled events; they never count"`
	Drafts         []coverageEventV1  `json:"drafts" jsonschema:"matching events still in draft"`
	MissingDates   []string           `json:"missing_dates" jsonschema:"weekly: expected dates with no event"`
	DraftOnlyDates []string           `json:"draft_only_dates" jsonschema:"weekly: expected dates covered only by drafts"`
	Missing        int                `json:"missing" jsonschema:"how many events are missing"`
	State          string             `json:"state" jsonschema:"ok, drafts (covered, partly by drafts) or missing"`
}

type coverageLumaDiffV1 struct {
	Event     coverageEventV1 `json:"event"`
	LumaURL   *string         `json:"luma_url"`
	LumaTitle *string         `json:"luma_title"`
	Diffs     []luma.Diff     `json:"diffs"`
}

type coverageLumaOnlyV1 struct {
	Title    string    `json:"title"`
	StartsAt time.Time `json:"starts_at"`
	LumaURL  *string   `json:"luma_url"`
}

type coverageLumaV1 struct {
	Status     luma.Status          `json:"status" jsonschema:"ok, off, unreachable or malformed; the lists are empty unless ok"`
	ReadAt     *string              `json:"read_at"`
	LastGoodAt *string              `json:"last_good_at"`
	NotOnLuma  []coverageEventV1    `json:"not_on_luma" jsonschema:"published events with no Luma copy"`
	Differs    []coverageLumaDiffV1 `json:"differs" jsonschema:"published events whose Luma copy has different details"`
	OnlyOnLuma []coverageLumaOnlyV1 `json:"only_on_luma" jsonschema:"Luma events in the month that match no published event"`
	Note       *string              `json:"note"`
}

type coverageSummaryV1 struct {
	Rules     int `json:"rules" jsonschema:"active rules"`
	Missing   int `json:"missing" jsonschema:"events missing across all rules"`
	Drafts    int `json:"drafts" jsonschema:"distinct matching events still in draft"`
	Unmatched int `json:"unmatched"`
}

type coverageV1 struct {
	Month           string            `json:"month" jsonschema:"YYYY-MM"`
	Label           string            `json:"label" jsonschema:"e.g. December 2026"`
	Zone            string            `json:"zone"`
	From            time.Time         `json:"from"`
	To              time.Time         `json:"to"`
	Rules           []ruleCoverageV1  `json:"rules" jsonschema:"one per active rule"`
	UnmatchedEvents []coverageEventV1 `json:"unmatched_events" jsonschema:"events in the month that match no active rule, canceled ones excluded"`
	Luma            coverageLumaV1    `json:"luma"`
	Summary         coverageSummaryV1 `json:"summary"`
}

// ── conversions ──────────────────────────────────────────────────────────────

// cadenceNames are the type and venue names a rule shows.
type cadenceNames struct {
	types  map[int64]eventtypes.EventType
	venues map[int64]venues.Venue
}

func (h *EventCadenceHandler) names(ctx context.Context) (cadenceNames, error) {
	n := cadenceNames{types: map[int64]eventtypes.EventType{}, venues: map[int64]venues.Venue{}}
	if h.types != nil {
		all, err := h.types.ListAll(ctx)
		if err != nil {
			return n, err
		}
		for _, t := range all {
			n.types[t.ID] = t
		}
	}
	if h.venues != nil {
		all, err := h.venues.ListAll(ctx)
		if err != nil {
			return n, err
		}
		for _, v := range all {
			n.venues[v.ID] = v
		}
	}
	return n, nil
}

func (n cadenceNames) rule(r cadence.Rule) cadenceRuleV1 {
	out := cadenceRuleV1{
		ID: r.ID, Name: r.Name, Description: r.Describe(), Frequency: string(r.Frequency), Active: r.Active,
		CreatedAt: *inPacific(&r.CreatedAt), UpdatedAt: *inPacific(&r.UpdatedAt),
	}
	if r.EventTypeID != nil {
		t := n.types[*r.EventTypeID]
		out.EventType = &eventTypeRefV1{ID: *r.EventTypeID, Slug: t.Slug, Name: t.Name}
	}
	if r.TitlePrefix != "" {
		p := r.TitlePrefix
		out.TitlePrefix = &p
	}
	if r.Weekday != nil {
		w := cadence.WeekdayName(*r.Weekday)
		out.Weekday = &w
	}
	if r.Frequency == cadence.Monthly {
		pm := r.PerMonth
		out.PerMonth = &pm
	}
	if r.VenueID != nil {
		out.Venue = &cadenceVenueRefV1{ID: *r.VenueID, Name: n.venues[*r.VenueID].Name}
	}
	return out
}

func (h *EventCadenceHandler) event(e cadence.Event) coverageEventV1 {
	return coverageEventV1{
		ID: e.ID, Slug: e.Slug, Title: e.Title, Status: e.Status, StartsAt: *inPacific(&e.StartsAt),
		AdminURL:  h.baseURL + "/admin#/workshops/" + strconv.FormatInt(e.ID, 10),
		PublicURL: h.baseURL + "/events/" + e.Slug,
	}
}

func (h *EventCadenceHandler) eventList(es []cadence.Event) []coverageEventV1 {
	out := make([]coverageEventV1, 0, len(es))
	for _, e := range es {
		out = append(out, h.event(e))
	}
	return out
}

func nonNilDates(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// cadenceEvent maps a dated workshop; ok is false for an undated one.
func cadenceEvent(w workshops.Workshop) (cadence.Event, bool) {
	if w.StartsAt == nil {
		return cadence.Event{}, false
	}
	return cadence.Event{ID: w.ID, Slug: w.Slug, Title: w.Title, Status: w.Status, StartsAt: *w.StartsAt, EventTypeID: w.EventTypeID, VenueID: w.VenueID}, true
}

// ── the service ──────────────────────────────────────────────────────────────

// listRules returns every rule as v1 shows it, by id.
func (h *EventCadenceHandler) listRules(ctx context.Context) ([]cadenceRuleV1, error) {
	rules, err := h.rules.List(ctx)
	if err != nil {
		return nil, err
	}
	n, err := h.names(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]cadenceRuleV1, 0, len(rules))
	for _, r := range rules {
		out = append(out, n.rule(r))
	}
	return out, nil
}

// report is the coverage of month ("YYYY-MM"; "" is next month in
// America/Los_Angeles). The Luma part never fails it: an unreadable feed is
// a status, not an error.
func (h *EventCadenceHandler) report(ctx context.Context, month string) (coverageV1, error) {
	now := h.now()
	m := cadence.NextMonth(now)
	if month != "" {
		var err error
		if m, err = cadence.ParseMonth(month); err != nil {
			return coverageV1{}, &cadenceFieldError{fields: map[string]string{"month": "must be YYYY-MM"}}
		}
	}
	rules, err := h.rules.List(ctx)
	if err != nil {
		return coverageV1{}, err
	}
	all, err := h.events.List(ctx)
	if err != nil {
		return coverageV1{}, err
	}
	n, err := h.names(ctx)
	if err != nil {
		return coverageV1{}, err
	}
	evs := make([]cadence.Event, 0, len(all))
	for _, w := range all {
		if e, ok := cadenceEvent(w); ok {
			evs = append(evs, e)
		}
	}
	rep := cadence.Compute(m, rules, evs)

	out := coverageV1{
		Month: m.String(), Label: m.Label(), Zone: cadence.Pacific.String(),
		From: rep.From, To: rep.To,
		Rules: make([]ruleCoverageV1, 0, len(rep.Rules)), UnmatchedEvents: h.eventList(rep.Unmatched),
	}
	drafts := map[int64]bool{}
	for _, rc := range rep.Rules {
		out.Rules = append(out.Rules, ruleCoverageV1{
			Rule:           n.rule(rc.Rule),
			Expected:       coverageExpectedV1{Dates: nonNilDates(rc.ExpectedDates), Count: rc.ExpectedCount},
			Events:         h.eventList(rc.Events),
			Canceled:       h.eventList(rc.Canceled),
			Drafts:         h.eventList(rc.Drafts),
			MissingDates:   nonNilDates(rc.MissingDates),
			DraftOnlyDates: nonNilDates(rc.DraftOnlyDates),
			Missing:        rc.Missing,
			State:          rc.State,
		})
		out.Summary.Missing += rc.Missing
		for _, d := range rc.Drafts {
			drafts[d.ID] = true
		}
	}
	out.Summary.Rules, out.Summary.Drafts, out.Summary.Unmatched = len(out.Rules), len(drafts), len(out.UnmatchedEvents)
	out.Luma = h.lumaPart(ctx, all, rep.From, rep.To, now)
	return out, nil
}

// lumaPart compares the month's published events with the Luma feed.
func (h *EventCadenceHandler) lumaPart(ctx context.Context, all []workshops.Workshop, from, to, now time.Time) coverageLumaV1 {
	out := coverageLumaV1{Status: luma.StatusOff, NotOnLuma: []coverageEventV1{}, Differs: []coverageLumaDiffV1{}, OnlyOnLuma: []coverageLumaOnlyV1{}}
	if h.luma == nil {
		return out
	}
	snap := h.luma.Feed(ctx)
	out.Status, out.LastGoodAt = snap.Status, rfc3339OrNil(snap.LastGoodAt)
	if snap.Status != luma.StatusOK {
		return out
	}
	out.ReadAt = rfc3339OrNil(snap.ReadAt)
	var list []luma.Workshop
	byID := map[int64]cadence.Event{}
	for _, w := range all {
		if w.Status != workshops.StatusPublished || w.StartsAt == nil || w.StartsAt.Before(from) || !w.StartsAt.Before(to) {
			continue
		}
		list = append(list, lumaWorkshopFor(w))
		byID[w.ID], _ = cadenceEvent(w)
	}
	results, left := luma.Compare(list, snap.Events, now)
	for _, res := range results {
		ev := h.event(byID[res.WorkshopID])
		switch res.State {
		case luma.StateNotOnLuma:
			out.NotOnLuma = append(out.NotOnLuma, ev)
		case luma.StateDiffers:
			d := coverageLumaDiffV1{Event: ev, Diffs: res.Diffs}
			if res.Event != nil {
				d.LumaURL, d.LumaTitle = strOrNil(res.Event.URL), strOrNil(res.Event.Summary)
			}
			out.Differs = append(out.Differs, d)
		}
	}
	for _, e := range left {
		if e.Start.Before(from) || !e.Start.Before(to) {
			continue
		}
		out.OnlyOnLuma = append(out.OnlyOnLuma, coverageLumaOnlyV1{Title: e.Summary, StartsAt: *inPacific(&e.Start), LumaURL: strOrNil(e.URL)})
	}
	if from.Before(now) {
		note := "only_on_luma lists Luma events from now on; earlier ones in the month are not compared"
		out.Note = &note
	}
	return out
}

// setRule creates r (id nil) or replaces rule *id, after Validate. Refusals
// are *cadenceFieldError, or cadence.ErrNotFound for an unknown id.
func (h *EventCadenceHandler) setRule(ctx context.Context, ip string, actorID *int64, id *int64, r cadence.Rule) (cadence.Rule, error) {
	r = r.Normalized()
	if bad := r.Validate(); bad != nil {
		return cadence.Rule{}, &cadenceFieldError{fields: bad}
	}
	var (
		saved  cadence.Rule
		err    error
		action = audit.ActionEventCadenceCreated
	)
	if id == nil {
		saved, err = h.rules.Create(ctx, r)
	} else {
		action = audit.ActionEventCadenceUpdated
		saved, err = h.rules.Update(ctx, *id, r)
	}
	switch {
	case errors.Is(err, cadence.ErrEventTypeNotFound):
		return cadence.Rule{}, &cadenceFieldError{fields: map[string]string{"event_type_id": "no event type has this id"}}
	case errors.Is(err, cadence.ErrVenueNotFound):
		return cadence.Rule{}, &cadenceFieldError{fields: map[string]string{"venue_id": "no venue has this id"}}
	case err != nil:
		return cadence.Rule{}, err
	}
	h.record(ctx, ip, actorID, action, saved)
	return saved, nil
}

// deleteRule removes rule id (admin only; v1 retires a rule with active
// false instead).
func (h *EventCadenceHandler) deleteRule(ctx context.Context, ip string, actorID *int64, id int64) error {
	cur, err := h.rules.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := h.rules.Delete(ctx, id); err != nil {
		return err
	}
	h.record(ctx, ip, actorID, audit.ActionEventCadenceDeleted, cur)
	return nil
}

func (h *EventCadenceHandler) record(ctx context.Context, ip string, actorID *int64, action string, r cadence.Rule) {
	if h.auditor == nil {
		return
	}
	var weekday, perMonth any
	if r.Weekday != nil {
		weekday = cadence.WeekdayName(*r.Weekday)
	}
	if r.Frequency == cadence.Monthly {
		perMonth = r.PerMonth
	}
	var prefix any
	if r.TitlePrefix != "" {
		prefix = r.TitlePrefix
	}
	id := r.ID
	h.auditor.Record(ctx, middleware.AttributeAPIToken(ctx, audit.Entry{
		ActorID: actorID, Action: action, TargetType: audit.TargetEventCadenceRule, TargetID: &id, IP: ip,
		Metadata: map[string]any{
			"name": r.Name, "frequency": string(r.Frequency), "weekday": weekday, "per_month": perMonth,
			"event_type_id": r.EventTypeID, "title_prefix": prefix, "venue_id": r.VenueID, "active": r.Active,
		},
	}))
}
