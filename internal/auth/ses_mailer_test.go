package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// sesMailerTestPool returns the package's shared pool or skips — mirrors
// every other DB-backed test file's own testPool helper in this repo.
func sesMailerTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

func uniqueMailerRecipient(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("ses-mailer-test-%d@example.com", testdb.Unique())
}

// outboundQueueRowFor reads the single outbound_queue row for recipient —
// #0126's replacement for the old RecordingMailer-based assertions: this
// file now asserts on what SESMailer enqueues, not on a rendered message,
// since rendering is internal/mailing.OutboxWorker's job.
func outboundQueueRowFor(t *testing.T, pool *pgxpool.Pool, recipient string) (kind, payload string) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE recipient = $1`, recipient,
	).Scan(&n); err != nil {
		t.Fatalf("counting outbound_queue rows for %q: %v", recipient, err)
	}
	if n != 1 {
		t.Fatalf("outbound_queue rows for %q = %d, want 1", recipient, n)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT kind, payload::text FROM outbound_queue WHERE recipient = $1`, recipient,
	).Scan(&kind, &payload); err != nil {
		t.Fatalf("reading outbound_queue row for %q: %v", recipient, err)
	}
	return kind, payload
}

// TestSESMailer_SendVerification_Enqueues proves SendVerification enqueues
// a registration row carrying the token and TTL as payload — #0126:
// rendering (subject/body/link) moved to internal/mailing.OutboxWorker's
// own tests, since this type no longer renders anything.
func TestSESMailer_SendVerification_Enqueues(t *testing.T) {
	pool := sesMailerTestPool(t)
	m := NewSESMailer(pool, &config.Config{BaseURL: "https://opencircuitsf.com"})

	recipient := uniqueMailerRecipient(t)
	if err := m.SendVerification(context.Background(), recipient, "tok-123"); err != nil {
		t.Fatalf("SendVerification: %v", err)
	}

	kind, payloadText := outboundQueueRowFor(t, pool, recipient)
	if kind != "registration" {
		t.Errorf("kind = %q, want %q", kind, "registration")
	}
	var payload registrationPayload
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		t.Fatalf("unmarshalling payload %q: %v", payloadText, err)
	}
	if payload.Token != "tok-123" {
		t.Errorf("payload.Token = %q, want %q", payload.Token, "tok-123")
	}
	if payload.TTLSeconds != int64(registrationTTL.Seconds()) {
		t.Errorf("payload.TTLSeconds = %d, want %d", payload.TTLSeconds, int64(registrationTTL.Seconds()))
	}
}

// TestSESMailer_SendRecovery_Enqueues is SendVerification's twin for
// recovery mail.
func TestSESMailer_SendRecovery_Enqueues(t *testing.T) {
	pool := sesMailerTestPool(t)
	m := NewSESMailer(pool, &config.Config{BaseURL: "https://opencircuitsf.com"})

	recipient := uniqueMailerRecipient(t)
	if err := m.SendRecovery(context.Background(), recipient, "rec-789"); err != nil {
		t.Fatalf("SendRecovery: %v", err)
	}

	kind, payloadText := outboundQueueRowFor(t, pool, recipient)
	if kind != "recovery" {
		t.Errorf("kind = %q, want %q", kind, "recovery")
	}
	var payload recoveryPayload
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		t.Fatalf("unmarshalling payload %q: %v", payloadText, err)
	}
	if payload.Token != "rec-789" {
		t.Errorf("payload.Token = %q, want %q", payload.Token, "rec-789")
	}
	if payload.TTLSeconds != int64(recoveryTTL.Seconds()) {
		t.Errorf("payload.TTLSeconds = %d, want %d", payload.TTLSeconds, int64(recoveryTTL.Seconds()))
	}
}

// TestSESMailer_SendSessionsRevoked_Enqueues is SendVerification's twin for
// the sessions-revoked notice.
func TestSESMailer_SendSessionsRevoked_Enqueues(t *testing.T) {
	pool := sesMailerTestPool(t)
	m := NewSESMailer(pool, &config.Config{BaseURL: "https://opencircuitsf.com"})

	recipient := uniqueMailerRecipient(t)
	at := time.Date(2026, 8, 6, 15, 4, 5, 0, time.UTC)
	if err := m.SendSessionsRevoked(context.Background(), recipient, at); err != nil {
		t.Fatalf("SendSessionsRevoked: %v", err)
	}

	kind, payloadText := outboundQueueRowFor(t, pool, recipient)
	if kind != "sessions_revoked" {
		t.Errorf("kind = %q, want %q", kind, "sessions_revoked")
	}
	var payload sessionsRevokedPayload
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		t.Fatalf("unmarshalling payload %q: %v", payloadText, err)
	}
	if !payload.At.Equal(at) {
		t.Errorf("payload.At = %v, want %v", payload.At, at)
	}
}

// TestSESMailer_ContextCancelled verifies the context is honored before the
// enqueue is attempted.
func TestSESMailer_ContextCancelled(t *testing.T) {
	pool := sesMailerTestPool(t)
	m := NewSESMailer(pool, &config.Config{BaseURL: "https://opencircuitsf.com"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	recipient := uniqueMailerRecipient(t)
	if err := m.SendVerification(ctx, recipient, "tok"); err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbound_queue WHERE recipient = $1`, recipient).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Error("a row was enqueued despite a cancelled context")
	}
}

// TestNewSESMailer_InstallsBaseURL verifies the constructor maps BaseURL
// from config.
func TestNewSESMailer_InstallsBaseURL(t *testing.T) {
	pool := sesMailerTestPool(t)
	cfg := &config.Config{BaseURL: "https://opencircuitsf.com"}
	m := NewSESMailer(pool, cfg)

	if m.baseURL != cfg.BaseURL {
		t.Errorf("baseURL = %q, want %q", m.baseURL, cfg.BaseURL)
	}
}

// TestNoOpMailer verifies the stub satisfies Mailer and never errors.
func TestNoOpMailer(t *testing.T) {
	var m Mailer = NoOpMailer{BaseURL: "https://opencircuitsf.com"}
	if err := m.SendVerification(context.Background(), "a@example.com", "t"); err != nil {
		t.Errorf("NoOpMailer.SendVerification: %v", err)
	}
	if err := m.SendRecovery(context.Background(), "a@example.com", "t"); err != nil {
		t.Errorf("NoOpMailer.SendRecovery: %v", err)
	}
	if err := m.SendSessionsRevoked(context.Background(), "a@example.com", time.Now()); err != nil {
		t.Errorf("NoOpMailer.SendSessionsRevoked: %v", err)
	}
}
