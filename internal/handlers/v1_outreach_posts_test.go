package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// v1OutreachWriteEnv serves the outreach reads and writes (#0699) with a
// write key, and a read key that lacks outreach:write.
type v1OutreachWriteEnv struct {
	*outreachEnv
	api      http.Handler
	write    string
	writeTok apitokens.Token
	read     string
}

func newV1OutreachWriteEnv(t *testing.T) *v1OutreachWriteEnv {
	t.Helper()
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	reg := v1TestRegistry(e.pool, append(outreachOperations(e.h), outreachWriteOperations(e.h)...)...)
	h := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})
	write, tok := v1TestKey(t, e.pool, apitokens.ScopeOutreachRead, apitokens.ScopeOutreachWrite)
	read, _ := v1TestKey(t, e.pool, apitokens.ScopeOutreachRead)
	return &v1OutreachWriteEnv{outreachEnv: e, api: h, write: write, writeTok: tok, read: read}
}

func (e *v1OutreachWriteEnv) v1(t *testing.T, method, path, secret, body string, want int, hdr ...string) []byte {
	t.Helper()
	rec := apitest.Do(t, e.api, method, path, secret, body, hdr...)
	if rec.Code != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, rec.Code, want, rec.Body)
	}
	return rec.Body.Bytes()
}

// markPosted puts a post where the store refuses edits and deletes.
func (e *v1OutreachWriteEnv) markPosted(t *testing.T, id int64) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE outreach_posts SET status = 'posted', posted_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

// assertTokenAudit: exactly n rows for action, each attributed to the key.
func (e *v1OutreachWriteEnv) assertTokenAudit(t *testing.T, action string, n int) {
	t.Helper()
	rows := v1AuditRows(t, e.pool, action, e.writeTok.ID)
	if len(rows) != n {
		t.Fatalf("%s: %d rows attributed to the token, want %d", action, len(rows), n)
	}
	for _, r := range rows {
		if r.ActorID != nil || r.Metadata["api_token"] == nil {
			t.Errorf("%s row %+v", action, r)
		}
	}
}

func (e *v1OutreachWriteEnv) published(t *testing.T) workshops.Workshop {
	t.Helper()
	return seedOutreachWorkshop(t, e.pool, e.ws, "published")
}

func TestV1OutreachWrite_Start(t *testing.T) {
	e := newV1OutreachWriteEnv(t)
	w := e.published(t)
	path := fmt.Sprintf("/api/v1/events/%d/outreach", w.ID)

	e.v1(t, http.MethodPost, path, e.read, "", http.StatusForbidden)
	b := e.v1(t, http.MethodPost, path, e.write, "", http.StatusOK)
	if !strings.Contains(string(b), `"started":true`) || !strings.Contains(string(b), `"link_report":{`) {
		t.Errorf("start body %s", b)
	}
	if strings.Contains(string(b), `Z"`) {
		t.Errorf("start body has a UTC time, want Pacific: %s", b)
	}
	// Repeating returns the existing outreach and audits nothing more.
	e.v1(t, http.MethodPost, path, e.write, "", http.StatusOK)
	e.assertTokenAudit(t, audit.ActionOutreachStarted, 1)
	if rows := v1AuditRows(t, e.pool, audit.ActionOutreachLinkCreated, e.writeTok.ID); len(rows) == 0 {
		t.Error("link creation not attributed to the token")
	}

	e.v1(t, http.MethodPost, "/api/v1/events/999999999/outreach", e.write, "", http.StatusNotFound)
	draft := seedOutreachWorkshop(t, e.pool, e.ws, "draft")
	e.v1(t, http.MethodPost, fmt.Sprintf("/api/v1/events/%d/outreach", draft.ID), e.write, "", http.StatusConflict)
}

