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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/eventtypes"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

func adminEventTypesMux(pool *pgxpool.Pool) http.Handler {
	return adminEventTypesMuxWith(pool, nil)
}

func adminEventTypesMuxWith(pool *pgxpool.Pool, invalidator seoCacheInvalidator) http.Handler {
	h := NewAdminEventTypesHandler(eventtypes.NewStore(pool), audit.New(pool), invalidator)
	requireSession := middleware.RequireSession(auth.NewStore(pool))
	guard := func(f http.HandlerFunc) http.Handler { return requireSession(middleware.RequireAdmin(f)) }
	mux := http.NewServeMux()
	mux.Handle("GET /admin/event-types", guard(h.List))
	mux.Handle("POST /admin/event-types", guard(h.Create))
	mux.Handle("PATCH /admin/event-types/{id}", guard(h.Patch))
	mux.Handle("DELETE /admin/event-types/{id}", guard(h.Delete))
	return mux
}

// seedEventTypeRow adds a throwaway type. Register it before any listing using
// it, so cleanup removes the listing first.
func seedEventTypeRow(t *testing.T, pool *pgxpool.Pool) eventtypes.EventType {
	t.Helper()
	slug := testEventTypeSlug(t, pool)
	et, err := eventtypes.NewStore(pool).Create(context.Background(), slug, "Zz type", 900)
	if err != nil {
		t.Fatalf("seed event type: %v", err)
	}
	return et
}

func testEventTypeSlug(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	slug := fmt.Sprintf("zz-test-%d", testdb.Unique())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM event_types WHERE slug = $1`, slug)
	})
	return slug
}

func seededEventType(t *testing.T, pool *pgxpool.Pool, slug string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM event_types WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("finding %s: %v", slug, err)
	}
	return id
}

func eventTypeAuditActions(t *testing.T, pool *pgxpool.Pool, id int64) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT action FROM audit_log WHERE target_type = $1 AND target_id = $2 ORDER BY id ASC`, audit.TargetEventType, id)
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

// eventTypeServer is a test server with one seeded admin session.
func eventTypeServer(t *testing.T) (*pgxpool.Pool, func(method, path, body string) (int, string)) {
	t.Helper()
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminEventTypesMux(pool))
	t.Cleanup(srv.Close)
	admin := seedAdmin(t, pool, "admin-event-types@example.com")
	seedSession(t, pool, admin, "event-types-admin-token")
	return pool, func(method, path, body string) (int, string) {
		t.Helper()
		resp := doJSON(t, srv.Client(), method, srv.URL+path, "event-types-admin-token", body)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
}

func typePath(id int64) string { return fmt.Sprintf("/admin/event-types/%d", id) }

func TestAdminEventTypes_Unauthenticated(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminEventTypesMux(pool))
	defer srv.Close()
	resp := doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/admin/event-types", "", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAdminEventTypes_NonAdminForbidden(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminEventTypesMux(pool))
	defer srv.Close()
	user := seedUser(t, pool, "regular-event-types@example.com")
	seedSession(t, pool, user, "event-types-user-token")
	et := seedEventTypeRow(t, pool)
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/admin/event-types", ""},
		{http.MethodPost, "/admin/event-types", `{"slug":"zz-forbidden","name":"X"}`},
		{http.MethodPatch, typePath(et.ID), `{"name":"X"}`},
		{http.MethodDelete, typePath(et.ID), ""},
	}
	for _, c := range cases {
		resp := doJSON(t, srv.Client(), c.method, srv.URL+c.path, "event-types-user-token", c.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403", c.method, c.path, resp.StatusCode)
		}
	}
}

