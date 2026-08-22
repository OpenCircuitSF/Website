package mailing

import (
	"context"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

func TestSendStore_ClaimStart_TransitionsScheduledToSending(t *testing.T) {
	pool := testPool(t)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	store := NewSendStore(pool, NewAudienceStore(pool), dbSettings{pool: pool}, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)

	c, err := store.ClaimStart(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("ClaimStart: %v", err)
	}
	if c == nil {
		t.Fatal("ClaimStart returned nil claimedCampaign for a due scheduled campaign")
	}
	if c.ID != campaignID {
		t.Errorf("ID = %d, want %d", c.ID, campaignID)
	}
	if got := campaignStatus(t, pool, campaignID); got != CampaignStatusSending {
		t.Errorf("status = %q, want %q", got, CampaignStatusSending)
	}

	// A second claim attempt must see nothing — the WHERE status='scheduled'
	// guard is the mutual exclusion.
	again, err := store.ClaimStart(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("second ClaimStart: %v", err)
	}
	if again != nil {
		t.Errorf("second ClaimStart returned %+v, want nil", again)
	}
}

func TestSendStore_ClaimResume_FindsSendingCampaign(t *testing.T) {
	pool := testPool(t)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)
	store := NewSendStore(pool, NewAudienceStore(pool), dbSettings{pool: pool}, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)

	c, err := store.ClaimResume(context.Background())
	if err != nil {
		t.Fatalf("ClaimResume: %v", err)
	}
	if c == nil || c.ID != campaignID {
		t.Fatalf("ClaimResume() = %+v, want campaign %d", c, campaignID)
	}
	if c.MaterializedAt == nil {
		t.Error("MaterializedAt = nil, want the stamp forceSending set")
	}
}

func TestSendStore_DemoteToDraft_OnlyOnce(t *testing.T) {
	pool := testPool(t)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	store := NewSendStore(pool, NewAudienceStore(pool), dbSettings{pool: pool}, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)

	demoted, err := store.DemoteToDraft(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("DemoteToDraft: %v", err)
	}
	if !demoted {
		t.Fatal("DemoteToDraft() = false, want true for a scheduled campaign")
	}
	if got := campaignStatus(t, pool, campaignID); got != CampaignStatusDraft {
		t.Errorf("status = %q, want draft", got)
	}

	again, err := store.DemoteToDraft(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("second DemoteToDraft: %v", err)
	}
	if again {
		t.Error("second DemoteToDraft() = true, want false — campaign is no longer 'scheduled'")
	}
}

// TestSendStore_OrphanSweep_ResetsOnlySendingRows forces a row to 'sending'
// via raw SQL — bypassing ClaimRow, so claimed_at stays NULL — and proves
// OrphanSweep still recovers it regardless of the staleAfter duration passed
// in: a 'sending' row with no claimed_at is always treated as orphaned
// (#0122's migration comment).
func TestSendStore_OrphanSweep_ResetsOnlySendingRows(t *testing.T) {
	pool := testPool(t)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)
	store := NewSendStore(pool, NewAudienceStore(pool), dbSettings{pool: pool}, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)

	stuckID, _, _ := seedQueuedSend(t, pool, campaignID)
	if _, err := pool.Exec(context.Background(), `UPDATE email_sends SET status = 'sending' WHERE id = $1`, stuckID); err != nil {
		t.Fatalf("force stuck row to sending: %v", err)
	}
	queuedID, _, _ := seedQueuedSend(t, pool, campaignID)
	sentID, _, _ := seedQueuedSend(t, pool, campaignID)
	if _, err := pool.Exec(context.Background(), `UPDATE email_sends SET status = 'sent' WHERE id = $1`, sentID); err != nil {
		t.Fatalf("force row to sent: %v", err)
	}

	// A zero staleAfter is the tightest possible cutoff (claimed_at < now())
	// — even so, the NULL-claimed_at row must still be swept.
	n, err := store.OrphanSweep(context.Background(), campaignID, 0)
	if err != nil {
		t.Fatalf("OrphanSweep: %v", err)
	}
	if n != 1 {
		t.Errorf("OrphanSweep() = %d, want 1", n)
	}
	if got := sendRowStatus(t, pool, stuckID); got != "queued" {
		t.Errorf("stuck row status = %q, want queued", got)
	}
	if got := sendRowStatus(t, pool, queuedID); got != "queued" {
		t.Errorf("already-queued row status = %q, want queued (untouched)", got)
	}
	if got := sendRowStatus(t, pool, sentID); got != "sent" {
		t.Errorf("sent row status = %q, want sent (untouched)", got)
	}
}

