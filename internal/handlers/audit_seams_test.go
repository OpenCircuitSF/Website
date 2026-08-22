package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// auditRow is one persisted audit_log row, decoded for assertions.
type auditRow struct {
	ActorID    *int64
	UserID     *int64
	Action     string
	TargetType *string
	TargetID   *int64
	Metadata   map[string]any
}

// lastAuditFor returns the most recent audit_log row with the given action, or
// fails the test if none exists. It proves a seam fired AND lets the caller
// assert actor/target/metadata.
func lastAuditFor(t *testing.T, pool *pgxpool.Pool, action string) auditRow {
	t.Helper()
	var (
		row     auditRow
		metaRaw []byte
	)
	err := pool.QueryRow(context.Background(),
		`SELECT actor_id, user_id, action, target_type, target_id, metadata
		   FROM audit_log WHERE action = $1 ORDER BY id DESC LIMIT 1`, action,
	).Scan(&row.ActorID, &row.UserID, &row.Action, &row.TargetType, &row.TargetID, &metaRaw)
	if err != nil {
		t.Fatalf("no audit_log row for action %q: %v", action, err)
	}
	if metaRaw != nil {
		if err := json.Unmarshal(metaRaw, &row.Metadata); err != nil {
			t.Fatalf("decode metadata for %q: %v", action, err)
		}
	}
	return row
}

// auditAdminMux wires the admin settings route with a real audit.Logger so the
// settings.updated seam writes a row.
func auditAdminMux(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	authStore := auth.NewStore(pool)
	logger := audit.New(pool)
	settingsH := NewSettingsHandler(authStore, logger)
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("PATCH /admin/settings", requireAdmin(http.HandlerFunc(settingsH.Patch)))
	return mux
}

// TestAudit_SettingsUpdated proves PATCH /admin/settings writes a
// settings.updated row with {key, old_value, new_value}.
func TestAudit_SettingsUpdated(t *testing.T) {
	pool := credsTestPool(t)
	srv := httptest.NewServer(auditAdminMux(t, pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin@example.com")
	seedSession(t, pool, admin, "admin-token")
	// Seed the setting at "false" so the update flips it to "true".
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO settings (key, value, updated_at) VALUES ('registrations_enabled','false', now())
		 ON CONFLICT (key) DO UPDATE SET value = 'false'`); err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	// #0130: credsTestPool (unlike settingsTestPool) does not reset the
	// settings table, and this test's PATCH flips registrations_enabled to
	// "true" and leaves it there. Restore the full migration seed on
	// cleanup so that value cannot leak into whichever test runs next
	// under -shuffle=on (#0121's defect class, one table over).
	t.Cleanup(func() { resetSettings(t, pool) })

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admin/settings",
		jsonBody(`{"key":"registrations_enabled","value":"true"}`))
	resp, err := srv.Client().Do(withCookie(req, "admin-token"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	row := lastAuditFor(t, pool, audit.ActionSettingsUpdated)
	if row.ActorID == nil || *row.ActorID != admin {
		t.Errorf("actor_id = %v, want %d", row.ActorID, admin)
	}
	if row.TargetType == nil || *row.TargetType != audit.TargetSettings {
		t.Errorf("target_type = %v, want %q", row.TargetType, audit.TargetSettings)
	}
	if row.Metadata["key"] != "registrations_enabled" ||
		row.Metadata["old_value"] != "false" || row.Metadata["new_value"] != "true" {
		t.Errorf("metadata = %v, want key/old=false/new=true", row.Metadata)
	}
}
