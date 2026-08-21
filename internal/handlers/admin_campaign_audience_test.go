package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// adminCampaignAudienceMux wires the real GET /admin/campaigns/{id}/audience
// route guarded by RequireSession then RequireAdmin, backed by a real
// *mailing.AudienceStore — mirrors adminCampaignsMux (admin_campaigns_test.go).
func adminCampaignAudienceMux(pool *pgxpool.Pool) http.Handler {
	authStore := auth.NewStore(pool)
	audienceStore := mailing.NewAudienceStore(pool)
	h := NewAdminCampaignAudienceHandler(audienceStore)
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/campaigns/{id}/audience", requireAdmin(http.HandlerFunc(h.Preview)))
	return mux
}

type decodedCampaignAudience struct {
	Mode        string   `json:"mode"`
	InterestIDs []int64  `json:"interest_ids"`
	Count       int64    `json:"count"`
	Sample      []string `json:"sample"`
	Warnings    []string `json:"warnings"`
}

func decodeCampaignAudience(t *testing.T, body []byte) decodedCampaignAudience {
	t.Helper()
	var a decodedCampaignAudience
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("decode audience: %v (body=%s)", err, body)
	}
	return a
}

func TestAdminCampaignAudience_Preview_Success(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignAudienceMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-audience-ok@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-audience-ok")

	campaignStore := mailing.NewCampaignStore(pool)
	c, err := campaignStore.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	// Three throwaway active subscribers, scoped and cleaned up like every
	// other row in this package (CLAUDE.md §8b) — proves the count is real,
	// not a stub.
	for i := 0; i < 3; i++ {
		seedTestSubscriber(t, pool, subscribers.StatusActive)
	}

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/%d/audience", srv.URL, c.ID), "admin-token-campaign-audience-ok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	out := decodeCampaignAudience(t, readBody(t, resp))
	if out.Mode != mailing.AudienceAll {
		t.Errorf("Mode = %q, want %q", out.Mode, mailing.AudienceAll)
	}
	if out.Count != 3 {
		t.Errorf("Count = %d, want 3", out.Count)
	}
	if len(out.Sample) != 3 {
		t.Errorf("len(Sample) = %d, want 3 (under the %d sample cap)", len(out.Sample), campaignAudienceSampleLimit)
	}
	if out.InterestIDs == nil {
		t.Error("InterestIDs is nil, want an empty slice (never a bare JSON null)")
	}
	if out.Warnings == nil {
		t.Error("Warnings is nil, want an empty slice (never a bare JSON null)")
	}

	// This endpoint must never materialize — GET is read-only.
	var sendCount int64
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_sends WHERE campaign_id = $1`, c.ID,
	).Scan(&sendCount); err != nil {
		t.Fatalf("count email_sends: %v", err)
	}
	if sendCount != 0 {
		t.Errorf("email_sends rows = %d, want 0; GET /audience must never materialize", sendCount)
	}
}

func TestAdminCampaignAudience_Preview_SampleNeverExceedsCap(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignAudienceMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-audience-sample@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-audience-sample")

	campaignStore := mailing.NewCampaignStore(pool)
	c, err := campaignStore.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	for i := 0; i < campaignAudienceSampleLimit+5; i++ {
		seedTestSubscriber(t, pool, subscribers.StatusActive)
	}

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/%d/audience", srv.URL, c.ID), "admin-token-campaign-audience-sample", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	out := decodeCampaignAudience(t, readBody(t, resp))
	if out.Count != int64(campaignAudienceSampleLimit+5) {
		t.Errorf("Count = %d, want %d", out.Count, campaignAudienceSampleLimit+5)
	}
	if len(out.Sample) != campaignAudienceSampleLimit {
		t.Errorf("len(Sample) = %d, want the server-side cap of %d regardless of a larger real count", len(out.Sample), campaignAudienceSampleLimit)
	}
}

func TestAdminCampaignAudience_Preview_UnknownCampaignNotFound(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignAudienceMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-audience-404@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-audience-404")

	// A genuinely-missing id: seed through the real store, then delete it
	// immediately — never a literal id (CLAUDE.md §8b).
	campaignStore := mailing.NewCampaignStore(pool)
	c, err := campaignStore.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM email_campaigns WHERE id = $1`, c.ID); err != nil {
		t.Fatalf("delete campaign: %v", err)
	}

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/%d/audience", srv.URL, c.ID), "admin-token-campaign-audience-404", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}

// TestAdminCampaignAudience_Preview_EmptyInterestSetConflict exercises
// audience.go's own independent, materialize-time guard
// (mailing.ErrAudienceInterestsRequired) — deliberately a SECOND
// enforcement of the same rule mailing.CampaignStore.Create already applies
// at write time (mailing.ErrCampaignInterestsRequired), per #0044's plan
// §4 ("belt and braces, not shared implementation"). Reaching this state
// through the normal API is impossible (Create/Update both refuse it), so
// this test forces it directly at the row, the same way
// forceAdminCampaignStatus reaches 'sending' for the transition tests.
func TestAdminCampaignAudience_Preview_EmptyInterestSetConflict(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignAudienceMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaign-audience-409@example.com")
	seedSession(t, pool, admin, "admin-token-campaign-audience-409")

	campaignStore := mailing.NewCampaignStore(pool)
	c, err := campaignStore.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET audience_mode = 'any_of' WHERE id = $1`, c.ID,
	); err != nil {
		t.Fatalf("force audience_mode: %v", err)
	}

	resp := doJSON(t, srv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/%d/audience", srv.URL, c.ID), "admin-token-campaign-audience-409", "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}
