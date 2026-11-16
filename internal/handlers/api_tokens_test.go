package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

type apiTokEnv struct {
	srv  *httptest.Server
	pool *pgxpool.Pool
	h    *APITokensHandler
}

func newAPITokEnv(t *testing.T) *apiTokEnv {
	t.Helper()
	pool := outreachTestPool(t)
	clean := func() { pool.Exec(context.Background(), `DELETE FROM api_tokens`) }
	t.Cleanup(clean)
	clean()
	h := NewAPITokensHandler(apitokens.NewStore(pool), audit.New(pool))
	requireSession := middleware.RequireSession(auth.NewStore(pool))
	requireAdmin := func(next http.Handler) http.Handler { return requireSession(middleware.RequireAdmin(next)) }
	mux := http.NewServeMux()
	mux.Handle("GET /admin/api-tokens", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("POST /admin/api-tokens", requireAdmin(http.HandlerFunc(h.Create)))
	mux.Handle("POST /admin/api-tokens/{id}/revoke", requireAdmin(http.HandlerFunc(h.Revoke)))
	h.SetV1(BuildV1Registry(api.Config{Version: "test"}, V1Deps{}))
	mux.Handle("/api/v1/", api.NewHTTPRouter(h.V1(), api.RouterDeps{Resolver: h.Resolver(), Limiter: middleware.NewAPITokenRateLimiter()}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	admin := seedAdmin(t, pool, "admin-apitok@example.com")
	seedSession(t, pool, admin, "apitok-admin-token")
	user := seedUser(t, pool, "user-apitok@example.com")
	seedSession(t, pool, user, "apitok-user-token")
	return &apiTokEnv{srv: srv, pool: pool, h: h}
}

func (e *apiTokEnv) do(t *testing.T, token, method, path, body string) (int, []byte) {
	t.Helper()
	resp := doJSON(t, e.srv.Client(), method, e.srv.URL+path, token, body)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *apiTokEnv) admin(t *testing.T, method, path, body string) (int, []byte) {
	return e.do(t, "apitok-admin-token", method, path, body)
}

func (e *apiTokEnv) bearer(t *testing.T, secret string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/v1/token", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

type createdTok struct {
	Token struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Prefix string `json:"prefix"`
	} `json:"token"`
	Secret string `json:"secret"`
}

func (e *apiTokEnv) create(t *testing.T, name string) createdTok {
	t.Helper()
	code, b := e.admin(t, "POST", "/admin/api-tokens", `{"name":"`+name+`","scopes":["engagement:read"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, b)
	}
	var c createdTok
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAPITokens_CreateReturnsSecretOnce(t *testing.T) {
	e := newAPITokEnv(t)
	c := e.create(t, "Home Assistant")
	if !apitokens.LooksLikeToken(c.Secret) || c.Token.Prefix != c.Secret[:12] {
		t.Fatalf("created %+v", c)
	}
	code, b := e.admin(t, "GET", "/admin/api-tokens", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	if bytes.Contains(b, []byte(c.Secret)) || bytes.Contains(b, []byte(`"secret"`)) {
		t.Fatalf("list exposes the secret: %s", b)
	}
	if !bytes.Contains(b, []byte(`"Home Assistant"`)) {
		t.Fatalf("list missing token: %s", b)
	}
}

func TestAPITokens_CreateValidation(t *testing.T) {
	e := newAPITokEnv(t)
	cases := []struct{ body, msg string }{
		{`{"name":"","scopes":["engagement:read"]}`, "name is required"},
		{`{"name":"   ","scopes":["engagement:read"]}`, "name is required"},
		{`{"name":"` + strings.Repeat("a", 101) + `","scopes":["engagement:read"]}`, "name must be 100 characters or fewer"},
		{`{"name":"HA","scopes":[]}`, "at least one scope is required"},
		{`{"name":"HA","scopes":["admin:write"]}`, "unknown scope: admin:write"},
	}
	for _, c := range cases {
		code, b := e.admin(t, "POST", "/admin/api-tokens", c.body)
		if code != http.StatusBadRequest || !bytes.Contains(b, []byte(c.msg)) {
			t.Errorf("%s: %d %s", c.body, code, b)
		}
	}
}

func (e *apiTokEnv) auditRow(t *testing.T, action string) (string, int64, map[string]any, string) {
	t.Helper()
	var tt string
	var id int64
	var meta string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT target_type, target_id, metadata::text FROM audit_log WHERE action = $1`, action).Scan(&tt, &id, &meta); err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		t.Fatal(err)
	}
	return tt, id, m, meta
}

func apiTokMetaKeys(m map[string]any) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

func TestAPITokens_CreateAndRevokeAudited(t *testing.T) {
	e := newAPITokEnv(t)
	c := e.create(t, "HA")
	if code, b := e.admin(t, "POST", "/admin/api-tokens/"+strconv.FormatInt(c.Token.ID, 10)+"/revoke", ""); code != http.StatusOK {
		t.Fatalf("revoke: %d %s", code, b)
	}
	for _, want := range []struct{ action, keys string }{
		{audit.ActionAPITokenCreated, "expires_at,name,prefix,scopes"},
		{audit.ActionAPITokenRevoked, "name,prefix"},
	} {
		tt, id, m, raw := e.auditRow(t, want.action)
		if tt != audit.TargetAPIToken || id != c.Token.ID || apiTokMetaKeys(m) != want.keys {
			t.Errorf("%s: target %s id %d keys %s", want.action, tt, id, apiTokMetaKeys(m))
		}
		if strings.Contains(raw, c.Secret) || strings.Contains(raw, c.Secret[5:]) {
			t.Errorf("%s: metadata contains the secret", want.action)
		}
	}
}

func TestAPITokens_RevokeNotFoundAndTwice(t *testing.T) {
	e := newAPITokEnv(t)
	c := e.create(t, "HA")
	if code, _ := e.admin(t, "POST", "/admin/api-tokens/999999999/revoke", ""); code != http.StatusNotFound {
		t.Fatalf("unknown: %d", code)
	}
	path := "/admin/api-tokens/" + strconv.FormatInt(c.Token.ID, 10) + "/revoke"
	if code, _ := e.admin(t, "POST", path, ""); code != http.StatusOK {
		t.Fatalf("first: %d", code)
	}
	if code, _ := e.admin(t, "POST", path, ""); code != http.StatusConflict {
		t.Fatalf("second: %d", code)
	}
}

func TestAPITokens_RequireAdmin(t *testing.T) {
	e := newAPITokEnv(t)
	if code, _ := e.do(t, "", "GET", "/admin/api-tokens", ""); code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", code)
	}
	if code, _ := e.do(t, "apitok-user-token", "GET", "/admin/api-tokens", ""); code != http.StatusForbidden {
		t.Fatalf("non-admin: %d", code)
	}
}

func TestAPITokens_RevokeTakesEffectNextRequest(t *testing.T) {
	e := newAPITokEnv(t)
	c := e.create(t, "HA")
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/v1/token", nil)
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(b, []byte(`"HA"`)) {
		t.Fatalf("before revoke: %d %s", resp.StatusCode, b)
	}
	if code, _ := e.admin(t, "POST", "/admin/api-tokens/"+strconv.FormatInt(c.Token.ID, 10)+"/revoke", ""); code != http.StatusOK {
		t.Fatal("revoke failed")
	}
	if code := e.bearer(t, c.Secret); code != http.StatusUnauthorized {
		t.Fatalf("after revoke: %d", code)
	}
}

func TestAPITokens_SecretNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	prevSlog := slog.Default()
	prevOut := log.Writer()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.SetOutput(&buf)
	t.Cleanup(func() {
		slog.SetDefault(prevSlog)
		log.SetOutput(prevOut)
	})
	e := newAPITokEnv(t)
	c := e.create(t, "HA")
	if code := e.bearer(t, c.Secret); code != http.StatusOK {
		t.Fatalf("v1: %d", code)
	}
	if code, _ := e.admin(t, "POST", "/admin/api-tokens/"+strconv.FormatInt(c.Token.ID, 10)+"/revoke", ""); code != http.StatusOK {
		t.Fatal("revoke failed")
	}
	if strings.Contains(buf.String(), c.Secret) {
		t.Fatal("secret was logged")
	}
}

