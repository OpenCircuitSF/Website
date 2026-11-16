package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/eventtypes"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/venues"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// v1EventsEnv serves the #0677 operations over /api/v1 with real stores, a
// counting SEO invalidator and real API keys. Every id a test targets is a
// row it made: its own event type, venue and interest (AGENTS.md §8b).
type v1EventsEnv struct {
	pool       *pgxpool.Pool
	inv        *countingCacheInvalidator
	reg        *api.Registry
	api        http.Handler
	write      string
	writeTok   apitokens.Token
	read       string
	venuesRead string
	typeID     int64
	typeSlug   string
	venueID    int64
	interestID int64
}

func newV1EventsEnv(t *testing.T) *v1EventsEnv {
	t.Helper()
	pool := interestsTestPool(t)
	ctx := context.Background()
	inv := &countingCacheInvalidator{}
	wh := NewAdminWorkshopsHandler(workshops.NewStore(pool), inv, mailing.NewCampaignStore(pool), audit.New(pool), "https://site.example.test")
	d := V1Deps{
		BaseURL: "https://site.example.test/", Workshops: wh,
		Venues: venues.NewStore(pool), EventTypes: eventtypes.NewStore(pool), Interests: interests.NewStore(pool),
	}
	reg := v1TestRegistry(pool, eventOperations(d)...)
	h := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})

	et := seedEventTypeRow(t, pool)
	addr := "1 Test St"
	note := "Ring the bell"
	venueID := seedVenue(t, pool, fmt.Sprintf("Zz v1 venue %d", testdb.Unique()), &addr, &note)
	in, err := interests.NewStore(pool).Create(ctx, testInterestSlug(t, pool), "Zz v1 interest", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Runs before the type, venue and interest cleanups (LIFO), so they can
	// be deleted.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE event_type_id = $1`, et.ID)
	})
	write, wt := v1TestKey(t, pool, apitokens.ScopeEventsWrite)
	read, _ := v1TestKey(t, pool, apitokens.ScopeEventsRead)
	vr, _ := v1TestKey(t, pool, apitokens.ScopeVenuesRead)
	return &v1EventsEnv{pool: pool, inv: inv, reg: reg, api: h, write: write, writeTok: wt, read: read, venuesRead: vr,
		typeID: et.ID, typeSlug: et.Slug, venueID: venueID, interestID: in.ID}
}

func (e *v1EventsEnv) call(t *testing.T, method, path, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	return apitest.Do(t, e.api, method, path, secret, body)
}

// create POSTs a create_event in the env's type with extra JSON fields and
// requires a 201.
func (e *v1EventsEnv) create(t *testing.T, title, extra string) eventDetailV1 {
	t.Helper()
	rec := e.call(t, http.MethodPost, "/api/v1/events", e.write,
		fmt.Sprintf(`{"title":%q,"event_type_id":%d%s}`, title, e.typeID, extra))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	ev := v1Decode[eventDetailV1](t, rec)
	cleanupAdminWorkshop(t, e.pool, ev.ID)
	return ev
}

func (e *v1EventsEnv) post(t *testing.T, id int64, action, body string, want int) eventDetailV1 {
	t.Helper()
	rec := e.call(t, http.MethodPost, fmt.Sprintf("/api/v1/events/%d/%s", id, action), e.write, body)
	if rec.Code != want {
		t.Fatalf("%s = %d %s, want %d", action, rec.Code, rec.Body, want)
	}
	ev := v1Decode[eventDetailV1](t, rec)
	if want == http.StatusCreated {
		cleanupAdminWorkshop(t, e.pool, ev.ID)
	}
	return ev
}

func (e *v1EventsEnv) wantField(t *testing.T, rec *httptest.ResponseRecorder, field string) {
	t.Helper()
	d := apitest.ErrorOf(t, rec.Body.Bytes())
	if rec.Code != http.StatusBadRequest || d.Code != api.CodeValidation || d.Fields[field] == "" {
		t.Errorf("got %d %+v, want a validation error naming %s", rec.Code, d, field)
	}
}

func v1EventTitle(prefix string) string { return fmt.Sprintf("%s %d", prefix, testdb.Unique()) }

