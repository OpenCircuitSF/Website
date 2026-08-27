// delivery_health_test.go proves #0124's circuit breaker end to end,
// through the real worker (claimAndDrain), not just checkDeliveryHealth in
// isolation — matching this package's own convention (worker_test.go)
// established for the physical_address refusal and every other worker-level
// safety property.
package mailing

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// breakerTestMailer always succeeds (RecordingMailer-shaped: a
// deterministic, incrementing fake message id per call) but, for every
// recipient in bounceEmails/complainEmails, synchronously inserts a
// matching email_events row for the message id it is about to return —
// BEFORE returning it. This puts the bounce/complaint on the record through
// the SAME table (and the SAME ses_message_id join key) a real SES webhook
// would populate, so CampaignStatsStore.EventCounts (what
// checkDeliveryHealth actually reads) sees it the moment the worker's
// per-batch health check runs after this send commits — no test-only
// bypass of the read path, only of the (irrelevant here) network send.
//
// #0269: prefix is stamped once from testdb.Unique() at construction, so
// every id this mailer ever returns is globally unique across the whole
// test binary, not just within one instance. Before this, ids were plain
// "breaker-1", "breaker-2", ... restarting at 1 in every test — and
// EventCounts joins on ses_message_id ALONE, so two tests running email_sends
// through two breakerTestMailers could (and, measured by the reviewer, did:
// 219 event rows sharing only 100 distinct ids, "breaker-1" appearing six
// times) collide on the same id and read each other's bounce/complaint
// counts. Production is unaffected — real SES message ids are unique.
type breakerTestMailer struct {
	mu             sync.Mutex
	pool           *pgxpool.Pool
	bounceEmails   map[string]bool
	complainEmails map[string]bool
	nextID         int
	prefix         int64
	sent           []Message
}

func newBreakerTestMailer(pool *pgxpool.Pool) *breakerTestMailer {
	return &breakerTestMailer{pool: pool, bounceEmails: map[string]bool{}, complainEmails: map[string]bool{}, prefix: testdb.Unique()}
}

func (m *breakerTestMailer) Send(ctx context.Context, msg Message) (string, error) {
	m.mu.Lock()
	m.nextID++
	id := fmt.Sprintf("breaker-%d-%d", m.prefix, m.nextID)
	m.sent = append(m.sent, msg)
	m.mu.Unlock()

	if m.bounceEmails[msg.To] {
		if err := m.recordEvent(ctx, id, msg.To, EventTypeBounceForTest); err != nil {
			return "", err
		}
	}
	if m.complainEmails[msg.To] {
		if err := m.recordEvent(ctx, id, msg.To, EventTypeComplaintForTest); err != nil {
			return "", err
		}
	}
	return id, nil
}

// EventTypeBounceForTest/EventTypeComplaintForTest mirror sesnotify's own
// event_type strings ("Bounce"/"Complaint") — internal/mailing does not
// import internal/sesnotify (no production dependency between them; this
// test's job is only to populate the SAME table with the SAME shape a real
// webhook would), so the literal strings are restated here rather than
// imported.
const (
	EventTypeBounceForTest    = "Bounce"
	EventTypeComplaintForTest = "Complaint"
)

func (m *breakerTestMailer) recordEvent(ctx context.Context, sesMessageID, recipient, eventType string) error {
	bounceType := ""
	if eventType == EventTypeBounceForTest {
		bounceType = "Transient"
	}
	_, err := m.pool.Exec(ctx,
		`INSERT INTO email_events (sns_message_id, ses_message_id, event_type, bounce_type, recipient, payload)
		 VALUES ($1, $2, $3, NULLIF($4, ''), lower(trim($5)), '{}'::jsonb)`,
		"zz-breaker-sns-"+sesMessageID, sesMessageID, eventType, bounceType, recipient,
	)
	return err
}

func (m *breakerTestMailer) Sent() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.sent))
	copy(out, m.sent)
	return out
}

var _ Mailer = (*breakerTestMailer)(nil)

