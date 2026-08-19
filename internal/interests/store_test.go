package interests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool connects to TEST_DATABASE_URL or skips. Unlike internal/auth's
// testPool, this does NOT truncate the interests table: migrations/000009
// seeds it with the twelve production taxonomy rows on `migrate up`, and
// tests that truncated it would either destroy that seed for the rest of the
// suite or force every other package's tests to re-seed it themselves. Tests
// here instead create rows with a slug scoped to the test (testSlug) and
// delete exactly those rows in cleanup, leaving the seed untouched.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// testSlug returns a slug unique to this test (and this run), scoped with a
// "zz-test-" prefix so it never collides with a real taxonomy slug and is
// easy to spot in a manual query. It also registers cleanup to delete any
// interests row left behind under this slug, so tests never accumulate state
// in the shared database.
func testSlug(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	slug := fmt.Sprintf("zz-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM interests WHERE slug = $1`, slug)
	})
	return slug
}

func TestListActive_ContainsSeededTaxonomy(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	got, err := store.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(got) < 12 {
		t.Fatalf("ListActive returned %d interests, want at least the 12 seeded ones", len(got))
	}

	wantSlugs := []string{
		"microcontrollers", "soldering", "homelab", "home-automation",
		"pcb-design", "sensors-iot", "robotics", "radio-rf",
		"retro-computing", "3d-printing", "test-equipment", "beginner",
	}
	bySlug := make(map[string]Interest, len(got))
	for _, it := range got {
		bySlug[it.Slug] = it
	}
	for _, slug := range wantSlugs {
		it, ok := bySlug[slug]
		if !ok {
			t.Errorf("seeded slug %q missing from ListActive", slug)
			continue
		}
		if !it.Active {
			t.Errorf("seeded slug %q returned by ListActive but Active=false", slug)
		}
	}

	// Ordering: sort_order ascending, per the seed values in
	// migrations/000009_create_interests.up.sql (10, 20, ... 120).
	first, last := bySlug["microcontrollers"], bySlug["beginner"]
	idxFirst, idxLast := -1, -1
	for i, it := range got {
		if it.Slug == first.Slug {
			idxFirst = i
		}
		if it.Slug == last.Slug {
			idxLast = i
		}
	}
	if idxFirst == -1 || idxLast == -1 || idxFirst >= idxLast {
		t.Errorf("ListActive not ordered by sort_order: microcontrollers at %d, beginner at %d", idxFirst, idxLast)
	}
}

func TestCreate_RejectsInvalidSlugFormat(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	for _, bad := range []string{"Upper-Case", "has_underscore", "trailing-", "-leading", "double--hyphen", ""} {
		_, err := store.Create(context.Background(), bad, "Bad slug", nil, 0)
		if !errors.Is(err, ErrInvalidSlug) {
			t.Errorf("Create(%q): got err=%v, want ErrInvalidSlug", bad, err)
		}
	}
}

func TestCreate_ThenGetBySlug(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	desc := "a test interest"
	created, err := store.Create(context.Background(), slug, "Test Interest", &desc, 999)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Slug != slug || created.Name != "Test Interest" || created.SortOrder != 999 || !created.Active {
		t.Fatalf("Create returned unexpected row: %+v", created)
	}

	got, err := store.GetBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("GetBySlug returned id %d, want %d", got.ID, created.ID)
	}
}

func TestCreate_DuplicateSlugRejected(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	if _, err := store.Create(context.Background(), slug, "First", nil, 0); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := store.Create(context.Background(), slug, "Second", nil, 0)
	if !errors.Is(err, ErrDuplicateSlug) {
		t.Fatalf("second Create: got err=%v, want ErrDuplicateSlug", err)
	}
}

