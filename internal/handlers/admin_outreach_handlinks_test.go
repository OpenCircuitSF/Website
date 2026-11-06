package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

type testHandLink struct {
	Channel  string  `json:"channel"`
	Wave     string  `json:"wave"`
	ShortURL *string `json:"short_url"`
}

type testHandPanel struct {
	testPanel
	HandLinks        []testHandLink `json:"hand_links"`
	HandLinksPending bool           `json:"hand_links_pending"`
}

type testHandReport struct {
	testLinkReport
	Drafted int `json:"drafted"`
}

func threeChannels() []string { return []string{"bluesky", "mastodon", "instagram"} }

// handSetup starts a published workshop under the old three-channel set and
// writes the given draft-all body, so TikTok and YouTube are newly enabled
// when the caller switches them on with SetEnabledChannels(nil).
func (e *outreachEnv) handSetup(t *testing.T, draftBody string) (workshops.Workshop, int64) {
	t.Helper()
	e.beforeEvent()
	e.h.SetEnabledChannels(threeChannels)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	oid := e.startHTTP(t, w).Outreach.ID
	if code, b := e.do(t, http.MethodPost, e.linksPath(w, "draft-all"), draftBody); code != http.StatusOK {
		t.Fatalf("draft-all = %d (%s)", code, b)
	}
	return w, oid
}

// beforeEvent pins the handler's clock a month before
// seedOutreachWorkshop's 2026-12-05 start, so the hand-links pass is
// allowed (#0638) whatever today's date is.
func (e *outreachEnv) beforeEvent() {
	e.h.now = func() time.Time { return time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC) }
}

func (e *outreachEnv) handPanel(t *testing.T, w workshops.Workshop) testHandPanel {
	t.Helper()
	code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("panel = %d (%s)", code, b)
	}
	return decodeOutreach[testHandPanel](t, b)
}

func (e *outreachEnv) handSync(t *testing.T, w workshops.Workshop) (testHandReport, string) {
	t.Helper()
	code, b := e.do(t, http.MethodPost, e.linksPath(w, "hand-links"), "")
	if code != http.StatusOK {
		t.Fatalf("hand-links = %d (%s)", code, b)
	}
	return decodeOutreach[testHandReport](t, b), string(b)
}

