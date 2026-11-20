package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter enforces a per-IP request rate using a token-bucket limiter per
// client IP. It is intended for the public, abuse-prone auth endpoints
// (registration, login, recovery), where a single source should not be able to
// hammer the WebAuthn/email machinery.
//
// The limiter is constructed once per protected route with NewRateLimiter and
// installed via Middleware. Each distinct client IP gets its own
// *rate.Limiter created lazily on first sight and reused thereafter; the map is
// guarded by a mutex so the middleware is safe for concurrent use.
//
// Memory note: limiters are never evicted. For this single-instance service
// behind Apache, the set of distinct client IPs is bounded enough that the
// simplicity is worth it; see issues/0020.md Gotchas for the tradeoff.
type RateLimiter struct {
	mu    sync.Mutex
	rate  rate.Limit
	burst int
	byIP  map[string]*rate.Limiter
}

// NewRateLimiter returns a RateLimiter that allows requests at sustained rate r
// with bucket capacity burst, applied independently per client IP. Setting
// burst to the per-window allowance lets a freshly seen IP spend its whole
// allotment immediately, then refill at rate r.
//
// Examples matching the PRD's Phase 2 auth limits:
//
//	NewRateLimiter(rate.Every(time.Hour/3), 3)    // 3 requests / hour / IP
//	NewRateLimiter(rate.Every(time.Minute/10), 10) // 10 requests / minute / IP
func NewRateLimiter(r rate.Limit, burst int) *RateLimiter {
	return &RateLimiter{
		rate:  r,
		burst: burst,
		byIP:  make(map[string]*rate.Limiter),
	}
}

// limiterFor returns the *rate.Limiter for the given IP, creating it on first
// sight. The lock is held only for the map lookup/insert.
func (rl *RateLimiter) limiterFor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	lim, ok := rl.byIP[ip]
	if !ok {
		lim = rate.NewLimiter(rl.rate, rl.burst)
		rl.byIP[ip] = lim
	}
	return lim
}

// Middleware returns an http middleware that rejects requests from a client IP
// once that IP exceeds the configured rate, responding 429 Too Many Requests
// with a {"error":"rate_limit_exceeded"} JSON body and a Retry-After header.
// Requests within the limit are passed to next unchanged.
//
// The client IP is derived by ClientIP (this package, clientip.go) — the
// single security-relevant X-Forwarded-For parse shared with
// internal/handlers.clientIP, so every component buckets/attributes clients
// identically. See ClientIP's doc comment for the trust model (#0077).
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return rl.MiddlewareByKey(ClientIP)(next)
}

// MiddlewareByKey is Middleware with the bucket chosen by key instead of the
// client IP (#0674 buckets /api/v1 by API token). A request over the limit
// gets 429 with Retry-After: the whole seconds, rounded up, until one more
// request would be allowed.
func (rl *RateLimiter) MiddlewareByKey(key func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if wait, ok := rl.allow(key(r), time.Now()); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(wait)))
				writeJSONError(w, http.StatusTooManyRequests, "rate_limit_exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Allow spends one token from key's bucket. When the bucket is empty it
// spends nothing and returns how long until a token is available. For
// callers that write their own refusal (publicapi.Limiter, #0730).
func (rl *RateLimiter) Allow(key string) (time.Duration, bool) { return rl.allow(key, time.Now()) }

// allow spends one token from key's bucket. When the bucket is empty it spends
// nothing and returns how long until a token is available.
func (rl *RateLimiter) allow(key string, now time.Time) (time.Duration, bool) {
	res := rl.limiterFor(key).ReserveN(now, 1)
	if !res.OK() {
		return time.Minute, false
	}
	if d := res.DelayFrom(now); d > 0 {
		res.CancelAt(now)
		return d, false
	}
	return 0, true
}

// retryAfterSeconds rounds d up to whole seconds, at least 1.
func retryAfterSeconds(d time.Duration) int {
	n := int(math.Ceil(d.Seconds()))
	if n < 1 {
		n = 1
	}
	return n
}
