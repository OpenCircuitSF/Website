package publicapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// TestAdminV1HasNoCORS: the CORS * on /api/public/v1/ (#0734) does not
// reach the admin /api/v1/, which stays same-origin.
func TestAdminV1HasNoCORS(t *testing.T) {
	reg := api.NewRegistry(api.Config{Version: "1"})
	reg.Add(api.CoreOperations("1")...)
	h := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitest.NewResolver(), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "x"})
	for _, c := range []struct{ method, path string }{{http.MethodGet, api.OpenAPIPath}, {http.MethodOptions, "/api/v1/me"}} {
		req := httptest.NewRequest(c.method, c.path, nil)
		req.Header.Set("Origin", "https://elsewhere.test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
			t.Errorf("%s %s has Access-Control-Allow-Origin %q", c.method, c.path, v)
		}
	}
}
