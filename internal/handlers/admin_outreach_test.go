package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// fakeShortLinker records calls and answers from canned values.
type fakeShortLinker struct {
	mu        sync.Mutex
	enabled   bool
	meCalls   int
	campaigns []shortlinks.CreateCampaignRequest
	slugs     []string
	links     []shortlinks.CreateLinkRequest
	linkErr   error
	campSlug  string
	// CampaignStats (#0546): a configurable rollup or error, with every
	// request recorded.
	statsRollup *shortlinks.CampaignRollup
	statsErr    error
	statsCalls  []statsCall
}

type statsCall struct {
	slug     string
	from, to time.Time
}

func (f *fakeShortLinker) CampaignStats(_ context.Context, slug string, from, to time.Time) (*shortlinks.CampaignRollup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statsCalls = append(f.statsCalls, statsCall{slug, from, to})
	if f.statsErr != nil {
		return nil, f.statsErr
	}
	if f.statsRollup == nil {
		return &shortlinks.CampaignRollup{}, nil
	}
	return f.statsRollup, nil
}

func (f *fakeShortLinker) Enabled() bool { return f.enabled }
func (f *fakeShortLinker) Me(context.Context) (*shortlinks.Me, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.meCalls++
	return &shortlinks.Me{BaseURL: "https://go.example.test/"}, nil
}
func (f *fakeShortLinker) EnsureCampaign(_ context.Context, slug string, req shortlinks.CreateCampaignRequest) (*shortlinks.Campaign, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slugs = append(f.slugs, slug)
	f.campaigns = append(f.campaigns, req)
	out := slug
	if f.campSlug != "" {
		out = f.campSlug
	}
	return &shortlinks.Campaign{Slug: out}, nil
}
func (f *fakeShortLinker) CreateLink(_ context.Context, req shortlinks.CreateLinkRequest) (*shortlinks.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.linkErr != nil {
		return nil, f.linkErr
	}
	f.links = append(f.links, req)
	return &shortlinks.Link{Key: fmt.Sprintf("k%d", len(f.links))}, nil
}

func outreachTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	truncateCredsTables(t, testDBPool)
	return testDBPool
}

func outreachMux(pool *pgxpool.Pool, links shortLinker) (http.Handler, *workshops.Store) {
	mux, ws, _ := outreachMuxWithHandler(pool, links)
	return mux, ws
}

func outreachMuxWithHandler(pool *pgxpool.Pool, links shortLinker) (http.Handler, *workshops.Store, *AdminOutreachHandler) {
	ws := workshops.NewStore(pool)
	h := NewAdminOutreachHandler(outreach.NewStore(pool), outreach.NewStore(pool), ws, links, audit.New(pool), "https://site.example.test")
	requireSession := middleware.RequireSession(auth.NewStore(pool))
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/outreach", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("GET /admin/workshops/{id}/outreach", requireAdmin(http.HandlerFunc(h.Get)))
	mux.Handle("POST /admin/workshops/{id}/outreach", requireAdmin(http.HandlerFunc(h.Start)))
	mux.Handle("POST /admin/outreach/{id}/posts", requireAdmin(http.HandlerFunc(h.CreatePost)))
	mux.Handle("PATCH /admin/outreach-posts/{id}", requireAdmin(http.HandlerFunc(h.PatchPost)))
	mux.Handle("POST /admin/outreach-posts/{id}/prepare", requireAdmin(http.HandlerFunc(h.Prepare)))
	mux.Handle("POST /admin/outreach-posts/{id}/posted", requireAdmin(http.HandlerFunc(h.MarkPosted)))
	mux.Handle("DELETE /admin/outreach-posts/{id}", requireAdmin(http.HandlerFunc(h.DeletePost)))
	mux.Handle("GET /admin/workshops/{id}/outreach/results", requireAdmin(http.HandlerFunc(h.ResultsForWorkshop)))
	mux.Handle("GET /admin/outreach-results", requireAdmin(http.HandlerFunc(h.ResultsSummary)))
	mux.Handle("GET /admin/outreach-locations", requireAdmin(http.HandlerFunc(h.ListLocations)))
	mux.Handle("POST /admin/outreach-locations", requireAdmin(http.HandlerFunc(h.CreateLocation)))
	mux.Handle("PATCH /admin/outreach-locations/{id}", requireAdmin(http.HandlerFunc(h.PatchLocation)))
	mux.Handle("DELETE /admin/outreach-locations/{id}", requireAdmin(http.HandlerFunc(h.DeleteLocation)))
	return mux, ws, h
}

