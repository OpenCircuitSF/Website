package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
)

// fakeTokenResolver is an in-memory APITokenResolver that counts its calls.
type fakeTokenResolver struct {
	tokens  map[string]apitokens.Token
	revoked map[string]bool
	err     error
	calls   int
}

func (f *fakeTokenResolver) Resolve(_ context.Context, secret string, _ time.Time) (apitokens.Token, error) {
	f.calls++
	if f.err != nil {
		return apitokens.Token{}, f.err
	}
	t, ok := f.tokens[secret]
	if !ok || f.revoked[secret] {
		return apitokens.Token{}, apitokens.ErrNotFound
	}
	return t, nil
}

func newTokenFixture(t *testing.T, scopes ...string) (*fakeTokenResolver, string) {
	t.Helper()
	secret, err := apitokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeTokenResolver{
		tokens:  map[string]apitokens.Token{secret: {ID: 7, Name: "HA", Prefix: secret[:12], Scopes: scopes}},
		revoked: map[string]bool{},
	}, secret
}

// serveToken runs RequireAPIToken over a handler that records the principal.
func serveToken(res APITokenResolver, scope string, r *http.Request) (*httptest.ResponseRecorder, *APIToken) {
	var got *APIToken
	h := RequireAPIToken(res, scope)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = APITokenFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec, got
}

func v1Req(authHeader string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/token", nil)
	if authHeader != "" {
		r.Header.Set("Authorization", authHeader)
	}
	return r
}

func TestRequireAPIToken_ValidTokenWithScope(t *testing.T) {
	res, secret := newTokenFixture(t, apitokens.ScopeEngagementRead)
	rec, p := serveToken(res, apitokens.ScopeEngagementRead, v1Req("Bearer "+secret))
	if rec.Code != http.StatusOK || p == nil || p.ID != 7 || p.Name != "HA" {
		t.Fatalf("code %d principal %+v", rec.Code, p)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("Cache-Control not no-store")
	}
}

