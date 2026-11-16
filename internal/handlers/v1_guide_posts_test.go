package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/guideposts"
	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// v1GuidePostsEnv is newGuidePostsEnv (the admin routes, a fake ShortLinks
// and real stores) plus the #0676 operations served over /api/v1 with real
// API keys.
type v1GuidePostsEnv struct {
	*guidePostsEnv
	reg      *api.Registry
	api      http.Handler
	write    string
	writeTok apitokens.Token
	read     string
}

func newV1GuidePostsEnv(t *testing.T, channels fakePosting) *v1GuidePostsEnv {
	t.Helper()
	e := newGuidePostsEnv(t, true, channels)
	reg := v1TestRegistry(e.pool, guideOperations("https://site.example.test", e.h)...)
	h := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})
	write, wt := v1TestKey(t, e.pool, apitokens.ScopeGuidePostsWrite)
	read, _ := v1TestKey(t, e.pool, apitokens.ScopeGuidePostsRead)
	return &v1GuidePostsEnv{guidePostsEnv: e, reg: reg, api: h, write: write, writeTok: wt, read: read}
}

func bothConnected() fakePosting { return fakePosting{"bluesky": true, "mastodon": true} }

func (e *v1GuidePostsEnv) call(t *testing.T, method, path, secret, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	return apitest.Do(t, e.api, method, path, secret, body, hdr...)
}

// createV1 POSTs a create for both channels with extra JSON fields and
// requires a 201.
func (e *v1GuidePostsEnv) createV1(t *testing.T, extra string) createGuidePostsOut {
	t.Helper()
	body := fmt.Sprintf(`{"slug":%q,"channels":["bluesky","mastodon"]%s}`, e.slug, extra)
	rec := e.call(t, http.MethodPost, "/api/v1/guide-posts", e.write, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	return v1Decode[createGuidePostsOut](t, rec)
}

func (e *v1GuidePostsEnv) postCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM guide_posts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *v1GuidePostsEnv) wantFieldError(t *testing.T, rec *httptest.ResponseRecorder, field string) api.ErrorDetail {
	t.Helper()
	d := apitest.ErrorOf(t, rec.Body.Bytes())
	if rec.Code != http.StatusBadRequest || d.Code != api.CodeValidation || d.Fields[field] == "" {
		t.Fatalf("got %d %+v, want a validation error naming %s", rec.Code, d, field)
	}
	return d
}

func inAnHour() string { return time.Now().Add(time.Hour).UTC().Format(time.RFC3339) }

func TestV1GuidePosts_CreateBothChannels(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	out := e.createV1(t, "")
	if len(out.Results) != 2 || out.Results[0].Channel != "bluesky" || out.Results[1].Channel != "mastodon" {
		t.Fatalf("results %+v", out.Results)
	}
	urls := map[string]bool{}
	for _, r := range out.Results {
		if r.Post == nil || r.Post.ShortURL == nil || r.LinkError != nil || r.Error != nil || r.Post.Status != "draft" {
			t.Fatalf("%s: %+v", r.Channel, r)
		}
		if strings.Contains(r.Post.Body, outreach.LinkToken) || !strings.Contains(r.Post.Body, *r.Post.ShortURL) {
			t.Errorf("%s body %q", r.Channel, r.Post.Body)
		}
		urls[*r.Post.ShortURL] = true
	}
	if len(urls) != 2 {
		t.Errorf("short URLs not distinct: %v", urls)
	}
	for _, action := range []string{audit.ActionGuidePostCreated, audit.ActionGuidePostLinkCreated} {
		rows := v1AuditRows(t, e.pool, action, e.writeTok.ID)
		if len(rows) != 2 {
			t.Fatalf("%s: %d rows attributed to the token", action, len(rows))
		}
		for _, r := range rows {
			if r.ActorID != nil || r.Metadata["actor"] != fmt.Sprintf("token:%d", e.writeTok.ID) || r.Metadata["api_token"] == nil {
				t.Errorf("%s row %+v", action, r)
			}
		}
	}
}

func TestV1GuidePosts_CreateOneChannelLinkFails(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	e.links.linkErrForSource = map[string]error{"mastodon": errors.New("boom")}
	out := e.createV1(t, "")
	b, m := out.Results[0], out.Results[1]
	if b.Post == nil || b.Post.ShortURL == nil || b.LinkError != nil {
		t.Errorf("bluesky %+v", b)
	}
	if m.Post == nil || m.Post.ShortURL != nil || m.LinkError == nil || *m.LinkError != "failed" || m.Post.Status != "draft" {
		t.Errorf("mastodon %+v", m)
	}
	if n := e.postCount(t); n != 2 {
		t.Errorf("%d rows, want 2", n)
	}
}

