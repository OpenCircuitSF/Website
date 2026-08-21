package mailing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
)

// fakeSESAPI is a sesAPI that never touches the network: each call pops the
// next scripted response/error pair and records the input it was given, so
// tests can assert exactly what SESMailer.Send handed to the SDK.
type fakeSESAPI struct {
	calls     []*sesv2.SendEmailInput
	responses []fakeSESResponse
}

type fakeSESResponse struct {
	output *sesv2.SendEmailOutput
	err    error
}

func (f *fakeSESAPI) SendEmail(_ context.Context, params *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	f.calls = append(f.calls, params)
	i := len(f.calls) - 1
	if i >= len(f.responses) {
		return nil, errors.New("fakeSESAPI: no scripted response for call")
	}
	r := f.responses[i]
	return r.output, r.err
}

// noopSleep replaces the real backoff delay in tests so retry tests don't
// wait in real time.
func noopSleep(_ context.Context, _ time.Duration) error { return nil }

func newTestSESMailer(api sesAPI) *SESMailer {
	return &SESMailer{
		client:           api,
		from:             "Open Circuit SF <hello@opencircuitsf.com>",
		replyTo:          "hello@opencircuitsf.com",
		configurationSet: "opencircuit-transactional",
		sleep:            noopSleep,
	}
}

