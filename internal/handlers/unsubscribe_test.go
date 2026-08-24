package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// journeyUnsubscribeMux mirrors journeyPreferencesMux (journey_testutil_test.go)
// for #0034's handler: a real mux with both GET and POST wired, the same
// shape mountAndServe registers in production.
func journeyUnsubscribeMux(subs unsubscribeSubscriberStore, auditor *audit.Logger) (*UnsubscribeHandler, http.Handler) {
	h := NewUnsubscribeHandler(subs, auditor, nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/unsubscribe", h.Get)
	mux.HandleFunc("POST /api/unsubscribe", h.Post)
	return h, mux
}

func doGetUnsubscribe(t *testing.T, mux http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/unsubscribe?token="+token, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// doPostUnsubscribe issues the exact request shape RFC 8058 defines: token
// as a URL query parameter, body "List-Unsubscribe=One-Click" with the
// matching content type. This handler must never parse or validate that
// body — see unsubscribe.go's package doc comment — so every test below
// sends it, rather than an empty body, to prove that.
func doPostUnsubscribe(t *testing.T, mux http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/unsubscribe?token="+token, strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeUnsubscribeResponse(t *testing.T, rr *httptest.ResponseRecorder) unsubscribeResponse {
	t.Helper()
	var resp unsubscribeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode unsubscribe response: %v (body=%s)", err, rr.Body.String())
	}
	return resp
}

// TestUnsubscribeHandler_Post_ValidToken_UnsubscribesRotatesAndAudits is the
// core one-click POST path: PRD §6.5's "unsubscribes synchronously", #0034's
// "rotates manage_token", and the audit row carrying source=one_click.
func TestUnsubscribeHandler_Post_ValidToken_UnsubscribesRotatesAndAudits(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
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
	_, mux := journeyUnsubscribeMux(subs, auditor)

	rr := doPostUnsubscribe(t, mux, created.ManageToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	resp := decodeUnsubscribeResponse(t, rr)
	if resp.NoOp {
		t.Error("NoOp = true, want false — this is a real unsubscribe")
	}

	// #0221: unsubscribe.go's doc comment claims this response shares the
	// same no_op schema as preferences.go and admin_subscribers.go (#0093),
	// but only those two carried a raw-JSON presence assertion proving the
	// key survives on the wire rather than merely decoding to the zero
	// value. Struct-decoding can't tell an explicit `false` from an omitted
	// key, so this mirrors preferences_test.go's
	// TestPreferencesHandler_Patch_UnsubscribeEverything assertion: parse
	// into a map and confirm "no_op" is present and false. Reinstating
	// `,omitempty` on unsubscribeResponse.NoOp would drop the key here and
	// fail this check.
	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	if v, ok := raw["no_op"]; !ok {
		t.Error(`response body has no "no_op" key at all — want it present and false (omitempty would drop it here)`)
	} else if v != false {
		t.Errorf(`response body's "no_op" = %v, want false`, v)
	}

	after, err := subs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != subscribers.StatusUnsubscribed {
		t.Errorf("Status = %q, want %q", after.Status, subscribers.StatusUnsubscribed)
	}
	if after.UnsubscribedAt == nil {
		t.Error("UnsubscribedAt is nil, want set")
	}
	if after.UnsubscribeSource == nil || *after.UnsubscribeSource != subscribers.SourceOneClick {
		t.Errorf("UnsubscribeSource = %v, want %q", after.UnsubscribeSource, subscribers.SourceOneClick)
	}
	if after.ManageToken == created.ManageToken {
		t.Error("ManageToken unchanged, want rotated after a real (non-no-op) one-click unsubscribe")
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

	var source string
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'source' FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&source); err != nil {
		t.Fatalf("query audit_log metadata: %v", err)
	}
	if source != subscribers.SourceOneClick {
		t.Errorf("audit metadata source = %q, want %q", source, subscribers.SourceOneClick)
	}
}

// TestUnsubscribeHandler_Get_NeverMutates is the RFC 8058 prefetch-safety
// requirement: a mail client or security scanner prefetching the
// List-Unsubscribe href must not unsubscribe anyone. GET must 302 to
// /unsubscribe and leave the subscriber's status untouched.
func TestUnsubscribeHandler_Get_NeverMutates(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
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
	_, mux := journeyUnsubscribeMux(subs, auditor)

	rr := doGetUnsubscribe(t, mux, created.ManageToken)
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body=%s)", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if loc == "" || loc[:len("/unsubscribe")] != "/unsubscribe" {
		t.Errorf("Location = %q, want it to start with /unsubscribe", loc)
	}

	after, err := subs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != subscribers.StatusActive {
		t.Errorf("Status = %q, want %q — GET must never mutate", after.Status, subscribers.StatusActive)
	}
	if after.ManageToken != created.ManageToken {
		t.Error("ManageToken changed by a GET request, want unchanged — GET must never mutate")
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 0 {
		t.Errorf("audit_log rows for subscriber.unsubscribed after GET = %d, want 0", count)
	}
}

// TestUnsubscribeHandler_Post_UnknownToken_Returns200Neutral covers PRD
// §6.5's "unknown or already-used token → still return 200 with a neutral
// ... page. Never 404" — a provider seeing errors here downgrades sender
// reputation. Deliberately the opposite of PreferencesHandler, which 404s
// an unknown token (#0034's carried-in review findings: "do not reuse
// PreferencesHandler's error contract").
func TestUnsubscribeHandler_Post_UnknownToken_Returns200Neutral(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	auditor := audit.New(pool)
	_, mux := journeyUnsubscribeMux(subs, auditor)

	rr := doPostUnsubscribe(t, mux, "not-a-real-token-"+journeyUniqueEmail(t))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	resp := decodeUnsubscribeResponse(t, rr)
	if !resp.NoOp {
		t.Error("NoOp = false, want true — nothing existed to unsubscribe")
	}
	if resp.Message == "" {
		t.Error("Message is empty, want a neutral confirmation")
	}
}

// TestUnsubscribeHandler_Post_MissingToken_Returns200Neutral covers garbage
// input at the extreme: no token at all. Still 200, never 400/404.
func TestUnsubscribeHandler_Post_MissingToken_Returns200Neutral(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
	auditor := audit.New(pool)
	_, mux := journeyUnsubscribeMux(subs, auditor)

	req := httptest.NewRequest(http.MethodPost, "/api/unsubscribe", strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	resp := decodeUnsubscribeResponse(t, rr)
	if !resp.NoOp {
		t.Error("NoOp = false, want true")
	}
}

// TestUnsubscribeHandler_Post_Replay_SecondHitIsNeutralNoOp is the replay
// requirement: a real one-click POST rotates manage_token (so the link a
// provider's retry logic replays no longer resolves), and the replay itself
// must still answer 200 neutral rather than erroring — and must not touch
// the store or write a second audit row.
func TestUnsubscribeHandler_Post_Replay_SecondHitIsNeutralNoOp(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
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
	_, mux := journeyUnsubscribeMux(subs, auditor)

	first := doPostUnsubscribe(t, mux, created.ManageToken)
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200 (body=%s)", first.Code, first.Body.String())
	}
	firstResp := decodeUnsubscribeResponse(t, first)
	if firstResp.NoOp {
		t.Fatal("first request NoOp = true, want false (real unsubscribe)")
	}

	// Replay the SAME (now-stale, since rotated) token.
	second := doPostUnsubscribe(t, mux, created.ManageToken)
	if second.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (body=%s)", second.Code, second.Body.String())
	}
	secondResp := decodeUnsubscribeResponse(t, second)
	if !secondResp.NoOp {
		t.Error("replay NoOp = false, want true — the token no longer resolves after rotation")
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 1 {
		t.Errorf("audit_log rows for subscriber.unsubscribed after replay = %d, want 1 (replay must not write a second row)", count)
	}
}

// TestUnsubscribeHandler_Post_Complained_IsSilentNoOp is #0034's carried-in
// review finding from #0031: on an already-complained row, do nothing at
// all — no status change, no manage_token rotation, no audit row — and
// still answer a neutral 200 (the no-op must be silent to the provider).
func TestUnsubscribeHandler_Post_Complained_IsSilentNoOp(t *testing.T) {
	pool := journeyTestPool(t)
	subs := subscribers.NewStore(pool)
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
	_, mux := journeyUnsubscribeMux(subs, auditor)

	rr := doPostUnsubscribe(t, mux, created.ManageToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	resp := decodeUnsubscribeResponse(t, rr)
	if !resp.NoOp {
		t.Error("NoOp = false, want true")
	}

	after, err := subs.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != subscribers.StatusComplained {
		t.Errorf("Status = %q, want %q — one-click must not touch a complained row", after.Status, subscribers.StatusComplained)
	}
	if after.UnsubscribedAt != nil {
		t.Error("UnsubscribedAt is set, want nil — no status change at all")
	}
	if after.ManageToken != created.ManageToken {
		t.Error("ManageToken changed, want unchanged — no rotation on a complained no-op")
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		audit.ActionSubscriberUnsubscribed, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 0 {
		t.Errorf("audit_log rows for subscriber.unsubscribed = %d, want 0 (complained no-op must not write an audit row)", count)
	}
}

// raceComplainingUnsubscribeStore wraps a real *subscribers.Store and marks
// the target row complained the moment Unsubscribe is called — i.e. AFTER
// the handler's FindByManageToken lookup already returned an "active" row
// but BEFORE the guarded UPDATE runs. This reproduces the exact window
// #0104 is about: a status read before the mutating call is stale by the
// time the call lands, but the row Unsubscribe itself returns is not.
type raceComplainingUnsubscribeStore struct {
	subs         *subscribers.Store
	now          func() time.Time
	complainedID int64
	complained   bool
}

func (s *raceComplainingUnsubscribeStore) FindByManageToken(ctx context.Context, token string) (subscribers.Subscriber, error) {
	return s.subs.FindByManageToken(ctx, token)
}

func (s *raceComplainingUnsubscribeStore) Unsubscribe(ctx context.Context, id int64, source string, now time.Time) (subscribers.Subscriber, error) {
	if !s.complained && id == s.complainedID {
		s.complained = true
		if _, err := s.subs.MarkComplained(ctx, id, s.now()); err != nil {
			return subscribers.Subscriber{}, err
		}
	}
	return s.subs.Unsubscribe(ctx, id, source, now)
}

func (s *raceComplainingUnsubscribeStore) RotateManageToken(ctx context.Context, id int64, now time.Time) (subscribers.Subscriber, error) {
	return s.subs.RotateManageToken(ctx, id, now)
}

// TestUnsubscribeHandler_Post_ComplainedBetweenLookupAndUnsubscribe_IsNoOp
// (#0104) proves noOp is decided from the row Store.Unsubscribe returns,
// not from a status read before the call. Without that fix, the handler's
// stale pre-call read would see "active", take the real-unsubscribe branch,
// rotate manage_token, and write a subscriber.unsubscribed audit row for an
// unsubscribe that the guarded UPDATE actually refused — corrupting the
// consent-evidence record #0038/#0060 read as fact. Seeds a throwaway
// subscriber (CLAUDE.md §8b) rather than targeting any literal or seeded id.
func TestUnsubscribeHandler_Post_ComplainedBetweenLookupAndUnsubscribe_IsNoOp(t *testing.T) {
	pool := journeyTestPool(t)
	realStore := subscribers.NewStore(pool)
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
	subs := &raceComplainingUnsubscribeStore{
		subs:         realStore,
		now:          func() time.Time { return racingNow },
		complainedID: created.ID,
	}
	auditor := audit.New(pool)
	_, mux := journeyUnsubscribeMux(subs, auditor)

	rr := doPostUnsubscribe(t, mux, created.ManageToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	resp := decodeUnsubscribeResponse(t, rr)
	if !resp.NoOp {
		t.Error("NoOp = false, want true — the row was complained by the time Unsubscribe's UPDATE ran")
	}

	after, err := realStore.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != subscribers.StatusComplained {
		t.Errorf("Status = %q, want %q", after.Status, subscribers.StatusComplained)
	}
	if after.ManageToken != created.ManageToken {
		t.Error("ManageToken changed, want unchanged — a stale pre-call read would have rotated it")
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