func TestV1GuidePosts_CreateAndSchedule(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	at := inAnHour()
	out := e.createV1(t, fmt.Sprintf(`,"scheduled_for":%q`, at))
	for _, r := range out.Results {
		if r.Post == nil || r.Post.Status != "scheduled" || r.Post.ScheduledAt == nil || r.Post.ScheduledAt.UTC().Format(time.RFC3339) != at || r.ScheduleError != nil {
			t.Fatalf("%s: %+v", r.Channel, r)
		}
	}

	e.links.linkErrForSource = map[string]error{"mastodon": errors.New("boom")}
	out = e.createV1(t, fmt.Sprintf(`,"scheduled_for":%q`, at))
	b, m := out.Results[0], out.Results[1]
	if b.Post.Status != "scheduled" {
		t.Errorf("bluesky %+v", b.Post)
	}
	if m.Post.Status != "draft" || m.ScheduleError == nil || m.LinkError == nil {
		t.Errorf("mastodon %+v", m)
	}
}

func TestV1GuidePosts_CreateOverLimitOnOneChannel(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	body := fmt.Sprintf(`{"slug":%q,"channels":["bluesky","mastodon"],"body":%q}`, e.slug, strings.Repeat("a", 320))
	d := e.wantFieldError(t, e.call(t, http.MethodPost, "/api/v1/guide-posts", e.write, body), "body.bluesky")
	if !strings.Contains(d.Fields["body.bluesky"], "300") {
		t.Errorf("message %q does not name the limit", d.Fields["body.bluesky"])
	}
	if _, ok := d.Fields["body.mastodon"]; ok {
		t.Errorf("mastodon (limit 500) flagged: %+v", d.Fields)
	}
	if n := e.postCount(t); n != 0 {
		t.Errorf("%d rows created, want 0", n)
	}
}

func TestV1GuidePosts_CreateUnconnectedChannel(t *testing.T) {
	e := newV1GuidePostsEnv(t, fakePosting{"bluesky": true})
	body := fmt.Sprintf(`{"slug":%q,"channels":["bluesky","mastodon"]}`, e.slug)
	d := e.wantFieldError(t, e.call(t, http.MethodPost, "/api/v1/guide-posts", e.write, body), "channels.mastodon")
	if d.Fields["channels.mastodon"] != "Mastodon is not connected" {
		t.Errorf("message %q", d.Fields["channels.mastodon"])
	}
	if n := e.postCount(t); n != 0 {
		t.Errorf("%d rows created, want 0", n)
	}
}

func TestV1GuidePosts_CreateValidation(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	ws := guides.WorkshopGuides()[0].Slug
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	cases := map[string]struct{ body, field string }{
		"workshop guide":     {fmt.Sprintf(`{"slug":%q,"channels":["bluesky"]}`, ws), "slug"},
		"no channels":        {fmt.Sprintf(`{"slug":%q,"channels":[]}`, e.slug), "channels"},
		"unknown channel":    {fmt.Sprintf(`{"slug":%q,"channels":["myspace"]}`, e.slug), "channels"},
		"repeated channel":   {fmt.Sprintf(`{"slug":%q,"channels":["bluesky","bluesky"]}`, e.slug), "channels"},
		"bad campaign":       {fmt.Sprintf(`{"slug":%q,"channels":["bluesky"],"campaign":"Bad Slug"}`, e.slug), "campaign"},
		"past scheduled":     {fmt.Sprintf(`{"slug":%q,"channels":["bluesky"],"scheduled_for":%q}`, e.slug, past), "scheduled_for"},
		"bad scheduled":      {fmt.Sprintf(`{"slug":%q,"channels":["bluesky"],"scheduled_for":"tomorrow"}`, e.slug), "scheduled_for"},
		"unknown body field": {fmt.Sprintf(`{"slug":%q,"channels":["bluesky"],"title":"x"}`, e.slug), "title"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e.wantFieldError(t, e.call(t, http.MethodPost, "/api/v1/guide-posts", e.write, c.body), c.field)
		})
	}
	if n := e.postCount(t); n != 0 {
		t.Errorf("%d rows created, want 0", n)
	}
}

