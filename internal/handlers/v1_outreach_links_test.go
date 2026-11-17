package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// newV1OutreachLinksEnv serves the #0700 operations over an outreach env
// whose ShortLinks is enabled or not.
func newV1OutreachLinksEnv(t *testing.T, enabled bool) *v1OutreachWriteEnv {
	t.Helper()
	e := newOutreachEnvPosting(t, enabled, fakePosting{"bluesky": true})
	reg := v1TestRegistry(e.pool, outreachLinkOperations(e.h)...)
	h := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})
	write, tok := v1TestKey(t, e.pool, apitokens.ScopeOutreachRead, apitokens.ScopeOutreachWrite)
	read, _ := v1TestKey(t, e.pool, apitokens.ScopeOutreachRead)
	return &v1OutreachWriteEnv{outreachEnv: e, api: h, write: write, writeTok: tok, read: read}
}

func TestV1OutreachLinks_DraftAll(t *testing.T) {
	e := newV1OutreachLinksEnv(t, true)
	w := e.published(t)
	path := fmt.Sprintf("/api/v1/events/%d/outreach/draft-all", w.ID)

	e.v1(t, http.MethodPost, path, e.write, "", http.StatusConflict) // not started
	e.start(t, w)
	e.v1(t, http.MethodPost, path, e.read, "", http.StatusForbidden)
	var first draftAllReport
	if err := json.Unmarshal(e.v1(t, http.MethodPost, path, e.write, "", http.StatusOK), &first); err != nil || len(first.Created) == 0 {
		t.Fatalf("first draft-all %+v (%v)", first, err)
	}
	for _, c := range first.Created {
		if c.Wave != "announce" {
			t.Errorf("default draft-all made a %s post", c.Wave)
		}
	}
	e.assertTokenAudit(t, audit.ActionOutreachPostCreated, len(first.Created))
	var second draftAllReport
	if err := json.Unmarshal(e.v1(t, http.MethodPost, path, e.write, `{"waves":["reminder","last-call"]}`, http.StatusOK), &second); err != nil {
		t.Fatal(err)
	}
	if second.Skipped != len(first.Created) || len(second.Created) == 0 {
		t.Errorf("second draft-all %+v", second)
	}
	b := e.v1(t, http.MethodPost, path, e.write, `{"waves":["nope"]}`, http.StatusBadRequest)
	if !strings.Contains(string(b), `"waves"`) {
		t.Errorf("validation body %s", b)
	}
	e.v1(t, http.MethodPost, "/api/v1/events/999999999/outreach/draft-all", e.write, "", http.StatusNotFound)
}

func TestV1OutreachLinks_PrepareIsIdempotent(t *testing.T) {
	e := newV1OutreachLinksEnv(t, true)
	o := e.start(t, e.published(t)).Outreach
	p := e.createPost(t, o.ID, "bluesky", "announce")
	path := fmt.Sprintf("/api/v1/outreach-posts/%d/prepare", p.ID)

	e.v1(t, http.MethodPost, path, e.read, "", http.StatusForbidden)
	first := e.v1(t, http.MethodPost, path, e.write, "", http.StatusOK)
	if !strings.Contains(string(first), `"prepared":true`) || !strings.Contains(string(first), "-08:00") {
		t.Errorf("prepare body %s", first)
	}
	calls := len(e.links.links)
	if calls != 1 {
		t.Fatalf("CreateLink calls = %d, want 1", calls)
	}
	second := e.v1(t, http.MethodPost, path, e.write, "", http.StatusOK)
	if len(e.links.links) != calls {
		t.Errorf("second prepare called ShortLinks (%d calls)", len(e.links.links))
	}
	if string(first) != string(second) {
		t.Errorf("second prepare differs:\n%s\n%s", first, second)
	}
	e.assertTokenAudit(t, audit.ActionOutreachPostPrepared, 1)
	e.v1(t, http.MethodPost, "/api/v1/outreach-posts/999999999/prepare", e.write, "", http.StatusNotFound)
}

func TestV1OutreachLinks_HandLinks(t *testing.T) {
	e := newV1OutreachLinksEnv(t, true)
	e.beforeEvent()
	w := e.published(t)
	path := fmt.Sprintf("/api/v1/events/%d/outreach/hand-links", w.ID)

	e.v1(t, http.MethodPost, path, e.write, "", http.StatusConflict) // not started
	e.start(t, w)
	e.v1(t, http.MethodPost, path, e.read, "", http.StatusForbidden)
	var first outreachLinkReport
	if err := json.Unmarshal(e.v1(t, http.MethodPost, path, e.write, "", http.StatusOK), &first); err != nil || first.Created == 0 {
		t.Fatalf("first hand-links %+v (%v)", first, err)
	}
	var second outreachLinkReport
	if err := json.Unmarshal(e.v1(t, http.MethodPost, path, e.write, "", http.StatusOK), &second); err != nil || second.Created != 0 || second.Existing < first.Created {
		t.Errorf("second hand-links %+v (%v)", second, err)
	}
	if rows := v1AuditRows(t, e.pool, audit.ActionOutreachLinkCreated, e.writeTok.ID); len(rows) != first.Created {
		t.Errorf("link audits = %d, want %d", len(rows), first.Created)
	}
}

func TestV1OutreachLinks_ShortLinksUnconfigured(t *testing.T) {
	e := newV1OutreachLinksEnv(t, false)
	w := e.published(t)
	o := e.start(t, w).Outreach
	p := e.createPost(t, o.ID, "bluesky", "announce")
	e.v1(t, http.MethodPost, fmt.Sprintf("/api/v1/outreach-posts/%d/prepare", p.ID), e.write, "", http.StatusServiceUnavailable)
	e.v1(t, http.MethodPost, fmt.Sprintf("/api/v1/events/%d/outreach/hand-links", w.ID), e.write, "", http.StatusServiceUnavailable)
	// Drafting makes no ShortLinks call, so it still works.
	e.v1(t, http.MethodPost, fmt.Sprintf("/api/v1/events/%d/outreach/draft-all", w.ID), e.write, "", http.StatusOK)
}
