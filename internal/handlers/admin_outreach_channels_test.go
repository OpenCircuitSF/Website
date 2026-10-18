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
	if code, _ := unmark(999999); code != http.StatusNotFound {
		t.Errorf("unmark unknown = %d, want 404", code)
	}
}

// #0588: a channel switched off in Settings is not drafted, not counted and
// not shown; what it already had is kept and returns when it is switched on.
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

	on := []string{"bluesky", "mastodon", "instagram"}
	e.h.SetEnabledChannels(func() []string { return on })

	panelOf := func() testPanel {
		code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
		if code != http.StatusOK {
			t.Fatalf("panel = %d %s", code, b)
		}
		return decodeOutreach[testPanel](t, b)
	}
	p := panelOf()
	if len(p.Channels) != 3 {
		t.Errorf("channels = %d, want the 3 enabled", len(p.Channels))
	}
	if len(p.Posts) != 0 {
		t.Errorf("posts = %d, want the disabled tiktok post hidden", len(p.Posts))
	}

	// Draft all skips tiktok and youtube.
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach/draft-all", w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("draft-all = %d %s", code, b)
	}
	for _, post := range panelOf().Posts {
		if post.Channel == "tiktok" || post.Channel == "youtube" {
			t.Errorf("draft-all drafted %s", post.Channel)
		}
	}
	if n := len(panelOf().Posts); n != 3 {
		t.Errorf("posts after draft-all = %d, want 3", n)
	}
	// A direct create for a disabled channel is refused.
	if code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach/%d/posts", o.ID), `{"channel":"youtube","wave":"announce"}`); code != http.StatusConflict {
		t.Errorf("create youtube while off = %d, want 409", code)
	}

	// The overview does not count or name the disabled channel's post.
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
	for _, r := range sum.Rows {
		if r.WorkshopID == w.ID && (r.PostsTotal != 3 || r.PostsPosted != 0 || len(r.PostedChannels) != 0) {
			t.Errorf("summary row = %+v, want 3 posts, none posted", r)
		}
	}

	// Switched back on, the kept post returns.
	on = append(on, "tiktok")
	found := false
	for _, post := range panelOf().Posts {
		found = found || (post.Channel == "tiktok" && post.Status == "posted")
	}
	if !found {
		t.Error("tiktok post did not return when the channel was switched back on")
	}
}
