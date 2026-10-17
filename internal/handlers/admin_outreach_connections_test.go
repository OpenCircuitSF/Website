package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreachconn"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// Fakes only: nothing here reaches a real ShortLinks, Bluesky, or Mastodon
// server. Secrets are canary strings so a leak is easy to spot.
const (
	connEnvCanary = "env-secret-CANARY"
	connDBCanary  = "db-secret-CANARY"
)

type connEnv struct {
	srv  *httptest.Server
	pool *pgxpool.Pool
	mgr  *outreachconn.Manager
	ws   *workshops.Store
}

func connQuiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newConnEnv mounts the connection routes and a live-backed outreach handler
// on one mux, behind the real session and admin middleware.
func newConnEnv(t *testing.T, env map[string]string) *connEnv {
	t.Helper()
	pool := outreachTestPool(t)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM outreach_connection_values`) })
	pool.Exec(context.Background(), `DELETE FROM outreach_connection_values`)
	mgr := outreachconn.NewManager(outreachconn.NewStore(pool), env, connQuiet())
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	ws := workshops.NewStore(pool)
	oh := NewAdminOutreachHandler(outreach.NewStore(pool), outreach.NewStore(pool), outreach.NewStore(pool), ws, mgr.ShortLinker(), audit.New(pool), "https://site.example.test", nil)
	mgr.OnChange(func(c outreachconn.Changed) {
		if c.ShortLinks {
			oh.ResetShortLinks()
		}
	})
	ch := NewAdminOutreachConnectionsHandler(mgr, audit.New(pool))
	requireSession := middleware.RequireSession(auth.NewStore(pool))
	requireAdmin := func(next http.Handler) http.Handler { return requireSession(middleware.RequireAdmin(next)) }
	mux := http.NewServeMux()
	mux.Handle("GET /admin/outreach-connections", requireAdmin(http.HandlerFunc(ch.Get)))
	mux.Handle("PATCH /admin/outreach-connections", requireAdmin(http.HandlerFunc(ch.Patch)))
	mux.Handle("POST /admin/outreach-connections/shortlinks/test", requireAdmin(http.HandlerFunc(ch.TestShortLinks)))
	mux.Handle("POST /admin/outreach-connections/bluesky/test", requireAdmin(http.HandlerFunc(ch.TestBluesky)))
	mux.Handle("POST /admin/outreach-connections/mastodon/test", requireAdmin(http.HandlerFunc(ch.TestMastodon)))
	mux.Handle("GET /admin/workshops/{id}/outreach", requireAdmin(http.HandlerFunc(oh.Get)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	admin := seedAdmin(t, pool, "admin-conn@example.com")
	seedSession(t, pool, admin, "conn-admin-token")
	user := seedUser(t, pool, "user-conn@example.com")
	seedSession(t, pool, user, "conn-user-token")
	return &connEnv{srv: srv, pool: pool, mgr: mgr, ws: ws}
}

func (e *connEnv) do(t *testing.T, token, method, path, body string) (int, []byte) {
	t.Helper()
	resp := doJSON(t, e.srv.Client(), method, e.srv.URL+path, token, body)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *connEnv) admin(t *testing.T, method, path, body string) (int, []byte) {
	return e.do(t, "conn-admin-token", method, path, body)
}

func (e *connEnv) auditRows(t *testing.T) []string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT metadata::text FROM audit_log WHERE action = $1`, audit.ActionOutreachConnectionsUpdated)
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

