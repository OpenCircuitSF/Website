package publicapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

type slugIn struct {
	Slug string `json:"slug" path:"slug"`
}

type limitOut struct {
	Limit int `json:"limit"`
}

type listIn struct {
	Limit *int `json:"limit,omitempty" query:"limit"`
}

// stubRouter serves get_x (always ok), list_x (echoes the clamped limit)
// and get_thing/{slug} (found only for "ok").
func stubRouter(t *testing.T) http.Handler {
	t.Helper()
	return NewHTTPRouter(stubRegistry(), "http://example.test", nil)
}

// stubRegistry is stubRouter's registry, unfrozen.
func stubRegistry() *Registry {
	reg := NewRegistry("1.0.0")
	reg.Add(def(good()),
		Define(api.Operation{Name: "list_x", Method: http.MethodGet, Path: Prefix + "list", Summary: "List."},
			func(_ context.Context, in listIn) (limitOut, error) {
				return limitOut{Limit: ClampLimit(in.Limit)}, nil
			}, func(limitOut) string { return "" }),
		Define(api.Operation{Name: "get_thing", Method: http.MethodGet, Path: Prefix + "things/{slug}", Summary: "Get a thing."},
			func(_ context.Context, in slugIn) (tOut, error) {
				if in.Slug != "ok" {
					return tOut{}, api.NotFound("thing")
				}
				return tOut{OK: true}, nil
			}, func(tOut) string { return "" }))
	return reg
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServesEachOperation(t *testing.T) {
	h := stubRouter(t)
	for _, p := range []string{Prefix + "x", Prefix + "list?limit=999", Prefix + "things/ok"} {
		rec := do(h, http.MethodGet, p, nil)
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" ||
			rec.Header().Get("Cache-Control") != "public, max-age=60" || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("GET %s = %d %v %s", p, rec.Code, rec.Header(), rec.Body)
		}
	}
	if b := do(h, http.MethodGet, Prefix+"list?limit=999", nil).Body.String(); !strings.Contains(b, `"limit":50`) {
		t.Errorf("limit not clamped: %s", b)
	}
	if rec := do(h, http.MethodGet, Prefix+"list?limit=abc", nil); rec.Code != 400 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("bad limit = %d %v", rec.Code, rec.Header())
	}
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b api.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body %q is not the error envelope: %v", rec.Body, err)
	}
	return b.Error.Code
}

func TestUnknownAndMethodNotAllowed(t *testing.T) {
	h := stubRouter(t)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := do(h, m, Prefix+"things/ok", nil)
		if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD, OPTIONS" || rec.Header().Get("Access-Control-Allow-Origin") != "*" || errCode(t, rec) != api.CodeMethodNotAllowed {
			t.Errorf("%s = %d %v", m, rec.Code, rec.Header())
		}
	}
	rec := do(h, http.MethodGet, Prefix+"nope", nil)
	if rec.Code != 404 || rec.Header().Get("Cache-Control") != "no-store" || errCode(t, rec) != api.CodeNotFound {
		t.Errorf("unknown = %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
}

func TestPreflight(t *testing.T) {
	rec := do(stubRouter(t), http.MethodOptions, Prefix+"x", map[string]string{"Origin": "https://elsewhere.test", "Access-Control-Request-Method": "GET"})
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "*" ||
		rec.Header().Get("Access-Control-Allow-Methods") != "GET, HEAD, OPTIONS" ||
		rec.Header().Get("Access-Control-Allow-Headers") != "*" || rec.Header().Get("Access-Control-Max-Age") != "86400" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("OPTIONS = %d %v", rec.Code, rec.Header())
	}
}

func TestAuthorizationIgnored(t *testing.T) {
	h := stubRouter(t)
	for _, p := range []string{Prefix + "x", Prefix + "things/ok", Prefix + "things/missing", Prefix + "nope", OpenAPIPath} {
		plain := do(h, http.MethodGet, p, nil)
		keyed := do(h, http.MethodGet, p, map[string]string{"Authorization": "Bearer ocsf_x"})
		if plain.Code != keyed.Code || !bytes.Equal(plain.Body.Bytes(), keyed.Body.Bytes()) {
			t.Errorf("%s differs with Authorization: %d vs %d", p, plain.Code, keyed.Code)
		}
		for _, r := range []*httptest.ResponseRecorder{plain, keyed} {
			if r.Code == 401 || r.Header().Get("WWW-Authenticate") != "" {
				t.Errorf("%s = %d %v", p, r.Code, r.Header())
			}
		}
	}
}

func TestNotFoundBodyIsUniform(t *testing.T) {
	h := stubRouter(t)
	a, b := do(h, http.MethodGet, Prefix+"things/draft-one", nil), do(h, http.MethodGet, Prefix+"things/never-existed", nil)
	if a.Code != 404 || b.Code != 404 || !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
		t.Errorf("404 bodies differ: %d %s / %d %s", a.Code, a.Body, b.Code, b.Body)
	}
}

func TestLimitWrapsOperations(t *testing.T) {
	reg := NewRegistry("1")
	reg.Add(def(good()))
	h := NewHTTPRouter(reg, "x", NewLimiter())
	for i := 0; i < RequestsPerMinute; i++ {
		do(h, http.MethodGet, Prefix+"x", nil)
	}
	if do(h, http.MethodGet, Prefix+"x", nil).Code != http.StatusTooManyRequests {
		t.Error("operation not wrapped")
	}
	if do(h, http.MethodGet, OpenAPIPath, nil).Code != http.StatusOK {
		t.Error("OpenAPI document wrapped")
	}
}

func TestMountRoutesEveryMethod(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<html>spa") }))
	Mount(mux, stubRouter(t))
	for _, m := range Methods {
		rec := do(mux, m, Prefix+"nope", nil)
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s did not reach the router: %d %s", m, rec.Code, rec.Body)
		}
	}
}
