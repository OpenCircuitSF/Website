package outreach

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

// Everything in this file talks to httptest fakes. No Bluesky or Mastodon
// credential exists for this project, and nothing here may reach a real
// platform (#0545). Credentials below are obviously fake fixtures.

const (
	fakeBskyPassword = "fake-app-password-xyz"
	fakeMastoToken   = "fake-mastodon-token-abc"
	fakeDID          = "did:plc:fakefakefake"
)

type fakeBsky struct {
	srv *httptest.Server

	mu      sync.Mutex
	paths   []string
	records map[string]bool
	created []string // createRecord bodies
	failAll int      // when non-zero, createRecord answers this status
}

func newFakeBsky(t *testing.T) *fakeBsky {
	t.Helper()
	f := &fakeBsky{records: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.paths = append(f.paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			io.WriteString(w, `{"accessJwt":"fake-access","refreshJwt":"fake-refresh","did":"`+fakeDID+`","handle":"alice.example.test"}`)
		case "/xrpc/com.atproto.repo.getRecord":
			rk := r.URL.Query().Get("rkey")
			if !f.records[rk] {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"RecordNotFound","message":"not found"}`)
				return
			}
			io.WriteString(w, `{"uri":"at://`+fakeDID+`/app.bsky.feed.post/`+rk+`"}`)
		case "/xrpc/com.atproto.repo.createRecord":
			if f.failAll != 0 {
				w.WriteHeader(f.failAll)
				io.WriteString(w, `{"error":"InvalidRequest","message":"nope"}`)
				return
			}
			var in struct{ RKey string }
			json.Unmarshal(body, &in)
			f.records[in.RKey] = true
			f.created = append(f.created, string(body))
			io.WriteString(w, `{"uri":"at://`+fakeDID+`/app.bsky.feed.post/`+in.RKey+`","cid":"fakecid"}`)
		case "/xrpc/com.atproto.repo.uploadBlob":
			io.WriteString(w, `{"blob":{"$type":"blob","ref":{"$link":"fakeblob"},"mimeType":"image/jpeg","size":4}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBsky) createCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created)
}

func (f *fakeBsky) hits(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.paths {
		if p == path {
			n++
		}
	}
	return n
}

type fakeMasto struct {
	srv    *httptest.Server
	mu     sync.Mutex
	status int // 0 = succeed
	keys   []string
	count  int
}

func newFakeMasto(t *testing.T) *fakeMasto {
	t.Helper()
	f := &fakeMasto{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.count++
		if r.Method == http.MethodGet {
			// The recent-status look made before a post that follows an earlier claimed attempt.
			if r.URL.Path == "/api/v1/accounts/verify_credentials" {
				io.WriteString(w, `{"id":"5"}`)
			} else {
				io.WriteString(w, `[]`)
			}
			return
		}
		f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
		if f.status != 0 {
			w.WriteHeader(f.status)
			io.WriteString(w, `{"error":"fake failure"}`)
			return
		}
		io.WriteString(w, `{"id":"9001","url":"https://social.example.test/@alice/9001","uri":"u"}`)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMasto) setStatus(s int) { f.mu.Lock(); f.status = s; f.mu.Unlock() }
func (f *fakeMasto) requests() int   { f.mu.Lock(); defer f.mu.Unlock(); return f.count }

type fakeSettings struct{ maxRetries string }

func (f fakeSettings) GetSetting(_ context.Context, key string) (string, error) {
	if key == "queue_max_retries" && f.maxRetries != "" {
		return f.maxRetries, nil
	}
	return "", fmt.Errorf("no such setting")
}

func newTestPublisher(t *testing.T, s *Store, bsky *fakeBsky, masto *fakeMasto, mediaDir string) *Publisher {
	t.Helper()
	var bc *bluesky.Client
	var mc *mastodon.Client
	var err error
	if bsky != nil {
		if bc, err = bluesky.New(bsky.srv.URL, "alice.example.test", fakeBskyPassword); err != nil {
			t.Fatal(err)
		}
	}
	if masto != nil {
		if mc, err = mastodon.New(masto.srv.URL, fakeMastoToken); err != nil {
			t.Fatal(err)
		}
	}
	p := NewPublisher(PublisherDeps{
		Store: s, Outbox: outbox.NewStore(s.pool), Settings: fakeSettings{},
		Bluesky: bc, Mastodon: mc, MediaDir: mediaDir, PollInterval: time.Hour,
	})
	return p
}

// seedMastodon seeds a prepared mastodon post on a published workshop.
func seedMastodon(t *testing.T, s *Store) (Outreach, Post) {
	t.Helper()
	w := seedWorkshop(t, s.pool)
	o, _, err := s.Create(context.Background(), w.ID, w.Slug)
	if err != nil {
		t.Fatal(err)
	}
	setWorkshopStatus(t, s.pool, o.ID, "published")
	p, err := s.CreatePost(context.Background(), o.ID, "mastodon", "announce", "Join us {link}", "fp")
	if err != nil {
		t.Fatal(err)
	}
	p, _, err = s.SetPrepared(context.Background(), p.ID, fmt.Sprintf("m%d", p.ID), fmt.Sprintf("https://go.example.test/m%d", p.ID), "draft")
	if err != nil {
		t.Fatal(err)
	}
	id := p.ID
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE recipient = $1`, fmt.Sprintf("outreach_post:%d", id))
	})
	return o, p
}

func queueState(t *testing.T, s *Store, postID int64) (status string, attempts int, errMsg *string) {
	t.Helper()
	err := s.pool.QueryRow(context.Background(),
		`SELECT status, attempts, error FROM outbound_queue WHERE recipient = $1 ORDER BY id DESC LIMIT 1`,
		fmt.Sprintf("outreach_post:%d", postID)).Scan(&status, &attempts, &errMsg)
	if err != nil {
		t.Fatalf("queue row for post %d: %v", postID, err)
	}
	return
}

func runPass(t *testing.T, p *Publisher) {
	t.Helper()
	if _, err := p.pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
}

func TestPublisher_BlueskySuccess(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	bsky := newFakeBsky(t)
	pub := newTestPublisher(t, s, bsky, nil, "")
	_, post := seedSendable(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)

	got, _ := s.GetPost(ctx, post.ID)
	rkey := bluesky.TID(post.CreatedAt.UnixMicro(), uint16(post.ID&1023))
	if got.Status != "posted" || got.PostedAt == nil || got.LastError != nil {
		t.Fatalf("post = %+v", got)
	}
	if got.RemoteID == nil || *got.RemoteID != "at://"+fakeDID+"/app.bsky.feed.post/"+rkey {
		t.Errorf("remote_id = %v", got.RemoteID)
	}
	if got.RemoteURL == nil || *got.RemoteURL != "https://bsky.app/profile/alice.example.test/post/"+rkey {
		t.Errorf("remote_url = %v", got.RemoteURL)
	}
	if st, _, _ := queueState(t, s, post.ID); st != "sent" {
		t.Errorf("queue row status = %q", st)
	}
	if bsky.createCount() != 1 {
		t.Errorf("createRecord count = %d", bsky.createCount())
	}
}

// The card's uri follows bluesky_card_url (#0571): the tracked short link by
// default, the workshop's canonical URL with UTM when set. The post text
// never carries the link either way.
func TestPublisher_BlueskyCardURISetting(t *testing.T) {
	for _, tc := range []struct{ setting, wantPrefix string }{
		{"", "https://go.example.test/"},
		{"short", "https://go.example.test/"},
		{"canonical", "https://site.example.test/workshops/"},
	} {
		t.Run("setting="+tc.setting, func(t *testing.T) {
			s := NewStore(testPool(t))
			ctx := context.Background()
			bsky := newFakeBsky(t)
			pub := newTestPublisher(t, s, bsky, nil, "")
			pub.cardURL = func() string { return tc.setting }
			pub.siteBase = "https://site.example.test"
			_, post := seedSendable(t, s)
			if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
				t.Fatal(err)
			}
			runPass(t, pub)
			if bsky.createCount() != 1 {
				t.Fatalf("createRecord count = %d", bsky.createCount())
			}
			var rec struct {
				Record struct {
					Text  string `json:"text"`
					Embed struct {
						External struct {
							URI string `json:"uri"`
						} `json:"external"`
					} `json:"embed"`
				} `json:"record"`
			}
			if err := json.Unmarshal([]byte(bsky.created[0]), &rec); err != nil {
				t.Fatal(err)
			}
			uri := rec.Record.Embed.External.URI
			if !strings.HasPrefix(uri, tc.wantPrefix) {
				t.Errorf("card uri = %q, want prefix %q", uri, tc.wantPrefix)
			}
			if tc.setting == "canonical" && !strings.Contains(uri, "utm_source=bluesky") {
				t.Errorf("canonical card uri lacks UTM: %q", uri)
			}
		})
	}
}

func TestPublisher_BlueskyThumbnailFromMediaDir(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cover.jpg"), []byte{0xFF, 0xD8, 0xFF, 0xD9}, 0o644); err != nil {
		t.Fatal(err)
	}
	bsky := newFakeBsky(t)
	pub := newTestPublisher(t, s, bsky, nil, dir)
	o, post := seedSendable(t, s)
	if _, err := s.pool.Exec(ctx, `UPDATE workshops SET cover_image = '/media/cover.jpg' WHERE id = (SELECT workshop_id FROM outreach WHERE id = $1)`, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	if bsky.hits("/xrpc/com.atproto.repo.uploadBlob") != 1 || !strings.Contains(bsky.created[0], `"thumb"`) {
		t.Errorf("thumbnail not uploaded and embedded: uploads=%d", bsky.hits("/xrpc/com.atproto.repo.uploadBlob"))
	}
}

func TestPublisher_ThumbnailRules(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ok.png"), append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, 1, 2), 0o644)
	os.WriteFile(filepath.Join(dir, "big.jpg"), append([]byte{0xFF, 0xD8, 0xFF}, make([]byte, bluesky.MaxThumbBytes)...), 0o644)
	os.WriteFile(filepath.Join(dir, "x.gif"), []byte("GIF89a...."), 0o644)
	os.Mkdir(filepath.Join(dir, "dir.png"), 0o755)
	pub := NewPublisher(PublisherDeps{MediaDir: dir})
	str := func(s string) *string { return &s }
	cases := []struct {
		name  string
		cover *string
		mime  string
	}{
		{"png ok", str("/media/ok.png"), "image/png"},
		{"too big", str("/media/big.jpg"), ""},
		{"gif refused", str("/media/x.gif"), ""},
		{"missing", str("/media/nope.jpg"), ""},
		{"not a regular file", str("/media/dir.png"), ""},
		{"absolute url never fetched", str("https://example.test/a.jpg"), ""},
		{"non-media path", str("/assets/ok.png"), ""},
		{"traversal flattened", str("/media/" + strings.Repeat(string(rune(46)), 2) + "/etc/passwd"), ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		_, mime := pub.thumbnail(tc.cover)
		if mime != tc.mime {
			t.Errorf("%s: mime = %q, want %q", tc.name, mime, tc.mime)
		}
	}
	if _, mime := NewPublisher(PublisherDeps{}).thumbnail(str("/media/ok.png")); mime != "" {
		t.Error("no MEDIA_DIR means no thumbnail")
	}
}

// MediaDirFn is read on every thumbnail, so a directory that appears later is
// picked up without rebuilding the publisher.
func TestPublisher_MediaDirFnIsLive(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ok.png"), append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, 1, 2), 0o644)
	current := ""
	pub := NewPublisher(PublisherDeps{MediaDirFn: func() string { return current }})
	cover := "/media/ok.png"
	if _, mime := pub.thumbnail(&cover); mime != "" {
		t.Errorf("empty dir: mime = %q", mime)
	}
	current = dir
	if _, mime := pub.thumbnail(&cover); mime != "image/png" {
		t.Errorf("after switch: mime = %q", mime)
	}
}

func TestPublisher_BlueskyExistingRecordNotRecreated(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	bsky := newFakeBsky(t)
	pub := newTestPublisher(t, s, bsky, nil, "")
	_, post := seedSendable(t, s)
	bsky.records[bluesky.TID(post.CreatedAt.UnixMicro(), uint16(post.ID&1023))] = true
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "posted" || bsky.createCount() != 0 {
		t.Errorf("status=%q createRecord=%d; an earlier attempt's record must be adopted, not duplicated", got.Status, bsky.createCount())
	}
}

func TestPublisher_MastodonSuccessUsesPerPostIdempotencyKey(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	masto := newFakeMasto(t)
	pub := newTestPublisher(t, s, nil, masto, "")
	_, post := seedMastodon(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "posted" || got.RemoteID == nil || *got.RemoteID != "9001" || got.RemoteURL == nil || *got.RemoteURL != "https://social.example.test/@alice/9001" {
		t.Errorf("post = %+v", got)
	}
	if want := fmt.Sprintf("opencircuitsf-outreach-post-%d", post.ID); len(masto.keys) != 1 || masto.keys[0] != want {
		t.Errorf("idempotency keys = %v, want [%s]", masto.keys, want)
	}
}

func TestPublisher_TransientFailureRetriesWithBackoff(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	masto := newFakeMasto(t)
	masto.setStatus(503)
	pub := newTestPublisher(t, s, nil, masto, "")
	_, post := seedMastodon(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "queued" || got.LastError == nil || !strings.Contains(*got.LastError, "retrying") {
		t.Fatalf("post = %+v", got)
	}
	if strings.Contains(*got.LastError, fakeMastoToken) {
		t.Error("last_error leaks the token")
	}
	st, attempts, _ := queueState(t, s, post.ID)
	if st != "queued" || attempts != 1 {
		t.Errorf("queue row status=%q attempts=%d", st, attempts)
	}
	var future bool
	if err := s.pool.QueryRow(ctx, `SELECT next_attempt_at > now() + interval '30 seconds' FROM outbound_queue WHERE recipient = $1`,
		fmt.Sprintf("outreach_post:%d", post.ID)).Scan(&future); err != nil || !future {
		t.Errorf("backoff not applied: %v %v", future, err)
	}
	// Not due yet: a second pass sends nothing.
	runPass(t, pub)
	if masto.requests() != 1 {
		t.Errorf("requests = %d, want 1", masto.requests())
	}
}

func TestPublisher_TransientAtFinalAttemptFails(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	masto := newFakeMasto(t)
	masto.setStatus(503)
	pub := newTestPublisher(t, s, nil, masto, "")
	_, post := seedMastodon(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	// The claim increments attempts, so 7 -> 8 == outbox.DefaultMaxRetries.
	if _, err := s.pool.Exec(ctx, `UPDATE outbound_queue SET attempts = $2 WHERE recipient = $1`,
		fmt.Sprintf("outreach_post:%d", post.ID), outbox.DefaultMaxRetries-1); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "failed" || got.LastError == nil {
		t.Fatalf("post = %+v", got)
	}
	if st, _, _ := queueState(t, s, post.ID); st != "abandoned" {
		t.Errorf("queue row status = %q", st)
	}
}

func TestPublisher_PermanentFailureFailsOnFirstAttempt(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	masto := newFakeMasto(t)
	masto.setStatus(422)
	pub := newTestPublisher(t, s, nil, masto, "")
	_, post := seedMastodon(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "failed" || got.LastError == nil || !strings.Contains(*got.LastError, "rejected") {
		t.Fatalf("post = %+v", got)
	}
	if st, attempts, _ := queueState(t, s, post.ID); st != "abandoned" || attempts != 1 {
		t.Errorf("queue row status=%q attempts=%d", st, attempts)
	}
}

func TestPublisher_RetryAfterFailurePostsAgain(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	masto := newFakeMasto(t)
	masto.setStatus(422)
	pub := newTestPublisher(t, s, nil, masto, "")
	_, post := seedMastodon(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	if got, _ := s.GetPost(ctx, post.ID); got.Status != "failed" {
		t.Fatalf("status = %q", got.Status)
	}
	masto.setStatus(0)
	_, prev, err := s.QueueNow(ctx, post.ID)
	if err != nil || prev != "failed" {
		t.Fatalf("retry QueueNow: prev=%q err=%v", prev, err)
	}
	runPass(t, pub)
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "posted" || got.LastError != nil {
		t.Errorf("after retry: %+v", got)
	}
}

func TestPublisher_WorkshopCanceledAfterQueueingSendsNothing(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	masto := newFakeMasto(t)
	pub := newTestPublisher(t, s, nil, masto, "")
	o, post := seedMastodon(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	setWorkshopStatus(t, s.pool, o.ID, "canceled")
	runPass(t, pub)
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "draft" || got.LastError == nil || !strings.Contains(*got.LastError, "canceled") {
		t.Fatalf("post = %+v", got)
	}
	if st, _, _ := queueState(t, s, post.ID); st != "skipped" {
		t.Errorf("queue row status = %q", st)
	}
	if masto.requests() != 0 {
		t.Errorf("%d requests were sent for a canceled workshop", masto.requests())
	}
}

func TestPublisher_UnconfiguredChannelFailsPermanently(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	pub := newTestPublisher(t, s, nil, nil, "") // neither client
	_, post := seedMastodon(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "failed" || got.LastError == nil || !strings.Contains(*got.LastError, "not configured") {
		t.Errorf("post = %+v", got)
	}
	if pub.ChannelConfigured("bluesky") || pub.ChannelConfigured("mastodon") || pub.ChannelConfigured("instagram") {
		t.Error("no channel should report configured")
	}
	var nilPub *Publisher
	if nilPub.ChannelConfigured("bluesky") {
		t.Error("a nil publisher reports nothing configured")
	}
}

func TestPublisher_ScheduledPostIsPromotedAndSent(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	masto := newFakeMasto(t)
	pub := newTestPublisher(t, s, nil, masto, "")
	_, post := seedMastodon(t, s)
	if _, err := s.Schedule(ctx, post.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	if masto.requests() != 0 {
		t.Fatal("a post scheduled for later was sent early")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE outreach_posts SET scheduled_at = now() - interval '1 second' WHERE id = $1`, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "posted" || masto.requests() != 1 {
		t.Errorf("status=%q requests=%d", got.Status, masto.requests())
	}
}

func TestPublisher_OverLimitBodyFailsBeforeSending(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	masto := newFakeMasto(t)
	pub := newTestPublisher(t, s, nil, masto, "")
	_, post := seedMastodon(t, s)
	if _, err := s.UpdatePostBody(ctx, post.ID, strings.Repeat("x", 501), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "failed" || masto.requests() != 0 {
		t.Errorf("status=%q requests=%d", got.Status, masto.requests())
	}
}

func TestPublisher_RunAndStopLifecycle(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	masto := newFakeMasto(t)
	pub := newTestPublisher(t, s, nil, masto, "")
	// Every started publisher is stopped: a leaked poller claims other
	// tests' rows (#0470).
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); pub.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		stopCtx, stopCancel := context.WithTimeout(ctx, 5*time.Second)
		defer stopCancel()
		if err := pub.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
		<-done
	})
	_, post := seedMastodon(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	// The first pass may already have run; poll interval is an hour, so the
	// post is only sent if a pass happens after QueueNow. Drive one directly.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runPass(t, pub)
		if got, _ := s.GetPost(ctx, post.ID); got.Status == "posted" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got, _ := s.GetPost(ctx, post.ID); got.Status != "posted" {
		t.Errorf("status = %q", got.Status)
	}
}

func TestPublisher_StopWithoutRun(t *testing.T) {
	pub := NewPublisher(PublisherDeps{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pub.Stop(ctx); err != nil {
		t.Errorf("Stop on a never-started publisher: %v", err)
	}
	if err := pub.Stop(ctx); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

// forgetfulMasto is a Mastodon fake that, like an instance an hour later,
// has forgotten every Idempotency-Key: each POST creates a new status. The
// first POST creates the status but answers 503, as a timeout after the
// write would look.
type forgetfulMasto struct {
	srv      *httptest.Server
	mu       sync.Mutex
	posts    int
	statuses []string // html contents, newest first
}

func newForgetfulMasto(t *testing.T) *forgetfulMasto {
	t.Helper()
	f := &forgetfulMasto{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodPost:
			f.posts++
			var in struct {
				Status string `json:"status"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.statuses = append([]string{in.Status}, f.statuses...)
			if f.posts == 1 {
				w.WriteHeader(503)
				io.WriteString(w, `{"error":"upstream timeout"}`)
				return
			}
			fmt.Fprintf(w, `{"id":"%d","url":"https://social.example.test/@alice/%d","uri":"u"}`, 100+f.posts, 100+f.posts)
		case r.URL.Path == "/api/v1/accounts/verify_credentials":
			io.WriteString(w, `{"id":"5"}`)
		case r.URL.Path == "/api/v1/accounts/5/statuses":
			var parts []string
			for i, c := range f.statuses {
				b, _ := json.Marshal(map[string]string{"id": fmt.Sprint(900 - i), "url": fmt.Sprintf("https://social.example.test/@alice/%d", 900-i), "uri": "u", "content": "<p>" + c + "</p>"})
				parts = append(parts, string(b))
			}
			io.WriteString(w, "["+strings.Join(parts, ",")+"]")
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *forgetfulMasto) postCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.posts }

func TestPublisher_MastodonRetryAdoptsStatusWhenInstanceForgotKey(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	fm := newForgetfulMasto(t)
	mc, err := mastodon.New(fm.srv.URL, fakeMastoToken)
	if err != nil {
		t.Fatal(err)
	}
	pub := NewPublisher(PublisherDeps{Store: s, Outbox: outbox.NewStore(s.pool), Settings: fakeSettings{}, Mastodon: mc, PollInterval: time.Hour})
	_, post := seedMastodon(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub) // posts, but answers 503: outcome unknown
	if got, _ := s.GetPost(ctx, post.ID); got.Status != "queued" {
		t.Fatalf("after the 503: %+v", got)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE outbound_queue SET next_attempt_at = now() WHERE recipient = $1`, fmt.Sprintf("outreach_post:%d", post.ID)); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub) // must adopt, not post again
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "posted" || got.RemoteID == nil || *got.RemoteID != "900" {
		t.Errorf("post = %+v, want posted and adopting the existing status 900", got)
	}
	if n := fm.postCount(); n != 1 {
		t.Errorf("POSTs = %d, want 1: the retry must adopt the earlier status", n)
	}
}

func TestPublisher_MastodonManualRetryAfterAbandonAdopts(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	fm := newForgetfulMasto(t)
	mc, err := mastodon.New(fm.srv.URL, fakeMastoToken)
	if err != nil {
		t.Fatal(err)
	}
	pub := NewPublisher(PublisherDeps{Store: s, Outbox: outbox.NewStore(s.pool), Settings: fakeSettings{}, Mastodon: mc, PollInterval: time.Hour})
	_, post := seedMastodon(t, s)
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub) // posts, but answers 503: outcome unknown
	rcpt := fmt.Sprintf("outreach_post:%d", post.ID)
	if _, err := s.pool.Exec(ctx, `UPDATE outbound_queue SET status = 'abandoned' WHERE recipient = $1`, rcpt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE outreach_posts SET status = 'failed' WHERE id = $1`, post.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil { // the manual Retry
		t.Fatal(err)
	}
	runPass(t, pub)
	got, _ := s.GetPost(ctx, post.ID)
	if got.Status != "posted" || got.RemoteID == nil || *got.RemoteID != "900" {
		t.Errorf("post = %+v, want posted and adopting the existing status 900", got)
	}
	if n := fm.postCount(); n != 1 {
		t.Errorf("POSTs = %d, want 1: the manual Retry must adopt, not post again", n)
	}
}

func TestPublisher_MastodonFirstAttemptDoesNotLookFirst(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	fm := newForgetfulMasto(t)
	// A status already carrying the link must not be adopted on attempt 1.
	_, post := seedMastodon(t, s)
	fm.statuses = []string{"older " + *post.ShortURL}
	fm.posts = 1 // so this POST succeeds
	mc, _ := mastodon.New(fm.srv.URL, fakeMastoToken)
	pub := NewPublisher(PublisherDeps{Store: s, Outbox: outbox.NewStore(s.pool), Settings: fakeSettings{}, Mastodon: mc, PollInterval: time.Hour})
	if _, _, err := s.QueueNow(ctx, post.ID); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	if got, _ := s.GetPost(ctx, post.ID); got.Status != "posted" || got.RemoteID == nil || *got.RemoteID != "102" {
		t.Errorf("post = %+v, want a fresh post (102)", got)
	}
}

// mutableClients is a ClientSource whose Mastodon client a test can swap.
type mutableClients struct {
	mu sync.Mutex
	bs *bluesky.Client
	ma *mastodon.Client
}

func (m *mutableClients) Bluesky() *bluesky.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bs
}

func (m *mutableClients) Mastodon() *mastodon.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ma
}

func TestPublisher_ChannelConfiguredFollowsClientSource(t *testing.T) {
	src := &mutableClients{}
	p := NewPublisher(PublisherDeps{Clients: src})
	if p.ChannelConfigured("mastodon") {
		t.Fatal("mastodon must not be configured while the source has no client")
	}
	fm := newFakeMasto(t)
	mc, err := mastodon.New(fm.srv.URL, fakeMastoToken)
	if err != nil {
		t.Fatal(err)
	}
	src.mu.Lock()
	src.ma = mc
	src.mu.Unlock()
	if !p.ChannelConfigured("mastodon") {
		t.Fatal("mastodon must be configured once the source holds a client")
	}
	if p.ChannelConfigured("bluesky") {
		t.Error("bluesky must stay unconfigured")
	}
}
