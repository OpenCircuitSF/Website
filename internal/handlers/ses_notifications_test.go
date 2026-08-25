package handlers

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // signing a SignatureVersion "1" fixture, not verifying one in production code.
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/sesnotify"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// failingBeginner stands in for "the database is unreachable" — Begin
// always errors, so ses_notifications.go must answer 500 rather than
// pretending the message was recorded (#0038 §5's one exception to "always
// 200").
type failingBeginner struct{}

func (failingBeginner) Begin(ctx context.Context) (pgx.Tx, error) {
	return nil, fmt.Errorf("simulated: cannot begin transaction")
}

// ── SNS envelope fixtures: signing helpers this package's own copy of, ────
// since sesnotify.canonicalString and its test keypair helper are both
// unexported to that package. See sesnotify.NewVerifierForTesting's doc
// comment for why the fetcher seam (not the whole Verifier) is what's
// injected here — this reproduces the same "exercise the real Verify"
// property internal/sesnotify's own tests get from within that package.

const sesTestTopicArn = "arn:aws:sns:us-west-2:123456789012:opencircuit-ses-events"

var (
	sesFixtureOnce sync.Once
	sesFixtureKey  *rsa.PrivateKey
	sesFixtureCert *x509.Certificate
)

func sesTestFixture(t *testing.T) (*rsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	sesFixtureOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("rsa.GenerateKey: %v", err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject:      pkix.Name{CommonName: "ses_notifications test fixture"},
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
		sesFixtureKey, sesFixtureCert = key, cert
	})
	return sesFixtureKey, sesFixtureCert
}

// sesCanonicalString reproduces sesnotify's canonical-string field order
// (message.go's doc comment is the source of truth) for this file's signing
// helper. It is deliberately independent of the production builder — the
// point of signing test fixtures is to prove the PRODUCTION builder
// produces a signature Verify accepts, which a shared implementation
// couldn't prove.
func sesCanonicalString(m *sesnotify.Message) string {
	var b strings.Builder
	add := func(name, value string) {
		b.WriteString(name)
		b.WriteByte('\n')
		b.WriteString(value)
		b.WriteByte('\n')
	}
	switch m.Type {
	case sesnotify.TypeNotification:
		add("Message", m.Message)
		add("MessageId", m.MessageId)
		if m.Subject != "" {
			add("Subject", m.Subject)
		}
		add("Timestamp", m.Timestamp)
		add("TopicArn", m.TopicArn)
		add("Type", m.Type)
	case sesnotify.TypeSubscriptionConfirmation, sesnotify.TypeUnsubscribeConfirmation:
		add("Message", m.Message)
		add("MessageId", m.MessageId)
		add("SubscribeURL", m.SubscribeURL)
		add("Timestamp", m.Timestamp)
		add("Token", m.Token)
		add("TopicArn", m.TopicArn)
		add("Type", m.Type)
	}
	return b.String()
}

func sesSignMessage(t *testing.T, priv *rsa.PrivateKey, m *sesnotify.Message) {
	t.Helper()
	m.SignatureVersion = "2"
	canon := sesCanonicalString(m)
	sum := sha256.Sum256([]byte(canon))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("rsa.SignPKCS1v15: %v", err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(sig)
}

func sesSignMessageV1(t *testing.T, priv *rsa.PrivateKey, m *sesnotify.Message) {
	t.Helper()
	m.SignatureVersion = "1"
	canon := sesCanonicalString(m)
	sum := sha1.Sum([]byte(canon)) //nolint:gosec
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA1, sum[:])
	if err != nil {
		t.Fatalf("rsa.SignPKCS1v15: %v", err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(sig)
}

// sesUniqueID returns an SNS MessageId scoped to this test run (CLAUDE.md
// §8b: never target a literal or seeded id).
func sesUniqueID(t *testing.T, label string) string {
	t.Helper()
	return fmt.Sprintf("zz-0038-%s-%d", label, testdb.Unique())
}

// sesBaseNotification returns a well-formed, unsigned Notification envelope
// wrapping innerJSON (the SES event, itself JSON-encoded into the Message
// field per SNS's double-JSON shape).
func sesBaseNotification(t *testing.T, innerJSON string) *sesnotify.Message {
	t.Helper()
	return &sesnotify.Message{
		Type:           sesnotify.TypeNotification,
		MessageId:      sesUniqueID(t, "sns"),
		Message:        innerJSON,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		TopicArn:       sesTestTopicArn,
		SigningCertURL: "https://sns.us-west-2.amazonaws.com/SimpleNotificationService-abcdef.pem",
	}
}

func sesEncodeSESEvent(t *testing.T, ev map[string]any) string {
	t.Helper()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal SES event fixture: %v", err)
	}
	return string(b)
}

// sesTestHandler constructs a SESNotificationsHandler wired to real stores
// over pool and a real sesnotify.Verifier whose ONLY injected seam is the
// certificate fetcher (sesnotify.NewVerifierForTesting) — signature
// verification, TopicArn pinning, and validateCertURL all run for real.
func sesTestHandler(t *testing.T, pool *pgxpool.Pool) *SESNotificationsHandler {
	t.Helper()
	_, cert := sesTestFixture(t)
	verifier := sesnotify.NewVerifierForTesting("us-west-2", sesTestTopicArn, nil, func(ctx context.Context, certURL string) (*x509.Certificate, error) {
		return cert, nil
	})
	return NewSESNotificationsHandler(
		verifier, pool, sesnotify.NewStore(pool), subscribers.NewStore(pool), subscribers.NewSuppressionStore(pool),
		auth.NewStore(pool), audit.New(pool), nil,
	)
}

// sesPost invokes h.Notify directly (no network listener needed — the
// handler is pure net/http) and returns the recorded response.
func sesPost(h *SESNotificationsHandler, m *sesnotify.Message) *httptest.ResponseRecorder {
	body, _ := json.Marshal(m)
	req := httptest.NewRequest(http.MethodPost, "/api/ses/notifications", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h.Notify(rec, req)
	return rec
}

func sesNotificationsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

func sesCleanupSuppression(t *testing.T, pool *pgxpool.Pool, email string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM suppressions WHERE email = $1`, email)
	})
}

func sesCleanupEvents(t *testing.T, pool *pgxpool.Pool, snsMessageID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM email_events WHERE sns_message_id = $1`, snsMessageID)
	})
}

