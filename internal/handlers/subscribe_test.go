package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// subscribeTestPool connects to TEST_DATABASE_URL or skips, then truncates
// the subscribers/subscriber_interests tables so each test starts from a
// clean slate. Mirrors internal/subscribers' own testPool.
func subscribeTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}

	truncateSubscribeTables(t, pool)
	t.Cleanup(func() {
		truncateSubscribeTables(t, pool)
		pool.Close()
	})
	return pool
}

// truncateSubscribeTables wipes subscribers/subscriber_interests (RESTART
// IDENTITY, so ids are small and predictable within a test) and audit_log.
// audit_log has to be included: subscribers' ids restart at 1 every time
// this runs, so without also clearing audit_log, a later test's
// auditSignupCount query for target_id=1 would pick up rows left behind by
// an EARLIER test's own id-1 subscriber, matching internal/handlers'
// existing credsTestPool convention (truncateCredsTables also clears
// audit_log for the same id-reuse reason).
func truncateSubscribeTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`TRUNCATE subscriber_interests, subscribers, audit_log RESTART IDENTITY CASCADE`,
	); err != nil {
		t.Fatalf("truncate subscribe tables: %v", err)
	}
}

func subscribeUniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-subtest-%d@example.com", time.Now().UnixNano())
}

// fakeSuppressionChecker is a test double for SuppressionChecker: reports
// suppressed for any address in the set, or a configured error.
type fakeSuppressionChecker struct {
	suppressed map[string]bool
	err        error
}

func (f fakeSuppressionChecker) IsSuppressed(_ context.Context, email string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.suppressed[email], nil
}

// fakePhysicalAddress is a test double for physicalAddressReader.
type fakePhysicalAddress struct{ value string }

func (f fakePhysicalAddress) GetSetting(context.Context, string) (string, error) {
	return f.value, nil
}

// subscribeMux wires the real *SubscribeHandler over the real
// *subscribers.Store and *interests.Store (both #0025/#0023, approved),
// with an injected mailer and suppression checker so tests never touch the
// network and can control every branch precisely. Returns the handler
// itself too, so a test can override h.now for deterministic timestamps
// (e.g. the resend-cooldown test).
func subscribeMux(pool *pgxpool.Pool, mailer mailing.Mailer, suppression SuppressionChecker) (*SubscribeHandler, http.Handler) {
	h := NewSubscribeHandler(
		subscribers.NewStore(pool), interests.NewStore(pool), mailer,
		suppression, fakePhysicalAddress{value: "123 Main St, San Francisco, CA"},
		audit.New(pool), "https://example.test", nil,
	)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/subscribe", h.Subscribe)
	return h, mux
}

// subscribeBody builds a well-formed request body: honeypot empty, timing
// gate satisfied (rendered 5s before now), overridable via the returned map.
func subscribeBody(email string, slugs []string, now time.Time) map[string]any {
	return map[string]any{
		"email":       email,
		"interests":   slugs,
		"website":     "",
		"rendered_at": now.Add(-5 * time.Second).UnixMilli(),
	}
}

func doSubscribe(t *testing.T, mux http.Handler, body map[string]any) *http.Response {
	t.Helper()
	return doSubscribeFrom(t, mux, body, "", "")
}

// doSubscribeFrom is doSubscribe with control over RemoteAddr and
// X-Forwarded-For, so #0077 regression tests can simulate a request arriving
// via the trusted Apache hop (loopback RemoteAddr, matching
// deploy/systemd/opencircuit.service) versus a direct/spoofed one.
func doSubscribeFrom(t *testing.T, mux http.Handler, body map[string]any, remoteAddr, xff string) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Result()
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b := make([]byte, 0, 256)
	buf := make([]byte, 256)
	for {
		n, err := resp.Body.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			break
		}
	}
	return b
}

