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
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/sesnotify"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// adminDeliverabilityMux wires the real admin deliverability routes guarded
// by RequireSession then RequireAdmin, backed by real stores — mirrors
// adminSubscribersMux/adminCampaignStatsMux.
func adminDeliverabilityMux(pool *pgxpool.Pool) http.Handler {
	authStore := auth.NewStore(pool)
	subStore := subscribers.NewStore(pool)
	eventsStore := sesnotify.NewStore(pool)
	statsStore := mailing.NewCampaignStatsStore(pool)
	h := NewAdminDeliverabilityHandler(subStore, subStore, subStore, eventsStore, statsStore, audit.New(pool))
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /admin/deliverability", requireAdmin(http.HandlerFunc(h.List)))
	mux.Handle("GET /admin/deliverability/{email}", requireAdmin(http.HandlerFunc(h.Detail)))
	mux.Handle("POST /admin/deliverability/{email}/reset-streak", requireAdmin(http.HandlerFunc(h.ResetStreak)))
	return mux
}

func setSubscriberStreak(t *testing.T, pool *pgxpool.Pool, id int64, streak int, lastBounceAt *time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE subscribers SET soft_bounce_streak = $2, last_bounce_at = $3 WHERE id = $1`,
		id, streak, lastBounceAt,
	); err != nil {
		t.Fatalf("set streak for subscriber %d: %v", id, err)
	}
}

// seedBounceEventWithDiagnostic inserts a real Bounce email_events row whose
// payload matches SES's actual shape (bounce.bouncedRecipients[].diagnosticCode),
// so the handler's sesnotify.ParseSESEvent + DiagnosticCodeFor path is
// exercised against real JSON, not a stub.
func seedBounceEventWithDiagnostic(t *testing.T, pool *pgxpool.Pool, snsMessageID, sesMessageID, recipient, diagnosticCode string) {
	t.Helper()
	payload := fmt.Sprintf(`{"eventType":"Bounce","mail":{"messageId":%q},"bounce":{"bounceType":"Permanent","bounceSubType":"General","bouncedRecipients":[{"emailAddress":%q,"diagnosticCode":%q}]}}`,
		sesMessageID, recipient, diagnosticCode)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO email_events (sns_message_id, ses_message_id, event_type, bounce_type, bounce_subtype, recipient, payload)
		 VALUES ($1, $2, 'Bounce', 'Permanent', 'General', lower(trim($3)), $4::jsonb)`,
		snsMessageID, sesMessageID, recipient, payload,
	); err != nil {
		t.Fatalf("seed bounce event: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM email_events WHERE sns_message_id = $1`, snsMessageID)
	})
}

type deliverabilityListItemForTest struct {
	SubscriberID       int64    `json:"subscriber_id"`
	Email              string   `json:"email"`
	SoftBounceStreak   int      `json:"soft_bounce_streak"`
	Suppressed         bool     `json:"suppressed"`
	SuppressionReasons []string `json:"suppression_reasons"`
}

func decodeDeliverabilityList(t *testing.T, body []byte) []deliverabilityListItemForTest {
	t.Helper()
	var v struct {
		Items []deliverabilityListItemForTest `json:"items"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode deliverability list: %v", err)
	}
	return v.Items
}

func TestAdminDeliverability_List_SortedByStreakThenRecency(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminDeliverabilityMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-deliv-list@example.com")
	seedSession(t, pool, admin, "admin-token-deliv-list")

	low := seedTestSubscriber(t, pool, subscribers.StatusActive)
	setSubscriberStreak(t, pool, low, 1, nil)
	high := seedTestSubscriber(t, pool, subscribers.StatusActive)
	setSubscriberStreak(t, pool, high, 4, nil)
	// Never bounced at all: must NOT appear in the list.
	clean := seedTestSubscriber(t, pool, subscribers.StatusActive)
	_ = clean

	resp := doJSON(t, srv.Client(), "GET", srv.URL+"/admin/deliverability", "admin-token-deliv-list", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	items := decodeDeliverabilityList(t, readBody(t, resp))

	indexOf := func(id int64) int {
		for i, it := range items {
			if it.SubscriberID == id {
				return i
			}
		}
		return -1
	}
	hi, lo := indexOf(high), indexOf(low)
	if hi == -1 || lo == -1 {
		t.Fatalf("expected both seeded subscribers in the list; got %+v", items)
	}
	if hi >= lo {
		t.Errorf("high-streak subscriber (index %d) must sort before low-streak (index %d) — streak descending", hi, lo)
	}
	if indexOf(clean) != -1 {
		t.Error("a subscriber with no bounce activity must not appear in the deliverability list")
	}
}

func TestAdminDeliverability_Detail_ResolvesDiagnosticCodeAndCampaign(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminDeliverabilityMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-deliv-detail@example.com")
	seedSession(t, pool, admin, "admin-token-deliv-detail")

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	setSubscriberStreak(t, pool, subID, 2, nil)

	campaignStore := mailing.NewCampaignStore(pool)
	c, err := campaignStore.Create(context.Background(), mailing.CampaignInput{
		Name: fmt.Sprintf("zz-deliv-campaign-%d", testdb.Unique()), Subject: "s", BodyMD: "b", AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)

	sesMessageID := fmt.Sprintf("zz-deliv-ses-%d", testdb.Unique())
	seedEmailSendRow(t, pool, c.ID, subID, email, "sent", &sesMessageID, nil, 1)
	snsID := fmt.Sprintf("zz-deliv-sns-%d", testdb.Unique())
	seedBounceEventWithDiagnostic(t, pool, snsID, sesMessageID, email, "smtp; 550 5.1.1 user unknown")

	resp := doJSON(t, srv.Client(), "GET", srv.URL+"/admin/deliverability/"+email, "admin-token-deliv-detail", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var view struct {
		Email            string `json:"email"`
		SoftBounceStreak *int   `json:"soft_bounce_streak"`
		Events           []struct {
			EventType      string `json:"event_type"`
			DiagnosticCode string `json:"diagnostic_code"`
			CampaignID     *int64 `json:"campaign_id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(readBody(t, resp), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.SoftBounceStreak == nil || *view.SoftBounceStreak != 2 {
		t.Errorf("SoftBounceStreak = %v, want 2", view.SoftBounceStreak)
	}
	if len(view.Events) != 1 {
		t.Fatalf("events = %+v, want exactly 1", view.Events)
	}
	ev := view.Events[0]
	if ev.EventType != "Bounce" {
		t.Errorf("EventType = %q, want Bounce", ev.EventType)
	}
	if ev.DiagnosticCode != "smtp; 550 5.1.1 user unknown" {
		t.Errorf("DiagnosticCode = %q, want the seeded value", ev.DiagnosticCode)
	}
	if ev.CampaignID == nil || *ev.CampaignID != c.ID {
		t.Errorf("CampaignID = %v, want %d", ev.CampaignID, c.ID)
	}
}