// ── Tests ────────────────────────────────────────────────────────────────

func TestSESNotifications_PermanentBounce_SuppressesAndMarksBounced(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-perm-1"},
		"bounce": map[string]any{
			"bounceType": "Permanent", "bounceSubType": "General",
			"bouncedRecipients": []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}

	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Error("IsSuppressed = false, want true after a permanent bounce")
	}

	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusBounced {
		t.Errorf("subscriber status = %q, want %q", sub.Status, subscribers.StatusBounced)
	}

	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("email_events rows = %d, want 1", len(rows))
	}
	if rows[0].BounceType == nil || *rows[0].BounceType != "Permanent" {
		t.Errorf("BounceType = %v, want Permanent", rows[0].BounceType)
	}
}

func TestSESNotifications_TransientBounce_RecordsOnlyNoSuppression(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-trans-1"},
		"bounce": map[string]any{
			"bounceType": "Transient", "bounceSubType": "General",
			"bouncedRecipients": []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}

	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true, want false — a transient bounce alone must never suppress (that's #0039's job)")
	}

	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusActive {
		t.Errorf("subscriber status = %q, want unchanged %q", sub.Status, subscribers.StatusActive)
	}

	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 || rows[0].BounceType == nil || *rows[0].BounceType != "Transient" {
		t.Fatalf("rows = %+v, want exactly one row with bounce_type=Transient", rows)
	}
}

// ── #0124: consecutive-streak repeated soft-bounce suppression ──────────────
//
// #0039/#0109's rolling-window count (seeded prior email_events rows, then
// one bounce "under test") is gone. #0124 replaces it with a CONSECUTIVE
// STREAK stored on subscribers.soft_bounce_streak, incremented only by a
// real bounce through the handler under test — so these tests build the
// streak by posting N bounces through h, not by seeding raw rows.

// sesSetSoftBounceThreshold temporarily overrides #0039's threshold setting
// for one test, restoring the migrations/000015 default (5) via t.Cleanup
// so later tests in the same run see the seeded value again.
func sesSetSoftBounceThreshold(t *testing.T, pool *pgxpool.Pool, count int) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`UPDATE settings SET value = $1, updated_at = now() WHERE key = 'soft_bounce_threshold_count'`, strconv.Itoa(count),
	); err != nil {
		t.Fatalf("set soft_bounce_threshold_count: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `UPDATE settings SET value = '5', updated_at = now() WHERE key = 'soft_bounce_threshold_count'`)
	})
}

// sesPostTransientBounce posts one signed Transient bounce for email through
// h and returns the response, for the repeated-soft-bounce tests below where
// only the bounce itself (not permanent/complaint framing) matters.
func sesPostTransientBounce(t *testing.T, h *SESNotificationsHandler, key *rsa.PrivateKey, pool *pgxpool.Pool, email, label string) *httptest.ResponseRecorder {
	t.Helper()
	return sesPostBounce(t, h, key, pool, email, label, "Transient", "General")
}

// sesPostBounce is sesPostTransientBounce's general form (#0109): it takes
// bounceType/bounceSubType directly, so a test can post an Undetermined
// bounce or a sender-fault-subtype Transient bounce as "the bounce under
// test" for the repeated-soft-bounce suite below.
func sesPostBounce(t *testing.T, h *SESNotificationsHandler, key *rsa.PrivateKey, pool *pgxpool.Pool, email, label, bounceType, bounceSubType string) *httptest.ResponseRecorder {
	t.Helper()
	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-" + label},
		"bounce": map[string]any{
			"bounceType": bounceType, "bounceSubType": bounceSubType,
			"bouncedRecipients": []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)
	return sesPost(h, m)
}

// sesPostConsecutiveBounces posts n bounces of (bounceType, bounceSubtype)
// for email through h, one at a time (each a real HTTP round trip through
// the handler under test, so each genuinely increments
// subscribers.soft_bounce_streak the way a real SES redelivery sequence
// would) — the streak-based replacement for the old raw-row seeding helpers.
// Returns the LAST response, so a caller posting exactly the bounce that
// should cross the threshold can assert on it directly.
func sesPostConsecutiveBounces(t *testing.T, h *SESNotificationsHandler, key *rsa.PrivateKey, pool *pgxpool.Pool, email, labelPrefix string, n int, bounceType, bounceSubtype string) *httptest.ResponseRecorder {
	t.Helper()
	var resp *httptest.ResponseRecorder
	for i := 1; i <= n; i++ {
		resp = sesPostBounce(t, h, key, pool, email, fmt.Sprintf("%s-%d", labelPrefix, i), bounceType, bounceSubtype)
		if resp.Code != http.StatusOK {
			t.Fatalf("bounce %d/%d status = %d, want 200 (body=%s)", i, n, resp.Code, resp.Body.String())
		}
	}
	return resp
}

// sesPostDelivery posts one signed Delivery event for email through h.
func sesPostDelivery(t *testing.T, h *SESNotificationsHandler, key *rsa.PrivateKey, pool *pgxpool.Pool, email, label string) *httptest.ResponseRecorder {
	t.Helper()
	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Delivery",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-" + label},
		"delivery":  map[string]any{"recipients": []string{email}},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)
	return sesPost(h, m)
}

