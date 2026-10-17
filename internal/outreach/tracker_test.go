package outreach

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
)

// Everything here talks to httptest fakes. No Bluesky or Mastodon credential
// exists for this project, and nothing in this file may reach a real
// platform (#0547). Hosts, handles and DIDs are obviously fake fixtures.

// ---- pure mapping ----

func TestBskyMention(t *testing.T) {
	ours := map[string]int64{"at://ours/1": 7, "at://ours/root": 8, "at://ours/q": 9}
	base := bluesky.Notification{URI: "at://did:plc:testtesttesttest/app.bsky.feed.post/3kabc", IndexedAt: "2026-10-16T10:00:00.000Z",
		AuthorHandle: "alice.example.test", Text: "hello", CreatedAt: "2026-10-16T09:59:00.000Z"}
	with := func(f func(n *bluesky.Notification)) bluesky.Notification { n := base; f(&n); return n }
	id := func(p *int64) int64 {
		if p == nil {
			return -1
		}
		return *p
	}
	for _, tc := range []struct {
		name string
		n    bluesky.Notification
		kind string
		post int64
	}{
		{"reply to our post", with(func(n *bluesky.Notification) { n.Reason, n.ReplyParent = "reply", "at://ours/1" }), "reply", 7},
		{"deeper reply falls back to root", with(func(n *bluesky.Notification) {
			n.Reason, n.ReplyParent, n.ReplyRoot = "reply", "at://theirs/2", "at://ours/root"
		}), "reply", 8},
		{"reply falls back to reasonSubject", with(func(n *bluesky.Notification) { n.Reason, n.ReasonSubject = "reply", "at://ours/1" }), "reply", 7},
		{"reply to someone else", with(func(n *bluesky.Notification) { n.Reason, n.ReplyParent = "reply", "at://theirs/1" }), "reply", -1},
		{"quote of our post", with(func(n *bluesky.Notification) { n.Reason, n.QuoteURI = "quote", "at://ours/q" }), "quote", 9},
		{"plain mention is never tied", with(func(n *bluesky.Notification) { n.Reason, n.ReasonSubject = "mention", "at://ours/1" }), "mention", -1},
	} {
		m, ok := bskyMention(tc.n, ours)
		if !ok || m.Kind != tc.kind || id(m.PostID) != tc.post {
			t.Errorf("%s: ok=%v kind=%q post=%d", tc.name, ok, m.Kind, id(m.PostID))
		}
	}
	m, _ := bskyMention(with(func(n *bluesky.Notification) { n.Reason = "mention" }), ours)
	if m.Author != "@alice.example.test" || m.Channel != "bluesky" || m.RemoteID != base.URI || m.Text != "hello" ||
		m.URL == nil || *m.URL != "https://bsky.app/profile/alice.example.test/post/3kabc" || !m.CreatedAt.Equal(time.Date(2026, 10, 16, 9, 59, 0, 0, time.UTC)) {
		t.Errorf("fields = %+v", m)
	}
	if m, _ := bskyMention(with(func(n *bluesky.Notification) { n.Reason, n.CreatedAt = "mention", "garbage" }), ours); !m.CreatedAt.Equal(time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("createdAt must fall back to indexedAt: %v", m.CreatedAt)
	}
	if _, ok := bskyMention(with(func(n *bluesky.Notification) { n.Reason = "like" }), ours); ok {
		t.Error("an unknown reason was kept")
	}
	if _, ok := bskyMention(with(func(n *bluesky.Notification) { n.Reason, n.URI = "mention", "" }), ours); ok {
		t.Error("a notification with no uri was kept")
	}
	nul := with(func(n *bluesky.Notification) {
		n.Reason, n.Text, n.AuthorHandle = "mention", "he\x00llo\x00", "al\x00ice.example.test"
	})
	if m, ok := bskyMention(nul, ours); !ok || m.Text != "hello" || m.Author != "@alice.example.test" || strings.Contains(m.RemoteID, "\x00") {
		t.Errorf("NUL not stripped: ok=%v %+v", ok, m)
	}
	long := with(func(n *bluesky.Notification) { n.Reason, n.Text = "mention", strings.Repeat("é", 1500) })
	if m, _ := bskyMention(long, ours); len([]rune(m.Text)) != 1000 {
		t.Errorf("text not capped at 1000 runes: %d", len([]rune(m.Text)))
	}
}