// seedActiveSubscribers seeds n distinct active subscribers and returns
// their (normalized) emails, in no particular order relative to
// ClaimBatch's own claim order.
func seedActiveSubscribers(t *testing.T, pool *pgxpool.Pool, n int) []string {
	t.Helper()
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := seedSubscriber(t, pool, subscribers.StatusActive)
		out = append(out, subscriberEmailByIDForTest(t, pool, id))
	}
	return out
}

func subscriberEmailByIDForTest(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var email string
	if err := pool.QueryRow(context.Background(), `SELECT email FROM subscribers WHERE id = $1`, id).Scan(&email); err != nil {
		t.Fatalf("read subscriber %d email: %v", id, err)
	}
	return email
}

// countEmailSendsStatus counts campaignID's email_sends rows at status.
func countEmailSendsStatus(t *testing.T, pool *pgxpool.Pool, campaignID int64, status string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_sends WHERE campaign_id = $1 AND status = $2`, campaignID, status,
	).Scan(&n); err != nil {
		t.Fatalf("count email_sends(status=%s) for campaign %d: %v", status, campaignID, err)
	}
	return n
}

// ── Threshold boundary: bounce rate ─────────────────────────────────────────

// TestWorker_CircuitBreaker_BounceRate_JustBelowThresholdDoesNotTrip and
// its sibling below are #0124's mutation check for the bounce-rate
// threshold, mirroring #0227's own "one event either side of the band"
// method: sent=100 (>= min_sample), bounced=4 (4%, below the 5.0% default)
// must NOT trip — the campaign completes normally.
func TestWorker_CircuitBreaker_BounceRate_JustBelowThresholdDoesNotTrip(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	setSetting(t, pool, SettingSendHealthMinSample, "100")
	setSetting(t, pool, SettingSendHealthBouncePct, "5.0")
	setSetting(t, pool, SettingSendHealthComplaintPct, "0.1")

	emails := seedActiveSubscribers(t, pool, 100)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	resetCampaignAuditRows(t, pool, campaignID)

	mailer := newBreakerTestMailer(pool)
	for i := 0; i < 4; i++ {
		mailer.bounceEmails[emails[i]] = true
	}

	w := newTestWorker(t, pool, mailer)
	w.batchSize = 100
	w.stats = NewCampaignStatsStore(pool)

	if _, err := w.claimAndDrain(context.Background()); err != nil {
		t.Fatalf("claimAndDrain: %v", err)
	}

	if status := campaignStatus(t, pool, campaignID); status != CampaignStatusSent {
		t.Errorf("campaign status = %q, want %q (4%% bounce rate must not trip a 5.0%% threshold)", status, CampaignStatusSent)
	}
	if got := len(mailer.Sent()); got != 100 {
		t.Errorf("messages sent = %d, want 100 (nothing stopped)", got)
	}
}

// TestWorker_CircuitBreaker_BounceRate_AtThresholdTrips is the "at
// threshold" half: sent=100, bounced=5 (exactly 5.0%) must trip. Proves the
// comparison is >=, not >, and that the campaign is genuinely stopped: a
// second batch of 100 recipients must never be attempted.
func TestWorker_CircuitBreaker_BounceRate_AtThresholdTrips(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	setSetting(t, pool, SettingSendHealthMinSample, "100")
	setSetting(t, pool, SettingSendHealthBouncePct, "5.0")
	setSetting(t, pool, SettingSendHealthComplaintPct, "0.1")

	emails := seedActiveSubscribers(t, pool, 200)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	resetCampaignAuditRows(t, pool, campaignID)

	mailer := newBreakerTestMailer(pool)
	for i := 0; i < 5; i++ {
		mailer.bounceEmails[emails[i]] = true
	}

	auditLogger := auditLoggerForTest(t, pool)
	w := newTestWorker(t, pool, mailer)
	w.batchSize = 100 // two batches of 100; only the first should ever run
	w.stats = NewCampaignStatsStore(pool)
	w.auditor = auditLogger
	w.outbox = outbox.NewStore(pool)
	adminEmail := fmt.Sprintf("zz-breaker-admin-%d@example.com", testdb.Unique())
	w.adminEmail = adminEmail
	// The admin_alert this test's trip enqueues is a REAL outbound_queue
	// row, not scoped to this campaign — main_test.go's TestMain truncates
	// outbound_queue once at package setup, not between tests (CLAUDE.md
	// §8b: every test cleans up its own rows). Left uncleaned, it is a
	// stray 'queued' row a LATER test's OutboxWorker.pass could claim and
	// drain unexpectedly — this exact failure mode was caught by running
	// the full package suite, not just this test in isolation (an
	// unscoped kind='admin_alert' row inflated
	// TestOutboxWorker_DrainsConfirmationRow's mailer.Sent() from 1 to 2).
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE recipient = $1`, adminEmail)
	})

	if _, err := w.claimAndDrain(context.Background()); err != nil {
		t.Fatalf("claimAndDrain: %v", err)
	}

	if status := campaignStatus(t, pool, campaignID); status != CampaignStatusPausedDeliveryHealth {
		t.Fatalf("campaign status = %q, want %q (5%% bounce rate must trip a 5.0%% threshold)", status, CampaignStatusPausedDeliveryHealth)
	}
	if got := len(mailer.Sent()); got != 100 {
		t.Errorf("messages sent = %d, want exactly 100 — the breaker must stop before the second batch", got)
	}
	if queued := countEmailSendsStatus(t, pool, campaignID, "queued"); queued != 100 {
		t.Errorf("queued email_sends rows = %d, want 100 (the second batch's recipients, never attempted)", queued)
	}

	var auditCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE target_type = 'email_campaign' AND target_id = $1 AND action = 'email_campaign.paused_delivery_health'`,
		campaignID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("paused_delivery_health audit rows = %d, want 1", auditCount)
	}

	var alertCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE kind = 'admin_alert' AND recipient = $1 AND status = 'queued'`, adminEmail,
	).Scan(&alertCount); err != nil {
		t.Fatalf("count outbound_queue admin_alert rows: %v", err)
	}
	if alertCount < 1 {
		t.Error("no admin_alert row enqueued for the trip")
	}
}

