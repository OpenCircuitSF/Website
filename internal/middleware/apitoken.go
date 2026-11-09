package middleware

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

// APIToken is the principal RequireAPIToken attaches; it never carries the secret.
type APIToken struct {
	ID     int64
	Name   string
	Prefix string
	Scopes []string
}

// APITokenResolver looks up an active API token by its secret (#0652).
// *apitokens.Store satisfies it.
type APITokenResolver interface {
	Resolve(ctx context.Context, secret string, now time.Time) (apitokens.Token, error)
}

// apiTokenContextKey is the key under which the APIToken is stored
// (userContextKey is 0).
const apiTokenContextKey contextKey = 1

// APITokenFromContext returns the token principal attached by RequireAPIToken.
func APITokenFromContext(ctx context.Context) (*APIToken, bool) {
	t, ok := ctx.Value(apiTokenContextKey).(*APIToken)
	return t, ok
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
// unknown or revoked token gets the same 401; a valid token lacking scope gets
// 403. An empty scope accepts any valid token. Nothing here logs.
func RequireAPIToken(resolver APITokenResolver, scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				writeJSONError(w, http.StatusInternalServerError, "internal_error")
				return
			}
			if scope != "" && !slices.Contains(t.Scopes, scope) {
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			p := &APIToken{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scopes: t.Scopes}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), apiTokenContextKey, p)))
		})
	}
}

// writeAPIUnauthenticated writes the one 401 every token failure gets, so the
// response never says which failure it was.
func writeAPIUnauthenticated(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="opencircuitsf"`)
	writeUnauthenticated(w)
}
