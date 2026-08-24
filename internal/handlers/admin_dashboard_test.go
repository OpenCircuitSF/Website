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
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// adminDashboardMux wires the real GET /admin/overview route guarded by
// RequireSession then RequireAdmin, backed by real stores — mirrors
// adminCampaignStatsMux (admin_campaign_stats_test.go). sesSandbox lets each
// test choose the manually-set flag AdminDashboardHandler.Overview surfaces
// verbatim as warnings.ses_sandbox_active.
func adminDashboardMux(pool *pgxpool.Pool, sesSandbox bool) http.Handler {
	authStore := auth.NewStore(pool)
	subsStore := subscribers.NewStore(pool)
	interestsStore := interests.NewStore(pool)
	campaignsStore := mailing.NewCampaignStore(pool)
	statsStore := mailing.NewCampaignStatsStore(pool)
	h := NewAdminDashboardHandler(subsStore, interestsStore, campaignsStore, statsStore, authStore, sesSandbox)
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/overview", requireAdmin(http.HandlerFunc(h.Overview)))
	return mux
}

type decodedDashboard struct {
	Subscribers struct {
		Counts struct {
			Pending      int64 `json:"pending"`
			Active       int64 `json:"active"`
			Unsubscribed int64 `json:"unsubscribed"`
			Bounced      int64 `json:"bounced"`
			Complained   int64 `json:"complained"`
		} `json:"counts"`
		Growth struct {
			Confirmed30d    int64 `json:"confirmed_30d"`
			Unsubscribed30d int64 `json:"unsubscribed_30d"`
			Net30d          int64 `json:"net_30d"`
		} `json:"growth_30d"`
	} `json:"subscribers"`
	Interests []struct {
		ID              int64  `json:"id"`
		Slug            string `json:"slug"`
		Name            string `json:"name"`
		SubscriberCount int64  `json:"subscriber_count"`
	} `json:"interests"`
	RecentCampaigns []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"recent_campaigns"`
	SendingCampaign *struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"sending_campaign"`
	Warnings struct {
		ComplaintRateHigh      bool     `json:"complaint_rate_high"`
		ComplaintRatePct       *float64 `json:"complaint_rate_pct"`
		ComplaintSampleSize    int64    `json:"complaint_sample_size"`
		PhysicalAddressUnset   bool     `json:"physical_address_unset"`
		SESSandboxActive       bool     `json:"ses_sandbox_active"`
		InboundMailUnavailable bool     `json:"inbound_mail_unavailable"`
	} `json:"warnings"`
}

func getDashboard(t *testing.T, client *http.Client, url, token string) decodedDashboard {
	t.Helper()
	resp := doJSON(t, client, "GET", url, token, "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/overview status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	var out decodedDashboard
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode dashboard: %v (body=%s)", err, body)
	}
	return out
}

