package sesnotify

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // test signs with SignatureVersion 1 as well as 2.
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// --- test fixture: one self-signed keypair/certificate per test binary. ---
// 2048-bit RSA keygen is the slowest thing in this suite, so it is generated
// once via sync.Once rather than per subtest.

var (
	fixtureOnce sync.Once
	fixtureKey  *rsa.PrivateKey
	fixtureCert *x509.Certificate
)

func testFixture(t *testing.T) (*rsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	fixtureOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("rsa.GenerateKey: %v", err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject:      pkix.Name{CommonName: "sesnotify test fixture"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			t.Fatalf("x509.CreateCertificate: %v", err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatalf("x509.ParseCertificate: %v", err)
		}
		fixtureKey, fixtureCert = key, cert
	})
	return fixtureKey, fixtureCert
}

// signMessage signs m's canonical string with the given SignatureVersion
// ("1" or "2") and the fixture key, setting m.SignatureVersion and
// m.Signature.
func signMessage(t *testing.T, priv *rsa.PrivateKey, m *Message, version string) {
	t.Helper()
	m.SignatureVersion = version
	canon, err := canonicalString(m)
	if err != nil {
		t.Fatalf("canonicalString: %v", err)
	}
	var sig []byte
	switch version {
	case "1":
		sum := sha1.Sum([]byte(canon)) //nolint:gosec
		sig, err = rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA1, sum[:])
	case "2":
		sum := sha256.Sum256([]byte(canon))
		sig, err = rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	default:
		t.Fatalf("signMessage: unsupported version %q", version)
	}
	if err != nil {
		t.Fatalf("rsa.SignPKCS1v15: %v", err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(sig)
}

// baseNotification returns a well-formed, unsigned Notification message with
// a valid-shaped (region-matching) SigningCertURL.
func baseNotification() *Message {
	return &Message{
		Type:           TypeNotification,
		MessageId:      "msg-1",
		Message:        `{"eventType":"Bounce"}`,
		Timestamp:      "2026-08-18T12:00:00.000Z",
		TopicArn:       "arn:aws:sns:us-west-2:123456789012:opencircuit-ses-events",
		SigningCertURL: "https://sns.us-west-2.amazonaws.com/SimpleNotificationService-abcdef.pem",
	}
}

// newTestVerifier returns a Verifier wired to a fake fetcher that always
// returns the fixture certificate, recording how many times it was called.
func newTestVerifier(t *testing.T, cert *x509.Certificate) (*Verifier, *int) {
	t.Helper()
	calls := 0
	v := NewVerifier("us-west-2", "arn:aws:sns:us-west-2:123456789012:opencircuit-ses-events", discardLogger())
	v.fetchCert = func(ctx context.Context, certURL string) (*x509.Certificate, error) {
		calls++
		return cert, nil
	}
	return v, &calls
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestVerify_ValidSignatureV2(t *testing.T) {
	key, cert := testFixture(t)
	v, _ := newTestVerifier(t, cert)

	m := baseNotification()
	signMessage(t, key, m, "2")

	if err := v.Verify(context.Background(), m); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_ValidSignatureV1(t *testing.T) {
	key, cert := testFixture(t)
	v, _ := newTestVerifier(t, cert)

	m := baseNotification()
	signMessage(t, key, m, "1")

	if err := v.Verify(context.Background(), m); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_UnsupportedSignatureVersion(t *testing.T) {
	key, cert := testFixture(t)
	v, _ := newTestVerifier(t, cert)

	m := baseNotification()
	signMessage(t, key, m, "2")
	m.SignatureVersion = "3"

	if err := v.Verify(context.Background(), m); err == nil {
		t.Fatal("expected error for unsupported SignatureVersion, got nil")
	}
}

func TestVerify_TamperedMessageBody(t *testing.T) {
	key, cert := testFixture(t)
	v, _ := newTestVerifier(t, cert)

	m := baseNotification()
	signMessage(t, key, m, "2")
	m.Message = `{"eventType":"Complaint"}` // mutated after signing

	if err := v.Verify(context.Background(), m); err == nil {
		t.Fatal("expected error for tampered Message, got nil")
	}
}

func TestVerify_TamperedTopicArn(t *testing.T) {
	key, cert := testFixture(t)
	v, _ := newTestVerifier(t, cert)

	m := baseNotification()
	signMessage(t, key, m, "2")
	m.TopicArn = "arn:aws:sns:us-west-2:123456789012:attacker-topic" // mutated after signing

	if err := v.Verify(context.Background(), m); err == nil {
		t.Fatal("expected error for tampered TopicArn, got nil")
	}
}

func TestVerify_TamperedDroppedSubject(t *testing.T) {
	key, cert := testFixture(t)
	v, _ := newTestVerifier(t, cert)

	m := baseNotification()
	m.Subject = "Original Subject"
	signMessage(t, key, m, "2")
	m.Subject = "" // dropped after signing — changes the canonical string

	if err := v.Verify(context.Background(), m); err == nil {
		t.Fatal("expected error when a signed Subject is dropped, got nil")
	}
}

func TestVerify_TopicArnMismatch(t *testing.T) {
	key, cert := testFixture(t)
	v, _ := newTestVerifier(t, cert)

	m := baseNotification()
	m.TopicArn = "arn:aws:sns:us-west-2:123456789012:some-other-topic"
	signMessage(t, key, m, "2") // genuinely signed, but for the wrong topic

	err := v.Verify(context.Background(), m)
	if err == nil {
		t.Fatal("expected error for a TopicArn outside the allowlist, got nil")
	}
}

func TestVerify_EmptyConfiguredTopicArnRejectsEverything(t *testing.T) {
	key, cert := testFixture(t)
	calls := 0
	v := NewVerifier("us-west-2", "", discardLogger()) // topic not yet configured
	v.fetchCert = func(ctx context.Context, certURL string) (*x509.Certificate, error) {
		calls++
		return cert, nil
	}

	m := baseNotification()
	signMessage(t, key, m, "2")

	if err := v.Verify(context.Background(), m); err == nil {
		t.Fatal("expected error when no topic ARN is configured, got nil")
	}
}

// TestVerify_HostileSigningCertURL is the important one: for each malicious
// SigningCertURL, Verify must reject the message AND the fetcher must never
// be invoked. This is what proves the SSRF guard runs before the fetch, not
// just "before verification succeeds".
func TestVerify_HostileSigningCertURL(t *testing.T) {
	key, cert := testFixture(t)

	hostile := []string{
		"http://sns.us-west-2.amazonaws.com/x.pem",                // not HTTPS
		"https://sns.us-west-2.amazonaws.com.evil.com/x.pem",      // suffix trick
		"https://evil.com/sns.us-west-2.amazonaws.com.pem",        // path trick
		"https://my-bucket.s3.amazonaws.com/x.pem",                // correction 1: shared-tenancy S3 host
		"https://sns.us-west-2.amazonaws.com@evil.com/x.pem",      // userinfo
		"https://169.254.169.254/latest/meta-data/",               // IMDS
		"https://sns.us-west-2.amazonaws.com:8443/x.pem",          // explicit port
		"https://sns.eu-west-1.amazonaws.com/x.pem",               // wrong region, region pinning on
		"file:///etc/passwd",                                      // non-http(s) scheme
		"",                                                        // empty
	}

	for _, certURL := range hostile {
		t.Run(certURL, func(t *testing.T) {
			calls := 0
			v := NewVerifier("us-west-2", "arn:aws:sns:us-west-2:123456789012:opencircuit-ses-events", discardLogger())
			v.fetchCert = func(ctx context.Context, u string) (*x509.Certificate, error) {
				calls++
				return cert, nil
			}

			m := baseNotification()
			m.SigningCertURL = certURL
			signMessage(t, key, m, "2")

			if err := v.Verify(context.Background(), m); err == nil {
				t.Errorf("Verify with SigningCertURL %q: expected error, got nil", certURL)
			}
			if calls != 0 {
				t.Errorf("Verify with SigningCertURL %q: fetcher was invoked %d times, want 0", certURL, calls)
			}
		})
	}
}

// TestValidateCertURL_PositiveControls asserts the host check accepts a
// case-varied host and a trailing-dot FQDN — both are legitimate DNS
// spellings of the same SNS host, and neither should be over-rejected by an
// implementation that does naive string equality.
func TestValidateCertURL_PositiveControls(t *testing.T) {
	v := NewVerifier("us-west-2", "irrelevant", discardLogger())

	ok := []string{
		"https://SNS.US-WEST-2.AMAZONAWS.COM/x.pem",
		"https://sns.us-west-2.amazonaws.com./x.pem",
	}
	for _, u := range ok {
		t.Run(u, func(t *testing.T) {
			if _, err := v.validateCertURL(u); err != nil {
				t.Errorf("validateCertURL(%q) = %v, want nil", u, err)
			}
		})
	}
}

func TestValidateCertURL_NoRegionPinSkipsRegionCheck(t *testing.T) {
	v := NewVerifier("", "irrelevant", discardLogger())
	if _, err := v.validateCertURL("https://sns.eu-west-1.amazonaws.com/x.pem"); err != nil {
		t.Errorf("validateCertURL with no configured region = %v, want nil", err)
	}
}

// TestFetchCertReal_RefusesRedirect drives the real fetcher against a local
// httptest server that 302s to another path, asserting the fetch fails
// rather than following the redirect. Host validation is intentionally not
// exercised here (the httptest URL's host is 127.0.0.1:port, which the SNS
// host allowlist would reject on unrelated grounds) — this test targets the
// CheckRedirect wiring specifically, calling fetchCertReal directly.
func TestFetchCertReal_RefusesRedirect(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should never be reached"))
	}))
	defer final.Close()

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirecting.Close()

	v := NewVerifier("us-west-2", "irrelevant", discardLogger())
	if _, err := v.fetchCertReal(context.Background(), redirecting.URL); err == nil {
		t.Fatal("fetchCertReal followed a redirect instead of refusing it")
	}
}

// TestFetchCertReal_FetchesAndParses drives the real fetcher against a local
// httptest server serving the fixture certificate as PEM, confirming the
// happy path of the real (non-faked) network code.
func TestFetchCertReal_FetchesAndParses(t *testing.T) {
	_, cert := testFixture(t)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pemBytes)
	}))
	defer srv.Close()

	v := NewVerifier("us-west-2", "irrelevant", discardLogger())
	got, err := v.fetchCertReal(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchCertReal: %v", err)
	}
	if got.SerialNumber.Cmp(cert.SerialNumber) != 0 {
		t.Errorf("fetched certificate serial = %v, want %v", got.SerialNumber, cert.SerialNumber)
	}
}

