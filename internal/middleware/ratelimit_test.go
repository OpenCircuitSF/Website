package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// okHandler is the downstream handler the limiter wraps; it records that it ran
// and returns 200 so tests can distinguish a passed request (200) from a
// throttled one (429).
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// do issues one request through h with the given X-Forwarded-For and RemoteAddr
// (either may be empty) and returns the recorded response. Since #0077,
// X-Forwarded-For is honored only when RemoteAddr's host is loopback (the
// trusted Apache hop; see ClientIP in clientip.go) — tests that mean to
// simulate a request arriving via the proxy must pass a loopback RemoteAddr
// such as "127.0.0.1:1111".
func do(h http.Handler, xff, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/login/start", nil)
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestRateLimiterBurstThenReject confirms that with burst 2, the first two
// requests from one IP pass (200) and the third is rejected (429) with the
// canonical JSON body. Bucketed by RemoteAddr directly (no proxy hop) since
// this test only cares about the generic token-bucket behavior, not the
// X-Forwarded-For trust model — see TestRateLimiterUsesXForwardedFor and
// TestRateLimiterIgnoresXFFWithoutTrustedProxy for that.
func TestRateLimiterBurstThenReject(t *testing.T) {
	rl := NewRateLimiter(rate.Every(time.Minute), 2)
	h := rl.Middleware(okHandler())

	const remoteAddr = "203.0.113.7:1111"
	if got := do(h, "", remoteAddr).Code; got != http.StatusOK {
		t.Fatalf("request 1: status = %d, want 200", got)
	}
	if got := do(h, "", remoteAddr).Code; got != http.StatusOK {
		t.Fatalf("request 2: status = %d, want 200", got)
	}

	rec := do(h, "", remoteAddr)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request 3: status = %d, want 429", rec.Code)
	}
	if body := rec.Body.String(); body != `{"error":"rate_limit_exceeded"}` {
		t.Fatalf("request 3: body = %q, want rate_limit_exceeded JSON", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("request 3: Content-Type = %q, want JSON", ct)
	}
}

// TestRateLimiterPerIPIsolation confirms each IP has its own bucket: exhausting
// IP-A must not throttle IP-B. Bucketed by RemoteAddr directly (no proxy hop);
// see the note on TestRateLimiterBurstThenReject.
func TestRateLimiterPerIPIsolation(t *testing.T) {
	rl := NewRateLimiter(rate.Every(time.Minute), 1)
	h := rl.Middleware(okHandler())

	const ipA = "198.51.100.1:1111"
	const ipB = "198.51.100.2:2222"

	// Spend IP-A's single token, then confirm IP-A is throttled.
	if got := do(h, "", ipA).Code; got != http.StatusOK {
		t.Fatalf("IP-A request 1: status = %d, want 200", got)
	}
	if got := do(h, "", ipA).Code; got != http.StatusTooManyRequests {
		t.Fatalf("IP-A request 2: status = %d, want 429", got)
	}

	// IP-B has its own bucket and must still be allowed.
	if got := do(h, "", ipB).Code; got != http.StatusOK {
		t.Fatalf("IP-B request 1: status = %d, want 200 (IP-A exhaustion leaked)", got)
	}
}

// TestRateLimiterUsesXForwardedFor confirms the limiter buckets by the
// RIGHTMOST X-Forwarded-For entry — the one the trusted Apache hop itself
// appended, per mod_proxy_http's append-not-replace behaviour
// (deploy/apache/opencircuitsf.com.conf) — and ONLY when the immediate peer
// (RemoteAddr) is the loopback address that hop always connects from
// (deploy/systemd/opencircuit.service: "listens on 127.0.0.1:8080 behind the
// Apache reverse proxy"). This is #0077's fix: the old code trusted the
// LEFTMOST entry, which is client-supplied.
func TestRateLimiterUsesXForwardedFor(t *testing.T) {
	rl := NewRateLimiter(rate.Every(time.Minute), 1)
	h := rl.Middleware(okHandler())

	const realPeer = "192.0.2.55" // the entry the trusted proxy itself appended

	if got := do(h, realPeer, "127.0.0.1:1111").Code; got != http.StatusOK {
		t.Fatalf("XFF request 1: status = %d, want 200", got)
	}
	// Different RemoteAddr port (still loopback — still the trusted hop),
	// same single-entry XFF → same bucket → throttled.
	if got := do(h, realPeer, "127.0.0.1:2222").Code; got != http.StatusTooManyRequests {
		t.Fatalf("XFF request 2: status = %d, want 429 (XFF not honored for bucketing)", got)
	}

	// A multi-hop list "attacker-claimed-identity, realPeer" buckets on the
	// RIGHTMOST entry, not the client-supplied leftmost one: same realPeer as
	// above → still the same bucket → still throttled, even though the
	// attacker claims a brand-new leftmost identity on every request.
	if got := do(h, "203.0.113.200, "+realPeer, "127.0.0.1:3333").Code; got != http.StatusTooManyRequests {
		t.Fatalf("XFF list request: status = %d, want 429 (rightmost XFF entry not used — leftmost-trust regression)", got)
	}
}

// TestRateLimiterIgnoresXFFWithoutTrustedProxy proves the "no proxy in
// front" half of #0077's fix, and that a spoofed header cannot forge rate-
// limit identity in that case: when the immediate peer is NOT the trusted
// loopback hop — direct connections, dev mode, or a client that somehow
// reaches the service without going through Apache — X-Forwarded-For MUST be
// ignored entirely and bucketing must fall back to the real RemoteAddr.
func TestRateLimiterIgnoresXFFWithoutTrustedProxy(t *testing.T) {
	rl := NewRateLimiter(rate.Every(time.Minute), 1)
	h := rl.Middleware(okHandler())

	// Same spoofed XFF, two different non-loopback RemoteAddr peers (no
	// proxy in front) → XFF must be ignored → each buckets on its own real
	// RemoteAddr → neither throttles the other.
	const spoofed = "10.0.0.1"
	if got := do(h, spoofed, "203.0.113.50:1111").Code; got != http.StatusOK {
		t.Fatalf("direct request A: status = %d, want 200", got)
	}
	if got := do(h, spoofed, "203.0.113.51:2222").Code; got != http.StatusOK {
		t.Fatalf("direct request B (different real peer, same spoofed XFF): status = %d, want 200 — a spoofed X-Forwarded-For must not determine identity with no trusted proxy in front", got)
	}

	// But two requests from the SAME real (non-loopback) peer still share a
	// bucket via the RemoteAddr fallback — proving isolation isn't simply
	// broken, only the untrusted XFF is ignored.
	if got := do(h, spoofed, "203.0.113.50:9999").Code; got != http.StatusTooManyRequests {
		t.Fatalf("direct request A (second, different port): status = %d, want 429 (RemoteAddr fallback should still bucket by the real peer)", got)
	}
}

// TestRateLimiterFallsBackToRemoteAddr confirms that with no X-Forwarded-For,
// the limiter buckets by RemoteAddr (host portion, port stripped): two requests
// from the same host but different ports share a bucket.
func TestRateLimiterFallsBackToRemoteAddr(t *testing.T) {
	rl := NewRateLimiter(rate.Every(time.Minute), 1)
	h := rl.Middleware(okHandler())

	if got := do(h, "", "203.0.113.42:5000").Code; got != http.StatusOK {
		t.Fatalf("RemoteAddr request 1: status = %d, want 200", got)
	}
	// Same host, different port → same bucket → throttled.
	if got := do(h, "", "203.0.113.42:6000").Code; got != http.StatusTooManyRequests {
		t.Fatalf("RemoteAddr request 2: status = %d, want 429 (port not stripped?)", got)
	}
	// A different host gets its own bucket.
	if got := do(h, "", "203.0.113.43:5000").Code; got != http.StatusOK {
		t.Fatalf("RemoteAddr different host: status = %d, want 200", got)
	}
}

// TestRateLimiterReplenishes confirms tokens refill over time: with a fast
// refill (every 5ms) and burst 1, the second immediate request is throttled but
// a request after the refill interval is allowed again. Uses a short, generous
// interval to stay non-flaky. Bucketed by RemoteAddr directly (no proxy hop);
// see the note on TestRateLimiterBurstThenReject.
func TestRateLimiterReplenishes(t *testing.T) {
	rl := NewRateLimiter(rate.Every(5*time.Millisecond), 1)
	h := rl.Middleware(okHandler())

	const remoteAddr = "203.0.113.99:1111"
	if got := do(h, "", remoteAddr).Code; got != http.StatusOK {
		t.Fatalf("replenish request 1: status = %d, want 200", got)
	}
	if got := do(h, "", remoteAddr).Code; got != http.StatusTooManyRequests {
		t.Fatalf("replenish request 2 (immediate): status = %d, want 429", got)
	}

	// Wait comfortably past the refill interval, then expect a token again.
	time.Sleep(50 * time.Millisecond)
	if got := do(h, "", remoteAddr).Code; got != http.StatusOK {
		t.Fatalf("replenish request 3 (after refill): status = %d, want 200", got)
	}
}

// TestRateLimiterXFFRotationBypassFixed is #0077's reproduction, kept as a
// permanent regression test. It simulates exactly what mod_proxy_http does in
// deploy/apache/opencircuitsf.com.conf — APPEND the real peer to
// X-Forwarded-For rather than replace it — from a single real attacker
// source rotating a claimed leftmost identity on every request, behind the
// trusted proxy hop (RemoteAddr is loopback, matching
// deploy/systemd/opencircuit.service: "listens on 127.0.0.1:8080 behind the
// Apache reverse proxy"). Numbers mirror the reviewer's #0026 finding: limit
// 5/min, burst 3.
//
// Before the fix (leftmost-trust) this reproduced the reviewer's finding
// exactly: 50/50 requests accepted. Mutation-proof: reverting ClientIP to
// take the LEFTMOST X-Forwarded-For entry instead of the rightmost makes
// this fail again the same way.
func TestRateLimiterXFFRotationBypassFixed(t *testing.T) {
	rl := NewRateLimiter(rate.Every(time.Minute/5), 3)
	h := rl.Middleware(okHandler())

	const realPeer = "198.51.100.9" // what Apache actually appends
	accepted, throttled := 0, 0
	for i := 0; i < 50; i++ {
		xff := fmt.Sprintf("172.16.0.%d, %s", i, realPeer)
		rec := do(h, xff, "127.0.0.1:9999")
		if rec.Code == http.StatusOK {
			accepted++
		} else {
			throttled++
		}
	}
	t.Logf("XFF-rotating attacker, one real source IP, 50 requests: accepted=%d throttled=%d (limit 5/min, burst 3)", accepted, throttled)
	if accepted != 3 {
		t.Errorf("accepted = %d, want 3 (burst) — rotating X-Forwarded-For must not bypass the limiter", accepted)
	}
}
