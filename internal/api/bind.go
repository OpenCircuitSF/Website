package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// bind builds op's In from a REST request: the body (empty is {}), then the
// path and query parameters. It is the REST twin of Operation.Decode, and
// both go through decodeIn, so the two accept and reject the same bodies.
func bind(op Operation, body []byte, pathValue func(string) string, query url.Values) (any, *Error) {
	v, e := decodeIn(op, body, true)
	if e != nil {
		return nil, e
	}
	elem := v.Elem()
	bad := map[string]string{}
	for _, f := range op.fields {
		fv := elem.FieldByIndex(f.index)
		switch f.source {
		case fromPath:
			if msg := setURLValue(fv, pathValue(f.param)); msg != "" {
				bad[f.jsonName] = msg
			}
		case fromQuery:
			if query.Has(f.param) {
				if msg := setURLValue(fv, query.Get(f.param)); msg != "" {
					bad[f.jsonName] = msg
				}
			}
		}
	}
	if len(bad) > 0 {
		return nil, Validation(bad)
	}
	return elem.Interface(), nil
}

// decodeIn decodes raw into a new *In. With rest set, a path or query field
// present in the body is an error: it belongs in the URL.
func decodeIn(op Operation, raw []byte, rest bool) (reflect.Value, *Error) {
	v := reflect.New(op.in)
	if e := decodeObject(raw, v.Interface()); e != nil {
		return reflect.Value{}, e
	}
	if rest {
		bad := map[string]string{}
		for _, f := range op.fields {
			if f.source != fromBody && !v.Elem().FieldByIndex(f.index).IsZero() {
				bad[f.jsonName] = "belongs in the URL, not the body"
			}
		}
		if len(bad) > 0 {
			return reflect.Value{}, Validation(bad)
		}
	}
	return v, nil
}

// decodeObject decodes one JSON object from raw into ptr, rejecting unknown
// fields and trailing data. Empty raw is {}.
func decodeObject(raw []byte, ptr any) *Error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if raw[0] != '{' {
		return Validation(map[string]string{"body": "must be a JSON object"})
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(ptr); err != nil {
		e := decodeError(err)
		if _, bodyLevel := e.Fields["body"]; bodyLevel && json.Valid(raw) {
			// A field's own UnmarshalJSON failed (time.Time's, for one), and
			// encoding/json does not say which field: find it.
			if name := badField(raw, reflect.TypeOf(ptr).Elem()); name != "" {
				msg := "is not a valid value"
				var timeErr *time.ParseError
				if errors.As(err, &timeErr) {
					msg = "must be an RFC 3339 time with an offset"
				}
				return Validation(map[string]string{name: msg})
			}
		}
		return e
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Validation(map[string]string{"body": "must hold exactly one JSON object"})
	}
	return nil
}

// badField returns the json name of the first top-level field of struct t
// whose value in raw (a valid JSON object) does not decode on its own, or "".
func badField(raw []byte, t reflect.Type) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	for _, sf := range reflect.VisibleFields(t) {
		if sf.Anonymous || !sf.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		v, ok := obj[name]
		if !ok || name == "" {
			continue
		}
		if json.Unmarshal(v, reflect.New(sf.Type).Interface()) != nil {
			return name
		}
	}
	return ""
}

// decodeError maps an encoding/json error to a validation error naming the
// field when the error says which one.
func decodeError(err error) *Error {
	var typeErr *json.UnmarshalTypeError
	var timeErr *time.ParseError
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, `json: unknown field "`):
		name, uerr := strconv.Unquote(strings.TrimPrefix(msg, "json: unknown field "))
		if uerr != nil {
			name = "body"
		}
		return Validation(map[string]string{name: "unknown field"})
	case errors.As(err, &typeErr):
		field := typeErr.Field
		if field == "" {
			field = "body"
		}
		return Validation(map[string]string{field: "must be " + kindPhrase(typeErr.Type)})
	case errors.As(err, &timeErr):
		return Validation(map[string]string{"body": "has a time that is not RFC 3339"})
	}
	return Validation(map[string]string{"body": "is not valid JSON"})
}

// kindPhrase names the JSON shape a Go type decodes from.
func kindPhrase(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		return "an RFC 3339 time"
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "an array"
	case reflect.Struct, reflect.Map:
		return "an object"
	}
	return "a " + t.Kind().String()
}

// setURLValue converts s into fv (a field urlKindOK accepted), allocating a
// pointer. It returns "" on success and the validation message otherwise.
func setURLValue(fv reflect.Value, s string) string {
	if fv.Kind() == reflect.Pointer {
		p := reflect.New(fv.Type().Elem())
		if msg := setURLValue(p.Elem(), s); msg != "" {
			return msg
		}
		fv.Set(p)
		return ""
	}
	if fv.Type() == timeType {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return "must be an RFC 3339 time with an offset"
		}
		fv.Set(reflect.ValueOf(t))
		return ""
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(s)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || fv.OverflowInt(n) {
			return "must be a whole number"
		}
		fv.SetInt(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return "must be true or false"
		}
		fv.SetBool(b)
	}
	return ""
}
