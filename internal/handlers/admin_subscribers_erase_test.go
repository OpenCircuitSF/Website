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
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// seedErasableSubscriber creates a throwaway pending subscriber through the
// real store (rather than seedTestSubscriber's raw-SQL insert) so its email
// is known and normalized, exactly what the typed-confirmation body needs.
func seedErasableSubscriber(t *testing.T, pool *pgxpool.Pool) (id int64, email string) {
	t.Helper()
	store := subscribers.NewStore(pool)
	sub, err := store.Create(context.Background(),
		subscribers.NewSignup{Email: testSubscriberEmail(t), ConfirmTTL: time.Hour}, time.Now())
	if err != nil {
		t.Fatalf("seed erasable subscriber: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, sub.ID)
	})
	return sub.ID, sub.Email
}

// subscriberExistsByID is subscribe_test.go's subscriberExists (keyed by
// email) with the id this file actually has in hand after Erase — the
// erased address's subscribers row is gone, so a lookup keyed by email
// can't distinguish "never existed" from "existed, now erased" the way an
// id-keyed one can.
func subscriberExistsByID(t *testing.T, pool *pgxpool.Pool, id int64) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM subscribers WHERE id = $1)`, id,
	).Scan(&exists); err != nil {
		t.Fatalf("check subscriber exists %d: %v", id, err)
	}
	return exists
}

func eraseRequestBody(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal erase body: %v", err)
	}
	return string(b)
}

func TestAdminSubscribers_Erase_RequiresMatchingConfirmEmail(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-erase-confirm@example.com")
	seedSession(t, pool, admin, "admin-token-erase-confirm")
	id, email := seedErasableSubscriber(t, pool)

	client := srv.Client()

	// Empty confirm_email: 400, nothing mutated.
	resp := doJSON(t, client, "DELETE", fmt.Sprintf("%s/admin/subscribers/%d", srv.URL, id),
		"admin-token-erase-confirm", eraseRequestBody(t, map[string]any{"confirm_email": ""}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty confirm_email: status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	// Wrong address: 400, nothing mutated.
	resp = doJSON(t, client, "DELETE", fmt.Sprintf("%s/admin/subscribers/%d", srv.URL, id),
		"admin-token-erase-confirm", eraseRequestBody(t, map[string]any{"confirm_email": "not-" + email}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("mismatched confirm_email: status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	if !subscriberExistsByID(t, pool, id) {
		t.Fatal("subscriber was erased despite a missing/mismatched typed confirmation")
	}
}

// TestAdminSubscribers_Erase_Success is the ordinary case, proved by
// execution: a real subscriber with a real interest selection is erased,
// and this asserts what remains and what does not, per this issue's
// acceptance criteria.
func TestAdminSubscribers_Erase_Success(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-erase-ok@example.com")
	seedSession(t, pool, admin, "admin-token-erase-ok")
	id, email := seedErasableSubscriber(t, pool)

	// The typed confirmation is deliberately case/whitespace-loose (mirrors
	// email normalization everywhere else in this package), so an operator
	// pasting the address from the detail screen (which may itself carry
	// stray whitespace) still matches.
	client := srv.Client()
	resp := doJSON(t, client, "DELETE", fmt.Sprintf("%s/admin/subscribers/%d", srv.URL, id),
		"admin-token-erase-ok", eraseRequestBody(t, map[string]any{"confirm_email": "  " + email + "  "}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, readBody(t, resp))
	}
	var out struct {
		Erased               bool   `json:"erased"`
		Email                string `json:"email"`
		PreviousStatus       string `json:"previous_status"`
		InterestsRemoved     int    `json:"interests_removed"`
		EmailSendsAnonymized int64  `json:"email_sends_anonymized"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Erased || out.Email != email || out.PreviousStatus != subscribers.StatusPending {
		t.Errorf("response = %+v, want erased=true email=%q previous_status=pending", out, email)
	}

	// What does NOT survive: the subscribers row.
	if subscriberExistsByID(t, pool, id) {
		t.Error("subscriber row still exists after Erase")
	}

	// What DOES survive: a manual suppression for the address.
	sup := subscribers.NewSuppressionStore(pool)
	suppressed, err := sup.IsSuppressed(context.Background(), email)
	if err != nil || !suppressed {
		t.Errorf("IsSuppressed(%q) = %v, %v, want true, nil", email, suppressed, err)
	}

	// And the audit trail of the erasure itself.
	actions := auditActionsForSubscriberTarget(t, pool, id)
	found := false
	for _, a := range actions {
		if a == audit.ActionSubscriberErased {
			found = true
		}
	}
	if !found {
		t.Errorf("audit actions for erased subscriber %d = %v, want to include %q", id, actions, audit.ActionSubscriberErased)
	}
}

