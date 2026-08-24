package mailing

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// seedSentSend inserts one email_sends row directly at status='sent' with a
// unique ses_message_id, for a throwaway subscriber and campaign — the shape
// AccountComplaintRate reads, without going through the worker's claim path.
func seedSentSend(t *testing.T, pool *pgxpool.Pool, campaignID int64) (sendID int64, sesMessageID string) {
	t.Helper()
	subscriberID := seedSubscriber(t, pool, subscribers.StatusActive)
	sesMessageID = fmt.Sprintf("zz-complaintrate-%d", testdb.Unique())
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO email_sends (campaign_id, subscriber_id, email, status, ses_message_id)
		 VALUES ($1, $2, $3, 'sent', $4) RETURNING id`,
		campaignID, subscriberID, fmt.Sprintf("zz-complaintrate-%d@example.com", testdb.Unique()), sesMessageID,
	).Scan(&sendID); err != nil {
		t.Fatalf("seed sent send: %v", err)
	}
	return sendID, sesMessageID
}

// seedComplaintEvent inserts one email_events row of type Complaint linked to
// sesMessageID, and cleans it up at test end — email_events carries no FK to
// email_sends (joined only by ses_message_id), so it needs its own cleanup,
// unlike email_sends which cascades off email_campaigns.
func seedComplaintEvent(t *testing.T, pool *pgxpool.Pool, sesMessageID string) {
	t.Helper()
	snsMessageID := fmt.Sprintf("zz-complaintrate-sns-%d", testdb.Unique())
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO email_events (sns_message_id, ses_message_id, event_type, recipient, payload)
		 VALUES ($1, $2, 'Complaint', 'complainer@example.com', '{}'::jsonb)`,
		snsMessageID, sesMessageID,
	); err != nil {
		t.Fatalf("seed complaint event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM email_events WHERE sns_message_id = $1`, snsMessageID)
	})
}

// TestAccountComplaintRate_ReconcilesAcrossCampaignsAndCountsCleanSends is
// #0061's proof for the admin overview dashboard's account-wide complaint
// rate. Like every other test against this shared, never-truncated database
// (see internal/subscribers' testPool doc comment for why), this asserts a
// before/after DELTA around the rows this test itself seeds, never an
// absolute count.
func TestAccountComplaintRate_ReconcilesAcrossCampaignsAndCountsCleanSends(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	stats := NewCampaignStatsStore(pool)
	campaignStore := NewCampaignStore(pool)

	c, err := campaignStore.Create(ctx, CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	beforeComplained, beforeSent, err := stats.AccountComplaintRate(ctx)
	if err != nil {
		t.Fatalf("AccountComplaintRate (before): %v", err)
	}

	// Two clean sends (no event at all) — must each count toward `sent`
	// without needing a matching email_events row. This is the case a plain
	// INNER JOIN would silently drop (see AccountComplaintRate's doc
	// comment).
	seedSentSend(t, pool, c.ID)
	seedSentSend(t, pool, c.ID)

	// One send that generated a Complaint.
	_, msgID := seedSentSend(t, pool, c.ID)
	seedComplaintEvent(t, pool, msgID)

	afterComplained, afterSent, err := stats.AccountComplaintRate(ctx)
	if err != nil {
		t.Fatalf("AccountComplaintRate (after): %v", err)
	}

	if afterSent != beforeSent+3 {
		t.Errorf("sent = %d, want %d (before=%d + 3 clean/complained sends) — a clean send with no linked "+
			"event must still count; an INNER JOIN would drop it", afterSent, beforeSent+3, beforeSent)
	}
	if afterComplained != beforeComplained+1 {
		t.Errorf("complained = %d, want %d (before=%d + 1) — only the send with a Complaint event should count",
			afterComplained, beforeComplained+1, beforeComplained)
	}
}

// TestAccountComplaintRate_DuplicateEventsCountRecipientOnce proves the
// count(DISTINCT s.id) construction: a second Complaint notification for the
// SAME send (a receiving MTA reporting twice, or SES retrying its own
// notification) must not double-count that recipient — the dashboard's
// 0.3% threshold is defined against affected recipients, not raw
// notifications (mirroring CampaignEventCounts.EventCounts' own doc
// comment, which this method's construction deliberately copies).
func TestAccountComplaintRate_DuplicateEventsCountRecipientOnce(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	stats := NewCampaignStatsStore(pool)
	campaignStore := NewCampaignStore(pool)

	c, err := campaignStore.Create(ctx, CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	beforeComplained, _, err := stats.AccountComplaintRate(ctx)
	if err != nil {
		t.Fatalf("AccountComplaintRate (before): %v", err)
	}

	_, msgID := seedSentSend(t, pool, c.ID)
	seedComplaintEvent(t, pool, msgID)
	seedComplaintEvent(t, pool, msgID) // duplicate notification, same send

	afterComplained, _, err := stats.AccountComplaintRate(ctx)
	if err != nil {
		t.Fatalf("AccountComplaintRate (after): %v", err)
	}
	if afterComplained != beforeComplained+1 {
		t.Errorf("complained = %d, want %d (before=%d + 1) — two Complaint events for the same send must count once",
			afterComplained, beforeComplained+1, beforeComplained)
	}
}
