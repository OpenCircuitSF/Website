package testdb

import (
	"sync"
	"testing"
)

// TestUnique_DistinctBackToBack proves Unique returns a different value on
// every call, including two calls with no intervening work at all — the
// exact shape that made the old time.Now().UnixNano()-based test helpers a
// near coin-flip for "slug already exists" (#0158). A 200,000-pair probe on
// this project's hardware measured UnixNano repeating on 94.7% of
// back-to-back calls (minimum delta 0ns), so a helper built on it fails
// this test on almost every run; see #0158's `## Verification` for a
// demonstration against a UnixNano-based Unique.
func TestUnique_DistinctBackToBack(t *testing.T) {
	a := Unique()
	b := Unique()
	if a == b {
		t.Fatalf("Unique() returned the same value twice back-to-back: %d", a)
	}
}

// TestUnique_ConcurrentDistinct proves Unique never hands out the same
// value twice under concurrent use from many goroutines, which is closer to
// how table-driven and t.Parallel tests actually call the helpers built on
// top of it.
func TestUnique_ConcurrentDistinct(t *testing.T) {
	const n = 1000
	results := make([]int64, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			results[i] = Unique()
		}()
	}
	wg.Wait()

	seen := make(map[int64]bool, n)
	for _, v := range results {
		if seen[v] {
			t.Fatalf("Unique() returned duplicate value %d under concurrent use", v)
		}
		seen[v] = true
	}
}
