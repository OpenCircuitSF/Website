package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

type fakeEngagementStore struct {
	win          outreach.EngagementWindow
	err          error
	called       bool
	since, until time.Time
}

func (f *fakeEngagementStore) EngagementWindow(_ context.Context, since, until time.Time) (outreach.EngagementWindow, error) {
	f.called, f.since, f.until = true, since, until
	return f.win, f.err
}

var engNow = time.Date(2026, 11, 8, 15, 30, 45, 500_000_000, time.UTC)

func serveEngagement(t *testing.T, f *fakeEngagementStore, query string) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()
	h := NewEngagementAPIHandler(f)
	h.now = func() time.Time { return engNow }
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/engagement"+query, nil))
	var body map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func rawString(t *testing.T, m map[string]json.RawMessage, k string) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(m[k], &s); err != nil {
		t.Fatalf("%s: %v (%s)", k, err, m[k])
	}
	return s
}

func TestEngagementAPI_DefaultWindow24Hours(t *testing.T) {
	f := &fakeEngagementStore{}
	rec, body := serveEngagement(t, f, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	if f.until.Sub(f.since) != 24*time.Hour || !f.until.Equal(engNow.Truncate(time.Second)) {
		t.Fatalf("window %v .. %v", f.since, f.until)
	}
	if got := rawString(t, body, "since"); got != "2026-11-07T15:30:45Z" {
		t.Fatalf("since %q", got)
	}
	if got := rawString(t, body, "until"); got != "2026-11-08T15:30:45Z" {
		t.Fatalf("until %q", got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("Cache-Control not no-store")
	}
}

func TestEngagementAPI_HoursParam(t *testing.T) {
	for _, n := range []int{1, 168} {
		f := &fakeEngagementStore{}
		rec, _ := serveEngagement(t, f, "?hours="+strconv.Itoa(n))
		if rec.Code != http.StatusOK || f.until.Sub(f.since) != time.Duration(n)*time.Hour {
			t.Fatalf("hours=%d: code %d window %v", n, rec.Code, f.until.Sub(f.since))
		}
	}
}

func TestEngagementAPI_SinceParam(t *testing.T) {
	f := &fakeEngagementStore{}
	rec, body := serveEngagement(t, f, "?since="+url.QueryEscape("2026-11-07T09:00:00.75-08:00"))
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d %s", rec.Code, rec.Body)
	}
	want := time.Date(2026, 11, 7, 17, 0, 0, 0, time.UTC)
	if !f.since.Equal(want) {
		t.Fatalf("since %v", f.since)
	}
	if got := rawString(t, body, "since"); got != "2026-11-07T17:00:00Z" {
		t.Fatalf("since %q", got)
	}
	if got := rawString(t, body, "summary"); got != "No new engagement on Bluesky or Mastodon since Nov 7 at 9:00 AM PST." {
		t.Fatalf("summary %q", got)
	}
}

func TestEngagementAPI_BadParams400(t *testing.T) {
	const hoursMsg = "hours must be a whole number from 1 to 168"
	future := engNow.Add(time.Minute).Format(time.RFC3339)
	old := engNow.Add(-30*24*time.Hour - time.Minute).Format(time.RFC3339)
	for _, tc := range []struct{ query, msg string }{
		{"?hours=0", hoursMsg},
		{"?hours=169", hoursMsg},
		{"?hours=abc", hoursMsg},
		{"?hours=1.5", hoursMsg},
		{"?hours=", hoursMsg},
		{"?since=yesterday", "since must be an RFC 3339 time"},
		{"?since=2026-11-08", "since must be an RFC 3339 time"},
		{"?since=" + url.QueryEscape(future), "since must not be in the future"},
		{"?since=" + url.QueryEscape(old), "since must be within the last 30 days"},
		{"?hours=24&since=" + url.QueryEscape(old), "use either hours or since, not both"},
	} {
		f := &fakeEngagementStore{}
		rec, body := serveEngagement(t, f, tc.query)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code %d", tc.query, rec.Code)
			continue
		}
		if got := rawString(t, body, "error"); got != tc.msg {
			t.Errorf("%s: error %q, want %q", tc.query, got, tc.msg)
		}
		if f.called {
			t.Errorf("%s: store called", tc.query)
		}
	}
}

