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

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// adminCampaignsMux wires the real admin campaigns routes guarded by
// RequireSession then RequireAdmin, backed by real stores — mirrors
// adminSuppressionsMux (admin_suppressions_test.go). preflight is nil unless
// a test explicitly needs the advisory-gate seam (see stubPreflightChecker
// below).
func adminCampaignsMux(pool *pgxpool.Pool, preflight campaignPreflightChecker) http.Handler {
	authStore := auth.NewStore(pool)
	store := mailing.NewCampaignStore(pool)
	audienceStore := mailing.NewAudienceStore(pool)
	h := NewAdminCampaignsHandler(store, preflight, audienceStore, audit.New(pool))
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/campaigns", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("POST /admin/campaigns", requireAdmin(http.HandlerFunc(h.Create)))
	mux.Handle("GET /admin/campaigns/{id}", requireAdmin(http.HandlerFunc(h.Get)))
	mux.Handle("PATCH /admin/campaigns/{id}", requireAdmin(http.HandlerFunc(h.Patch)))
	mux.Handle("POST /admin/campaigns/{id}/send", requireAdmin(http.HandlerFunc(h.Send)))
	mux.Handle("POST /admin/campaigns/{id}/cancel", requireAdmin(http.HandlerFunc(h.Cancel)))
	return mux
}

// stubPreflightChecker is a fake campaignPreflightChecker for exercising the
// advisory-gate seam (#0045's plan) before mailing.Preflight exists. It
// returns a fixed set of failures for every campaign id, proving Send's
// {"unmet":[{code,message}]} response contract works end to end.
type stubPreflightChecker struct {
	failures []campaignPreflightFailure
	err      error
}

func (s stubPreflightChecker) Check(context.Context, int64) ([]campaignPreflightFailure, error) {
	return s.failures, s.err
}

func uniqueAdminCampaignName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-subtest-campaign-%d", time.Now().UnixNano())
}

// cleanupAdminCampaign deletes a campaign row (cascading to
// campaign_interests/email_sends per migration 000017's ON DELETE CASCADE).
func cleanupAdminCampaign(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM email_campaigns WHERE id = $1`, id)
	})
}

// forceAdminCampaignStatus sets a campaign's status directly via SQL, for
// reaching a state (e.g. 'sending') that only #0045's future worker writes
// through this package's own API — mirrors
// internal/mailing/campaigns_test.go's setCampaignStatus. Never targets a
// literal or seeded id.
func forceAdminCampaignStatus(t *testing.T, pool *pgxpool.Pool, id int64, status string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET status = $2 WHERE id = $1`, id, status); err != nil {
		t.Fatalf("force campaign %d to status %q: %v", id, status, err)
	}
}

// auditActionsForCampaign returns the ordered audit_log.action values
// recorded against a campaign, mirroring interests_test.go's
// auditActionsForSlug.
func auditActionsForCampaign(t *testing.T, pool *pgxpool.Pool, id int64) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT action FROM audit_log WHERE target_type = $1 AND target_id = $2 ORDER BY id ASC`,
		audit.TargetEmailCampaign, id)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("scan audit action: %v", err)
		}
		actions = append(actions, a)
	}
	return actions
}

type decodedCampaign struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Subject      string  `json:"subject"`
	BodyMD       string  `json:"body_md"`
	Status       string  `json:"status"`
	AudienceMode string  `json:"audience_mode"`
	InterestIDs  []int64 `json:"interest_ids"`
	ScheduledAt  *string `json:"scheduled_at"`
	TestSentAt   *string `json:"test_sent_at"`
}

func decodeCampaign(t *testing.T, body []byte) decodedCampaign {
	t.Helper()
	var c decodedCampaign
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatalf("decode campaign: %v (body=%s)", err, body)
	}
	return c
}

// ── Create ───────────────────────────────────────────────────────────────────

func TestAdminCampaigns_Create_Success(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-create@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-create")

	name := uniqueAdminCampaignName(t)
	body := fmt.Sprintf(`{"name":%q,"subject":"Test subject","body_md":"# Hello","audience_mode":"all"}`, name)
	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/campaigns", "admin-token-campaigns-create", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	c := decodeCampaign(t, readBody(t, resp))
	cleanupAdminCampaign(t, pool, c.ID)

	if c.Status != mailing.CampaignStatusDraft {
		t.Errorf("Status = %q, want %q", c.Status, mailing.CampaignStatusDraft)
	}
	if c.Name != name {
		t.Errorf("Name = %q, want %q", c.Name, name)
	}

	actions := auditActionsForCampaign(t, pool, c.ID)
	if len(actions) != 1 || actions[0] != audit.ActionEmailCampaignCreated {
		t.Errorf("audit actions = %v, want [%s]", actions, audit.ActionEmailCampaignCreated)
	}
}

func TestAdminCampaigns_Create_MissingName(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-noname@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-noname")

	body := `{"subject":"Test subject","body_md":"# Hello","audience_mode":"all"}`
	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/campaigns", "admin-token-campaigns-noname", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAdminCampaigns_Create_EmptyInterestSetRejected(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-emptyint@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-emptyint")

	name := uniqueAdminCampaignName(t)
	body := fmt.Sprintf(`{"name":%q,"subject":"s","body_md":"b","audience_mode":"any_of"}`, name)
	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/campaigns", "admin-token-campaigns-emptyint", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	// The guard this test names: no row was written.
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_campaigns WHERE name = $1`, name).Scan(&count); err != nil {
		t.Fatalf("count check: %v", err)
	}
	if count != 0 {
		t.Errorf("email_campaigns row count = %d, want 0", count)
	}
}

