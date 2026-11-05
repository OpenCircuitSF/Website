package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/venues"
)

// seedVenue adds a throwaway venue. Its cleanup clears the venue from any listing
// first, so it works whatever order cleanups run in.
func seedVenue(t *testing.T, pool *pgxpool.Pool, name string, address, note *string) int64 {
	t.Helper()
	ctx := context.Background()
	v, err := venues.NewStore(pool).Create(ctx, venues.CreateInput{Name: name, Address: address, Note: note, SortOrder: 900})
	if err != nil {
		t.Fatalf("seed venue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE workshops SET venue_id = NULL WHERE venue_id = $1`, v.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM venues WHERE id = $1`, v.ID)
	})
	return v.ID
}

func disableVenue(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE venues SET enabled = FALSE WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

const restoreDefaultVenueSQL = `INSERT INTO settings (key, value, updated_at)
	SELECT 'default_venue_id', id::text, now() FROM venues WHERE name = 'Sequoia Fabrica'
	ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`

// removeDefaultVenueSetting deletes the default's settings row and puts it back at
// cleanup, naming the seeded venue.
func removeDefaultVenueSetting(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, restoreDefaultVenueSQL) })
	if _, err := pool.Exec(ctx, `DELETE FROM settings WHERE key = 'default_venue_id'`); err != nil {
		t.Fatal(err)
	}
}

func adminVenuesMux(pool *pgxpool.Pool, invalidator seoCacheInvalidator) http.Handler {
	h := NewAdminVenuesHandler(venues.NewStore(pool), audit.New(pool), invalidator)
	requireSession := middleware.RequireSession(auth.NewStore(pool))
	guard := func(f http.HandlerFunc) http.Handler { return requireSession(middleware.RequireAdmin(f)) }
	mux := http.NewServeMux()
	mux.Handle("GET /admin/venues", guard(h.List))
	mux.Handle("POST /admin/venues", guard(h.Create))
	mux.Handle("PATCH /admin/venues/{id}", guard(h.Patch))
	mux.Handle("DELETE /admin/venues/{id}", guard(h.Delete))
	return mux
}

