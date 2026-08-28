// Tests for AdminCampaignArchiveHandler (#0123): PATCH
// /admin/campaigns/{id}/archive toggles published/withheld, audited.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

func adminCampaignArchiveMux(pool *pgxpool.Pool, invalidator workshopCacheInvalidator) http.Handler {
	authStore := auth.NewStore(pool)
	store := mailing.NewCampaignStore(pool)
	h := NewAdminCampaignArchiveHandler(store, audit.New(pool), invalidator)
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("PATCH /admin/campaigns/{id}/archive", requireAdmin(http.HandlerFunc(h.Patch)))
	return mux
}

// seedAdminArchiveCampaign creates a draft campaign via the real store, then
// force-sets status/archive_status directly via SQL to reach a state this
// package's own API cannot -- mirrors forceAdminCampaignStatus above.
func seedAdminArchiveCampaign(t *testing.T, pool *pgxpool.Pool, status, archiveStatus string) mailing.Campaign {
	t.Helper()
	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: uniqueAdminCampaignName(t) + " subject",
		BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET status = $2, archive_status = $3, archived_at = now() WHERE id = $1`,
		c.ID, status, archiveStatus,
	); err != nil {
		t.Fatalf("force campaign %d: %v", c.ID, err)
	}
	c.Status = status
	c.ArchiveStatus = archiveStatus
	return c
}

func TestAdminCampaignArchive_Patch_PublishedToWithheld(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignArchiveMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-archive-withhold@example.com")
	seedSession(t, pool, admin, "admin-token-archive-withhold")

	c := seedAdminArchiveCampaign(t, pool, mailing.CampaignStatusSent, mailing.ArchiveStatusPublished)

	resp := doJSON(t, srv.Client(), "PATCH", fmt.Sprintf("%s/admin/campaigns/%d/archive", srv.URL, c.ID),
		"admin-token-archive-withhold", `{"status":"withheld"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var got decodedCampaign
	if err := json.Unmarshal(readBody(t, resp), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ArchiveStatus != mailing.ArchiveStatusWithheld {
		t.Errorf("ArchiveStatus = %q, want %q", got.ArchiveStatus, mailing.ArchiveStatusWithheld)
	}

	actions := auditActionsForCampaign(t, pool, c.ID)
	if len(actions) != 1 || actions[0] != audit.ActionEmailCampaignArchiveUpdated {
		t.Errorf("audit actions = %v, want [%s]", actions, audit.ActionEmailCampaignArchiveUpdated)
	}
}

func TestAdminCampaignArchive_Patch_RefusesWhilePending(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignArchiveMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-archive-pending@example.com")
	seedSession(t, pool, admin, "admin-token-archive-pending")

	c := seedAdminArchiveCampaign(t, pool, mailing.CampaignStatusDraft, mailing.ArchiveStatusPending)

	resp := doJSON(t, srv.Client(), "PATCH", fmt.Sprintf("%s/admin/campaigns/%d/archive", srv.URL, c.ID),
		"admin-token-archive-pending", `{"status":"withheld"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}

func TestAdminCampaignArchive_Patch_RejectsUnknownStatus(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignArchiveMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-archive-badstatus@example.com")
	seedSession(t, pool, admin, "admin-token-archive-badstatus")

	c := seedAdminArchiveCampaign(t, pool, mailing.CampaignStatusSent, mailing.ArchiveStatusPublished)

	resp := doJSON(t, srv.Client(), "PATCH", fmt.Sprintf("%s/admin/campaigns/%d/archive", srv.URL, c.ID),
		"admin-token-archive-badstatus", `{"status":"pending"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}

// TestAdminCampaignArchive_Patch_InvalidatesSEOCaches is #0319's regression
// test: a successful published<->withheld transition must clear
// internal/seo's meta/sitemap caches through the SAME workshopCacheInvalidator
// seam AdminWorkshopsHandler already uses (countingWorkshopInvalidator,
// admin_workshops_test.go) -- not a second, invented mechanism.
func TestAdminCampaignArchive_Patch_InvalidatesSEOCaches(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	inv := &countingWorkshopInvalidator{}
	srv := httptest.NewServer(adminCampaignArchiveMux(pool, inv))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-archive-invalidate@example.com")
	seedSession(t, pool, admin, "admin-token-archive-invalidate")

	c := seedAdminArchiveCampaign(t, pool, mailing.CampaignStatusSent, mailing.ArchiveStatusPublished)

	resp := doJSON(t, srv.Client(), "PATCH", fmt.Sprintf("%s/admin/campaigns/%d/archive", srv.URL, c.ID),
		"admin-token-archive-invalidate", `{"status":"withheld"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	if inv.calls != 1 {
		t.Errorf("invalidator.calls = %d after a withhold, want 1", inv.calls)
	}
}

// TestAdminCampaignArchive_Patch_RefusedWriteDoesNotInvalidate proves the
// invalidation call sits after the store write commits, not before or on
// every request regardless of outcome -- a 409 (never sent, no archive page
// to toggle) must not clear a cache that holds nothing stale.
func TestAdminCampaignArchive_Patch_RefusedWriteDoesNotInvalidate(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	inv := &countingWorkshopInvalidator{}
	srv := httptest.NewServer(adminCampaignArchiveMux(pool, inv))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-archive-noinvalidate@example.com")
	seedSession(t, pool, admin, "admin-token-archive-noinvalidate")

	c := seedAdminArchiveCampaign(t, pool, mailing.CampaignStatusDraft, mailing.ArchiveStatusPending)

	resp := doJSON(t, srv.Client(), "PATCH", fmt.Sprintf("%s/admin/campaigns/%d/archive", srv.URL, c.ID),
		"admin-token-archive-noinvalidate", `{"status":"withheld"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	if inv.calls != 0 {
		t.Errorf("invalidator.calls = %d after a refused (409) transition, want 0", inv.calls)
	}
}

// Session/admin-guard coverage for this route is centralized in
// admin_wiring_test.go's TestMountAndServe_AdminRoutesRequireSessionAndAdmin,
// which enumerates adminRoutes() (including PATCH .../archive) against the
// real mux — no need to duplicate that proof here.
