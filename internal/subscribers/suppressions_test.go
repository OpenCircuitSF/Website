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

// TestSuppressionStore_Add_IdempotentPerReason proves Add's documented
// idempotency contract post-#0100: a second Add for the SAME (email, reason)
// pair does not overwrite the first row's note/created_at and returns no
// error, and adds no additional row. (The old contract — idempotent per
// email alone, so a different reason no-opped — is exactly what #0100
// closes; see TestSuppressionStore_Add_TwoReasonsForOneEmailCoexist for the
// replacement behavior on a DIFFERENT reason.)
func TestSuppressionStore_Add_IdempotentPerReason(t *testing.T) {
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
		Reason: SuppressionReasonComplaint,
		Note:   "a repeat of the same reason must not replace the original note",
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

	rows, err := store.ListByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("ListByEmail returned %d rows, want 1 — a repeat Add for the same reason must not insert a second row", len(rows))
	}
}

// TestSuppressionStore_Add_TwoReasonsForOneEmailCoexist is #0100's central
// behavior change: Add's idempotency key is (email, reason), not email
// alone, so a second, DIFFERENT reason for an already-suppressed address
// inserts a second row rather than no-opping. This is exactly what lets an
// address that both hard-bounced and complained retain both facts.
func TestSuppressionStore_Add_TwoReasonsForOneEmailCoexist(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	ctx := context.Background()
	email := uniqueSuppressionEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	if _, err := store.Add(ctx, NewSuppression{
		Email:  email,
		Reason: SuppressionReasonHardBounce,
	}, now); err != nil {
		t.Fatalf("Add hard_bounce: %v", err)
	}
	if _, err := store.Add(ctx, NewSuppression{
		Email:  email,
		Reason: SuppressionReasonComplaint,
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("Add complaint: %v", err)
	}

	rows, err := store.ListByEmail(ctx, email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListByEmail returned %d rows, want 2 (hard_bounce and complaint coexisting)", len(rows))
	}
	var sawHardBounce, sawComplaint bool
	for _, row := range rows {
		switch row.Reason {
		case SuppressionReasonHardBounce:
			sawHardBounce = true
		case SuppressionReasonComplaint:
			sawComplaint = true
		}
	}
	if !sawHardBounce || !sawComplaint {
		t.Errorf("rows = %+v, want both hard_bounce and complaint present", rows)
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

// TestSuppressionStore_IsSuppressed_TrueForEveryReason proves IsSuppressed
// stays reason-blind (#0100 §3): a row of ANY one of the four reasons blocks
// the address, independently, each on its own throwaway address.
func TestSuppressionStore_IsSuppressed_TrueForEveryReason(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	ctx := context.Background()

	reasons := []string{
		SuppressionReasonHardBounce,
		SuppressionReasonComplaint,
		SuppressionReasonManual,
		SuppressionReasonRepeatedSoftBounce,
	}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			email := uniqueSuppressionEmail(t)
			if _, err := store.Add(ctx, NewSuppression{Email: email, Reason: reason}, time.Now()); err != nil {
				t.Fatalf("Add: %v", err)
			}
			suppressed, err := store.IsSuppressed(ctx, email)
			if err != nil {
				t.Fatalf("IsSuppressed: %v", err)
			}
			if !suppressed {
				t.Errorf("IsSuppressed = false for a row with reason %q, want true", reason)
			}
		})
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

// TestSuppressionStore_List_JoinsSubscriberStatus is #0100 §4/§5: one
// suppressed address with a live subscribers row (SubscriberStatus
// non-nil, equal to that row's status) and one orphaned suppression with no
// subscribers row at all (SubscriberStatus nil) — the LEFT JOIN's two
// branches. Also proves the ORDER BY email ASC grouping: a second reason for
// the WITH-subscriber address sorts adjacent to its first.
func TestSuppressionStore_List_JoinsSubscriberStatus(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	subs := NewStore(pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	withSub := uniqueSuppressionEmail(t)
	orphan := uniqueSuppressionEmail(t)

	sub, err := subs.Create(ctx, NewSignup{Email: withSub, ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("seed subscriber: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subscribers SET status = $2 WHERE id = $1`, sub.ID, StatusUnsubscribed); err != nil {
		t.Fatalf("seed status: %v", err)
	}

	if _, err := store.Add(ctx, NewSuppression{Email: withSub, Reason: SuppressionReasonManual}, now); err != nil {
		t.Fatalf("Add manual for withSub: %v", err)
	}
	if _, err := store.Add(ctx, NewSuppression{Email: withSub, Reason: SuppressionReasonHardBounce}, now.Add(time.Minute)); err != nil {
		t.Fatalf("Add hard_bounce for withSub: %v", err)
	}
	if _, err := store.Add(ctx, NewSuppression{Email: orphan, Reason: SuppressionReasonManual}, now); err != nil {
		t.Fatalf("Add manual for orphan: %v", err)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var withSubRows, orphanRows []SuppressionListItem
	withSubIndices := map[int]bool{}
	for i, item := range list {
		switch item.Email {
		case withSub:
			withSubRows = append(withSubRows, item)
			withSubIndices[i] = true
		case orphan:
			orphanRows = append(orphanRows, item)
		}
	}

	if len(withSubRows) != 2 {
		t.Fatalf("withSub rows = %d, want 2", len(withSubRows))
	}
	for _, item := range withSubRows {
		if item.SubscriberStatus == nil || *item.SubscriberStatus != StatusUnsubscribed {
			t.Errorf("withSub row SubscriberStatus = %v, want %q", item.SubscriberStatus, StatusUnsubscribed)
		}
	}
	if len(orphanRows) != 1 {
		t.Fatalf("orphan rows = %d, want 1", len(orphanRows))
	}
	if orphanRows[0].SubscriberStatus != nil {
		t.Errorf("orphan row SubscriberStatus = %v, want nil", *orphanRows[0].SubscriberStatus)
	}

	// email ASC grouping: withSub's two rows must be adjacent in the result.
	var indices []int
	for i := range withSubIndices {
		indices = append(indices, i)
	}
	if len(indices) == 2 {
		diff := indices[0] - indices[1]
		if diff != 1 && diff != -1 {
			t.Errorf("withSub's two rows are not adjacent (indices %v) — ORDER BY email ASC grouping broken", indices)
		}
	}
}

// TestSuppressionStore_ListByEmail_NormalizesEmail proves ListByEmail
// normalizes its argument the same way every other lookup in this package
// does — a differently-cased/whitespaced call must still find rows added via
// Add's own normalized form.
func TestSuppressionStore_ListByEmail_NormalizesEmail(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	normalized := fmt.Sprintf("zz-suppress-listbyemail-%s@example.com", suffix)
	decorated := fmt.Sprintf("  ZZ-Suppress-ListByEmail-%s@Example.com  ", suffix)

	if _, err := store.Add(ctx, NewSuppression{Email: normalized, Reason: SuppressionReasonManual}, time.Now()); err != nil {
		t.Fatalf("Add: %v", err)
	}

	rows, err := store.ListByEmail(ctx, decorated)
	if err != nil {
		t.Fatalf("ListByEmail with decorated email: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListByEmail returned %d rows, want 1", len(rows))
	}
	if rows[0].Email != normalized {
		t.Errorf("row Email = %q, want normalized %q", rows[0].Email, normalized)
	}
}

// TestSuppressionStore_Remove_ScopedByReason_LeavesHardBounceInForce is
// #0100's central correctness case: an address suppressed for BOTH
// hard_bounce and complaint must survive a complaint-only removal with the
// hard_bounce suppression still in force. Run in both insertion orders — the
// old single-row model only failed in one of them (complaint-then-hard_bounce,
// where the bounce Add no-opped and Remove("complaint") deleted the only
// row) — so a test that happens to pick the lucky order proves nothing.
func TestSuppressionStore_Remove_ScopedByReason_LeavesHardBounceInForce(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	run := func(t *testing.T, hardBounceFirst bool) {
		store := NewSuppressionStore(pool)
		email := uniqueSuppressionEmail(t)
		now := time.Now()

		if hardBounceFirst {
			if _, err := store.Add(ctx, NewSuppression{Email: email, Reason: SuppressionReasonHardBounce}, now); err != nil {
				t.Fatalf("Add hard_bounce: %v", err)
			}
			if _, err := store.Add(ctx, NewSuppression{Email: email, Reason: SuppressionReasonComplaint}, now.Add(time.Minute)); err != nil {
				t.Fatalf("Add complaint: %v", err)
			}
		} else {
			if _, err := store.Add(ctx, NewSuppression{Email: email, Reason: SuppressionReasonComplaint}, now); err != nil {
				t.Fatalf("Add complaint: %v", err)
			}
			if _, err := store.Add(ctx, NewSuppression{Email: email, Reason: SuppressionReasonHardBounce}, now.Add(time.Minute)); err != nil {
				t.Fatalf("Add hard_bounce: %v", err)
			}
		}

		if _, err := store.Remove(ctx, email, SuppressionReasonComplaint); err != nil {
			t.Fatalf("Remove(complaint): %v", err)
		}

		suppressed, err := store.IsSuppressed(ctx, email)
		if err != nil {
			t.Fatalf("IsSuppressed: %v", err)
		}
		if !suppressed {
			t.Fatalf("IsSuppressed = false after removing only the complaint reason, want true — the hard_bounce suppression must survive")
		}

		rows, err := store.ListByEmail(ctx, email)
		if err != nil {
			t.Fatalf("ListByEmail: %v", err)
		}
		if len(rows) != 1 || rows[0].Reason != SuppressionReasonHardBounce {
			t.Errorf("ListByEmail = %+v, want exactly one hard_bounce row", rows)
		}
	}

	t.Run("hard_bounce_then_complaint", func(t *testing.T) { run(t, true) })
	t.Run("complaint_then_hard_bounce", func(t *testing.T) { run(t, false) })
}

// TestSuppressionStore_Remove_NotFoundWhenReasonDoesNotMatch proves Remove
// is scoped by reason, not just email: removing a reason that was never
// recorded for this address returns ErrSuppressionNotFound and leaves the
// existing row untouched.
func TestSuppressionStore_Remove_NotFoundWhenReasonDoesNotMatch(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	ctx := context.Background()
	email := uniqueSuppressionEmail(t)

	if _, err := store.Add(ctx, NewSuppression{Email: email, Reason: SuppressionReasonHardBounce}, time.Now()); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, err := store.Remove(ctx, email, SuppressionReasonComplaint); !errors.Is(err, ErrSuppressionNotFound) {
		t.Errorf("Remove(complaint) err = %v, want ErrSuppressionNotFound", err)
	}

	suppressed, err := store.IsSuppressed(ctx, email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Errorf("IsSuppressed = false, want true — the hard_bounce row must survive a mismatched-reason Remove")
	}
}

// TestSuppressionStore_Remove_InvalidReasonRejected proves Remove validates
// its reason argument the same way Add does, and deletes nothing on a bad
// value.
func TestSuppressionStore_Remove_InvalidReasonRejected(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	ctx := context.Background()
	email := uniqueSuppressionEmail(t)

	if _, err := store.Add(ctx, NewSuppression{Email: email, Reason: SuppressionReasonManual}, time.Now()); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, err := store.Remove(ctx, email, "not_a_real_reason"); !errors.Is(err, ErrInvalidSuppressionReason) {
		t.Errorf("Remove err = %v, want ErrInvalidSuppressionReason", err)
	}

	suppressed, err := store.IsSuppressed(ctx, email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Errorf("IsSuppressed = false after a rejected Remove, want true — nothing should have been deleted")
	}
}

// TestSuppressionStore_Remove_ReturnsDeletedRow proves Remove returns the
// deleted row's note/created_at, since the row is destroyed and a caller's
// audit entry is the only remaining record of that provenance.
func TestSuppressionStore_Remove_ReturnsDeletedRow(t *testing.T) {
	pool := testPool(t)
	store := NewSuppressionStore(pool)
	ctx := context.Background()
	email := uniqueSuppressionEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	if _, err := store.Add(ctx, NewSuppression{
		Email:  email,
		Reason: SuppressionReasonManual,
		Note:   "requested by phone",
	}, now); err != nil {
		t.Fatalf("Add: %v", err)
	}

	deleted, err := store.Remove(ctx, email, SuppressionReasonManual)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if deleted.Note == nil || *deleted.Note != "requested by phone" {
		t.Errorf("deleted.Note = %v, want %q", deleted.Note, "requested by phone")
	}
	if !deleted.CreatedAt.Equal(now) {
		t.Errorf("deleted.CreatedAt = %v, want %v", deleted.CreatedAt, now)
	}
	if deleted.Reason != SuppressionReasonManual {
		t.Errorf("deleted.Reason = %q, want %q", deleted.Reason, SuppressionReasonManual)
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

	if _, err := store.Remove(ctx, email, SuppressionReasonManual); err != nil {
		t.Fatalf("first Remove: %v", err)
	}

	suppressed, err := store.IsSuppressed(ctx, email)
	if err != nil {
		t.Fatalf("IsSuppressed after Remove: %v", err)
	}
	if suppressed {
		t.Errorf("IsSuppressed = true after Remove, want false")
	}

	if _, err := store.Remove(ctx, email, SuppressionReasonManual); !errors.Is(err, ErrSuppressionNotFound) {
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

	if _, err := store.Remove(ctx, decorated, SuppressionReasonManual); err != nil {
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