func connField(t *testing.T, body []byte, key string) map[string]any {
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

func TestOutreachConnections_AuthGuards(t *testing.T) {
	e := newConnEnv(t, nil)
	routes := []struct{ method, path, body string }{
		{"GET", "/admin/outreach-connections", ""},
		{"PATCH", "/admin/outreach-connections", `{"values":{}}`},
		{"POST", "/admin/outreach-connections/shortlinks/test", ""},
		{"POST", "/admin/outreach-connections/bluesky/test", ""},
		{"POST", "/admin/outreach-connections/mastodon/test", ""},
	}
	for _, r := range routes {
		if code, _ := e.do(t, "", r.method, r.path, r.body); code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", r.method, r.path, code)
		}
		if code, _ := e.do(t, "conn-user-token", r.method, r.path, r.body); code != http.StatusForbidden {
			t.Errorf("%s %s as non-admin = %d, want 403", r.method, r.path, code)
		}
	}
}

func TestOutreachConnections_GetHidesSecrets(t *testing.T) {
	e := newConnEnv(t, map[string]string{
		outreachconn.KeyMastodonToken: connEnvCanary, outreachconn.KeyShortLinksAPIToken: connEnvCanary,
		outreachconn.KeyBlueskyAppPassword: connEnvCanary,
	})
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO outreach_connection_values (key, value) VALUES ('mastodon_token', $1)`, connDBCanary); err != nil {
		t.Fatal(err)
	}
	if err := e.mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	code, body := e.admin(t, "GET", "/admin/outreach-connections", "")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	if strings.Contains(string(body), "CANARY") {
		t.Errorf("response leaked a secret: %s", body)
	}
	for _, k := range []string{"shortlinks_api_token", "bluesky_app_password", "mastodon_token"} {
		f := connField(t, body, k)
		if f["set"] != true || f["secret"] != true {
			t.Errorf("%s = %v, want set and secret", k, f)
		}
		if _, has := f["value"]; has {
			t.Errorf("%s must carry no value key: %v", k, f)
		}
	}
	if f := connField(t, body, "mastodon_token"); f["source"] != "database" {
		t.Errorf("mastodon_token source = %v", f["source"])
	}
	if f := connField(t, body, "shortlinks_api_token"); f["source"] != "environment" {
		t.Errorf("shortlinks_api_token source = %v", f["source"])
	}
}

func TestOutreachConnections_PatchSetClearAndAudit(t *testing.T) {
	e := newConnEnv(t, map[string]string{outreachconn.KeyMastodonBaseURL: "https://env.example"})
	code, body := e.admin(t, "PATCH", "/admin/outreach-connections",
		`{"values":{"mastodon_base_url":"https://db.example","mastodon_token":"`+connDBCanary+`"}}`)
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	if strings.Contains(string(body), "CANARY") {
		t.Errorf("PATCH response leaked a secret: %s", body)
	}
	if f := connField(t, body, "mastodon_base_url"); f["source"] != "database" || f["value"] != "https://db.example" {
		t.Errorf("mastodon_base_url = %v", f)
	}
	rows := e.auditRows(t)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	var meta struct{ Set, Cleared []string }
	json.Unmarshal([]byte(rows[0]), &meta)
	if len(meta.Set) != 2 || meta.Set[0] != "mastodon_base_url" || meta.Set[1] != "mastodon_token" {
		t.Errorf("audit set = %v", meta.Set)
	}
	if strings.Contains(rows[0], "CANARY") || strings.Contains(rows[0], "db.example") {
		t.Errorf("audit metadata carries a value: %s", rows[0])
	}

	// Clear reverts to the environment value.
	code, body = e.admin(t, "PATCH", "/admin/outreach-connections", `{"values":{"mastodon_base_url":"  "}}`)
	if code != 200 {
		t.Fatalf("clear status %d: %s", code, body)
	}
	if f := connField(t, body, "mastodon_base_url"); f["source"] != "environment" || f["value"] != "https://env.example" {
		t.Errorf("after clear = %v", f)
	}
	rows = e.auditRows(t)
	if len(rows) != 2 || !strings.Contains(rows[1], `"cleared": ["mastodon_base_url"]`) {
		t.Errorf("audit rows after clear = %v", rows)
	}

	// A no-op PATCH writes nothing.
	code, _ = e.admin(t, "PATCH", "/admin/outreach-connections", `{"values":{"mastodon_base_url":"","mastodon_token":"`+connDBCanary+`"}}`)
	if code != 200 {
		t.Fatalf("no-op status %d", code)
	}
	if n := len(e.auditRows(t)); n != 2 {
		t.Errorf("a no-op PATCH wrote an audit row: %d rows", n)
	}
}

func TestOutreachConnections_PatchValidation(t *testing.T) {
	e := newConnEnv(t, nil)
	code, body := e.admin(t, "PATCH", "/admin/outreach-connections",
		`{"values":{"mastodon_token":"good-token-fake","mastodon_base_url":"http://example.com/BAD-VALUE-CANARY"}}`)
	if code != 400 || !strings.Contains(string(body), "mastodon_base_url") || strings.Contains(string(body), "CANARY") {
		t.Errorf("status %d body %s", code, body)
	}
	var n int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outreach_connection_values`).Scan(&n)
	if n != 0 {
		t.Errorf("a rejected request wrote %d rows", n)
	}
	for _, c := range []struct{ name, body string }{
		{"unknown key", `{"values":{"nope":"x"}}`},
		{"null entry", `{"values":{"mastodon_token":null}}`},
		{"empty values", `{"values":{}}`},
		{"missing values", `{}`},
	} {
		if code, body := e.admin(t, "PATCH", "/admin/outreach-connections", c.body); code != 400 {
			t.Errorf("%s: status %d body %s", c.name, code, body)
		}
	}
}

