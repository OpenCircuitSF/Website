package guideposts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

const testBase = "https://site.example.test"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

func newTestStore(t *testing.T) *Store {
	pool := testPool(t)
	return NewStore(pool, outbox.NewStore(pool))
}

// create makes a draft and deletes it, and its queue rows, on cleanup.
func create(t *testing.T, s *Store, slug, channel, body, campaign string) Post {
	t.Helper()
	p, err := s.Create(context.Background(), slug, channel, body, testBase, campaign)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = s.pool.Exec(ctx, `DELETE FROM outbound_queue WHERE recipient = $1`, Recipient(p.ID))
		_, _ = s.pool.Exec(ctx, `DELETE FROM guide_posts WHERE id = $1`, p.ID)
	})
	return p
}

// linked makes a draft and gives it a fake short link.
func linked(t *testing.T, s *Store, channel string) Post {
	t.Helper()
	p := create(t, s, "baud-rate", channel, "", "")
	key := fmt.Sprintf("t%d", p.ID)
	p, _, err := s.SetLink(context.Background(), p.ID, key, "https://go.example.test/u/"+key, CampaignSlug)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func queueRows(t *testing.T, s *Store, id int64) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE recipient = $1 AND kind = $2`, Recipient(id), string(outbox.KindGuidePost)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreate_PrefillsFromTopicWithLinkToken(t *testing.T) {
	s := newTestStore(t)
	p := create(t, s, "baud-rate", "bluesky", "", "")
	g, _ := guides.Lookup("baud-rate")
	if p.Body != g.Post+"\n\n"+outreach.LinkToken || p.Status != "draft" || p.ShortURL != nil || p.CampaignSlug != CampaignSlug {
		t.Errorf("post = %+v", p)
	}
}

func TestCreate_RejectsUnknownSlug(t *testing.T) {
	s := newTestStore(t)
	for _, slug := range []string{"nope", "programming-leds"} {
		if _, err := s.Create(context.Background(), slug, "bluesky", "", testBase, ""); !errors.Is(err, ErrUnknownGuide) {
			t.Errorf("%s: err = %v", slug, err)
		}
	}
	if _, err := s.Create(context.Background(), "baud-rate", "instagram", "", testBase, ""); !errors.Is(err, ErrChannelUnknown) {
		t.Errorf("manual channel: err = %v", err)
	}
	if _, err := s.Create(context.Background(), "baud-rate", "bluesky", "", testBase, "Bad Slug"); !errors.Is(err, ErrBadCampaignSlug) {
		t.Errorf("bad campaign: err = %v", err)
	}
}

func TestCreate_RejectsOverLimitBody(t *testing.T) {
	s := newTestStore(t)
	body := strings.Repeat("x", 280) + " " + outreach.LinkToken
	if _, err := s.Create(context.Background(), "baud-rate", "bluesky", body, testBase, ""); err == nil {
		t.Error("over-limit body accepted")
	}
}

func TestCreate_DestinationCarriesOwnID(t *testing.T) {
	s := newTestStore(t)
	p := create(t, s, "baud-rate", "mastodon", "", "")
	want := DestinationURL(testBase, "baud-rate", mustChannel(t, "mastodon"), p.ID)
	if p.DestinationURL != want {
		t.Errorf("destination = %q, want %q", p.DestinationURL, want)
	}
}

func TestSetLink_ReplacesTokenOnceAndIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := create(t, s, "baud-rate", "bluesky", "", "")
	got, changed, err := s.SetLink(ctx, p.ID, "k1"+fmt.Sprint(p.ID), "https://go.example.test/u/one", "guide-posts-2")
	if err != nil || !changed {
		t.Fatalf("SetLink = %v, %v", changed, err)
	}
	if strings.Contains(got.Body, outreach.LinkToken) || strings.Count(got.Body, "https://go.example.test/u/one") != 1 || got.CampaignSlug != "guide-posts-2" {
		t.Errorf("post = %+v", got)
	}
	again, changed, err := s.SetLink(ctx, p.ID, "k2"+fmt.Sprint(p.ID), "https://go.example.test/u/two", CampaignSlug)
	if err != nil || changed || again.Body != got.Body || *again.ShortURL != "https://go.example.test/u/one" {
		t.Errorf("second SetLink changed the row: %+v %v %v", again, changed, err)
	}
}

func TestSetLink_AppendsWhenTokenRemoved(t *testing.T) {
	s := newTestStore(t)
	p := create(t, s, "baud-rate", "bluesky", "Just text.", "")
	got, _, err := s.SetLink(context.Background(), p.ID, "a"+fmt.Sprint(p.ID), "https://go.example.test/u/a", CampaignSlug)
	if err != nil || got.Body != "Just text.\n\nhttps://go.example.test/u/a" {
		t.Errorf("body = %q, %v", got.Body, err)
	}
}

func TestSchedule_RefusesPostWithoutLink(t *testing.T) {
	s := newTestStore(t)
	p := create(t, s, "baud-rate", "bluesky", "", "")
	if _, err := s.Schedule(context.Background(), p.ID, time.Now().Add(time.Hour)); !errors.Is(err, ErrNoLink) {
		t.Errorf("err = %v", err)
	}
}

func TestQueueNow_RefusesPostWithoutLink(t *testing.T) {
	s := newTestStore(t)
	p := create(t, s, "baud-rate", "bluesky", "", "")
	if _, _, err := s.QueueNow(context.Background(), p.ID); !errors.Is(err, ErrNoLink) {
		t.Errorf("err = %v", err)
	}
	if n := queueRows(t, s, p.ID); n != 0 {
		t.Errorf("%d queue rows", n)
	}
}

func TestUpdateBody_LockedWhenQueuedOrPosted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := linked(t, s, "mastodon")
	if _, err := s.UpdateBody(ctx, p.ID, "edited "+*p.ShortURL); err != nil {
		t.Fatalf("draft edit: %v", err)
	}
	if _, err := s.UpdateBody(ctx, p.ID, "still has "+outreach.LinkToken); err == nil {
		t.Error("a linked post accepted the link token back")
	}
	if _, _, err := s.QueueNow(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateBody(ctx, p.ID, "x"); !errors.Is(err, ErrPostInFlight) {
		t.Errorf("queued: err = %v", err)
	}
	if _, err := s.MarkRemotePosted(ctx, p.ID, "r", "u", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateBody(ctx, p.ID, "x"); !errors.Is(err, ErrPostLocked) {
		t.Errorf("posted: err = %v", err)
	}
}

func TestSchedule_ThenUnschedule(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := linked(t, s, "bluesky")
	at := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	got, err := s.Schedule(ctx, p.ID, at)
	if err != nil || got.Status != "scheduled" || !got.ScheduledAt.Equal(at) {
		t.Fatalf("Schedule = %+v, %v", got, err)
	}
	got, err = s.Unschedule(ctx, p.ID)
	if err != nil || got.Status != "draft" || got.ScheduledAt != nil {
		t.Fatalf("Unschedule = %+v, %v", got, err)
	}
	if _, err := s.Unschedule(ctx, p.ID); !errors.Is(err, ErrWrongStatus) {
		t.Errorf("second Unschedule: %v", err)
	}
}

func TestQueueNow_EnqueuesOneGuidePostRowInSameTx(t *testing.T) {
	s := newTestStore(t)
	p := linked(t, s, "bluesky")
	got, prev, err := s.QueueNow(context.Background(), p.ID)
	if err != nil || got.Status != "queued" || prev != "draft" {
		t.Fatalf("QueueNow = %+v, %q, %v", got, prev, err)
	}
	if n := queueRows(t, s, p.ID); n != 1 {
		t.Errorf("%d guide_post queue rows, want 1", n)
	}
	if _, _, err := s.QueueNow(context.Background(), p.ID); !errors.Is(err, ErrPostInFlight) {
		t.Errorf("requeue: %v", err)
	}
}

func TestPromoteDue_QueuesDuePosts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := linked(t, s, "bluesky")
	if _, err := s.Schedule(ctx, p.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PromoteDue(ctx, 100); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, p.ID)
	if got.Status != "queued" || queueRows(t, s, p.ID) != 1 {
		t.Errorf("post = %+v", got)
	}
}

func TestPromoteDue_HoldsUnknownSlug(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := linked(t, s, "bluesky")
	if _, err := s.Schedule(ctx, p.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE guide_posts SET guide_slug = 'retired-guide' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PromoteDue(ctx, 100); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, p.ID)
	if got.Status != "draft" || got.LastError == nil || *got.LastError != HeldUnknownGuideMessage || queueRows(t, s, p.ID) != 0 {
		t.Errorf("post = %+v", got)
	}
}

func TestDelete_RefusesPosted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := linked(t, s, "bluesky")
	if _, _, err := s.QueueNow(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, p.ID); !errors.Is(err, ErrPostInFlight) {
		t.Errorf("queued: %v", err)
	}
	if _, err := s.MarkRemotePosted(ctx, p.ID, "r", "u", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, p.ID); !errors.Is(err, ErrPostLocked) {
		t.Errorf("posted: %v", err)
	}
	d := create(t, s, "baud-rate", "bluesky", "", "")
	if err := s.Delete(ctx, d.ID); err != nil {
		t.Errorf("draft: %v", err)
	}
	if _, err := s.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted draft still there: %v", err)
	}
}

func TestShortLinkKey_UniqueAcrossPosts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := linked(t, s, "bluesky")
	b := create(t, s, "baud-rate", "bluesky", "", "")
	if _, _, err := s.SetLink(ctx, b.ID, *a.ShortLinkKey, "https://go.example.test/u/other", CampaignSlug); err == nil {
		t.Error("two posts share a short-link key")
	}
}

func TestClickKeys_GroupsByCampaign(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	camp1 := fmt.Sprintf("test-ck-%d-a", time.Now().UnixNano())
	camp2 := fmt.Sprintf("test-ck-%d-b", time.Now().UnixNano())
	var want1, want2 []string
	for i, c := range []string{camp1, camp1, camp2} {
		p := create(t, s, "baud-rate", "bluesky", "", c)
		key := fmt.Sprintf("ck%d", p.ID)
		if _, _, err := s.SetLink(ctx, p.ID, key, "https://go.example.test/u/"+key, c); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			want1 = append(want1, key)
		} else {
			want2 = append(want2, key)
		}
	}
	create(t, s, "baud-rate", "bluesky", "", camp2) // unlinked: not listed
	got, err := s.ClickKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string][]string{}
	for _, c := range got {
		found[c.CampaignSlug] = c.Keys
		if c.Since.IsZero() {
			t.Errorf("%s: zero Since", c.CampaignSlug)
		}
	}
	if fmt.Sprint(found[camp1]) != fmt.Sprint(want1) || fmt.Sprint(found[camp2]) != fmt.Sprint(want2) {
		t.Errorf("ClickKeys = %v, want %v and %v", found, want1, want2)
	}
}