// TestSESNotifications_RepeatedTransientBounce_OffByOne_FourDoesNotSuppressFiveDoes
// is #0124's threshold mutation check: with the default threshold (5), a
// streak of 4 consecutive transient bounces must NOT suppress, and the 5th
// (posted right after, same recipient) must. This proves the streak
// comparison is live, not merely present — a mutated `<=` in place of `<`
// would suppress on the 4th; a threshold that never fires would let the 5th
// through too.
func TestSESNotifications_RepeatedTransientBounce_OffByOne_FourDoesNotSuppressFiveDoes(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)
	suppressions := subscribers.NewSuppressionStore(pool)

	// 4 consecutive bounces: streak = 4, must NOT cross the threshold of 5.
	sesPostConsecutiveBounces(t, h, key, pool, email, "offbyone", 4, "Transient", "General")
	suppressed, err := suppressions.IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed after 4th: %v", err)
	}
	if suppressed {
		t.Fatal("IsSuppressed = true after the 4th consecutive transient bounce, want false (threshold is 5)")
	}
	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID after 4th: %v", err)
	}
	if sub.Status != subscribers.StatusActive {
		t.Errorf("status after 4th = %q, want unchanged %q", sub.Status, subscribers.StatusActive)
	}
	if sub.SoftBounceStreak != 4 {
		t.Errorf("soft_bounce_streak after 4th = %d, want 4", sub.SoftBounceStreak)
	}

	// The 5th consecutive bounce: streak = 5, must cross the threshold.
	resp := sesPostTransientBounce(t, h, key, pool, email, "offbyone-5th")
	if resp.Code != http.StatusOK {
		t.Fatalf("5th bounce status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}
	suppressed, err = suppressions.IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed after 5th: %v", err)
	}
	if !suppressed {
		t.Fatal("IsSuppressed = false after the 5th consecutive transient bounce, want true (threshold crossed)")
	}
	list, err := suppressions.ListByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	var sawReason bool
	for _, s := range list {
		if s.Reason == subscribers.SuppressionReasonRepeatedSoftBounce {
			sawReason = true
		}
	}
	if !sawReason {
		t.Errorf("suppressions for %s = %+v, want a repeated_soft_bounce row", email, list)
	}
	sub, err = subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID after 5th: %v", err)
	}
	if sub.Status != subscribers.StatusBounced {
		t.Errorf("status after 5th = %q, want %q", sub.Status, subscribers.StatusBounced)
	}
	if sub.SoftBounceStreak != 5 {
		t.Errorf("soft_bounce_streak after 5th = %d, want 5", sub.SoftBounceStreak)
	}
}

// TestSESNotifications_RepeatedTransientBounce_StaysUnderThreshold_NoSuppression
// is #0039's "staying under [the threshold]" acceptance criterion, restated
// for a streak: 3 consecutive bounces must not suppress.
func TestSESNotifications_RepeatedTransientBounce_StaysUnderThreshold_NoSuppression(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	sesPostConsecutiveBounces(t, h, key, pool, email, "under-threshold", 3, "Transient", "General")

	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true, want false — only 3 consecutive transient bounces, threshold is 5")
	}
	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusActive {
		t.Errorf("status = %q, want unchanged %q", sub.Status, subscribers.StatusActive)
	}
}

// TestSESNotifications_Delivery_ResetsStreak_SoLaterBouncesDoNotCumulate is
// #0124's central behavioral change from the rolling-window rule it
// replaces (PRD §6.9: "A SES Delivery event for the address sets it back to
// 0"): 4 consecutive bounces (one short of the threshold), a Delivery event,
// then 4 MORE consecutive bounces — 8 bounces total, twice the threshold —
// must still NOT suppress, because the Delivery event reset the streak
// back to 0 partway through. The old windowed rule would have suppressed
// here (8 bounces, any 30-day window); the streak rule must not.
func TestSESNotifications_Delivery_ResetsStreak_SoLaterBouncesDoNotCumulate(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	sesPostConsecutiveBounces(t, h, key, pool, email, "pre-delivery", 4, "Transient", "General")
	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID before delivery: %v", err)
	}
	if sub.SoftBounceStreak != 4 {
		t.Fatalf("soft_bounce_streak before delivery = %d, want 4", sub.SoftBounceStreak)
	}

	resp := sesPostDelivery(t, h, key, pool, email, "resets-streak")
	if resp.Code != http.StatusOK {
		t.Fatalf("delivery status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}
	sub, err = subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID after delivery: %v", err)
	}
	if sub.SoftBounceStreak != 0 {
		t.Fatalf("soft_bounce_streak after delivery = %d, want 0", sub.SoftBounceStreak)
	}
	if sub.LastDeliveryAt == nil {
		t.Error("last_delivery_at not stamped after a Delivery event")
	}

	sesPostConsecutiveBounces(t, h, key, pool, email, "post-delivery", 4, "Transient", "General")

	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true, want false — the Delivery event reset the streak, so 4+4 bounces around it must not cumulate to 8")
	}
	sub, err = subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusActive {
		t.Errorf("status = %q, want unchanged %q", sub.Status, subscribers.StatusActive)
	}
	if sub.SoftBounceStreak != 4 {
		t.Errorf("soft_bounce_streak = %d, want 4 (only the post-delivery run counts)", sub.SoftBounceStreak)
	}
}

// TestSESNotifications_RemovingSuppression_ResetsStreak is #0124's other
// reset path: "Removing a suppression resets the streak to 0 — a
// re-enabled address gets a fresh runway, not one bounce from
// re-suppression." Builds a streak to the threshold (suppressing the
// address), removes the suppression, and asserts the streak is back to 0 —
// so a SINGLE bounce afterward does not immediately re-suppress it.
func TestSESNotifications_RemovingSuppression_ResetsStreak(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)
	suppressions := subscribers.NewSuppressionStore(pool)

	sesPostConsecutiveBounces(t, h, key, pool, email, "to-threshold", 5, "Transient", "General")
	suppressed, err := suppressions.IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed after 5th: %v", err)
	}
	if !suppressed {
		t.Fatal("IsSuppressed = false after 5 consecutive bounces, want true")
	}

	if _, err := suppressions.Remove(context.Background(), email, subscribers.SuppressionReasonRepeatedSoftBounce); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID after Remove: %v", err)
	}
	if sub.SoftBounceStreak != 0 {
		t.Fatalf("soft_bounce_streak after removing the suppression = %d, want 0 — a re-enabled address must get a fresh runway", sub.SoftBounceStreak)
	}

	// One bounce after the reset must not immediately re-suppress.
	resp := sesPostTransientBounce(t, h, key, pool, email, "after-reset")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}
	suppressed, err = suppressions.IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed after one post-reset bounce: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true after ONE bounce following a streak reset, want false — one bounce from re-suppression is exactly the bug this criterion forbids")
	}
}

