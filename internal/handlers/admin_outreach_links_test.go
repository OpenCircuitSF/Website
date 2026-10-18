package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

type testLinkReport struct {
	Created  int `json:"created"`
	Existing int `json:"existing"`
	Failed   []struct {
		Kind   string `json:"kind"`
		Label  string `json:"label"`
		Reason string `json:"reason"`
	} `json:"failed"`
	Stopped bool `json:"stopped"`
}

type testLinkRow struct {
	Kind           string  `json:"kind"`
	Channel        string  `json:"channel"`
	Wave           string  `json:"wave"`
	LocationCode   *string `json:"location_code"`
	ShortLinkKey   *string `json:"short_link_key"`
	ShortURL       *string `json:"short_url"`
	DestinationURL string  `json:"destination_url"`
	Clicks         *int64  `json:"clicks"`
}

type testLinksView struct {
	Available    bool            `json:"available"`
	ClicksStatus string          `json:"clicks_status"`
	Missing      int             `json:"missing"`
	Links        []testLinkRow   `json:"links"`
	Report       *testLinkReport `json:"report"`
}

type testStartPanel struct {
	testPanel
	LinkReport *testLinkReport `json:"link_report"`
}

// linkLocations creates two active locations and one inactive one, before
// any workshop so their cleanup runs after the workshop's (the flyer-link
// foreign key is RESTRICT).
func (e *outreachEnv) linkLocations(t *testing.T) (a, b, off testLocation) {
	t.Helper()
	mk := func(name string) testLocation {
		return e.createLocation(t, fmt.Sprintf(`{"name":%q,"code":%q}`, name, locTestCode()))
	}
	a, b, off = mk("Mission Library"), mk("Cafe Zed"), mk("Closed Shop")
	if _, err := e.pool.Exec(context.Background(), `UPDATE outreach_locations SET active = false WHERE id = $1`, off.ID); err != nil {
		t.Fatal(err)
	}
	return a, b, off
}

func (e *outreachEnv) linksPath(w workshops.Workshop, suffix string) string {
	return fmt.Sprintf("/admin/workshops/%d/outreach/%s", w.ID, suffix)
}

func (e *outreachEnv) postLinksView(t *testing.T, method string, w workshops.Workshop) testLinksView {
	t.Helper()
	code, b := e.do(t, method, e.linksPath(w, "links"), "")
	if code != http.StatusOK {
		t.Fatalf("%s links = %d (%s)", method, code, b)
	}
	return decodeOutreach[testLinksView](t, b)
}

