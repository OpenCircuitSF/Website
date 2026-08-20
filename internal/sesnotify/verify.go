package sesnotify

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // SignatureVersion 1 support; see doc.go and canonicalString.
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// certCacheCap bounds the number of certificates the Verifier will cache by
// URL before flushing. The fetch happens before signature verification (the
// key is needed to verify), so an attacker who can reach the endpoint can
// drive cache growth by cycling through distinct valid-host paths. On
// reaching the cap the whole map is cleared rather than evicting one entry:
// the steady state is a single entry (AWS rotates the signing cert roughly
// yearly) and a flush costs one refetch.
const certCacheCap = 16

// certFetchTimeout bounds each certificate/SubscribeURL fetch.
const certFetchTimeout = 5 * time.Second

// maxCertBodyBytes bounds the response body read for a fetched certificate.
const maxCertBodyBytes = 32 << 10 // 32 KiB

// ErrCertUnavailable wraps a failure to *fetch* the signing certificate —
// network error, timeout, non-200 response, or a malformed PEM/certificate
// body. It is distinct from every other verification failure: a fetch
// failure means verification could not be performed, not that the message
// was forged. Callers (the #0038 HTTP handler) use errors.Is to map this to a
// 500 (SNS retries; the event is not lost) while every other Verify error
// maps to a 403 (SNS gives up; an attacker must not be able to induce a
// retry).
var ErrCertUnavailable = errors.New("sesnotify: certificate unavailable")

