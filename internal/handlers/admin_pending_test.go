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

// ── ResendInvitation (#0367) ─────────────────────────────────────────────────
//
// ResendInvitation (#0312) was the only route in this file with no
// handler-level tests — everything above proves Resend's twin, nothing
// proved this one at the HTTP layer. #0312's own review drove it once by
// hand over httptest and reported every case correct; these are that same
// coverage, kept, mirroring the six TestAdminPending_Resend_* shapes above.
//
// seedInvitedPendingSubscriber commits a real invite-mode import (the same
// producer internal/subscribers/imports_test.go's commitInvite uses, one
// package over) so the seeded row carries a genuine import_id/invited_at —
// what AdminResendInvitation actually reads — rather than a synthetic
// import_id that happens to be non-nil.
func seedInvitedPendingSubscriber(t *testing.T, pool *pgxpool.Pool) (id int64, email string) {
	t.Helper()
	ctx := context.Background()
	email = fmt.Sprintf("zz-pending-invite-subtest-%d@example.com", testdb.Unique())
	importStore := subscribers.NewImportStore(pool)
	in := subscribers.CommitInput{
		Source:       subscribers.ImportSourceManualCSV,
		SourceDetail: "admin_pending_test.go invite batch",
		ConsentMode:  subscribers.ConsentModeInvite,
		ConsentNote:  "collected via a paper sign-in sheet, attested by the organizer",
		CollectedAt:  time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		Filename:     "attendees.csv",
		Rows:         []subscribers.ImportRow{{Email: email}},
	}
	if _, err := importStore.Commit(ctx, in, time.Now()); err != nil {
		t.Fatalf("seed invited subscriber: Commit: %v", err)
	}
	subStore := subscribers.NewStore(pool)
	sub, err := subStore.FindByEmail(ctx, email)
	if err != nil {
		t.Fatalf("seed invited subscriber: FindByEmail: %v", err)
	}
	importID := *sub.ImportID
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM outbound_queue WHERE subscriber_id = $1`, sub.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM subscribers WHERE id = $1`, sub.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM subscriber_imports WHERE id = $1`, importID)
	})
	return sub.ID, email
}

// setPhysicalAddressSetting sets the (process-wide, one-row) physical_address
// setting for the duration of the calling test and restores whatever value
// was there before at cleanup — settings is a singleton table shared by
// every test in this package's pool (adminSubscribersTestPool), not scoped
// per test the way "zz-subtest-" rows are.
func setPhysicalAddressSetting(t *testing.T, pool *pgxpool.Pool, addr string) {
	t.Helper()
	authStore := auth.NewStore(pool)
	ctx := context.Background()
	previous, err := authStore.GetSetting(ctx, settingPhysicalAddress)
	if err != nil {
		t.Fatalf("reading current physical_address setting: %v", err)
	}
	if _, err := authStore.UpdateSetting(ctx, settingPhysicalAddress, addr, time.Now()); err != nil {
		t.Fatalf("setting physical_address: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		if _, err := authStore.UpdateSetting(ctx, settingPhysicalAddress, previous, time.Now()); err != nil {
			t.Errorf("restoring physical_address setting: %v", err)
		}
	})
}

// decodedResendInvitationResponse mirrors resendInvitationResponse's JSON
// shape (admin_pending.go) for test decoding.
type decodedResendInvitationResponse struct {
	ID               int64   `json:"id"`
	ConfirmSentAt    *string `json:"confirm_sent_at"`
	ConfirmExpiresAt *string `json:"confirm_expires_at"`
	InviteResentAt   *string `json:"invite_resent_at"`
}

func TestAdminPending_ResendInvitation_RequiresSession(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()

	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/subscribers/1/resend-invitation", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestAdminPending_ResendInvitation_Success_MintsTokenAndAudits covers
// #0367 criterion 1's success/body/audit shapes together, matching
// TestAdminPending_Resend_Success_MintsFreshTokenAndAudits's own pattern:
// 200, the response body shape (confirm_expires_at ~importInviteConfirmTTL
// out, not subscribeConfirmTTL — #0312's whole point), and exactly one
// audit_log row whose metadata carries import_id and never the address.
func TestAdminPending_ResendInvitation_Success_MintsTokenAndAudits(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()
	setPhysicalAddressSetting(t, pool, "123 Main St, San Francisco, CA 94110")

	admin := seedAdmin(t, pool, "admin-pending-invite-resend@example.com")
	seedSession(t, pool, admin, "admin-token-pending-invite-resend")

	id, email := seedInvitedPendingSubscriber(t, pool)
	var originalToken string
	if err := pool.QueryRow(context.Background(), `SELECT confirm_token FROM subscribers WHERE id = $1`, id).Scan(&originalToken); err != nil {
		t.Fatalf("select original token: %v", err)
	}

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/subscribers/%d/resend-invitation", srv.URL, id), "admin-token-pending-invite-resend", "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var decoded decodedResendInvitationResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, body)
	}
	if decoded.ID != id {
		t.Errorf("response id = %d, want %d", decoded.ID, id)
	}
	if decoded.ConfirmSentAt == nil || *decoded.ConfirmSentAt == "" {
		t.Error("response confirm_sent_at is empty, want set")
	}
	if decoded.ConfirmExpiresAt == nil || *decoded.ConfirmExpiresAt == "" {
		t.Error("response confirm_expires_at is empty, want set")
	}
	if decoded.InviteResentAt == nil || *decoded.InviteResentAt == "" {
		t.Error("response invite_resent_at is empty, want set")
	}

	var newToken string
	if err := pool.QueryRow(context.Background(), `SELECT confirm_token FROM subscribers WHERE id = $1`, id).Scan(&newToken); err != nil {
		t.Fatalf("select new token: %v", err)
	}
	if newToken == originalToken {
		t.Errorf("confirm_token unchanged after resend — want a freshly minted token")
	}

	var auditCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`, audit.ActionSubscriberResendInvitation, id,
	).Scan(&auditCount); err != nil {
		t.Fatalf("counting audit rows: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("audit rows for invitation resend = %d, want 1", auditCount)
	}

	var auditMetadataText string
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata::text FROM audit_log WHERE action = $1 AND target_id = $2`, audit.ActionSubscriberResendInvitation, id,
	).Scan(&auditMetadataText); err != nil {
		t.Fatalf("selecting audit metadata: %v", err)
	}
	if !strings.Contains(auditMetadataText, `"import_id"`) {
		t.Errorf("audit metadata missing import_id: %s", auditMetadataText)
	}
	if email != "" && strings.Contains(auditMetadataText, email) {
		t.Errorf("audit metadata unexpectedly contains the subscriber's email: %s", auditMetadataText)
	}
}

func TestAdminPending_ResendInvitation_NotFoundReturns404(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()
	setPhysicalAddressSetting(t, pool, "123 Main St, San Francisco, CA 94110")

	admin := seedAdmin(t, pool, "admin-pending-invite-resend-404@example.com")
	seedSession(t, pool, admin, "admin-token-pending-invite-resend-404")

	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/subscribers/999999999/resend-invitation", "admin-token-pending-invite-resend-404", "")
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404 (body=%s)", resp.StatusCode, body)
	}
}

// TestAdminPending_ResendInvitation_SecondCallReturns409 is #0367 criterion
// 1's "409 on a second call" — the one-ever bound (issues/0312.md's
// approved PRD §6.10.1 deviation) mapped to HTTP.
func TestAdminPending_ResendInvitation_SecondCallReturns409(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()
	setPhysicalAddressSetting(t, pool, "123 Main St, San Francisco, CA 94110")

	admin := seedAdmin(t, pool, "admin-pending-invite-resend-second@example.com")
	seedSession(t, pool, admin, "admin-token-pending-invite-resend-second")

	id, _ := seedInvitedPendingSubscriber(t, pool)
	url := fmt.Sprintf("%s/admin/subscribers/%d/resend-invitation", srv.URL, id)

	first := doJSON(t, srv.Client(), "POST", url, "admin-token-pending-invite-resend-second", "")
	if first.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(first.Body)
		t.Fatalf("first call status = %d, want 200 (body=%s)", first.StatusCode, body)
	}

	second := doJSON(t, srv.Client(), "POST", url, "admin-token-pending-invite-resend-second", "")
	if second.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("second call status = %d, want 409 (body=%s)", second.StatusCode, body)
	}
}

// TestAdminPending_ResendInvitation_BlankPhysicalAddressReturns409 is #0367
// criterion 1's "409 on a blank physical_address" — the ADVISORY pre-check
// (admin_pending.go's ResendInvitation doc comment), never the §9 gate
// itself. This test proves only that the handler's usability nicety is
// wired up; it must NOT be read as proof that a blank physical_address
// makes sending impossible — that proof is
// TestAdminResendInvitation_PhysicalAddressGateNotBypassable
// (internal/mailing/outbox_worker_test.go, #0312), which calls the STORE
// method directly with this pre-check entirely out of the picture. #0367
// criterion 3 / CLAUDE.md §9: a test at this layer must not encode the
// pre-check as *the* gate, so this one asserts only the 409 and nothing
// about OutboxWorker's own behavior.
func TestAdminPending_ResendInvitation_BlankPhysicalAddressReturns409(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()
	setPhysicalAddressSetting(t, pool, "")

	admin := seedAdmin(t, pool, "admin-pending-invite-resend-noaddr@example.com")
	seedSession(t, pool, admin, "admin-token-pending-invite-resend-noaddr")

	id, _ := seedInvitedPendingSubscriber(t, pool)

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/subscribers/%d/resend-invitation", srv.URL, id), "admin-token-pending-invite-resend-noaddr", "")
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 409 (body=%s)", resp.StatusCode, body)
	}

	// The pre-check must refuse BEFORE calling the store — no re-send should
	// have been recorded and no confirm_token rotated.
	var inviteResentAt *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT invite_resent_at FROM subscribers WHERE id = $1`, id).Scan(&inviteResentAt); err != nil {
		t.Fatalf("select invite_resent_at: %v", err)
	}
	if inviteResentAt != nil {
		t.Errorf("invite_resent_at = %v after a 409'd advisory pre-check, want nil (the one-ever re-send must not be burned)", *inviteResentAt)
	}
}

