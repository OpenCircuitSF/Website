package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// blockingTestSubscriberStore blocks Create until either release is closed
// (by the test) or ctx is cancelled, closing entered the instant Create is
// called. A package-local twin of internal/handlers' own unexported
// blockingSubscriberStore test double (unreachable from here) — this
// package only needs the one property it proves: a subscription mutation
// genuinely caught in flight. Before #0126 this file used an equivalent
// blockingTestMailer wrapping mailer.Send, since that was the async work
// SubscribeHandler.Close waited on; #0126 removed the mailer entirely
// (sending moved to internal/mailing.OutboxWorker, a separate process this
// test does not exercise) and made Create the last async DB write
// processMutateJob performs for a brand-new signup, so this blocks Create
// instead.
type blockingTestSubscriberStore struct {
	*subscribers.Store
	entered chan struct{}
	release chan struct{}
}

func (b *blockingTestSubscriberStore) Create(ctx context.Context, in subscribers.NewSignup, now time.Time) (subscribers.Subscriber, error) {
	close(b.entered)
	select {
	case <-b.release:
		return b.Store.Create(ctx, in, now)
	case <-ctx.Done():
		return subscribers.Subscriber{}, ctx.Err()
	}
}

// TestMountAndServe_SIGTERMReleasesInFlightClaim is #0081's end-to-end
// proof that mountAndServe's graceful-shutdown wiring works against a REAL
// os.Signal — not merely a direct h.Close(ctx) call, which
// internal/handlers' own
// TestSubscribeHandler_Close_InterruptsInFlightMutationPromptly already
// covers at the unit level. This drives a real HTTP listener via
// mountAndServe (the exact function servePostgres calls), sends the
// process a genuine SIGTERM while a brand-new signup's Create is caught in
// flight, and confirms mountAndServe returns promptly with no half-created
// row left behind — matching issues/0081.md's ## Root cause reproduction,
// except this time the graceful-shutdown wiring is exercised instead of
// skipped. #0126 note: before #0126, this scenario left a STAMPED-BUT-
// UNSENT confirm_sent_at claim that had to be explicitly released; since
// Create now claims-and-enqueues atomically in one transaction, an
// interrupted Create never reaches the database at all (blockingTestSubscriberStore
// returns ctx.Err() BEFORE delegating), so there is no claim to release —
// the row simply does not exist yet, a stronger property than "released".
//
// Sending SIGTERM to this test's own process is safe and does not
// terminate the test binary: mountAndServe registers
// signal.Notify(sigCh, syscall.SIGTERM, ...), and Go delivers an
// intercepted signal to the registered channel INSTEAD of taking the
// default (process-terminating) action.
func TestMountAndServe_SIGTERMReleasesInFlightClaim(t *testing.T) {
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
	defer func() {
		_, _ = pool.Exec(context.Background(), `TRUNCATE subscriber_interests, subscribers RESTART IDENTITY CASCADE`)
		pool.Close()
	}()
	if _, err := pool.Exec(ctx, `TRUNCATE subscriber_interests, subscribers RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
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

	blocked := &blockingTestSubscriberStore{Store: subscribers.NewStore(pool), entered: make(chan struct{}), release: make(chan struct{})}
	subscribeH := handlers.NewSubscribeHandler(
		blocked, interests.NewStore(pool),
		handlers.NoSuppressions{}, audit.New(pool), cfg.BaseURL, slog.Default(),
	)

	// mountAndServe calls requireSession/requireAdmin immediately while
	// mounting the (unused-by-this-test) session/admin-guarded routes, so a
	// nil func here would panic before ListenAndServe even starts.
	passthrough := func(next http.Handler) http.Handler { return next }

	// #0054: a real *seo.Site, built the same way production does.
	site, err := buildSEOSite(cfg, nil)
	if err != nil {
		t.Fatalf("build seo site: %v", err)
	}

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		// mountAndServe now returns once its SIGTERM-driven graceful
		// shutdown completes, rather than blocking forever — this test is
		// the proof of that; the older mountAndServe-based wiring tests in
		// this package still never send it a signal, so they remain
		// abandoned goroutines for the life of the binary exactly as their
		// own comments describe. Delivering a SIGTERM to this process (a
		// few lines down) also reaches THEIR already-registered sigCh —
		// harmless, since their own assertions have already completed by
		// the time this test runs and each mountAndServe call only ever
		// shuts down its own listener/handler.
		errCh <- mountAndServe(cfg, pool,
			nil, nil, nil, nil, nil, nil, /* adminInterestsH: not exercised */
			nil, /* adminSubscribersH: not exercised */
			nil, /* adminPendingH: not exercised */
			nil, /* adminSuppressionsH: not exercised */
			nil, /* adminDeliverabilityH: not exercised */
			nil, /* adminCampaignsH: not exercised */
			nil, /* adminCampaignAudienceH: not exercised */
			nil, /* adminCampaignPreviewH: not exercised */
			nil, /* adminCampaignPreflightH: not exercised */
			nil, /* adminCampaignStatsH: not exercised */
			nil, /* adminWorkshopsH: not exercised */
			nil, /* adminDashboardH: not exercised */
			nil, nil, subscribeH,
			nil, nil, nil, nil, nil, nil, /* publicInterestsH, preferencesH, confirmH, unsubscribeH, publicWorkshopsH, publicListStatsH: not exercised by this test */
			nil, /* sesNotifyH: not exercised by this test */
			nil, /* sendWorker: not exercised by this test */
			nil, /* outboxWorker: not exercised */
			site,
			passthrough, passthrough, nil, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, baseURL, errCh, ready)

	email := fmt.Sprintf("zz-sigterm-%d@example.com", testdb.Unique())
	body, err := json.Marshal(map[string]any{
		"email":       email,
		"interests":   []string{},
		"website":     "",
		"rendered_at": time.Now().Add(-5 * time.Second).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/subscribe", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/subscribe: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	// Wait for the mutation to actually be in flight — the moment a real
	// SIGTERM (about to be sent below) needs to interrupt.
	select {
	case <-blocked.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Create was never entered — nothing in flight to interrupt; test invalid")
	}

	if subscriberExistsWiring(t, pool, email) {
		t.Fatal("a subscriber row already exists before shutdown — Create should still be blocked; test invalid")
	}
	t.Logf("before SIGTERM: Create is blocked, no subscriber row yet")

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill self: %v", err)
	}

	// mountAndServe should return promptly, well under
	// shutdownServerTimeout+subscribeCloseTimeout (#0087 split what used to
	// be one shutdownTimeout): srv.Shutdown is fast (no handler blocks on a
	// network call), and subscribeH.Close's sendCtx cancellation interrupts
	// the blocked Create almost immediately rather than waiting for
	// release (which this test never closes).
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("mountAndServe returned %v after SIGTERM, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mountAndServe did not return within 5s of SIGTERM — graceful shutdown appears to be hanging")
	}

	if subscriberExistsWiring(t, pool, email) {
		t.Fatal("a subscriber row was created after SIGTERM-driven shutdown — an interrupted Create must not have reached the database")
	}
	t.Logf("after SIGTERM-driven shutdown: no half-created row left behind")

	// Retry, immediately, against the same address: must succeed for real
	// now. Driven directly against a fresh handler (not a second real
	// listener — that mechanism is already covered by
	// TestMountAndServe_RateLimitsSubscribe and by the unit-level sibling
	// test) so this test stays focused on the one thing it's uniquely
	// proving: the real-signal path.
	retryH := handlers.NewSubscribeHandler(
		subscribers.NewStore(pool), interests.NewStore(pool),
		handlers.NoSuppressions{}, nil, cfg.BaseURL, slog.Default(),
	)
	defer func() {
		// wiringDBOpTimeout (#0084): Close's own work here is a fast DB
		// transaction (Create's insert+claim+enqueue), not a long-running
		// network send.
		closeCtx, closeCancel := context.WithTimeout(context.Background(), wiringDBOpTimeout)
		defer closeCancel()
		if err := retryH.Close(closeCtx); err != nil {
			t.Errorf("retryH.Close: %v", err)
		}
	}()
	mux2 := http.NewServeMux()
	mux2.HandleFunc("POST /api/subscribe", retryH.Subscribe)
	rec := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	mux2.ServeHTTP(rec, req2)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d, want 202", rec.Code)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !subscriberExistsWiring(t, pool, email) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !subscriberExistsWiring(t, pool, email) {
		t.Fatal("retry: no subscriber row was created — the interrupted first attempt must not have silenced a real retry")
	}
	if got := outboundQueueCountWiring(t, pool, email); got != 1 {
		t.Fatalf("retry: outbound_queue rows for %q = %d, want exactly 1", email, got)
	}
	t.Logf("retry: subscriber created and confirmation enqueued — the visitor's next attempt is no longer silenced")
}

// subscriberExistsWiring reports whether a subscribers row exists for
// email — this file's own copy of internal/handlers' subscriberExists
// (unreachable from here), reading directly so this test can observe the
// row from outside either package, the same way an operator's own query
// would.
func subscriberExistsWiring(t *testing.T, pool *pgxpool.Pool, email string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscribers WHERE email = $1`, email,
	).Scan(&n); err != nil {
		t.Fatalf("count subscriber %s: %v", email, err)
	}
	return n > 0
}

// outboundQueueCountWiring counts outbound_queue rows for recipient — this
// file's own copy of internal/handlers' outboundQueueCountFor (unreachable
// from here).
func outboundQueueCountWiring(t *testing.T, pool *pgxpool.Pool, recipient string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE recipient = $1`, recipient,
	).Scan(&n); err != nil {
		t.Fatalf("count outbound_queue for %s: %v", recipient, err)
	}
	return n
}
