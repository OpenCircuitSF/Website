package mastodon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Fakes only: nothing here reaches a real Mastodon instance (#0547).

type seen struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *seen) add(r *http.Request) { s.mu.Lock(); s.reqs = append(s.reqs, r); s.mu.Unlock() }
func (s *seen) last() *http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reqs[len(s.reqs)-1]
}

func fake(t *testing.T, s *seen, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, fakeToken)
	if err != nil || c == nil {
		t.Fatal(err)
	}
	return c
}

func TestGetStatusCounts(t *testing.T) {
	var s seen
	body := `{"favourites_count":4,"reblogs_count":2,"replies_count":1,"quotes_count":3}`
	c := fake(t, &s, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
	if _, err := c.GetStatusCounts(context.Background(), "112/233"); !errors.Is(err, ErrInvalid) || len(s.reqs) != 0 {
		t.Errorf("a path-changing id must be refused before any request: %v", err)
	}
	got, err := c.GetStatusCounts(context.Background(), "112233")
	if err != nil {
		t.Fatal(err)
	}
	if got != (Counts{Likes: 4, Reposts: 2, Replies: 1, Quotes: 3}) {
		t.Errorf("counts = %+v", got)
	}
	r := s.last()
	if r.Header.Get("Authorization") != "Bearer "+fakeToken || r.URL.EscapedPath() != "/api/v1/statuses/112233" {
		t.Errorf("request auth ok=%v path=%q", r.Header.Get("Authorization") == "Bearer "+fakeToken, r.URL.EscapedPath())
	}
	body = `{"favourites_count":1,"reblogs_count":0,"replies_count":0}` // pre-4.5: no quotes_count
	if got, err = c.GetStatusCounts(context.Background(), "9"); err != nil || got.Quotes != 0 || got.Likes != 1 {
		t.Errorf("no quotes_count: %+v %v", got, err)
	}
}

func TestGetStatusCounts_Errors(t *testing.T) {
	for _, tc := range []struct {
		code      int
		kind      error
		permanent bool
	}{{404, ErrNotFound, true}, {403, ErrAuth, true}, {500, ErrServer, false}, {429, ErrRateLimited, false}} {
		var s seen
		c := fake(t, &s, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.code)
			io.WriteString(w, `{"error":"nope `+fakeToken+`"}`)
		})
		_, err := c.GetStatusCounts(context.Background(), "1")
		if !errors.Is(err, tc.kind) || IsPermanent(err) != tc.permanent {
			t.Errorf("%d: err=%v permanent=%v", tc.code, err, IsPermanent(err))
		}
		if strings.Contains(err.Error(), fakeToken) {
			t.Errorf("%d: error leaks the token: %v", tc.code, err)
		}
	}
}

