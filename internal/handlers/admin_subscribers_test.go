package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// adminSubscribersTestPool returns the package's single shared pool (opened
// once in TestMain — #0091) or skips if TEST_DATABASE_URL was unset. Like
// interestsTestPool (interests_test.go), it truncates ONLY the auth tables —
// never `interests` (the seeded taxonomy) or `subscribers` (shared with
// #0026's own concurrent test runs against TEST_DATABASE_URL, CLAUDE.md §8b).
// Every row this file creates is scoped with a "zz-subtest-" email/slug
// prefix and cleaned up individually via t.Cleanup.
func adminSubscribersTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	truncateCredsTables(t, testDBPool)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = testDBPool.Exec(ctx, `DELETE FROM subscribers WHERE email LIKE 'zz-subtest-%'`)
		// #0033: Suppress/ClearComplaint now also write/remove real
		// suppressions rows for the same scoped emails; clean those up too
		// so a suppressions row never outlives the subscribers row a later
		// test run's testSubscriberEmail(t) could coincidentally reuse.
		_, _ = testDBPool.Exec(ctx, `DELETE FROM suppressions WHERE email LIKE 'zz-subtest-%'`)
	})
	return testDBPool
}

// testSubscriberEmail returns an email scoped to this test run.
func testSubscriberEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-subtest-%d@example.com", time.Now().UnixNano())
}

// newTestSubscribeHandler builds a real *SubscribeHandler over the given
// pool's subscribers/interests stores, backed by a RecordingMailer (no
// network) — the same dependency Create (admin_subscribers.go) drives
// through its unexported newSignup/existingSignup dispatch.
func newTestSubscribeHandler(pool *pgxpool.Pool) *SubscribeHandler {
	return NewSubscribeHandler(
		subscribers.NewStore(pool), interests.NewStore(pool), &mailing.RecordingMailer{},
		NoSuppressions{}, nil, nil, "http://localhost:8080", slog.Default(),
	)
}

// adminSubscribersMux wires the real admin subscribers routes guarded by
// RequireSession then RequireAdmin, backed by real stores — mirrors
// adminInterestsMux (interests_test.go). manualAdd may be nil, matching
// Create's own nil-tolerance (503).
func adminSubscribersMux(pool *pgxpool.Pool, manualAdd *SubscribeHandler) http.Handler {
	authStore := auth.NewStore(pool)
	subStore := subscribers.NewStore(pool)
	interestsStore := interests.NewStore(pool)
	suppressionsStore := subscribers.NewSuppressionStore(pool)
	h := NewAdminSubscribersHandler(subStore, interestsStore, manualAdd, suppressionsStore, audit.New(pool))
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/subscribers", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("POST /admin/subscribers", requireAdmin(http.HandlerFunc(h.Create)))
	mux.Handle("GET /admin/subscribers/{id}", requireAdmin(http.HandlerFunc(h.Get)))
	mux.Handle("POST /admin/subscribers/{id}/suppress", requireAdmin(http.HandlerFunc(h.Suppress)))
	mux.Handle("POST /admin/subscribers/{id}/clear-complaint", requireAdmin(http.HandlerFunc(h.ClearComplaint)))
	return mux
}

// readBody is defined in subscribe_test.go (same package, same signature) —
// reused here rather than redeclared.

