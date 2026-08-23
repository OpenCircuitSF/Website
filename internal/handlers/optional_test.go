package handlers

import (
	"encoding/json"
	"testing"
)

// TestOptional_DistinguishesAbsentNullAndValue is the load-bearing check for
// #0146: the whole point of Optional[T] is that these three JSON bodies
// decode to three different states, which a plain *int cannot do (both
// "absent" and "null" collapse to a nil pointer there).
func TestOptional_DistinguishesAbsentNullAndValue(t *testing.T) {
	type req struct {
		Capacity Optional[int] `json:"capacity"`
	}

	cases := []struct {
		name        string
		body        string
		wantPresent bool
		wantValid   bool
		wantValue   int
	}{
		{"absent", `{}`, false, false, 0},
		{"null", `{"capacity": null}`, true, false, 0},
		{"value", `{"capacity": 42}`, true, true, 42},
		{"zero value", `{"capacity": 0}`, true, true, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r req
			if err := json.Unmarshal([]byte(tc.body), &r); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.body, err)
			}
			if r.Capacity.Present != tc.wantPresent {
				t.Errorf("Present = %v, want %v", r.Capacity.Present, tc.wantPresent)
			}
			if r.Capacity.Valid != tc.wantValid {
				t.Errorf("Valid = %v, want %v", r.Capacity.Valid, tc.wantValid)
			}
			if r.Capacity.Valid && r.Capacity.Value != tc.wantValue {
				t.Errorf("Value = %v, want %v", r.Capacity.Value, tc.wantValue)
			}
		})
	}
}

// TestOptional_RejectsUnknownFieldInStructType is the #0169 regression test:
// Optional[T].UnmarshalJSON must inherit decodeJSON's DisallowUnknownFields
// strictness, not just for the Optional[int] this codebase happens to use
// today, but for a struct T — the shape a future field is expected to reach
// for (#0146's own note: "the next nullable non-string column would need
// it"). Optional[int] alone can never exercise this: an int has no fields to
// be unknown, so the defect #0169 found is invisible until a struct is
// actually plugged in here.
func TestOptional_RejectsUnknownFieldInStructType(t *testing.T) {
	type inner struct {
		Name string `json:"name"`
	}
	type req struct {
		Detail Optional[inner] `json:"detail"`
	}

	// Known field only: decodes cleanly.
	var ok req
	if err := json.Unmarshal([]byte(`{"detail": {"name": "x"}}`), &ok); err != nil {
		t.Fatalf("Unmarshal(known field): %v", err)
	}
	if !ok.Detail.Present || !ok.Detail.Valid || ok.Detail.Value.Name != "x" {
		t.Fatalf("Detail = %+v, want Present/Valid with Name=x", ok.Detail)
	}

	// Unknown field inside the wrapped struct: must be rejected, matching
	// decodeJSON's behavior for every top-level request body (#0137).
	var bad req
	err := json.Unmarshal([]byte(`{"detail": {"name": "x", "extra": "surprise"}}`), &bad)
	if err == nil {
		t.Fatalf("Unmarshal(unknown inner field) succeeded, want rejection; decoded Detail = %+v", bad.Detail)
	}
}

// TestOptional_PointerFieldDoesNotWork documents, by test, the pitfall named
// in optional.go's doc comment: wrapping Optional[T] in an extra pointer
// (`*Optional[T]` as the struct field's type, rather than `Optional[T]`)
// reintroduces exactly the bug #0146 fixes, because encoding/json resolves a
// JSON `null` against a pointer-typed field by setting that pointer to nil
// directly, before ever asking whether the pointed-to type implements
// json.Unmarshaler. This was discovered by running exactly this check during
// implementation, not recalled from memory — kept here so a future edit that
// "simplifies" Optional's field placement back to a pointer fails loudly
// instead of silently reintroducing #0146.
func TestOptional_PointerFieldDoesNotWork(t *testing.T) {
	type req struct {
		Capacity *Optional[int] `json:"capacity"`
	}

	var absent, explicitNull req
	if err := json.Unmarshal([]byte(`{}`), &absent); err != nil {
		t.Fatalf("Unmarshal(absent): %v", err)
	}
	if err := json.Unmarshal([]byte(`{"capacity": null}`), &explicitNull); err != nil {
		t.Fatalf("Unmarshal(null): %v", err)
	}
	if absent.Capacity != nil {
		t.Fatalf("absent: Capacity = %+v, want nil", absent.Capacity)
	}
	if explicitNull.Capacity != nil {
		t.Fatalf("*Optional[T] field: explicit null decoded to Capacity = %+v, want nil "+
			"(this is the pitfall — null and absent are indistinguishable through a pointer "+
			"field, which is why optional.go requires a value field instead)", explicitNull.Capacity)
	}
}