// TestSESMailer_Send_ReturnsRealMessageIDFromResponse proves the returned
// messageID is read off the SDK response, not a locally generated value —
// the join key #0038 needs must be the one SES actually assigned.
func TestSESMailer_Send_ReturnsRealMessageIDFromResponse(t *testing.T) {
	api := &fakeSESAPI{
		responses: []fakeSESResponse{
			{output: &sesv2.SendEmailOutput{MessageId: aws.String("0102018c-real-ses-id-from-response")}},
		},
	}
	m := newTestSESMailer(api)

	id, err := m.Send(context.Background(), Message{To: "alice@example.com", Subject: "Hi", TextBody: "body"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "0102018c-real-ses-id-from-response" {
		t.Errorf("messageID = %q, want the exact value from the SDK response", id)
	}
}

// TestSESMailer_Send_NoMessageIDIsAnError guards the other half of the same
// property: if SES ever accepts a send but the SDK response carries no
// MessageId, Send must fail loudly rather than return an empty or fabricated
// ID that would silently break #0038's bounce/complaint join.
func TestSESMailer_Send_NoMessageIDIsAnError(t *testing.T) {
	api := &fakeSESAPI{
		responses: []fakeSESResponse{
			{output: &sesv2.SendEmailOutput{}}, // MessageId nil
		},
	}
	m := newTestSESMailer(api)

	if _, err := m.Send(context.Background(), Message{To: "alice@example.com", Subject: "Hi", TextBody: "body"}); err == nil {
		t.Fatal("expected an error when SES returns no MessageId, got nil")
	}
}

// TestSESMailer_Send_AttachesConfigurationSet proves every send carries
// SES_CONFIGURATION_SET — the mechanism #0038's SNS event publishing depends
// on entirely. A send that silently omits it would still appear to succeed.
func TestSESMailer_Send_AttachesConfigurationSet(t *testing.T) {
	api := &fakeSESAPI{
		responses: []fakeSESResponse{{output: &sesv2.SendEmailOutput{MessageId: aws.String("id-1")}}},
	}
	m := newTestSESMailer(api)

	if _, err := m.Send(context.Background(), Message{To: "alice@example.com", Subject: "Hi", TextBody: "body"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(api.calls) != 1 {
		t.Fatalf("SendEmail called %d times, want 1", len(api.calls))
	}
	got := api.calls[0].ConfigurationSetName
	if got == nil || *got != "opencircuit-transactional" {
		t.Errorf("ConfigurationSetName = %v, want %q", got, "opencircuit-transactional")
	}
}

// TestSESMailer_Send_MultipartWithCustomHeaders exercises the full
// composition path #0035/#0028 depend on: both HTML and text bodies present
// (multipart/alternative) and custom List-Unsubscribe headers carried
// through verbatim.
func TestSESMailer_Send_MultipartWithCustomHeaders(t *testing.T) {
	api := &fakeSESAPI{
		responses: []fakeSESResponse{{output: &sesv2.SendEmailOutput{MessageId: aws.String("id-1")}}},
	}
	m := newTestSESMailer(api)

	msg := Message{
		To:       "alice@example.com",
		Subject:  "Newsletter",
		HTMLBody: "<p>hi</p>",
		TextBody: "hi",
		Headers: []Header{
			{Name: "List-Unsubscribe", Value: "<mailto:unsubscribe@lists.opencircuitsf.com>"},
			{Name: "List-Unsubscribe-Post", Value: "List-Unsubscribe=One-Click"},
			{Name: "List-Id", Value: "Open Circuit SF <list.opencircuitsf.com>"},
		},
	}
	if _, err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	simple := api.calls[0].Content.Simple
	if simple.Body.Html == nil || *simple.Body.Html.Data != "<p>hi</p>" {
		t.Errorf("HTML body = %v, want <p>hi</p>", simple.Body.Html)
	}
	if simple.Body.Text == nil || *simple.Body.Text.Data != "hi" {
		t.Errorf("text body = %v, want %q", simple.Body.Text, "hi")
	}
	if len(simple.Headers) != 3 {
		t.Fatalf("headers = %d, want 3: %+v", len(simple.Headers), simple.Headers)
	}
	wantHeaders := map[string]string{
		"List-Unsubscribe":      "<mailto:unsubscribe@lists.opencircuitsf.com>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		"List-Id":               "Open Circuit SF <list.opencircuitsf.com>",
	}
	for _, h := range simple.Headers {
		want, ok := wantHeaders[*h.Name]
		if !ok {
			t.Errorf("unexpected header %q", *h.Name)
			continue
		}
		if *h.Value != want {
			t.Errorf("header %q = %q, want %q", *h.Name, *h.Value, want)
		}
	}
}

// TestSESMailer_Send_SetsReplyToWhenConfigured proves the configured
// Reply-To address actually reaches the SES request, not just that a blank
// one is refused upstream (that's the worker's reply_to_missing gate,
// #0045's preflight). #0043's review bounced #0045 for claiming this was
// covered by TestSESMailer_Send_MultipartWithCustomHeaders, which asserts
// only the bodies and the three List-* headers and says nothing about
// Reply-To — grep for ReplyToAddresses across every *_test.go in the repo
// returned zero hits before this test existed. Deleting the
// `if m.replyTo != "" { input.ReplyToAddresses = ... }` block in Send must
// turn this red.
func TestSESMailer_Send_SetsReplyToWhenConfigured(t *testing.T) {
	api := &fakeSESAPI{
		responses: []fakeSESResponse{{output: &sesv2.SendEmailOutput{MessageId: aws.String("id-1")}}},
	}
	m := newTestSESMailer(api) // replyTo: "hello@opencircuitsf.com"

	if _, err := m.Send(context.Background(), Message{To: "alice@example.com", Subject: "Hi", TextBody: "body"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := api.calls[0].ReplyToAddresses
	want := []string{"hello@opencircuitsf.com"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("ReplyToAddresses = %v, want %v", got, want)
	}
}

// TestSESMailer_Send_OmitsReplyToAddressesWhenMailerReplyToIsBlank is the
// other half of the same property: a mailer constructed with no Reply-To
// (the worker's own preflight gate refuses to send in that case, but
// SESMailer itself must not fabricate a header) must not set
// ReplyToAddresses at all.
func TestSESMailer_Send_OmitsReplyToAddressesWhenMailerReplyToIsBlank(t *testing.T) {
	api := &fakeSESAPI{
		responses: []fakeSESResponse{{output: &sesv2.SendEmailOutput{MessageId: aws.String("id-1")}}},
	}
	m := newTestSESMailer(api)
	m.replyTo = ""

	if _, err := m.Send(context.Background(), Message{To: "alice@example.com", Subject: "Hi", TextBody: "body"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := api.calls[0].ReplyToAddresses; got != nil {
		t.Errorf("ReplyToAddresses = %v, want nil (blank replyTo must not set the header)", got)
	}
}

// TestSESMailer_Send_RetriesThrottlingWithBackoff proves the retry path: a
// TooManyRequestsException (SES v2's throttling error) on the first attempt
// is retried and the eventual success is returned, with the backoff delay
// invoked between attempts.
func TestSESMailer_Send_RetriesThrottlingWithBackoff(t *testing.T) {
	api := &fakeSESAPI{
		responses: []fakeSESResponse{
			{err: &types.TooManyRequestsException{Message: aws.String("slow down")}},
			{err: &types.TooManyRequestsException{Message: aws.String("slow down")}},
			{output: &sesv2.SendEmailOutput{MessageId: aws.String("id-after-retry")}},
		},
	}
	m := newTestSESMailer(api)

	var sleeps []time.Duration
	m.sleep = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}

	id, err := m.Send(context.Background(), Message{To: "alice@example.com", Subject: "Hi", TextBody: "body"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "id-after-retry" {
		t.Errorf("messageID = %q, want %q", id, "id-after-retry")
	}
	if len(api.calls) != 3 {
		t.Fatalf("SendEmail called %d times, want 3 (2 throttled + 1 success)", len(api.calls))
	}
	if len(sleeps) != 2 {
		t.Fatalf("backoff sleep called %d times, want 2", len(sleeps))
	}
	if sleeps[1] <= sleeps[0] {
		t.Errorf("backoff did not increase: %v then %v", sleeps[0], sleeps[1])
	}
}

// TestSESMailer_Send_NonThrottlingErrorDoesNotRetry proves a non-throttling
// error (e.g. MessageRejected) surfaces to the caller immediately, on the
// first attempt, rather than being retried like a throttling error.
func TestSESMailer_Send_NonThrottlingErrorDoesNotRetry(t *testing.T) {
	rejectErr := &types.AccountSuspendedException{Message: aws.String("account suspended")}
	api := &fakeSESAPI{
		responses: []fakeSESResponse{
			{err: rejectErr},
			{output: &sesv2.SendEmailOutput{MessageId: aws.String("should-not-be-reached")}},
		},
	}
	m := newTestSESMailer(api)
	m.sleep = func(context.Context, time.Duration) error {
		t.Fatal("sleep should not be called for a non-throttling error")
		return nil
	}

	_, err := m.Send(context.Background(), Message{To: "alice@example.com", Subject: "Hi", TextBody: "body"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if len(api.calls) != 1 {
		t.Fatalf("SendEmail called %d times, want 1 (no retry on a non-throttling error)", len(api.calls))
	}
	if !errors.As(err, new(*types.AccountSuspendedException)) {
		t.Errorf("returned error does not wrap the original AccountSuspendedException: %v", err)
	}
}

// TestSESMailer_Send_GivesUpAfterMaxAttempts proves persistent throttling
// eventually surfaces an error instead of retrying forever.
func TestSESMailer_Send_GivesUpAfterMaxAttempts(t *testing.T) {
	api := &fakeSESAPI{}
	for i := 0; i < maxSendAttempts; i++ {
		api.responses = append(api.responses, fakeSESResponse{err: &types.TooManyRequestsException{Message: aws.String("slow down")}})
	}
	m := newTestSESMailer(api)
	m.sleep = noopSleep

	_, err := m.Send(context.Background(), Message{To: "alice@example.com", Subject: "Hi", TextBody: "body"})
	if err == nil {
		t.Fatal("expected an error after exhausting retries, got nil")
	}
	if len(api.calls) != maxSendAttempts {
		t.Fatalf("SendEmail called %d times, want exactly maxSendAttempts=%d", len(api.calls), maxSendAttempts)
	}
}

// TestSESMailer_Send_RequiresRecipient and
// TestSESMailer_Send_RequiresABody guard the two structural preconditions
// Send checks before ever calling SES.
func TestSESMailer_Send_RequiresRecipient(t *testing.T) {
	m := newTestSESMailer(&fakeSESAPI{})
	if _, err := m.Send(context.Background(), Message{Subject: "Hi", TextBody: "body"}); err == nil {
		t.Fatal("expected an error for a message with no recipient")
	}
}

func TestSESMailer_Send_RequiresABody(t *testing.T) {
	m := newTestSESMailer(&fakeSESAPI{})
	if _, err := m.Send(context.Background(), Message{To: "alice@example.com", Subject: "Hi"}); err == nil {
		t.Fatal("expected an error for a message with neither an HTML nor a text body")
	}
}

// TestNewSESMailer_FailsClearlyOnMissingConfig proves construction fails
// loudly — rather than returning a mailer that later sends nothing — when
// AWS_REGION, SES_CONFIGURATION_SET, or EMAIL_FROM is unset. This is the
// property CLAUDE.md's "SES is not set up yet" caution and #0026's uniform
// 202 response both depend on: a misconfigured mailer must not look healthy.
func TestNewSESMailer_FailsClearlyOnMissingConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{
			name: "missing AWSRegion",
			cfg:  &config.Config{SESConfigurationSet: "cs", EmailFrom: "a@b.com"},
			want: "AWS_REGION",
		},
		{
			name: "missing SESConfigurationSet",
			cfg:  &config.Config{AWSRegion: "us-west-2", EmailFrom: "a@b.com"},
			want: "SES_CONFIGURATION_SET",
		},
		{
			name: "missing EmailFrom",
			cfg:  &config.Config{AWSRegion: "us-west-2", SESConfigurationSet: "cs"},
			want: "EMAIL_FROM",
		},
		{
			name: "all missing",
			cfg:  &config.Config{},
			want: "AWS_REGION, SES_CONFIGURATION_SET, EMAIL_FROM",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewSESMailer(context.Background(), tc.cfg)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}
