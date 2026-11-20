package publicapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// OpenAPIDrift compares a served OpenAPI document with reg: routes,
// operationIds, x-mcp-tool names, parameters, 200 schemas, a 429 on every
// op, and no auth. It returns one line per difference. It exists for tests
// in this package and in internal/handlers (#0737).
func OpenAPIDrift(b []byte, reg *Registry) []string {
	var drift []string
	if bytes.Contains(b, []byte("securitySchemes")) || bytes.Contains(b, []byte(`"401"`)) || bytes.Contains(b, []byte(`"403"`)) {
		drift = append(drift, "document mentions auth")
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
		return append(drift, fmt.Sprintf("decode: %v", err))
	}
	if doc.OpenAPI != "3.1.0" || len(doc.Servers) != 1 {
		drift = append(drift, fmt.Sprintf("header: %s %+v", doc.OpenAPI, doc.Servers))
	}
	want := []string{"GET " + OpenAPIPath}
	for _, op := range reg.Ops() {
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
		return append(drift, fmt.Sprintf("routes %v, want %v", got, want))
	}
	for _, op := range reg.Ops() {
		e := doc.Paths[op.Path]["get"]
		if e.OperationID != op.Name || e.MCPTool != op.Name {
			drift = append(drift, fmt.Sprintf("%s: operationId %q x-mcp-tool %q", op.Name, e.OperationID, e.MCPTool))
		}
		if _, ok := e.Responses["429"]; !ok {
			// #0730: every op is rate limited, so every op documents a 429.
			drift = append(drift, fmt.Sprintf("%s: no 429 response", op.Name))
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
			drift = append(drift, fmt.Sprintf("%s: parameters %v, want %v", op.Name, names, wantNames))
		}
		out, _ := json.Marshal(op.OutputSchema())
		var a, w any
		_ = json.Unmarshal(e.Responses["200"].Content["application/json"].Schema, &a)
		_ = json.Unmarshal(out, &w)
		ab, _ := json.Marshal(a)
		wb, _ := json.Marshal(w)
		if !bytes.Equal(ab, wb) {
			drift = append(drift, fmt.Sprintf("%s: 200 schema %s, want %s", op.Name, ab, wb))
		}
	}
	return drift
}
