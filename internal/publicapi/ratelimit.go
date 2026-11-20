package publicapi

import (
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// Limiter is the per-client-IP budget for the whole public surface
// (#0730): RequestsPerMinute, keyed by middleware.ClientIP.
type Limiter struct{ rl *middleware.RateLimiter }

// NewLimiter returns a fresh budget. Build one in mountAndServe and pass it
// to every public adapter, so an IP has one budget across /mcp and
// /api/public/v1/; never share it with an admin limiter.
func NewLimiter() *Limiter {
	return &Limiter{rl: middleware.NewRateLimiter(rate.Every(time.Minute/RequestsPerMinute), RequestsPerMinute)}
}

// Wrap refuses a request over budget with the v1 error envelope (429,
// Retry-After) before it reaches next. A nil *Limiter returns next
// unchanged, for tests only.
func (l *Limiter) Wrap(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wait, ok := l.rl.Allow("public:" + middleware.ClientIP(r)); !ok {
			writeJSONError(w, api.RateLimited("too many requests; try again shortly", wait))
			return
		}
		next.ServeHTTP(w, r)
	})
}