// TestWorker_CircuitBreaker_ResumeIntoStillTrippedRate_SendsZeroMore is
// #0269 criterion 5: a Resume into a rate that is still over threshold must
// send ZERO further messages, not one more full batch. Before #0269,
// checkDeliveryHealth ran only at the END of a batch, so drainLoop's first
// pass after a resume claimed and sent a full w.batchSize before
// re-evaluating — measured by the reviewer: 300 recipients at a 100% bounce
// rate with batchSize=100 tripped at 100, then one Resume sent exactly +100
// more before re-pausing. This test reproduces exactly that shape and
// asserts the count stays at 100 across the resume.
func TestWorker_CircuitBreaker_ResumeIntoStillTrippedRate_SendsZeroMore(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	setSetting(t, pool, SettingSendHealthMinSample, "100")
	setSetting(t, pool, SettingSendHealthBouncePct, "5.0")
	setSetting(t, pool, SettingSendHealthComplaintPct, "0.1")

	// 300 recipients, every one bounces: the first batch of 100 alone is
	// enough to trip (100% >= 5.0%), and the rate never recovers on its
	// own — proving a genuine resume-into-a-still-bad-rate, not a rate that
	// happened to dip below threshold between batches.
	emails := seedActiveSubscribers(t, pool, 300)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	resetCampaignAuditRows(t, pool, campaignID)

	mailer := newBreakerTestMailer(pool)
	for _, e := range emails {
		mailer.bounceEmails[e] = true
	}

	w := newTestWorker(t, pool, mailer)
	w.batchSize = 100 // three batches of 100; only the first should ever run, before AND after the resume
	w.stats = NewCampaignStatsStore(pool)

	if _, err := w.claimAndDrain(context.Background()); err != nil {
		t.Fatalf("claimAndDrain (initial): %v", err)
	}
	if status := campaignStatus(t, pool, campaignID); status != CampaignStatusPausedDeliveryHealth {
		t.Fatalf("campaign status after initial drain = %q, want %q", status, CampaignStatusPausedDeliveryHealth)
	}
	if got := len(mailer.Sent()); got != 100 {
		t.Fatalf("messages sent after initial drain = %d, want exactly 100", got)
	}

	// The admin action: resume, exactly like POST
	// /admin/campaigns/{id}/resume (internal/handlers/admin_campaigns.go).
	campaignStore := NewCampaignStore(pool)
	if _, err := campaignStore.Resume(context.Background(), campaignID, time.Now()); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	// The worker's next poll pass claims the now-'scheduled' campaign
	// (materialization already happened, so drainCampaign goes straight to
	// drainLoop) and must re-trip at the TOP of the loop, before ClaimBatch
	// — sending nothing more.
	if _, err := w.claimAndDrain(context.Background()); err != nil {
		t.Fatalf("claimAndDrain (after resume): %v", err)
	}

	if status := campaignStatus(t, pool, campaignID); status != CampaignStatusPausedDeliveryHealth {
		t.Errorf("campaign status after resume = %q, want %q (still-bad rate must re-trip)", status, CampaignStatusPausedDeliveryHealth)
	}
	if got := len(mailer.Sent()); got != 100 {
		t.Errorf("messages sent after resume = %d, want still exactly 100 (zero further messages)", got)
	}
	if queued := countEmailSendsStatus(t, pool, campaignID, "queued"); queued != 200 {
		t.Errorf("queued email_sends rows after resume = %d, want 200 (both remaining batches, still untouched)", queued)
	}
}