// TestAdminDashboardOverview_RequiresAdmin proves the route is wired behind
// RequireSession then RequireAdmin at this handler's own mux -- the
// dedicated, per-endpoint proof CLAUDE.md asks every admin handler test to
// carry, distinct from (and in addition to) the enumerated guard proof
// TestMountAndServe_AdminRoutesRequireSessionAndAdmin runs over the real
// adminRoutes table in cmd/opencircuit.
func TestAdminDashboardOverview_RequiresAdmin(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminDashboardMux(pool, true))
	defer srv.Close()

	nonAdmin := seedNonAdminLocal(t, pool, "dashboard-nonadmin@example.com")
	admin := seedAdmin(t, pool, "dashboard-admin@example.com")
	seedSession(t, pool, nonAdmin, "dashboard-nonadmin-token")
	seedSession(t, pool, admin, "dashboard-admin-token")

	url := srv.URL + "/admin/overview"

	if resp := doJSON(t, srv.Client(), "GET", url, "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no session: status = %d, want 401", resp.StatusCode)
	}
	if resp := doJSON(t, srv.Client(), "GET", url, "dashboard-nonadmin-token", ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin session: status = %d, want 403", resp.StatusCode)
	}
	if resp := doJSON(t, srv.Client(), "GET", url, "dashboard-admin-token", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("admin session: status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}

// seedNonAdminLocal mirrors seedAdmin (settings_test.go) for the one
// non-admin user this file's guard test needs.
func seedNonAdminLocal(t *testing.T, pool *pgxpool.Pool, email string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, is_admin, active, created_at) VALUES ($1, FALSE, TRUE, now()) RETURNING id`,
		email,
	).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

// TestAdminDashboardOverview_SubscriberCountsAndGrowthExcludeSynthetic seeds
// one of each subscriber status plus a synthetic row, and one subscriber
// confirmed just now (inside the 30-day window), and proves the response
// reflects a before/after DELTA -- this shared database is never truncated
// between tests (see internal/subscribers' testPool doc comment), so an
// absolute-count assertion would be meaningless here.
func TestAdminDashboardOverview_SubscriberCountsAndGrowthExcludeSynthetic(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminDashboardMux(pool, true))
	defer srv.Close()

	admin := seedAdmin(t, pool, "dashboard-counts-admin@example.com")
	seedSession(t, pool, admin, "dashboard-counts-admin-token")
	url := srv.URL + "/admin/overview"

	before := getDashboard(t, srv.Client(), url, "dashboard-counts-admin-token")

	subsStore := subscribers.NewStore(pool)
	now := time.Now()

	// A real, freshly-confirmed active subscriber: bumps active and
	// confirmed_30d.
	created, err := subsStore.Create(context.Background(), subscribers.NewSignup{
		Email: fmt.Sprintf("zz-dash-active-%d@example.com", testdb.Unique()), ConfirmTTL: time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("create active subscriber: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := subsStore.Confirm(context.Background(), *created.ConfirmToken, now); err != nil {
		t.Fatalf("confirm subscriber: %v", err)
	}

	// A synthetic subscriber, also confirmed just now: must NOT move any
	// counter (#0061's amendment).
	syn, err := subsStore.Create(context.Background(), subscribers.NewSignup{
		Email: fmt.Sprintf("zz-dash-synthetic-%d@example.com", testdb.Unique()), ConfirmTTL: time.Hour, Synthetic: true,
	}, now)
	if err != nil {
		t.Fatalf("create synthetic subscriber: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, syn.ID) })
	if _, err := subsStore.Confirm(context.Background(), *syn.ConfirmToken, now); err != nil {
		t.Fatalf("confirm synthetic subscriber: %v", err)
	}

	after := getDashboard(t, srv.Client(), url, "dashboard-counts-admin-token")

	if after.Subscribers.Counts.Active != before.Subscribers.Counts.Active+1 {
		t.Errorf("active = %d, want %d (before=%d + 1 real subscriber, excluding the synthetic one)",
			after.Subscribers.Counts.Active, before.Subscribers.Counts.Active+1, before.Subscribers.Counts.Active)
	}
	if after.Subscribers.Growth.Confirmed30d != before.Subscribers.Growth.Confirmed30d+1 {
		t.Errorf("confirmed_30d = %d, want %d (before=%d + 1)",
			after.Subscribers.Growth.Confirmed30d, before.Subscribers.Growth.Confirmed30d+1, before.Subscribers.Growth.Confirmed30d)
	}
}

// TestAdminDashboardOverview_InterestDistribution proves the interests list
// carries a real subscriber_count for a freshly-linked subscriber.
func TestAdminDashboardOverview_InterestDistribution(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminDashboardMux(pool, true))
	defer srv.Close()

	admin := seedAdmin(t, pool, "dashboard-interests-admin@example.com")
	seedSession(t, pool, admin, "dashboard-interests-admin-token")
	url := srv.URL + "/admin/overview"

	interestsStore := interests.NewStore(pool)
	slug := fmt.Sprintf("zz-dash-interest-%d", testdb.Unique())
	it, err := interestsStore.Create(context.Background(), slug, "Dashboard Test Interest", nil, 0)
	if err != nil {
		t.Fatalf("create interest: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM interests WHERE id = $1`, it.ID) })

	seedSubscriberLinkedToInterest(t, pool, it.ID)

	out := getDashboard(t, srv.Client(), url, "dashboard-interests-admin-token")

	var found bool
	for _, row := range out.Interests {
		if row.ID == it.ID {
			found = true
			if row.SubscriberCount != 1 {
				t.Errorf("interest %d subscriber_count = %d, want 1", it.ID, row.SubscriberCount)
			}
			if row.Slug != slug || row.Name != "Dashboard Test Interest" {
				t.Errorf("interest %d = {slug:%q name:%q}, want {slug:%q name:%q}", it.ID, row.Slug, row.Name, slug, "Dashboard Test Interest")
			}
		}
	}
	if !found {
		t.Fatalf("interest %d not present in dashboard response's interests list", it.ID)
	}
}

