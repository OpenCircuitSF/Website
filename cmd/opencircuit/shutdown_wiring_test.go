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
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// blockingTestMailer blocks Send until either release is closed (by the
// test) or ctx is cancelled, closing entered the instant Send is called. A
// package-local twin of internal/handlers' own unexported blockingMailer
// test double (unreachable from here) — this package only needs the one
// property it proves: a send genuinely caught in flight.
type blockingTestMailer struct {
	entered chan struct{}
	release chan struct{}
}

func (m *blockingTestMailer) Send(ctx context.Context, _ mailing.Message) (string, error) {
	close(m.entered)
	select {
	case <-m.release:
		return "blocked-then-sent", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// TestMountAndServe_SIGTERMReleasesInFlightClaim is #0081's end-to-end
// proof that mountAndServe's graceful-shutdown wiring works against a REAL
// os.Signal — not merely a direct h.Close(ctx) call, which
// internal/handlers' own
// TestSubscribeHandler_Close_ReleasesInFlightClaimAndRetrySendsImmediately
// already covers at the unit level. This drives a real HTTP listener via
// mountAndServe (the exact function servePostgres calls), sends the
// process a genuine SIGTERM while a confirmation send is caught inside
// mailer.Send, and confirms mountAndServe returns promptly with the claim
// released — matching issues/0081.md's ## Root cause reproduction, except
// this time the graceful-shutdown wiring is exercised instead of skipped.
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

	blocked := &blockingTestMailer{entered: make(chan struct{}), release: make(chan struct{})}
	subscribeH := handlers.NewSubscribeHandler(
		subscribers.NewStore(pool), interests.NewStore(pool), blocked,
		handlers.NoSuppressions{}, nil, audit.New(pool), cfg.BaseURL, slog.Default(),
	)

	// mountAndServe calls requireSession/requireAdmin immediately while
	// mounting the (unused-by-this-test) session/admin-guarded routes, so a
	// nil func here would panic before ListenAndServe even starts.
	passthrough := func(next http.Handler) http.Handler { return next }

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
			nil, /* adminSuppressionsH: not exercised */
			nil, nil, subscribeH,
			nil, nil, nil, nil, /* publicInterestsH, preferencesH, confirmH, unsubscribeH: not exercised by this test */
			passthrough, passthrough, nil, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, baseURL, errCh, ready)

	email := fmt.Sprintf("zz-sigterm-%d@example.com", time.Now().UnixNano())
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

	// Wait for the send to actually be in flight — the moment a real
	// SIGTERM (about to be sent below) needs to interrupt.
	select {
	case <-blocked.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("mailer.Send was never entered — nothing in flight to interrupt; test invalid")
	}

	confirmSentAt := readConfirmSentAt(t, pool, email)
	if confirmSentAt == nil {
		t.Fatal("confirm_sent_at = nil before shutdown, want stamped — the claim was never taken; test invalid")
	}
	t.Logf("before SIGTERM: confirm_sent_at=%v emails actually sent=0", *confirmSentAt)

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill self: %v", err)
	}

	// mountAndServe should return promptly, well under
	// shutdownServerTimeout+subscribeCloseTimeout (#0087 split what used to
	// be one shutdownTimeout): srv.Shutdown is fast (no handler blocks on a
	// network call), and
	// subscribeH.Close's sendCtx cancellation interrupts blocked.Send
	// almost immediately rather than waiting the full sendJobTimeout or
	// for release (which this test never closes).
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("mountAndServe returned %v after SIGTERM, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mountAndServe did not return within 5s of SIGTERM — graceful shutdown appears to be hanging")
	}

	confirmSentAt = readConfirmSentAt(t, pool, email)
	t.Logf("after SIGTERM-driven shutdown: confirm_sent_at=%v", confirmSentAt)
	if confirmSentAt != nil {
		t.Fatalf("confirm_sent_at = %v after SIGTERM-driven shutdown, want nil — the claim must be released so a retry is not silenced for subscribeResendCooldown", *confirmSentAt)
	}

	// Retry, immediately, against the same database row: must send for
	// real now. Driven directly against a fresh handler (not a second real
	// listener — that mechanism is already covered by
	// TestMountAndServe_RateLimitsSubscribe and by the unit-level sibling
	// test) so this test stays focused on the one thing it's uniquely
	// proving: the real-signal path.
	after := &mailing.RecordingMailer{}
	retryH := handlers.NewSubscribeHandler(
		subscribers.NewStore(pool), interests.NewStore(pool), after,
		handlers.NoSuppressions{}, nil, nil, cfg.BaseURL, slog.Default(),
	)
	defer func() {
		// wiringDBOpTimeout (#0084): Close's own work here is a fast DB
		// UPDATE releasing the confirmation claim — the same class of
		// single-statement operation the other wiring tests bound with this
		// constant, not a long-running send (no mailer.Send blocks this
		// handler's Close).
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
	for len(after.Sent()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := len(after.Sent()); got != 1 {
		t.Fatalf("retry sent %d emails, want exactly 1 — the released claim must allow an immediate real send", got)
	}
	t.Logf("retry: 1 real email sent — the visitor's next attempt is no longer silenced")
}

// readConfirmSentAt reads subscribers.confirm_sent_at for email directly,
// bypassing internal/handlers/internal/subscribers so this test can observe
// the claim column from outside either package, the same way an operator's
// own query would.
func readConfirmSentAt(t *testing.T, pool *pgxpool.Pool, email string) *time.Time {
	t.Helper()
	var confirmSentAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT confirm_sent_at FROM subscribers WHERE email = $1`, email,
	).Scan(&confirmSentAt); err != nil {
		t.Fatalf("read confirm_sent_at for %s: %v", email, err)
	}
	return confirmSentAt
}