// TestSetV1RoundTrips (#0675): the registry SetV1 attaches is the one V1
// returns, and a fresh handler has none.
func TestSetV1RoundTrips(t *testing.T) {
	h := NewAPITokensHandler(nil, nil)
	if h.V1() != nil {
		t.Fatal("fresh handler has a registry")
	}
	reg := BuildV1Registry(api.Config{Version: "x"}, V1Deps{})
	h.SetV1(reg)
	if h.V1() != reg {
		t.Fatal("V1 did not return the registry SetV1 attached")
	}
}

// TestAPITokens_CreateWithExpiryAndWriteScopes (#0674): expires_at round-trips
// through create and list; agent scopes are accepted.
func TestAPITokens_CreateWithExpiryAndWriteScopes(t *testing.T) {
	e := newAPITokEnv(t)
	exp := time.Now().Add(90 * 24 * time.Hour).UTC().Truncate(time.Second)
	body := `{"name":"Agent","scopes":["guide_posts:write","events:write","guides:read"],"expires_at":"` + exp.Format(time.RFC3339) + `"}`
	code, b := e.admin(t, "POST", "/admin/api-tokens", body)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, b)
	}
	var c struct {
		Token struct {
			Scopes    []string   `json:"scopes"`
			ExpiresAt *time.Time `json:"expires_at"`
		} `json:"token"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.Token.ExpiresAt == nil || !c.Token.ExpiresAt.Equal(exp) || len(c.Token.Scopes) != 3 {
		t.Fatalf("created %+v", c.Token)
	}
	_, list := e.admin(t, "GET", "/admin/api-tokens", "")
	var l struct {
		Tokens []struct {
			ExpiresAt *time.Time `json:"expires_at"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(list, &l); err != nil || len(l.Tokens) != 1 || l.Tokens[0].ExpiresAt == nil || !l.Tokens[0].ExpiresAt.Equal(exp) {
		t.Fatalf("list expires_at: %s (err %v)", list, err)
	}
	// A token with no expiry lists expires_at as null.
	e.create(t, "Forever")
	if _, list := e.admin(t, "GET", "/admin/api-tokens", ""); !bytes.Contains(list, []byte(`"expires_at":null`)) {
		t.Fatalf("list missing null expires_at: %s", list)
	}
}

