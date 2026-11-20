package publicapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

func limited(t *testing.T) (http.Handler, *int) {
	t.Helper()
	n := new(int)
	return NewLimiter().Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { *n++ })), n
}

func hit(h http.Handler, peer, xff string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, Prefix+"x", nil)
	req.RemoteAddr = peer
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLimiterRefusesOverBudget(t *testing.T) {
	h, n := limited(t)
	for i := 0; i < RequestsPerMinute; i++ {
		if rec := hit(h, "192.0.2.1:1234", ""); rec.Code != 200 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	rec := hit(h, "192.0.2.1:1234", "")
	if rec.Code != http.StatusTooManyRequests || *n != RequestsPerMinute {
		t.Fatalf("over budget: %d, next ran %d times", rec.Code, *n)
	}
	if s, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || s < 1 || s > 60 {
		t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control %q", rec.Header().Get("Cache-Control"))
	}
	var body api.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != api.CodeRateLimited {
		t.Errorf("body %s (%v)", rec.Body, err)
	}
}

func TestLimiterBucketsByTrustedClientIP(t *testing.T) {
	h, _ := limited(t)
	for i := 0; i < RequestsPerMinute; i++ {
		hit(h, "192.0.2.2:1", "10.0.0."+strconv.Itoa(i))
	}
	if rec := hit(h, "192.0.2.2:1", "10.9.9.9"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("untrusted X-Forwarded-For reset the budget: %d", rec.Code)
	}
	for i := 0; i < RequestsPerMinute; i++ {
		hit(h, "127.0.0.1:5555", "203.0.113.1, 198.51.100.7")
	}
	if rec := hit(h, "127.0.0.1:5555", "203.0.113.1, 198.51.100.7"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("198.51.100.7 not limited: %d", rec.Code)
	}
	if rec := hit(h, "127.0.0.1:5555", "203.0.113.1, 198.51.100.8"); rec.Code != 200 {
		t.Errorf("198.51.100.8 shares 198.51.100.7's budget: %d", rec.Code)
	}
}

func TestNilLimiterPassesThrough(t *testing.T) {
	next := http.NotFoundHandler()
	var l *Limiter
	if rec := hit(l.Wrap(next), "192.0.2.1:1", ""); rec.Code != 404 {
		t.Errorf("nil limiter: %d", rec.Code)
	}
}