func TestAdminEventTypes_ListIncludesCounts(t *testing.T) {
	pool, do := eventTypeServer(t)
	et := seedEventTypeRow(t, pool)
	w, err := workshops.NewStore(pool).Create(context.Background(), workshops.CreateInput{Title: fmt.Sprintf("zz-%d", testdb.Unique()), EventTypeID: &et.ID})
	if err != nil {
		t.Fatal(err)
	}
	cleanupAdminWorkshop(t, pool, w.ID)

	code, body := do(http.MethodGet, "/admin/event-types", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d (%s)", code, body)
	}
	var got struct {
		EventTypes []struct {
			ID            int64  `json:"id"`
			Slug          string `json:"slug"`
			IsDefault     bool   `json:"is_default"`
			WorkshopCount int64  `json:"workshop_count"`
		} `json:"event_types"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.EventTypes[0].Slug != "workshop" || !got.EventTypes[0].IsDefault {
		t.Errorf("first = %+v", got.EventTypes[0])
	}
	found := false
	for _, v := range got.EventTypes {
		if v.ID == et.ID {
			found = true
			if v.WorkshopCount != 1 {
				t.Errorf("count = %d, want 1", v.WorkshopCount)
			}
		}
	}
	if !found {
		t.Error("seeded type missing from list")
	}
}

func TestAdminEventTypes_CreateAudits(t *testing.T) {
	pool, do := eventTypeServer(t)
	slug := testEventTypeSlug(t, pool)
	code, body := do(http.MethodPost, "/admin/event-types", fmt.Sprintf(`{"slug":%q,"name":"  Field Day ","sort_order":70}`, slug))
	if code != http.StatusCreated {
		t.Fatalf("status = %d (%s)", code, body)
	}
	if !strings.Contains(body, `"name":"Field Day"`) {
		t.Errorf("name not trimmed: %s", body)
	}
	actions := eventTypeAuditActions(t, pool, seededEventType(t, pool, slug))
	if len(actions) != 1 || actions[0] != audit.ActionEventTypeCreated {
		t.Errorf("actions = %v", actions)
	}
	if code, _ := do(http.MethodPost, "/admin/event-types", fmt.Sprintf(`{"slug":%q,"name":"Again"}`, slug)); code != http.StatusConflict {
		t.Errorf("duplicate status = %d, want 409", code)
	}
}

func TestAdminEventTypes_CreateRejectsBadSlug(t *testing.T) {
	_, do := eventTypeServer(t)
	if code, _ := do(http.MethodPost, "/admin/event-types", `{"slug":"Not A Slug","name":"X"}`); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
	if code, _ := do(http.MethodPost, "/admin/event-types", `{"slug":"zz-empty-name","name":"  "}`); code != http.StatusBadRequest {
		t.Errorf("empty name status = %d, want 400", code)
	}
}

func TestAdminEventTypes_PatchRefusesSlug(t *testing.T) {
	pool, do := eventTypeServer(t)
	et := seedEventTypeRow(t, pool)
	for _, body := range []string{`{"slug":"other"}`, `{"slug":null,"name":"X"}`} {
		if code, _ := do(http.MethodPatch, typePath(et.ID), body); code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", body, code)
		}
	}
}

func TestAdminEventTypes_PatchRenameReorderAudits(t *testing.T) {
	pool, do := eventTypeServer(t)
	et := seedEventTypeRow(t, pool)
	code, body := do(http.MethodPatch, typePath(et.ID), `{"name":"New","sort_order":901}`)
	if code != http.StatusOK || !strings.Contains(body, `"name":"New"`) || !strings.Contains(body, `"sort_order":901`) {
		t.Fatalf("status = %d (%s)", code, body)
	}
	actions := eventTypeAuditActions(t, pool, et.ID)
	if len(actions) != 1 || actions[0] != audit.ActionEventTypeUpdated {
		t.Errorf("actions = %v", actions)
	}
}

func TestAdminEventTypes_PatchDisableAuditsDistinctAction(t *testing.T) {
	pool, do := eventTypeServer(t)
	et := seedEventTypeRow(t, pool)
	do(http.MethodPatch, typePath(et.ID), `{"enabled":false}`)
	do(http.MethodPatch, typePath(et.ID), `{"enabled":true}`)
	actions := eventTypeAuditActions(t, pool, et.ID)
	if len(actions) != 2 || actions[0] != audit.ActionEventTypeDisabled || actions[1] != audit.ActionEventTypeEnabled {
		t.Errorf("actions = %v", actions)
	}
}

func TestAdminEventTypes_PatchRefusesDisablingDefault(t *testing.T) {
	pool, do := eventTypeServer(t)
	code, body := do(http.MethodPatch, typePath(seededEventType(t, pool, "workshop")), `{"enabled":false}`)
	if code != http.StatusConflict || !strings.Contains(body, "choose another default first") {
		t.Errorf("status = %d (%s)", code, body)
	}
}

func TestAdminEventTypes_PatchIsDefaultMovesItAndAudits(t *testing.T) {
	pool, do := eventTypeServer(t)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `UPDATE event_types SET is_default = FALSE WHERE is_default`)
		_, _ = pool.Exec(ctx, `UPDATE event_types SET is_default = TRUE WHERE slug = 'workshop'`)
	})
	et := seedEventTypeRow(t, pool)
	code, body := do(http.MethodPatch, typePath(et.ID), `{"is_default":true}`)
	if code != http.StatusOK || !strings.Contains(body, `"is_default":true`) {
		t.Fatalf("status = %d (%s)", code, body)
	}
	actions := eventTypeAuditActions(t, pool, et.ID)
	if len(actions) != 1 || actions[0] != audit.ActionEventTypeDefaultChanged {
		t.Errorf("actions = %v", actions)
	}
}

func TestAdminEventTypes_PatchIsDefaultFalseRejected(t *testing.T) {
	pool, do := eventTypeServer(t)
	if code, _ := do(http.MethodPatch, typePath(seededEventType(t, pool, "workshop")), `{"is_default":false}`); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestAdminEventTypes_PatchIsDefaultWithOtherFieldsRejected(t *testing.T) {
	pool, do := eventTypeServer(t)
	et := seedEventTypeRow(t, pool)
	if code, _ := do(http.MethodPatch, typePath(et.ID), `{"is_default":true,"name":"X"}`); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
	got, err := eventtypes.NewStore(pool).GetByID(context.Background(), et.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsDefault || got.Name == "X" {
		t.Errorf("partially applied: %+v", got)
	}
}

func TestAdminEventTypes_DeleteInUseSaysDisable(t *testing.T) {
	pool, do := eventTypeServer(t)
	et := seedEventTypeRow(t, pool)
	w, err := workshops.NewStore(pool).Create(context.Background(), workshops.CreateInput{Title: fmt.Sprintf("zz-%d", testdb.Unique()), EventTypeID: &et.ID})
	if err != nil {
		t.Fatal(err)
	}
	cleanupAdminWorkshop(t, pool, w.ID)
	code, body := do(http.MethodDelete, typePath(et.ID), "")
	if code != http.StatusConflict || !strings.Contains(body, "disable it instead") {
		t.Errorf("status = %d (%s)", code, body)
	}
}

func TestAdminEventTypes_DeleteDefaultRefused(t *testing.T) {
	pool, do := eventTypeServer(t)
	code, body := do(http.MethodDelete, typePath(seededEventType(t, pool, "workshop")), "")
	if code != http.StatusConflict || !strings.Contains(body, "choose another default first") {
		t.Errorf("status = %d (%s)", code, body)
	}
}

func TestAdminEventTypes_DeleteUnusedAudits(t *testing.T) {
	pool, do := eventTypeServer(t)
	et := seedEventTypeRow(t, pool)
	if code, body := do(http.MethodDelete, typePath(et.ID), ""); code != http.StatusOK {
		t.Fatalf("status = %d (%s)", code, body)
	}
	actions := eventTypeAuditActions(t, pool, et.ID)
	if len(actions) != 1 || actions[0] != audit.ActionEventTypeDeleted {
		t.Errorf("actions = %v", actions)
	}
}

// invalidatingEventTypeServer is eventTypeServer with a counting invalidator.
func invalidatingEventTypeServer(t *testing.T, label string) (*pgxpool.Pool, *countingCacheInvalidator, func(method, path, body string) (int, string)) {
	t.Helper()
	pool := interestsTestPool(t)
	inv := &countingCacheInvalidator{}
	srv := httptest.NewServer(adminEventTypesMuxWith(pool, inv))
	t.Cleanup(srv.Close)
	token := "event-types-inv-token-" + label
	seedSession(t, pool, seedAdmin(t, pool, "event-types-inv-"+label+"@example.com"), token)
	return pool, inv, func(method, path, body string) (int, string) {
		resp := doJSON(t, srv.Client(), method, srv.URL+path, token, body)
		return resp.StatusCode, string(readBody(t, resp))
	}
}

func TestAdminEventTypes_MutationsInvalidateSEOCache(t *testing.T) {
	pool, inv, do := invalidatingEventTypeServer(t, "ok")
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `UPDATE event_types SET is_default = FALSE WHERE is_default`)
		_, _ = pool.Exec(ctx, `UPDATE event_types SET is_default = TRUE WHERE slug = 'workshop'`)
	})
	et := seedEventTypeRow(t, pool)
	spare := seedEventTypeRow(t, pool)

	createSlug := testEventTypeSlug(t, pool)
	steps := []struct {
		name, method, path, body string
		want                     int
	}{
		{"create", http.MethodPost, "/admin/event-types", fmt.Sprintf(`{"slug":%q,"name":"Zz new","sort_order":901}`, createSlug), http.StatusCreated},
		{"rename", http.MethodPatch, typePath(et.ID), `{"name":"Zz renamed"}`, http.StatusOK},
		{"disable", http.MethodPatch, typePath(et.ID), `{"enabled":false}`, http.StatusOK},
		{"reorder", http.MethodPatch, typePath(et.ID), `{"sort_order":902}`, http.StatusOK},
		{"set default", http.MethodPatch, typePath(spare.ID), `{"is_default":true}`, http.StatusOK},
		{"delete", http.MethodDelete, typePath(et.ID), "", http.StatusOK},
	}
	for i, step := range steps {
		code, body := do(step.method, step.path, step.body)
		if code != step.want {
			t.Fatalf("%s: status = %d, want %d (%s)", step.name, code, step.want, body)
		}
		if inv.calls != i+1 {
			t.Errorf("%s: invalidator.calls = %d, want %d", step.name, inv.calls, i+1)
		}
	}
}

func TestAdminEventTypes_RefusedMutationDoesNotInvalidate(t *testing.T) {
	pool, inv, do := invalidatingEventTypeServer(t, "refused")
	et := seedEventTypeRow(t, pool)
	w, err := workshops.NewStore(pool).Create(context.Background(), workshops.CreateInput{Title: fmt.Sprintf("zz-%d", testdb.Unique()), EventTypeID: &et.ID})
	if err != nil {
		t.Fatal(err)
	}
	cleanupAdminWorkshop(t, pool, w.ID)

	for _, step := range []struct{ method, path, body string }{
		{http.MethodPost, "/admin/event-types", `{"slug":"Not A Slug","name":"X"}`},
		{http.MethodPatch, typePath(et.ID), `{"slug":"changed"}`},
		{http.MethodDelete, typePath(et.ID), ""},
	} {
		code, body := do(step.method, step.path, step.body)
		if code < 400 {
			t.Fatalf("%s %s: status = %d, want a refusal (%s)", step.method, step.path, code, body)
		}
	}
	if inv.calls != 0 {
		t.Errorf("invalidator.calls = %d after refused writes, want 0", inv.calls)
	}
}