// TestAdminDashboardOverview_RecentAndSendingCampaigns seeds one 'sending'
// campaign and asserts it is reported as THE sending campaign and also
// appears in recent_campaigns. Campaigns created immediately before the
// request are, by construction, the most-recently-created rows globally
// (monotonic id, sequential test execution, no concurrent writers against
// this test's own database — CLAUDE.md §5a), so this does not depend on the
// shared table being empty.
func TestAdminDashboardOverview_RecentAndSendingCampaigns(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminDashboardMux(pool, true))
	defer srv.Close()

	admin := seedAdmin(t, pool, "dashboard-campaigns-admin@example.com")
	seedSession(t, pool, admin, "dashboard-campaigns-admin-token")
	url := srv.URL + "/admin/overview"

	campaignsStore := mailing.NewCampaignStore(pool)
	c, err := campaignsStore.Create(context.Background(), mailing.CampaignInput{
		Name: fmt.Sprintf("zz-dash-campaign-%d", testdb.Unique()), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM email_campaigns WHERE id = $1`, c.ID) })
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET status = 'sending', started_at = now(), materialized_at = now() WHERE id = $1`, c.ID,
	); err != nil {
		t.Fatalf("force campaign to sending: %v", err)
	}

	out := getDashboard(t, srv.Client(), url, "dashboard-campaigns-admin-token")

	if out.SendingCampaign == nil {
		t.Fatalf("sending_campaign is absent, want campaign %d", c.ID)
	}
	if out.SendingCampaign.ID != c.ID || out.SendingCampaign.Status != mailing.CampaignStatusSending {
		t.Errorf("sending_campaign = {id:%d status:%q}, want {id:%d status:%q}",
			out.SendingCampaign.ID, out.SendingCampaign.Status, c.ID, mailing.CampaignStatusSending)
	}

	var foundInRecent bool
	for _, row := range out.RecentCampaigns {
		if row.ID == c.ID {
			foundInRecent = true
		}
	}
	if !foundInRecent {
		t.Errorf("campaign %d not present in recent_campaigns", c.ID)
	}
}

// TestAdminDashboardOverview_Warnings covers the physical_address,
// SES-sandbox, complaint-rate, and inbound-mail warnings together, since
// physical_address is a single global settings row this test must save and
// restore rather than assume a starting value for (other tests in this
// package mutate it too, and the shared database is never truncated between
// tests).
func TestAdminDashboardOverview_Warnings(t *testing.T) {
	pool := adminSubscribersTestPool(t)

	authStore := auth.NewStore(pool)
	origAddr := settingValue(t, pool, "physical_address")
	t.Cleanup(func() {
		if _, err := authStore.UpdateSetting(context.Background(), "physical_address", origAddr, time.Now()); err != nil {
			t.Fatalf("restore physical_address: %v", err)
		}
	})

	admin := seedAdmin(t, pool, "dashboard-warnings-admin@example.com")
	seedSession(t, pool, admin, "dashboard-warnings-admin-token")

	t.Run("physical address unset and SES sandbox true", func(t *testing.T) {
		if _, err := authStore.UpdateSetting(context.Background(), "physical_address", "", time.Now()); err != nil {
			t.Fatalf("clear physical_address: %v", err)
		}
		srv := httptest.NewServer(adminDashboardMux(pool, true))
		defer srv.Close()
		out := getDashboard(t, srv.Client(), srv.URL+"/admin/overview", "dashboard-warnings-admin-token")
		if !out.Warnings.PhysicalAddressUnset {
			t.Error("physical_address_unset = false, want true (address cleared)")
		}
		if !out.Warnings.SESSandboxActive {
			t.Error("ses_sandbox_active = false, want true (constructed with sesSandbox=true)")
		}
		if !out.Warnings.InboundMailUnavailable {
			t.Error("inbound_mail_unavailable = false, want true — PRD §6.5 path 3 (#0058) is not built")
		}
	})

	t.Run("physical address set and SES sandbox false", func(t *testing.T) {
		if _, err := authStore.UpdateSetting(context.Background(), "physical_address", "Open Circuit SF, PO Box 1, SF, CA 94104", time.Now()); err != nil {
			t.Fatalf("set physical_address: %v", err)
		}
		srv := httptest.NewServer(adminDashboardMux(pool, false))
		defer srv.Close()
		out := getDashboard(t, srv.Client(), srv.URL+"/admin/overview", "dashboard-warnings-admin-token")
		if out.Warnings.PhysicalAddressUnset {
			t.Error("physical_address_unset = true, want false (address set)")
		}
		if out.Warnings.SESSandboxActive {
			t.Error("ses_sandbox_active = true, want false (constructed with sesSandbox=false)")
		}
	})
}