func TestV1GuidePosts_IdempotentReplay(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	body := fmt.Sprintf(`{"slug":%q,"channels":["bluesky","mastodon"]}`, e.slug)
	first := e.call(t, http.MethodPost, "/api/v1/guide-posts", e.write, body, "Idempotency-Key", "create-0676-a")
	second := e.call(t, http.MethodPost, "/api/v1/guide-posts", e.write, body, "Idempotency-Key", "create-0676-a")
	if first.Code != http.StatusCreated || second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("first %d, second %d\n%s\n%s", first.Code, second.Code, first.Body, second.Body)
	}
	if second.Header().Get("Idempotent-Replayed") != "true" || first.Header().Get("Idempotent-Replayed") != "" {
		t.Errorf("Idempotent-Replayed first %q second %q", first.Header().Get("Idempotent-Replayed"), second.Header().Get("Idempotent-Replayed"))
	}
	if n := e.postCount(t); n != 2 {
		t.Errorf("%d rows, want 2", n)
	}
	other := fmt.Sprintf(`{"slug":%q,"channels":["bluesky"]}`, e.slug)
	if rec := e.call(t, http.MethodPost, "/api/v1/guide-posts", e.write, other, "Idempotency-Key", "create-0676-a"); rec.Code != http.StatusConflict {
		t.Errorf("reused key, different body = %d", rec.Code)
	}
}

func TestV1GuidePosts_Scopes(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	id := e.createV1(t, "").Results[0].Post.ID
	p := fmt.Sprintf("/api/v1/guide-posts/%d", id)
	writes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/guide-posts", fmt.Sprintf(`{"slug":%q,"channels":["bluesky"]}`, e.slug)},
		{http.MethodPatch, p, `{"body":"x {link}"}`},
		{http.MethodPost, p + "/schedule", fmt.Sprintf(`{"scheduled_at":%q}`, inAnHour())},
		{http.MethodPost, p + "/unschedule", ""},
		{http.MethodPost, p + "/send", ""},
		{http.MethodPost, p + "/again", ""},
		{http.MethodDelete, p, ""},
	}
	for _, w := range writes {
		rec := e.call(t, w.method, w.path, e.read, w.body)
		if d := apitest.ErrorOf(t, rec.Body.Bytes()); rec.Code != http.StatusForbidden || d.Code != api.CodeForbidden {
			t.Errorf("read key %s %s = %d %+v", w.method, w.path, rec.Code, d)
		}
	}
	for _, path := range []string{"/api/v1/guide-posts", "/api/v1/guide-posts/overview", "/api/v1/channels"} {
		if rec := e.call(t, http.MethodGet, path, e.read, ""); rec.Code != http.StatusOK {
			t.Errorf("read key GET %s = %d %s", path, rec.Code, rec.Body)
		}
		if rec := e.call(t, http.MethodGet, path, e.write, ""); rec.Code != http.StatusOK {
			t.Errorf("write key GET %s = %d", path, rec.Code)
		}
	}
	guidesKey, _ := v1TestKey(t, e.pool, apitokens.ScopeGuidesRead)
	if rec := e.call(t, http.MethodGet, "/api/v1/guide-posts", guidesKey, ""); rec.Code != http.StatusForbidden {
		t.Errorf("guides:read key on /guide-posts = %d", rec.Code)
	}
	if n := e.postCount(t); n != 2 {
		t.Errorf("forbidden writes changed rows: %d", n)
	}
}

