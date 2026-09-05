package mailing

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// uniqueCampaignName returns a nanosecond-suffixed name unique to this call,
// following internal/subscribers' uniqueEmail(t) convention (CLAUDE.md §8b:
// every test seeds its own throwaway rows, never targets a literal id).
func uniqueCampaignName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-mailing-test-%d", testdb.Unique())
}

// cleanupCampaign deletes a campaign row (cascading to campaign_interests and
// email_sends per migration 000017's ON DELETE CASCADE) at test end.
func cleanupCampaign(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM email_campaigns WHERE id = $1`, id)
	})
}

// seedInterest creates a throwaway interest with a zz-mailing- slug prefix,
// cleaned up at test end — never a seeded production taxonomy row.
func seedInterest(t *testing.T, pool *pgxpool.Pool) interests.Interest {
	t.Helper()
	store := interests.NewStore(pool)
	slug := fmt.Sprintf("zz-mailing-test-%d", testdb.Unique())
	it, err := store.Create(context.Background(), slug, "Mailing test interest", nil, 0)
	if err != nil {
		t.Fatalf("seed interest: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM interests WHERE id = $1`, it.ID)
	})
	return it
}

// setCampaignStatus force-sets a campaign's status directly via SQL — used
// only to put a fixture into a state this package's own API cannot reach
// (e.g. 'sending', which only #0045's future worker writes), so tests can
// exercise the edit-lock and transition guards against it. Never targets a
// literal or seeded id — id is always one this test created.
func setCampaignStatus(t *testing.T, pool *pgxpool.Pool, id int64, status string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET status = $2 WHERE id = $1`, id, status); err != nil {
		t.Fatalf("force campaign %d to status %q: %v", id, status, err)
	}
}

// ── Create ───────────────────────────────────────────────────────────────────

func TestCampaignStore_Create_Basic(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	name := uniqueCampaignName(t)

	c, err := store.Create(context.Background(), CampaignInput{
		Name:         name,
		Subject:      "Test subject",
		BodyMD:       "# Hello",
		AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	if c.Status != CampaignStatusDraft {
		t.Errorf("Status = %q, want %q", c.Status, CampaignStatusDraft)
	}
	if c.Name != name {
		t.Errorf("Name = %q, want %q", c.Name, name)
	}
	if len(c.InterestIDs) != 0 {
		t.Errorf("InterestIDs = %v, want empty", c.InterestIDs)
	}
}

func TestCampaignStore_Create_AnyOfRequiresInterests(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	name := uniqueCampaignName(t)

	_, err := store.Create(context.Background(), CampaignInput{
		Name:         name,
		Subject:      "Test subject",
		BodyMD:       "# Hello",
		AudienceMode: AudienceAnyOf,
		InterestIDs:  nil,
	})
	if !errors.Is(err, ErrCampaignInterestsRequired) {
		t.Fatalf("err = %v, want ErrCampaignInterestsRequired", err)
	}

	// The guard this test names: no row was written.
	var count int
	if scanErr := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_campaigns WHERE name = $1`, name).Scan(&count); scanErr != nil {
		t.Fatalf("count check: %v", scanErr)
	}
	if count != 0 {
		t.Errorf("email_campaigns row count = %d, want 0 (rejected create must not write a row)", count)
	}
}

func TestCampaignStore_Create_AllOfRequiresInterests(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	name := uniqueCampaignName(t)

	_, err := store.Create(context.Background(), CampaignInput{
		Name:         name,
		Subject:      "Test subject",
		BodyMD:       "# Hello",
		AudienceMode: AudienceAllOf,
		InterestIDs:  []int64{},
	})
	if !errors.Is(err, ErrCampaignInterestsRequired) {
		t.Fatalf("err = %v, want ErrCampaignInterestsRequired", err)
	}
}

