package subscribers

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestListPending_ExcludesSyntheticAndNonPending(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	pending, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create pending: %v", err)
	}
	synthetic, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour, Synthetic: true}, now)
	if err != nil {
		t.Fatalf("Create synthetic: %v", err)
	}
	activeSub, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create active: %v", err)
	}
	if _, err := store.Confirm(context.Background(), *activeSub.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	rows, err := store.ListPending(context.Background(), true)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}

	seen := map[int64]bool{}
	for _, r := range rows {
		seen[r.ID] = true
	}
	if !seen[pending.ID] {
		t.Errorf("ListPending missing genuinely pending subscriber %d", pending.ID)
	}
	if seen[synthetic.ID] {
		t.Errorf("ListPending unexpectedly includes synthetic subscriber %d", synthetic.ID)
	}
	if seen[activeSub.ID] {
		t.Errorf("ListPending unexpectedly includes confirmed (active) subscriber %d", activeSub.ID)
	}
}

// TestListPending_SortOrder proves oldestFirst actually drives the ORDER BY
// direction, not just that the query runs — a table-driven property, with
// two subscribers whose confirm_sent_at is forced apart so ascending and
// descending genuinely disagree on which comes first.
func TestListPending_SortOrder(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	older, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Create older: %v", err)
	}
	newer, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create newer: %v", err)
	}

	indexOf := func(rows []Subscriber, id int64) int {
		for i, r := range rows {
			if r.ID == id {
				return i
			}
		}
		return -1
	}

	asc, err := store.ListPending(context.Background(), true)
	if err != nil {
		t.Fatalf("ListPending(oldestFirst=true): %v", err)
	}
	if indexOf(asc, older.ID) > indexOf(asc, newer.ID) {
		t.Errorf("oldestFirst=true: older subscriber %d did not sort before newer %d", older.ID, newer.ID)
	}

	desc, err := store.ListPending(context.Background(), false)
	if err != nil {
		t.Fatalf("ListPending(oldestFirst=false): %v", err)
	}
	if indexOf(desc, newer.ID) > indexOf(desc, older.ID) {
		t.Errorf("oldestFirst=false: newer subscriber %d did not sort before older %d", newer.ID, older.ID)
	}
}

func TestAdminResendConfirmation_Success(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Move confirm_sent_at into the past so a fresh cooldown check passes.
	past := now.Add(-2 * time.Hour)
	if _, err := pool.Exec(context.Background(), `UPDATE subscribers SET confirm_sent_at = $2 WHERE id = $1`, created.ID, past); err != nil {
		t.Fatalf("backdating confirm_sent_at: %v", err)
	}

	result, err := store.AdminResendConfirmation(context.Background(), created.ID, now, time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("AdminResendConfirmation: %v", err)
	}
	if result.Subscriber.ConfirmToken == nil || *result.Subscriber.ConfirmToken == *created.ConfirmToken {
		t.Errorf("resend did not mint a fresh, different confirm_token")
	}
	if result.Subscriber.ConfirmSentAt == nil || !result.Subscriber.ConfirmSentAt.Equal(now) {
		t.Errorf("ConfirmSentAt = %v, want stamped to %v", result.Subscriber.ConfirmSentAt, now)
	}
	if result.Subscriber.ConfirmExpiresAt == nil || !result.Subscriber.ConfirmExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Errorf("ConfirmExpiresAt = %v, want extended to %v", result.Subscriber.ConfirmExpiresAt, now.Add(7*24*time.Hour))
	}
	if result.PreviousConfirmSentAt == nil || !result.PreviousConfirmSentAt.Equal(past) {
		t.Errorf("PreviousConfirmSentAt = %v, want %v", result.PreviousConfirmSentAt, past)
	}

	var queued int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'confirmation' AND status = 'queued'`,
		created.ID,
	).Scan(&queued); err != nil {
		t.Fatalf("counting queued confirmation rows: %v", err)
	}
	// Create's own signup already queued one; the resend queues a second.
	if queued != 2 {
		t.Errorf("queued confirmation rows after resend = %d, want 2 (one from signup, one from resend)", queued)
	}
}

func TestAdminResendConfirmation_CooldownActive(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Create just stamped confirm_sent_at = now, well inside a 1-hour cooldown.
	if _, err := store.AdminResendConfirmation(context.Background(), created.ID, now.Add(time.Minute), time.Hour, 7*24*time.Hour); !errors.Is(err, ErrResendCooldownActive) {
		t.Fatalf("got err=%v, want ErrResendCooldownActive", err)
	}

	var queued int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'confirmation'`, created.ID,
	).Scan(&queued); err != nil {
		t.Fatalf("counting queued rows: %v", err)
	}
	if queued != 1 {
		t.Errorf("queued confirmation rows after a cooldown-refused resend = %d, want still 1 (mail-bomb guard held)", queued)
	}
}

