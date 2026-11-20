package publicapi

import (
	"strings"
	"time"
)

// Limits shared by every public operation.
const (
	DefaultLimit  = 10  // a list's size when limit is unset or not positive
	MaxLimit      = 50  // the largest list size, whatever limit asks for
	MaxQueryRunes = 200 // a search query is cut to this many runes

	RequestsPerMinute = 60  // per client IP, /mcp and /api/public/v1/ together (#0730)
	MaxCacheEntries   = 512 // the list cache is emptied rather than grow past this
	LogQueryRunes     = 64  // a logged query is cut to this many runes
)

// ListCacheTTL is how long a list result is served from the cache (#0730).
const ListCacheTTL = 60 * time.Second

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