// certHostPattern matches the SNS service host, and only the SNS service
// host. "amazonaws.com" alone is a shared-tenancy domain — any AWS customer
// can put an arbitrary PEM at "<their-bucket>.s3.amazonaws.com", which would
// pass a laxer suffix check and let them forge signatures with a key they
// control. This pattern, applied to url.URL.Hostname() (never the raw
// string, never url.URL.Host, which carries the port), is the floor; the
// region pin in validateCertURL is the actual rule.
var certHostPattern = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com$`)

// Verifier checks SNS message signatures and topic provenance. It holds no
// database dependency; it is safe to construct once at startup and reuse
// concurrently.
type Verifier struct {
	// region pins the expected SigningCertURL/SubscribeURL host to
	// sns.<region>.amazonaws.com when non-empty. It is always set in
	// production (internal/config's AWS_REGION is a required variable).
	region string

	// topicARN is the only TopicArn Verify will accept. A valid SNS
	// signature proves SNS sent the message, not that our topic sent it —
	// this is the check that closes that gap. An empty topicARN means no
	// message can ever match, so every message is rejected (fail closed
	// rather than fail open on a missing config value).
	topicARN string

	httpClient *http.Client

	// fetchCert is the certificate-fetch seam. nil is never valid on a
	// constructed Verifier — NewVerifier always wires the real fetcher; tests
	// substitute a fake to run with no network access. validateCertURL's
	// check in Verify runs before this seam is ever invoked, so a test fake
	// cannot mask a missing URL check.
	fetchCert func(ctx context.Context, certURL string) (*x509.Certificate, error)

	mu    sync.RWMutex
	cache map[string]*x509.Certificate

	log *slog.Logger
}

// NewVerifier constructs a Verifier for the given region (internal/config's
// AWSRegion — pass "" only in tests that intend to skip region pinning) and
// the single topic ARN this endpoint accepts (internal/config's
// SESEventsTopicARN; pass "" to reject every message, e.g. before the topic
// is provisioned). A nil logger falls back to slog.Default().
func NewVerifier(region, topicARN string, log *slog.Logger) *Verifier {
	if log == nil {
		log = slog.Default()
	}
	v := &Verifier{
		region:   region,
		topicARN: topicARN,
		httpClient: &http.Client{
			Timeout: certFetchTimeout,
			// Redirects are a second SSRF vector: an attacker-influenced URL
			// that passes the host check could still redirect to an internal
			// address. AWS serves the PEM directly, so refuse to follow.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cache: make(map[string]*x509.Certificate),
		log:   log,
	}
	v.fetchCert = v.fetchCertReal
	return v
}

// Verify checks m's signature against SNS's signing certificate and confirms
// m.TopicArn matches the configured allowlist. A nil return means the
// message may be trusted; any non-nil error means it must not be acted on.
// Use errors.Is(err, ErrCertUnavailable) to distinguish "could not verify"
// (retryable) from "failed to verify" (forged or misconfigured, not
// retryable).
func (v *Verifier) Verify(ctx context.Context, m *Message) error {
	if m == nil {
		return errors.New("sesnotify: nil message")
	}

	// The host check on SigningCertURL runs first, unconditionally, before
	// the fetcher seam is ever reached — see doc.go. This ordering is the
	// property under test: a hostile URL must never reach fetchCert.
	certURL, err := v.validateCertURL(m.SigningCertURL)
	if err != nil {
		return fmt.Errorf("sesnotify: SigningCertURL: %w", err)
	}

	cert, err := v.getCert(ctx, certURL)
	if err != nil {
		return err // already wraps ErrCertUnavailable
	}

	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("sesnotify: signing certificate key is %T, want *rsa.PublicKey", cert.PublicKey)
	}

	canon, err := canonicalString(m)
	if err != nil {
		return fmt.Errorf("sesnotify: %w", err)
	}

	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return fmt.Errorf("sesnotify: invalid base64 Signature: %w", err)
	}

	var hash crypto.Hash
	var digest []byte
	switch m.SignatureVersion {
	case "1":
		sum := sha1.Sum([]byte(canon)) //nolint:gosec // see doc.go: not practically forgeable here.
		digest = sum[:]
		hash = crypto.SHA1
		v.log.Warn("sesnotify: verified a SignatureVersion 1 (SHA-1) message",
			"message_id", m.MessageId, "topic_arn", m.TopicArn)
	case "2":
		sum := sha256.Sum256([]byte(canon))
		digest = sum[:]
		hash = crypto.SHA256
	default:
		return fmt.Errorf("sesnotify: unsupported SignatureVersion %q", m.SignatureVersion)
	}

	if err := rsa.VerifyPKCS1v15(pub, hash, digest, sig); err != nil {
		return fmt.Errorf("sesnotify: signature verification failed: %w", err)
	}

	// A valid signature proves SNS sent this message, not that it came
	// through our topic — see doc.go. This check is fail-closed: an empty
	// topicARN (topic not yet configured) matches nothing.
	if v.topicARN == "" {
		v.log.Error("sesnotify: SES events topic ARN not configured; rejecting all SNS deliveries")
		return errors.New("sesnotify: no topic ARN configured")
	}
	if m.TopicArn != v.topicARN {
		v.log.Warn("sesnotify: rejected message for unconfigured topic",
			"message_id", m.MessageId, "topic_arn", m.TopicArn)
		return fmt.Errorf("sesnotify: TopicArn %q does not match the configured topic", m.TopicArn)
	}

	return nil
}

// validateCertURL is the SSRF guard applied to both SigningCertURL and
// SubscribeURL: both are attacker-influenced fields fetched over HTTP, and
// both must be constrained to an AWS-owned, region-pinned host before the
// fetch happens. It returns the original URL unchanged (never normalized —
// case and a trailing dot are accepted per SNS's own behaviour, and the
// fetch uses the URL as given).
func (v *Verifier) validateCertURL(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("scheme %q is not https", u.Scheme)
	}
	if u.User != nil {
		return "", errors.New("URL must not contain userinfo")
	}
	if u.Port() != "" {
		return "", errors.New("URL must not specify a port")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if !certHostPattern.MatchString(host) {
		return "", fmt.Errorf("host %q is not an SNS host", host)
	}
	if v.region != "" && host != "sns."+v.region+".amazonaws.com" {
		return "", fmt.Errorf("host %q does not match configured region %q", host, v.region)
	}
	return raw, nil
}

// getCert returns the certificate for certURL, using the URL-keyed cache
// when present. certURL must already have passed validateCertURL.
func (v *Verifier) getCert(ctx context.Context, certURL string) (*x509.Certificate, error) {
	v.mu.RLock()
	cert, ok := v.cache[certURL]
	v.mu.RUnlock()
	if ok {
		return cert, nil
	}

	cert, err := v.fetchCert(ctx, certURL)
	if err != nil {
		return nil, fmt.Errorf("sesnotify: %w: %v", ErrCertUnavailable, err)
	}

	v.mu.Lock()
	if len(v.cache) >= certCacheCap {
		v.cache = make(map[string]*x509.Certificate)
	}
	v.cache[certURL] = cert
	v.mu.Unlock()

	return cert, nil
}

// fetchCertReal is the production certificate fetcher: HTTPS GET, no
// redirects, a bounded body, and a validity-window check. It deliberately
// does not perform its own X.509 chain validation — the HTTPS fetch to a
// pinned sns.<region>.amazonaws.com host is what authenticates the
// certificate, since Go's TLS stack already validated that chain. A second,
// hand-rolled chain check would add a failure mode without adding a
// property.
func (v *Verifier) fetchCertReal(ctx context.Context, certURL string) (*x509.Certificate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, certURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// CheckRedirect returning http.ErrUseLastResponse makes Do return the
	// redirect response itself (with a non-2xx status) rather than an error,
	// so this status check is what actually refuses the redirect.
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching certificate", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCertBodyBytes))
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(body)
	if block == nil {
		return nil, errors.New("no PEM block found in certificate response")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing certificate: %w", err)
	}

	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return nil, fmt.Errorf("certificate not valid at %s (window %s to %s)",
			now.UTC().Format(time.RFC3339), cert.NotBefore.UTC().Format(time.RFC3339), cert.NotAfter.UTC().Format(time.RFC3339))
	}

	return cert, nil
}
