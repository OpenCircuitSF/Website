package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
)

// APIv1RequestsPerMinute is the per-token request budget on /api/v1 (#0674).
// The bucket holds a full minute's allowance, so a burst is allowed and then
// refills at this rate.
const APIv1RequestsPerMinute = 120

// APITokenResolver looks up an active API token by its secret (#0652).
// *apitokens.Store satisfies it.
type APITokenResolver interface {
	Resolve(ctx context.Context, secret string, now time.Time) (apitokens.Token, error)
}

// apiTokenContextKey is the key under which the resolved apitokens.Token is
// stored (userContextKey is 0).
const apiTokenContextKey contextKey = 1

// APITokenFromContext returns the token RequireAPIToken resolved for this
// request (#0674). apitokens.Token never carries the secret.
func APITokenFromContext(ctx context.Context) (apitokens.Token, bool) {
	t, ok := ctx.Value(apiTokenContextKey).(apitokens.Token)
	return t, ok
}

// WithAPIToken returns ctx carrying t, as RequireAPIToken does. It exists for
// tests of handlers that sit behind RequireAPIToken.
func WithAPIToken(ctx context.Context, t apitokens.Token) context.Context {
	return context.WithValue(ctx, apiTokenContextKey, t)
}

// AttributeAPIToken returns e attributed to the request's API token when one
// is in ctx (audit.AttributeAPIToken), and e unchanged otherwise. A /api/v1
// write handler passes every audit entry through it.
func AttributeAPIToken(ctx context.Context, e audit.Entry) audit.Entry {
	t, ok := APITokenFromContext(ctx)
	if !ok {
		return e
	}
	return audit.AttributeAPIToken(e, audit.APITokenActor{ID: t.ID, Name: t.Name, Prefix: t.Prefix, CreatedBy: t.CreatedBy})
}

// NewAPITokenRateLimiter returns the limiter for /api/v1: APIv1RequestsPerMinute
// per token. Build one and share it across every v1 route, so the budget is
// per token rather than per route.
func NewAPITokenRateLimiter() *RateLimiter {
	return NewRateLimiter(rate.Every(time.Minute/APIv1RequestsPerMinute), APIv1RequestsPerMinute)
}

// APITokenRateLimit buckets requests by the API token RequireAPIToken put in
// the context, so it must sit inside RequireAPIToken. Over the limit gets 429
// with Retry-After. A request with no token in context (a wiring bug) shares
// one bucket rather than going unlimited.
func APITokenRateLimit(rl *RateLimiter) func(http.Handler) http.Handler {
	return rl.MiddlewareByKey(func(r *http.Request) string {
		if t, ok := APITokenFromContext(r.Context()); ok {
			return "token:" + strconv.FormatInt(t.ID, 10)
		}
		return "token:none"
	})
}

// apiBearerToken returns the token from an "Authorization: Bearer" header, or
// "" when the header is absent, malformed, or not API-token shaped. It never
// reads the query string (it would end up in Apache logs) or any cookie.
func apiBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) <= len(bearerPrefix) || !strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	tok := strings.TrimSpace(h[len(bearerPrefix):])
	if tok == "" || !apitokens.LooksLikeToken(tok) {
		return ""
	}
	return tok
}

// RequireAPIToken guards a /api/v1/ route (#0652). A missing, malformed,
// unknown, revoked or expired token gets the same 401; a valid token lacking
// scope gets 403. A token holding the matching write scope satisfies a read
// scope (apitokens.ScopeSatisfies, #0674). An empty scope accepts any valid
// token. The resolved token is in the context (APITokenFromContext). Every response, failure or
// not, is Cache-Control: no-store. The only log line is an unexpected
// resolver error (the 500), and it never names the presented token.
func RequireAPIToken(resolver APITokenResolver, scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			tok := apiBearerToken(r)
			if tok == "" {
				writeAPIUnauthenticated(w)
				return
			}
			t, err := resolver.Resolve(r.Context(), tok, nowFunc())
			if err != nil {
				if errors.Is(err, apitokens.ErrNotFound) {
					writeAPIUnauthenticated(w)
					return
				}
				slog.Error("api token lookup failed", "err", err)
				writeJSONError(w, http.StatusInternalServerError, "internal_error")
				return
			}
			if !apitokens.ScopeSatisfies(t.Scopes, scope) {
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithAPIToken(r.Context(), t)))
		})
	}
}

// writeAPIUnauthenticated writes the one 401 every token failure gets, so the
// response never says which failure it was.
func writeAPIUnauthenticated(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="opencircuitsf"`)
	writeUnauthenticated(w)
}
