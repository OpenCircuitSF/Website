package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

type errResolver struct{}

func (errResolver) Resolve(context.Context, string, time.Time) (apitokens.Token, error) {
	return apitokens.Token{}, errors.New("db down")
}

// TestMiddlewareErrorBodiesUseEnvelope keeps middleware's writeAPIError and
// ErrorBody in step: the 401, 403, 429 and 500 the token middleware writes
// itself decode as ErrorBody with the expected code, and nothing else.
func TestMiddlewareErrorBodiesUseEnvelope(t *testing.T) {
	quietLogs(t)
	secret, _ := apitokens.Generate()
	res := fakeResolver{secret: {ID: 1, Scopes: []string{apitokens.ScopeVenuesRead}}}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	run := func(h http.Handler, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rl := middleware.NewRateLimiter(rate.Every(time.Hour), 1)
	limited := middleware.RequireAPIToken(res, "")(middleware.APITokenRateLimit(rl)(ok))
	run(limited, secret)
	for _, c := range []struct {
		name   string
		rec    *httptest.ResponseRecorder
		status int
		code   string
	}{
		{"401", run(middleware.RequireAPIToken(res, "")(ok), ""), 401, CodeUnauthorized},
		{"403", run(middleware.RequireAPIToken(res, apitokens.ScopeEngagementRead)(ok), secret), 403, CodeForbidden},
		{"429", run(limited, secret), 429, CodeRateLimited},
		{"500", run(middleware.RequireAPIToken(errResolver{}, "")(ok), secret), 500, CodeInternal},
	} {
		if c.rec.Code != c.status {
			t.Errorf("%s: status %d", c.name, c.rec.Code)
			continue
		}
		if d := errBody(t, c.rec.Body.Bytes()); d.Code != c.code || d.Message == "" || d.Fields != nil {
			t.Errorf("%s: %+v, want code %s", c.name, d, c.code)
		}
	}
}
