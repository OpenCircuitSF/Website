package mcptest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

type thingIn struct {
	Title string `json:"title"`
}

type thingOut struct {
	Title string `json:"title"`
}

// TestHelpersDriveTheRealPath: Handler, Connect, Call, Text and ErrorOf
// together, as #0676–#0678's tool tests use them.
func TestHelpersDriveTheRealPath(t *testing.T) {
	op := api.Define(api.Operation{Name: "make_thing", Method: http.MethodPost, Path: "/api/v1/things",
		Scope: apitokens.ScopeGuidePostsWrite, Summary: "Make.", Status: http.StatusCreated},
		func(_ context.Context, i thingIn) (thingOut, error) {
			if i.Title == "" {
				return thingOut{}, api.Validation(map[string]string{"title": "is required"})
			}
			return thingOut(i), nil
		})
	res := apitest.NewResolver()
	writer, _ := res.Key(t, "w", apitokens.ScopeGuidePostsWrite)
	h := Handler(t, res, op)
	cs := Connect(t, h, writer)

	got := Call(t, cs, "make_thing", map[string]any{"title": "a"})
	var o thingOut
	if err := json.Unmarshal([]byte(Text(got)), &o); err != nil || o.Title != "a" {
		t.Fatalf("make_thing: %s (%v)", Text(got), err)
	}
	if e := ErrorOf(t, Call(t, cs, "make_thing", nil)); e.Code != api.CodeValidation || e.Fields["title"] == "" {
		t.Fatalf("empty title: %+v", e)
	}
	if _, err := Dial(Serve(t, h), &Transport{Bearer: "ocsf_wrong"}); err == nil {
		t.Fatal("connected with a bad key")
	}
}
