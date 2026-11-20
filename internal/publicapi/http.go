package publicapi

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

// Methods are the methods Mount routes to the REST router, so each one
// gets its JSON and CORS answer rather than the mux's plain 405 or the SPA.
var Methods = []string{http.MethodGet, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// NewHTTPRouter freezes reg and returns the public REST handler for
// Prefix; mount it once with Mount. It serves GET OpenAPIPath (the
// document built from reg), each operation at GET op.Path through
// reg.Invoke, a JSON 405 for another method on a known path and a JSON
// 404 otherwise. Every response carries Access-Control-Allow-Origin: *,
// and OPTIONS is a 204 preflight. No request header is read for
// authentication: there is none.
//
// limiter wraps every operation route (not the OpenAPI document, the
// 404/405 catch-all or the CORS preflight); it is the per-IP budget shared
// with /mcp (#0730), and nil means unlimited, for tests only.
func NewHTTPRouter(reg *Registry, baseURL string, limiter *Limiter) http.Handler {
	reg.Freeze()
	doc, err := buildOpenAPI(reg, baseURL)
	if err != nil {
		panic("publicapi: OpenAPI document: " + err.Error())
	}
	inner := http.NewServeMux()
	inner.HandleFunc("GET "+OpenAPIPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(doc)
	})
	for _, op := range reg.Ops() {
		inner.Handle("GET "+op.Path, limiter.Wrap(serve(reg, op)))
	}
	inner.HandleFunc(Prefix, func(w http.ResponseWriter, r *http.Request) {
		probe := r.Clone(r.Context())
		probe.Method = http.MethodGet
		if _, pat := inner.Handler(probe); pat != "" && pat != Prefix {
			w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			writeJSONError(w, &api.Error{Status: http.StatusMethodNotAllowed, Code: api.CodeMethodNotAllowed, Message: "this endpoint does not accept " + r.Method})
			return
		}
		writeJSONError(w, &api.Error{Status: http.StatusNotFound, Code: api.CodeNotFound, Message: "no such API endpoint"})
	})
	return cors(inner)
}

// cors allows any origin to read these routes. There are no credentials,
// so no Allow-Credentials and no cookie is read.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serve binds op's input from the URL, runs it through reg.Invoke (the
// single call path shared with /mcp) and writes the JSON result.
func serve(reg *Registry, op Op) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in, e := api.BindURL(op.Operation, r)
		if e != nil {
			writeJSONError(w, e)
			return
		}
		out, err := reg.Invoke(r.Context(), op, in)
		if err != nil {
			writeJSONError(w, api.AsError(op.Name, err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(out)
	})
}

// writeJSONError is api's writeError, for every error this package
// writes (#0730's 429 included): the error envelope, no-store, and
// Retry-After for a 429.
func writeJSONError(w http.ResponseWriter, e *api.Error) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(e.RetryAfter.Seconds()))))
	}
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e.Body())
}

// Mount registers h for Prefix on mux, one pattern per method in Methods.
// A pattern with no method cannot be used: it conflicts with the SPA's
// "GET /" catch-all in Go's ServeMux. Call it once.
func Mount(mux *http.ServeMux, h http.Handler) {
	for _, m := range Methods {
		mux.Handle(m+" "+Prefix, h)
	}
}