// ── Threshold boundary: min_sample ──────────────────────────────────────────

// TestWorker_CircuitBreaker_BelowMinSample_NeverTrips proves "below
// send_health_min_sample sends, the breaker never trips" — even at a 100%
// bounce rate. sent=99 < min_sample=100 must complete normally.
func TestWorker_CircuitBreaker_BelowMinSample_NeverTrips(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	setSetting(t, pool, SettingSendHealthMinSample, "100")
	setSetting(t, pool, SettingSendHealthBouncePct, "5.0")
	setSetting(t, pool, SettingSendHealthComplaintPct, "0.1")

	emails := seedActiveSubscribers(t, pool, 99)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	resetCampaignAuditRows(t, pool, campaignID)

	mailer := newBreakerTestMailer(pool)
	for _, e := range emails {
		mailer.bounceEmails[e] = true // 100% bounce rate
	}

	w := newTestWorker(t, pool, mailer)
	w.batchSize = 99
	w.stats = NewCampaignStatsStore(pool)

	if _, err := w.claimAndDrain(context.Background()); err != nil {
		t.Fatalf("claimAndDrain: %v", err)
	}

	if status := campaignStatus(t, pool, campaignID); status != CampaignStatusSent {
		t.Errorf("campaign status = %q, want %q — 99 sent is below min_sample=100, so even a 100%% bounce rate must not trip", status, CampaignStatusSent)
	}
}

// TestWorker_CircuitBreaker_AtMinSample_CanTrip is the min_sample boundary's
// other side: sent=100 == min_sample=100 must be eligible to trip (same
// 100% bounce rate as the test above, only the sample size differs).
func TestWorker_CircuitBreaker_AtMinSample_CanTrip(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	setSetting(t, pool, SettingSendHealthMinSample, "100")
	setSetting(t, pool, SettingSendHealthBouncePct, "5.0")
	setSetting(t, pool, SettingSendHealthComplaintPct, "0.1")

	emails := seedActiveSubscribers(t, pool, 100)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	resetCampaignAuditRows(t, pool, campaignID)

	mailer := newBreakerTestMailer(pool)
	for _, e := range emails {
		mailer.bounceEmails[e] = true // 100% bounce rate
	}

	w := newTestWorker(t, pool, mailer)
	w.batchSize = 100
	w.stats = NewCampaignStatsStore(pool)

	if _, err := w.claimAndDrain(context.Background()); err != nil {
		t.Fatalf("claimAndDrain: %v", err)
	}

	if status := campaignStatus(t, pool, campaignID); status != CampaignStatusPausedDeliveryHealth {
		t.Errorf("campaign status = %q, want %q — 100 sent meets min_sample=100, so the breaker must be eligible to trip", status, CampaignStatusPausedDeliveryHealth)
	}
}

// ── Threshold boundary: complaint rate ──────────────────────────────────────

