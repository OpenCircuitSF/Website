package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// #0592: Reset removes a workshop's posts and flyers while nothing is public.

func resetPath(workshopID int64) string {
	return fmt.Sprintf("/admin/workshops/%d/outreach/reset", workshopID)
}

func (e *outreachEnv) rowCount(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type resetPanel struct {
	testPanel
	CanReset bool `json:"can_reset"`
}

func TestAdminOutreachReset_AuthAndNotStarted(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	for token, want := range map[string]int{"": http.StatusUnauthorized, "outreach-user-token": http.StatusForbidden} {
		resp := doJSON(t, e.srv.Client(), http.MethodPost, e.srv.URL+resetPath(w.ID), token, "")
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("token %q = %d, want %d", token, resp.StatusCode, want)
		}
	}
	if code, b := e.do(t, http.MethodPost, resetPath(w.ID), ""); code != http.StatusNotFound {
		t.Errorf("reset before start = %d (%s), want 404", code, b)
	}
}

func TestAdminOutreachReset_SuccessKeepsLinksAndStartStillWorks(t *testing.T) {
	e := newFlyerEnv(t, true, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	p := e.startHTTP(t, w)
	if code, b := e.do(t, http.MethodPost, e.linksPath(w, "draft-all"), ""); code != http.StatusOK {
		t.Fatalf("draft-all = %d (%s)", code, b)
	}
	v := e.flyerFor2(t, p.Outreach.ID, e.locs[0])
	if code, _, b := e.generate(t, v.Flyer.ID); code != http.StatusOK {
		t.Fatalf("generate = %d (%s)", code, b)
	}
	// A finished queue row left by an earlier failed send goes with its post.
	var postID int64
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM outreach_posts WHERE outreach_id = $1 LIMIT 1`, p.Outreach.ID).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO outbound_queue (kind, recipient, payload, status) VALUES ('outreach_post', $1, jsonb_build_object('post_id', $2::bigint), 'abandoned')`,
		fmt.Sprintf("outreach_post:%d", postID), postID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE kind = 'outreach_post'`)
	})

	_, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
	if got := decodeOutreach[resetPanel](t, b); !got.CanReset || len(got.Posts) == 0 {
		t.Fatalf("before reset: can_reset=%v posts=%d", got.CanReset, len(got.Posts))
	}
	oid := p.Outreach.ID
	linksBefore := e.rowCount(t, `SELECT count(*) FROM outreach_links WHERE outreach_id = $1`, oid)
	flyerLinksBefore := e.rowCount(t, `SELECT count(*) FROM outreach_flyer_links WHERE outreach_id = $1`, oid)
	if linksBefore == 0 || flyerLinksBefore == 0 {
		t.Fatalf("setup: links=%d flyer links=%d", linksBefore, flyerLinksBefore)
	}
	created := len(e.links.links)

	code, b := e.do(t, http.MethodPost, resetPath(w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("reset = %d (%s)", code, b)
	}
	got := decodeOutreach[resetPanel](t, b)
	if len(got.Posts) != 0 || !got.CanReset || got.Outreach == nil {
		t.Errorf("response = %+v, want no posts, can_reset, outreach kept", got)
	}
	if n := e.rowCount(t, `SELECT count(*) FROM outreach_posts WHERE outreach_id = $1`, oid); n != 0 {
		t.Errorf("posts left = %d", n)
	}
	if n := e.rowCount(t, `SELECT count(*) FROM outreach_flyers WHERE outreach_id = $1`, oid); n != 0 {
		t.Errorf("flyers left = %d", n)
	}
	if n := e.rowCount(t, `SELECT count(*) FROM outbound_queue WHERE recipient = $1`, fmt.Sprintf("outreach_post:%d", postID)); n != 0 {
		t.Errorf("queue rows left = %d", n)
	}
	if n := e.rowCount(t, `SELECT count(*) FROM outreach_links WHERE outreach_id = $1`, oid); n != linksBefore {
		t.Errorf("slot links = %d, want %d kept", n, linksBefore)
	}
	if n := e.rowCount(t, `SELECT count(*) FROM outreach_flyer_links WHERE outreach_id = $1`, oid); n != flyerLinksBefore {
		t.Errorf("flyer links = %d, want %d kept", n, flyerLinksBefore)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachReset, oid); n != 1 {
		t.Errorf("reset audit rows = %d, want 1", n)
	}

	// Start after Reset succeeds, and Draft all reuses the kept links: no new
	// ShortLinks link is created, so there is nothing to collide.
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), ""); code != http.StatusOK {
		t.Fatalf("start after reset = %d (%s)", code, b)
	}
	if code, b := e.do(t, http.MethodPost, e.linksPath(w, "draft-all"), ""); code != http.StatusOK {
		t.Fatalf("draft-all after reset = %d (%s)", code, b)
	}
	if code, b := e.do(t, http.MethodPost, e.linksPath(w, "links"), ""); code != http.StatusOK {
		t.Fatalf("create links after reset = %d (%s)", code, b)
	}
	if len(e.links.links) != created {
		t.Errorf("ShortLinks links created after reset: %d new", len(e.links.links)-created)
	}
	if n := e.rowCount(t, `SELECT count(*) FROM outreach_links WHERE outreach_id = $1`, oid); n != linksBefore {
		t.Errorf("slot links after redraft = %d, want %d", n, linksBefore)
	}
}

func (e *flyerEnv) flyerFor2(t *testing.T, outreachID int64, locs ...outreach.Location) testFlyerView {
	t.Helper()
	code, b := e.do(t, http.MethodPost, "/admin/outreach-flyers", fmt.Sprintf(`{"outreach_id":%d,"template":"split","tagline":"x"}`, outreachID))
	if code != http.StatusCreated {
		t.Fatalf("create flyer = %d (%s)", code, b)
	}
	v := decodeOutreach[testFlyerView](t, b)
	e.selectLocations(t, v.Flyer.ID, locs...)
	return v
}

func TestAdminOutreachReset_RefusesWhenPublic(t *testing.T) {
	assertRefused := func(t *testing.T, e *outreachEnv, workshopID, outreachID int64, wantMsg string) {
		t.Helper()
		before := e.rowCount(t, `SELECT count(*) FROM outreach_posts WHERE outreach_id = $1`, outreachID)
		code, b := e.do(t, http.MethodPost, resetPath(workshopID), "")
		if code != http.StatusConflict || !strings.Contains(string(b), wantMsg) {
			t.Fatalf("reset = %d (%s), want 409 containing %q", code, b, wantMsg)
		}
		if n := e.rowCount(t, `SELECT count(*) FROM outreach_posts WHERE outreach_id = $1`, outreachID); n != before {
			t.Errorf("refused reset changed posts: %d -> %d", before, n)
		}
		if n := outreachAuditCount(t, e.pool, audit.ActionOutreachReset, outreachID); n != 0 {
			t.Errorf("refused reset wrote %d audit rows", n)
		}
		_, pb := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", workshopID), "")
		if got := decodeOutreach[resetPanel](t, pb); got.CanReset {
			t.Error("can_reset = true while something is public")
		}
	}

	t.Run("posted post", func(t *testing.T) {
		e := newOutreachEnv(t, true)
		wid, p := e.preparedPost(t, "instagram")
		if code, b := e.do(t, http.MethodPost, postPath(p.ID, "posted"), ""); code != http.StatusOK {
			t.Fatalf("mark posted = %d (%s)", code, b)
		}
		var oid int64
		_ = e.pool.QueryRow(context.Background(), `SELECT outreach_id FROM outreach_posts WHERE id = $1`, p.ID).Scan(&oid)
		assertRefused(t, e, wid, oid, "already been posted")
	})
	t.Run("queued post", func(t *testing.T) {
		e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
		wid, p := e.preparedPost(t, "bluesky")
		if code, b := e.do(t, http.MethodPost, postPath(p.ID, "send"), ""); code != http.StatusAccepted {
			t.Fatalf("send = %d (%s)", code, b)
		}
		var oid int64
		_ = e.pool.QueryRow(context.Background(), `SELECT outreach_id FROM outreach_posts WHERE id = $1`, p.ID).Scan(&oid)
		assertRefused(t, e, wid, oid, "queued or being sent")
		if n := e.queueRowCount(t, p.ID); n != 1 {
			t.Errorf("queue rows = %d, want 1 untouched", n)
		}
	})
	// The post's own queued status refuses on its own, with no live queue row
	// behind it (review of #0592: the subtest above also has a live row, so
	// it could not tell the two conditions apart).
	t.Run("queued post without a live queue row", func(t *testing.T) {
		e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
		wid, p := e.preparedPost(t, "bluesky")
		if code, b := e.do(t, http.MethodPost, postPath(p.ID, "send"), ""); code != http.StatusAccepted {
			t.Fatalf("send = %d (%s)", code, b)
		}
		if _, err := e.pool.Exec(context.Background(),
			`UPDATE outbound_queue SET status = 'abandoned' WHERE kind = 'outreach_post' AND (payload->>'post_id')::bigint = $1`, p.ID); err != nil {
			t.Fatal(err)
		}
		var oid int64
		_ = e.pool.QueryRow(context.Background(), `SELECT outreach_id FROM outreach_posts WHERE id = $1`, p.ID).Scan(&oid)
		assertRefused(t, e, wid, oid, "queued or being sent")
	})
	t.Run("live queue row on a draft", func(t *testing.T) {
		e := newOutreachEnv(t, true)
		wid, p := e.preparedPost(t, "instagram")
		var oid int64
		_ = e.pool.QueryRow(context.Background(), `SELECT outreach_id FROM outreach_posts WHERE id = $1`, p.ID).Scan(&oid)
		if _, err := e.pool.Exec(context.Background(),
			`INSERT INTO outbound_queue (kind, recipient, payload, status) VALUES ('outreach_post', $1, jsonb_build_object('post_id', $2::bigint), 'sending')`,
			fmt.Sprintf("outreach_post:%d", p.ID), p.ID); err != nil {
			t.Fatal(err)
		}
		assertRefused(t, e, wid, oid, "queued or being sent")
	})
	t.Run("flyer put up", func(t *testing.T) {
		e := newFlyerEnv(t, true, 1)
		w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
		v := e.flyerFor(t, w, e.locs[0])
		code, got, b := e.generate(t, v.Flyer.ID)
		if code != http.StatusOK {
			t.Fatalf("generate = %d (%s)", code, b)
		}
		if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-flyer-links/%d/put-up", got.Selection[0].Link.ID), ""); code != http.StatusOK {
			t.Fatalf("put up = %d (%s)", code, b)
		}
		assertRefused(t, e.outreachEnv, w.ID, v.Flyer.OutreachID, "marked put up")
		if n := e.rowCount(t, `SELECT count(*) FROM outreach_flyers WHERE id = $1`, v.Flyer.ID); n != 1 {
			t.Errorf("refused reset removed the flyer")
		}
	})
}