func TestGetBySlug_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	_, err := store.GetBySlug(context.Background(), "zz-does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

func TestDeactivate_HidesFromListActiveButRowSurvives(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	created, err := store.Create(context.Background(), slug, "Will deactivate", nil, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Deactivate(context.Background(), created.ID); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	active, err := store.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	for _, it := range active {
		if it.ID == created.ID {
			t.Fatalf("deactivated interest %d still present in ListActive", created.ID)
		}
	}

	// The row itself must still exist (not deleted) — GetBySlug still resolves
	// it, and it appears in ListAll.
	got, err := store.GetBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetBySlug after deactivate: %v", err)
	}
	if got.Active {
		t.Fatalf("expected Active=false after Deactivate, got true")
	}

	all, err := store.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	found := false
	for _, it := range all {
		if it.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("deactivated interest %d missing from ListAll", created.ID)
	}
}

func TestUpdate_ChangesFieldsNotSlug(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	created, err := store.Create(context.Background(), slug, "Original name", nil, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newDesc := "updated description"
	updated, err := store.Update(context.Background(), created.ID, "New name", &newDesc, 42, false)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Slug != slug {
		t.Fatalf("Update changed slug to %q, want unchanged %q", updated.Slug, slug)
	}
	if updated.Name != "New name" || updated.SortOrder != 42 || updated.Active {
		t.Fatalf("Update did not apply expected fields: %+v", updated)
	}
}

func TestGetByIDs_IgnoresUnknownIDs(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	created, err := store.Create(context.Background(), slug, "For GetByIDs", nil, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetByIDs(context.Background(), []int64{created.ID, 99999999})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 1 || got[0].ID != created.ID {
		t.Fatalf("GetByIDs returned %+v, want exactly [%d]", got, created.ID)
	}
}

func TestGetByID_ResolvesRegardlessOfActive(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	created, err := store.Create(context.Background(), slug, "For GetByID", nil, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID || got.Slug != slug {
		t.Fatalf("GetByID = %+v, want id=%d slug=%q", got, created.ID, slug)
	}

	if err := store.Deactivate(context.Background(), created.ID); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	got, err = store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID after deactivate: %v", err)
	}
	if got.Active {
		t.Fatalf("GetByID after deactivate: Active = true, want false")
	}
}

func TestGetByID_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	_, err := store.GetByID(context.Background(), 99999999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

// seedSubscriberWithInterest inserts a minimal subscribers row and links it to
// interestID via subscriber_interests, registering cleanup that deletes the
// subscriber (subscriber_interests rows cascade via ON DELETE CASCADE, per
// migrations/000010). Returns the subscriber's id.
func seedSubscriberWithInterest(t *testing.T, pool *pgxpool.Pool, interestID int64) int64 {
	t.Helper()
	ctx := context.Background()
	email := fmt.Sprintf("zz-test-sub-%d@example.com", time.Now().UnixNano())
	var subID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO subscribers (email, manage_token) VALUES ($1, $2) RETURNING id`,
		email, fmt.Sprintf("zz-token-%d", time.Now().UnixNano()),
	).Scan(&subID); err != nil {
		t.Fatalf("seed subscriber: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, subID)
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO subscriber_interests (subscriber_id, interest_id) VALUES ($1, $2)`,
		subID, interestID,
	); err != nil {
		t.Fatalf("link subscriber to interest: %v", err)
	}
	return subID
}

func TestSubscriberCounts_CountsLinkedAndOmitsZero(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slugWith := testSlug(t, pool)
	slugWithout := testSlug(t, pool)

	withSubs, err := store.Create(context.Background(), slugWith, "Has subscribers", nil, 0)
	if err != nil {
		t.Fatalf("Create withSubs: %v", err)
	}
	without, err := store.Create(context.Background(), slugWithout, "No subscribers", nil, 0)
	if err != nil {
		t.Fatalf("Create without: %v", err)
	}
	seedSubscriberWithInterest(t, pool, withSubs.ID)
	seedSubscriberWithInterest(t, pool, withSubs.ID)

	counts, err := store.SubscriberCounts(context.Background())
	if err != nil {
		t.Fatalf("SubscriberCounts: %v", err)
	}
	if counts[withSubs.ID] != 2 {
		t.Errorf("counts[%d] = %d, want 2", withSubs.ID, counts[withSubs.ID])
	}
	if _, ok := counts[without.ID]; ok {
		t.Errorf("counts contains id %d with zero subscribers, want omitted", without.ID)
	}
}

func TestDelete_RemovesRowWithNoSubscribers(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	created, err := store.Create(context.Background(), slug, "Deletable", nil, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.GetByID(context.Background(), created.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID after Delete: got err=%v, want ErrNotFound", err)
	}
}

func TestDelete_RefusedWhenSubscribersExist(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	created, err := store.Create(context.Background(), slug, "Has a subscriber", nil, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	seedSubscriberWithInterest(t, pool, created.ID)

	err = store.Delete(context.Background(), created.ID)
	if !errors.Is(err, ErrHasSubscribers) {
		t.Fatalf("Delete: got err=%v, want ErrHasSubscribers", err)
	}

	// The row must still exist -- refusal must not have deleted it anyway.
	got, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID after refused Delete: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("GetByID after refused Delete = %+v, want id=%d", got, created.ID)
	}
}

func TestDelete_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	err := store.Delete(context.Background(), 99999999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

// --- Database-constraint tests (exercised via raw SQL, bypassing the store's
// own Go-level validation) so the migration's CHECK/UNIQUE constraints are
// what's actually under test, not just the Go layer in front of them. See
// this issue's Verification notes for the mutation proof against these two. ---

func TestDB_SlugFormatConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	for _, bad := range []string{"Upper-Case", "has_underscore", "trailing-", "double--hyphen"} {
		_, err := pool.Exec(ctx,
			`INSERT INTO interests (slug, name) VALUES ($1, 'raw insert')`, bad)
		if err == nil {
			_, _ = pool.Exec(ctx, `DELETE FROM interests WHERE slug = $1`, bad)
			t.Errorf("raw INSERT with slug %q succeeded; want interests_slug_format CHECK violation", bad)
		}
	}
}

func TestDB_SlugUniqueConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	slug := fmt.Sprintf("zz-test-unique-%d", time.Now().UnixNano())

	if _, err := pool.Exec(ctx,
		`INSERT INTO interests (slug, name) VALUES ($1, 'first')`, slug); err != nil {
		t.Fatalf("first raw insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM interests WHERE slug = $1`, slug)
	})

	_, err := pool.Exec(ctx,
		`INSERT INTO interests (slug, name) VALUES ($1, 'second')`, slug)
	if err == nil {
		t.Fatalf("second raw insert with duplicate slug %q succeeded; want UNIQUE violation", slug)
	}
}
