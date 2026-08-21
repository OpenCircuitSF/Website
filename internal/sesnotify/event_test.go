package sesnotify

import (
	"testing"
	"time"
)

func TestSESEvent_Type_PrefersEventTypeOverNotificationType(t *testing.T) {
	e := SESEvent{EventType: "Bounce", NotificationType: "Delivery"}
	if got := e.Type(); got != "Bounce" {
		t.Errorf("Type() = %q, want %q", got, "Bounce")
	}
}

func TestSESEvent_Type_FallsBackToNotificationType(t *testing.T) {
	e := SESEvent{NotificationType: "Complaint"}
	if got := e.Type(); got != "Complaint" {
		t.Errorf("Type() = %q, want %q", got, "Complaint")
	}
}

func TestParseSESEvent_ConfigurationSetFormat(t *testing.T) {
	raw := `{"eventType":"Bounce","mail":{"timestamp":"2026-08-20T12:00:00.000Z","messageId":"ses-msg-1","destination":["a@example.com"]},"bounce":{"bounceType":"Permanent","bounceSubType":"General","bouncedRecipients":[{"emailAddress":"a@example.com"}]}}`
	ev, err := ParseSESEvent(raw)
	if err != nil {
		t.Fatalf("ParseSESEvent: %v", err)
	}
	if ev.Type() != "Bounce" {
		t.Errorf("Type() = %q, want Bounce", ev.Type())
	}
	if ev.BounceType() != "Permanent" {
		t.Errorf("BounceType() = %q, want Permanent", ev.BounceType())
	}
	if ev.BounceSubType() != "General" {
		t.Errorf("BounceSubType() = %q, want General", ev.BounceSubType())
	}
	if ev.Mail.MessageID != "ses-msg-1" {
		t.Errorf("Mail.MessageID = %q, want ses-msg-1", ev.Mail.MessageID)
	}
}

func TestParseSESEvent_IdentityNotificationFormat(t *testing.T) {
	raw := `{"notificationType":"Complaint","mail":{"timestamp":"2026-08-20T12:00:00.000Z","messageId":"ses-msg-2","destination":["b@example.com"]},"complaint":{"complaintFeedbackType":"abuse","complainedRecipients":[{"emailAddress":"b@example.com"}]}}`
	ev, err := ParseSESEvent(raw)
	if err != nil {
		t.Fatalf("ParseSESEvent: %v", err)
	}
	if ev.Type() != "Complaint" {
		t.Errorf("Type() = %q, want Complaint", ev.Type())
	}
	if ev.ComplaintFeedbackType() != "abuse" {
		t.Errorf("ComplaintFeedbackType() = %q, want abuse", ev.ComplaintFeedbackType())
	}
}

func TestParseSESEvent_InvalidJSON(t *testing.T) {
	_, err := ParseSESEvent("not json at all")
	if err == nil {
		t.Fatal("ParseSESEvent(garbage) = nil error, want an error")
	}
}

func TestSESEvent_Recipients_Bounce(t *testing.T) {
	ev := SESEvent{
		EventType: "Bounce",
		Bounce: &SESBounce{
			BouncedRecipients: []SESRecipient{{EmailAddress: "a@example.com"}, {EmailAddress: "b@example.com"}},
		},
		Mail: SESMail{Destination: []string{"a@example.com", "b@example.com", "c@example.com"}},
	}
	got := ev.Recipients()
	want := []string{"a@example.com", "b@example.com"}
	if !equalStrings(got, want) {
		t.Errorf("Recipients() = %v, want %v (bounce recipients, not mail.destination)", got, want)
	}
}

func TestSESEvent_Recipients_Complaint(t *testing.T) {
	ev := SESEvent{
		EventType: "Complaint",
		Complaint: &SESComplaint{
			ComplainedRecipients: []SESRecipient{{EmailAddress: "c@example.com"}},
		},
	}
	got := ev.Recipients()
	want := []string{"c@example.com"}
	if !equalStrings(got, want) {
		t.Errorf("Recipients() = %v, want %v", got, want)
	}
}

func TestSESEvent_Recipients_Delivery(t *testing.T) {
	ev := SESEvent{
		EventType: "Delivery",
		Delivery:  &SESDelivery{Recipients: []string{"d@example.com"}},
	}
	got := ev.Recipients()
	want := []string{"d@example.com"}
	if !equalStrings(got, want) {
		t.Errorf("Recipients() = %v, want %v", got, want)
	}
}

func TestSESEvent_Recipients_FallsBackToMailDestination(t *testing.T) {
	ev := SESEvent{
		EventType: "Reject",
		Mail:      SESMail{Destination: []string{"e@example.com"}},
	}
	got := ev.Recipients()
	want := []string{"e@example.com"}
	if !equalStrings(got, want) {
		t.Errorf("Recipients() = %v, want %v", got, want)
	}
}

func TestSESEvent_Recipients_FallsBackToSingleEmptyRecipient(t *testing.T) {
	ev := SESEvent{EventType: "Send"}
	got := ev.Recipients()
	want := []string{""}
	if !equalStrings(got, want) {
		t.Errorf("Recipients() = %v, want %v (single empty recipient so the event is still recorded)", got, want)
	}
}

func TestSESEvent_Recipients_UnparseableEventFallsThroughToEmptyRecipient(t *testing.T) {
	// The zero-value SESEvent (what handleNotification uses on a parse
	// failure) must not panic and must still yield a recordable recipient.
	var ev SESEvent
	got := ev.Recipients()
	want := []string{""}
	if !equalStrings(got, want) {
		t.Errorf("Recipients() on zero value = %v, want %v", got, want)
	}
}

func TestSESEvent_ComplaintFeedbackType_NotSpamConstant(t *testing.T) {
	ev := SESEvent{EventType: "Complaint", Complaint: &SESComplaint{ComplaintFeedbackType: "not-spam"}}
	if ev.ComplaintFeedbackType() != ComplaintFeedbackTypeNotSpam {
		t.Errorf("ComplaintFeedbackType() = %q, want %q", ev.ComplaintFeedbackType(), ComplaintFeedbackTypeNotSpam)
	}
}

func TestParseEventTimestamp_ValidRFC3339Nano(t *testing.T) {
	got, ok := ParseEventTimestamp("2026-01-27T14:59:38.237Z")
	if !ok {
		t.Fatal("ParseEventTimestamp: ok = false, want true")
	}
	want := time.Date(2026, 1, 27, 14, 59, 38, 237000000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("ParseEventTimestamp = %v, want %v", got, want)
	}
}

func TestParseEventTimestamp_EmptyIsNotOK(t *testing.T) {
	if _, ok := ParseEventTimestamp(""); ok {
		t.Error("ParseEventTimestamp(\"\") ok = true, want false")
	}
}

func TestParseEventTimestamp_GarbageIsNotOK(t *testing.T) {
	if _, ok := ParseEventTimestamp("not a timestamp"); ok {
		t.Error("ParseEventTimestamp(garbage) ok = true, want false")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
