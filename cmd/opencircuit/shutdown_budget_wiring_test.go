package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/events"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// delayedMailer models a mailer send that takes real, deterministic wall-clock
// time to complete and — deliberately — does NOT react to context
// cancellation, matching the one case Close's own doc comment (subscribe.go)
// calls out as "expected" to exceed its budget: "the mailer itself ignores
// context cancellation". entered is closed the instant Send is invoked, so a
// test can confirm the send has genuinely started (been dequeued by a worker)
// before proceeding — the same synchronization idiom shutdown_wiring_test.go's
// blockingTestMailer uses.
//
// trueStart records the wall-clock instant Send itself begins, on the worker
// goroutine, BEFORE entered is closed. This is deliberately not the same
// moment the test goroutine observes via <-entered: the test goroutine only
// runs after the scheduler gets around to it, which under load can lag the
// real Send entry by hundreds of ms (measured directly during #0087's review:
// lags of 177-327ms were routine). Using the test's own receive time as the
// send-start reference systematically UNDERcounts elapsed time by that lag,
// which is what made the old version of this test false-fail on healthy code
// — see issues/0087.md's review notes. Reading trueStart instead measures
// from where the work actually starts, not from where a different goroutine
// happens to notice it did.
type delayedMailer struct {
	delay   time.Duration
	entered chan struct{}

	mu        sync.Mutex
	trueStart time.Time
}

func (m *delayedMailer) Send(_ context.Context, _ mailing.Message) (string, error) {
	m.mu.Lock()
	m.trueStart = time.Now()
	m.mu.Unlock()
	close(m.entered)
	time.Sleep(m.delay)
	return "delayed-sent", nil
}

// SendStart returns the instant Send began, valid only after entered has
// been observed closed (the mu guards the write in Send against this read
// racing it before that happens; callers are expected to synchronize via
// entered first, exactly as this test does).
func (m *delayedMailer) SendStart() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.trueStart
}

