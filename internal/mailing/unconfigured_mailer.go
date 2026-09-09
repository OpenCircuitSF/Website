package mailing

import (
	"context"
	"errors"
)

// ErrSESUnconfigured is UnconfiguredMailer.Send's only return value. Wrapped
// (via %w) rather than reissued as a bare string, so a caller can
// distinguish "SES was never configured on this instance" from any other
// send failure with errors.Is.
var ErrSESUnconfigured = errors.New("mailing: SES is not configured on this instance")

// UnconfiguredMailer is a Mailer for an instance that has deliberately been
// booted with no SES configuration at all (#0472) — CLAUDE.md §10 item 2's
// "Turn SES off in production is expressed as SEND_WORKER_ENABLED=false plus
// an unconfigured SES" recipe. Before #0472, that recipe did not actually
// work: NewSESMailer refuses to construct without SES_CONFIGURATION_SET, so
// the Postgres serve path had no way to boot at all in that configuration —
// see cmd/opencircuit's newSESSender, the only place this type is
// constructed.
//
// It is deliberately NOT the same shape as cmd/opencircuit's
// noOpMailingMailer (MAILER_NOOP's backing type). noOpMailingMailer's whole
// job is to look like success — it logs the message and returns a fake
// "noop" message ID, which is exactly right for local development but is
// confined to localhost by checkMailerNoOp (CLAUDE.md §10) precisely
// because it would silently swallow every outbound email anywhere else.
// UnconfiguredMailer does the opposite: every Send call fails, loudly, with
// ErrSESUnconfigured, so nothing this project sends is ever silently
// discarded (CLAUDE.md §9's standing concern). A failed Send is not a new
// failure mode for a caller to handle — internal/mailing.OutboxWorker and
// mailing.Worker already retry a Send error on outbound_queue's existing
// backoff schedule and eventually mark the row abandoned (PRD §6.11;
// CLAUDE.md §10's own "a magic link requested before SES exists is simply
// burned — request it after" already documents exactly this outcome for
// the pre-SES-setup window that motivated MAILER_NOOP in the first place).
//
// UnconfiguredMailer never touches the network and never blocks — Send
// returns immediately, so a sustained "no SES configured" state costs
// nothing beyond the queue rows it accumulates, unlike a real misconfigured
// SES client that would have to time out on every attempt.
type UnconfiguredMailer struct{}

// NewUnconfiguredMailer constructs an UnconfiguredMailer. It takes no
// arguments and can never fail — unlike NewSESMailer, there is no
// configuration to validate, which is the entire point: this type exists
// for the instance that has none.
func NewUnconfiguredMailer() *UnconfiguredMailer {
	return &UnconfiguredMailer{}
}

// Send always fails with ErrSESUnconfigured. It never returns a message ID
// and never logs msg's contents — logging a message this mailer is about to
// report as failed-to-send would read as evidence of delivery to anyone
// scanning logs for confirmation, which is the opposite of "fails loudly."
// The caller's own error handling (OutboxWorker.finishFailed,
// mailing.Worker's classifySendError path) is what surfaces the failure.
func (UnconfiguredMailer) Send(_ context.Context, _ Message) (string, error) {
	return "", ErrSESUnconfigured
}

// Ensure UnconfiguredMailer satisfies Mailer at compile time.
var _ Mailer = UnconfiguredMailer{}
