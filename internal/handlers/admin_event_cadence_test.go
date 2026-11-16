package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// adminCadenceServer serves the #0678 admin routes on a coverage env's
// handler behind the real session and admin guards, with an admin session
// and a non-admin one.
func adminCadenceServer(t *testing.T, e *coverageEnv) func(method, path, token, body string) (int, string) {
	t.Helper()
	requireSession := middleware.RequireSession(auth.NewStore(e.pool))
	guard := func(f http.HandlerFunc) http.Handler { return requireSession(middleware.RequireAdmin(f)) }
	mux := http.NewServeMux()
	mux.Handle("GET /admin/event-cadence", guard(e.h.ListRules))
	mux.Handle("POST /admin/event-cadence", guard(e.h.CreateRule))
	mux.Handle("PATCH /admin/event-cadence/{id}", guard(e.h.UpdateRule))
	mux.Handle("DELETE /admin/event-cadence/{id}", guard(e.h.DeleteRule))
	mux.Handle("GET /admin/event-coverage", guard(e.h.Coverage))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	seedSession(t, e.pool, seedAdmin(t, e.pool, "admin-cadence@example.com"), "cadence-admin-token")
	seedSession(t, e.pool, seedUser(t, e.pool, "user-cadence@example.com"), "cadence-user-token")
	return func(method, path, token, body string) (int, string) {
		t.Helper()
		resp := doJSON(t, srv.Client(), method, srv.URL+path, token, body)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
}

func cadenceAuditActions(t *testing.T, e *coverageEnv, id int64) []string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`SELECT action FROM audit_log WHERE target_type = $1 AND target_id = $2 ORDER BY id`, audit.TargetEventCadenceRule, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func TestAdminEventCadence_CRUDAndAudit(t *testing.T) {
	e := newCoverageEnv(t)
	do := adminCadenceServer(t, e)
	const tok = "cadence-admin-token"

	code, body := do(http.MethodPost, "/admin/event-cadence", tok, fmt.Sprintf(`{"name":"Hack night","event_type_id":%d,"frequency":"weekly","weekday":"tuesday","active":true}`, e.typeID))
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	var created cadenceRuleV1
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	cleanupCadenceRule(t, e.pool, created.ID)
	if created.Weekday == nil || *created.Weekday != "tuesday" || created.Description != "every Tuesday" {
		t.Fatalf("created %+v", created)
	}

	code, body = do(http.MethodPost, "/admin/event-cadence", tok, fmt.Sprintf(`{"name":"x","event_type_id":%d,"frequency":"weekly"}`, e.typeID))
	if code != http.StatusBadRequest || !strings.Contains(body, "weekday") {
		t.Errorf("weekly without weekday = %d %s", code, body)
	}

	path := fmt.Sprintf("/admin/event-cadence/%d", created.ID)
	code, body = do(http.MethodPatch, path, tok, fmt.Sprintf(`{"name":"Hack night","event_type_id":%d,"frequency":"monthly","per_month":3,"active":false}`, e.typeID))
	if code != http.StatusOK || !strings.Contains(body, `"per_month":3`) || !strings.Contains(body, `"weekday":null`) || !strings.Contains(body, `"active":false`) {
		t.Fatalf("patch = %d %s", code, body)
	}

	code, body = do(http.MethodGet, "/admin/event-cadence", tok, "")
	if code != http.StatusOK || !strings.Contains(body, fmt.Sprintf(`"id":%d,`, created.ID)) {
		t.Errorf("list = %d %s", code, body)
	}

	if code, body = do(http.MethodDelete, path, tok, ""); code != http.StatusOK {
		t.Fatalf("delete = %d %s", code, body)
	}
	if code, _ = do(http.MethodDelete, path, tok, ""); code != http.StatusNotFound {
		t.Errorf("second delete = %d", code)
	}
	if code, _ = do(http.MethodPatch, path, tok, fmt.Sprintf(`{"name":"x","event_type_id":%d,"frequency":"monthly","per_month":1}`, e.typeID)); code != http.StatusNotFound {
		t.Errorf("patch deleted = %d", code)
	}
	got := strings.Join(cadenceAuditActions(t, e, created.ID), ",")
	if got != "event_cadence.created,event_cadence.updated,event_cadence.deleted" {
		t.Errorf("audit %s", got)
	}
}

func TestAdminEventCadence_RequiresAdmin(t *testing.T) {
	e := newCoverageEnv(t)
	do := adminCadenceServer(t, e)
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/admin/event-cadence"},
		{http.MethodPost, "/admin/event-cadence"},
		{http.MethodPatch, "/admin/event-cadence/1"},
		{http.MethodDelete, "/admin/event-cadence/1"},
		{http.MethodGet, "/admin/event-coverage"},
	} {
		if code, _ := do(r.method, r.path, "", ""); code != http.StatusUnauthorized {
			t.Errorf("%s %s no session = %d", r.method, r.path, code)
		}
		if code, _ := do(r.method, r.path, "cadence-user-token", ""); code != http.StatusForbidden {
			t.Errorf("%s %s non-admin = %d", r.method, r.path, code)
		}
	}
}

func TestAdminEventCoverage_MatchesV1(t *testing.T) {
	e := newCoverageEnv(t)
	do := adminCadenceServer(t, e)
	e.setRule(t, fmt.Sprintf(`{"name":"Hack night","event_type_id":%d,"frequency":"weekly","weekday":"tuesday"}`, e.typeID))
	e.event(t, "Hack night", workshops.StatusDraft, "2031-03-04T18:00:00-08:00")

	code, adminBody := do(http.MethodGet, "/admin/event-coverage?month=2031-03", "cadence-admin-token", "")
	if code != http.StatusOK {
		t.Fatalf("admin coverage = %d %s", code, adminBody)
	}
	rec := e.call(t, http.MethodGet, "/api/v1/events/coverage?month=2031-03", e.read, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("v1 coverage = %d %s", rec.Code, rec.Body)
	}
	if strings.TrimSpace(adminBody) != strings.TrimSpace(rec.Body.String()) {
		t.Errorf("admin and v1 differ:\nadmin %s\nv1    %s", adminBody, rec.Body)
	}
	if code, body := do(http.MethodGet, "/admin/event-coverage?month=nope", "cadence-admin-token", ""); code != http.StatusBadRequest || !strings.Contains(body, "month") {
		t.Errorf("bad month = %d %s", code, body)
	}
	// The default is the month after the pinned clock.
	if _, body := do(http.MethodGet, "/admin/event-coverage", "cadence-admin-token", ""); !strings.Contains(body, `"month":"2031-03"`) {
		t.Errorf("default month: %s", body)
	}
}
