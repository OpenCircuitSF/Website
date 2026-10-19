package outreach

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
)

// All servers here are httptest fakes; nothing reaches a real network (#0604).

func TestRefreshBodyLink(t *testing.T) {
	const cur = "https://go.example.test/u/abc"
	for _, tc := range []struct{ name, body, want string }{
		{"old form replaced", "Join us https://go.example.test/abc", "Join us " + cur},
		{"old form before punctuation", "Join us https://go.example.test/abc.", "Join us " + cur + "."},
		{"already current is untouched", "Join us " + cur, "Join us " + cur},
		{"link token is untouched", "Join us {link}", "Join us {link}"},
		{"longer key is not rewritten", "see https://go.example.test/abcd", "see https://go.example.test/abcd"},
		{"longer path is not rewritten", "see https://go.example.test/abc/x", "see https://go.example.test/abc/x"},
		{"both in one body", "https://go.example.test/abcd and https://go.example.test/abc", "https://go.example.test/abcd and " + cur},
		{"twice", "https://go.example.test/abc https://go.example.test/abc", cur + " " + cur},
		{"other host untouched", "https://other.example.test/abc", "https://other.example.test/abc"},
		{"no link at all", "plain text", "plain text"},
	} {
		if got := RefreshBodyLink(tc.body, cur); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	// A key that is itself "u": the old form is a prefix of the current one.
	if got := RefreshBodyLink("x https://go.example.test/u", "https://go.example.test/u/u"); got != "x https://go.example.test/u/u" {
		t.Errorf("key u: %q", got)
	}
	if got := RefreshBodyLink("x https://go.example.test/u/u", "https://go.example.test/u/u"); got != "x https://go.example.test/u/u" {
		t.Errorf("key u, already repaired: %q", got)
	}
	// The production shape (#0604 review): a key beginning with "u", so the
	// dead form host/uKEY and the live form host/u/uKEY differ only by the
	// slash. The dead form is replaced; the live form is left alone.
	const prodCur = "https://go.example.test/u/uXLcM0"
	if got := RefreshBodyLink("Solder with us!\nhttps://go.example.test/uXLcM0\n#DIY", prodCur); got != "Solder with us!\n"+prodCur+"\n#DIY" {
		t.Errorf("production shape: %q", got)
	}
	if got := RefreshBodyLink("Solder with us! "+prodCur, prodCur); got != "Solder with us! "+prodCur {
		t.Errorf("production shape, already repaired: %q", got)
	}
	// A short URL not in the /u/ form has no derivable old form: unchanged.
	if got := RefreshBodyLink("x https://go.example.test/abc", "https://go.example.test/abc"); got != "x https://go.example.test/abc" {
		t.Errorf("non-/u/ short url: %q", got)
	}
}

func TestIdempotencyKeys_GenerationZeroIsUnchanged(t *testing.T) {
	created := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	if got, want := MastodonIdempotencyKey(7, 0), "opencircuitsf-outreach-post-7"; got != want {
		t.Errorf("gen 0 mastodon key = %q, want %q", got, want)
	}
	seen := map[string]bool{}
	mk := map[string]bool{}
	for g := 0; g < 5; g++ {
		r, k := BlueskyRKey(created, 7, g), MastodonIdempotencyKey(7, g)
		if seen[r] || mk[k] {
			t.Errorf("generation %d reuses a key: %q %q", g, r, k)
		}
		seen[r], mk[k] = true, true
	}
}

func TestBlueskyRKey_GenerationZeroMatchesTheOriginalDerivation(t *testing.T) {
	// The pre-#0604 publisher computed bluesky.TID(createdAt.UnixMicro(), id&1023).
	created := time.Date(2026, 10, 16, 12, 0, 0, 123456000, time.UTC)
	want := bluesky.TID(created.UnixMicro(), 1030&1023)
	if got := BlueskyRKey(created, 1030, 0); got != want {
		t.Errorf("gen 0 rkey = %q, want original %q", got, want)
	}
}

func TestStore_CheckAndCompleteTakedown(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	_, post := seedSendable(t, s)

	if _, err := s.CheckTakedown(ctx, post.ID); !errors.Is(err, ErrTakedownNotPosted) {
		t.Fatalf("draft: err = %v, want ErrTakedownNotPosted", err)
	}
	if _, err := s.CheckTakedown(ctx, 999999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: err = %v, want ErrNotFound", err)
	}

	short := *post.ShortURL // https://go.example.test/k<ID> in the seed
	// Production shape after #0603: short_url repaired, body keeps the old URL.
	repaired := strings.Replace(short, "go.example.test/", "go.example.test/u/", 1)
	old := short
	if _, err := s.pool.Exec(ctx, `UPDATE outreach_posts SET short_url = $2, body = $3, status = 'posted', posted_at = now(),
		remote_id = 'at://did:plc:x/app.bsky.feed.post/abc', remote_url = 'https://bsky.app/x', scheduled_at = now(), last_error = 'old' WHERE id = $1`,
		post.ID, repaired, "Soldering night "+old); err != nil {
		t.Fatal(err)
	}
	got, err := s.CheckTakedown(ctx, post.ID)
	if err != nil || got.Status != "posted" {
		t.Fatalf("posted: %+v %v", got, err)
	}

	// A live queue row blocks it.
	if _, err := s.pool.Exec(ctx, `INSERT INTO outbound_queue (kind, recipient, payload, status) VALUES ('outreach_post', $1, $2::jsonb, 'sending')`,
		fmt.Sprintf("outreach_post:%d", post.ID), fmt.Sprintf(`{"post_id": %d}`, post.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckTakedown(ctx, post.ID); !errors.Is(err, ErrTakedownInFlight) {
		t.Fatalf("live row: err = %v, want ErrTakedownInFlight", err)
	}
	if _, err := s.CompleteTakedown(ctx, post.ID, "at://did:plc:x/app.bsky.feed.post/abc"); !errors.Is(err, ErrTakedownInFlight) {
		t.Fatalf("complete with live row: err = %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE outbound_queue SET status = 'sent' WHERE recipient = $1`, fmt.Sprintf("outreach_post:%d", post.ID)); err != nil {
		t.Fatal(err)
	}

	// A different remote id means someone else changed it.
	if _, err := s.CompleteTakedown(ctx, post.ID, "at://did:plc:x/app.bsky.feed.post/zzz"); !errors.Is(err, ErrTakedownChanged) {
		t.Fatalf("stale remote id: err = %v, want ErrTakedownChanged", err)
	}

	done, err := s.CompleteTakedown(ctx, post.ID, "at://did:plc:x/app.bsky.feed.post/abc")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "draft" || done.PostedAt != nil || done.RemoteID != nil || done.RemoteURL != nil || done.ScheduledAt != nil || done.LastError != nil {
		t.Errorf("not reset to a clean draft: %+v", done)
	}
	if done.AttemptGeneration != 1 {
		t.Errorf("attempt_generation = %d, want 1", done.AttemptGeneration)
	}
	if want := "Soldering night " + repaired; done.Body != want {
		t.Errorf("body = %q, want %q", done.Body, want)
	}
	if done.ShortURL == nil || *done.ShortURL != repaired {
		t.Errorf("short_url changed: %v", done.ShortURL)
	}
	// A second takedown (a double click) finds a draft and refuses.
	if _, err := s.CompleteTakedown(ctx, post.ID, "at://did:plc:x/app.bsky.feed.post/abc"); !errors.Is(err, ErrTakedownChanged) {
		t.Errorf("repeat: err = %v, want ErrTakedownChanged", err)
	}
	// Reset is possible again once nothing is posted.
	if ok, err := s.CanReset(ctx, done.OutreachID); err != nil || !ok {
		t.Errorf("CanReset = %v, %v; want true", ok, err)
	}
}

func TestStore_CheckTakedownRefusesAHandPostedRow(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	_, post := seedSendable(t, s)
	if _, err := s.pool.Exec(ctx, `UPDATE outreach_posts SET status = 'posted', posted_at = now() WHERE id = $1`, post.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckTakedown(ctx, post.ID); !errors.Is(err, ErrTakedownNotPosted) {
		t.Errorf("no remote id: err = %v, want ErrTakedownNotPosted", err)
	}
}

// post, take down, post again: the second send creates a NEW Bluesky record
// and does not recover the deleted one.
func TestTakedownThenResend_Bluesky(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	bsky := newFakeBsky(t)
	pub := newTestPublisher(t, s, bsky, nil, "")
	_, post := seedSendable(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	first, _ := s.GetPost(ctx, post.ID)
	if first.Status != "posted" || first.RemoteID == nil {
		t.Fatalf("first send: %+v", first)
	}

	existed, err := pub.DeleteRemote(ctx, first)
	if err != nil || !existed {
		t.Fatalf("DeleteRemote = %v, %v", existed, err)
	}
	if _, err := s.CompleteTakedown(ctx, post.ID, *first.RemoteID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)

	second, _ := s.GetPost(ctx, post.ID)
	if second.Status != "posted" || second.RemoteID == nil {
		t.Fatalf("second send: %+v", second)
	}
	if *second.RemoteID == *first.RemoteID {
		t.Errorf("re-send recovered the deleted record %s", *first.RemoteID)
	}
	if n := bsky.createCount(); n != 2 {
		t.Errorf("createRecord count = %d, want 2 (a new record, not an adoption)", n)
	}
	if second.AttemptGeneration != 1 {
		t.Errorf("generation = %d", second.AttemptGeneration)
	}
}

func TestTakedownThenResend_Mastodon(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	masto := newFakeMasto(t)
	pub := newTestPublisher(t, s, nil, masto, "")
	_, post := seedMastodon(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	first, _ := s.GetPost(ctx, post.ID)
	if first.Status != "posted" || first.RemoteID == nil || *first.RemoteID != "9001" {
		t.Fatalf("first send: %+v", first)
	}
	if existed, err := pub.DeleteRemote(ctx, first); err != nil || !existed {
		t.Fatalf("DeleteRemote = %v, %v", existed, err)
	}
	if len(masto.deleted) != 1 || masto.deleted[0] != "/api/v1/statuses/9001" {
		t.Errorf("DELETE requests = %v", masto.deleted)
	}
	if _, err := s.CompleteTakedown(ctx, post.ID, *first.RemoteID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	second, _ := s.GetPost(ctx, post.ID)
	if second.Status != "posted" || second.RemoteID == nil || *second.RemoteID == "9001" {
		t.Fatalf("second send recovered the deleted status: %+v", second)
	}
	want := []string{MastodonIdempotencyKey(post.ID, 0), MastodonIdempotencyKey(post.ID, 1)}
	if len(masto.keys) != 2 || masto.keys[0] != want[0] || masto.keys[1] != want[1] || want[0] == want[1] {
		t.Errorf("idempotency keys = %v, want %v", masto.keys, want)
	}
}

func TestDeleteRemote_AlreadyGone(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	bsky := newFakeBsky(t)
	masto := newFakeMasto(t)
	masto.deleteStatus = 404
	pub := newTestPublisher(t, s, bsky, masto, "")

	// Bluesky: the record was deleted by hand, so it is not on the PDS.
	bid := "at://" + fakeDID + "/app.bsky.feed.post/3kgone"
	if existed, err := pub.DeleteRemote(ctx, Post{Channel: "bluesky", RemoteID: &bid}); err != nil || existed {
		t.Errorf("bluesky gone: %v, %v; want not existed, nil", existed, err)
	}
	if len(bsky.deleted) != 0 {
		t.Errorf("deleteRecord sent for a missing record: %v", bsky.deleted)
	}
	// Mastodon: DELETE answers 404.
	mid := "123"
	if existed, err := pub.DeleteRemote(ctx, Post{Channel: "mastodon", RemoteID: &mid}); err != nil || existed {
		t.Errorf("mastodon gone: %v, %v; want not existed, nil", existed, err)
	}
}

func TestDeleteRemote_Refusals(t *testing.T) {
	ctx := context.Background()
	var nilPub *Publisher
	id := "1"
	if _, err := nilPub.DeleteRemote(ctx, Post{Channel: "mastodon", RemoteID: &id}); err == nil {
		t.Error("nil publisher must error")
	}
	pub := NewPublisher(PublisherDeps{})
	if _, err := pub.DeleteRemote(ctx, Post{Channel: "mastodon", RemoteID: &id}); err == nil {
		t.Error("unconfigured Mastodon must error")
	}
	if _, err := pub.DeleteRemote(ctx, Post{Channel: "bluesky", RemoteID: &id}); err == nil {
		t.Error("unconfigured Bluesky must error")
	}
	if _, err := pub.DeleteRemote(ctx, Post{Channel: "mastodon"}); err == nil {
		t.Error("no remote id must error")
	}
	if _, err := pub.DeleteRemote(ctx, Post{Channel: "instagram", RemoteID: &id}); err == nil {
		t.Error("manual channel must error")
	}
}