// TestSESNotifications_RepeatedTransientBounce_RespectsConfiguredThreshold is
// #0039's "threshold values live in settings... must be configurable"
// acceptance criterion: lowering the configured threshold to 2 makes the
// 2nd consecutive transient bounce suppress, where the default (5) would
// not.
func TestSESNotifications_RepeatedTransientBounce_RespectsConfiguredThreshold(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)
	sesSetSoftBounceThreshold(t, pool, 2)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	sesPostConsecutiveBounces(t, h, key, pool, email, "configured-threshold", 2, "Transient", "General")

	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Error("IsSuppressed = false, want true — the configured threshold (2) was reached by the 2nd consecutive transient bounce")
	}
}

// TestSESNotifications_RepeatedTransientBounce_ComplainedStaysComplained is
// CLAUDE.md §9's "complained subscribers never auto-resubscribe" guarantee,
// applied to the threshold path specifically (mirrors
// TestSESNotifications_ComplainedIsNotErasedByLaterBounce for Permanent).
func TestSESNotifications_RepeatedTransientBounce_ComplainedStaysComplained(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusComplained)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	sesPostConsecutiveBounces(t, h, key, pool, email, "complained-threshold", 5, "Transient", "General")

	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusComplained {
		t.Errorf("status = %q, want unchanged %q (CLAUDE.md §9)", sub.Status, subscribers.StatusComplained)
	}

	// The suppression write still happens unconditionally (#0100), same as
	// the Permanent case's carried-in criterion.
	list, err := subscribers.NewSuppressionStore(pool).ListByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	var sawReason bool
	for _, s := range list {
		if s.Reason == subscribers.SuppressionReasonRepeatedSoftBounce {
			sawReason = true
		}
	}
	if !sawReason {
		t.Errorf("suppressions for %s = %+v, want a repeated_soft_bounce row even though status stayed complained", email, list)
	}
}

// TestSESNotifications_RepeatedUndeterminedBounce_SuppressesAtThreshold is
// #0109's Q1 end to end: an address whose bounces are all classified
// Undetermined (never Transient) must still be suppressed once its streak
// crosses the threshold — the exact "sending into a hole forever" gap #0109
// exists to close.
func TestSESNotifications_RepeatedUndeterminedBounce_SuppressesAtThreshold(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)
	suppressions := subscribers.NewSuppressionStore(pool)

	sesPostConsecutiveBounces(t, h, key, pool, email, "undetermined", 5, sesnotify.BounceTypeUndetermined, "")

	suppressed, err := suppressions.IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Fatal("IsSuppressed = false, want true — 5 consecutive Undetermined bounces should cross the default threshold of 5 (#0109 Q1)")
	}
	list, err := suppressions.ListByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	var sawReason bool
	for _, s := range list {
		if s.Reason == subscribers.SuppressionReasonRepeatedSoftBounce {
			sawReason = true
		}
	}
	if !sawReason {
		t.Errorf("suppressions for %s = %+v, want a repeated_soft_bounce row", email, list)
	}
	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusBounced {
		t.Errorf("status = %q, want %q", sub.Status, subscribers.StatusBounced)
	}
}

// TestSESNotifications_RepeatedSenderFaultBounce_NeverSuppresses is #0109's
// Q2 end to end: MessageTooLarge/ContentRejected/AttachmentRejected are
// faults in OUR message, not evidence the recipient's address is bad, and
// must never suppress a live subscriber — even well past the default
// threshold, and even though the streak column would otherwise be
// incremented. 11 bounces (more than twice the default threshold of 5), all
// carrying a sender-fault subtype, must produce no suppression and leave
// the streak at 0 (never incremented at all, not merely "not enough").
func TestSESNotifications_RepeatedSenderFaultBounce_NeverSuppresses(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	sesPostConsecutiveBounces(t, h, key, pool, email, "senderfault", 11, "Transient", sesnotify.BounceSubTypeMessageTooLarge)

	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true, want false — MessageTooLarge is a fault in our own message, not the recipient's address (#0109 Q2)")
	}
	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusActive {
		t.Errorf("status = %q, want unchanged %q", sub.Status, subscribers.StatusActive)
	}
	if sub.SoftBounceStreak != 0 {
		t.Errorf("soft_bounce_streak = %d, want 0 — a sender-fault subtype must never increment it", sub.SoftBounceStreak)
	}
}

func TestSESNotifications_Complaint_SuppressesAndMarksComplained(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Complaint",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-compl-1"},
		"complaint": map[string]any{
			"complaintFeedbackType": "abuse",
			"complainedRecipients":  []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}

	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Error("IsSuppressed = false, want true after a complaint")
	}

	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusComplained {
		t.Errorf("subscriber status = %q, want %q", sub.Status, subscribers.StatusComplained)
	}
}

func TestSESNotifications_ComplaintNotSpam_NoSuppression(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Complaint",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-notspam-1"},
		"complaint": map[string]any{
			"complaintFeedbackType": "not-spam",
			"complainedRecipients":  []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}

	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true, want false — complaintFeedbackType=not-spam must never suppress")
	}
	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusActive {
		t.Errorf("subscriber status = %q, want unchanged %q", sub.Status, subscribers.StatusActive)
	}
}

// TestSESNotifications_Delivery_RecordsEventNeverSuppressesResetsStreak is
// #0124's Delivery mapping: never suppresses, resets the streak (already 0
// here, so this proves the reset doesn't ERROR on a zero streak, not just
// that it succeeds from a nonzero one — that positive case is
// TestSESNotifications_Delivery_ResetsStreak_SoLaterBouncesDoNotCumulate
// above), stamps last_delivery_at, and writes a subscriber_events
// `delivered` row (PRD §6.11).
func TestSESNotifications_Delivery_RecordsEventNeverSuppressesResetsStreak(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Delivery",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-delivery-1"},
		"delivery":  map[string]any{"recipients": []string{email}},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}

	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true, want false — Delivery never suppresses")
	}
	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 || rows[0].EventType != "Delivery" {
		t.Fatalf("rows = %+v, want exactly one Delivery row", rows)
	}

	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusActive {
		t.Errorf("status = %q, want unchanged %q", sub.Status, subscribers.StatusActive)
	}
	if sub.SoftBounceStreak != 0 {
		t.Errorf("soft_bounce_streak = %d, want 0", sub.SoftBounceStreak)
	}
	if sub.LastDeliveryAt == nil {
		t.Error("last_delivery_at not stamped")
	}

	events, err := subscribers.NewStore(pool).EventHistory(context.Background(), subID)
	if err != nil {
		t.Fatalf("EventHistory: %v", err)
	}
	var sawDelivered bool
	for _, e := range events {
		if e.Action == subscribers.ActionDelivered {
			sawDelivered = true
		}
	}
	if !sawDelivered {
		t.Errorf("events for subscriber %d = %+v, want a %q row", subID, events, subscribers.ActionDelivered)
	}
}