func TestMastoMention(t *testing.T) {
	ours := map[string]int64{"111": 5}
	base := mastodon.Notification{ID: "30", Type: "mention", CreatedAt: "2026-10-16T10:00:00.000Z", Acct: "bob@other.example.test", HasStatus: true,
		StatusID: "900", StatusURI: "https://other.example.test/users/bob/statuses/900", StatusURL: "https://other.example.test/@bob/900",
		StatusCreatedAt: "2026-10-16T09:59:00.000Z", ContentHTML: "<p>hi &amp; bye</p>"}
	with := func(f func(n *mastodon.Notification)) mastodon.Notification { n := base; f(&n); return n }
	id := func(p *int64) int64 {
		if p == nil {
			return -1
		}
		return *p
	}
	for _, tc := range []struct {
		name string
		n    mastodon.Notification
		kind string
		post int64
	}{
		{"mention that replies to ours is a reply", with(func(n *mastodon.Notification) { n.InReplyToID = "111" }), "reply", 5},
		{"plain mention", base, "mention", -1},
		{"mention replying to someone else", with(func(n *mastodon.Notification) { n.InReplyToID = "222" }), "mention", -1},
		{"quote of ours", with(func(n *mastodon.Notification) { n.Type, n.QuotedStatusID = "quote", "111" }), "quote", 5},
		{"quote with no quoted status (pending or revoked)", with(func(n *mastodon.Notification) { n.Type = "quote" }), "quote", -1},
	} {
		m, ok := mastoMention(tc.n, ours, time.Time{})
		if !ok || m.Kind != tc.kind || id(m.PostID) != tc.post {
			t.Errorf("%s: ok=%v kind=%q post=%d", tc.name, ok, m.Kind, id(m.PostID))
		}
	}
	m, _ := mastoMention(base, ours, time.Time{})
	if m.RemoteID != base.StatusURI || m.Author != "@bob@other.example.test" || m.Text != "hi & bye" || m.URL == nil || *m.URL != base.StatusURL {
		t.Errorf("fields = %+v", m)
	}
	if m, _ := mastoMention(with(func(n *mastodon.Notification) { n.StatusURL = "javascript:alert(1)" }), ours, time.Time{}); m.URL != nil {
		t.Errorf("a non-https url was kept: %q", *m.URL)
	}
	if m, _ := mastoMention(with(func(n *mastodon.Notification) { n.StatusURL = "http://other.example.test/x" }), ours, time.Time{}); m.URL != nil {
		t.Errorf("an http url was kept: %q", *m.URL)
	}
	if _, ok := mastoMention(with(func(n *mastodon.Notification) { n.HasStatus = false }), ours, time.Time{}); ok {
		t.Error("a notification without a status was kept")
	}
	if _, ok := mastoMention(with(func(n *mastodon.Notification) { n.Type = "favourite" }), ours, time.Time{}); ok {
		t.Error("an unknown type was kept")
	}
	floor := time.Date(2026, 10, 16, 9, 59, 30, 0, time.UTC)
	if _, ok := mastoMention(base, ours, floor); ok {
		t.Error("a status older than the floor was kept")
	}
}

// ---- fakes ----

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func captureLog() (*slog.Logger, *syncBuf) {
	b := &syncBuf{}
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug})), b
}

// trackerBsky is one server playing both the PDS and the AppView.
type trackerBsky struct {
	srv *httptest.Server

	mu         sync.Mutex
	pages      map[string]string // listNotifications body by cursor
	notifCode  int               // forced listNotifications status
	notifHits  []string          // cursor of each listNotifications call
	counts     map[string][4]int // getPosts counts by uri; absent = deleted
	postsHits  [][]string        // uris of each getPosts call
	block      bool              // listNotifications and getPosts wait for the request to be cancelled
	blockEntry chan struct{}
}

