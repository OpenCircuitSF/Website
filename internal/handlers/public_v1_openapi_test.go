package handlers

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
)

// servedPublicOpenAPI serves the real public registry's OpenAPI document
// in-process and returns it with the registry (#0737).
func servedPublicOpenAPI(t *testing.T) ([]byte, *publicapi.Registry) {
	t.Helper()
	reg := BuildPublicRegistry(PublicDeps{BaseURL: "https://www.opencircuitsf.com", Version: "test"})
	h := publicapi.NewHTTPRouter(reg, "https://www.opencircuitsf.com", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, publicapi.OpenAPIPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", publicapi.OpenAPIPath, rec.Code)
	}
	b, _ := io.ReadAll(rec.Body)
	return b, reg
}

// TestPublicOpenAPIMatchesRealRegistry runs OpenAPIDrift over the document
// served for the real registry, not a stub (#0737).
func TestPublicOpenAPIMatchesRealRegistry(t *testing.T) {
	body, reg := servedPublicOpenAPI(t)
	for _, d := range publicapi.OpenAPIDrift(body, reg) {
		t.Error(d)
	}
	if n := len(reg.Ops()); n < 6 {
		t.Errorf("registry has %d ops, want at least 6", n)
	}
}

// TestPublicOpenAPIDriftBites: each mutation of the served document is
// reported by OpenAPIDrift.
func TestPublicOpenAPIDriftBites(t *testing.T) {
	body, reg := servedPublicOpenAPI(t)
	for _, m := range []struct{ name, old, new string }{
		{"schema key", `"starts_at"`, `"start"`},
		{"query parameter", `"include_past"`, `"includePast"`},
		{"operationId", `"operationId": "get_about"`, `"operationId": "about"`}, // MarshalIndent spacing
	} {
		mutated := bytes.Replace(body, []byte(m.old), []byte(m.new), 1)
		if bytes.Equal(mutated, body) {
			t.Errorf("%s: mutation did not apply", m.name)
			continue
		}
		if len(publicapi.OpenAPIDrift(mutated, reg)) == 0 {
			t.Errorf("%s: OpenAPIDrift reported nothing", m.name)
		}
	}
}
