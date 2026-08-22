package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// adminCampaignStatsMux wires the real GET /admin/campaigns/{id}/stats route
// guarded by RequireSession then RequireAdmin, backed by a real
// *mailing.CampaignStatsStore/*mailing.CampaignStore pair — mirrors
// adminCampaignPreflightMux (admin_campaign_preflight_test.go).
func adminCampaignStatsMux(pool *pgxpool.Pool) http.Handler {
	authStore := auth.NewStore(pool)
	campaignStore := mailing.NewCampaignStore(pool)
	statsStore := mailing.NewCampaignStatsStore(pool)
	h := NewAdminCampaignStatsHandler(statsStore, campaignStore)
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/campaigns/{id}/stats", requireAdmin(http.HandlerFunc(h.Stats)))
	return mux
}

type decodedCampaignStats struct {
	CampaignID int64  `json:"campaign_id"`
	Status     string `json:"status"`
	Counts     struct {
		Queued     int64 `json:"queued"`
		Sending    int64 `json:"sending"`
		Sent       int64 `json:"sent"`
		Failed     int64 `json:"failed"`
		Bounced    int64 `json:"bounced"`
		Complained int64 `json:"complained"`
		Skipped    int64 `json:"skipped"`
	} `json:"counts"`
	Reconciled struct {
		Bounced    int64 `json:"bounced"`
		Complained int64 `json:"complained"`
	} `json:"reconciled"`
	FailedSends []struct {
		ID       int64  `json:"id"`
		Email    string `json:"email"`
		Error    string `json:"error"`
		Attempts int    `json:"attempts"`
	} `json:"failed_sends"`
}

// seedStatsCampaign creates a throwaway draft campaign for these tests,
// following CLAUDE.md §8b: never a literal/hardcoded id.
func seedStatsCampaign(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)
	return c.ID
}

// seedStatsSubscriber seeds one throwaway active subscriber and returns its
// id and normalized email — one call per email_sends row this file seeds,
// since (campaign_id, subscriber_id) is UNIQUE (migration 000014).
func seedStatsSubscriber(t *testing.T, pool *pgxpool.Pool, tag string) (id int64, email string) {
	t.Helper()
	email = fmt.Sprintf("zz-campstats-%s-%d@example.com", tag, time.Now().UnixNano())
	id = seedSubscriberRow(t, pool, email, subscribers.StatusActive, nil, nil)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, id) })
	return id, email
}

