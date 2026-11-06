package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// Fakes for AdminNewsletterEventsHandler's seams (#0643). Only the admin
// session comes from the database.

type nlCampaigns map[int64]mailing.Campaign

func (f nlCampaigns) GetByID(_ context.Context, id int64) (mailing.Campaign, error) {
	c, ok := f[id]
	if !ok {
		return mailing.Campaign{}, mailing.ErrCampaignNotFound
	}
	return c, nil
}

type nlWorkshops []workshops.Workshop

func (f nlWorkshops) ListVisible(context.Context, time.Time) ([]workshops.Workshop, []workshops.Workshop, error) {
	return f, nil, nil
}

type nlOutreach map[int64]string // workshop id -> campaign slug

func (f nlOutreach) GetByWorkshop(_ context.Context, id int64) (outreach.Outreach, error) {
	s, ok := f[id]
	if !ok {
		return outreach.Outreach{}, outreach.ErrNotFound
	}
	return outreach.Outreach{WorkshopID: id, CampaignSlug: s}, nil
}

type nlLinks struct {
	mu   sync.Mutex
	rows map[[2]int64]outreach.NewsletterLink
}

func (f *nlLinks) GetNewsletterLink(_ context.Context, c, w int64) (outreach.NewsletterLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.rows[[2]int64{c, w}]
	if !ok {
		return outreach.NewsletterLink{}, outreach.ErrNotFound
	}
	return l, nil
}

func (f *nlLinks) InsertNewsletterLink(_ context.Context, c, w int64, slug, key, short string) (outreach.NewsletterLink, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l, ok := f.rows[[2]int64{c, w}]; ok {
		return l, false, nil
	}
	if f.rows == nil {
		f.rows = map[[2]int64]outreach.NewsletterLink{}
	}
	l := outreach.NewsletterLink{ID: int64(len(f.rows) + 1), EmailCampaignID: c, WorkshopID: w, CampaignSlug: slug, Key: key, ShortURL: short}
	f.rows[[2]int64{c, w}] = l
	return l, true, nil
}

// nlShortLinker wraps fakeShortLinker so a test can fail the Nth CreateLink.
type nlShortLinker struct {
	*fakeShortLinker
	calls int
	errAt map[int]error // 1-based CreateLink call -> error
}

func (f *nlShortLinker) CreateLink(ctx context.Context, req shortlinks.CreateLinkRequest) (*shortlinks.Link, error) {
	f.calls++
	if err := f.errAt[f.calls]; err != nil {
		return nil, err
	}
	return f.fakeShortLinker.CreateLink(ctx, req)
}

type nlAuditor struct{ entries []audit.Entry }

func (a *nlAuditor) Record(_ context.Context, e audit.Entry) { a.entries = append(a.entries, e) }

type nlEnv struct {
	srv     *httptest.Server
	h       *AdminNewsletterEventsHandler
	sl      *nlShortLinker
	links   *nlLinks
	auditor *nlAuditor
}

var nlNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

// nlNewsletterSlug builds a newsletter-shaped slug from parts, never a
// literal (#0405).
func nlNewsletterSlug() string { return fmt.Sprintf("%02d-%04d", 11, 2026) }

func nlWorkshop(id int64, slug, status string, starts *time.Time, venue string) workshops.Workshop {
	w := workshops.Workshop{ID: id, Slug: slug, Title: "Event " + slug, Status: status, StartsAt: starts}
	if venue != "" {
		w.LocationName = &venue
	}
	return w
}

func at(t time.Time) *time.Time { return &t }

func newNLEnv(t *testing.T, enabled bool, status string, ws nlWorkshops, o nlOutreach) *nlEnv {
	t.Helper()
	pool := outreachTestPool(t)
	camps := nlCampaigns{7: {ID: 7, Status: status, Slug: nlNewsletterSlug()}}
	sl := &nlShortLinker{fakeShortLinker: &fakeShortLinker{enabled: enabled}}
	links := &nlLinks{}
	h := NewAdminNewsletterEventsHandler(camps, ws, o, links, sl, nil, "https://site.example.test")
	aud := &nlAuditor{}
	h.auditor = aud
	h.now = func() time.Time { return nlNow }
	requireSession := middleware.RequireSession(auth.NewStore(pool))
	mux := http.NewServeMux()
	mux.Handle("POST /admin/campaigns/{id}/upcoming-events", requireSession(middleware.RequireAdmin(http.HandlerFunc(h.UpcomingEvents))))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	admin := seedAdmin(t, pool, "admin-newsletter@example.com")
	seedSession(t, pool, admin, "newsletter-admin-token")
	return &nlEnv{srv: srv, h: h, sl: sl, links: links, auditor: aud}
}

type nlResp struct {
	Month    string `json:"month"`
	Label    string `json:"label"`
	Markdown string `json:"markdown"`
	Events   []struct {
		WorkshopID int64  `json:"workshop_id"`
		URL        string `json:"url"`
		Link       string `json:"link"`
		Reused     bool   `json:"reused"`
		Reason     string `json:"reason"`
	} `json:"events"`
	Created  int `json:"created"`
	Reused   int `json:"reused"`
	Fallback int `json:"fallback"`
}

