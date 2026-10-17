package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

// None of these tests can post anywhere: the handler only queues, and the
// publisher is replaced by fakePosting, which just reports which channels
// are configured (#0545).

type postingPostView struct {
	ID          int64   `json:"id"`
	Status      string  `json:"status"`
	Held        bool    `json:"held"`
	ScheduledAt *string `json:"scheduled_at"`
	LastError   *string `json:"last_error"`
}

func (e *outreachEnv) setWorkshopStatus(t *testing.T, workshopID int64, status string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE workshops SET status = $2 WHERE id = $1`, workshopID, status); err != nil {
		t.Fatalf("set workshop status: %v", err)
	}
}

// preparedPost seeds a published workshop with outreach and one prepared post
// on channel.
func (e *outreachEnv) preparedPost(t *testing.T, channel string) (int64, testPost) {
	t.Helper()
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	p := e.createPost(t, o.ID, channel, "announce")
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", p.ID), "")
	if code != http.StatusOK {
		t.Fatalf("prepare = %d (%s)", code, b)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE kind = 'outreach_post'`)
	})
	return w.ID, decodeOutreach[testPost](t, b)
}

func (e *outreachEnv) queueRowCount(t *testing.T, postID int64) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE recipient = $1`, fmt.Sprintf("outreach_post:%d", postID)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func postPath(id int64, action string) string {
	return fmt.Sprintf("/admin/outreach-posts/%d/%s", id, action)
}

func TestAdminOutreachPosting_PanelReportsConfiguredAndHeld(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	wid, p := e.preparedPost(t, "bluesky")
	code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", wid), "")
	if code != http.StatusOK {
		t.Fatalf("get = %d", code)
	}
	panel := decodeOutreach[testPanel](t, b)
	want := map[string]bool{"bluesky": true, "mastodon": false, "instagram": false, "tiktok": false, "youtube": false}
	for _, ch := range panel.Channels {
		name, _ := ch["name"].(string)
		if got, _ := ch["configured"].(bool); got != want[name] {
			t.Errorf("channel %s configured = %v, want %v", name, got, want[name])
		}
	}
	if _, err := e.schedule(t, p.ID, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status string
		held   bool
	}{{"published", false}, {"canceled", true}, {"draft", true}} {
		e.setWorkshopStatus(t, wid, tc.status)
		_, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", wid), "")
		var panel struct {
			Posts []postingPostView `json:"posts"`
		}
		panel = decodeOutreach[struct {
			Posts []postingPostView `json:"posts"`
		}](t, b)
		if len(panel.Posts) != 1 || panel.Posts[0].Held != tc.held || panel.Posts[0].Status != "scheduled" {
			t.Errorf("workshop %s: posts = %+v, want held=%v", tc.status, panel.Posts, tc.held)
		}
	}
}

func (e *outreachEnv) schedule(t *testing.T, id int64, at time.Time) (int, error) {
	t.Helper()
	code, b := e.do(t, http.MethodPost, postPath(id, "schedule"), fmt.Sprintf(`{"scheduled_at":%q}`, at.UTC().Format(time.RFC3339)))
	if code != http.StatusOK {
		return code, fmt.Errorf("schedule = %d (%s)", code, b)
	}
	return code, nil
}

func TestAdminOutreachPosting_SendQueuesAndLocks(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	_, p := e.preparedPost(t, "bluesky")

	code, b := e.do(t, http.MethodPost, postPath(p.ID, "send"), "")
	if code != http.StatusAccepted {
		t.Fatalf("send = %d (%s)", code, b)
	}
	if got := decodeOutreach[postingPostView](t, b); got.Status != "queued" {
		t.Errorf("status = %q", got.Status)
	}
	if n := e.queueRowCount(t, p.ID); n != 1 {
		t.Errorf("queue rows = %d, want 1", n)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostQueued, p.ID); n != 1 {
		t.Errorf("queued audit rows = %d, want 1", n)
	}
	var retry bool
	if err := e.pool.QueryRow(context.Background(), `SELECT (metadata->>'retry')::bool FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionOutreachPostQueued, p.ID).Scan(&retry); err != nil || retry {
		t.Errorf("retry metadata = %v err=%v, want false", retry, err)
	}

	// In flight: every mutating call is refused with the same 409.
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, postPath(p.ID, "send"), ""},
		{http.MethodPatch, fmt.Sprintf("/admin/outreach-posts/%d", p.ID), `{"body":"changed"}`},
		{http.MethodPatch, fmt.Sprintf("/admin/outreach-posts/%d", p.ID), `{"regenerate":true}`},
		{http.MethodPost, postPath(p.ID, "posted"), ""},
		{http.MethodDelete, fmt.Sprintf("/admin/outreach-posts/%d", p.ID), ""},
		{http.MethodPost, postPath(p.ID, "unschedule"), ""},
	} {
		code, b := e.do(t, tc.method, tc.path, tc.body)
		if code != http.StatusConflict || !strings.Contains(string(b), "being sent") {
			t.Errorf("%s %s = %d (%s), want 409 being sent", tc.method, tc.path, code, b)
		}
	}
	if n := e.queueRowCount(t, p.ID); n != 1 {
		t.Errorf("refused calls changed the queue: %d rows", n)
	}
}

