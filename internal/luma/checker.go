package luma

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Feed settings.
const (
	FeedBase     = "https://api.luma.com/ics/get"
	FetchTimeout = 10 * time.Second
	// CacheTTL is how long a good read is reused; FailTTL how long a failed
	// one is, so a down Luma costs at most one FetchTimeout wait a minute.
	CacheTTL     = 5 * time.Minute
	FailTTL      = time.Minute
	MaxFeedBytes = 2 << 20
	userAgent    = "OpenCircuitSF (+https://www.opencircuitsf.com)"
)

// Status is the outcome of reading the feed.
type Status string

// Feed statuses.
const (
	StatusOK          Status = "ok"
	StatusOff         Status = "off"
	StatusUnreachable Status = "unreachable"
	StatusMalformed   Status = "malformed"
)

// Snapshot is one read of the feed. Events is empty unless Status is
// StatusOK; ReadAt and LastGoodAt are zero when there is none.
type Snapshot struct {
	Status     Status
	Events     []Event
	ReadAt     time.Time
	LastGoodAt time.Time
}

// Checker fetches a calendar's feed and caches the result.
type Checker struct {
	calendarID func() string
	base       string
	client     *http.Client
	now        func() time.Time

	// mu guards the cache and is held across the fetch on purpose, so
	// concurrent page loads share one request; the client timeout bounds it.
	mu       sync.Mutex
	id       string
	snap     Snapshot
	expires  time.Time
	lastGood time.Time
}

// NewChecker builds a Checker reading the calendar calendarID names (""
// turns the check off). base "" means FeedBase.
func NewChecker(calendarID func() string, base string) *Checker {
	if base == "" {
		base = FeedBase
	}
	return &Checker{calendarID: calendarID, base: base, client: &http.Client{Timeout: FetchTimeout}, now: time.Now}
}

// FeedURL is the iCal feed URL for calendar id under base.
func FeedURL(base, id string) string {
	return base + "?entity=calendar&id=" + url.QueryEscape(id)
}

// Feed returns the cached snapshot, fetching the feed when the cache is cold,
// expired, or for a different calendar. The fetch runs under
// context.WithoutCancel(ctx): an admin who navigates away mid-fetch must not
// leave "unreachable" cached for a minute. The client's own timeout still
// bounds it.
func (c *Checker) Feed(ctx context.Context) Snapshot {
	id := strings.TrimSpace(c.calendarID())
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == "" {
		return Snapshot{Status: StatusOff, LastGoodAt: c.lastGood}
	}
	now := c.now()
	if id == c.id && now.Before(c.expires) {
		return c.snap
	}
	st, evs := c.fetch(context.WithoutCancel(ctx), id)
	snap := Snapshot{Status: st}
	ttl := FailTTL
	if st == StatusOK {
		snap.Events, snap.ReadAt = evs, now
		c.lastGood = now
		ttl = CacheTTL
	}
	snap.LastGoodAt = c.lastGood
	c.id, c.snap, c.expires = id, snap, now.Add(ttl)
	return snap
}

func (c *Checker) fetch(ctx context.Context, id string) (Status, []Event) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FeedURL(c.base, id), nil)
	if err != nil {
		return StatusUnreachable, nil
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.client.Do(req)
	if err != nil {
		return StatusUnreachable, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return StatusUnreachable, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxFeedBytes+1))
	if err != nil {
		return StatusUnreachable, nil
	}
	if len(body) > MaxFeedBytes {
		return StatusMalformed, nil
	}
	evs, err := ParseICS(bytes.NewReader(body))
	if err != nil {
		return StatusMalformed, nil
	}
	return StatusOK, evs
}
