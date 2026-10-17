package shortlinks

import (
	"context"
	"sync"
	"time"
)

const (
	// StatsCacheTTL is how long a successful stats fetch is reused, so the
	// Outreach screens do not call ShortLinks on every render.
	StatsCacheTTL = 60 * time.Second
	// StatsCacheFailTTL is how long a failed fetch is remembered, so an
	// unreachable ShortLinks is not retried on every render.
	StatsCacheFailTTL = 15 * time.Second
)

// StatsFetcher fetches one campaign rollup; *Client.CampaignStats satisfies it.
type StatsFetcher func(ctx context.Context, slug string, from, to time.Time) (*CampaignRollup, error)

type statsEntry struct {
	rollup    *CampaignRollup
	err       error
	fetchedAt time.Time
	expires   time.Time
}

// StatsCache memoizes CampaignStats results per slug and window.
type StatsCache struct {
	fetch   StatsFetcher
	ttl     time.Duration
	failTTL time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]statsEntry
}

// NewStatsCache wraps fetch with a success TTL and a shorter failure TTL.
func NewStatsCache(fetch StatsFetcher, ttl, failTTL time.Duration) *StatsCache {
	return &StatsCache{fetch: fetch, ttl: ttl, failTTL: failTTL, now: time.Now, entries: map[string]statsEntry{}}
}

// Get returns the cached or freshly fetched rollup and when it was fetched.
// The returned rollup is shared: callers must not mutate it. A fetch is made
// without holding the lock; an error caused by the caller's own canceled
// context is never cached.
func (c *StatsCache) Get(ctx context.Context, slug string, from, to time.Time) (*CampaignRollup, time.Time, error) {
	const day = "2006-01-02"
	key := slug + "|" + from.UTC().Format(day) + "|" + to.UTC().Format(day)

	c.mu.Lock()
	if e, ok := c.entries[key]; ok && c.now().Before(e.expires) {
		c.mu.Unlock()
		return e.rollup, e.fetchedAt, e.err
	}
	c.mu.Unlock()

	rollup, err := c.fetch(ctx, slug, from, to)
	now := c.now()
	if err != nil && ctx.Err() != nil {
		return nil, now, err
	}
	ttl := c.ttl
	if err != nil {
		ttl = c.failTTL
	}
	c.mu.Lock()
	for k, e := range c.entries {
		if !now.Before(e.expires) {
			delete(c.entries, k)
		}
	}
	c.entries[key] = statsEntry{rollup: rollup, err: err, fetchedAt: now, expires: now.Add(ttl)}
	c.mu.Unlock()
	return rollup, now, err
}