func TestAPITokens_CreateRejectsPastExpiry(t *testing.T) {
	e := newAPITokEnv(t)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	code, b := e.admin(t, "POST", "/admin/api-tokens", `{"name":"HA","scopes":["engagement:read"],"expires_at":"`+past+`"}`)
	if code != http.StatusBadRequest || !bytes.Contains(b, []byte("expiry must be in the future")) {
		t.Fatalf("%d %s", code, b)
	}
	code, _ = e.admin(t, "POST", "/admin/api-tokens", `{"name":"HA","scopes":["engagement:read"],"expires_at":"not a date"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("malformed expiry: %d", code)
	}
}

// TestAPITokens_ExpiredTokenGets401 runs the real store and middleware: once
// a token's expires_at passes, /api/v1/token answers 401.
func TestAPITokens_ExpiredTokenGets401(t *testing.T) {
	e := newAPITokEnv(t)
	c := e.create(t, "HA")
	if code := e.bearer(t, c.Secret); code != http.StatusOK {
		t.Fatalf("fresh token: %d", code)
	}
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE api_tokens SET created_at = now() - interval '2 days', expires_at = now() - interval '1 day' WHERE id = $1`, c.Token.ID); err != nil {
		t.Fatal(err)
	}
	if code := e.bearer(t, c.Secret); code != http.StatusUnauthorized {
		t.Fatalf("expired token: %d", code)
	}
}