func TestEngagementAPI_EmptyResponse(t *testing.T) {
	f := &fakeEngagementStore{win: outreach.EngagementWindow{Mentions: map[string]int{"bluesky": 0, "mastodon": 0}}}
	rec, body := serveEngagement(t, f, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	for k, want := range map[string]string{"has_engagement": "false", "total": "0", "posts": "[]", "data_as_of": "null"} {
		if string(body[k]) != want {
			t.Errorf("%s = %s, want %s", k, body[k], want)
		}
	}
	if got := rawString(t, body, "summary"); got != "No new engagement on Bluesky or Mastodon in the last 24 hours." {
		t.Errorf("summary %q", got)
	}
}

func TestEngagementAPI_MixedResponse(t *testing.T) {
	u1, u2 := "https://bsky.app/profile/x/post/1", "https://sfba.social/@x/2"
	posted := time.Date(2026, 11, 6, 18, 0, 0, 0, time.FixedZone("x", -8*3600))
	starts := time.Date(2026, 11, 10, 3, 30, 0, 0, time.UTC)
	asOf := time.Date(2026, 11, 8, 15, 0, 0, 0, time.FixedZone("y", 3600))
	f := &fakeEngagementStore{win: outreach.EngagementWindow{
		Posts: []outreach.PostDelta{
			{PostID: 1, Channel: "bluesky", Wave: "reminder", URL: &u1, PostedAt: &posted, WorkshopTitle: "Hack Night", StartsAt: &starts, Delta: outreach.Counts{Likes: 3, Reposts: 1}},
			{PostID: 2, Channel: "mastodon", Wave: "announce", URL: &u2, WorkshopTitle: "Solder Night", Delta: outreach.Counts{Likes: 2}},
		},
		Mentions: map[string]int{"bluesky": 1, "mastodon": 0},
		DataAsOf: &asOf,
	}}
	rec, _ := serveEngagement(t, f, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	var resp engagementResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.HasEngagement || resp.Total != 7 {
		t.Fatalf("has %v total %d", resp.HasEngagement, resp.Total)
	}
	if resp.Bluesky != (engagementCounts{3, 1, 0, 0, 1, 5}) || resp.Mastodon != (engagementCounts{2, 0, 0, 0, 0, 2}) {
		t.Fatalf("channels %+v %+v", resp.Bluesky, resp.Mastodon)
	}
	if resp.Summary != "7 new interactions: Bluesky 5 (3 likes, 1 repost, 1 mention), Mastodon 2 (2 likes)." {
		t.Fatalf("summary %q", resp.Summary)
	}
	if resp.DataAsOf == nil || *resp.DataAsOf != "2026-11-08T14:00:00Z" {
		t.Fatalf("data_as_of %v", resp.DataAsOf)
	}
	p := resp.Posts[0]
	if len(resp.Posts) != 2 || p.Event != "Hack Night" || p.EventDate == nil || *p.EventDate != "2026-11-09" ||
		p.Wave != "reminder" || p.Channel != "bluesky" || p.URL == nil || *p.URL != u1 ||
		p.PostedAt == nil || *p.PostedAt != "2026-11-07T02:00:00Z" || p.Likes != 3 || p.Reposts != 1 || p.Total != 4 {
		t.Fatalf("post 0 %+v", p)
	}
	if resp.Posts[1].EventDate != nil || resp.Posts[1].PostedAt != nil {
		t.Fatalf("post 1 %+v", resp.Posts[1])
	}
}

func TestEngagementAPI_StoreError500(t *testing.T) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	rec, body := serveEngagement(t, &fakeEngagementStore{err: errors.New("boom")}, "")
	if rec.Code != http.StatusInternalServerError || rawString(t, body, "error") != "internal error" {
		t.Fatalf("code %d body %s", rec.Code, rec.Body)
	}
}

func TestEngagementAPI_WithTokenThroughMiddleware(t *testing.T) {
	pool := outreachTestPool(t)
	clean := func() { pool.Exec(context.Background(), `DELETE FROM api_tokens`) }
	t.Cleanup(clean)
	clean()
	_, secret, err := apitokens.NewStore(pool).Create(context.Background(), "HA", []string{apitokens.ScopeEngagementRead}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	h := NewAPITokensHandler(apitokens.NewStore(pool), audit.New(pool))
	h.AddV1Route("/api/v1/engagement", apitokens.ScopeEngagementRead, NewEngagementAPIHandler(outreach.NewStore(pool)))
	mux := http.NewServeMux()
	for _, r := range h.V1Routes() {
		mux.Handle("GET "+r.Path, middleware.RequireAPIToken(h.Resolver(), r.Scope)(r.Handler))
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	get := func(bearer string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/engagement", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	code, b := get(secret)
	if code != http.StatusOK {
		t.Fatalf("bearer GET = %d %s", code, b)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"since", "until", "data_as_of", "has_engagement", "total", "bluesky", "mastodon", "posts", "summary"} {
		if _, ok := body[k]; !ok {
			t.Errorf("missing key %s", k)
		}
	}
	if code, _ := get(""); code != http.StatusUnauthorized {
		t.Errorf("no header = %d, want 401", code)
	}
	other, err := apitokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := get(other); code != http.StatusUnauthorized {
		t.Errorf("unknown token = %d, want 401", code)
	}
}

type stubScopeResolver struct{ scopes []string }

func (s stubScopeResolver) Resolve(context.Context, string, time.Time) (apitokens.Token, error) {
	return apitokens.Token{ID: 1, Name: "x", Scopes: s.scopes}, nil
}

func TestEngagementAPI_WrongScope403(t *testing.T) {
	secret, err := apitokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeEngagementStore{}
	h := middleware.RequireAPIToken(stubScopeResolver{[]string{"other:read"}}, apitokens.ScopeEngagementRead)(NewEngagementAPIHandler(f))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/engagement", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || f.called {
		t.Fatalf("code %d, store called %v", rec.Code, f.called)
	}
}