func TestV1OutreachWrite_CreatePost(t *testing.T) {
	e := newV1OutreachWriteEnv(t)
	o := e.start(t, e.published(t)).Outreach
	path := fmt.Sprintf("/api/v1/outreach/%d/posts", o.ID)
	body := `{"channel":"bluesky","wave":"announce","body":"hello"}`

	e.v1(t, http.MethodPost, path, e.read, body, http.StatusForbidden)
	first := e.v1(t, http.MethodPost, path, e.write, body, http.StatusCreated, "Idempotency-Key", "create-0699")
	second := e.v1(t, http.MethodPost, path, e.write, body, http.StatusCreated, "Idempotency-Key", "create-0699")
	if string(first) != string(second) {
		t.Errorf("replay differs:\n%s\n%s", first, second)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outreach_posts WHERE outreach_id = $1`, o.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("posts = %d (%v), want 1", n, err)
	}
	e.assertTokenAudit(t, audit.ActionOutreachPostCreated, 1)

	// A second post for the slot, without a key, is a conflict.
	e.v1(t, http.MethodPost, path, e.write, body, http.StatusConflict)
	e.v1(t, http.MethodPost, "/api/v1/outreach/999999999/posts", e.write, body, http.StatusNotFound)
	b := e.v1(t, http.MethodPost, path, e.write, `{"channel":"nope","wave":"announce"}`, http.StatusBadRequest)
	if !strings.Contains(string(b), `"channel"`) {
		t.Errorf("validation body %s", b)
	}
}

func TestV1OutreachWrite_UpdatePost(t *testing.T) {
	e := newV1OutreachWriteEnv(t)
	o := e.start(t, e.published(t)).Outreach
	p := e.createPost(t, o.ID, "bluesky", "announce")
	path := fmt.Sprintf("/api/v1/outreach-posts/%d", p.ID)

	e.v1(t, http.MethodPatch, path, e.read, `{"body":"x"}`, http.StatusForbidden)
	b := e.v1(t, http.MethodPatch, path, e.write, `{"body":"edited"}`, http.StatusOK)
	if !strings.Contains(string(b), `"body":"edited"`) || !strings.Contains(string(b), "-08:00") {
		t.Errorf("update body %s", b)
	}
	e.v1(t, http.MethodPatch, path, e.write, `{"regenerate":true}`, http.StatusOK)
	e.v1(t, http.MethodPatch, path, e.write, `{"body":"x","regenerate":true}`, http.StatusBadRequest)
	e.assertTokenAudit(t, audit.ActionOutreachPostUpdated, 2)

	e.v1(t, http.MethodPatch, "/api/v1/outreach-posts/999999999", e.write, `{"body":"x"}`, http.StatusNotFound)
	e.markPosted(t, p.ID)
	e.v1(t, http.MethodPatch, path, e.write, `{"body":"late"}`, http.StatusConflict)
}

func TestV1OutreachWrite_DeletePost(t *testing.T) {
	e := newV1OutreachWriteEnv(t)
	o := e.start(t, e.published(t)).Outreach
	p := e.createPost(t, o.ID, "bluesky", "announce")
	q := e.createPost(t, o.ID, "mastodon", "announce")

	e.v1(t, http.MethodDelete, fmt.Sprintf("/api/v1/outreach-posts/%d", p.ID), e.read, "", http.StatusForbidden)
	b := e.v1(t, http.MethodDelete, fmt.Sprintf("/api/v1/outreach-posts/%d", p.ID), e.write, "", http.StatusOK)
	if !strings.Contains(string(b), `"deleted":true`) {
		t.Errorf("delete body %s", b)
	}
	e.assertTokenAudit(t, audit.ActionOutreachPostDeleted, 1)
	e.v1(t, http.MethodDelete, fmt.Sprintf("/api/v1/outreach-posts/%d", p.ID), e.write, "", http.StatusNotFound)
	e.markPosted(t, q.ID)
	e.v1(t, http.MethodDelete, fmt.Sprintf("/api/v1/outreach-posts/%d", q.ID), e.write, "", http.StatusConflict)
}

func TestV1OutreachWrite_Reset(t *testing.T) {
	e := newV1OutreachWriteEnv(t)
	w := e.published(t)
	o := e.start(t, w).Outreach
	e.createPost(t, o.ID, "bluesky", "announce")
	path := fmt.Sprintf("/api/v1/events/%d/outreach/reset", w.ID)

	e.v1(t, http.MethodPost, path, e.read, "", http.StatusForbidden)
	rb := e.v1(t, http.MethodPost, path, e.write, "", http.StatusOK)
	if strings.Contains(string(rb), `Z"`) {
		t.Errorf("reset body has a UTC time, want Pacific: %s", rb)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outreach_posts WHERE outreach_id = $1`, o.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("posts after reset = %d (%v)", n, err)
	}
	e.assertTokenAudit(t, audit.ActionOutreachReset, 1)

	p := e.createPost(t, o.ID, "bluesky", "announce")
	e.markPosted(t, p.ID)
	e.v1(t, http.MethodPost, path, e.write, "", http.StatusConflict)
	unstarted := e.published(t)
	e.v1(t, http.MethodPost, fmt.Sprintf("/api/v1/events/%d/outreach/reset", unstarted.ID), e.write, "", http.StatusNotFound)
}
