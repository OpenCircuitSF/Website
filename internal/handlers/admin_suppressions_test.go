package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// adminSuppressionsMux wires the real admin suppressions routes guarded by
// RequireSession then RequireAdmin, backed by real stores — mirrors
// adminSubscribersMux (admin_subscribers_test.go).
func adminSuppressionsMux(pool *pgxpool.Pool) http.Handler {
	authStore := auth.NewStore(pool)
	subStore := subscribers.NewStore(pool)
	suppressionsStore := subscribers.NewSuppressionStore(pool)
	h := NewAdminSuppressionsHandler(suppressionsStore, subStore, audit.New(pool))
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/suppressions", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("POST /admin/suppressions/remove", requireAdmin(http.HandlerFunc(h.Remove)))
	return mux
}

func uniqueAdminSuppressionEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-subtest-suppress-%d@example.com", time.Now().UnixNano())
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestAdminSuppressions_List_ReturnsRowsWithSubscriberStatus(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSuppressionsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-suppr-list@example.com")
	seedSession(t, pool, admin, "admin-token-suppr-list")

	ctx := context.Background()
	suppressionsStore := subscribers.NewSuppressionStore(pool)

	// One suppression WITH a live subscriber row.
	subID := seedTestSubscriber(t, pool, subscribers.StatusUnsubscribed)
	withSubEmail := subscriberEmailByID(t, pool, subID)
	if _, err := suppressionsStore.Add(ctx, subscribers.NewSuppression{
		Email:  withSubEmail,
		Reason: subscribers.SuppressionReasonManual,
		Note:   "seeded for list test",
	}, time.Now()); err != nil {
		t.Fatalf("seed suppression with subscriber: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM suppressions WHERE email = $1`, withSubEmail)
	})

	// One orphaned suppression — no subscribers row at all.
	orphanEmail := uniqueAdminSuppressionEmail(t)
	if _, err := suppressionsStore.Add(ctx, subscribers.NewSuppression{
		Email:  orphanEmail,
		Reason: subscribers.SuppressionReasonHardBounce,
	}, time.Now()); err != nil {
		t.Fatalf("seed orphan suppression: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM suppressions WHERE email = $1`, orphanEmail)
	})

	client := srv.Client()
	resp := doJSON(t, client, "GET", srv.URL+"/admin/suppressions", "admin-token-suppr-list", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Suppressions []struct {
			Email            string  `json:"email"`
			Reason           string  `json:"reason"`
			SubscriberStatus *string `json:"subscriber_status"`
		} `json:"suppressions"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count != len(out.Suppressions) {
		t.Errorf("count = %d, len(suppressions) = %d, want equal", out.Count, len(out.Suppressions))
	}

	var sawWithSub, sawOrphan bool
	for _, s := range out.Suppressions {
		switch s.Email {
		case withSubEmail:
			sawWithSub = true
			if s.SubscriberStatus == nil || *s.SubscriberStatus != subscribers.StatusUnsubscribed {
				t.Errorf("withSub row SubscriberStatus = %v, want %q", s.SubscriberStatus, subscribers.StatusUnsubscribed)
			}
		case orphanEmail:
			sawOrphan = true
			if s.SubscriberStatus != nil {
				t.Errorf("orphan row SubscriberStatus = %v, want nil", *s.SubscriberStatus)
			}
		}
	}
	if !sawWithSub {
		t.Error("list did not include the suppression with a live subscriber row")
	}
	if !sawOrphan {
		t.Error("list did not include the orphaned suppression")
	}
}

// ── Remove ────────────────────────────────────────────────────────────────────

func TestAdminSuppressions_Remove_RequiresNote(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSuppressionsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-suppr-note@example.com")
	seedSession(t, pool, admin, "admin-token-suppr-note")

	suppressionsStore := subscribers.NewSuppressionStore(pool)
	email := uniqueAdminSuppressionEmail(t)
	if _, err := suppressionsStore.Add(context.Background(), subscribers.NewSuppression{
		Email:  email,
		Reason: subscribers.SuppressionReasonManual,
	}, time.Now()); err != nil {
		t.Fatalf("seed suppression: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM suppressions WHERE email = $1`, email)
	})

	client := srv.Client()
	body := fmt.Sprintf(`{"email":%q,"reason":"manual","note":"  "}`, email)
	resp := doJSON(t, client, "POST", srv.URL+"/admin/suppressions/remove", "admin-token-suppr-note", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a blank note", resp.StatusCode)
	}
	resp.Body.Close()

	suppressed, err := suppressionsStore.IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Error("IsSuppressed = false after a rejected (note-less) removal request, want true — nothing should have been removed")
	}
}

