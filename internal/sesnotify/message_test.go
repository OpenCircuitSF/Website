package sesnotify

import (
	"encoding/json"
	"testing"
)

// TestCanonicalString_NotificationWithSubject pins the exact field order and
// separator format for a Notification that carries a Subject. This is the
// test that actually catches a wrong field order: it does not reuse the
// production builder to construct its expectation.
func TestCanonicalString_NotificationWithSubject(t *testing.T) {
	m := &Message{
		Type:      TypeNotification,
		Message:   "test message body",
		MessageId: "abc-123",
		Subject:   "Test Subject",
		Timestamp: "2026-08-18T12:00:00.000Z",
		TopicArn:  "arn:aws:sns:us-west-2:123456789012:test-topic",
	}

	want := "Message\ntest message body\n" +
		"MessageId\nabc-123\n" +
		"Subject\nTest Subject\n" +
		"Timestamp\n2026-08-18T12:00:00.000Z\n" +
		"TopicArn\narn:aws:sns:us-west-2:123456789012:test-topic\n" +
		"Type\nNotification\n"

	got, err := canonicalString(m)
	if err != nil {
		t.Fatalf("canonicalString: %v", err)
	}
	if got != want {
		t.Errorf("canonicalString =\n%q\nwant\n%q", got, want)
	}
}

// TestCanonicalString_NotificationWithoutSubject pins that an absent Subject
// is omitted entirely — not emitted as an empty "Subject\n\n" pair.
func TestCanonicalString_NotificationWithoutSubject(t *testing.T) {
	m := &Message{
		Type:      TypeNotification,
		Message:   "test message body",
		MessageId: "abc-123",
		Timestamp: "2026-08-18T12:00:00.000Z",
		TopicArn:  "arn:aws:sns:us-west-2:123456789012:test-topic",
	}

	want := "Message\ntest message body\n" +
		"MessageId\nabc-123\n" +
		"Timestamp\n2026-08-18T12:00:00.000Z\n" +
		"TopicArn\narn:aws:sns:us-west-2:123456789012:test-topic\n" +
		"Type\nNotification\n"

	got, err := canonicalString(m)
	if err != nil {
		t.Fatalf("canonicalString: %v", err)
	}
	if got != want {
		t.Errorf("canonicalString =\n%q\nwant\n%q", got, want)
	}
}

// TestCanonicalString_SubscriptionConfirmation pins the different field order
// used for SubscriptionConfirmation/UnsubscribeConfirmation messages.
func TestCanonicalString_SubscriptionConfirmation(t *testing.T) {
	m := &Message{
		Type:         TypeSubscriptionConfirmation,
		Message:      "You have chosen to subscribe to the topic.",
		MessageId:    "def-456",
		SubscribeURL: "https://sns.us-west-2.amazonaws.com/?Action=ConfirmSubscription&Token=abc",
		Timestamp:    "2026-08-18T12:00:00.000Z",
		Token:        "abc",
		TopicArn:     "arn:aws:sns:us-west-2:123456789012:test-topic",
	}

	want := "Message\nYou have chosen to subscribe to the topic.\n" +
		"MessageId\ndef-456\n" +
		"SubscribeURL\nhttps://sns.us-west-2.amazonaws.com/?Action=ConfirmSubscription&Token=abc\n" +
		"Timestamp\n2026-08-18T12:00:00.000Z\n" +
		"Token\nabc\n" +
		"TopicArn\narn:aws:sns:us-west-2:123456789012:test-topic\n" +
		"Type\nSubscriptionConfirmation\n"

	got, err := canonicalString(m)
	if err != nil {
		t.Fatalf("canonicalString: %v", err)
	}
	if got != want {
		t.Errorf("canonicalString =\n%q\nwant\n%q", got, want)
	}
}

func TestCanonicalString_UnknownType(t *testing.T) {
	m := &Message{Type: "SomethingElse"}
	if _, err := canonicalString(m); err == nil {
		t.Fatal("expected error for unknown message type, got nil")
	}
}

// TestMessage_TypeReadFromBody asserts Type (and TopicArn) come from the JSON
// body's own fields when a Message is unmarshaled. This package never reads
// x-amz-sns-message-type or x-amz-sns-topic-arn headers — there is no code
// path here that consults them — so the only source of truth for either
// field is, and must remain, this struct's JSON tags on the body.
func TestMessage_TypeReadFromBody(t *testing.T) {
	body := []byte(`{
		"Type": "SubscriptionConfirmation",
		"MessageId": "def-456",
		"TopicArn": "arn:aws:sns:us-west-2:123456789012:test-topic",
		"Token": "abc",
		"Message": "You have chosen to subscribe to the topic.",
		"SubscribeURL": "https://sns.us-west-2.amazonaws.com/?Action=ConfirmSubscription&Token=abc",
		"Timestamp": "2026-08-18T12:00:00.000Z",
		"SignatureVersion": "2",
		"Signature": "deadbeef",
		"SigningCertURL": "https://sns.us-west-2.amazonaws.com/x.pem"
	}`)

	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if m.Type != TypeSubscriptionConfirmation {
		t.Errorf("Type = %q, want %q", m.Type, TypeSubscriptionConfirmation)
	}
	if m.TopicArn != "arn:aws:sns:us-west-2:123456789012:test-topic" {
		t.Errorf("TopicArn = %q, want the ARN from the body", m.TopicArn)
	}
}
