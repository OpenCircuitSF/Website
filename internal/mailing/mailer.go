// Package mailing provides the SES v2 API sending primitive shared by every
// outbound mail path in the project: transactional email (internal/auth,
// wrapped in its own auth.SESMailer) and, once #0028/#0042/#0045 land, the
// campaign send worker. See PRD.md §6.6 for the full design and CLAUDE.md
// §10 item 2 for what's not yet configured on the AWS side.
package mailing

import "context"

// Header is a single custom email header, e.g. List-Unsubscribe,
// List-Unsubscribe-Post, or List-Id (#0035).
type Header struct {
	Name  string
	Value string
}

// Message is a single outbound email, addressed to one recipient. At least
// one of HTMLBody/TextBody must be set; SESMailer.Send rejects a message with
// neither. Providing both sends multipart/alternative — SES's Simple email
// content builds that MIME structure automatically from the two Content
// parts, so callers never hand-roll MIME.
type Message struct {
	To      string
	Subject string
	// From, when non-empty, overrides the mailer's configured sender
	// address for this one message. #0045's send worker sets it from the
	// default_from_name setting (a display name wrapped around
	// EMAIL_FROM, e.g. `"Open Circuit SF" <hello@opencircuitsf.com>`);
	// every other caller leaves it empty and gets the mailer's default
	// (SESMailer.from). Carried in from #0027's review (2026-08-19): a
	// struct addition, not an interface break, so every existing caller
	// compiles unchanged.
	From     string
	HTMLBody string
	TextBody string
	Headers  []Header
}

// Mailer sends a single email and reports the provider's message ID, which is
// the join key #0038 uses to correlate SES bounce/complaint events back to
// the send that produced them.
//
// This is ShortLinks' Mailer interface seam (PRD §6.6), preserved verbatim so
// every caller — internal/auth today, the send worker and #0026's subscribe
// endpoint later — can inject RecordingMailer in tests instead of touching
// the network.
type Mailer interface {
	Send(ctx context.Context, msg Message) (messageID string, err error)
}
