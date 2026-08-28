package auth

import (
	"context"
	"log"
	"time"
)

// Mailer sends transactional emails for the authentication flows: magic-link
// verification during registration and single-use recovery links.
//
// The interface is intentionally narrow so the transport can be swapped in
// tests (see NoOpMailer) and so callers in the auth package need not know
// whether email is delivered via SES SMTP, a fake, or stdout.
//
// # #0278: SendVerification and SendRecovery are production-dead
//
// As of #0260, store.CreatePendingRegistration and store.CreateRecoveryToken
// enqueue the registration/recovery email directly onto outbound_queue
// inside their own transaction (store.go), so a crash between the token
// commit and the enqueue is impossible. RegistrationService.StartRegistration
// and RecoveryService.StartRecovery therefore no longer call
// s.mailer.SendVerification/SendRecovery at all — only SendSessionsRevoked
// (login.go's LogoutAll) still has a live caller.
//
// #0278 decided to RETAIN both methods on the interface rather than narrow
// it to SendSessionsRevoked alone: every implementation (SESMailer,
// NoOpMailer) is a handful of lines, and several tests
// (registration_test.go, recovery_test.go,
// internal/handlers/logout_all_test.go, internal/handlers/settings_test.go)
// deliberately assert these methods are NOT called by the current
// start-flow — narrowing the interface would have meant rewriting that
// fake-mailer machinery across four files for a low-priority tidiness item
// (#0278's own description). See SendVerification's and SendRecovery's doc
// comments below, and SESMailer's and NoOpMailer's, for the same note
// repeated at the implementation.
type Mailer interface {
	// SendVerification sends a registration magic-link email to toEmail. The
	// body contains the link {BASE_URL}/register/verify?token={token}, which is
	// an SPA browser path (not an /auth/* API endpoint) that loads the Svelte
	// app. The SPA then calls GET /auth/register/verify?token={token} to fetch
	// WebAuthn creation options.
	//
	// #0278: no production caller since #0260 — see this interface's own doc
	// comment for why it is retained rather than removed.
	SendVerification(ctx context.Context, toEmail, token string) error

	// SendRecovery sends an account-recovery email to toEmail. The body
	// contains the link {BASE_URL}/recover/verify?token={token}, which is an
	// SPA browser path. The SPA calls GET /auth/recover/verify?token={token}
	// for WebAuthn creation options.
	//
	// #0278: no production caller since #0260 — see this interface's own doc
	// comment for why it is retained rather than removed.
	SendRecovery(ctx context.Context, toEmail, token string) error

	// SendSessionsRevoked notifies toEmail that every active session was just
	// signed out ("sign out everywhere") at the given time. Unlike
	// SendVerification/SendRecovery this carries no credentials: no token, no
	// single-use link, nothing that expires — it is safe to re-read at any
	// time. (It is themed the same HTML+text pair as the other two — see
	// internal/mailing.BuildSessionsRevokedEmail — this sentence is about the
	// payload, not the formatting.) It must say the account's EXISTING passkey
	// still works; enrolling a new one is only a conditional follow-up for the
	// lost-device case, never the primary instruction.
	SendSessionsRevoked(ctx context.Context, toEmail string, at time.Time) error
}

// NoOpMailer is a Mailer that does not send anything. It logs the would-be
// recipient and link to stdout, which makes local development and tests usable
// without any SES credentials or network access.
type NoOpMailer struct {
	// BaseURL is used to render the link in the log line so developers can copy
	// it into a browser. If empty, only the token is logged.
	BaseURL string
}

// SendVerification logs the verification link instead of sending it.
//
// #0277/#0278: this method has had no production caller since #0260 —
// RegistrationService.StartRegistration no longer calls it (see Mailer's
// doc comment) — so this no longer runs under MAILER_NOOP=true in the
// registration flow; that path now enqueues onto outbound_queue and drains
// through cmd/opencircuit's noOpMailingMailer instead, which #0277 fixed to
// log the rendered body. Retained rather than deleted, consistent with
// #0278's decision to keep SendVerification/SendRecovery on the Mailer
// interface: NoOpMailer must keep implementing whatever the interface
// names, and this is still exercised directly by TestNoOpMailer.
func (m NoOpMailer) SendVerification(_ context.Context, toEmail, token string) error {
	log.Printf("NoOpMailer: verification email to %s: %s", toEmail, verificationURL(m.BaseURL, token))
	return nil
}

// SendRecovery logs the recovery link instead of sending it.
//
// #0277/#0278: no production caller since #0260, same as SendVerification
// above — see that method's doc comment.
func (m NoOpMailer) SendRecovery(_ context.Context, toEmail, token string) error {
	log.Printf("NoOpMailer: recovery email to %s: %s", toEmail, recoveryURL(m.BaseURL, token))
	return nil
}

// SendSessionsRevoked logs the would-be sessions-revoked notice instead of
// sending it, same as the other two methods.
func (m NoOpMailer) SendSessionsRevoked(_ context.Context, toEmail string, at time.Time) error {
	log.Printf("NoOpMailer: sessions-revoked notice to %s at %s", toEmail, at.UTC().Format(time.RFC3339))
	return nil
}

// verificationURL builds the registration magic-link URL.
//
// The path /register/verify is an SPA route (not an /auth/* API path), so the
// Go mux catch-all "GET /" serves index.html and the Svelte app reads the token
// from the query string. The JSON creation-options endpoint remains at
// GET /auth/register/verify and is called by the SPA after landing.
func verificationURL(baseURL, token string) string {
	return baseURL + "/register/verify?token=" + token
}

// recoveryURL builds the account-recovery magic-link URL.
//
// Same scheme as verificationURL: /recover/verify is an SPA route that falls
// through to index.html; the JSON options are at GET /auth/recover/verify.
func recoveryURL(baseURL, token string) string {
	return baseURL + "/recover/verify?token=" + token
}