func (e *outreachEnv) linkRowCount(t *testing.T, outreachID int64) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outreach_links WHERE outreach_id = $1`, outreachID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *outreachEnv) flyerLinkCount(t *testing.T, outreachID, locationID int64) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outreach_flyer_links WHERE outreach_id = $1 AND location_id = $2`, outreachID, locationID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAdminOutreachLinks_StartCreatesEveryLink(t *testing.T) {
	e := newOutreachEnv(t, true)
	locA, locB, locOff := e.linkLocations(t)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")

	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
	if code != http.StatusCreated {
		t.Fatalf("start = %d (%s)", code, b)
	}
	p := decodeOutreach[testStartPanel](t, b)
	if p.LinkReport == nil || len(p.LinkReport.Failed) != 0 || p.LinkReport.Created < 17 {
		t.Fatalf("link_report = %+v", p.LinkReport)
	}
	oid := p.Outreach.ID
	if n := e.linkRowCount(t, oid); n != 15 {
		t.Errorf("outreach_links rows = %d, want 15", n)
	}
	if e.flyerLinkCount(t, oid, locA.ID) != 1 || e.flyerLinkCount(t, oid, locB.ID) != 1 || e.flyerLinkCount(t, oid, locOff.ID) != 0 {
		t.Error("flyer links: want one per active location and none for the inactive one")
	}
	var posts, flyers int
	var sawPost, sawFlyer bool
	for _, req := range e.links.links {
		if req.Key != "" {
			t.Errorf("request sets a custom Key %q (turns de-duplication off)", req.Key)
		}
		if req.CampaignSlug != p.Outreach.CampaignSlug {
			t.Errorf("campaign = %q, want %q", req.CampaignSlug, p.Outreach.CampaignSlug)
		}
		if req.UTMSource == "flyer" {
			flyers++
			if req.UTMContent == locA.Code {
				sawFlyer = true
				want := fmt.Sprintf("https://site.example.test/workshops/%s?utm_campaign=%s&utm_content=%s&utm_medium=print&utm_source=flyer", w.Slug, w.Slug, locA.Code)
				if req.DestinationURL != want {
					t.Errorf("flyer destination = %q, want %q", req.DestinationURL, want)
				}
			}
			continue
		}
		posts++
		if req.UTMSource == "bluesky" && req.UTMContent == "announce" {
			sawPost = true
			want := fmt.Sprintf("https://site.example.test/workshops/%s?utm_campaign=%s&utm_content=announce&utm_medium=social&utm_source=bluesky", w.Slug, w.Slug)
			if req.DestinationURL != want || req.UTMMedium != "social" || req.UTMCampaign != w.Slug || req.Placement != "bluesky" {
				t.Errorf("post request = %+v, want destination %q", req, want)
			}
		}
	}
	if posts != 15 || flyers < 2 || !sawPost || !sawFlyer {
		t.Errorf("CreateLink calls: posts=%d flyers=%d sawPost=%v sawFlyer=%v", posts, flyers, sawPost, sawFlyer)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = $1`, audit.ActionOutreachLinkCreated).Scan(&n)
	if n != 15 {
		t.Errorf("outreach_link.created audit rows = %d, want 15", n)
	}
	// Start on an existing outreach makes no further ShortLinks call.
	before := len(e.links.links)
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), ""); code != http.StatusOK {
		t.Errorf("second start = %d", code)
	}
	if len(e.links.links) != before {
		t.Error("second Start created links")
	}
}

func TestAdminOutreachLinks_Idempotent(t *testing.T) {
	e := newOutreachEnv(t, true)
	e.linkLocations(t)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	p := e.startHTTP(t, w)
	before := len(e.links.links)

	v := e.postLinksView(t, http.MethodPost, w)
	if v.Report == nil || v.Report.Created != 0 || v.Report.Existing < 17 || len(v.Report.Failed) != 0 {
		t.Fatalf("re-run report = %+v", v.Report)
	}
	if len(e.links.links) != before {
		t.Errorf("re-run made %d CreateLink calls", len(e.links.links)-before)
	}

	var key string
	if err := e.pool.QueryRow(context.Background(),
		`DELETE FROM outreach_links WHERE outreach_id = $1 AND channel = 'tiktok' AND wave = 'reminder' RETURNING short_link_key`, p.Outreach.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	v = e.postLinksView(t, http.MethodPost, w)
	if v.Report.Created != 1 {
		t.Fatalf("after deleting one row, created = %d, want 1", v.Report.Created)
	}
	var again string
	_ = e.pool.QueryRow(context.Background(),
		`SELECT short_link_key FROM outreach_links WHERE outreach_id = $1 AND channel = 'tiktok' AND wave = 'reminder'`, p.Outreach.ID).Scan(&again)
	if again != key {
		t.Errorf("restored key = %q, want the de-duplicated %q", again, key)
	}
}

func TestAdminOutreachLinks_AdoptsPostLink(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	post := e.createPost(t, o.ID, "bluesky", "announce")
	code, b := e.do(t, http.MethodPost, postPath(post.ID, "prepare"), "")
	if code != http.StatusOK {
		t.Fatalf("prepare = %d (%s)", code, b)
	}
	prepared := decodeOutreach[testPost](t, b)
	// Simulate a post prepared before slot links existed.
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM outreach_links WHERE outreach_id = $1`, o.ID); err != nil {
		t.Fatal(err)
	}
	before := len(e.links.links)
	v := e.postLinksView(t, http.MethodPost, w)
	for _, req := range e.links.links[before:] {
		if req.UTMSource == "bluesky" && req.UTMContent == "announce" {
			t.Error("the prepared slot made a CreateLink call")
		}
	}
	var key string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT short_link_key FROM outreach_links WHERE outreach_id = $1 AND channel = 'bluesky' AND wave = 'announce'`, o.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if prepared.ShortLinkKey == nil || key != *prepared.ShortLinkKey {
		t.Errorf("adopted key = %q, want %v", key, prepared.ShortLinkKey)
	}
	if v.Report.Existing < 1 {
		t.Errorf("adopted link not counted as existing: %+v", v.Report)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = $1`, audit.ActionOutreachLinkCreated).Scan(&n)
	if n != 14 {
		t.Errorf("outreach_link.created audit rows = %d, want 14 (an adopted row is not a creation)", n)
	}
}