func newTrackerBsky(t *testing.T) *trackerBsky {
	t.Helper()
	f := &trackerBsky{pages: map[string]string{}, counts: map[string][4]int{}, blockEntry: make(chan struct{}, 8)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		block := f.block
		f.mu.Unlock()
		if block && r.URL.Path != "/xrpc/com.atproto.server.createSession" {
			f.blockEntry <- struct{}{}
			<-r.Context().Done()
			return
		}
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			io.WriteString(w, `{"accessJwt":"fake-access","refreshJwt":"fake-refresh","did":"did:plc:testtesttesttest","handle":"alice.example.test"}`)
		case "/xrpc/app.bsky.notification.listNotifications":
			f.mu.Lock()
			f.notifHits = append(f.notifHits, r.URL.Query().Get("cursor"))
			code, body := f.notifCode, f.pages[r.URL.Query().Get("cursor")]
			f.mu.Unlock()
			if code != 0 {
				w.WriteHeader(code)
				io.WriteString(w, `{"error":"InternalServerError","message":"boom"}`)
				return
			}
			if body == "" {
				body = `{"notifications":[]}`
			}
			io.WriteString(w, body)
		case "/xrpc/app.bsky.feed.getPosts":
			uris := r.URL.Query()["uris"]
			f.mu.Lock()
			f.postsHits = append(f.postsHits, uris)
			var posts []map[string]any
			for _, u := range uris {
				if c, ok := f.counts[u]; ok {
					posts = append(posts, map[string]any{"uri": u, "likeCount": c[0], "repostCount": c[1], "replyCount": c[2], "quoteCount": c[3]})
				}
			}
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"posts": posts})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *trackerBsky) client(t *testing.T) *bluesky.Client {
	t.Helper()
	c, err := bluesky.New(f.srv.URL, "alice.example.test", "fake-app-password-xyz")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if err := c.SetAppViewURL(f.srv.URL); err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *trackerBsky) postsCalls() [][]string { f.mu.Lock(); defer f.mu.Unlock(); return f.postsHits }
func (f *trackerBsky) notifCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.notifHits...)
}

type trackerMasto struct {
	srv *httptest.Server

	mu         sync.Mutex
	reqs       []string          // path?query of every request
	notifs     map[string]string // body by min_id ("" = none)
	notifCode  int               // forced notifications status
	statuses   map[string]string // body by status id; absent = 404
	block      bool
	blockEntry chan struct{}
}

func newTrackerMasto(t *testing.T) *trackerMasto {
	t.Helper()
	f := &trackerMasto{notifs: map[string]string{}, statuses: map[string]string{}, blockEntry: make(chan struct{}, 8)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.URL.Path+"?"+r.URL.RawQuery)
		block := f.block
		f.mu.Unlock()
		if block {
			f.blockEntry <- struct{}{}
			<-r.Context().Done()
			return
		}
		switch {
		case r.URL.Path == "/api/v1/notifications":
			f.mu.Lock()
			code, body := f.notifCode, f.notifs[r.URL.Query().Get("min_id")]
			f.mu.Unlock()
			if code != 0 {
				w.WriteHeader(code)
				io.WriteString(w, `{"error":"The access token is invalid"}`)
				return
			}
			if body == "" {
				body = `[]`
			}
			io.WriteString(w, body)
		case strings.HasPrefix(r.URL.Path, "/api/v1/statuses/"):
			f.mu.Lock()
			body, ok := f.statuses[strings.TrimPrefix(r.URL.Path, "/api/v1/statuses/")]
			f.mu.Unlock()
			if !ok {
				w.WriteHeader(404)
				io.WriteString(w, `{"error":"Record not found"}`)
				return
			}
			io.WriteString(w, body)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *trackerMasto) client(t *testing.T) *mastodon.Client {
	t.Helper()
	c, err := mastodon.New(f.srv.URL, "fake-mastodon-token-abc")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	return c
}

func (f *trackerMasto) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

func (f *trackerMasto) matching(sub string) int {
	n := 0
	for _, r := range f.requests() {
		if strings.Contains(r, sub) {
			n++
		}
	}
	return n
}

// resetCursors clears both channels' stored positions before and after a
// test; they are keyed by channel name and shared by the whole database.
func resetCursors(t *testing.T, s *Store) {
	t.Helper()
	del := func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM outreach_mention_cursors WHERE channel IN ('bluesky','mastodon')`)
	}
	del()
	t.Cleanup(del)
}

func newTracker(t *testing.T, s *Store, b *bluesky.Client, m *mastodon.Client) (*Tracker, *syncBuf) {
	t.Helper()
	log, buf := captureLog()
	return NewTracker(TrackerDeps{Store: s, Bluesky: b, Mastodon: m, Now: time.Now, Log: log}), buf
}

func snapshots(t *testing.T, s *Store, postID int64) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM outreach_engagement WHERE post_id = $1`, postID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mentionKinds(t *testing.T, s *Store, tag string) map[string]string {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), `SELECT remote_id, kind || ':' || coalesce(post_id::text, '-') FROM outreach_mentions WHERE remote_id LIKE '%' || $1 || '%'`, tag)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		out[k] = v
	}
	return out
}

