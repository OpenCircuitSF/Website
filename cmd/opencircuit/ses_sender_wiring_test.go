// ses_sender_wiring_test.go proves #0472's newSESSender decision — the fix
// for "there is no supported way to boot without SES" — at the exact call
// site servePostgres uses, construction-only (no network, no database), the
// same shape worker_wiring_test.go already uses for
// newSendStoreIfEnabled/newSendWorkerIfEnabled.
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// TestNewSESSender_MailerNoOp_ReturnsNoOpMailer proves MAILER_NOOP=true
// still selects noOpMailingMailer, unaffected by #0472's new case — this is
// the pre-existing behavior and must not have moved.
func TestNewSESSender_MailerNoOp_ReturnsNoOpMailer(t *testing.T) {
	cfg := &config.Config{MailerNoOp: true, SendWorkerEnabled: false}
	sender, err := newSESSender(context.Background(), cfg)
	if err != nil {
		t.Fatalf("newSESSender: %v", err)
	}
	if _, ok := sender.(noOpMailingMailer); !ok {
		t.Fatalf("newSESSender under MAILER_NOOP=true = %T, want noOpMailingMailer", sender)
	}
}

// TestNewSESSender_SendWorkerDisabledAndSESUnconfigured_ReturnsUnconfiguredMailer
// is the core #0472 fix: CLAUDE.md §10 item 2 prescribes
// "SEND_WORKER_ENABLED=false plus an unconfigured SES" as how to turn SES
// off in production. Before this issue, that combination made servePostgres
// return an error and the service never started (mailing.NewSESMailer
// refuses to construct without SES_CONFIGURATION_SET). It must now
// construct cleanly.
func TestNewSESSender_SendWorkerDisabledAndSESUnconfigured_ReturnsUnconfiguredMailer(t *testing.T) {
	cfg := &config.Config{
		SendWorkerEnabled:   false,
		SESConfigurationSet: "",
		AWSRegion:           "us-east-1",
		EmailFrom:           "hello@example-oc-test.com",
	}
	sender, err := newSESSender(context.Background(), cfg)
	if err != nil {
		t.Fatalf("newSESSender: %v, want nil error — CLAUDE.md §10's prescribed no-SES recipe must actually boot", err)
	}
	if _, ok := sender.(*mailing.UnconfiguredMailer); !ok {
		t.Fatalf("newSESSender = %T, want *mailing.UnconfiguredMailer", sender)
	}

	// And it must fail LOUDLY at the point of send (#0472 criterion 3), not
	// silently discard the way noOpMailingMailer deliberately does.
	if _, sendErr := sender.Send(context.Background(), mailing.Message{To: "a@b.com", TextBody: "x"}); !errors.Is(sendErr, mailing.ErrSESUnconfigured) {
		t.Errorf("Send() = %v, want ErrSESUnconfigured", sendErr)
	}
}

// TestNewSESSender_SendWorkerEnabledDefaultAndSESUnconfigured_StillFailsLoudAtBoot
// is the regression this issue must NOT introduce: the common accidental
// misconfiguration (SES_CONFIGURATION_SET simply forgotten, everything else
// left at its default) must still refuse to construct, exactly as before
// #0472 — SEND_WORKER_ENABLED defaults to true, so this is the ordinary
// case, not an opt-in one. This is the "do not weaken" half of criterion 2:
// the new case requires BOTH conditions, so a plain missing-config mistake
// is not silently reinterpreted as "no email, on purpose."
func TestNewSESSender_SendWorkerEnabledDefaultAndSESUnconfigured_StillFailsLoudAtBoot(t *testing.T) {
	cfg := &config.Config{
		SendWorkerEnabled:   true, // the default; operator did not opt in
		SESConfigurationSet: "",
		AWSRegion:           "us-east-1",
		EmailFrom:           "hello@example-oc-test.com",
	}
	_, err := newSESSender(context.Background(), cfg)
	if err == nil {
		t.Fatal("newSESSender = nil error, want a construction error — SEND_WORKER_ENABLED=true (the default) must not tolerate an unconfigured SES")
	}
	if !strings.Contains(err.Error(), "SES_CONFIGURATION_SET") {
		t.Errorf("newSESSender error = %q, want it to name SES_CONFIGURATION_SET", err.Error())
	}
}

// TestNewSESSender_SendWorkerDisabledButSESConfigured_ConstructsRealMailer
// proves the new case is scoped to "SES is unconfigured," not to
// "SEND_WORKER_ENABLED=false" alone — a genuinely SES-configured second
// instance (CLAUDE.md §10 item 4's scaling story) must still get the real
// mailing.SESMailer, not the loud-failure stand-in.
func TestNewSESSender_SendWorkerDisabledButSESConfigured_ConstructsRealMailer(t *testing.T) {
	cfg := &config.Config{
		SendWorkerEnabled:   false,
		SESConfigurationSet: "opencircuit-transactional",
		AWSRegion:           "us-east-1",
		EmailFrom:           "hello@example-oc-test.com",
	}
	sender, err := newSESSender(context.Background(), cfg)
	if err != nil {
		t.Fatalf("newSESSender: %v, want a real *mailing.SESMailer to construct", err)
	}
	if _, ok := sender.(*mailing.SESMailer); !ok {
		t.Fatalf("newSESSender = %T, want *mailing.SESMailer — a configured SES must not be swapped for the loud-failure stand-in", sender)
	}
}