// seedSubscriberRow inserts a subscribers row with full control over status
// and confirm_token/confirm_sent_at, bypassing the store's own API so tests
// can construct states (active, bounced, complained, ...) the public API
// alone cannot reach directly. Returns the row id.
func seedSubscriberRow(t *testing.T, pool *pgxpool.Pool, email, status string, confirmToken *string, confirmSentAt *time.Time) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO subscribers (email, status, confirm_token, confirm_sent_at, confirm_expires_at, manage_token)
		 VALUES ($1, $2, $3, $4, now() + interval '7 days', $5)
		 RETURNING id`,
		email, status, confirmToken, confirmSentAt, "mtok-"+email,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed subscriber %s (%s): %v", email, status, err)
	}
	return id
}

func subscriberStatus(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM subscribers WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read status for %d: %v", id, err)
	}
	return status
}

func subscriberRow(t *testing.T, pool *pgxpool.Pool, email string) (id int64, status string, confirmToken *string, confirmSentAt *time.Time) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT id, status, confirm_token, confirm_sent_at FROM subscribers WHERE email = $1`, email,
	).Scan(&id, &status, &confirmToken, &confirmSentAt)
	if err != nil {
		t.Fatalf("read subscriber row for %s: %v", email, err)
	}
	return
}

// subscriberSignupIP reads back the stored signup_ip (host portion only,
// stripping the /32 or /128 CIDR suffix pgx's inet mapping adds) for the
// PRD §6.3 consent-evidence assertions.
func subscriberSignupIP(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var ip *string
	if err := pool.QueryRow(context.Background(),
		`SELECT host(signup_ip) FROM subscribers WHERE email = $1`, email).Scan(&ip); err != nil {
		t.Fatalf("read signup_ip for %s: %v", email, err)
	}
	if ip == nil {
		return ""
	}
	return *ip
}

func subscriberExists(t *testing.T, pool *pgxpool.Pool, email string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscribers WHERE email = $1`, email).Scan(&n); err != nil {
		t.Fatalf("count subscriber %s: %v", email, err)
	}
	return n > 0
}

func auditSignupCount(t *testing.T, pool *pgxpool.Pool, subscriberID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_type = $2 AND target_id = $3`,
		audit.ActionSubscriberSignup, audit.TargetSubscriber, subscriberID,
	).Scan(&n); err != nil {
		t.Fatalf("count audit rows for subscriber %d: %v", subscriberID, err)
	}
	return n
}

// mustSeededInterestID resolves a seeded taxonomy slug to its id (see
// migrations/000009), failing the test if it's somehow missing.
func mustSeededInterestID(t *testing.T, pool *pgxpool.Pool, slug string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM interests WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("resolve seeded interest %q: %v", slug, err)
	}
	return id
}

// ============================================================================
// The uniform 202 — the crown jewel of #0026.
// ============================================================================

// TestSubscribe_UniformResponseAcrossBranches drives every branch PRD §6.3
// enumerates (new signup, active, pending, unsubscribed, bounced,
// complained, suppressed) plus the two anti-bot short-circuits (honeypot,
// timing gate) and asserts every single response is BYTE-IDENTICAL — not
// merely the same shape or the same status code. This is the endpoint's
// core security property: CLAUDE.md §9 requires it, and varying so much as
// a header would turn the endpoint into an email-enumeration oracle.
func TestSubscribe_UniformResponseAcrossBranches(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	now := time.Now()

	pendingEmail := subscribeUniqueEmail(t) + "-pending"
	pendingToken := "tok-" + pendingEmail
	seedSubscriberRow(t, pool, pendingEmail, subscribers.StatusPending, &pendingToken, nil)
	unsubEmail := subscribeUniqueEmail(t) + "-unsub"
	seedSubscriberRow(t, pool, unsubEmail, subscribers.StatusUnsubscribed, nil, nil)
	bouncedEmail := subscribeUniqueEmail(t) + "-bounced"
	seedSubscriberRow(t, pool, bouncedEmail, subscribers.StatusBounced, nil, nil)
	complainedEmail := subscribeUniqueEmail(t) + "-complained"
	seedSubscriberRow(t, pool, complainedEmail, subscribers.StatusComplained, nil, nil)

	suppressedEmail := subscribeUniqueEmail(t) + "-suppressed"
	suppression := fakeSuppressionChecker{suppressed: map[string]bool{suppressedEmail: true}}

	_, mux := subscribeMux(pool, mailer, suppression)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"new-signup", subscribeBody(subscribeUniqueEmail(t)+"-new", nil, now)},
		{"active", subscribeBody(activeEmailFor(t, pool), nil, now)},
		{"pending", subscribeBody(pendingEmail, nil, now)},
		{"unsubscribed", subscribeBody(unsubEmail, nil, now)},
		{"bounced", subscribeBody(bouncedEmail, nil, now)},
		{"complained", subscribeBody(complainedEmail, nil, now)},
		{"suppressed", subscribeBody(suppressedEmail, nil, now)},
		{"honeypot", withHoneypot(subscribeBody(subscribeUniqueEmail(t)+"-bot", nil, now))},
		{"timing-gate", withTooFast(subscribeBody(subscribeUniqueEmail(t)+"-fast", nil, now), now)},
	}

	var firstBody []byte
	var firstContentType string
	for _, c := range cases {
		resp := doSubscribe(t, mux, c.body)
		body := readBody(t, resp)

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: status = %d, want %d; body=%s", c.name, resp.StatusCode, http.StatusAccepted, body)
		}
		if firstBody == nil {
			firstBody = body
			firstContentType = resp.Header.Get("Content-Type")
			continue
		}
		if !bytes.Equal(body, firstBody) {
			t.Errorf("%s: body = %q, want byte-identical to the first branch's %q", c.name, body, firstBody)
		}
		if ct := resp.Header.Get("Content-Type"); ct != firstContentType {
			t.Errorf("%s: Content-Type = %q, want %q", c.name, ct, firstContentType)
		}
	}
}

