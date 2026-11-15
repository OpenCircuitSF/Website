package guideposts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

// Everything here talks to httptest fakes; nothing reaches a real platform.
// Credentials are obviously fake fixtures.

const (
	fakeBskyPassword = "fake-app-password-xyz"
	fakeMastoToken   = "fake-mastodon-token-abc"
	fakeDID          = "did:plc:fakefakefake"
)

type fakeBsky struct {
	srv     *httptest.Server
	mu      sync.Mutex
	paths   []string
	created []string
	failAll int
}

func newFakeBsky(t *testing.T) *fakeBsky {
	t.Helper()
	f := &fakeBsky{}
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
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"RecordNotFound","message":"not found"}`)
		case "/xrpc/com.atproto.repo.createRecord":
			if f.failAll != 0 {
				w.WriteHeader(f.failAll)
				io.WriteString(w, `{"error":"InternalServerError","message":"nope"}`)
				return
			}
			var in struct{ RKey string }
			json.Unmarshal(body, &in)
			f.created = append(f.created, string(body))
			io.WriteString(w, `{"uri":"at://`+fakeDID+`/app.bsky.feed.post/`+in.RKey+`","cid":"fakecid"}`)
		case "/xrpc/com.atproto.repo.uploadBlob":
			io.WriteString(w, `{"blob":{"$type":"blob","ref":{"$link":"fakeblob"},"mimeType":"image/png","size":4}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
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

// fakeMasto answers POST /api/v1/statuses, recording each Idempotency-Key,
// and serves statuses (html contents, newest first) for the recent-status
// look. failFirst makes the first POST create the status but answer 503.
type fakeMasto struct {
	srv       *httptest.Server
	mu        sync.Mutex
	posts     int
	keys      []string
	statuses  []string
	failFirst bool
	status    int // when non-zero, every POST answers it
}

func newFakeMasto(t *testing.T) *fakeMasto {
	t.Helper()
	f := &fakeMasto{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodPost:
			f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
			if f.status != 0 {
				w.WriteHeader(f.status)
				io.WriteString(w, `{"error":"fake failure"}`)
				return
			}
			f.posts++
			var in struct {
				Status string `json:"status"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.statuses = append([]string{in.Status}, f.statuses...)
			if f.failFirst && f.posts == 1 {
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

func (f *fakeMasto) postCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.posts }

type fakeSettings struct{}

func (fakeSettings) GetSetting(context.Context, string) (string, error) {
	return "", errors.New("no such setting")
}

type clients struct {
	bs *bluesky.Client
	ma *mastodon.Client
}

func (c clients) Bluesky() *bluesky.Client   { return c.bs }
func (c clients) Mastodon() *mastodon.Client { return c.ma }

var tinyPNG = []byte("\x89PNG\r\n\x1a\nfake")

func newTestPublisher(t *testing.T, s *Store, bsky *fakeBsky, masto *fakeMasto, card func(string) ([]byte, string, error)) *Publisher {
	t.Helper()
	var c clients
	var err error
	if bsky != nil {
		if c.bs, err = bluesky.New(bsky.srv.URL, "alice.example.test", fakeBskyPassword); err != nil {
			t.Fatal(err)
		}
	}
	if masto != nil {
		if c.ma, err = mastodon.New(masto.srv.URL, fakeMastoToken); err != nil {
			t.Fatal(err)
		}
	}
	return NewPublisher(PublisherDeps{Store: s, Outbox: s.outbox, Settings: fakeSettings{}, Clients: c, CardFn: card, PollInterval: time.Hour})
}

func runPass(t *testing.T, p *Publisher) {
	t.Helper()
	if _, err := p.pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
}

func queued(t *testing.T, s *Store, channel string) Post {
	t.Helper()
	p := linked(t, s, channel)
	p, _, err := s.QueueNow(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func queueState(t *testing.T, s *Store, id int64) (status string, attempts int) {
	t.Helper()
	if err := s.pool.QueryRow(context.Background(),
		`SELECT status, attempts FROM outbound_queue WHERE recipient = $1 ORDER BY id DESC LIMIT 1`, Recipient(id)).Scan(&status, &attempts); err != nil {
		t.Fatalf("queue row for %d: %v", id, err)
	}
	return
}

func TestPublisher_PostsBlueskyWithShortLinkCardAndThumb(t *testing.T) {
	s := newTestStore(t)
	bsky := newFakeBsky(t)
	var asked string
	pub := newTestPublisher(t, s, bsky, nil, func(slug string) ([]byte, string, error) { asked = slug; return tinyPNG, "image/png", nil })
	p := queued(t, s, "bluesky")
	runPass(t, pub)
	got, _ := s.Get(context.Background(), p.ID)
	if got.Status != "posted" || got.RemoteID == nil {
		t.Fatalf("post = %+v", got)
	}
	if len(bsky.created) != 1 || asked != "baud-rate" || bsky.hits("/xrpc/com.atproto.repo.uploadBlob") != 1 {
		t.Fatalf("created %d, card for %q, uploads %d", len(bsky.created), asked, bsky.hits("/xrpc/com.atproto.repo.uploadBlob"))
	}
	var rec struct {
		Record struct {
			Text  string `json:"text"`
			Embed struct {
				External struct {
					URI   string `json:"uri"`
					Title string `json:"title"`
					Thumb any    `json:"thumb"`
				} `json:"external"`
			} `json:"embed"`
		} `json:"record"`
	}
	if err := json.Unmarshal([]byte(bsky.created[0]), &rec); err != nil {
		t.Fatal(err)
	}
	ext := rec.Record.Embed.External
	if ext.URI != *p.ShortURL || !strings.Contains(rec.Record.Text, *p.ShortURL) || ext.Thumb == nil || ext.Title == "" {
		t.Errorf("record = %+v", rec.Record)
	}
	if st, _ := queueState(t, s, p.ID); st != "sent" {
		t.Errorf("queue row %q", st)
	}
}

func TestPublisher_BlueskyThumbFailurePostsWithoutThumb(t *testing.T) {
	for name, card := range map[string]func(string) ([]byte, string, error){
		"error":     func(string) ([]byte, string, error) { return nil, "", errors.New("render failed") },
		"too large": func(string) ([]byte, string, error) { return make([]byte, bluesky.MaxThumbBytes+1), "image/png", nil },
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			bsky := newFakeBsky(t)
			pub := newTestPublisher(t, s, bsky, nil, card)
			p := queued(t, s, "bluesky")
			runPass(t, pub)
			got, _ := s.Get(context.Background(), p.ID)
			if got.Status != "posted" || bsky.hits("/xrpc/com.atproto.repo.uploadBlob") != 0 {
				t.Errorf("post = %+v, uploads %d", got, bsky.hits("/xrpc/com.atproto.repo.uploadBlob"))
			}
		})
	}
}

func TestPublisher_PostsMastodonWithGuideIdempotencyKey(t *testing.T) {
	s := newTestStore(t)
	masto := newFakeMasto(t)
	pub := newTestPublisher(t, s, nil, masto, nil)
	p := queued(t, s, "mastodon")
	runPass(t, pub)
	got, _ := s.Get(context.Background(), p.ID)
	if got.Status != "posted" || got.RemoteID == nil || *got.RemoteID != "101" {
		t.Fatalf("post = %+v", got)
	}
	if len(masto.keys) != 1 || masto.keys[0] != MastodonIdempotencyKey(p.ID, 0) {
		t.Errorf("keys = %v", masto.keys)
	}
}

func TestPublisher_MastodonRetryAdoptsStatusWithOwnShortURL(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	masto := newFakeMasto(t)
	masto.failFirst = true
	pub := newTestPublisher(t, s, nil, masto, nil)
	p := queued(t, s, "mastodon")
	runPass(t, pub) // posts, but answers 503: outcome unknown
	if got, _ := s.Get(ctx, p.ID); got.Status != "queued" {
		t.Fatalf("after the 503: %+v", got)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE outbound_queue SET next_attempt_at = now() WHERE recipient = $1`, Recipient(p.ID)); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	got, _ := s.Get(ctx, p.ID)
	if got.Status != "posted" || got.RemoteID == nil || *got.RemoteID != "900" || masto.postCount() != 1 {
		t.Errorf("post = %+v, POSTs %d; want the earlier status 900 adopted", got, masto.postCount())
	}
}

func TestPublisher_MastodonRetryIgnoresEarlierPostOfSameGuide(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	masto := newFakeMasto(t)
	pub := newTestPublisher(t, s, nil, masto, nil)
	older := linked(t, s, "mastodon") // same guide and channel, its own link
	masto.statuses = []string{older.Body}
	p := queued(t, s, "mastodon")
	if _, err := s.pool.Exec(ctx, `UPDATE outbound_queue SET attempts = 1 WHERE recipient = $1`, Recipient(p.ID)); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub) // attempt 2: looks first, finds only the older post
	got, _ := s.Get(ctx, p.ID)
	if got.Status != "posted" || masto.postCount() != 1 || *got.RemoteID == "900" {
		t.Errorf("post = %+v, POSTs %d; want a new status", got, masto.postCount())
	}
}

func TestPublisher_NoShortLinkFailsPermanently(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// The database refuses a queued post without a link outright...
	d := create(t, s, "baud-rate", "mastodon", "", "")
	if _, err := s.pool.Exec(ctx, `UPDATE guide_posts SET status = 'queued' WHERE id = $1`, d.ID); err == nil {
		t.Fatal("guide_posts_link_check allowed a queued post without a link")
	}
	// ...and the publisher's own guard refuses one anyway.
	ch := mustChannel(t, "mastodon")
	if msg := sendRefusal(ch, Post{Body: "text"}); msg != "Cannot post: the post has no short link" {
		t.Errorf("refusal = %q", msg)
	}
	url := "https://go.example.test/u/abc"
	if msg := sendRefusal(ch, Post{Body: "text " + url, ShortURL: &url}); msg != "" {
		t.Errorf("a linked post was refused: %q", msg)
	}
}

func TestPublisher_UnconfiguredChannelFailsPermanently(t *testing.T) {
	s := newTestStore(t)
	pub := newTestPublisher(t, s, nil, nil, nil)
	p := queued(t, s, "bluesky")
	runPass(t, pub)
	got, _ := s.Get(context.Background(), p.ID)
	if got.Status != "failed" || got.LastError == nil || !strings.Contains(*got.LastError, "not configured") {
		t.Errorf("post = %+v", got)
	}
	if st, _ := queueState(t, s, p.ID); st != "abandoned" {
		t.Errorf("queue row %q", st)
	}
}

func TestPublisher_TransientErrorRetriesThenAbandons(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	masto := newFakeMasto(t)
	masto.status = 503
	pub := newTestPublisher(t, s, nil, masto, nil)
	p := queued(t, s, "mastodon")
	runPass(t, pub)
	got, _ := s.Get(ctx, p.ID)
	if st, n := queueState(t, s, p.ID); got.Status != "queued" || st != "queued" || n != 1 || got.LastError == nil {
		t.Fatalf("after one failure: post %+v, queue %q/%d", got, st, n)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE outbound_queue SET attempts = $2, next_attempt_at = now() WHERE recipient = $1`,
		Recipient(p.ID), outbox.DefaultMaxRetries-1); err != nil {
		t.Fatal(err)
	}
	runPass(t, pub)
	got, _ = s.Get(ctx, p.ID)
	if st, _ := queueState(t, s, p.ID); got.Status != "failed" || st != "abandoned" {
		t.Errorf("after the last attempt: post %+v, queue %q", got, st)
	}
}

func TestPublisher_ClaimsOnlyGuidePostKind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	stamp := time.Now().UnixNano()
	var ids []int64
	for _, it := range []outbox.Item{
		{Kind: outbox.KindOutreachPost, Recipient: fmt.Sprintf("outreach_post:test-%d", stamp), Payload: map[string]int64{"post_id": 1}},
		{Kind: outbox.KindConfirmation, Recipient: fmt.Sprintf("guideposts-%d@example.test", stamp)},
	} {
		id, err := s.outbox.Enqueue(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE id = ANY($1)`, ids) })
	masto := newFakeMasto(t)
	pub := newTestPublisher(t, s, nil, masto, nil)
	p := queued(t, s, "mastodon")
	runPass(t, pub)
	if got, _ := s.Get(ctx, p.ID); got.Status != "posted" {
		t.Fatalf("guide post = %+v", got)
	}
	var untouched int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outbound_queue WHERE id = ANY($1) AND status = 'queued' AND attempts = 0`, ids).Scan(&untouched); err != nil {
		t.Fatal(err)
	}
	if untouched != 2 {
		t.Errorf("%d of 2 foreign rows untouched", untouched)
	}
}
