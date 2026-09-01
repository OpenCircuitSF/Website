package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// subscribeTestPool returns the package's single shared pool (opened once in
// TestMain — #0091) or skips if TEST_DATABASE_URL was unset.
//
// #0091 round two: this used to TRUNCATE subscribers/subscriber_interests/
// audit_log on every call. It no longer does — every test in this file
// already seeds through subscribeUniqueEmail(t), and every audit_log
// lookback (auditSignupCount) filters by the specific subscriber id a test
// itself created rather than assuming ids restart at 1, so no two tests'
// rows collide. The tables only need to start clean once — main_test.go's
// TestMain — not before every test. Mirrors internal/subscribers' own
// testPool.
func subscribeTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

func subscribeUniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-subtest-%d@example.com", testdb.Unique())
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

// fakePhysicalAddress is a test double for the GetSetting-shaped physical
// address reader other handlers in this package depend on (e.g.
// admin_campaign_preview_test.go). #0126 removed SubscribeHandler's own use
// of this shape (physical_address is now resolved by
// internal/mailing.OutboxWorker at send time, not by this handler), but the
// type stays here as shared package-level test infra for the handlers that
// still need it.
type fakePhysicalAddress struct{ value string }

func (f fakePhysicalAddress) GetSetting(context.Context, string) (string, error) {
	return f.value, nil
}

// blockingSubscriberStore wraps a real subscriberStore, delegating every
// method to inner except the ONE named by which, which blocks until either
// release is closed (by the test) or ctx is cancelled — whichever happens
// first — and closes entered the instant that method is called, before it
// starts waiting. #0081 originally used an equivalent blockingMailer
// wrapping the mailer itself, to prove the analogous property for
// mailer.Send; #0126 removed the mailer from this handler entirely (sending
// moved to internal/mailing.OutboxWorker over internal/outbox), so
// blockingSubscriberStore is now the only such double in this file — it
// proves the same "response is not blocked by async work" shape, and also
// the shutdown-interrupts-a-blocked-mutation shape #0081's Close test used
// to prove via the mailer. entered is closed at most once per instance —
// construct a fresh blockingSubscriberStore per expected call to the
// blocked method.
//
// which selects exactly one of "Create", "RestartSignup", or
// "ClaimAndEnqueueAlreadySubscribed" — #0098's regression coverage for the
// deferral guard's original gap: the guard's own
// TestSubscribe_MutationDeferredDoesNotBlockResponse only ever proved out
// Create (the brand-new-signup branch), so a change that left the
// EXISTING-subscriber branches (RestartSignup,
// ClaimAndEnqueueAlreadySubscribed) on the request path — exactly the shape
// #0088's second review demonstrated leaves the whole package green — went
// undetected. Every other method still delegates straight to inner
// unconditionally, same as before #0098.
type blockingSubscriberStore struct {
	inner   subscriberStore
	entered chan struct{}
	release chan struct{}
	which   string
}

func newBlockingSubscriberStore(inner subscriberStore) *blockingSubscriberStore {
	return newBlockingSubscriberStoreOn(inner, "Create")
}

// newBlockingSubscriberStoreOn is newBlockingSubscriberStore generalized to
// block a caller-chosen method — see the type's own doc comment for why.
func newBlockingSubscriberStoreOn(inner subscriberStore, which string) *blockingSubscriberStore {
	return &blockingSubscriberStore{inner: inner, entered: make(chan struct{}), release: make(chan struct{}), which: which}
}

// awaitReleaseOrCancel is the shared block/unblock mechanics every blocked
// method uses: close entered (once), then wait for either release (the test
// letting it through) or ctx cancellation (Close's shutdown path).
func (b *blockingSubscriberStore) awaitReleaseOrCancel(ctx context.Context) error {
	close(b.entered)
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *blockingSubscriberStore) Create(ctx context.Context, in subscribers.NewSignup, now time.Time) (subscribers.Subscriber, error) {
	if b.which == "Create" {
		if err := b.awaitReleaseOrCancel(ctx); err != nil {
			return subscribers.Subscriber{}, err
		}
	}
	return b.inner.Create(ctx, in, now)
}

func (b *blockingSubscriberStore) FindByEmail(ctx context.Context, email string) (subscribers.Subscriber, error) {
	return b.inner.FindByEmail(ctx, email)
}

func (b *blockingSubscriberStore) RestartSignup(ctx context.Context, id int64, in subscribers.RestartSignupInput, now time.Time) (subscribers.Subscriber, error) {
	if b.which == "RestartSignup" {
		if err := b.awaitReleaseOrCancel(ctx); err != nil {
			return subscribers.Subscriber{}, err
		}
	}
	return b.inner.RestartSignup(ctx, id, in, now)
}

func (b *blockingSubscriberStore) ClaimAndEnqueueConfirmation(ctx context.Context, sub subscribers.Subscriber, now time.Time, cooldown, ttl time.Duration, evidence subscribers.RestartSignupInput) (bool, error) {
	if b.which == "ClaimAndEnqueueConfirmation" {
		if err := b.awaitReleaseOrCancel(ctx); err != nil {
			return false, err
		}
	}
	return b.inner.ClaimAndEnqueueConfirmation(ctx, sub, now, cooldown, ttl, evidence)
}

func (b *blockingSubscriberStore) ClaimAndEnqueueAlreadySubscribed(ctx context.Context, sub subscribers.Subscriber, now time.Time, cooldown time.Duration) (bool, error) {
	if b.which == "ClaimAndEnqueueAlreadySubscribed" {
		if err := b.awaitReleaseOrCancel(ctx); err != nil {
			return false, err
		}
	}
	return b.inner.ClaimAndEnqueueAlreadySubscribed(ctx, sub, now, cooldown)
}

func (b *blockingSubscriberStore) SetInterests(ctx context.Context, subscriberID int64, interestIDs []int64) error {
	return b.inner.SetInterests(ctx, subscriberID, interestIDs)
}

// subscribeMux wires the real *SubscribeHandler over the real
// *subscribers.Store and *interests.Store (both #0025/#0023, approved),
// with an injected suppression checker so tests can control that branch
// precisely. Returns the handler itself too, so a test can override h.now
// for deterministic timestamps (e.g. the resend-cooldown test).
//
// #0126 removed the mailer parameter entirely — sending now happens via
// internal/mailing.OutboxWorker draining internal/outbox, a separate
// process this package's tests don't run; assertions that used to check
// mailer.Sent() now check outbound_queue rows directly (see
// outboundQueueRowsFor).
//
// Registers t.Cleanup(...) to call h.Close, bounded, so this package's ~20
// call sites stop leaking mutateWorkerCount goroutines per construction
// (#0081) — before Close existed there was no way to stop them at all, and
// they accumulated for the life of the test binary.
func subscribeMux(t *testing.T, pool *pgxpool.Pool, suppression SuppressionChecker) (*SubscribeHandler, http.Handler) {
	t.Helper()
	h := NewSubscribeHandler(
		subscribers.NewStore(pool), interests.NewStore(pool),
		suppression, audit.New(pool), "https://example.test", nil,
		outbox.NewStore(pool),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.Close(ctx); err != nil {
			t.Errorf("subscribeMux cleanup: h.Close: %v", err)
		}
	})
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

// doSubscribe issues the request AND waits for h's async send workers to
// drain (h.waitForSends) before returning, so a test can assert on mailer
// state or subscriber rows immediately afterward without an arbitrary
// sleep. This is deterministic-but-real: #0026's review moved mailer.Send
// off the request/response path (finding 1), so the response itself no
// longer indicates whether a queued send has finished — waitForSends is
// what makes these tests trustworthy under that change.
func doSubscribe(t *testing.T, h *SubscribeHandler, mux http.Handler, body map[string]any) *http.Response {
	t.Helper()
	return doSubscribeFrom(t, h, mux, body, "", "")
}

// doSubscribeFrom is doSubscribe with control over RemoteAddr and
// X-Forwarded-For, so #0077 regression tests can simulate a request arriving
// via the trusted Apache hop (loopback RemoteAddr, matching
// deploy/systemd/opencircuit.service) versus a direct/spoofed one.
func doSubscribeFrom(t *testing.T, h *SubscribeHandler, mux http.Handler, body map[string]any, remoteAddr, xff string) *http.Response {
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
	resp := rec.Result()
	if h != nil {
		h.waitForSends()
	}
	return resp
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
//
// #0121: registers its own t.Cleanup deleting the row by the id it just
// captured (CLAUDE.md §8b — never a literal id), so every call site gets
// isolation for free instead of each of them having to remember to. Two
// call sites (admin_campaign_preflight_test.go) already registered their
// own identical cleanup before this existed; deleting an already-deleted id
// is a no-op, so that redundancy is harmless and left as-is.
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
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, id) })
	return id
}