// activeEmailFor seeds a fresh active subscriber and returns its email — a
// small helper so TestSubscribe_UniformResponseAcrossBranches's table can
// stay declarative.
func activeEmailFor(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	email := subscribeUniqueEmail(t) + "-active2"
	seedSubscriberRow(t, pool, email, subscribers.StatusActive, nil, nil)
	return email
}

func withHoneypot(body map[string]any) map[string]any {
	body["website"] = "filled-by-a-bot"
	return body
}

func withTooFast(body map[string]any, now time.Time) map[string]any {
	body["rendered_at"] = now.UnixMilli() // 0 elapsed — fails the 2s gate
	return body
}

// ============================================================================
// Honeypot
// ============================================================================

func TestSubscribe_HoneypotDiscardsSilently(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	email := subscribeUniqueEmail(t)
	resp := doSubscribe(t, mux, withHoneypot(subscribeBody(email, []string{}, time.Now())))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if subscriberExists(t, pool, email) {
		t.Error("a subscriber row was created for a honeypot-triggering request")
	}
	if len(mailer.Sent()) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0", len(mailer.Sent()))
	}
}

// ============================================================================
// Timing gate
// ============================================================================

func TestSubscribe_TimingGateRejectsTooFastOrMissing(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	t.Run("too-fast", func(t *testing.T) {
		email := subscribeUniqueEmail(t) + "-fast"
		resp := doSubscribe(t, mux, withTooFast(subscribeBody(email, []string{}, time.Now()), time.Now()))
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", resp.StatusCode)
		}
		if subscriberExists(t, pool, email) {
			t.Error("a subscriber row was created for a too-fast submission")
		}
	})

	t.Run("missing", func(t *testing.T) {
		email := subscribeUniqueEmail(t) + "-missing"
		body := subscribeBody(email, []string{}, time.Now())
		delete(body, "rendered_at")
		resp := doSubscribe(t, mux, body)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", resp.StatusCode)
		}
		if subscriberExists(t, pool, email) {
			t.Error("a subscriber row was created despite a missing rendered_at")
		}
	})

	if len(mailer.Sent()) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0", len(mailer.Sent()))
	}
}

func TestPassesTimingGate(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name       string
		renderedAt int64
		want       bool
	}{
		{"well in the past", now.Add(-10 * time.Second).UnixMilli(), true},
		{"exactly at the boundary", now.Add(-subscribeTimingGateMinDwell).UnixMilli(), true},
		{"just under the boundary", now.Add(-subscribeTimingGateMinDwell + 100*time.Millisecond).UnixMilli(), false},
		{"zero", 0, false},
		{"negative", -1, false},
		{"in the future", now.Add(time.Minute).UnixMilli(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := passesTimingGate(c.renderedAt, now); got != c.want {
				t.Errorf("passesTimingGate(%d, now) = %v, want %v", c.renderedAt, got, c.want)
			}
		})
	}
}