// TestWorker_CircuitBreaker_ComplaintRate_JustBelowAndAtThreshold covers
// both sides of the complaint-rate boundary in one test (default 0.1%
// needs a sample of at least 1000 to express as an integer count, so both
// cases share the same 1000-recipient audience, at min_sample=1000 to keep
// both batches in scope): sent=1000, complained=0 (0%) must not trip;
// complained=1 (0.1%) must.
func TestWorker_CircuitBreaker_ComplaintRate_JustBelowAndAtThreshold(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	setSetting(t, pool, SettingSendHealthMinSample, "1000")
	setSetting(t, pool, SettingSendHealthBouncePct, "100") // isolate: never trip on bounce rate in this test
	setSetting(t, pool, SettingSendHealthComplaintPct, "0.1")

	t.Run("below", func(t *testing.T) {
		emails := seedActiveSubscribers(t, pool, 1000)
		campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
		resetCampaignAuditRows(t, pool, campaignID)
		mailer := newBreakerTestMailer(pool)
		_ = emails // no complaints seeded: 0%

		w := newTestWorker(t, pool, mailer)
		w.batchSize = 1000
		w.stats = NewCampaignStatsStore(pool)

		if _, err := w.claimAndDrain(context.Background()); err != nil {
			t.Fatalf("claimAndDrain: %v", err)
		}
		if status := campaignStatus(t, pool, campaignID); status != CampaignStatusSent {
			t.Errorf("campaign status = %q, want %q (0%% complaint rate must not trip 0.1%%)", status, CampaignStatusSent)
		}
	})

	t.Run("at threshold", func(t *testing.T) {
		emails := seedActiveSubscribers(t, pool, 2000)
		campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
		resetCampaignAuditRows(t, pool, campaignID)
		mailer := newBreakerTestMailer(pool)
		mailer.complainEmails[emails[0]] = true // 1 of first 1000 = 0.1%

		w := newTestWorker(t, pool, mailer)
		w.batchSize = 1000 // two batches; only the first should run
		w.stats = NewCampaignStatsStore(pool)

		if _, err := w.claimAndDrain(context.Background()); err != nil {
			t.Fatalf("claimAndDrain: %v", err)
		}
		if status := campaignStatus(t, pool, campaignID); status != CampaignStatusPausedDeliveryHealth {
			t.Fatalf("campaign status = %q, want %q (0.1%% complaint rate must trip 0.1%%)", status, CampaignStatusPausedDeliveryHealth)
		}
		if got := len(mailer.Sent()); got != 1000 {
			t.Errorf("messages sent = %d, want exactly 1000 — the breaker must stop before the second batch", got)
		}
	})
}

// TestWorker_CircuitBreaker_NilStats_NeverTrips proves the documented
// nil-tolerance: a Worker with no CampaignStatsStore wired (matching every
// OTHER worker_test.go test in this package, which never sets w.stats)
// never evaluates the breaker at all, even at outrageous rates — the
// existing suite's implicit assumption ("the breaker doesn't interfere
// with ordinary sends") made explicit and checked.
func TestWorker_CircuitBreaker_NilStats_NeverTrips(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	setSetting(t, pool, SettingSendHealthMinSample, "1")
	setSetting(t, pool, SettingSendHealthBouncePct, "0.001") // as low as validSettingValue allows in production; this test bypasses that validator, but the point is "trips at anything"
	setSetting(t, pool, SettingSendHealthComplaintPct, "0.001")

	emails := seedActiveSubscribers(t, pool, 10)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	resetCampaignAuditRows(t, pool, campaignID)

	mailer := newBreakerTestMailer(pool)
	for _, e := range emails {
		mailer.bounceEmails[e] = true
	}

	w := newTestWorker(t, pool, mailer) // w.stats left nil, deliberately

	if _, err := w.claimAndDrain(context.Background()); err != nil {
		t.Fatalf("claimAndDrain: %v", err)
	}
	if status := campaignStatus(t, pool, campaignID); status != CampaignStatusSent {
		t.Errorf("campaign status = %q, want %q — a nil Stats dependency must disable the breaker, not crash or silently trip", status, CampaignStatusSent)
	}
}