func TestRequireAPIToken_MissingHeader401(t *testing.T) {
	res, _ := newTokenFixture(t, apitokens.ScopeEngagementRead)
	if rec, _ := serveToken(res, "", v1Req("")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestRequireAPIToken_UnknownToken401(t *testing.T) {
	res, _ := newTokenFixture(t, apitokens.ScopeEngagementRead)
	other, _ := apitokens.Generate()
	if rec, _ := serveToken(res, "", v1Req("Bearer "+other)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestRequireAPIToken_RevokedToken401(t *testing.T) {
	res, secret := newTokenFixture(t, apitokens.ScopeEngagementRead)
	res.revoked[secret] = true
	if rec, _ := serveToken(res, "", v1Req("Bearer "+secret)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestRequireAPIToken_401sAreIdentical(t *testing.T) {
	res, _ := newTokenFixture(t, apitokens.ScopeEngagementRead)
	revoked, _ := apitokens.Generate()
	res.tokens[revoked] = apitokens.Token{ID: 8, Scopes: []string{apitokens.ScopeEngagementRead}}
	res.revoked[revoked] = true
	unknown, _ := apitokens.Generate()
	var bodies []string
	var auths []string
	for _, h := range []string{"", "Bearer not-a-token", "Bearer " + unknown, "Bearer " + revoked} {
		rec, _ := serveToken(res, "", v1Req(h))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%q: code %d", h, rec.Code)
		}
		b, _ := io.ReadAll(rec.Body)
		bodies = append(bodies, string(b))
		auths = append(auths, rec.Header().Get("WWW-Authenticate"))
	}
	for i := range bodies {
		if bodies[i] != bodies[0] || auths[i] != auths[0] || auths[i] == "" {
			t.Fatalf("401s differ: %q %q", bodies, auths)
		}
	}
}

func TestRequireAPIToken_QueryStringNeverRead(t *testing.T) {
	res, secret := newTokenFixture(t, apitokens.ScopeEngagementRead)
	for _, q := range []string{"access_token", "token"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/token?"+q+"="+secret, nil)
		if rec, _ := serveToken(res, "", r); rec.Code != http.StatusUnauthorized {
			t.Fatalf("?%s: code %d", q, rec.Code)
		}
	}
	if res.calls != 0 {
		t.Fatalf("resolver called %d times", res.calls)
	}
}

func TestRequireAPIToken_SessionCookieNeverRead(t *testing.T) {
	res, secret := newTokenFixture(t, apitokens.ScopeEngagementRead)
	r := v1Req("")
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: secret})
	if rec, _ := serveToken(res, "", r); rec.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", rec.Code)
	}
	if res.calls != 0 {
		t.Fatalf("resolver called %d times", res.calls)
	}
}

func TestRequireAPIToken_MissingScope403(t *testing.T) {
	res, secret := newTokenFixture(t, "other:read")
	if rec, _ := serveToken(res, apitokens.ScopeEngagementRead, v1Req("Bearer "+secret)); rec.Code != http.StatusForbidden {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestRequireAPIToken_EmptyScopeAcceptsAnyValidToken(t *testing.T) {
	res, secret := newTokenFixture(t, "other:read")
	if rec, _ := serveToken(res, "", v1Req("Bearer "+secret)); rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestRequireAPIToken_ResolverError500(t *testing.T) {
	res, secret := newTokenFixture(t, apitokens.ScopeEngagementRead)
	res.err = errors.New("db down")
	if rec, _ := serveToken(res, "", v1Req("Bearer "+secret)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("code %d", rec.Code)
	}
}

// countingSessionResolver counts ResolveSession calls and always fails.
type countingSessionResolver struct{ calls int }

func (c *countingSessionResolver) ResolveSession(context.Context, string, time.Time) (auth.SessionUser, error) {
	c.calls++
	return auth.SessionUser{}, auth.ErrSessionInvalid
}

func TestRequireSession_APITokenRefusedWithoutLookup(t *testing.T) {
	secret, _ := apitokens.Generate()
	res := &countingSessionResolver{}
	h := RequireSession(res)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqAuth("Bearer "+secret, ""))
	if rec.Code != http.StatusUnauthorized || res.calls != 0 {
		t.Fatalf("code %d, ResolveSession calls %d", rec.Code, res.calls)
	}
}

// TestRequireAPIToken_FailuresAreNoStore pins #0652's review follow-up: the
// 401, 403 and 500 responses carry Cache-Control: no-store too (#0653).
func TestRequireAPIToken_FailuresAreNoStore(t *testing.T) {
	res, secret := newTokenFixture(t, "other:read")
	errRes, errSecret := newTokenFixture(t, apitokens.ScopeEngagementRead)
	errRes.err = errors.New("db down")
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	prev := slog.Default()
	slog.SetDefault(quiet)
	t.Cleanup(func() { slog.SetDefault(prev) })
	for _, c := range []struct {
		name string
		res  APITokenResolver
		auth string
		want int
	}{
		{"missing", res, "", http.StatusUnauthorized},
		{"unknown", res, "Bearer " + errSecret, http.StatusUnauthorized},
		{"wrong scope", res, "Bearer " + secret, http.StatusForbidden},
		{"resolver error", errRes, "Bearer " + errSecret, http.StatusInternalServerError},
	} {
		rec, _ := serveToken(c.res, apitokens.ScopeEngagementRead, v1Req(c.auth))
		if rec.Code != c.want {
			t.Errorf("%s: code %d, want %d", c.name, rec.Code, c.want)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control %q, want no-store", c.name, got)
		}
	}
}

// TestRequireAPIToken_ResolverErrorLogsWithoutToken: the 500 path logs one
// error line naming the failure and the error, never the token (#0653).
func TestRequireAPIToken_ResolverErrorLogsWithoutToken(t *testing.T) {
	res, secret := newTokenFixture(t, apitokens.ScopeEngagementRead)
	res.err = errors.New("db down")
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	if rec, _ := serveToken(res, "", v1Req("Bearer "+secret)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("code %d", rec.Code)
	}
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "level=ERROR") ||
		!strings.Contains(out, "api token lookup failed") || !strings.Contains(out, "db down") {
		t.Fatalf("log = %q", out)
	}
	if strings.Contains(out, secret) || strings.Contains(out, secret[:12]) || strings.Contains(out, secret[len(secret)-8:]) {
		t.Fatalf("log names the token: %q", out)
	}
}