// ---- engagement ----

func TestTracker_EngagementCapture(t *testing.T) {
	s := NewStore(testPool(t))
	resetCursors(t, s)
	ctx := context.Background()
	tag := uniq()
	bf, mf := newTrackerBsky(t), newTrackerMasto(t)
	due := "at://did:plc:testtesttesttest/app.bsky.feed.post/due" + tag
	recent := "at://did:plc:testtesttesttest/app.bsky.feed.post/recent" + tag
	bf.counts[due] = [4]int{3, 1, 2, 0}
	bf.counts[recent] = [4]int{9, 9, 9, 9}
	_, pDue := seedPosted(t, s, "bluesky", due, time.Now().Add(-2*time.Hour))
	_, pRecent := seedPosted(t, s, "bluesky", recent, time.Now().Add(-3*time.Hour))
	_, pOld := seedPosted(t, s, "bluesky", "at://old"+tag, time.Now().Add(-31*24*time.Hour))
	_, pMasto := seedPosted(t, s, "mastodon", "5001"+tag, time.Now().Add(-2*time.Hour))
	if err := s.RecordEngagement(ctx, pRecent.ID, time.Now().Add(-10*time.Minute), Counts{Likes: 1}); err != nil {
		t.Fatal(err)
	}
	mf.statuses["5001"+tag] = `{"favourites_count":4,"reblogs_count":2,"replies_count":1,"quotes_count":3}`

	tr, logs := newTracker(t, s, bf.client(t), mf.client(t))
	tr.pass(ctx)

	got, err := s.LatestEngagement(ctx, []int64{pDue.ID, pRecent.ID, pOld.ID, pMasto.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got[pDue.ID].Counts != (Counts{3, 1, 2, 0}) {
		t.Errorf("bluesky snapshot = %+v", got[pDue.ID])
	}
	if got[pMasto.ID].Counts != (Counts{4, 2, 1, 3}) {
		t.Errorf("mastodon snapshot = %+v", got[pMasto.ID])
	}
	if n := snapshots(t, s, pRecent.ID); n != 1 || got[pRecent.ID].Likes != 1 {
		t.Errorf("a post captured 10 minutes ago gained a snapshot: %d rows", n)
	}
	if n := snapshots(t, s, pOld.ID); n != 0 {
		t.Errorf("a 31-day-old post got %d snapshots", n)
	}
	for _, call := range bf.postsCalls() {
		for _, u := range call {
			if strings.Contains(u, "at://old"+tag) || u == recent {
				t.Errorf("getPosts was asked for %q, which is not due", u)
			}
		}
	}
	if !strings.Contains(logs.String(), "snapshots_written=2") {
		t.Errorf("pass summary missing or wrong:\n%s", logs.String())
	}

	// An immediate second pass finds nothing due.
	tr.pass(ctx)
	if snapshots(t, s, pDue.ID) != 1 || snapshots(t, s, pMasto.ID) != 1 {
		t.Error("a second immediate pass captured again")
	}
}

func TestTracker_NilMastodonMakesNoMastodonRequests(t *testing.T) {
	s := NewStore(testPool(t))
	resetCursors(t, s)
	tag := uniq()
	bf, mf := newTrackerBsky(t), newTrackerMasto(t)
	seedPosted(t, s, "mastodon", "7001"+tag, time.Now().Add(-2*time.Hour))
	mf.statuses["7001"+tag] = `{}`
	tr, _ := newTracker(t, s, bf.client(t), nil)
	tr.pass(context.Background())
	if n := len(mf.requests()); n != 0 {
		t.Errorf("mastodon fake saw %d requests with no client configured", n)
	}
}

func TestTracker_GoneRemotePostLogsOnceAndWaits(t *testing.T) {
	s := NewStore(testPool(t))
	resetCursors(t, s)
	ctx := context.Background()
	tag := uniq()
	bf, mf := newTrackerBsky(t), newTrackerMasto(t)
	bsk := "at://did:plc:testtesttesttest/app.bsky.feed.post/gone" + tag
	_, pB := seedPosted(t, s, "bluesky", bsk, time.Now().Add(-2*time.Hour))
	_, pM := seedPosted(t, s, "mastodon", "8001"+tag, time.Now().Add(-2*time.Hour))
	tr, logs := newTracker(t, s, bf.client(t), mf.client(t))
	tr.pass(ctx)
	tr.pass(ctx)
	if calls := len(bf.postsCalls()); calls != 1 {
		t.Errorf("getPosts calls = %d, want 1: a gone post is retried at its next slot, not every pass", calls)
	}
	if n := mf.matching("/api/v1/statuses/8001" + tag); n != 1 {
		t.Errorf("status requests = %d, want 1", n)
	}
	if snapshots(t, s, pB.ID)+snapshots(t, s, pM.ID) != 0 {
		t.Error("a gone post wrote a snapshot")
	}
	if n := strings.Count(logs.String(), "remote post not found"); n != 2 {
		t.Errorf("not-found Info lines = %d, want 2 (one per attempt)\n%s", n, logs.String())
	}
}

// ---- mentions ----

func TestTracker_BlueskyMentionsAcrossPages(t *testing.T) {
	s := NewStore(testPool(t))
	resetCursors(t, s)
	ctx := context.Background()
	tag := uniq()
	cleanMentions(t, s.pool, tag)
	bf := newTrackerBsky(t)
	ours := "at://did:plc:testtesttesttest/app.bsky.feed.post/ours" + tag
	_, post := seedPosted(t, s, "bluesky", ours, time.Now().Add(-time.Hour))
	bf.counts[ours] = [4]int{}

	at := func(ago time.Duration) string { return time.Now().Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	reply := "at://did:plc:aaa" + tag + "/app.bsky.feed.post/r1"
	quote := "at://did:plc:bbb" + tag + "/app.bsky.feed.post/q1"
	plain := "at://did:plc:ccc" + tag + "/app.bsky.feed.post/m1"
	bf.pages[""] = fmt.Sprintf(`{"cursor":"p2","notifications":[
	 {"uri":%q,"reason":"reply","indexedAt":%q,"author":{"handle":"alice.example.test"},"record":{"text":"yes!","createdAt":%q,"reply":{"parent":{"uri":%q},"root":{"uri":%q}}}},
	 {"uri":%q,"reason":"quote","indexedAt":%q,"author":{"handle":"bob.example.test"},"record":{"text":"look","createdAt":%q,"embed":{"$type":"app.bsky.embed.recordWithMedia","record":{"record":{"uri":%q}}}}}]}`,
		reply, at(10*time.Minute), at(11*time.Minute), ours, ours,
		quote, at(20*time.Minute), at(21*time.Minute), ours)
	bf.pages["p2"] = fmt.Sprintf(`{"notifications":[
	 {"uri":%q,"reason":"mention","indexedAt":%q,"author":{"handle":"carol.example.test"},"record":{"text":"@us hello","createdAt":%q}}]}`,
		plain, at(30*time.Minute), at(31*time.Minute))

	tr, _ := newTracker(t, s, bf.client(t), nil)
	tr.pass(ctx)
	want := map[string]string{reply: fmt.Sprintf("reply:%d", post.ID), quote: fmt.Sprintf("quote:%d", post.ID), plain: "mention:-"}
	got := mentionKinds(t, s, tag)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("mention %q = %q, want %q", k, got[k], v)
		}
	}
	if calls := bf.notifCalls(); len(calls) != 2 || calls[0] != "" || calls[1] != "p2" {
		t.Errorf("pass 1 listNotifications cursors = %q", calls)
	}

	// Pass 2: the stored mark is the newest indexedAt, so the walk stops at
	// the first item older than it and never asks for page 2 again.
	tr.pass(ctx)
	if calls := bf.notifCalls(); len(calls) != 3 {
		t.Errorf("pass 2 listNotifications cursors = %q, want exactly one more call", calls)
	}
	if n := len(mentionKinds(t, s, tag)); n != 3 {
		t.Errorf("mentions after pass 2 = %d, want 3 (nothing new)", n)
	}
}

func TestTracker_BlueskyPageCapLogsWarn(t *testing.T) {
	s := NewStore(testPool(t))
	resetCursors(t, s)
	bf := newTrackerBsky(t)
	at := time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	tag := uniq()
	for p := 0; p < maxNotificationPages; p++ {
		var items []string
		for i := 0; i < bskyPage; i++ {
			items = append(items, fmt.Sprintf(`{"uri":"at://did:plc:cap%s/app.bsky.feed.post/p%di%d","reason":"like","indexedAt":%q}`, tag, p, i, at))
		}
		key := ""
		if p > 0 {
			key = fmt.Sprintf("p%d", p)
		}
		bf.pages[key] = fmt.Sprintf(`{"cursor":"p%d","notifications":[%s]}`, p+1, strings.Join(items, ","))
	}
	tr, buf := newTracker(t, s, bf.client(t), nil)
	tr.pass(context.Background())
	if n := len(bf.notifCalls()); n != maxNotificationPages {
		t.Errorf("listNotifications calls = %d, want %d", n, maxNotificationPages)
	}
	if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "page cap reached") || !strings.Contains(out, "channel=bluesky") {
		t.Errorf("no page-cap warning in log:\n%s", out)
	}
}

