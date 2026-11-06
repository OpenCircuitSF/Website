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
	e.beforeEvent()
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
				want := fmt.Sprintf("https://site.example.test/events/%s?utm_campaign=%s&utm_content=%s&utm_medium=print&utm_source=flyer", w.Slug, w.Slug, locA.Code)
				if req.DestinationURL != want {
					t.Errorf("flyer destination = %q, want %q", req.DestinationURL, want)
				}
			}
			continue
		}
		posts++
		if req.UTMSource == "bluesky" && req.UTMContent == "announce" {
			sawPost = true
			want := fmt.Sprintf("https://site.example.test/events/%s?utm_campaign=%s&utm_content=announce&utm_medium=social&utm_source=bluesky", w.Slug, w.Slug)
			if req.DestinationURL != want || req.UTMMedium != "social" || req.UTMCampaign != w.Slug || req.Placement != "bluesky" {
				t.Errorf("post request = %+v, want destination %q", req, want)
			}
		}
	}
	if posts != 15 || flyers < 2 || !sawPost || !sawFlyer {
		t.Errorf("CreateLink calls: posts=%d flyers=%d sawPost=%v sawFlyer=%v", posts, flyers, sawPost, sawFlyer)
	}
	// #0603: every stored short URL is the redirect form host/u/KEY, never host/KEY.
	var badPost, badFlyer int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outreach_links WHERE short_url <> 'https://go.example.test/u/' || short_link_key`).Scan(&badPost); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outreach_flyer_links WHERE short_url <> 'https://go.example.test/u/' || short_link_key`).Scan(&badFlyer); err != nil {
		t.Fatal(err)
	}
	if badPost != 0 || badFlyer != 0 {
		t.Errorf("short_url without /u/: outreach_links=%d outreach_flyer_links=%d", badPost, badFlyer)
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

func TestAdminOutreachLinks_StartSkipsHandChannelsOnceStarted(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	e.h.now = func() time.Time { return w.StartsAt.Add(time.Hour) }
	p := e.startHTTP(t, w)
	if n := e.linkRowCount(t, p.Outreach.ID); n != 6 {
		t.Errorf("outreach_links rows = %d, want 6 (automatic channels only)", n)
	}
	for _, req := range e.links.links {
		switch req.UTMSource {
		case "instagram", "tiktok", "youtube":
			t.Errorf("started workshop made a %s/%s link", req.UTMSource, req.UTMContent)
		}
	}
}

func TestAdminOutreachLinks_Idempotent(t *testing.T) {
	e := newOutreachEnv(t, true)
	e.beforeEvent()
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	p := e.startHTTP(t, w)
	before := len(e.links.links)

	r, _ := e.handSync(t, w)
	if r.Created != 0 || r.Existing != 9 || len(r.Failed) != 0 {
		t.Fatalf("re-run report = %+v", r)
	}
	if len(e.links.links) != before {
		t.Errorf("re-run made %d CreateLink calls", len(e.links.links)-before)
	}

	var key string
	if err := e.pool.QueryRow(context.Background(),
		`DELETE FROM outreach_links WHERE outreach_id = $1 AND channel = 'tiktok' AND wave = 'reminder' RETURNING short_link_key`, p.Outreach.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	r, _ = e.handSync(t, w)
	if r.Created != 1 {
		t.Fatalf("after deleting one row, created = %d, want 1", r.Created)
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
	e.beforeEvent()
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	post := e.createPost(t, o.ID, "tiktok", "announce")
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
	r, _ := e.handSync(t, w)
	for _, req := range e.links.links[before:] {
		if req.UTMSource == "tiktok" && req.UTMContent == "announce" {
			t.Error("the prepared slot made a CreateLink call")
		}
	}
	var key string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT short_link_key FROM outreach_links WHERE outreach_id = $1 AND channel = 'tiktok' AND wave = 'announce'`, o.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if prepared.ShortLinkKey == nil || key != *prepared.ShortLinkKey {
		t.Errorf("adopted key = %q, want %v", key, prepared.ShortLinkKey)
	}
	if r.Existing < 1 {
		t.Errorf("adopted link not counted as existing: %+v", r)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = $1`, audit.ActionOutreachLinkCreated).Scan(&n)
	if n != 8 {
		t.Errorf("outreach_link.created audit rows = %d, want 8 (an adopted row is not a creation)", n)
	}
}

func TestAdminOutreachLinks_PartialFailure(t *testing.T) {
	e := newOutreachEnv(t, true)
	e.beforeEvent()
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	e.links.linkErrFor = map[string]error{"tiktok/reminder": errors.New("HTTP 500: secret-upstream-detail")}

	code, b := e.do(t, http.MethodPost, e.linksPath(w, "hand-links"), "")
	if code != http.StatusOK {
		t.Fatalf("status = %d (%s)", code, b)
	}
	if strings.Contains(string(b), "secret-upstream-detail") {
		t.Error("upstream error text reached the response")
	}
	r := decodeOutreach[testHandReport](t, b)
	if len(r.Failed) != 1 || r.Failed[0].Label != "TikTok · Reminder" || r.Failed[0].Reason != "failed" || r.Stopped {
		t.Fatalf("report = %+v", r)
	}
	if n := e.linkRowCount(t, o.ID); n != 8 {
		t.Errorf("saved link rows = %d, want 8", n)
	}

	e.links.linkErrFor = nil
	r, _ = e.handSync(t, w)
	if r.Created != 1 || len(r.Failed) != 0 {
		t.Errorf("retry report = %+v, want exactly the one missing link", r)
	}
	if n := e.linkRowCount(t, o.ID); n != 9 {
		t.Errorf("link rows after retry = %d, want 9", n)
	}
}

func TestAdminOutreachLinks_RateLimitStops(t *testing.T) {
	e := newOutreachEnv(t, true)
	e.beforeEvent()
	e.linkLocations(t)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	e.links.linkErr = fmt.Errorf("create link: %w", shortlinks.ErrRateLimited)

	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
	if code != http.StatusCreated {
		t.Fatalf("start = %d (%s)", code, b)
	}
	p := decodeOutreach[testStartPanel](t, b)
	if p.LinkReport == nil || !p.LinkReport.Stopped || p.LinkReport.Created != 0 || len(p.LinkReport.Failed) < 17 {
		t.Fatalf("report = %+v", p.LinkReport)
	}
	for _, f := range p.LinkReport.Failed {
		if f.Reason != "rate_limited" {
			t.Errorf("%s reason = %q, want rate_limited", f.Label, f.Reason)
		}
	}
	if e.linkRowCount(t, p.Outreach.ID) != 0 {
		t.Error("links saved despite every call failing")
	}
}

func TestAdminOutreachLinks_Guards(t *testing.T) {
	off := newOutreachEnv(t, false)
	w0 := seedOutreachWorkshop(t, off.pool, off.ws, "published")
	if code, _ := off.do(t, http.MethodPost, off.linksPath(w0, "hand-links"), ""); code != http.StatusServiceUnavailable {
		t.Errorf("not configured = %d, want 503", code)
	}

	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	if code, _ := e.do(t, http.MethodPost, e.linksPath(w, "hand-links"), ""); code != http.StatusConflict {
		t.Errorf("not started = %d, want 409", code)
	}
	for _, suffix := range []string{"draft-all"} {
		if code, _ := e.do(t, http.MethodPost, e.linksPath(w, suffix), `{"dry_run":true}`); code != http.StatusConflict {
			t.Errorf("%s not started = %d, want 409", suffix, code)
		}
	}
	e.start(t, w)
	e.setWorkshopStatus(t, w.ID, "draft")
	if code, _ := e.do(t, http.MethodPost, e.linksPath(w, "hand-links"), ""); code != http.StatusConflict {
		t.Errorf("unpublished = %d, want 409", code)
	}
	for _, path := range []string{e.linksPath(w, "hand-links"), e.linksPath(w, "draft-all")} {
		for token, want := range map[string]int{"": http.StatusUnauthorized, "outreach-user-token": http.StatusForbidden} {
			resp := doJSON(t, e.srv.Client(), http.MethodPost, e.srv.URL+path, token, "")
			resp.Body.Close()
			if resp.StatusCode != want {
				t.Errorf("POST %s token %q = %d, want %d", path, token, resp.StatusCode, want)
			}
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
		PostID  int64  `json:"post_id"`
		Wave    string `json:"wave"`
		HasLink bool   `json:"has_link"`
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
	if len(d.Created) != 4 || d.Skipped != 1 || d.WithoutLink != 0 { // Announce only by default (#0574)
		t.Fatalf("draft-all = %+v", d)
	}
	for _, c := range d.Created {
		if c.Wave != "announce" {
			t.Errorf("default draft-all drafted %s", c.Wave)
		}
	}
	if code, _ := e.do(t, http.MethodPost, e.linksPath(w, "draft-all"), `{"waves":["nonsense"]}`); code != http.StatusBadRequest {
		t.Errorf("unknown wave = %d, want 400", code)
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
	if len(d.Created) != 0 || d.Skipped != 5 {
		t.Errorf("second run = %+v, want 0 created and 5 skipped", d)
	}
	_, b = e.do(t, http.MethodPost, e.linksPath(w, "draft-all"), `{"waves":["reminder","last-call"]}`)
	d = decodeOutreach[testDraftAll](t, b)
	if len(d.Created) != 10 || d.Skipped != 5 {
		t.Errorf("with the optional waves = %+v, want 10 created and 5 skipped", d)
	}

	// Without links: drafted with the placeholder, counted.
	w2 := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o2 := e.start(t, w2).Outreach
	_, b = e.do(t, http.MethodPost, e.linksPath(w2, "draft-all"), `{"waves":["reminder","last-call"]}`)
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
