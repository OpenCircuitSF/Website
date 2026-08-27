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

// #0289: RecordingMailer used to restart its id counter at 1 per instance,
// so two instances each minted "recorded-1" as their first id. Proving the
// absence of #0269's symptom (a bogus Bounced count) is not evidence here,
// since nothing in this package writes email_events with these ids today —
// this asserts the ids directly, across two separate instances, the shape
// that actually collided.
func TestRecordingMailer_IDsDoNotCollideAcrossInstances(t *testing.T) {
	var m1, m2 RecordingMailer

	id1a, err := m1.Send(context.Background(), Message{To: "a@example.com"})
	if err != nil {
		t.Fatalf("m1 Send 1: %v", err)
	}
	id2a, err := m2.Send(context.Background(), Message{To: "b@example.com"})
	if err != nil {
		t.Fatalf("m2 Send 1: %v", err)
	}
	if id1a == id2a {
		t.Fatalf("two fresh RecordingMailer instances' first ids collided: both %q", id1a)
	}

	id1b, err := m1.Send(context.Background(), Message{To: "c@example.com"})
	if err != nil {
		t.Fatalf("m1 Send 2: %v", err)
	}
	id2b, err := m2.Send(context.Background(), Message{To: "d@example.com"})
	if err != nil {
		t.Fatalf("m2 Send 2: %v", err)
	}

	seen := map[string]bool{id1a: true, id2a: true, id1b: true, id2b: true}
	if len(seen) != 4 {
		t.Fatalf("expected 4 distinct ids across two instances' two sends each, got %v", seen)
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