func TestListNotifications(t *testing.T) {
	var s seen
	c := fake(t, &s, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[
		 {"id":"30","type":"quote","created_at":"2026-10-16T10:00:00.000Z","account":{"acct":"bob@other.example.test"},
		  "status":{"id":"900","uri":"https://other.example.test/users/bob/statuses/900","url":"https://other.example.test/@bob/900","created_at":"2026-10-16T09:59:00.000Z","content":"<p>nice</p>","quote":{"quoted_status":{"id":"111"}}}},
		 {"id":"29","type":"mention","created_at":"2026-10-16T09:00:00.000Z","account":{"acct":"carol"},
		  "status":{"id":"899","uri":"https://social.example.test/users/carol/statuses/899","url":"https://social.example.test/@carol/899","in_reply_to_id":"111","content":"<p>hi</p>","quote":null}},
		 {"id":"28","type":"mention","created_at":"2026-10-16T08:00:00.000Z","account":{"acct":"dave"}}
		]`)
	})
	got, err := c.ListNotifications(context.Background(), "", 80)
	if err != nil || len(got) != 3 {
		t.Fatalf("got %d, %v", len(got), err)
	}
	q := s.last().URL.Query()
	if len(q["types[]"]) != 2 || q["types[]"][0] != "mention" || q["types[]"][1] != "quote" || q.Get("limit") != "80" || q.Has("min_id") {
		t.Errorf("query = %v", q)
	}
	if !strings.Contains(s.last().URL.RawQuery, url.QueryEscape("types[]")) {
		t.Errorf("types[] key not literal: %s", s.last().URL.RawQuery)
	}
	if n := got[0]; n.QuotedStatusID != "111" || n.StatusURI == "" || n.Acct != "bob@other.example.test" || !n.HasStatus {
		t.Errorf("quote = %+v", n)
	}
	if n := got[1]; n.InReplyToID != "111" || n.QuotedStatusID != "" {
		t.Errorf("reply = %+v", n)
	}
	if got[2].HasStatus {
		t.Errorf("a notification without a status must report HasStatus false: %+v", got[2])
	}
	if _, err := c.ListNotifications(context.Background(), "30", 80); err != nil {
		t.Fatal(err)
	}
	if s.last().URL.Query().Get("min_id") != "30" || s.last().Header.Get("Authorization") != "Bearer "+fakeToken {
		t.Errorf("second request = %v", s.last().URL)
	}
}

func TestPlainText(t *testing.T) {
	in := `<p>Hi <span class="h-card"><a href="https://x.example.test/@oc">@<span>oc</span></a></span> &amp; see you</p><p>Line2<br>Line3</p>`
	if got, want := PlainText(in), "Hi @oc & see you\n\nLine2\nLine3"; got != want {
		t.Errorf("PlainText = %q, want %q", got, want)
	}
	if got := PlainText(`<p>a<script>alert(1)</script></p>`); got != "a" {
		t.Errorf("script survived: %q", got)
	}
	if got := PlainText(`&lt;b&gt;x&lt;/b&gt;`); got != "<b>x</b>" {
		t.Errorf("escaped markup must stay literal text: %q", got)
	}
}

func TestFindRecentStatusWithURL(t *testing.T) {
	var s seen
	c := fake(t, &s, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts/verify_credentials":
			io.WriteString(w, `{"id":"77"}`)
		case "/api/v1/accounts/77/statuses":
			io.WriteString(w, `[
			 {"id":"3","url":"https://m.test/@a/3","uri":"u3","content":"<p>other <a href=\"https://go.test/abc123\">go.test/abc12…</a></p>"},
			 {"id":"2","url":"https://m.test/@a/2","uri":"u2","content":"<p>Join us <a href=\"https://go.test/abc\">go.test/abc</a>.</p>"},
			 {"id":"1","url":"https://m.test/@a/1","uri":"u1","content":"<p>old</p>"}]`)
		default:
			w.WriteHeader(404)
		}
	})
	st, found, err := c.FindRecentStatusWithURL(context.Background(), "https://go.test/abc")
	if err != nil || !found || st.ID != "2" || st.URL != "https://m.test/@a/2" {
		t.Fatalf("found=%v st=%+v err=%v (a longer link sharing the prefix must not match)", found, st, err)
	}
	if r := s.last(); r.Header.Get("Authorization") != "Bearer "+fakeToken {
		t.Error("request not authenticated")
	}
	if _, found, err := c.FindRecentStatusWithURL(context.Background(), "https://go.test/zzz"); err != nil || found {
		t.Errorf("unknown link: found=%v err=%v", found, err)
	}
	if _, _, err := c.FindRecentStatusWithURL(context.Background(), " "); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty link: %v", err)
	}
}

func TestFindRecentStatusWithURL_ServerErrorIsReturned(t *testing.T) {
	var s seen
	c := fake(t, &s, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	if _, found, err := c.FindRecentStatusWithURL(context.Background(), "https://go.test/abc"); found || !errors.Is(err, ErrServer) {
		t.Errorf("found=%v err=%v", found, err)
	}
}

func TestVerifyCredentials(t *testing.T) {
	var s seen
	c := fake(t, &s, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"acct":"opencircuit","url":"https://example.social/@opencircuit"}`)
	})
	got, err := c.VerifyCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Acct != "opencircuit" {
		t.Errorf("acct = %q", got.Acct)
	}
	r := s.last()
	if r.URL.Path != "/api/v1/accounts/verify_credentials" || r.Header.Get("Authorization") != "Bearer "+fakeToken {
		t.Errorf("request path=%q auth ok=%v", r.URL.Path, r.Header.Get("Authorization") == "Bearer "+fakeToken)
	}
}
