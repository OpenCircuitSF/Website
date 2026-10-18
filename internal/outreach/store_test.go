package outreach

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

// seedWorkshop creates a throwaway workshop and deletes it on cleanup (which
// cascades to its outreach).
func seedWorkshop(t *testing.T, pool *pgxpool.Pool) workshops.Workshop {
	t.Helper()
	ws := workshops.NewStore(pool)
	w, err := ws.Create(context.Background(), workshops.CreateInput{Title: fmt.Sprintf("zz outreach test %d", testdb.Unique())})
	if err != nil {
		t.Fatalf("create workshop: %v", err)
	}
	t.Cleanup(func() { _ = ws.Delete(context.Background(), w.ID) })
	return w
}

func seedPost(t *testing.T, s *Store, body string) (Outreach, Post) {
	t.Helper()
	w := seedWorkshop(t, s.pool)
	o, created, err := s.Create(context.Background(), w.ID, w.Slug)
	if err != nil || !created {
		t.Fatalf("create outreach: %v created=%v", err, created)
	}
	p, err := s.CreatePost(context.Background(), o.ID, "bluesky", "announce", body, "fp")
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	return o, p
}

func TestCreateIsIdempotent(t *testing.T) {
	s := NewStore(testPool(t))
	w := seedWorkshop(t, s.pool)
	ctx := context.Background()
	a, created, err := s.Create(ctx, w.ID, "slug-a")
	if err != nil || !created {
		t.Fatalf("first: %v %v", err, created)
	}
	b, created, err := s.Create(ctx, w.ID, "slug-b")
	if err != nil || created || b.ID != a.ID || b.CampaignSlug != "slug-a" {
		t.Fatalf("second: %+v created=%v err=%v", b, created, err)
	}
	if _, err := s.GetByWorkshop(ctx, w.ID+999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown workshop err = %v", err)
	}
}

func TestSlotUniqueness(t *testing.T) {
	s := NewStore(testPool(t))
	o, _ := seedPost(t, s, "x")
	if _, err := s.CreatePost(context.Background(), o.ID, "bluesky", "announce", "y", "fp"); !errors.Is(err, ErrPostExists) {
		t.Errorf("duplicate slot err = %v", err)
	}
	if _, err := s.CreatePost(context.Background(), o.ID, "bluesky", "reminder", "y", "fp"); err != nil {
		t.Errorf("other wave: %v", err)
	}
}

func TestSetPreparedSubstitutesAndIsIdempotent(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	_, p := seedPost(t, s, "Hello\n\nSign up: {link}")
	got, changed, err := s.SetPrepared(ctx, p.ID, "abc", "https://go.example/abc", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("the first SetPrepared must report that it changed the row")
	}
	if got.Body != "Hello\n\nSign up: https://go.example/abc" || !got.Prepared() || got.Status != "manual" {
		t.Errorf("prepared = %+v", got)
	}
	again, changed, err := s.SetPrepared(ctx, p.ID, "zzz", "https://go.example/zzz", "manual")
	if changed {
		t.Error("a second SetPrepared must report false, so a racing loser does not audit (#0544 review follow-up)")
	}
	// Even a second call carrying the SAME key (what a racing Prepare holds,
	// since ShortLinks de-duplicates the link) reports false.
	if _, changed, err := s.SetPrepared(ctx, p.ID, "abc", "https://go.example/abc", "manual"); err != nil || changed {
		t.Errorf("same-key second call: changed=%v err=%v", changed, err)
	}
	if err != nil || *again.ShortLinkKey != "abc" || again.Body != got.Body {
		t.Errorf("second prepare changed the post: %+v err=%v", again, err)
	}
}

