package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/cadence"
	"github.com/brennanMKE/OpenCircuitSF/internal/eventtypes"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/luma"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/venues"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// #0678's coverage tests use March 2031, far from other tests' events, and
// each makes its own event type, so its rules match only its own events.
// Unmatched assertions use contains, never equality.

// fakeLumaFeed returns a fixed snapshot and never touches the network.
type fakeLumaFeed struct{ snap luma.Snapshot }

func (f *fakeLumaFeed) Feed(context.Context) luma.Snapshot { return f.snap }

type coverageEnv struct {
	pool     *pgxpool.Pool
	h        *EventCadenceHandler
	feed     *fakeLumaFeed
	reg      *api.Registry
	api      http.Handler
	write    string
	writeTok apitokens.Token
	read     string
	guides   string
	typeID   int64
	venueID  int64
}

// coverageNow is the pinned clock: mid-February 2031, so the default month
// is March 2031.
var coverageNow = time.Date(2031, 2, 15, 12, 0, 0, 0, announceLocation)

func newCoverageEnv(t *testing.T) *coverageEnv {
	t.Helper()
	pool := interestsTestPool(t)
	et := seedEventTypeRow(t, pool)
	venueID := seedVenue(t, pool, fmt.Sprintf("Zz cov venue %d", testdb.Unique()), nil, nil)
	// Runs before the type and venue cleanups (LIFO); rules cascade with
	// the type, and prefix-only rules are deleted by cleanupCadenceRule.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE event_type_id = $1`, et.ID)
	})
	feed := &fakeLumaFeed{snap: luma.Snapshot{Status: luma.StatusOff}}
	h := NewEventCadenceHandler(cadence.NewStore(pool), workshops.NewStore(pool), eventtypes.NewStore(pool), venues.NewStore(pool), feed, audit.New(pool), "https://site.example.test/")
	h.SetNow(func() time.Time { return coverageNow })
	wh := NewAdminWorkshopsHandler(workshops.NewStore(pool), nil, mailing.NewCampaignStore(pool), audit.New(pool), "https://site.example.test")
	d := V1Deps{
		BaseURL: "https://site.example.test/", Workshops: wh,
		Venues: venues.NewStore(pool), EventTypes: eventtypes.NewStore(pool), Interests: interests.NewStore(pool),
		Cadence: h,
	}
	reg := v1TestRegistry(pool, append(eventOperations(d), coverageOperations(h)...)...)
	router := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})
	write, wt := v1TestKey(t, pool, apitokens.ScopeEventsWrite)
	read, _ := v1TestKey(t, pool, apitokens.ScopeEventsRead)
	guides, _ := v1TestKey(t, pool, apitokens.ScopeGuidesRead)
	return &coverageEnv{pool: pool, h: h, feed: feed, reg: reg, api: router, write: write, writeTok: wt, read: read, guides: guides, typeID: et.ID, venueID: venueID}
}

func (e *coverageEnv) call(t *testing.T, method, path, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	return apitest.Do(t, e.api, method, path, secret, body)
}

// event inserts an event of the env's type at the Pacific time given.
func (e *coverageEnv) event(t *testing.T, title, status, at string) int64 {
	t.Helper()
	start, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	slug := fmt.Sprintf("zz-cov-%d", testdb.Unique())
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO workshops (slug, title, event_type_id, status, starts_at, published_at)
		 VALUES ($1, $2, $3, $4, $5, CASE WHEN $4 = 'draft' THEN NULL ELSE now() END) RETURNING id`,
		slug, title, e.typeID, status, start).Scan(&id); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	cleanupAdminWorkshop(t, e.pool, id)
	return id
}

func cleanupCadenceRule(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM event_cadence_rules WHERE id = $1`, id) })
}

// setRule POSTs set_cadence_rule with the write key and requires a 200.
func (e *coverageEnv) setRule(t *testing.T, body string) cadenceRuleV1 {
	t.Helper()
	rec := e.call(t, http.MethodPost, "/api/v1/cadence-rules", e.write, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("set_cadence_rule = %d %s", rec.Code, rec.Body)
	}
	r := v1Decode[cadenceRuleV1](t, rec)
	cleanupCadenceRule(t, e.pool, r.ID)
	return r
}

func (e *coverageEnv) coverage(t *testing.T, query string) coverageV1 {
	t.Helper()
	rec := e.call(t, http.MethodGet, "/api/v1/events/coverage"+query, e.read, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("event_coverage = %d %s", rec.Code, rec.Body)
	}
	return v1Decode[coverageV1](t, rec)
}