func TestV1GuidePosts_UpdateBodyCampaignSchedule(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	e.links.linkErrForSource = map[string]error{"mastodon": errors.New("boom")}
	out := e.createV1(t, "")
	linked, unlinked := out.Results[0].Post, out.Results[1].Post
	lp := fmt.Sprintf("/api/v1/guide-posts/%d", linked.ID)
	up := fmt.Sprintf("/api/v1/guide-posts/%d", unlinked.ID)

	rec := e.call(t, http.MethodPatch, lp, e.write, `{"body":"fresh words {link}"}`)
	if got := v1Decode[guideposts.Post](t, rec); rec.Code != http.StatusOK || got.Body != "fresh words "+*linked.ShortURL {
		t.Fatalf("body edit = %d %q", rec.Code, got.Body)
	}
	rec = e.call(t, http.MethodPatch, lp, e.write, `{"campaign":"guides-2026-12"}`)
	if d := apitest.ErrorOf(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || d.Code != api.CodeConflict {
		t.Errorf("campaign on linked post = %d %+v", rec.Code, d)
	}
	rec = e.call(t, http.MethodPatch, up, e.write, `{"campaign":"guides-2026-12"}`)
	if got := v1Decode[guideposts.Post](t, rec); rec.Code != http.StatusOK || got.CampaignSlug != "guides-2026-12" {
		t.Errorf("campaign on unlinked draft = %d %q", rec.Code, got.CampaignSlug)
	}
	at := inAnHour()
	rec = e.call(t, http.MethodPatch, lp, e.write, fmt.Sprintf(`{"scheduled_at":%q}`, at))
	if got := v1Decode[guideposts.Post](t, rec); rec.Code != http.StatusOK || got.Status != "scheduled" {
		t.Errorf("schedule via patch = %d %+v", rec.Code, got)
	}
	e.wantFieldError(t, e.call(t, http.MethodPatch, lp, e.write, `{}`), "body")
	e.wantFieldError(t, e.call(t, http.MethodPatch, lp, e.write, `{"scheduled_at":"soon"}`), "scheduled_at")
	e.wantFieldError(t, e.call(t, http.MethodPatch, lp, e.write, `{"body":"no link at all"}`), "body")

	// The unlinked draft's schedule mints its link now (ShortLinks is back).
	e.links.linkErrForSource = nil
	rec = e.call(t, http.MethodPatch, up, e.write, fmt.Sprintf(`{"scheduled_at":%q}`, at))
	if got := v1Decode[guideposts.Post](t, rec); rec.Code != http.StatusOK || got.Status != "scheduled" || got.ShortURL == nil || got.CampaignSlug != "guides-2026-12" {
		t.Errorf("schedule unlinked = %d %+v", rec.Code, got)
	}
	if rows := v1AuditRows(t, e.pool, audit.ActionGuidePostUpdated, e.writeTok.ID); len(rows) != 2 {
		t.Errorf("%d guide_post.updated rows, want 2 (body, campaign)", len(rows))
	}
}

func TestV1GuidePosts_ShortLinksDownIsUnavailable(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	e.links.linkErrForSource = map[string]error{"mastodon": errors.New("boom")}
	out := e.createV1(t, "")
	rec := e.call(t, http.MethodPost, fmt.Sprintf("/api/v1/guide-posts/%d/send", out.Results[1].Post.ID), e.write, "")
	if d := apitest.ErrorOf(t, rec.Body.Bytes()); rec.Code != http.StatusServiceUnavailable || d.Code != api.CodeUnavailable {
		t.Errorf("send with ShortLinks failing = %d %+v", rec.Code, d)
	}
}

// failingCreateStore fails every Create, as a database error would.
type failingCreateStore struct{ guidePostStore }

func (failingCreateStore) Create(context.Context, string, string, string, string, string) (guideposts.Post, error) {
	return guideposts.Post{}, errors.New("database unavailable")
}

// TestV1GuidePosts_AllChannelsFailingReturnsFirstError: when no channel
// produced a post, the op answers with the first channel's error rather
// than a 201 holding only failures. No database: the store is a fake.
func TestV1GuidePosts_AllChannelsFailingReturnsFirstError(t *testing.T) {
	h := NewAdminGuidePostsHandler(failingCreateStore{}, nil, nil, nil, "https://site.example.test", bothConnected())
	_, err := h.createGuidePostsV1(context.Background(), "create_guide_posts", createGuidePostsIn{Slug: guides.Topics()[0].Slug, Channels: []string{"bluesky", "mastodon"}})
	if err == nil || err.Error() != "database unavailable" {
		t.Fatalf("err = %v, want the store's error", err)
	}
	if ae := api.AsError("create_guide_posts", err); ae.Status != http.StatusInternalServerError || ae.Code != api.CodeInternal {
		t.Errorf("answered as %+v", ae)
	}
}

func TestV1GuidePosts_ScheduleUnscheduleSendAgain(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	id := e.createV1(t, "").Results[0].Post.ID
	p := fmt.Sprintf("/api/v1/guide-posts/%d", id)

	rec := e.call(t, http.MethodPost, p+"/schedule", e.write, fmt.Sprintf(`{"scheduled_at":%q}`, inAnHour()))
	if got := v1Decode[guideposts.Post](t, rec); rec.Code != http.StatusOK || got.Status != "scheduled" {
		t.Fatalf("schedule = %d %+v", rec.Code, got)
	}
	e.wantFieldError(t, e.call(t, http.MethodPost, p+"/schedule", e.write, `{"scheduled_at":"2000-01-01T00:00:00Z"}`), "scheduled_at")
	rec = e.call(t, http.MethodPost, p+"/unschedule", e.write, "")
	if got := v1Decode[guideposts.Post](t, rec); rec.Code != http.StatusOK || got.Status != "draft" {
		t.Fatalf("unschedule = %d %+v", rec.Code, got)
	}
	if rec := e.call(t, http.MethodPost, p+"/again", e.write, ""); rec.Code != http.StatusConflict {
		t.Errorf("again on a draft = %d", rec.Code)
	}
	rec = e.call(t, http.MethodPost, p+"/send", e.write, "")
	if got := v1Decode[guideposts.Post](t, rec); rec.Code != http.StatusAccepted || got.Status != "queued" {
		t.Fatalf("send = %d %+v", rec.Code, got)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outbound_queue WHERE kind = 'guide_post' AND recipient = $1`, guideposts.Recipient(id)).Scan(&n); err != nil || n != 1 {
		t.Errorf("outbound_queue rows %d, %v", n, err)
	}
	e.markPosted(t, id)
	rec = e.call(t, http.MethodPost, p+"/again", e.write, `{"campaign":"guides-2027-01"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("again = %d %s", rec.Code, rec.Body)
	}
	again := v1Decode[guidePostLinkResponse](t, rec)
	if again.Post.ID == id || again.Post.Status != "draft" || again.Post.CampaignSlug != "guides-2027-01" || again.Post.ShortURL == nil {
		t.Errorf("again post %+v", again.Post)
	}
	if rec := e.call(t, http.MethodPost, "/api/v1/guide-posts/0/send", e.write, ""); rec.Code != http.StatusNotFound {
		t.Errorf("send missing = %d %s", rec.Code, rec.Body)
	}
}

func TestV1GuidePosts_DeleteDraftOnly(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	out := e.createV1(t, "")
	draft, other := out.Results[0].Post.ID, out.Results[1].Post.ID
	rec := e.call(t, http.MethodDelete, fmt.Sprintf("/api/v1/guide-posts/%d", draft), e.write, "")
	if got := v1Decode[deletedV1](t, rec); rec.Code != http.StatusOK || !got.Deleted || got.ID != draft {
		t.Fatalf("delete draft = %d %+v", rec.Code, got)
	}
	if rows := v1AuditRows(t, e.pool, audit.ActionGuidePostDeleted, e.writeTok.ID); len(rows) != 1 || *rows[0].TargetID != draft {
		t.Errorf("deleted audit rows %+v", rows)
	}
	if rec := e.call(t, http.MethodPost, fmt.Sprintf("/api/v1/guide-posts/%d/schedule", other), e.write, fmt.Sprintf(`{"scheduled_at":%q}`, inAnHour())); rec.Code != http.StatusOK {
		t.Fatalf("schedule = %d", rec.Code)
	}
	rec = e.call(t, http.MethodDelete, fmt.Sprintf("/api/v1/guide-posts/%d", other), e.write, "")
	if d := apitest.ErrorOf(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || !strings.Contains(d.Message, "only a draft") {
		t.Errorf("delete scheduled = %d %+v", rec.Code, d)
	}
	if n := e.postCount(t); n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
}

func TestV1GuidePosts_ListFilters(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	out := e.createV1(t, `,"campaign":"guides-2026-12"`)
	b, m := out.Results[0].Post, out.Results[1].Post
	e.markPosted(t, b.ID)
	list := func(q string) []int64 {
		t.Helper()
		rec := e.call(t, http.MethodGet, "/api/v1/guide-posts"+q, e.read, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list%s = %d %s", q, rec.Code, rec.Body)
		}
		var ids []int64
		for _, p := range v1Decode[api.List[guideposts.Post]](t, rec).Items {
			ids = append(ids, p.ID)
		}
		return ids
	}
	if got := list(""); !slices.Equal(got, []int64{m.ID, b.ID}) {
		t.Errorf("all = %v, want newest first", got)
	}
	if got := list("?status=posted"); !slices.Equal(got, []int64{b.ID}) {
		t.Errorf("status=posted %v", got)
	}
	if got := list("?channel=mastodon"); !slices.Equal(got, []int64{m.ID}) {
		t.Errorf("channel=mastodon %v", got)
	}
	if got := list("?slug=" + e.slug + "&campaign=guides-2026-12"); len(got) != 2 {
		t.Errorf("slug+campaign %v", got)
	}
	if got := list("?campaign=other"); len(got) != 0 {
		t.Errorf("campaign=other %v", got)
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	pastHour := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if got := list("?from=" + future); len(got) != 0 {
		t.Errorf("from future %v", got)
	}
	if got := list("?from=" + pastHour + "&to=" + future); len(got) != 2 {
		t.Errorf("from/to window %v", got)
	}
	e.wantFieldError(t, e.call(t, http.MethodGet, "/api/v1/guide-posts?status=sent", e.read, ""), "status")
	e.wantFieldError(t, e.call(t, http.MethodGet, "/api/v1/guide-posts?channel=myspace", e.read, ""), "channel")
	e.wantFieldError(t, e.call(t, http.MethodGet, "/api/v1/guide-posts?from=yesterday", e.read, ""), "from")

	rec := e.call(t, http.MethodGet, "/api/v1/guide-posts?limit=1", e.read, "")
	p1 := v1Decode[api.List[guideposts.Post]](t, rec)
	if len(p1.Items) != 1 || p1.NextCursor == nil || p1.Items[0].ID != m.ID {
		t.Fatalf("page 1 %+v", p1)
	}
	rec = e.call(t, http.MethodGet, "/api/v1/guide-posts?limit=1&cursor="+*p1.NextCursor, e.read, "")
	p2 := v1Decode[api.List[guideposts.Post]](t, rec)
	if len(p2.Items) != 1 || p2.NextCursor != nil || p2.Items[0].ID != b.ID {
		t.Fatalf("page 2 %+v", p2)
	}
}

func TestV1GuidePosts_OverviewMatchesAdmin(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	out := e.createV1(t, "")
	e.markPosted(t, out.Results[0].Post.ID)
	code, adminBody := e.do(t, http.MethodGet, "/admin/guide-posts-overview", "")
	if code != http.StatusOK {
		t.Fatalf("admin overview = %d", code)
	}
	rec := e.call(t, http.MethodGet, "/api/v1/guide-posts/overview", e.read, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("v1 overview = %d %s", rec.Code, rec.Body)
	}
	var a, v any
	if err := json.Unmarshal(adminBody, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, v) {
		t.Errorf("v1 overview differs from admin:\nadmin %s\nv1    %s", adminBody, rec.Body)
	}
}

func TestV1GuidePosts_ChannelsReportLimits(t *testing.T) {
	e := newV1GuidePostsEnv(t, fakePosting{"mastodon": true})
	rec := e.call(t, http.MethodGet, "/api/v1/channels", e.read, "")
	got := v1Decode[channelsV1](t, rec)
	if rec.Code != http.StatusOK || len(got.Channels) != 2 || got.LinkToken != outreach.LinkToken || got.LinkMeasuredAs != guideposts.LinkMeasuredAs {
		t.Fatalf("channels = %d %+v", rec.Code, got)
	}
	bs, md := got.Channels[0], got.Channels[1]
	if bs.Name != "bluesky" || bs.CharLimit != 300 || bs.Configured || bs.URLWeight != 0 || bs.ByteLimit == 0 || bs.Counting != "graphemes" {
		t.Errorf("bluesky %+v", bs)
	}
	if md.Name != "mastodon" || md.CharLimit != 500 || !md.Configured || md.URLWeight != 23 || md.ByteLimit != 0 {
		t.Errorf("mastodon %+v", md)
	}
}

func TestV1GuidePosts_MCPCreate(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	mcpH := mcpserver.NewHandler(mcpserver.Deps{Registry: e.reg, Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter()})
	cs := mcptest.Connect(t, mcpH, e.write)
	res := mcptest.Call(t, cs, "create_guide_posts", map[string]any{"slug": e.slug, "channels": []string{"bluesky", "mastodon"}})
	if res.IsError {
		t.Fatalf("tool error: %s", mcptest.Text(res))
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out createGuidePostsOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 || out.Results[0].Post == nil || out.Results[1].Post == nil {
		t.Fatalf("results %s", raw)
	}
	if rows := v1AuditRows(t, e.pool, audit.ActionGuidePostCreated, e.writeTok.ID); len(rows) != 2 {
		t.Errorf("%d created rows via MCP", len(rows))
	}

	bad := mcptest.Call(t, cs, "create_guide_posts", map[string]any{"slug": e.slug, "channels": []string{"bluesky"}, "body": strings.Repeat("a", 320)})
	if d := mcptest.ErrorOf(t, bad); d.Code != api.CodeValidation || d.Fields["body.bluesky"] == "" {
		t.Errorf("over-limit via MCP %+v", d)
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
	if slices.Contains(names, "create_guide_posts") || !slices.Contains(names, "list_guide_posts") {
		t.Errorf("read key tools %v", names)
	}
}