func TestAdminDeliverability_Detail_UnknownAddress_ReturnsEmptyNot404(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminDeliverabilityMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-deliv-unknown@example.com")
	seedSession(t, pool, admin, "admin-token-deliv-unknown")

	resp := doJSON(t, srv.Client(), "GET", srv.URL+fmt.Sprintf("/admin/deliverability/zz-deliv-nobody-%d@example.com", testdb.Unique()),
		"admin-token-deliv-unknown", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (an arbitrary address is not an error)", resp.StatusCode)
	}
	var view struct {
		SubscriberID *int64 `json:"subscriber_id"`
		Events       []any  `json:"events"`
	}
	if err := json.Unmarshal(readBody(t, resp), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.SubscriberID != nil {
		t.Errorf("SubscriberID = %v, want nil for an address with no subscribers row", view.SubscriberID)
	}
	if len(view.Events) != 0 {
		t.Errorf("Events = %v, want empty", view.Events)
	}
}

func TestAdminDeliverability_ResetStreak_ClearsStreakAndAudits(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminDeliverabilityMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-deliv-reset@example.com")
	seedSession(t, pool, admin, "admin-token-deliv-reset")

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)
	setSubscriberStreak(t, pool, subID, 5, nil)

	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/deliverability/"+email+"/reset-streak", "admin-token-deliv-reset", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.SoftBounceStreak != 0 {
		t.Errorf("soft_bounce_streak = %d, want 0 after reset", sub.SoftBounceStreak)
	}

	actions := auditActionsForSubscriberTarget(t, pool, subID)
	var sawReset bool
	for _, a := range actions {
		if a == audit.ActionSubscriberSoftBounceStreakReset {
			sawReset = true
		}
	}
	if !sawReset {
		t.Errorf("audit actions = %v, want %s present", actions, audit.ActionSubscriberSoftBounceStreakReset)
	}
}

// TestAdminDeliverability_ResetStreak_NeverChangesComplainedStatus is
// CLAUDE.md §9's guarantee, applied to this new write path: resetting the
// streak must never move a subscriber out of `complained` — the streak and
// the status are orthogonal columns, and this proves the reset path
// (unlike AdminClearComplaint, the sole sanctioned exit from `complained`)
// touches only the former.
func TestAdminDeliverability_ResetStreak_NeverChangesComplainedStatus(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminDeliverabilityMux(pool))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-deliv-resetcomplained@example.com")
	seedSession(t, pool, admin, "admin-token-deliv-resetcomplained")

	subID := seedTestSubscriber(t, pool, subscribers.StatusComplained)
	email := subscriberEmailByID(t, pool, subID)
	setSubscriberStreak(t, pool, subID, 3, nil)

	resp := doJSON(t, srv.Client(), "POST", srv.URL+"/admin/deliverability/"+email+"/reset-streak", "admin-token-deliv-resetcomplained", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	sub, err := subscribers.NewStore(pool).GetByID(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sub.Status != subscribers.StatusComplained {
		t.Errorf("status = %q, want unchanged %q (CLAUDE.md §9)", sub.Status, subscribers.StatusComplained)
	}
	if sub.SoftBounceStreak != 0 {
		t.Errorf("soft_bounce_streak = %d, want 0 — the reset itself must still work for a complained address", sub.SoftBounceStreak)
	}
}

func TestAdminDeliverability_NonAdminForbidden(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminDeliverabilityMux(pool))
	defer srv.Close()

	user := seedUser(t, pool, "regular-deliv@example.com")
	seedSession(t, pool, user, "user-token-deliv")

	subID := seedTestSubscriber(t, pool, subscribers.StatusActive)
	email := subscriberEmailByID(t, pool, subID)

	cases := []struct{ method, path string }{
		{"GET", "/admin/deliverability"},
		{"GET", "/admin/deliverability/" + email},
		{"POST", "/admin/deliverability/" + email + "/reset-streak"},
	}
	client := srv.Client()
	for _, c := range cases {
		resp := doJSON(t, client, c.method, srv.URL+c.path, "user-token-deliv", "")
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s with non-admin session: status = %d, want 403", c.method, c.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