func TestV1Events_CreateDraftThenPublish(t *testing.T) {
	e := newV1EventsEnv(t)
	title := v1EventTitle("V1 Create")
	ev := e.create(t, title, fmt.Sprintf(`,"venue_id":%d`, e.venueID))
	if ev.Status != workshops.StatusDraft || ev.Slug != slugifyForTest(title) || ev.Public || ev.PublishedAt != nil {
		t.Fatalf("created %+v", ev.eventV1)
	}
	if ev.PublicURL != "https://site.example.test/events/"+ev.Slug {
		t.Errorf("public_url %q", ev.PublicURL)
	}
	pub := e.post(t, ev.ID, "publish", "", http.StatusOK)
	if pub.Status != workshops.StatusPublished || pub.PublishedAt == nil || !pub.Public {
		t.Fatalf("published %+v", pub.eventV1)
	}
	for _, action := range []string{audit.ActionWorkshopCreated, audit.ActionWorkshopPublished} {
		rows := v1AuditRows(t, e.pool, action, e.writeTok.ID)
		if len(rows) != 1 || rows[0].ActorID != nil || rows[0].TargetID == nil || *rows[0].TargetID != ev.ID {
			t.Errorf("%s rows %+v", action, rows)
		}
	}
}

// slugifyForTest is the slug Create gives a fresh title: lowercase, runs of
// anything else become one hyphen.
func slugifyForTest(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func TestV1Events_PublishInvalidatesSEO(t *testing.T) {
	e := newV1EventsEnv(t)
	ev := e.create(t, v1EventTitle("V1 SEO"), "")
	if e.inv.calls != 1 {
		t.Fatalf("after create: %d", e.inv.calls)
	}
	rec := e.call(t, http.MethodPatch, fmt.Sprintf("/api/v1/events/%d", ev.ID), e.write, `{"summary":"new"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	if e.inv.calls != 2 {
		t.Fatalf("after update: %d", e.inv.calls)
	}
	for i, action := range []string{"publish", "unpublish", "cancel"} {
		e.post(t, ev.ID, action, "", http.StatusOK)
		if e.inv.calls != 3+i {
			t.Fatalf("after %s: %d", action, e.inv.calls)
		}
	}
}

func TestV1Events_TransitionRulesMirrorUI(t *testing.T) {
	e := newV1EventsEnv(t)
	draft := e.create(t, v1EventTitle("V1 Rules"), "")
	rec := e.call(t, http.MethodPost, fmt.Sprintf("/api/v1/events/%d/unpublish", draft.ID), e.write, "")
	if d := apitest.ErrorOf(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || d.Code != api.CodeConflict {
		t.Errorf("unpublish a draft = %d %+v", rec.Code, d)
	}
	e.post(t, draft.ID, "publish", "", http.StatusOK)
	rec = e.call(t, http.MethodPost, fmt.Sprintf("/api/v1/events/%d/publish", draft.ID), e.write, "")
	if d := apitest.ErrorOf(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || d.Code != api.CodeConflict {
		t.Errorf("publish a published = %d %+v", rec.Code, d)
	}
	e.post(t, draft.ID, "cancel", "", http.StatusOK)
	before := e.inv.calls
	rec = e.call(t, http.MethodPost, fmt.Sprintf("/api/v1/events/%d/cancel", draft.ID), e.write, "")
	if d := apitest.ErrorOf(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || d.Code != api.CodeConflict {
		t.Errorf("cancel a canceled = %d %+v", rec.Code, d)
	}
	got, err := workshops.NewStore(e.pool).GetByID(context.Background(), draft.ID)
	if err != nil || got.Status != workshops.StatusCanceled || e.inv.calls != before {
		t.Errorf("row after refused cancel: %+v %v (invalidations %d -> %d)", got.Status, err, before, e.inv.calls)
	}
	rec = e.call(t, http.MethodPost, "/api/v1/events/999999999/publish", e.write, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("publish missing = %d", rec.Code)
	}
}

func TestV1Events_DuplicateToNewDate(t *testing.T) {
	e := newV1EventsEnv(t)
	title := v1EventTitle("V1 Dup")
	src := e.create(t, title, fmt.Sprintf(
		`,"starts_at":"2030-06-04T18:00:00-07:00","ends_at":"2030-06-04T20:00:00-07:00","venue_id":%d,"interest_ids":[%d],"signup_url":"https://lu.ma/v1dup","capacity":12,"summary":"s","body_md":"b"`,
		e.venueID, e.interestID))
	dup := e.post(t, src.ID, "duplicate", `{"starts_at":"2030-06-11T18:00:00-07:00"}`, http.StatusCreated)
	if dup.ID == src.ID || dup.Slug != src.Slug+"-2" || dup.Status != workshops.StatusDraft {
		t.Fatalf("dup %+v", dup.eventV1)
	}
	wantStart := src.StartsAt.Add(7 * 24 * time.Hour)
	wantEnd := src.EndsAt.Add(7 * 24 * time.Hour)
	if !dup.StartsAt.Equal(wantStart) || !dup.EndsAt.Equal(wantEnd) {
		t.Errorf("dup times %v %v", dup.StartsAt, dup.EndsAt)
	}
	if dup.Type.ID != e.typeID || dup.Venue == nil || dup.Venue.ID != e.venueID ||
		len(dup.Interests) != 1 || dup.Interests[0].ID != e.interestID ||
		dup.SignupURL == nil || *dup.SignupURL != "https://lu.ma/v1dup" || dup.Capacity == nil || *dup.Capacity != 12 ||
		dup.BodyMD == nil || *dup.BodyMD != "b" {
		t.Errorf("dup fields %+v", dup)
	}
	rows := v1AuditRows(t, e.pool, audit.ActionWorkshopCreated, e.writeTok.ID)
	if len(rows) != 2 || rows[1].Metadata["copied_from"] != float64(src.ID) {
		t.Errorf("created rows %+v", rows)
	}
	again := e.post(t, src.ID, "duplicate", "", http.StatusCreated)
	if again.Slug != src.Slug+"-3" || !again.StartsAt.Equal(*src.StartsAt) || !again.EndsAt.Equal(*src.EndsAt) {
		t.Errorf("second dup %+v", again.eventV1)
	}
	rec := e.call(t, http.MethodPost, "/api/v1/events/999999999/duplicate", e.write, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("dup missing = %d", rec.Code)
	}
	rec = e.call(t, http.MethodPost, fmt.Sprintf("/api/v1/events/%d/duplicate", src.ID), e.write, `{"starts_at":"next week"}`)
	e.wantField(t, rec, "starts_at")
}

func TestV1Events_DuplicateAcrossDST(t *testing.T) {
	e := newV1EventsEnv(t)
	src := e.create(t, v1EventTitle("V1 DST"), `,"starts_at":"2026-10-30T18:00:00-07:00","ends_at":"2026-10-30T20:00:00-07:00"`)
	dup := e.post(t, src.ID, "duplicate", `{"starts_at":"2026-11-06T18:00:00-08:00"}`, http.StatusCreated)
	if got := dup.EndsAt.Sub(*dup.StartsAt); got != 2*time.Hour {
		t.Errorf("duration %v", got)
	}
	rec := e.call(t, http.MethodGet, fmt.Sprintf("/api/v1/events/%d", dup.ID), e.read, "")
	if !strings.Contains(rec.Body.String(), `"ends_at":"2026-11-06T20:00:00-08:00"`) {
		t.Errorf("ends_at not 20:00 PST: %s", rec.Body)
	}
}

func TestV1Events_ValidationErrors(t *testing.T) {
	e := newV1EventsEnv(t)
	ctx := context.Background()
	var badVenue, badInterest int64
	if err := e.pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) + 1000 FROM venues`).Scan(&badVenue); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) + 1000 FROM interests`).Scan(&badInterest); err != nil {
		t.Fatal(err)
	}
	disabled := seedVenue(t, e.pool, fmt.Sprintf("Zz v1 disabled %d", testdb.Unique()), nil, nil)
	disableVenue(t, e.pool, disabled)
	ev := e.create(t, v1EventTitle("V1 Valid"), `,"starts_at":"2030-06-04T18:00:00-07:00"`)
	before := workshopCount(t, e.pool)

	cases := []struct {
		name, method, field, body string
	}{
		{"unknown venue", http.MethodPost, "venue_id", fmt.Sprintf(`{"title":"x","venue_id":%d}`, badVenue)},
		{"disabled venue", http.MethodPost, "venue_id", fmt.Sprintf(`{"title":"x","venue_id":%d}`, disabled)},
		{"ends before starts", http.MethodPost, "ends_at", `{"title":"x","starts_at":"2030-06-04T18:00:00-07:00","ends_at":"2030-06-04T17:00:00-07:00"}`},
		{"ends equal starts", http.MethodPost, "ends_at", `{"title":"x","starts_at":"2030-06-04T18:00:00-07:00","ends_at":"2030-06-04T18:00:00-07:00"}`},
		{"bad starts_at", http.MethodPost, "starts_at", `{"title":"x","starts_at":"tomorrow"}`},
		{"capacity 0", http.MethodPost, "capacity", `{"title":"x","capacity":0}`},
		{"javascript signup", http.MethodPost, "signup_url", `{"title":"x","signup_url":"javascript:alert(1)"}`},
		{"empty title", http.MethodPost, "title", `{"title":"  "}`},
		{"unknown interest", http.MethodPost, "interest_ids", fmt.Sprintf(`{"title":"x","interest_ids":[%d]}`, badInterest)},
		{"venue and no_venue", http.MethodPost, "venue_id", fmt.Sprintf(`{"title":"x","venue_id":%d,"no_venue":true}`, e.venueID)},
		{"slug is unknown", http.MethodPost, "slug", `{"title":"x","slug":"mine"}`},
		{"update ends before starts", http.MethodPatch, "ends_at", `{"ends_at":"2030-06-04T17:00:00-07:00"}`},
		{"update status is unknown", http.MethodPatch, "status", `{"status":"published"}`},
		{"update empty", http.MethodPatch, "body", `{}`},
		{"update capacity both", http.MethodPatch, "capacity", `{"capacity":3,"clear_capacity":true}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := "/api/v1/events"
			if c.method == http.MethodPatch {
				path = fmt.Sprintf("/api/v1/events/%d", ev.ID)
			}
			e.wantField(t, e.call(t, c.method, path, e.write, c.body), c.field)
		})
	}
	if after := workshopCount(t, e.pool); after != before {
		t.Errorf("workshops %d -> %d after refused writes", before, after)
	}
	got, err := workshops.NewStore(e.pool).GetByID(ctx, ev.ID)
	if err != nil || got.Status != workshops.StatusDraft || got.EndsAt != nil {
		t.Errorf("row changed: %+v %v", got, err)
	}
}

func workshopCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM workshops`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestV1Events_NoEmailOnWrite(t *testing.T) {
	e := newV1EventsEnv(t)
	counts := func() (int, int) {
		var q, c int
		if err := e.pool.QueryRow(context.Background(),
			`SELECT (SELECT count(*) FROM outbound_queue), (SELECT count(*) FROM email_campaigns)`).Scan(&q, &c); err != nil {
			t.Fatal(err)
		}
		return q, c
	}
	q0, c0 := counts()
	ev := e.create(t, v1EventTitle("V1 Quiet"), fmt.Sprintf(`,"interest_ids":[%d]`, e.interestID))
	if rec := e.call(t, http.MethodPatch, fmt.Sprintf("/api/v1/events/%d", ev.ID), e.write, `{"title":"V1 Quiet renamed"}`); rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	e.post(t, ev.ID, "publish", "", http.StatusOK)
	e.post(t, ev.ID, "duplicate", "", http.StatusCreated)
	if q1, c1 := counts(); q1 != q0 || c1 != c0 {
		t.Errorf("outbound_queue %d -> %d, email_campaigns %d -> %d", q0, q1, c0, c1)
	}
}

func TestV1Events_ListFiltersAndPages(t *testing.T) {
	e := newV1EventsEnv(t)
	a := e.create(t, v1EventTitle("V1 List A"), fmt.Sprintf(`,"starts_at":"2030-06-04T18:00:00-07:00","venue_id":%d`, e.venueID))
	b := e.create(t, v1EventTitle("V1 List B"), `,"starts_at":"2030-05-01T18:00:00-07:00","no_venue":true`)
	c := e.create(t, v1EventTitle("V1 List C"), "")
	e.post(t, a.ID, "publish", "", http.StatusOK)

	list := func(q string) []int64 {
		t.Helper()
		rec := e.call(t, http.MethodGet, "/api/v1/events?type="+e.typeSlug+q, e.read, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list %s = %d %s", q, rec.Code, rec.Body)
		}
		var ids []int64
		for _, ev := range v1Decode[api.List[eventV1]](t, rec).Items {
			ids = append(ids, ev.ID)
		}
		return ids
	}
	if got := list(""); !slices.Equal(got, []int64{b.ID, a.ID, c.ID}) {
		t.Errorf("all = %v, want b, a, c (soonest first, undated last)", got)
	}
	if got := list("&status=published"); !slices.Equal(got, []int64{a.ID}) {
		t.Errorf("published = %v", got)
	}
	if got := list(fmt.Sprintf("&venue=%d", e.venueID)); !slices.Equal(got, []int64{a.ID}) {
		t.Errorf("venue = %v", got)
	}
	if got := list("&from=" + url.QueryEscape("2030-06-01T00:00:00Z")); !slices.Equal(got, []int64{a.ID}) {
		t.Errorf("from = %v", got)
	}
	if got := list("&to=" + url.QueryEscape("2030-06-01T00:00:00Z")); !slices.Equal(got, []int64{b.ID}) {
		t.Errorf("to = %v", got)
	}

	var walked []int64
	cursor := ""
	for range 5 {
		rec := e.call(t, http.MethodGet, "/api/v1/events?limit=1&type="+e.typeSlug+cursor, e.read, "")
		page := v1Decode[api.List[eventV1]](t, rec)
		if len(page.Items) != 1 {
			t.Fatalf("page %s", rec.Body)
		}
		walked = append(walked, page.Items[0].ID)
		if page.NextCursor == nil {
			break
		}
		cursor = "&cursor=" + url.QueryEscape(*page.NextCursor)
	}
	if !slices.Equal(walked, []int64{b.ID, a.ID, c.ID}) {
		t.Errorf("walked %v", walked)
	}
	e.wantField(t, e.call(t, http.MethodGet, "/api/v1/events?status=cancelled", e.read, ""), "status")
	e.wantField(t, e.call(t, http.MethodGet, "/api/v1/events?cursor=bogus", e.read, ""), "cursor")
}

func TestV1Events_GetIncludesBody(t *testing.T) {
	e := newV1EventsEnv(t)
	ev := e.create(t, v1EventTitle("V1 Get"), fmt.Sprintf(
		`,"starts_at":"2030-12-02T18:00:00Z","body_md":"# Hello","venue_id":%d,"interest_ids":[%d],"signup_url":"https://lu.ma/x"`,
		e.venueID, e.interestID))
	rec := e.call(t, http.MethodGet, fmt.Sprintf("/api/v1/events/%d", ev.ID), e.read, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	got := v1Decode[eventDetailV1](t, rec)
	if got.BodyMD == nil || *got.BodyMD != "# Hello" || got.LocationNote == nil || *got.LocationNote != "Ring the bell" {
		t.Errorf("detail %+v", got)
	}
	if got.Venue == nil || got.Venue.Address == nil || *got.Venue.Address != "1 Test St" {
		t.Errorf("venue %+v", got.Venue)
	}
	if len(got.Interests) != 1 || got.Interests[0].Name != "Zz v1 interest" {
		t.Errorf("interests %+v", got.Interests)
	}
	if !strings.Contains(rec.Body.String(), `"starts_at":"2030-12-02T10:00:00-08:00"`) {
		t.Errorf("starts_at not Pacific: %s", rec.Body)
	}
	if got.LumaURL == nil || *got.LumaURL != "https://lu.ma/x" {
		t.Errorf("luma_url %v", got.LumaURL)
	}
	if rec := e.call(t, http.MethodGet, "/api/v1/events/999999999", e.read, ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing = %d", rec.Code)
	}
}

func TestLumaURL(t *testing.T) {
	for in, want := range map[string]bool{
		"https://lu.ma/x": true, "https://luma.com/e/abc": true, "https://www.luma.com/x": true,
		"https://evil-lu.ma/x": false, "https://lu.ma.evil.test/x": false, "mailto:a@lu.ma": false,
		"/events/x": false, "https://example.com": false,
	} {
		if got := lumaURL(&in) != nil; got != want {
			t.Errorf("lumaURL(%q) = %v, want %v", in, got, want)
		}
	}
	if lumaURL(nil) != nil {
		t.Error("nil")
	}
}

func TestV1Events_Scopes(t *testing.T) {
	e := newV1EventsEnv(t)
	ev := e.create(t, v1EventTitle("V1 Scopes"), "")
	forbidden := func(method, path, secret, body string) {
		t.Helper()
		rec := e.call(t, method, path, secret, body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", method, path, rec.Code)
		}
	}
	id := fmt.Sprintf("/api/v1/events/%d", ev.ID)
	forbidden(http.MethodPost, "/api/v1/events", e.read, `{"title":"x"}`)
	forbidden(http.MethodPatch, id, e.read, `{"title":"x"}`)
	forbidden(http.MethodPost, id+"/publish", e.read, "")
	forbidden(http.MethodPost, id+"/duplicate", e.read, "")
	forbidden(http.MethodGet, "/api/v1/events", e.venuesRead, "")
	forbidden(http.MethodGet, "/api/v1/venues", e.write, "")
	if rec := e.call(t, http.MethodGet, "/api/v1/events?type="+e.typeSlug, e.write, ""); rec.Code != http.StatusOK {
		t.Errorf("write lists = %d", rec.Code)
	}
	if rec := e.call(t, http.MethodGet, "/api/v1/venues", e.venuesRead, ""); rec.Code != http.StatusOK {
		t.Errorf("venues:read lists venues = %d", rec.Code)
	}
}

func TestV1Events_MCPListAndCreate(t *testing.T) {
	e := newV1EventsEnv(t)
	e.create(t, v1EventTitle("V1 MCP A"), "")
	e.create(t, v1EventTitle("V1 MCP B"), "")
	mcpH := mcpserver.NewHandler(mcpserver.Deps{Registry: e.reg, Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter()})
	cs := mcptest.Connect(t, mcpH, e.write)

	res := mcptest.Call(t, cs, "list_events", map[string]any{"limit": 1, "type": e.typeSlug})
	if res.IsError {
		t.Fatalf("list tool error: %s", mcptest.Text(res))
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var page api.List[eventV1]
	if err := json.Unmarshal(raw, &page); err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("list %s (%v)", raw, err)
	}

	res = mcptest.Call(t, cs, "create_event", map[string]any{"title": v1EventTitle("V1 MCP C"), "event_type_id": e.typeID})
	if res.IsError {
		t.Fatalf("create tool error: %s", mcptest.Text(res))
	}
	raw, _ = json.Marshal(res.StructuredContent)
	var ev eventDetailV1
	if err := json.Unmarshal(raw, &ev); err != nil || ev.Status != workshops.StatusDraft {
		t.Fatalf("create %s (%v)", raw, err)
	}
	cleanupAdminWorkshop(t, e.pool, ev.ID)
	if rows := v1AuditRows(t, e.pool, audit.ActionWorkshopCreated, e.writeTok.ID); len(rows) != 3 {
		t.Errorf("%d created rows", len(rows))
	}

	bad := mcptest.Call(t, cs, "create_event", map[string]any{"title": "x", "starts_at": "2030-06-04T18:00:00Z", "ends_at": "2030-06-04T17:00:00Z"})
	if d := mcptest.ErrorOf(t, bad); d.Code != api.CodeValidation || d.Fields["ends_at"] == "" {
		t.Errorf("ends before start via MCP %+v", d)
	}

	readCS := mcptest.Connect(t, mcpH, e.read)
	tools, err := readCS.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	if slices.Contains(names, "create_event") || !slices.Contains(names, "list_events") || slices.Contains(names, "list_venues") {
		t.Errorf("read key tools %v", names)
	}
}
