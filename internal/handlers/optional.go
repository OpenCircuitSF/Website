// Optional[T] — a JSON field that can be absent, explicitly null, or set
// (#0146). See this file's package doc comment on why patchWorkshopRequest
// needed it and patchCampaignRequest did not.
package handlers

import "encoding/json"

// Optional wraps a PATCH request field whose wire representation must
// distinguish three states: the key absent from the JSON object ("leave
// alone"), the key present as the literal `null` ("clear it"), and the key
// present with a value ("set it"). A plain `*T` can only tell "value" apart
// from "everything else" — encoding/json decodes both an absent key and an
// explicit `null` to the same nil pointer, which is exactly the defect this
// type exists to fix (see issues/0146.md: a cleared workshop capacity came
// back after reload, because the PATCH merge's `if req.Capacity != nil`
// guard could never see the difference).
//
// #0139 fixed the same three-state problem for every *string* field by
// having the client always send the key, using an explicit "" to mean
// "clear" — that works because "" is already a valid sentinel distinct from
// real content for a trimmed string. capacity has no such spare value: 0
// is arguably a valid capacity in the abstract (though this codebase's own
// validation rejects it — see workshopAdmin.ts's `Capacity must be a
// positive whole number`), and every other integer is a real value someone
// might type. A field-specific sentinel would work but would need
// documenting and re-deriving for the next field of this shape; Optional[T]
// is the general fix instead. This is the second field in this codebase to
// need the three-state distinction (capacity, after every optional string in
// #0139), which is enough repetition to justify the small generic type over
// another one-off.
//
// Embed a VALUE (not a pointer) of this type directly in a request struct:
//
//	type patchFooRequest struct {
//	    Capacity Optional[int] `json:"capacity"`
//	}
//
// That non-pointer placement is required, not stylistic. A `*Optional[T]`
// field does NOT work: encoding/json special-cases a JSON `null` for any
// field whose own Go type is itself a pointer by setting that pointer to nil
// directly, without ever consulting whatever the pointed-to type implements
// — so an explicit `null` and an absent key would both leave the field nil,
// reproducing the exact bug this type exists to fix. A plain, addressable
// struct field sidesteps that: encoding/json takes its address and always
// calls UnmarshalJSON when (and only when) the key is present, `null` or
// not. Confirmed by test (optional_test.go), per this issue's own
// instruction to check encoding/json's behavior rather than assume it — the
// first draft of this type used the pointer form and silently failed to
// tell "null" apart from "absent" (verified with `go run`, not shipped).
//
// Typical merge in a PATCH handler, current holding the row's existing
// value:
//
//	capacity := current.Capacity
//	switch {
//	case !req.Capacity.Present:
//	    // key absent from the PATCH body: leave capacity alone.
//	case !req.Capacity.Valid:
//	    capacity = nil // key present as `null`: explicit clear.
//	default:
//	    v := req.Capacity.Value
//	    capacity = &v // key present with a value.
//	}
type Optional[T any] struct {
	// Value holds the decoded value when Valid is true. Zero otherwise.
	Value T
	// Present is true iff the field's JSON key appeared in the source
	// object at all (with any value, including `null`). False means the
	// zero Optional[T] this field started as was never touched by decode
	// — i.e. the key was absent, and both Value and Valid are meaningless.
	Present bool
	// Valid is true iff the key was present with a value other than the
	// JSON literal `null`. Present && !Valid means "present, and that
	// value was null" — the explicit-clear state.
	Valid bool
}

// UnmarshalJSON implements json.Unmarshaler. encoding/json only invokes this
// method for a key that is actually present in the source JSON object — an
// absent key never calls it at all, leaving the field at its Go zero value
// (Present: false), which is exactly "absent" here. A `null` value marks the
// field Present but not Valid, leaving Value at T's zero value. Any other
// JSON value decodes into Value with encoding/json's normal rules and marks
// the field both Present and Valid.
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.Present = true
	if string(data) == "null" {
		o.Valid = false
		var zero T
		o.Value = zero
		return nil
	}
	if err := json.Unmarshal(data, &o.Value); err != nil {
		return err
	}
	o.Valid = true
	return nil
}