func TestOutreachConnections_TestEndpoint(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me" && r.Header.Get("Authorization") == "Bearer sl-token-fake" {
			io.WriteString(w, `{"email":"admin@example.test","base_url":"https://go.example.test"}`)
			return
		}
		w.WriteHeader(401)
	}))
	defer fake.Close()
	e := newConnEnv(t, nil)
	if code, body := e.admin(t, "PATCH", "/admin/outreach-connections",
		`{"values":{"shortlinks_base_url":"`+fake.URL+`","shortlinks_api_token":"sl-token-fake"}}`); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	code, body := e.admin(t, "POST", "/admin/outreach-connections/shortlinks/test", "")
	var res struct {
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	json.Unmarshal(body, &res)
	if code != 200 || !res.OK || !strings.Contains(res.Detail, "admin@example.test") {
		t.Errorf("shortlinks test: %d %s", code, body)
	}
	code, body = e.admin(t, "POST", "/admin/outreach-connections/bluesky/test", "")
	json.Unmarshal(body, &res)
	if code != 200 || res.OK {
		t.Errorf("an unconfigured bluesky test must answer 200 ok:false: %d %s", code, body)
	}
}

func TestOutreachConnections_TakesEffectWithoutRestart(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"email":"admin@example.test","base_url":"https://go.example.test"}`)
	}))
	defer fake.Close()
	e := newConnEnv(t, nil)
	wk := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	path := fmt.Sprintf("/admin/workshops/%d/outreach", wk.ID)
	panel := func() (bool, string) {
		code, body := e.admin(t, "GET", path, "")
		if code != 200 {
			t.Fatalf("panel: %d %s", code, body)
		}
		var p struct {
			Available         bool   `json:"available"`
			UnavailableReason string `json:"unavailable_reason"`
		}
		json.Unmarshal(body, &p)
		return p.Available, p.UnavailableReason
	}
	if avail, reason := panel(); avail || reason != "shortlinks_not_configured" {
		t.Fatalf("before: available=%v reason=%q", avail, reason)
	}
	if code, body := e.admin(t, "PATCH", "/admin/outreach-connections",
		`{"values":{"shortlinks_base_url":"`+fake.URL+`","shortlinks_api_token":"sl-token-fake"}}`); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	if avail, reason := panel(); !avail {
		t.Errorf("after save: available=%v reason=%q", avail, reason)
	}
}
