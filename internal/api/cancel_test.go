package api

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestCallContextPolicy: #0685's cancellation policy. A GET runs on the
// caller's context and is cancelled with it; every write runs detached and
// keeps the context's values.
func TestCallContextPolicy(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "v"))
	cancel()
	if WriteTimeout >= IdempotencyInFlightTimeout {
		t.Errorf("WriteTimeout %v must be under IdempotencyInFlightTimeout %v, or a running write's key can be taken over", WriteTimeout, IdempotencyInFlightTimeout)
	}
	for _, m := range Methods {
		ctx, done := CallContext(parent, Operation{Method: m})
		if ctx.Value(key{}) != "v" {
			t.Errorf("%s: context values were dropped", m)
		}
		if got, want := ctx.Err() != nil, m == http.MethodGet; got != want {
			t.Errorf("%s: cancelled = %v, want %v", m, got, want)
		}
		// A detached write is still bounded, below the in-flight takeover.
		if dl, ok := ctx.Deadline(); m != http.MethodGet && (!ok || time.Until(dl) > WriteTimeout) {
			t.Errorf("%s: deadline %v, %v; want within WriteTimeout", m, dl, ok)
		}
		done()
	}
}
