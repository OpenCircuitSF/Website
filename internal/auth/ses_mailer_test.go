package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// newTestMailer returns an SESMailer wired with a RecordingMailer sender, and
// the recorder itself so a test can inspect what was sent.
func newTestMailer() (*SESMailer, *mailing.RecordingMailer) {
	rec := &mailing.RecordingMailer{}
	cfg := &config.Config{BaseURL: "https://opencircuitsf.com"}
	return NewSESMailerWithSender(rec, cfg), rec
}

// TestSESMailer_SendVerification_InjectedSender asserts the composed message
// handed to the underlying mailing.Mailer for a verification email.
func TestSESMailer_SendVerification_InjectedSender(t *testing.T) {
	m, rec := newTestMailer()

	if err := m.SendVerification(context.Background(), "alice@example.com", "tok-123"); err != nil {
		t.Fatalf("SendVerification: %v", err)
	}

	sent := rec.Sent()
	if len(sent) != 1 {
		t.Fatalf("messages sent = %d, want 1", len(sent))
	}
	msg := sent[0]

	if msg.To != "alice@example.com" {
		t.Errorf("To = %q, want %q", msg.To, "alice@example.com")
	}
	if msg.Subject != "Verify your Open Circuit SF account" {
		t.Errorf("Subject = %q", msg.Subject)
	}
	// The email link points at the SPA browser path (/register/verify), not
	// the /auth/* JSON API endpoint. The SPA reads the token and calls
	// GET /auth/register/verify to fetch creation options.
	wantLink := "https://opencircuitsf.com/register/verify?token=tok-123"
	if !strings.Contains(msg.TextBody, wantLink) {
		t.Errorf("message missing verification link %q\nbody:\n%s", wantLink, msg.TextBody)
	}
	if msg.HTMLBody != "" {
		t.Errorf("HTMLBody = %q, want empty (auth transactional mail is text-only)", msg.HTMLBody)
	}
}

// TestSESMailer_SendRecovery_InjectedSender asserts the recovery email
// recipient and link.
func TestSESMailer_SendRecovery_InjectedSender(t *testing.T) {
	m, rec := newTestMailer()

	if err := m.SendRecovery(context.Background(), "bob@example.com", "rec-789"); err != nil {
		t.Fatalf("SendRecovery: %v", err)
	}

	sent := rec.Sent()
	if len(sent) != 1 {
		t.Fatalf("messages sent = %d, want 1", len(sent))
	}
	msg := sent[0]
	if msg.To != "bob@example.com" {
		t.Errorf("To = %q, want %q", msg.To, "bob@example.com")
	}
	if msg.Subject != "Recover your Open Circuit SF account" {
		t.Errorf("Subject = %q", msg.Subject)
	}
	wantLink := "https://opencircuitsf.com/recover/verify?token=rec-789"
	if !strings.Contains(msg.TextBody, wantLink) {
		t.Errorf("message missing recovery link %q\nbody:\n%s", wantLink, msg.TextBody)
	}
}

// TestSESMailer_SendSessionsRevoked_InjectedSender asserts the "sign out
// everywhere" notification: no token/link-with-credentials, the account's
// EXISTING passkey is what still works (never "a new passkey" — a prior
// draft of this copy wrongly implied re-enrollment was required), and the
// timestamp is included.
func TestSESMailer_SendSessionsRevoked_InjectedSender(t *testing.T) {
	m, rec := newTestMailer()

	at := time.Date(2026, 8, 6, 15, 4, 5, 0, time.UTC)
	if err := m.SendSessionsRevoked(context.Background(), "dana@example.com", at); err != nil {
		t.Fatalf("SendSessionsRevoked: %v", err)
	}

	sent := rec.Sent()
	if len(sent) != 1 {
		t.Fatalf("messages sent = %d, want 1", len(sent))
	}
	msg := sent[0]
	if msg.To != "dana@example.com" {
		t.Errorf("To = %q, want %q", msg.To, "dana@example.com")
	}
	if msg.Subject != "All sessions signed out" {
		t.Errorf("Subject = %q", msg.Subject)
	}
	if !strings.Contains(msg.TextBody, "existing passkey") {
		t.Errorf("message must tell the user their EXISTING passkey still works\nbody:\n%s", msg.TextBody)
	}
	if strings.Contains(msg.TextBody, "a new passkey") || strings.Contains(msg.TextBody, "sign in with a new") {
		t.Errorf("message must NOT instruct signing in with a new passkey\nbody:\n%s", msg.TextBody)
	}
	if !strings.Contains(msg.TextBody, "https://opencircuitsf.com") {
		t.Errorf("message missing base URL\nbody:\n%s", msg.TextBody)
	}
	if !strings.Contains(msg.TextBody, at.Format(time.RFC1123Z)) {
		t.Errorf("message missing formatted timestamp\nbody:\n%s", msg.TextBody)
	}
}

// TestSESMailer_ContextCancelled verifies the context is honored before the
// underlying sender is ever called.
func TestSESMailer_ContextCancelled(t *testing.T) {
	m, rec := newTestMailer()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := m.SendVerification(ctx, "alice@example.com", "tok"); err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if len(rec.Sent()) != 0 {
		t.Error("sender was called despite cancelled context")
	}
}

// TestSESMailer_SendErrorIsWrappedWithoutRecipient guards the PII-leak fix
// this file's tests protect (see internal/handlers/logging_test.go's
// TestAuthHandler_MailerErrorDoesNotLeakEmail for the handler-layer version
// of this same guard): SESMailer.send's error wrapping must never embed the
// recipient's address, since RegisterStart/RecoverStart are unauthenticated
// routes that log this error verbatim.
func TestSESMailer_SendErrorIsWrappedWithoutRecipient(t *testing.T) {
	m, rec := newTestMailer()
	rec.SetError(errors.New("simulated SES failure"))

	const victimEmail = "victim@example.com"
	err := m.SendVerification(context.Background(), victimEmail, "tok")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if strings.Contains(err.Error(), victimEmail) {
		t.Errorf("wrapped error leaked the recipient address: %v", err)
	}
	if !strings.Contains(err.Error(), "simulated SES failure") {
		t.Errorf("wrapped error lost the underlying cause: %v", err)
	}
}

// TestNewSESMailerWithSender verifies the constructor maps BaseURL from
// config and installs the given sender — the seam used both by tests in this
// package and by internal/handlers (see NewSESMailerWithSender's doc comment
// for why it replaced NewSESMailerWithAddr).
func TestNewSESMailerWithSender(t *testing.T) {
	rec := &mailing.RecordingMailer{}
	cfg := &config.Config{BaseURL: "https://opencircuitsf.com"}
	m := NewSESMailerWithSender(rec, cfg)

	if m.baseURL != cfg.BaseURL {
		t.Errorf("baseURL = %q, want %q", m.baseURL, cfg.BaseURL)
	}
	if m.sender != mailing.Mailer(rec) {
		t.Error("NewSESMailerWithSender did not install the given sender")
	}
}

// TestNewSESMailer_PropagatesConstructionError verifies auth.NewSESMailer
// fails clearly (rather than returning a half-built mailer) when the
// underlying mailing.NewSESMailer construction fails — here, because
// AWS_REGION/SES_CONFIGURATION_SET/EMAIL_FROM are all unset.
func TestNewSESMailer_PropagatesConstructionError(t *testing.T) {
	_, err := NewSESMailer(context.Background(), &config.Config{})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "AWS_REGION") {
		t.Errorf("error = %q, want it to name the missing configuration", err.Error())
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
