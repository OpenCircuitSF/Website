package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
)

// fakeDevAdminResolver is a minimal in-memory SessionResolver for
// DevAdminAutoLogin's tests — no Postgres required. Unlike fakeDevStore
// above (devauth_test.go), sessions are added directly via addSession
// rather than through a CreateSession-shaped call: DevAdminAutoLogin's own
// DevAdminSessionMinter is what creates sessions in production, so this
// fake's only job is to answer ResolveSession honestly — including after a
// token is removed out of band via invalidate, mirroring #0409's scenario
// of a cookie surviving a db-reset.sh/TRUNCATE/logout that deleted the row
// it names.
type fakeDevAdminResolver struct {
	sessions map[string]testSession
}

func newFakeDevAdminResolver() *fakeDevAdminResolver {
	return &fakeDevAdminResolver{sessions: make(map[string]testSession)}
}

func (f *fakeDevAdminResolver) addSession(token string, userID int64, expiresAt time.Time) {
	f.sessions[token] = testSession{userID: userID, expiresAt: expiresAt}
}

func (f *fakeDevAdminResolver) invalidate(token string) {
	delete(f.sessions, token)
}

// ResolveSession satisfies middleware.SessionResolver.
func (f *fakeDevAdminResolver) ResolveSession(_ context.Context, token string, now time.Time) (auth.SessionUser, error) {
	s, ok := f.sessions[token]
	if !ok || !s.expiresAt.After(now) {
		return auth.SessionUser{}, auth.ErrSessionInvalid
	}
	return auth.SessionUser{ID: s.userID, Email: "admin@localhost", IsAdmin: true}, nil
}

// newCountingMinter builds a DevAdminSessionMinter that mints a fresh,
// resolver-known token on every call and counts how many times it was
// called — the seam devadminauth_test.go's cache tests need to prove "one
// mint per first-request, not per request" and "a mint happens again after
// the cached token is invalidated".
func newCountingMinter(resolver *fakeDevAdminResolver, userID int64) (DevAdminSessionMinter, *int) {
	calls := 0
	minter := DevAdminSessionMinter(func(_ context.Context) (string, time.Time, error) {
		calls++
		tok, err := auth.NewSessionToken()
		if err != nil {
			return "", time.Time{}, err
		}
		exp := time.Now().Add(24 * time.Hour)
		resolver.addSession(tok, userID, exp)
		return tok, exp, nil
	})
	return minter, &calls
}

// TestDevAdminAutoLogin_PanicsWhenNotAllowed proves the structural backstop:
// constructing the middleware with allowed=false must panic. The load-bearing
// refusal is cmd/opencircuit's newDevAdminAutoLogin startup error; this exists
// only so a future miswiring cannot construct the middleware silently.
func TestDevAdminAutoLogin_PanicsWhenNotAllowed(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("DevAdminAutoLogin(allowed=false) did not panic — guardrail missing")
		}
	}()
	resolver := newFakeDevAdminResolver()
	minter, _ := newCountingMinter(resolver, 1)
	DevAdminAutoLogin(resolver, minter, false) // must panic
}

// TestDevAdminAutoLogin_PanicsOnNilDependencies covers the same structural
// backstop for a nil resolver or a nil mint func, mirroring
// DevAdminAutoLogin's doc comment.
func TestDevAdminAutoLogin_PanicsOnNilDependencies(t *testing.T) {
	resolver := newFakeDevAdminResolver()
	minter, _ := newCountingMinter(resolver, 1)

	t.Run("nil resolver", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("DevAdminAutoLogin(nil resolver) did not panic")
			}
		}()
		DevAdminAutoLogin(nil, minter, true)
	})
	t.Run("nil mint", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("DevAdminAutoLogin(nil mint) did not panic")
			}
		}()
		DevAdminAutoLogin(resolver, nil, true)
	})
}