func TestAdminCampaigns_Create_UnknownAudienceMode(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-unkmode@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-unkmode")

	body := fmt.Sprintf(`{"name":%q,"subject":"s","body_md":"b","audience_mode":"sometimes"}`, uniqueAdminCampaignName(t))
	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/campaigns", "admin-token-campaigns-unkmode", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// ── List / Get ───────────────────────────────────────────────────────────────

func TestAdminCampaigns_List_IncludesCreated(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-list@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-list")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	resp := doJSON(t, srv.Client(), "GET", srv.URL+"/admin/campaigns", "admin-token-campaigns-list", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Campaigns []decodedCampaign `json:"campaigns"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found bool
	for _, item := range out.Campaigns {
		if item.ID == c.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("list did not include the campaign just seeded (id=%d)", c.ID)
	}
}

func TestAdminCampaigns_Get_NotFound(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-getnf@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-getnf")

	resp := doJSON(t, srv.Client(), "GET", srv.URL+"/admin/campaigns/99999999", "admin-token-campaigns-getnf", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// ── Patch ────────────────────────────────────────────────────────────────────

func TestAdminCampaigns_Patch_ContentEdit(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-patch@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-patch")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "old subject", BodyMD: "old body", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	body := `{"subject":"new subject"}`
	resp := doJSON(t, srv.Client(), "PATCH", fmt.Sprintf("%s/admin/campaigns/%d", srv.URL, c.ID), "admin-token-campaigns-patch", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	updated := decodeCampaign(t, readBody(t, resp))
	if updated.Subject != "new subject" {
		t.Errorf("Subject = %q, want %q", updated.Subject, "new subject")
	}
	if updated.BodyMD != "old body" {
		t.Errorf("BodyMD = %q, want unchanged %q", updated.BodyMD, "old body")
	}

	actions := auditActionsForCampaign(t, pool, c.ID)
	if len(actions) != 1 || actions[0] != audit.ActionEmailCampaignUpdated {
		t.Errorf("audit actions = %v, want [%s]", actions, audit.ActionEmailCampaignUpdated)
	}
}

func TestAdminCampaigns_Patch_NotEditableWhenSending(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-patchlock@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-patchlock")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)
	forceAdminCampaignStatus(t, pool, c.ID, mailing.CampaignStatusSending)

	body := `{"subject":"changed"}`
	resp := doJSON(t, srv.Client(), "PATCH", fmt.Sprintf("%s/admin/campaigns/%d", srv.URL, c.ID), "admin-token-campaigns-patchlock", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}

// ── Send ─────────────────────────────────────────────────────────────────────

func TestAdminCampaigns_Send_DraftToScheduled(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-send@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-send")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/send", srv.URL, c.ID), "admin-token-campaigns-send", `{"confirm_count":0}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	updated := decodeCampaign(t, readBody(t, resp))
	if updated.Status != mailing.CampaignStatusScheduled {
		t.Errorf("Status = %q, want %q", updated.Status, mailing.CampaignStatusScheduled)
	}
	if updated.ScheduledAt == nil {
		t.Error("ScheduledAt is nil, want set (send-now default)")
	}

	actions := auditActionsForCampaign(t, pool, c.ID)
	if len(actions) != 1 || actions[0] != audit.ActionEmailCampaignScheduled {
		t.Errorf("audit actions = %v, want [%s]", actions, audit.ActionEmailCampaignScheduled)
	}
}

func TestAdminCampaigns_Send_MissingConfirmCountRejected(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-sendnocount@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-sendnocount")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	// #0047's plan (2026-08-21): confirm_count is required — a typed
	// confirmation enforced only in the browser is theatre. An empty body
	// (no confirm_count at all) must be refused, not defaulted.
	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/send", srv.URL, c.ID), "admin-token-campaigns-sendnocount", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	// The guard this test names: the row must still be draft — a missing
	// confirm_count must never let the campaign through to 'scheduled'.
	got, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != mailing.CampaignStatusDraft {
		t.Errorf("status after Send with no confirm_count = %q, want unchanged %q", got.Status, mailing.CampaignStatusDraft)
	}
}

func TestAdminCampaigns_Send_NegativeConfirmCountRejected(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-sendnegcount@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-sendnegcount")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/send", srv.URL, c.ID), "admin-token-campaigns-sendnegcount", `{"confirm_count":-1}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}

func TestAdminCampaigns_Send_IllegalFromScheduled(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-sendillegal@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-sendillegal")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)
	forceAdminCampaignStatus(t, pool, c.ID, mailing.CampaignStatusScheduled)

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/send", srv.URL, c.ID), "admin-token-campaigns-sendillegal", `{"confirm_count":0}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	// #0047's plan (2026-08-21): the two 409 shapes Send can return must be
	// distinguishable by body shape. An illegal-transition 409 must NOT look
	// like a preflight failure — no top-level "unmet" key.
	body := readBody(t, resp)
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(body, &shape); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, hasUnmet := shape["unmet"]; hasUnmet {
		t.Errorf("illegal-transition 409 body carries an \"unmet\" key (body=%s); this must be indistinguishable from a preflight failure only over its ABSENCE", body)
	}
	if _, hasError := shape["error"]; !hasError {
		t.Errorf("illegal-transition 409 body has no \"error\" key (body=%s)", body)
	}

	// The guard this test names: the row's status must be unchanged.
	got, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != mailing.CampaignStatusScheduled {
		t.Errorf("status after rejected Send = %q, want unchanged %q", got.Status, mailing.CampaignStatusScheduled)
	}
}

func TestAdminCampaigns_Send_PreflightFailureReturns409UnmetShape(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	checker := stubPreflightChecker{failures: []campaignPreflightFailure{
		{Code: "no_test_send", Message: "No test send has been delivered yet."},
		{Code: "physical_address_missing", Message: "Physical mailing address is not set."},
	}}
	srv := httptest.NewServer(adminCampaignsMux(pool, checker))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-preflight@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-preflight")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/send", srv.URL, c.ID), "admin-token-campaigns-preflight", `{"confirm_count":0}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out struct {
		Unmet []campaignPreflightFailure `json:"unmet"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Unmet) != 2 || out.Unmet[0].Code != "no_test_send" || out.Unmet[1].Code != "physical_address_missing" {
		t.Errorf("Unmet = %+v, want the two stub failures in order", out.Unmet)
	}

	// The guard this test names: a failed preflight must never transition
	// the campaign — it stays draft, not scheduled.
	got, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != mailing.CampaignStatusDraft {
		t.Errorf("status after preflight-refused Send = %q, want unchanged %q", got.Status, mailing.CampaignStatusDraft)
	}

	// No email_campaign.scheduled audit row should exist either.
	actions := auditActionsForCampaign(t, pool, c.ID)
	for _, a := range actions {
		if a == audit.ActionEmailCampaignScheduled {
			t.Errorf("audit actions = %v, must not include %s when preflight refused", actions, audit.ActionEmailCampaignScheduled)
		}
	}
}

// TestAdminCampaigns_Send_RealPreflightAdapter_Returns409UnmetShape proves
// #0045's own NewCampaignPreflightChecker adapter — a real
// *mailing.SendStore backed by this pool, not stubPreflightChecker — wires
// into Send correctly: a campaign missing physical_address and a test send
// is refused with the same {"unmet":[...]} shape, using mailing.Preflight's
// real stable codes (§2 of that issue's plan), never transitioning the
// campaign to scheduled. This is the seam #0041 left as a documented TODO
// (campaignPreflightChecker's own doc comment) — this test proves it is no
// longer nil in real wiring.
func TestAdminCampaigns_Send_RealPreflightAdapter_Returns409UnmetShape(t *testing.T) {
	pool := adminSubscribersTestPool(t)

	// Set physical_address to '' explicitly rather than relying on the
	// migration 000008 seed still being in place — settingsTestPool's own
	// tests mutate this row and, absent an explicit t.Cleanup on their
	// part, could leave it non-empty for whichever test runs next under
	// -shuffle=on (#0121). EMAIL_LIST_DOMAIN/EMAIL_REPLY_TO have no
	// settings-table equivalent — this test's SendStore is constructed with
	// real, non-blank values for both so the ONLY unmet requirements are
	// the two this test names.
	setPhysicalAddressForTest(t, pool, "")
	audienceStore := mailing.NewAudienceStore(pool)
	authStore := auth.NewStore(pool)
	sendStore := mailing.NewSendStore(pool, audienceStore, authStore, nil,
		"https://www.example-oc-test.com", "lists.example-oc-test.com", "hello@example-oc-test.com")
	checker := NewCampaignPreflightChecker(sendStore)

	srv := httptest.NewServer(adminCampaignsMux(pool, checker))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-real-preflight@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-real-preflight")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)
	// Seed one active subscriber so empty_audience does not also fire —
	// this test isolates physical_address_missing and no_test_send.
	seedSubscriberRow(t, pool, fmt.Sprintf("zz-realpreflight-%d@example.com", time.Now().UnixNano()), subscribers.StatusActive, nil, nil)

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/send", srv.URL, c.ID), "admin-token-campaigns-real-preflight", `{"confirm_count":1}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out struct {
		Unmet []campaignPreflightFailure `json:"unmet"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	codes := map[string]bool{}
	for _, f := range out.Unmet {
		codes[f.Code] = true
	}
	if !codes["physical_address_missing"] {
		t.Errorf("Unmet = %+v, want physical_address_missing", out.Unmet)
	}
	if !codes["no_test_send"] {
		t.Errorf("Unmet = %+v, want no_test_send", out.Unmet)
	}
	if codes["empty_audience"] {
		t.Errorf("Unmet = %+v, must not include empty_audience — a subscriber was seeded", out.Unmet)
	}

	got, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != mailing.CampaignStatusDraft {
		t.Errorf("status after real-preflight-refused Send = %q, want unchanged %q", got.Status, mailing.CampaignStatusDraft)
	}
}

// TestNewCampaignPreflightChecker_NilStore_ReturnsGenuinelyNilInterface
// proves the typed-nil trap CLAUDE.md §9 and this issue's plan both warn
// about cannot happen through this constructor: passing a nil
// *mailing.SendStore must produce an untyped nil campaignPreflightChecker,
// not a non-nil interface wrapping a nil pointer (which would defeat Send's
// `h.preflight != nil` guard and panic inside Check the first time it's
// called).
func TestNewCampaignPreflightChecker_NilStore_ReturnsGenuinelyNilInterface(t *testing.T) {
	checker := NewCampaignPreflightChecker(nil)
	if checker != nil {
		t.Fatalf("NewCampaignPreflightChecker(nil) = %#v, want a genuinely nil interface", checker)
	}
}

// TestAdminCampaigns_Send_ConfirmCountMismatchRejected is #0044's carried-in
// acceptance criterion: confirm_count is now compared against the
// authoritative audience count (mailing.AudienceStore.Preview), not merely
// shape-validated. A stale/wrong count must be refused with the plain
// {"error":"..."} envelope — never the {"unmet":[...]} preflight shape
// #0047 branches on — and must never let the campaign transition.
func TestAdminCampaigns_Send_ConfirmCountMismatchRejected(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-countmismatch@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-countmismatch")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	// The real audience count for a fresh mode='all' campaign in this
	// test's isolated fixture is 0; typing anything else must be refused.
	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/send", srv.URL, c.ID), "admin-token-campaigns-countmismatch", `{"confirm_count":5}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	body := readBody(t, resp)
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(body, &shape); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, hasUnmet := shape["unmet"]; hasUnmet {
		t.Errorf("confirm_count mismatch body carries an \"unmet\" key (body=%s); must use the plain error envelope, not the preflight shape", body)
	}
	if _, hasError := shape["error"]; !hasError {
		t.Errorf("confirm_count mismatch body has no \"error\" key (body=%s)", body)
	}

	got, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != mailing.CampaignStatusDraft {
		t.Errorf("status after confirm_count mismatch = %q, want unchanged %q", got.Status, mailing.CampaignStatusDraft)
	}
}

// ── Cancel ───────────────────────────────────────────────────────────────────

func TestAdminCampaigns_Cancel_ScheduledToCanceled(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-cancel@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-cancel")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)
	if _, err := store.Send(context.Background(), c.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/cancel", srv.URL, c.ID), "admin-token-campaigns-cancel", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	updated := decodeCampaign(t, readBody(t, resp))
	if updated.Status != mailing.CampaignStatusCanceled {
		t.Errorf("Status = %q, want %q", updated.Status, mailing.CampaignStatusCanceled)
	}

	// Send above went through the store directly (not POST .../send), so it
	// wrote no audit row — only the HTTP Cancel call did.
	actions := auditActionsForCampaign(t, pool, c.ID)
	if len(actions) != 1 || actions[0] != audit.ActionEmailCampaignCanceled {
		t.Errorf("audit actions = %v, want [%s]", actions, audit.ActionEmailCampaignCanceled)
	}
}

func TestAdminCampaigns_Cancel_IllegalFromDraft(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-cancelillegal@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-cancelillegal")

	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/cancel", srv.URL, c.ID), "admin-token-campaigns-cancelillegal", "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (a draft campaign is not cancellable) (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}

func TestAdminCampaigns_Cancel_NotFound(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-cancelnf@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-cancelnf")

	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/campaigns/99999999/cancel", "admin-token-campaigns-cancelnf", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