// TestSendStore_OrphanSweep_LeavesFreshlyClaimedRowAlone is the store-level
// half of #0122's proof: a row claimed through ClaimRow (so claimed_at is
// stamped) must NOT be reset while staleAfter is still wide enough to cover
// claimed_at — the exact condition a live worker's in-flight row satisfies —
// but MUST be reset once staleAfter has narrowed past claimed_at, exactly as
// it would once orphanStaleAfter's window has elapsed for a genuinely
// abandoned row. staleAfter is a duration handed straight to Postgres
// (`claimed_at < now() - $2::interval`, worker_store.go's OrphanSweep), not
// a Go-computed timestamp — see that method's doc comment (#0122, review
// pass 2) for why the cutoff must be computed by one clock, not two.
func TestSendStore_OrphanSweep_LeavesFreshlyClaimedRowAlone(t *testing.T) {
	pool := testPool(t)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)
	store := NewSendStore(pool, NewAudienceStore(pool), dbSettings{pool: pool}, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)

	sendID, _, _ := seedQueuedSend(t, pool, campaignID)
	_, claimed, err := store.ClaimRow(context.Background(), sendID)
	if err != nil || !claimed {
		t.Fatalf("ClaimRow: claimed=%v err=%v", claimed, err)
	}

	// A wide staleAfter (cutoff an hour before Postgres's now()): the row,
	// claimed moments ago, must survive.
	n, err := store.OrphanSweep(context.Background(), campaignID, time.Hour)
	if err != nil {
		t.Fatalf("OrphanSweep (live claim): %v", err)
	}
	if n != 0 {
		t.Fatalf("OrphanSweep() = %d, want 0 — must not un-claim a fresh row", n)
	}
	if got := sendRowStatus(t, pool, sendID); got != "sending" {
		t.Fatalf("row status = %q, want sending (untouched)", got)
	}

	// A negative staleAfter (cutoff an hour AFTER Postgres's now()): the row
	// must now be recovered, simulating orphanStaleAfter's window having
	// elapsed for a row a crashed worker really did abandon.
	n, err = store.OrphanSweep(context.Background(), campaignID, -time.Hour)
	if err != nil {
		t.Fatalf("OrphanSweep (stale claim): %v", err)
	}
	if n != 1 {
		t.Fatalf("OrphanSweep() = %d, want 1 — a stale claim must be recovered", n)
	}
	if got := sendRowStatus(t, pool, sendID); got != "queued" {
		t.Fatalf("row status = %q, want queued", got)
	}
}

func TestSendStore_ClaimRow_LeavesQueuedExactlyOnce(t *testing.T) {
	pool := testPool(t)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)
	store := NewSendStore(pool, NewAudienceStore(pool), dbSettings{pool: pool}, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)
	sendID, _, _ := seedQueuedSend(t, pool, campaignID)

	attempts, claimed, err := store.ClaimRow(context.Background(), sendID)
	if err != nil {
		t.Fatalf("ClaimRow: %v", err)
	}
	if !claimed || attempts != 1 {
		t.Fatalf("ClaimRow() = (%d, %v), want (1, true)", attempts, claimed)
	}

	_, claimedAgain, err := store.ClaimRow(context.Background(), sendID)
	if err != nil {
		t.Fatalf("second ClaimRow: %v", err)
	}
	if claimedAgain {
		t.Error("second ClaimRow() claimed=true, want false — row is no longer 'queued'")
	}
}

