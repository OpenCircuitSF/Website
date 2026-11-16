package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

func whoamiEnv(t *testing.T) (http.Handler, string, string) {
	t.Helper()
	secret, _ := apitokens.Generate()
	exp := time.Date(2027, 2, 1, 8, 0, 0, 0, time.UTC)
	res := fakeResolver{secret: {ID: 4, Name: "Claude", Prefix: secret[:12], Scopes: []string{apitokens.ScopeEventsWrite}, ExpiresAt: &exp}}
	reg := NewRegistry(Config{Version: "1.2.3"})
	reg.Add(CoreOperations("1.2.3")...)
	return NewHTTPRouter(reg, RouterDeps{Resolver: res, Limiter: middleware.NewAPITokenRateLimiter()}), secret, secret[:12]
}

func TestWhoAmI(t *testing.T) {
	h, secret, prefix := whoamiEnv(t)
	e := &env{h: h}
	r := e.do(t, "GET", "/api/v1/me", secret, "")
	if r.code != 200 {
		t.Fatalf("whoami: %d %s", r.code, r.body)
	}
	var got WhoAmI
	if err := json.Unmarshal(r.body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Claude" || got.Prefix != prefix || len(got.Scopes) != 1 || got.Scopes[0] != apitokens.ScopeEventsWrite ||
		got.ExpiresAt == nil || !got.ExpiresAt.Equal(time.Date(2027, 2, 1, 8, 0, 0, 0, time.UTC)) ||
		got.ServerVersion != "1.2.3" || got.APIVersion != "v1" {
		t.Fatalf("whoami %+v", got)
	}
	if bytes.Contains(r.body, []byte(secret)) {
		t.Fatal("whoami body contains the secret")
	}
}

func TestTokenProbeAliasMatchesWhoAmI(t *testing.T) {
	h, secret, _ := whoamiEnv(t)
	e := &env{h: h}
	me := e.do(t, "GET", "/api/v1/me", secret, "")
	probe := e.do(t, "GET", "/api/v1/token", secret, "")
	if probe.code != 200 || !bytes.Equal(me.body, probe.body) {
		t.Fatalf("probe %d %s, me %s", probe.code, probe.body, me.body)
	}
	// #0652's body keys are still there.
	var m map[string]any
	_ = json.Unmarshal(probe.body, &m)
	for _, k := range []string{"name", "prefix", "scopes"} {
		if _, ok := m[k]; !ok {
			t.Errorf("probe body lacks %s", k)
		}
	}
	for _, op := range CoreOperations("x") {
		if op.Name == "token_probe" && (op.MCPExcluded == "" || !op.Deprecated) {
			t.Fatal("token_probe is not excluded from MCP and deprecated")
		}
		if op.Name == "whoami" && op.MCPExcluded != "" {
			t.Fatal("whoami excluded from MCP")
		}
	}
}