func venueServer(t *testing.T) (*pgxpool.Pool, *countingCacheInvalidator, func(method, path, body string) (int, string)) {
	t.Helper()
	pool := interestsTestPool(t)
	inv := &countingCacheInvalidator{}
	srv := httptest.NewServer(adminVenuesMux(pool, inv))
	t.Cleanup(srv.Close)
	seedSession(t, pool, seedAdmin(t, pool, "admin-venues@example.com"), "venues-admin-token")
	return pool, inv, func(method, path, body string) (int, string) {
		t.Helper()
		resp := doJSON(t, srv.Client(), method, srv.URL+path, "venues-admin-token", body)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
}

func venuePath(id int64) string { return fmt.Sprintf("/admin/venues/%d", id) }

func venueAuditActions(t *testing.T, pool *pgxpool.Pool, id int64) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT action FROM audit_log WHERE target_type = $1 AND target_id = $2 ORDER BY id ASC`, audit.TargetVenue, id)
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

func seededVenueID(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM venues WHERE name = 'Sequoia Fabrica' ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAdminVenues_RequiresAdmin(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminVenuesMux(pool, nil))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/admin/venues")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d, want 401", resp.StatusCode)
	}
	user := seedUser(t, pool, "venues-nonadmin@example.com")
	seedSession(t, pool, user, "venues-nonadmin-token")
	r := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/admin/venues", "venues-nonadmin-token", `{"name":"x"}`)
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403", r.StatusCode)
	}
}

func TestAdminVenues_CreateAudits(t *testing.T) {
	pool, inv, do := venueServer(t)
	name := fmt.Sprintf("Zz created %d", testdb.Unique())
	code, body := do(http.MethodPost, "/admin/venues", fmt.Sprintf(`{"name":"  %s  ","address":"1 Test St","note":""}`, name))
	if code != http.StatusCreated {
		t.Fatalf("status = %d (%s)", code, body)
	}
	var id int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM venues WHERE name = $1`, name).Scan(&id); err != nil {
		t.Fatalf("trimmed name not stored: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM venues WHERE id = $1`, id) })
	if !strings.Contains(body, `"address":"1 Test St"`) || !strings.Contains(body, `"note":null`) {
		t.Errorf("body = %s", body)
	}
	actions := venueAuditActions(t, pool, id)
	if len(actions) != 1 || actions[0] != audit.ActionVenueCreated {
		t.Errorf("actions = %v", actions)
	}
	if inv.calls != 1 {
		t.Errorf("invalidations = %d, want 1", inv.calls)
	}
	if code, body := do(http.MethodPost, "/admin/venues", `{"name":"   "}`); code != http.StatusBadRequest {
		t.Errorf("blank name status = %d (%s)", code, body)
	}
}

func TestAdminVenues_PatchAuditsAndInvalidates(t *testing.T) {
	pool, inv, do := venueServer(t)
	id := seedVenue(t, pool, "Zz patch", nil, nil)
	code, body := do(http.MethodPatch, venuePath(id), `{"address":"2 Test St","sort_order":950}`)
	if code != http.StatusOK || !strings.Contains(body, `"address":"2 Test St"`) || !strings.Contains(body, `"sort_order":950`) {
		t.Fatalf("status = %d (%s)", code, body)
	}
	actions := venueAuditActions(t, pool, id)
	if len(actions) != 1 || actions[0] != audit.ActionVenueUpdated {
		t.Errorf("actions = %v", actions)
	}
	if inv.calls != 1 {
		t.Errorf("invalidations = %d, want 1", inv.calls)
	}
}

func TestAdminVenues_NoteVerbatimAndBlankClears(t *testing.T) {
	pool, _, do := venueServer(t)
	id := seedVenue(t, pool, "Zz note", nil, nil)
	code, body := do(http.MethodPatch, venuePath(id), `{"note":"  Padded real content  "}`)
	if code != http.StatusOK || !strings.Contains(body, `"note":"  Padded real content  "`) {
		t.Errorf("status = %d (%s)", code, body)
	}
	code, body = do(http.MethodPatch, venuePath(id), `{"note":"   "}`)
	if code != http.StatusOK || !strings.Contains(body, `"note":null`) {
		t.Errorf("status = %d (%s)", code, body)
	}
}

func TestAdminVenues_DisableAuditsDisabledAction(t *testing.T) {
	pool, _, do := venueServer(t)
	id := seedVenue(t, pool, "Zz disable", nil, nil)
	if code, body := do(http.MethodPatch, venuePath(id), `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("status = %d (%s)", code, body)
	}
	if code, body := do(http.MethodPatch, venuePath(id), `{"enabled":true}`); code != http.StatusOK {
		t.Fatalf("status = %d (%s)", code, body)
	}
	actions := venueAuditActions(t, pool, id)
	if len(actions) != 2 || actions[0] != audit.ActionVenueDisabled || actions[1] != audit.ActionVenueEnabled {
		t.Errorf("actions = %v", actions)
	}
}

func TestAdminVenues_PatchIsDefaultMovesItAndAudits(t *testing.T) {
	pool, _, do := venueServer(t)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), restoreDefaultVenueSQL) })
	id := seedVenue(t, pool, "Zz default", nil, nil)
	code, body := do(http.MethodPatch, venuePath(id), `{"is_default":true}`)
	if code != http.StatusOK || !strings.Contains(body, `"is_default":true`) {
		t.Fatalf("status = %d (%s)", code, body)
	}
	actions := venueAuditActions(t, pool, id)
	if len(actions) != 1 || actions[0] != audit.ActionVenueDefaultChanged {
		t.Errorf("actions = %v", actions)
	}
	var setting string
	if err := pool.QueryRow(context.Background(), `SELECT value FROM settings WHERE key = 'default_venue_id'`).Scan(&setting); err != nil {
		t.Fatal(err)
	}
	if setting != strconv.FormatInt(id, 10) {
		t.Errorf("setting = %q, want %d", setting, id)
	}
	disabled := seedVenue(t, pool, "Zz default disabled", nil, nil)
	disableVenue(t, pool, disabled)
	code, body = do(http.MethodPatch, venuePath(disabled), `{"is_default":true}`)
	if code != http.StatusConflict || !strings.Contains(body, "only an enabled venue can be the default; enable it first") {
		t.Errorf("disabled status = %d (%s)", code, body)
	}
}

func TestAdminVenues_IsDefaultCombinedRefused(t *testing.T) {
	pool, _, do := venueServer(t)
	id := seedVenue(t, pool, "Zz combined", nil, nil)
	if code, body := do(http.MethodPatch, venuePath(id), `{"is_default":true,"name":"x"}`); code != http.StatusBadRequest {
		t.Errorf("combined status = %d (%s)", code, body)
	}
	if code, body := do(http.MethodPatch, venuePath(id), `{"is_default":false}`); code != http.StatusBadRequest {
		t.Errorf("false status = %d (%s)", code, body)
	}
}

func TestAdminVenues_DeleteInUseConflicts(t *testing.T) {
	pool, inv, do := venueServer(t)
	id := seedVenue(t, pool, "Zz in use", nil, nil)
	var listing int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO workshops (slug, title, venue_id) VALUES ($1, $1, $2) RETURNING id`,
		fmt.Sprintf("zz-venue-use-%d", testdb.Unique()), id).Scan(&listing); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, listing) })
	code, body := do(http.MethodDelete, venuePath(id), "")
	if code != http.StatusConflict || !strings.Contains(body, "cannot delete a venue that listings still use; disable it instead") {
		t.Errorf("status = %d (%s)", code, body)
	}
	if inv.calls != 0 {
		t.Errorf("invalidations = %d, want 0", inv.calls)
	}
}