// TestAdminSuppressions_Remove_ManualSuppressionIsReversible is #0100's
// criterion 1's round trip: suppress a subscriber through the existing
// admin action, then reverse it here, and confirm it is no longer
// suppressed AND that the subscriber's status is untouched (still
// `unsubscribed`, never silently flipped back to `active`; #0100 plan §6).
func TestAdminSuppressions_Remove_ManualSuppressionIsReversible(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	subscribersSrv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer subscribersSrv.Close()
	suppressionsSrv := httptest.NewServer(adminSuppressionsMux(pool))
	defer suppressionsSrv.Close()

	admin := seedAdmin(t, pool, "admin-suppr-reversible@example.com")
	seedSession(t, pool, admin, "admin-token-suppr-reversible")
	id := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, id)

	client := subscribersSrv.Client()
	resp := doJSON(t, client, "POST", fmt.Sprintf("%s/admin/subscribers/%d/suppress", subscribersSrv.URL, id),
		"admin-token-suppr-reversible", `{"note":"mis-click, reverse it"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("suppress status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	suppressionsStore := subscribers.NewSuppressionStore(pool)
	if suppressed, err := suppressionsStore.IsSuppressed(context.Background(), email); err != nil {
		t.Fatalf("IsSuppressed after Suppress: %v", err)
	} else if !suppressed {
		t.Fatal("IsSuppressed = false after Suppress, want true (test setup failed)")
	}

	removeClient := suppressionsSrv.Client()
	body := fmt.Sprintf(`{"email":%q,"reason":"manual","note":"confirmed with the subscriber, reversing"}`, email)
	removeResp := doJSON(t, removeClient, "POST", suppressionsSrv.URL+"/admin/suppressions/remove", "admin-token-suppr-reversible", body)
	if removeResp.StatusCode != http.StatusOK {
		t.Fatalf("remove status = %d, want 200: %s", removeResp.StatusCode, string(readBody(t, removeResp)))
	}
	var out struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(readBody(t, removeResp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Message == "" {
		t.Error("message is empty")
	}

	suppressed, err := suppressionsStore.IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed after Remove: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true after removal, want false")
	}

	// §6: reversal must never change subscriber status. Suppress left this
	// row `unsubscribed`; it must still be `unsubscribed`, never `active`.
	subStore := subscribers.NewStore(pool)
	sub, err := subStore.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusUnsubscribed {
		t.Errorf("subscriber status = %q after un-suppressing, want unchanged %q — removal must never resubscribe an address", sub.Status, subscribers.StatusUnsubscribed)
	}
}

func TestAdminSuppressions_Remove_ComplaintRefusedWhenSubscriberExists(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSuppressionsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-suppr-complaint-refuse@example.com")
	seedSession(t, pool, admin, "admin-token-suppr-complaint-refuse")
	id := seedTestSubscriber(t, pool, subscribers.StatusComplained)
	email := subscriberEmailByID(t, pool, id)

	suppressionsStore := subscribers.NewSuppressionStore(pool)
	if _, err := suppressionsStore.Add(context.Background(), subscribers.NewSuppression{
		Email:  email,
		Reason: subscribers.SuppressionReasonComplaint,
	}, time.Now()); err != nil {
		t.Fatalf("seed complaint suppression: %v", err)
	}

	client := srv.Client()
	body := fmt.Sprintf(`{"email":%q,"reason":"complaint","note":"trying to bypass Clear complaint"}`, email)
	resp := doJSON(t, client, "POST", srv.URL+"/admin/suppressions/remove", "admin-token-suppr-complaint-refuse", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (a subscriber row exists; use Clear complaint instead)", resp.StatusCode)
	}
	resp.Body.Close()

	suppressed, err := suppressionsStore.IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Error("IsSuppressed = false after a refused removal, want true — the row must survive")
	}
}

func TestAdminSuppressions_Remove_ComplaintAllowedWhenOrphaned(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSuppressionsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-suppr-complaint-orphan@example.com")
	seedSession(t, pool, admin, "admin-token-suppr-complaint-orphan")

	// No subscribers row for this address at all — an orphaned suppression
	// (#0060 hard deletion, or added before any signup).
	email := uniqueAdminSuppressionEmail(t)
	suppressionsStore := subscribers.NewSuppressionStore(pool)
	if _, err := suppressionsStore.Add(context.Background(), subscribers.NewSuppression{
		Email:  email,
		Reason: subscribers.SuppressionReasonComplaint,
	}, time.Now()); err != nil {
		t.Fatalf("seed orphan complaint suppression: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM suppressions WHERE email = $1`, email)
	})

	client := srv.Client()
	body := fmt.Sprintf(`{"email":%q,"reason":"complaint","note":"orphaned complaint, safe to remove"}`, email)
	resp := doJSON(t, client, "POST", srv.URL+"/admin/suppressions/remove", "admin-token-suppr-complaint-orphan", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (an orphaned complaint suppression must be removable, or it is permanently unremovable)", resp.StatusCode)
	}
	resp.Body.Close()

	suppressed, err := suppressionsStore.IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true after removal, want false")
	}
}