func ruleIn(t *testing.T, rep coverageV1, id int64) ruleCoverageV1 {
	t.Helper()
	for _, r := range rep.Rules {
		if r.Rule.ID == id {
			return r
		}
	}
	t.Fatalf("rule %d not in report", id)
	return ruleCoverageV1{}
}

func eventIDs(es []coverageEventV1) []int64 {
	out := []int64{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func TestV1Coverage_Report(t *testing.T) {
	e := newCoverageEnv(t)
	prefix := fmt.Sprintf("Cov Monthly %d", testdb.Unique())
	weekly := e.setRule(t, fmt.Sprintf(`{"name":"Hack night","event_type_id":%d,"frequency":"weekly","weekday":"tuesday"}`, e.typeID))
	monthly := e.setRule(t, fmt.Sprintf(`{"name":"LEDs","title_prefix":%q,"frequency":"monthly","per_month":2}`, prefix))

	pub := e.event(t, "Hack night", workshops.StatusPublished, "2031-03-04T18:00:00-08:00")
	draft := e.event(t, "Hack night", workshops.StatusDraft, "2031-03-11T18:00:00-07:00")
	canceled := e.event(t, "Hack night", workshops.StatusCanceled, "2031-03-18T18:00:00-07:00")
	leds := e.event(t, prefix+": intro", workshops.StatusPublished, "2031-03-06T18:00:00-08:00")
	// The month after: never in March's report.
	e.event(t, "Hack night", workshops.StatusPublished, "2031-04-01T18:00:00-07:00")

	rep := e.coverage(t, "?month=2031-03")
	if rep.Month != "2031-03" || rep.Label != "March 2031" || rep.Zone != "America/Los_Angeles" {
		t.Fatalf("header %+v", rep)
	}
	if rep.From.Format(time.RFC3339) != "2031-03-01T00:00:00-08:00" || rep.To.Format(time.RFC3339) != "2031-04-01T00:00:00-07:00" {
		t.Errorf("bounds %s – %s", rep.From.Format(time.RFC3339), rep.To.Format(time.RFC3339))
	}
	w := ruleIn(t, rep, weekly.ID)
	if !slices.Equal(w.Expected.Dates, []string{"2031-03-04", "2031-03-11", "2031-03-18", "2031-03-25"}) || w.Expected.Count != 4 {
		t.Errorf("expected %+v", w.Expected)
	}
	if !slices.Equal(w.MissingDates, []string{"2031-03-18", "2031-03-25"}) || w.Missing != 2 || w.State != cadence.StateMissing {
		t.Errorf("missing %v (%d) %s", w.MissingDates, w.Missing, w.State)
	}
	if !slices.Equal(w.DraftOnlyDates, []string{"2031-03-11"}) || !slices.Equal(eventIDs(w.Drafts), []int64{draft}) {
		t.Errorf("drafts %v %v", w.DraftOnlyDates, eventIDs(w.Drafts))
	}
	if !slices.Equal(eventIDs(w.Canceled), []int64{canceled}) {
		t.Errorf("canceled %v", eventIDs(w.Canceled))
	}
	// The LEDs event is the same type, so the weekly rule lists it too.
	if !slices.Equal(eventIDs(w.Events), []int64{pub, leds, draft}) {
		t.Errorf("weekly events %v", eventIDs(w.Events))
	}
	if w.Rule.Weekday == nil || *w.Rule.Weekday != "tuesday" || w.Rule.Description != "every Tuesday" || w.Rule.EventType == nil || w.Rule.EventType.ID != e.typeID {
		t.Errorf("rule %+v", w.Rule)
	}
	m := ruleIn(t, rep, monthly.ID)
	if m.Expected.Count != 2 || len(m.Expected.Dates) != 0 || m.Missing != 1 || m.State != cadence.StateMissing || !slices.Equal(eventIDs(m.Events), []int64{leds}) {
		t.Errorf("monthly %+v", m)
	}
	for _, ev := range append(append(w.Events, w.Canceled...), m.Events...) {
		if ev.AdminURL != fmt.Sprintf("https://site.example.test/admin#/workshops/%d", ev.ID) || ev.PublicURL == "" {
			t.Errorf("event urls %+v", ev)
		}
		if _, off := ev.StartsAt.Zone(); off != -8*3600 && off != -7*3600 {
			t.Errorf("starts_at not Pacific: %s", ev.StartsAt)
		}
	}
	if rep.Summary.Missing < 3 || rep.Summary.Drafts < 1 {
		t.Errorf("summary %+v", rep.Summary)
	}
	if rep.Luma.Status != luma.StatusOff || rep.Luma.NotOnLuma == nil || rep.Luma.Differs == nil || rep.Luma.OnlyOnLuma == nil || rep.UnmatchedEvents == nil {
		t.Errorf("non-nil lists %+v", rep.Luma)
	}
}

func TestV1Coverage_DefaultsToNextMonth(t *testing.T) {
	e := newCoverageEnv(t)
	if rep := e.coverage(t, ""); rep.Month != "2031-03" {
		t.Errorf("default month %s, want 2031-03", rep.Month)
	}
}

func TestV1Coverage_BadMonth400(t *testing.T) {
	e := newCoverageEnv(t)
	for _, q := range []string{"?month=2031-13", "?month=31-03", "?month=march"} {
		rec := e.call(t, http.MethodGet, "/api/v1/events/coverage"+q, e.read, "")
		d := apitest.ErrorOf(t, rec.Body.Bytes())
		if rec.Code != http.StatusBadRequest || d.Code != api.CodeValidation || d.Fields["month"] == "" {
			t.Errorf("%s: %d %+v", q, rec.Code, d)
		}
	}
}

func TestV1Coverage_LumaUnavailable(t *testing.T) {
	e := newCoverageEnv(t)
	r := e.setRule(t, fmt.Sprintf(`{"name":"Hack night","event_type_id":%d,"frequency":"weekly","weekday":"tuesday"}`, e.typeID))
	e.event(t, "Hack night", workshops.StatusPublished, "2031-03-04T18:00:00-08:00")
	good := time.Date(2031, 2, 14, 10, 0, 0, 0, time.UTC)
	e.feed.snap = luma.Snapshot{Status: luma.StatusUnreachable, LastGoodAt: good}
	rep := e.coverage(t, "?month=2031-03")
	if rep.Luma.Status != luma.StatusUnreachable || rep.Luma.LastGoodAt == nil || len(rep.Luma.NotOnLuma)+len(rep.Luma.Differs)+len(rep.Luma.OnlyOnLuma) != 0 {
		t.Errorf("luma %+v", rep.Luma)
	}
	if w := ruleIn(t, rep, r.ID); w.Missing != 3 {
		t.Errorf("rules section incomplete: %+v", w)
	}

	// A nil feed is "off".
	e.h.luma = nil
	if rep := e.coverage(t, "?month=2031-03"); rep.Luma.Status != luma.StatusOff {
		t.Errorf("nil feed status %s", rep.Luma.Status)
	}
}

func TestV1Coverage_LumaComparison(t *testing.T) {
	e := newCoverageEnv(t)
	n := testdb.Unique()
	alpha := e.event(t, fmt.Sprintf("Alpha soldering %d", n), workshops.StatusPublished, "2031-03-05T18:00:00-08:00")
	beta := e.event(t, fmt.Sprintf("Beta robots %d", n), workshops.StatusPublished, "2031-03-12T18:00:00-07:00")
	gamma := e.event(t, fmt.Sprintf("Gamma radios %d", n), workshops.StatusPublished, "2031-03-19T18:00:00-07:00")
	delta := e.event(t, fmt.Sprintf("Delta drafts %d", n), workshops.StatusDraft, "2031-03-20T18:00:00-07:00")
	at := func(s string) time.Time {
		v, _ := time.Parse(time.RFC3339, s)
		return v
	}
	e.feed.snap = luma.Snapshot{Status: luma.StatusOK, ReadAt: coverageNow, LastGoodAt: coverageNow, Events: []luma.Event{
		{UID: "a", Summary: fmt.Sprintf("Alpha soldering %d", n), Start: at("2031-03-05T18:00:00-08:00"), URL: "https://luma.com/alpha"},
		{UID: "b", Summary: fmt.Sprintf("Beta robots %d", n), Start: at("2031-03-12T19:00:00-07:00"), URL: "https://luma.com/beta"},
		{UID: "c", Summary: "Something only on Luma", Start: at("2031-03-26T18:00:00-07:00"), URL: "https://luma.com/only"},
		{UID: "d", Summary: "Next month on Luma", Start: at("2031-04-02T18:00:00-07:00")},
	}}
	rep := e.coverage(t, "?month=2031-03")
	l := rep.Luma
	if l.Status != luma.StatusOK || l.ReadAt == nil {
		t.Fatalf("luma %+v", l)
	}
	if not := eventIDs(l.NotOnLuma); !slices.Contains(not, gamma) || slices.Contains(not, alpha) || slices.Contains(not, beta) || slices.Contains(not, delta) {
		t.Errorf("not_on_luma %v (alpha %d beta %d gamma %d delta %d)", not, alpha, beta, gamma, delta)
	}
	if len(l.Differs) != 1 || l.Differs[0].Event.ID != beta || len(l.Differs[0].Diffs) == 0 || l.Differs[0].Diffs[0].Field != "start" ||
		l.Differs[0].LumaURL == nil || *l.Differs[0].LumaURL != "https://luma.com/beta" {
		t.Errorf("differs %+v", l.Differs)
	}
	if len(l.OnlyOnLuma) != 1 || l.OnlyOnLuma[0].Title != "Something only on Luma" {
		t.Errorf("only_on_luma %+v", l.OnlyOnLuma)
	}
	if l.Note != nil {
		t.Errorf("note for a future month: %q", *l.Note)
	}
}

func TestV1Coverage_PathNotShadowed(t *testing.T) {
	e := newCoverageEnv(t)
	id := e.event(t, "Hack night", workshops.StatusPublished, "2031-03-04T18:00:00-08:00")
	rec := e.call(t, http.MethodGet, "/api/v1/events/coverage?month=2031-03", e.read, "")
	if rec.Code != http.StatusOK || v1Decode[coverageV1](t, rec).Month != "2031-03" {
		t.Errorf("coverage = %d %s", rec.Code, rec.Body)
	}
	rec = e.call(t, http.MethodGet, fmt.Sprintf("/api/v1/events/%d", id), e.read, "")
	if rec.Code != http.StatusOK || v1Decode[eventDetailV1](t, rec).ID != id {
		t.Errorf("get_event = %d %s", rec.Code, rec.Body)
	}
}

func TestV1CadenceRules_SetCreateReplaceList(t *testing.T) {
	e := newCoverageEnv(t)
	created := e.setRule(t, fmt.Sprintf(`{"name":"  LEDs  ","event_type_id":%d,"title_prefix":"Programming LEDs","frequency":"monthly","per_month":1,"venue_id":%d}`, e.typeID, e.venueID))
	if created.ID == 0 || created.Name != "LEDs" || created.PerMonth == nil || *created.PerMonth != 1 || !created.Active ||
		created.Venue == nil || created.Venue.ID != e.venueID || created.Venue.Name == "" || created.Description != "1 a month" {
		t.Fatalf("created %+v", created)
	}
	replaced := e.setRule(t, fmt.Sprintf(`{"id":%d,"name":"LEDs","event_type_id":%d,"frequency":"monthly","per_month":2,"active":false}`, created.ID, e.typeID))
	if replaced.ID != created.ID || *replaced.PerMonth != 2 || replaced.Active || replaced.TitlePrefix != nil || replaced.Venue != nil {
		t.Fatalf("replaced %+v", replaced)
	}
	other := e.setRule(t, fmt.Sprintf(`{"name":"Hack","event_type_id":%d,"frequency":"weekly","weekday":"Tuesday"}`, e.typeID))

	list := func(q string) []int64 {
		rec := e.call(t, http.MethodGet, "/api/v1/cadence-rules"+q, e.read, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list = %d %s", rec.Code, rec.Body)
		}
		var ids []int64
		for _, r := range v1Decode[api.List[cadenceRuleV1]](t, rec).Items {
			ids = append(ids, r.ID)
		}
		return ids
	}
	if ids := list("?active=true"); !slices.Contains(ids, other.ID) || slices.Contains(ids, created.ID) {
		t.Errorf("active=true %v", ids)
	}
	if ids := list("?active=false"); slices.Contains(ids, other.ID) || !slices.Contains(ids, created.ID) {
		t.Errorf("active=false %v", ids)
	}
	if ids := list(""); !slices.Contains(ids, other.ID) || !slices.Contains(ids, created.ID) {
		t.Errorf("all %v", ids)
	}

	cr := v1AuditRows(t, e.pool, audit.ActionEventCadenceCreated, e.writeTok.ID)
	up := v1AuditRows(t, e.pool, audit.ActionEventCadenceUpdated, e.writeTok.ID)
	if len(cr) != 2 || len(up) != 1 || up[0].TargetID == nil || *up[0].TargetID != created.ID || up[0].ActorID != nil {
		t.Fatalf("audit created %d updated %d %+v", len(cr), len(up), up)
	}
	if up[0].Metadata["per_month"] != float64(2) || up[0].Metadata["active"] != false || up[0].Metadata["frequency"] != "monthly" {
		t.Errorf("updated metadata %v", up[0].Metadata)
	}
}

func TestV1CadenceRules_Validation(t *testing.T) {
	e := newCoverageEnv(t)
	cases := []struct{ body, field string }{
		{`{"name":"x","frequency":"monthly","per_month":1}`, "event_type_id"},
		{fmt.Sprintf(`{"name":"x","event_type_id":%d,"frequency":"weekly"}`, e.typeID), "weekday"},
		{fmt.Sprintf(`{"name":"x","event_type_id":%d,"frequency":"weekly","weekday":"tues"}`, e.typeID), "weekday"},
		{fmt.Sprintf(`{"name":"x","event_type_id":%d,"frequency":"monthly","per_month":0}`, e.typeID), "per_month"},
		{fmt.Sprintf(`{"name":"","event_type_id":%d,"frequency":"monthly","per_month":1}`, e.typeID), "name"},
		{fmt.Sprintf(`{"name":"x","event_type_id":%d,"frequency":"daily"}`, e.typeID), "frequency"},
		{fmt.Sprintf(`{"name":"x","event_type_id":%d,"frequency":"monthly","per_month":1}`, e.typeID+1_000_000), "event_type_id"},
		{fmt.Sprintf(`{"name":"x","title_prefix":"x","venue_id":%d,"frequency":"monthly","per_month":1}`, e.venueID+1_000_000), "venue_id"},
	}
	for _, c := range cases {
		rec := e.call(t, http.MethodPost, "/api/v1/cadence-rules", e.write, c.body)
		d := apitest.ErrorOf(t, rec.Body.Bytes())
		if rec.Code != http.StatusBadRequest || d.Code != api.CodeValidation || d.Fields[c.field] == "" {
			t.Errorf("%s: %d %+v, want validation naming %s", c.body, rec.Code, d, c.field)
		}
	}
	rec := e.call(t, http.MethodPost, "/api/v1/cadence-rules", e.write,
		fmt.Sprintf(`{"id":%d,"name":"x","event_type_id":%d,"frequency":"monthly","per_month":1}`, int64(9_000_000_000)+testdb.Unique(), e.typeID))
	if d := apitest.ErrorOf(t, rec.Body.Bytes()); rec.Code != http.StatusNotFound || d.Code != api.CodeNotFound {
		t.Errorf("unknown id: %d %+v", rec.Code, d)
	}
}

func TestV1CadenceRules_Scopes(t *testing.T) {
	e := newCoverageEnv(t)
	body := fmt.Sprintf(`{"name":"x","event_type_id":%d,"frequency":"monthly","per_month":1}`, e.typeID)
	for _, c := range []struct {
		name, method, path, body string
	}{
		{"list", http.MethodGet, "/api/v1/cadence-rules", ""},
		{"coverage", http.MethodGet, "/api/v1/events/coverage?month=2031-03", ""},
	} {
		if rec := e.call(t, c.method, c.path, e.read, c.body); rec.Code != http.StatusOK {
			t.Errorf("events:read %s = %d", c.name, rec.Code)
		}
		if rec := e.call(t, c.method, c.path, e.guides, c.body); rec.Code != http.StatusForbidden {
			t.Errorf("guides:read %s = %d", c.name, rec.Code)
		}
	}
	for _, secret := range []string{e.read, e.guides} {
		if rec := e.call(t, http.MethodPost, "/api/v1/cadence-rules", secret, body); rec.Code != http.StatusForbidden {
			t.Errorf("set with a read key = %d %s", rec.Code, rec.Body)
		}
	}
}

func TestV1Coverage_MCP(t *testing.T) {
	e := newCoverageEnv(t)
	r := e.setRule(t, fmt.Sprintf(`{"name":"Hack night","event_type_id":%d,"frequency":"weekly","weekday":"tuesday"}`, e.typeID))
	mcpH := mcpserver.NewHandler(mcpserver.Deps{Registry: e.reg, Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter()})
	cs := mcptest.Connect(t, mcpH, e.read)
	res := mcptest.Call(t, cs, "event_coverage", map[string]any{"month": "2031-03"})
	if res.IsError {
		t.Fatalf("event_coverage tool error: %s", mcptest.Text(res))
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var rep coverageV1
	if err := json.Unmarshal(raw, &rep); err != nil || rep.Month != "2031-03" {
		t.Fatalf("structured %s (%v)", raw, err)
	}
	if w := ruleIn(t, rep, r.ID); w.Missing != 4 {
		t.Errorf("rule via MCP %+v", w)
	}
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, "event_coverage") || !slices.Contains(names, "list_cadence_rules") || slices.Contains(names, "set_cadence_rule") {
		t.Errorf("events:read tools %v", names)
	}
}
