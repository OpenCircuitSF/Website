// Package inbound parses inbound unsubscribe mail fetched from S3 (#0058,
// PRD §6.5 path 3) and fetches/deletes the S3 object itself. It is
// deliberately free of any database or HTTP dependency — see this file's
// Parse and s3.go's S3Store for the two independently-testable halves —
// so internal/handlers' SESInboundHandler can depend on narrow interfaces
// (CLAUDE.md §1) rather than on net/mail parsing details or a concrete
// *s3.Client.
package inbound

import (
	"bytes"
	"mime"
	"net/mail"
	"regexp"
	"strings"
)

// tokenPattern matches the preferred unsubscribe-token form PRD §6.5
// specifies: a Subject line containing "unsubscribe:<token>". Case-
// insensitive and matched anywhere in the subject (not anchored) because a
// reply commonly prepends "Re: " or a mail client's own thread markers, and
// PRD §6.5's diagram shows the campaign's own List-Unsubscribe mailto: form
// setting exactly this subject verbatim, unprefixed, for the recipient's
// mail client to reply to or forward as-is. \S+ (rather than a token-shape
// regex) keeps this decoupled from the token's own generation format in
// internal/subscribers.
var tokenPattern = regexp.MustCompile(`(?i)unsubscribe:\s*(\S+)`)

// ParsedMessage is what SESInboundHandler needs from a fetched inbound mail
// object to decide whether — and how — to act on it. Token and From are
// independent extraction attempts (either or both may be empty); the
// handler's own logic decides precedence (token first, PRD §6.5).
type ParsedMessage struct {
	// Token is the value following "unsubscribe:" in the Subject header,
	// or "" if the header doesn't contain that marker.
	Token string
	// From is the lowercased, trimmed address portion of the From header
	// (e.g. "user@example.com" out of "Jane Doe <User@Example.com>"), or ""
	// if the header is absent or unparseable. Matched against
	// subscribers.email as the fallback path — genuinely spoofable, which
	// is exactly why it stays the fallback and never the primary path (this
	// issue's Notes).
	From string
	// AutoReply reports whether the message carries a conventional
	// automated-mail signal (an out-of-office, a bounce/DSN, a mailing-list
	// software reply) and must NOT be read as a person's unsubscribe
	// request — see isAutoReply's doc comment for exactly which signals.
	AutoReply bool
}

// Parse parses raw (the full RFC 5322 message fetched from S3) with
// net/mail and extracts the fields SESInboundHandler needs. The only error
// it returns is net/mail's own parse failure — an unparseable message is
// exactly the "neither matching" case PRD §6.5 requires be logged and left
// in place for manual review, so the caller treats a Parse error the same
// way it treats "no token and no From match" rather than as a 500: the
// object is almost certainly not a well-formed unsubscribe request no
// retry will fix, and SES already re-delivers the SNS notification on any
// non-2xx, which would just loop on the same unparseable bytes.
func Parse(raw []byte) (ParsedMessage, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ParsedMessage{}, err
	}

	pm := ParsedMessage{
		Token:     extractToken(msg.Header.Get("Subject")),
		AutoReply: isAutoReply(msg.Header),
	}
	if addr, err := mail.ParseAddress(msg.Header.Get("From")); err == nil {
		pm.From = strings.ToLower(strings.TrimSpace(addr.Address))
	}
	return pm, nil
}

// extractToken pulls the token out of a (possibly RFC 2047 MIME-encoded,
// e.g. "=?UTF-8?B?...?=") Subject header. Decoding failures fall back to
// the raw header value unchanged — our own campaign-generated subject is
// plain ASCII and was never encoded to begin with, so this only matters for
// a hand-typed reply subject a mail client chose to encode, and a decode
// error there is better handled by matching the raw bytes than by giving up.
func extractToken(subject string) string {
	if decoded, err := (&mime.WordDecoder{}).DecodeHeader(subject); err == nil {
		subject = decoded
	}
	m := tokenPattern.FindStringSubmatch(subject)
	if m == nil {
		return ""
	}
	return m[1]
}

// isAutoReply reports whether h carries a conventional signal that the
// message was generated automatically rather than typed by a person — the
// practical hazard #0058's Notes call out: an out-of-office bouncing off a
// campaign send must never be read as an unsubscribe request. Any ONE
// signal is sufficient; these are deliberately independent (not a scored
// combination) since each is, on its own, standard practice for exactly one
// of the automated-mail shapes this endpoint will actually see:
//
//   - Auto-Submitted (RFC 3834): present with any value other than the
//     literal "no" — the RFC's own definition of "not manually submitted".
//   - Precedence: bulk / auto_reply / list / junk — conventional (if
//     never formally standardized) marker used by list software, ticketing
//     systems, and some autoresponders.
//   - X-Autoreply / X-Auto-Response-Suppress: presence alone is enough —
//     both are automated-mail signals used by vacation responders and
//     Exchange/Outlook autoresponders regardless of the value carried.
//   - Return-Path: <> — the null reverse-path convention RFC 3834 §4 and
//     RFC 5321 both use for messages (bounces/DSNs, many autoresponders)
//     that must never themselves generate a bounce or an auto-reply.
//   - Content-Type: multipart/report; report-type=delivery-status — an
//     RFC 3464 delivery status notification, i.e. a bounce-to-sender.
func isAutoReply(h mail.Header) bool {
	if v := strings.ToLower(strings.TrimSpace(h.Get("Auto-Submitted"))); v != "" && v != "no" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(h.Get("Precedence"))) {
	case "bulk", "auto_reply", "list", "junk":
		return true
	}
	if strings.TrimSpace(h.Get("X-Autoreply")) != "" {
		return true
	}
	if strings.TrimSpace(h.Get("X-Auto-Response-Suppress")) != "" {
		return true
	}
	if strings.TrimSpace(h.Get("Return-Path")) == "<>" {
		return true
	}
	if ct := strings.ToLower(h.Get("Content-Type")); strings.Contains(ct, "multipart/report") && strings.Contains(ct, "delivery-status") {
		return true
	}
	return false
}