func TestAdminSuppressions_Remove_NotFound(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSuppressionsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-suppr-notfound@example.com")
	seedSession(t, pool, admin, "admin-token-suppr-notfound")

	email := uniqueAdminSuppressionEmail(t) // never added

	client := srv.Client()
	body := fmt.Sprintf(`{"email":%q,"reason":"manual","note":"nothing to remove"}`, email)
	resp := doJSON(t, client, "POST", srv.URL+"/admin/suppressions/remove", "admin-token-suppr-notfound", body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAdminSuppressions_Remove_InvalidReasonRejected(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSuppressionsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-suppr-badreason@example.com")
	seedSession(t, pool, admin, "admin-token-suppr-badreason")

	email := uniqueAdminSuppressionEmail(t)
	client := srv.Client()
	body := fmt.Sprintf(`{"email":%q,"reason":"not_a_real_reason","note":"bad reason"}`, email)
	resp := doJSON(t, client, "POST", srv.URL+"/admin/suppressions/remove", "admin-token-suppr-badreason", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAdminSuppressions_Remove_WritesAuditWithRemovedAndRemaining(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSuppressionsMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-suppr-audit@example.com")
	seedSession(t, pool, admin, "admin-token-suppr-audit")
	id := seedTestSubscriber(t, pool, subscribers.StatusUnsubscribed)
	email := subscriberEmailByID(t, pool, id)

	suppressionsStore := subscribers.NewSuppressionStore(pool)
	ctx := context.Background()
	now := time.Now()
	if _, err := suppressionsStore.Add(ctx, subscribers.NewSuppression{
		Email:  email,
		Reason: subscribers.SuppressionReasonManual,
		Note:   "original manual note",
	}, now); err != nil {
		t.Fatalf("seed manual suppression: %v", err)
	}
	if _, err := suppressionsStore.Add(ctx, subscribers.NewSuppression{
		Email:  email,
		Reason: subscribers.SuppressionReasonHardBounce,
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("seed hard_bounce suppression: %v", err)
	}

	client := srv.Client()
	body := fmt.Sprintf(`{"email":%q,"reason":"manual","note":"admin justification for this removal"}`, email)
	resp := doJSON(t, client, "POST", srv.URL+"/admin/suppressions/remove", "admin-token-suppr-audit", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, string(readBody(t, resp)))
	}
	resp.Body.Close()

	var metadata map[string]any
	rows, err := pool.Query(ctx,
		`SELECT metadata FROM audit_log WHERE target_type = 'subscriber' AND target_id = $1 AND action = $2 ORDER BY id DESC LIMIT 1`,
		id, audit.ActionSuppressionRemoved)
	if err != nil {
		t.Fatalf("query audit metadata: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no audit row found for ActionSuppressionRemoved")
	}
	if err := rows.Scan(&metadata); err != nil {
		t.Fatalf("scan audit metadata: %v", err)
	}

	if metadata["email"] != email {
		t.Errorf("audit email = %v, want %q", metadata["email"], email)
	}
	if metadata["reason"] != subscribers.SuppressionReasonManual {
		t.Errorf("audit reason = %v, want %q", metadata["reason"], subscribers.SuppressionReasonManual)
	}
	if metadata["note"] != "admin justification for this removal" {
		t.Errorf("audit note = %v, want the admin's justification", metadata["note"])
	}
	if metadata["removed_note"] != "original manual note" {
		t.Errorf("audit removed_note = %v, want %q (the destroyed row's own note)", metadata["removed_note"], "original manual note")
	}
	remaining, ok := metadata["suppressions_remaining"].([]any)
	if !ok || len(remaining) != 1 || remaining[0] != subscribers.SuppressionReasonHardBounce {
		t.Errorf("audit suppressions_remaining = %v, want [%q]", metadata["suppressions_remaining"], subscribers.SuppressionReasonHardBounce)
	}
	if unchanged, ok := metadata["subscriber_status_unchanged"].(bool); !ok || !unchanged {
		t.Errorf("audit subscriber_status_unchanged = %v, want true", metadata["subscriber_status_unchanged"])
	}
	if metadata["subscriber_status"] != subscribers.StatusUnsubscribed {
		t.Errorf("audit subscriber_status = %v, want %q", metadata["subscriber_status"], subscribers.StatusUnsubscribed)
	}
}