// TestAdminDashboardOverview_ComplaintRateHighAboveThreshold seeds enough
// account-wide sent/complained email_sends+email_events rows (reusing
// admin_campaign_stats_test.go's own seedStatsCampaign/seedStatsSubscriber/
// seedEmailSendRow/seedEmailEventRow helpers) to push the DELTA over both
// dashboardComplaintMinSample and dashboardComplaintRateThresholdPct, and
// proves complaint_rate_high flips true and complaint_rate_pct is populated
// once the sample is large enough. Both start possibly non-zero (the shared
// database is never truncated), so this seeds a large delta specifically
// designed to move the AFTER rate above threshold regardless of the BEFORE
// baseline, rather than asserting an absolute percentage.
func TestAdminDashboardOverview_ComplaintRateHighAboveThreshold(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminDashboardMux(pool, true))
	defer srv.Close()

	admin := seedAdmin(t, pool, "dashboard-complaint-admin@example.com")
	seedSession(t, pool, admin, "dashboard-complaint-admin-token")
	url := srv.URL + "/admin/overview"

	before := getDashboard(t, srv.Client(), url, "dashboard-complaint-admin-token")

	campaignID := seedStatsCampaign(t, pool)
	// dashboardComplaintMinSample is 50: seed 60 sent rows so this delta
	// alone clears the sample floor even if the account-wide baseline was
	// below it, and make every one of them a Complaint so the delta's own
	// rate (100%) pulls the after-rate above 0.3% regardless of the before
	// baseline (a lower baseline rate can only pull the blended rate down
	// from 100%, and with the baseline's own sample "diluting" a 100%-seeded
	// batch of 60, the blended rate stays far above 0.3% for any realistic
	// prior sample size on a test database).
	const seededSends = 60
	for i := 0; i < seededSends; i++ {
		subID, email := seedStatsSubscriber(t, pool, fmt.Sprintf("complaintrate-%d", i))
		sesMessageID := fmt.Sprintf("zz-dash-complaint-%d-%d", testdb.Unique(), i)
		seedEmailSendRow(t, pool, campaignID, subID, email, "sent", &sesMessageID, nil, 0)
		seedEmailEventRow(t, pool, fmt.Sprintf("zz-dash-complaint-sns-%d-%d", testdb.Unique(), i), sesMessageID, "Complaint", email)
	}

	after := getDashboard(t, srv.Client(), url, "dashboard-complaint-admin-token")

	if after.Warnings.ComplaintSampleSize < before.Warnings.ComplaintSampleSize+seededSends {
		t.Fatalf("complaint_sample_size = %d, want at least %d (before=%d + %d seeded sends)",
			after.Warnings.ComplaintSampleSize, before.Warnings.ComplaintSampleSize+seededSends,
			before.Warnings.ComplaintSampleSize, seededSends)
	}
	if !after.Warnings.ComplaintRateHigh {
		t.Errorf("complaint_rate_high = false, want true after seeding %d all-complained sends", seededSends)
	}
	if after.Warnings.ComplaintRatePct == nil {
		t.Fatal("complaint_rate_pct is nil, want a populated percentage once the sample clears the minimum")
	}
	if *after.Warnings.ComplaintRatePct <= dashboardComplaintRateThresholdPct {
		t.Errorf("complaint_rate_pct = %.4f, want > %.4f threshold", *after.Warnings.ComplaintRatePct, dashboardComplaintRateThresholdPct)
	}
}

