// Package sesnotify verifies SNS message signatures for SES event
// notifications delivered over HTTPS (#0037), the security boundary in front
// of SES bounce/complaint ingestion (#0038) and inbound unsubscribe mail
// (#0058).
//
// # Trust model
//
// An HTTPS SNS subscription is a public, unauthenticated endpoint: anyone who
// knows the URL can POST to it. Everything in this package exists to answer
// one question before a caller is allowed to act on a message: did SNS
// actually send this, for the topic we expect?
//
// Verify establishes two independent facts, both required:
//
//  1. The message is authentically signed by SNS. This is proven by fetching
//     the signing certificate from an AWS-owned, region-pinned host — never
//     from an attacker-controlled URL — and checking the RSA signature over
//     SNS's documented canonical string.
//  2. The message was published to *our* topic. A valid SNS signature only
//     proves SNS sent the message; any AWS account can create a topic and
//     subscribe our endpoint to it, then publish forged-but-genuinely-signed
//     payloads through it. Verify rejects any TopicArn that does not exactly
//     match the configured allowlist (internal/config's SESEventsTopicARN).
//
// # What this package deliberately does not do
//
// It has no database dependency and mounts no HTTP handler. It answers only
// "is this message trustworthy". Message-type dispatch — auto-confirming a
// SubscriptionConfirmation, alerting on an UnsubscribeConfirmation, and
// turning a verified Notification into rows in email_events — is
// state-mapping behaviour that belongs to the caller (#0038), which imports
// this package rather than duplicating its trust logic. Keeping this package
// free of storage and routing is what lets #0058 (a second, unrelated SNS
// endpoint for inbound unsubscribe mail, with a completely different payload)
// reuse Verify without dragging in email_events.
//
// # SigningCertURL is attacker-influenced
//
// Every field in the SNS envelope, including SigningCertURL, arrives from the
// network before it has been verified. Validating its host against an
// AWS-owned, region-pinned pattern MUST happen before it is fetched —
// fetching an arbitrary attacker-chosen URL is a server-side request forgery
// primitive (see validateCertURL). This check cannot be bypassed by design:
// Verify performs it unconditionally, before the certificate-fetch seam is
// ever invoked, so no caller or test fake can skip it by supplying a fetcher
// that doesn't check.
//
// # SignatureVersion 1 and 2
//
// Both are accepted. Version 1 (RSA-SHA1) is not practically forgeable here:
// SHA-1's known break is collision resistance, not second-preimage, and an
// attacker does not hold SNS's private key regardless. A version-1 message is
// logged at WARN so an operator can see whether the topic has actually been
// switched to version 2. Any other SignatureVersion is rejected outright, not
// defaulted.
//
// # No timestamp-freshness check
//
// SNS's HTTPS retry policy can span days, so a freshness window would drop
// legitimate retries. Replay of a signed event is harmless because the
// downstream writes (#0038) are idempotent, keyed on the SNS MessageId.
package sesnotify
