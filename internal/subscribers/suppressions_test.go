package subscribers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func uniqueSuppressionEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-suppress-%d@example.com", time.Now().UnixNano())
}

func TestSuppressionStore_Add_NormalizesEmailAndPersistsFields(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	raw := fmt.Sprintf("  ZZ-Suppress-Mixed-%s@Example.com  ", suffix)
	want := fmt.Sprintf("zz-suppress-mixed-%s@example.com", suffix)

	sup, err := store.Add(context.Background(), NewSuppression{
		Email:  raw,
		Reason: SuppressionReasonManual,
		Note:   "requested by phone",
	}, now)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if sup.Email != want {
		t.Errorf("Email = %q, want normalized %q", sup.Email, want)
	}
	if sup.Reason != SuppressionReasonManual {
		t.Errorf("Reason = %q, want %q", sup.Reason, SuppressionReasonManual)
	}
	if sup.Note == nil || *sup.Note != "requested by phone" {
		t.Errorf("Note = %v, want \"requested by phone\"", sup.Note)
	}
	if !sup.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", sup.CreatedAt, now)
	}
}

// TestSuppressionStore_Add_IdempotentKeepsOriginalRow proves Add's documented
// idempotency contract: a second Add for an already-suppressed address does
// not overwrite the first row's reason/note/created_at, and returns no
// error.
func TestSuppressionStore_Add_IdempotentKeepsOriginalRow(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	email := uniqueSuppressionEmail(t)
	firstNow := time.Now().UTC().Truncate(time.Second)

	first, err := store.Add(context.Background(), NewSuppression{
		Email:  email,
		Reason: SuppressionReasonComplaint,
		Note:   "first record",
	}, firstNow)
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}

	second, err := store.Add(context.Background(), NewSuppression{
		Email:  email,
		Reason: SuppressionReasonManual,
		Note:   "a later admin suppress must not replace the complaint record",
	}, firstNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("second Add (idempotent case): %v", err)
	}

	if second.Reason != SuppressionReasonComplaint {
		t.Errorf("second Add's Reason = %q, want original %q (idempotent Add must not overwrite)", second.Reason, first.Reason)
	}
	if second.Note == nil || *second.Note != "first record" {
		t.Errorf("second Add's Note = %v, want original %q", second.Note, "first record")
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("second Add's CreatedAt = %v, want original %v", second.CreatedAt, first.CreatedAt)
	}
}

func TestSuppressionStore_Add_InvalidReasonRejected(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)

	_, err := store.Add(context.Background(), NewSuppression{
		Email:  uniqueSuppressionEmail(t),
		Reason: "not_a_real_reason",
	}, time.Now())
	if !errors.Is(err, ErrInvalidSuppressionReason) {
		t.Errorf("err = %v, want ErrInvalidSuppressionReason", err)
	}
}