// seedExpiredPendingSubscriberRow inserts a status='pending' row shaped
// exactly like ExpirePendingSweep's own output (#0128): confirm_token NULL,
// confirm_expires_at in the past. This is #0314's whole reproduction case —
// before that issue's fix, sendConfirmation refused to act on such a row at
// all, so this state was a dead end with no self-service recovery.
func seedExpiredPendingSubscriberRow(t *testing.T, pool *pgxpool.Pool, email string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO subscribers (email, status, confirm_token, confirm_sent_at, confirm_expires_at, manage_token)
		 VALUES ($1, 'pending', NULL, NULL, now() - interval '1 hour', $2)
		 RETURNING id`,
		email, "mtok-"+email,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed expired-pending subscriber %s: %v", email, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, id) })
	return id
}

// confirmationPayloadToken decodes the confirm_token field out of a raw
// outbound_queue payload::text — the confirmation/import_invite payload
// shapes both carry this field under the same JSON name (see
// internal/subscribers' confirmationPayload / importInvitePayload).
func confirmationPayloadToken(t *testing.T, payloadJSON string) string {
	t.Helper()
	var p struct {
		ConfirmToken string `json:"confirm_token"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
		t.Fatalf("decoding confirm_token from payload %s: %v", payloadJSON, err)
	}
	if p.ConfirmToken == "" {
		t.Fatalf("payload %s carries no confirm_token", payloadJSON)
	}
	return p.ConfirmToken
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

// outboundQueueTestRow is one outbound_queue row, as read back by
// outboundQueueRowsFor — #0126's replacement for this file's pre-#0126
// mailer.Sent() assertions. Rendering (subject/body content) now happens in
// internal/mailing.OutboxWorker, not this handler, so this file asserts
// only what the handler itself is responsible for: that the right kind of
// row was enqueued, for the right recipient/subscriber, with a payload that
// carries the right token — never rendered content, which belongs to
// internal/mailing's own tests (transactional_templates_test.go).
type outboundQueueTestRow struct {
	Kind         string
	Recipient    string
	Status       string
	SubscriberID *int64
	Payload      string // raw JSONB text
}