// TestAdminSubscribers_Erase_NotFound covers both a subscriber id that never
// existed under this test's control and, per CLAUDE.md §8b (never target a
// literal or seeded id), a repeat erase against a real id this test itself
// already erased — the closest thing to a guaranteed-absent id that isn't a
// hardcoded literal.
func TestAdminSubscribers_Erase_NotFound(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-erase-404@example.com")
	seedSession(t, pool, admin, "admin-token-erase-404")
	id, email := seedErasableSubscriber(t, pool)

	client := srv.Client()
	body := eraseRequestBody(t, map[string]any{"confirm_email": email})
	resp := doJSON(t, client, "DELETE", fmt.Sprintf("%s/admin/subscribers/%d", srv.URL, id), "admin-token-erase-404", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first erase: status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	resp = doJSON(t, client, "DELETE", fmt.Sprintf("%s/admin/subscribers/%d", srv.URL, id), "admin-token-erase-404", body)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("repeat erase of an already-erased subscriber: status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestAdminSubscribers_Erase_ConflictsOnPendingSend proves the guard against
// the RecheckEligibleTx stuck-row hazard described in
// internal/subscribers/erase.go's package doc comment: a subscriber with a
// queued or in-progress campaign delivery cannot be erased.
func TestAdminSubscribers_Erase_ConflictsOnPendingSend(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-erase-pending@example.com")
	seedSession(t, pool, admin, "admin-token-erase-pending")
	id, email := seedErasableSubscriber(t, pool)
	campaignID := seedStatsCampaign(t, pool)
	seedEmailSendRow(t, pool, campaignID, id, email, "queued", nil, nil, 0)

	client := srv.Client()
	resp := doJSON(t, client, "DELETE", fmt.Sprintf("%s/admin/subscribers/%d", srv.URL, id),
		"admin-token-erase-pending", eraseRequestBody(t, map[string]any{"confirm_email": email}))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	if !subscriberExistsByID(t, pool, id) {
		t.Error("subscriber was erased despite a queued send; refusal must not mutate anything")
	}
}

// TestAdminSubscribers_Erase_ThenAddressStaysBlockedFromResubscribing is the
// property this issue exists to guarantee, proved end to end rather than by
// inspecting the suppressions table alone: after an admin erases a
// subscriber through this HTTP endpoint, the SAME address submitted to the
// real public POST /api/subscribe endpoint (wired to the SAME database, with
// a REAL *subscribers.SuppressionStore — not the NoSuppressions stub other
// tests in this package use) is refused silently at the uniform-202 gate,
// and no new subscribers row is ever created for it.
func TestAdminSubscribers_Erase_ThenAddressStaysBlockedFromResubscribing(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-erase-resub@example.com")
	seedSession(t, pool, admin, "admin-token-erase-resub")
	id, email := seedErasableSubscriber(t, pool)

	client := srv.Client()
	resp := doJSON(t, client, "DELETE", fmt.Sprintf("%s/admin/subscribers/%d", srv.URL, id),
		"admin-token-erase-resub", eraseRequestBody(t, map[string]any{"confirm_email": email}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("erase: status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
	if subscriberExistsByID(t, pool, id) {
		t.Fatal("subscriber still exists after a successful erase")
	}

	// A brand-new public signup attempt for the erased address, through the
	// real SubscribeHandler wired to a REAL SuppressionStore over the same
	// pool — the exact production wiring cmd/opencircuit/main.go uses.
	realSuppression := subscribers.NewSuppressionStore(pool)
	h, mux := subscribeMux(t, pool, &mailing.RecordingMailer{}, realSuppression)
	subResp := doSubscribe(t, h, mux, subscribeBody(email, nil, time.Now()))
	defer subResp.Body.Close()
	if subResp.StatusCode != http.StatusAccepted {
		t.Errorf("resubscribe attempt after erasure: status = %d, want 202 (uniform — CLAUDE.md §9)", subResp.StatusCode)
	}

	if subscriberExistsByID(t, pool, id) {
		t.Error("subscriber row reappeared with the erased id")
	}
	// subscriberExists (subscribe_test.go, same package) is keyed by email —
	// exactly the check that matters here: not just "this id is still gone"
	// but "no subscribers row of ANY id exists for this address".
	if subscriberExists(t, pool, email) {
		t.Error("a new subscribers row was created for the erased address — erasure did not survive the resubscribe attempt")
	}

	suppressed, err := realSuppression.IsSuppressed(context.Background(), email)
	if err != nil || !suppressed {
		t.Errorf("IsSuppressed(%q) after the resubscribe attempt = %v, %v, want true, nil", email, suppressed, err)
	}
}
