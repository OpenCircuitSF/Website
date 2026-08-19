package handlers

// Shared journey test helpers (journeyTestPool, truncateJourneyTables,
// journeySeededInterestID, journeyUniqueEmail) live in
// journey_testutil_test.go (#0029) -- this file just uses them.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// journeyConfirmMux wires POST /api/subscribe/confirm over real stores, no
// auth middleware (the route is public).
func journeyConfirmMux(pool *pgxpool.Pool, auditor *audit.Logger) (*ConfirmHandler, http.Handler) {
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	h := NewConfirmHandler(subs, ints, auditor, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/subscribe/confirm", h.Confirm)
	return h, mux
}

func doConfirm(t *testing.T, mux http.Handler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal confirm body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe/confirm", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeConfirmResponse(t *testing.T, rr *httptest.ResponseRecorder) confirmResponse {
	t.Helper()
	var resp confirmResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode confirm response: %v (body=%s)", err, rr.Body.String())
	}
	return resp
}

func TestConfirmHandler_ValidToken_ActivatesAndReturnsManageToken(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	homelabID := journeySeededInterestID(t, pool, "homelab")
	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := subs.SetInterests(ctx, created.ID, []int64{homelabID}); err != nil {
		t.Fatalf("SetInterests: %v", err)
	}

	auditor := audit.New(pool)
	_, mux := journeyConfirmMux(pool, auditor)

	rr := doConfirm(t, mux, map[string]any{"token": *created.ConfirmToken})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	resp := decodeConfirmResponse(t, rr)
	if resp.ManageToken != created.ManageToken {
		t.Errorf("ManageToken = %q, want %q", resp.ManageToken, created.ManageToken)
	}
	if resp.Email != created.Email {
		t.Errorf("Email = %q, want %q", resp.Email, created.Email)
	}
	if len(resp.Interests) != 1 || resp.Interests[0] != "homelab" {
		t.Errorf("Interests = %v, want [\"homelab\"]", resp.Interests)
	}
	if len(resp.ActiveInterests) < 12 {
		t.Errorf("ActiveInterests has %d entries, want at least the 12 seeded", len(resp.ActiveInterests))
	}

	updated, err := subs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.Status != subscribers.StatusActive {
		t.Errorf("Status = %q, want %q", updated.Status, subscribers.StatusActive)
	}
	if updated.ConfirmedAt == nil {
		t.Error("ConfirmedAt is nil, want set")
	}

	// audit.confirmed row written.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberConfirmed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 1 {
		t.Errorf("audit_log rows for subscriber.confirmed = %d, want 1", count)
	}
}

func TestConfirmHandler_UnknownToken_ReturnsFriendly400(t *testing.T) {
	pool := journeyTestPool(t)
	auditor := audit.New(pool)
	_, mux := journeyConfirmMux(pool, auditor)

	rr := doConfirm(t, mux, map[string]any{"token": "totally-unknown-token"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"] == "" {
		t.Error("error body is empty, want a message")
	}
}

func TestConfirmHandler_ExpiredToken_Returns400(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	// TTL of 1 nanosecond: expired by the time Confirm runs.
	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Nanosecond}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(2 * time.Millisecond)

	auditor := audit.New(pool)
	_, mux := journeyConfirmMux(pool, auditor)

	rr := doConfirm(t, mux, map[string]any{"token": *created.ConfirmToken})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestConfirmHandler_ReplayedToken_ReturnsSameFriendly400AsUnknown(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	auditor := audit.New(pool)
	_, mux := journeyConfirmMux(pool, auditor)

	first := doConfirm(t, mux, map[string]any{"token": *created.ConfirmToken})
	if first.Code != http.StatusOK {
		t.Fatalf("first confirm status = %d, want 200 (body=%s)", first.Code, first.Body.String())
	}

	// Replay: subscribers.Store.Confirm clears confirm_token on success
	// (single-use by design -- see confirm.go's package doc comment), so
	// the store cannot distinguish "already used" from "never existed".
	// The handler's contract is the SAME friendly 400 either way.
	second := doConfirm(t, mux, map[string]any{"token": *created.ConfirmToken})
	if second.Code != http.StatusBadRequest {
		t.Fatalf("replayed confirm status = %d, want 400 (body=%s)", second.Code, second.Body.String())
	}

	// Only ONE subscriber.confirmed audit row -- the replay must not write
	// a second one.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberConfirmed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 1 {
		t.Errorf("audit_log rows for subscriber.confirmed after replay = %d, want 1", count)
	}
}

func TestConfirmHandler_ComplainedSubscriber_NeverReactivatedByConfirm(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := subs.MarkComplained(ctx, created.ID, now); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}

	auditor := audit.New(pool)
	_, mux := journeyConfirmMux(pool, auditor)

	rr := doConfirm(t, mux, map[string]any{"token": *created.ConfirmToken})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}

	after, err := subs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != subscribers.StatusComplained {
		t.Errorf("Status = %q, want %q (CLAUDE.md §9: complained never auto-resubscribes)", after.Status, subscribers.StatusComplained)
	}
}

func TestConfirmHandler_EmptyToken_Returns400(t *testing.T) {
	pool := journeyTestPool(t)
	auditor := audit.New(pool)
	_, mux := journeyConfirmMux(pool, auditor)

	rr := doConfirm(t, mux, map[string]any{"token": ""})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestConfirmHandler_MalformedJSON_Returns400(t *testing.T) {
	pool := journeyTestPool(t)
	auditor := audit.New(pool)
	_, mux := journeyConfirmMux(pool, auditor)

	req := httptest.NewRequest(http.MethodPost, "/api/subscribe/confirm", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}
