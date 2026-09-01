package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

func adminPendingMux(pool *pgxpool.Pool) http.Handler {
	authStore := auth.NewStore(pool)
	subStore := subscribers.NewStore(pool)
	h := NewAdminPendingHandler(subStore, outbox.NewStore(pool), authStore, audit.New(pool))
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/subscribers/pending", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("POST /admin/subscribers/{id}/resend-confirmation", requireAdmin(http.HandlerFunc(h.Resend)))
	mux.Handle("POST /admin/subscribers/{id}/resend-invitation", requireAdmin(http.HandlerFunc(h.ResendInvitation)))
	return mux
}

// seedPendingSubscriber inserts a status='pending' subscribers row directly
// with a real confirm_token/confirm_sent_at/confirm_expires_at (unlike
// seedTestSubscriber in admin_subscribers_test.go, which leaves those
// columns NULL — fine for that file's status/filter tests, not enough for
// this file's age/expiry assertions). Registers cleanup for both the
// subscribers row and any outbound_queue rows a resend against it enqueues.
func seedPendingSubscriber(t *testing.T, pool *pgxpool.Pool, confirmSentAt, confirmExpiresAt time.Time) (id int64, email string) {
	t.Helper()
	ctx := context.Background()
	email = fmt.Sprintf("zz-pending-subtest-%d@example.com", testdb.Unique())
	token := fmt.Sprintf("zz-pending-token-%d", testdb.Unique())
	manageToken := fmt.Sprintf("zz-pending-manage-%d", testdb.Unique())
	if err := pool.QueryRow(ctx,
		`INSERT INTO subscribers (email, status, confirm_token, confirm_sent_at, confirm_expires_at, manage_token, utm_source, created_at, updated_at)
		 VALUES ($1, 'pending', $2, $3, $4, $5, 'newsletter', $3, $3)
		 RETURNING id`,
		email, token, confirmSentAt, confirmExpiresAt, manageToken,
	).Scan(&id); err != nil {
		t.Fatalf("seed pending subscriber: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM outbound_queue WHERE subscriber_id = $1`, id)
		// subscriber_events is append-only outside erase.go's redaction
		// (internal/subscribers/events_append_only_guard_test.go) — deleting
		// this subscriber's row alone is enough cleanup; the FK's ON DELETE
		// SET NULL leaves any subscriber_events rows in place with
		// subscriber_id nulled, the same residue every other test in this
		// package already leaves behind.
		_, _ = pool.Exec(ctx, `DELETE FROM subscribers WHERE id = $1`, id)
	})
	return id, email
}

// decodedPendingRow mirrors pendingSubscriberRow's JSON shape
// (admin_pending.go) for test decoding.
type decodedPendingRow struct {
	ID               int64   `json:"id"`
	Email            string  `json:"email"`
	ConfirmSentAt    *string `json:"confirm_sent_at"`
	ConfirmExpiresAt *string `json:"confirm_expires_at"`
	AgeSeconds       int64   `json:"age_seconds"`
	Expired          bool    `json:"expired"`
	QueueState       string  `json:"queue_state"`
}

