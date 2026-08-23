package workshops

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// seedInterest inserts a throwaway interests row for workshop_interests
// linking tests, scoped with a "zz-test-" slug and cleaned up via
// t.Cleanup — never a literal/seeded id, per CLAUDE.md §8b.
func seedInterest(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	slug := fmt.Sprintf("zz-test-workshop-interest-%d", testdb.Unique())
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO interests (slug, name) VALUES ($1, $2) RETURNING id`,
		slug, "Test Interest",
	).Scan(&id); err != nil {
		t.Fatalf("seed interest: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM interests WHERE id = $1`, id)
	})
	return id
}

func uniqueTitle(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("Test Workshop %d", testdb.Unique())
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{"Soldering Basics", "soldering-basics"},
		{"  Leading/Trailing Spaces  ", "leading-trailing-spaces"},
		{"C++ for Microcontrollers!", "c-for-microcontrollers"},
		{"3D Printing 101", "3d-printing-101"},
		{"!!!", "workshop"},
		{"", "workshop"},
	}
	for _, c := range cases {
		got := slugify(c.title)
		if got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

// TestCreate_SlugCollisionGetsDistinctSuffix is #0051's binding
// carried-in acceptance criterion from #0050's review: two workshops
// sharing a title must get distinct slugs, the second via a numeric
// collision suffix.
func TestCreate_SlugCollisionGetsDistinctSuffix(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	title := uniqueTitle(t)
	wantBase := slugify(title)

	first, err := store.Create(ctx, CreateInput{Title: title})
	if err != nil {
		t.Fatalf("Create (first): %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, first.ID) })
	if first.Slug != wantBase {
		t.Fatalf("first.Slug = %q, want %q", first.Slug, wantBase)
	}

	second, err := store.Create(ctx, CreateInput{Title: title})
	if err != nil {
		t.Fatalf("Create (second): %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, second.ID) })
	wantSecond := wantBase + "-2"
	if second.Slug != wantSecond {
		t.Fatalf("second.Slug = %q, want %q", second.Slug, wantSecond)
	}
	if second.Slug == first.Slug {
		t.Fatalf("second workshop got the same slug as the first: %q", second.Slug)
	}

	// A third with the same title takes the next suffix, proving the
	// collision loop keeps advancing rather than getting stuck retrying "-2".
	third, err := store.Create(ctx, CreateInput{Title: title})
	if err != nil {
		t.Fatalf("Create (third): %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, third.ID) })
	if third.Slug != wantBase+"-3" {
		t.Fatalf("third.Slug = %q, want %q", third.Slug, wantBase+"-3")
	}
}

func TestCreate_EmptyTitleRejected(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	_, err := store.Create(context.Background(), CreateInput{Title: "   "})
	if !errors.Is(err, ErrTitleRequired) {
		t.Fatalf("Create with blank title: err = %v, want ErrTitleRequired", err)
	}
}

func TestCreate_WithInterests(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	i1 := seedInterest(t, pool)
	i2 := seedInterest(t, pool)

	w, err := store.Create(ctx, CreateInput{Title: uniqueTitle(t), InterestIDs: []int64{i1, i2, i1}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, w.ID) })

	if len(w.InterestIDs) != 2 {
		t.Fatalf("InterestIDs = %v, want 2 deduped ids", w.InterestIDs)
	}

	reloaded, err := store.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(reloaded.InterestIDs) != 2 {
		t.Fatalf("reloaded.InterestIDs = %v, want 2", reloaded.InterestIDs)
	}
	if w.Status != StatusDraft {
		t.Fatalf("new workshop Status = %q, want %q", w.Status, StatusDraft)
	}
}

// TestCreate_UnknownInterestRejected is #0134's atomicity acceptance
// criterion, proved the way the #0051 reviewer proved the bug: call Create
// with an unknown interest id, assert the error, then assert count(*) = 0
// for the slug it would have taken — no ghost workshop row left behind, and
// the slug is free for a subsequent real Create to claim.
func TestCreate_UnknownInterestRejected(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	title := uniqueTitle(t)
	wantSlug := slugify(title)

	_, err := store.Create(ctx, CreateInput{Title: title, InterestIDs: []int64{999999999}})
	if !errors.Is(err, ErrInterestNotFound) {
		t.Fatalf("Create with unknown interest id: err = %v, want ErrInterestNotFound", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workshops WHERE slug = $1`, wantSlug).Scan(&count); err != nil {
		t.Fatalf("counting workshops with slug %q: %v", wantSlug, err)
	}
	if count != 0 {
		t.Fatalf("count(*) for slug %q = %d, want 0 (ghost row left behind by a non-atomic Create)", wantSlug, count)
	}

	// The slug must also be free for a real, valid Create to claim — proof
	// that nothing (not even a half-committed row) survived to squat on it.
	recovered, err := store.Create(ctx, CreateInput{Title: title})
	if err != nil {
		t.Fatalf("Create after the failed attempt: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, recovered.ID) })
	if recovered.Slug != wantSlug {
		t.Fatalf("recovered.Slug = %q, want %q (the base slug, unclaimed)", recovered.Slug, wantSlug)
	}
}

func createTestWorkshop(t *testing.T, store *Store, title string) Workshop {
	t.Helper()
	w, err := store.Create(context.Background(), CreateInput{Title: title})
	if err != nil {
		t.Fatalf("seed workshop %q: %v", title, err)
	}
	t.Cleanup(func() { _, _ = store.pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, w.ID) })
	return w
}

// TestUpdate_PublishStampsPublishedAt covers the first half of #0051's own
// acceptance criterion "Publish and unpublish transitions stamp
// published_at".
func TestUpdate_PublishStampsPublishedAt(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	w := createTestWorkshop(t, store, uniqueTitle(t))
	if w.PublishedAt != nil {
		t.Fatalf("freshly created workshop PublishedAt = %v, want nil", w.PublishedAt)
	}

	updated, err := store.Update(ctx, w.ID, UpdateInput{Title: w.Title, Status: StatusPublished})
	if err != nil {
		t.Fatalf("Update to published: %v", err)
	}
	if updated.PublishedAt == nil {
		t.Fatalf("PublishedAt is nil after publishing, want a timestamp")
	}
	if updated.Status != StatusPublished {
		t.Fatalf("Status = %q, want %q", updated.Status, StatusPublished)
	}
}

// TestUpdate_UnpublishClearsPublishedAt covers the second half of the same
// criterion: published -> draft clears published_at back to NULL.
func TestUpdate_UnpublishClearsPublishedAt(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	w := createTestWorkshop(t, store, uniqueTitle(t))
	published, err := store.Update(ctx, w.ID, UpdateInput{Title: w.Title, Status: StatusPublished})
	if err != nil {
		t.Fatalf("Update to published: %v", err)
	}
	if published.PublishedAt == nil {
		t.Fatalf("PublishedAt is nil after publishing")
	}

	unpublished, err := store.Update(ctx, w.ID, UpdateInput{Title: w.Title, Status: StatusDraft})
	if err != nil {
		t.Fatalf("Update to draft (unpublish): %v", err)
	}
	if unpublished.PublishedAt != nil {
		t.Fatalf("PublishedAt = %v after unpublishing, want nil", unpublished.PublishedAt)
	}
}

// TestUpdate_CancelPreservesPublishedAt: a published -> canceled transition
// leaves published_at untouched (the package doc comment's documented
// design choice — cancel is not unpublish).
func TestUpdate_CancelPreservesPublishedAt(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	w := createTestWorkshop(t, store, uniqueTitle(t))
	published, err := store.Update(ctx, w.ID, UpdateInput{Title: w.Title, Status: StatusPublished})
	if err != nil {
		t.Fatalf("Update to published: %v", err)
	}
	if published.PublishedAt == nil {
		t.Fatalf("PublishedAt is nil after publishing")
	}

	canceled, err := store.Update(ctx, w.ID, UpdateInput{Title: w.Title, Status: StatusCanceled})
	if err != nil {
		t.Fatalf("Update to canceled: %v", err)
	}
	if canceled.Status != StatusCanceled {
		t.Fatalf("Status = %q, want %q", canceled.Status, StatusCanceled)
	}
	if canceled.PublishedAt == nil {
		t.Fatalf("PublishedAt cleared on cancel, want it preserved")
	}
	if !canceled.PublishedAt.Equal(*published.PublishedAt) {
		t.Fatalf("PublishedAt changed on cancel: got %v, want unchanged %v", canceled.PublishedAt, published.PublishedAt)
	}
}

func TestUpdate_UnknownStatusRejected(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	w := createTestWorkshop(t, store, uniqueTitle(t))
	_, err := store.Update(context.Background(), w.ID, UpdateInput{Title: w.Title, Status: "bogus"})
	if !errors.Is(err, ErrUnknownStatus) {
		t.Fatalf("Update with unknown status: err = %v, want ErrUnknownStatus", err)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	_, err := store.Update(context.Background(), 999999999, UpdateInput{Title: "x", Status: StatusDraft})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update nonexistent id: err = %v, want ErrNotFound", err)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	_, err := store.GetByID(context.Background(), 999999999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID nonexistent id: err = %v, want ErrNotFound", err)
	}
}

func TestGetBySlug_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	_, err := store.GetBySlug(context.Background(), "zz-does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBySlug nonexistent slug: err = %v, want ErrNotFound", err)
	}
}

// TestListVisible_SplitsUpcomingAndPast is #0051's own acceptance
// criterion, widened by #0135: GET /api/workshops (backed by ListVisible)
// splits published-or-canceled workshops into upcoming and past, and
// excludes draft entirely.
func TestListVisible_SplitsUpcomingAndPast(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	now := time.Now().UTC()

	future := now.Add(48 * time.Hour)
	past := now.Add(-48 * time.Hour)

	mk := func(title string, startsAt *time.Time, status string) Workshop {
		w, err := store.Create(ctx, CreateInput{Title: title, StartsAt: startsAt})
		if err != nil {
			t.Fatalf("seed %q: %v", title, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, w.ID) })
		if status != StatusDraft {
			if _, err := store.Update(ctx, w.ID, UpdateInput{Title: w.Title, StartsAt: startsAt, Status: status}); err != nil {
				t.Fatalf("transition %q to %s: %v", title, status, err)
			}
		}
		return w
	}

	upcomingW := mk(uniqueTitle(t)+" upcoming", &future, StatusPublished)
	pastW := mk(uniqueTitle(t)+" past", &past, StatusPublished)
	draftW := mk(uniqueTitle(t)+" draft", &future, StatusDraft)
	tbdW := mk(uniqueTitle(t)+" tbd", nil, StatusPublished)
	canceledUpcomingW := mk(uniqueTitle(t)+" canceled upcoming", &future, StatusCanceled)
	canceledPastW := mk(uniqueTitle(t)+" canceled past", &past, StatusCanceled)

	gotUpcoming, gotPast, err := store.ListVisible(ctx, now)
	if err != nil {
		t.Fatalf("ListVisible: %v", err)
	}

	upcomingIDs := map[int64]bool{}
	for _, w := range gotUpcoming {
		upcomingIDs[w.ID] = true
		if w.Status != StatusPublished && w.Status != StatusCanceled {
			t.Errorf("upcoming contains draft workshop %d (status=%s)", w.ID, w.Status)
		}
	}
	if !upcomingIDs[upcomingW.ID] {
		t.Errorf("upcoming workshop %d missing from upcoming list", upcomingW.ID)
	}
	if !upcomingIDs[tbdW.ID] {
		t.Errorf("TBD (nil starts_at) published workshop %d missing from upcoming list", tbdW.ID)
	}
	if !upcomingIDs[canceledUpcomingW.ID] {
		t.Errorf("canceled-but-upcoming workshop %d missing from upcoming list", canceledUpcomingW.ID)
	}
	if upcomingIDs[pastW.ID] {
		t.Errorf("past workshop %d incorrectly in upcoming list", pastW.ID)
	}
	if upcomingIDs[draftW.ID] {
		t.Errorf("draft workshop %d leaked into upcoming list", draftW.ID)
	}

	pastIDs := map[int64]bool{}
	for _, w := range gotPast {
		pastIDs[w.ID] = true
		if w.Status != StatusPublished && w.Status != StatusCanceled {
			t.Errorf("past contains draft workshop %d (status=%s)", w.ID, w.Status)
		}
	}
	if !pastIDs[pastW.ID] {
		t.Errorf("past workshop %d missing from past list", pastW.ID)
	}
	if !pastIDs[canceledPastW.ID] {
		t.Errorf("canceled-and-past workshop %d missing from past list", canceledPastW.ID)
	}
	if pastIDs[upcomingW.ID] || pastIDs[tbdW.ID] || pastIDs[canceledUpcomingW.ID] {
		t.Errorf("upcoming/TBD workshop incorrectly in past list")
	}
	if pastIDs[draftW.ID] {
		t.Errorf("draft workshop %d leaked into past list", draftW.ID)
	}
}

// TestDelete_BlockedByReferencingCampaign is #0051's binding carried-in
// acceptance criterion from #0050's review: DELETE must map SQLSTATE 23503
// on email_campaigns_workshop_id_fkey to a typed error the handler turns
// into 409, never a bare 500.
func TestDelete_BlockedByReferencingCampaign(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	w := createTestWorkshop(t, store, uniqueTitle(t))

	var campaignID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO email_campaigns (name, subject, body_md, audience_mode, workshop_id)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		fmt.Sprintf("zz-test-campaign-%d", testdb.Unique()), "Subject", "Body", "all", w.ID,
	).Scan(&campaignID); err != nil {
		t.Fatalf("seed referencing campaign: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM email_campaigns WHERE id = $1`, campaignID)
	})

	err := store.Delete(ctx, w.ID)
	if !errors.Is(err, ErrHasCampaigns) {
		t.Fatalf("Delete blocked-by-campaign: err = %v, want ErrHasCampaigns", err)
	}

	// Confirm the row genuinely survives the refused delete.
	if _, gerr := store.GetByID(ctx, w.ID); gerr != nil {
		t.Fatalf("workshop %d missing after refused delete: %v", w.ID, gerr)
	}
}

func TestDelete_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	err := store.Delete(context.Background(), 999999999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete nonexistent id: err = %v, want ErrNotFound", err)
	}
}

func TestDelete_Succeeds(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	w := createTestWorkshop(t, store, uniqueTitle(t))
	if err := store.Delete(context.Background(), w.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.GetByID(context.Background(), w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID after delete: err = %v, want ErrNotFound", err)
	}
}
