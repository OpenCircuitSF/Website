package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// SESMailer is a Mailer that delivers the three transactional emails
// (registration magic link, recovery magic link, "sessions revoked" notice)
// through the AWS SES v2 API via internal/mailing.Mailer.
//
// NOTE (#0027): this replaces the earlier SES-SMTP transport. That transport
// could never authenticate in the first place — this project has no static
// AWS credentials anywhere, and there was no SES SMTP username/password in
// configuration for it to use (#0007) — so main.go never wired it up. The SES
// v2 API mailer authenticates via the EC2 instance role's default AWS
// credential chain instead, which is why the swap unblocks main.go actually
// wiring a working mailer (see cmd/opencircuit/main.go).
type SESMailer struct {
	sender  mailing.Mailer
	baseURL string
}

// NewSESMailer constructs an SESMailer backed by a real AWS SES v2 client
// (internal/mailing.NewSESMailer), resolving credentials via the default
// chain. It fails clearly — returns an error — rather than constructing a
// mailer that appears to work and then sends nothing; see
// mailing.NewSESMailer's doc comment for exactly which configuration
// (AWS_REGION, SES_CONFIGURATION_SET, EMAIL_FROM) it requires.
func NewSESMailer(ctx context.Context, cfg *config.Config) (*SESMailer, error) {
	sender, err := mailing.NewSESMailer(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("auth: constructing SES mailer: %w", err)
	}
	return NewSESMailerWithSender(sender, cfg), nil
}

// NewSESMailerWithSender constructs an SESMailer around an explicit
// mailing.Mailer, bypassing AWS client construction and credential
// resolution entirely.
//
// This is the test seam #0027 introduced to replace NewSESMailerWithAddr,
// deleted along with the SMTP transport it existed for (that seam pointed
// SESMailer's SMTP dial at an unreachable host/port to force a deterministic,
// synchronous failure without touching the network). An SES v2 API client has
// no dial step to fail the same way, so the seam moves one level up: inject a
// fake mailing.Mailer whose Send returns an error, and this constructor still
// exercises the real production SESMailer.send composition and error-handling
// path around it — see internal/handlers/logging_test.go's
// TestAuthHandler_MailerErrorDoesNotLeakEmail, the test that depended on the
// old seam.
func NewSESMailerWithSender(sender mailing.Mailer, cfg *config.Config) *SESMailer {
	return &SESMailer{sender: sender, baseURL: cfg.BaseURL}
}

// SendVerification sends the registration magic-link email.
func (m *SESMailer) SendVerification(ctx context.Context, toEmail, token string) error {
	link := verificationURL(m.baseURL, token)
	subject := "Verify your Open Circuit SF account"
	text := "Welcome to Open Circuit SF.\r\n\r\n" +
		"Click the link below to verify your email and add a passkey. " +
		"This link expires in 5 minutes.\r\n\r\n" +
		link + "\r\n\r\n" +
		"If you did not request this, you can ignore this email.\r\n"
	return m.send(ctx, toEmail, subject, text)
}

// SendRecovery sends the single-use account-recovery email.
func (m *SESMailer) SendRecovery(ctx context.Context, toEmail, token string) error {
	link := recoveryURL(m.baseURL, token)
	subject := "Recover your Open Circuit SF account"
	text := "A recovery link was requested for your Open Circuit SF account.\r\n\r\n" +
		"Click the link below to register a new passkey. " +
		"This link expires in 15 minutes.\r\n\r\n" +
		link + "\r\n\r\n" +
		"If you did not request this, you can ignore this email; " +
		"your existing passkeys remain valid.\r\n"
	return m.send(ctx, toEmail, subject, text)
}

// SendSessionsRevoked sends the plain "sign out everywhere" notification.
// Unlike SendVerification/SendRecovery it carries no link with
// credentials and no token — nothing single-use, so no expiry is mentioned.
// The copy deliberately says the account's EXISTING passkey still works
// (revoking sessions never touches passkey_credentials) and only mentions
// enrolling a replacement as a conditional follow-up for the lost-device case.
func (m *SESMailer) SendSessionsRevoked(ctx context.Context, toEmail string, at time.Time) error {
	subject := "All sessions signed out"
	text := fmt.Sprintf(
		"All sessions for %s were signed out on %s.\r\n\r\n"+
			"To get back in, go to %s and sign in with your existing passkey — "+
			"it still works and nothing about your account has changed.\r\n\r\n"+
			"If you did this because a device was lost or is no longer yours, also open "+
			"Account settings after signing in and revoke that device's passkey. You can "+
			"enroll a replacement from the same screen.\r\n\r\n"+
			"If you did not do this, sign in and revoke your passkeys immediately.\r\n",
		toEmail, at.UTC().Format(time.RFC1123Z), m.baseURL,
	)
	return m.send(ctx, toEmail, subject, text)
}

// send composes a plain-text message and hands it to the underlying
// mailing.Mailer. The context is honored before dispatch.
func (m *SESMailer) send(ctx context.Context, toEmail, subject, textBody string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	_, err := m.sender.Send(ctx, mailing.Message{
		To:       toEmail,
		Subject:  subject,
		TextBody: textBody,
	})
	if err != nil {
		// Recipient omitted deliberately: this error propagates to
		// AuthHandler's unauthenticated RegisterStart/RecoverStart 500 branches,
		// which log it verbatim, and both routes are unauthenticated so
		// any caller could otherwise write an arbitrary address into the journal.
		// Nothing diagnostic is lost, but the two callers recover it differently:
		// on recovery, account.recovery_started's audit row carries user_id and
		// joins straight to users.email (recovery.go:98-104); on registration,
		// account.registration_started carries no actor_id/user_id/target_id (the
		// account doesn't exist yet — registration.go:104-109), so the address
		// isn't on that row at all. It is still recoverable there, just by a
		// two-step correlation instead of a join: registration.go:97 commits it
		// to pending_registrations immediately before this send, so an operator
		// matches the outage's timestamp and the audit row's IP to the pending
		// registration rather than reading it off the audit entry directly.
		return fmt.Errorf("auth: sending email: %w", err)
	}
	return nil
}

// Ensure the concrete types satisfy the interface at compile time.
var (
	_ Mailer = (*SESMailer)(nil)
	_ Mailer = NoOpMailer{}
)
