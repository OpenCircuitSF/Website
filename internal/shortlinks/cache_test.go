package shortlinks

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeFetch struct {
	calls int
	err   error
}

func (f *fakeFetch) fetch(ctx context.Context, slug string, from, to time.Time) (*CampaignRollup, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &CampaignRollup{ClickCount: int64(f.calls)}, nil
}

func cacheAt(f *fakeFetch, clock *time.Time) *StatsCache {
	c := NewStatsCache(f.fetch, StatsCacheTTL, StatsCacheFailTTL)
	c.now = func() time.Time { return *clock }
	return c
}

var (
	cFrom = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	cTo   = time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
)

func TestStatsCacheHitAndExpiry(t *testing.T) {
	f := &fakeFetch{}
	clock := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	c := cacheAt(f, &clock)
	ctx := context.Background()
	r1, at1, _ := c.Get(ctx, "s", cFrom, cTo)
	clock = clock.Add(59 * time.Second)
	r2, at2, _ := c.Get(ctx, "s", cFrom, cTo)
	if f.calls != 1 || r1 != r2 || !at1.Equal(at2) {
		t.Fatalf("calls %d", f.calls)
	}
	clock = clock.Add(2 * time.Second)
	r3, _, _ := c.Get(ctx, "s", cFrom, cTo)
	if f.calls != 2 || r3.ClickCount != 2 {
		t.Fatalf("calls %d", f.calls)
	}
}

func TestStatsCacheWindowIsPartOfKey(t *testing.T) {
	f := &fakeFetch{}
	clock := time.Now()
	c := cacheAt(f, &clock)
	ctx := context.Background()
	c.Get(ctx, "s", cFrom, cTo)
	c.Get(ctx, "s", cFrom, cTo.AddDate(0, 0, 1))
	c.Get(ctx, "other", cFrom, cTo)
	if f.calls != 3 {
		t.Fatalf("calls %d", f.calls)
	}
}

func TestStatsCacheErrorCachedForFailTTL(t *testing.T) {
	boom := errors.New("boom")
	f := &fakeFetch{err: boom}
	clock := time.Now()
	c := cacheAt(f, &clock)
	ctx := context.Background()
	if _, _, err := c.Get(ctx, "s", cFrom, cTo); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	clock = clock.Add(14 * time.Second)
	c.Get(ctx, "s", cFrom, cTo)
	if f.calls != 1 {
		t.Fatalf("calls %d, want the failure cached", f.calls)
	}
	clock = clock.Add(2 * time.Second)
	f.err = nil
	r, _, err := c.Get(ctx, "s", cFrom, cTo)
	if err != nil || f.calls != 2 || r == nil {
		t.Fatalf("calls %d err %v", f.calls, err)
	}
}

func TestStatsCacheCanceledContextNotCached(t *testing.T) {
	f := &fakeFetch{err: context.Canceled}
	clock := time.Now()
	c := cacheAt(f, &clock)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Get(ctx, "s", cFrom, cTo)
	f.err = nil
	r, _, err := c.Get(context.Background(), "s", cFrom, cTo)
	if err != nil || r == nil || f.calls != 2 {
		t.Fatalf("calls %d err %v", f.calls, err)
	}
}
