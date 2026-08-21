package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/sesnotify"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// This file is #0038's §12 tests 16–17: proving, against the REAL
// mountAndServe wiring (not a handler unit test — those can't see a
// guard added one layer out, per #0103's own carried-in lesson recorded in
// unsubscribe_wiring_test.go), that POST /api/ses/notifications is reachable
// with no session cookie and that a message for the wrong topic answers 403
// with zero rows written.

const sesWiringTopicArn = "arn:aws:sns:us-west-2:123456789012:opencircuit-ses-wiring-events"

var (
	sesWiringFixtureOnce sync.Once
	sesWiringFixtureKey  *rsa.PrivateKey
	sesWiringFixtureCert *x509.Certificate
)

func sesWiringFixture(t *testing.T) (*rsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	sesWiringFixtureOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("rsa.GenerateKey: %v", err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject:      pkix.Name{CommonName: "ses wiring test fixture"},
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
		sesWiringFixtureKey, sesWiringFixtureCert = key, cert
	})
	return sesWiringFixtureKey, sesWiringFixtureCert
}

// sesWiringCanonicalString reproduces sesnotify's Notification field order
// (see internal/sesnotify/message.go) — duplicated here because
// canonicalString is unexported to that package, exactly as
// internal/handlers/ses_notifications_test.go's own copy is.
func sesWiringCanonicalString(m *sesnotify.Message) string {
	var b strings.Builder
	add := func(name, value string) {
		b.WriteString(name)
		b.WriteByte('\n')
		b.WriteString(value)
		b.WriteByte('\n')
	}
	add("Message", m.Message)
	add("MessageId", m.MessageId)
	if m.Subject != "" {
		add("Subject", m.Subject)
	}
	add("Timestamp", m.Timestamp)
	add("TopicArn", m.TopicArn)
	add("Type", m.Type)
	return b.String()
}

func sesWiringSign(t *testing.T, priv *rsa.PrivateKey, m *sesnotify.Message) {
	t.Helper()
	m.SignatureVersion = "2"
	sum := sha256.Sum256([]byte(sesWiringCanonicalString(m)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("rsa.SignPKCS1v15: %v", err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(sig)
}

// newSESNotificationsWiringHandler builds a real *handlers.SESNotificationsHandler
// over pool, with a real sesnotify.Verifier whose only injected seam is the
// certificate fetcher (sesnotify.NewVerifierForTesting) — the same approach
// internal/handlers/ses_notifications_test.go uses, so this file can post a
// genuinely signed envelope without a real AWS network call, while still
// exercising the real Verify/TopicArn logic and the real mountAndServe route
// table.
func newSESNotificationsWiringHandler(t *testing.T, pool *pgxpool.Pool) *handlers.SESNotificationsHandler {
	t.Helper()
	_, cert := sesWiringFixture(t)
	verifier := sesnotify.NewVerifierForTesting("us-west-2", sesWiringTopicArn, nil,
		func(ctx context.Context, certURL string) (*x509.Certificate, error) { return cert, nil })
	return handlers.NewSESNotificationsHandler(
		verifier, pool, sesnotify.NewStore(pool), subscribers.NewStore(pool), subscribers.NewSuppressionStore(pool),
		auth.NewStore(pool), audit.New(pool), nil,
	)
}

func sesWiringBaseMessage(t *testing.T, topicArn, innerJSON string) *sesnotify.Message {
	t.Helper()
	return &sesnotify.Message{
		Type:           sesnotify.TypeNotification,
		MessageId:      fmt.Sprintf("zz-0038-wiring-%d", time.Now().UnixNano()),
		Message:        innerJSON,
		Timestamp:      time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		TopicArn:       topicArn,
		SigningCertURL: "https://sns.us-west-2.amazonaws.com/SimpleNotificationService-abcdef.pem",
	}
}

func sesWiringStartServer(t *testing.T, pool *pgxpool.Pool, sesNotifyH *handlers.SESNotificationsHandler) (baseURL string, errCh chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}

	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg := &config.Config{
		Port:             port,
		BaseURL:          baseURL,
		WebAuthnRPID:     "localhost",
		WebAuthnRPOrigin: baseURL,
	}

	// requireSession/requireAdmin are the REAL middleware (not no-op
	// stand-ins) so that, if a future edit ever wrapped
	// POST /api/ses/notifications in either guard, the "no session cookie"
	// requests below would start failing instead of silently passing — the
	// same lesson unsubscribe_wiring_test.go's own #0103 carried-in comment
	// records. Neither is exercised by THIS route today (main.go registers
	// it unguarded), only by the admin routes this test never calls.
	requireSession := middleware.RequireSession(auth.NewStore(pool))
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}

	errCh = make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, pool,
			nil, nil, nil, nil, nil, nil, /* adminInterestsH: not exercised by this test */
			nil, /* adminSubscribersH: not exercised by this test */
			nil, /* adminSuppressionsH: not exercised by this test */
			nil, /* adminCampaignsH: not exercised by this test */
			nil, /* adminCampaignAudienceH: not exercised by this test */
			nil, nil, nil,
			nil, nil, nil, /* publicInterestsH, preferencesH, confirmH: not exercised by this test */
			nil, /* unsubscribeH: not exercised by this test */
			sesNotifyH,
			requireSession, requireAdmin, nil, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, baseURL, errCh, ready)
	return baseURL, errCh
}