func TestFetchCertReal_NonOKStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	v := NewVerifier("us-west-2", "irrelevant", discardLogger())
	if _, err := v.fetchCertReal(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error for a non-200 response, got nil")
	}
}

// TestGetCert_CachesByURL asserts two Verify calls for the same
// SigningCertURL invoke the fetcher once, and that the cache does not grow
// past certCacheCap across many distinct URLs.
func TestGetCert_CachesByURL(t *testing.T) {
	key, cert := testFixture(t)
	v, calls := newTestVerifier(t, cert)

	m := baseNotification()
	signMessage(t, key, m, "2")

	if err := v.Verify(context.Background(), m); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	if err := v.Verify(context.Background(), m); err != nil {
		t.Fatalf("second Verify: %v", err)
	}
	if *calls != 1 {
		t.Errorf("fetcher called %d times for two Verify calls with the same URL, want 1", *calls)
	}
}

func TestGetCert_CacheBoundedAtCap(t *testing.T) {
	_, cert := testFixture(t)
	v := NewVerifier("us-west-2", "irrelevant", discardLogger())
	v.fetchCert = func(ctx context.Context, certURL string) (*x509.Certificate, error) {
		return cert, nil
	}

	for i := 0; i < 17; i++ {
		url := "https://sns.us-west-2.amazonaws.com/cert-" + string(rune('a'+i)) + ".pem"
		if _, err := v.getCert(context.Background(), url); err != nil {
			t.Fatalf("getCert(%d): %v", i, err)
		}
	}

	v.mu.RLock()
	n := len(v.cache)
	v.mu.RUnlock()
	if n > certCacheCap {
		t.Errorf("cache grew to %d entries, want <= %d", n, certCacheCap)
	}
}

