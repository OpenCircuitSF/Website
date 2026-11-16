package api

import (
	"context"
	"net/http"
	"testing"
)

// TestCallContextPolicy: #0685's cancellation policy. A GET runs on the
// caller's context and is cancelled with it; every write runs detached and
// keeps the context's values.
func TestCallContextPolicy(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "v"))
	cancel()
	for _, m := range Methods {
		ctx := CallContext(parent, Operation{Method: m})
		if ctx.Value(key{}) != "v" {
			t.Errorf("%s: context values were dropped", m)
		}
		if got, want := ctx.Err() != nil, m == http.MethodGet; got != want {
			t.Errorf("%s: cancelled = %v, want %v", m, got, want)
		}
	}
}
