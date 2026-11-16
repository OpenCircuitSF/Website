package apitest

import (
	"context"
	"net/http"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

type in struct {
	Title string `json:"title"`
}

type out struct {
	Title string `json:"title"`
}

// TestRouterServesOpsBehindAuth: the helper's router runs the real path —
// 401 without a key, 403 without the scope, the op with it, and replay.
func TestRouterServesOpsBehindAuth(t *testing.T) {
	calls := 0
	op := api.Define(api.Operation{Name: "make_thing", Method: http.MethodPost, Path: "/api/v1/things",
		Scope: apitokens.ScopeGuidePostsWrite, Summary: "Make.", Idempotency: true, Status: http.StatusCreated},
		func(_ context.Context, i in) (out, error) { calls++; return out(i), nil })
	res := NewResolver()
	writer, _ := res.Key(t, "w", apitokens.ScopeGuidePostsWrite)
	reader, _ := res.Key(t, "r", apitokens.ScopeGuidePostsRead)
	h := Router(t, res, op)
	if rec := Do(t, h, "POST", "/api/v1/things", "", `{"title":"a"}`); rec.Code != 401 || ErrorOf(t, rec.Body.Bytes()).Code != api.CodeUnauthorized {
		t.Fatalf("no key: %d %s", rec.Code, rec.Body)
	}
	if rec := Do(t, h, "POST", "/api/v1/things", reader, `{"title":"a"}`); rec.Code != 403 {
		t.Fatalf("read key: %d", rec.Code)
	}
	for i := 0; i < 2; i++ {
		if rec := Do(t, h, "POST", "/api/v1/things", writer, `{"title":"a"}`, "Idempotency-Key", "k"); rec.Code != 201 {
			t.Fatalf("write key: %d %s", rec.Code, rec.Body)
		}
	}
	if calls != 1 {
		t.Fatalf("op ran %d times, want 1 (replay)", calls)
	}
	if rec := Do(t, h, "GET", "/api/v1/me", reader, ""); rec.Code != 200 {
		t.Fatalf("whoami: %d", rec.Code)
	}
}
