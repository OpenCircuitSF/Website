package inbound

import "testing"

func rawMessage(headers map[string]string, body string) []byte {
	raw := ""
	for k, v := range headers {
		raw += k + ": " + v + "\r\n"
	}
	raw += "\r\n" + body
	return []byte(raw)
}

func TestParse_SubjectToken(t *testing.T) {
	raw := rawMessage(map[string]string{
		"From":    "Someone Else <someone@example.com>",
		"Subject": "unsubscribe:abc123XYZ",
	}, "please take me off the list\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pm.Token != "abc123XYZ" {
		t.Errorf("Token = %q, want %q", pm.Token, "abc123XYZ")
	}
	if pm.From != "someone@example.com" {
		t.Errorf("From = %q, want %q", pm.From, "someone@example.com")
	}
	if pm.AutoReply {
		t.Error("AutoReply = true, want false")
	}
	if pm.Subject != "unsubscribe:abc123XYZ" {
		t.Errorf("Subject = %q, want %q", pm.Subject, "unsubscribe:abc123XYZ")
	}
}

// TestParse_SubjectFieldDecodesMIMEEncoding pins #0499's Subject field: it
// carries the DECODED Subject header (the same decode extractToken already
// applies when hunting for the token), not the raw RFC 2047-encoded bytes —
// otherwise a parked-message audit record (recordParked,
// internal/handlers/ses_inbound.go) would show an admin unreadable encoded
// text instead of the subject a person actually typed.
func TestParse_SubjectFieldDecodesMIMEEncoding(t *testing.T) {
	raw := rawMessage(map[string]string{
		"From":    "someone@example.com",
		"Subject": "=?UTF-8?Q?please_unsubscribe?=",
	}, "body\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pm.Subject != "please unsubscribe" {
		t.Errorf("Subject = %q, want %q", pm.Subject, "please unsubscribe")
	}
}

// TestParse_SubjectFieldEmptyWhenHeaderAbsent pins the "" fallback recordParked
// relies on to omit the metadata key entirely rather than store an empty
// string (see that function's doc comment).
func TestParse_SubjectFieldEmptyWhenHeaderAbsent(t *testing.T) {
	raw := rawMessage(map[string]string{
		"From": "someone@example.com",
	}, "body\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pm.Subject != "" {
		t.Errorf("Subject = %q, want empty", pm.Subject)
	}
}

func TestParse_SubjectTokenWithReplyPrefixAndCase(t *testing.T) {
	raw := rawMessage(map[string]string{
		"From":    "someone@example.com",
		"Subject": "Re: UNSUBSCRIBE:tok-42",
	}, "body\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pm.Token != "tok-42" {
		t.Errorf("Token = %q, want %q", pm.Token, "tok-42")
	}
}

func TestParse_NoTokenFromFallback(t *testing.T) {
	raw := rawMessage(map[string]string{
		"From":    "Jane Doe <jane@example.com>",
		"Subject": "please stop emailing me",
	}, "body\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pm.Token != "" {
		t.Errorf("Token = %q, want empty", pm.Token)
	}
	if pm.From != "jane@example.com" {
		t.Errorf("From = %q, want %q", pm.From, "jane@example.com")
	}
}

func TestParse_NoTokenNoFrom(t *testing.T) {
	raw := rawMessage(map[string]string{
		"Subject": "hello",
	}, "body\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pm.Token != "" || pm.From != "" {
		t.Errorf("Token/From = %q/%q, want both empty", pm.Token, pm.From)
	}
}

func TestParse_UnparseableMessageReturnsError(t *testing.T) {
	// net/mail.ReadMessage requires a header/body split ("\r\n\r\n"); a
	// bare header line with no terminator is not a valid RFC 5322 message.
	_, err := Parse([]byte("not a valid message at all, no header split"))
	if err == nil {
		t.Fatal("Parse: want error for unparseable message, got nil")
	}
}

