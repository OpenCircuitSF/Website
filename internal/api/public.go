package api

import (
	"net/http"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// Defined reports whether op was built with Define. The public registry
// (internal/publicapi, #0729) refuses an operation that was not, since it
// has no call function, schemas or bound fields.
func (op Operation) Defined() bool { return op.call != nil }

// BindURL is the REST binder for a GET operation: it binds op's input from
// r's path values and query string by exactly the rules /api/v1/ uses. The
// public REST adapter (internal/publicapi, #0734) calls it.
func BindURL(op Operation, r *http.Request) (any, *Error) {
	return bind(op, nil, r.PathValue, r.URL.Query())
}

// URLParam is one input field bound from the URL: Name is the path or
// query parameter, JSONName the input schema property, InPath whether it is
// a path segment.
type URLParam struct {
	Name, JSONName string
	InPath         bool
}

// URLParams lists op's input fields bound from the URL, in field order.
func (op Operation) URLParams() []URLParam {
	var ps []URLParam
	for _, f := range op.fields {
		if f.source == fromBody {
			continue
		}
		ps = append(ps, URLParam{Name: f.param, JSONName: f.jsonName, InPath: f.source == fromPath})
	}
	return ps
}

// ErrorSchema is the JSON Schema of ErrorBody, built with the same options
// as every operation schema.
func ErrorSchema() (*jsonschema.Schema, error) {
	return jsonschema.ForType(reflect.TypeFor[ErrorBody](), schemaOptions())
}