// auditActionsForSubscriberTarget returns the ordered audit_log.action
// values whose target_type='subscriber' and target_id=id, newest last.
func auditActionsForSubscriberTarget(t *testing.T, pool *pgxpool.Pool, id int64) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT action FROM audit_log WHERE target_type = 'subscriber' AND target_id = $1 ORDER BY id ASC`, id)
	if err != nil {
		t.Fatalf("query audit actions: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("scan audit action: %v", err)
		}
		out = append(out, a)
	}
	return out
}

// ── Authorization guard ──────────────────────────────────────────────────────

// TestAdminSubscribers_NonAdminForbidden mirrors
// TestAdminInterests_NonAdminForbidden (interests_test.go): a non-admin with
// a VALID session is rejected 403 on every route, proving RequireAdmin
// guards them at this handler-local mux. The authoritative, single-source
// proof that main.go's REAL mountAndServe wires these routes the same way
// lives in cmd/opencircuit/admin_wiring_test.go
// (TestMountAndServe_AdminRoutesRequireSessionAndAdmin), which enumerates
// adminRoutes() automatically — this test is the defense-in-depth layer at
// the handler level, matching every other admin handler's own suite.
func TestAdminSubscribers_NonAdminForbidden(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	user := seedUser(t, pool, "regular-subs@example.com") // is_admin = FALSE
	seedSession(t, pool, user, "user-token-subs")

	target := seedTestSubscriber(t, pool, subscribers.StatusPending)

	cases := []struct {
		method, path string
	}{
		{"GET", "/admin/subscribers"},
		{"POST", "/admin/subscribers"},
		{"GET", fmt.Sprintf("/admin/subscribers/%d", target)},
		{"POST", fmt.Sprintf("/admin/subscribers/%d/suppress", target)},
		{"POST", fmt.Sprintf("/admin/subscribers/%d/clear-complaint", target)},
	}
	client := srv.Client()
	for _, c := range cases {
		resp := doJSON(t, client, c.method, srv.URL+c.path, "user-token-subs", "")
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s with non-admin session: status = %d, want 403", c.method, c.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// seedTestSubscriber inserts a subscribers row directly at the given status
// (bypassing the store's guarded status mutators, which is fine here — this
// is test setup, not the code under test) and registers cleanup.
func seedTestSubscriber(t *testing.T, pool *pgxpool.Pool, status string) int64 {
	t.Helper()
	ctx := context.Background()
	email := testSubscriberEmail(t)
	token := fmt.Sprintf("zz-subtest-token-%d", time.Now().UnixNano())
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO subscribers (email, status, manage_token, signup_ip, signup_user_agent,
		                          utm_source, utm_medium, utm_campaign, confirmed_at)
		 VALUES ($1, $2, $3, '203.0.113.5', 'zz-test-agent/1.0', 'newsletter', 'email', 'launch',
		         CASE WHEN $2 IN ('active','unsubscribed','bounced','complained') THEN now() ELSE NULL END)
		 RETURNING id`,
		email, status, token,
	).Scan(&id); err != nil {
		t.Fatalf("seed subscriber at status %q: %v", status, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM subscribers WHERE id = $1`, id)
	})
	return id
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestAdminSubscribers_List_FiltersAndCounts(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-list@example.com")
	seedSession(t, pool, admin, "admin-token-list")

	pendingID := seedTestSubscriber(t, pool, subscribers.StatusPending)
	complainedID := seedTestSubscriber(t, pool, subscribers.StatusComplained)

	client := srv.Client()
	resp := doJSON(t, client, "GET", srv.URL+"/admin/subscribers?status=complained&per_page=200", "admin-token-list", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", resp.StatusCode)
	}
	var listResp struct {
		Subscribers []struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"subscribers"`
		Total  int64 `json:"total"`
		Counts struct {
			Complained int64 `json:"complained"`
			Pending    int64 `json:"pending"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(readBody(t, resp), &listResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	foundComplained, foundPending := false, false
	for _, s := range listResp.Subscribers {
		if s.ID == pendingID {
			foundPending = true
		}
		if s.ID == complainedID {
			foundComplained = true
		}
		if s.Status != subscribers.StatusComplained {
			t.Errorf("row %d has status %q, filter was status=complained", s.ID, s.Status)
		}
	}
	if !foundComplained {
		t.Error("status=complained filter did not include the complained subscriber")
	}
	if foundPending {
		t.Error("status=complained filter incorrectly included the pending subscriber")
	}
	if listResp.Counts.Complained < 1 {
		t.Errorf("Counts.Complained = %d, want >= 1", listResp.Counts.Complained)
	}
	if listResp.Counts.Pending < 1 {
		t.Errorf("Counts.Pending = %d, want >= 1 (counts must reflect the whole table, not just the filtered page)", listResp.Counts.Pending)
	}
}

func TestAdminSubscribers_List_InvalidStatusRejected(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-badstatus@example.com")
	seedSession(t, pool, admin, "admin-token-badstatus")

	client := srv.Client()
	resp := doJSON(t, client, "GET", srv.URL+"/admin/subscribers?status=bogus", "admin-token-badstatus", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestAdminSubscribers_Get_ReturnsConsentEvidenceAndInterests(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-get@example.com")
	seedSession(t, pool, admin, "admin-token-get")

	id := seedTestSubscriber(t, pool, subscribers.StatusActive)
	iotaID := mustSeededInterestID(t, pool, "microcontrollers")
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO subscriber_interests (subscriber_id, interest_id) VALUES ($1, $2)`, id, iotaID,
	); err != nil {
		t.Fatalf("link interest: %v", err)
	}

	client := srv.Client()
	resp := doJSON(t, client, "GET", fmt.Sprintf("%s/admin/subscribers/%d", srv.URL, id), "admin-token-get", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var view struct {
		SignupIP        *string `json:"signup_ip"`
		SignupUserAgent *string `json:"signup_user_agent"`
		UTMSource       *string `json:"utm_source"`
		ConfirmedAt     *string `json:"confirmed_at"`
		CreatedAt       string  `json:"created_at"`
		Interests       []struct {
			ID   int64  `json:"id"`
			Slug string `json:"slug"`
		} `json:"interests"`
		EmailEvents []any `json:"email_events"`
	}
	if err := json.Unmarshal(readBody(t, resp), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.SignupIP == nil || *view.SignupIP != "203.0.113.5" {
		t.Errorf("SignupIP = %v, want 203.0.113.5", view.SignupIP)
	}
	if view.SignupUserAgent == nil || *view.SignupUserAgent != "zz-test-agent/1.0" {
		t.Errorf("SignupUserAgent = %v, want zz-test-agent/1.0", view.SignupUserAgent)
	}
	if view.UTMSource == nil || *view.UTMSource != "newsletter" {
		t.Errorf("UTMSource = %v, want newsletter", view.UTMSource)
	}
	if view.ConfirmedAt == nil {
		t.Error("ConfirmedAt = nil, want set (seeded active subscriber)")
	}
	if view.CreatedAt == "" {
		t.Error("CreatedAt is empty")
	}
	if len(view.Interests) != 1 || view.Interests[0].Slug != "microcontrollers" {
		t.Errorf("Interests = %+v, want exactly [microcontrollers]", view.Interests)
	}
	if view.EmailEvents == nil {
		t.Error("EmailEvents = nil, want [] (present, empty — #0038 fills it in later)")
	}
}

func TestAdminSubscribers_Get_NotFound(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-get404@example.com")
	seedSession(t, pool, admin, "admin-token-get404")

	client := srv.Client()
	resp := doJSON(t, client, "GET", srv.URL+"/admin/subscribers/999999999", "admin-token-get404", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// ── Suppress ─────────────────────────────────────────────────────────────────

func TestAdminSubscribers_Suppress_RequiresNote(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-suppress-note@example.com")
	seedSession(t, pool, admin, "admin-token-suppress-note")
	id := seedTestSubscriber(t, pool, subscribers.StatusActive)

	client := srv.Client()
	resp := doJSON(t, client, "POST", fmt.Sprintf("%s/admin/subscribers/%d/suppress", srv.URL, id),
		"admin-token-suppress-note", `{"note":"  "}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a blank note", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestAdminSubscribers_Suppress_ActiveSubscriberBecomesUnsubscribed proves the
// ordinary case: suppressing an active subscriber unsubscribes it and audits
// the action.
func TestAdminSubscribers_Suppress_ActiveSubscriberBecomesUnsubscribed(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-suppress-ok@example.com")
	seedSession(t, pool, admin, "admin-token-suppress-ok")
	id := seedTestSubscriber(t, pool, subscribers.StatusActive)

	client := srv.Client()
	resp := doJSON(t, client, "POST", fmt.Sprintf("%s/admin/subscribers/%d/suppress", srv.URL, id),
		"admin-token-suppress-ok", `{"note":"requested by phone"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Subscriber struct {
			Status string `json:"status"`
		} `json:"subscriber"`
		NoOp bool `json:"no_op"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Subscriber.Status != subscribers.StatusUnsubscribed {
		t.Errorf("resulting status = %q, want %q", out.Subscriber.Status, subscribers.StatusUnsubscribed)
	}
	if out.NoOp {
		t.Error("NoOp = true for an active subscriber, want false (the action took effect)")
	}

	actions := auditActionsForSubscriberTarget(t, pool, id)
	found := false
	for _, a := range actions {
		if a == audit.ActionSubscriberSuppressed {
			found = true
		}
	}
	if !found {
		t.Errorf("audit actions for subscriber %d = %v, want to include %q", id, actions, audit.ActionSubscriberSuppressed)
	}
}

// TestAdminSubscribers_Suppress_ComplainedRowIsNoOp is the carried-in review
// finding's exact scenario: Unsubscribe silently changes nothing on a
// complained row, and the handler must report that rather than imply success.
func TestAdminSubscribers_Suppress_ComplainedRowIsNoOp(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-suppress-noop@example.com")
	seedSession(t, pool, admin, "admin-token-suppress-noop")
	id := seedTestSubscriber(t, pool, subscribers.StatusComplained)

	client := srv.Client()
	resp := doJSON(t, client, "POST", fmt.Sprintf("%s/admin/subscribers/%d/suppress", srv.URL, id),
		"admin-token-suppress-noop", `{"note":"trying to suppress a complained row"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (Unsubscribe never errors, it just no-ops)", resp.StatusCode)
	}
	var out struct {
		Subscriber struct {
			Status string `json:"status"`
		} `json:"subscriber"`
		NoOp bool `json:"no_op"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Subscriber.Status != subscribers.StatusComplained {
		t.Errorf("resulting status = %q, want unchanged %q", out.Subscriber.Status, subscribers.StatusComplained)
	}
	if !out.NoOp {
		t.Error("NoOp = false for a complained row, want true — the screen must not imply the action took effect")
	}
}

// ── ClearComplaint ───────────────────────────────────────────────────────────

func TestAdminSubscribers_ClearComplaint_NotComplainedConflict(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-clear-conflict@example.com")
	seedSession(t, pool, admin, "admin-token-clear-conflict")
	id := seedTestSubscriber(t, pool, subscribers.StatusActive)

	client := srv.Client()
	resp := doJSON(t, client, "POST", fmt.Sprintf("%s/admin/subscribers/%d/clear-complaint", srv.URL, id),
		"admin-token-clear-conflict", "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (subscriber is not complained; the screen should only offer this action on complained rows)", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAdminSubscribers_ClearComplaint_Success(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-clear-ok@example.com")
	seedSession(t, pool, admin, "admin-token-clear-ok")
	id := seedTestSubscriber(t, pool, subscribers.StatusComplained)

	client := srv.Client()
	resp := doJSON(t, client, "POST", fmt.Sprintf("%s/admin/subscribers/%d/clear-complaint", srv.URL, id),
		"admin-token-clear-ok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Subscriber struct {
			Status string `json:"status"`
		} `json:"subscriber"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// AdminClearComplaint's documented result is `unsubscribed`, not
	// `active` — clearing a complaint does not by itself re-establish
	// double opt-in consent.
	if out.Subscriber.Status != subscribers.StatusUnsubscribed {
		t.Errorf("resulting status = %q, want %q", out.Subscriber.Status, subscribers.StatusUnsubscribed)
	}

	actions := auditActionsForSubscriberTarget(t, pool, id)
	found := false
	for _, a := range actions {
		if a == audit.ActionSubscriberComplaintCleared {
			found = true
		}
	}
	if !found {
		t.Errorf("audit actions for subscriber %d = %v, want to include %q", id, actions, audit.ActionSubscriberComplaintCleared)
	}
}

// subscriberEmailByID reads back the email a seedTestSubscriber call
// generated, so a test can check the suppressions table (keyed by email, not
// subscriber id) for the same address.
func subscriberEmailByID(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var email string
	if err := pool.QueryRow(context.Background(), `SELECT email FROM subscribers WHERE id = $1`, id).Scan(&email); err != nil {
		t.Fatalf("read back subscriber %d email: %v", id, err)
	}
	return email
}

// TestAdminSubscribers_Suppress_WritesRealSuppressionRow is #0033's carried-in
// review finding (from #0032): Suppress must write a real suppressions-table
// row, not just flip the subscriber's status, so a future resignup by this
// address is actually blocked at #0026's suppressed send gate.
func TestAdminSubscribers_Suppress_WritesRealSuppressionRow(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-suppress-real@example.com")
	seedSession(t, pool, admin, "admin-token-suppress-real")
	id := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, id)

	client := srv.Client()
	resp := doJSON(t, client, "POST", fmt.Sprintf("%s/admin/subscribers/%d/suppress", srv.URL, id),
		"admin-token-suppress-real", `{"note":"real suppression row check"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		SuppressionAdded bool `json:"suppression_added"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.SuppressionAdded {
		t.Error("suppression_added = false, want true")
	}

	suppressionsStore := subscribers.NewSuppressionStore(pool)
	suppressed, err := suppressionsStore.IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Errorf("IsSuppressed(%q) = false after Suppress, want true — no real suppressions row was written", email)
	}
}

// TestAdminSubscribers_ClearComplaint_RemovesSuppressionRow is #0033's
// carried-in review finding's other half: clearing a complaint must remove
// any matching suppressions row, or the address stays permanently blocked at
// the send gate despite the admin's clear-complaint action.
func TestAdminSubscribers_ClearComplaint_RemovesSuppressionRow(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-clear-suppression@example.com")
	seedSession(t, pool, admin, "admin-token-clear-suppression")
	id := seedTestSubscriber(t, pool, subscribers.StatusComplained)
	email := subscriberEmailByID(t, pool, id)

	suppressionsStore := subscribers.NewSuppressionStore(pool)
	// Simulate what #0038's SES complaint ingestion will eventually do
	// itself: a real suppressions row alongside the complained status.
	if _, err := suppressionsStore.Add(context.Background(), subscribers.NewSuppression{
		Email:  email,
		Reason: subscribers.SuppressionReasonComplaint,
	}, time.Now()); err != nil {
		t.Fatalf("seed suppression: %v", err)
	}

	client := srv.Client()
	resp := doJSON(t, client, "POST", fmt.Sprintf("%s/admin/subscribers/%d/clear-complaint", srv.URL, id),
		"admin-token-clear-suppression", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	suppressed, err := suppressionsStore.IsSuppressed(context.Background(), email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Errorf("IsSuppressed(%q) = true after ClearComplaint, want false — the suppression row should have been removed", email)
	}
}

// TestAdminSubscribers_ClearComplaint_RemovesOnlyComplaintSuppression is
// #0100's central end-to-end case: an address suppressed for BOTH
// hard_bounce and complaint must have ONLY the complaint row removed by
// ClearComplaint, with the response message and audit metadata both naming
// the surviving hard_bounce reason.
func TestAdminSubscribers_ClearComplaint_RemovesOnlyComplaintSuppression(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-clear-scoped@example.com")
	seedSession(t, pool, admin, "admin-token-clear-scoped")
	id := seedTestSubscriber(t, pool, subscribers.StatusComplained)
	email := subscriberEmailByID(t, pool, id)

	suppressionsStore := subscribers.NewSuppressionStore(pool)
	ctx := context.Background()
	now := time.Now()
	if _, err := suppressionsStore.Add(ctx, subscribers.NewSuppression{
		Email:  email,
		Reason: subscribers.SuppressionReasonHardBounce,
	}, now); err != nil {
		t.Fatalf("seed hard_bounce suppression: %v", err)
	}
	if _, err := suppressionsStore.Add(ctx, subscribers.NewSuppression{
		Email:  email,
		Reason: subscribers.SuppressionReasonComplaint,
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("seed complaint suppression: %v", err)
	}

	client := srv.Client()
	resp := doJSON(t, client, "POST", fmt.Sprintf("%s/admin/subscribers/%d/clear-complaint", srv.URL, id),
		"admin-token-clear-scoped", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(out.Message, "hard_bounce") {
		t.Errorf("message = %q, want it to name the surviving hard_bounce reason", out.Message)
	}

	// The complaint row is gone, but the hard_bounce row must survive — the
	// address must STILL be suppressed at the send gate.
	rows, err := suppressionsStore.ListByEmail(ctx, email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	if len(rows) != 1 || rows[0].Reason != subscribers.SuppressionReasonHardBounce {
		t.Fatalf("ListByEmail = %+v, want exactly one hard_bounce row", rows)
	}
	suppressed, err := suppressionsStore.IsSuppressed(ctx, email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Error("IsSuppressed = false, want true — the hard_bounce suppression must still block this address")
	}

	var found map[string]any
	rowsAudit, err := pool.Query(ctx,
		`SELECT metadata FROM audit_log WHERE target_type = 'subscriber' AND target_id = $1 AND action = $2 ORDER BY id DESC LIMIT 1`,
		id, audit.ActionSubscriberComplaintCleared)
	if err != nil {
		t.Fatalf("query audit metadata: %v", err)
	}
	defer rowsAudit.Close()
	if rowsAudit.Next() {
		if err := rowsAudit.Scan(&found); err != nil {
			t.Fatalf("scan audit metadata: %v", err)
		}
	} else {
		t.Fatal("no audit row found for ActionSubscriberComplaintCleared")
	}
	remaining, ok := found["suppressions_remaining"].([]any)
	if !ok || len(remaining) != 1 || remaining[0] != subscribers.SuppressionReasonHardBounce {
		t.Errorf("audit suppressions_remaining = %v, want [%q]", found["suppressions_remaining"], subscribers.SuppressionReasonHardBounce)
	}
	if found["suppression_removed_reason"] != subscribers.SuppressionReasonComplaint {
		t.Errorf("audit suppression_removed_reason = %v, want %q", found["suppression_removed_reason"], subscribers.SuppressionReasonComplaint)
	}
}

// TestAdminSubscribers_ClearComplaint_MessageWhenNothingRemains proves the
// other branch of §5a's four-case message table: when the complaint row is
// removed and nothing else is suppressed, the message says so instead of the
// old "removed any matching entry" over-claim.
func TestAdminSubscribers_ClearComplaint_MessageWhenNothingRemains(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-clear-nothing-left@example.com")
	seedSession(t, pool, admin, "admin-token-clear-nothing-left")
	id := seedTestSubscriber(t, pool, subscribers.StatusComplained)
	email := subscriberEmailByID(t, pool, id)

	suppressionsStore := subscribers.NewSuppressionStore(pool)
	ctx := context.Background()
	if _, err := suppressionsStore.Add(ctx, subscribers.NewSuppression{
		Email:  email,
		Reason: subscribers.SuppressionReasonComplaint,
	}, time.Now()); err != nil {
		t.Fatalf("seed complaint suppression: %v", err)
	}

	client := srv.Client()
	resp := doJSON(t, client, "POST", fmt.Sprintf("%s/admin/subscribers/%d/clear-complaint", srv.URL, id),
		"admin-token-clear-nothing-left", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(out.Message, "no other suppression remains") {
		t.Errorf("message = %q, want it to say no other suppression remains", out.Message)
	}
	if strings.Contains(out.Message, "resignup is no longer blocked") {
		t.Errorf("message = %q, still carries the old over-claiming phrasing #0100 removed", out.Message)
	}

	suppressed, err := suppressionsStore.IsSuppressed(ctx, email)
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("IsSuppressed = true, want false — nothing should remain blocking this address")
	}
}

// ── Create (manual add) ──────────────────────────────────────────────────────

// TestAdminSubscribers_Create_NewAddressEndsUpPendingNeverActive is #0032's
// central acceptance criterion: "no bypassing double opt-in." A brand-new
// address must land `pending`, never `active`, regardless of who added it.
func TestAdminSubscribers_Create_NewAddressEndsUpPendingNeverActive(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	sh := newTestSubscribeHandler(pool)
	srv := httptest.NewServer(adminSubscribersMux(pool, sh))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-create@example.com")
	seedSession(t, pool, admin, "admin-token-create")

	email := testSubscriberEmail(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM subscribers WHERE email = lower(trim($1))`, email)
	})

	client := srv.Client()
	body := fmt.Sprintf(`{"email":%q,"interests":[],"note":"added from a workshop sign-up sheet"}`, email)
	resp := doJSON(t, client, "POST", srv.URL+"/admin/subscribers", "admin-token-create", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out struct {
		Subscriber struct {
			Status          string  `json:"status"`
			SignupIP        *string `json:"signup_ip"`
			SignupUserAgent *string `json:"signup_user_agent"`
		} `json:"subscriber"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Subscriber.Status != subscribers.StatusPending {
		t.Fatalf("resulting status = %q, want %q — manual add must never create an active subscriber directly", out.Subscriber.Status, subscribers.StatusPending)
	}
	if out.Subscriber.SignupIP != nil {
		t.Errorf("SignupIP = %v, want nil — a manually-added subscriber has no browser-submitted consent evidence to fabricate", out.Subscriber.SignupIP)
	}

	// Confirm this via an independent store read too, not just the response.
	sub, err := subscribers.NewStore(pool).FindByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if sub.Status != subscribers.StatusPending {
		t.Fatalf("stored status = %q, want %q", sub.Status, subscribers.StatusPending)
	}
	if sub.ConfirmToken == nil {
		t.Error("ConfirmToken = nil, want set — a confirmation link must exist for this pending signup")
	}

	actions := auditActionsForSubscriberTarget(t, pool, sub.ID)
	found := false
	for _, a := range actions {
		if a == audit.ActionSubscriberManualAdd {
			found = true
		}
	}
	if !found {
		t.Errorf("audit actions for subscriber %d = %v, want to include %q", sub.ID, actions, audit.ActionSubscriberManualAdd)
	}
}

func TestAdminSubscribers_Create_ComplainedAddressNeverResubscribed(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	sh := newTestSubscribeHandler(pool)
	srv := httptest.NewServer(adminSubscribersMux(pool, sh))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-create-complained@example.com")
	seedSession(t, pool, admin, "admin-token-create-complained")

	id := seedTestSubscriber(t, pool, subscribers.StatusComplained)
	var email string
	if err := pool.QueryRow(context.Background(), `SELECT email FROM subscribers WHERE id = $1`, id).Scan(&email); err != nil {
		t.Fatalf("read seeded email: %v", err)
	}

	client := srv.Client()
	body := fmt.Sprintf(`{"email":%q,"interests":[]}`, email)
	resp := doJSON(t, client, "POST", srv.URL+"/admin/subscribers", "admin-token-create-complained", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out struct {
		Subscriber struct {
			Status string `json:"status"`
		} `json:"subscriber"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Subscriber.Status != subscribers.StatusComplained {
		t.Fatalf("resulting status = %q, want unchanged %q — manual add must never auto-resubscribe a complained address (CLAUDE.md §9)", out.Subscriber.Status, subscribers.StatusComplained)
	}
}

// TestAdminSubscribers_Create_ExistingPendingResendsConfirmation proves
// Create actually dispatches on the existing row's status (via
// existingSignup) rather than blindly treating every address as brand new:
// an existing PENDING subscriber must get its confirmation *resent*
// (confirm_sent_at stamped), which only happens on the existingSignup path —
// a naive "always call newSignup" implementation would hit
// subscribers.Store.Create's ErrEmailExists guard and silently do nothing at
// all, leaving confirm_sent_at untouched.
func TestAdminSubscribers_Create_ExistingPendingResendsConfirmation(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	sh := newTestSubscribeHandler(pool)
	srv := httptest.NewServer(adminSubscribersMux(pool, sh))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-create-resend@example.com")
	seedSession(t, pool, admin, "admin-token-create-resend")

	id := seedTestSubscriber(t, pool, subscribers.StatusPending)
	var email string
	if err := pool.QueryRow(context.Background(), `SELECT email FROM subscribers WHERE id = $1`, id).Scan(&email); err != nil {
		t.Fatalf("read seeded email: %v", err)
	}
	// seedTestSubscriber leaves confirm_sent_at NULL (it inserts directly,
	// bypassing Store.Create) and has no confirm_token — give it one so
	// sendConfirmation's claim has something to act on, matching a real
	// pending row.
	if _, err := pool.Exec(context.Background(),
		`UPDATE subscribers SET confirm_token = $2, confirm_expires_at = now() + interval '7 days' WHERE id = $1`,
		id, fmt.Sprintf("zz-subtest-confirmtok-%d", time.Now().UnixNano()),
	); err != nil {
		t.Fatalf("seed confirm token: %v", err)
	}

	client := srv.Client()
	body := fmt.Sprintf(`{"email":%q,"interests":[]}`, email)
	resp := doJSON(t, client, "POST", srv.URL+"/admin/subscribers", "admin-token-create-resend", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	sh.waitForSends()

	var confirmSentAt *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT confirm_sent_at FROM subscribers WHERE id = $1`, id).Scan(&confirmSentAt); err != nil {
		t.Fatalf("read confirm_sent_at: %v", err)
	}
	if confirmSentAt == nil {
		t.Error("confirm_sent_at = nil after manual-add on an existing pending subscriber, want stamped — the resend (existingSignup) path was not taken")
	}
}

func TestAdminSubscribers_Create_ManualAddUnavailableReturns503(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, nil)) // manualAdd = nil, matching dev-mode wiring
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-create-unavail@example.com")
	seedSession(t, pool, admin, "admin-token-create-unavail")

	client := srv.Client()
	resp := doJSON(t, client, "POST", srv.URL+"/admin/subscribers", "admin-token-create-unavail", `{"email":"whoever@example.com"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	resp.Body.Close()
}
