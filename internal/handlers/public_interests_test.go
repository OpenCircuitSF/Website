package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
)

func TestPublicInterestsHandler_List_OnlyActiveAndNarrowShape(t *testing.T) {
	pool := journeyTestPool(t)
	ctx := context.Background()
	store := interests.NewStore(pool)

	// A freshly-created, then deactivated interest must never appear —
	// this is the exact defect #0024's review carried into #0029: a public
	// form built from ListAll would show a checkbox for something #0026
	// would then reject with a 400.
	slug := testInterestSlug(t, pool)
	created, err := store.Create(ctx, slug, "Should never be public", nil, 500)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Deactivate(ctx, created.ID); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	h := NewPublicInterestsHandler(store)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/interests", h.List)

	req := httptest.NewRequest(http.MethodGet, "/api/interests", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	var body struct {
		Interests []map[string]any `json:"interests"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rr.Body.String())
	}
	if len(body.Interests) < 12 {
		t.Fatalf("got %d interests, want at least the 12 seeded", len(body.Interests))
	}

	sawExpectedSlug := false
	for _, it := range body.Interests {
		if it["slug"] == slug {
			t.Errorf("deactivated interest %q present in public list", slug)
		}
		if it["slug"] == "homelab" {
			sawExpectedSlug = true
		}
		// Narrow shape: id, active, and subscriber_count are admin-only
		// fields (interestView, internal/handlers/interests.go) and must
		// never leak here.
		for _, forbidden := range []string{"id", "active", "subscriber_count"} {
			if _, present := it[forbidden]; present {
				t.Errorf("public interest view carries admin-only field %q: %v", forbidden, it)
			}
		}
		for _, required := range []string{"slug", "name", "sort_order"} {
			if _, present := it[required]; !present {
				t.Errorf("public interest view missing field %q: %v", required, it)
			}
		}
	}
	if !sawExpectedSlug {
		t.Error("seeded slug \"homelab\" missing from the public list")
	}
}