func TestTracker_MastodonPageCapLogsWarn(t *testing.T) {
	s := NewStore(testPool(t))
	resetCursors(t, s)
	ctx := context.Background()
	mf := newTrackerMasto(t)
	if err := s.SetMentionCursor(ctx, "mastodon", "1000"); err != nil {
		t.Fatal(err)
	}
	// Every page is full (80 items) so the walk never reaches a short page;
	// the items are favourites, which the tracker skips, so nothing is stored.
	// Chain: min_id -> page whose first id is the next min_id.
	minID := "1000"
	for p := 0; p < maxNotificationPages; p++ {
		first := 2000 + p*100 + mastodonPage
		var items []string
		for i := 0; i < mastodonPage; i++ {
			items = append(items, fmt.Sprintf(`{"id":"%d","type":"favourite","created_at":"2026-10-16T10:00:00.000Z","account":{"acct":"bob@other.example.test"}}`, first-i))
		}
		mf.notifs[minID] = "[" + strings.Join(items, ",") + "]"
		minID = fmt.Sprintf("%d", first)
	}
	tr, buf := newTracker(t, s, nil, mf.client(t))
	tr.pass(ctx)
	if n := mf.matching("/api/v1/notifications"); n != maxNotificationPages {
		t.Errorf("notification requests = %d, want %d", n, maxNotificationPages)
	}
	if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "page cap reached") || !strings.Contains(out, "channel=mastodon") {
		t.Errorf("no page-cap warning in log:\n%s", out)
	}
}

