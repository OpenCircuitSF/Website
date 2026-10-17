package bluesky

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Everything here runs against httptest fakes; nothing reaches a real PDS or
// AppView (#0547).

type fakeAppView struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
	gone map[string]bool // uris the AppView omits
	code int             // forced status, 0 = 200
}

func newFakeAppView(t *testing.T) *fakeAppView {
	t.Helper()
	f := &fakeAppView{gone: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r)
		code := f.code
		f.mu.Unlock()
		if code != 0 {
			w.WriteHeader(code)
			return
		}
		type post struct {
			URI         string `json:"uri"`
			LikeCount   int    `json:"likeCount"`
			RepostCount int    `json:"repostCount"`
			ReplyCount  int    `json:"replyCount"`
		}
		var posts []post
		for _, u := range r.URL.Query()["uris"] {
			if f.gone[u] {
				continue
			}
			// quoteCount is deliberately never sent: missing means 0.
			posts = append(posts, post{URI: u, LikeCount: 3, RepostCount: 1, ReplyCount: 2})
		}
		json.NewEncoder(w).Encode(map[string]any{"posts": posts})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestGetPostCounts(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	av := newFakeAppView(t)
	if err := c.SetAppViewURL(av.srv.URL); err != nil {
		t.Fatal(err)
	}
	var uris []string
	for i := 0; i < 30; i++ {
		uris = append(uris, fmt.Sprintf("at://%s/app.bsky.feed.post/%d", fakeDID, i))
	}
	av.gone[uris[4]] = true
	got, err := c.GetPostCounts(context.Background(), uris)
	if err != nil {
		t.Fatal(err)
	}
	if len(av.reqs) != 2 || len(av.reqs[0].URL.Query()["uris"]) != 25 || len(av.reqs[1].URL.Query()["uris"]) != 5 {
		t.Fatalf("requests = %d, want batches of 25 and 5", len(av.reqs))
	}
	for _, r := range av.reqs {
		if r.Header.Get("Authorization") != "" {
			t.Error("the public AppView call carried an Authorization header")
		}
	}
	if n := f.count("/xrpc/com.atproto.server.createSession"); n != 0 {
		t.Errorf("public read logged in %d times", n)
	}
	if _, ok := got[uris[4]]; ok || len(got) != 29 {
		t.Errorf("omitted post present or wrong size: %d", len(got))
	}
	if got[uris[0]] != (Counts{Likes: 3, Reposts: 1, Replies: 2, Quotes: 0}) {
		t.Errorf("counts = %+v", got[uris[0]])
	}
}

func TestGetPostCounts_ServerError(t *testing.T) {
	c := newTestClient(t, newFakePDS(t))
	av := newFakeAppView(t)
	av.code = 500
	c.SetAppViewURL(av.srv.URL)
	_, err := c.GetPostCounts(context.Background(), []string{"at://x/y/z"})
	if !errors.Is(err, ErrServer) || IsPermanent(err) {
		t.Errorf("err = %v, want transient ErrServer", err)
	}
}

func TestSetAppViewURL(t *testing.T) {
	c := newTestClient(t, newFakePDS(t))
	if c.appView.String() != DefaultAppViewURL {
		t.Errorf("default appView = %v", c.appView)
	}
	if err := c.SetAppViewURL("ftp://x"); err == nil {
		t.Error("non-http AppView URL accepted")
	}
}

func setNotif(f *fakePDS, cursor, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notif == nil {
		f.notif = map[string]string{}
	}
	f.notif[cursor] = body
}

func TestListNotifications_RequestAndParse(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	setNotif(f, "", `{"cursor":"next1","notifications":[
	 {"uri":"at://did:plc:aaa/app.bsky.feed.post/1","reason":"reply","reasonSubject":"at://ours/1","indexedAt":"2026-10-16T10:00:00.000Z","author":{"handle":"alice.example.test"},
	  "record":{"text":"see you there","createdAt":"2026-10-16T09:59:00.000Z","reply":{"parent":{"uri":"at://ours/1"},"root":{"uri":"at://ours/root"}}}},
	 {"uri":"at://did:plc:bbb/app.bsky.feed.post/2","reason":"quote","indexedAt":"2026-10-16T09:00:00.000Z","author":{"handle":"bob.example.test"},
	  "record":{"text":"q1","embed":{"$type":"app.bsky.embed.record","record":{"uri":"at://ours/2"}}}},
	 {"uri":"at://did:plc:ccc/app.bsky.feed.post/3","reason":"quote","indexedAt":"2026-10-16T08:00:00.000Z","author":{"handle":"carol.example.test"},
	  "record":{"text":"q2","embed":{"$type":"app.bsky.embed.recordWithMedia","record":{"record":{"uri":"at://ours/3"}}}}},
	 {"uri":"at://did:plc:ddd/app.bsky.feed.post/4","reason":"like","indexedAt":"2026-10-16T07:00:00.000Z","author":{"handle":"dave.example.test"}}
	]}`)
	page, err := c.ListNotifications(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	const p = "/xrpc/app.bsky.notification.listNotifications"
	last := f.last(p)
	if last.Auth == "" || !strings.HasPrefix(last.Auth, "Bearer fake-access-") {
		t.Errorf("auth = %q", last.Auth)
	}
	q, _ := url.ParseQuery(last.Query)
	if strings.Join(q["reasons"], ",") != "mention,reply,quote" || q.Get("limit") != "100" || q.Has("cursor") {
		t.Errorf("query = %v", q)
	}
	if page.Cursor != "next1" || len(page.Items) != 3 {
		t.Fatalf("page = %+v (a like must be filtered client-side)", page)
	}
	if it := page.Items[0]; it.ReplyParent != "at://ours/1" || it.ReplyRoot != "at://ours/root" || it.Text != "see you there" || it.AuthorHandle != "alice.example.test" || it.ReasonSubject != "at://ours/1" {
		t.Errorf("reply = %+v", it)
	}
	if page.Items[1].QuoteURI != "at://ours/2" || page.Items[2].QuoteURI != "at://ours/3" {
		t.Errorf("quote uris = %q, %q", page.Items[1].QuoteURI, page.Items[2].QuoteURI)
	}
	setNotif(f, "next1", `{"notifications":[]}`)
	if _, err := c.ListNotifications(context.Background(), "next1", 50); err != nil {
		t.Fatal(err)
	}
	q, _ = url.ParseQuery(f.last(p).Query)
	if q.Get("cursor") != "next1" || q.Get("limit") != "50" {
		t.Errorf("second query = %v", q)
	}
}

func TestListNotifications_ProxyHeaderAndRefresh(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	var mu sync.Mutex
	var proxies []string
	if _, err := c.ListNotifications(context.Background(), "", 100); err != nil {
		t.Fatal(err)
	}
	// Expire the access token on the server: the client must refresh once,
	// not log in again.
	f.mu.Lock()
	f.access = "rotated-away"
	f.override["/xrpc/app.bsky.notification.listNotifications"] = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		proxies = append(proxies, r.Header.Get("Atproto-Proxy"))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fresh-access" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"ExpiredToken","message":"Token has expired"}`))
			return
		}
		w.Write([]byte(`{"notifications":[]}`))
	}
	f.override["/xrpc/com.atproto.server.refreshSession"] = func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"accessJwt": "fresh-access", "refreshJwt": "fresh-refresh", "did": fakeDID, "handle": fakeHandle})
	}
	f.mu.Unlock()
	if _, err := c.ListNotifications(context.Background(), "", 100); err != nil {
		t.Fatal(err)
	}
	if n := f.count("/xrpc/com.atproto.server.createSession"); n != 1 {
		t.Errorf("createSession = %d, want 1", n)
	}
	if n := f.count("/xrpc/com.atproto.server.refreshSession"); n != 1 {
		t.Errorf("refreshSession = %d, want 1", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(proxies) != 2 || proxies[0] != "did:web:api.bsky.app#bsky_appview" || proxies[1] != proxies[0] {
		t.Errorf("atproto-proxy headers = %v (must be sent on the retry too)", proxies)
	}
}

func TestListNotifications_ErrorsLeakNothing(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	f.mu.Lock()
	f.override["/xrpc/app.bsky.notification.listNotifications"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"InternalServerError","message":"boom ` + fakePassword + `"}`))
	}
	f.mu.Unlock()
	_, err := c.ListNotifications(context.Background(), "", 100)
	if !errors.Is(err, ErrServer) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), fakePassword) {
		t.Errorf("error leaks the password: %v", err)
	}
}
