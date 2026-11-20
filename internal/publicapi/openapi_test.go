package publicapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestOpenAPIMatchesRegistry ties the generated document to the routes:
// the same paths, operationIds and x-mcp-tool names as the registry, the
// same parameters as URLParams, the same 200 schemas, and no auth.
func TestOpenAPIMatchesRegistry(t *testing.T) {
	reg := NewRegistry("1.0.0")
	reg.Add(def(good()))
	reg2 := stubRegistry()
	for _, r := range []*Registry{reg, reg2} {
		b, err := buildOpenAPI(r, "http://example.test/")
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("securitySchemes")) || bytes.Contains(b, []byte(`"401"`)) || bytes.Contains(b, []byte(`"403"`)) {
			t.Error("document mentions auth")
		}
		var doc struct {
			OpenAPI string `json:"openapi"`
			Servers []struct {
				URL string `json:"url"`
			} `json:"servers"`
			Paths map[string]map[string]struct {
				OperationID string `json:"operationId"`
				MCPTool     string `json:"x-mcp-tool"`
				Parameters  []struct {
					Name string `json:"name"`
					In   string `json:"in"`
				} `json:"parameters"`
				Responses map[string]struct {
					Content map[string]struct {
						Schema json.RawMessage `json:"schema"`
					} `json:"content"`
				} `json:"responses"`
			} `json:"paths"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.OpenAPI != "3.1.0" || len(doc.Servers) != 1 || doc.Servers[0].URL != "http://example.test" {
			t.Errorf("header: %s %+v", doc.OpenAPI, doc.Servers)
		}
		want := []string{"GET " + OpenAPIPath}
		for _, op := range r.Ops() {
			want = append(want, "GET "+op.Path)
		}
		var got []string
		for p, item := range doc.Paths {
			for m := range item {
				got = append(got, strings.ToUpper(m)+" "+p)
			}
		}
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("routes %v, want %v", got, want)
		}
		for _, op := range r.Ops() {
			e := doc.Paths[op.Path]["get"]
			if e.OperationID != op.Name || e.MCPTool != op.Name {
				t.Errorf("%s: operationId %q x-mcp-tool %q", op.Name, e.OperationID, e.MCPTool)
			}
			if _, ok := e.Responses["429"]; !ok {
				t.Errorf("%s: no 429 response (#0730's rate limit)", op.Name)
			}
			var names, wantNames []string
			for _, p := range e.Parameters {
				names = append(names, p.In+":"+p.Name)
			}
			for _, u := range op.URLParams() {
				in := "query"
				if u.InPath {
					in = "path"
				}
				wantNames = append(wantNames, in+":"+u.Name)
			}
			if !slices.Equal(names, wantNames) {
				t.Errorf("%s: parameters %v, want %v", op.Name, names, wantNames)
			}
			out, _ := json.Marshal(op.OutputSchema())
			var a, w any
			_ = json.Unmarshal(e.Responses["200"].Content["application/json"].Schema, &a)
			_ = json.Unmarshal(out, &w)
			ab, _ := json.Marshal(a)
			wb, _ := json.Marshal(w)
			if !bytes.Equal(ab, wb) {
				t.Errorf("%s: 200 schema %s, want %s", op.Name, ab, wb)
			}
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
