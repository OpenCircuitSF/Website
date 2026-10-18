package outreach

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setWorkshopStatus flips a seeded workshop's status directly.
func setWorkshopStatus(t *testing.T, pool *pgxpool.Pool, outreachID int64, status string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE workshops SET status = $2 WHERE id = (SELECT workshop_id FROM outreach WHERE id = $1)`, outreachID, status)
	if err != nil {
		t.Fatalf("set workshop status: %v", err)
	}
}

// seedSendable seeds a prepared bluesky post on a published workshop and
// removes its outbound_queue rows on cleanup (the rows carry no FK to the
// post, so the workshop's cascade does not reach them).
func seedSendable(t *testing.T, s *Store) (Outreach, Post) {
	t.Helper()
	o, p := seedPost(t, s, "Soldering night {link}")
	setWorkshopStatus(t, s.pool, o.ID, "published")
	p, _, err := s.SetPrepared(context.Background(), p.ID, fmt.Sprintf("k%d", p.ID), fmt.Sprintf("https://go.example.test/k%d", p.ID), "draft")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	id := p.ID
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE recipient = $1`, fmt.Sprintf("outreach_post:%d", id))
	})
	return o, p
}

func queueRows(t *testing.T, s *Store, postID int64) (n int, kind, payload string) {
	t.Helper()
	rows, err := s.pool.Query(context.Background(),
		`SELECT kind, payload::text FROM outbound_queue WHERE recipient = $1`, fmt.Sprintf("outreach_post:%d", postID))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		n++
		if err := rows.Scan(&kind, &payload); err != nil {
			t.Fatal(err)
		}
	}
	return n, kind, payload
}