// TestMountAndServe_ShutdownAndCloseHaveIndependentBudgets is #0087's
// end-to-end reproduction and fix proof: an open GET /api/events stream
// forces srv.Shutdown to consume its ENTIRE budget (http.Server.Shutdown
// does not cancel in-flight request contexts, and Stream — events.go —
// returns only on client disconnect), and a concurrently in-flight
// POST /api/subscribe send (via delayedMailer, sendDelay above) needs real
// time of its own to finish after that.
//
// The observable signal is TIMING of mountAndServe's own return (via errCh),
// not log output or eventual DB state — both of those stay true regardless
// of whether Close actually WAITED for completion, because nothing in this
// test process actually exits when mountAndServe returns (unlike
// production, where returning from mountAndServe means the process is about
// to exit and anything Close didn't wait for can be cut short). Elapsed
// time since the send worker actually started delayedMailer.Send (mailer.entered,
// which happens moments before SIGTERM is sent below) is the one thing that
// directly reveals whether Close was given its own real budget:
//
//   - With shutdownServerTimeout and subscribeCloseTimeout as independent
//     contexts (the fix): Shutdown consumes its own dedicated budget (the
//     SSE stream never goes idle) while delayedMailer's sleep runs
//     concurrently in the background (it is a separate goroutine, not
//     something Shutdown or Close start), then Close is called with a
//     FRESH budget and genuinely waits out however much of sendDelay is
//     still remaining. Because testShutdownServerTimeout < sendDelay, total
//     elapsed from mailer.entered to mountAndServe's return lands close to
//     sendDelay itself (Shutdown's wait and most of the send's sleep
//     overlap in real time).
//   - With a single shared context (the pre-#0087 shape — reproduced by
//     temporarily editing mountAndServe to pass one shared ctx to both
//     calls; see issues/0087.md's Verification for the exact mutation and
//     transcript): Shutdown consumes the ENTIRE shared budget (same
//     reason), leaving Close's ctx already expired the instant it is
//     called. Close's own select hits ctx.Done() immediately, returning
//     WITHOUT waiting for delayedMailer at all — regardless of how much of
//     its sleep has actually elapsed. Total elapsed lands close to just
//     testShutdownServerTimeout, well short of sendDelay.
//
// wantMinElapsed below sits strictly between those two totals (with margin
// on both sides for scheduling jitter), so this one threshold distinguishes
// fixed from broken deterministically.
func TestMountAndServe_ShutdownAndCloseHaveIndependentBudgets(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), wiringDBConnectTimeout)
	defer cancel()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() {
		truncateAdminWiringTables(t, pool)
		_, _ = pool.Exec(context.Background(), `TRUNCATE subscriber_interests, subscribers RESTART IDENTITY CASCADE`)
		pool.Close()
	})
	truncateAdminWiringTables(t, pool)
	if _, err := pool.Exec(ctx, `TRUNCATE subscriber_interests, subscribers RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate subscribers: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg := &config.Config{
		Port:             port,
		BaseURL:          baseURL,
		WebAuthnRPID:     "localhost",
		WebAuthnRPOrigin: baseURL,
	}

	// Real session auth for GET /api/events — it is mounted behind
	// requireSession (main.go), unlike the passthrough-guarded routes the
	// other subscribe/shutdown wiring tests exercise.
	store := auth.NewStore(pool)
	requireSession := middleware.RequireSession(store)
	passthroughAdmin := func(next http.Handler) http.Handler { return next }
	userID := seedAdminWiringUser(t, pool, fmt.Sprintf("zz-shutdown-budget-%d@example.com", time.Now().UnixNano()), false)
	seedAdminWiringSession(t, pool, userID, "zz-shutdown-budget-token")

	broker := events.NewBroker()
	eventsH := handlers.NewEventsHandler(broker)

	const sendDelay = 600 * time.Millisecond
	mailer := &delayedMailer{delay: sendDelay, entered: make(chan struct{})}
	subscribeH := handlers.NewSubscribeHandler(
		subscribers.NewStore(pool), interests.NewStore(pool), mailer,
		handlers.NoSuppressions{}, nil, audit.New(pool), cfg.BaseURL, slog.Default(),
	)

	// Test-only override of the production budgets (#0087's shutdownServerTimeout
	// and subscribeCloseTimeout — package vars precisely so this test can do
	// this deterministically instead of waiting out the real 5s/15s values).
	// Restored unconditionally so no later test in this binary observes the
	// shrunk budgets.
	origServerTimeout, origCloseTimeout := shutdownServerTimeout, subscribeCloseTimeout
	const testShutdownServerTimeout = 150 * time.Millisecond
	shutdownServerTimeout = testShutdownServerTimeout
	subscribeCloseTimeout = 3 * time.Second
	t.Cleanup(func() {
		shutdownServerTimeout, subscribeCloseTimeout = origServerTimeout, origCloseTimeout
	})

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, pool,
			nil, nil, nil, nil, nil, nil, /* adminInterestsH: not exercised */
			nil, /* adminSubscribersH: not exercised */
			nil, /* adminSuppressionsH: not exercised */
			nil, /* adminCampaignsH: not exercised */
			nil, /* adminCampaignAudienceH: not exercised */
			nil, /* adminCampaignPreviewH: not exercised */
			nil, /* adminCampaignPreflightH: not exercised */
			eventsH, nil, subscribeH,
			nil, nil, nil, nil, /* publicInterestsH, preferencesH, confirmH, unsubscribeH: not exercised */
			nil, /* sesNotifyH: not exercised */
			nil, /* sendWorker: not exercised */
			requireSession, passthroughAdmin, nil, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, baseURL, errCh, ready)

	// Open the SSE stream and DON'T close its body until this test is done —
	// srv.Shutdown will see this connection as perpetually active (Stream,
	// events.go, blocks until client disconnect) and so cannot return before
	// its own dedicated deadline.
	sseClient := &http.Client{} // no Timeout: this connection is meant to stay open
	sseReq, err := http.NewRequest(http.MethodGet, baseURL+"/api/events", nil)
	if err != nil {
		t.Fatalf("build SSE request: %v", err)
	}
	sseReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "zz-shutdown-budget-token"})
	sseResp, err := sseClient.Do(sseReq)
	if err != nil {
		t.Fatalf("open GET /api/events: %v", err)
	}
	t.Cleanup(func() { sseResp.Body.Close() })
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/events status = %d, want 200", sseResp.StatusCode)
	}

	// Enqueue a real async send via POST /api/subscribe, then wait for the
	// worker to actually dequeue it (mailer.entered) before triggering
	// shutdown — otherwise the SIGTERM below could race the enqueue and the
	// elapsed-time math would be measuring the wrong thing.
	email := fmt.Sprintf("zz-shutdown-budget-%d@example.com", time.Now().UnixNano())
	body, err := json.Marshal(map[string]any{
		"email":       email,
		"interests":   []string{},
		"website":     "",
		"rendered_at": time.Now().Add(-5 * time.Second).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("marshal subscribe body: %v", err)
	}
	subReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/subscribe", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build subscribe request: %v", err)
	}
	subReq.Header.Set("Content-Type", "application/json")
	subResp, err := client.Do(subReq)
	if err != nil {
		t.Fatalf("POST /api/subscribe: %v", err)
	}
	subResp.Body.Close()
	if subResp.StatusCode != http.StatusAccepted {
		t.Fatalf("subscribe status = %d, want 202", subResp.StatusCode)
	}

	// sendStart marks the moment delayedMailer's sendDelay clock actually
	// starts — used below instead of "time since SIGTERM" because the send
	// worker starts sleeping the instant it dequeues the job (moments
	// before SIGTERM is sent, not after Close is called), so measuring from
	// here is what makes the "total ≈ sendDelay" math in this test's doc
	// comment hold.
	//
	// This reads mailer.SendStart() — the timestamp Send recorded on its own
	// goroutine — rather than time.Now() taken here after the receive. The
	// two are NOT interchangeable: this goroutine only gets to run this line
	// after the scheduler wakes it up following <-mailer.entered, which lags
	// the real Send entry by an amount that grows with machine load (measured
	// up to ~330ms during #0087's review). That lag was subtracted straight
	// out of the elapsed measurement below, which is exactly what made the
	// old version of this test false-fail on healthy code under load. See
	// delayedMailer's doc comment and issues/0087.md's review notes.
	select {
	case <-mailer.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("delayedMailer.Send was never entered — nothing in flight; test invalid")
	}
	sendStart := mailer.SendStart()

	// wantMinElapsed sits strictly between the two possible totals described
	// in this test's doc comment: the "starved Close" total is close to just
	// testShutdownServerTimeout (150ms); the "fixed, independent budgets"
	// total is close to sendDelay (600ms), since Shutdown's wait and most of
	// the send's sleep run concurrently. 350ms leaves ~200ms of margin on
	// both sides against scheduling jitter while still cleanly separating
	// the two outcomes.
	const wantMinElapsed = 350 * time.Millisecond
	// wantMaxElapsed is a sanity ceiling well above sendDelay (600ms) but
	// far below subscribeCloseTimeout's test value (3s), so a hang or a
	// regression to "Close waits its full budget regardless of when the
	// send actually finishes" is also caught, not just starvation.
	const wantMaxElapsed = 1200 * time.Millisecond

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill self: %v", err)
	}

	select {
	case shutdownErr := <-errCh:
		elapsed := time.Since(sendStart)
		t.Logf("mountAndServe returned %v after the send began (Shutdown error: %v)", elapsed, shutdownErr)
		if elapsed < wantMinElapsed {
			t.Fatalf("mountAndServe returned only %v after the send began (want >= %v) — Close appears to have been "+
				"starved of its own budget by Shutdown's open-SSE-stream delay, exactly the #0087 bug",
				elapsed, wantMinElapsed)
		}
		if elapsed > wantMaxElapsed {
			t.Fatalf("mountAndServe took %v after the send began to return (want <= %v) — Close does not appear "+
				"to have returned once delayedMailer's send actually completed", elapsed, wantMaxElapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mountAndServe did not return within 5s of SIGTERM")
	}
}
