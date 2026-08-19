package mailing

import (
	"context"
	"fmt"
	"sync"
)

// RecordingMailer is a Mailer that never touches the network — it records
// every Send call for assertion in tests, mirroring ShortLinks' recorder
// pattern (PRD §6.6). Safe for concurrent use.
type RecordingMailer struct {
	mu     sync.Mutex
	sent   []Message
	nextID int
	err    error // when set, Send returns this error instead of recording
}

// Send records msg and returns a deterministic, incrementing fake message ID
// ("recorded-1", "recorded-2", ...), or the error configured via SetError if
// one is set.
func (r *RecordingMailer) Send(_ context.Context, msg Message) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return "", r.err
	}
	r.sent = append(r.sent, msg)
	r.nextID++
	return fmt.Sprintf("recorded-%d", r.nextID), nil
}

// Sent returns a copy of every message recorded so far, in send order.
func (r *RecordingMailer) Sent() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Message, len(r.sent))
	copy(out, r.sent)
	return out
}

// SetError makes every subsequent Send call fail with err instead of
// recording, for tests that need to exercise a mailer failure. Pass nil to
// resume recording.
func (r *RecordingMailer) SetError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

// Ensure RecordingMailer satisfies Mailer at compile time.
var _ Mailer = (*RecordingMailer)(nil)
