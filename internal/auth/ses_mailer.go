package auth

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

// SESMailer is a Mailer that enqueues the three transactional emails
// (registration magic link, recovery magic link, "sessions revoked" notice)
// onto internal/outbox's outbound_queue, rather than sending them directly.
//
// # #0126: this used to send synchronously
//
// Before #0126, SESMailer wrapped internal/mailing.Mailer and called
// sender.Send inline — an in-process SES call with the same durability gap
// #0126's plan describes for the public subscribe endpoint: a process crash
// (or a transient SES outage) between the token being committed and the
// send completing silenced that visitor's registration/recovery attempt
// with nothing to retry it. internal/mailing.OutboxWorker now performs the
// actual send, rendering at send time from the payload this file enqueues
// (registrationPayload/recoveryPayload/sessionsRevokedPayload below —
// matched by JSON field name against internal/mailing/outbox_worker.go's
// consumer-side structs, since producer and consumer live in different
// packages and outbox.Item.Payload is deliberately `any`).
//
// # Enqueue is NOT inside StartRegistration's/StartRecovery's own transaction
//
// #0126's plan asserted the registration/recovery ceremonies "already own a
// transaction" and suggested enqueuing inside it, by analogy with
// audit.WriteTx. That is true of FinishRegistration/FinishRecovery (the
// passkey-creation step), but NOT of StartRegistration/StartRecovery (the
// step that actually calls SendVerification/SendRecovery) — Store.
// CreatePendingRegistration and Store.CreateRecoveryToken are both plain
// pool calls with no transaction of their own. Making this fully atomic
// (committed pending-registration/recovery-token row ⇒ queued mail, with
// no window between them) would require adding transaction-scoped variants
// of those two store methods and wrapping StartRegistration/StartRecovery
// in a transaction — out of scope for the time this pass had; flagged here
// rather than silently claimed. The residual: a crash between the token
// commit and this enqueue leaves a token with no queued mail, exactly the
// pre-#0126 failure mode but for a narrower window (one Enqueue call
// instead of a full SES round trip). See #0126's report for the follow-up
// this is worth its own issue.
type SESMailer struct {
	outbox  *outbox.Store
	baseURL string
}

// NewSESMailer constructs an SESMailer over the shared connection pool. No
// AWS client, no credential resolution, and no construction-time error path
// — sending is internal/mailing.OutboxWorker's job now, which validates its
// own Mailer dependency independently in cmd/opencircuit/main.go.
func NewSESMailer(pool *pgxpool.Pool, cfg *config.Config) *SESMailer {
	return &SESMailer{outbox: outbox.NewStore(pool), baseURL: cfg.BaseURL}
}

// registrationPayload/recoveryPayload/sessionsRevokedPayload are the JSONB
// contract with internal/mailing/outbox_worker.go's renderer — see that
// file's own copies of these same shapes and this file's package doc
// comment for why they are duplicated rather than shared.
type registrationPayload struct {
	Token      string `json:"token"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type recoveryPayload struct {
	Token      string `json:"token"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type sessionsRevokedPayload struct {
	At time.Time `json:"at"`
}

// errEnqueueFailed is the ONLY error text SendVerification/SendRecovery/
// SendSessionsRevoked ever return — deliberately recipient-free. #0027's
// review found RegisterStart/RecoverStart (unauthenticated routes) log a
// mailer error verbatim, so the pre-#0126 sendMessage helper stripped the
// recipient from its own wrapped error string; internal/outbox.Store's own
// enqueue error EMBEDS the recipient (via %q in its own message), so
// wrapping it here with %w — the naive fix — would leak an
// attacker-supplied address into that same log line. The real error
// (recipient included) is logged server-side instead, via slog, which is
// not attacker-visible.
var errEnqueueFailed = errors.New("auth: enqueueing email failed")

// logEnqueueFailure logs err (which may embed toEmail) server-side and
// returns errEnqueueFailed, the sanitized error every Send* method below
// returns to its caller.
func logEnqueueFailure(kind outbox.Kind, toEmail string, err error) error {
	slog.Default().Error("auth: enqueueing email failed", "kind", kind, "to", toEmail, "err", err)
	return errEnqueueFailed
}

// SendVerification enqueues the registration magic-link email.
// registrationTTL is the same constant Store.CreatePendingRegistration
// actually stamps the token's expiry with (store.go), so the worker's
// rendered "expires in" line can't drift from the TTL that's actually
// enforced.
//
// #0278: this method has had no production caller since #0260 — Store.
// CreatePendingRegistration now enqueues the same outbox.KindRegistration
// item itself, inside its own transaction (store.go), and
// RegistrationService.StartRegistration no longer calls s.mailer.
// SendVerification at all. Retained on Mailer (rather than narrowed off
// it) per #0278's decision — see that interface's doc comment for why —
// so this stays implemented, correct, and covered by
// TestSESMailer_SendVerification_Enqueues even though nothing in
// cmd/opencircuit calls it today.
func (m *SESMailer) SendVerification(ctx context.Context, toEmail, token string) error {
	if _, err := m.outbox.Enqueue(ctx, outbox.Item{
		Kind:      outbox.KindRegistration,
		Recipient: toEmail,
		Payload:   registrationPayload{Token: token, TTLSeconds: int64(registrationTTL.Seconds())},
	}); err != nil {
		return logEnqueueFailure(outbox.KindRegistration, toEmail, err)
	}
	return nil
}

// SendRecovery enqueues the single-use account-recovery email. recoveryTTL
// is the constant Store.CreateRecoveryToken stamps the token's expiry with.
//
// #0278: no production caller since #0260, same as SendVerification above
// — see that method's doc comment.
func (m *SESMailer) SendRecovery(ctx context.Context, toEmail, token string) error {
	if _, err := m.outbox.Enqueue(ctx, outbox.Item{
		Kind:      outbox.KindRecovery,
		Recipient: toEmail,
		Payload:   recoveryPayload{Token: token, TTLSeconds: int64(recoveryTTL.Seconds())},
	}); err != nil {
		return logEnqueueFailure(outbox.KindRecovery, toEmail, err)
	}
	return nil
}

// SendSessionsRevoked enqueues the "sign out everywhere" notification.
// #0126's plan §5: moved onto the queue alongside the other two rather than
// left on the synchronous path, since leaving one of three auth mails there
// preserves the failure mode for no reason — login.go's own call site
// already treats a failure as non-fatal to the sign-out itself.
func (m *SESMailer) SendSessionsRevoked(ctx context.Context, toEmail string, at time.Time) error {
	if _, err := m.outbox.Enqueue(ctx, outbox.Item{
		Kind:      outbox.KindSessionsRevoked,
		Recipient: toEmail,
		Payload:   sessionsRevokedPayload{At: at},
	}); err != nil {
		return logEnqueueFailure(outbox.KindSessionsRevoked, toEmail, err)
	}
	return nil
}

// Ensure the concrete types satisfy the interface at compile time.
var (
	_ Mailer = (*SESMailer)(nil)
	_ Mailer = NoOpMailer{}
)
