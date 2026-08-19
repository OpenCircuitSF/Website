package mailing

import (
	"context"
	"errors"
	"testing"
)

func TestRecordingMailer_RecordsMessagesAndReturnsIncrementingIDs(t *testing.T) {
	var m RecordingMailer

	id1, err := m.Send(context.Background(), Message{To: "a@example.com", Subject: "one"})
	if err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	id2, err := m.Send(context.Background(), Message{To: "b@example.com", Subject: "two"})
	if err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	if id1 == id2 {
		t.Errorf("expected distinct message IDs, got %q twice", id1)
	}

	sent := m.Sent()
	if len(sent) != 2 {
		t.Fatalf("Sent() = %d messages, want 2", len(sent))
	}
	if sent[0].Subject != "one" || sent[1].Subject != "two" {
		t.Errorf("Sent() out of order or wrong content: %+v", sent)
	}
}

func TestRecordingMailer_SetErrorFailsSendAndDoesNotRecord(t *testing.T) {
	var m RecordingMailer
	wantErr := errors.New("simulated failure")
	m.SetError(wantErr)

	if _, err := m.Send(context.Background(), Message{To: "a@example.com"}); !errors.Is(err, wantErr) {
		t.Errorf("Send error = %v, want %v", err, wantErr)
	}
	if len(m.Sent()) != 0 {
		t.Errorf("Sent() = %d, want 0 (a failed send must not be recorded)", len(m.Sent()))
	}

	m.SetError(nil)
	if _, err := m.Send(context.Background(), Message{To: "a@example.com"}); err != nil {
		t.Fatalf("Send after clearing error: %v", err)
	}
	if len(m.Sent()) != 1 {
		t.Errorf("Sent() = %d, want 1 after clearing the error", len(m.Sent()))
	}
}

func TestRecordingMailer_SentReturnsACopy(t *testing.T) {
	var m RecordingMailer
	if _, err := m.Send(context.Background(), Message{Subject: "original"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	sent := m.Sent()
	sent[0].Subject = "mutated"

	if got := m.Sent()[0].Subject; got != "original" {
		t.Errorf("internal state mutated through Sent()'s return value: got %q, want %q", got, "original")
	}
}