func TestSESNotifications_MalformedOuterBody_Returns200NoRows(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)

	req := httptest.NewRequest(http.MethodPost, "/api/ses/notifications", strings.NewReader("not json at all"))
	rec := httptest.NewRecorder()
	h.Notify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (an uninterpretable body must never be retried)", rec.Code)
	}
	_ = pool // no message id to look up; nothing to assert beyond the status
}

func TestSESNotifications_UnparseableInnerMessage_Returns200RecordsRawPayload(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	m := sesBaseNotification(t, "this is not json")
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}

	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (PRD §6.7 item 3: the raw payload is recorded even when uninterpretable)", len(rows))
	}
	if rows[0].EventType != "" {
		t.Errorf("EventType = %q, want empty (unknown)", rows[0].EventType)
	}
	var payload map[string]string
	if err := json.Unmarshal(rows[0].Payload, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v (%s)", err, rows[0].Payload)
	}
	if payload["raw"] != "this is not json" {
		t.Errorf("payload[raw] = %q, want the original unparseable string", payload["raw"])
	}
}

func TestSESNotifications_UnknownEventType_Returns200RecordsRowNoStateChange(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "SomeFutureEventType",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-unknown-1", "destination": []string{email}},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}

	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 || rows[0].EventType != "SomeFutureEventType" {
		t.Fatalf("rows = %+v, want one row recording the unrecognized eventType verbatim", rows)
	}
	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true, want false")
	}
}

func TestSESNotifications_StorageFailure_Returns500(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	key, _ := sesTestFixture(t)
	_, cert := sesTestFixture(t)

	verifier := sesnotify.NewVerifierForTesting("us-west-2", sesTestTopicArn, nil, func(ctx context.Context, certURL string) (*x509.Certificate, error) {
		return cert, nil
	})
	// A pool whose Begin always fails stands in for "the transaction could
	// not even start" (DB down) — the one case #0038 §5 requires a 500,
	// not a 200: the raw payload could not be recorded at all, so SNS's
	// retry is the only thing standing between us and a silently lost
	// event.
	h := NewSESNotificationsHandler(
		verifier, failingBeginner{}, sesnotify.NewStore(pool), subscribers.NewStore(pool), subscribers.NewSuppressionStore(pool),
		auth.NewStore(pool), nil, nil,
	)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-storagefail-1"},
		"bounce":    map[string]any{"bounceType": "Permanent", "bouncedRecipients": []map[string]string{{"emailAddress": "zz-0038-storagefail@example.com"}}},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)

	resp := sesPost(h, m)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — a storage failure on a genuinely trustworthy message must not be silently dropped as a 200", resp.Code)
	}
}

// commitFailingTx wraps a REAL pgx.Tx so every write in it — the
// email_events insert, suppressions.AddTx, subscribers.MarkBouncedTx —
// executes for real against the live database, and only Commit is made to
// fail. This models a crash or a serialization failure landing in the
// window between the last write and COMMIT: the exact window #0033 shipped
// AddTx to close, and the one plan §12 test 9 requires a test for. Every
// other pgx.Tx method (including Rollback, which handleNotification's own
// deferred cleanup calls) delegates to the embedded real transaction.
type commitFailingTx struct {
	pgx.Tx
}

func (t commitFailingTx) Commit(ctx context.Context) error {
	return fmt.Errorf("simulated: commit failed (crash window between last write and COMMIT)")
}

// commitFailingBeginner begins a real transaction against pool — so the
// SQL that runs inside it is the genuine production statements, not a
// stand-in — and wraps the first failN of them in commitFailingTx. Once
// calls exceeds failN, Begin returns the real, unwrapped Tx, so a retry
// (the identical signed envelope re-posted, exactly as SNS would) commits
// normally. That is what lets one test prove both halves of plan §12 test
// 9: the crash-window rollback, and that the retry lands all three writes.
type commitFailingBeginner struct {
	pool  *pgxpool.Pool
	failN int
	calls int
}

func (b *commitFailingBeginner) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	b.calls++
	if b.calls <= b.failN {
		return commitFailingTx{Tx: tx}, nil
	}
	return tx, nil
}

