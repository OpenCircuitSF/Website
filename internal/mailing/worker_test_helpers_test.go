// worker_test_helpers_test.go: shared fixtures for worker_store_test.go and
// worker_test.go, following campaigns_test.go/audience_test.go's own
// conventions (CLAUDE.md §8b: every test seeds its own throwaway rows, never
// targets a literal or seeded id).
package mailing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// errTestSettingNotFound mirrors auth.ErrSettingNotFound's role for
// dbSettings below. internal/mailing cannot import internal/auth — auth
// already imports mailing (auth.SESMailer wraps mailing.Mailer) — so this
// package's tests carry their own tiny settings reader rather than the real
// one.
var errTestSettingNotFound = errors.New("mailing_test: setting not found")

// dbSettings is a minimal SettingsReader backed directly by the settings
// table — a test-only twin of auth.Store.GetSetting's own query.
type dbSettings struct{ pool *pgxpool.Pool }

func (d dbSettings) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := d.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&value)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", errTestSettingNotFound
	case err != nil:
		return "", err
	}
	return value, nil
}

// setSetting upserts key=value, restoring whatever was there before (or
// deleting the row if it didn't exist) at test cleanup. physical_address,
// default_from_name, and max_send_rate are all GLOBAL rows seeded by
// migration — mutating them without restoring would leak into whichever
// test runs next, since this package's tests do not run in parallel.
func setSetting(t *testing.T, pool *pgxpool.Pool, key, value string) {
	t.Helper()
	ctx := context.Background()
	var old string
	err := pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&old)
	had := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("read setting %q: %v", key, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		key, value,
	); err != nil {
		t.Fatalf("set setting %q: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_, _ = pool.Exec(context.Background(), `UPDATE settings SET value = $2 WHERE key = $1`, key, old)
		} else {
			_, _ = pool.Exec(context.Background(), `DELETE FROM settings WHERE key = $1`, key)
		}
	})
}

// workerTestFixture bundles the settings a worker test needs to actually
// send mail: a non-blank physical_address (CAN-SPAM), a non-blank reply-to
// wired through WorkerDeps, and EMAIL_LIST_DOMAIN equivalent (ListDomain) —
// plus a fast max_send_rate so the rate limiter never makes a test wait.
func workerTestFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	setSetting(t, pool, settingPhysicalAddress, "123 Main St, San Francisco, CA 94103")
	setSetting(t, pool, settingDefaultFromName, "")
	setSetting(t, pool, settingMaxSendRate, "100000")
}

// baseURL/listDomain are fixed test constants shared by every worker test.
const (
	testWorkerBaseURL    = "https://www.example-oc-test.com"
	testWorkerListDomain = "lists.example-oc-test.com"
	testWorkerReplyTo    = "hello@example-oc-test.com"
	testWorkerFromAddr   = "hello@example-oc-test.com"
)

// newTestWorker constructs a Worker wired to pool/mailer/progress with fast
// defaults (no real waiting), and its own SendStore.
func newTestWorker(t *testing.T, pool *pgxpool.Pool, mailer Mailer) *Worker {
	t.Helper()
	audienceStore := NewAudienceStore(pool)
	settings := dbSettings{pool: pool}
	sendStore := NewSendStore(pool, audienceStore, settings, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)
	w, err := NewWorker(WorkerDeps{
		Store:          sendStore,
		Audience:       audienceStore,
		Audit:          nil,
		Mailer:         mailer,
		Settings:       settings,
		BaseURL:        testWorkerBaseURL,
		ListDomain:     testWorkerListDomain,
		FromAddr:       testWorkerFromAddr,
		ReplyTo:        testWorkerReplyTo,
		EnvMaxSendRate: 100000,
		BatchSize:      50,
		PollInterval:   time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	return w
}

// seedScheduledCampaign creates a campaign, seeds interests as needed, and
// schedules it (status='scheduled', scheduled_at in the past so the worker's
// claim predicate matches immediately). test_sent_at is stamped so the
// no_test_send gate passes by default — tests that specifically exercise
// that gate override it.
func seedScheduledCampaign(t *testing.T, pool *pgxpool.Pool, subject, bodyMD string, aud Audience) int64 {
	t.Helper()
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: subject, BodyMD: bodyMD,
		AudienceMode: aud.Mode, InterestIDs: aud.InterestIDs,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET status = 'scheduled', scheduled_at = now() - interval '1 minute', test_sent_at = now() WHERE id = $1`,
		c.ID,
	); err != nil {
		t.Fatalf("schedule campaign %d: %v", c.ID, err)
	}
	return c.ID
}

// forceSending puts a campaign directly into 'sending' with
// materialized_at already stamped — bypassing the worker's own claim path,
// for tests that want to drive drainLoop directly against a campaign that's
// already past materialization (the two-worker and abort/resume tests).
func forceSending(t *testing.T, pool *pgxpool.Pool, campaignID int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET status = 'sending', started_at = now(), materialized_at = now() WHERE id = $1`,
		campaignID,
	); err != nil {
		t.Fatalf("force campaign %d to sending: %v", campaignID, err)
	}
}

// seedQueuedSend inserts one email_sends row directly at status='queued' for
// an active, non-suppressed subscriber — the shape Materialize itself would
// produce, without going through the audience predicate. Returns the send
// id, subscriber id, and email.
func seedQueuedSend(t *testing.T, pool *pgxpool.Pool, campaignID int64) (sendID, subscriberID int64, email string) {
	t.Helper()
	subscriberID = seedSubscriber(t, pool, subscribers.StatusActive)
	var e string
	if err := pool.QueryRow(context.Background(),
		`SELECT email FROM subscribers WHERE id = $1`, subscriberID,
	).Scan(&e); err != nil {
		t.Fatalf("read seeded subscriber email: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO email_sends (campaign_id, subscriber_id, email) VALUES ($1, $2, $3) RETURNING id`,
		campaignID, subscriberID, e,
	).Scan(&sendID); err != nil {
		t.Fatalf("seed queued send: %v", err)
	}
	return sendID, subscriberID, e
}

// countSendStatuses returns how many email_sends rows for campaignID carry
// each status, for assertions that want the full breakdown at once.
func countSendStatuses(t *testing.T, pool *pgxpool.Pool, campaignID int64) map[string]int {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT status, count(*) FROM email_sends WHERE campaign_id = $1 GROUP BY status`, campaignID)
	if err != nil {
		t.Fatalf("count send statuses: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			t.Fatalf("scan send status count: %v", err)
		}
		out[status] = n
	}
	return out
}

// campaignStatus reads a campaign's current status.
func campaignStatus(t *testing.T, pool *pgxpool.Pool, campaignID int64) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM email_campaigns WHERE id = $1`, campaignID,
	).Scan(&status); err != nil {
		t.Fatalf("read campaign %d status: %v", campaignID, err)
	}
	return status
}

// campaignArchiveState reads a campaign's archive_status and whether
// archived_at is set (#0123's CompleteIfDone same-transaction stamp).
func campaignArchiveState(t *testing.T, pool *pgxpool.Pool, campaignID int64) (archiveStatus string, hasArchivedAt bool) {
	t.Helper()
	var archivedAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT archive_status, archived_at FROM email_campaigns WHERE id = $1`, campaignID,
	).Scan(&archiveStatus, &archivedAt); err != nil {
		t.Fatalf("read campaign %d archive state: %v", campaignID, err)
	}
	return archiveStatus, archivedAt != nil
}

// sendRowStatus reads one email_sends row's status.
func sendRowStatus(t *testing.T, pool *pgxpool.Pool, sendID int64) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM email_sends WHERE id = $1`, sendID,
	).Scan(&status); err != nil {
		t.Fatalf("read send %d status: %v", sendID, err)
	}
	return status
}

