package crt

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// testPool returns the package's single shared pool (opened once in
// TestMain -- #0091) or skips if TEST_DATABASE_URL was unset. Does NOT
// truncate crt_commands: migrations/000028 seeds it with eighteen rows on
// `migrate up`, and tests here create rows with a slug scoped to the test
// (testSlug) and delete exactly those rows in cleanup, leaving the seed
// untouched -- same convention as internal/interests' testPool.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

// testSlug returns a slug unique to this test (and this run), scoped with a
// "zz-test-" prefix so it never collides with a real seeded slug, and
// registers cleanup to delete any crt_commands row left behind under it.
func testSlug(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	slug := fmt.Sprintf("zz-test-%d", testdb.Unique())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM crt_commands WHERE slug = $1`, slug)
	})
	return slug
}

func TestListActive_ContainsSeededDefaultSession(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	got, err := store.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(got) < 8 {
		t.Fatalf("ListActive returned %d commands, want at least the 8 seeded active ones", len(got))
	}

	wantSlugs := []string{
		"workshops-next", "whoami", "ls-tools", "cat-topics",
		"where-venues", "skill-required", "subscribe-interests", "uptime",
	}
	bySlug := make(map[string]Command, len(got))
	for _, c := range got {
		bySlug[c.Slug] = c
	}
	for _, slug := range wantSlugs {
		c, ok := bySlug[slug]
		if !ok {
			t.Errorf("seeded slug %q missing from ListActive", slug)
			continue
		}
		if !c.Active {
			t.Errorf("seeded slug %q returned by ListActive but Active=false", slug)
		}
	}

	// The ten "fun" seed rows are inactive by design (#0393's Design §5) --
	// merging the migration must change nothing on the live screen.
	for _, funSlug := range []string{"fortune", "sl", "ping-bench", "topics-subscribers"} {
		if _, present := bySlug[funSlug]; present {
			t.Errorf("inactive seed row %q appeared in ListActive", funSlug)
		}
	}

	// Ordering: sort_order ascending, per the seed values (10, 20, ... 80).
	first, last := bySlug["workshops-next"], bySlug["uptime"]
	idxFirst, idxLast := -1, -1
	for i, c := range got {
		if c.Slug == first.Slug {
			idxFirst = i
		}
		if c.Slug == last.Slug {
			idxLast = i
		}
	}
	if idxFirst == -1 || idxLast == -1 || idxFirst >= idxLast {
		t.Errorf("ListActive not ordered by sort_order: workshops-next at %d, uptime at %d", idxFirst, idxLast)
	}
}

func TestList_IncludesInactiveFunRows(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	all, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	bySlug := make(map[string]Command, len(all))
	for _, c := range all {
		bySlug[c.Slug] = c
	}
	for _, slug := range []string{"fortune", "sl", "ping-bench", "man-patience", "finger-members", "cat-motd", "topics-subscribers", "date", "ls-dev", "history-tail"} {
		c, ok := bySlug[slug]
		if !ok {
			t.Errorf("seeded fun row %q missing from List", slug)
			continue
		}
		if c.Active {
			t.Errorf("seeded fun row %q has Active=true, want false (must not change the live screen on merge)", slug)
		}
	}
	interestsRow, ok := bySlug["topics-subscribers"]
	if !ok {
		t.Fatalf("seeded row %q missing", "topics-subscribers")
	}
	if interestsRow.Source != SourceInterests {
		t.Errorf("topics-subscribers source = %q, want %q", interestsRow.Source, SourceInterests)
	}
}

func TestCreate_RejectsInvalidSlugFormat(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	for _, bad := range []string{"Upper-Case", "has_underscore", "trailing-", "-leading", "double--hyphen", ""} {
		_, err := store.Create(context.Background(), bad, "cmd", "line1", SourceStatic, 0)
		if !errors.Is(err, ErrInvalidSlug) {
			t.Errorf("Create(%q): got err=%v, want ErrInvalidSlug", bad, err)
		}
	}
}

func TestCreate_RejectsInvalidSource(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	_, err := store.Create(context.Background(), slug, "cmd", "line1", "not-a-real-source", 0)
	if !errors.Is(err, ErrInvalidSource) {
		t.Errorf("Create with bad source: got err=%v, want ErrInvalidSource", err)
	}
}

func TestCreate_ThenGetByID(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	created, err := store.Create(context.Background(), slug, "test --cmd", "line one\nline two", SourceStatic, 999)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Slug != slug || created.Command != "test --cmd" || created.SortOrder != 999 || !created.Active {
		t.Fatalf("Create returned unexpected row: %+v", created)
	}
	if created.Source != SourceStatic {
		t.Fatalf("Create default source = %q, want %q", created.Source, SourceStatic)
	}

	got, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID || got.Output != "line one\nline two" {
		t.Fatalf("GetByID returned %+v, want output %q", got, "line one\nline two")
	}
}

func TestCreate_DuplicateSlugRejected(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	if _, err := store.Create(context.Background(), slug, "cmd", "out", SourceStatic, 0); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := store.Create(context.Background(), slug, "cmd2", "out2", SourceStatic, 0)
	if !errors.Is(err, ErrDuplicateSlug) {
		t.Fatalf("second Create: got err=%v, want ErrDuplicateSlug", err)
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

func TestUpdate_ChangesFieldsNotSlug(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	created, err := store.Create(context.Background(), slug, "original", "orig out", SourceStatic, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.Update(context.Background(), created.ID, "new cmd", "new out", SourceWorkshops, 42, false)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Slug != slug {
		t.Fatalf("Update changed slug to %q, want unchanged %q", updated.Slug, slug)
	}
	if updated.Command != "new cmd" || updated.Output != "new out" || updated.Source != SourceWorkshops || updated.SortOrder != 42 || updated.Active {
		t.Fatalf("Update did not apply expected fields: %+v", updated)
	}
	if updated.UpdatedAt == nil {
		t.Errorf("Update left updated_at nil")
	}
}

func TestUpdate_RejectsInvalidSource(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	created, err := store.Create(context.Background(), slug, "cmd", "out", SourceStatic, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = store.Update(context.Background(), created.ID, "cmd", "out", "bogus", 0, true)
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("Update with bad source: got err=%v, want ErrInvalidSource", err)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	_, err := store.Update(context.Background(), 99999999, "cmd", "out", SourceStatic, 0, true)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

func TestDelete_RemovesRowUnconditionally(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	slug := testSlug(t, pool)

	created, err := store.Create(context.Background(), slug, "cmd", "out", SourceStatic, 0)
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

func TestDelete_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	err := store.Delete(context.Background(), 99999999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

func TestCommand_Lines_TrimsTrailingEmptiesOnly(t *testing.T) {
	cases := []struct {
		output string
		want   []string
	}{
		{"a\nb\nc", []string{"a", "b", "c"}},
		{"a\n\nb", []string{"a", "", "b"}}, // interior blank preserved
		{"a\nb\n\n\n", []string{"a", "b"}}, // trailing blanks trimmed
		{"", []string{}},
		{"single", []string{"single"}},
	}
	for _, tc := range cases {
		c := Command{Output: tc.output}
		got := c.Lines()
		if len(got) != len(tc.want) {
			t.Errorf("Lines(%q) = %#v, want %#v", tc.output, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("Lines(%q)[%d] = %q, want %q", tc.output, i, got[i], tc.want[i])
			}
		}
	}
}

func TestValidSource(t *testing.T) {
	for _, ok := range []string{SourceStatic, SourceWorkshops, SourceListStats, SourceInterests} {
		if !ValidSource(ok) {
			t.Errorf("ValidSource(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "STATIC", "bogus", "workshop"} {
		if ValidSource(bad) {
			t.Errorf("ValidSource(%q) = true, want false", bad)
		}
	}
}