// postRows snapshots every column of every post of an outreach as text.
func (e *outreachEnv) postRows(t *testing.T, oid int64) map[int64]string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT p.id, to_jsonb(p)::text FROM outreach_posts p WHERE p.outreach_id = $1`, oid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var s string
		if err := rows.Scan(&id, &s); err != nil {
			t.Fatal(err)
		}
		out[id] = s
	}
	return out
}

func findPost(t *testing.T, p testHandPanel, channel, wave string) testPost {
	t.Helper()
	for _, x := range p.Posts {
		if x.Channel == channel && x.Wave == wave {
			return x
		}
	}
	t.Fatalf("no %s/%s post in the panel", channel, wave)
	return testPost{}
}

func hasPost(p testHandPanel, channel, wave string) bool {
	for _, x := range p.Posts {
		if x.Channel == channel && x.Wave == wave {
			return true
		}
	}
	return false
}

func TestAdminOutreachHandLinks_ExistingWorkshopGainsLinksAndDrafts(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	e.beforeEvent()
	e.linkLocations(t)
	w, oid := e.handSetup(t, `{"waves":["reminder"]}`)
	p := e.handPanel(t, w)
	bsky := findPost(t, p, "bluesky", "reminder")
	e.schedulePost(t, bsky.ID, time.Now().Add(24*time.Hour))
	ig := findPost(t, p, "instagram", "announce")
	if code, b := e.do(t, http.MethodPost, postPath(ig.ID, "posted"), ""); code != http.StatusOK {
		t.Fatalf("posted = %d (%s)", code, b)
	}
	before := e.postRows(t, oid)
	calls := len(e.links.links)

	e.h.SetEnabledChannels(nil)
	p = e.handPanel(t, w)
	if !p.HandLinksPending {
		t.Error("GET: pending = false, want true")
	}
	for _, l := range p.HandLinks {
		if isVideo := l.Channel == "tiktok" || l.Channel == "youtube"; isVideo != (l.ShortURL == nil) {
			t.Errorf("GET row %s/%s url = %v", l.Channel, l.Wave, l.ShortURL)
		}
	}
	if len(e.links.links) != calls {
		t.Fatalf("GET made %d CreateLink calls", len(e.links.links)-calls)
	}

	r, _ := e.handSync(t, w)
	if r.Created != 6 || r.Drafted != 4 || len(r.Failed) != 0 {
		t.Fatalf("report = %+v", r)
	}
	reqs := e.links.links[calls:]
	if len(reqs) != 6 {
		t.Fatalf("CreateLink calls = %d, want 6", len(reqs))
	}
	seen := map[string]bool{}
	for _, q := range reqs {
		if (q.UTMSource != "tiktok" && q.UTMSource != "youtube") || q.UTMMedium != "video" {
			t.Errorf("request source/medium = %s/%s", q.UTMSource, q.UTMMedium)
		}
		seen[q.UTMSource+"/"+q.UTMContent] = true
	}
	for _, slot := range []string{"announce", "reminder", "last-call"} {
		if !seen["tiktok/"+slot] || !seen["youtube/"+slot] {
			t.Errorf("no link requested for wave %s: %v", slot, seen)
		}
	}

	after := e.postRows(t, oid)
	for id, row := range before {
		if after[id] != row {
			t.Errorf("post %d changed:\n was %s\n now %s", id, row, after[id])
		}
	}
	if len(after) != len(before)+4 {
		t.Errorf("posts = %d, want %d", len(after), len(before)+4)
	}

	p = e.handPanel(t, w)
	for _, ch := range []string{"tiktok", "youtube"} {
		for _, wv := range []string{"announce", "reminder"} {
			n := findPost(t, p, ch, wv)
			if n.Status != "manual" || !n.Prepared || n.Stale || strings.Contains(n.Body, "{link}") || n.ShortURL == nil || !strings.Contains(n.Body, *n.ShortURL) {
				t.Errorf("%s/%s: %+v", ch, wv, n)
			}
		}
		if hasPost(p, ch, "last-call") {
			t.Errorf("%s drafted into the unwritten last-call wave", ch)
		}
	}
	if p.HandLinksPending || len(p.HandLinks) != 9 {
		t.Errorf("after sync: pending = %v, rows = %d", p.HandLinksPending, len(p.HandLinks))
	}
	urls := map[string]bool{}
	for _, l := range p.HandLinks {
		if l.ShortURL == nil {
			t.Fatalf("row %s/%s has no URL", l.Channel, l.Wave)
		}
		urls[*l.ShortURL] = true
	}
	if len(urls) != 9 {
		t.Errorf("distinct URLs = %d, want 9", len(urls))
	}
}

// Production's programming-leds-2 already has TikTok and YouTube links and
// drafts in all three waves; such a workshop gets nothing new.
func TestAdminOutreachHandLinks_ExistingVideoLinksAndDraftsUnchanged(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	e.beforeEvent()
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	oid := e.startHTTP(t, w).Outreach.ID
	if code, b := e.do(t, http.MethodPost, e.linksPath(w, "draft-all"), `{"waves":["reminder","last-call"]}`); code != http.StatusOK {
		t.Fatalf("draft-all = %d (%s)", code, b)
	}
	before := e.postRows(t, oid)
	if len(before) != 15 {
		t.Fatalf("posts = %d, want 15", len(before))
	}
	linkRows, calls := e.linkRowCount(t, oid), len(e.links.links)

	p := e.handPanel(t, w)
	if p.HandLinksPending || len(p.HandLinks) != 9 {
		t.Errorf("GET: pending = %v, rows = %d", p.HandLinksPending, len(p.HandLinks))
	}
	r, _ := e.handSync(t, w)
	if r.Created != 0 || r.Drafted != 0 || r.Existing != 9 || len(r.Failed) != 0 {
		t.Errorf("report = %+v", r)
	}
	if len(e.links.links) != calls {
		t.Errorf("made %d CreateLink calls", len(e.links.links)-calls)
	}
	if n := e.linkRowCount(t, oid); n != linkRows {
		t.Errorf("link rows = %d, want %d", n, linkRows)
	}
	after := e.postRows(t, oid)
	if len(after) != len(before) {
		t.Errorf("posts = %d, want %d", len(after), len(before))
	}
	for id, row := range before {
		if after[id] != row {
			t.Errorf("post %d changed:\n was %s\n now %s", id, row, after[id])
		}
	}
}

func TestAdminOutreachHandLinks_Idempotent(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	e.beforeEvent()
	w, _ := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(nil)
	e.handSync(t, w)
	calls := len(e.links.links)
	r, _ := e.handSync(t, w)
	if r.Created != 0 || r.Drafted != 0 || len(e.links.links) != calls {
		t.Errorf("second pass: %+v, new calls %d", r, len(e.links.links)-calls)
	}
}

func TestAdminOutreachHandLinks_OutageKeepsScreenWorking(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	e.beforeEvent()
	w, _ := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(nil)
	postsBefore := len(e.handPanel(t, w).Posts)

	e.links.linkErr = errors.New("shortlinks: HTTP 500: secret-upstream-detail")
	r, body := e.handSync(t, w)
	if len(r.Failed) != 6 || r.Drafted != 0 || r.Created != 0 {
		t.Errorf("outage report = %+v", r)
	}
	if strings.Contains(body, "secret-upstream-detail") {
		t.Errorf("upstream text in the response: %s", body)
	}
	p := e.handPanel(t, w)
	if !p.HandLinksPending || len(p.Posts) != postsBefore {
		t.Errorf("GET during outage: pending = %v, posts = %d, want %d", p.HandLinksPending, len(p.Posts), postsBefore)
	}

	e.links.linkErr = nil
	if r, _ := e.handSync(t, w); r.Created != 6 || r.Drafted != 4 {
		t.Errorf("after recovery = %+v", r)
	}

	w2, _ := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(nil)
	e.links.linkErr = fmt.Errorf("create link: %w", shortlinks.ErrRateLimited)
	if r, _ := e.handSync(t, w2); !r.Stopped || r.Drafted != 0 {
		t.Errorf("rate limited = %+v", r)
	}
}

func TestAdminOutreachHandLinks_DeletedDraftStaysDeleted(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	e.beforeEvent()
	w, oid := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(nil)
	e.handSync(t, w)
	tt := findPost(t, e.handPanel(t, w), "tiktok", "announce")
	if code, b := e.do(t, http.MethodDelete, fmt.Sprintf("/admin/outreach-posts/%d", tt.ID), ""); code >= 300 {
		t.Fatalf("delete = %d (%s)", code, b)
	}
	if r, _ := e.handSync(t, w); r.Drafted != 0 {
		t.Errorf("drafted = %d after a delete", r.Drafted)
	}
	if hasPost(e.handPanel(t, w), "tiktok", "announce") {
		t.Error("the deleted draft came back")
	}

	if code, b := e.do(t, http.MethodPost, e.linksPath(w, "reset"), ""); code != http.StatusOK {
		t.Fatalf("reset = %d (%s)", code, b)
	}
	if r, _ := e.handSync(t, w); r.Drafted != 0 {
		t.Errorf("drafted = %d after a reset", r.Drafted)
	}
	if rows := e.postRows(t, oid); len(rows) != 0 {
		t.Errorf("posts after reset = %d", len(rows))
	}
}

func TestAdminOutreachHandLinks_RespectsWaveOptIn(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	e.beforeEvent()
	w, _ := e.handSetup(t, "")
	e.h.SetEnabledChannels(nil)
	if r, _ := e.handSync(t, w); r.Created != 6 || r.Drafted != 2 {
		t.Fatalf("report = %+v", r)
	}
	p := e.handPanel(t, w)
	if hasPost(p, "tiktok", "reminder") || !hasPost(p, "tiktok", "announce") {
		t.Error("want a TikTok announce draft and no reminder draft")
	}

	calls := len(e.links.links)
	if code, b := e.do(t, http.MethodPost, e.linksPath(w, "draft-all"), `{"waves":["reminder"]}`); code != http.StatusOK {
		t.Fatalf("draft-all = %d (%s)", code, b)
	}
	if len(e.links.links) != calls {
		t.Errorf("draft-all made %d CreateLink calls", len(e.links.links)-calls)
	}
	p = e.handPanel(t, w)
	for _, ch := range []string{"tiktok", "youtube"} {
		if n := findPost(t, p, ch, "reminder"); !n.Prepared {
			t.Errorf("%s reminder is not prepared", ch)
		}
	}
}

func TestAdminOutreachHandLinks_SwitchedOffChannelGetsNothing(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	e.beforeEvent()
	w, oid := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(func() []string { return []string{"bluesky", "mastodon", "instagram", "tiktok"} })
	calls := len(e.links.links)
	if r, _ := e.handSync(t, w); r.Created != 3 || r.Drafted != 2 {
		t.Fatalf("report = %+v", r)
	}
	for _, q := range e.links.links[calls:] {
		if q.UTMSource != "tiktok" {
			t.Errorf("link requested for %s", q.UTMSource)
		}
	}
	var yt int
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM outreach_links WHERE outreach_id = $1 AND channel = 'youtube') + (SELECT count(*) FROM outreach_posts WHERE outreach_id = $1 AND channel = 'youtube')`, oid).Scan(&yt); err != nil {
		t.Fatal(err)
	}
	if yt != 0 {
		t.Errorf("youtube rows = %d", yt)
	}
	p := e.handPanel(t, w)
	if p.HandLinksPending {
		t.Error("a switched-off channel left the panel pending")
	}
	for _, l := range p.HandLinks {
		if l.Channel == "youtube" {
			t.Errorf("youtube row: %+v", l)
		}
	}
}