func TestAdminVenues_DeleteDefaultConflicts(t *testing.T) {
	pool, _, do := venueServer(t)
	code, body := do(http.MethodDelete, venuePath(seededVenueID(t, pool)), "")
	if code != http.StatusConflict || !strings.Contains(body, "cannot delete the default venue; choose another default first") {
		t.Errorf("status = %d (%s)", code, body)
	}
}

func TestAdminVenues_DisableDefaultConflicts(t *testing.T) {
	pool, _, do := venueServer(t)
	code, body := do(http.MethodPatch, venuePath(seededVenueID(t, pool)), `{"enabled":false}`)
	if code != http.StatusConflict || !strings.Contains(body, "cannot disable the default venue; choose another default first") {
		t.Errorf("status = %d (%s)", code, body)
	}
}

func TestAdminVenues_ListWithMissingSettingStillLoads(t *testing.T) {
	pool, _, do := venueServer(t)
	removeDefaultVenueSetting(t, pool)
	code, body := do(http.MethodGet, "/admin/venues", "")
	if code != http.StatusOK || !strings.Contains(body, "Sequoia Fabrica") || strings.Contains(body, `"is_default":true`) {
		t.Errorf("status = %d (%s)", code, body)
	}
	id := seedVenue(t, pool, "Zz no default", nil, nil)
	code, body = do(http.MethodPatch, venuePath(id), `{"enabled":false}`)
	if code != http.StatusConflict || !strings.Contains(body, "no default venue is set; choose one first") {
		t.Errorf("disable status = %d (%s)", code, body)
	}
}

func TestAdminVenues_MakeDefaultRepairsMissingSetting(t *testing.T) {
	pool, _, do := venueServer(t)
	removeDefaultVenueSetting(t, pool)
	code, body := do(http.MethodPatch, venuePath(seededVenueID(t, pool)), `{"is_default":true}`)
	if code != http.StatusOK || !strings.Contains(body, `"is_default":true`) {
		t.Errorf("status = %d (%s)", code, body)
	}
}
