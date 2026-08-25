package sesnotify

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
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
	return fmt.Sprintf("zz-0038-sns-%d", testdb.Unique())
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

// seedTransientBounce inserts one Transient-bounce email_events row for
// recipient via the normal InsertTx path, then backdates its received_at
// directly with SQL — InsertTx has no received_at parameter (it always
// defaults to now()), so seeding a row at a controlled point in the past for
// the sliding-window tests below requires this direct UPDATE. Not a
// production code path.
func seedTransientBounce(t *testing.T, pool *pgxpool.Pool, store *Store, snsID, recipient string, receivedAt time.Time) {
	t.Helper()
	seedBounceEvent(t, pool, store, snsID, recipient, BounceTypeTransient, "", receivedAt)
}

// seedBounceEvent is seedTransientBounce's general form (#0109): it takes the
// bounce_type and bounce_subtype directly, so a test can seed an Undetermined
// bounce or a sender-fault-subtype Transient bounce without hand-rolling
// InsertTx + a backdate UPDATE itself.
func seedBounceEvent(t *testing.T, pool *pgxpool.Pool, store *Store, snsID, recipient, bounceType, bounceSubtype string, receivedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := store.InsertTx(ctx, pool, NewEmailEvent{
		SNSMessageID: snsID, EventType: EventTypeBounce, BounceType: bounceType, BounceSubtype: bounceSubtype,
		Recipient: recipient, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed InsertTx: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE email_events SET received_at = $1 WHERE sns_message_id = $2 AND recipient = lower(trim($3))`,
		receivedAt, snsID, recipient,
	); err != nil {
		t.Fatalf("backdate received_at: %v", err)
	}
}

// TestStore_ByRecipient_ReturnsNewestFirstForThatAddressOnly is #0124's
// replacement for the #0039/#0109 windowed-count tests this migration
// removed (see this file's history): ByRecipient backs GET
// /admin/deliverability/{email}'s full event history, so its job is
// "every row for this address, newest first" — not a threshold count.
func TestStore_ByRecipient_ReturnsNewestFirstForThatAddressOnly(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	recipient := fmt.Sprintf("zz-0124-recipient-%d@example.com", testdb.Unique())
	now := time.Now()

	seedBounceEvent(t, pool, store, uniqueSNSMessageID(t), recipient, BounceTypeTransient, "", now.Add(-2*time.Hour))
	seedBounceEvent(t, pool, store, uniqueSNSMessageID(t), recipient, BounceTypeUndetermined, "", now.Add(-time.Hour))
	if _, _, err := store.InsertTx(ctx, pool, NewEmailEvent{
		SNSMessageID: uniqueSNSMessageID(t), EventType: EventTypeDelivery, Recipient: recipient, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("InsertTx (delivery): %v", err)
	}
	// A different recipient's row must never appear in this recipient's history.
	other := fmt.Sprintf("zz-0124-recipient-other-%d@example.com", testdb.Unique())
	seedBounceEvent(t, pool, store, uniqueSNSMessageID(t), other, BounceTypeTransient, "", now)

	rows, err := store.ByRecipient(ctx, recipient)
	if err != nil {
		t.Fatalf("ByRecipient: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3 (two bounces + one delivery, none of the other recipient's)", len(rows))
	}
	// Ordering is by received_at DESC, not event_at (nil on every row seeded
	// above — none of them set mail.timestamp): newest received first.
	if rows[0].EventType != EventTypeDelivery {
		t.Errorf("rows[0].EventType = %q, want %q (newest first)", rows[0].EventType, EventTypeDelivery)
	}
	for _, r := range rows {
		if r.Recipient != recipient {
			t.Errorf("row recipient = %q, want %q — a different address's event leaked into this history", r.Recipient, recipient)
		}
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
