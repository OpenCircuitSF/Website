package handlers

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// #0588: Unmark takes back a Mark posted on a channel the site does not post
// to itself, and refuses a post the site sent.
func TestAdminOutreach_UnmarkPosted(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	ig := e.createPost(t, o.ID, "instagram", "announce")
	unmark := func(id int64) (int, []byte) {
		return e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/unmark", id), "")
	}

	if code, _ := unmark(ig.ID); code != http.StatusConflict {
		t.Errorf("unmark a draft = %d, want 409", code)
	}
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", ig.ID), ""); code != http.StatusOK {
		t.Fatalf("prepare = %d %s", code, b)
	}
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/posted", ig.ID), `{"remote_url":"https://instagram.com/p/abc"}`); code != http.StatusOK {
		t.Fatalf("posted = %d %s", code, b)
	}
	code, b := unmark(ig.ID)
	if code != http.StatusOK {
		t.Fatalf("unmark = %d %s", code, b)
	}
	type unmarked struct {
		Status    string  `json:"status"`
		Prepared  bool    `json:"prepared"`
		PostedAt  *string `json:"posted_at"`
		RemoteURL *string `json:"remote_url"`
	}
	got := decodeOutreach[unmarked](t, b)
	if got.Status != "manual" || !got.Prepared || got.PostedAt != nil || got.RemoteURL != nil {
		t.Errorf("after unmark = %+v, want prepared manual with no posted_at or remote_url", got)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostUnmarked, ig.ID); n != 1 {
		t.Errorf("unmarked audit rows = %d, want 1", n)
	}
	if code, _ := unmark(ig.ID); code != http.StatusConflict {
		t.Errorf("second unmark = %d, want 409", code)
	}
	// It can be marked and edited again.
	if code, b := e.do(t, http.MethodPatch, fmt.Sprintf("/admin/outreach-posts/%d", ig.ID), `{"body":"edited again"}`); code != http.StatusOK {
		t.Errorf("edit after unmark = %d %s", code, b)
	}

	// A post the site sent through a configured channel stays posted.
	bs := e.createPost(t, o.ID, "bluesky", "announce")
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", bs.ID), ""); code != http.StatusOK {
		t.Fatalf("prepare bluesky = %d %s", code, b)
	}
	if _, err := outreach.NewStore(e.pool).MarkPosted(context.Background(), bs.ID, nil, time.Now()); err != nil {
		t.Fatalf("mark bluesky posted: %v", err)
	}
	if code, _ := unmark(bs.ID); code != http.StatusConflict {
		t.Errorf("unmark a site-sent post = %d, want 409", code)
	}
	// One the site sent (it carries a remote id) stays posted even when the
	// channel is no longer connected.
	mast := e.createPost(t, o.ID, "mastodon", "announce")
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", mast.ID), ""); code != http.StatusOK {
		t.Fatalf("prepare mastodon = %d %s", code, b)
	}
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE outreach_posts SET status = 'posted', posted_at = now(), remote_id = 'r1', remote_url = 'https://m.example/1' WHERE id = $1`, mast.ID); err != nil {
		t.Fatalf("mark mastodon remote-posted: %v", err)
	}
	if code, _ := unmark(mast.ID); code != http.StatusConflict {
		t.Errorf("unmark a site-sent post on an unconfigured channel = %d, want 409", code)
	}
	if code, _ := unmark(999999); code != http.StatusNotFound {
		t.Errorf("unmark unknown = %d, want 404", code)
	}
}

// #0588: a channel switched off in Settings is not drafted and its drafts are
// not counted or shown; a posted post there stays (public history), and a
// hidden draft returns when the channel is switched back on.
func TestAdminOutreach_DisabledChannels(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	tt := e.createPost(t, o.ID, "tiktok", "announce")
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", tt.ID), ""); code != http.StatusOK {
		t.Fatalf("prepare = %d %s", code, b)
	}
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/posted", tt.ID), ""); code != http.StatusOK {
		t.Fatalf("posted = %d %s", code, b)
	}
	yt := e.createPost(t, o.ID, "youtube", "announce") // a draft, hidden once off

	on := []string{"bluesky", "mastodon", "instagram"}
	e.h.SetEnabledChannels(func() []string { return on })

	panelOf := func() testPanel {
		code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
		if code != http.StatusOK {
			t.Fatalf("panel = %d %s", code, b)
		}
		return decodeOutreach[testPanel](t, b)
	}
	channelNames := func(p testPanel) []string {
		var out []string
		for _, c := range p.Channels {
			out = append(out, fmt.Sprint(c["name"]))
		}
		return out
	}
	p := panelOf()
	if got := fmt.Sprint(channelNames(p)); got != "[bluesky mastodon instagram tiktok]" {
		t.Errorf("channels = %s, want the 3 enabled plus tiktok, which has a posted post", got)
	}
	if len(p.Posts) != 1 || p.Posts[0].ID != tt.ID {
		t.Errorf("posts = %+v, want only the posted tiktok post (the youtube draft hidden)", p.Posts)
	}

	// Draft all skips tiktok and youtube.
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach/draft-all", w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("draft-all = %d %s", code, b)
	}
	if n := len(panelOf().Posts); n != 4 {
		t.Errorf("posts after draft-all = %d, want 3 drafts plus the posted tiktok post", n)
	}
	// A direct create for a disabled channel is refused.
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach/%d/posts", o.ID), `{"channel":"youtube","wave":"reminder"}`); code != http.StatusConflict {
		t.Errorf("create youtube while off = %d, want 409", code)
	}

	// The overview counts the posted tiktok post but not the youtube draft.
	code, b = e.do(t, http.MethodGet, "/admin/outreach-results", "")
	if code != http.StatusOK {
		t.Fatalf("summary = %d %s", code, b)
	}
	type summary struct {
		Rows []struct {
			WorkshopID     int64    `json:"workshop_id"`
			PostsTotal     int      `json:"posts_total"`
			PostsPosted    int      `json:"posts_posted"`
			PostedChannels []string `json:"posted_channels"`
		} `json:"rows"`
		Enabled []string `json:"enabled_channels"`
	}
	sum := decodeOutreach[summary](t, b)
	if len(sum.Enabled) != 3 {
		t.Errorf("enabled_channels = %v", sum.Enabled)
	}
	seen := false
	for _, r := range sum.Rows {
		if r.WorkshopID == w.ID {
			seen = true
			if r.PostsTotal != 4 || r.PostsPosted != 1 || fmt.Sprint(r.PostedChannels) != "[tiktok]" {
				t.Errorf("summary row = %+v, want 4 posts, 1 posted on tiktok", r)
			}
		}
	}
	if !seen {
		t.Error("summary has no row for the workshop")
	}

	// The per-workshop results keep the posted post and drop the draft.
	code, b = e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach/results", w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("results = %d %s", code, b)
	}
	type results struct {
		Channels []struct {
			Name string `json:"name"`
		} `json:"channels"`
		Posts []struct {
			ID int64 `json:"id"`
		} `json:"posts"`
	}
	res := decodeOutreach[results](t, b)
	for _, rp := range res.Posts {
		if rp.ID == yt.ID {
			t.Error("results show the youtube draft of a switched-off channel")
		}
	}
	hasTT, hasYT := false, false
	for _, c := range res.Channels {
		hasTT = hasTT || c.Name == "tiktok"
		hasYT = hasYT || c.Name == "youtube"
	}
	if !hasTT || hasYT {
		t.Errorf("results channels = %+v, want tiktok (posted) and not youtube", res.Channels)
	}

	// The list carries the enabled channels for the Dashboard.
	code, b = e.do(t, http.MethodGet, "/admin/outreach", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d %s", code, b)
	}
	if got := fmt.Sprint(decodeOutreach[struct {
		Enabled []string `json:"enabled_channels"`
	}](t, b).Enabled); got != "[bluesky mastodon instagram]" {
		t.Errorf("list enabled_channels = %s", got)
	}

	// Switched back on, the hidden draft returns.
	on = append(on, "youtube")
	found := false
	for _, post := range panelOf().Posts {
		found = found || post.ID == yt.ID
	}
	if !found {
		t.Error("youtube draft did not return when the channel was switched back on")
	}
}

// #0588: switching off a channel the site posts to never hides a scheduled
// post (it would still go out unseen); it stays shown and actionable, while a
// draft there is hidden and refused by Post now.
func TestAdminOutreach_DisabledAutomaticChannel(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	prepare := func(id int64) {
		t.Helper()
		if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", id), ""); code != http.StatusOK {
			t.Fatalf("prepare = %d %s", code, b)
		}
	}
	sched := e.createPost(t, o.ID, "bluesky", "reminder")
	prepare(sched.ID)
	e.schedulePost(t, sched.ID, time.Now().Add(2*time.Hour))
	draft := e.createPost(t, o.ID, "bluesky", "announce")
	prepare(draft.ID)

	e.h.SetEnabledChannels(func() []string { return []string{"mastodon", "instagram"} })

	code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("panel = %d %s", code, b)
	}
	p := decodeOutreach[testPanel](t, b)
	if len(p.Posts) != 1 || p.Posts[0].ID != sched.ID || p.Posts[0].Status != "scheduled" {
		t.Errorf("posts = %+v, want only the scheduled bluesky post", p.Posts)
	}
	if len(p.Channels) == 0 || p.Channels[0]["name"] != "bluesky" {
		t.Errorf("channels = %v, want bluesky kept for its scheduled post", p.Channels)
	}
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/send", draft.ID), ""); code != http.StatusConflict {
		t.Errorf("post now on a hidden draft = %d, want 409", code)
	}
	if code, b := e.do(t, http.MethodPost, sectionPath(w.ID, "unschedule"), `{"wave":"reminder"}`); code != http.StatusOK {
		t.Errorf("unschedule the shown scheduled post = %d %s", code, b)
	}
}

// testOffPanel decodes only what #0628 adds, leaving testPanel alone.
type testOffPanel struct {
	Channels    []struct{ Name string } `json:"channels"`
	OffChannels []struct{ Name string } `json:"off_channels"`
	Posts       []struct {
		Channel string `json:"channel"`
		Status  string `json:"status"`
	} `json:"posts"`
}

func (e *outreachEnv) offPanel(t *testing.T, workshopID int64) testOffPanel {
	t.Helper()
	code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", workshopID), "")
	if code != http.StatusOK {
		t.Fatalf("panel = %d %s", code, b)
	}
	return decodeOutreach[testOffPanel](t, b)
}

func offNames(p testOffPanel) string {
	names := []string{}
	for _, c := range p.OffChannels {
		names = append(names, c.Name)
	}
	return fmt.Sprint(names)
}

// #0628: the panel names every switched-off channel before Start and after it,
// while channels keeps an off channel only for its visible post.
func TestAdminOutreach_PanelListsOffChannels(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	notStarted := seedOutreachWorkshop(t, e.pool, e.ws, "published")

	on := e.offPanel(t, notStarted.ID)
	if on.OffChannels == nil || len(on.OffChannels) != 0 {
		t.Errorf("everything on: off_channels = %#v, want [] not null", on.OffChannels)
	}

	o := e.start(t, w).Outreach
	tt := e.createPost(t, o.ID, "tiktok", "announce")
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", tt.ID), ""); code != http.StatusOK {
		t.Fatalf("prepare = %d %s", code, b)
	}
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/posted", tt.ID), `{"remote_url":"https://tiktok.com/@a/video/1"}`); code != http.StatusOK {
		t.Fatalf("posted = %d %s", code, b)
	}
	e.h.SetEnabledChannels(func() []string { return []string{"bluesky", "mastodon", "instagram"} })

	if got := offNames(e.offPanel(t, notStarted.ID)); got != "[tiktok youtube]" {
		t.Errorf("before Start: off_channels = %s", got)
	}
	p := e.offPanel(t, w.ID)
	if got := offNames(p); got != "[tiktok youtube]" {
		t.Errorf("after Start: off_channels = %s", got)
	}
	shown := map[string]bool{}
	for _, c := range p.Channels {
		shown[c.Name] = true
	}
	if !shown["tiktok"] || shown["youtube"] {
		t.Errorf("channels = %v, want tiktok kept for its post and youtube gone", shown)
	}
}

// #0628: a posted row keeps an off channel on the board, and that must not
// bring back links, drafts or schedule plans for it.
func TestAdminOutreach_OffChannelWithHistoryGetsNoNewLinksOrDrafts(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	w, oid := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(nil)
	tt := e.createPost(t, oid, "tiktok", "announce")
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", tt.ID), ""); code != http.StatusOK {
		t.Fatalf("prepare = %d %s", code, b)
	}
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/posted", tt.ID), `{"remote_url":"https://tiktok.com/@a/video/1"}`); code != http.StatusOK {
		t.Fatalf("posted = %d %s", code, b)
	}
	e.h.SetEnabledChannels(func() []string { return []string{"bluesky", "mastodon", "instagram", "youtube"} })

	rows := func() int {
		return e.rowCount(t, `SELECT (SELECT count(*) FROM outreach_links WHERE outreach_id = $1 AND channel = 'tiktok') + (SELECT count(*) FROM outreach_posts WHERE outreach_id = $1 AND channel = 'tiktok')`, oid)
	}
	before, calls := rows(), len(e.links.links)
	e.handSync(t, w)
	for _, q := range e.links.links[calls:] {
		if q.UTMSource == "tiktok" {
			t.Errorf("link requested for tiktok: %+v", q)
		}
	}
	if after := rows(); after != before {
		t.Errorf("tiktok rows = %d, was %d", after, before)
	}
	hp := e.handPanel(t, w)
	if hp.HandLinksPending {
		t.Error("the off channel left the panel pending")
	}
	for _, l := range hp.HandLinks {
		if l.Channel == "tiktok" {
			t.Errorf("tiktok hand-link row: %+v", l)
		}
	}
	kept := false
	for _, p := range e.offPanel(t, w.ID).Posts {
		kept = kept || (p.Channel == "tiktok" && p.Status == "posted")
	}
	if !kept {
		t.Error("the posted tiktok row left the panel")
	}
}

// #0628: Prepare on a hidden draft would create a ShortLink for a switched-off
// channel. Switching back on proves the refusal was the guard, not the fixture.
func TestAdminOutreach_PrepareRefusesHiddenDraft(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	_, oid := e.handSetup(t, `{"waves":["reminder"]}`)
	e.h.SetEnabledChannels(nil)
	yt := e.createPost(t, oid, "youtube", "reminder")
	e.h.SetEnabledChannels(func() []string { return []string{"bluesky", "mastodon", "instagram", "tiktok"} })

	path := fmt.Sprintf("/admin/outreach-posts/%d/prepare", yt.ID)
	calls := len(e.links.links)
	if code, b := e.do(t, http.MethodPost, path, ""); code != http.StatusConflict {
		t.Fatalf("prepare while off = %d %s, want 409", code, b)
	}
	if got := len(e.links.links); got != calls {
		t.Errorf("links requested while off: %d, was %d", got, calls)
	}
	e.h.SetEnabledChannels(nil)
	if code, b := e.do(t, http.MethodPost, path, ""); code != http.StatusOK {
		t.Fatalf("prepare once on = %d %s", code, b)
	}
	if got := len(e.links.links); got != calls+1 {
		t.Errorf("links after switching on = %d, want %d", got, calls+1)
	}
}
