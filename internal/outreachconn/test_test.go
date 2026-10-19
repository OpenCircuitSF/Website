package outreachconn

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
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
	// The reload's handle lookup (#0601) also signs in here; let it finish
	// before the handler's flag changes, or the two race.
	m.lookups.Wait()
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
	m.lookups.Wait()
	// Make the reload lookup fail from here on, so the cache invalidation
	// below is observable on its own (#0597 makes a reload fetch the account).
	m.lookupAccount = func(context.Context, *mastodon.Client) (mastodon.Account, error) {
		return mastodon.Account{}, errors.New("lookup disabled")
	}
	r := m.Test(context.Background(), GroupMastodon)
	if !r.OK || r.Detail != "Signed in to Mastodon as @opencircuit" {
		t.Errorf("ok result = %+v", r)
	}
	if got, want := m.Account("mastodon"), "@opencircuit@"+strings.TrimPrefix(srv.URL, "http://"); got != want {
		t.Errorf("Account after a verified test = %q, want %q", got, want)
	}
	// A changed token invalidates the verified account: back to the bare host
	// until a test verifies the new one (#0593).
	if _, err := m.Apply(context.Background(), map[string]string{KeyMastodonToken: "replacement-token-0593"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	m.lookups.Wait()
	if got, want := m.Account("mastodon"), strings.TrimPrefix(srv.URL, "http://"); got != want {
		t.Errorf("Account after a token change = %q, want the bare host %q", got, want)
	}
	if _, err := m.Apply(context.Background(), map[string]string{KeyMastodonToken: tokenCanary}, time.Now()); err != nil {
		t.Fatal(err)
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

// #0597: a reload fetches the Mastodon account itself, so "@user@host"
// survives a restart without a manual Test; a failed fetch keeps the bare
// host and logs the group name only.
func TestMastodonAccountFetchedOnReload(t *testing.T) {
	var status atomic.Int32 // the handler goroutine reads it; a socket is no happens-before edge
	status.Store(200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+tokenCanary {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(int(status.Load()))
		w.Write([]byte(`{"acct":"opencircuit","url":"https://m.example/@opencircuit"}`))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	env := map[string]string{KeyMastodonBaseURL: srv.URL, KeyMastodonToken: tokenCanary}

	m := testMgr(t, env)
	m.lookups.Wait()
	if got, want := m.Account("mastodon"), "@opencircuit@"+host; got != want {
		t.Errorf("Account after reload = %q, want %q", got, want)
	}

	// A failing instance falls back to the host; the log has no token.
	status.Store(500)
	var logs strings.Builder
	m2 := NewManager(newFakeStore(nil), env, slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(m2.lookups.Wait)
	if err := m2.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	m2.lookups.Wait()
	if got := m2.Account("mastodon"); got != host {
		t.Errorf("Account after a failed lookup = %q, want the bare host %q", got, host)
	}
	if !strings.Contains(logs.String(), "account lookup failed") || strings.Contains(logs.String(), tokenCanary) {
		t.Errorf("log = %q, want the failure named and no token", logs.String())
	}
}

// #0597: a slow lookup for a replaced token must not overwrite the answer the
// newer token's lookup already cached. No network: the lookup is stubbed.
func TestStaleMastodonAccountLookupIsDiscarded(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler()) // a loopback URL mastodon.New accepts; never called
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	m := NewManager(newFakeStore(nil), map[string]string{KeyMastodonBaseURL: srv.URL, KeyMastodonToken: "tok-a-fake"}, quietLog())
	t.Cleanup(m.lookups.Wait)
	entered, release := make(chan struct{}), make(chan struct{})
	m.lookupAccount = func(context.Context, *mastodon.Client) (mastodon.Account, error) {
		close(entered)
		<-release // the old token's lookup answers last
		return mastodon.Account{Acct: "old"}, nil
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-entered // the old lookup has read lookupAccount; swapping it is now race-free
	m.lookupAccount = func(context.Context, *mastodon.Client) (mastodon.Account, error) {
		return mastodon.Account{Acct: "new"}, nil
	}
	if _, err := m.Apply(context.Background(), map[string]string{KeyMastodonToken: "tok-b-fake"}, time.Now()); err != nil {
		close(release)
		t.Fatal(err)
	}
	want := "@new@" + host
	for deadline := time.Now().Add(2 * time.Second); m.Account("mastodon") != want && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.Account("mastodon"); got != want {
		close(release)
		t.Fatalf("Account after the new token's lookup = %q, want %q", got, want)
	}
	close(release)
	m.lookups.Wait()
	if got := m.Account("mastodon"); got != want {
		t.Errorf("Account after the stale lookup answered = %q, want %q kept", got, want)
	}
}

// #0601: the saved Bluesky identifier may be the sign-in email. Account shows
// nothing for it until the session's own handle is known, then that handle.
func TestBlueskyAccountShowsSessionHandleForEmail(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler()) // a loopback PDS; never called, the lookup is stubbed
	defer srv.Close()
	env := map[string]string{KeyBlueskyPDSURL: srv.URL, KeyBlueskyHandle: "owner@example.test", KeyBlueskyAppPassword: "pw-fake"}
	m := NewManager(newFakeStore(nil), env, quietLog())
	t.Cleanup(m.lookups.Wait)
	m.lookupBlueskyHandle = func(context.Context, *bluesky.Client) (string, error) {
		return "", errors.New("lookup failed")
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.lookups.Wait()
	if got := m.Account("bluesky"); got != "" {
		t.Errorf("Account for an email before the handle is known = %q, want empty", got)
	}
	// A reload with the handle available fetches it.
	m.lookupBlueskyHandle = func(context.Context, *bluesky.Client) (string, error) {
		return "www.example.test", nil
	}
	if _, err := m.Apply(context.Background(), map[string]string{KeyBlueskyAppPassword: "pw-fake-2"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	m.lookups.Wait()
	if got := m.Account("bluesky"); got != "@www.example.test" {
		t.Errorf("Account after the lookup = %q, want @www.example.test", got)
	}
	// A saved plain handle shows before any lookup.
	m2 := NewManager(newFakeStore(nil), map[string]string{KeyBlueskyPDSURL: "https://pds.example.test", KeyBlueskyHandle: "@alice.example.test", KeyBlueskyAppPassword: "pw"}, quietLog())
	if err := m2.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m2.Account("bluesky"); got != "@alice.example.test" {
		t.Errorf("Account for a plain handle = %q", got)
	}
}

// #0601: a Test connection result is cached for Account, and a lookup begun
// under a replaced config is discarded.
func TestStaleBlueskyHandleLookupIsDiscarded(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	m := NewManager(newFakeStore(nil), map[string]string{KeyBlueskyPDSURL: srv.URL, KeyBlueskyHandle: "owner@example.test", KeyBlueskyAppPassword: "pw-a-fake"}, quietLog())
	t.Cleanup(m.lookups.Wait)
	entered, release := make(chan struct{}), make(chan struct{})
	m.lookupBlueskyHandle = func(context.Context, *bluesky.Client) (string, error) {
		close(entered)
		<-release
		return "old.example.test", nil
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-entered
	m.lookupBlueskyHandle = func(context.Context, *bluesky.Client) (string, error) {
		return "new.example.test", nil
	}
	if _, err := m.Apply(context.Background(), map[string]string{KeyBlueskyAppPassword: "pw-b-fake"}, time.Now()); err != nil {
		close(release)
		t.Fatal(err)
	}
	want := "@new.example.test"
	for deadline := time.Now().Add(2 * time.Second); m.Account("bluesky") != want && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.Account("bluesky"); got != want {
		close(release)
		t.Fatalf("Account after the new config's lookup = %q, want %q", got, want)
	}
	close(release)
	m.lookups.Wait()
	if got := m.Account("bluesky"); got != want {
		t.Errorf("Account after the stale lookup answered = %q, want %q kept", got, want)
	}
}