// TestDevAdminAutoLogin_NoCredentialMintsAndComposes is #0402's core proof:
// a request with no credential at all reaches the inner handler as the
// authenticated admin through the FULL DevAdminAutoLogin -> RequireSession ->
// handler chain, with the response carrying the new session cookie.
func TestDevAdminAutoLogin_NoCredentialMintsAndComposes(t *testing.T) {
	resolver := newFakeDevAdminResolver()
	minter, calls := newCountingMinter(resolver, 42)
	mw := DevAdminAutoLogin(resolver, minter, true)
	requireSession := RequireSession(resolver)

	inner := &captureHandler{}
	chain := mw(requireSession(inner))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !inner.ran || !inner.ok || inner.user == nil {
		t.Fatal("inner handler did not run authenticated — DevAdminAutoLogin -> RequireSession chain failed")
	}
	if inner.user.ID != 42 {
		t.Errorf("user ID = %d, want 42", inner.user.ID)
	}
	if !inner.user.IsAdmin {
		t.Error("IsAdmin = false, want true")
	}
	if *calls != 1 {
		t.Errorf("mint calls = %d, want 1", *calls)
	}

	var respCookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			respCookie = c.Value
		}
	}
	if respCookie == "" {
		t.Fatal("response did not carry the new session cookie")
	}
}

// TestDevAdminAutoLogin_ValidCookiePassesThroughUnchanged proves the only
// pass-through branch: a request already carrying a VALID session cookie
// must reach next with zero mint calls and no Set-Cookie on the response —
// this is #0409's acceptance criterion for the valid case, proved here at
// construction time.
func TestDevAdminAutoLogin_ValidCookiePassesThroughUnchanged(t *testing.T) {
	resolver := newFakeDevAdminResolver()
	minter, calls := newCountingMinter(resolver, 7)
	const liveToken = "already-live-token"
	resolver.addSession(liveToken, 7, time.Now().Add(time.Hour))

	mw := DevAdminAutoLogin(resolver, minter, true)

	var seenToken string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(auth.SessionCookieName); err == nil {
			seenToken = c.Value
		}
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := reqWithCookie(liveToken)
	mw(next).ServeHTTP(rec, req)

	if seenToken != liveToken {
		t.Errorf("cookie seen by next = %q, want unchanged %q", seenToken, liveToken)
	}
	if *calls != 0 {
		t.Errorf("mint calls = %d, want 0 for a valid cookie", *calls)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("response set %d cookie(s), want 0 for a valid cookie (no rewrite)", len(rec.Result().Cookies()))
	}
}

// TestDevAdminAutoLogin_StaleCookieHeals is #0409's regression pin: a cookie
// whose sessions row is gone (a db-reset.sh, a TRUNCATE, a logout — see
// #0409's description) must NOT 401. The request must reach the admin
// (through the composed RequireSession chain) and the request's rebuilt
// Cookie header must carry EXACTLY ONE opencircuit_session entry, whose
// value is the freshly minted token — proving r.Header.Set (replace) was
// used rather than r.AddCookie (append), which is what let the stale value
// win at extraction before this fix.
func TestDevAdminAutoLogin_StaleCookieHeals(t *testing.T) {
	resolver := newFakeDevAdminResolver()
	minter, calls := newCountingMinter(resolver, 9)
	mw := DevAdminAutoLogin(resolver, minter, true)
	requireSession := RequireSession(resolver)

	inner := &captureHandler{}
	var sawSessionCookies []string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, c := range r.Cookies() {
			if c.Name == auth.SessionCookieName {
				sawSessionCookies = append(sawSessionCookies, c.Value)
			}
		}
		requireSession(inner).ServeHTTP(w, r)
	})

	rec := httptest.NewRecorder()
	req := reqWithCookie("stale-token-whose-row-is-gone")
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (stale cookie must heal, not 401 — #0409)", rec.Code)
	}
	if !inner.ran || !inner.ok {
		t.Fatal("inner handler did not run authenticated after a stale cookie")
	}
	if len(sawSessionCookies) != 1 {
		t.Fatalf("request carried %d session cookie(s), want exactly 1 (r.AddCookie would leave 2 — see #0409)", len(sawSessionCookies))
	}
	if sawSessionCookies[0] == "stale-token-whose-row-is-gone" {
		t.Error("the request's surviving session cookie is still the stale one — not replaced")
	}
	if *calls != 1 {
		t.Errorf("mint calls = %d, want 1", *calls)
	}
}

