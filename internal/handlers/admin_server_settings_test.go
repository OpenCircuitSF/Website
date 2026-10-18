package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/serversettings"
)

type srvSetEnv struct {
	srv  *httptest.Server
	pool *pgxpool.Pool
	mgr  *serversettings.Manager
}

// newSrvSetEnv mounts the server settings routes and a live-backed media
// upload route on one mux, behind the real session and admin middleware.
func newSrvSetEnv(t *testing.T, env, defaults map[string]string) *srvSetEnv {
	t.Helper()
	pool := outreachTestPool(t)
	clean := func() { pool.Exec(context.Background(), `DELETE FROM server_setting_values`) }
	t.Cleanup(clean)
	clean()
	mgr := serversettings.NewManager(serversettings.NewStore(pool), env, defaults, nil, connQuiet())
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := NewAdminServerSettingsHandler(mgr, audit.New(pool))
	mh := NewAdminMediaHandlerLive(mgr.MediaDir, audit.New(pool))
	requireSession := middleware.RequireSession(auth.NewStore(pool))
	requireAdmin := func(next http.Handler) http.Handler { return requireSession(middleware.RequireAdmin(next)) }
	mux := http.NewServeMux()
	mux.Handle("GET /admin/server-settings", requireAdmin(http.HandlerFunc(h.Get)))
	mux.Handle("PATCH /admin/server-settings", requireAdmin(http.HandlerFunc(h.Patch)))
	mux.Handle("POST /admin/media/upload", requireAdmin(http.HandlerFunc(mh.Upload)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	admin := seedAdmin(t, pool, "admin-srvset@example.com")
	seedSession(t, pool, admin, "srvset-admin-token")
	user := seedUser(t, pool, "user-srvset@example.com")
	seedSession(t, pool, user, "srvset-user-token")
	return &srvSetEnv{srv: srv, pool: pool, mgr: mgr}
}

func (e *srvSetEnv) do(t *testing.T, token, method, path, body string) (int, []byte) {
	t.Helper()
	resp := doJSON(t, e.srv.Client(), method, e.srv.URL+path, token, body)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *srvSetEnv) admin(t *testing.T, method, path, body string) (int, []byte) {
	return e.do(t, "srvset-admin-token", method, path, body)
}

func (e *srvSetEnv) auditRows(t *testing.T) []string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT metadata::text FROM audit_log WHERE action = $1`, audit.ActionServerSettingsUpdated)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func srvSetField(t *testing.T, body []byte, key string) map[string]any {
	t.Helper()
	var v struct {
		Groups []struct {
			Fields []map[string]any `json:"fields"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	for _, g := range v.Groups {
		for _, f := range g.Fields {
			if f["key"] == key {
				return f
			}
		}
	}
	t.Fatalf("no field %q in %s", key, body)
	return nil
}

func TestServerSettings_AuthGuards(t *testing.T) {
	e := newSrvSetEnv(t, nil, nil)
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/admin/server-settings", ""},
		{"PATCH", "/admin/server-settings", `{"values":{}}`},
	} {
		if code, _ := e.do(t, "", r.method, r.path, r.body); code != http.StatusUnauthorized {
			t.Errorf("%s without a session = %d, want 401", r.method, code)
		}
		if code, _ := e.do(t, "srvset-user-token", r.method, r.path, r.body); code != http.StatusForbidden {
			t.Errorf("%s as non-admin = %d, want 403", r.method, code)
		}
	}
}