func TestSendStore_MarkSentAndMarkFailedRow(t *testing.T) {
	pool := testPool(t)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)
	store := NewSendStore(pool, NewAudienceStore(pool), dbSettings{pool: pool}, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)

	sentID, _, _ := seedQueuedSend(t, pool, campaignID)
	if _, _, err := store.ClaimRow(context.Background(), sentID); err != nil {
		t.Fatalf("claim sentID: %v", err)
	}
	ok, err := store.MarkSent(context.Background(), sentID, "ses-message-1")
	if err != nil || !ok {
		t.Fatalf("MarkSent: ok=%v err=%v", ok, err)
	}
	if got := sendRowStatus(t, pool, sentID); got != "sent" {
		t.Errorf("status = %q, want sent", got)
	}

	failedID, _, _ := seedQueuedSend(t, pool, campaignID)
	if _, _, err := store.ClaimRow(context.Background(), failedID); err != nil {
		t.Fatalf("claim failedID: %v", err)
	}
	ok, err = store.MarkFailedRow(context.Background(), failedID, "rejected")
	if err != nil || !ok {
		t.Fatalf("MarkFailedRow: ok=%v err=%v", ok, err)
	}
	if got := sendRowStatus(t, pool, failedID); got != "failed" {
		t.Errorf("status = %q, want failed", got)
	}
}

func TestSendStore_MarkRetryOrFailed_FailsAtThreeAttempts(t *testing.T) {
	pool := testPool(t)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)
	store := NewSendStore(pool, NewAudienceStore(pool), dbSettings{pool: pool}, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)
	sendID, _, _ := seedQueuedSend(t, pool, campaignID)

	for i := 1; i <= 2; i++ {
		if _, _, err := store.ClaimRow(context.Background(), sendID); err != nil {
			t.Fatalf("ClaimRow attempt %d: %v", i, err)
		}
		if _, err := store.MarkRetryOrFailed(context.Background(), sendID, "throttled"); err != nil {
			t.Fatalf("MarkRetryOrFailed attempt %d: %v", i, err)
		}
		if got := sendRowStatus(t, pool, sendID); got != "queued" {
			t.Fatalf("after attempt %d: status = %q, want queued", i, got)
		}
	}

	// Third attempt: attempts reaches 3, row becomes failed.
	attempts, _, err := store.ClaimRow(context.Background(), sendID)
	if err != nil {
		t.Fatalf("ClaimRow attempt 3: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if _, err := store.MarkRetryOrFailed(context.Background(), sendID, "throttled"); err != nil {
		t.Fatalf("MarkRetryOrFailed attempt 3: %v", err)
	}
	if got := sendRowStatus(t, pool, sendID); got != "failed" {
		t.Errorf("after attempt 3: status = %q, want failed", got)
	}
}

func TestSendStore_CompleteIfDone(t *testing.T) {
	pool := testPool(t)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)
	store := NewSendStore(pool, NewAudienceStore(pool), dbSettings{pool: pool}, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)

	queuedID, _, _ := seedQueuedSend(t, pool, campaignID)

	done, err := store.CompleteIfDone(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("CompleteIfDone: %v", err)
	}
	if done {
		t.Fatal("CompleteIfDone() = true while a row is still queued")
	}

	if _, err := pool.Exec(context.Background(), `UPDATE email_sends SET status = 'sent' WHERE id = $1`, queuedID); err != nil {
		t.Fatalf("mark queued row sent: %v", err)
	}
	done, err = store.CompleteIfDone(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("CompleteIfDone after all sent: %v", err)
	}
	if !done {
		t.Fatal("CompleteIfDone() = false once nothing is queued/sending")
	}
	if got := campaignStatus(t, pool, campaignID); got != CampaignStatusSent {
		t.Errorf("status = %q, want sent", got)
	}
}