// TestAdminPending_ResendInvitation_CooldownReturns429 is #0367 criterion
// 1's 429 rate-limit path, mirroring
// TestAdminPending_Resend_CooldownReturns429.
func TestAdminPending_ResendInvitation_CooldownReturns429(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminPendingMux(pool))
	defer srv.Close()
	setPhysicalAddressSetting(t, pool, "123 Main St, San Francisco, CA 94110")

	admin := seedAdmin(t, pool, "admin-pending-invite-resend-cooldown@example.com")
	seedSession(t, pool, admin, "admin-token-pending-invite-resend-cooldown")

	id, _ := seedInvitedPendingSubscriber(t, pool)
	// ImportStore.Commit never stamps confirm_sent_at (see
	// importInvitePayload's own doc comment) — stamp it directly to "now" so
	// the cooldown has something recent to refuse against, matching
	// TestAdminResendInvitation_RefusesCooldown's own setup
	// (internal/subscribers/pending_test.go).
	if _, err := pool.Exec(context.Background(), `UPDATE subscribers SET confirm_sent_at = $2 WHERE id = $1`, id, time.Now()); err != nil {
		t.Fatalf("stamping confirm_sent_at: %v", err)
	}

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/subscribers/%d/resend-invitation", srv.URL, id), "admin-token-pending-invite-resend-cooldown", "")
	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 429 (body=%s)", resp.StatusCode, body)
	}
}
