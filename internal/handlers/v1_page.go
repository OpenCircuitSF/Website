package handlers

import (
	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

// paginate is the shared keyset pager for /api/v1 list operations (#0676;
// #0677 and #0678 reuse it). The caller loads its rows, filters and sorts
// them in Go (the tables are small), then hands them here.
//
// key gives the cursor value for an item; after reports whether an item
// comes after a decoded cursor value, in the caller's sort order. A cursor is
// decoded into K and used only as a sort key: it never filters or widens
// what the caller already selected, so a forged cursor can only move the
// window over rows the token may already see. next_cursor is set only when
// more items remain.
func paginate[T, K any](sorted []T, p api.Page, key func(T) K, after func(T, K) bool) (api.List[T], error) {
	limit, err := p.Resolve()
	if err != nil {
		return api.List[T]{}, err
	}
	rest := sorted
	if p.Cursor != "" {
		var k K
		if err := api.DecodeCursor(p.Cursor, &k); err != nil {
			return api.List[T]{}, err
		}
		i := 0
		for i < len(rest) && !after(rest[i], k) {
			i++
		}
		rest = rest[i:]
	}
	var next *string
	if len(rest) > limit {
		rest = rest[:limit]
		c := api.EncodeCursor(key(rest[len(rest)-1]))
		next = &c
	}
	return api.NewList(append([]T(nil), rest...), next), nil
}