func TestTracker_MastodonMentionsUseMinID(t *testing.T) {
	s := NewStore(testPool(t))
	resetCursors(t, s)
	ctx := context.Background()
	tag := uniq()
	cleanMentions(t, s.pool, tag)
	mf := newTrackerMasto(t)
	ourID := "6001" + tag
	_, post := seedPosted(t, s, "mastodon", ourID, time.Now().Add(-time.Hour))
	mf.statuses[ourID] = `{}`
	ts := func(ago time.Duration) string { return time.Now().Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	item := func(nid, typ, sid, extra string) string {
		return fmt.Sprintf(`{"id":%q,"type":%q,"created_at":%q,"account":{"acct":"bob@other.example.test"},"status":{"id":%q,"uri":"https://other.example.test/users/bob/statuses/%s%s","url":"https://other.example.test/@bob/%s","created_at":%q,"content":"<p>hello</p>"%s}}`,
			nid, typ, ts(time.Minute), sid, sid, tag, sid, ts(time.Minute), extra)
	}
	mf.notifs[""] = "[" + item("20", "mention", "m20", fmt.Sprintf(`,"in_reply_to_id":%q`, ourID)) + "," + item("19", "mention", "m19", "") + "]"
	tr, _ := newTracker(t, s, nil, mf.client(t))
	tr.pass(ctx)
	got := mentionKinds(t, s, tag)
	if got["https://other.example.test/users/bob/statuses/m20"+tag] != fmt.Sprintf("reply:%d", post.ID) ||
		got["https://other.example.test/users/bob/statuses/m19"+tag] != "mention:-" {
		t.Errorf("mentions = %v", got)
	}
	if c, ok, _ := s.MentionCursor(ctx, "mastodon"); !ok || c != "20" {
		t.Errorf("cursor after pass 1 = %q", c)
	}
	// Pass 2 asks for notifications newer than the newest seen.
	mf.notifs["20"] = "[" + item("21", "quote", "m21", fmt.Sprintf(`,"quote":{"quoted_status":{"id":%q}}`, ourID)) + "]"
	tr.pass(ctx)
	if mf.matching("min_id=20") != 1 {
		t.Errorf("requests = %q, want one with min_id=20", mf.requests())
	}
	if got := mentionKinds(t, s, tag); got["https://other.example.test/users/bob/statuses/m21"+tag] != fmt.Sprintf("quote:%d", post.ID) {
		t.Errorf("quote not stored: %v", got)
	}
	if c, _, _ := s.MentionCursor(ctx, "mastodon"); c != "21" {
		t.Errorf("cursor after pass 2 = %q", c)
	}
}

func TestTracker_FailureLogsKeepsCursorAndOtherChannelRuns(t *testing.T) {
	s := NewStore(testPool(t))
	resetCursors(t, s)
	ctx := context.Background()
	tag := uniq()
	cleanMentions(t, s.pool, tag)
	bf, mf := newTrackerBsky(t), newTrackerMasto(t)
	bf.notifCode = 500
	if err := s.SetMentionCursor(ctx, "bluesky", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	mf.notifs[""] = fmt.Sprintf(`[{"id":"3","type":"mention","created_at":%q,"account":{"acct":"bob"},"status":{"id":"x","uri":"https://other.example.test/s/%s","created_at":%q,"content":"<p>hi</p>"}}]`, ts, tag, ts)
	tr, logs := newTracker(t, s, bf.client(t), mf.client(t))
	tr.pass(ctx)
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "channel=bluesky") || !strings.Contains(logs.String(), "list notifications") {
		t.Errorf("a failing channel must log an Error naming it:\n%s", logs.String())
	}
	if c, _, _ := s.MentionCursor(ctx, "bluesky"); c != "2026-01-01T00:00:00Z" {
		t.Errorf("bluesky cursor moved on failure: %q", c)
	}
	if len(mentionKinds(t, s, tag)) != 1 {
		t.Error("the mastodon half did not run after the bluesky failure")
	}

	// A Mastodon token without the read scopes is named in the log.
	mf.notifCode = 403
	tr2, logs2 := newTracker(t, s, nil, mf.client(t))
	tr2.pass(ctx)
	if !strings.Contains(logs2.String(), "read:notifications") {
		t.Errorf("a 403 must name the missing scope:\n%s", logs2.String())
	}
}

// ---- lifecycle ----

func TestTracker_StopCancelsABlockedRequest(t *testing.T) {
	s := NewStore(testPool(t))
	resetCursors(t, s)
	bf := newTrackerBsky(t)
	bf.block = true
	tr, _ := newTracker(t, s, bf.client(t), nil)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); tr.Run(runCtx) }()
	// Every started tracker is stopped, even if an assertion fails first.
	t.Cleanup(func() {
		cancel()
		sc, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		tr.Stop(sc)
		<-done
	})
	select {
	case <-bf.blockEntry:
	case <-time.After(5 * time.Second):
		t.Fatal("the tracker never reached the fake")
	}
	start := time.Now()
	sc, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	if err := tr.Stop(sc); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Stop took %v against a blocked fake; it must cancel the in-flight request", d)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("Run did not return after Stop")
	}
}

