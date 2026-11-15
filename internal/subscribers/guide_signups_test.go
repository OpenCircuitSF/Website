package subscribers

import (
	"context"
	"strings"
	"testing"
	"time"
)

// guideSignupsFor runs SignupsByGuide over [from, now+1h) and keeps only rows
// for guide, so tests sharing the database cannot see each other's rows.
func guideSignupsFor(t *testing.T, store *Store, guide string, from time.Time) []GuideSignups {
	t.Helper()
	rows, err := store.SignupsByGuide(context.Background(), from, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("SignupsByGuide: %v", err)
	}
	var out []GuideSignups
	for _, r := range rows {
		if r.Guide == guide {
			out = append(out, r)
		}
	}
	return out
}

// testGuide is a per-test guide slug. The store does not validate slugs (the
// handler does), so a unique one isolates each test's rows.
func testGuide(t *testing.T, suffix string) string {
	return strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")) + "-" + suffix
}

// longAgo is the application-side clock these tests pass to Create, Confirm
// and Unsubscribe. Event rows take the database clock regardless, so the
// SignupsByGuide windows below are unaffected, while the subscribers row's
// confirmed_at/unsubscribed_at stay far outside the windows
// TestGrowth30Days_BoundaryAndSyntheticExclusion measures.
func longAgo() time.Time { return time.Now().Add(-72 * time.Hour) }

func guideSignup(t *testing.T, store *Store, guide, content string) Subscriber {
	t.Helper()
	sub, err := store.Create(context.Background(), NewSignup{
		Email: uniqueEmail(t), ConfirmTTL: time.Hour,
		SignupGuide: guide, UTMCampaign: "guides", UTMContent: content,
	}, longAgo())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return sub
}

// latestEventAt is the created_at of subscriberID's latest event.
func latestEventAt(t *testing.T, store *Store, subscriberID int64) time.Time {
	t.Helper()
	var at time.Time
	if err := store.pool.QueryRow(context.Background(),
		`SELECT max(created_at) FROM subscriber_events WHERE subscriber_id = $1`, subscriberID).Scan(&at); err != nil {
		t.Fatalf("reading event time: %v", err)
	}
	return at
}

func TestSignupsByGuide_CountsRequestedAndConfirmedInWindow(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	a, b := testGuide(t, "a"), testGuide(t, "b")

	// Outside the window: the window starts just after this signup's event.
	old := guideSignup(t, store, b, "")
	from := latestEventAt(t, store, old.ID).Add(time.Microsecond)

	confirmed := guideSignup(t, store, a, "")
	if _, err := store.Confirm(ctx, *confirmed.ConfirmToken, longAgo().Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	guideSignup(t, store, a, "")
	guideSignup(t, store, b, "")
	if _, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, longAgo()); err != nil {
		t.Fatalf("Create (no guide): %v", err)
	}

	if got := guideSignupsFor(t, store, a, from); len(got) != 1 || got[0].Requested != 2 || got[0].Confirmed != 1 || got[0].UTMCampaign != "guides" {
		t.Errorf("guide a = %+v, want one row Requested 2 Confirmed 1", got)
	}
	if got := guideSignupsFor(t, store, b, from); len(got) != 1 || got[0].Requested != 1 || got[0].Confirmed != 0 {
		t.Errorf("guide b = %+v, want one row Requested 1 Confirmed 0 (old signup outside the window)", got)
	}
}

func TestSignupsByGuide_GroupsByCampaignPost(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	g := testGuide(t, "g")
	guideSignup(t, store, g, "gp1")
	guideSignup(t, store, g, "gp2")
	got := guideSignupsFor(t, store, g, time.Now().Add(-time.Hour))
	if len(got) != 2 || got[0].UTMContent != "gp1" || got[1].UTMContent != "gp2" || got[0].Requested != 1 || got[1].Requested != 1 {
		t.Errorf("rows = %+v, want gp1 and gp2 with one signup each", got)
	}
}

func TestSignupsByGuide_RepeatRequestCountsOnce(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	g := testGuide(t, "g")
	sub := guideSignup(t, store, g, "")
	if _, err := store.Unsubscribe(ctx, sub.ID, SourceOneClick, longAgo().Add(time.Minute)); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if _, err := store.RestartSignup(ctx, sub.ID, RestartSignupInput{
		ConfirmTTL: time.Hour, SignupGuide: g, UTMCampaign: "guides",
	}, longAgo().Add(2*time.Minute)); err != nil {
		t.Fatalf("RestartSignup: %v", err)
	}
	if got := guideSignupsFor(t, store, g, time.Now().Add(-time.Hour)); len(got) != 1 || got[0].Requested != 1 {
		t.Errorf("rows = %+v, want Requested 1", got)
	}
}

// A confirmation that predates the guide signup (an earlier, unattributed
// subscription) is not credited to the guide.
func TestSignupsByGuide_ConfirmBeforeRequestNotCounted(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	g := testGuide(t, "g")
	sub, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, longAgo())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Confirm(ctx, *sub.ConfirmToken, longAgo().Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := store.Unsubscribe(ctx, sub.ID, SourceOneClick, longAgo().Add(2*time.Minute)); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if _, err := store.RestartSignup(ctx, sub.ID, RestartSignupInput{ConfirmTTL: time.Hour, SignupGuide: g}, longAgo().Add(3*time.Minute)); err != nil {
		t.Fatalf("RestartSignup: %v", err)
	}
	if got := guideSignupsFor(t, store, g, time.Now().Add(-time.Hour)); len(got) != 1 || got[0].Requested != 1 || got[0].Confirmed != 0 {
		t.Errorf("rows = %+v, want Requested 1 Confirmed 0", got)
	}
}
