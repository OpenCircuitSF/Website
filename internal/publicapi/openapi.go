package publicapi

import (
	"encoding/json"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

const openAPIDescription = "Read-only public data about Open Circuit SF's events and newsletter issues. " +
	"No key or account; an Authorization header is ignored. " +
	"Times are RFC 3339 with an offset in America/Los_Angeles. " +
	"limit defaults to 10 and is capped at 50; query is cut to 200 characters. " +
	`Every error is {"error":{"code","message","fields"}}. ` +
	"The same operations are MCP tools at https://www.opencircuitsf.com/mcp."

// buildOpenAPI renders reg as an OpenAPI 3.1 document from plain maps,
// marshalled indented so the bytes are deterministic. There is no security
// scheme and no 401 or 403: nothing is authenticated.
func buildOpenAPI(reg *Registry, baseURL string) ([]byte, error) {
	errSchema, err := api.ErrorSchema()
	if err != nil {
		return nil, err
	}
	errRef := func(desc string) map[string]any {
		return map[string]any{"description": desc, "content": map[string]any{
			"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Error"}}}}
	}
	paths := map[string]any{
		OpenAPIPath: map[string]any{"get": map[string]any{
			"operationId": "openapi",
			"summary":     "This document.",
			"responses": map[string]any{"200": map[string]any{"description": "the OpenAPI document",
				"content": map[string]any{"application/json": map[string]any{}}}},
		}},
	}
	for _, op := range reg.Ops() {
		in := op.InputSchema()
		var params []any
		for _, u := range op.URLParams() {
			p := map[string]any{"name": u.Name, "schema": in.Properties[u.JSONName]}
			if u.InPath {
				p["in"], p["required"] = "path", true
			} else {
				p["in"] = "query"
			}
			if s := in.Properties[u.JSONName]; s != nil && s.Description != "" {
				p["description"] = s.Description
			}
			params = append(params, p)
		}
		responses := map[string]any{
			"200": map[string]any{"description": "success", "content": map[string]any{
				"application/json": map[string]any{"schema": op.OutputSchema()}}},
			"400": errRef("validation: fields names each invalid field"),
			"500": errRef("internal error"),
		}
		if strings.Contains(op.Path, "{") {
			responses["404"] = errRef("not_found")
		}
		entry := map[string]any{
			"operationId": op.Name,
			"x-mcp-tool":  op.Name,
			"summary":     op.Summary,
			"description": op.Describe(),
			"responses":   responses,
		}
		if len(params) > 0 {
			entry["parameters"] = params
		}
		paths[op.Path] = map[string]any{"get": entry}
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Open Circuit SF public API",
			"version":     reg.Version(),
			"description": openAPIDescription,
		},
		"servers":    []any{map[string]any{"url": strings.TrimRight(baseURL, "/")}},
		"security":   []any{},
		"paths":      paths,
		"components": map[string]any{"schemas": map[string]any{"Error": errSchema}},
	}
	return json.MarshalIndent(doc, "", "  ")
}
