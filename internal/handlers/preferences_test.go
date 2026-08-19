package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

func journeyPreferencesMux(subs preferencesSubscriberStore, ints preferencesInterestStore, auditor *audit.Logger) (*PreferencesHandler, http.Handler) {
	h := NewPreferencesHandler(subs, ints, auditor, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/preferences", h.Get)
	mux.HandleFunc("PATCH /api/preferences", h.Patch)
	return h, mux
}

func doGetPreferences(t *testing.T, mux http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/preferences?token="+token, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func doPatchPreferences(t *testing.T, mux http.Handler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal patch body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/preferences", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodePreferencesResponse(t *testing.T, rr *httptest.ResponseRecorder) preferencesResponse {
	t.Helper()
	var resp preferencesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode preferences response: %v (body=%s)", err, rr.Body.String())
	}
	return resp
}

func TestPreferencesHandler_Get_ValidToken_ReturnsMaskedEmailAndInterests(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	pcbID := journeySeededInterestID(t, pool, "pcb-design")
	created, err := subs.Create(ctx, subscribers.NewSignup{Email: "zz-journeytest-pref@example.com", ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := subs.SetInterests(ctx, created.ID, []int64{pcbID}); err != nil {
		t.Fatalf("SetInterests: %v", err)
	}

	_, mux := journeyPreferencesMux(subs, ints, audit.New(pool))

	rr := doGetPreferences(t, mux, created.ManageToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	resp := decodePreferencesResponse(t, rr)
	if resp.Email != "z•••••f@example.com" {
		t.Errorf("Email = %q, want masked form", resp.Email)
	}
	if len(resp.Interests) != 1 || resp.Interests[0] != "pcb-design" {
		t.Errorf("Interests = %v, want [\"pcb-design\"]", resp.Interests)
	}
	if resp.Status != subscribers.StatusPending {
		t.Errorf("Status = %q, want %q", resp.Status, subscribers.StatusPending)
	}
}

func TestPreferencesHandler_Get_UnknownToken_Returns404(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	_, mux := journeyPreferencesMux(subs, ints, audit.New(pool))

	rr := doGetPreferences(t, mux, "not-a-real-token")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestPreferencesHandler_Get_MissingToken_Returns404(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	_, mux := journeyPreferencesMux(subs, ints, audit.New(pool))

	req := httptest.NewRequest(http.MethodGet, "/api/preferences", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestPreferencesHandler_Patch_ReplacesInterestsAndWritesAudit(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	homelabID := journeySeededInterestID(t, pool, "homelab")
	roboticsID := journeySeededInterestID(t, pool, "robotics")
	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := subs.SetInterests(ctx, created.ID, []int64{homelabID}); err != nil {
		t.Fatalf("SetInterests: %v", err)
	}

	auditor := audit.New(pool)
	_, mux := journeyPreferencesMux(subs, ints, auditor)

	rr := doPatchPreferences(t, mux, map[string]any{
		"token":     created.ManageToken,
		"interests": []string{"robotics"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	resp := decodePreferencesResponse(t, rr)
	if len(resp.Interests) != 1 || resp.Interests[0] != "robotics" {
		t.Errorf("Interests = %v, want [\"robotics\"]", resp.Interests)
	}

	ids, err := subs.InterestIDs(ctx, created.ID)
	if err != nil {
		t.Fatalf("InterestIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != roboticsID {
		t.Errorf("stored interest ids = %v, want [%d]", ids, roboticsID)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberPreferencesUpdated, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 1 {
		t.Errorf("audit_log rows for subscriber.preferences_updated = %d, want 1", count)
	}
}

func TestPreferencesHandler_Patch_EmptyInterests_StaysActiveGeneralAnnouncementsOnly(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	homelabID := journeySeededInterestID(t, pool, "homelab")
	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := subs.SetInterests(ctx, created.ID, []int64{homelabID}); err != nil {
		t.Fatalf("SetInterests: %v", err)
	}

	_, mux := journeyPreferencesMux(subs, ints, audit.New(pool))

	rr := doPatchPreferences(t, mux, map[string]any{
		"token":     created.ManageToken,
		"interests": []string{},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	resp := decodePreferencesResponse(t, rr)
	if len(resp.Interests) != 0 {
		t.Errorf("Interests = %v, want empty", resp.Interests)
	}
	if resp.Message == "" {
		t.Error("Message is empty, want an explicit 'general announcements only' explanation")
	}

	after, err := subs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != subscribers.StatusActive {
		t.Errorf("Status = %q, want %q — an empty interest selection must not itself unsubscribe the caller", after.Status, subscribers.StatusActive)
	}
}

func TestPreferencesHandler_Patch_UnsubscribeEverything(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	auditor := audit.New(pool)
	_, mux := journeyPreferencesMux(subs, ints, auditor)

	rr := doPatchPreferences(t, mux, map[string]any{
		"token":       created.ManageToken,
		"unsubscribe": true,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	resp := decodePreferencesResponse(t, rr)
	if !resp.Unsubscribed {
		t.Error("Unsubscribed = false, want true")
	}

	after, err := subs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != subscribers.StatusUnsubscribed {
		t.Errorf("Status = %q, want %q", after.Status, subscribers.StatusUnsubscribed)
	}
	if after.UnsubscribeSource == nil || *after.UnsubscribeSource != subscribers.SourcePreferences {
		t.Errorf("UnsubscribeSource = %v, want %q", after.UnsubscribeSource, subscribers.SourcePreferences)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 1 {
		t.Errorf("audit_log rows for subscriber.unsubscribed = %d, want 1", count)
	}
}

func TestPreferencesHandler_Patch_UnknownInterestSlug_Returns400(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, mux := journeyPreferencesMux(subs, ints, audit.New(pool))

	rr := doPatchPreferences(t, mux, map[string]any{
		"token":     created.ManageToken,
		"interests": []string{"not-a-real-slug"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestPreferencesHandler_Patch_UnknownToken_Returns404(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	_, mux := journeyPreferencesMux(subs, ints, audit.New(pool))

	rr := doPatchPreferences(t, mux, map[string]any{
		"token":     "not-a-real-token",
		"interests": []string{},
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestPreferencesHandler_Patch_PreservesInactiveInterestOnUnrelatedSave(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	slug := testInterestSlug(t, pool)
	legacy, err := ints.Create(ctx, slug, "Legacy topic", nil, 999)
	if err != nil {
		t.Fatalf("Create legacy interest: %v", err)
	}
	if err := ints.Deactivate(ctx, legacy.ID); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create subscriber: %v", err)
	}
	if err := subs.SetInterests(ctx, created.ID, []int64{legacy.ID}); err != nil {
		t.Fatalf("SetInterests: %v", err)
	}

	_, mux := journeyPreferencesMux(subs, ints, audit.New(pool))

	// GET first, mirroring the SPA: it would show this slug as still
	// selected even though it is no longer offered as a checkbox, and
	// submitting the SAME set back must not drop it.
	getRR := doGetPreferences(t, mux, created.ManageToken)
	getResp := decodePreferencesResponse(t, getRR)
	if len(getResp.Interests) != 1 || getResp.Interests[0] != slug {
		t.Fatalf("GET Interests = %v, want [%q] (deactivated interest should still resolve)", getResp.Interests, slug)
	}

	patchRR := doPatchPreferences(t, mux, map[string]any{
		"token":     created.ManageToken,
		"interests": getResp.Interests,
	})
	if patchRR.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200 (body=%s)", patchRR.Code, patchRR.Body.String())
	}
	patchResp := decodePreferencesResponse(t, patchRR)
	if len(patchResp.Interests) != 1 || patchResp.Interests[0] != slug {
		t.Errorf("PATCH Interests = %v, want [%q] preserved", patchResp.Interests, slug)
	}
}

func TestMaskEmail(t *testing.T) {
	cases := []struct {
		email string
		want  string
	}{
		{"brennan@gmail.com", "b•••••n@gmail.com"},
		{"bob@gmail.com", "b•••••b@gmail.com"},
		{"a@example.com", "•@example.com"},
		{"ab@example.com", "a•••••b@example.com"},
	}
	for _, tc := range cases {
		if got := maskEmail(tc.email); got != tc.want {
			t.Errorf("maskEmail(%q) = %q, want %q", tc.email, got, tc.want)
		}
	}
}