// TestSuppressionStore_AddTx_RollsBackWithTransaction proves AddTx honors
// the transaction it is given — the property #0038 depends on to write its
// email_events row and its suppression row atomically (see the package doc
// comment and issues/0033.md's "Criterion added 2026-08-19"). Mirrors
// internal/audit's TestWriteTx_RollsBackWithTransaction.
func TestSuppressionStore_AddTx_RollsBackWithTransaction(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	ctx := context.Background()
	email := uniqueSuppressionEmail(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if _, err := store.AddTx(ctx, tx, NewSuppression{
		Email:  email,
		Reason: SuppressionReasonHardBounce,
	}, time.Now()); err != nil {
		t.Fatalf("AddTx: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	suppressed, err := store.IsSuppressed(ctx, email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Errorf("IsSuppressed = true after rollback, want false — AddTx did not honor the transaction it was given")
	}
}

// TestSuppressionStore_AddTx_CommitsWithTransaction is the paired positive
// control: the same call, but committed instead of rolled back, must
// actually persist.
func TestSuppressionStore_AddTx_CommitsWithTransaction(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	ctx := context.Background()
	email := uniqueSuppressionEmail(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if _, err := store.AddTx(ctx, tx, NewSuppression{
		Email:  email,
		Reason: SuppressionReasonHardBounce,
	}, time.Now()); err != nil {
		t.Fatalf("AddTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	suppressed, err := store.IsSuppressed(ctx, email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Errorf("IsSuppressed = false after commit, want true")
	}
}

func TestSuppressionStore_IsSuppressed_FalseWhenNeverAdded(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)

	suppressed, err := store.IsSuppressed(context.Background(), uniqueSuppressionEmail(t))
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Errorf("IsSuppressed = true for an address never added, want false")
	}
}

// TestSuppressionStore_IsSuppressed_TrueRegardlessOfSubscriberStatus is
// #0033's acceptance criterion "go test ./internal/subscribers/... covers
// suppression checks against every status": suppression is deliberately
// independent of subscriber status (see the package doc comment and PRD
// §6.2 — "checked before EVERY send, regardless of subscriber status"), so
// this proves IsSuppressed reports true for a suppressed address no matter
// what status (or absence of any) subscribers row exists for that same
// email. Carried in from #0025's review: "do not rely on subscriber status
// as the sole send gate."
func TestSuppressionStore_IsSuppressed_TrueRegardlessOfSubscriberStatus(t *testing.T) {
	pool := testPool(t)
	subs := NewStore(pool)
	suppressions := NewSuppressionStore(pool)
	ctx := context.Background()
	now := time.Now()

	statuses := []string{
		StatusPending,
		StatusActive,
		StatusUnsubscribed,
		StatusBounced,
		StatusComplained,
		"no_subscriber_row", // sentinel: no subscribers row exists at all
	}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			email := uniqueSuppressionEmail(t)

			if status != "no_subscriber_row" {
				sub, err := subs.Create(ctx, NewSignup{Email: email, ConfirmTTL: time.Hour}, now)
				if err != nil {
					t.Fatalf("seed Create: %v", err)
				}
				if status != StatusPending {
					// setStatus is unexported but every one of these
					// statuses is reachable through it; reach it via the
					// package-level raw UPDATE instead of duplicating
					// per-status store-method plumbing that isn't this
					// test's concern — only the resulting status value
					// matters here, not how a real caller would reach it.
					if _, err := pool.Exec(ctx, `UPDATE subscribers SET status = $2 WHERE id = $1`, sub.ID, status); err != nil {
						t.Fatalf("seed status %q: %v", status, err)
					}
				}
			}

			if _, err := suppressions.Add(ctx, NewSuppression{
				Email:  email,
				Reason: SuppressionReasonManual,
				Note:   "status coverage",
			}, now); err != nil {
				t.Fatalf("Add: %v", err)
			}

			suppressed, err := suppressions.IsSuppressed(ctx, email)
			if err != nil {
				t.Fatalf("IsSuppressed: %v", err)
			}
			if !suppressed {
				t.Errorf("IsSuppressed = false for a suppressed address with subscriber status %q, want true", status)
			}
		})
	}
}