func TestQueueNowEnqueuesExactlyOneRow(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	_, p := seedSendable(t, s)
	got, prev, err := s.QueueNow(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "queued" || prev != "draft" || got.ScheduledAt == nil {
		t.Errorf("post = %+v prev=%q", got, prev)
	}
	n, kind, payload := queueRows(t, s, p.ID)
	if n != 1 || kind != "outreach_post" || !strings.Contains(payload, fmt.Sprintf(`"post_id": %d`, p.ID)) {
		t.Errorf("queue rows n=%d kind=%q payload=%q", n, kind, payload)
	}
	if _, _, err := s.QueueNow(ctx, p.ID); !errors.Is(err, ErrPostInFlight) {
		t.Errorf("second QueueNow err = %v, want ErrPostInFlight", err)
	}
	if n, _, _ := queueRows(t, s, p.ID); n != 1 {
		t.Errorf("a refused QueueNow left %d rows", n)
	}
}

func TestQueueNowRefusals(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()

	_, unprepared := seedPost(t, s, "x {link}")
	if _, _, err := s.QueueNow(ctx, unprepared.ID); !errors.Is(err, ErrNotPrepared) {
		t.Errorf("unprepared err = %v", err)
	}
	if _, _, err := s.QueueNow(ctx, unprepared.ID+999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown err = %v", err)
	}

	o, p := seedSendable(t, s)
	setWorkshopStatus(t, s.pool, o.ID, "canceled")
	if _, _, err := s.QueueNow(ctx, p.ID); !errors.Is(err, ErrWorkshopNotPublished) {
		t.Errorf("canceled workshop err = %v", err)
	}
	if n, _, _ := queueRows(t, s, p.ID); n != 0 {
		t.Errorf("refused QueueNow enqueued %d rows", n)
	}
	if _, err := s.Schedule(ctx, p.ID, time.Now().Add(time.Hour)); !errors.Is(err, ErrWorkshopNotPublished) {
		t.Errorf("schedule on canceled workshop err = %v", err)
	}

	setWorkshopStatus(t, s.pool, o.ID, "published")
	u := "https://example.test/p"
	if _, err := s.MarkPosted(ctx, p.ID, &u, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.QueueNow(ctx, p.ID); !errors.Is(err, ErrPostLocked) {
		t.Errorf("posted err = %v", err)
	}

	// A manual-channel post is prepared with status manual and cannot be sent.
	w := seedWorkshop(t, s.pool)
	mo, _, _ := s.Create(ctx, w.ID, w.Slug)
	setWorkshopStatus(t, s.pool, mo.ID, "published")
	mp, _ := s.CreatePost(ctx, mo.ID, "instagram", "announce", "x {link}", "fp")
	if _, _, err := s.SetPrepared(ctx, mp.ID, "mk", "https://go.example.test/mk", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.QueueNow(ctx, mp.ID); !errors.Is(err, ErrWrongStatus) {
		t.Errorf("manual status err = %v", err)
	}
}

func TestScheduleAndUnschedule(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	_, p := seedSendable(t, s)
	at := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	got, err := s.Schedule(ctx, p.ID, at)
	if err != nil || got.Status != "scheduled" || got.ScheduledAt == nil || !got.ScheduledAt.Equal(at) {
		t.Fatalf("schedule: %+v %v", got, err)
	}
	if n, _, _ := queueRows(t, s, p.ID); n != 0 {
		t.Errorf("scheduling must not enqueue, got %d rows", n)
	}
	later := at.Add(time.Hour)
	if got, err := s.Schedule(ctx, p.ID, later); err != nil || !got.ScheduledAt.Equal(later) {
		t.Errorf("reschedule: %+v %v", got, err)
	}
	// A scheduled post stays editable.
	if _, err := s.UpdatePostBody(ctx, p.ID, "edited https://go.example.test/x", nil, nil); err != nil {
		t.Errorf("scheduled post should be editable: %v", err)
	}
	un, err := s.Unschedule(ctx, p.ID)
	if err != nil || un.Status != "draft" || un.ScheduledAt != nil {
		t.Fatalf("unschedule: %+v %v", un, err)
	}
	if _, err := s.Unschedule(ctx, p.ID); !errors.Is(err, ErrWrongStatus) {
		t.Errorf("unschedule a draft err = %v", err)
	}
}

func TestQueuedPostIsLocked(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	_, p := seedSendable(t, s)
	if _, _, err := s.QueueNow(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePostBody(ctx, p.ID, "new", nil, nil); !errors.Is(err, ErrPostInFlight) {
		t.Errorf("update err = %v", err)
	}
	if err := s.DeletePost(ctx, p.ID); !errors.Is(err, ErrPostInFlight) {
		t.Errorf("delete err = %v", err)
	}
	if _, err := s.MarkPosted(ctx, p.ID, nil, time.Now()); !errors.Is(err, ErrPostInFlight) {
		t.Errorf("mark posted err = %v", err)
	}
	if _, err := s.Unschedule(ctx, p.ID); !errors.Is(err, ErrPostInFlight) {
		t.Errorf("unschedule err = %v", err)
	}
}

func TestPromoteDue(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()

	_, due := seedSendable(t, s)
	_, future := seedSendable(t, s)
	co, canceled := seedSendable(t, s)
	for _, p := range []Post{due, canceled} {
		if _, err := s.Schedule(ctx, p.ID, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Schedule(ctx, future.ID, time.Now().Add(72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Both become due; the canceled one's workshop is canceled after scheduling.
	if _, err := s.pool.Exec(ctx, `UPDATE outreach_posts SET scheduled_at = now() - interval '1 minute' WHERE id = ANY($1)`, []int64{due.ID, canceled.ID}); err != nil {
		t.Fatal(err)
	}
	setWorkshopStatus(t, s.pool, co.ID, "canceled")

	promoted, held, err := s.PromoteDue(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if promoted < 1 || held < 1 {
		t.Errorf("promoted=%d held=%d, want at least 1 each", promoted, held)
	}
	d, _ := s.GetPost(ctx, due.ID)
	if d.Status != "queued" {
		t.Errorf("due post status = %q", d.Status)
	}
	if n, kind, _ := queueRows(t, s, due.ID); n != 1 || kind != "outreach_post" {
		t.Errorf("due post queue rows = %d %q", n, kind)
	}
	f, _ := s.GetPost(ctx, future.ID)
	if f.Status != "scheduled" {
		t.Errorf("future post status = %q", f.Status)
	}
	if n, _, _ := queueRows(t, s, future.ID); n != 0 {
		t.Errorf("future post queued %d rows", n)
	}
	c, _ := s.GetPost(ctx, canceled.ID)
	if c.Status != "draft" || c.ScheduledAt != nil || c.LastError == nil || !strings.Contains(*c.LastError, "canceled") {
		t.Errorf("held post = %+v", c)
	}
	if n, _, _ := queueRows(t, s, canceled.ID); n != 0 {
		t.Errorf("held post queued %d rows", n)
	}
	// A second pass promotes nothing twice.
	if _, _, err := s.PromoteDue(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := queueRows(t, s, due.ID); n != 1 {
		t.Errorf("second pass duplicated the queue row: %d", n)
	}
}