func TestAdminOutreachLinks_PartialFailure(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	e.links.linkErrFor = map[string]error{"mastodon/reminder": errors.New("HTTP 500: secret-upstream-detail")}

	code, b := e.do(t, http.MethodPost, e.linksPath(w, "links"), "")
	if code != http.StatusOK {
		t.Fatalf("status = %d (%s)", code, b)
	}
	if strings.Contains(string(b), "secret-upstream-detail") {
		t.Error("upstream error text reached the response")
	}
	v := decodeOutreach[testLinksView](t, b)
	if v.Report == nil || len(v.Report.Failed) != 1 || v.Report.Failed[0].Label != "Mastodon · Reminder" ||
		v.Report.Failed[0].Reason != "failed" || v.Report.Stopped {
		t.Fatalf("report = %+v", v.Report)
	}
	if n := e.linkRowCount(t, o.ID); n != 14 {
		t.Errorf("saved link rows = %d, want 14", n)
	}
	if v.Missing < 1 {
		t.Errorf("missing = %d", v.Missing)
	}

	e.links.linkErrFor = nil
	v = e.postLinksView(t, http.MethodPost, w)
	if v.Report.Created != 1 || len(v.Report.Failed) != 0 {
		t.Errorf("retry report = %+v, want exactly the one missing link", v.Report)
	}
	if n := e.linkRowCount(t, o.ID); n != 15 {
		t.Errorf("link rows after retry = %d, want 15", n)
	}
}

func TestAdminOutreachLinks_RateLimitStops(t *testing.T) {
	e := newOutreachEnv(t, true)
	e.linkLocations(t)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	e.links.linkErr = fmt.Errorf("create link: %w", shortlinks.ErrRateLimited)

	v := e.postLinksView(t, http.MethodPost, w)
	if v.Report == nil || !v.Report.Stopped || v.Report.Created != 0 || len(v.Report.Failed) < 17 {
		t.Fatalf("report = %+v", v.Report)
	}
	for _, f := range v.Report.Failed {
		if f.Reason != "rate_limited" {
			t.Errorf("%s reason = %q, want rate_limited", f.Label, f.Reason)
		}
	}
	if e.linkRowCount(t, o.ID) != 0 {
		t.Error("links saved despite every call failing")
	}
}

func TestAdminOutreachLinks_Guards(t *testing.T) {
	off := newOutreachEnv(t, false)
	w0 := seedOutreachWorkshop(t, off.pool, off.ws, "published")
	if code, _ := off.do(t, http.MethodPost, off.linksPath(w0, "links"), ""); code != http.StatusServiceUnavailable {
		t.Errorf("not configured = %d, want 503", code)
	}

	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	if code, _ := e.do(t, http.MethodPost, e.linksPath(w, "links"), ""); code != http.StatusConflict {
		t.Errorf("not started = %d, want 409", code)
	}
	for _, suffix := range []string{"draft-all", "schedule-all"} {
		if code, _ := e.do(t, http.MethodPost, e.linksPath(w, suffix), `{"dry_run":true}`); code != http.StatusConflict {
			t.Errorf("%s not started = %d, want 409", suffix, code)
		}
	}
	e.start(t, w)
	e.setWorkshopStatus(t, w.ID, "draft")
	if code, _ := e.do(t, http.MethodPost, e.linksPath(w, "links"), ""); code != http.StatusConflict {
		t.Errorf("unpublished = %d, want 409", code)
	}
	if code, _ := e.do(t, http.MethodPost, e.linksPath(w, "schedule-all"), `{"dry_run":true}`); code != http.StatusConflict {
		t.Errorf("schedule-all unpublished = %d, want 409", code)
	}
	for _, path := range []string{e.linksPath(w, "links"), e.linksPath(w, "draft-all"), e.linksPath(w, "schedule-all")} {
		for token, want := range map[string]int{"": http.StatusUnauthorized, "outreach-user-token": http.StatusForbidden} {
			resp := doJSON(t, e.srv.Client(), http.MethodPost, e.srv.URL+path, token, "")
			resp.Body.Close()
			if resp.StatusCode != want {
				t.Errorf("POST %s token %q = %d, want %d", path, token, resp.StatusCode, want)
			}
		}
	}
}

