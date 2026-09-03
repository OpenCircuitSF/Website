// Handler-level tests for #0405's opt-in newsletter-month slug template:
// POST /admin/campaigns' newsletter_month field, and the non-newsletter
// proof — that the announce-to-list shortcut (#0056) can never consume a
// newsletter's month slug, since it builds its own CampaignInput and never
// sets NewsletterMonth (admin_workshop_announce.go). A new file, deliberately,
// so admin_campaigns_test.go's own tests are provably unedited.
//
// The database-backed tests here use month 2031-03 (slug "03-2031") — far
// from any real campaign and from #0404's production backfill on id 1
// (issues/0405.md §6: that production literal must never appear under
// internal/ or web/src/).
package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

func TestAdminCampaigns_Create_NewsletterMonthMintsTemplateSlug(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-newsletter-mint@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-newsletter-mint")

	name := uniqueAdminCampaignName(t)
	body := fmt.Sprintf(`{"name":%q,"subject":"March update","body_md":"# Hello","audience_mode":"all","newsletter_month":"2031-03"}`, name)
	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/campaigns", "admin-token-campaigns-newsletter-mint", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	c := decodeCampaign(t, readBody(t, resp))
	cleanupAdminCampaign(t, pool, c.ID)

	if c.Slug != "03-2031" {
		t.Errorf("Slug = %q, want %q", c.Slug, "03-2031")
	}
}

func TestAdminCampaigns_Create_OmittedNewsletterMonthMintsFromSubject(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-newsletter-omit@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-newsletter-omit")

	name := uniqueAdminCampaignName(t)
	body := fmt.Sprintf(`{"name":%q,"subject":"Plain Subject Line","body_md":"# Hello","audience_mode":"all"}`, name)
	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/campaigns", "admin-token-campaigns-newsletter-omit", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	c := decodeCampaign(t, readBody(t, resp))
	cleanupAdminCampaign(t, pool, c.ID)

	if c.Slug != "plain-subject-line" {
		t.Errorf("Slug = %q, want %q (the wire default is unchanged when newsletter_month is absent)", c.Slug, "plain-subject-line")
	}
}

func TestAdminCampaigns_Create_InvalidNewsletterMonthReturns400(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-campaigns-newsletter-invalid@example.com")
	seedSession(t, pool, admin, "admin-token-campaigns-newsletter-invalid")

	name := uniqueAdminCampaignName(t)
	body := fmt.Sprintf(`{"name":%q,"subject":"s","body_md":"b","audience_mode":"all","newsletter_month":"2031-13"}`, name)
	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/campaigns", "admin-token-campaigns-newsletter-invalid", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_campaigns WHERE name = $1`, name).Scan(&count); err != nil {
		t.Fatalf("count check: %v", err)
	}
	if count != 0 {
		t.Errorf("email_campaigns row count = %d, want 0 (rejected create must not write a row)", count)
	}
}

// newsletterSlugShape matches #0405's MM-YYYY template, with or without the
// Create collision suffix (-2, -3, ...) — used only to assert an
// announcement's slug does NOT have this shape. The suffix is optional in
// the pattern because a workshop announcement that consumed the newsletter's
// month AND got suffixed (e.g. "03-2031-2") is exactly the same defect as
// one that took the unsuffixed slug outright; a pattern anchored to the
// unsuffixed shape alone would miss that case (issues/0405.md's review
// notes, commit 21d6d0c).
var newsletterSlugShape = regexp.MustCompile(`^[0-9]{2}-[0-9]{4}(-[0-9]+)?$`)

// TestAdminWorkshopAnnounce_DoesNotConsumeTheNewsletterMonthSlug is
// issues/0405.md criterion 6's sharpest requirement: create a 2031-03
// newsletter (slug 03-2031), THEN announce a workshop, and assert BOTH
// halves — the announcement gets its own subject-derived slug, not the
// month's, AND the newsletter's slug is still exactly 03-2031, unsuffixed.
// Asserting only the first half would miss the actual failure this issue
// exists to prevent: an announcement silently consuming the month's slug
// and pushing the real newsletter to 03-2031-2.
func TestAdminWorkshopAnnounce_DoesNotConsumeTheNewsletterMonthSlug(t *testing.T) {
	pool := interestsTestPool(t)
	campaignsSrv := httptest.NewServer(adminCampaignsMux(pool, nil))
	defer campaignsSrv.Close()
	announceSrv := httptest.NewServer(adminWorkshopAnnounceMux(pool))
	defer announceSrv.Close()

	admin := seedAdmin(t, pool, "admin-announce-no-consume@example.com")
	seedSession(t, pool, admin, "announce-no-consume-token")

	newsletterName := uniqueAdminCampaignName(t)
	createBody := fmt.Sprintf(`{"name":%q,"subject":"March newsletter","body_md":"# Hello","audience_mode":"all","newsletter_month":"2031-03"}`, newsletterName)
	createResp := doJSON(t, campaignsSrv.Client(), "POST", campaignsSrv.URL+"/admin/campaigns", "announce-no-consume-token", createBody)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create newsletter status = %d, want 201 (body=%s)", createResp.StatusCode, readBody(t, createResp))
	}
	newsletter := decodeCampaign(t, readBody(t, createResp))
	cleanupAdminCampaign(t, pool, newsletter.ID)
	if newsletter.Slug != "03-2031" {
		t.Fatalf("newsletter.Slug = %q, want %q before the announce even happens", newsletter.Slug, "03-2031")
	}

	wkStore := workshops.NewStore(pool)
	title := fmt.Sprintf("zz-subtest-workshop-%d", testdb.Unique())
	wk, err := wkStore.Create(context.Background(), workshops.CreateInput{Title: title})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, wk.ID)
	})

	announceResp := doJSON(t, announceSrv.Client(), http.MethodPost,
		fmt.Sprintf("%s/admin/workshops/%d/announce", announceSrv.URL, wk.ID), "announce-no-consume-token", "")
	announceBody, _ := io.ReadAll(announceResp.Body)
	announceResp.Body.Close()
	if announceResp.StatusCode != http.StatusCreated {
		t.Fatalf("announce status = %d, want 201 (body=%s)", announceResp.StatusCode, announceBody)
	}
	announced := decodeCampaign(t, announceBody)
	cleanupAdminCampaign(t, pool, announced.ID)

	if newsletterSlugShape.MatchString(announced.Slug) {
		t.Errorf("announced campaign's slug = %q, matches the MM-YYYY template shape — it must not consume a newsletter month", announced.Slug)
	}

	reGet := doJSON(t, campaignsSrv.Client(), "GET", fmt.Sprintf("%s/admin/campaigns/%d", campaignsSrv.URL, newsletter.ID), "announce-no-consume-token", "")
	if reGet.StatusCode != http.StatusOK {
		t.Fatalf("re-fetch newsletter status = %d, want 200 (body=%s)", reGet.StatusCode, readBody(t, reGet))
	}
	refetched := decodeCampaign(t, readBody(t, reGet))
	if refetched.Slug != "03-2031" {
		t.Errorf("newsletter.Slug after the announce = %q, want unchanged %q (unsuffixed)", refetched.Slug, "03-2031")
	}
}
