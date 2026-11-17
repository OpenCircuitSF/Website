package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/guideposts"
	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// guidePostsTestPool is outreachTestPool plus an empty guide_posts table.
func guidePostsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := outreachTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE guide_posts, guide_post_engagement RESTART IDENTITY`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM outbound_queue WHERE kind = 'guide_post'`); err != nil {
		t.Fatal(err)
	}
	return pool
}

type fakeGuideSignups struct {
	rows []subscribers.GuideSignups
	err  error
}

func (f *fakeGuideSignups) SignupsByGuide(context.Context, time.Time, time.Time) ([]subscribers.GuideSignups, error) {
	return f.rows, f.err
}

type guidePostsEnv struct {
	srv   *httptest.Server
	pool  *pgxpool.Pool
	links *fakeShortLinker
	h     *AdminGuidePostsHandler
	slug  string
}

func newGuidePostsEnv(t *testing.T, enabled bool, channels fakePosting) *guidePostsEnv {
	t.Helper()
	pool := guidePostsTestPool(t)
	links := &fakeShortLinker{enabled: enabled}
	store := guideposts.NewStore(pool, outbox.NewStore(pool))
	h := NewAdminGuidePostsHandler(store, guideposts.NewLinker(store, links), links, audit.New(pool), "https://site.example.test", channels)
	requireSession := middleware.RequireSession(auth.NewStore(pool))
	requireAdmin := func(next http.Handler) http.Handler { return requireSession(middleware.RequireAdmin(next)) }
	mux := http.NewServeMux()
	mux.Handle("GET /admin/guide-posts", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("POST /admin/guide-posts", requireAdmin(http.HandlerFunc(h.Create)))
	mux.Handle("PATCH /admin/guide-posts/{id}", requireAdmin(http.HandlerFunc(h.Patch)))
	mux.Handle("DELETE /admin/guide-posts/{id}", requireAdmin(http.HandlerFunc(h.Delete)))
	mux.Handle("POST /admin/guide-posts/{id}/schedule", requireAdmin(http.HandlerFunc(h.Schedule)))
	mux.Handle("POST /admin/guide-posts/{id}/unschedule", requireAdmin(http.HandlerFunc(h.Unschedule)))
	mux.Handle("POST /admin/guide-posts/{id}/send", requireAdmin(http.HandlerFunc(h.Send)))
	mux.Handle("POST /admin/guide-posts/{id}/again", requireAdmin(http.HandlerFunc(h.Again)))
	mux.Handle("GET /admin/guide-posts-overview", requireAdmin(http.HandlerFunc(h.Overview)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	admin := seedAdmin(t, pool, "admin-guideposts@example.com")
	seedSession(t, pool, admin, "guideposts-admin-token")
	user := seedUser(t, pool, "user-guideposts@example.com")
	seedSession(t, pool, user, "guideposts-user-token")
	topics := guides.Topics()
	if len(topics) == 0 {
		t.Skip("no topic guides in the catalog")
	}
	return &guidePostsEnv{srv: srv, pool: pool, links: links, h: h, slug: topics[0].Slug}
}

func (e *guidePostsEnv) doAs(t *testing.T, token, method, path, body string) (int, []byte) {
	t.Helper()
	resp := doJSON(t, e.srv.Client(), method, e.srv.URL+path, token, body)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *guidePostsEnv) do(t *testing.T, method, path, body string) (int, []byte) {
	t.Helper()
	return e.doAs(t, "guideposts-admin-token", method, path, body)
}

type testGuidePostLink struct {
	Post      guideposts.Post `json:"post"`
	LinkError *string         `json:"link_error"`
}

func (e *guidePostsEnv) create(t *testing.T, channel, campaign string) testGuidePostLink {
	t.Helper()
	code, b := e.do(t, http.MethodPost, "/admin/guide-posts", fmt.Sprintf(`{"slug":%q,"channel":%q,"campaign":%q}`, e.slug, channel, campaign))
	if code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", code, b)
	}
	return decodeOutreach[testGuidePostLink](t, b)
}

func (e *guidePostsEnv) markPosted(t *testing.T, id int64) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE guide_posts SET status = 'posted', posted_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

type testGuidePostsList struct {
	Topics []struct {
		Slug        string `json:"slug"`
		DefaultBody string `json:"default_body"`
		URL         string `json:"url"`
	} `json:"topics"`
	Posts []struct {
		guideposts.Post
		Clicks  *int64             `json:"clicks"`
		Signups *guideSignupCounts `json:"signups"`
	} `json:"posts"`
	Channels []struct {
		Name       string `json:"name"`
		CharLimit  int    `json:"char_limit"`
		Configured bool   `json:"configured"`
	} `json:"channels"`
	PostHour          int     `json:"post_hour"`
	SuggestedCampaign string  `json:"suggested_campaign"`
	ClicksError       *string `json:"clicks_error"`
	SignupsError      *string `json:"signups_error"`
}

type testGuidePostsOverview struct {
	Guides []struct {
		guideposts.GuideShare
		Signups *guideSignupCounts `json:"signups"`
	} `json:"guides"`
	ClicksError  *string `json:"clicks_error"`
	SignupsError *string `json:"signups_error"`
}

func (e *guidePostsEnv) list(t *testing.T) testGuidePostsList {
	t.Helper()
	code, b := e.do(t, http.MethodGet, "/admin/guide-posts", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d (%s)", code, b)
	}
	return decodeOutreach[testGuidePostsList](t, b)
}

func (e *guidePostsEnv) overview(t *testing.T) testGuidePostsOverview {
	t.Helper()
	code, b := e.do(t, http.MethodGet, "/admin/guide-posts-overview", "")
	if code != http.StatusOK {
		t.Fatalf("overview = %d (%s)", code, b)
	}
	return decodeOutreach[testGuidePostsOverview](t, b)
}

func (e *guidePostsEnv) auditCount(t *testing.T, action string, id int64) int {
	t.Helper()
	return outreachAuditCount(t, e.pool, action, id)
}

func TestAdminGuidePosts_ListIncludesTopicsChannelsAndSuggestedCampaign(t *testing.T) {
	e := newGuidePostsEnv(t, true, fakePosting{"bluesky": true})
	l := e.list(t)
	if len(l.Topics) != len(guides.Topics()) || l.Topics[0].Slug != e.slug || l.Topics[0].DefaultBody == "" ||
		l.Topics[0].URL != "https://site.example.test/guides/"+e.slug {
		t.Errorf("topics = %+v", l.Topics)
	}
	if len(l.Channels) != 2 || l.Channels[0].Name != "bluesky" || !l.Channels[0].Configured || l.Channels[1].Configured || l.Channels[1].CharLimit == 0 {
		t.Errorf("channels = %+v", l.Channels)
	}
	if l.SuggestedCampaign != guideposts.SuggestedCampaign(time.Now()) || l.PostHour == 0 || len(l.Posts) != 0 {
		t.Errorf("list = %+v", l)
	}
	if l.ClicksError != nil || l.SignupsError != nil {
		t.Errorf("errors = %v %v", l.ClicksError, l.SignupsError)
	}
}

func TestAdminGuidePosts_CreateMintsLinkAndAudits(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	r := e.create(t, "bluesky", "")
	if r.LinkError != nil || r.Post.ShortURL == nil || r.Post.Status != "draft" {
		t.Fatalf("create = %+v", r)
	}
	if e.auditCount(t, audit.ActionGuidePostCreated, r.Post.ID) != 1 || e.auditCount(t, audit.ActionGuidePostLinkCreated, r.Post.ID) != 1 {
		t.Error("want one created and one link_created audit row")
	}
	var bodyKeys int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE target_type = 'guide_post' AND target_id = $1 AND metadata ? 'body'`, r.Post.ID).Scan(&bodyKeys); err != nil || bodyKeys != 0 {
		t.Errorf("audit rows carrying body = %d (%v)", bodyKeys, err)
	}
}

func TestAdminGuidePosts_CreateUsesRequestedCampaign(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	r := e.create(t, "mastodon", "guides-2026-11")
	if r.Post.CampaignSlug != "guides-2026-11" || len(e.links.slugs) != 1 || e.links.slugs[0] != "guides-2026-11" {
		t.Errorf("campaign = %q, EnsureCampaign saw %v", r.Post.CampaignSlug, e.links.slugs)
	}
}

func TestAdminGuidePosts_CreateKeepsDraftWhenShortLinksFails(t *testing.T) {
	e := newGuidePostsEnv(t, false, nil)
	r := e.create(t, "bluesky", "")
	if r.LinkError == nil || *r.LinkError != "not_configured" || r.Post.ShortURL != nil || r.Post.ID == 0 {
		t.Fatalf("create = %+v", r)
	}
	if e.auditCount(t, audit.ActionGuidePostLinkCreated, r.Post.ID) != 0 {
		t.Error("link_created audited without a link")
	}
	if l := e.list(t); len(l.Posts) != 1 {
		t.Errorf("draft not kept: %d posts", len(l.Posts))
	}
}

func TestAdminGuidePosts_CreateRejectsUnknownSlugAndBadCampaign(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	if code, b := e.do(t, http.MethodPost, "/admin/guide-posts", `{"slug":"no-such-guide","channel":"bluesky"}`); code != http.StatusBadRequest {
		t.Errorf("unknown slug = %d (%s)", code, b)
	}
	if code, b := e.do(t, http.MethodPost, "/admin/guide-posts", fmt.Sprintf(`{"slug":%q,"channel":"bluesky","campaign":"Bad Slug"}`, e.slug)); code != http.StatusUnprocessableEntity {
		t.Errorf("bad campaign = %d (%s)", code, b)
	}
}

func TestAdminGuidePosts_PatchRefusesRemovingLinkIs422(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	r := e.create(t, "bluesky", "")
	code, b := e.do(t, http.MethodPatch, fmt.Sprintf("/admin/guide-posts/%d", r.Post.ID), `{"body":"no link"}`)
	if code != http.StatusUnprocessableEntity {
		t.Errorf("patch = %d (%s)", code, b)
	}
	code, _ = e.do(t, http.MethodPatch, fmt.Sprintf("/admin/guide-posts/%d", r.Post.ID), fmt.Sprintf(`{"body":"new %s"}`, *r.Post.ShortURL))
	if code != http.StatusOK || e.auditCount(t, audit.ActionGuidePostUpdated, r.Post.ID) != 1 {
		t.Errorf("keeping the link = %d", code)
	}
}

func TestAdminGuidePosts_ScheduleInPastIs422(t *testing.T) {
	e := newGuidePostsEnv(t, true, fakePosting{"bluesky": true})
	r := e.create(t, "bluesky", "")
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/guide-posts/%d/schedule", r.Post.ID), fmt.Sprintf(`{"scheduled_at":%q}`, past)); code != http.StatusUnprocessableEntity {
		t.Errorf("schedule in past = %d (%s)", code, b)
	}
}

func TestAdminGuidePosts_ScheduleRetriesMissingLink(t *testing.T) {
	e := newGuidePostsEnv(t, false, fakePosting{"bluesky": true})
	r := e.create(t, "bluesky", "")
	if r.Post.ShortURL != nil {
		t.Fatal("expected an unlinked draft")
	}
	e.links.enabled = true
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/guide-posts/%d/schedule", r.Post.ID), fmt.Sprintf(`{"scheduled_at":%q}`, at))
	if code != http.StatusOK {
		t.Fatalf("schedule = %d (%s)", code, b)
	}
	p := decodeOutreach[guideposts.Post](t, b)
	if p.Status != "scheduled" || p.ShortURL == nil {
		t.Errorf("post = %+v", p)
	}
	if e.auditCount(t, audit.ActionGuidePostLinkCreated, p.ID) != 1 || e.auditCount(t, audit.ActionGuidePostScheduled, p.ID) != 1 {
		t.Error("want link_created and scheduled audits")
	}
}

func TestAdminGuidePosts_ScheduleStillFailingShortLinksIs503(t *testing.T) {
	e := newGuidePostsEnv(t, false, fakePosting{"bluesky": true})
	r := e.create(t, "bluesky", "")
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/guide-posts/%d/schedule", r.Post.ID), fmt.Sprintf(`{"scheduled_at":%q}`, at)); code != http.StatusServiceUnavailable {
		t.Errorf("schedule = %d (%s)", code, b)
	}
}

func TestAdminGuidePosts_SendUnconfiguredChannelIs409(t *testing.T) {
	e := newGuidePostsEnv(t, true, fakePosting{"bluesky": true})
	r := e.create(t, "mastodon", "")
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/guide-posts/%d/send", r.Post.ID), ""); code != http.StatusConflict {
		t.Errorf("send = %d (%s)", code, b)
	}
}

func TestAdminGuidePosts_SendQueuesOneGuidePostRow(t *testing.T) {
	e := newGuidePostsEnv(t, true, fakePosting{"bluesky": true})
	r := e.create(t, "bluesky", "")
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/guide-posts/%d/send", r.Post.ID), "")
	if code != http.StatusAccepted {
		t.Fatalf("send = %d (%s)", code, b)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outbound_queue WHERE kind = 'guide_post' AND recipient = $1`, guideposts.Recipient(r.Post.ID)).Scan(&n); err != nil || n != 1 {
		t.Errorf("queue rows = %d (%v)", n, err)
	}
	if e.auditCount(t, audit.ActionGuidePostQueued, r.Post.ID) != 1 {
		t.Error("want one queued audit")
	}
}

func TestAdminGuidePosts_UnscheduleThenDelete(t *testing.T) {
	e := newGuidePostsEnv(t, true, fakePosting{"bluesky": true})
	r := e.create(t, "bluesky", "")
	at := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	path := fmt.Sprintf("/admin/guide-posts/%d", r.Post.ID)
	if code, b := e.do(t, http.MethodPost, path+"/schedule", fmt.Sprintf(`{"scheduled_at":%q}`, at)); code != http.StatusOK {
		t.Fatalf("schedule = %d (%s)", code, b)
	}
	if code, b := e.do(t, http.MethodPost, path+"/unschedule", ""); code != http.StatusOK {
		t.Fatalf("unschedule = %d (%s)", code, b)
	}
	if code, b := e.do(t, http.MethodDelete, path, ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d (%s)", code, b)
	}
	if e.auditCount(t, audit.ActionGuidePostUnscheduled, r.Post.ID) != 1 || e.auditCount(t, audit.ActionGuidePostDeleted, r.Post.ID) != 1 {
		t.Error("want unscheduled and deleted audits")
	}
	if code, _ := e.do(t, http.MethodDelete, path, ""); code != http.StatusNotFound {
		t.Errorf("second delete = %d", code)
	}
	p := e.create(t, "bluesky", "")
	e.markPosted(t, p.Post.ID)
	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/admin/guide-posts/%d", p.Post.ID), ""); code != http.StatusConflict {
		t.Errorf("delete posted = %d", code)
	}
}

