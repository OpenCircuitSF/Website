package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/luma"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

type fixedLumaFeed struct{ snap luma.Snapshot }

func (f fixedLumaFeed) Feed(context.Context) luma.Snapshot { return f.snap }

// newV1OutreachResultsEnv serves the #0698 operations through the full v1
// registry (BuildV1Registry), so a path clash with another outreach route
// would panic here.
func newV1OutreachResultsEnv(t *testing.T, enabled bool) *v1OutreachWriteEnv {
	t.Helper()
	e := newOutreachEnv(t, enabled)
	reg := BuildV1Registry(api.Config{Version: "test", Idempotency: api.NewIdempotencyStore(e.pool)}, V1Deps{Outreach: e.h})
	h := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})
	read, _ := v1TestKey(t, e.pool, apitokens.ScopeOutreachRead)
	none, _ := v1TestKey(t, e.pool, apitokens.ScopeEventsRead)
	return &v1OutreachWriteEnv{outreachEnv: e, api: h, read: read, write: none}
}

func TestV1OutreachResults_ForEvent(t *testing.T) {
	e := newV1OutreachResultsEnv(t, true)
	w, panel := e.seedResultsOutreach(t)
	ctx := context.Background()
	store := outreach.NewStore(e.pool)
	var bsky int64
	for _, p := range panel.Posts {
		if p.Channel == "bluesky" {
			bsky = p.ID
		}
	}
	if _, err := e.pool.Exec(ctx, `UPDATE outreach_posts SET status='posted', posted_at=now() WHERE id=$1`, bsky); err != nil {
		t.Fatal(err)
	}
	store.RecordEngagement(ctx, bsky, time.Now(), outreach.Counts{Likes: 4, Reposts: 2, Replies: 1, Quotes: 3})
	e.links.statsRollup = &shortlinks.CampaignRollup{
		ClickCount: 9, WindowFrom: "2026-10-01", WindowTo: "2026-10-17",
		ByLink: []shortlinks.LinkBucket{{Key: "k1", Count: 7}, {Key: "k2", Count: 2}},
	}
	path := fmt.Sprintf("/api/v1/events/%d/outreach/results", w.ID)

	e.v1(t, http.MethodGet, path, e.write, "", http.StatusForbidden)
	b := e.v1(t, http.MethodGet, path, e.read, "", http.StatusOK)
	assertPacific(t, b)
	var r outreachResultsView
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Status != resultsOK || r.FetchedAt == nil || !strings.Contains(*r.FetchedAt, "-0") {
		t.Errorf("status/fetched = %s %v", r.Status, r.FetchedAt)
	}
	found := false
	for _, p := range r.Posts {
		if p.ID != bsky {
			continue
		}
		found = true
		if p.Clicks == nil || *p.Clicks != 7 {
			t.Errorf("bluesky clicks = %v", p.Clicks)
		}
		if g := p.Engagement; g == nil || g.Likes != 4 || g.Reposts != 2 || g.Replies != 1 || g.Quotes != 3 || !strings.HasSuffix(g.CapturedAt, ":00") {
			t.Errorf("bluesky engagement = %+v", g)
		}
		if p.PostedAt == nil || !strings.Contains(*p.PostedAt, "-0") {
			t.Errorf("posted_at = %v", p.PostedAt)
		}
	}
	if !found {
		t.Fatalf("bluesky post missing: %s", b)
	}
	e.v1(t, http.MethodGet, "/api/v1/events/999999999/outreach/results", e.read, "", http.StatusNotFound)

}

// A ShortLinks failure still answers 200, with the status named and the
// posts listed (a fresh env, since the stats cache would answer otherwise).
func TestV1OutreachResults_ShortLinksFailureIsAStatus(t *testing.T) {
	e := newV1OutreachResultsEnv(t, true)
	w, _ := e.seedResultsOutreach(t)
	e.links.statsErr = shortlinks.ErrNotFound
	b := e.v1(t, http.MethodGet, fmt.Sprintf("/api/v1/events/%d/outreach/results", w.ID), e.read, "", http.StatusOK)
	assertPacific(t, b)
	if !strings.Contains(string(b), `"status":"campaign_missing"`) || !strings.Contains(string(b), `"channel":"bluesky"`) {
		t.Errorf("failure body %s", b)
	}
}

func TestV1OutreachResults_Summary(t *testing.T) {
	e := newV1OutreachResultsEnv(t, true)
	w, _ := e.seedResultsOutreach(t)
	e.links.statsRollup = &shortlinks.CampaignRollup{ClickCount: 5}
	e.v1(t, http.MethodGet, "/api/v1/outreach/results", e.write, "", http.StatusForbidden)
	b := e.v1(t, http.MethodGet, "/api/v1/outreach/results", e.read, "", http.StatusOK)
	assertPacific(t, b)
	var v resultsSummaryView
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	for _, row := range v.Rows {
		if row.WorkshopID == w.ID {
			if row.StartsAt == nil || !strings.HasSuffix(*row.StartsAt, "-08:00") || row.Clicks == nil || *row.Clicks != 5 {
				t.Errorf("row = %+v", row)
			}
			return
		}
	}
	t.Errorf("no row for event %d: %s", w.ID, b)
}

func TestV1OutreachResults_Timeline(t *testing.T) {
	e := newV1OutreachResultsEnv(t, false)
	e.beforeEvent()
	w := e.published(t)
	o := e.start(t, w).Outreach
	e.createPost(t, o.ID, "bluesky", "announce")
	e.v1(t, http.MethodGet, "/api/v1/outreach/timeline", e.write, "", http.StatusForbidden)
	b := e.v1(t, http.MethodGet, "/api/v1/outreach/timeline", e.read, "", http.StatusOK)
	assertPacific(t, b)
	var v outreachTimelineView
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(v.Now, "-08:00") {
		t.Errorf("now = %s", v.Now)
	}
	for _, ev := range v.Events {
		if ev.Workshop.ID == w.ID {
			if ev.Outreach == nil || len(ev.Posts) != 1 || !strings.HasSuffix(ev.Outreach.CreatedAt, ":00") {
				t.Errorf("event = %+v", ev)
			}
			return
		}
	}
	t.Errorf("event %d not on the timeline: %s", w.ID, b)
}

func TestV1OutreachResults_Luma(t *testing.T) {
	e := newV1OutreachResultsEnv(t, false)
	e.v1(t, http.MethodGet, "/api/v1/outreach/luma", e.write, "", http.StatusForbidden)
	b := e.v1(t, http.MethodGet, "/api/v1/outreach/luma", e.read, "", http.StatusOK)
	if !strings.Contains(string(b), `"status":"off"`) {
		t.Errorf("unwired luma %s", b)
	}

	e.beforeEvent()
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
	e.h.SetLuma(fixedLumaFeed{luma.Snapshot{Status: luma.StatusOK, ReadAt: now, LastGoodAt: now,
		Events: []luma.Event{{UID: "x", Summary: "Somebody else's meetup", URL: "https://lu.ma/x", Start: now.Add(72 * time.Hour)}}}})
	b = e.v1(t, http.MethodGet, "/api/v1/outreach/luma", e.read, "", http.StatusOK)
	assertPacific(t, b)
	var v outreachLumaView
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v.ReadAt == nil || *v.ReadAt != "2026-11-05T10:00:00-08:00" || len(v.Leftovers) != 1 || v.Leftovers[0].StartsAt != "2026-11-08T10:00:00-08:00" {
		t.Errorf("luma = %s", b)
	}
}