func seedOutreachWorkshop(t *testing.T, pool *pgxpool.Pool, ws *workshops.Store, status string) workshops.Workshop {
	t.Helper()
	start := time.Date(2026, 12, 5, 18, 0, 0, 0, time.UTC)
	place := "The Shop"
	w, err := ws.Create(context.Background(), workshops.CreateInput{
		Title: fmt.Sprintf("zz outreach %d", testdb.Unique()), StartsAt: &start, LocationName: &place,
	})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	t.Cleanup(func() { _ = ws.Delete(context.Background(), w.ID) })
	if status != "draft" {
		w, err = ws.Update(context.Background(), w.ID, workshops.UpdateInput{
			Title: w.Title, StartsAt: w.StartsAt, LocationName: w.LocationName, Status: status,
		})
		if err != nil {
			t.Fatalf("set status: %v", err)
		}
	}
	return w
}

type outreachEnv struct {
	srv   *httptest.Server
	pool  *pgxpool.Pool
	ws    *workshops.Store
	links *fakeShortLinker
}

func newOutreachEnv(t *testing.T, enabled bool) *outreachEnv {
	t.Helper()
	pool := outreachTestPool(t)
	links := &fakeShortLinker{enabled: enabled}
	mux, ws := outreachMux(pool, links)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	admin := seedAdmin(t, pool, "admin-outreach@example.com")
	seedSession(t, pool, admin, "outreach-admin-token")
	user := seedUser(t, pool, "user-outreach@example.com")
	seedSession(t, pool, user, "outreach-user-token")
	return &outreachEnv{srv: srv, pool: pool, ws: ws, links: links}
}

func (e *outreachEnv) do(t *testing.T, method, path, body string) (int, []byte) {
	t.Helper()
	resp := doJSON(t, e.srv.Client(), method, e.srv.URL+path, "outreach-admin-token", body)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func decodeOutreach[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode: %v (%s)", err, b)
	}
	return v
}

type testPanel struct {
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason"`
	Outreach          *struct {
		ID           int64  `json:"id"`
		CampaignSlug string `json:"campaign_slug"`
	} `json:"outreach"`
	Channels []map[string]any `json:"channels"`
	Posts    []testPost       `json:"posts"`
}

type testPost struct {
	ID             int64   `json:"id"`
	Channel        string  `json:"channel"`
	Wave           string  `json:"wave"`
	Body           string  `json:"body"`
	Status         string  `json:"status"`
	Prepared       bool    `json:"prepared"`
	Stale          bool    `json:"stale"`
	ShortURL       *string `json:"short_url"`
	ShortLinkKey   *string `json:"short_link_key"`
	DestinationURL string  `json:"destination_url"`
}

func (e *outreachEnv) start(t *testing.T, w workshops.Workshop) testPanel {
	t.Helper()
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("start status = %d (%s)", code, b)
	}
	return decodeOutreach[testPanel](t, b)
}

func (e *outreachEnv) createPost(t *testing.T, outreachID int64, channel, wave string) testPost {
	t.Helper()
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach/%d/posts", outreachID),
		fmt.Sprintf(`{"channel":%q,"wave":%q}`, channel, wave))
	if code != http.StatusCreated {
		t.Fatalf("create post status = %d (%s)", code, b)
	}
	return decodeOutreach[testPost](t, b)
}