func TestAdminOutreachPosting_SendRefusals(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})

	// Not configured automatic channel -> 503; manual channel -> 400.
	_, masto := e.preparedPost(t, "mastodon")
	if code, b := e.do(t, http.MethodPost, postPath(masto.ID, "send"), ""); code != http.StatusServiceUnavailable || !strings.Contains(string(b), "Mastodon is not configured") {
		t.Errorf("unconfigured send = %d (%s)", code, b)
	}
	_, insta := e.preparedPost(t, "instagram")
	if code, _ := e.do(t, http.MethodPost, postPath(insta.ID, "send"), ""); code != http.StatusBadRequest {
		t.Errorf("manual channel send = %d, want 400", code)
	}

	// Not prepared -> 409.
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	raw := e.createPost(t, o.ID, "bluesky", "announce")
	if code, _ := e.do(t, http.MethodPost, postPath(raw.ID, "send"), ""); code != http.StatusConflict {
		t.Errorf("unprepared send = %d, want 409", code)
	}

	// Over the limit -> 400 naming count and limit; nothing queued.
	wid, long := e.preparedPost(t, "bluesky")
	if code, b := e.do(t, http.MethodPatch, fmt.Sprintf("/admin/outreach-posts/%d", long.ID), fmt.Sprintf(`{"body":%q}`, strings.Repeat("x", 301))); code != http.StatusOK {
		t.Fatalf("patch = %d (%s)", code, b)
	}
	code, b := e.do(t, http.MethodPost, postPath(long.ID, "send"), "")
	if code != http.StatusBadRequest || !strings.Contains(string(b), "301") || !strings.Contains(string(b), "300") {
		t.Errorf("over-limit send = %d (%s)", code, b)
	}
	if n := e.queueRowCount(t, long.ID); n != 0 {
		t.Errorf("a refused send queued %d rows", n)
	}

	// Unpublished workshop -> 409 for send and schedule.
	_, held := e.preparedPost(t, "bluesky")
	var heldWorkshop int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT o.workshop_id FROM outreach_posts p JOIN outreach o ON o.id = p.outreach_id WHERE p.id = $1`, held.ID).Scan(&heldWorkshop); err != nil {
		t.Fatal(err)
	}
	e.setWorkshopStatus(t, heldWorkshop, "canceled")
	if code, _ := e.do(t, http.MethodPost, postPath(held.ID, "send"), ""); code != http.StatusConflict {
		t.Errorf("canceled workshop send = %d, want 409", code)
	}
	if _, err := e.schedule(t, held.ID, time.Now().Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "409") {
		t.Errorf("canceled workshop schedule: %v, want 409", err)
	}
	_ = wid

	if code, _ := e.do(t, http.MethodPost, postPath(held.ID+999999, "send"), ""); code != http.StatusNotFound {
		t.Errorf("unknown post send = %d, want 404", code)
	}
}

func TestAdminOutreachPosting_RetryOfFailedPost(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	_, p := e.preparedPost(t, "bluesky")
	if _, err := e.pool.Exec(context.Background(), `UPDATE outreach_posts SET status = 'failed', last_error = 'boom' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	code, b := e.do(t, http.MethodPost, postPath(p.ID, "send"), "")
	if code != http.StatusAccepted {
		t.Fatalf("retry = %d (%s)", code, b)
	}
	got := decodeOutreach[postingPostView](t, b)
	if got.Status != "queued" || got.LastError != nil {
		t.Errorf("post = %+v", got)
	}
	var retry bool
	if err := e.pool.QueryRow(context.Background(), `SELECT (metadata->>'retry')::bool FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionOutreachPostQueued, p.ID).Scan(&retry); err != nil || !retry {
		t.Errorf("retry metadata = %v err=%v, want true", retry, err)
	}
}

func TestAdminOutreachPosting_ScheduleBoundsAndUnschedule(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	_, p := e.preparedPost(t, "bluesky")
	path := postPath(p.ID, "schedule")
	for name, body := range map[string]string{
		"not json":       `nope`,
		"missing":        `{}`,
		"bad format":     `{"scheduled_at":"tomorrow"}`,
		"past":           fmt.Sprintf(`{"scheduled_at":%q}`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)),
		"under a minute": fmt.Sprintf(`{"scheduled_at":%q}`, time.Now().Add(20*time.Second).UTC().Format(time.RFC3339)),
		"over a year":    fmt.Sprintf(`{"scheduled_at":%q}`, time.Now().Add(400*24*time.Hour).UTC().Format(time.RFC3339)),
	} {
		if code, b := e.do(t, http.MethodPost, path, body); code != http.StatusBadRequest {
			t.Errorf("%s: %d (%s), want 400", name, code, b)
		}
	}
	at := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	code, b := e.do(t, http.MethodPost, path, fmt.Sprintf(`{"scheduled_at":%q}`, at.UTC().Format(time.RFC3339)))
	if code != http.StatusOK {
		t.Fatalf("schedule = %d (%s)", code, b)
	}
	got := decodeOutreach[postingPostView](t, b)
	if got.Status != "scheduled" || got.ScheduledAt == nil || *got.ScheduledAt != at.UTC().Format(time.RFC3339) {
		t.Errorf("post = %+v", got)
	}
	if n := e.queueRowCount(t, p.ID); n != 0 {
		t.Errorf("scheduling enqueued %d rows; the publisher enqueues when due", n)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostScheduled, p.ID); n != 1 {
		t.Errorf("scheduled audit rows = %d", n)
	}
	// Rescheduling is the same call.
	if _, err := e.schedule(t, p.ID, at.Add(time.Hour)); err != nil {
		t.Errorf("reschedule: %v", err)
	}

	code, b = e.do(t, http.MethodPost, postPath(p.ID, "unschedule"), "")
	if code != http.StatusOK {
		t.Fatalf("unschedule = %d (%s)", code, b)
	}
	if got := decodeOutreach[postingPostView](t, b); got.Status != "draft" || got.ScheduledAt != nil {
		t.Errorf("unscheduled post = %+v", got)
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostUnscheduled, p.ID); n != 1 {
		t.Errorf("unscheduled audit rows = %d", n)
	}
	if code, _ := e.do(t, http.MethodPost, postPath(p.ID, "unschedule"), ""); code != http.StatusConflict {
		t.Errorf("unschedule a draft = %d, want 409", code)
	}
}

// TestAdminOutreachPosting_RacingPreparesAuditOnce is the #0544 review
// follow-up: two Prepares that both pass the already-prepared check and both
// hold the SAME key (ShortLinks de-duplicates) must write one audit row.
func TestAdminOutreachPosting_RacingPreparesAuditOnce(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	p := e.createPost(t, o.ID, "instagram", "announce")

	var barrier sync.WaitGroup
	barrier.Add(2)
	e.links.mu.Lock()
	e.links.fixedKey = "samekey"
	e.links.createBarrier = &barrier
	e.links.mu.Unlock()

	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = e.do(t, http.MethodPost, postPath(p.ID, "prepare"), "")
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("prepare %d = %d", i, c)
		}
	}
	if n := outreachAuditCount(t, e.pool, audit.ActionOutreachPostPrepared, p.ID); n != 1 {
		t.Errorf("prepared audit rows = %d, want exactly 1", n)
	}
}

// meLinker counts concurrent Me calls so a test can tell whether the handler
// holds its mutex across the network call.
type meLinker struct {
	*fakeShortLinker
	mu      sync.Mutex
	calls   int
	entered sync.WaitGroup
	fail    error
}

func (m *meLinker) Me(ctx context.Context) (*shortlinks.Me, error) {
	m.mu.Lock()
	m.calls++
	fail := m.fail
	m.mu.Unlock()
	if fail != nil {
		return nil, fail
	}
	m.entered.Done()
	done := make(chan struct{})
	go func() { m.entered.Wait(); close(done) }()
	select {
	case <-done:
		return &shortlinks.Me{BaseURL: "https://go.example.test/"}, nil
	case <-time.After(3 * time.Second):
		return nil, errors.New("second caller never entered Me: the handler holds its mutex across the call")
	}
}

func TestShortLinksBase_DoesNotHoldLockAcrossFetch(t *testing.T) {
	pool := outreachTestPool(t)
	ml := &meLinker{fakeShortLinker: &fakeShortLinker{enabled: true}}
	ml.entered.Add(2)
	_, _, h := outreachMuxPosting(pool, ml, nil)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	bases := make([]string, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bases[i], errs[i] = h.shortLinksBase(context.Background())
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil || bases[i] != "https://go.example.test" {
			t.Errorf("caller %d: base=%q err=%v", i, bases[i], errs[i])
		}
	}
	// Cached now: no third fetch.
	if b, err := h.shortLinksBase(context.Background()); err != nil || b != "https://go.example.test" {
		t.Errorf("cached call: %q %v", b, err)
	}
	ml.mu.Lock()
	calls := ml.calls
	ml.mu.Unlock()
	if calls != 2 {
		t.Errorf("Me calls = %d, want 2 (concurrent first callers) and none once cached", calls)
	}
}

func TestShortLinksBase_FailedFetchIsNotCached(t *testing.T) {
	pool := outreachTestPool(t)
	ml := &meLinker{fakeShortLinker: &fakeShortLinker{enabled: true}, fail: errors.New("upstream down")}
	_, _, h := outreachMuxPosting(pool, ml, nil)
	if _, err := h.shortLinksBase(context.Background()); err == nil {
		t.Fatal("expected the failure")
	}
	ml.mu.Lock()
	ml.fail = nil
	ml.mu.Unlock()
	ml.entered.Add(1)
	if b, err := h.shortLinksBase(context.Background()); err != nil || b != "https://go.example.test" {
		t.Errorf("after recovery: %q %v", b, err)
	}
}
