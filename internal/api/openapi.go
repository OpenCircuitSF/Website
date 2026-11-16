package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

const openAPIDescription = "The Open Circuit SF agent API. " +
	"Authenticate with `Authorization: Bearer ocsf_…`, a key an admin creates under Settings → API tokens; each operation names the scope it needs. " +
	"Times are RFC 3339 with an offset; the site's zone is America/Los_Angeles. " +
	"List operations take `limit` (default 50, maximum 200) and `cursor`, and return `{\"items\": [...], \"next_cursor\": ...}`; pass `next_cursor` back as `cursor` until it is null. " +
	"Every error is `{\"error\": {\"code\": \"...\", \"message\": \"...\", \"fields\": {...}}}` with a stable code: " +
	"unauthorized, forbidden, not_found, method_not_allowed, validation, conflict, rate_limited, unavailable or internal. " +
	"Write requests take application/json only, at most 64 KB, and unknown fields are a validation error. " +
	"Create operations accept an optional Idempotency-Key header (kept 24 hours per API key): a retry with the same key and body replays the first success."

// buildOpenAPI renders reg as an OpenAPI 3.1 document. It builds plain maps
// and marshals them indented, so the bytes are deterministic (map keys are
// sorted). OpenAPI 3.1 schemas are JSON Schema 2020-12, which is what
// jsonschema-go emits, so the schemas go in unchanged.
func buildOpenAPI(reg *Registry, baseURL string) ([]byte, error) {
	errSchema, err := jsonschema.ForType(reflect.TypeFor[ErrorBody](), schemaOptions())
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
			"security":    []any{},
			"responses": map[string]any{"200": map[string]any{"description": "the OpenAPI document",
				"content": map[string]any{"application/json": map[string]any{}}}},
		}},
	}
	for _, op := range reg.Operations() {
		var params []any
		urlProps := map[string]bool{}
		for _, f := range op.fields {
			if f.source == fromBody {
				continue
			}
			urlProps[f.jsonName] = true
			p := map[string]any{"name": f.param, "schema": op.inSchema.Properties[f.jsonName]}
			if f.source == fromPath {
				p["in"], p["required"] = "path", true
			} else {
				p["in"] = "query"
			}
			if s := op.inSchema.Properties[f.jsonName]; s != nil && s.Description != "" {
				p["description"] = s.Description
			}
			params = append(params, p)
		}
		if op.Idempotency {
			params = append(params, map[string]any{"name": "Idempotency-Key", "in": "header",
				"description": "optional; 1 to 255 printable ASCII characters. A retry with the same key and body replays the first success for 24 hours.",
				"schema":      map[string]any{"type": "string", "minLength": 1, "maxLength": 255}})
		}
		status := op.Status
		if status == 0 {
			status = http.StatusOK
		}
		responses := map[string]any{
			strconv.Itoa(status): map[string]any{"description": "success", "content": map[string]any{
				"application/json": map[string]any{"schema": op.outSchema}}},
			"400": errRef("validation: fields names each invalid field"),
			"401": errRef("unauthorized: a missing, unknown, revoked or expired API key"),
			"403": errRef("forbidden: the key lacks the required scope"),
			"429": errRef("rate_limited: too many requests for this key; see Retry-After"),
			"500": errRef("internal error"),
		}
		if strings.Contains(op.Path, "{") {
			responses["404"] = errRef("not_found")
		}
		if op.MayBeUnavailable {
			responses["503"] = errRef("unavailable: a service this operation depends on is down; retry later")
		}
		if op.Idempotency {
			responses["409"] = errRef("conflict: the Idempotency-Key was used with a different request, or is still in progress")
		}
		entry := map[string]any{
			"operationId":      op.Name,
			"summary":          op.Summary,
			"description":      op.Describe(),
			"x-required-scope": op.RequiredScope(),
			"security":         []any{map[string]any{"bearer": []any{}}},
			"responses":        responses,
		}
		if op.Deprecated {
			entry["deprecated"] = true
		}
		if len(params) > 0 {
			entry["parameters"] = params
		}
		if op.hasBody() {
			body := op.inSchema.CloneSchemas()
			for name := range urlProps {
				delete(body.Properties, name)
			}
			body.Required = slices.DeleteFunc(slices.Clone(body.Required), func(n string) bool { return urlProps[n] })
			entry["requestBody"] = map[string]any{"required": true, "content": map[string]any{
				"application/json": map[string]any{"schema": body}}}
			responses["413"] = errRef("validation: the body is over 64 KB")
			responses["415"] = errRef("validation: the body is not application/json")
		}
		p := openAPIPath(op.Path)
		item, _ := paths[p].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[p] = item
		}
		item[strings.ToLower(op.Method)] = entry
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Open Circuit SF API",
			"version":     reg.Version(),
			"description": openAPIDescription,
		},
		"servers": []any{map[string]any{"url": strings.TrimRight(baseURL, "/")}},
		"paths":   paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{"bearer": map[string]any{"type": "http", "scheme": "bearer"}},
			"schemas":         map[string]any{"Error": errSchema},
		},
	}
	return json.MarshalIndent(doc, "", "  ")
}

// openAPIPath converts a Go pattern path to an OpenAPI one. Go's {id} is
// already OpenAPI's; only a trailing {x...} wildcard needs its dots dropped.
func openAPIPath(p string) string {
	return pathParamRE.ReplaceAllString(p, "{$1}")
}
