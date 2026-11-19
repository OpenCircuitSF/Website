package middleware

import (
	"context"
	"encoding/json"
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
//
// It does not use MiddlewareByKey: that body, {"error":"rate_limit_exceeded"},
// is shared with the IP limiters on signup and login and stays as it is,
// while every /api/v1 error is the #0675 envelope (writeAPIError).
func APITokenRateLimit(rl *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "token:none"
			if t, ok := APITokenFromContext(r.Context()); ok {
				key = "token:" + strconv.FormatInt(t.ID, 10)
			}
			if wait, ok := rl.allow(key, time.Now()); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(wait)))
				writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "too many requests for this API key")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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
				writeAPIError(w, http.StatusInternalServerError, "internal", "internal error")
				return
			}
			if !apitokens.ScopeSatisfies(t.Scopes, scope) {
				writeAPIError(w, http.StatusForbidden, "forbidden", "this API key lacks the "+scope+" scope")
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
	writeAPIError(w, http.StatusUnauthorized, "unauthorized", "a valid API key is required")
}

// apiErrorBody is the v1 error envelope (#0675). It must stay identical in
// shape to api.ErrorBody: internal/api's envelope_test.go decodes these
// bodies into that type. Fields is never set here; only validation errors
// carry it, and internal/api writes those.
type apiErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// writeAPIError writes {"error":{"code":...,"message":...}}. RequireAPIToken
// guards only /api/v1/ and /admin/mcp, so no other caller sees it.
func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	var b apiErrorBody
	b.Error.Code, b.Error.Message = code, message
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(b)
}
