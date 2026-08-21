// event.go parses the SES event payload carried inside a verified SNS
// Notification's Message field (#0038, PRD §6.7).
//
// # The Message field is a JSON string, not a nested object
//
// SNS's envelope (message.go) carries Message as a plain JSON string; the
// SES event it contains must be unmarshalled a SECOND time. This is the
// single most common bug in SNS/SES ingestion code, which is why
// ParseSESEvent takes the raw string rather than json.RawMessage — there is
// no way to call it wrong and skip the second unmarshal.
//
// # Two payload shapes, deliberately both accepted
//
// PRD §6.7 wires a Configuration Set event destination, whose payload uses
// "eventType". SES's older identity-notification format uses
// "notificationType" instead, and AWS's own documentation for the two sits
// side by side — an implementer can easily read the wrong page. Type()
// prefers eventType and falls back to notificationType; both formats agree
// on the mail/bounce/complaint/delivery sub-objects, so nothing else in
// this file needs to know which one arrived.
package sesnotify

import (
	"encoding/json"
	"time"
)

// SES event type strings this package's state mapping cares about
// (internal/handlers/ses_notifications.go). Others (DeliveryDelay, Send,
// and anything SES adds later) are recorded but otherwise unhandled — see
// that file's applyRecipient.
const (
	EventTypeBounce           = "Bounce"
	EventTypeComplaint        = "Complaint"
	EventTypeDelivery         = "Delivery"
	EventTypeReject           = "Reject"
	EventTypeRenderingFailure = "RenderingFailure"
)

// Bounce bounceType values (PRD §6.5).
const (
	BounceTypePermanent    = "Permanent"
	BounceTypeTransient    = "Transient"
	BounceTypeUndetermined = "Undetermined"
)

// ComplaintFeedbackTypeNotSpam marks a complaint event that is actually a
// feedback-loop report that a message was moved OUT of a spam folder — the
// opposite of a complaint. #0038 §4: suppressing on this would unsubscribe
// people for the crime of rescuing our mail, so callers must check for it
// explicitly rather than treating every Complaint event as a complaint.
const ComplaintFeedbackTypeNotSpam = "not-spam"

// SESRecipient is one entry of bounce.bouncedRecipients or
// complaint.complainedRecipients.
type SESRecipient struct {
	EmailAddress string `json:"emailAddress"`
}

// SESMail is the "mail" object every SES event carries: metadata about the
// outbound email the event concerns.
type SESMail struct {
	Timestamp   string   `json:"timestamp"`   // ISO 8601; parsed by ParseEventTimestamp
	MessageID   string   `json:"messageId"`   // SES's id for the outbound email — #0049's reconciliation key
	Destination []string `json:"destination"` // fallback recipient source, see SESEvent.Recipients
}

// SESBounce is the "bounce" object present on a Bounce event.
type SESBounce struct {
	BounceType        string         `json:"bounceType"`    // Permanent | Transient | Undetermined
	BounceSubType     string         `json:"bounceSubType"` // General | NoEmail | Suppressed | OnAccountSuppressionList | ...
	BouncedRecipients []SESRecipient `json:"bouncedRecipients"`
}

// SESComplaint is the "complaint" object present on a Complaint event.
type SESComplaint struct {
	ComplaintFeedbackType string         `json:"complaintFeedbackType"`
	ComplainedRecipients  []SESRecipient `json:"complainedRecipients"`
}

// SESDelivery is the "delivery" object present on a Delivery event. Its
// Recipients are plain strings, unlike Bounce/Complaint's {emailAddress}
// objects — SES's own schema, not an inconsistency introduced here.
type SESDelivery struct {
	Recipients []string `json:"recipients"`
}

// SESEvent is the payload inside a verified Notification's Message field.
type SESEvent struct {
	EventType        string        `json:"eventType"`
	NotificationType string        `json:"notificationType"`
	Mail             SESMail       `json:"mail"`
	Bounce           *SESBounce    `json:"bounce,omitempty"`
	Complaint        *SESComplaint `json:"complaint,omitempty"`
	Delivery         *SESDelivery  `json:"delivery,omitempty"`
}

// Type returns EventType if set, else NotificationType — see the package
// doc comment above for why both are accepted.
func (e SESEvent) Type() string {
	if e.EventType != "" {
		return e.EventType
	}
	return e.NotificationType
}

// ParseSESEvent unmarshals the SNS envelope's Message field (a JSON
// string) into an SESEvent. Callers MUST pass Message.Message, never
// Message.Message re-wrapped or pre-parsed — see the package doc comment.
func ParseSESEvent(rawMessage string) (SESEvent, error) {
	var e SESEvent
	if err := json.Unmarshal([]byte(rawMessage), &e); err != nil {
		return SESEvent{}, err
	}
	return e, nil
}

// Recipients extracts the address list an event applies to, per #0038 §2:
//
//	Bounce     -> bounce.bouncedRecipients[].emailAddress
//	Complaint  -> complaint.complainedRecipients[].emailAddress
//	Delivery   -> delivery.recipients[] (plain strings)
//	anything else, or the type-specific list came back empty -> mail.destination[]
//	still nothing -> a single "" recipient, so the event is still recorded
//
// Addresses are returned exactly as SES reported them — normalisation
// (lower(trim(...))) happens in SQL at insert time, matching every other
// table in this codebase (see internal/subscribers' package doc comment for
// why normalising in Go instead would risk disagreeing with Postgres's
// lower() on some codepoints).
func (e SESEvent) Recipients() []string {
	var out []string
	switch e.Type() {
	case EventTypeBounce:
		if e.Bounce != nil {
			for _, r := range e.Bounce.BouncedRecipients {
				out = append(out, r.EmailAddress)
			}
		}
	case EventTypeComplaint:
		if e.Complaint != nil {
			for _, r := range e.Complaint.ComplainedRecipients {
				out = append(out, r.EmailAddress)
			}
		}
	case EventTypeDelivery:
		if e.Delivery != nil {
			out = append(out, e.Delivery.Recipients...)
		}
	}
	if len(out) == 0 {
		out = append(out, e.Mail.Destination...)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// BounceType returns the event's bounce.bounceType, or "" when this isn't a
// Bounce event or carries no bounce object at all.
func (e SESEvent) BounceType() string {
	if e.Bounce == nil {
		return ""
	}
	return e.Bounce.BounceType
}

// BounceSubType returns the event's bounce.bounceSubType, or "" when this
// isn't a Bounce event or carries no bounce object at all.
func (e SESEvent) BounceSubType() string {
	if e.Bounce == nil {
		return ""
	}
	return e.Bounce.BounceSubType
}

// ComplaintFeedbackType returns the event's complaint.complaintFeedbackType,
// or "" when this isn't a Complaint event or carries no complaint object.
func (e SESEvent) ComplaintFeedbackType() string {
	if e.Complaint == nil {
		return ""
	}
	return e.Complaint.ComplaintFeedbackType
}

// ParseEventTimestamp parses mail.timestamp, SES's ISO 8601 timestamp for
// the event (e.g. "2016-01-27T14:59:38.237Z"). Returns ok=false on an empty
// or unparseable value rather than an error — callers (#0038 §3's staleness
// guard, and email_events.event_at) treat "unparseable" the same as
// "absent": best-effort evidence, never a reason to fail the request.
func ParseEventTimestamp(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}