func decodePendingListBody(t *testing.T, resp *http.Response) []decodedPendingRow {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out struct {
		Pending []decodedPendingRow `json:"pending"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode pending list: %v (body=%s)", err, body)
	}
	return out.Pending
}

func findPendingRow(rows []decodedPendingRow, id int64) (decodedPendingRow, bool) {
	for _, r := range rows {
		if r.ID == id {
			return r, true
		}
	}
	return decodedPendingRow{}, false
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestAdminPending_List_RequiresSession(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()

	resp := doJSON(t, srv.Client(), "GET", srv.URL+"/admin/subscribers/pending", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestAdminPending_List_MarksExpiredAndComputesAge proves the server
// computes Expired/AgeSeconds itself (not left to the client to derive from
// raw timestamps) for two rows on opposite sides of confirm_expires_at.
func TestAdminPending_List_MarksExpiredAndComputesAge(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-pending-list@example.com")
	seedSession(t, pool, admin, "admin-token-pending-list")

	now := time.Now()
	liveID, _ := seedPendingSubscriber(t, pool, now.Add(-time.Hour), now.Add(6*24*time.Hour))
	expiredID, _ := seedPendingSubscriber(t, pool, now.Add(-8*24*time.Hour), now.Add(-24*time.Hour))

	resp := doJSON(t, srv.Client(), "GET", srv.URL+"/admin/subscribers/pending", "admin-token-pending-list", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	rows := decodePendingListBody(t, resp)

	live, liveOK := findPendingRow(rows, liveID)
	expired, expiredOK := findPendingRow(rows, expiredID)
	if !liveOK || !expiredOK {
		t.Fatalf("pending list missing one or both seeded rows: live present=%v expired present=%v", liveOK, expiredOK)
	}
	if live.Expired {
		t.Errorf("live row Expired = true, want false")
	}
	if !expired.Expired {
		t.Errorf("expired row Expired = false, want true")
	}
	if live.AgeSeconds < 3500 || live.AgeSeconds > 3700 {
		t.Errorf("live row AgeSeconds = %d, want ~3600 (1 hour)", live.AgeSeconds)
	}
}

// TestAdminPending_List_ReflectsRealOutboundQueueState proves queue_state
// is not always "none"/"unknown" — for an address with a real
// outbound_queue row, List surfaces that row's actual status, joined via
// outbox.Store.LatestByRecipients (#0128's whole point: turning "why
// didn't they confirm?" into a fact instead of a mystery).
func TestAdminPending_List_ReflectsRealOutboundQueueState(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-pending-queuestate@example.com")
	seedSession(t, pool, admin, "admin-token-pending-queuestate")

	now := time.Now()
	id, email := seedPendingSubscriber(t, pool, now.Add(-time.Hour), now.Add(6*24*time.Hour))

	outboxStore := outbox.NewStore(pool)
	queueID, err := outboxStore.Enqueue(context.Background(), outbox.Item{
		Kind: outbox.KindConfirmation, Recipient: email, SubscriberID: &id,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// MarkSent only transitions a CLAIMED (status='sending') row — claim it
	// first, mirroring the outbox package's own worker-drain tests. Scoped
	// to the one kind enqueued above (#0281) — an unscoped ClaimDue outside
	// internal/outbox claims every kind by default, #0254's failure mode.
	if _, err := outboxStore.ClaimDue(context.Background(), 100, []outbox.Kind{outbox.KindConfirmation}); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if done, err := outboxStore.MarkSent(context.Background(), queueID, "msg-id"); err != nil {
		t.Fatalf("MarkSent: %v", err)
	} else if !done {
		t.Fatalf("MarkSent reported done=false for row %d — it was not claimed", queueID)
	}

	resp := doJSON(t, srv.Client(), "GET", srv.URL+"/admin/subscribers/pending", "admin-token-pending-queuestate", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	rows := decodePendingListBody(t, resp)
	row, ok := findPendingRow(rows, id)
	if !ok {
		t.Fatalf("pending list missing seeded row %d", id)
	}
	if row.QueueState != "sent" {
		t.Errorf("QueueState = %q, want %q (the real outbound_queue row's status)", row.QueueState, "sent")
	}
}

// TestAdminPending_List_SortOrder proves the sort=age_desc query parameter
// actually flips the order (default is oldest-first, age_asc).
func TestAdminPending_List_SortOrder(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-pending-sort@example.com")
	seedSession(t, pool, admin, "admin-token-pending-sort")

	now := time.Now()
	olderID, _ := seedPendingSubscriber(t, pool, now.Add(-3*time.Hour), now.Add(time.Hour))
	newerID, _ := seedPendingSubscriber(t, pool, now.Add(-time.Minute), now.Add(time.Hour))

	indexOf := func(rows []decodedPendingRow, id int64) int {
		for i, r := range rows {
			if r.ID == id {
				return i
			}
		}
		return -1
	}

	ascResp := doJSON(t, srv.Client(), "GET", srv.URL+"/admin/subscribers/pending", "admin-token-pending-sort", "")
	asc := decodePendingListBody(t, ascResp)
	if indexOf(asc, olderID) > indexOf(asc, newerID) {
		t.Errorf("default sort: older row %d did not come before newer row %d", olderID, newerID)
	}

	descResp := doJSON(t, srv.Client(), "GET", srv.URL+"/admin/subscribers/pending?sort=age_desc", "admin-token-pending-sort", "")
	desc := decodePendingListBody(t, descResp)
	if indexOf(desc, newerID) > indexOf(desc, olderID) {
		t.Errorf("sort=age_desc: newer row %d did not come before older row %d", newerID, olderID)
	}
}

// ── Resend ────────────────────────────────────────────────────────────────────

func TestAdminPending_Resend_RequiresSession(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()

	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/subscribers/1/resend-confirmation", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAdminPending_Resend_Success_MintsFreshTokenAndAudits(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-pending-resend@example.com")
	seedSession(t, pool, admin, "admin-token-pending-resend")

	now := time.Now()
	id, email := seedPendingSubscriber(t, pool, now.Add(-2*time.Hour), now.Add(6*24*time.Hour))
	var originalToken string
	if err := pool.QueryRow(context.Background(), `SELECT confirm_token FROM subscribers WHERE id = $1`, id).Scan(&originalToken); err != nil {
		t.Fatalf("select original token: %v", err)
	}

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/subscribers/%d/resend-confirmation", srv.URL, id), "admin-token-pending-resend", "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}

	var newToken string
	if err := pool.QueryRow(context.Background(), `SELECT confirm_token FROM subscribers WHERE id = $1`, id).Scan(&newToken); err != nil {
		t.Fatalf("select new token: %v", err)
	}
	if newToken == originalToken {
		t.Errorf("confirm_token unchanged after resend — want a freshly minted token")
	}

	var queued int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'confirmation' AND status = 'queued'`, id,
	).Scan(&queued); err != nil {
		t.Fatalf("counting queued confirmation rows: %v", err)
	}
	if queued < 1 {
		t.Errorf("queued confirmation rows after resend = %d, want at least 1", queued)
	}

	var auditCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`, audit.ActionSubscriberResendConfirmation, id,
	).Scan(&auditCount); err != nil {
		t.Fatalf("counting audit rows: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("audit rows for resend = %d, want 1", auditCount)
	}

	var auditMetadataText string
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata::text FROM audit_log WHERE action = $1 AND target_id = $2`, audit.ActionSubscriberResendConfirmation, id,
	).Scan(&auditMetadataText); err != nil {
		t.Fatalf("selecting audit metadata: %v", err)
	}
	if email != "" && strings.Contains(auditMetadataText, email) {
		t.Errorf("audit metadata unexpectedly contains the subscriber's email: %s", auditMetadataText)
	}
}

