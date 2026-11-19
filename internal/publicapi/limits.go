package publicapi

import "strings"

// Limits shared by every public operation.
const (
	DefaultLimit  = 10  // a list's size when limit is unset or not positive
	MaxLimit      = 50  // the largest list size, whatever limit asks for
	MaxQueryRunes = 200 // a search query is cut to this many runes
)

// ClampLimit is limit bounded to 1..MaxLimit: nil or ≤0 is DefaultLimit,
// above MaxLimit is MaxLimit.
func ClampLimit(limit *int) int {
	switch {
	case limit == nil || *limit <= 0:
		return DefaultLimit
	case *limit > MaxLimit:
		return MaxLimit
	}
	return *limit
}

// CapQuery is q with surrounding space trimmed, truncated to MaxQueryRunes
// runes. A long query is shortened, never refused.
func CapQuery(q string) string {
	q = strings.TrimSpace(q)
	n := 0
	for i := range q {
		if n == MaxQueryRunes {
			return strings.TrimSpace(q[:i])
		}
		n++
	}
	return q
}
