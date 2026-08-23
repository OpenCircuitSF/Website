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
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// adminCampaignPreflightMux wires the real GET /admin/campaigns/{id}/preflight
// route guarded by RequireSession then RequireAdmin, backed by a real
// *mailing.SendStore/*mailing.CampaignStore pair — mirrors
// adminCampaignsMux (admin_campaigns_test.go).
func adminCampaignPreflightMux(pool *pgxpool.Pool, listDomain, replyTo, fromAddr string) http.Handler {
	authStore := auth.NewStore(pool)
	campaignStore := mailing.NewCampaignStore(pool)
	audienceStore := mailing.NewAudienceStore(pool)
	sendStore := mailing.NewSendStore(pool, audienceStore, authStore, nil, "https://www.example-oc-test.com", listDomain, replyTo)
	h := NewAdminCampaignPreflightHandler(sendStore, campaignStore, authStore, fromAddr)
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/campaigns/{id}/preflight", requireAdmin(http.HandlerFunc(h.Preflight)))
	return mux
}

type decodedPreflight struct {
	OK      bool                       `json:"ok"`
	Unmet   []campaignPreflightFailure `json:"unmet"`
	Summary struct {
		Subject    string `json:"subject"`
		From       string `json:"from"`
		Recipients int64  `json:"recipients"`
	} `json:"summary"`
}

// TestAdminCampaignPreflight_UnmetCodesInPinnedOrder proves this endpoint
// evaluates the SAME mailing.Preflight function the send-transition handler
// and the worker use, in the exact pinned order internal/mailing/preflight.go
// documents — physical_address_missing must appear before no_test_send in
// the response, matching #0045's TestPreflight_FailureOrderIsStable.
func TestAdminCampaignPreflight_UnmetCodesInPinnedOrder(t *testing.T) {
	pool := adminSubscribersTestPool(t)

	// Set physical_address to '' explicitly rather than relying on the
	// migration 000008 seed still being in place — settingsTestPool's own
	// tests mutate this row and, absent an explicit t.Cleanup on their
	// part, could leave it non-empty for whichever test runs next under
	// -shuffle=on (#0121). Real, non-blank EMAIL_LIST_DOMAIN/EMAIL_REPLY_TO
	// so the only unmet requirements are the two this test names.
	setPhysicalAddressForTest(t, pool, "")
	srv := httptest.NewServer(adminCampaignPreflightMux(pool,
		"lists.example-oc-test.com", "hello@example-oc-test.com", "hello@example-oc-test.com"))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-preflight-order@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-preflight-order")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)
	// One active subscriber so empty_audience does not also fire — this
	// test isolates physical_address_missing and no_test_send, and their
	// order relative to each other.
	subID1 := seedSubscriberRow(t, pool, fmt.Sprintf("zz-preflightorder-%d@example.com", testdb.Unique()), subscribers.StatusActive, nil, nil)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, subID1) })

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/%d/preflight", srv.URL, c.ID), "admin-token-campaign-preflight-order", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out decodedPreflight
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.OK {
		t.Fatalf("ok = true, want false (physical_address/no_test_send unmet)")
	}
	var codes []string
	for _, f := range out.Unmet {
		codes = append(codes, f.Code)
	}
	addrIdx, testIdx := -1, -1
	for i, c := range codes {
		if c == mailing.PreflightCodePhysicalAddress {
			addrIdx = i
		}
		if c == mailing.PreflightCodeNoTestSend {
			testIdx = i
		}
	}
	if addrIdx == -1 || testIdx == -1 {
		t.Fatalf("codes = %v, want both physical_address_missing and no_test_send", codes)
	}
	if addrIdx >= testIdx {
		t.Errorf("codes = %v, want physical_address_missing before no_test_send (pinned order)", codes)
	}
	for _, code := range codes {
		if code == mailing.PreflightCodeEmptyAudience {
			t.Errorf("codes = %v, must not include empty_audience — a subscriber was seeded", codes)
		}
	}

	if out.Summary.Subject != "s" {
		t.Errorf("summary.subject = %q, want %q", out.Summary.Subject, "s")
	}
	if out.Summary.Recipients != 1 {
		t.Errorf("summary.recipients = %d, want 1", out.Summary.Recipients)
	}
}

// TestAdminCampaignPreflight_OKWhenNothingUnmet proves ok=true and an empty
// unmet list when every requirement is satisfied.
func TestAdminCampaignPreflight_OKWhenNothingUnmet(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	setPhysicalAddressForTest(t, pool, "123 Main St, San Francisco, CA 94103")

	srv := httptest.NewServer(adminCampaignPreflightMux(pool,
		"lists.example-oc-test.com", "hello@example-oc-test.com", "hello@example-oc-test.com"))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-preflight-ok@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-preflight-ok")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)
	subID2 := seedSubscriberRow(t, pool, fmt.Sprintf("zz-preflightok-%d@example.com", testdb.Unique()), subscribers.StatusActive, nil, nil)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, subID2) })
	if _, err := store.MarkTestSent(context.Background(), c.ID, time.Now()); err != nil {
		t.Fatalf("MarkTestSent: %v", err)
	}

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/%d/preflight", srv.URL, c.ID), "admin-token-campaign-preflight-ok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out decodedPreflight
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.OK {
		t.Errorf("ok = false, unmet = %+v, want true and empty", out.Unmet)
	}
	if len(out.Unmet) != 0 {
		t.Errorf("unmet = %+v, want empty", out.Unmet)
	}
}

// TestAdminCampaignPreflight_UnknownCampaign_404 proves an unknown campaign
// id 404s rather than evaluating Preflight against a zero-value input.
func TestAdminCampaignPreflight_UnknownCampaign_404(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignPreflightMux(pool,
		"lists.example-oc-test.com", "hello@example-oc-test.com", "hello@example-oc-test.com"))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-preflight-404@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-preflight-404")

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/999999999/preflight", srv.URL), "admin-token-campaign-preflight-404", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}

// TestAdminCampaignPreflight_NeverMutates proves this handler never
// transitions the campaign's status or writes an audit row — it is a
// read-only dry-run, matching #0044's decision for GET .../audience.
func TestAdminCampaignPreflight_NeverMutates(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignPreflightMux(pool,
		"lists.example-oc-test.com", "hello@example-oc-test.com", "hello@example-oc-test.com"))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-preflight-noop@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-preflight-noop")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/%d/preflight", srv.URL, c.ID), "admin-token-campaign-preflight-noop", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	got, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != mailing.CampaignStatusDraft {
		t.Errorf("status after GET .../preflight = %q, want unchanged %q", got.Status, mailing.CampaignStatusDraft)
	}
	actions := auditActionsForCampaign(t, pool, c.ID)
	for _, a := range actions {
		if a != "email_campaign.created" {
			t.Errorf("audit actions after GET .../preflight = %v, want no new row beyond creation", actions)
		}
	}
}

// setPhysicalAddressForTest writes the physical_address setting directly,
// mirroring how internal/handlers/settings_test.go's own helpers seed
// settings for a single test rather than going through the admin PATCH
// endpoint.
func setPhysicalAddressForTest(t *testing.T, pool *pgxpool.Pool, addr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
	defer cancel()
	if _, err := pool.Exec(ctx, `UPDATE settings SET value = $1 WHERE key = 'physical_address'`, addr); err != nil {
		t.Fatalf("seed physical_address: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `UPDATE settings SET value = '' WHERE key = 'physical_address'`)
	})
}
