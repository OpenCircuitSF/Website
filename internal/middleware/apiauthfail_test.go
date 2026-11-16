package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

// authFailChain is the #0683 order: the failure limit outside RequireAPIToken.
func authFailChain(rl *RateLimiter, res APITokenResolver, scope string) http.Handler {
	return APIAuthFailureLimit(rl)(RequireAPIToken(res, scope)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
}

func authFailReq(remote, xff, bearer string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/token", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	return r
}

func unknownSecret(t *testing.T) string {
	t.Helper()
	s, err := apitokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// slowLimiter refills one token an hour, so a test never sees a refill.
func slowLimiter(burst int) *RateLimiter { return NewRateLimiter(rate.Every(time.Hour), burst) }

func TestAPIAuthFailureLimit_RefusesAfterBudgetWithoutLookup(t *testing.T) {
	res, good := newTokenFixture(t, apitokens.ScopeEngagementRead)
	h := authFailChain(slowLimiter(3), res, "")
	bad := unknownSecret(t)
	for i := range 3 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authFailReq("203.0.113.5:1000", "", bad))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("bad key #%d = %d, want 401", i+1, rec.Code)
		}
	}
	if res.calls != 3 {
		t.Fatalf("resolver calls = %d, want 3", res.calls)
	}
	for _, secret := range []string{bad, good} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authFailReq("203.0.113.5:1000", "", secret))
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("over the limit = %d, want 429", rec.Code)
		}
		assertAPIErrorCode(t, rec.Body.Bytes(), "rate_limited")
		if s, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || s < 1 {
			t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("Cache-Control %q", rec.Header().Get("Cache-Control"))
		}
	}
	if res.calls != 3 {
		t.Errorf("resolver calls after refusal = %d, want 3 (no lookup once over)", res.calls)
	}
}

// TestAPIAuthFailureLimit_NoLookupNotCounted: a missing or malformed header
// is refused without a token lookup, so it does not spend the budget.
func TestAPIAuthFailureLimit_NoLookupNotCounted(t *testing.T) {
	res, _ := newTokenFixture(t)
	h := authFailChain(slowLimiter(1), res, "")
	for i, bearer := range []string{"", "not-a-key", "", "ocsf_short"} {
		for range 3 {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, authFailReq("203.0.113.6:1000", "", bearer))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("case %d (%q) = %d, want 401", i, bearer, rec.Code)
			}
		}
	}
	if res.calls != 0 {
		t.Fatalf("resolver calls = %d, want 0", res.calls)
	}
	bad := unknownSecret(t)
	for _, want := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authFailReq("203.0.113.6:1000", "", bad))
		if rec.Code != want {
			t.Fatalf("unknown key = %d, want %d", rec.Code, want)
		}
	}
}

func TestAPIAuthFailureLimit_SuccessesAndForbiddenDoNotCount(t *testing.T) {
	res, good := newTokenFixture(t, apitokens.ScopeEngagementRead)
	rl := slowLimiter(2)
	okH := authFailChain(rl, res, "")
	forbidH := authFailChain(rl, res, apitokens.ScopeEventsRead)
	for i := range 20 {
		rec := httptest.NewRecorder()
		okH.ServeHTTP(rec, authFailReq("203.0.113.7:1", "", good))
		if rec.Code != http.StatusOK {
			t.Fatalf("valid key #%d = %d", i+1, rec.Code)
		}
		rec = httptest.NewRecorder()
		forbidH.ServeHTTP(rec, authFailReq("203.0.113.7:1", "", good))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("missing scope #%d = %d, want 403", i+1, rec.Code)
		}
	}
	bad := unknownSecret(t)
	want := []int{http.StatusUnauthorized, http.StatusUnauthorized, http.StatusTooManyRequests}
	for i, code := range want {
		rec := httptest.NewRecorder()
		okH.ServeHTTP(rec, authFailReq("203.0.113.7:1", "", bad))
		if rec.Code != code {
			t.Fatalf("bad key #%d = %d, want %d", i+1, rec.Code, code)
		}
	}
}

// TestAPIAuthFailureLimit_BucketsByClientIP: buckets follow ClientIP, so a
// spoofed leftmost X-Forwarded-For entry behind Apache does not buy a fresh
// bucket, while a different real client does.
func TestAPIAuthFailureLimit_BucketsByClientIP(t *testing.T) {
	res, _ := newTokenFixture(t)
	h := authFailChain(slowLimiter(1), res, "")
	bad := unknownSecret(t)
	serve := func(remote, xff string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authFailReq(remote, xff, bad))
		return rec.Code
	}
	if c := serve("127.0.0.1:5000", "198.51.100.1, 192.0.2.10"); c != http.StatusUnauthorized {
		t.Fatalf("first = %d", c)
	}
	if c := serve("127.0.0.1:5001", "198.51.100.99, 192.0.2.10"); c != http.StatusTooManyRequests {
		t.Errorf("same real client, spoofed leftmost entry = %d, want 429", c)
	}
	if c := serve("127.0.0.1:5002", "192.0.2.11"); c != http.StatusUnauthorized {
		t.Errorf("another client behind the proxy = %d, want 401", c)
	}
	if c := serve("192.0.2.10:7000", ""); c != http.StatusTooManyRequests {
		t.Errorf("the same IP connecting directly = %d, want 429", c)
	}
}

func TestAPIAuthFailureLimit_NilIsPassThrough(t *testing.T) {
	res, _ := newTokenFixture(t)
	h := authFailChain(nil, res, "")
	bad := unknownSecret(t)
	for range 50 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authFailReq("203.0.113.8:1", "", bad))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("nil limiter = %d, want 401", rec.Code)
		}
	}
}

func TestNewAPIAuthFailureLimiter_Budget(t *testing.T) {
	rl := NewAPIAuthFailureLimiter()
	if rl.burst != APIAuthFailuresPerMinute || rl.rate != rate.Every(time.Minute/APIAuthFailuresPerMinute) {
		t.Errorf("burst %d rate %v", rl.burst, rl.rate)
	}
}
