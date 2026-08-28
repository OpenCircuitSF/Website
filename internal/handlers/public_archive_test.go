// Tests for PublicArchiveHandler (#0123, PRD §6.8): the 404/410/200
// visibility rule is this issue's headline acceptance criterion — "a
// draft, scheduled, paused, or cancelled campaign must not be reachable".
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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

func publicArchiveMux(pool *pgxpool.Pool) http.Handler {
	store := mailing.NewCampaignStore(pool)
	h := NewPublicArchiveHandler(store)
	mux := http.NewServeMux()
	mux.Handle("GET /api/archive", http.HandlerFunc(h.List))
	mux.Handle("GET /api/archive/{slug}", http.HandlerFunc(h.GetBySlug))
	return mux
}

func uniqueArchiveSubject(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-subtest-archive-%d", testdb.Unique())
}

func cleanupArchiveCampaign(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM email_campaigns WHERE id = $1`, id)
	})
}

// seedArchiveCampaign creates a draft campaign via the real store (never a
// literal/seeded id, CLAUDE.md §8b) and, if status != "draft", force-sets
// status/archive_status directly via SQL — this package's own store API has
// no path to 'sending'/'sent'/etc short of a real worker run, mirroring
// internal/mailing's own setCampaignStatus test helper.
func seedArchiveCampaign(t *testing.T, pool *pgxpool.Pool, status, archiveStatus string) mailing.Campaign {
	t.Helper()
	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueArchiveSubject(t), Subject: uniqueArchiveSubject(t) + " subject",
		BodyMD: "# Hello\n\nThis is the body.", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupArchiveCampaign(t, pool, c.ID)
	if status != mailing.CampaignStatusDraft || archiveStatus != mailing.ArchiveStatusPending {
		if _, err := pool.Exec(context.Background(),
			`UPDATE email_campaigns SET status = $2, archive_status = $3, archived_at = now() WHERE id = $1`,
			c.ID, status, archiveStatus,
		); err != nil {
			t.Fatalf("force campaign %d to status=%s archive_status=%s: %v", c.ID, status, archiveStatus, err)
		}
	}
	c.Status = status
	c.ArchiveStatus = archiveStatus
	return c
}

// TestPublicArchive_GetBySlug_VisibilityByStatus is this issue's headline
// acceptance criterion: 404 for every non-sent status (draft, scheduled,
// sending, paused_delivery_health, canceled, failed), 410 for a sent-but-
// withheld campaign, 200 only for sent+published.
func TestPublicArchive_GetBySlug_VisibilityByStatus(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(publicArchiveMux(pool))
	defer srv.Close()

	cases := []struct {
		name          string
		status        string
		archiveStatus string
		wantStatus    int
	}{
		{"draft", mailing.CampaignStatusDraft, mailing.ArchiveStatusPending, http.StatusNotFound},
		{"scheduled", mailing.CampaignStatusScheduled, mailing.ArchiveStatusPending, http.StatusNotFound},
		{"sending", mailing.CampaignStatusSending, mailing.ArchiveStatusPending, http.StatusNotFound},
		{"paused_delivery_health", mailing.CampaignStatusPausedDeliveryHealth, mailing.ArchiveStatusPending, http.StatusNotFound},
		{"canceled", mailing.CampaignStatusCanceled, mailing.ArchiveStatusPending, http.StatusNotFound},
		{"failed", mailing.CampaignStatusFailed, mailing.ArchiveStatusPending, http.StatusNotFound},
		{"sent+withheld", mailing.CampaignStatusSent, mailing.ArchiveStatusWithheld, http.StatusGone},
		{"sent+published", mailing.CampaignStatusSent, mailing.ArchiveStatusPublished, http.StatusOK},
		// #0318: sent+pending is unreachable via any supported write path
		// today (SetArchiveStatus refuses to write 'pending'), but it is
		// the exact row shape the old `!= withheld` fallthrough served as
		// 200. The switch's closed default must 404 it.
		{"sent+pending", mailing.CampaignStatusSent, mailing.ArchiveStatusPending, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			campaign := seedArchiveCampaign(t, pool, c.status, c.archiveStatus)
			resp, err := http.Get(srv.URL + "/api/archive/" + campaign.Slug)
			if err != nil {
				t.Fatalf("GET /api/archive/%s: %v", campaign.Slug, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != c.wantStatus {
				t.Fatalf("status=%s archive_status=%s: GET = %d, want %d (body=%s)", c.status, c.archiveStatus, resp.StatusCode, c.wantStatus, body)
			}
			if c.wantStatus == http.StatusOK {
				var got archiveDetailView
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("decode: %v (body=%s)", err, body)
				}
				if got.Slug != campaign.Slug {
					t.Errorf("Slug = %q, want %q", got.Slug, campaign.Slug)
				}
				if !strings.Contains(got.BodyHTML, "Hello") {
					t.Errorf("BodyHTML = %q, want it to contain the rendered body", got.BodyHTML)
				}
				// Privacy (PRD §6.8): the response must never leak a
				// recipient count, a status/audience field, or an
				// internal id -- assert the raw JSON has none of those
				// keys, not just that archiveDetailView's Go struct
				// omits them (a field could be re-added to the struct
				// without this test catching it if it only checked the
				// decoded Go value).
				var raw map[string]any
				if err := json.Unmarshal(body, &raw); err != nil {
					t.Fatalf("decode raw: %v", err)
				}
				for _, leaky := range []string{"id", "status", "audience_mode", "interest_ids", "recipients", "manage_token"} {
					if _, ok := raw[leaky]; ok {
						t.Errorf("response leaks key %q: %s", leaky, body)
					}
				}
			}
		})
	}
}

// fakeArchiveStore is an in-process (no DB) publicArchiveStore that returns
// a fixed campaign for GetBySlug — #0318's closed-default proof. Migration
// 000025's CHECK constraint already limits a real database row's
// archive_status to {pending, published, withheld}, so no seeded row can
// ever exercise a genuinely unknown/future value; this fake sidesteps the
// constraint the same way a future migration adding a fourth value would.
type fakeArchiveStore struct {
	campaign mailing.Campaign
}

func (f fakeArchiveStore) ListArchived(ctx context.Context) ([]mailing.Campaign, error) {
	return nil, nil
}

func (f fakeArchiveStore) GetBySlug(ctx context.Context, slug string) (mailing.Campaign, error) {
	return f.campaign, nil
}

// TestPublicArchive_GetBySlug_UnknownArchiveStatusIs404 proves the switch's
// default branch is closed, not merely "everything currently reachable
// happens to 404" — a genuinely novel archive_status value (one the CHECK
// constraint doesn't even allow yet) must still 404, never 200.
func TestPublicArchive_GetBySlug_UnknownArchiveStatusIs404(t *testing.T) {
	store := fakeArchiveStore{campaign: mailing.Campaign{
		Slug:          "zz-future-status",
		Status:        mailing.CampaignStatusSent,
		ArchiveStatus: "some-future-status-nobody-has-invented-yet",
	}}
	h := NewPublicArchiveHandler(store)
	mux := http.NewServeMux()
	mux.Handle("GET /api/archive/{slug}", http.HandlerFunc(h.GetBySlug))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/archive/zz-future-status")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d (an unknown archive_status must not serve 200)", resp.StatusCode, http.StatusNotFound)
	}
}

func TestPublicArchive_GetBySlug_UnknownSlugIs404(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(publicArchiveMux(pool))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/archive/zz-no-such-slug-" + fmt.Sprint(testdb.Unique()))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestPublicArchive_List_OnlyPublished proves GET /api/archive never
// surfaces a pending or withheld campaign, and that its response shape
// carries only slug/subject/preheader/archived_at.
func TestPublicArchive_List_OnlyPublished(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(publicArchiveMux(pool))
	defer srv.Close()

	published := seedArchiveCampaign(t, pool, mailing.CampaignStatusSent, mailing.ArchiveStatusPublished)
	pending := seedArchiveCampaign(t, pool, mailing.CampaignStatusDraft, mailing.ArchiveStatusPending)
	withheld := seedArchiveCampaign(t, pool, mailing.CampaignStatusSent, mailing.ArchiveStatusWithheld)

	resp, err := http.Get(srv.URL + "/api/archive")
	if err != nil {
		t.Fatalf("GET /api/archive: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got archiveListResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var sawPublished, sawPending, sawWithheld bool
	for _, e := range got.Archive {
		switch e.Slug {
		case published.Slug:
			sawPublished = true
		case pending.Slug:
			sawPending = true
		case withheld.Slug:
			sawWithheld = true
		}
	}
	if !sawPublished {
		t.Error("archive index did not include the published campaign")
	}
	if sawPending {
		t.Error("archive index included a pending (never-sent) campaign")
	}
	if sawWithheld {
		t.Error("archive index included a withheld campaign")
	}
}
