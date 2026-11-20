package publicapi

import (
	"encoding/json"
	"sync"
	"time"
)

// resultCache holds successful results of Cached ops for ListCacheTTL
// (#0730). A cached out is shared between callers; neither adapter
// mutates it.
type resultCache struct {
	mu      sync.Mutex
	gen     uint64
	entries map[string]cacheEntry
}

type cacheEntry struct {
	out     any
	expires time.Time
}

func newResultCache() *resultCache { return &resultCache{entries: map[string]cacheEntry{}} }

// cacheKey is name plus the JSON of in; false if in cannot be marshalled.
func cacheKey(name string, in any) (string, bool) {
	b, err := json.Marshal(in)
	if err != nil {
		return "", false
	}
	return name + "\x00" + string(b), true
}

// get returns the live entry for key, and the generation a later put must
// match. An expired entry is a miss and is deleted.
func (c *resultCache) get(key string, now time.Time) (any, bool, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if ok && now.Before(e.expires) {
		return e.out, true, c.gen
	}
	if ok {
		delete(c.entries, key)
	}
	return nil, false, c.gen
}

// put stores out unless an invalidate happened since gen was read. At
// MaxCacheEntries it drops expired entries, then empties the map if that
// was not enough.
func (c *resultCache) put(key string, out any, gen uint64, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return
	}
	if len(c.entries) >= MaxCacheEntries {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= MaxCacheEntries {
			c.entries = map[string]cacheEntry{}
		}
	}
	c.entries[key] = cacheEntry{out: out, expires: now.Add(ListCacheTTL)}
}

func (c *resultCache) invalidate() {
	c.mu.Lock()
	c.gen++
	c.entries = map[string]cacheEntry{}
	c.mu.Unlock()
}

func (c *resultCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