// seedEmailSendRow inserts one email_sends row directly at the given status,
// bypassing Materialize/the worker's claim path — this file's tests are
// about the stats read, not the send pipeline. sesMessageID/errMsg may be
// nil.
func seedEmailSendRow(t *testing.T, pool *pgxpool.Pool, campaignID, subscriberID int64, email, status string, sesMessageID, errMsg *string, attempts int) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO email_sends (campaign_id, subscriber_id, email, status, ses_message_id, error, attempts)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		campaignID, subscriberID, email, status, sesMessageID, errMsg, attempts,
	).Scan(&id); err != nil {
		t.Fatalf("seed email_sends row (status=%s): %v", status, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM email_sends WHERE id = $1`, id) })
	return id
}

// seedEmailEventRow inserts one email_events row directly, mirroring
// admin_subscribers_test.go's TestAdminSubscribers_Get_IncludesSoftBounceCount
// fixture shape.
func seedEmailEventRow(t *testing.T, pool *pgxpool.Pool, snsMessageID, sesMessageID, eventType, recipient string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO email_events (sns_message_id, ses_message_id, event_type, recipient, payload)
		 VALUES ($1, $2, $3, lower(trim($4)), '{}'::jsonb)`,
		snsMessageID, sesMessageID, eventType, recipient,
	); err != nil {
		t.Fatalf("seed email_events row (event_type=%s): %v", eventType, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM email_events WHERE sns_message_id = $1`, snsMessageID)
	})
}

// TestAdminCampaignStats_BucketsAllSevenStatuses seeds one email_sends row
// per status migration 000018's CHECK allows and proves every one is
// counted — including 'sending' (the seventh status, #0045/#0122) and
// 'skipped' as its own bucket distinct from 'failed' (carried in from
// #0044's plan: an unsubscribe/suppression between materialization and send
// is not a delivery failure).
func TestAdminCampaignStats_BucketsAllSevenStatuses(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignStatsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-stats-buckets@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-stats-buckets")

	campaignID := seedStatsCampaign(t, pool)

	statuses := []string{"queued", "sending", "sent", "failed", "bounced", "complained", "skipped"}
	for _, status := range statuses {
		subID, email := seedStatsSubscriber(t, pool, status)
		seedEmailSendRow(t, pool, campaignID, subID, email, status, nil, nil, 0)
	}

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/%d/stats", srv.URL, campaignID), "admin-token-campaign-stats-buckets", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out decodedCampaignStats
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]int64{
		"queued": 1, "sending": 1, "sent": 1, "failed": 1, "bounced": 1, "complained": 1, "skipped": 1,
	}
	got := map[string]int64{
		"queued": out.Counts.Queued, "sending": out.Counts.Sending, "sent": out.Counts.Sent,
		"failed": out.Counts.Failed, "bounced": out.Counts.Bounced, "complained": out.Counts.Complained,
		"skipped": out.Counts.Skipped,
	}
	for status, wantN := range want {
		if got[status] != wantN {
			t.Errorf("counts[%s] = %d, want %d (full counts = %+v)", status, got[status], wantN, out.Counts)
		}
	}
	if out.CampaignID != campaignID {
		t.Errorf("campaign_id = %d, want %d", out.CampaignID, campaignID)
	}
	if out.Status != mailing.CampaignStatusDraft {
		t.Errorf("status = %q, want %q", out.Status, mailing.CampaignStatusDraft)
	}
}

// TestAdminCampaignStats_ReconcilesBounceAndComplaintFromEvents is the load-
// bearing proof for this issue: internal/mailing.CampaignStatsStore's
// package doc comment explains that NOTHING in this codebase ever writes
// email_sends.status='bounced'/'complained' — only email_events (#0038)
// records those, joined back by ses_message_id. This test seeds two 'sent'
// rows and attaches a Bounce event to one and a Complaint event to the
// other, and proves `reconciled` reports them while the raw `counts` bucket
// (which nothing here ever flips) stays zero — demonstrating the
// reconciliation is real, not a passthrough of a column nothing sets.
func TestAdminCampaignStats_ReconcilesBounceAndComplaintFromEvents(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignStatsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-stats-reconcile@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-stats-reconcile")

	campaignID := seedStatsCampaign(t, pool)

	bouncedSubID, bouncedEmail := seedStatsSubscriber(t, pool, "bounced")
	complainedSubID, complainedEmail := seedStatsSubscriber(t, pool, "complained")
	cleanSubID, cleanEmail := seedStatsSubscriber(t, pool, "clean")

	bouncedMsgID := fmt.Sprintf("zz-campstats-msg-bounced-%d", time.Now().UnixNano())
	complainedMsgID := fmt.Sprintf("zz-campstats-msg-complained-%d", time.Now().UnixNano())
	cleanMsgID := fmt.Sprintf("zz-campstats-msg-clean-%d", time.Now().UnixNano())

	seedEmailSendRow(t, pool, campaignID, bouncedSubID, bouncedEmail, mailing.CampaignStatusSent, &bouncedMsgID, nil, 1)
	seedEmailSendRow(t, pool, campaignID, complainedSubID, complainedEmail, mailing.CampaignStatusSent, &complainedMsgID, nil, 1)
	seedEmailSendRow(t, pool, campaignID, cleanSubID, cleanEmail, mailing.CampaignStatusSent, &cleanMsgID, nil, 1)

	// Two Bounce events for the SAME ses_message_id — proves EventCounts
	// counts DISTINCT email_sends rows, not raw event rows (a recipient
	// bounced twice must still read as one affected recipient).
	seedEmailEventRow(t, pool, fmt.Sprintf("zz-campstats-sns-bounce-1-%d", time.Now().UnixNano()), bouncedMsgID, "Bounce", bouncedEmail)
	seedEmailEventRow(t, pool, fmt.Sprintf("zz-campstats-sns-bounce-2-%d", time.Now().UnixNano()), bouncedMsgID, "Bounce", bouncedEmail)
	seedEmailEventRow(t, pool, fmt.Sprintf("zz-campstats-sns-complaint-%d", time.Now().UnixNano()), complainedMsgID, "Complaint", complainedEmail)

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/%d/stats", srv.URL, campaignID), "admin-token-campaign-stats-reconcile", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out decodedCampaignStats
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if out.Counts.Sent != 3 {
		t.Errorf("counts.sent = %d, want 3", out.Counts.Sent)
	}
	// The raw bucket: nothing wrote 'bounced'/'complained' onto email_sends
	// directly, so it must read zero even though real events exist.
	if out.Counts.Bounced != 0 || out.Counts.Complained != 0 {
		t.Errorf("counts.bounced/complained = %d/%d, want 0/0 (nothing writes these email_sends statuses directly)",
			out.Counts.Bounced, out.Counts.Complained)
	}
	// The reconciled figures: joined from email_events, DISTINCT per send row.
	if out.Reconciled.Bounced != 1 {
		t.Errorf("reconciled.bounced = %d, want 1 (deduplicated across two Bounce events on the same message)", out.Reconciled.Bounced)
	}
	if out.Reconciled.Complained != 1 {
		t.Errorf("reconciled.complained = %d, want 1", out.Reconciled.Complained)
	}
}

// TestAdminCampaignStats_FailedSendsIncludeErrorMessages proves the "failed
// sends listable with their error messages" acceptance criterion end to end.
func TestAdminCampaignStats_FailedSendsIncludeErrorMessages(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignStatsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-stats-failed@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-stats-failed")

	campaignID := seedStatsCampaign(t, pool)

	sub1ID, email1 := seedStatsSubscriber(t, pool, "failed1")
	sub2ID, email2 := seedStatsSubscriber(t, pool, "failed2")
	err1 := "AWS.SimpleEmailService.MessageRejected: bad address"
	err2 := "context deadline exceeded"
	seedEmailSendRow(t, pool, campaignID, sub1ID, email1, mailing.CampaignStatusFailed, nil, &err1, 3)
	seedEmailSendRow(t, pool, campaignID, sub2ID, email2, mailing.CampaignStatusFailed, nil, &err2, 1)
	// One 'sent' row present too, to prove FailedSends filters by status
	// rather than returning every row.
	sub3ID, email3 := seedStatsSubscriber(t, pool, "sentnotfailed")
	seedEmailSendRow(t, pool, campaignID, sub3ID, email3, mailing.CampaignStatusSent, nil, nil, 1)

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/%d/stats", srv.URL, campaignID), "admin-token-campaign-stats-failed", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out decodedCampaignStats
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.FailedSends) != 2 {
		t.Fatalf("len(failed_sends) = %d, want 2 (full = %+v)", len(out.FailedSends), out.FailedSends)
	}
	byEmail := map[string]string{}
	for _, f := range out.FailedSends {
		byEmail[f.Email] = f.Error
	}
	if byEmail[email1] != err1 {
		t.Errorf("failed_sends[%s].error = %q, want %q", email1, byEmail[email1], err1)
	}
	if byEmail[email2] != err2 {
		t.Errorf("failed_sends[%s].error = %q, want %q", email2, byEmail[email2], err2)
	}
}

// TestAdminCampaignStats_UnknownCampaign_404 proves an unknown campaign id
// 404s rather than reporting a zero-value stats payload for it.
func TestAdminCampaignStats_UnknownCampaign_404(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignStatsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-stats-404@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-stats-404")

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/999999999/stats", srv.URL), "admin-token-campaign-stats-404", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}