// ============================================================================
// Suppression
// ============================================================================

func TestSubscribe_SuppressedAddress_NoActionTaken(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	email := subscribeUniqueEmail(t)
	suppression := fakeSuppressionChecker{suppressed: map[string]bool{email: true}}
	_, mux := subscribeMux(pool, mailer, suppression)

	resp := doSubscribe(t, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if subscriberExists(t, pool, email) {
		t.Error("a subscriber row was created for a suppressed address")
	}
	if len(mailer.Sent()) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0", len(mailer.Sent()))
	}
}

func TestSubscribe_SuppressionCheckError_FailsSafeToUniform202(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	email := subscribeUniqueEmail(t)
	suppression := fakeSuppressionChecker{err: errors.New("suppression backend unavailable")}
	_, mux := subscribeMux(pool, mailer, suppression)

	resp := doSubscribe(t, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 even when the suppression check itself errors", resp.StatusCode)
	}
	if subscriberExists(t, pool, email) {
		t.Error("a subscriber row was created despite the suppression check failing")
	}
	if len(mailer.Sent()) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0", len(mailer.Sent()))
	}
}

// ============================================================================
// New signup
// ============================================================================

func TestSubscribe_NewSignup_CreatesPendingSendsConfirmationAndAudits(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	email := subscribeUniqueEmail(t)
	resp := doSubscribe(t, mux, subscribeBody(email, []string{"beginner", "microcontrollers"}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	id, status, confirmToken, confirmSentAt := subscriberRow(t, pool, email)
	if status != subscribers.StatusPending {
		t.Errorf("status = %q, want %q", status, subscribers.StatusPending)
	}
	if confirmToken == nil || *confirmToken == "" {
		t.Error("confirm_token is nil/empty")
	}
	if confirmSentAt == nil {
		t.Error("confirm_sent_at is nil, want stamped after a successful send")
	}

	store := subscribers.NewStore(pool)
	ids, err := store.InterestIDs(context.Background(), id)
	if err != nil {
		t.Fatalf("InterestIDs: %v", err)
	}
	want := []int64{mustSeededInterestID(t, pool, "beginner"), mustSeededInterestID(t, pool, "microcontrollers")}
	if !int64SetEqual(ids, want) {
		t.Errorf("InterestIDs = %v, want %v", ids, want)
	}

	sent := mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("mailer.Sent() = %d messages, want 1", len(sent))
	}
	if sent[0].To != email {
		t.Errorf("To = %q, want %q", sent[0].To, email)
	}
	if !strings.Contains(sent[0].Subject, "Confirm") {
		t.Errorf("Subject = %q, want it to mention confirming", sent[0].Subject)
	}
	if confirmToken != nil && !strings.Contains(sent[0].HTMLBody, *confirmToken) {
		t.Error("confirmation email body does not contain the confirm token")
	}

	if n := auditSignupCount(t, pool, id); n != 1 {
		t.Errorf("subscriber.signup audit rows for %d = %d, want 1", id, n)
	}
}

// ============================================================================
// #0077 — signup_ip is consent evidence (PRD §6.3) and must not be
// attacker-chosen.
// ============================================================================

// TestSubscribe_SignupIPUsesRightmostTrustedProxyEntry confirms the real
// end-to-end path: a request arriving via the trusted Apache hop (loopback
// RemoteAddr, matching deploy/systemd/opencircuit.service) with a
// mod_proxy_http-style X-Forwarded-For — attacker-claimed leftmost entry,
// Apache-appended rightmost entry — stores the RIGHTMOST (real) entry as
// signup_ip, not the client-supplied leftmost one.
func TestSubscribe_SignupIPUsesRightmostTrustedProxyEntry(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	email := subscribeUniqueEmail(t)
	const attackerClaimed = "172.16.0.7"
	const realPeer = "198.51.100.9"
	resp := doSubscribeFrom(t, mux, subscribeBody(email, []string{}, time.Now()),
		"127.0.0.1:9999", attackerClaimed+", "+realPeer)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	if got := subscriberSignupIP(t, pool, email); got != realPeer {
		t.Errorf("signup_ip = %q, want %q (the Apache-appended real peer, not the attacker-claimed %q)", got, realPeer, attackerClaimed)
	}
}

// TestSubscribe_SignupIPCannotBeSpoofedWithoutTrustedProxy is #0077's
// consent-evidence proof: with no trusted proxy in front (RemoteAddr is not
// the loopback hop — a direct connection), a spoofed X-Forwarded-For must
// not determine signup_ip. The real RemoteAddr must be recorded instead,
// exactly as PRD §6.3 and #0075's published privacy policy promise
// subscribers.
func TestSubscribe_SignupIPCannotBeSpoofedWithoutTrustedProxy(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	email := subscribeUniqueEmail(t)
	const spoofed = "172.16.0.7"
	const realPeer = "198.51.100.9"
	resp := doSubscribeFrom(t, mux, subscribeBody(email, []string{}, time.Now()),
		realPeer+":4242", spoofed)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	if got := subscriberSignupIP(t, pool, email); got != realPeer {
		t.Errorf("signup_ip = %q, want %q (the real RemoteAddr) — an attacker-chosen X-Forwarded-For value (%q) must never become consent evidence with no trusted proxy in front", got, realPeer, spoofed)
	}
}

func TestSubscribe_EmptyInterestsAccepted(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	email := subscribeUniqueEmail(t)
	resp := doSubscribe(t, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	id, status, _, _ := subscriberRow(t, pool, email)
	if status != subscribers.StatusPending {
		t.Fatalf("status = %q, want %q", status, subscribers.StatusPending)
	}
	store := subscribers.NewStore(pool)
	ids, err := store.InterestIDs(context.Background(), id)
	if err != nil {
		t.Fatalf("InterestIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("InterestIDs = %v, want empty — zero interests must be accepted, not rejected", ids)
	}
}

func TestSubscribe_UnknownInterestSlugRejected(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	email := subscribeUniqueEmail(t)
	resp := doSubscribe(t, mux, subscribeBody(email, []string{"not-a-real-slug"}, time.Now()))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if subscriberExists(t, pool, email) {
		t.Error("a subscriber row was created despite an unknown interest slug")
	}
	if len(mailer.Sent()) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0", len(mailer.Sent()))
	}
}

func int64SetEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[int64]bool, len(a))
	for _, v := range a {
		set[v] = true
	}
	for _, v := range b {
		if !set[v] {
			return false
		}
	}
	return true
}