func TestAdminOutreachHandLinks_Guards(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	e.beforeEvent()
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")

	resp := doJSON(t, e.srv.Client(), http.MethodPost, e.srv.URL+e.linksPath(w, "hand-links"), "outreach-user-token", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin = %d", resp.StatusCode)
	}
	if code, _ := e.do(t, http.MethodPost, e.linksPath(w, "hand-links"), ""); code != http.StatusConflict {
		t.Errorf("no outreach = %d", code)
	}

	// Started under three channels, so TikTok and YouTube really are missing
	// and only the guard keeps the GETs below from reporting pending.
	e.h.SetEnabledChannels(threeChannels)
	e.startHTTP(t, w)
	e.setWorkshopStatus(t, w.ID, "draft")
	if code, _ := e.do(t, http.MethodPost, e.linksPath(w, "hand-links"), ""); code != http.StatusConflict {
		t.Errorf("unpublished = %d", code)
	}
	e.h.SetEnabledChannels(nil)
	if e.handPanel(t, w).HandLinksPending {
		t.Error("unpublished GET reports pending")
	}

	e.setWorkshopStatus(t, w.ID, "published")
	e.links.enabled = false
	if code, _ := e.do(t, http.MethodPost, e.linksPath(w, "hand-links"), ""); code != http.StatusServiceUnavailable {
		t.Errorf("ShortLinks disabled = %d", code)
	}
	if e.handPanel(t, w).HandLinksPending {
		t.Error("disabled GET reports pending")
	}
}