func TestAdminOutreachLinks_View(t *testing.T) {
	e := newOutreachEnv(t, true)
	locA, _, locOff := e.linkLocations(t)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")

	// Not started.
	v := e.postLinksView(t, http.MethodGet, w)
	if len(v.Links) != 0 || v.ClicksStatus != "not_started" || !v.Available {
		t.Errorf("not started view = %+v", v)
	}

	e.start(t, w)
	v = e.postLinksView(t, http.MethodGet, w)
	if len(v.Links) < 17 || v.Missing != len(v.Links) {
		t.Fatalf("before create: rows=%d missing=%d", len(v.Links), v.Missing)
	}
	if v.Links[0].Channel != "bluesky" || v.Links[0].Wave != "announce" || v.Links[14].Channel != "youtube" || v.Links[14].Wave != "last-call" {
		t.Errorf("post row order wrong: first=%+v last=%+v", v.Links[0], v.Links[14])
	}
	for _, r := range v.Links[15:] {
		if r.Kind != "flyer" {
			t.Errorf("row after the 15 posts is %q, want flyer", r.Kind)
		}
		if r.LocationCode != nil && *r.LocationCode == locOff.Code {
			t.Error("inactive location without a link is listed")
		}
	}

	e.postLinksView(t, http.MethodPost, w)
	v0 := e.postLinksView(t, http.MethodGet, w)
	if v0.Missing != 0 {
		t.Fatalf("missing after create = %d", v0.Missing)
	}
	// The stats cache memoizes, so the clicks check runs on a cold handler;
	// the fake hands out k1, k2, ... in creation order, bluesky announce first.
	e3 := newOutreachEnv(t, true)
	e3.links.statsRollup = &shortlinks.CampaignRollup{ByLink: []shortlinks.LinkBucket{{Key: "k1", Count: 4}, {Key: "k2", Count: 2}}}
	w3 := seedOutreachWorkshop(t, e3.pool, e3.ws, "published")
	e3.startHTTP(t, w3)
	cv := e3.postLinksView(t, http.MethodGet, w3)
	clicks := func(i int) int64 {
		if cv.Links[i].Clicks == nil {
			t.Fatalf("row %d has no clicks", i)
		}
		return *cv.Links[i].Clicks
	}
	if cv.ClicksStatus != "ok" || clicks(0) != 4 || clicks(1) != 2 || clicks(2) != 0 {
		t.Errorf("clicks = %d %d %d status=%s", clicks(0), clicks(1), clicks(2), cv.ClicksStatus)
	}
	v = v0
	var sawFlyer bool
	for _, r := range v.Links {
		if r.LocationCode != nil && *r.LocationCode == locA.Code {
			sawFlyer = r.ShortURL != nil && strings.HasPrefix(*r.ShortURL, "https://go.example.test/")
		}
	}
	if !sawFlyer {
		t.Error("flyer row for the active location has no short URL")
	}

	// A fresh environment so the stats cache is cold.
	e2 := newOutreachEnv(t, true)
	w2 := seedOutreachWorkshop(t, e2.pool, e2.ws, "published")
	e2.startHTTP(t, w2)
	e2.links.statsErr = errors.New("boom")
	v = e2.postLinksView(t, http.MethodGet, w2)
	if v.ClicksStatus != "unreachable" {
		t.Errorf("clicks_status = %q, want unreachable", v.ClicksStatus)
	}
	for _, r := range v.Links {
		if r.Clicks != nil {
			t.Fatal("a click count is set although stats failed")
		}
	}
}

func TestAdminOutreachLinks_PrepareAndCreatePostUseTheRow(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	p := e.startHTTP(t, w)
	oid := p.Outreach.ID

	// CreatePost applies the slot's row.
	bs := e.createPost(t, oid, "bluesky", "reminder")
	if !bs.Prepared || bs.ShortURL == nil || strings.Contains(bs.Body, *bs.ShortURL) || bs.Status != "draft" {
		t.Errorf("created post = %+v, want prepared, with no URL in the Bluesky body (the card carries it, #0571)", bs)
	}
	ig := e.createPost(t, oid, "instagram", "reminder")
	if !ig.Prepared || ig.Status != "manual" {
		t.Errorf("manual post = %+v, want prepared and manual", ig)
	}

	// Prepare on a bare post (made behind the handler's back) uses the row too.
	bare, err := e.h.store.CreatePost(context.Background(), oid, "mastodon", "last-call", "Body {link}", "fp")
	if err != nil {
		t.Fatal(err)
	}
	me, links := e.links.meCalls, len(e.links.links)
	code, b := e.do(t, http.MethodPost, postPath(bare.ID, "prepare"), "")
	if code != http.StatusOK {
		t.Fatalf("prepare = %d (%s)", code, b)
	}
	got := decodeOutreach[testPost](t, b)
	if e.links.meCalls != me || len(e.links.links) != links {
		t.Error("Prepare made ShortLinks calls although the slot already had a link")
	}
	var want string
	_ = e.pool.QueryRow(context.Background(),
		`SELECT short_url FROM outreach_links WHERE outreach_id = $1 AND channel = 'mastodon' AND wave = 'last-call'`, oid).Scan(&want)
	if got.ShortURL == nil || *got.ShortURL != want || !strings.Contains(got.Body, want) {
		t.Errorf("prepared URL = %v, want %q", got.ShortURL, want)
	}
}

