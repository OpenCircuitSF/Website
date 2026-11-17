package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// newV1OutreachLocationsEnv serves the #0702 operations alongside the
// #0699 writes, so /api/v1/outreach/locations/{id} and
// /api/v1/outreach/{id}/posts share one router.
func newV1OutreachLocationsEnv(t *testing.T) *v1OutreachWriteEnv {
	t.Helper()
	e := newOutreachEnv(t, true)
	ops := append(outreachWriteOperations(e.h), outreachLocationOperations(e.h)...)
	reg := v1TestRegistry(e.pool, ops...)
	h := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})
	write, tok := v1TestKey(t, e.pool, apitokens.ScopeOutreachRead, apitokens.ScopeOutreachWrite)
	read, _ := v1TestKey(t, e.pool, apitokens.ScopeOutreachRead)
	return &v1OutreachWriteEnv{outreachEnv: e, api: h, write: write, writeTok: tok, read: read}
}

func noUTC(t *testing.T, b []byte) {
	t.Helper()
	if strings.Contains(string(b), `Z"`) {
		t.Errorf("UTC time in v1 body: %s", b)
	}
}

func TestV1OutreachLocations_CRUD(t *testing.T) {
	e := newV1OutreachLocationsEnv(t)
	code := locTestCode()
	body := fmt.Sprintf(`{"name":"  Noisebridge ","code":%q,"address":"2169 Mission St"}`, code)

	e.v1(t, http.MethodPost, "/api/v1/outreach/locations", e.read, body, http.StatusForbidden)
	b := e.v1(t, http.MethodPost, "/api/v1/outreach/locations", e.write, body, http.StatusCreated, "Idempotency-Key", "loc-0702-"+code)
	noUTC(t, b)
	var l outreachLocationView
	if err := json.Unmarshal(b, &l); err != nil || l.Name != "Noisebridge" || !l.Active || !strings.Contains(l.CreatedAt, "-0") {
		t.Fatalf("create %s (%v)", b, err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM outreach_locations WHERE id = $1`, l.ID)
	})
	again := e.v1(t, http.MethodPost, "/api/v1/outreach/locations", e.write, body, http.StatusCreated, "Idempotency-Key", "loc-0702-"+code)
	if string(again) != string(b) {
		t.Errorf("idempotent replay differs:\n%s\n%s", b, again)
	}
	e.assertTokenAudit(t, audit.ActionOutreachLocationCreated, 1)

	dup := e.v1(t, http.MethodPost, "/api/v1/outreach/locations", e.write, fmt.Sprintf(`{"name":"B","code":%q}`, code), http.StatusConflict)
	if !strings.Contains(string(dup), "code already in use") {
		t.Errorf("duplicate code: %s", dup)
	}
	bad := e.v1(t, http.MethodPost, "/api/v1/outreach/locations", e.write, `{"name":"B","code":"NO"}`, http.StatusBadRequest)
	if !strings.Contains(string(bad), `"code"`) {
		t.Errorf("bad code: %s", bad)
	}

	list := e.v1(t, http.MethodGet, "/api/v1/outreach/locations", e.read, "", http.StatusOK)
	noUTC(t, list)
	if !strings.Contains(string(list), code) {
		t.Errorf("list missing %s: %s", code, list)
	}

	path := fmt.Sprintf("/api/v1/outreach/locations/%d", l.ID)
	e.v1(t, http.MethodPatch, path, e.read, `{"active":false}`, http.StatusForbidden)
	e.v1(t, http.MethodPatch, path, e.write, `{}`, http.StatusBadRequest)
	pb := e.v1(t, http.MethodPatch, path, e.write, `{"active":false,"notes":"by the door"}`, http.StatusOK)
	noUTC(t, pb)
	if !strings.Contains(string(pb), `"active":false`) || !strings.Contains(string(pb), "by the door") {
		t.Errorf("patch %s", pb)
	}
	e.assertTokenAudit(t, audit.ActionOutreachLocationUpdated, 1)
	e.v1(t, http.MethodPatch, "/api/v1/outreach/locations/999999999", e.write, `{"active":true}`, http.StatusNotFound)

	e.v1(t, http.MethodDelete, path, e.read, "", http.StatusForbidden)
	db := e.v1(t, http.MethodDelete, path, e.write, "", http.StatusOK)
	if !strings.Contains(string(db), `"deleted":true`) {
		t.Errorf("delete %s", db)
	}
	e.assertTokenAudit(t, audit.ActionOutreachLocationDeleted, 1)
	e.v1(t, http.MethodDelete, path, e.write, "", http.StatusNotFound)
}

func TestV1OutreachLocations_InUse(t *testing.T) {
	e := newV1OutreachLocationsEnv(t)
	l := e.createLocation(t, fmt.Sprintf(`{"name":"A","code":%q}`, locTestCode()))
	if err := outreach.NewStore(e.pool).MarkLocationUsed(context.Background(), e.pool, l.ID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/outreach/locations/%d", l.ID)

	locked := e.v1(t, http.MethodPatch, path, e.write, fmt.Sprintf(`{"code":%q}`, locTestCode()), http.StatusConflict)
	if !strings.Contains(string(locked), "code is locked") {
		t.Errorf("locked code: %s", locked)
	}
	pb := e.v1(t, http.MethodPatch, path, e.write, `{"active":true}`, http.StatusOK)
	noUTC(t, pb)
	if !strings.Contains(string(pb), `"code_locked":true`) {
		t.Errorf("patch %s", pb)
	}
	del := e.v1(t, http.MethodDelete, path, e.write, "", http.StatusConflict)
	if !strings.Contains(string(del), "deactivate it instead") {
		t.Errorf("in-use delete: %s", del)
	}
	if n := locAuditCount(t, e.pool, audit.ActionOutreachLocationDeleted, l.ID); n != 0 {
		t.Errorf("refused delete audited %d rows", n)
	}
}

// TestV1OutreachLocations_RouterHasBothPaths: the full v1 router builds with
// /api/v1/outreach/locations/{id} beside /api/v1/outreach/{id}/posts, and
// each path reaches its own operation.
func TestV1OutreachLocations_RouterHasBothPaths(t *testing.T) {
	e := newV1OutreachLocationsEnv(t)
	reg := BuildV1Registry(api.Config{Version: "x", Idempotency: api.NewIdempotencyStore(e.pool)}, V1Deps{Outreach: e.h})
	paths := map[string]bool{}
	for _, op := range reg.Operations() {
		paths[op.Method+" "+op.Path] = true
	}
	for _, p := range []string{"PATCH /api/v1/outreach/locations/{id}", "DELETE /api/v1/outreach/locations/{id}", "POST /api/v1/outreach/{id}/posts"} {
		if !paths[p] {
			t.Errorf("registry lacks %s", p)
		}
	}
	full := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})

	for _, c := range []struct {
		method, path, body string
		status             int
		want               string
	}{
		{http.MethodPost, "/api/v1/outreach/999999999/posts", `{"channel":"bluesky","wave":"announce"}`, http.StatusNotFound, "outreach not found"},
		{http.MethodDelete, "/api/v1/outreach/locations/999999999", "", http.StatusNotFound, "outreach location not found"},
		{http.MethodGet, "/api/v1/outreach/locations", "", http.StatusOK, ""},
	} {
		rec := apitest.Do(t, full, c.method, c.path, e.write, c.body)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s = %d %s, want %d containing %q", c.method, c.path, rec.Code, rec.Body.String(), c.status, c.want)
		}
	}
}