// TestSESNotifications_CrashWindowEquivalence_RetryLandsAllWrites is plan
// §12 test 9 — the case the first pass's review found entirely untested.
// The review proved the gap by mutation: moving AddTx/MarkBouncedTx/
// MarkComplainedTx off the shared transaction and onto the pool left every
// other test green, because nothing exercised a failure landing INSIDE the
// window the transaction exists to close (issues/0038.md, "Blocker 1").
//
// This test forces exactly that failure — every write for one SNS message
// runs for real, then COMMIT itself fails — and asserts all three writes
// (email_events, the suppression row, the subscriber status change) are
// atomically absent afterward, not merely that one of them is. That last
// part is what actually catches the mutation: under it, the suppression
// row and the status change commit independently, on the pool, before the
// wrapped transaction's own (failing) Commit is ever reached, so they
// would survive this rollback even though email_events did not.
//
// It then re-posts the identical signed envelope and asserts all three
// land — proving the retry the mutation would otherwise silently defeat
// (a hard-bounced address kept receiving mail with nothing to show why).
func TestSESNotifications_CrashWindowEquivalence_RetryLandsAllWrites(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	key, _ := sesTestFixture(t)
	_, cert := sesTestFixture(t)

	verifier := sesnotify.NewVerifierForTesting("us-west-2", sesTestTopicArn, nil, func(ctx context.Context, certURL string) (*x509.Certificate, error) {
		return cert, nil
	})
	beginner := &commitFailingBeginner{pool: pool, failN: 1}
	h := NewSESNotificationsHandler(
		verifier, beginner, sesnotify.NewStore(pool), subscribers.NewStore(pool), subscribers.NewSuppressionStore(pool),
		auth.NewStore(pool), nil, nil,
	)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-crashwindow-1"},
		"bounce": map[string]any{
			"bounceType": "Permanent", "bounceSubType": "General",
			"bouncedRecipients": []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	// First delivery: the real writes happen inside the transaction, then
	// COMMIT fails — modeling a crash in the window between the last write
	// and commit. The handler must answer 500 (criterion 7's "storage
	// failure -> 500" exception), not pretend the message was recorded.
	first := sesPost(h, m)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first (commit-failing) delivery status = %d, want 500", first.Code)
	}

	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("email_events rows after commit failure = %d, want 0 — the insert must have rolled back with everything else", len(rows))
	}
	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true after a rolled-back commit, want false — the suppression write must not have survived independently of email_events")
	}
	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusActive {
		t.Errorf("subscriber status after rolled-back commit = %q, want unchanged %q", sub.Status, subscribers.StatusActive)
	}

	// Retry: SNS resends the identical signed envelope. Because
	// email_events truly rolled back, (sns_message_id, recipient) is free
	// again — this is NOT deduped away as a pure redelivery, it is
	// processed in full — and this time Begin returns a normal, unwrapped
	// Tx, so COMMIT succeeds for real.
	second := sesPost(h, m)
	if second.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 (body=%s)", second.Code, second.Body.String())
	}

	rows, err = sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID after retry: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("email_events rows after retry = %d, want exactly 1", len(rows))
	}
	suppressed, err = subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed after retry: %v", err)
	}
	if !suppressed {
		t.Error("IsSuppressed = false after retry, want true — the retry must land the suppression this time")
	}
	sub, err = subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID after retry: %v", err)
	}
	if sub.Status != subscribers.StatusBounced {
		t.Errorf("subscriber status after retry = %q, want %q", sub.Status, subscribers.StatusBounced)
	}
}

func TestSESNotifications_Redelivery_ExactlyOneRowAndOneSuppression(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-redeliver-1"},
		"bounce": map[string]any{
			"bounceType":        "Permanent",
			"bouncedRecipients": []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	first := sesPost(h, m)
	if first.Code != http.StatusOK {
		t.Fatalf("first delivery status = %d, want 200", first.Code)
	}
	second := sesPost(h, m) // identical signed envelope, posted again
	if second.Code != http.StatusOK {
		t.Fatalf("redelivery status = %d, want 200", second.Code)
	}

	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("email_events rows after redelivery = %d, want exactly 1", len(rows))
	}

	list, err := subscribers.NewSuppressionStore(pool).ListByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("suppression rows after redelivery = %d, want exactly 1", len(list))
	}
}

// TestSESNotifications_SecondReasonForAlreadySuppressedAddressCoexists is
// the property #0038's "Carried in from #0100's plan" section makes a hard
// requirement: AddTx must run UNCONDITIONALLY per the §4 mapping, never
// skipped because the address is "already suppressed" — that skip is
// exactly the write-time data loss #0100 exists to fix (the suppressions
// key widened to (email, reason), so a second, different reason must
// coexist as a second row). Pre-seeds a `complaint` suppression, then sends
// a Permanent bounce for the same address and asserts BOTH rows exist
// afterward.
func TestSESNotifications_SecondReasonForAlreadySuppressedAddressCoexists(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	// Pre-existing suppression for a DIFFERENT reason, added directly
	// through the store (as an admin's manual suppress, or an earlier
	// complaint, would have) — this must survive AND be joined by a second
	// row, not be treated as "nothing to do here".
	if _, err := subscribers.NewSuppressionStore(pool).Add(context.Background(), subscribers.NewSuppression{
		Email: email, Reason: subscribers.SuppressionReasonComplaint, Note: "pre-existing",
	}, time.Now()); err != nil {
		t.Fatalf("seed pre-existing complaint suppression: %v", err)
	}

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-second-reason-1"},
		"bounce": map[string]any{
			"bounceType":        "Permanent",
			"bouncedRecipients": []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}

	list, err := subscribers.NewSuppressionStore(pool).ListByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	reasons := map[string]bool{}
	for _, s := range list {
		reasons[s.Reason] = true
	}
	if !reasons[subscribers.SuppressionReasonComplaint] {
		t.Errorf("suppressions for %s = %+v, want the pre-existing complaint row to survive", email, list)
	}
	if !reasons[subscribers.SuppressionReasonHardBounce] {
		t.Errorf("suppressions for %s = %+v, want a NEW hard_bounce row to coexist with it — AddTx must run unconditionally (#0100)", email, list)
	}
	if len(list) != 2 {
		t.Errorf("suppression row count for %s = %d, want 2", email, len(list))
	}
}

func TestSESNotifications_MultiRecipientBounce_BothSuppressedAndBounced(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	sub1 := seedTestSubscriber(t, pool, subscribers.StatusActive)
	sub2 := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email1 := subscriberEmailByID(t, pool, sub1)
	email2 := subscriberEmailByID(t, pool, sub2)
	sesCleanupSuppression(t, pool, email1)
	sesCleanupSuppression(t, pool, email2)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-multi-1"},
		"bounce": map[string]any{
			"bounceType": "Permanent",
			"bouncedRecipients": []map[string]string{
				{"emailAddress": email1}, {"emailAddress": email2},
			},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}

	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("email_events rows = %d, want 2 (one per bounced recipient)", len(rows))
	}

	subsStore := subscribers.NewStore(pool)
	supprStore := subscribers.NewSuppressionStore(pool)
	for _, id := range []int64{sub1, sub2} {
		sub, err := subsStore.GetByID(context.Background(), id)
		if err != nil {
			t.Fatalf("GetByID(%d): %v", id, err)
		}
		if sub.Status != subscribers.StatusBounced {
			t.Errorf("subscriber %d status = %q, want %q", id, sub.Status, subscribers.StatusBounced)
		}
	}
	for _, email := range []string{email1, email2} {
		suppressed, err := supprStore.IsSuppressed(context.Background(), email)
		if err != nil {
			t.Fatalf("IsSuppressed(%s): %v", email, err)
		}
		if !suppressed {
			t.Errorf("IsSuppressed(%s) = false, want true", email)
		}
	}
}