// ============================================================================
// Email syntax / disposable domain
// ============================================================================

func TestValidEmailSyntax(t *testing.T) {
	cases := []struct {
		email string
		want  bool
	}{
		{"person@example.com", true},
		{"person+tag@example.co.uk", true},
		{"", false},
		{"not-an-email", false},
		{"missing-at.example.com", false},
		{"two@@example.com", false},
		{"person@no-tld", false},
		{"Display Name <person@example.com>", false}, // decorated form rejected
		{"café@example.com", false},                  // non-ASCII local part
		{"person@exämple.com", false},                // non-ASCII domain
	}
	for _, c := range cases {
		t.Run(c.email, func(t *testing.T) {
			if got := validEmailSyntax(c.email); got != c.want {
				t.Errorf("validEmailSyntax(%q) = %v, want %v", c.email, got, c.want)
			}
		})
	}
}

func TestIsDisposableDomain(t *testing.T) {
	if !isDisposableDomain("someone@mailinator.com") {
		t.Error("mailinator.com should be flagged disposable")
	}
	if !isDisposableDomain("someone@MAILINATOR.COM") {
		t.Error("domain match should be case-insensitive")
	}
	if isDisposableDomain("someone@example.com") {
		t.Error("example.com should not be flagged disposable")
	}
}