// TestMountAndServe_SESNotifications_UnauthenticatedReachability is #0038
// §12 test 16: the route must answer with no session cookie at all — SNS
// posts with no bearer credential, and Verify (plus the TopicArn pin) IS
// the authentication. It distinguishes a guard-403 (sesnotify's own
// rejection, on a genuine crypto failure) from a session-403/401 (which
// would mean a future edit had wrongly wrapped this route in
// requireSession/requireAdmin) by posting a VALID signed envelope and
// asserting 200 — a session guard would 401/403 that unconditionally,
// regardless of body content, since it never sees a cookie.
func TestMountAndServe_SESNotifications_UnauthenticatedReachability(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), wiringDBConnectTimeout)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	key, _ := sesWiringFixture(t)
	sesNotifyH := newSESNotificationsWiringHandler(t, pool)
	baseURL, errCh := sesWiringStartServer(t, pool, sesNotifyH)
	client := &http.Client{Timeout: wiringHTTPTimeout}

	// A bad signature: the request carries no Cookie header at all (no
	// session), and the body is genuinely malformed crypto — this must 403
	// from sesnotify's own verification, not from a session guard.
	badSig := sesWiringBaseMessage(t, sesWiringTopicArn, `{"eventType":"Delivery"}`)
	sesWiringSign(t, key, badSig)
	badSig.Message = badSig.Message + " " // tamper after signing
	badResp := postJSONNoAuth(t, client, baseURL+"/api/ses/notifications", badSig)
	defer badResp.Body.Close()
	if badResp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad-signature status = %d, want 403", badResp.StatusCode)
	}

	// A genuinely valid signed envelope, still with no session cookie: must
	// be answered 200 by the real handler, not blocked by any guard.
	valid := sesWiringBaseMessage(t, sesWiringTopicArn, `{"eventType":"Delivery","mail":{"timestamp":"2026-08-20T00:00:00Z","messageId":"ses-wiring-1"}}`)
	sesWiringSign(t, key, valid)
	validResp := postJSONNoAuth(t, client, baseURL+"/api/ses/notifications", valid)
	defer validResp.Body.Close()
	if validResp.StatusCode != http.StatusOK {
		t.Fatalf("valid-signature status = %d, want 200 — the route must be reachable with no session (SNS never sends one)", validResp.StatusCode)
	}

	select {
	case err := <-errCh:
		t.Fatalf("mountAndServe exited unexpectedly: %v", err)
	default:
	}
}

// TestMountAndServe_SESNotifications_TopicArnMismatch is #0038 §12 test 17:
// a message for a topic other than the configured one answers 403 with zero
// rows written, proven against the real route (not just the verifier unit
// test).
func TestMountAndServe_SESNotifications_TopicArnMismatch(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), wiringDBConnectTimeout)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	key, _ := sesWiringFixture(t)
	sesNotifyH := newSESNotificationsWiringHandler(t, pool)
	baseURL, errCh := sesWiringStartServer(t, pool, sesNotifyH)
	client := &http.Client{Timeout: wiringHTTPTimeout}

	wrongTopic := "arn:aws:sns:us-west-2:999999999999:someone-elses-topic"
	m := sesWiringBaseMessage(t, wrongTopic, `{"eventType":"Bounce","mail":{"timestamp":"2026-08-20T00:00:00Z","messageId":"ses-wiring-2"},"bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"zz-0038-wiring@example.com"}]}}`)
	sesWiringSign(t, key, m)

	resp := postJSONNoAuth(t, client, baseURL+"/api/ses/notifications", m)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM email_events WHERE sns_message_id = $1`, m.MessageId).Scan(&count); err != nil {
		t.Fatalf("query email_events: %v", err)
	}
	if count != 0 {
		t.Errorf("email_events rows for %s = %d, want 0", m.MessageId, count)
	}

	select {
	case err := <-errCh:
		t.Fatalf("mountAndServe exited unexpectedly: %v", err)
	default:
	}
}

// postJSONNoAuth POSTs v as JSON with no Cookie header — SNS's real
// delivery carries none — and returns the response.
func postJSONNoAuth(t *testing.T, client *http.Client, url string, v any) *http.Response {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}