// outboundQueueRowsFor reads every MAIL outbound_queue row for recipient,
// oldest first.
//
// #0254 excludes outbox.KindSubscribeIntake explicitly: Subscribe now
// writes exactly one such row, synchronously, for every accepted request
// regardless of branch (the durability backstop subscribe_intake.go
// documents) — it is not mail, and every caller of this helper (and of
// outboundQueueCountFor) predates #0254 and means "what mail got queued for
// this recipient", not "every outbound_queue row of any kind". Without this
// filter, every one of this file's "exactly N messages" assertions would be
// off by exactly one intake row per accepted request.
func outboundQueueRowsFor(t *testing.T, pool *pgxpool.Pool, recipient string) []outboundQueueTestRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT kind, recipient, status, subscriber_id, payload::text
		   FROM outbound_queue WHERE recipient = $1 AND kind <> $2 ORDER BY id`,
		recipient, string(outbox.KindSubscribeIntake),
	)
	if err != nil {
		t.Fatalf("querying outbound_queue for %q: %v", recipient, err)
	}
	defer rows.Close()
	var out []outboundQueueTestRow
	for rows.Next() {
		var r outboundQueueTestRow
		if err := rows.Scan(&r.Kind, &r.Recipient, &r.Status, &r.SubscriberID, &r.Payload); err != nil {
			t.Fatalf("scanning outbound_queue row for %q: %v", recipient, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating outbound_queue rows for %q: %v", recipient, err)
	}
	return out
}

// outboundQueueCountFor is len(outboundQueueRowsFor(...)) — the direct
// replacement for outboundQueueCountFor(t, pool, email) at every call site that only cared
// about the count.
func outboundQueueCountFor(t *testing.T, pool *pgxpool.Pool, recipient string) int {
	t.Helper()
	return len(outboundQueueRowsFor(t, pool, recipient))
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

	// #0314: an expired-pending row (ExpirePendingSweep's own output —
	// confirm_token NULL, confirm_expires_at in the past) must answer the
	// same uniform 202 as every other branch, even though internally it now
	// establishes a fresh live token where before this issue it silently
	// sent nothing at all.
	expiredPendingEmail := subscribeUniqueEmail(t) + "-expired-pending"
	seedExpiredPendingSubscriberRow(t, pool, expiredPendingEmail)

	// #0313: a still-pending import invitation, submitted through the
	// public form by its own address, must be equally invisible from
	// outside — the conversion (import_id cleared, evidence refreshed,
	// queued invitation cancelled, generic confirmation sent) is entirely
	// internal to the mutate worker, which runs after the response is
	// already written (see this method's own "How this resolves under
	// #0026's uniform 202" reasoning in issues/0313.md).
	pendingInvitedEmail := subscribeUniqueEmail(t) + "-pending-invited"
	commitPublicTakeoverInvite(t, pool, pendingInvitedEmail, "Intro to Soldering (uniform-202 branch)")

	h, mux := subscribeMux(t, pool, suppression)

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
		// #0046's review (phase 3, second bounce, finding 2): the reserved
		// test-email-domain no-op processMutateJob added lives on the same
		// async branch every other case here exercises, but this table had
		// no entry naming it -- a genuine per-branch response oracle keyed
		// on subscribers.IsReservedTestEmail slipped past every existing
		// case. Proven by mutation: setting w.Header().Set("X-Reserved", "1")
		// immediately before writeSubscribeUniform202 in Subscribe left this
		// test green until this case was added.
		{"reserved-test-domain", subscribeBody(fmt.Sprintf("zz-subtest-reserved-%d@%s", testdb.Unique(), subscribers.ReservedTestEmailDomain), nil, now)},
		{"expired-pending", subscribeBody(expiredPendingEmail, nil, now)},
		{"pending-invited", subscribeBody(pendingInvitedEmail, nil, now)},
	}

	var firstBody []byte
	var firstHeaders http.Header
	for _, c := range cases {
		resp := doSubscribe(t, h, mux, c.body)
		body := readBody(t, resp)

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: status = %d, want %d; body=%s", c.name, resp.StatusCode, http.StatusAccepted, body)
		}
		headers := headersIgnoringDate(resp.Header)
		if firstBody == nil {
			firstBody = body
			firstHeaders = headers
			continue
		}
		if !bytes.Equal(body, firstBody) {
			t.Errorf("%s: body = %q, want byte-identical to the first branch's %q", c.name, body, firstBody)
		}
		// Full header set (minus Date), not just Content-Type: #0026's
		// review found TestSubscribe_UniformResponseAcrossBranches's
		// previous Content-Type-only comparison let a mutation adding
		// w.Header().Set("X-Suppressed", "1") before the shared write
		// pass — a genuine per-branch oracle the test claimed to rule
		// out. See this file's mutation-proof notes.
		if !headersEqual(headers, firstHeaders) {
			t.Errorf("%s: headers = %v, want byte-identical (minus Date) to the first branch's %v", c.name, headers, firstHeaders)
		}
	}
}

// headersIgnoringDate returns a copy of h with any Date header removed —
// the one header this test permits to vary, since #0026's endpoint doesn't
// set it itself but a real net/http.Server would stamp it per-response.
func headersIgnoringDate(h http.Header) http.Header {
	clone := h.Clone()
	clone.Del("Date")
	return clone
}

// headersEqual reports whether a and b have exactly the same set of header
// names, each with exactly the same (ordered) slice of values.
func headersEqual(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for name, av := range a {
		bv, ok := b[name]
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if av[i] != bv[i] {
				return false
			}
		}
	}
	return true
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
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	resp := doSubscribe(t, h, mux, withHoneypot(subscribeBody(email, []string{}, time.Now())))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if subscriberExists(t, pool, email) {
		t.Error("a subscriber row was created for a honeypot-triggering request")
	}
	if outboundQueueCountFor(t, pool, email) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0", outboundQueueCountFor(t, pool, email))
	}
}

// ============================================================================
// Timing gate
// ============================================================================

func TestSubscribe_TimingGateRejectsTooFastOrMissing(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	t.Run("too-fast", func(t *testing.T) {
		email := subscribeUniqueEmail(t) + "-fast"
		resp := doSubscribe(t, h, mux, withTooFast(subscribeBody(email, []string{}, time.Now()), time.Now()))
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", resp.StatusCode)
		}
		if subscriberExists(t, pool, email) {
			t.Error("a subscriber row was created for a too-fast submission")
		}
		if outboundQueueCountFor(t, pool, email) != 0 {
			t.Errorf("outbound_queue rows for %q = %d, want 0", email, outboundQueueCountFor(t, pool, email))
		}
	})

	t.Run("missing", func(t *testing.T) {
		email := subscribeUniqueEmail(t) + "-missing"
		body := subscribeBody(email, []string{}, time.Now())
		delete(body, "rendered_at")
		resp := doSubscribe(t, h, mux, body)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", resp.StatusCode)
		}
		if subscriberExists(t, pool, email) {
			t.Error("a subscriber row was created despite a missing rendered_at")
		}
		if outboundQueueCountFor(t, pool, email) != 0 {
			t.Errorf("outbound_queue rows for %q = %d, want 0", email, outboundQueueCountFor(t, pool, email))
		}
	})
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
	email := subscribeUniqueEmail(t)
	suppression := fakeSuppressionChecker{suppressed: map[string]bool{email: true}}
	h, mux := subscribeMux(t, pool, suppression)

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if subscriberExists(t, pool, email) {
		t.Error("a subscriber row was created for a suppressed address")
	}
	if outboundQueueCountFor(t, pool, email) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0", outboundQueueCountFor(t, pool, email))
	}
}

func TestSubscribe_SuppressionCheckError_FailsSafeToUniform202(t *testing.T) {
	pool := subscribeTestPool(t)
	email := subscribeUniqueEmail(t)
	suppression := fakeSuppressionChecker{err: errors.New("suppression backend unavailable")}
	h, mux := subscribeMux(t, pool, suppression)

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 even when the suppression check itself errors", resp.StatusCode)
	}
	if subscriberExists(t, pool, email) {
		t.Error("a subscriber row was created despite the suppression check failing")
	}
	if outboundQueueCountFor(t, pool, email) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0", outboundQueueCountFor(t, pool, email))
	}
}

// TestSubscribe_ReservedTestDomain_NoActionTaken is #0046's review finding B,
// closed: the public endpoint must never attempt to mail an address at
// subscribers.ReservedTestEmailDomain, since that domain is RFC
// 2606-reserved (guaranteed to never resolve — any real send would be a
// hard bounce) and #0046's admin test-send recipient format
// (campaign-test+admin-<id>@...) makes such an address trivially
// enumerable via small admin ids. Covers both the brand-new-address branch
// (no row exists yet) and the existing-row branch (an admin already ran a
// test send, so a synthetic row is sitting there) — the guard must fire
// either way, since the enumeration risk lives in the domain, not in
// whether a row happens to exist.
func TestSubscribe_ReservedTestDomain_NoActionTaken(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := fmt.Sprintf("campaign-test+admin-%d@%s", testdb.Unique(), subscribers.ReservedTestEmailDomain)

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (must not vary from any other branch)", resp.StatusCode)
	}
	if subscriberExists(t, pool, email) {
		t.Error("a subscriber row was created for an address at the reserved test domain")
	}
	if outboundQueueCountFor(t, pool, email) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0 — a send to this domain is a guaranteed hard bounce", outboundQueueCountFor(t, pool, email))
	}

	// Now the existing-row branch: seed a synthetic row at the same address
	// first (mirroring what ensureTestRecipient does), then submit again.
	store := subscribers.NewStore(pool)
	sub, err := store.Create(context.Background(), subscribers.NewSignup{Email: email, ConfirmTTL: time.Hour, Synthetic: true}, time.Now())
	if err != nil {
		t.Fatalf("seed synthetic row: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, sub.ID) })

	resp2 := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp2.StatusCode)
	}
	if outboundQueueCountFor(t, pool, email) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0 even once a synthetic row already exists at this address", outboundQueueCountFor(t, pool, email))
	}
}

// ============================================================================
// New signup
// ============================================================================

func TestSubscribe_NewSignup_CreatesPendingSendsConfirmationAndAudits(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{"beginner", "microcontrollers"}, time.Now()))
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

	// #0126: content assertions (subject/body) moved to
	// internal/mailing's own template tests — this handler no longer
	// renders anything, it only enqueues template inputs. What belongs
	// here is that exactly one confirmation row was enqueued, for the
	// right recipient/subscriber, carrying the actual confirm token as
	// payload.
	rows := outboundQueueRowsFor(t, pool, email)
	if len(rows) != 1 {
		t.Fatalf("outbound_queue rows for %q = %d, want 1", email, len(rows))
	}
	if rows[0].Kind != "confirmation" {
		t.Errorf("kind = %q, want %q", rows[0].Kind, "confirmation")
	}
	if rows[0].SubscriberID == nil || *rows[0].SubscriberID != id {
		t.Errorf("subscriber_id = %v, want %d", rows[0].SubscriberID, id)
	}
	if confirmToken != nil && !strings.Contains(rows[0].Payload, *confirmToken) {
		t.Errorf("payload %q does not contain the confirm token", rows[0].Payload)
	}

	if n := auditSignupCount(t, pool, id); n != 1 {
		t.Errorf("subscriber.signup audit rows for %d = %d, want 1", id, n)
	}
}

// ============================================================================
// #0314 — an expired-pending signup can sign up again and actually receive
// a working confirm link.
// ============================================================================

// TestSubscribe_ExpiredPending_SignsUpAgainAndConfirms is #0314's core
// reproduction: before this issue's fix, a pending row whose confirm_token
// ExpirePendingSweep had nulled could never sign up again — sendConfirmation
// refused outright, the endpoint still answered its uniform 202, and no
// email ever arrived. This drives the whole cycle through the REAL handler
// and proves the fix by an end-to-end oracle — the token in the NEWLY
// enqueued mail actually confirms the row — not a copy of the SQL that
// establishes it.
func TestSubscribe_ExpiredPending_SignsUpAgainAndConfirms(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	id := seedExpiredPendingSubscriberRow(t, pool, email)

	// Sanity-check the starting state matches ExpirePendingSweep's own
	// output exactly, so a later failure can't be misread as this test
	// having seeded something else by mistake.
	if _, status, confirmToken, _ := subscriberRow(t, pool, email); status != subscribers.StatusPending || confirmToken != nil {
		t.Fatalf("seed state = (status=%q, confirm_token=%v), want (pending, nil)", status, confirmToken)
	}

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	rows := outboundQueueRowsFor(t, pool, email)
	if len(rows) != 1 {
		t.Fatalf("outbound_queue rows for %q = %d, want 1 (a fresh confirmation must now be enqueued)", email, len(rows))
	}
	if rows[0].Kind != "confirmation" {
		t.Errorf("kind = %q, want %q", rows[0].Kind, "confirmation")
	}
	token := confirmationPayloadToken(t, rows[0].Payload)

	store := subscribers.NewStore(pool)
	confirmed, err := store.Confirm(context.Background(), token, time.Now())
	if err != nil {
		t.Fatalf("Confirm with the newly-enqueued token: %v", err)
	}
	if confirmed.ID != id {
		t.Fatalf("Confirm resolved subscriber %d, want %d", confirmed.ID, id)
	}
	if confirmed.Status != subscribers.StatusActive {
		t.Errorf("Status after Confirm = %q, want %q", confirmed.Status, subscribers.StatusActive)
	}
}

// TestSubscribe_ExpiredPending_SuppressedStaysSilent is #0314 criterion 4:
// the expiry-recovery path is not a route around suppression.
// dispatchMutation's `if suppressed { return }` runs before existingSignup
// (and therefore before sendConfirmation) regardless of which branch would
// otherwise fire, so this is the SAME property #0026 already established,
// re-asserted specifically against the new expired-pending state.
func TestSubscribe_ExpiredPending_SuppressedStaysSilent(t *testing.T) {
	pool := subscribeTestPool(t)
	email := subscribeUniqueEmail(t)
	seedExpiredPendingSubscriberRow(t, pool, email)
	suppression := fakeSuppressionChecker{suppressed: map[string]bool{email: true}}
	h, mux := subscribeMux(t, pool, suppression)

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if n := outboundQueueCountFor(t, pool, email); n != 0 {
		t.Errorf("outbound_queue rows for a suppressed expired-pending address = %d, want 0", n)
	}
	if _, status, confirmToken, _ := subscriberRow(t, pool, email); status != subscribers.StatusPending || confirmToken != nil {
		t.Errorf("row state after a suppressed submit = (status=%q, confirm_token=%v), want unchanged (pending, nil)", status, confirmToken)
	}
}

// ============================================================================
// #0313 — a still-pending import invitation becomes a self-initiated
// signup when its own address submits the public form.
// ============================================================================

// commitPublicTakeoverInvite commits a single invite-mode import through
// the real ImportStore (never a copy of its SQL) and returns the resulting
// pending, import-linked subscriber id and the owning import's id.
// sourceDetail is distinctive per test so a payload/body assertion can tell
// THIS test's provenance apart from another test's leftover row in the
// shared outbound_queue.
func commitPublicTakeoverInvite(t *testing.T, pool *pgxpool.Pool, email, sourceDetail string) (subscriberID, importID int64) {
	t.Helper()
	importStore := subscribers.NewImportStore(pool)
	result, err := importStore.Commit(context.Background(), subscribers.CommitInput{
		Source:       subscribers.ImportSourceLuma,
		SourceDetail: sourceDetail,
		ConsentMode:  subscribers.ConsentModeInvite,
		ConsentNote:  "collected via Luma RSVP export, attested by the organizer",
		CollectedAt:  time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		Filename:     "attendees.csv",
		Rows:         []subscribers.ImportRow{{Email: email}},
	}, time.Now())
	if err != nil {
		t.Fatalf("commitPublicTakeoverInvite: Commit: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriber_imports WHERE id = $1`, result.Import.ID)
	})
	sub, err := subscribers.NewStore(pool).FindByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("commitPublicTakeoverInvite: FindByEmail: %v", err)
	}
	return sub.ID, result.Import.ID
}