type testDraftAll struct {
	Created []struct {
		PostID  int64 `json:"post_id"`
		HasLink bool  `json:"has_link"`
	} `json:"created"`
	Skipped     int `json:"skipped"`
	WithoutLink int `json:"without_link"`
}

func TestAdminOutreachLinks_DraftAll(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	p := e.startHTTP(t, w)
	oid := p.Outreach.ID

	edited := e.createPost(t, oid, "bluesky", "announce")
	code, b := e.do(t, http.MethodPatch, fmt.Sprintf("/admin/outreach-posts/%d", edited.ID), `{"body":"hand edited text"}`)
	if code != http.StatusOK {
		t.Fatalf("patch = %d (%s)", code, b)
	}
	before := len(e.links.links)
	code, b = e.do(t, http.MethodPost, e.linksPath(w, "draft-all"), "")
	if code != http.StatusOK {
		t.Fatalf("draft-all = %d (%s)", code, b)
	}
	d := decodeOutreach[testDraftAll](t, b)
	if len(d.Created) != 14 || d.Skipped != 1 || d.WithoutLink != 0 {
		t.Fatalf("draft-all = %+v", d)
	}
	for _, c := range d.Created {
		if !c.HasLink {
			t.Errorf("post %d has no link", c.PostID)
		}
	}
	if len(e.links.links) != before {
		t.Error("draft-all called ShortLinks")
	}
	var body string
	_ = e.pool.QueryRow(context.Background(), `SELECT body FROM outreach_posts WHERE id = $1`, edited.ID).Scan(&body)
	if body != "hand edited text" {
		t.Errorf("edited draft overwritten: %q", body)
	}
	_, b = e.do(t, http.MethodPost, e.linksPath(w, "draft-all"), "")
	d = decodeOutreach[testDraftAll](t, b)
	if len(d.Created) != 0 || d.Skipped != 15 {
		t.Errorf("second run = %+v, want 0 created and 15 skipped", d)
	}

	// Without links: drafted with the placeholder, counted.
	w2 := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o2 := e.start(t, w2).Outreach
	_, b = e.do(t, http.MethodPost, e.linksPath(w2, "draft-all"), "")
	d = decodeOutreach[testDraftAll](t, b)
	if len(d.Created) != 15 || d.WithoutLink != 15 {
		t.Fatalf("bare draft-all = %+v", d)
	}
	var withToken int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outreach_posts WHERE outreach_id = $1 AND position('{link}' in body) > 0`, o2.ID).Scan(&withToken)
	if withToken != 9 { // Bluesky and Instagram drafts carry no link (#0570)
		t.Errorf("bodies carrying {link} = %d, want 9", withToken)
	}
}

type testScheduleAll struct {
	DryRun     bool `json:"dry_run"`
	ToSchedule []struct {
		PostID      int64  `json:"post_id"`
		Channel     string `json:"channel"`
		Wave        string `json:"wave"`
		ScheduledAt string `json:"scheduled_at"`
	} `json:"to_schedule"`
	Skipped []struct {
		PostID      *int64  `json:"post_id"`
		Channel     string  `json:"channel"`
		Wave        string  `json:"wave"`
		Reason      string  `json:"reason"`
		SuggestedAt *string `json:"suggested_at"`
	} `json:"skipped"`
}

func TestAdminOutreachLinks_ScheduleAll(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	starts := *w.StartsAt
	e.h.now = func() time.Time { return starts.AddDate(0, 0, -3) } // reminder passed, last call ahead
	p := e.startHTTP(t, w)
	oid := p.Outreach.ID
	if code, b := e.do(t, http.MethodPost, e.linksPath(w, "draft-all"), ""); code != http.StatusOK {
		t.Fatalf("draft-all = %d (%s)", code, b)
	}
	statuses := func() map[string]string {
		rows, err := e.pool.Query(context.Background(), `SELECT channel || '/' || wave, status FROM outreach_posts WHERE outreach_id = $1`, oid)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var k, s string
			_ = rows.Scan(&k, &s)
			out[k] = s
		}
		return out
	}
	before := statuses()

	code, b := e.do(t, http.MethodPost, e.linksPath(w, "schedule-all"), `{"dry_run":true}`)
	if code != http.StatusOK {
		t.Fatalf("dry run = %d (%s)", code, b)
	}
	d := decodeOutreach[testScheduleAll](t, b)
	wantAt := starts.AddDate(0, 0, -1).UTC().Format(time.RFC3339)
	if !d.DryRun || len(d.ToSchedule) != 1 || d.ToSchedule[0].Channel != "bluesky" || d.ToSchedule[0].Wave != "last-call" || d.ToSchedule[0].ScheduledAt != wantAt {
		t.Fatalf("to_schedule = %+v, want only bluesky last-call at %s", d.ToSchedule, wantAt)
	}
	reasons := map[string]string{}
	for _, s := range d.Skipped {
		if s.Channel == "instagram" || s.Channel == "tiktok" || s.Channel == "youtube" {
			t.Errorf("manual channel listed: %+v", s)
		}
		reasons[s.Channel+"/"+s.Wave] = s.Reason
		if s.Reason == "time_passed" && s.SuggestedAt == nil {
			t.Error("time_passed carries no suggested time")
		}
	}
	for k, want := range map[string]string{
		"bluesky/reminder": "time_passed", "bluesky/announce": "no_suggested_time",
		"mastodon/announce": "not_connected", "mastodon/reminder": "not_connected", "mastodon/last-call": "not_connected",
	} {
		if reasons[k] != want {
			t.Errorf("skip %s = %q, want %q", k, reasons[k], want)
		}
	}
	after := statuses()
	for k, v := range before {
		if after[k] != v {
			t.Errorf("dry run changed %s: %s -> %s", k, v, after[k])
		}
	}

	if code, _ := e.do(t, http.MethodPost, e.linksPath(w, "schedule-all"), `{"dry_run":false,"post_ids":[]}`); code != http.StatusBadRequest {
		t.Errorf("empty post_ids = %d, want 400", code)
	}

	lastCall := d.ToSchedule[0].PostID
	code, b = e.do(t, http.MethodPost, e.linksPath(w, "schedule-all"), fmt.Sprintf(`{"dry_run":false,"post_ids":[%d]}`, lastCall))
	if code != http.StatusOK {
		t.Fatalf("confirm = %d (%s)", code, b)
	}
	c := decodeOutreach[testScheduleAll](t, b)
	if c.DryRun || len(c.ToSchedule) != 1 || c.ToSchedule[0].PostID != lastCall {
		t.Fatalf("confirm = %+v", c)
	}
	var gotAt time.Time
	_ = e.pool.QueryRow(context.Background(), `SELECT scheduled_at FROM outreach_posts WHERE id = $1`, lastCall).Scan(&gotAt)
	if !gotAt.Equal(starts.AddDate(0, 0, -1)) {
		t.Errorf("scheduled_at = %v, want %v", gotAt, starts.AddDate(0, 0, -1))
	}
	final := statuses()
	for k, v := range before {
		want := v
		if k == "bluesky/last-call" {
			want = "scheduled"
		}
		if final[k] != want {
			t.Errorf("%s = %s, want %s", k, final[k], want)
		}
	}
	var n int
	_ = e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND metadata->>'bulk' = 'true'`, audit.ActionOutreachPostScheduled).Scan(&n)
	if n != 1 {
		t.Errorf("bulk outreach_post.scheduled audit rows = %d, want 1", n)
	}

	// An id that is no longer in the plan comes back as changed.
	var reminder int64
	_ = e.pool.QueryRow(context.Background(), `SELECT id FROM outreach_posts WHERE outreach_id = $1 AND channel = 'bluesky' AND wave = 'reminder'`, oid).Scan(&reminder)
	_, b = e.do(t, http.MethodPost, e.linksPath(w, "schedule-all"), fmt.Sprintf(`{"dry_run":false,"post_ids":[%d,%d]}`, lastCall, reminder))
	c = decodeOutreach[testScheduleAll](t, b)
	if len(c.ToSchedule) != 0 || len(c.Skipped) != 2 || c.Skipped[0].Reason != "changed" || c.Skipped[1].Reason != "changed" {
		t.Errorf("stale ids = %+v", c)
	}
}
