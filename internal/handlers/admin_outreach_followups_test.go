package handlers

import (
	"fmt"
	"net/http"
	"testing"
)

type thumbPosting struct {
	fakePosting
	ok bool
}

func (f thumbPosting) ThumbStatus(*string) (bool, string) {
	if f.ok {
		return true, ""
	}
	return false, "No picture: test reason."
}

// #0593: the panel carries the canonical card host and the server's own
// answer on whether the picture would be attached.
func TestAdminOutreach_PanelCardFacts(t *testing.T) {
	for _, ok := range []bool{true, false} {
		e := newOutreachEnvPosting(t, true, thumbPosting{fakePosting{"bluesky": true}, ok})
		w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
		code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
		if code != http.StatusOK {
			t.Fatalf("panel = %d %s", code, b)
		}
		got := decodeOutreach[struct {
			CardHost    string `json:"card_host"`
			ThumbOK     bool   `json:"thumb_ok"`
			ThumbReason string `json:"thumb_reason"`
		}](t, b)
		if (got.ThumbReason == "") != ok {
			t.Errorf("thumb_reason = %q with thumb_ok %v", got.ThumbReason, ok)
		}
		if got.CardHost != "site.example.test" || got.ThumbOK != ok {
			t.Errorf("panel card facts = %+v, want host site.example.test and thumb_ok %v", got, ok)
		}
	}
	// A posting seam that cannot say attaches nothing.
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	_, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
	if decodeOutreach[struct {
		ThumbOK bool `json:"thumb_ok"`
	}](t, b).ThumbOK {
		t.Error("thumb_ok true without a publisher to ask")
	}
}

// #0593: a queued post counts in the overview row.
func TestAdminOutreach_SummaryCountsQueued(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	wid, p := e.preparedPost(t, "bluesky")
	if code, b := e.do(t, http.MethodPost, postPath(p.ID, "send"), ""); code != http.StatusAccepted {
		t.Fatalf("send = %d %s", code, b)
	}
	_, b := e.do(t, http.MethodGet, "/admin/outreach-results", "")
	type row struct {
		WorkshopID int64 `json:"workshop_id"`
		PostsTotal int   `json:"posts_total"`
		Queued     int   `json:"queued"`
	}
	for _, r := range decodeOutreach[struct {
		Rows []row `json:"rows"`
	}](t, b).Rows {
		if r.WorkshopID == wid {
			if r.Queued != 1 {
				t.Errorf("row = %+v, want 1 queued", r)
			}
			return
		}
	}
	t.Error("no row for the workshop")
}

// #0593: the list counts and the links table follow the visible posts. A
// switched-off channel's draft is not counted and has no link row; its posted
// post keeps both.
func TestAdminOutreach_SwitchedOffChannelCountsAndLinks(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	tt := e.createPost(t, o.ID, "tiktok", "announce")
	if code, b := e.do(t, http.MethodPost, postPath(tt.ID, "prepare"), ""); code != http.StatusOK {
		t.Fatalf("prepare = %d %s", code, b)
	}
	if code, b := e.do(t, http.MethodPost, postPath(tt.ID, "posted"), ""); code != http.StatusOK {
		t.Fatalf("posted = %d %s", code, b)
	}
	e.createPost(t, o.ID, "youtube", "announce")
	e.h.SetEnabledChannels(func() []string { return []string{"bluesky", "mastodon", "instagram"} })

	_, b := e.do(t, http.MethodGet, "/admin/outreach", "")
	found := false
	for _, r := range decodeOutreach[struct {
		Outreach []struct {
			WorkshopID int64          `json:"workshop_id"`
			Counts     map[string]int `json:"counts"`
		} `json:"outreach"`
	}](t, b).Outreach {
		if r.WorkshopID == w.ID {
			found = true
			total := 0
			for _, n := range r.Counts {
				total += n
			}
			if total != 1 || r.Counts["posted"] != 1 {
				t.Errorf("counts = %v, want only the posted tiktok post", r.Counts)
			}
		}
	}
	if !found {
		t.Fatal("list has no row for the workshop")
	}

	code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach/links", w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("links = %d %s", code, b)
	}
	var tiktok, youtube, instagram int
	for _, l := range decodeOutreach[testLinksView](t, b).Links {
		if l.Kind != "post" {
			continue
		}
		switch l.Channel {
		case "tiktok":
			tiktok++
			if l.Wave != "announce" {
				t.Errorf("tiktok row for wave %q, want only the posted announce", l.Wave)
			}
		case "youtube":
			youtube++
		case "instagram":
			instagram++
		}
	}
	if tiktok != 1 || youtube != 0 || instagram == 0 {
		t.Errorf("link rows tiktok=%d youtube=%d instagram=%d, want 1, 0 and the enabled channel's rows", tiktok, youtube, instagram)
	}
}