func TestAdminOutreachHandLinks_NeverPostedAutomatically(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	e.beforeEvent()
	w, _ := e.handSetup(t, "")
	e.h.SetEnabledChannels(nil)
	e.handSync(t, w)
	p := e.handPanel(t, w)
	for _, ch := range []string{"instagram", "tiktok", "youtube"} {
		n := findPost(t, p, ch, "announce")
		if code, b := e.do(t, http.MethodPost, postPath(n.ID, "send"), ""); code != http.StatusBadRequest {
			t.Errorf("%s send = %d (%s)", ch, code, b)
		}
		if q := e.queueRowCount(t, n.ID); q != 0 {
			t.Errorf("%s queue rows = %d", ch, q)
		}
	}
}

func TestAdminOutreachHandLinks_ClicksCounted(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	e.beforeEvent()
	w, _ := e.handSetup(t, "")
	e.h.SetEnabledChannels(nil)
	e.handSync(t, w)
	p := e.handPanel(t, w)
	tt, bsky := findPost(t, p, "tiktok", "announce"), findPost(t, p, "bluesky", "announce")
	e.links.statsRollup = &shortlinks.CampaignRollup{ClickCount: 5, ByLink: []shortlinks.LinkBucket{{Key: *tt.ShortLinkKey, Count: 3}, {Key: *bsky.ShortLinkKey, Count: 2}}}
	res := e.results(t, w)
	if res.Totals == nil || res.Totals.Clicks != 5 {
		t.Errorf("totals = %+v", res.Totals)
	}
	got := map[string]int64{}
	for _, c := range res.Channels {
		if c.Clicks != nil {
			got[c.Name] = *c.Clicks
		}
	}
	if got["tiktok"] != 3 || got["bluesky"] != 2 {
		t.Errorf("channel clicks = %v", got)
	}
}

