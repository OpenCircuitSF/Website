package outreach

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// The case list below is mirrored by hand in web/src/lib/outreachLocations.test.ts
// (no shared fixture file: that would make this package a repo-wide reacher).
func TestValidCode(t *testing.T) {
	valid := []string{"ab", "a1", "mission-1", "abcdefghijklmnop", "a-b-c"}
	invalid := []string{"a", "abcdefghijklmnopq", "Ab", "-ab", "ab-", "a--b", "a_b", "a b", "é", ""}
	for _, c := range valid {
		if !ValidCode(c) {
			t.Errorf("ValidCode(%q) = false, want true", c)
		}
	}
	for _, c := range invalid {
		if ValidCode(c) {
			t.Errorf("ValidCode(%q) = true, want false", c)
		}
	}
}

func uniqueLocCode() string { return "z" + strconv.FormatInt(testdb.Unique(), 36) }

func seedLocation(t *testing.T, s *Store) Location {
	t.Helper()
	l, err := s.CreateLocation(context.Background(), LocationInput{Name: "Test Board", Code: uniqueLocCode(), Active: true})
	if err != nil {
		t.Fatalf("create location: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM outreach_locations WHERE id = $1`, l.ID)
	})
	return l
}

func TestLocationCreateListAndDuplicate(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	a := seedLocation(t, s)
	if a.CodeLockedAt != nil || !a.Active {
		t.Fatalf("fresh location: %+v", a)
	}
	if _, err := s.CreateLocation(ctx, LocationInput{Name: "Dup", Code: a.Code}); !errors.Is(err, ErrCodeTaken) {
		t.Errorf("duplicate err = %v, want ErrCodeTaken", err)
	}
	// Ordering: an inactive location sorts after every active one.
	b := seedLocation(t, s)
	f := false
	if _, err := s.UpdateLocation(ctx, a.ID, LocationPatch{Active: &f}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListLocations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ia, ib := -1, -1
	for i, l := range list {
		switch l.ID {
		case a.ID:
			ia = i
		case b.ID:
			ib = i
		}
	}
	if ia < 0 || ib < 0 || ia < ib {
		t.Errorf("inactive %d should sort after active %d", ia, ib)
	}
}

func TestLocationCheckConstraintRejectsBadCode(t *testing.T) {
	s := NewStore(testPool(t))
	for _, bad := range []string{"A1", "a", "a--b", "-ab", strings.Repeat("a", 17)} {
		_, err := s.pool.Exec(context.Background(), `INSERT INTO outreach_locations (name, code) VALUES ('x', $1)`, bad)
		if err == nil {
			t.Errorf("raw insert of code %q succeeded; CHECK should reject it", bad)
			_, _ = s.pool.Exec(context.Background(), `DELETE FROM outreach_locations WHERE code = $1`, bad)
		}
	}
}

func TestLocationUpdateAndLock(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	l := seedLocation(t, s)

	newCode := uniqueLocCode()
	got, err := s.UpdateLocation(ctx, l.ID, LocationPatch{Code: &newCode})
	if err != nil || got.Code != newCode || got.UpdatedAt == nil {
		t.Fatalf("unlocked code change: %+v %v", got, err)
	}

	if err := s.MarkLocationUsed(ctx, s.pool, l.ID); err != nil {
		t.Fatal(err)
	}
	first, _ := s.GetLocation(ctx, l.ID)
	if first.CodeLockedAt == nil {
		t.Fatal("not locked after MarkLocationUsed")
	}
	if err := s.MarkLocationUsed(ctx, s.pool, l.ID); err != nil {
		t.Fatal(err)
	}
	second, _ := s.GetLocation(ctx, l.ID)
	if !second.CodeLockedAt.Equal(*first.CodeLockedAt) {
		t.Errorf("second mark moved the timestamp: %v -> %v", first.CodeLockedAt, second.CodeLockedAt)
	}

	other := uniqueLocCode()
	if _, err := s.UpdateLocation(ctx, l.ID, LocationPatch{Code: &other}); !errors.Is(err, ErrCodeLocked) {
		t.Errorf("locked code change err = %v, want ErrCodeLocked", err)
	}
	if _, err := s.UpdateLocation(ctx, l.ID, LocationPatch{Code: &newCode, Name: strp("Renamed"), Notes: strp("n")}); err != nil {
		t.Errorf("same-code patch on locked row: %v", err)
	}
	f := false
	if got, err := s.UpdateLocation(ctx, l.ID, LocationPatch{Active: &f}); err != nil || got.Active {
		t.Errorf("deactivate locked: %+v %v", got, err)
	}
	if err := s.DeleteLocation(ctx, l.ID); !errors.Is(err, ErrLocationInUse) {
		t.Errorf("delete locked err = %v, want ErrLocationInUse", err)
	}
}

func TestLocationUpdateCodeCollision(t *testing.T) {
	s := NewStore(testPool(t))
	a, b := seedLocation(t, s), seedLocation(t, s)
	if _, err := s.UpdateLocation(context.Background(), a.ID, LocationPatch{Code: &b.Code}); !errors.Is(err, ErrCodeTaken) {
		t.Errorf("err = %v, want ErrCodeTaken", err)
	}
}

func TestLocationDeleteUnlocked(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	l := seedLocation(t, s)
	if err := s.DeleteLocation(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetLocation(ctx, l.ID); !errors.Is(err, ErrLocationNotFound) {
		t.Errorf("after delete err = %v", err)
	}
}

func TestLocationUnknownID(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	var id int64
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) + 1000000 FROM outreach_locations`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetLocation(ctx, id); !errors.Is(err, ErrLocationNotFound) {
		t.Errorf("Get err = %v", err)
	}
	if _, err := s.UpdateLocation(ctx, id, LocationPatch{Name: strp("x")}); !errors.Is(err, ErrLocationNotFound) {
		t.Errorf("Update err = %v", err)
	}
	if err := s.DeleteLocation(ctx, id); !errors.Is(err, ErrLocationNotFound) {
		t.Errorf("Delete err = %v", err)
	}
	if err := s.MarkLocationUsed(ctx, s.pool, id); !errors.Is(err, ErrLocationNotFound) {
		t.Errorf("Mark err = %v", err)
	}
}

func TestMarkLocationUsedRollsBackWithTx(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	l := seedLocation(t, s)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkLocationUsed(ctx, tx, l.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetLocation(ctx, l.ID)
	if got.CodeLockedAt != nil {
		t.Error("rolled-back mark left the location locked")
	}
}