func TestSendStore_ClaimBatch_GroupsSkipsByReason(t *testing.T) {
	pool := testPool(t)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)
	audienceStore := NewAudienceStore(pool)
	store := NewSendStore(pool, audienceStore, dbSettings{pool: pool}, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)

	eligibleID, _, eligibleEmail := seedQueuedSend(t, pool, campaignID)

	unsubID, unsubSubID, _ := seedQueuedSend(t, pool, campaignID)
	if _, err := pool.Exec(context.Background(), `UPDATE subscribers SET status = 'unsubscribed' WHERE id = $1`, unsubSubID); err != nil {
		t.Fatalf("unsubscribe seeded subscriber: %v", err)
	}

	suppressedID, _, suppressedEmail := seedQueuedSend(t, pool, campaignID)
	seedSuppression(t, pool, suppressedEmail, subscribers.SuppressionReasonHardBounce)

	recipients, err := store.ClaimBatch(context.Background(), campaignID, 10)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(recipients) != 1 || recipients[0].SendID != eligibleID || recipients[0].Email != eligibleEmail {
		t.Fatalf("ClaimBatch() recipients = %+v, want exactly [%d]", recipients, eligibleID)
	}

	if got := sendRowStatus(t, pool, unsubID); got != "skipped" {
		t.Errorf("unsubscribed row status = %q, want skipped", got)
	}
	if got := sendRowStatus(t, pool, suppressedID); got != "skipped" {
		t.Errorf("suppressed row status = %q, want skipped", got)
	}

	var unsubReason, suppressedReason string
	if err := pool.QueryRow(context.Background(), `SELECT error FROM email_sends WHERE id = $1`, unsubID).Scan(&unsubReason); err != nil {
		t.Fatalf("read unsub reason: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT error FROM email_sends WHERE id = $1`, suppressedID).Scan(&suppressedReason); err != nil {
		t.Fatalf("read suppressed reason: %v", err)
	}
	if unsubReason == suppressedReason {
		t.Errorf("both skip reasons are %q — ClaimBatch collapsed two distinct reasons to one, losing #0049's detail", unsubReason)
	}
	if unsubReason == "" || suppressedReason == "" {
		t.Errorf("skip reasons must be non-empty: unsub=%q suppressed=%q", unsubReason, suppressedReason)
	}
}

func TestSendStore_GatherPreflight_ReadsCampaignAndSettings(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	campaignID := seedScheduledCampaign(t, pool, "My subject", "My body", Audience{Mode: AudienceAll})
	store := NewSendStore(pool, NewAudienceStore(pool), dbSettings{pool: pool}, nil, testWorkerBaseURL, testWorkerListDomain, testWorkerReplyTo)

	in, err := store.GatherPreflight(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("GatherPreflight: %v", err)
	}
	if in.Subject != "My subject" {
		t.Errorf("Subject = %q, want %q", in.Subject, "My subject")
	}
	if in.TestSentAt == nil {
		t.Error("TestSentAt = nil, want the stamp seedScheduledCampaign set")
	}
	if in.PhysicalAddress == "" {
		t.Error("PhysicalAddress is empty, want the fixture's value")
	}
	if in.AudienceCount != 0 {
		// No subscribers seeded for this campaign's audience.
		t.Errorf("AudienceCount = %d, want 0 (no subscribers seeded)", in.AudienceCount)
	}
	if !Preflight(in).OK() {
		// AudienceCount=0 (empty_audience) is the only expected failure
		// here — nobody was seeded into the audience.
		codes := Preflight(in).Codes()
		if len(codes) != 1 || codes[0] != PreflightCodeEmptyAudience {
			t.Errorf("Preflight(GatherPreflight()) = %v, want exactly [empty_audience]", codes)
		}
	}
}
