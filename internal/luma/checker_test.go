package luma

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type feedServer struct {
	*httptest.Server
	hits   atomic.Int32
	status atomic.Int32
	body   atomic.Value // string
	last   atomic.Value // *http.Request
}

func newFeedServer(t *testing.T) *feedServer {
	t.Helper()
	b, err := os.ReadFile("testdata/luma.ics")
	if err != nil {
		t.Fatal(err)
	}
	fs := &feedServer{}
	fs.status.Store(200)
	fs.body.Store(string(b))
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.hits.Add(1)
		fs.last.Store(r)
		w.WriteHeader(int(fs.status.Load()))
		_, _ = w.Write([]byte(fs.body.Load().(string)))
	}))
	t.Cleanup(fs.Close)
	return fs
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestChecker(id *string, base string) (*Checker, *clock) {
	c := NewChecker(func() string { return *id }, base)
	clk := &clock{t: time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)}
	c.now = clk.now
	return c, clk
}

func TestCheckerOffMakesNoRequest(t *testing.T) {
	fs := newFeedServer(t)
	id := "  "
	c, _ := newTestChecker(&id, fs.URL)
	if s := c.Feed(context.Background()); s.Status != StatusOff || len(s.Events) != 0 {
		t.Fatalf("snapshot = %+v", s)
	}
	if fs.hits.Load() != 0 {
		t.Errorf("hits = %d", fs.hits.Load())
	}
}

func TestCheckerRequestsCalendarFeed(t *testing.T) {
	fs := newFeedServer(t)
	id := "cal-test"
	c, clk := newTestChecker(&id, fs.URL)
	s := c.Feed(context.Background())
	if s.Status != StatusOK || len(s.Events) != 6 || !s.ReadAt.Equal(clk.t) || !s.LastGoodAt.Equal(clk.t) {
		t.Fatalf("snapshot = %+v", s)
	}
	r := fs.last.Load().(*http.Request)
	if r.URL.Path != "/" || r.URL.Query().Get("entity") != "calendar" || r.URL.Query().Get("id") != "cal-test" {
		t.Errorf("request = %s", r.URL)
	}
	if !strings.HasPrefix(r.UserAgent(), "OpenCircuitSF") {
		t.Errorf("user agent = %q", r.UserAgent())
	}
}

func TestCheckerCachesGoodReadFiveMinutes(t *testing.T) {
	fs := newFeedServer(t)
	id := "cal-test"
	c, clk := newTestChecker(&id, fs.URL)
	c.Feed(context.Background())
	c.Feed(context.Background())
	if fs.hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", fs.hits.Load())
	}
	clk.t = clk.t.Add(5*time.Minute + time.Second)
	c.Feed(context.Background())
	if fs.hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2", fs.hits.Load())
	}
}

func TestCheckerFailureKeepsLastGoodButNoEvents(t *testing.T) {
	fs := newFeedServer(t)
	id := "cal-test"
	c, clk := newTestChecker(&id, fs.URL)
	first := clk.t
	c.Feed(context.Background())
	fs.status.Store(503)
	clk.t = clk.t.Add(CacheTTL + time.Second)
	s := c.Feed(context.Background())
	if s.Status != StatusUnreachable || len(s.Events) != 0 || !s.LastGoodAt.Equal(first) || !s.ReadAt.IsZero() {
		t.Fatalf("snapshot = %+v", s)
	}
	clk.t = clk.t.Add(30 * time.Second)
	c.Feed(context.Background())
	if fs.hits.Load() != 2 {
		t.Errorf("hits = %d, want 2", fs.hits.Load())
	}
}

func TestCheckerMalformedFeed(t *testing.T) {
	fs := newFeedServer(t)
	fs.body.Store("<!doctype html><p>hi</p>")
	id := "cal-test"
	c, _ := newTestChecker(&id, fs.URL)
	if s := c.Feed(context.Background()); s.Status != StatusMalformed || len(s.Events) != 0 {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestCheckerOversizedFeed(t *testing.T) {
	fs := newFeedServer(t)
	fs.body.Store(strings.Repeat("x", MaxFeedBytes+1))
	id := "cal-test"
	c, _ := newTestChecker(&id, fs.URL)
	if s := c.Feed(context.Background()); s.Status != StatusMalformed {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestCheckerRefetchesWhenCalendarChanges(t *testing.T) {
	fs := newFeedServer(t)
	id := "cal-test"
	c, _ := newTestChecker(&id, fs.URL)
	c.Feed(context.Background())
	id = "cal-other"
	c.Feed(context.Background())
	if fs.hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2", fs.hits.Load())
	}
	if got := fs.last.Load().(*http.Request).URL.Query().Get("id"); got != "cal-other" {
		t.Errorf("id = %q", got)
	}
}

func TestCheckerTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	id := "cal-test"
	c, _ := newTestChecker(&id, srv.URL)
	c.client.Timeout = 50 * time.Millisecond
	if s := c.Feed(context.Background()); s.Status != StatusUnreachable {
		t.Fatalf("snapshot = %+v", s)
	}
}