// TestDevAdminAutoLogin_GarbageCookieHeals is the garbage-value sibling of
// TestDevAdminAutoLogin_StaleCookieHeals — #0409's second acceptance
// criterion. A cookie value that was never a real token must heal exactly
// the same way as a stale one.
func TestDevAdminAutoLogin_GarbageCookieHeals(t *testing.T) {
	resolver := newFakeDevAdminResolver()
	minter, _ := newCountingMinter(resolver, 11)
	mw := DevAdminAutoLogin(resolver, minter, true)
	requireSession := RequireSession(resolver)

	inner := &captureHandler{}
	chain := mw(requireSession(inner))

	rec := httptest.NewRecorder()
	req := reqWithCookie("opencircuit_session=nonsense")
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (garbage cookie must heal, not 401 — #0409)", rec.Code)
	}
	if !inner.ran || !inner.ok {
		t.Fatal("inner handler did not run authenticated after a garbage cookie")
	}
}

// TestDevAdminAutoLogin_UnusableBearerTokenDropped is #0402's second
// mechanical finding: sessionToken (auth.go) prefers Authorization: Bearer
// over the cookie, so an unusable bearer token — with no cookie present at
// all — must not reproduce #0409's stuck 401 by that second route. The
// request must reach the admin, and the Authorization header must be gone
// by the time RequireSession reads the request.
func TestDevAdminAutoLogin_UnusableBearerTokenDropped(t *testing.T) {
	resolver := newFakeDevAdminResolver()
	minter, _ := newCountingMinter(resolver, 13)
	mw := DevAdminAutoLogin(resolver, minter, true)
	requireSession := RequireSession(resolver)

	inner := &captureHandler{}
	var sawAuthHeader string
	sawAuthHeaderSet := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthHeader = r.Header.Get("Authorization")
		sawAuthHeaderSet = true
		requireSession(inner).ServeHTTP(w, r)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer unusable-garbage-token")
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (unusable bearer token must heal, not 401)", rec.Code)
	}
	if !inner.ran || !inner.ok {
		t.Fatal("inner handler did not run authenticated after an unusable bearer token")
	}
	if !sawAuthHeaderSet {
		t.Fatal("test bug: next was never called")
	}
	if sawAuthHeader != "" {
		t.Errorf("Authorization header = %q, want empty (must be dropped so it can't win at RequireSession)", sawAuthHeader)
	}
}

// TestDevAdminAutoLogin_CachesAcrossCredentialLessRequests proves the
// cache-or-mint contract: ten successive credential-less requests must
// produce exactly one mint call and therefore exactly one sessions row in
// production — "a first page load's twenty parallel asset requests create
// exactly one sessions row" is the acceptance criterion this pins serially.
func TestDevAdminAutoLogin_CachesAcrossCredentialLessRequests(t *testing.T) {
	resolver := newFakeDevAdminResolver()
	minter, calls := newCountingMinter(resolver, 21)
	mw := DevAdminAutoLogin(resolver, minter, true)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := mw(next)

	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
		}
	}

	if *calls != 1 {
		t.Errorf("mint calls after 10 credential-less requests = %d, want 1", *calls)
	}
}

// TestDevAdminAutoLogin_CacheHealsAfterInvalidation proves the other half of
// the cache contract: once the cached token is invalidated out of band (a
// db-reset.sh, a TRUNCATE, a logout — the same scenario #0409 names), the
// next request must mint again rather than handing out a dead token.
func TestDevAdminAutoLogin_CacheHealsAfterInvalidation(t *testing.T) {
	resolver := newFakeDevAdminResolver()
	minter, calls := newCountingMinter(resolver, 33)
	mw := DevAdminAutoLogin(resolver, minter, true)

	var lastToken string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(auth.SessionCookieName); err == nil {
			lastToken = c.Value
		}
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(next)

	// First request: mints and caches.
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	if *calls != 1 {
		t.Fatalf("mint calls after first request = %d, want 1", *calls)
	}
	firstToken := lastToken

	// Second request: cache still valid, no new mint.
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	if *calls != 1 {
		t.Fatalf("mint calls after second (cache-hit) request = %d, want 1", *calls)
	}

	// Invalidate the cached token out of band, mirroring a deleted row.
	resolver.invalidate(firstToken)

	// Third request: the cache is now dead, so this must mint again.
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	if rec3.Code != http.StatusOK {
		t.Fatalf("third request status = %d, want 200", rec3.Code)
	}
	if *calls != 2 {
		t.Errorf("mint calls after invalidating the cache = %d, want 2", *calls)
	}
	if lastToken == firstToken {
		t.Error("third request still carries the invalidated token — cache did not heal")
	}
}