func TestSetPreparedAppendsWhenNoToken(t *testing.T) {
	s := NewStore(testPool(t))
	o, bs := seedPost(t, s, "bluesky body, no link by design")
	ma, err := s.CreatePost(context.Background(), o.ID, "mastodon", "announce", "hand edited, no token", "fp")
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := s.SetPrepared(context.Background(), ma.ID, "k", "https://go.example/k", "draft")
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "hand edited, no token\n\nhttps://go.example/k" || got.Status != "draft" {
		t.Errorf("body = %q status=%q", got.Body, got.Status)
	}
	// Bluesky carries its link in the card (#0570), so Prepare leaves the body alone.
	got, _, err = s.SetPrepared(context.Background(), bs.ID, "k2", "https://go.example/k2", "draft")
	if err != nil || got.Body != "bluesky body, no link by design" || got.ShortURL == nil {
		t.Errorf("bluesky body = %q err=%v", got.Body, err)
	}
}

func TestPostedRefusals(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	_, p := seedPost(t, s, "x {link}")
	if _, err := s.MarkPosted(ctx, p.ID, nil, time.Now()); !errors.Is(err, ErrNotPrepared) {
		t.Errorf("unprepared mark err = %v", err)
	}
	if _, _, err := s.SetPrepared(ctx, p.ID, "k", "https://go.example/k", "manual"); err != nil {
		t.Fatal(err)
	}
	u := "https://bsky.app/profile/x/post/1"
	posted, err := s.MarkPosted(ctx, p.ID, &u, time.Now())
	if err != nil || posted.Status != "posted" || posted.PostedAt == nil || *posted.RemoteURL != u {
		t.Fatalf("mark posted: %+v %v", posted, err)
	}
	if _, err := s.MarkPosted(ctx, p.ID, nil, time.Now()); !errors.Is(err, ErrPostLocked) {
		t.Errorf("second mark err = %v", err)
	}
	if _, err := s.UpdatePostBody(ctx, p.ID, "new", nil, nil); !errors.Is(err, ErrPostLocked) {
		t.Errorf("update err = %v", err)
	}
	if err := s.DeletePost(ctx, p.ID); !errors.Is(err, ErrPostLocked) {
		t.Errorf("delete err = %v", err)
	}
	if err := s.DeletePost(ctx, p.ID+999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete unknown err = %v", err)
	}
}

func TestUpdateBodyFingerprintAndDelete(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	_, p := seedPost(t, s, "x")
	edited, err := s.UpdatePostBody(ctx, p.ID, "edited", nil, nil)
	if err != nil || edited.Body != "edited" || edited.SourceFingerprint != "fp" {
		t.Fatalf("hand edit: %+v %v", edited, err)
	}
	tr := true
	cust, err := s.UpdatePostBody(ctx, p.ID, "edited2", nil, &tr)
	if err != nil || !cust.Customized {
		t.Fatalf("customize: %+v %v", cust, err)
	}
	kept, err := s.UpdatePostBody(ctx, p.ID, "edited3", nil, nil)
	if err != nil || !kept.Customized {
		t.Fatalf("nil customized must leave the flag: %+v %v", kept, err)
	}
	fp2 := "fp2"
	regen, err := s.UpdatePostBody(ctx, p.ID, "regen", &fp2, nil)
	if err != nil || regen.SourceFingerprint != "fp2" || regen.Customized {
		t.Fatalf("regenerate: %+v %v", regen, err)
	}
	if err := s.DeletePost(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPost(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete err = %v", err)
	}
}

func TestWorkshopDeleteCascadesAndSummaries(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	w := seedWorkshop(t, s.pool)
	o, _, err := s.Create(ctx, w.ID, w.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePost(ctx, o.ID, "bluesky", "announce", "a", "fp"); err != nil {
		t.Fatal(err)
	}
	sums, err := s.ListSummaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, sm := range sums {
		if sm.OutreachID == o.ID {
			found = true
			if sm.Counts["draft"] != 1 || sm.WorkshopSlug != w.Slug {
				t.Errorf("summary = %+v", sm)
			}
		}
	}
	if !found {
		t.Error("summary missing")
	}
	if err := workshops.NewStore(s.pool).Delete(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByID(ctx, o.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("outreach survived workshop delete: %v", err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outreach_posts WHERE outreach_id = $1`, o.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("posts survived: n=%d err=%v", n, err)
	}
}
