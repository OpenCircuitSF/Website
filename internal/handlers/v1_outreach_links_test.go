package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
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

// TestV1OutreachLinks_RateLimitedIs429 (#0708): a ShortLinks rate limit is a
// 429 with Retry-After on v1, as on the admin, falling back to 60 seconds
// when ShortLinks gave none; the wrapped error text stays out.
func TestV1OutreachLinks_RateLimitedIs429(t *testing.T) {
	e := newV1OutreachLinksEnv(t, true)
	o := e.start(t, e.published(t)).Outreach
	p := e.createPost(t, o.ID, "bluesky", "announce")
	e.links.linkErr = fmt.Errorf("create link: %w", shortlinks.ErrRateLimited)
	rec := apitest.Do(t, e.api, http.MethodPost, fmt.Sprintf("/api/v1/outreach-posts/%d/prepare", p.ID), e.write, "")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("prepare = %d Retry-After=%q: %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body)
	}
	b := rec.Body.String()
	if !strings.Contains(b, `"rate_limited"`) || !strings.Contains(b, "60 seconds") || strings.Contains(b, "create link") {
		t.Errorf("prepare body %s", b)
	}
}

// TestShortLinksAPIError_RetryAfterFromShortLinks (#0708): a real
// *shortlinks.APIError's Retry-After is carried through; any other failure
// stays a 503.
func TestShortLinksAPIError_RetryAfterFromShortLinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c, err := shortlinks.New(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateLink(context.Background(), shortlinks.CreateLinkRequest{})
	if !errors.Is(err, shortlinks.ErrRateLimited) {
		t.Fatalf("CreateLink err = %v, want a rate limit", err)
	}
	var ae *api.Error
	if !errors.As(shortLinksAPIError("p: ", err), &ae) || ae.Status != http.StatusTooManyRequests || ae.RetryAfter != 7*time.Second || !strings.Contains(ae.Message, "7 seconds") {
		t.Fatalf("mapped %+v", ae)
	}
	if !errors.As(shortLinksAPIError("p: ", errors.New("boom")), &ae) || ae.Status != http.StatusServiceUnavailable || ae.Message != "p: failed" {
		t.Fatalf("mapped other %+v", ae)
	}
}
