package outreachconn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Fakes only: nothing here reaches a real ShortLinks, Bluesky, or Mastodon
// server. Every failure body carries a canary the result must never echo.
const (
	bodyCanary  = "UPSTREAM-BODY-CANARY"
	tokenCanary = "tok-CANARY-fake"
)

func testMgr(t *testing.T, env map[string]string) *Manager {
	t.Helper()
	return newMgr(t, newFakeStore(nil), env)
}

func assertClean(t *testing.T, r TestResult) {
	t.Helper()
	if strings.Contains(r.Detail+r.Error, bodyCanary) || strings.Contains(r.Detail+r.Error, tokenCanary) {
		t.Errorf("result leaked upstream text or a secret: %+v", r)
	}
}

func TestShortLinksConnection(t *testing.T) {
	var status = 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" || r.Header.Get("Authorization") != "Bearer "+tokenCanary {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(status)
		if status == 200 {
			w.Write([]byte(`{"email":"admin@example.test","base_url":"https://go.example.test"}`))
			return
		}
		w.Write([]byte(`{"error":"` + bodyCanary + `"}`))
	}))
	defer srv.Close()
	m := testMgr(t, map[string]string{KeyShortLinksBaseURL: srv.URL, KeyShortLinksAPIToken: tokenCanary})
	r := m.Test(context.Background(), GroupShortLinks)
	if !r.OK || !strings.Contains(r.Detail, "admin@example.test") {
		t.Errorf("ok result = %+v", r)
	}
	status = 401
	r = m.Test(context.Background(), GroupShortLinks)
	if r.OK || r.Error != "ShortLinks rejected the API token." {
		t.Errorf("401 result = %+v", r)
	}
	assertClean(t, r)
	srv.Close()
	r = m.Test(context.Background(), GroupShortLinks)
	if r.OK || r.Error != "Could not reach ShortLinks at the saved base URL." {
		t.Errorf("closed server result = %+v", r)
	}
	assertClean(t, r)
}

func TestBlueskyConnection(t *testing.T) {
	good := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !good {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"AuthenticationRequired","message":"` + bodyCanary + `"}`))
			return
		}
		w.Write([]byte(`{"accessJwt":"a","refreshJwt":"r","did":"did:plc:x","handle":"alice.example.test"}`))
	}))
	defer srv.Close()
	m := testMgr(t, map[string]string{KeyBlueskyPDSURL: srv.URL, KeyBlueskyHandle: "alice.example.test", KeyBlueskyAppPassword: tokenCanary})
	r := m.Test(context.Background(), GroupBluesky)
	if !r.OK || r.Detail != "Signed in to Bluesky as @alice.example.test" {
		t.Errorf("ok result = %+v", r)
	}
	good = false
	r = m.Test(context.Background(), GroupBluesky)
	if r.OK || r.Error != "Bluesky rejected the handle or app password." {
		t.Errorf("401 result = %+v", r)
	}
	assertClean(t, r)
}

func TestMastodonConnection(t *testing.T) {
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		if status == 200 {
			w.Write([]byte(`{"acct":"opencircuit","url":"https://m.example/@opencircuit"}`))
			return
		}
		w.Write([]byte(`{"error":"` + bodyCanary + `"}`))
	}))
	defer srv.Close()
	m := testMgr(t, map[string]string{KeyMastodonBaseURL: srv.URL, KeyMastodonToken: tokenCanary})
	r := m.Test(context.Background(), GroupMastodon)
	if !r.OK || r.Detail != "Signed in to Mastodon as @opencircuit" {
		t.Errorf("ok result = %+v", r)
	}
	status = 401
	r = m.Test(context.Background(), GroupMastodon)
	if r.OK || r.Error != "Mastodon rejected the access token." {
		t.Errorf("401 result = %+v", r)
	}
	assertClean(t, r)
	status = 403
	r = m.Test(context.Background(), GroupMastodon)
	if r.OK || !strings.Contains(r.Error, "scope") {
		t.Errorf("403 result = %+v", r)
	}
	assertClean(t, r)
}

func TestNotConfiguredAndUnknown(t *testing.T) {
	m := testMgr(t, nil)
	for _, g := range Groups {
		r := m.Test(context.Background(), g)
		if r.OK || !strings.HasPrefix(r.Error, "Not configured: ") {
			t.Errorf("%s result = %+v", g, r)
		}
	}
	if r := m.Test(context.Background(), "nope"); r.Error != "unknown connection" {
		t.Errorf("unknown group result = %+v", r)
	}
}

func TestTestUsesAFreshClient(t *testing.T) {
	// A cached session on the live client must not make Test skip the login.
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"accessJwt":"a","refreshJwt":"r","did":"did:plc:x","handle":"alice.example.test"}`))
	}))
	defer srv.Close()
	m := testMgr(t, map[string]string{KeyBlueskyPDSURL: srv.URL, KeyBlueskyHandle: "alice.example.test", KeyBlueskyAppPassword: "pw"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := m.Bluesky().VerifyCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	m.Test(ctx, GroupBluesky)
	if hits != 2 {
		t.Errorf("createSession hits = %d, want 2 (one cached login, one fresh test)", hits)
	}
}