func TestAdminOutreachHandLinks_StartedWorkshopGetsNothing(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	w, oid := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(nil)

	// Existing rows are untouched once the event has started.
	r, _ := e.handSync(t, w)
	if r.Created != 6 || r.Drafted != 4 {
		t.Fatalf("first pass = %+v", r)
	}
	rows, linkRows, calls := e.postRows(t, oid), e.linkRowCount(t, oid), len(e.links.links)
	e.h.now = func() time.Time { return w.StartsAt.Add(time.Minute) }
	r, _ = e.handSync(t, w)
	if r.Created != 0 || r.Drafted != 0 || len(r.Failed) != 0 {
		t.Errorf("started pass = %+v, want nothing done", r)
	}
	if len(e.links.links) != calls {
		t.Errorf("started pass made %d CreateLink calls", len(e.links.links)-calls)
	}
	if n := e.linkRowCount(t, oid); n != linkRows {
		t.Errorf("link rows = %d, want %d", n, linkRows)
	}
	if got := e.postRows(t, oid); !reflect.DeepEqual(got, rows) {
		t.Error("posts changed on a started workshop")
	}
	if !hasPost(e.handPanel(t, w), "tiktok", "announce") {
		t.Error("existing tiktok/announce draft is gone")
	}

	// A past workshop that was never synced.
	w2, oid2 := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(nil)
	e.h.now = func() time.Time { return w2.StartsAt.Add(time.Minute) }
	if e.handPanel(t, w2).HandLinksPending {
		t.Error("pending = true for a started workshop")
	}
	rows2, calls2 := e.postRows(t, oid2), len(e.links.links)
	if r, _ := e.handSync(t, w2); r.Created != 0 {
		t.Errorf("never-synced started pass = %+v", r)
	}
	if len(e.links.links) != calls2 {
		t.Error("never-synced started pass called ShortLinks")
	}
	if !reflect.DeepEqual(e.postRows(t, oid2), rows2) {
		t.Error("posts changed on a never-synced started workshop")
	}

	// Starting exactly now is still upcoming.
	w3, _ := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(nil)
	e.h.now = func() time.Time { return *w3.StartsAt }
	if !e.handPanel(t, w3).HandLinksPending {
		t.Error("pending = false at the exact start time")
	}
	if r, _ := e.handSync(t, w3); r.Created != 6 {
		t.Errorf("start-time pass = %+v, want 6 created", r)
	}
}