// TestSuppressionStore_SurvivesSubscriberHardDeletion is #0033's acceptance
// criterion "Hard deletion of a subscriber (#0060) leaves the suppression
// row in place": suppressions carries no foreign key to subscribers (see
// the package doc comment), so a raw DELETE of the subscribers row — what
// #0060's eventual hard-erasure path will do — must not touch the
// suppressions row for the same email.
func TestSuppressionStore_SurvivesSubscriberHardDeletion(t *testing.T) {
	pool := testPool(t)
	subs := NewStore(pool)
	suppressions := NewSuppressionStore(pool)
	ctx := context.Background()
	now := time.Now()
	email := uniqueSuppressionEmail(t)

	sub, err := subs.Create(ctx, NewSignup{Email: email, ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	if _, err := suppressions.Add(ctx, NewSuppression{
		Email:  email,
		Reason: SuppressionReasonHardBounce,
	}, now); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// #0060 hasn't landed yet; a raw DELETE is the closest available stand-in
	// for "the subscriber row is hard-deleted" and is exactly what this test
	// needs to prove — that no FK/cascade exists from subscribers to
	// suppressions.
	if _, err := pool.Exec(ctx, `DELETE FROM subscribers WHERE id = $1`, sub.ID); err != nil {
		t.Fatalf("hard-delete subscriber: %v", err)
	}

	suppressed, err := suppressions.IsSuppressed(ctx, email)
	if err != nil {
		t.Fatalf("IsSuppressed after hard deletion: %v", err)
	}
	if !suppressed {
		t.Errorf("IsSuppressed = false after the subscriber row was hard-deleted, want true — the suppression row must survive")
	}
}

func TestSuppressionStore_List_ReturnsNewestFirst(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	ctx := context.Background()

	older := uniqueSuppressionEmail(t)
	time.Sleep(2 * time.Millisecond) // guarantee distinct created_at ordering
	newer := uniqueSuppressionEmail(t)

	base := time.Now().UTC().Truncate(time.Second)
	if _, err := store.Add(ctx, NewSuppression{Email: older, Reason: SuppressionReasonManual}, base); err != nil {
		t.Fatalf("Add older: %v", err)
	}
	if _, err := store.Add(ctx, NewSuppression{Email: newer, Reason: SuppressionReasonManual}, base.Add(time.Minute)); err != nil {
		t.Fatalf("Add newer: %v", err)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	idxOlder, idxNewer := -1, -1
	for i, sup := range list {
		if sup.Email == older {
			idxOlder = i
		}
		if sup.Email == newer {
			idxNewer = i
		}
	}
	if idxOlder == -1 || idxNewer == -1 {
		t.Fatalf("List did not contain both seeded rows: older found=%v newer found=%v", idxOlder != -1, idxNewer != -1)
	}
	if idxNewer >= idxOlder {
		t.Errorf("List order: newer row at index %d, older at %d — want newer first (ORDER BY created_at DESC)", idxNewer, idxOlder)
	}
}

func TestSuppressionStore_Remove_DeletesAndIsNotFoundOnSecondCall(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	ctx := context.Background()
	email := uniqueSuppressionEmail(t)

	if _, err := store.Add(ctx, NewSuppression{Email: email, Reason: SuppressionReasonManual}, time.Now()); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := store.Remove(ctx, email); err != nil {
		t.Fatalf("first Remove: %v", err)
	}

	suppressed, err := store.IsSuppressed(ctx, email)
	if err != nil {
		t.Fatalf("IsSuppressed after Remove: %v", err)
	}
	if suppressed {
		t.Errorf("IsSuppressed = true after Remove, want false")
	}

	if err := store.Remove(ctx, email); !errors.Is(err, ErrSuppressionNotFound) {
		t.Errorf("second Remove err = %v, want ErrSuppressionNotFound", err)
	}
}

// TestSuppressionStore_Remove_NormalizesEmail proves Remove normalizes its
// argument the same way Add does — a differently-cased/whitespaced Remove
// call for an address added via Add must still find and delete the row.
func TestSuppressionStore_Remove_NormalizesEmail(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	normalized := fmt.Sprintf("zz-suppress-remove-norm-%s@example.com", suffix)
	decorated := fmt.Sprintf("  ZZ-Suppress-Remove-Norm-%s@Example.com  ", suffix)

	if _, err := store.Add(ctx, NewSuppression{Email: normalized, Reason: SuppressionReasonManual}, time.Now()); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := store.Remove(ctx, decorated); err != nil {
		t.Fatalf("Remove with decorated email: %v", err)
	}

	suppressed, err := store.IsSuppressed(ctx, normalized)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Errorf("IsSuppressed = true after Remove, want false")
	}
}
