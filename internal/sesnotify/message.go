package sesnotify

import (
	"fmt"
	"strings"
)

// SNS message types this package understands. Notification carries an SES
// event; SubscriptionConfirmation and UnsubscribeConfirmation are SNS
// subscription-lifecycle messages.
const (
	TypeNotification             = "Notification"
	TypeSubscriptionConfirmation = "SubscriptionConfirmation"
	TypeUnsubscribeConfirmation  = "UnsubscribeConfirmation"
)

// Message is the JSON envelope SNS POSTs to an HTTPS subscription endpoint.
// Every field is attacker-influenced until Verify succeeds.
//
// Type is read from this struct — i.e. from the JSON body — never from the
// x-amz-sns-message-type header, and the same goes for TopicArn versus the
// x-amz-sns-topic-arn header. SNS's headers are not covered by the signature
// and are entirely attacker-controlled; only the signed body fields may be
// trusted.
type Message struct {
	Type             string `json:"Type"`
	MessageId        string `json:"MessageId"`
	Token            string `json:"Token,omitempty"`
	TopicArn         string `json:"TopicArn"`
	Subject          string `json:"Subject,omitempty"`
	Message          string `json:"Message"`
	SubscribeURL     string `json:"SubscribeURL,omitempty"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	UnsubscribeURL   string `json:"UnsubscribeURL,omitempty"`
}

// canonicalString builds the string SNS signs, per its documented field
// order for each message type. Included fields contribute
// "name\nvalue\n", concatenated in order. A field absent from the message is
// omitted entirely, not emitted with an empty value — Subject is the one
// that actually varies for Notification.
//
// Field order (from AWS's SNS message-signature documentation):
//
//	Notification:                          Message, MessageId, Subject
//	                                        (only if present), Timestamp,
//	                                        TopicArn, Type
//	SubscriptionConfirmation,
//	UnsubscribeConfirmation:                Message, MessageId, SubscribeURL,
//	                                        Timestamp, Token, TopicArn, Type
func canonicalString(m *Message) (string, error) {
	var b strings.Builder
	add := func(name, value string) {
		b.WriteString(name)
		b.WriteByte('\n')
		b.WriteString(value)
		b.WriteByte('\n')
	}

	switch m.Type {
	case TypeNotification:
		add("Message", m.Message)
		add("MessageId", m.MessageId)
		if m.Subject != "" {
			add("Subject", m.Subject)
		}
		add("Timestamp", m.Timestamp)
		add("TopicArn", m.TopicArn)
		add("Type", m.Type)
	case TypeSubscriptionConfirmation, TypeUnsubscribeConfirmation:
		add("Message", m.Message)
		add("MessageId", m.MessageId)
		add("SubscribeURL", m.SubscribeURL)
		add("Timestamp", m.Timestamp)
		add("Token", m.Token)
		add("TopicArn", m.TopicArn)
		add("Type", m.Type)
	default:
		return "", fmt.Errorf("sesnotify: unknown message type %q", m.Type)
	}

	return b.String(), nil
}