// TestAdminDashboardBuildWarnings_ComplaintRateBandBoundaries is #0227's
// regression test for both complaint-rate bands, proved the way #0061's
// review proved the original single 0.3% threshold: one send either side of
// each boundary, asserting the corresponding flag flips. Unlike that
// review's proof (a throwaway probe binary against a live database, recorded
// only in prose — see #0061's Review notes), this drives buildWarnings
// directly — it is pure arithmetic over (complained, sent) with no store
// dependency, so no database or seeded fixture is needed to pin an EXACT
// fraction the way the DB-backed tests in this file (which assert only a
// delta against a shared, never-truncated table) cannot.
//
// The amber boundary (0.1%) is the arithmetically harder one per #0227's
// notes: 1/1000 = 0.100000000000000005551115... in float64, which this test
// confirmed (by hand, outside Go) rounds to the SAME float64 value as the
// literal 0.1 used for dashboardComplaintReviewThresholdPct, so `pct >=
// threshold` is a genuine equality at the boundary, not a near-miss that
// happens to round the right way. 1/1001 lands measurably below it
// (0.0999000999...%), so the two cases are not a coincidence of rounding.
//
// The red boundary (0.3%) reuses #0061's own already-verified pair (1/333
// warns, 1/334 does not) rather than re-deriving a new one, since #0227
// leaves that threshold's VALUE unchanged — only the comparison operator
// moved from strict `>` to `>=` (## Decision's table: "≥ 0.3%"), which this
// pair cannot distinguish (neither fraction lands exactly on 0.3%) but which
// the exact-0.3% case below does.
func TestAdminDashboardBuildWarnings_ComplaintRateBandBoundaries(t *testing.T) {
	h := &AdminDashboardHandler{}

	cases := []struct {
		name       string
		complained int64
		sent       int64
		wantReview bool
		wantHigh   bool
	}{
		{"far below both bands: 0/1000 = 0.0%", 0, 1000, false, false},
		{"amber boundary, warns: 1/1000 = 0.1000...%", 1, 1000, true, false},
		{"amber boundary, does not warn: 1/1001 = 0.0999...%", 1, 1001, false, false},
		{"between the bands: 1/500 = 0.2%", 1, 500, true, false},
		{"red boundary, exact: 3/1000 = 0.3000...%", 3, 1000, true, true},
		{"red boundary, just below exact: 2999/1000000 = 0.2999%", 2999, 1_000_000, true, false},
		{"red boundary, warns (#0061's own pair): 1/333 = 0.3003...%", 1, 333, true, true},
		{"red boundary, does not warn (#0061's own pair): 1/334 = 0.2994...%", 1, 334, true, false},
		{"far above both bands: 60/60 = 100%", 60, 60, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := h.buildWarnings(tc.complained, tc.sent, nil)
			pct := float64(tc.complained) / float64(tc.sent) * 100
			if w.ComplaintRatePct == nil {
				t.Fatalf("complaint_rate_pct is nil at sent=%d (>= dashboardComplaintMinSample=%d, should be populated)", tc.sent, dashboardComplaintMinSample)
			}
			if w.ComplaintRateReview != tc.wantReview {
				t.Errorf("complaint_rate_review = %v, want %v (pct=%.10f%%, amber threshold=%.2f%%)",
					w.ComplaintRateReview, tc.wantReview, pct, dashboardComplaintReviewThresholdPct)
			}
			if w.ComplaintRateHigh != tc.wantHigh {
				t.Errorf("complaint_rate_high = %v, want %v (pct=%.10f%%, red threshold=%.2f%%)",
					w.ComplaintRateHigh, tc.wantHigh, pct, dashboardComplaintRateThresholdPct)
			}
		})
	}
}
