package handlers

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/events"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// countingBroker wraps a real *events.Broker to record Subscribe/Unsubscribe so
// a test can assert the handler tore down its subscription on disconnect, and to
// signal when a subscription has been registered.
type countingBroker struct {
	inner      *events.Broker
	mu         sync.Mutex
	subs       int
	unsubs     int
	subscribed chan struct{} // receives once per Subscribe
}

func newCountingBroker() *countingBroker {
	return &countingBroker{inner: events.NewBroker(), subscribed: make(chan struct{}, 8)}
}

func (b *countingBroker) Subscribe(userID int64) chan events.Event {
	ch := b.inner.Subscribe(userID)
	b.mu.Lock()
	b.subs++
	b.mu.Unlock()
	b.subscribed <- struct{}{}
	return ch
}

func (b *countingBroker) Unsubscribe(userID int64, ch chan events.Event) {
	b.inner.Unsubscribe(userID, ch)
	b.mu.Lock()
	b.unsubs++
	b.mu.Unlock()
}

func (b *countingBroker) counts() (subs, unsubs int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.subs, b.unsubs
}

// TestEventsStream_DeliversFrameAndUnsubscribesOnCancel drives the SSE handler
// through the REAL RequireSession (live DB session): it subscribes, writes a
// well-formed frame for a published event, and—when the client
// disconnects (request context canceled)—unsubscribes and returns.
func TestEventsStream_DeliversFrameAndUnsubscribesOnCancel(t *testing.T) {
	pool := credsTestPool(t) // skips when TEST_DATABASE_URL unset.
	authStore := auth.NewStore(pool)

	broker := newCountingBroker()
	h := NewEventsHandler(broker)
	requireSession := middleware.RequireSession(authStore)
	mux := http.NewServeMux()
	mux.Handle("GET /api/events", requireSession(http.HandlerFunc(h.Stream)))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	uid := seedUser(t, pool, "sse@example.com")
	seedSession(t, pool, uid, "sse-token")

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	resp, err := srv.Client().Do(withCookie(req, "sse-token"))
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	if ab := resp.Header.Get("X-Accel-Buffering"); ab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", ab)
	}

	reader := bufio.NewReader(resp.Body)

	// Initial ": connected" comment frame.
	if line, _ := reader.ReadString('\n'); !strings.HasPrefix(line, ":") {
		t.Errorf("first line = %q, want a comment frame", line)
	}

	// Wait until the handler has subscribed before publishing, so the event
	// cannot race ahead of the subscription.
	select {
	case <-broker.subscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never subscribed")
	}

	broker.inner.Publish(uid, events.Event{Name: "test.event", Payload: []byte(`{"ok":true}`)})

	frame := readFrame(t, reader)
	if !strings.Contains(frame, "event: test.event\n") {
		t.Errorf("frame missing event line:\n%q", frame)
	}
	if !strings.Contains(frame, `data: {"ok":true}`) {
		t.Errorf("frame missing/incorrect data line:\n%q", frame)
	}

	// Disconnect: cancel the request context; the handler must Unsubscribe.
	cancel()
	deadline := time.After(2 * time.Second)
	for {
		if _, unsubs := broker.counts(); unsubs == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("handler did not Unsubscribe after client disconnect")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if subs, unsubs := broker.counts(); subs != 1 || unsubs != 1 {
		t.Errorf("subs=%d unsubs=%d, want 1/1", subs, unsubs)
	}
}

// readFrame reads lines until it has collected a `data:` line followed by a
// blank line, returning the accumulated `event:`/`data:` text.
func readFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	sawData := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read frame: %v (so far: %q)", err, b.String())
		}
		if strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "data:") {
			b.WriteString(line)
			if strings.HasPrefix(line, "data:") {
				sawData = true
			}
			continue
		}
		if line == "\n" && sawData {
			return b.String()
		}
	}
	t.Fatalf("timed out reading frame (so far: %q)", b.String())
	return ""
}

// TestEventsStream_Unauthenticated asserts the handler 401s when no user is in
// context (defense in depth; in production RequireSession answers first). This
// path needs no DB.
func TestEventsStream_Unauthenticated(t *testing.T) {
	h := NewEventsHandler(newCountingBroker())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	h.Stream(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