func TestServerSettings_GetShape(t *testing.T) {
	e := newSrvSetEnv(t, nil, nil)
	code, body := e.admin(t, "GET", "/admin/server-settings", "")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var v struct {
		Groups []struct {
			Name   string `json:"name"`
			Fields []struct {
				Key             string `json:"key"`
				RestartRequired bool   `json:"restart_required"`
			} `json:"fields"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	var names []string
	restart := map[string]bool{}
	for _, g := range v.Groups {
		names = append(names, g.Name)
		for _, f := range g.Fields {
			restart[f.Key] = f.RestartRequired
		}
	}
	if strings.Join(names, ",") != "media,email,ses,sending" {
		t.Errorf("group order = %v", names)
	}
	for _, k := range []string{"email_from", "email_reply_to", "email_list_domain", "send_batch_size"} {
		if !restart[k] {
			t.Errorf("%s must be restart_required", k)
		}
	}
	for _, k := range []string{"media_dir", "ses_sandbox", "ses_inbound_bucket"} {
		if restart[k] {
			t.Errorf("%s must be live", k)
		}
	}
}

func TestServerSettings_PatchSetClearAndAudit(t *testing.T) {
	e := newSrvSetEnv(t, map[string]string{serversettings.KeyEmailReplyTo: "env@example.org"}, nil)
	code, body := e.admin(t, "PATCH", "/admin/server-settings", `{"values":{"email_reply_to":"private@example.org"}}`)
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	f := srvSetField(t, body, "email_reply_to")
	if f["source"] != "database" || f["value"] != "private@example.org" || f["pending_restart"] != true {
		t.Errorf("email_reply_to = %v", f)
	}
	var n int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM server_setting_values WHERE key='email_reply_to'`).Scan(&n)
	if n != 1 {
		t.Errorf("row count = %d", n)
	}
	rows := e.auditRows(t)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	var meta struct{ Set, Cleared []string }
	json.Unmarshal([]byte(rows[0]), &meta)
	if len(meta.Set) != 1 || meta.Set[0] != "email_reply_to" {
		t.Errorf("audit set = %v", meta.Set)
	}
	if strings.Contains(rows[0], "private@example.org") {
		t.Errorf("audit metadata carries a value: %s", rows[0])
	}

	code, body = e.admin(t, "PATCH", "/admin/server-settings", `{"values":{"email_reply_to":" "}}`)
	if code != 200 {
		t.Fatalf("clear status %d: %s", code, body)
	}
	if f := srvSetField(t, body, "email_reply_to"); f["source"] != "environment" || f["value"] != "env@example.org" {
		t.Errorf("after clear = %v", f)
	}
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM server_setting_values`).Scan(&n)
	if n != 0 {
		t.Errorf("clear left %d rows", n)
	}
	rows = e.auditRows(t)
	if len(rows) != 2 || !strings.Contains(rows[1], `"cleared": ["email_reply_to"]`) {
		t.Errorf("audit rows after clear = %v", rows)
	}

	if code, _ = e.admin(t, "PATCH", "/admin/server-settings", `{"values":{"email_reply_to":""}}`); code != 200 {
		t.Fatalf("no-op status %d", code)
	}
	if got := len(e.auditRows(t)); got != 2 {
		t.Errorf("a no-op PATCH wrote an audit row: %d rows", got)
	}
}

func TestServerSettings_PatchValidation(t *testing.T) {
	e := newSrvSetEnv(t, nil, nil)
	code, body := e.admin(t, "PATCH", "/admin/server-settings",
		`{"values":{"email_from":"good@example.org","send_batch_size":"99999-CANARY"}}`)
	if code != 400 || !strings.Contains(string(body), "send_batch_size") || strings.Contains(string(body), "CANARY") {
		t.Errorf("status %d body %s", code, body)
	}
	var n int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM server_setting_values`).Scan(&n)
	if n != 0 {
		t.Errorf("a rejected request wrote %d rows", n)
	}
	for _, c := range []struct{ name, body string }{
		{"unknown key", `{"values":{"nope":"x"}}`},
		{"null entry", `{"values":{"media_dir":null}}`},
		{"empty values", `{"values":{}}`},
		{"missing values", `{}`},
	} {
		if code, body := e.admin(t, "PATCH", "/admin/server-settings", c.body); code != 400 {
			t.Errorf("%s: status %d body %s", c.name, code, body)
		}
	}
}

// A media directory saved through the API takes effect on the next upload,
// with no restart.
func TestServerSettings_MediaDirLiveWithoutRestart(t *testing.T) {
	e := newSrvSetEnv(t, nil, map[string]string{serversettings.KeyMediaDir: ""})
	upload := func() *http.Response {
		return doMediaUpload(t, e.srv.Client(), e.srv.URL+"/admin/media/upload", "srvset-admin-token", buildTestJPEG(t, 8, 8, false), "photo.jpg")
	}
	resp := upload()
	body := readBody(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "Settings → Server settings") || !strings.Contains(string(body), "enable-media-upload") {
		t.Fatalf("before: status %d body %s", resp.StatusCode, body)
	}
	dir := t.TempDir()
	pb, _ := json.Marshal(map[string]any{"values": map[string]string{"media_dir": dir}})
	if code, b := e.admin(t, "PATCH", "/admin/server-settings", string(pb)); code != 200 {
		t.Fatalf("patch status %d: %s", code, b)
	}
	resp = upload()
	body = readBody(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("after: status %d body %s", resp.StatusCode, body)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("want one file in %s, got %v", filepath.Base(dir), entries)
	}
}