func TestAdminPending_Resend_CooldownReturns429(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-pending-resend-cooldown@example.com")
	seedSession(t, pool, admin, "admin-token-pending-resend-cooldown")

	now := time.Now()
	// confirm_sent_at = now (just claimed) — well inside the 1-hour cooldown.
	id, _ := seedPendingSubscriber(t, pool, now, now.Add(6*24*time.Hour))

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/subscribers/%d/resend-confirmation", srv.URL, id), "admin-token-pending-resend-cooldown", "")
	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 429 (body=%s)", resp.StatusCode, body)
	}
}

func TestAdminPending_Resend_SuppressedReturns409(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-pending-resend-suppressed@example.com")
	seedSession(t, pool, admin, "admin-token-pending-resend-suppressed")

	now := time.Now()
	id, email := seedPendingSubscriber(t, pool, now.Add(-2*time.Hour), now.Add(6*24*time.Hour))
	suppressions := subscribers.NewSuppressionStore(pool)
	if _, err := suppressions.Add(context.Background(), subscribers.NewSuppression{
		Email: email, Reason: subscribers.SuppressionReasonManual, Note: "test",
	}, now); err != nil {
		t.Fatalf("Add suppression: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM suppressions WHERE email = $1`, email)
	})

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/subscribers/%d/resend-confirmation", srv.URL, id), "admin-token-pending-resend-suppressed", "")
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 409 (body=%s)", resp.StatusCode, body)
	}
}

func TestAdminPending_Resend_NotPendingReturns409(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-pending-resend-active@example.com")
	seedSession(t, pool, admin, "admin-token-pending-resend-active")

	activeID := seedTestSubscriber(t, pool, subscribers.StatusActive)

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/subscribers/%d/resend-confirmation", srv.URL, activeID), "admin-token-pending-resend-active", "")
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 409 (body=%s)", resp.StatusCode, body)
	}
}

func TestAdminPending_Resend_NotFoundReturns404(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-pending-resend-404@example.com")
	seedSession(t, pool, admin, "admin-token-pending-resend-404")

	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/subscribers/999999999/resend-confirmation", "admin-token-pending-resend-404", "")
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404 (body=%s)", resp.StatusCode, body)
	}
}