// TestSubscribe_PendingInvitedRow_BecomesSelfInitiatedSignup is #0313's
// core property, asserted through four independent mechanisms per that
// issue's own test strategy — none of them a restatement of the UPDATE
// under test:
//
// Mutation M1 (#0313's plan): delete the `import_id = NULL` clause. Must
// fail on the suppression count and the confirmed_count delta below.
func TestSubscribe_PendingInvitedRow_BecomesSelfInitiatedSignup(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	subID, importID := commitPublicTakeoverInvite(t, pool, email, "Intro to Soldering (public takeover test)")

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	subStore := subscribers.NewStore(pool)
	converted, err := subStore.GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	// 1. ImportID is nil.
	if converted.ImportID != nil {
		t.Errorf("ImportID after public takeover = %v, want nil", converted.ImportID)
	}
	if converted.Status != subscribers.StatusPending {
		t.Fatalf("Status after public takeover = %q, want %q", converted.Status, subscribers.StatusPending)
	}

	rows := outboundQueueRowsFor(t, pool, email)
	var confirmationRow *outboundQueueTestRow
	for i := range rows {
		if rows[i].Kind == "confirmation" {
			confirmationRow = &rows[i]
		}
	}
	if confirmationRow == nil {
		t.Fatalf("no confirmation row enqueued for %q; rows=%v", email, rows)
	}
	token := confirmationPayloadToken(t, confirmationRow.Payload)

	// 2. A subsequent ordinary Unsubscribe creates ZERO suppressions.
	if _, err := subStore.Unsubscribe(context.Background(), subID, "one_click", time.Now()); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	var suppressionCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM suppressions WHERE email = $1`, email,
	).Scan(&suppressionCount); err != nil {
		t.Fatalf("counting suppressions: %v", err)
	}
	if suppressionCount != 0 {
		t.Errorf("suppressions for %q after an ordinary post-takeover unsubscribe = %d, want 0 (must not read as an invitation decline)", email, suppressionCount)
	}

	// Restart the signup and re-confirm through the SAME token this
	// method already minted, so criteria 3-4 below observe an active row
	// rather than an unsubscribed one — Unsubscribe above only exists to
	// prove criterion 2 and must not block the rest of this test.
	restarted, err := subStore.RestartSignup(context.Background(), subID, subscribers.RestartSignupInput{ConfirmTTL: time.Hour}, time.Now())
	if err != nil {
		t.Fatalf("RestartSignup: %v", err)
	}
	if restarted.ConfirmToken == nil {
		t.Fatal("RestartSignup left ConfirmToken nil")
	}
	token = *restarted.ConfirmToken

	importBefore, err := subscribers.NewImportStore(pool).GetImport(context.Background(), importID)
	if err != nil {
		t.Fatalf("GetImport before confirm: %v", err)
	}

	confirmed, err := subStore.Confirm(context.Background(), token, time.Now())
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if confirmed.Status != subscribers.StatusActive {
		t.Fatalf("Status after Confirm = %q, want %q", confirmed.Status, subscribers.StatusActive)
	}

	// 3. Confirm does NOT increment the ORIGINATING import's confirmed_count
	// — the row's consent no longer derives from that batch.
	importAfter, err := subscribers.NewImportStore(pool).GetImport(context.Background(), importID)
	if err != nil {
		t.Fatalf("GetImport after confirm: %v", err)
	}
	if importAfter.ConfirmedCount != importBefore.ConfirmedCount {
		t.Errorf("import %d ConfirmedCount changed from %d to %d after confirming a converted row — want unchanged", importID, importBefore.ConfirmedCount, importAfter.ConfirmedCount)
	}

	// 4. The confirming subscriber DOES receive a welcome — denied to an
	// accepted invitation ("the invitation was the introduction"), correct
	// here because the invitation is no longer this row's introduction.
	var welcomeCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'welcome'`, subID,
	).Scan(&welcomeCount); err != nil {
		t.Fatalf("counting welcome rows: %v", err)
	}
	if welcomeCount != 1 {
		t.Errorf("welcome rows for the converted-then-confirmed subscriber = %d, want 1", welcomeCount)
	}
}

// TestSubscribe_PendingInvitedRow_SendsGenericConfirmation is #0313
// criterion 2's direct half: the public path enqueues the GENERIC
// confirmation for a pending invited row, never a second invitation.
//
// Mutation M2 (#0313's plan): enqueue outbox.KindImportInvite on this path
// instead. Must fail — both on the kind check and on the payload shape
// check (only a KindImportInvite payload ever carries source_detail).
func TestSubscribe_PendingInvitedRow_SendsGenericConfirmation(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	commitPublicTakeoverInvite(t, pool, email, "Intro to Soldering (generic-confirmation test)")

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	rows := outboundQueueRowsFor(t, pool, email)
	var confirmationRows, inviteRows int
	var confirmationPayload string
	var inviteStatus string
	for _, r := range rows {
		switch r.Kind {
		case "confirmation":
			confirmationRows++
			confirmationPayload = r.Payload
		case "import_invite":
			inviteRows++
			inviteStatus = r.Status
		}
	}
	if confirmationRows != 1 {
		t.Fatalf("confirmation rows enqueued = %d, want 1", confirmationRows)
	}
	// Exactly ONE import_invite row total — the ORIGINAL, from
	// ImportStore.Commit — never a second. This path must never enqueue a
	// new invitation; the mutation this rules out is enqueueing
	// KindImportInvite here instead of/alongside KindConfirmation, which
	// would show up as either a second import_invite row or as
	// confirmationRows == 0.
	if inviteRows != 1 {
		t.Errorf("import_invite rows for %q = %d, want exactly 1 (the original only, never a second)", email, inviteRows)
	}
	if inviteStatus != "abandoned" {
		t.Errorf("the original invitation's status = %q, want %q (cancelled by the conversion, #0313 step 2)", inviteStatus, "abandoned")
	}
	if strings.Contains(confirmationPayload, "source_detail") {
		t.Errorf("the enqueued confirmation payload %s carries source_detail — that field only ever appears on an import_invite payload", confirmationPayload)
	}
}

