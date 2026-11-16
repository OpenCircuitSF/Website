package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

func fetchOpenAPI(t *testing.T, h http.Handler) (map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, OpenAPIPath, nil))
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("openapi.json: %v (%d %s)", err, rec.Code, rec.Body)
	}
	return doc, rec
}

func TestOpenAPIIsPublic(t *testing.T) {
	e := newEnv(t, nil)
	_, rec := fetchOpenAPI(t, e.h)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("no token: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("Cache-Control %q", rec.Header().Get("Cache-Control"))
	}
}

// testRegistry is newEnv's operation set, for document-level checks.
func testRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry(Config{Version: "1.2.3", Idempotency: &memIdem{}})
	type upd struct {
		ID    int64  `json:"id" path:"id"`
		Title string `json:"title"`
		Note  string `json:"note,omitempty"`
	}
	reg.Add(CoreOperations("1.2.3")...)
	reg.Add(
		Define(Operation{Name: "test_list", Method: http.MethodGet, Path: "/api/v1/items", Scope: apitokens.ScopeGuidePostsRead, Summary: "List."}, okFn[listIn]),
		Define(Operation{Name: "test_create", Method: http.MethodPost, Path: "/api/v1/items", Scope: apitokens.ScopeGuidePostsWrite, Summary: "Create.", Idempotency: true, Status: 201, MayBeUnavailable: true}, okFn[createIn]),
		Define(Operation{Name: "test_update", Method: http.MethodPatch, Path: "/api/v1/items/{id}", Scope: apitokens.ScopeGuidePostsWrite, Summary: "Update."}, okFn[upd]),
	)
	return reg
}

func TestOpenAPICoversEveryOperation(t *testing.T) {
	reg := testRegistry(t)
	h := NewHTTPRouter(reg, RouterDeps{Resolver: fakeResolver{}, Limiter: middleware.NewAPITokenRateLimiter()})
	doc, _ := fetchOpenAPI(t, h)
	paths := doc["paths"].(map[string]any)
	seen := map[string]bool{}
	for _, op := range reg.Operations() {
		item, _ := paths[openAPIPath(op.Path)].(map[string]any)
		entry, _ := item[strings.ToLower(op.Method)].(map[string]any)
		if entry == nil {
			t.Errorf("%s %s missing", op.Method, op.Path)
			continue
		}
		if entry["operationId"] != op.Name || entry["x-required-scope"] != op.RequiredScope() {
			t.Errorf("%s: operationId %v scope %v", op.Name, entry["operationId"], entry["x-required-scope"])
		}
		seen[strings.ToLower(op.Method)+" "+openAPIPath(op.Path)] = true
	}
	for p, item := range paths {
		for m := range item.(map[string]any) {
			if p == OpenAPIPath {
				continue
			}
			if !seen[m+" "+p] {
				t.Errorf("document has %s %s with no operation", m, p)
			}
		}
	}
}

func TestOpenAPIShape(t *testing.T) {
	reg := testRegistry(t)
	raw, err := buildOpenAPI(reg, "https://example.test/")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("openapi %v", doc["openapi"])
	}
	comp := doc["components"].(map[string]any)
	if b := comp["securitySchemes"].(map[string]any)["bearer"].(map[string]any); b["type"] != "http" || b["scheme"] != "bearer" {
		t.Fatalf("bearer %v", b)
	}
	if comp["schemas"].(map[string]any)["Error"] == nil {
		t.Fatal("no Error schema")
	}
	if doc["servers"].([]any)[0].(map[string]any)["url"] != "https://example.test" {
		t.Fatalf("servers %v", doc["servers"])
	}
	paths := doc["paths"].(map[string]any)
	op := func(p, m string) map[string]any { return paths[p].(map[string]any)[m].(map[string]any) }

	upd := op("/api/v1/items/{id}", "patch")
	params := upd["parameters"].([]any)
	if len(params) != 1 || params[0].(map[string]any)["in"] != "path" || params[0].(map[string]any)["required"] != true {
		t.Fatalf("update params %v", params)
	}
	body := upd["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	props := body["properties"].(map[string]any)
	if props["id"] != nil || props["title"] == nil {
		t.Fatalf("update body props %v", props)
	}
	for _, r := range body["required"].([]any) {
		if r == "id" {
			t.Fatal("body still requires the path field")
		}
	}
	if upd["responses"].(map[string]any)["404"] == nil || upd["responses"].(map[string]any)["413"] == nil {
		t.Fatal("update lacks 404 or 413")
	}

	list := op("/api/v1/items", "get")
	if list["requestBody"] != nil {
		t.Fatal("GET has a requestBody")
	}
	var qnames []string
	for _, p := range list["parameters"].([]any) {
		qnames = append(qnames, p.(map[string]any)["name"].(string))
	}
	if strings.Join(qnames, ",") != "limit,cursor" {
		t.Fatalf("list params %v", qnames)
	}

	create := op("/api/v1/items", "post")
	var idem bool
	for _, p := range create["parameters"].([]any) {
		idem = idem || p.(map[string]any)["name"] == "Idempotency-Key"
	}
	resps := create["responses"].(map[string]any)
	if !idem || resps["201"] == nil || resps["409"] == nil || resps["503"] == nil {
		t.Fatalf("create: idem %v responses %v", idem, resps)
	}
	if list["responses"].(map[string]any)["503"] != nil {
		t.Fatal("503 on an op that does not declare it")
	}
	if op("/api/v1/token", "get")["deprecated"] != true {
		t.Fatal("token_probe not deprecated")
	}
	if s := op(OpenAPIPath, "get")["security"].([]any); len(s) != 0 {
		t.Fatalf("openapi.json security %v", s)
	}

	again, err := buildOpenAPI(testRegistry(t), "https://example.test/")
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("two builds differ")
	}
}
