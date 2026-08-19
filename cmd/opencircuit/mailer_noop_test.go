package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
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