// TestSubscribe_PendingInvitedRow_CancelsQueuedInvitation is #0313's
// queued-invitation cleanup: the ORIGINAL invitation ImportStore.Commit
// enqueued must be cancelled (abandoned), not left to sit in the queue and
// eventually mail a self-initiated signer the "we got your address from an
// import" sentence about an address they typed in themselves.
//
// Mutation M3 (#0313's plan): remove the CancelQueuedTx call. Must fail.
func TestSubscribe_PendingInvitedRow_CancelsQueuedInvitation(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	commitPublicTakeoverInvite(t, pool, email, "Intro to Soldering (cancel-queued-invite test)")

	// The original invitation is 'queued' right after Commit, regardless of
	// physical_address — OutboxWorker never runs in this handler-level
	// test, so this assertion is about outbound_queue state directly, not
	// about whether a real send was attempted.
	var beforeStatus string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM outbound_queue WHERE recipient = $1 AND kind = 'import_invite'`, email,
	).Scan(&beforeStatus); err != nil {
		t.Fatalf("reading the original invitation row: %v", err)
	}
	if beforeStatus != "queued" {
		t.Fatalf("original invitation status before the public takeover = %q, want %q", beforeStatus, "queued")
	}

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	var afterStatus string
	var afterError *string
	if err := pool.QueryRow(context.Background(),
		`SELECT status, error FROM outbound_queue WHERE recipient = $1 AND kind = 'import_invite'`, email,
	).Scan(&afterStatus, &afterError); err != nil {
		t.Fatalf("reading the original invitation row after takeover: %v", err)
	}
	if afterStatus != "abandoned" {
		t.Errorf("original invitation status after the public takeover = %q, want %q", afterStatus, "abandoned")
	}
	if afterError == nil || *afterError == "" {
		t.Error("original invitation row's error is empty after being cancelled, want a reason recorded")
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
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	const attackerClaimed = "172.16.0.7"
	const realPeer = "198.51.100.9"
	resp := doSubscribeFrom(t, h, mux, subscribeBody(email, []string{}, time.Now()),
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
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	const spoofed = "172.16.0.7"
	const realPeer = "198.51.100.9"
	resp := doSubscribeFrom(t, h, mux, subscribeBody(email, []string{}, time.Now()),
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
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
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
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{"not-a-real-slug"}, time.Now()))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if subscriberExists(t, pool, email) {
		t.Error("a subscriber row was created despite an unknown interest slug")
	}
	if outboundQueueCountFor(t, pool, email) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0", outboundQueueCountFor(t, pool, email))
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
		// #0026's review (narrowing the non-ASCII rejection): non-ASCII
		// local parts and domains are now accepted. Normalization moved
		// into SQL (lower(trim($1)) in internal/subscribers), so the
		// Go/Postgres lower() disagreement that used to justify rejecting
		// these outright no longer exists — see that package's doc
		// comment.
		{"café@example.com", true},
		{"person@exämple.com", true},
		{"ǅ@example.com", true}, // the exact titlecase-digraph codepoint the review named
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
	h, mux := subscribeMux(t, pool, nil)

	resp := doSubscribe(t, h, mux, subscribeBody("not-an-email", nil, time.Now()))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestSubscribe_NonASCIIEmailAccepted is #0026's review, "narrow the
// non-ASCII rejection": a non-ASCII local part is now a legitimate signup
// (RFC 6531), not a 400. This end-to-end request exercises the SQL-side
// normalization path (internal/subscribers' Create uses lower(trim($1)))
// all the way through — a 500 here would mean the Go/Postgres lower()
// disagreement the review found still exists somewhere.
func TestSubscribe_NonASCIIEmailAccepted(t *testing.T) {
	pool := subscribeTestPool(t)
	email := fmt.Sprintf("café-%d@example.com", testdb.Unique())
	h, mux := subscribeMux(t, pool, nil)

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 for a non-ASCII local part", resp.StatusCode)
	}
	if !subscriberExists(t, pool, email) {
		t.Error("no subscriber row was created for a non-ASCII address")
	}
	if outboundQueueCountFor(t, pool, email) != 1 {
		t.Fatalf("mailer.Sent() = %d messages, want 1", outboundQueueCountFor(t, pool, email))
	}
}

// TestSubscribe_TitlecaseDigraphEmailAccepted is the end-to-end regression
// test for the exact codepoint #0026's review named (ǅ U+01C5): Go's
// strings.ToLower folds it to ǆ U+01C6, Postgres's lower() leaves it
// unchanged. Under the old Go-side normalizeEmail + ASCII-only rejection,
// this address was simply refused; under SQL-side normalization it must
// succeed with no CHECK-constraint 500.
func TestSubscribe_TitlecaseDigraphEmailAccepted(t *testing.T) {
	pool := subscribeTestPool(t)
	email := fmt.Sprintf("ǅ-%d@example.com", testdb.Unique())
	h, mux := subscribeMux(t, pool, nil)

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if !subscriberExists(t, pool, email) {
		t.Error("no subscriber row was created")
	}
}

func TestSubscribe_DisposableDomainRejected(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := "zz-subtest@mailinator.com"
	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
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
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	id := seedSubscriberRow(t, pool, email, subscribers.StatusActive, nil, nil)

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	// #0126: content assertions moved to internal/mailing's template
	// tests — see TestSubscribe_NewSignup_CreatesPendingSendsConfirmationAndAudits's
	// comment for why.
	rows := outboundQueueRowsFor(t, pool, email)
	if len(rows) != 1 {
		t.Fatalf("outbound_queue rows for %q = %d, want 1", email, len(rows))
	}
	if rows[0].Kind != "already_subscribed" {
		t.Errorf("kind = %q, want %q", rows[0].Kind, "already_subscribed")
	}

	if status := subscriberStatus(t, pool, id); status != subscribers.StatusActive {
		t.Errorf("status = %q after re-submitting an active address, want unchanged %q", status, subscribers.StatusActive)
	}
}

// TestSubscribe_ActiveSubscriber_AlreadySubscribedEmailCooldown is #0026's
// review finding 1's amplification fix, reproduced end to end: 20
// sequential submits of one active subscriber's address used to produce 20
// "already subscribed" emails to that person — an unauthenticated mail-
// amplification vector against any confirmed subscriber. It must now
// produce exactly one, with the rest silently claimed-out.
func TestSubscribe_ActiveSubscriber_AlreadySubscribedEmailCooldown(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	seedSubscriberRow(t, pool, email, subscribers.StatusActive, nil, nil)

	const submits = 20
	for i := 0; i < submits; i++ {
		resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("submit %d: status = %d, want 202", i, resp.StatusCode)
		}
	}

	if sent := outboundQueueCountFor(t, pool, email); sent != 1 {
		t.Fatalf("mailer.Sent() = %d messages after %d sequential submits, want exactly 1 (PRD §6.3 amplification fix)", sent, submits)
	}
}

func TestSubscribe_PendingSubscriber_ResendRateLimitedToOncePerHour(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	fixedNow := time.Now().UTC().Truncate(time.Second)
	h.now = func() time.Time { return fixedNow }

	email := subscribeUniqueEmail(t)
	token := "tok-" + email
	recentlySent := fixedNow.Add(-30 * time.Minute)
	id := seedSubscriberRow(t, pool, email, subscribers.StatusPending, &token, &recentlySent)

	// Within the cooldown: no email sent, confirm_sent_at untouched.
	resp := doSubscribe(t, h, mux, subscribeBody(email, nil, fixedNow))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if outboundQueueCountFor(t, pool, email) != 0 {
		t.Fatalf("mailer.Sent() = %d messages within the cooldown window, want 0", outboundQueueCountFor(t, pool, email))
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

	resp2 := doSubscribe(t, h, mux, subscribeBody(email, nil, fixedNow))
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp2.StatusCode)
	}
	if outboundQueueCountFor(t, pool, email) != 1 {
		t.Fatalf("mailer.Sent() = %d messages outside the cooldown window, want 1", outboundQueueCountFor(t, pool, email))
	}
	_, _, _, confirmSentAt2 := subscriberRow(t, pool, email)
	if confirmSentAt2 == nil || !confirmSentAt2.Equal(fixedNow) {
		t.Errorf("confirm_sent_at = %v, want %v", confirmSentAt2, fixedNow)
	}
}

// ============================================================================
// #0026's review, finding 3 — concurrent submits must send at most one
// confirmation, end to end over the real HTTP handler (not just the store's
// own ClaimConfirmationSend test). Reproduces the review's exact numbers:
// "8 simultaneous submits of a BRAND-NEW address -> 3 confirmation emails
// sent" and "8 simultaneous submits of a PENDING address, confirm_sent_at
// NULL -> 7 confirmation emails sent" against the pre-fix handler. Both
// must now be exactly 1.
// ============================================================================

// concurrentSubscribe fires n simultaneous POST /api/subscribe requests for
// the same body and blocks until every one has both returned AND had its
// async send (if any) processed, so the caller can count sent mail
// deterministically.
func concurrentSubscribe(t *testing.T, h *SubscribeHandler, mux http.Handler, body map[string]any, n int) {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			b, err := json.Marshal(body)
			if err != nil {
				t.Errorf("marshal: %v", err)
				return
			}
			req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusAccepted {
				t.Errorf("concurrent request: status = %d, want 202", rec.Code)
			}
		}()
	}
	close(start) // release every goroutine at once, maximizing the race window
	wg.Wait()
	h.waitForSends()
}

// TestSubscribe_ConcurrentNewSignups_SendsExactlyOneConfirmation is the
// "8 simultaneous submits of a BRAND-NEW address" case from #0026's review.
// Before the fix (a read-then-write MarkConfirmationSent, with FindByEmail
// racing Create), interleaved requests could observe a just-created pending
// row with confirm_sent_at still NULL and independently resend — 3 of 8 in
// the review's own measurement. The atomic ClaimConfirmationSend closes
// that window: exactly one request may ever claim a cold row.
func TestSubscribe_ConcurrentNewSignups_SendsExactlyOneConfirmation(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t) + "-concurrent-new"
	concurrentSubscribe(t, h, mux, subscribeBody(email, nil, time.Now()), 8)

	if sent := outboundQueueCountFor(t, pool, email); sent != 1 {
		t.Fatalf("mailer.Sent() = %d messages after 8 simultaneous submits of a brand-new address, want exactly 1", sent)
	}
	if !subscriberExists(t, pool, email) {
		t.Error("no subscriber row was created")
	}
}

// TestSubscribe_ConcurrentPendingResends_SendsExactlyOneConfirmation is the
// "8 simultaneous submits of a PENDING address, confirm_sent_at NULL" case
// from #0026's review (7 of 8 sent, pre-fix).
func TestSubscribe_ConcurrentPendingResends_SendsExactlyOneConfirmation(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t) + "-concurrent-pending"
	token := "tok-" + email
	seedSubscriberRow(t, pool, email, subscribers.StatusPending, &token, nil) // confirm_sent_at NULL

	concurrentSubscribe(t, h, mux, subscribeBody(email, nil, time.Now()), 8)

	if sent := outboundQueueCountFor(t, pool, email); sent != 1 {
		t.Fatalf("mailer.Sent() = %d messages after 8 simultaneous submits of a pending address with confirm_sent_at NULL, want exactly 1", sent)
	}
}

func TestSubscribe_UnsubscribedSubscriber_RestartsWithFreshTokenAndSendsConfirmation(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	id := seedSubscriberRow(t, pool, email, subscribers.StatusUnsubscribed, nil, nil)

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{"homelab"}, time.Now()))
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

	if outboundQueueCountFor(t, pool, email) != 1 {
		t.Fatalf("mailer.Sent() = %d messages, want 1", outboundQueueCountFor(t, pool, email))
	}
	if n := auditSignupCount(t, pool, id); n != 1 {
		t.Errorf("subscriber.signup audit rows for %d = %d, want 1", id, n)
	}
}

func TestSubscribe_BouncedSubscriber_RestartsSameAsUnsubscribed(t *testing.T) {
	pool := subscribeTestPool(t)
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	seedSubscriberRow(t, pool, email, subscribers.StatusBounced, nil, nil)

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{}, time.Now()))
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
	if outboundQueueCountFor(t, pool, email) != 1 {
		t.Fatalf("mailer.Sent() = %d messages, want 1", outboundQueueCountFor(t, pool, email))
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
	h, mux := subscribeMux(t, pool, nil)

	email := subscribeUniqueEmail(t)
	id := seedSubscriberRow(t, pool, email, subscribers.StatusComplained, nil, nil)

	resp := doSubscribe(t, h, mux, subscribeBody(email, []string{"beginner"}, time.Now()))
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
	if outboundQueueCountFor(t, pool, email) != 0 {
		t.Errorf("mailer.Sent() = %d messages, want 0 — nothing is ever sent to a complained address", outboundQueueCountFor(t, pool, email))
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

// TestSubscribe_AsyncSendDoesNotBlockResponse is #0081's regression test for
// the async-work property #0026's review closed a 100%-accurate
// enumeration oracle by establishing (see subscribe.go's package doc
// comment, "The byte-identical body is not the only observable"). It was
// completely unguarded: #0081's own mutation proof found that putting
// mailer.Send back on the request goroutine left the entire suite green.
//
// #0126 removed the mailer this test used to block entirely — sending
// moved to internal/mailing.OutboxWorker, a separate process. The
// equivalent property for THIS handler is now "the response is not blocked
// by the async subscriber-store mutation", proven the same deterministic,
// non-timing way via blockingSubscriberStore (which TestSubscribe_
// MutationDeferredDoesNotBlockResponse, below, already exercises for all
// three of Create/RestartSignup/ClaimAndEnqueueAlreadySubscribed — this
// test keeps the Create case under its original #0081 name and citation
// for discoverability, since Create is also where #0126 folded the
// confirmation claim-and-enqueue itself, making this the closest direct
// descendant of the original mailer-blocking test).
func TestSubscribe_AsyncSendDoesNotBlockResponse(t *testing.T) {
	pool := subscribeTestPool(t)

	blocked := newBlockingSubscriberStore(subscribers.NewStore(pool))
	h := NewSubscribeHandler(
		blocked, interests.NewStore(pool),
		NoSuppressions{}, audit.New(pool), "https://example.test", nil,
		outbox.NewStore(pool),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.Close(ctx); err != nil {
			t.Errorf("cleanup: h.Close: %v", err)
		}
	})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/subscribe", h.Subscribe)

	email := subscribeUniqueEmail(t)
	body, err := json.Marshal(subscribeBody(email, nil, time.Now()))
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
		// Good: Subscribe returned even though blocked.release has not
		// been (and, within this test, will not be) closed.
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not return within 2s while Create is blocked — processMutateJob appears to be running on the request goroutine, which would reopen #0026's/#0088's timing oracle")
	}

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	// Confirm the mutation really was attempted for this signup —
	// otherwise a fast response would prove nothing (nothing queued at
	// all).
	select {
	case <-blocked.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Create was never entered — no mutation was queued for this signup; the fast response above proves nothing")
	}
}

// TestSubscribeHandler_Close_InterruptsInFlightMutationPromptly is #0081's
// proof, adapted for #0126: Close must interrupt a queued-but-not-yet-
// started or in-flight mutation job promptly (via sendCtx cancellation)
// rather than waiting for it to finish. Before #0126 this test proved the
// analogous property by blocking mailer.Send and checking the confirmation
// claim was released on shutdown (ReleaseConfirmationClaim); that
// compensating action no longer exists (see subscribers.Store.
// ClaimAndEnqueueConfirmation's doc comment) because there is no longer a
// window where a claim can be stamped without its enqueue — the two commit
// together in one transaction. So this test instead blocks Create itself:
// a Create interrupted by Close's cancellation never reaches
// b.inner.Create at all (blockingSubscriberStore.awaitReleaseOrCancel
// returns ctx.Err() BEFORE the delegate call), so no subscriber row is
// created and there is nothing left half-done to clean up — a stronger
// property than "release the claim", since there is no claim to release in
// the first place.
func TestSubscribeHandler_Close_InterruptsInFlightMutationPromptly(t *testing.T) {
	pool := subscribeTestPool(t)

	blocked := newBlockingSubscriberStore(subscribers.NewStore(pool)) // release is never closed — Close's context cancellation is the only thing that can end this
	h := NewSubscribeHandler(
		blocked, interests.NewStore(pool),
		NoSuppressions{}, audit.New(pool), "https://example.test", nil,
		outbox.NewStore(pool),
	)

	email := subscribeUniqueEmail(t)
	resp := doSubscribeFrom(t, nil, http.HandlerFunc(h.Subscribe), subscribeBody(email, nil, time.Now()), "", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	select {
	case <-blocked.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Create was never entered — nothing for Close to interrupt")
	}

	if subscriberExists(t, pool, email) {
		t.Fatal("before Close: a subscriber row already exists — Create should still be blocked")
	}

	// Graceful shutdown, bounded generously — should return in well under
	// the bound because sendCtx cancellation interrupts the blocked Create
	// directly, rather than this depending on release ever being closed
	// (it never is).
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := h.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Close took %v to return the mutation was interrupted, not waited out — want well under the 3s bound", elapsed)
	}

	if subscriberExists(t, pool, email) {
		t.Fatal("after Close: a subscriber row was created — an interrupted Create must not have reached the delegate store")
	}

	// A second call must be a no-op, not a panic (double-close of
	// mutateQueue) or a second cancel.
	if err := h.Close(closeCtx); err != nil {
		t.Fatalf("second Close call: %v, want nil (idempotent)", err)
	}

	// Retry, immediately: must succeed for real now, over a fresh handler
	// (the interrupted one is closed and refuses new work).
	h2, mux2 := subscribeMux(t, pool, nil)
	resp2 := doSubscribe(t, h2, mux2, subscribeBody(email, nil, time.Now()))
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("retry status = %d, want 202", resp2.StatusCode)
	}
	if !subscriberExists(t, pool, email) {
		t.Fatal("retry: no subscriber row was created")
	}
	if got := outboundQueueCountFor(t, pool, email); got != 1 {
		t.Fatalf("retry: outbound_queue rows for %q = %d, want exactly 1", email, got)
	}
}

// TestNewSubscribeHandler_NoGoroutineLeakAcrossConstruction is #0081's proof
// that Close actually stops mutateWorkerCount goroutines per construction
// from accumulating forever — before Close existed, there was no way to
// stop them at all (#0026's review residual #1: "~120 of them accumulate in
// the test binary"). #0126 removed the separate sendWorkerCount pool this
// test used to also cover; mutateQueue/mutateWG is now the only pool.
func TestNewSubscribeHandler_NoGoroutineLeakAcrossConstruction(t *testing.T) {
	pool := subscribeTestPool(t)

	runtime.GC()
	before := runtime.NumGoroutine()

	const rounds = 50
	for i := 0; i < rounds; i++ {
		h := NewSubscribeHandler(
			subscribers.NewStore(pool), interests.NewStore(pool),
			NoSuppressions{}, nil, "https://example.test", nil,
			outbox.NewStore(pool),
		)
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := h.Close(ctx); err != nil {
				t.Fatalf("round %d: Close: %v", i, err)
			}
		}()
	}

	// A worker's final loop iteration — receiving the closed, drained
	// channel's zero value and returning — happens a moment after Close's
	// mutateWG.Wait() unblocks, not synchronously with it. Poll with a
	// bounded settle window instead of asserting the instant Close returns,
	// the same allowance the standard library's own goroutine-leak checks
	// make for scheduler tail latency.
	var after int
	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.GC()
		after = runtime.NumGoroutine()
		if after <= before+2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	perHandler := mutateWorkerCount
	t.Logf("goroutines before=%d after=%d across %d constructions (each starting %d workers, %d total if leaked)",
		before, after, rounds, perHandler, rounds*perHandler)
	if after > before+2 {
		t.Fatalf("goroutine count grew from %d to %d after constructing and closing %d handlers — mutateWorkerCount workers are leaking", before, after, rounds)
	}
}

// ============================================================================
// #0088 — equal query counts per branch.
//
// #0026's review found that leaving a synchronous SES call (~80ms) on the
// request goroutine made "did this branch send mail" observable as latency,
// with fully disjoint distributions across branches. Moving mailer.Send off
// the request path closed that channel, but #0088 measured a smaller,
// still-total residual: even with the mail send gone, honeypot/timing-gate/
// active/pending/new-signup each did a DIFFERENT number of synchronous store
// calls (0 for honeypot and timing-gate, 2-3 for active/pending, 5+ for a
// brand-new signup), and that alone was enough to classify branch membership
// from a single observation of elapsed time (see issues/0088.md's measured
// clusters).
//
// A test that asserts elapsed time directly would be flaky — #0084 exists
// because load-sensitive tests train people to re-run instead of
// investigate. The structural property that actually has to hold, and can
// be asserted without any clock, is this: every branch does EXACTLY the
// same synchronous store/suppression-checker call count (one IsSuppressed,
// one FindByEmail, zero of everything else) before the response is
// written — with all of the calls that legitimately differ by branch
// (Create, RestartSignup, SetInterests, the two Claim* calls) deferred onto
// the async mutation queue #0088 added, so they can never contribute to
// request-path latency regardless of how many of them a given branch ends
// up making.
// ============================================================================

// countingSubscriberStore wraps a real subscriberStore, counting calls to
// each method so a test can assert on call counts instead of timing.
// Delegates every call to inner (subscribers.NewStore(pool) in these
// tests), so behavior is identical to the real store — only the counts are
// new.
type countingSubscriberStore struct {
	inner subscriberStore

	mu                                    sync.Mutex
	createCalls                           int
	findByEmailCalls                      int
	restartSignupCalls                    int
	claimAndEnqueueConfirmationCalls      int
	claimAndEnqueueAlreadySubscribedCalls int
	setInterestsCalls                     int
}

func (c *countingSubscriberStore) Create(ctx context.Context, in subscribers.NewSignup, now time.Time) (subscribers.Subscriber, error) {
	c.mu.Lock()
	c.createCalls++
	c.mu.Unlock()
	return c.inner.Create(ctx, in, now)
}

func (c *countingSubscriberStore) FindByEmail(ctx context.Context, email string) (subscribers.Subscriber, error) {
	c.mu.Lock()
	c.findByEmailCalls++
	c.mu.Unlock()
	return c.inner.FindByEmail(ctx, email)
}

func (c *countingSubscriberStore) RestartSignup(ctx context.Context, id int64, in subscribers.RestartSignupInput, now time.Time) (subscribers.Subscriber, error) {
	c.mu.Lock()
	c.restartSignupCalls++
	c.mu.Unlock()
	return c.inner.RestartSignup(ctx, id, in, now)
}

func (c *countingSubscriberStore) ClaimAndEnqueueConfirmation(ctx context.Context, sub subscribers.Subscriber, now time.Time, cooldown, ttl time.Duration, evidence subscribers.RestartSignupInput) (bool, error) {
	c.mu.Lock()
	c.claimAndEnqueueConfirmationCalls++
	c.mu.Unlock()
	return c.inner.ClaimAndEnqueueConfirmation(ctx, sub, now, cooldown, ttl, evidence)
}

func (c *countingSubscriberStore) ClaimAndEnqueueAlreadySubscribed(ctx context.Context, sub subscribers.Subscriber, now time.Time, cooldown time.Duration) (bool, error) {
	c.mu.Lock()
	c.claimAndEnqueueAlreadySubscribedCalls++
	c.mu.Unlock()
	return c.inner.ClaimAndEnqueueAlreadySubscribed(ctx, sub, now, cooldown)
}

func (c *countingSubscriberStore) SetInterests(ctx context.Context, subscriberID int64, interestIDs []int64) error {
	c.mu.Lock()
	c.setInterestsCalls++
	c.mu.Unlock()
	return c.inner.SetInterests(ctx, subscriberID, interestIDs)
}

type subscriberStoreCallCounts struct {
	create, findByEmail, restartSignup                                          int
	claimAndEnqueueConfirmation, claimAndEnqueueAlreadySubscribed, setInterests int
}

// snapshot returns a race-safe copy of every counter, for comparison after
// the async queues have fully drained.
func (c *countingSubscriberStore) snapshot() subscriberStoreCallCounts {
	c.mu.Lock()
	defer c.mu.Unlock()
	return subscriberStoreCallCounts{
		create:                           c.createCalls,
		findByEmail:                      c.findByEmailCalls,
		restartSignup:                    c.restartSignupCalls,
		claimAndEnqueueConfirmation:      c.claimAndEnqueueConfirmationCalls,
		claimAndEnqueueAlreadySubscribed: c.claimAndEnqueueAlreadySubscribedCalls,
		setInterests:                     c.setInterestsCalls,
	}
}

// countingSuppressionChecker wraps a SuppressionChecker, counting calls to
// IsSuppressed.
type countingSuppressionChecker struct {
	inner SuppressionChecker

	mu    sync.Mutex
	calls int
}

func (c *countingSuppressionChecker) IsSuppressed(ctx context.Context, email string) (bool, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	if c.inner == nil {
		return NoSuppressions{}.IsSuppressed(ctx, email)
	}
	return c.inner.IsSuppressed(ctx, email)
}

func (c *countingSuppressionChecker) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// subscribeCountingMux is subscribeMux but with the subscriberStore and
// SuppressionChecker wrapped in call counters, returning the counters
// alongside the usual handler/mux pair.
func subscribeCountingMux(t *testing.T, pool *pgxpool.Pool, suppression SuppressionChecker) (*SubscribeHandler, http.Handler, *countingSubscriberStore, *countingSuppressionChecker) {
	t.Helper()
	subs := &countingSubscriberStore{inner: subscribers.NewStore(pool)}
	supp := &countingSuppressionChecker{inner: suppression}
	h := NewSubscribeHandler(
		subs, interests.NewStore(pool),
		supp, audit.New(pool), "https://example.test", nil,
		outbox.NewStore(pool),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.Close(ctx); err != nil {
			t.Errorf("subscribeCountingMux cleanup: h.Close: %v", err)
		}
	})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/subscribe", h.Subscribe)
	return h, mux, subs, supp
}

// TestSubscribe_SyncQueryCountEqualAcrossBranches is #0088's structural
// proof, replacing a direct timing assertion (which would flake — #0084).
// It drives every branch through a FRESH handler/counter pair (so counts
// never accumulate across branches) and asserts that IsSuppressed and
// FindByEmail are each called exactly once, no matter which branch ran —
// the fixed synchronous cost the package doc comment ("The elapsed-time
// channel — #0088") describes — while every branch-dependent mutation call
// (Create, RestartSignup, SetInterests, either Claim*) only ever happens
// for the branches that legitimately need it, confirming those calls truly
// moved off anything that could be the request-path cost.
func TestSubscribe_SyncQueryCountEqualAcrossBranches(t *testing.T) {
	pool := subscribeTestPool(t)

	pendingEmail := subscribeUniqueEmail(t) + "-qc-pending"
	pendingToken := "tok-" + pendingEmail
	seedSubscriberRow(t, pool, pendingEmail, subscribers.StatusPending, &pendingToken, nil)
	unsubEmail := subscribeUniqueEmail(t) + "-qc-unsub"
	seedSubscriberRow(t, pool, unsubEmail, subscribers.StatusUnsubscribed, nil, nil)
	complainedEmail := subscribeUniqueEmail(t) + "-qc-complained"
	seedSubscriberRow(t, pool, complainedEmail, subscribers.StatusComplained, nil, nil)
	activeEmail := subscribeUniqueEmail(t) + "-qc-active"
	seedSubscriberRow(t, pool, activeEmail, subscribers.StatusActive, nil, nil)

	suppressedEmail := subscribeUniqueEmail(t) + "-qc-suppressed"
	now := time.Now()

	cases := []struct {
		name             string
		body             map[string]any
		suppression      SuppressionChecker
		wantMutationCall bool // whether ANY of create/restart/claim* should fire
	}{
		{"honeypot", withHoneypot(subscribeBody(subscribeUniqueEmail(t)+"-qc-bot", nil, now)), nil, false},
		{"timing-gate", withTooFast(subscribeBody(subscribeUniqueEmail(t)+"-qc-fast", nil, now), now), nil, false},
		{"suppressed", subscribeBody(suppressedEmail, nil, now), fakeSuppressionChecker{suppressed: map[string]bool{suppressedEmail: true}}, false},
		{"complained", subscribeBody(complainedEmail, nil, now), nil, false},
		{"active", subscribeBody(activeEmail, nil, now), nil, true},
		{"pending", subscribeBody(pendingEmail, nil, now), nil, true},
		{"unsubscribed", subscribeBody(unsubEmail, nil, now), nil, true},
		{"new-signup", subscribeBody(subscribeUniqueEmail(t)+"-qc-new", nil, now), nil, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, mux, subs, supp := subscribeCountingMux(t, pool, c.suppression)

			resp := doSubscribe(t, h, mux, c.body)
			if resp.StatusCode != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", resp.StatusCode)
			}

			if got := supp.callCount(); got != 1 {
				t.Errorf("IsSuppressed calls = %d, want exactly 1 (the fixed cost every branch must pay)", got)
			}
			snap := subs.snapshot()
			if snap.findByEmail != 1 {
				t.Errorf("FindByEmail calls = %d, want exactly 1 (the fixed cost every branch must pay)", snap.findByEmail)
			}

			gotMutation := snap.create > 0 || snap.restartSignup > 0 ||
				snap.claimAndEnqueueConfirmation > 0 || snap.claimAndEnqueueAlreadySubscribed > 0
			if gotMutation != c.wantMutationCall {
				t.Errorf("branch %q: a mutation store call happened = %v, want %v (create=%d restart=%d claimConfirm=%d claimAlready=%d)",
					c.name, gotMutation, c.wantMutationCall, snap.create, snap.restartSignup, snap.claimAndEnqueueConfirmation, snap.claimAndEnqueueAlreadySubscribed)
			}
		})
	}
}

// TestSubscribe_MutationDeferredDoesNotBlockResponse is #0088's regression
// test for the deferral half of the fix — the half the review found
// completely unguarded. TestSubscribe_SyncQueryCountEqualAcrossBranches
// snapshots its counters AFTER h.waitForSends() has already drained the
// queue (via doSubscribe), so it cannot distinguish a store call made on
// the request goroutine from one deferred to a worker: the totals it
// checks are identical either way. The review proved this is not
// theoretical — reverting Subscribe to call processMutateJob synchronously
// on the request goroutine (Create/RestartSignup/SetInterests/Claim*/audit,
// all inline, exactly as before #0088) left every test in this file green,
// including that one.
//
// This closes the hole the same deterministic, non-timing way
// TestSubscribe_AsyncSendDoesNotBlockResponse (#0081, above) closes the
// equivalent gap for mailer.Send: a subscriberStore whose one designated
// method blocks until released, driven directly over ServeHTTP, with
// Subscribe's response required to return while that method is PROVABLY
// still blocked — blocked.release is never closed anywhere in this test,
// only Close's sendCtx cancellation (via t.Cleanup) can ever unblock it,
// and that cancellation does not fire within this test's own 2s bound. If
// processMutateJob ever ran synchronously on the request goroutine again,
// ServeHTTP would not return until Close's cancellation interrupts it —
// i.e. this subtest would hang past the bound and fail, not merely report
// a wrong count. The 2s figure is a hang guard, not a race window: the
// blocked method should return in well under a millisecond when the
// property holds, the same allowance TestSubscribe_AsyncSendDoesNotBlockResponse
// already makes.
//
// #0098 widened this from a single Create-only case to three subtests, one
// per store method processMutateJob's dispatch can reach — because the
// original single case caught only a defect that un-defers the ENTIRE
// mutation job, not one that un-defers a single branch. #0088's second
// review proved that gap concretely: dispatching only the
// existing-subscriber branches inline (`if findErr == nil {
// runMutationSynchronously(job) } else { enqueueMutation(job) }`) left the
// whole package green, because the guard's one Create-blocking case is only
// ever reached via the findErr != nil (ErrNotFound, brand-new signup)
// path — it never observes RestartSignup or ClaimAlreadySubscribedSend,
// which are exactly the calls that shape would put back on the request
// path. #0098's own Verification section (issues/0098.md) records the
// mutation proof: applying exactly that conditional dispatch made the
// "unsubscribed restart" and "already active resend" subtests below fail
// while the pre-existing "brand new signup" subtest kept passing —
// confirming the widened coverage catches what the original single case
// could not, without weakening what it already caught.
func TestSubscribe_MutationDeferredDoesNotBlockResponse(t *testing.T) {
	pool := subscribeTestPool(t)

	tests := []struct {
		// name also documents which existingSignup branch (PRD §6.3's
		// table) reaches the blocked method: brand-new signups reach
		// Create via newSignup; an unsubscribed (or bounced — same code
		// path) subscriber reaches RestartSignup via restartSignup; an
		// active subscriber reaches ClaimAlreadySubscribedSend via
		// sendAlreadySubscribed.
		name  string
		which string
		seed  func(t *testing.T, pool *pgxpool.Pool, email string)
	}{
		{
			name:  "brand new signup (Create)",
			which: "Create",
			seed:  func(t *testing.T, pool *pgxpool.Pool, email string) {},
		},
		{
			name:  "unsubscribed restart (RestartSignup)",
			which: "RestartSignup",
			seed: func(t *testing.T, pool *pgxpool.Pool, email string) {
				seedSubscriberRow(t, pool, email, subscribers.StatusUnsubscribed, nil, nil)
			},
		},
		{
			name:  "already active resend (ClaimAndEnqueueAlreadySubscribed)",
			which: "ClaimAndEnqueueAlreadySubscribed",
			seed: func(t *testing.T, pool *pgxpool.Pool, email string) {
				seedSubscriberRow(t, pool, email, subscribers.StatusActive, nil, nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			email := subscribeUniqueEmail(t)
			tc.seed(t, pool, email)

			blocked := newBlockingSubscriberStoreOn(subscribers.NewStore(pool), tc.which)
			h := NewSubscribeHandler(
				blocked, interests.NewStore(pool),
				NoSuppressions{}, audit.New(pool), "https://example.test", nil,
				outbox.NewStore(pool),
			)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := h.Close(ctx); err != nil {
					t.Errorf("cleanup: h.Close: %v", err)
				}
			})
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/subscribe", h.Subscribe)

			body, err := json.Marshal(subscribeBody(email, nil, time.Now()))
			if err != nil {
				t.Fatalf("marshal request body: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			done := make(chan struct{})
			go func() {
				mux.ServeHTTP(rec, req)
				close(done)
			}()

			select {
			case <-done:
				// Good: Subscribe returned even though blocked.release has
				// not been (and, within this subtest, will not be) closed.
			case <-time.After(2 * time.Second):
				t.Fatalf("Subscribe did not return within 2s while %s is still blocked — processMutateJob appears to be running on the request goroutine, which would reopen #0088's timing oracle", tc.which)
			}

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
			}

			// Confirm the mutation really was queued for this signup —
			// otherwise a fast response would prove nothing (the blocked
			// method never attempted).
			select {
			case <-blocked.entered:
			case <-time.After(2 * time.Second):
				t.Fatalf("%s was never entered — no mutation was queued for this signup; the fast response above proves nothing", tc.which)
			}
		})
	}
}
