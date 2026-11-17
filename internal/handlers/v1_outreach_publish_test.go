package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// newV1OutreachPublishEnv serves the #0701 operations over a fake publisher:
// nothing here reaches Bluesky or Mastodon.
func newV1OutreachPublishEnv(t *testing.T, posting outreachPosting) *v1OutreachWriteEnv {
	t.Helper()
	e := newOutreachEnvPosting(t, true, posting)
	reg := v1TestRegistry(e.pool, outreachPublishOperations(e.h)...)
	h := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})
	write, tok := v1TestKey(t, e.pool, apitokens.ScopeOutreachRead, apitokens.ScopeOutreachWrite)
	read, _ := v1TestKey(t, e.pool, apitokens.ScopeOutreachRead)
	return &v1OutreachWriteEnv{outreachEnv: e, api: h, write: write, writeTok: tok, read: read}
}

// assertPacific fails when a v1 response carries a UTC time.
func assertPacific(t *testing.T, b []byte) {
	t.Helper()
	if strings.Contains(string(b), `Z"`) {
		t.Errorf("response has a UTC time: %s", b)
	}
}

func TestV1OutreachPublish_Flags(t *testing.T) {
	want := map[string]string{
		"schedule_outreach_wave":      "PUBLIC: ",
		"unschedule_outreach_wave":    "",
		"send_outreach_post":          "PUBLIC: ",
		"mark_outreach_post_posted":   "",
		"unmark_outreach_post_posted": "",
		"takedown_outreach_post":      "DESTRUCTIVE: PUBLIC: ",
	}
	ops := outreachPublishOperations(nil)
	if len(ops) != len(want) {
		t.Fatalf("%d operations, want %d", len(ops), len(want))
	}
	for _, op := range ops {
		prefix, ok := want[op.Name]
		if !ok {
			t.Errorf("unexpected operation %s", op.Name)
			continue
		}
		d := op.Describe()
		if !strings.HasPrefix(d, prefix) {
			t.Errorf("%s description %q, want prefix %q", op.Name, d, prefix)
		}
		if prefix == "" && (strings.Contains(d, "PUBLIC:") || strings.Contains(d, "DESTRUCTIVE:")) {
			t.Errorf("%s description %q is flagged", op.Name, d)
		}
	}
}

