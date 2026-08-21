package sesnotify

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool returns the package's single shared pool (opened once in
// TestMain) or skips if TEST_DATABASE_URL was unset.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

// uniqueSNSMessageID returns an SNS MessageId scoped to this test run, so
// every test's rows are disjoint under email_events' UNIQUE
// (sns_message_id, recipient) constraint (CLAUDE.md §8b: seed throwaway
// rows, never target a literal or seeded id).
func uniqueSNSMessageID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-0038-sns-%d", time.Now().UnixNano())
}

func TestStore_InsertTx_InsertsRowAndReturnsInserted(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	snsID := uniqueSNSMessageID(t)

	id, inserted, err := store.InsertTx(ctx, pool, NewEmailEvent{
		SNSMessageID: snsID,
		SESMessageID: "ses-msg-1",
		EventType:    EventTypeBounce,
		BounceType:   BounceTypePermanent,
		Recipient:    " Zz-Store@Example.com ",
		Payload:      []byte(`{"eventType":"Bounce"}`),
	})
	if err != nil {
		t.Fatalf("InsertTx: %v", err)
	}
	if !inserted {
		t.Fatal("inserted = false on the first write, want true")
	}
	if id == 0 {
		t.Error("id = 0, want a positive generated id")
	}

	rows, err := store.ByMessageID(ctx, snsID)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ByMessageID returned %d rows, want 1", len(rows))
	}
	got := rows[0]
	if got.Recipient != "zz-store@example.com" {
		t.Errorf("Recipient = %q, want normalized %q", got.Recipient, "zz-store@example.com")
	}
	if got.SESMessageID == nil || *got.SESMessageID != "ses-msg-1" {
		t.Errorf("SESMessageID = %v, want ses-msg-1", got.SESMessageID)
	}
	if got.BounceType == nil || *got.BounceType != BounceTypePermanent {
		t.Errorf("BounceType = %v, want %q", got.BounceType, BounceTypePermanent)
	}
	if got.EventType != EventTypeBounce {
		t.Errorf("EventType = %q, want %q", got.EventType, EventTypeBounce)
	}
}

func TestStore_InsertTx_EmptyOptionalFieldsMapToNull(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	snsID := uniqueSNSMessageID(t)

	if _, _, err := store.InsertTx(ctx, pool, NewEmailEvent{
		SNSMessageID: snsID,
		EventType:    EventTypeDelivery,
		Recipient:    "zz-store2@example.com",
		Payload:      []byte(`{}`),
	}); err != nil {
		t.Fatalf("InsertTx: %v", err)
	}

	rows, err := store.ByMessageID(ctx, snsID)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].SESMessageID != nil {
		t.Errorf("SESMessageID = %v, want nil", rows[0].SESMessageID)
	}
	if rows[0].BounceType != nil {
		t.Errorf("BounceType = %v, want nil", rows[0].BounceType)
	}
	if rows[0].BounceSubtype != nil {
		t.Errorf("BounceSubtype = %v, want nil", rows[0].BounceSubtype)
	}
}

func TestStore_InsertTx_DuplicateMessageAndRecipientIsNoOp(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	snsID := uniqueSNSMessageID(t)
	in := NewEmailEvent{
		SNSMessageID: snsID,
		EventType:    EventTypeBounce,
		BounceType:   BounceTypeTransient,
		Recipient:    "zz-store-dup@example.com",
		Payload:      []byte(`{}`),
	}

	_, inserted1, err := store.InsertTx(ctx, pool, in)
	if err != nil {
		t.Fatalf("first InsertTx: %v", err)
	}
	if !inserted1 {
		t.Fatal("first InsertTx: inserted = false, want true")
	}

	_, inserted2, err := store.InsertTx(ctx, pool, in)
	if err != nil {
		t.Fatalf("second (redelivery) InsertTx: %v", err)
	}
	if inserted2 {
		t.Error("second InsertTx: inserted = true, want false (redelivery of the same sns_message_id+recipient)")
	}

	rows, err := store.ByMessageID(ctx, snsID)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ByMessageID returned %d rows after redelivery, want exactly 1", len(rows))
	}
}

func TestStore_InsertTx_DifferentRecipientsSameMessageBothInsert(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	snsID := uniqueSNSMessageID(t)

	for _, recipient := range []string{"zz-multi-a@example.com", "zz-multi-b@example.com"} {
		if _, inserted, err := store.InsertTx(ctx, pool, NewEmailEvent{
			SNSMessageID: snsID,
			EventType:    EventTypeBounce,
			BounceType:   BounceTypePermanent,
			Recipient:    recipient,
			Payload:      []byte(`{}`),
		}); err != nil {
			t.Fatalf("InsertTx(%s): %v", recipient, err)
		} else if !inserted {
			t.Errorf("InsertTx(%s): inserted = false, want true", recipient)
		}
	}

	rows, err := store.ByMessageID(ctx, snsID)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ByMessageID returned %d rows, want 2 (one per recipient — the composite key exists for exactly this)", len(rows))
	}
}

func TestStore_InsertTx_RollsBackWithTransaction(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	snsID := uniqueSNSMessageID(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if _, _, err := store.InsertTx(ctx, tx, NewEmailEvent{
		SNSMessageID: snsID, EventType: EventTypeBounce, Recipient: "zz-tx-rb@example.com", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("InsertTx: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	rows, err := store.ByMessageID(ctx, snsID)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("ByMessageID returned %d rows after rollback, want 0 — InsertTx did not honor the transaction it was given", len(rows))
	}
}

func TestStore_InsertTx_CommitsWithTransaction(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	snsID := uniqueSNSMessageID(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if _, _, err := store.InsertTx(ctx, tx, NewEmailEvent{
		SNSMessageID: snsID, EventType: EventTypeBounce, Recipient: "zz-tx-commit@example.com", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("InsertTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rows, err := store.ByMessageID(ctx, snsID)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("ByMessageID returned %d rows after commit, want 1", len(rows))
	}
}

func TestStore_ByMessageID_ReturnsNewestFirst(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	snsID := uniqueSNSMessageID(t)

	for _, recipient := range []string{"zz-order-a@example.com", "zz-order-b@example.com"} {
		if _, _, err := store.InsertTx(ctx, pool, NewEmailEvent{
			SNSMessageID: snsID, EventType: EventTypeDelivery, Recipient: recipient, Payload: []byte(`{}`),
		}); err != nil {
			t.Fatalf("InsertTx(%s): %v", recipient, err)
		}
	}

	rows, err := store.ByMessageID(ctx, snsID)
	if err != nil {
		t.Fatalf("ByMessageID: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	// Newest first = highest id first, since both rows were inserted in the
	// same call order above.
	if rows[0].ID < rows[1].ID {
		t.Errorf("rows not newest-first: rows[0].ID=%d < rows[1].ID=%d", rows[0].ID, rows[1].ID)
	}
}
