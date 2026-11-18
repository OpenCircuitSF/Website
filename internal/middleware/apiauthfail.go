package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// APIAuthFailuresPerMinute is how many failed bearer authentications (401s)
// one client IP may make on /api/v1/ and /mcp before it is refused (#0683).
// The bucket holds a minute's allowance, so a client with a mistyped key sees
// its 401s first and only then the 429.
const APIAuthFailuresPerMinute = 10

// NewAPIAuthFailureLimiter returns the limiter for failed API authentication:
// APIAuthFailuresPerMinute per client IP. Build one and share it between
// /api/v1/ and /mcp, so an IP has one failure budget across both.
func NewAPIAuthFailureLimiter() *RateLimiter {
	return NewRateLimiter(rate.Every(time.Minute/APIAuthFailuresPerMinute), APIAuthFailuresPerMinute)
}

// APIAuthFailureLimit counts failed bearer authentication per client IP
// (ClientIP, the same X-Forwarded-For rule as every IP limiter): a request
// that presented a token-shaped bearer (apiBearerToken) and got 401, which is
// exactly a token lookup that found nothing. A missing or malformed header is
// refused by RequireAPIToken without a lookup, so it costs nothing and is not
// counted; a request that authenticates (or gets 403) is not counted either.
// Once an IP's bucket is empty, every request from it gets 429 with
// Retry-After before the wrapped handler runs, so further bad keys cost no
// database lookup. It wraps RequireAPIToken from the outside (#0683).
//
// The 429 is the v1 envelope (writeAPIError), like APITokenRateLimit's; the
// {"error":"rate_limit_exceeded"} body of the signup and login IP limiters is
// not used here and stays as it is. A nil rl disables the limit.
func APIAuthFailureLimit(rl *RateLimiter) func(http.Handler) http.Handler {
	return authFailureLimit(rl, func(r *http.Request) bool { return apiBearerToken(r) != "" })
}

// BearerAuthFailureLimit is APIAuthFailureLimit for a route whose bearer is
// not an API token, such as the media upload ticket (#0708): it counts a 401
// for any request with a non-empty Bearer value (scheme matched without
// regard to case). A nil rl disables the limit.
func BearerAuthFailureLimit(rl *RateLimiter) func(http.Handler) http.Handler {
	return authFailureLimit(rl, func(r *http.Request) bool {
		h := r.Header.Get("Authorization")
		return len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") && strings.TrimSpace(h[7:]) != ""
	})
}

// authFailureLimit is the shared body: presented says whether a 401 counts.
func authFailureLimit(rl *RateLimiter, presented func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if rl == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "authfail:" + ClientIP(r)
			lim := rl.limiterFor(key)
			now := time.Now()
			if tokens := lim.TokensAt(now); tokens < 1 {
				wait := time.Duration((1 - tokens) / float64(rl.rate) * float64(time.Second))
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(wait)))
				writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "too many failed authentication attempts from this address")
				return
			}
			sw := &authStatusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			if sw.status == http.StatusUnauthorized && presented(r) {
				lim.AllowN(time.Now(), 1)
			}
		})
	}
}

// authStatusWriter records the status code a handler writes. It passes Flush
// and Unwrap through, so http.ResponseController and the MCP SDK still reach
// the underlying writer.
type authStatusWriter struct {
	http.ResponseWriter
	status int
}

func (s *authStatusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *authStatusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *authStatusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *authStatusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
