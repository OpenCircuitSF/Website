package mailing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
)

// sesAPI is the subset of *sesv2.Client this package calls. Defining it
// locally — rather than depending on the concrete *sesv2.Client everywhere —
// lets tests inject a fake that never dials AWS, the SES-v2-API equivalent of
// the sendMailFunc seam the SMTP transport used to have.
type sesAPI interface {
	SendEmail(ctx context.Context, params *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

// ErrNoMessageID is returned by Send when SES reports success but the
// response carries no MessageId — never a locally generated placeholder, so
// classifySendError (worker.go, #0045) can distinguish "SES accepted this
// but we have no join key" (terminalRow: the id is #0038's join key and a
// row with no id is unreconcilable) from every other outcome via errors.Is,
// not string matching.
var ErrNoMessageID = errors.New("mailing: SES accepted the send but returned no MessageId")

const (
	// maxSendAttempts bounds retries on SES throttling. 4 attempts with the
	// backoff schedule below (200ms, 400ms, 800ms) spans a bit over a second
	// before giving up, which is enough to ride out a short burst without a
	// single stalled recipient blocking a send loop for long.
	maxSendAttempts   = 4
	initialRetryDelay = 200 * time.Millisecond
)

// SESMailer sends email through the AWS SES v2 API. Credentials come from the
// AWS SDK's default credential chain — the EC2 instance role in production,
// falling back to ~/.aws/credentials locally (PRD §6.6; CLAUDE.md §10 item 2
// records that the instance role itself is the only thing not yet in place).
// There is no static-credential configuration anywhere in this project.
//
// Every send carries the configured SES_CONFIGURATION_SET, which is how event
// publishing to SNS is enabled for #0038's bounce/complaint ingestion, and
// retries types.TooManyRequestsException (SES v2's throttling error) with
// exponential backoff; every other error surfaces to the caller unchanged.
type SESMailer struct {
	client           sesAPI
	from             string
	replyTo          string
	configurationSet string

	// sleep is the backoff delay function; overridden in tests so retry tests
	// don't actually wait in real time.
	sleep func(ctx context.Context, d time.Duration) error
}

// NewSESMailer constructs an SESMailer backed by a real SES v2 client.
//
// It fails clearly — returns an error — rather than constructing a mailer
// that looks fine and then sends nothing (or sends into the wrong region, or
// sends without a configuration set so #0038 never sees the event) when
// AWS_REGION, SES_CONFIGURATION_SET, or EMAIL_FROM is unset. That distinction
// matters here specifically because #0026's subscribe endpoint returns a
// uniform 202 by design (CLAUDE.md §9) and would otherwise mask a
// misconfigured mailer completely.
func NewSESMailer(ctx context.Context, cfg *config.Config) (*SESMailer, error) {
	var missing []string
	if cfg.AWSRegion == "" {
		missing = append(missing, "AWS_REGION")
	}
	if cfg.SESConfigurationSet == "" {
		missing = append(missing, "SES_CONFIGURATION_SET")
	}
	if cfg.EmailFrom == "" {
		missing = append(missing, "EMAIL_FROM")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("mailing: cannot construct SES mailer: missing %s", strings.Join(missing, ", "))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("mailing: loading AWS config: %w", err)
	}

	return &SESMailer{
		client:           sesv2.NewFromConfig(awsCfg),
		from:             cfg.EmailFrom,
		replyTo:          cfg.EmailReplyTo,
		configurationSet: cfg.SESConfigurationSet,
		sleep:            sleepWithContext,
	}, nil
}

// Send builds a Simple-content SendEmailInput (multipart/alternative when
// both HTMLBody and TextBody are set — SES constructs that MIME structure
// itself) carrying msg.Headers as custom message headers, attaches the
// configured configuration set, and sends it through the SES v2 API. On
// success it returns the MessageId SES assigned, taken from the API
// response — never a locally generated placeholder, since that value is
// #0038's join key for bounce/complaint events.
func (m *SESMailer) Send(ctx context.Context, msg Message) (string, error) {
	if msg.To == "" {
		return "", errors.New("mailing: message has no recipient")
	}
	if msg.HTMLBody == "" && msg.TextBody == "" {
		return "", errors.New("mailing: message has neither an HTML nor a text body")
	}

	body := &types.Body{}
	if msg.HTMLBody != "" {
		body.Html = &types.Content{Data: aws.String(msg.HTMLBody), Charset: aws.String("UTF-8")}
	}
	if msg.TextBody != "" {
		body.Text = &types.Content{Data: aws.String(msg.TextBody), Charset: aws.String("UTF-8")}
	}

	headers := make([]types.MessageHeader, 0, len(msg.Headers))
	for _, h := range msg.Headers {
		headers = append(headers, types.MessageHeader{Name: aws.String(h.Name), Value: aws.String(h.Value)})
	}

	from := m.from
	if msg.From != "" {
		from = msg.From
	}

	input := &sesv2.SendEmailInput{
		FromEmailAddress:     aws.String(from),
		ConfigurationSetName: aws.String(m.configurationSet),
		Destination:          &types.Destination{ToAddresses: []string{msg.To}},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{Data: aws.String(msg.Subject), Charset: aws.String("UTF-8")},
				Body:    body,
				Headers: headers,
			},
		},
	}
	if m.replyTo != "" {
		input.ReplyToAddresses = []string{m.replyTo}
	}

	sleep := m.sleep
	if sleep == nil {
		sleep = sleepWithContext
	}

	var lastErr error
	delay := initialRetryDelay
	for attempt := 1; attempt <= maxSendAttempts; attempt++ {
		out, err := m.client.SendEmail(ctx, input)
		if err == nil {
			if out == nil || out.MessageId == nil || *out.MessageId == "" {
				return "", ErrNoMessageID
			}
			return *out.MessageId, nil
		}

		lastErr = err
		var throttled *types.TooManyRequestsException
		if !errors.As(err, &throttled) || attempt == maxSendAttempts {
			return "", fmt.Errorf("mailing: sending via SES: %w", err)
		}
		if sleepErr := sleep(ctx, delay); sleepErr != nil {
			return "", fmt.Errorf("mailing: sending via SES: %w", sleepErr)
		}
		delay *= 2
	}
	// Unreachable: the loop above always returns on its final attempt.
	return "", fmt.Errorf("mailing: sending via SES: %w", lastErr)
}

// sleepWithContext waits for d or ctx's cancellation, whichever comes first.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Ensure SESMailer satisfies Mailer at compile time.
var _ Mailer = (*SESMailer)(nil)