// TestVerify_FetchFailureClassifiedAsRetryable asserts a certificate-fetch
// failure (network error, timeout, bad PEM, etc.) is wrapped in
// ErrCertUnavailable so a caller can map it to a 500 rather than a 403.
func TestVerify_FetchFailureClassifiedAsRetryable(t *testing.T) {
	key, _ := testFixture(t)
	v := NewVerifier("us-west-2", "arn:aws:sns:us-west-2:123456789012:opencircuit-ses-events", discardLogger())
	v.fetchCert = func(ctx context.Context, certURL string) (*x509.Certificate, error) {
		return nil, errors.New("simulated network failure")
	}

	m := baseNotification()
	signMessage(t, key, m, "2")

	err := v.Verify(context.Background(), m)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrCertUnavailable) {
		t.Errorf("Verify error = %v, want it to wrap ErrCertUnavailable", err)
	}
}

// TestVerify_SignatureFailureNotClassifiedAsRetryable is the converse: a bad
// signature must NOT be classified as ErrCertUnavailable, since that is
// specifically the "could not verify" bucket, not "failed to verify".
func TestVerify_SignatureFailureNotClassifiedAsRetryable(t *testing.T) {
	key, cert := testFixture(t)
	v, _ := newTestVerifier(t, cert)

	m := baseNotification()
	signMessage(t, key, m, "2")
	m.Message = "tampered"

	err := v.Verify(context.Background(), m)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrCertUnavailable) {
		t.Errorf("a tampered-signature error must not be classified as ErrCertUnavailable: %v", err)
	}
}