func TestSESNotifications_NoSubscriberRow_StillRecordsAndSuppresses(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	email := fmt.Sprintf("zz-0038-nosub-%d@example.com", testdb.Unique())
	sesCleanupSuppression(t, pool, email)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-nosub-1"},
		"bounce": map[string]any{
			"bounceType":        "Permanent",
			"bouncedRecipients": []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}

	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("email_events rows = %d, want 1", len(rows))
	}
	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Error("IsSuppressed = false, want true — criterion 6: an event with no matching subscriber still suppresses")
	}
}

func TestSESNotifications_StaleEventGuard_PredatesConfirmedAt(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	// seedTestSubscriber sets confirmed_at = now() for an active row.
	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	staleTimestamp := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": staleTimestamp, "messageId": "ses-stale-1"},
		"bounce": map[string]any{
			"bounceType":        "Permanent",
			"bouncedRecipients": []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	m.Timestamp = staleTimestamp
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}

	// The event row is STILL written — the evidence is never discarded,
	// only the state change is suppressed (#0038 §3).
	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("email_events rows = %d, want 1", len(rows))
	}

	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true, want false — a bounce older than confirmed_at must not suppress")
	}
	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusActive {
		t.Errorf("subscriber status = %q, want unchanged %q", sub.Status, subscribers.StatusActive)
	}
}

func TestSESNotifications_ComplainedIsNotErasedByLaterBounce(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusComplained)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-complained-bounce-1"},
		"bounce": map[string]any{
			"bounceType":        "Permanent",
			"bouncedRecipients": []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}

	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusComplained {
		t.Errorf("subscriber status = %q, want unchanged %q (CLAUDE.md §9)", sub.Status, subscribers.StatusComplained)
	}

	// Both the event row AND the hard_bounce suppression must still be
	// written — carried in from #0025's review: MarkBouncedTx's no-op on an
	// already-complained row is success, not a reason to skip either write.
	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("email_events rows = %d, want 1", len(rows))
	}
	list, err := subscribers.NewSuppressionStore(pool).ListByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	var sawHardBounce bool
	for _, s := range list {
		if s.Reason == subscribers.SuppressionReasonHardBounce {
			sawHardBounce = true
		}
	}
	if !sawHardBounce {
		t.Errorf("suppressions for %s = %+v, want a hard_bounce row even though status stayed complained", email, list)
	}
}

func TestSESNotifications_RepeatComplaint_DoesNotBumpUpdatedAt(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusComplained)
	email := subscriberEmailByID(t, pool, subID)
	sesCleanupSuppression(t, pool, email)

	before, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID (before): %v", err)
	}

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Complaint",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-repeat-complaint-1"},
		"complaint": map[string]any{
			"complaintFeedbackType": "abuse",
			"complainedRecipients":  []map[string]string{{"emailAddress": email}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}

	after, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID (after): %v", err)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("UpdatedAt changed from %v to %v — a repeat complaint on an already-complained row must not bump it", before.UpdatedAt, after.UpdatedAt)
	}
}

func TestSESNotifications_CaseWhitespaceNormalization(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	storedEmail := subscriberEmailByID(t, pool, subID) // stored lower(trim(...))
	sesCleanupSuppression(t, pool, storedEmail)

	sesReportedEmail := " " + strings.ToUpper(storedEmail) + " "

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-case-1"},
		"bounce": map[string]any{
			"bounceType":        "Permanent",
			"bouncedRecipients": []map[string]string{{"emailAddress": sesReportedEmail}},
		},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}

	suppressed, err := subscribers.NewSuppressionStore(pool).IsSuppressed(context.Background(), storedEmail)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Error("IsSuppressed(stored form) = false, want true — differently-cased/whitespaced SES address must still match")
	}
	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusBounced {
		t.Errorf("subscriber status = %q, want %q", sub.Status, subscribers.StatusBounced)
	}
}

func TestSESNotifications_BadSignature_Returns403ZeroRows(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-badsig-1"},
		"bounce":    map[string]any{"bounceType": "Permanent", "bouncedRecipients": []map[string]string{{"emailAddress": "zz-0038-badsig@example.com"}}},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)
	m.Message = inner + " " // tamper AFTER signing
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("email_events rows = %d, want 0 — a message that fails verification must never be written", len(rows))
	}
}

func TestSESNotifications_TopicArnMismatch_Returns403ZeroRows(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-badtopic-1"},
		"bounce":    map[string]any{"bounceType": "Permanent", "bouncedRecipients": []map[string]string{{"emailAddress": "zz-0038-badtopic@example.com"}}},
	})
	m := sesBaseNotification(t, inner)
	m.TopicArn = "arn:aws:sns:us-west-2:999999999999:someone-elses-topic" // wrong topic, distinct from the bad-signature fixture (#0107)
	sesSignMessage(t, key, m)
	sesCleanupEvents(t, pool, m.MessageId)

	resp := sesPost(h, m)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
	rows, err := sesnotify.NewStore(pool).ByMessageID(context.Background(), m.MessageId)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("email_events rows = %d, want 0", len(rows))
	}
}

func TestSESNotifications_CertUnavailable_Returns500(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	key, _ := sesTestFixture(t)

	verifier := sesnotify.NewVerifierForTesting("us-west-2", sesTestTopicArn, nil, func(ctx context.Context, certURL string) (*x509.Certificate, error) {
		return nil, fmt.Errorf("simulated network failure")
	})
	h := NewSESNotificationsHandler(
		verifier, pool, sesnotify.NewStore(pool), subscribers.NewStore(pool), subscribers.NewSuppressionStore(pool),
		auth.NewStore(pool), nil, nil,
	)

	inner := sesEncodeSESEvent(t, map[string]any{
		"eventType": "Bounce",
		"mail":      map[string]any{"timestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "messageId": "ses-certfail-1"},
		"bounce":    map[string]any{"bounceType": "Permanent", "bouncedRecipients": []map[string]string{{"emailAddress": "zz-0038-certfail@example.com"}}},
	})
	m := sesBaseNotification(t, inner)
	sesSignMessage(t, key, m)

	resp := sesPost(h, m)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — the certificate could not be FETCHED, which is retryable, not forgery", resp.Code)
	}
}