// Before #0630 this returned 500 with drafts missing: the cancel stopped the
// link loop and the draft CreatePost failed on the dead context.
func TestAdminOutreachHandLinks_ClientGoneMidPassStillDrafts(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	w, _ := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	e.links.afterCreate = func() {
		n++
		if n == 2 {
			cancel()
		}
	}
	req := httptest.NewRequest(http.MethodPost, e.linksPath(w, "hand-links"), nil).WithContext(ctx)
	req = withCookie(req, "outreach-admin-token")
	rec := httptest.NewRecorder()
	e.srv.Config.Handler.ServeHTTP(rec, req)
	e.links.afterCreate = nil
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	r := decodeOutreach[testHandReport](t, rec.Body.Bytes())
	if r.Created != 6 || r.Drafted != 4 {
		t.Fatalf("report = %+v, want 6 created and 4 drafted", r)
	}
	p := e.handPanel(t, w)
	if p.HandLinksPending {
		t.Error("pending = true after the pass")
	}
	for _, ch := range []string{"tiktok", "youtube"} {
		for _, wv := range []string{"announce", "reminder"} {
			if !hasPost(p, ch, wv) {
				t.Errorf("no %s/%s draft", ch, wv)
			}
		}
	}
}

// This pins the local guarantee only: each slot gets one link and at most one
// draft. An upstream duplicate link is accepted (see EnsureHandLinks).
func TestAdminOutreachHandLinks_OverlappingPassesMakeEachSlotOnce(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	w, oid := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(nil)
	e.links.createDelay = 5 * time.Millisecond

	recs := []*httptest.ResponseRecorder{httptest.NewRecorder(), httptest.NewRecorder()}
	var wg sync.WaitGroup
	for _, rec := range recs {
		req := withCookie(httptest.NewRequest(http.MethodPost, e.linksPath(w, "hand-links"), nil), "outreach-admin-token")
		wg.Add(1)
		go func(rec *httptest.ResponseRecorder, req *http.Request) {
			defer wg.Done()
			e.srv.Config.Handler.ServeHTTP(rec, req)
		}(rec, req)
	}
	wg.Wait()
	created, drafted := 0, 0
	for i, rec := range recs {
		if rec.Code != http.StatusOK {
			t.Fatalf("pass %d status = %d (%s)", i, rec.Code, rec.Body.String())
		}
		r := decodeOutreach[testHandReport](t, rec.Body.Bytes())
		created += r.Created
		drafted += r.Drafted
	}
	if created != 6 || drafted != 4 {
		t.Errorf("summed created=%d drafted=%d, want 6 and 4", created, drafted)
	}
	if n := e.rowCount(t, `SELECT count(*) FROM outreach_links WHERE outreach_id = $1 AND channel IN ('tiktok','youtube')`, oid); n != 6 {
		t.Errorf("video link rows = %d, want 6", n)
	}
	if n := e.rowCount(t, `SELECT count(*) FROM outreach_posts WHERE outreach_id = $1 AND channel IN ('tiktok','youtube')`, oid); n != 4 {
		t.Errorf("video posts = %d, want 4", n)
	}
	if n := e.rowCount(t, `SELECT count(*) FROM (SELECT 1 FROM outreach_posts WHERE outreach_id = $1 GROUP BY channel, wave HAVING count(*) > 1) d`, oid); n != 0 {
		t.Errorf("%d slots have more than one post", n)
	}
}
