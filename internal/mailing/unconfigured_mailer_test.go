package mailing

import (
	"context"
	"errors"
	"testing"
)

// TestUnconfiguredMailer_Send_FailsLoudly proves #0472's core requirement:
// an instance with no SES configuration must fail loudly at the point of
// send, never silently discard or fake a success. Unlike noOpMailingMailer
// (cmd/opencircuit), which is designed to look like success for local
// development, UnconfiguredMailer's Send must always return an error a
// caller can act on.
func TestUnconfiguredMailer_Send_FailsLoudly(t *testing.T) {
	m := NewUnconfiguredMailer()
	id, err := m.Send(context.Background(), Message{
		To:       "alice@example.com",
		Subject:  "Hi",
		TextBody: "body",
	})
	if err == nil {
		t.Fatal("Send() error = nil, want ErrSESUnconfigured")
	}
	if !errors.Is(err, ErrSESUnconfigured) {
		t.Errorf("Send() error = %v, want it to wrap ErrSESUnconfigured", err)
	}
	if id != "" {
		t.Errorf("Send() messageID = %q, want empty — a non-empty id on a failed send would look like a fake success", id)
	}
}

// TestUnconfiguredMailer_NeverBlocks proves Send returns immediately without
// consulting ctx at all — a cancelled or already-expired context must not
// change the outcome, since there is no network call to bound.
func TestUnconfiguredMailer_NeverBlocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	m := NewUnconfiguredMailer()
	_, err := m.Send(ctx, Message{To: "alice@example.com", TextBody: "body"})
	if !errors.Is(err, ErrSESUnconfigured) {
		t.Errorf("Send() with a cancelled context = %v, want ErrSESUnconfigured regardless", err)
	}
}

// TestUnconfiguredMailer_SatisfiesMailer is a compile-time-shaped assertion
// exercised at runtime too: NewUnconfiguredMailer's return value must be
// usable everywhere a Mailer is expected (cmd/opencircuit wires it as
// sesSender, the same seam the real SESMailer and RecordingMailer fill).
func TestUnconfiguredMailer_SatisfiesMailer(t *testing.T) {
	var m Mailer = NewUnconfiguredMailer()
	if _, err := m.Send(context.Background(), Message{}); !errors.Is(err, ErrSESUnconfigured) {
		t.Errorf("Send() via the Mailer interface = %v, want ErrSESUnconfigured", err)
	}
}