func TestSubscribe_InvalidEmailSyntaxRejected(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	resp := doSubscribe(t, mux, subscribeBody("not-an-email", nil, time.Now()))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSubscribe_NonASCIIEmailRejected(t *testing.T) {
	// #0026's carried-in criterion: normalizeEmail's Go strings.ToLower and
	// the DB CHECK's Postgres lower() can disagree on a non-ASCII local
	// part, which — if this were allowed through — would surface as a 500
	// from an endpoint that must always answer 202. Closed off in syntax
	// validation instead, so it never reaches the store at all.
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	email := "café@example.com"
	_, mux := subscribeMux(pool, mailer, nil)

	resp := doSubscribe(t, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-ASCII local part", resp.StatusCode)
	}
	if subscriberExists(t, pool, email) {
		t.Error("a subscriber row was created for a non-ASCII address")
	}
}

func TestSubscribe_DisposableDomainRejected(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	email := "zz-subtest@mailinator.com"
	resp := doSubscribe(t, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if subscriberExists(t, pool, email) {
		t.Error("a subscriber row was created for a disposable-domain address")
	}
}

// ============================================================================
// Existing subscriber branches
// ============================================================================

func TestSubscribe_ActiveSubscriber_SendsAlreadySubscribedEmail(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	email := subscribeUniqueEmail(t)
	id := seedSubscriberRow(t, pool, email, subscribers.StatusActive, nil, nil)

	resp := doSubscribe(t, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	sent := mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("mailer.Sent() = %d messages, want 1", len(sent))
	}
	if !strings.Contains(sent[0].Subject, "already subscribed") {
		t.Errorf("Subject = %q, want it to mention already being subscribed", sent[0].Subject)
	}

	if status := subscriberStatus(t, pool, id); status != subscribers.StatusActive {
		t.Errorf("status = %q after re-submitting an active address, want unchanged %q", status, subscribers.StatusActive)
	}
}

func TestSubscribe_PendingSubscriber_ResendRateLimitedToOncePerHour(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	h, mux := subscribeMux(pool, mailer, nil)

	fixedNow := time.Now().UTC().Truncate(time.Second)
	h.now = func() time.Time { return fixedNow }

	email := subscribeUniqueEmail(t)
	token := "tok-" + email
	recentlySent := fixedNow.Add(-30 * time.Minute)
	id := seedSubscriberRow(t, pool, email, subscribers.StatusPending, &token, &recentlySent)

	// Within the cooldown: no email sent, confirm_sent_at untouched.
	resp := doSubscribe(t, mux, subscribeBody(email, nil, fixedNow))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if len(mailer.Sent()) != 0 {
		t.Fatalf("mailer.Sent() = %d messages within the cooldown window, want 0", len(mailer.Sent()))
	}
	_, _, _, confirmSentAt := subscriberRow(t, pool, email)
	if confirmSentAt == nil || !confirmSentAt.Equal(recentlySent) {
		t.Errorf("confirm_sent_at = %v, want unchanged %v", confirmSentAt, recentlySent)
	}

	// Push confirm_sent_at outside the cooldown and try again: this time it
	// resends and re-stamps confirm_sent_at to now.
	longAgo := fixedNow.Add(-90 * time.Minute)
	if _, err := pool.Exec(context.Background(),
		`UPDATE subscribers SET confirm_sent_at = $2 WHERE id = $1`, id, longAgo,
	); err != nil {
		t.Fatalf("resetting confirm_sent_at: %v", err)
	}

	resp2 := doSubscribe(t, mux, subscribeBody(email, nil, fixedNow))
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp2.StatusCode)
	}
	if len(mailer.Sent()) != 1 {
		t.Fatalf("mailer.Sent() = %d messages outside the cooldown window, want 1", len(mailer.Sent()))
	}
	_, _, _, confirmSentAt2 := subscriberRow(t, pool, email)
	if confirmSentAt2 == nil || !confirmSentAt2.Equal(fixedNow) {
		t.Errorf("confirm_sent_at = %v, want %v", confirmSentAt2, fixedNow)
	}
}

