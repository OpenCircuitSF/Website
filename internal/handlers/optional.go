// Optional[T] — a JSON field that can be absent, explicitly null, or set
// (#0146). See this file's package doc comment on why patchWorkshopRequest
// needed it and patchCampaignRequest did not.
package handlers

import (
	"bytes"
	"encoding/json"
)

// Request bodies only. Optional[T] has no MarshalJSON, so embedding it in a
// RESPONSE struct would serialize as its raw field layout
// (`{"Value":…,"Present":…,"Valid":…}`) instead of the plain JSON value a
// client expects — nothing currently does this (workshopView keeps a plain
// *int for Capacity; see admin_workshops.go's doc comment on that field),
// but if a future response shape reaches for this type, add MarshalJSON
// first (#0155, a ride-along nit from #0146's review).
//
// Inherits decodeJSON's strictness (#0169). internal/handlers has exactly one
// decode contract (auth.go's decodeJSON, DisallowUnknownFields on every
// request body — established by #0137's review) and Optional[T] is a request
// body field, so its inner value is decoded the same way: an unknown key in
// T is rejected, not silently dropped. This matters once T is a struct —
// today the only instantiation is Optional[int], where "unknown field"
// cannot occur, but a generic invites Optional[SomeStruct] and #0146 says
// that is expected, not hypothetical. See UnmarshalJSON's doc comment for
// the measured cost of enforcing this on every field of every PATCH.
//
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
// JSON value decodes into Value with a json.Decoder configured with
// DisallowUnknownFields, so an unknown field inside T is rejected the same
// way decodeJSON rejects one at the top level (#0169) — plain json.Unmarshal
// would silently drop it instead, reopening the hole #0169 found.
//
// Measured cost of the stricter decoder vs. plain json.Unmarshal (Apple M4
// Pro, go test -bench, see #0169's issue file for the full numbers): for the
// current Optional[int] instantiation, decoding a bare integer, the strict
// path is ~245ns and 3 allocations slower (306ns/4 allocs vs 61ns/1 alloc).
// For a struct-typed T the gap narrows in relative terms (342ns/7 allocs vs
// 249ns/4 allocs). Both are sub-microsecond, once per field per request, on
// a handler that also does a database round trip measured in milliseconds —
// not a cost worth trading away the single-decode-contract guarantee for,
// and this project has no performance requirement (CLAUDE.md §5).
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.Present = true
	if string(data) == "null" {
		o.Valid = false
		var zero T
		o.Value = zero
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o.Value); err != nil {
		return err
	}
	o.Valid = true
	return nil
}