func (e *nlEnv) post(t *testing.T, id, month string) (int, nlResp, string) {
	t.Helper()
	resp := doJSON(t, e.srv.Client(), http.MethodPost, e.srv.URL+"/admin/campaigns/"+id+"/upcoming-events", "newsletter-admin-token", `{"month":"`+month+`"}`)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var r nlResp
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatalf("decode: %v (%s)", err, b)
		}
	}
	return resp.StatusCode, r, string(b)
}

func nlTwoEvents() (nlWorkshops, nlOutreach) {
	return nlWorkshops{
		nlWorkshop(1, "alpha", "published", at(time.Date(2026, 11, 15, 21, 0, 0, 0, time.UTC)), "The Shop"),
		nlWorkshop(2, "beta", "published", at(time.Date(2026, 11, 20, 21, 0, 0, 0, time.UTC)), ""),
	}, nlOutreach{1: "alpha-camp", 2: "beta-camp"}
}

func TestUpcomingEvents_CreatesOneLinkPerEventInOutreachCampaign(t *testing.T) {
	ws, o := nlTwoEvents()
	e := newNLEnv(t, true, mailing.CampaignStatusDraft, ws, o)
	code, r, raw := e.post(t, "7", "2026-11")
	if code != http.StatusOK {
		t.Fatalf("status = %d %s", code, raw)
	}
	if r.Created != 2 || r.Reused != 0 || r.Fallback != 0 || len(r.Events) != 2 {
		t.Fatalf("resp = %+v", r)
	}
	reqs := e.sl.links
	if len(reqs) != 2 {
		t.Fatalf("CreateLink calls = %d", len(reqs))
	}
	for i, want := range []string{"alpha-camp", "beta-camp"} {
		q := reqs[i]
		if q.CampaignSlug != want || q.UTMSource != "newsletter" || q.UTMMedium != "email" || q.UTMContent != nlNewsletterSlug() || q.Key != "" || q.Placement != "newsletter" {
			t.Errorf("req %d = %+v", i, q)
		}
		u, _ := url.Parse(q.DestinationURL)
		if u.Query().Get("utm_source") != "newsletter" || u.Query().Get("utm_medium") != "email" || u.Query().Get("utm_content") != nlNewsletterSlug() {
			t.Errorf("destination %q", q.DestinationURL)
		}
	}
	if r.Events[0].Link != "short" || !strings.HasPrefix(r.Events[0].URL, "https://go.example.test/u/") {
		t.Errorf("event 0 = %+v", r.Events[0])
	}
	if !strings.Contains(r.Markdown, "## Upcoming events in November 2026") || !strings.Contains(r.Markdown, r.Events[1].URL) {
		t.Errorf("markdown = %q", r.Markdown)
	}
}

func TestUpcomingEvents_SecondCallReusesStoredLinks(t *testing.T) {
	ws, o := nlTwoEvents()
	e := newNLEnv(t, true, mailing.CampaignStatusDraft, ws, o)
	_, first, _ := e.post(t, "7", "2026-11")
	links, me := len(e.sl.links), e.sl.meCalls
	code, r, raw := e.post(t, "7", "2026-11")
	if code != http.StatusOK {
		t.Fatalf("status = %d %s", code, raw)
	}
	if len(e.sl.links) != links || e.sl.meCalls != me || e.sl.calls != 2 {
		t.Errorf("second call reached ShortLinks: links %d->%d me %d->%d", links, len(e.sl.links), me, e.sl.meCalls)
	}
	if r.Reused != 2 || r.Created != 0 || r.Events[0].URL != first.Events[0].URL || !r.Events[0].Reused {
		t.Errorf("resp = %+v", r)
	}
}

func TestUpcomingEvents_NoOutreachFallsBackToPlainUTMURL(t *testing.T) {
	ws, _ := nlTwoEvents()
	e := newNLEnv(t, true, mailing.CampaignStatusDraft, ws, nlOutreach{2: "beta-camp"})
	_, r, _ := e.post(t, "7", "2026-11")
	ev := r.Events[0]
	if ev.Link != "plain" || ev.Reason != "no_campaign" || ev.URL != outreach.NewsletterDestinationURL("https://site.example.test", "alpha", nlNewsletterSlug()) {
		t.Errorf("event 0 = %+v", ev)
	}
	if r.Events[1].Link != "short" || r.Fallback != 1 || r.Created != 1 {
		t.Errorf("resp = %+v", r)
	}
}

func TestUpcomingEvents_ShortLinksDisabledFallsBack(t *testing.T) {
	ws, o := nlTwoEvents()
	e := newNLEnv(t, false, mailing.CampaignStatusDraft, ws, o)
	_, r, _ := e.post(t, "7", "2026-11")
	if r.Fallback != 2 || e.sl.calls != 0 || e.sl.meCalls != 0 {
		t.Fatalf("resp = %+v calls=%d", r, e.sl.calls)
	}
	for _, ev := range r.Events {
		if ev.Reason != "not_configured" || ev.Link != "plain" {
			t.Errorf("event = %+v", ev)
		}
	}
}