// countMessagesTo counts how many times an address appears in msgs' To
// field — the duplicate-detection primitive every no-double-send test uses.
func countMessagesTo(msgs []Message, to string) int {
	n := 0
	for _, m := range msgs {
		if m.To == to {
			n++
		}
	}
	return n
}

// abortAfterNSends returns a sendPostCrashHook that aborts exactly once, the
// Nth time it is called (1-indexed), and is a no-op every other time. Safe
// for concurrent use (not that the crash tests need it to be, but the
// two-worker test's hooks slot is shared package state).
func abortAfterNSends(n int) func(sendID int64) bool {
	var mu sync.Mutex
	count := 0
	return func(int64) bool {
		mu.Lock()
		defer mu.Unlock()
		count++
		return count == n
	}
}

// forceOrphansStale makes OrphanSweep treat every 'sending' row as stale
// regardless of when it was actually claimed, for the rest of the calling
// test, by shrinking the package-level orphanStaleAfter var to a negative
// duration — restored via t.Cleanup, the same pattern setSetting above uses,
// and safe for the same reason: this package's tests never run in parallel.
//
// This replaces an earlier version of the fix that shifted a per-Worker
// injectable clock (w.now) forward instead. That worked but compared the Go
// process's clock against claimed_at, which Postgres stamps with ITS OWN
// clock (#0122, review pass 2) — a dependency nothing enforced. OrphanSweep
// now computes its cutoff entirely in SQL (`claimed_at < now() -
// $2::interval`), so there is no Go-side "now" left to inject; shrinking the
// duration handed to that statement is the equivalent lever. A negative
// duration makes the cutoff run PAST Postgres's own now(), which is stale
// enough to catch a claim stamped microseconds ago — simulating a resume
// that happens well after orphanStaleAfter's real window has elapsed,
// without a test ever sleeping that long.
func forceOrphansStale(t *testing.T) {
	t.Helper()
	orig := orphanStaleAfter
	orphanStaleAfter = -time.Hour
	t.Cleanup(func() { orphanStaleAfter = orig })
}

// resetCampaignAuditRows deletes any audit_log rows already present for
// campaignID (audit_log is never truncated by this package's TestMain — it
// is a shared, append-only table other packages' tests also write to — and
// email_campaigns.id is reset to 1 by RESTART IDENTITY each TestMain run,
// so a fresh campaign can collide with a stale row left by an EARLIER run
// of this same test binary within one session) and schedules the same
// cleanup at test end. Call this before asserting an exact audit row count
// for a campaign id.
func resetCampaignAuditRows(t *testing.T, pool *pgxpool.Pool, campaignID int64) {
	t.Helper()
	clean := func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE target_type = 'email_campaign' AND target_id = $1`, campaignID)
	}
	clean()
	t.Cleanup(clean)
}

func uniqueMailingEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-mailing-worker-%d@example.com", testdb.Unique())
}
