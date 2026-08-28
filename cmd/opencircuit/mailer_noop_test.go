package main

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// TestMailerNoOpAllowed covers the host allowlist carried into #0026 from
// #0027's review: MAILER_NOOP may only be honored when BASE_URL's host is
// exactly localhost or 127.0.0.1.
func TestMailerNoOpAllowed(t *testing.T) {
	cases := []struct {
		baseURL string
		want    bool
	}{
		{"http://localhost:8080", true},
		{"https://localhost", true},
		{"http://127.0.0.1:8080", true},
		{"http://127.0.0.1", true},
		{"https://www.opencircuitsf.com", false},
		{"https://opencircuitsf.com", false},
		{"http://sub.localhost:8080", false}, // hostname must match exactly, not merely contain it
		{"http://127.0.0.1.evil.com", false},
		{"not a url", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.baseURL, func(t *testing.T) {
			if got := mailerNoOpAllowed(c.baseURL); got != c.want {
				t.Errorf("mailerNoOpAllowed(%q) = %v, want %v", c.baseURL, got, c.want)
			}
		})
	}
}

// TestCheckMailerNoOp_Unset is a no-op regardless of BASE_URL.
func TestCheckMailerNoOp_Unset(t *testing.T) {
	cfg := &config.Config{MailerNoOp: false, BaseURL: "https://www.opencircuitsf.com"}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	if err := checkMailerNoOp(cfg, logger); err != nil {
		t.Fatalf("checkMailerNoOp with MailerNoOp=false: got err=%v, want nil", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log output when MAILER_NOOP is unset, got %q", buf.String())
	}
}

// TestCheckMailerNoOp_RefusedOutsideLocalhost is the hard-refusal half of
// the guard: MAILER_NOOP=true against a production-looking BASE_URL must
// fail startup, not silently disable email.
func TestCheckMailerNoOp_RefusedOutsideLocalhost(t *testing.T) {
	cfg := &config.Config{MailerNoOp: true, BaseURL: "https://www.opencircuitsf.com"}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	err := checkMailerNoOp(cfg, logger)
	if err == nil {
		t.Fatal("checkMailerNoOp with MailerNoOp=true and a non-local BASE_URL: got nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), cfg.BaseURL) {
		t.Errorf("error %q does not name the offending BASE_URL", err.Error())
	}
}

// TestCheckMailerNoOp_WarnsOnceWhenAllowed is the loud-startup-warning half
// of the guard: MAILER_NOOP=true against localhost must succeed, but leave
// a durable log line since nothing about the request path (the uniform 202)
// can otherwise reveal that email is disabled.
func TestCheckMailerNoOp_WarnsOnceWhenAllowed(t *testing.T) {
	cfg := &config.Config{MailerNoOp: true, BaseURL: "http://localhost:8080"}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	if err := checkMailerNoOp(cfg, logger); err != nil {
		t.Fatalf("checkMailerNoOp with MailerNoOp=true and BASE_URL=localhost: got err=%v, want nil", err)
	}
	out := buf.String()
	if !strings.Contains(out, "MAILER_NOOP") {
		t.Errorf("expected a warning log mentioning MAILER_NOOP, got %q", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("expected the log line at WARN level, got %q", out)
	}
}

// TestNoOpMailingMailer_Send_LogsRenderedBody is #0277's regression test.
// #0260 removed the only callers of auth.NoOpMailer.SendVerification/
// SendRecovery (the methods that used to log the magic link), so
// registration/recovery mail now drains through noOpMailingMailer.Send —
// which, before this fix, logged only msg.Subject and silently dropped the
// link. This asserts on the actual log OUTPUT (via log.SetOutput, restored
// on cleanup) rather than on the mailer's arguments: the defect was that
// the body stopped being logged at all, which an argument assertion on the
// call would not have caught (the call was still happening; only the log
// line was thin).
func TestNoOpMailingMailer_Send_LogsRenderedBody(t *testing.T) {
	var buf bytes.Buffer
	prevOutput := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(prevOutput)
		log.SetFlags(prevFlags)
	})

	const marker = "https://opencircuitsf.com/register/verify?token=super-secret-magic-link-token"
	msg := mailing.Message{
		To:       "someone@example.com",
		Subject:  "Confirm your Open Circuit SF account",
		HTMLBody: "<p>ignored for this assertion</p>",
		TextBody: "Click to finish registering:\n\n" + marker + "\n\nThis link expires in 5 minutes.",
	}

	m := noOpMailingMailer{}
	if _, err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("noOpMailingMailer.Send: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, msg.Subject) {
		t.Errorf("log output %q does not contain the subject %q", out, msg.Subject)
	}
	if !strings.Contains(out, marker) {
		t.Errorf("log output %q does not contain the rendered magic link %q — the regression #0277 fixes", out, marker)
	}
}

// TestNoOpMailingMailer_Send_LogsBodyRegardlessOfKind proves the body is
// logged unconditionally, not gated on any notion of "kind" — msg carries
// no kind field at all by the time it reaches the mailer, but this locks in
// the behavior for a message shaped like a non-auth send (e.g. a welcome or
// campaign test-send) so a future change can't reintroduce a per-kind
// allowlist by keying off Subject or some other proxy for kind.
func TestNoOpMailingMailer_Send_LogsBodyRegardlessOfKind(t *testing.T) {
	var buf bytes.Buffer
	prevOutput := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prevOutput) })

	msg := mailing.Message{
		To:       "subscriber@example.com",
		Subject:  "Welcome to Open Circuit SF",
		TextBody: "Thanks for subscribing. Manage your preferences at https://opencircuitsf.com/preferences?token=abc123",
	}

	m := noOpMailingMailer{}
	if _, err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("noOpMailingMailer.Send: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "abc123") {
		t.Errorf("log output %q does not contain the welcome email's body content", out)
	}
}