func TestAdminResendConfirmation_SuppressedRefused(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	suppressions := NewSuppressionStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := suppressions.Add(context.Background(), NewSuppression{Email: created.Email, Reason: SuppressionReasonManual, Note: "test"}, now); err != nil {
		t.Fatalf("Add suppression: %v", err)
	}

	if _, err := store.AdminResendConfirmation(context.Background(), created.ID, now, time.Hour, 7*24*time.Hour); !errors.Is(err, ErrResendSuppressed) {
		t.Fatalf("got err=%v, want ErrResendSuppressed", err)
	}
}

func TestAdminResendConfirmation_NotPending(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Confirm(context.Background(), *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	if _, err := store.AdminResendConfirmation(context.Background(), created.ID, now.Add(2*time.Minute), time.Hour, 7*24*time.Hour); !errors.Is(err, ErrNotPending) {
		t.Fatalf("got err=%v, want ErrNotPending", err)
	}
}

func TestAdminResendConfirmation_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	if _, err := store.AdminResendConfirmation(context.Background(), -1, time.Now(), time.Hour, 7*24*time.Hour); !errors.Is(err, ErrPendingSubscriberNotFound) {
		t.Fatalf("got err=%v, want ErrPendingSubscriberNotFound", err)
	}
}

// TestExpirePendingSweep_ExpiresPastTTL_LeavesRowPending is #0128's core
// property: a pending signup past confirm_expires_at gets its token cleared
// and a confirmation_expired event, but the row survives as 'pending', not
// deleted.
func TestExpirePendingSweep_ExpiresPastTTL_LeavesRowPending(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Minute}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	swept, err := store.ExpirePendingSweep(context.Background(), now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ExpirePendingSweep: %v", err)
	}
	if swept < 1 {
		t.Fatalf("ExpirePendingSweep swept %d rows, want at least 1", swept)
	}

	got, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != StatusPending {
		t.Errorf("Status = %q after sweep, want %q (left pending, not deleted)", got.Status, StatusPending)
	}
	if got.ConfirmToken != nil {
		t.Errorf("ConfirmToken = %v after sweep, want nil (cleared)", *got.ConfirmToken)
	}

	var eventCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1 AND action = 'confirmation_expired'`, created.ID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("counting confirmation_expired events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("confirmation_expired events = %d, want 1", eventCount)
	}
}

// TestExpirePendingSweep_NotYetExpired_LeavesRowUntouched proves the sweep
// does not touch a pending signup whose confirm_expires_at has not passed.
func TestExpirePendingSweep_NotYetExpired_LeavesRowUntouched(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.ExpirePendingSweep(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("ExpirePendingSweep: %v", err)
	}

	got, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ConfirmToken == nil {
		t.Errorf("ConfirmToken cleared by sweep before expiry — sweep ran too eagerly")
	}
}

// TestExpirePendingSweep_IsIdempotent proves a second sweep pass over an
// already-expired-and-cleared row does not write a duplicate
// confirmation_expired event — the WHERE clause's confirm_token IS NOT NULL
// guard is what makes this true.
func TestExpirePendingSweep_IsIdempotent(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Minute}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	later := now.Add(2 * time.Minute)
	if _, err := store.ExpirePendingSweep(context.Background(), later); err != nil {
		t.Fatalf("first ExpirePendingSweep: %v", err)
	}
	if _, err := store.ExpirePendingSweep(context.Background(), later.Add(time.Minute)); err != nil {
		t.Fatalf("second ExpirePendingSweep: %v", err)
	}

	var eventCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1 AND action = 'confirmation_expired'`, created.ID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("counting confirmation_expired events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("confirmation_expired events after two sweep passes = %d, want 1", eventCount)
	}
}
