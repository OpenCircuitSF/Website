package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
)

// List page sizes.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Page is embedded in a list operation's In: ?limit= and ?cursor=.
type Page struct {
	Limit  *int   `json:"limit,omitempty" query:"limit" jsonschema:"1 to 200, default 50"`
	Cursor string `json:"cursor,omitempty" query:"cursor" jsonschema:"opaque; pass next_cursor from the previous page"`
}

// Resolve returns the page size: DefaultLimit when unset, a validation error
// naming limit outside 1..MaxLimit.
func (p Page) Resolve() (int, error) {
	if p.Limit == nil {
		return DefaultLimit, nil
	}
	if *p.Limit < 1 || *p.Limit > MaxLimit {
		return 0, Validation(map[string]string{"limit": "must be 1 to 200"})
	}
	return *p.Limit, nil
}

// List is every list operation's Out: {"items":[...],"next_cursor":...}.
// NextCursor is null on the last page.
type List[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// NewList returns a List, turning nil items into [] so the JSON is never
// "items": null.
func NewList[T any](items []T, next *string) List[T] {
	if items == nil {
		items = []T{}
	}
	return List[T]{Items: items, NextCursor: next}
}

// EncodeCursor returns v as an opaque cursor: unpadded base64url of its JSON.
// A list operation encodes its last row's sort key (keyset, not offset), such
// as {"t":"...","id":41}. It panics if v cannot be marshalled, a wiring bug.
func EncodeCursor(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("api: EncodeCursor: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor reverses EncodeCursor into v. Anything this API did not issue
// is a validation error naming cursor.
func DecodeCursor(s string, v any) error {
	bad := Validation(map[string]string{"cursor": "is not a cursor this API issued"})
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return bad
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return bad
	}
	return nil
}