// fakeSESVerifier is used ONLY for the two SubscriptionConfirmation dispatch
// tests below, which are about this handler's OWN logic (call
// FetchSubscribeURL, write the right audit row, answer 200 either way) —
// not about sesnotify's trust boundary, which the real Verifier (via
// sesTestHandler / sesnotify.NewVerifierForTesting) already covers
// elsewhere in this file. Using a fake here keeps these two tests fast and
// deterministic: sesnotify.Verifier.FetchSubscribeURL's own host allowlist
// (validateCertURL) accepts only sns.<region>.amazonaws.com, so a "real
// successful fetch" test would require an actual outbound HTTPS call to
// AWS — see internal/sesnotify/verify_test.go's
// TestVerifier_FetchSubscribeURL_HostileURLRejectedBeforeFetch and
// TestFetchSubscribeURLReal_* for that boundary's own coverage.
type fakeSESVerifier struct {
	verifyErr         error
	fetchSubscribeErr error
	fetchCalled       bool
}

func (f *fakeSESVerifier) Verify(ctx context.Context, m *sesnotify.Message) error { return f.verifyErr }

func (f *fakeSESVerifier) FetchSubscribeURL(ctx context.Context, subscribeURL string) error {
	f.fetchCalled = true
	return f.fetchSubscribeErr
}

func TestSESNotifications_SubscriptionConfirmation_AutoConfirmsAndAudits(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	verifier := &fakeSESVerifier{}
	h := NewSESNotificationsHandler(
		verifier, pool, sesnotify.NewStore(pool), subscribers.NewStore(pool), subscribers.NewSuppressionStore(pool),
		auth.NewStore(pool), audit.New(pool), nil,
	)

	m := &sesnotify.Message{
		Type:         sesnotify.TypeSubscriptionConfirmation,
		MessageId:    sesUniqueID(t, "subconf"),
		Token:        "zz-token",
		TopicArn:     sesTestTopicArn,
		Message:      "You have chosen to subscribe to the topic.",
		SubscribeURL: "https://sns.us-west-2.amazonaws.com/?Action=ConfirmSubscription&Token=zz-token",
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	}

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	if !verifier.fetchCalled {
		t.Error("FetchSubscribeURL was not called — auto-confirm must fetch SubscribeURL exactly once")
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND metadata->>'message_id' = $2`,
		audit.ActionSESSubscriptionConfirmed, m.MessageId,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 1 {
		t.Errorf("audit rows for %s = %d, want 1", audit.ActionSESSubscriptionConfirmed, count)
	}
}

func TestSESNotifications_SubscriptionConfirmation_FetchFailureStillReturns200NoAudit(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	verifier := &fakeSESVerifier{fetchSubscribeErr: fmt.Errorf("simulated fetch failure")}
	h := NewSESNotificationsHandler(
		verifier, pool, sesnotify.NewStore(pool), subscribers.NewStore(pool), subscribers.NewSuppressionStore(pool),
		auth.NewStore(pool), audit.New(pool), nil,
	)

	m := &sesnotify.Message{
		Type:         sesnotify.TypeSubscriptionConfirmation,
		MessageId:    sesUniqueID(t, "subconf-fail"),
		Token:        "zz-token",
		TopicArn:     sesTestTopicArn,
		Message:      "You have chosen to subscribe to the topic.",
		SubscribeURL: "https://sns.us-west-2.amazonaws.com/?Action=ConfirmSubscription&Token=zz-token",
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	}

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a SubscribeURL fetch failure is operational, not forgery, and SNS has nothing different to retry into)", resp.Code)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND metadata->>'message_id' = $2`,
		audit.ActionSESSubscriptionConfirmed, m.MessageId,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 0 {
		t.Errorf("audit rows for %s = %d, want 0 — a failed fetch must not be recorded as a successful auto-confirm", audit.ActionSESSubscriptionConfirmed, count)
	}
}

func TestSESNotifications_SubscriptionConfirmation_HostileSubscribeURLNeverFetched(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	key, _ := sesTestFixture(t)
	_, cert := sesTestFixture(t)

	verifier := sesnotify.NewVerifierForTesting("us-west-2", sesTestTopicArn, nil, func(ctx context.Context, certURL string) (*x509.Certificate, error) {
		return cert, nil
	})
	h := NewSESNotificationsHandler(
		verifier, pool, sesnotify.NewStore(pool), subscribers.NewStore(pool), subscribers.NewSuppressionStore(pool),
		auth.NewStore(pool), nil, nil,
	)

	m := &sesnotify.Message{
		Type:           sesnotify.TypeSubscriptionConfirmation,
		MessageId:      sesUniqueID(t, "subconf-hostile"),
		Token:          "zz-token",
		TopicArn:       sesTestTopicArn,
		Message:        "You have chosen to subscribe to the topic.",
		SubscribeURL:   "https://evil.example.com/steal-me",
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		SigningCertURL: "https://sns.us-west-2.amazonaws.com/SimpleNotificationService-abcdef.pem",
	}
	sesSignMessage(t, key, m)

	resp := sesPost(h, m)
	// Verify passed (real signature, correct topic); the SubscribeURL host
	// check then fails, which is logged and answered 200 — never fetched.
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (host-validation failure on SubscribeURL is not retried)", resp.Code)
	}
}

func TestSESNotifications_UnsubscribeConfirmation_NeverActsAndReturns200(t *testing.T) {
	pool := sesNotificationsTestPool(t)
	h := sesTestHandler(t, pool)
	key, _ := sesTestFixture(t)

	m := &sesnotify.Message{
		Type:           sesnotify.TypeUnsubscribeConfirmation,
		MessageId:      sesUniqueID(t, "unsubconf"),
		TopicArn:       sesTestTopicArn,
		Message:        "You have chosen to unsubscribe.",
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		SigningCertURL: "https://sns.us-west-2.amazonaws.com/SimpleNotificationService-abcdef.pem",
	}
	sesSignMessage(t, key, m)

	resp := sesPost(h, m)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	_ = pool
}