func TestV1OutreachPublish_ScheduleUnschedule(t *testing.T) {
	e := newV1OutreachPublishEnv(t, fakePosting{"bluesky": true})
	w, ids := e.sectionWorkshop(t, []string{"bluesky", "mastodon"}, map[string]bool{"bluesky": true, "mastodon": true})
	path := fmt.Sprintf("/api/v1/events/%d/outreach/schedule", w.ID)
	at := time.Now().Add(2 * time.Hour).In(announceLocation).Format(time.RFC3339)
	body := fmt.Sprintf(`{"wave":"reminder","scheduled_at":%q}`, at)

	e.v1(t, http.MethodPost, path, e.read, body, http.StatusForbidden)
	b := e.v1(t, http.MethodPost, path, e.write, `{"wave":"nope","scheduled_at":"`+at+`"}`, http.StatusBadRequest)
	if !strings.Contains(string(b), `"wave"`) {
		t.Errorf("bad wave body %s", b)
	}
	b = e.v1(t, http.MethodPost, path, e.write, `{"wave":"reminder","scheduled_at":"soon"}`, http.StatusBadRequest)
	if !strings.Contains(string(b), `"scheduled_at"`) {
		t.Errorf("bad time body %s", b)
	}
	b = e.v1(t, http.MethodPost, path, e.write, body, http.StatusOK)
	assertPacific(t, b)
	var res struct {
		Posts []struct {
			ID          int64   `json:"id"`
			Status      string  `json:"status"`
			ScheduledAt *string `json:"scheduled_at"`
		} `json:"posts"`
		LeftAlone []sectionLeftAlone `json:"left_alone"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Posts) != 1 || res.Posts[0].ID != ids["bluesky"] || res.Posts[0].Status != "scheduled" || res.Posts[0].ScheduledAt == nil {
		t.Errorf("posts %+v", res.Posts)
	}
	if len(res.LeftAlone) != 1 || res.LeftAlone[0].Channel != "mastodon" || res.LeftAlone[0].Reason != "not_connected" {
		t.Errorf("left_alone %+v", res.LeftAlone)
	}
	e.assertTokenAudit(t, audit.ActionOutreachPostScheduled, 1)

	upath := fmt.Sprintf("/api/v1/events/%d/outreach/unschedule", w.ID)
	b = e.v1(t, http.MethodPost, upath, e.write, `{"wave":"reminder"}`, http.StatusOK)
	assertPacific(t, b)
	if st, _ := e.postStatus(t, ids["bluesky"]); st != "draft" {
		t.Errorf("after unschedule status %s", st)
	}
	e.assertTokenAudit(t, audit.ActionOutreachPostUnscheduled, 1)
	e.v1(t, http.MethodPost, upath, e.write, `{"wave":"reminder"}`, http.StatusConflict)
	e.v1(t, http.MethodPost, "/api/v1/events/999999999/outreach/unschedule", e.write, `{"wave":"reminder"}`, http.StatusNotFound)
}

func TestV1OutreachPublish_Send(t *testing.T) {
	e := newV1OutreachPublishEnv(t, fakePosting{"bluesky": true})
	_, p := e.preparedPost(t, "bluesky")
	path := fmt.Sprintf("/api/v1/outreach-posts/%d/send", p.ID)
	e.v1(t, http.MethodPost, path, e.read, "", http.StatusForbidden)
	b := e.v1(t, http.MethodPost, path, e.write, "", http.StatusAccepted)
	assertPacific(t, b)
	if !strings.Contains(string(b), `"status":"queued"`) {
		t.Errorf("send body %s", b)
	}
	e.assertTokenAudit(t, audit.ActionOutreachPostQueued, 1)
	e.v1(t, http.MethodPost, path, e.write, "", http.StatusConflict) // already queued

	_, m := e.preparedPost(t, "mastodon") // not connected
	e.v1(t, http.MethodPost, fmt.Sprintf("/api/v1/outreach-posts/%d/send", m.ID), e.write, "", http.StatusServiceUnavailable)
	e.v1(t, http.MethodPost, "/api/v1/outreach-posts/999999999/send", e.write, "", http.StatusNotFound)
}

func TestV1OutreachPublish_MarkUnmark(t *testing.T) {
	e := newV1OutreachPublishEnv(t, fakePosting{"bluesky": true})
	_, p := e.preparedPost(t, "instagram")
	path := fmt.Sprintf("/api/v1/outreach-posts/%d/posted", p.ID)
	b := e.v1(t, http.MethodPost, path, e.write, `{"remote_url":"ftp://x"}`, http.StatusBadRequest)
	if !strings.Contains(string(b), `"remote_url"`) {
		t.Errorf("bad url body %s", b)
	}
	b = e.v1(t, http.MethodPost, path, e.write, `{"remote_url":"https://instagram.example/p/1"}`, http.StatusOK)
	assertPacific(t, b)
	if !strings.Contains(string(b), `"status":"posted"`) {
		t.Errorf("mark body %s", b)
	}
	e.assertTokenAudit(t, audit.ActionOutreachPostPosted, 1)
	e.v1(t, http.MethodPost, path, e.write, "", http.StatusConflict)

	upath := fmt.Sprintf("/api/v1/outreach-posts/%d/unmark", p.ID)
	b = e.v1(t, http.MethodPost, upath, e.write, "", http.StatusOK)
	assertPacific(t, b)
	e.assertTokenAudit(t, audit.ActionOutreachPostUnmarked, 1)
	e.v1(t, http.MethodPost, upath, e.write, "", http.StatusConflict)
}

func TestV1OutreachPublish_Takedown(t *testing.T) {
	fp := &fakeTakedownPosting{fakePosting: fakePosting{"bluesky": true}, existed: true}
	e := newV1OutreachPublishEnv(t, fp)
	_, id, _, _ := seedPostedBluesky(t, e.outreachEnv, "bluesky")
	path := fmt.Sprintf("/api/v1/outreach-posts/%d/takedown", id)

	e.v1(t, http.MethodPost, path, e.read, "", http.StatusForbidden)
	// The network refuses: the post stays posted, and a retry is safe.
	fp.err = errors.New("network down")
	e.v1(t, http.MethodPost, path, e.write, "", http.StatusServiceUnavailable)
	if st, _ := e.postStatus(t, id); st != "posted" {
		t.Fatalf("after refusal status %s", st)
	}
	fp.err = nil
	b := e.v1(t, http.MethodPost, path, e.write, "", http.StatusOK)
	assertPacific(t, b)
	if !strings.Contains(string(b), `"status":"draft"`) {
		t.Errorf("takedown body %s", b)
	}
	if len(fp.calls) != 2 {
		t.Errorf("remote delete calls %d, want 2", len(fp.calls))
	}
	e.assertTokenAudit(t, audit.ActionOutreachPostTakenDown, 1)
	// A retry after success finds a draft and deletes nothing.
	e.v1(t, http.MethodPost, path, e.write, "", http.StatusConflict)
	if len(fp.calls) != 2 {
		t.Errorf("retry reached the network: %d calls", len(fp.calls))
	}
}