func TestIsAutoReply_AutoSubmitted(t *testing.T) {
	raw := rawMessage(map[string]string{
		"From":           "vacation@example.com",
		"Subject":        "Out of Office",
		"Auto-Submitted": "auto-replied",
	}, "I am away\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !pm.AutoReply {
		t.Error("AutoReply = false, want true (Auto-Submitted: auto-replied)")
	}
}

func TestIsAutoReply_AutoSubmittedNoIsNotFlagged(t *testing.T) {
	raw := rawMessage(map[string]string{
		"From":           "someone@example.com",
		"Subject":        "unsubscribe:abc",
		"Auto-Submitted": "no",
	}, "body\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pm.AutoReply {
		t.Error("AutoReply = true, want false (Auto-Submitted: no means NOT automated)")
	}
}

// TestIsAutoReply_PrecedenceBulk pins isAutoReply's Precedence handling for
// all four conventional values, deliberately with the DIFFERENT expectation
// #0498 introduced for "list": bulk, auto_reply, and junk are genuine
// machine-generation markers no ordinary person's reply carries, so they
// still classify as auto-replies; "list" only marks the message as having
// passed through a list manager, which says nothing about whether a human
// wrote it, so it must NOT classify as an auto-reply on its own (see
// isAutoReply's doc comment for the full rationale).
func TestIsAutoReply_PrecedenceBulk(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"bulk", true},
		{"auto_reply", true},
		{"list", false},
		{"junk", true},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			raw := rawMessage(map[string]string{
				"From":       "list-daemon@example.com",
				"Subject":    "unsubscribe:abc",
				"Precedence": c.value,
			}, "body\r\n")

			pm, err := Parse(raw)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if pm.AutoReply != c.want {
				t.Errorf("AutoReply = %v, want %v (Precedence: %s)", pm.AutoReply, c.want, c.value)
			}
		})
	}
}

func TestIsAutoReply_XAutoreplyHeader(t *testing.T) {
	raw := rawMessage(map[string]string{
		"From":        "vacation@example.com",
		"Subject":     "Away",
		"X-Autoreply": "yes",
	}, "body\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !pm.AutoReply {
		t.Error("AutoReply = false, want true (X-Autoreply present)")
	}
}

func TestIsAutoReply_XAutoResponseSuppressHeader(t *testing.T) {
	raw := rawMessage(map[string]string{
		"From":                     "vacation@example.com",
		"Subject":                  "Away",
		"X-Auto-Response-Suppress": "All",
	}, "body\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !pm.AutoReply {
		t.Error("AutoReply = false, want true (X-Auto-Response-Suppress present)")
	}
}

func TestIsAutoReply_NullReturnPath(t *testing.T) {
	raw := rawMessage(map[string]string{
		"From":        "mailer-daemon@example.com",
		"Subject":     "Delivery Status Notification (Failure)",
		"Return-Path": "<>",
	}, "body\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !pm.AutoReply {
		t.Error("AutoReply = false, want true (null Return-Path)")
	}
}

func TestIsAutoReply_DeliveryStatusReport(t *testing.T) {
	raw := rawMessage(map[string]string{
		"From":         "mailer-daemon@example.com",
		"Subject":      "Undeliverable",
		"Content-Type": `multipart/report; report-type=delivery-status; boundary="x"`,
	}, "body\r\n")

	pm, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !pm.AutoReply {
		t.Error("AutoReply = false, want true (multipart/report; delivery-status)")
	}
}

func TestExtractToken_MIMEEncodedSubject(t *testing.T) {
	// "=?UTF-8?Q?unsubscribe:tok9?=" round-tripped through RFC 2047
	// Q-encoding, matching what a mail client could produce from a plain
	// ASCII subject even though ours never needs the encoding.
	got := extractToken("=?UTF-8?Q?unsubscribe=3Atok9?=")
	if got != "tok9" {
		t.Errorf("extractToken(MIME-encoded) = %q, want %q", got, "tok9")
	}
}