func TestTracker_NothingConfiguredReturnsImmediately(t *testing.T) {
	log, buf := captureLog()
	tr := NewTracker(TrackerDeps{Log: log})
	done := make(chan struct{})
	go func() { tr.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run with no channel configured must return")
	}
	if !strings.Contains(buf.String(), "bluesky tracking off (not configured)") || !strings.Contains(buf.String(), "mastodon tracking off (not configured)") {
		t.Errorf("the off halves must be logged:\n%s", buf.String())
	}
	if err := tr.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func TestPublisher_StopsItsTracker(t *testing.T) {
	s := NewStore(testPool(t))
	resetCursors(t, s)
	bf := newTrackerBsky(t)
	bf.block = true // keep the tracker inside a request so it is provably running
	tr, _ := newTracker(t, s, bf.client(t), nil)
	pub := NewPublisher(PublisherDeps{Store: s, Tracker: tr, PollInterval: time.Hour, Settings: fakeSettings{}})
	pub.outbox = newTestPublisher(t, s, nil, nil, "").outbox
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); pub.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		sc, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		pub.Stop(sc)
		<-done
	})
	select {
	case <-bf.blockEntry:
	case <-time.After(5 * time.Second):
		t.Fatal("the publisher never started its tracker")
	}
	sc, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	if err := pub.Stop(sc); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-tr.doneCh:
	case <-time.After(time.Second):
		t.Error("Publisher.Stop returned but its tracker is still running")
	}
}
