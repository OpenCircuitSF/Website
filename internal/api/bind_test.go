package api

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

type bindIn struct {
	ID     int64      `json:"id" path:"id"`
	Limit  *int       `json:"limit,omitempty" query:"limit"`
	After  *time.Time `json:"after,omitempty" query:"after"`
	Drafts bool       `json:"drafts,omitempty" query:"drafts"`
	Title  string     `json:"title"`
	Count  int        `json:"count,omitempty"`
	Tags   []string   `json:"tags,omitempty"`
}

func bindOp() Operation {
	return Define(Operation{Name: "b", Method: http.MethodPatch, Path: "/api/v1/b/{id}"}, okFn[bindIn])
}

func pathOf(id string) func(string) string {
	return func(string) string { return id }
}

func wantField(t *testing.T, e *Error, field string) {
	t.Helper()
	if e == nil {
		t.Fatalf("no error, want one naming %q", field)
	}
	if e.Code != CodeValidation || e.Fields[field] == "" {
		t.Fatalf("error %+v, want a validation error naming %q", e, field)
	}
}

func TestBindRejectsUnknownField(t *testing.T) {
	_, e := bind(bindOp(), []byte(`{"title":"x","titel":"y"}`), pathOf("1"), nil)
	wantField(t, e, "titel")
	if e.Fields["titel"] != "unknown field" {
		t.Fatalf("fields %v", e.Fields)
	}
}

func TestBindTypeErrorNamesField(t *testing.T) {
	_, e := bind(bindOp(), []byte(`{"count":"three"}`), pathOf("1"), nil)
	wantField(t, e, "count")
	if e.Fields["count"] != "must be a whole number" {
		t.Fatalf("fields %v", e.Fields)
	}
	_, e = bind(bindOp(), []byte(`{"tags":"x"}`), pathOf("1"), nil)
	wantField(t, e, "tags")
}

func TestBindRejectsTrailingData(t *testing.T) {
	for _, b := range []string{`{"title":"x"} {}`, `{"title":"x"}x`, `[1]`, `"x"`, `{"title":`} {
		_, e := bind(bindOp(), []byte(b), pathOf("1"), nil)
		wantField(t, e, "body")
	}
}

func TestBindPathAndQuery(t *testing.T) {
	q := url.Values{"limit": {"7"}, "after": {"2026-11-15T09:00:00-08:00"}, "drafts": {"true"}}
	v, e := bind(bindOp(), nil, pathOf("41"), q)
	if e != nil {
		t.Fatal(e)
	}
	in := v.(bindIn)
	if in.ID != 41 || in.Limit == nil || *in.Limit != 7 || !in.Drafts || in.After == nil ||
		!in.After.Equal(time.Date(2026, 11, 15, 17, 0, 0, 0, time.UTC)) {
		t.Fatalf("bound %+v", in)
	}
	for _, c := range []struct {
		id    string
		q     url.Values
		field string
	}{
		{"x", nil, "id"},
		{"1", url.Values{"limit": {"ten"}}, "limit"},
		{"1", url.Values{"after": {"2026-11-15"}}, "after"},
		{"1", url.Values{"drafts": {"maybe"}}, "drafts"},
	} {
		_, e := bind(bindOp(), nil, pathOf(c.id), c.q)
		wantField(t, e, c.field)
	}
	// An absent query parameter leaves its pointer nil.
	v, _ = bind(bindOp(), nil, pathOf("1"), url.Values{})
	if v.(bindIn).Limit != nil {
		t.Fatal("absent limit bound")
	}
}

func TestBindRejectsURLFieldInBody(t *testing.T) {
	_, e := bind(bindOp(), []byte(`{"id":5}`), pathOf("1"), nil)
	wantField(t, e, "id")
	_, e = bind(bindOp(), []byte(`{"limit":5}`), pathOf("1"), nil)
	wantField(t, e, "limit")
	if e.Fields["limit"] != "belongs in the URL, not the body" {
		t.Fatalf("fields %v", e.Fields)
	}
}

func TestEmptyBodyIsEmptyObject(t *testing.T) {
	for _, b := range []string{"", "  \n", "{}"} {
		if _, e := bind(bindOp(), []byte(b), pathOf("1"), nil); e != nil {
			t.Fatalf("%q: %v", b, e)
		}
	}
	if v, err := bindOp().Decode(nil); err == nil {
		t.Fatalf("Decode(nil) = %+v without the required id", v)
	}
}

// TestDecodeMatchesBindForBody: the MCP path (Decode) and the REST path
// (bind) accept and reject the same bodies.
func TestDecodeMatchesBindForBody(t *testing.T) {
	op := Define(Operation{Name: "c", Method: http.MethodPost, Path: "/api/v1/c"}, okFn[struct {
		Title string   `json:"title"`
		Count int      `json:"count,omitempty"`
		Tags  []string `json:"tags,omitempty"`
	}])
	for _, b := range []string{
		``, `{}`, `{"title":"x"}`, `{"title":"x","count":2,"tags":["a"]}`,
		`{"titel":"x"}`, `{"count":"2"}`, `{"tags":{}}`, `[]`, `{"title":"x"}{}`, `{`, `null`,
	} {
		_, be := bind(op, []byte(b), pathOf(""), nil)
		_, de := op.Decode([]byte(b))
		if (be == nil) != (de == nil) {
			t.Errorf("%q: bind %v, Decode %v", b, be, de)
			continue
		}
		if be != nil {
			got := de.(*Error)
			for k := range be.Fields {
				if got.Fields[k] != be.Fields[k] {
					t.Errorf("%q: bind fields %v, Decode fields %v", b, be.Fields, got.Fields)
				}
			}
		}
	}
	// Decode takes path fields from the object, as MCP sends them.
	v, err := bindOp().Decode([]byte(`{"id":9,"limit":3,"title":"t"}`))
	if err != nil {
		t.Fatal(err)
	}
	if in := v.(bindIn); in.ID != 9 || *in.Limit != 3 || in.Title != "t" {
		t.Fatalf("decoded %+v", in)
	}
}

// TestBindBadTimeNamesField: a body time that is not RFC 3339 names its
// field, though encoding/json's own error does not.
func TestBindBadTimeNamesField(t *testing.T) {
	type in struct {
		Title string     `json:"title"`
		At    time.Time  `json:"at"`
		Until *time.Time `json:"until,omitempty"`
		Items []struct {
			Starts time.Time `json:"starts"`
		} `json:"items,omitempty"`
	}
	op := Define(Operation{Name: "t", Method: http.MethodPost, Path: "/api/v1/t"}, okFn[in])
	for body, field := range map[string]string{
		`{"title":"x","at":"tomorrow"}`:                      "at",
		`{"at":"2026-11-15T09:00:00Z","until":"2026-11-15"}`: "until",
		`{"items":[{"starts":"noon"}]}`:                      "items",
	} {
		_, e := bind(op, []byte(body), pathOf(""), nil)
		wantField(t, e, field)
		if _, de := op.Decode([]byte(body)); de == nil || de.(*Error).Fields[field] == "" {
			t.Errorf("%s: Decode %v", body, de)
		}
	}
	if _, e := bind(op, []byte(`{"at":"2026-11-15T09:00:00-08:00"}`), pathOf(""), nil); e != nil {
		t.Fatal(e)
	}
}
