package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	// #0091 round two: journeyUniqueEmail(t), not a fixed literal — this
	// package no longer truncates between tests (see journeyTestPool), so a
	// literal email would collide with this same test's own row on a second
	// `-count=2` iteration. The masked form is computed from the generated
	// email (first + last char of the local part, "email" package's masking
	// rule — see the table-driven mask tests below) instead of being a fixed
	// string, since the local part is no longer fixed either.
	email := journeyUniqueEmail(t)
	created, err := subs.Create(ctx, subscribers.NewSignup{Email: email, ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if err := subs.SetInterests(ctx, created.ID, []int64{pcbID}); err != nil {
		t.Fatalf("SetInterests: %v", err)
	}

	_, mux := journeyPreferencesMux(subs, ints, audit.New(pool))

	rr := doGetPreferences(t, mux, created.ManageToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	resp := decodePreferencesResponse(t, rr)
	local, domain, _ := strings.Cut(email, "@")
	wantMasked := string(local[0]) + "•••••" + string(local[len(local)-1]) + "@" + domain
	if resp.Email != wantMasked {
		t.Errorf("Email = %q, want masked form %q", resp.Email, wantMasked)
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
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
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
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
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

// TestPreferencesHandler_Patch_EmptyInterests_UnsubscribedStaysUnsubscribedAndSaysSo
// guards #0031's headline security claim on the concrete path the review
// reproduced: TestPreferencesHandler_Patch_EmptyInterests_StaysActiveGeneralAnnouncementsOnly
// above only ever exercises an active subscriber, where after.Status ==
// StatusActive passes whether or not the handler forces the status — it
// would not fail if a future change auto-reactivated on save. This one
// starts from an unsubscribed row and fails if status changes OR if the
// response falsely claims an active subscription.
func TestPreferencesHandler_Patch_EmptyInterests_UnsubscribedStaysUnsubscribedAndSaysSo(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := subs.Unsubscribe(ctx, created.ID, subscribers.SourcePreferences, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("Unsubscribe (seeding unsubscribed row): %v", err)
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
	if resp.Status != subscribers.StatusUnsubscribed {
		t.Errorf("response Status = %q, want %q", resp.Status, subscribers.StatusUnsubscribed)
	}
	if strings.Contains(resp.Message, "You're subscribed") {
		t.Errorf("Message = %q, falsely claims an active subscription for an unsubscribed row", resp.Message)
	}

	after, err := subs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != subscribers.StatusUnsubscribed {
		t.Errorf("Status = %q, want %q — an empty interest PATCH must not resurrect an unsubscribed row", after.Status, subscribers.StatusUnsubscribed)
	}
}

// TestPreferencesHandler_Patch_EmptyInterests_ComplainedStaysComplainedAndSaysSo
// is the same guard for the CLAUDE.md §9 case specifically: "complained
// never auto-resubscribes" is the loophole this whole issue exists to keep
// closed, and it had no non-active-subscriber test at all before this pass.
func TestPreferencesHandler_Patch_EmptyInterests_ComplainedStaysComplainedAndSaysSo(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := subs.MarkComplained(ctx, created.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkComplained (seeding complained row): %v", err)
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
	if resp.Status != subscribers.StatusComplained {
		t.Errorf("response Status = %q, want %q", resp.Status, subscribers.StatusComplained)
	}
	if strings.Contains(resp.Message, "You're subscribed") {
		t.Errorf("Message = %q, falsely claims an active subscription for a complained row", resp.Message)
	}

	after, err := subs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != subscribers.StatusComplained {
		t.Errorf("Status = %q, want %q — an empty interest PATCH must not clear a complaint (CLAUDE.md §9)", after.Status, subscribers.StatusComplained)
	}
}

// TestPreferencesHandler_Patch_UnsubscribeEverything_ComplainedIsNoOpNoAudit
// is #0031 review finding 2's own regression test: {unsubscribe: true}
// against a complained row must leave status unchanged AND must not write
// audit.ActionSubscriberUnsubscribed. Before the fix this test fails on the
// audit assertion — a spurious "subscriber.unsubscribed" row for someone who
// in fact complained and was never unsubscribed, which #0038/#0060 would
// have read as real.
func TestPreferencesHandler_Patch_UnsubscribeEverything_ComplainedIsNoOpNoAudit(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := subs.MarkComplained(ctx, created.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkComplained (seeding complained row): %v", err)
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
	if resp.Unsubscribed {
		t.Error("Unsubscribed = true, want false — the store no-op'd, nothing happened")
	}
	if !resp.NoOp {
		t.Error("NoOp = false, want true")
	}

	after, err := subs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != subscribers.StatusComplained {
		t.Errorf("Status = %q, want %q — unsubscribe must not touch a complained row", after.Status, subscribers.StatusComplained)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 0 {
		t.Errorf("audit_log rows for subscriber.unsubscribed = %d, want 0 (no-op must not write a spurious audit row)", count)
	}
}

// TestPreferencesHandler_Patch_UnsubscribeEverything_ComplainedNoOpMessageNamesContactAddress
// is #0090's bounce fix regression test: review found that the no-op
// message told a complained visitor "Contact us" without saying how, making
// it the sole (and inert) path offered once the "Subscribe again" affordance
// is correctly suppressed for this status. The fix inlines
// complainedContactEmail into the message (preferences.go can't render a
// mailto: link — this is plain-text JSON). Assert the address is actually
// present in the message a real request receives, not just that the
// constant exists.
func TestPreferencesHandler_Patch_UnsubscribeEverything_ComplainedNoOpMessageNamesContactAddress(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := subs.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := subs.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := subs.MarkComplained(ctx, created.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkComplained (seeding complained row): %v", err)
	}

	_, mux := journeyPreferencesMux(subs, ints, audit.New(pool))

	rr := doPatchPreferences(t, mux, map[string]any{
		"token":       created.ManageToken,
		"unsubscribe": true,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	resp := decodePreferencesResponse(t, rr)
	if !resp.NoOp {
		t.Fatalf("NoOp = false, want true (complained row, this test only makes sense for the no-op branch)")
	}
	if !strings.Contains(resp.Message, "hello@opencircuitsf.com") {
		t.Errorf("Message = %q, want it to contain the contact address (#0090 bounce fix: naming that a human exists isn't enough, the message must say how to reach one)", resp.Message)
	}
	if !strings.Contains(resp.Message, "Contact us at hello@opencircuitsf.com") {
		t.Errorf("Message = %q, want the address inline in the \"Contact us at …\" clause, not merely present somewhere in the string", resp.Message)
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
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
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
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })

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
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
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

// raceComplainingPreferencesStore wraps a real *subscribers.Store and marks
// the target row complained the moment Unsubscribe is called — i.e. AFTER
// patchUnsubscribe's earlier FindByManageToken lookup already returned an
// "active" row but BEFORE the guarded UPDATE runs. Mirrors
// raceComplainingUnsubscribeStore in unsubscribe_test.go for the
// preference-center path #0104 also fixed.
type raceComplainingPreferencesStore struct {
	subs         *subscribers.Store
	now          func() time.Time
	complainedID int64
	complained   bool
}

func (s *raceComplainingPreferencesStore) FindByManageToken(ctx context.Context, token string) (subscribers.Subscriber, error) {
	return s.subs.FindByManageToken(ctx, token)
}

func (s *raceComplainingPreferencesStore) InterestIDs(ctx context.Context, subscriberID int64) ([]int64, error) {
	return s.subs.InterestIDs(ctx, subscriberID)
}

func (s *raceComplainingPreferencesStore) SetInterests(ctx context.Context, subscriberID int64, interestIDs []int64) error {
	return s.subs.SetInterests(ctx, subscriberID, interestIDs)
}

func (s *raceComplainingPreferencesStore) Unsubscribe(ctx context.Context, id int64, source string, now time.Time) (subscribers.Subscriber, error) {
	if !s.complained && id == s.complainedID {
		s.complained = true
		if _, err := s.subs.MarkComplained(ctx, id, s.now()); err != nil {
			return subscribers.Subscriber{}, err
		}
	}
	return s.subs.Unsubscribe(ctx, id, source, now)
}

// TestPreferencesHandler_Patch_ComplainedBetweenLookupAndUnsubscribe_IsNoOp
// (#0104) proves patchUnsubscribe decides noOp from the row
// Store.Unsubscribe returns, not from a status read before the call.
// Without that fix, the stale pre-call read would see "active", take the
// real-unsubscribe branch, and write a subscriber.unsubscribed audit row
// for an unsubscribe the guarded UPDATE actually refused — corrupting the
// consent-evidence record #0038/#0060 read as fact. Seeds a throwaway
// subscriber (CLAUDE.md §8b) rather than targeting any literal or seeded id.
func TestPreferencesHandler_Patch_ComplainedBetweenLookupAndUnsubscribe_IsNoOp(t *testing.T) {
	pool := journeyTestPool(t)
	realStore := subscribers.NewStore(pool)
	ints := interests.NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := realStore.Create(ctx, subscribers.NewSignup{Email: journeyUniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, created.ID) })
	if _, err := realStore.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	racingNow := now.Add(2 * time.Minute)
	subs := &raceComplainingPreferencesStore{
		subs:         realStore,
		now:          func() time.Time { return racingNow },
		complainedID: created.ID,
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
	if resp.Unsubscribed {
		t.Error("Unsubscribed = true, want false — the row was complained by the time Unsubscribe's UPDATE ran")
	}
	if !resp.NoOp {
		t.Error("NoOp = false, want true")
	}

	after, err := realStore.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != subscribers.StatusComplained {
		t.Errorf("Status = %q, want %q", after.Status, subscribers.StatusComplained)
	}
	if after.ManageToken != created.ManageToken {
		t.Error("ManageToken changed, want unchanged")
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 0 {
		t.Errorf("audit_log rows for subscriber.unsubscribed = %d, want 0 — a stale pre-call read would have written one for an unsubscribe that never happened", count)
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
		// RFC 6531 non-ASCII local part (#0026 accepts these, SubscribeForm.svelte
		// doesn't filter them) — #0031 review's minor finding: byte-slicing the
		// local part here used to cut mid-codepoint and mask to invalid UTF-8.
		{"björn@example.com", "b•••••n@example.com"},
		{"日本語@example.com", "日•••••語@example.com"},
	}
	for _, tc := range cases {
		got := maskEmail(tc.email)
		if got != tc.want {
			t.Errorf("maskEmail(%q) = %q, want %q", tc.email, got, tc.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("maskEmail(%q) = %q, not valid UTF-8", tc.email, got)
		}
	}
}
