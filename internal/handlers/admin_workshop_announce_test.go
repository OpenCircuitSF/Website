package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// adminWorkshopAnnounceMux wires the real admin workshops routes (including
// POST .../announce) guarded by RequireSession then RequireAdmin, backed by
// real stores. Announce lives on AdminWorkshopsHandler itself
// (admin_workshop_announce.go's package doc comment explains why it isn't a
// separate handler type), so this is exactly adminWorkshopsMux with a nil
// invalidator — the announce tests below don't touch SEO/sitemap caching.
func adminWorkshopAnnounceMux(pool *pgxpool.Pool) http.Handler {
	return adminWorkshopsMux(pool, nil)
}

type decodedAnnouncedCampaign struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Subject      string  `json:"subject"`
	BodyMD       string  `json:"body_md"`
	Status       string  `json:"status"`
	AudienceMode string  `json:"audience_mode"`
	WorkshopID   *int64  `json:"workshop_id"`
	InterestIDs  []int64 `json:"interest_ids"`
}

func decodeAnnouncedCampaign(t *testing.T, body []byte) decodedAnnouncedCampaign {
	t.Helper()
	var c decodedAnnouncedCampaign
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatalf("decode announced campaign: %v (body=%s)", err, body)
	}
	return c
}

func cleanupAnnouncedCampaign(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM email_campaigns WHERE id = $1`, id)
	})
}

// TestAdminWorkshopAnnounce_PrefillAndLink is #0056's own binding
// acceptance criterion: the created campaign is draft, linked via
// workshop_id, and its subject/body carry the workshop's title, summary,
// date, and location.
func TestAdminWorkshopAnnounce_PrefillAndLink(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopAnnounceMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-announce-prefill@example.com")
	seedSession(t, pool, admin, "announce-prefill-token")

	interestSlug := fmt.Sprintf("zz-subtest-announce-interest-%d", testdb.Unique())
	interest, err := interests.NewStore(pool).Create(context.Background(), interestSlug, "Announce guard target", nil, 0)
	if err != nil {
		t.Fatalf("seed interest: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM interests WHERE id = $1`, interest.ID)
	})

	starts := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	ends := time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)
	summary := "Hands-on soldering for beginners."
	locationName := "The Shop"
	locationAddress := "123 Circuit Ave"
	locationNote := "Enter through the side door"
	signupURL := "https://example.com/rsvp"

	wkStore := workshops.NewStore(pool)
	wk, err := wkStore.Create(context.Background(), workshops.CreateInput{
		Title:           uniqueAdminWorkshopTitle(t),
		Summary:         &summary,
		StartsAt:        &starts,
		EndsAt:          &ends,
		LocationName:    &locationName,
		LocationAddress: &locationAddress,
		LocationNote:    &locationNote,
		SignupURL:       &signupURL,
		InterestIDs:     []int64{interest.ID},
	})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, wk.ID)

	resp := doJSON(t, srv.Client(), http.MethodPost,
		fmt.Sprintf("%s/admin/workshops/%d/announce", srv.URL, wk.ID), "announce-prefill-token", "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST .../announce status = %d, want 201 (body=%s)", resp.StatusCode, body)
	}
	created := decodeAnnouncedCampaign(t, body)
	cleanupAnnouncedCampaign(t, pool, created.ID)

	if created.Status != mailing.CampaignStatusDraft {
		t.Errorf("Status = %q, want %q — announce must never send", created.Status, mailing.CampaignStatusDraft)
	}
	if created.WorkshopID == nil || *created.WorkshopID != wk.ID {
		t.Fatalf("WorkshopID = %v, want %d", created.WorkshopID, wk.ID)
	}
	if created.AudienceMode != mailing.AudienceAnyOf {
		t.Errorf("AudienceMode = %q, want %q", created.AudienceMode, mailing.AudienceAnyOf)
	}
	if len(created.InterestIDs) != 1 || created.InterestIDs[0] != interest.ID {
		t.Errorf("InterestIDs = %v, want [%d]", created.InterestIDs, interest.ID)
	}
	if created.Subject == "" || !strings.Contains(created.Subject, wk.Title) {
		t.Errorf("Subject = %q, want it to contain the workshop title %q", created.Subject, wk.Title)
	}
	wantBodyFragments := []string{
		wk.Title, summary, "September 12, 2026", locationName, locationAddress, locationNote, signupURL,
		// #0144: rendered in the org's zone, not UTC — 18:00Z/20:00Z on
		// 2026-09-12 is 11:00 AM/1:00 PM PDT the same Pacific day.
		"11:00 AM PDT to 1:00 PM PDT",
		// #0145: the workshop's own page is always linked, end to end
		// through the real h.baseURL wired by adminWorkshopAnnounceMux
		// (adminWorkshopsMux passes "https://example.com").
		"https://example.com/workshops/" + wk.Slug,
	}
	for _, frag := range wantBodyFragments {
		if !strings.Contains(created.BodyMD, frag) {
			t.Errorf("BodyMD = %q, want it to contain %q", created.BodyMD, frag)
		}
	}

	actions := auditActionsForWorkshop(t, pool, wk.ID)
	if len(actions) != 0 {
		t.Errorf("audit actions targeting the WORKSHOP = %v, want none (the audit entry targets the campaign)", actions)
	}

	var auditCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_type = $2 AND target_id = $3`,
		audit.ActionEmailCampaignCreated, audit.TargetEmailCampaign, created.ID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("query audit_log for created campaign: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("audit_log rows for campaign %d = %d, want 1", created.ID, auditCount)
	}
}

// TestAdminWorkshopAnnounce_NoInterestsFallsBackToAll covers a workshop
// carrying zero interests: any_of with an empty interest set is refused
// store-side (mailing.ErrCampaignInterestsRequired), so the handler must
// fall back to AudienceAll rather than surface that as an error from what
// is supposed to be a one-click action.
func TestAdminWorkshopAnnounce_NoInterestsFallsBackToAll(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopAnnounceMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-announce-nointerests@example.com")
	seedSession(t, pool, admin, "announce-nointerests-token")

	wkStore := workshops.NewStore(pool)
	wk, err := wkStore.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, wk.ID)

	resp := doJSON(t, srv.Client(), http.MethodPost,
		fmt.Sprintf("%s/admin/workshops/%d/announce", srv.URL, wk.ID), "announce-nointerests-token", "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST .../announce status = %d, want 201 (body=%s)", resp.StatusCode, body)
	}
	created := decodeAnnouncedCampaign(t, body)
	cleanupAnnouncedCampaign(t, pool, created.ID)

	if created.AudienceMode != mailing.AudienceAll {
		t.Errorf("AudienceMode = %q, want %q for an interest-less workshop", created.AudienceMode, mailing.AudienceAll)
	}
	if len(created.InterestIDs) != 0 {
		t.Errorf("InterestIDs = %v, want none", created.InterestIDs)
	}
}

// TestAdminWorkshopAnnounce_RepeatedCallsCreateSeparateDrafts is #0056's
// explicit acceptance criterion: "Repeated announce calls create a new
// draft rather than overwriting an existing one."
func TestAdminWorkshopAnnounce_RepeatedCallsCreateSeparateDrafts(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopAnnounceMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-announce-repeat@example.com")
	seedSession(t, pool, admin, "announce-repeat-token")

	wkStore := workshops.NewStore(pool)
	wk, err := wkStore.Create(context.Background(), workshops.CreateInput{Title: uniqueAdminWorkshopTitle(t)})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, wk.ID)

	path := fmt.Sprintf("%s/admin/workshops/%d/announce", srv.URL, wk.ID)

	first := doJSON(t, srv.Client(), http.MethodPost, path, "announce-repeat-token", "")
	firstBody, _ := io.ReadAll(first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first announce status = %d, want 201 (body=%s)", first.StatusCode, firstBody)
	}
	firstCreated := decodeAnnouncedCampaign(t, firstBody)
	cleanupAnnouncedCampaign(t, pool, firstCreated.ID)

	second := doJSON(t, srv.Client(), http.MethodPost, path, "announce-repeat-token", "")
	secondBody, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("second announce status = %d, want 201 (body=%s)", second.StatusCode, secondBody)
	}
	secondCreated := decodeAnnouncedCampaign(t, secondBody)
	cleanupAnnouncedCampaign(t, pool, secondCreated.ID)

	if firstCreated.ID == secondCreated.ID {
		t.Fatalf("repeated announce calls returned the same campaign id %d, want two distinct drafts", firstCreated.ID)
	}
	if secondCreated.WorkshopID == nil || *secondCreated.WorkshopID != wk.ID {
		t.Errorf("second draft WorkshopID = %v, want %d", secondCreated.WorkshopID, wk.ID)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_campaigns WHERE workshop_id = $1`, wk.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count campaigns for workshop: %v", err)
	}
	if count != 2 {
		t.Errorf("email_campaigns rows referencing workshop %d = %d, want 2", wk.ID, count)
	}
}

// TestAdminWorkshopAnnounce_UnknownWorkshopNotFound covers the 404 path.
func TestAdminWorkshopAnnounce_UnknownWorkshopNotFound(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopAnnounceMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-announce-404@example.com")
	seedSession(t, pool, admin, "announce-404-token")

	resp := doJSON(t, srv.Client(), http.MethodPost,
		srv.URL+"/admin/workshops/99999999/announce", "announce-404-token", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST .../announce for a nonexistent workshop status = %d, want 404", resp.StatusCode)
	}
}

// TestAdminWorkshopAnnounce_NonAdminForbidden mirrors
// TestAdminWorkshops_NonAdminForbidden's shape for the new route.
func TestAdminWorkshopAnnounce_NonAdminForbidden(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopAnnounceMux(pool))
	defer srv.Close()

	nonAdmin := seedUser(t, pool, "nonadmin-announce@example.com")
	seedSession(t, pool, nonAdmin, "announce-nonadmin-token")

	resp := doJSON(t, srv.Client(), http.MethodPost,
		srv.URL+"/admin/workshops/1/announce", "announce-nonadmin-token", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST .../announce as non-admin status = %d, want 403", resp.StatusCode)
	}
}
