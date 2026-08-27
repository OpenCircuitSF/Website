package mailing

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// RecordingMailer is a Mailer that never touches the network — it records
// every Send call for assertion in tests, mirroring ShortLinks' recorder
// pattern (PRD §6.6). Safe for concurrent use.
type RecordingMailer struct {
	mu   sync.Mutex
	sent []Message
	err  error // when set, Send returns this error instead of recording
}

// recordingMailerIDCounter is process-wide, not per-instance: #0289 found
// that a per-instance counter restarting at 1 (the previous shape here) lets
// two RecordingMailer instances mint the same "recorded-1", "recorded-2", ...
// sequence, exactly the collision #0269 item 2 fixed in breakerTestMailer
// after email_events read back Bounced counts from an unrelated campaign
// (email_events is UNIQUE(sns_message_id, recipient), but
// idx_email_events_message_id — the index a lookup by message id actually
// uses — is non-unique, so a colliding id happily returns another run's
// rows).
//
// This is production code (recording_mailer.go has no _test.go suffix), so
// it can't reach for internal/testdb.Unique() the way #0269's own fix and
// worker_store_test.go (#0289 criterion 3) do — internal/mailing must not
// import internal/testdb. atomic.AddInt64 over a package-level counter,
// folded together with os.Getpid(), is the same technique testdb.Unique()
// itself uses, using only the standard library: the low bits make every id
// distinct within this process regardless of how many RecordingMailer
// instances exist, and the pid makes two instances in two concurrently
// running `go test` processes (or two packages of this same test binary)
// unable to collide either.
var recordingMailerIDCounter int64

// nextRecordingMailerID returns a fake SES-shaped message id that is unique
// across every RecordingMailer instance in this process, and across
// concurrently running processes too.
func nextRecordingMailerID() string {
	n := atomic.AddInt64(&recordingMailerIDCounter, 1)
	return fmt.Sprintf("recorded-%d-%d", os.Getpid(), n)
}

// Send records msg and returns a fake message ID unique across every
// RecordingMailer instance in this process ("recorded-<pid>-<n>"), or the
// error configured via SetError if one is set.
func (r *RecordingMailer) Send(_ context.Context, msg Message) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return "", r.err
	}
	r.sent = append(r.sent, msg)
	return nextRecordingMailerID(), nil
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