func TestSubscribe_UnsubscribedSubscriber_RestartsWithFreshTokenAndSendsConfirmation(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	email := subscribeUniqueEmail(t)
	id := seedSubscriberRow(t, pool, email, subscribers.StatusUnsubscribed, nil, nil)

	resp := doSubscribe(t, mux, subscribeBody(email, []string{"homelab"}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	gotID, status, confirmToken, confirmSentAt := subscriberRow(t, pool, email)
	if gotID != id {
		t.Fatalf("RestartSignup created a NEW row (id %d) instead of reusing %d", gotID, id)
	}
	if status != subscribers.StatusPending {
		t.Errorf("status = %q, want %q", status, subscribers.StatusPending)
	}
	if confirmToken == nil || *confirmToken == "" {
		t.Error("confirm_token is nil/empty after restart")
	}
	if confirmSentAt == nil {
		t.Error("confirm_sent_at is nil, want stamped after a successful send")
	}

	store := subscribers.NewStore(pool)
	ids, err := store.InterestIDs(context.Background(), id)
	if err != nil {
		t.Fatalf("InterestIDs: %v", err)
	}
	if want := []int64{mustSeededInterestID(t, pool, "homelab")}; !int64SetEqual(ids, want) {
		t.Errorf("InterestIDs = %v, want %v", ids, want)
	}

	if len(mailer.Sent()) != 1 {
		t.Fatalf("mailer.Sent() = %d messages, want 1", len(mailer.Sent()))
	}
	if n := auditSignupCount(t, pool, id); n != 1 {
		t.Errorf("subscriber.signup audit rows for %d = %d, want 1", id, n)
	}
}

func TestSubscribe_BouncedSubscriber_RestartsSameAsUnsubscribed(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	email := subscribeUniqueEmail(t)
	seedSubscriberRow(t, pool, email, subscribers.StatusBounced, nil, nil)

	resp := doSubscribe(t, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	_, status, confirmToken, _ := subscriberRow(t, pool, email)
	if status != subscribers.StatusPending {
		t.Errorf("status = %q, want %q (bounced routes through the same restart path as unsubscribed)", status, subscribers.StatusPending)
	}
	if confirmToken == nil {
		t.Error("confirm_token is nil after restart")
	}
	if len(mailer.Sent()) != 1 {
		t.Fatalf("mailer.Sent() = %d messages, want 1", len(mailer.Sent()))
	}
}

// TestSubscribe_ComplainedSubscriber_NoActionTaken is the end-to-end proof
// (not just at the store method) that a complained subscriber can never be
// laundered back toward active through this endpoint: no store mutation, no
// email, and the response is indistinguishable from every other branch. See
// internal/subscribers' TestRestartSignup_ComplainedStaysComplained for the
// data-layer guard this backstops.
func TestSubscribe_ComplainedSubscriber_NoActionTaken(t *testing.T) {
	pool := subscribeTestPool(t)
	mailer := &mailing.RecordingMailer{}
	_, mux := subscribeMux(pool, mailer, nil)

	email := subscribeUniqueEmail(t)
	id := seedSubscriberRow(t, pool, email, subscribers.StatusComplained, nil, nil)

	resp := doSubscribe(t, mux, subscribeBody(email, []string{"beginner"}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	body := readBody(t, resp)
	want, _ := json.Marshal(subscribeUniformBody)
	if !bytes.Equal(bytes.TrimSpace(body), bytes.TrimSpace(want)) {
		t.Errorf("body = %s, want %s (must be indistinguishable from every other branch)", body, want)
	}

	gotID, status, confirmToken, confirmSentAt := subscriberRow(t, pool, email)
	if gotID != id {
		t.Fatalf("unexpected id %d, want %d", gotID, id)
	}
	if status != subscribers.StatusComplained {
		t.Fatalf("status = %q, want unchanged %q — a complained subscriber must never move toward active", status, subscribers.StatusComplained)
	}
	if confirmToken != nil {
		t.Errorf("confirm_token = %v, want unchanged nil — no fresh token may ever be minted for a complained subscriber", confirmToken)
	}
	if confirmSentAt != nil {
		t.Errorf("confirm_sent_at = %v, want unchanged nil", confirmSentAt)
	}
	if len(mailer.Sent()) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0 — nothing is ever sent to a complained address", len(mailer.Sent()))
	}
	if n := auditSignupCount(t, pool, id); n != 0 {
		t.Errorf("subscriber.signup audit rows for %d = %d, want 0", id, n)
	}

	store := subscribers.NewStore(pool)
	ids, err := store.InterestIDs(context.Background(), id)
	if err != nil {
		t.Fatalf("InterestIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("InterestIDs = %v, want empty — the submitted interests must never be applied", ids)
	}
}