func TestUpcomingEvents_RateLimitStopsAndFallsBackRest(t *testing.T) {
	ws, o := nlTwoEvents()
	ws = append(ws, nlWorkshop(3, "gamma", "published", at(time.Date(2026, 11, 25, 21, 0, 0, 0, time.UTC)), ""))
	o[3] = "gamma-camp"
	e := newNLEnv(t, true, mailing.CampaignStatusDraft, ws, o)
	e.sl.errAt = map[int]error{2: shortlinks.ErrRateLimited}
	_, r, _ := e.post(t, "7", "2026-11")
	if e.sl.calls != 2 {
		t.Errorf("CreateLink calls = %d, want 2 (stop after the rate limit)", e.sl.calls)
	}
	if r.Events[0].Link != "short" || r.Events[1].Reason != "rate_limited" || r.Events[2].Reason != "rate_limited" || r.Fallback != 2 {
		t.Errorf("resp = %+v", r)
	}
}

func TestUpcomingEvents_FiltersPastDraftCanceledUndatedAndOtherMonths(t *testing.T) {
	ws := nlWorkshops{
		nlWorkshop(1, "keep", "published", at(time.Date(2026, 11, 15, 21, 0, 0, 0, time.UTC)), ""),
		nlWorkshop(2, "past", "published", at(time.Date(2026, 11, 5, 21, 0, 0, 0, time.UTC)), ""),
		nlWorkshop(3, "draft", "draft", at(time.Date(2026, 11, 16, 21, 0, 0, 0, time.UTC)), ""),
		nlWorkshop(4, "canceled", "canceled", at(time.Date(2026, 11, 17, 21, 0, 0, 0, time.UTC)), ""),
		nlWorkshop(5, "undated", "published", nil, ""),
		nlWorkshop(6, "december", "published", at(time.Date(2026, 12, 3, 21, 0, 0, 0, time.UTC)), ""),
	}
	e := newNLEnv(t, false, mailing.CampaignStatusDraft, ws, nlOutreach{})
	_, r, _ := e.post(t, "7", "2026-11")
	if len(r.Events) != 1 || r.Events[0].WorkshopID != 1 {
		t.Fatalf("events = %+v", r.Events)
	}
}

func TestUpcomingEvents_NotEditable409(t *testing.T) {
	ws, o := nlTwoEvents()
	for _, st := range []string{mailing.CampaignStatusSending, mailing.CampaignStatusSent, mailing.CampaignStatusCanceled} {
		e := newNLEnv(t, true, st, ws, o)
		if code, _, _ := e.post(t, "7", "2026-11"); code != http.StatusConflict {
			t.Errorf("%s: status = %d, want 409", st, code)
		}
		if e.sl.calls != 0 {
			t.Errorf("%s: reached ShortLinks", st)
		}
	}
}

func TestUpcomingEvents_UnknownCampaign404(t *testing.T) {
	ws, o := nlTwoEvents()
	e := newNLEnv(t, true, mailing.CampaignStatusDraft, ws, o)
	if code, _, _ := e.post(t, "999", "2026-11"); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

func TestUpcomingEvents_BadMonth400(t *testing.T) {
	ws, o := nlTwoEvents()
	e := newNLEnv(t, true, mailing.CampaignStatusDraft, ws, o)
	for _, m := range []string{"", "2026-13", nlNewsletterSlug()} {
		if code, _, _ := e.post(t, "7", m); code != http.StatusBadRequest {
			t.Errorf("month %q: status = %d, want 400", m, code)
		}
	}
	if code, _, _ := e.post(t, "abc", "2026-11"); code != http.StatusBadRequest {
		t.Errorf("bad id: status = %d, want 400", code)
	}
}

func TestUpcomingEvents_AuditsOnlyNewLinks(t *testing.T) {
	ws, o := nlTwoEvents()
	e := newNLEnv(t, true, mailing.CampaignStatusDraft, ws, o)
	e.post(t, "7", "2026-11")
	e.post(t, "7", "2026-11")
	if len(e.auditor.entries) != 2 {
		t.Fatalf("audit entries = %d, want 2", len(e.auditor.entries))
	}
	en := e.auditor.entries[0]
	md, _ := en.Metadata.(map[string]any)
	if en.Action != audit.ActionNewsletterLinkCreated || en.TargetType != audit.TargetNewsletterLink || md["campaign_slug"] != "alpha-camp" || md["campaign_id"] != int64(7) {
		t.Errorf("entry = %+v", en)
	}
}

func TestUpcomingEvents_EmptyMonth(t *testing.T) {
	ws, o := nlTwoEvents()
	e := newNLEnv(t, true, mailing.CampaignStatusDraft, ws, o)
	code, _, raw := e.post(t, "7", "2027-03")
	if code != http.StatusOK || !strings.Contains(raw, `"markdown":""`) || !strings.Contains(raw, `"events":[]`) {
		t.Fatalf("status = %d body = %s", code, raw)
	}
}
