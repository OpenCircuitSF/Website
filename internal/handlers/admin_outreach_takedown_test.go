package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// fakeTakedownPosting is a configured channel set whose remote delete answers
// as scripted. Nothing here reaches a network (#0604).
type fakeTakedownPosting struct {
	fakePosting
	mu      sync.Mutex
	existed bool
	err     error
	calls   []outreach.Post
}

func (f *fakeTakedownPosting) DeleteRemote(_ context.Context, p outreach.Post) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, p)
	return f.existed, f.err
}

type takedownView struct {
	Status    string  `json:"status"`
	Body      string  `json:"body"`
	ShortURL  *string `json:"short_url"`
	PostedAt  *string `json:"posted_at"`
	RemoteURL *string `json:"remote_url"`
	Sched     *string `json:"scheduled_at"`
	LastError *string `json:"last_error"`
}

// seedPostedBluesky leaves a prepared Bluesky post posted, the way production
// has it after #0603: short_url repaired to /u/, body still the old URL.
func seedPostedBluesky(t *testing.T, e *outreachEnv, channel string) (workshopID, postID int64, short, old string) {
	t.Helper()
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	p := e.createPost(t, o.ID, channel, "announce")
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", p.ID), ""); code != http.StatusOK {
		t.Fatalf("prepare = %d %s", code, b)
	}
	if err := e.pool.QueryRow(context.Background(), `SELECT short_url FROM outreach_posts WHERE id = $1`, p.ID).Scan(&short); err != nil {
		t.Fatal(err)
	}
	// short is already host/u/KEY (#0603); the old body form drops the /u/.
	old = strings.Replace(short, "/u/", "/", 1)
	if old == short {
		t.Fatalf("short url %q is not in the /u/ form", short)
	}
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE outreach_posts SET status = 'posted', posted_at = now(), body = 'Come along '||$2, remote_id = 'at://did:plc:x/app.bsky.feed.post/abc', remote_url = 'https://bsky.app/profile/x/post/abc', last_error = 'old' WHERE id = $1`,
		p.ID, old); err != nil {
		t.Fatal(err)
	}
	return w.ID, p.ID, short, old
}

func TestAdminOutreach_Takedown(t *testing.T) {
	fp := &fakeTakedownPosting{fakePosting: fakePosting{"bluesky": true, "mastodon": true}, existed: true}
	e := newOutreachEnvPosting(t, true, fp)
	wid, id, short, old := seedPostedBluesky(t, e, "bluesky")
	url := fmt.Sprintf("/admin/outreach-posts/%d/takedown", id)

	code, b := e.do(t, http.MethodPost, url, "")
	if code != http.StatusOK {
		t.Fatalf("takedown = %d %s", code, b)
	}
	got := decodeOutreach[takedownView](t, b)
	if got.Status != "draft" || got.PostedAt != nil || got.RemoteURL != nil || got.Sched != nil || got.LastError != nil {
		t.Errorf("view = %+v, want a clean draft", got)
	}
	if got.Body != "Come along "+short || strings.Contains(got.Body, old+" ") {
		t.Errorf("body = %q, want the current short link %q", got.Body, short)
	}
	if len(fp.calls) != 1 || fp.calls[0].RemoteID == nil || *fp.calls[0].RemoteID != "at://did:plc:x/app.bsky.feed.post/abc" {
		t.Errorf("remote delete calls = %+v", fp.calls)
	}
	var gen int
	if err := e.pool.QueryRow(context.Background(), `SELECT attempt_generation FROM outreach_posts WHERE id = $1`, id).Scan(&gen); err != nil || gen != 1 {
		t.Errorf("attempt_generation = %d, %v", gen, err)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostTakenDown, id); n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
	var meta string
	if err := e.pool.QueryRow(context.Background(), `SELECT metadata::text FROM audit_log WHERE action = $1 AND target_id = $2`, audit.ActionOutreachPostTakenDown, id).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(meta, "https://bsky.app/profile/x/post/abc") || !strings.Contains(meta, `"already_gone": false`) {
		t.Errorf("audit metadata = %s", meta)
	}
	// A second click finds a draft.
	if code, _ := e.do(t, http.MethodPost, url, ""); code != http.StatusConflict {
		t.Errorf("second takedown = %d, want 409", code)
	}
	// Nothing is posted now, so the panel offers Reset again.
	code, b = e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", wid), "")
	if code != http.StatusOK {
		t.Fatalf("get = %d %s", code, b)
	}
	if panel := decodeOutreach[resetPanel](t, b); !panel.CanReset {
		t.Error("can_reset = false after the only posted post was taken down")
	}
}

func TestAdminOutreach_Takedown_AlreadyGoneIsSuccess(t *testing.T) {
	fp := &fakeTakedownPosting{fakePosting: fakePosting{"bluesky": true}, existed: false}
	e := newOutreachEnvPosting(t, true, fp)
	_, id, _, _ := seedPostedBluesky(t, e, "bluesky")
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/takedown", id), "")
	if code != http.StatusOK {
		t.Fatalf("takedown of an already-deleted post = %d %s", code, b)
	}
	if got := decodeOutreach[takedownView](t, b); got.Status != "draft" {
		t.Errorf("status = %q", got.Status)
	}
	var meta string
	if err := e.pool.QueryRow(context.Background(), `SELECT metadata::text FROM audit_log WHERE action = $1 AND target_id = $2`, audit.ActionOutreachPostTakenDown, id).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(meta, `"already_gone": true`) {
		t.Errorf("audit metadata = %s", meta)
	}
}

func TestAdminOutreach_Takedown_RemoteFailureKeepsItPosted(t *testing.T) {
	fp := &fakeTakedownPosting{fakePosting: fakePosting{"bluesky": true}, err: errors.New("bluesky: HTTP 500: boom")}
	e := newOutreachEnvPosting(t, true, fp)
	_, id, _, old := seedPostedBluesky(t, e, "bluesky")
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/takedown", id), "")
	if code != http.StatusBadGateway {
		t.Fatalf("takedown = %d %s, want 502", code, b)
	}
	if !strings.Contains(string(b), "still marked posted") || !strings.Contains(string(b), "boom") {
		t.Errorf("message = %s", b)
	}
	p, _ := outreach.NewStore(e.pool).GetPost(context.Background(), id)
	if p.Status != "posted" || p.RemoteID == nil || p.AttemptGeneration != 0 || !strings.Contains(p.Body, old) {
		t.Errorf("row changed despite the failure: %+v", p)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostTakenDown, id); n != 0 {
		t.Errorf("audit rows = %d, want 0", n)
	}
}

func TestAdminOutreach_Takedown_Refusals(t *testing.T) {
	fp := &fakeTakedownPosting{fakePosting: fakePosting{"bluesky": true}, existed: true}
	e := newOutreachEnvPosting(t, true, fp)
	wid, id, _, _ := seedPostedBluesky(t, e, "bluesky")
	oid := outreachIDForWorkshop(t, e, wid)
	post := func(id int64) int {
		code, _ := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/takedown", id), "")
		return code
	}
	if code := post(999999999); code != http.StatusNotFound {
		t.Errorf("missing post = %d, want 404", code)
	}

	// A hand-posted manual channel keeps Unmark.
	ig := e.createPost(t, oid, "instagram", "announce")
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", ig.ID), ""); code != http.StatusOK {
		t.Fatalf("prepare = %d %s", code, b)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE outreach_posts SET status = 'posted', posted_at = now() WHERE id = $1`, ig.ID); err != nil {
		t.Fatal(err)
	}
	if code := post(ig.ID); code != http.StatusConflict {
		t.Errorf("manual channel = %d, want 409", code)
	}

	// A channel that is not connected cannot be deleted from here.
	ma := e.createPost(t, oid, "mastodon", "announce")
	if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", ma.ID), ""); code != http.StatusOK {
		t.Fatalf("prepare = %d %s", code, b)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE outreach_posts SET status = 'posted', posted_at = now(), remote_id = '1' WHERE id = $1`, ma.ID); err != nil {
		t.Fatal(err)
	}
	if code := post(ma.ID); code != http.StatusConflict {
		t.Errorf("unconfigured channel = %d, want 409", code)
	}

	// A draft is not posted.
	dr := e.createPost(t, oid, "bluesky", "reminder")
	if code := post(dr.ID); code != http.StatusConflict {
		t.Errorf("draft = %d, want 409", code)
	}
	if len(fp.calls) != 0 {
		t.Errorf("remote delete reached for a refused post: %d calls", len(fp.calls))
	}

	// A live queue row blocks the takedown before anything is deleted.
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO outbound_queue (kind, recipient, payload, status) VALUES ('outreach_post', $1, $2::jsonb, 'queued')`,
		fmt.Sprintf("outreach_post:%d", id), fmt.Sprintf(`{"post_id": %d}`, id)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE recipient = $1`, fmt.Sprintf("outreach_post:%d", id))
	})
	if code := post(id); code != http.StatusConflict {
		t.Errorf("live queue row = %d, want 409", code)
	}
	if len(fp.calls) != 0 {
		t.Errorf("remote delete reached with a live queue row: %d calls", len(fp.calls))
	}
}

func outreachIDForWorkshop(t *testing.T, e *outreachEnv, workshopID int64) int64 {
	t.Helper()
	var id int64
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM outreach WHERE workshop_id = $1`, workshopID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