func TestCampaignStore_Create_UnknownAudienceMode(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)

	_, err := store.Create(context.Background(), CampaignInput{
		Name:         uniqueCampaignName(t),
		Subject:      "Test subject",
		BodyMD:       "# Hello",
		AudienceMode: "sometimes",
	})
	if !errors.Is(err, ErrUnknownAudienceMode) {
		t.Fatalf("err = %v, want ErrUnknownAudienceMode", err)
	}
}

func TestCampaignStore_Create_WithInterests(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	it1 := seedInterest(t, pool)
	it2 := seedInterest(t, pool)

	c, err := store.Create(context.Background(), CampaignInput{
		Name:         uniqueCampaignName(t),
		Subject:      "Test subject",
		BodyMD:       "# Hello",
		AudienceMode: AudienceAnyOf,
		InterestIDs:  []int64{it1.ID, it2.ID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	if len(c.InterestIDs) != 2 {
		t.Fatalf("InterestIDs = %v, want 2 ids", c.InterestIDs)
	}

	// GetByID must agree.
	got, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.InterestIDs) != 2 {
		t.Errorf("GetByID InterestIDs = %v, want 2 ids", got.InterestIDs)
	}
}

func TestCampaignStore_Create_DedupesInterestIDs(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	it := seedInterest(t, pool)

	c, err := store.Create(context.Background(), CampaignInput{
		Name:         uniqueCampaignName(t),
		Subject:      "Test subject",
		BodyMD:       "# Hello",
		AudienceMode: AudienceAnyOf,
		InterestIDs:  []int64{it.ID, it.ID, it.ID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	if len(c.InterestIDs) != 1 {
		t.Fatalf("InterestIDs = %v, want exactly 1 (deduped)", c.InterestIDs)
	}
}

func TestCampaignStore_Create_UnknownInterestID(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	name := uniqueCampaignName(t)

	_, err := store.Create(context.Background(), CampaignInput{
		Name:         name,
		Subject:      "Test subject",
		BodyMD:       "# Hello",
		AudienceMode: AudienceAnyOf,
		InterestIDs:  []int64{99999999},
	})
	if !errors.Is(err, ErrCampaignInterestNotFound) {
		t.Fatalf("err = %v, want ErrCampaignInterestNotFound", err)
	}

	// The transaction must have rolled back entirely — no orphaned campaign row.
	var count int
	if scanErr := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_campaigns WHERE name = $1`, name).Scan(&count); scanErr != nil {
		t.Fatalf("count check: %v", scanErr)
	}
	if count != 0 {
		t.Errorf("email_campaigns row count = %d, want 0 (failed create must roll back)", count)
	}
}

// ── GetByID / List ───────────────────────────────────────────────────────────

func TestCampaignStore_GetByID_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)

	_, err := store.GetByID(context.Background(), 99999999)
	if !errors.Is(err, ErrCampaignNotFound) {
		t.Fatalf("err = %v, want ErrCampaignNotFound", err)
	}
}

func TestCampaignStore_List_IncludesCreated(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	name := uniqueCampaignName(t)

	c, err := store.Create(context.Background(), CampaignInput{
		Name: name, Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	list, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, item := range list {
		if item.ID == c.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("List did not include the campaign just created (id=%d)", c.ID)
	}
}

// ── Update ───────────────────────────────────────────────────────────────────

func TestCampaignStore_Update_Basic(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "old subject", BodyMD: "old body", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	updated, err := store.Update(context.Background(), c.ID, CampaignUpdate{
		Name: c.Name, Subject: "new subject", BodyMD: "new body", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Subject != "new subject" || updated.BodyMD != "new body" {
		t.Errorf("Update did not apply: subject=%q body=%q", updated.Subject, updated.BodyMD)
	}
}

func TestCampaignStore_Update_EmptyInterestSetRejected(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	it := seedInterest(t, pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b",
		AudienceMode: AudienceAnyOf, InterestIDs: []int64{it.ID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	_, err = store.Update(context.Background(), c.ID, CampaignUpdate{
		Name: c.Name, Subject: "s2", BodyMD: "b2",
		AudienceMode: AudienceAllOf, InterestIDs: nil,
	})
	if !errors.Is(err, ErrCampaignInterestsRequired) {
		t.Fatalf("err = %v, want ErrCampaignInterestsRequired", err)
	}

	// The guard this test names: the row must be untouched by the rejected update.
	got, gerr := store.GetByID(context.Background(), c.ID)
	if gerr != nil {
		t.Fatalf("GetByID after rejected update: %v", gerr)
	}
	if got.Subject != "s" || got.AudienceMode != AudienceAnyOf {
		t.Errorf("rejected update mutated the row: subject=%q audience_mode=%q, want unchanged", got.Subject, got.AudienceMode)
	}
}

func TestCampaignStore_Update_NotEditableWhenSending(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)
	setCampaignStatus(t, pool, c.ID, CampaignStatusSending)

	_, err = store.Update(context.Background(), c.ID, CampaignUpdate{
		Name: c.Name, Subject: "changed", BodyMD: "changed", AudienceMode: AudienceAll,
	})
	if !errors.Is(err, ErrCampaignNotEditable) {
		t.Fatalf("err = %v, want ErrCampaignNotEditable", err)
	}
}

func TestCampaignStore_Update_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)

	_, err := store.Update(context.Background(), 99999999, CampaignUpdate{
		Name: "x", Subject: "x", BodyMD: "x", AudienceMode: AudienceAll,
	})
	if !errors.Is(err, ErrCampaignNotFound) {
		t.Fatalf("err = %v, want ErrCampaignNotFound", err)
	}
}

// ── Send ─────────────────────────────────────────────────────────────────────

func TestCampaignStore_Send_DraftToScheduled(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	when := time.Now().Add(time.Hour)
	sent, err := store.Send(context.Background(), c.ID, when)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.Status != CampaignStatusScheduled {
		t.Errorf("Status = %q, want %q", sent.Status, CampaignStatusScheduled)
	}
	if sent.ScheduledAt == nil || !sent.ScheduledAt.Equal(when) {
		t.Errorf("ScheduledAt = %v, want %v", sent.ScheduledAt, when)
	}
}

func TestCampaignStore_Send_FailedToScheduled(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)
	setCampaignStatus(t, pool, c.ID, CampaignStatusFailed)

	sent, err := store.Send(context.Background(), c.ID, time.Now())
	if err != nil {
		t.Fatalf("Send (retry from failed): %v", err)
	}
	if sent.Status != CampaignStatusScheduled {
		t.Errorf("Status = %q, want %q", sent.Status, CampaignStatusScheduled)
	}
}

func TestCampaignStore_Send_IllegalFromNonDraftNonFailed(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)

	for _, status := range []string{
		CampaignStatusScheduled, CampaignStatusSending, CampaignStatusSent, CampaignStatusCanceled,
	} {
		t.Run(status, func(t *testing.T) {
			c, err := store.Create(context.Background(), CampaignInput{
				Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			cleanupCampaign(t, pool, c.ID)
			setCampaignStatus(t, pool, c.ID, status)

			_, err = store.Send(context.Background(), c.ID, time.Now())
			if !errors.Is(err, ErrIllegalStatusTransition) {
				t.Fatalf("from %q: err = %v, want ErrIllegalStatusTransition", status, err)
			}

			// The guard this test names: the row's status must be unchanged.
			got, gerr := store.GetByID(context.Background(), c.ID)
			if gerr != nil {
				t.Fatalf("GetByID after rejected Send: %v", gerr)
			}
			if got.Status != status {
				t.Errorf("status after rejected Send = %q, want unchanged %q", got.Status, status)
			}
		})
	}
}

func TestCampaignStore_Send_RequiresScheduleTime(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	_, err = store.Send(context.Background(), c.ID, time.Time{})
	if !errors.Is(err, ErrCampaignScheduleTimeRequired) {
		t.Fatalf("err = %v, want ErrCampaignScheduleTimeRequired", err)
	}
}

func TestCampaignStore_Send_DefensiveAudienceCheck(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	it := seedInterest(t, pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b",
		AudienceMode: AudienceAnyOf, InterestIDs: []int64{it.ID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	// Simulate a row that somehow reached an invalid state outside this
	// package's guarded write paths (direct SQL) — Send must still refuse it.
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM campaign_interests WHERE campaign_id = $1`, c.ID); err != nil {
		t.Fatalf("force-clear campaign_interests: %v", err)
	}

	_, err = store.Send(context.Background(), c.ID, time.Now())
	if !errors.Is(err, ErrCampaignInterestsRequired) {
		t.Fatalf("err = %v, want ErrCampaignInterestsRequired (defensive re-check)", err)
	}
}

// ── Cancel ───────────────────────────────────────────────────────────────────

func TestCampaignStore_Cancel_ScheduledToCanceled(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)
	if _, err := store.Send(context.Background(), c.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	canceled, err := store.Cancel(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if canceled.Status != CampaignStatusCanceled {
		t.Errorf("Status = %q, want %q", canceled.Status, CampaignStatusCanceled)
	}
}

func TestCampaignStore_Cancel_SendingToCanceled(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)
	setCampaignStatus(t, pool, c.ID, CampaignStatusSending)

	canceled, err := store.Cancel(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if canceled.Status != CampaignStatusCanceled {
		t.Errorf("Status = %q, want %q", canceled.Status, CampaignStatusCanceled)
	}
}

func TestCampaignStore_Cancel_IllegalFromDraft(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	_, err = store.Cancel(context.Background(), c.ID)
	if !errors.Is(err, ErrIllegalStatusTransition) {
		t.Fatalf("err = %v, want ErrIllegalStatusTransition (draft is not cancellable)", err)
	}

	got, gerr := store.GetByID(context.Background(), c.ID)
	if gerr != nil {
		t.Fatalf("GetByID: %v", gerr)
	}
	if got.Status != CampaignStatusDraft {
		t.Errorf("status after rejected Cancel = %q, want unchanged %q", got.Status, CampaignStatusDraft)
	}
}

func TestCampaignStore_Cancel_IllegalFromSent(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)
	setCampaignStatus(t, pool, c.ID, CampaignStatusSent)

	_, err = store.Cancel(context.Background(), c.ID)
	if !errors.Is(err, ErrIllegalStatusTransition) {
		t.Fatalf("err = %v, want ErrIllegalStatusTransition (sent cannot be cancelled/recalled)", err)
	}
}

func TestCampaignStore_Cancel_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)

	_, err := store.Cancel(context.Background(), 99999999)
	if !errors.Is(err, ErrCampaignNotFound) {
		t.Fatalf("err = %v, want ErrCampaignNotFound", err)
	}
}

// ── Delete (#0411) ───────────────────────────────────────────────────────────

func TestCampaignStore_Delete_DraftSucceeds(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Delete(context.Background(), c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, gerr := store.GetByID(context.Background(), c.ID); !errors.Is(gerr, ErrCampaignNotFound) {
		t.Fatalf("GetByID after delete: err = %v, want ErrCampaignNotFound", gerr)
	}
}

// TestCampaignStore_Delete_FreesSlugForReuse is #0411's central acceptance
// criterion made concrete: deleting a draft must genuinely release its slug,
// not merely hide the row. Proved by minting the SAME subject twice —
// Create's collision-suffix retry (this file's Create, "-2" on a taken slug)
// would silently mask a slug that was never actually freed, so the only
// real proof is that the SECOND campaign, created after the first is
// deleted, gets the identical bare slug back rather than a "-2" suffix.
func TestCampaignStore_Delete_FreesSlugForReuse(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	subject := fmt.Sprintf("Zz Repeat Subject %d", testdb.Unique())

	first, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: subject, BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}

	if err := store.Delete(context.Background(), first.ID); err != nil {
		t.Fatalf("Delete first: %v", err)
	}

	second, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: subject, BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	cleanupCampaign(t, pool, second.ID)

	if second.Slug != first.Slug {
		t.Fatalf("second.Slug = %q, want %q (the first campaign's slug, reused) — the delete did not genuinely free it",
			second.Slug, first.Slug)
	}
}

// TestCampaignStore_Delete_CascadesReferencingRows proves the referential
// integrity Delete's own doc comment claims: campaign_interests and
// email_sends both carry ON DELETE CASCADE into email_campaigns (migration
// 000017), so a draft's DELETE must remove both without a foreign-key
// violation. A still-draft campaign can never legitimately have an
// email_sends row through this package's own API (materialization only
// happens once a send starts — #0044), so this test creates one directly by
// SQL — CLAUDE.md §8b: the referencing row is seeded, never a literal id,
// and it is this test's own throwaway campaign, not a shared fixture.
func TestCampaignStore_Delete_CascadesReferencingRows(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	it := seedInterest(t, pool)

	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO campaign_interests (campaign_id, interest_id) VALUES ($1, $2)`,
		c.ID, it.ID,
	); err != nil {
		t.Fatalf("seed campaign_interests: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO email_sends (campaign_id, subscriber_id, email, status) VALUES ($1, NULL, $2, 'queued')`,
		c.ID, fmt.Sprintf("zz-mailing-test-%d@example.com", testdb.Unique()),
	); err != nil {
		t.Fatalf("seed email_sends: %v", err)
	}

	if err := store.Delete(ctx, c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var interestCount, sendCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM campaign_interests WHERE campaign_id = $1`, c.ID).Scan(&interestCount); err != nil {
		t.Fatalf("count campaign_interests: %v", err)
	}
	if interestCount != 0 {
		t.Errorf("campaign_interests rows remaining = %d, want 0 (should cascade)", interestCount)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_sends WHERE campaign_id = $1`, c.ID).Scan(&sendCount); err != nil {
		t.Fatalf("count email_sends: %v", err)
	}
	if sendCount != 0 {
		t.Errorf("email_sends rows remaining = %d, want 0 (should cascade)", sendCount)
	}
}

// TestCampaignStore_Delete_RefusedWhenNotDraft is #0411's other acceptance
// criterion: every status except draft must refuse with a clear, typed
// error and must leave the row untouched — never a silent no-op, and never
// a delete that quietly succeeds against a status whose slug may already be
// promoted or whose archive page may already be public.
func TestCampaignStore_Delete_RefusedWhenNotDraft(t *testing.T) {
	statuses := []string{
		CampaignStatusScheduled,
		CampaignStatusSending,
		CampaignStatusPausedDeliveryHealth,
		CampaignStatusSent,
		CampaignStatusCanceled,
		CampaignStatusFailed,
	}
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			pool := testPool(t)
			store := NewCampaignStore(pool)
			c, err := store.Create(context.Background(), CampaignInput{
				Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			cleanupCampaign(t, pool, c.ID)
			setCampaignStatus(t, pool, c.ID, status)

			err = store.Delete(context.Background(), c.ID)
			if !errors.Is(err, ErrCampaignNotDeletable) {
				t.Fatalf("Delete from %q: err = %v, want ErrCampaignNotDeletable", status, err)
			}

			got, gerr := store.GetByID(context.Background(), c.ID)
			if gerr != nil {
				t.Fatalf("GetByID after refused delete: %v", gerr)
			}
			if got.Status != status {
				t.Errorf("status after refused Delete = %q, want unchanged %q", got.Status, status)
			}
		})
	}
}

func TestCampaignStore_Delete_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)

	err := store.Delete(context.Background(), 99999999)
	if !errors.Is(err, ErrCampaignNotFound) {
		t.Fatalf("err = %v, want ErrCampaignNotFound", err)
	}
}
