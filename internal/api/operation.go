package api

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// Operation is one v1 API call. Fill the exported fields and pass it to
// Define with the function that runs it; Define fills the rest. The REST
// route, the OpenAPI entry and the MCP tool (#0679) all come from this one
// value.
type Operation struct {
	Name         string // snake_case, ^[a-z][a-z0-9_]{0,63}$: the OpenAPI operationId and the MCP tool name
	Method       string // http.MethodGet, Post, Put, Patch or Delete
	Path         string // a Go 1.22 pattern path under /api/v1/, e.g. "/api/v1/guide-posts/{id}"
	Scope        string // an apitokens scope; "" only with AnyToken
	AnyToken     bool   // any valid key may call it (whoami); makes "no scope" explicit
	Summary      string // one line: the OpenAPI summary and the start of every description
	Description  string // optional detail
	Destructive  bool   // deletes or irreversibly changes data
	PublicEffect string // "" or a short phrase, e.g. "posts publicly to Bluesky or Mastodon right away"
	Idempotency  bool   // accepts an Idempotency-Key header (create endpoints)
	Status       int    // the success status; 0 means 200. Creates use 201
	MCPExcluded  string // "" = exposed as an MCP tool; otherwise the reason it is not
	Deprecated   bool   // marked deprecated in the OpenAPI document
	// MayBeUnavailable: the operation can return Unavailable (a 503) when a
	// service it depends on is down; the OpenAPI document lists the 503.
	MayBeUnavailable bool

	// Set by Define.
	in, out             reflect.Type
	inSchema, outSchema *jsonschema.Schema
	fields              []inField
	call                func(ctx context.Context, in any) (any, error)
}

// fieldSource is where an In field is bound from.
type fieldSource int

const (
	fromBody fieldSource = iota
	fromPath
	fromQuery
)

// inField is one exported field of an operation's In struct.
type inField struct {
	index    []int // reflect.Value.FieldByIndex
	jsonName string
	source   fieldSource
	param    string // the path or query parameter name
	typ      reflect.Type
}

var opNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// pathParamRE finds {name} and {name...} in a pattern path.
var pathParamRE = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(\.\.\.)?\}`)

// timeType is time.Time, which the schema and the URL binder treat as an
// RFC 3339 string.
var timeType = reflect.TypeFor[time.Time]()

// schemaOptions is the one set of jsonschema-go options: time.Time is a
// date-time string. #0679's MCP server uses the same library and version, so
// the two cannot disagree about a schema.
func schemaOptions() *jsonschema.ForOptions {
	return &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		timeType: {Type: "string", Format: "date-time"},
	}}
}

// Define returns op bound to fn. In and Out must be structs. In's fields
// carry a json tag each (MCP sends the whole In as one object) and, for URL
// parameters, a path:"x" or query:"x" tag; a field with neither is a body
// field. Define panics on a wiring bug: a non-struct In or Out, a field
// without a json tag, a body field on a GET or DELETE, a path tag that does
// not match a {x} in Path or the reverse, an unsupported URL field type, or a
// type jsonschema-go cannot describe.
func Define[In, Out any](op Operation, fn func(ctx context.Context, in In) (Out, error)) Operation {
	inT, outT := reflect.TypeFor[In](), reflect.TypeFor[Out]()
	if inT.Kind() != reflect.Struct {
		panic(fmt.Sprintf("api: %s: In must be a struct, not %s", op.Name, inT))
	}
	if outT.Kind() != reflect.Struct {
		panic(fmt.Sprintf("api: %s: Out must be a struct, not %s (an MCP output schema must be an object)", op.Name, outT))
	}
	op.in, op.out = inT, outT
	op.fields = inFields(op.Name, inT)

	var pathTags []string
	for _, f := range op.fields {
		switch f.source {
		case fromBody:
			if op.Method == http.MethodGet || op.Method == http.MethodDelete {
				panic(fmt.Sprintf("api: %s: %s %s has body field %q; give it a path or query tag", op.Name, op.Method, op.Path, f.jsonName))
			}
		case fromPath:
			pathTags = append(pathTags, f.param)
		}
	}
	var inPath []string
	for _, m := range pathParamRE.FindAllStringSubmatch(op.Path, -1) {
		inPath = append(inPath, m[1])
	}
	slices.Sort(pathTags)
	slices.Sort(inPath)
	if !slices.Equal(pathTags, inPath) {
		panic(fmt.Sprintf("api: %s: path tags %v do not match the parameters %v in %s", op.Name, pathTags, inPath, op.Path))
	}

	var err error
	if op.inSchema, err = jsonschema.ForType(inT, schemaOptions()); err != nil {
		panic(fmt.Sprintf("api: %s: input schema: %v", op.Name, err))
	}
	if op.outSchema, err = jsonschema.ForType(outT, schemaOptions()); err != nil {
		panic(fmt.Sprintf("api: %s: output schema: %v", op.Name, err))
	}
	op.call = func(ctx context.Context, in any) (any, error) {
		v, ok := in.(In)
		if !ok {
			return nil, fmt.Errorf("api: %s: called with %T, want %s", op.Name, in, inT)
		}
		return fn(ctx, v)
	}
	return op
}

// inFields lists t's exported fields, flattening embedded structs (such as
// Page) the way encoding/json and jsonschema-go do.
func inFields(name string, t reflect.Type) []inField {
	var out []inField
	for _, sf := range reflect.VisibleFields(t) {
		if sf.Anonymous {
			if sf.Type.Kind() == reflect.Pointer {
				panic(fmt.Sprintf("api: %s: embedded pointer %s is not supported in an In struct", name, sf.Type))
			}
			continue
		}
		if !sf.IsExported() {
			continue
		}
		tag := sf.Tag.Get("json")
		jsonName, _, _ := strings.Cut(tag, ",")
		if jsonName == "" || jsonName == "-" {
			panic(fmt.Sprintf("api: %s: field %s needs a json tag", name, sf.Name))
		}
		f := inField{index: sf.Index, jsonName: jsonName, typ: sf.Type}
		p, hasPath := sf.Tag.Lookup("path")
		q, hasQuery := sf.Tag.Lookup("query")
		switch {
		case hasPath && hasQuery:
			panic(fmt.Sprintf("api: %s: field %s has both path and query tags", name, sf.Name))
		case hasPath:
			f.source, f.param = fromPath, p
			if strings.Contains(tag, "omitempty") {
				panic(fmt.Sprintf("api: %s: path field %s is required, so its json tag must not be omitempty", name, sf.Name))
			}
		case hasQuery:
			f.source, f.param = fromQuery, q
		}
		if f.source != fromBody {
			if f.param == "" {
				panic(fmt.Sprintf("api: %s: field %s has an empty parameter name", name, sf.Name))
			}
			if !urlKindOK(sf.Type) {
				panic(fmt.Sprintf("api: %s: URL field %s has unsupported type %s", name, sf.Name, sf.Type))
			}
		}
		out = append(out, f)
	}
	return out
}

// urlKindOK reports whether a path or query field's type is one the binder
// converts: string, int, int64, bool, time.Time, or a pointer to one.
func urlKindOK(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Int, reflect.Int64, reflect.Bool:
		return true
	}
	return false
}

// hasBody reports whether op takes a request body.
func (op Operation) hasBody() bool {
	for _, f := range op.fields {
		if f.source == fromBody {
			return true
		}
	}
	return false
}

// InputSchema is the JSON schema of the whole In, path and query fields
// included: what an MCP client sends as a tool's arguments.
func (op Operation) InputSchema() *jsonschema.Schema { return op.inSchema.CloneSchemas() }

// OutputSchema is the JSON schema of Out.
func (op Operation) OutputSchema() *jsonschema.Schema { return op.outSchema.CloneSchemas() }

// RequiredScope is the scope a key needs: op.Scope, or "any" for AnyToken.
func (op Operation) RequiredScope() string {
	if op.AnyToken {
		return "any"
	}
	return op.Scope
}

// Describe is the one description OpenAPI and MCP both use. A warning comes
// first, so a client that asks before acting can ask: "DESTRUCTIVE: " when
// Destructive, then "PUBLIC: <PublicEffect>. " when that is set. Then the
// summary, the description, and the scope the call needs.
func (op Operation) Describe() string {
	var b strings.Builder
	if op.Destructive {
		b.WriteString("DESTRUCTIVE: ")
	}
	if op.PublicEffect != "" {
		b.WriteString("PUBLIC: " + strings.TrimSuffix(op.PublicEffect, ".") + ". ")
	}
	b.WriteString(op.Summary)
	if op.Description != "" {
		b.WriteString(" " + op.Description)
	}
	if op.AnyToken {
		b.WriteString(" Any valid API key may call it.")
	} else {
		b.WriteString(" Requires the " + op.Scope + " scope.")
	}
	return b.String()
}

// Decode decodes a whole-object argument (the MCP path) into op's In, by the
// same rules as a REST body: unknown fields and type mismatches are
// validation errors naming the field. Path fields are required. Empty raw is
// {}. The error, when not nil, is an *Error.
func (op Operation) Decode(raw []byte) (any, error) {
	v, e := decodeIn(op, raw, false)
	if e != nil {
		return nil, e
	}
	missing := map[string]string{}
	for _, f := range op.fields {
		if f.source == fromPath && v.Elem().FieldByIndex(f.index).IsZero() {
			missing[f.jsonName] = "is required"
		}
	}
	if len(missing) > 0 {
		return nil, Validation(missing)
	}
	return v.Elem().Interface(), nil
}

// Call runs the operation on in, a value Decode or the REST binder returned.
// The error is the operation's own; pass it through AsError before showing
// it to anyone.
func (op Operation) Call(ctx context.Context, in any) (any, error) {
	return op.call(ctx, in)
}