func outreachAuditCount(t *testing.T, pool *pgxpool.Pool, action string, targetID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`, action, targetID).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

func TestAdminOutreach_AuthGuards(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	paths := []struct{ method, path string }{
		{http.MethodGet, "/admin/outreach"},
		{http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID)},
		{http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID)},
		{http.MethodPost, "/admin/outreach/1/posts"},
		{http.MethodPatch, "/admin/outreach-posts/1"},
		{http.MethodPost, "/admin/outreach-posts/1/prepare"},
		{http.MethodPost, "/admin/outreach-posts/1/posted"},
		{http.MethodDelete, "/admin/outreach-posts/1"},
	}
	for _, p := range paths {
		for token, want := range map[string]int{"": http.StatusUnauthorized, "outreach-user-token": http.StatusForbidden} {
			resp := doJSON(t, e.srv.Client(), p.method, e.srv.URL+p.path, token, "")
			resp.Body.Close()
			if resp.StatusCode != want {
				t.Errorf("%s %s token=%q status = %d, want %d", p.method, p.path, token, resp.StatusCode, want)
			}
		}
	}
	if len(e.links.slugs) != 0 {
		t.Error("an unauthorized request reached ShortLinks")
	}
}

func TestAdminOutreach_GetWhenNotConfigured(t *testing.T) {
	e := newOutreachEnv(t, false)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	p := decodeOutreach[testPanel](t, b)
	if p.Available || p.UnavailableReason != "shortlinks_not_configured" || p.Outreach != nil || len(p.Channels) != 5 {
		t.Errorf("panel = %+v", p)
	}
	if code, _ := e.do(t, http.MethodGet, "/admin/workshops/999999999/outreach", ""); code != http.StatusNotFound {
		t.Errorf("unknown workshop status = %d, want 404", code)
	}
}

func TestAdminOutreach_StartRules(t *testing.T) {
	e := newOutreachEnv(t, false)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), ""); code != http.StatusServiceUnavailable {
		t.Errorf("disabled start = %d, want 503", code)
	}

	e = newOutreachEnv(t, true)
	for _, status := range []string{"draft", "canceled"} {
		ws := seedOutreachWorkshop(t, e.pool, e.ws, status)
		if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach", ws.ID), ""); code != http.StatusConflict {
			t.Errorf("%s start = %d, want 409", status, code)
		}
	}
	if len(e.links.slugs) != 0 {
		t.Error("EnsureCampaign called for an ineligible workshop")
	}

	e.links.campSlug = "returned-slug"
	pub := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach", pub.ID), "")
	if code != http.StatusCreated {
		t.Fatalf("start = %d (%s)", code, b)
	}
	p := decodeOutreach[testPanel](t, b)
	if p.Outreach == nil || p.Outreach.CampaignSlug != "returned-slug" || !p.Available {
		t.Errorf("panel = %+v", p)
	}
	if len(e.links.slugs) != 1 || e.links.slugs[0] != pub.Slug || e.links.campaigns[0].Description != pub.Title ||
		e.links.campaigns[0].DefaultUTMCampaign != pub.Slug || e.links.campaigns[0].StartsAt == "" {
		t.Errorf("EnsureCampaign args: slugs=%v reqs=%+v", e.links.slugs, e.links.campaigns)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach", pub.ID), ""); code != http.StatusOK {
		t.Errorf("second start = %d, want 200", code)
	}
	if len(e.links.slugs) != 1 {
		t.Errorf("second start called EnsureCampaign again (%d calls)", len(e.links.slugs))
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachStarted, p.Outreach.ID); n != 1 {
		t.Errorf("outreach.started audit rows = %d, want 1", n)
	}
}

func TestAdminOutreach_CreatePrepareAndPostFlow(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach

	bsky := e.createPost(t, o.ID, "bluesky", "announce")
	if !strings.Contains(bsky.Body, w.Title) || !strings.Contains(bsky.Body, outreach.LinkToken) || bsky.Stale || bsky.Prepared {
		t.Errorf("generated draft = %+v", bsky)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach/%d/posts", o.ID), `{"channel":"bluesky","wave":"announce"}`); code != http.StatusConflict {
		t.Errorf("duplicate slot = %d, want 409", code)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach/%d/posts", o.ID), `{"channel":"myspace","wave":"announce"}`); code != http.StatusBadRequest {
		t.Errorf("unknown channel = %d, want 400", code)
	}
	ig := e.createPost(t, o.ID, "instagram", "reminder")

	// Prepare the manual channel.
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", ig.ID), "")
	if code != http.StatusOK {
		t.Fatalf("prepare = %d (%s)", code, b)
	}
	got := decodeOutreach[testPost](t, b)
	if got.Status != "manual" || !got.Prepared || got.ShortURL == nil || *got.ShortURL != "https://go.example.test/k1" ||
		strings.Contains(got.Body, outreach.LinkToken) || !strings.Contains(got.Body, "https://go.example.test/k1") {
		t.Errorf("prepared instagram = %+v", got)
	}
	if len(e.links.links) != 1 {
		t.Fatalf("CreateLink calls = %d", len(e.links.links))
	}
	l := e.links.links[0]
	wantDest := fmt.Sprintf("https://site.example.test/workshops/%s?utm_campaign=%s&utm_content=reminder&utm_medium=social&utm_source=instagram", w.Slug, w.Slug)
	if l.DestinationURL != wantDest || l.Key != "" || l.CampaignSlug != o.CampaignSlug || l.UTMSource != "instagram" ||
		l.UTMMedium != "social" || l.UTMCampaign != w.Slug || l.UTMContent != "reminder" || l.Placement != "instagram" {
		t.Errorf("CreateLink request = %+v", l)
	}
	// Second prepare makes no call and writes no audit row.
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", ig.ID), ""); code != http.StatusOK {
		t.Errorf("second prepare = %d", code)
	}
	if len(e.links.links) != 1 || e.links.meCalls != 1 {
		t.Errorf("second prepare called ShortLinks: links=%d me=%d", len(e.links.links), e.links.meCalls)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostPrepared, ig.ID); n != 1 {
		t.Errorf("prepared audit rows = %d, want 1", n)
	}

	// Automatic channel stays draft after prepare.
	code, b = e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", bsky.ID), "")
	if code != http.StatusOK || decodeOutreach[testPost](t, b).Status != "draft" {
		t.Errorf("bluesky prepare = %d %s", code, b)
	}

	// Mark posted: unprepared 409, bad URL 400, then success, then locks.
	tt := e.createPost(t, o.ID, "tiktok", "announce")
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/posted", tt.ID), ""); code != http.StatusConflict {
		t.Errorf("unprepared posted = %d, want 409", code)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/posted", ig.ID), `{"remote_url":"javascript:alert(1)"}`); code != http.StatusBadRequest {
		t.Errorf("bad remote_url = %d, want 400", code)
	}
	code, b = e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/posted", ig.ID), `{"remote_url":"https://instagram.com/p/abc"}`)
	if code != http.StatusOK || decodeOutreach[testPost](t, b).Status != "posted" {
		t.Fatalf("posted = %d %s", code, b)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/posted", ig.ID), ""); code != http.StatusConflict {
		t.Errorf("second posted = %d, want 409", code)
	}
	if code, _ := e.do(t, http.MethodPatch, fmt.Sprintf("/admin/outreach-posts/%d", ig.ID), `{"body":"x"}`); code != http.StatusConflict {
		t.Errorf("edit posted = %d, want 409", code)
	}
	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/admin/outreach-posts/%d", ig.ID), ""); code != http.StatusConflict {
		t.Errorf("delete posted = %d, want 409", code)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostPosted, ig.ID); n != 1 {
		t.Errorf("posted audit rows = %d, want 1", n)
	}

	// Delete a draft.
	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/admin/outreach-posts/%d", tt.ID), ""); code != http.StatusNoContent {
		t.Errorf("delete draft = %d, want 204", code)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostDeleted, tt.ID); n != 1 {
		t.Errorf("deleted audit rows = %d, want 1", n)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostCreated, bsky.ID); n != 1 {
		t.Errorf("created audit rows = %d, want 1", n)
	}
}

func TestAdminOutreach_PrepareUpstreamErrorLeaksNothing(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	p := e.createPost(t, o.ID, "bluesky", "announce")
	e.links.linkErr = errors.New("shortlinks: HTTP 500: secret-upstream-detail")
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", p.ID), "")
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", code)
	}
	if strings.Contains(string(b), "secret-upstream-detail") {
		t.Errorf("upstream text leaked: %s", b)
	}
	e.links.linkErr = fmt.Errorf("wrapped: %w", shortlinks.ErrRateLimited)
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", p.ID), ""); code != http.StatusTooManyRequests {
		t.Errorf("rate limited = %d, want 429", code)
	}
}

func TestAdminOutreach_PrepareRefusedForUnpublishedWorkshop(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	p := e.createPost(t, o.ID, "bluesky", "announce")
	if _, err := e.ws.Update(context.Background(), w.ID, workshops.UpdateInput{Title: w.Title, StartsAt: w.StartsAt, LocationName: w.LocationName, Status: "draft"}); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", p.ID), ""); code != http.StatusConflict {
		t.Errorf("prepare = %d, want 409", code)
	}
	if len(e.links.links) != 0 {
		t.Error("CreateLink called for an unpublished workshop")
	}
}

func TestAdminOutreach_RegenerateClearsStale(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	p := e.createPost(t, o.ID, "bluesky", "announce")
	e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", p.ID), "")

	later := w.StartsAt.Add(48 * time.Hour)
	if _, err := e.ws.Update(context.Background(), w.ID, workshops.UpdateInput{Title: w.Title, StartsAt: &later, LocationName: w.LocationName, Status: "published"}); err != nil {
		t.Fatal(err)
	}
	code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
	panel := decodeOutreach[testPanel](t, b)
	if code != http.StatusOK || len(panel.Posts) != 1 || !panel.Posts[0].Stale {
		t.Fatalf("after date change: %d %+v", code, panel.Posts)
	}
	if code, _ := e.do(t, http.MethodPatch, fmt.Sprintf("/admin/outreach-posts/%d", p.ID), `{}`); code != http.StatusBadRequest {
		t.Errorf("empty patch = %d, want 400", code)
	}
	if code, _ := e.do(t, http.MethodPatch, fmt.Sprintf("/admin/outreach-posts/%d", p.ID), `{"body":"x","regenerate":true}`); code != http.StatusBadRequest {
		t.Errorf("both = %d, want 400", code)
	}
	code, b = e.do(t, http.MethodPatch, fmt.Sprintf("/admin/outreach-posts/%d", p.ID), `{"regenerate":true}`)
	if code != http.StatusOK {
		t.Fatalf("regenerate = %d (%s)", code, b)
	}
	got := decodeOutreach[testPost](t, b)
	if got.Stale || strings.Contains(got.Body, outreach.LinkToken) || !strings.Contains(got.Body, *got.ShortURL) || !strings.Contains(got.Body, "Mon, Dec 7") {
		t.Errorf("regenerated = %+v", got)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostUpdated, p.ID); n != 1 {
		t.Errorf("updated audit rows = %d, want 1", n)
	}
}

func TestAdminOutreach_ListSummaries(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	e.createPost(t, o.ID, "bluesky", "announce")
	code, b := e.do(t, http.MethodGet, "/admin/outreach", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var resp struct {
		Outreach []struct {
			WorkshopID int64          `json:"workshop_id"`
			Counts     map[string]int `json:"counts"`
		} `json:"outreach"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	for _, s := range resp.Outreach {
		if s.WorkshopID == w.ID {
			if s.Counts["draft"] != 1 {
				t.Errorf("counts = %v", s.Counts)
			}
			return
		}
	}
	t.Error("workshop missing from list")
}
