package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type urlIn struct {
	Slug  string `json:"slug" path:"slug"`
	Limit *int   `json:"limit,omitempty" query:"limit"`
}

// TestBindURLAndURLParams: URLParams lists a GET op's path and query
// fields in order, and BindURL binds them by /api/v1/'s rules.
func TestBindURLAndURLParams(t *testing.T) {
	op := Define(Operation{Name: "u", Method: http.MethodGet, Path: "/api/public/v1/u/{slug}"},
		func(context.Context, urlIn) (struct{}, error) { return struct{}{}, nil })
	ps := op.URLParams()
	want := []URLParam{{Name: "slug", JSONName: "slug", InPath: true}, {Name: "limit", JSONName: "limit"}}
	if len(ps) != len(want) || ps[0] != want[0] || ps[1] != want[1] {
		t.Fatalf("URLParams = %+v, want %+v", ps, want)
	}
	mux := http.NewServeMux()
	var gotIn any
	var gotErr *Error
	mux.HandleFunc("GET "+op.Path, func(_ http.ResponseWriter, r *http.Request) { gotIn, gotErr = BindURL(op, r) })
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/public/v1/u/x?limit=abc", nil))
	wantField(t, gotErr, "limit")
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/public/v1/u/x?limit=3", nil))
	if gotErr != nil {
		t.Fatalf("BindURL: %v", gotErr)
	}
	if in := gotIn.(urlIn); in.Slug != "x" || in.Limit == nil || *in.Limit != 3 {
		t.Fatalf("bound %+v", in)
	}
}