func TestAdminGuidePosts_AgainCopiesPostedIntoNewCampaign(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	src := e.create(t, "bluesky", "guides-2026-10")
	e.markPosted(t, src.Post.ID)
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/guide-posts/%d/again", src.Post.ID), `{"campaign":"guides-2026-11"}`)
	if code != http.StatusCreated {
		t.Fatalf("again = %d (%s)", code, b)
	}
	r := decodeOutreach[testGuidePostLink](t, b)
	if r.Post.ID == src.Post.ID || r.Post.Status != "draft" || r.Post.CampaignSlug != "guides-2026-11" ||
		r.Post.ShortURL == nil || *r.Post.ShortURL == *src.Post.ShortURL || r.LinkError != nil {
		t.Errorf("copy = %+v", r)
	}
	var from int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT (metadata->>'copied_from')::bigint FROM audit_log WHERE action = $1 AND target_id = $2`, audit.ActionGuidePostCreated, r.Post.ID).Scan(&from); err != nil || from != src.Post.ID {
		t.Errorf("copied_from = %d (%v)", from, err)
	}
}

func TestAdminGuidePosts_AgainRefusesDraftIs409(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	r := e.create(t, "bluesky", "")
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/guide-posts/%d/again", r.Post.ID), ""); code != http.StatusConflict {
		t.Errorf("again on draft = %d (%s)", code, b)
	}
}

func TestAdminGuidePosts_OverviewOneRowPerGuide(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	r := e.create(t, "bluesky", "guides-2026-11")
	e.markPosted(t, r.Post.ID)
	o := e.overview(t)
	topics := guides.Topics()
	if len(o.Guides) != len(topics) {
		t.Fatalf("rows = %d, want %d", len(o.Guides), len(topics))
	}
	for i, g := range o.Guides {
		if g.Slug != topics[i].Slug {
			t.Errorf("row %d = %q, want %q", i, g.Slug, topics[i].Slug)
		}
		if g.Slug == e.slug {
			if g.LastPostedAt == nil || g.LastCampaign != "guides-2026-11" || len(g.Channels) != 1 {
				t.Errorf("posted row = %+v", g)
			}
		} else if g.LastPostedAt != nil || g.Channels == nil || len(g.Channels) != 0 {
			t.Errorf("never-posted row = %+v", g)
		}
	}
}

func rollup(key string, n int64) *shortlinks.CampaignRollup {
	return &shortlinks.CampaignRollup{ByLink: []shortlinks.LinkBucket{{Key: key, Count: n}}}
}

func TestAdminGuidePosts_ClicksAcrossCampaignsUseStatsCache(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	a := e.create(t, "bluesky", "c-one")
	b := e.create(t, "mastodon", "c-two")
	e.links.statsByCampaign = map[string]*shortlinks.CampaignRollup{
		"c-one": rollup(*a.Post.ShortLinkKey, 5), "c-two": rollup(*b.Post.ShortLinkKey, 7),
	}
	l := e.list(t)
	got := map[int64]int64{}
	for _, p := range l.Posts {
		if p.Clicks == nil {
			t.Fatalf("post %d clicks null", p.ID)
		}
		got[p.ID] = *p.Clicks
	}
	if got[a.Post.ID] != 5 || got[b.Post.ID] != 7 || l.ClicksError != nil {
		t.Errorf("clicks = %v, err %v", got, l.ClicksError)
	}
	calls := len(e.links.statsCalls)
	o := e.overview(t)
	for _, g := range o.Guides {
		if g.Slug == e.slug && (g.Clicks == nil || *g.Clicks != 12) {
			t.Errorf("guide clicks = %v, want 12", g.Clicks)
		}
	}
	if len(e.links.statsCalls) != calls {
		t.Errorf("second read made %d new CampaignStats calls", len(e.links.statsCalls)-calls)
	}
}

func TestAdminGuidePosts_ClicksFailureIsNotFatal(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	a := e.create(t, "bluesky", "c-one")
	b := e.create(t, "mastodon", "c-two")
	e.links.statsByCampaign = map[string]*shortlinks.CampaignRollup{"c-one": rollup(*a.Post.ShortLinkKey, 5)}
	e.links.statsErrFor = map[string]error{"c-two": errors.New("upstream detail")}
	l := e.list(t)
	for _, p := range l.Posts {
		switch p.ID {
		case a.Post.ID:
			if p.Clicks == nil || *p.Clicks != 5 {
				t.Errorf("c-one clicks = %v", p.Clicks)
			}
		case b.Post.ID:
			if p.Clicks != nil {
				t.Errorf("c-two clicks = %d, want null", *p.Clicks)
			}
		}
	}
	if l.ClicksError == nil || *l.ClicksError != "failed" {
		t.Errorf("clicks_error = %v", l.ClicksError)
	}
}

func TestAdminGuidePosts_ResetShortLinksDropsCachedStats(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	e.create(t, "bluesky", "c-one")
	e.list(t)
	n := len(e.links.statsCalls)
	e.list(t)
	if len(e.links.statsCalls) != n {
		t.Fatal("cache did not hold")
	}
	e.h.ResetShortLinks()
	e.list(t)
	if len(e.links.statsCalls) != n+1 {
		t.Errorf("after reset: %d calls, want %d", len(e.links.statsCalls), n+1)
	}
}

// TestAdminGuidePosts_RequiresAdmin checks this file's own mux; the real
// route table's guard is proven by cmd/opencircuit's wiring test.
func TestAdminGuidePosts_RequiresAdmin(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	if code, _ := e.doAs(t, "", http.MethodGet, "/admin/guide-posts", ""); code != http.StatusUnauthorized {
		t.Errorf("no session = %d", code)
	}
	if code, _ := e.doAs(t, "guideposts-user-token", http.MethodGet, "/admin/guide-posts-overview", ""); code != http.StatusForbidden {
		t.Errorf("non-admin = %d", code)
	}
}

func TestAdminGuidePosts_SignupsPerPostAndGuide(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	r := e.create(t, "bluesky", "")
	e.h.SetSignups(&fakeGuideSignups{rows: []subscribers.GuideSignups{
		{Guide: e.slug, UTMCampaign: "guides", UTMContent: guideposts.PostRef(r.Post.ID), Requested: 3, Confirmed: 2},
		{Guide: e.slug, Requested: 4, Confirmed: 1},
	}})
	l := e.list(t)
	if len(l.Posts) != 1 || l.Posts[0].Signups == nil || *l.Posts[0].Signups != (guideSignupCounts{Requested: 3, Confirmed: 2}) {
		t.Errorf("post signups = %+v", l.Posts)
	}
	o := e.overview(t)
	for _, g := range o.Guides {
		want := guideSignupCounts{}
		if g.Slug == e.slug {
			want = guideSignupCounts{Requested: 7, Confirmed: 3}
		}
		if g.Signups == nil || *g.Signups != want {
			t.Errorf("%s signups = %v, want %v", g.Slug, g.Signups, want)
		}
	}
}

func TestAdminGuidePosts_SignupsFailureIsNotFatal(t *testing.T) {
	e := newGuidePostsEnv(t, true, nil)
	e.create(t, "bluesky", "")
	e.h.SetSignups(&fakeGuideSignups{err: errors.New("db down")})
	l := e.list(t)
	if l.SignupsError == nil || *l.SignupsError != "failed" || l.Posts[0].Signups != nil {
		t.Errorf("list = %+v", l)
	}
	o := e.overview(t)
	if o.SignupsError == nil || o.Guides[0].Signups != nil {
		t.Errorf("overview signups_error = %v", o.SignupsError)
	}
}
