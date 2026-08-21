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

// seedTransientBounce inserts one Transient-bounce email_events row for
// recipient via the normal InsertTx path, then backdates its received_at
// directly with SQL — InsertTx has no received_at parameter (it always
// defaults to now()), so seeding a row at a controlled point in the past for
// the sliding-window tests below requires this direct UPDATE. Not a
// production code path.
func seedTransientBounce(t *testing.T, pool *pgxpool.Pool, store *Store, snsID, recipient string, receivedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := store.InsertTx(ctx, pool, NewEmailEvent{
		SNSMessageID: snsID, EventType: EventTypeBounce, BounceType: BounceTypeTransient,
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

func TestStore_CountRecentTransientBounces_CountsOnlyMatchingTypeAndRecipient(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	recipient := fmt.Sprintf("zz-0039-count-%d@example.com", time.Now().UnixNano())
	now := time.Now()

	// Two Transient bounces for the target recipient (should count)...
	seedTransientBounce(t, pool, store, uniqueSNSMessageID(t), recipient, now.Add(-time.Hour))
	seedTransientBounce(t, pool, store, uniqueSNSMessageID(t), recipient, now.Add(-2*time.Hour))
	// ...a Permanent bounce for the same recipient (must NOT count)...
	permSNS := uniqueSNSMessageID(t)
	if _, _, err := store.InsertTx(ctx, pool, NewEmailEvent{
		SNSMessageID: permSNS, EventType: EventTypeBounce, BounceType: BounceTypePermanent,
		Recipient: recipient, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("InsertTx (permanent): %v", err)
	}
	// ...a Delivery event for the same recipient (must NOT count)...
	delivSNS := uniqueSNSMessageID(t)
	if _, _, err := store.InsertTx(ctx, pool, NewEmailEvent{
		SNSMessageID: delivSNS, EventType: EventTypeDelivery,
		Recipient: recipient, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("InsertTx (delivery): %v", err)
	}
	// ...and a Transient bounce for a DIFFERENT recipient (must NOT count).
	otherRecipient := fmt.Sprintf("zz-0039-count-other-%d@example.com", time.Now().UnixNano())
	seedTransientBounce(t, pool, store, uniqueSNSMessageID(t), otherRecipient, now.Add(-time.Hour))

	count, err := store.CountRecentTransientBounces(ctx, pool, recipient, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatalf("CountRecentTransientBounces: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (only the two Transient bounces for this recipient)", count)
	}
}

// TestStore_CountRecentTransientBounces_WindowSlides_ExcludesOlderEvents is
// #0039's "the count window slides; older events age out" acceptance
// criterion, and doubles as the mutation check for the window bound: an
// event received exactly outside a 30-day window must not count, while one
// just inside it must.
func TestStore_CountRecentTransientBounces_WindowSlides_ExcludesOlderEvents(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	recipient := fmt.Sprintf("zz-0039-window-%d@example.com", time.Now().UnixNano())
	now := time.Now()
	window := 30 * 24 * time.Hour
	since := now.Add(-window)

	// Just inside the window: counts.
	seedTransientBounce(t, pool, store, uniqueSNSMessageID(t), recipient, since.Add(time.Minute))
	// Just outside the window: must NOT count — this is the boundary a
	// mutated `>` instead of `>=`, or a mutated window duration, would flip.
	seedTransientBounce(t, pool, store, uniqueSNSMessageID(t), recipient, since.Add(-time.Minute))
	// Far outside the window: must NOT count.
	seedTransientBounce(t, pool, store, uniqueSNSMessageID(t), recipient, now.Add(-60*24*time.Hour))

	count, err := store.CountRecentTransientBounces(ctx, pool, recipient, since)
	if err != nil {
		t.Fatalf("CountRecentTransientBounces: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 — only the event just inside the window should count", count)
	}
}

// TestStore_CountRecentTransientBounces_WindowBoundaryIsInclusive is the
// mutation check for `>=` vs `>` specifically: an event received at exactly
// the window's cutoff instant (received_at == since) must still count. The
// "just inside"/"just outside" cases above are both a full minute away from
// the boundary and would pass under either operator; only an event AT the
// boundary distinguishes them.
func TestStore_CountRecentTransientBounces_WindowBoundaryIsInclusive(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	recipient := fmt.Sprintf("zz-0039-boundary-%d@example.com", time.Now().UnixNano())
	since := time.Now().Add(-30 * 24 * time.Hour)

	seedTransientBounce(t, pool, store, uniqueSNSMessageID(t), recipient, since)

	count, err := store.CountRecentTransientBounces(ctx, pool, recipient, since)
	if err != nil {
		t.Fatalf("CountRecentTransientBounces: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 — an event received exactly at the window boundary must count", count)
	}
}

func TestStore_CountRecentTransientBouncesPool_MatchesTxVersion(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	recipient := fmt.Sprintf("zz-0039-pool-%d@example.com", time.Now().UnixNano())
	now := time.Now()

	seedTransientBounce(t, pool, store, uniqueSNSMessageID(t), recipient, now.Add(-time.Hour))
	seedTransientBounce(t, pool, store, uniqueSNSMessageID(t), recipient, now.Add(-2*time.Hour))

	since := now.Add(-30 * 24 * time.Hour)
	want, err := store.CountRecentTransientBounces(ctx, pool, recipient, since)
	if err != nil {
		t.Fatalf("CountRecentTransientBounces: %v", err)
	}
	got, err := store.CountRecentTransientBouncesPool(ctx, recipient, since)
	if err != nil {
		t.Fatalf("CountRecentTransientBouncesPool: %v", err)
	}
	if got != want {
		t.Errorf("CountRecentTransientBouncesPool = %d, want %d (matching CountRecentTransientBounces over the pool)", got, want)
	}
	if got != 2 {
		t.Errorf("count = %d, want 2", got)
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
