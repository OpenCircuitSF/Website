package publicapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// TestOpenAPIMatchesRegistry ties the generated document to the routes
// through OpenAPIDrift, and checks the server URL the stub passes in.
func TestOpenAPIMatchesRegistry(t *testing.T) {
	reg := NewRegistry("1.0.0")
	reg.Add(def(good()))
	reg2 := stubRegistry()
	for _, r := range []*Registry{reg, reg2} {
		b, err := buildOpenAPI(r, "http://example.test/")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Servers []struct {
				URL string `json:"url"`
			} `json:"servers"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Servers) != 1 || doc.Servers[0].URL != "http://example.test" {
			t.Errorf("servers %+v", doc.Servers)
		}
		for _, d := range OpenAPIDrift(b, r) {
			t.Error(d)
		}
	}
}

// TestOpenAPIServed: the router serves exactly buildOpenAPI's bytes.
func TestOpenAPIServed(t *testing.T) {
	want, err := buildOpenAPI(stubRegistry(), "http://example.test")
	if err != nil {
		t.Fatal(err)
	}
	rec := do(stubRouter(t), http.MethodGet, OpenAPIPath, nil)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), want) || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("served %d, %d bytes, want %d", rec.Code, rec.Body.Len(), len(want))
	}
}
