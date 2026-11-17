package mastodon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// All values are fake fixtures; nothing here reaches a real instance (#0545).
const fakeToken = "fake-mastodon-token-123"

func TestNew(t *testing.T) {
	for _, tc := range [][2]string{{"", ""}, {"https://social.example.test", ""}, {"", "t"}} {
		c, err := New(tc[0], tc[1])
		if c != nil || err != nil || c.Enabled() {
			t.Errorf("New(%q,%q) = %v, %v; want nil, nil", tc[0], tc[1], c, err)
		}
	}
	if c, err := New("https://social.example.test/", "t"); err != nil || !c.Enabled() {
		t.Errorf("valid https: %v %v", c, err)
	}
	if _, err := New("http://social.example.test", "t"); err == nil {
		t.Error("plain http to a remote host must be refused")
	}
	if c, err := New("http://127.0.0.1:9", "t"); err != nil || c == nil {
		t.Errorf("http to loopback should be allowed: %v %v", c, err)
	}
	if _, err := New("not a url", "t"); err == nil {
		t.Error("malformed URL must be an error")
	}
}

func TestPostStatus_RequestAndResult(t *testing.T) {
	var gotAuth, gotKey, gotCT, gotPath string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotKey, gotCT, gotPath = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type"), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		io.WriteString(w, `{"id":"1234","url":"https://social.example.test/@alice/1234","uri":"https://social.example.test/users/alice/statuses/1234"}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, fakeToken)
	st, err := c.PostStatus(context.Background(), "Hello \U0001F527 https://go.example.test/k", "opencircuitsf-outreach-post-7")
	if err != nil {
		t.Fatal(err)
	}
	if st.ID != "1234" || st.URL != "https://social.example.test/@alice/1234" {
		t.Errorf("status = %+v", st)
	}
	if gotPath != "/api/v1/statuses" || gotAuth != "Bearer "+fakeToken || gotKey != "opencircuitsf-outreach-post-7" || gotCT != "application/json" {
		t.Errorf("request: path=%q auth set=%v key=%q ct=%q", gotPath, gotAuth == "Bearer "+fakeToken, gotKey, gotCT)
	}
	if gotBody["status"] != "Hello \U0001F527 https://go.example.test/k" || gotBody["visibility"] != "public" || gotBody["language"] != "en" {
		t.Errorf("body = %v", gotBody)
	}
}

func TestPostStatus_ErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		status    int
		permanent bool
		kind      error
	}{
		{401, true, ErrAuth}, {403, true, ErrAuth}, {422, true, ErrInvalid},
		{429, false, ErrRateLimited}, {500, false, ErrServer}, {503, false, ErrServer},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			// A server that echoes the credential it was given.
			json.NewEncoder(w).Encode(map[string]string{"error": "nope " + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")})
		}))
		c, _ := New(srv.URL, fakeToken)
		_, err := c.PostStatus(context.Background(), "hi", "k")
		srv.Close()
		if err == nil || IsPermanent(err) != tc.permanent || !errors.Is(err, tc.kind) {
			t.Errorf("%d: err = %v permanent=%v", tc.status, err, IsPermanent(err))
		}
		if err != nil && strings.Contains(err.Error(), fakeToken) {
			t.Errorf("%d: error leaks the token: %v", tc.status, err)
		}
	}
}

func TestPostStatus_NetworkErrorTransient(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	c, _ := New(srv.URL, fakeToken)
	srv.Close()
	_, err := c.PostStatus(context.Background(), "hi", "k")
	if err == nil || IsPermanent(err) || strings.Contains(err.Error(), fakeToken) {
		t.Errorf("err = %v", err)
	}
}

func TestPostStatus_NilAndEmpty(t *testing.T) {
	var c *Client
	if _, err := c.PostStatus(context.Background(), "hi", "k"); !IsPermanent(err) {
		t.Errorf("nil client: %v", err)
	}
	c2, _ := New("https://social.example.test", "t")
	if _, err := c2.PostStatus(context.Background(), "  ", "k"); !IsPermanent(err) {
		t.Errorf("blank text: %v", err)
	}
}

func TestCountCharacters(t *testing.T) {
	long := "https://example.test/" + strings.Repeat("a", 60)
	cases := []struct {
		name, s string
		want    int
	}{
		{"plain", "hello", 5},
		{"emoji is one rune", "\U0001F527\U0001F527", 2},
		{"long url counts 23", "see " + long, 4 + 23},
		{"short url also 23", "go https://a.test/x now", 3 + 23 + 4},
		{"two urls", long + " " + long, 23 + 1 + 23},
		{"empty", "", 0},
		{"trailing period is text", "see https://a.test/x.", 4 + 23 + 1},
		{"trailing punctuation run", "go https://a.test/x?!) ok", 3 + 23 + 3 + 3},
		{"paren kept when balanced", "(https://a.test/x_(y))", 1 + 23 + 0},
		{"paren wrapping a url", "(see https://a.test/x)", 5 + 23 + 1},
	}
	for _, tc := range cases {
		if got := CountCharacters(tc.s); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestDeleteStatus(t *testing.T) {
	var method, path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		io.WriteString(w, `{"id":"1234","text":"gone"}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, fakeToken)
	existed, err := c.DeleteStatus(context.Background(), "1234")
	if err != nil || !existed {
		t.Fatalf("DeleteStatus = %v, %v", existed, err)
	}
	if method != http.MethodDelete || path != "/api/v1/statuses/1234" || auth != "Bearer "+fakeToken {
		t.Errorf("request: %s %s auth ok=%v", method, path, auth == "Bearer "+fakeToken)
	}
}

func TestDeleteStatus_AlreadyGoneIsSuccess(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusGone} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			io.WriteString(w, `{"error":"Record not found"}`)
		}))
		c, _ := New(srv.URL, fakeToken)
		existed, err := c.DeleteStatus(context.Background(), "1234")
		srv.Close()
		if err != nil || existed {
			t.Errorf("HTTP %d: DeleteStatus = %v, %v; want not existed, nil", code, existed, err)
		}
	}
}

func TestDeleteStatus_FailuresAreErrorsAndRedacted(t *testing.T) {
	for code, permanent := range map[int]bool{401: true, 403: true, 429: false, 500: false} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			io.WriteString(w, `{"error":"nope `+fakeToken+`"}`)
		}))
		c, _ := New(srv.URL, fakeToken)
		_, err := c.DeleteStatus(context.Background(), "1234")
		srv.Close()
		if err == nil || IsPermanent(err) != permanent {
			t.Errorf("HTTP %d: err = %v, permanent = %v", code, err, IsPermanent(err))
			continue
		}
		if strings.Contains(err.Error(), fakeToken) {
			t.Errorf("HTTP %d: token leaked: %v", code, err)
		}
	}
}

func TestDeleteStatus_BadInputAndNilClient(t *testing.T) {
	var nilc *Client
	if _, err := nilc.DeleteStatus(context.Background(), "1"); !IsPermanent(err) {
		t.Errorf("nil client err = %v", err)
	}
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	c, _ := New(srv.URL, fakeToken)
	for _, bad := range []string{"", "1/2", "1?x"} {
		if _, err := c.DeleteStatus(context.Background(), bad); !IsPermanent(err) {
			t.Errorf("id %q: err = %v", bad, err)
		}
	}
	if hit {
		t.Error("a malformed id must not reach the network")
	}
}

// newMediaClient is a client against srv with a few-ms media poll (#0572).
func newMediaClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(srv.URL, fakeToken)
	if err != nil {
		t.Fatal(err)
	}
	c.mediaPoll = 2 * time.Millisecond
	return c
}

func TestUploadMedia_ReadyImmediately(t *testing.T) {
	var gotPath, gotAuth, gotPartCT, gotDesc string
	var gotFile []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		gotDesc = r.FormValue("description")
		if f, fh, err := r.FormFile("file"); err == nil {
			gotFile, _ = io.ReadAll(f)
			gotPartCT = fh.Header.Get("Content-Type")
		}
		io.WriteString(w, `{"id":"m1","url":"https://files.example.test/m1.png"}`)
	}))
	defer srv.Close()
	id, err := newMediaClient(t, srv).UploadMedia(context.Background(), []byte("PNGBYTES"), "image/png", "A soldering iron")
	if err != nil || id != "m1" {
		t.Fatalf("UploadMedia = %q, %v", id, err)
	}
	if gotPath != "/api/v2/media" || gotAuth != "Bearer "+fakeToken || string(gotFile) != "PNGBYTES" || gotPartCT != "image/png" || gotDesc != "A soldering iron" {
		t.Errorf("request: path=%q auth ok=%v file=%q partCT=%q desc=%q", gotPath, gotAuth == "Bearer "+fakeToken, gotFile, gotPartCT, gotDesc)
	}
}

func TestUploadMedia_PollsUntilProcessed(t *testing.T) {
	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/media":
			w.WriteHeader(http.StatusAccepted)
			io.WriteString(w, `{"id":"m2","url":null}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/media/m2":
			if gets.Add(1) < 3 {
				w.WriteHeader(http.StatusPartialContent)
			}
			io.WriteString(w, `{"id":"m2"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer srv.Close()
	id, err := newMediaClient(t, srv).UploadMedia(context.Background(), []byte("x"), "image/jpeg", "alt")
	if err != nil || id != "m2" {
		t.Fatalf("UploadMedia = %q, %v", id, err)
	}
	if n := gets.Load(); n != 3 {
		t.Errorf("GETs = %d, want 3 (206, 206, 200)", n)
	}
}

func TestUploadMedia_PollStopsAtDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
		} else {
			w.WriteHeader(http.StatusPartialContent)
		}
		io.WriteString(w, `{"id":"m3"}`)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	id, err := newMediaClient(t, srv).UploadMedia(ctx, []byte("x"), "image/jpeg", "alt")
	if err == nil || id != "" {
		t.Fatalf("UploadMedia = %q, %v; want an error once the deadline passes", id, err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("UploadMedia took %v after a 50ms deadline", d)
	}
}

func TestUploadMedia_RefusesBlankAltOrData(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	c := newMediaClient(t, srv)
	for _, tc := range []struct {
		data []byte
		alt  string
	}{{[]byte("x"), ""}, {[]byte("x"), "  \n"}, {nil, "alt"}} {
		_, err := c.UploadMedia(context.Background(), tc.data, "image/jpeg", tc.alt)
		if !IsPermanent(err) || !errors.Is(err, ErrInvalid) {
			t.Errorf("UploadMedia(%q, %q) err = %v, want permanent ErrInvalid", tc.data, tc.alt, err)
		}
	}
	var nilClient *Client
	if _, err := nilClient.UploadMedia(context.Background(), []byte("x"), "image/jpeg", "alt"); !errors.Is(err, ErrAuth) {
		t.Errorf("nil client err = %v, want ErrAuth", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

func TestUploadMedia_ErrorsAreRedacted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"bad token `+fakeToken+`"}`)
	}))
	defer srv.Close()
	_, err := newMediaClient(t, srv).UploadMedia(context.Background(), []byte("x"), "image/jpeg", "alt")
	if !errors.Is(err, ErrAuth) || !IsPermanent(err) {
		t.Fatalf("err = %v, want permanent ErrAuth", err)
	}
	if strings.Contains(err.Error(), fakeToken) {
		t.Errorf("error leaks the token: %v", err)
	}
}

func TestPostStatus_SendsMediaIDs(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &m)
		bodies = append(bodies, m)
		io.WriteString(w, `{"id":"1","url":"u","uri":"u"}`)
	}))
	defer srv.Close()
	c := newMediaClient(t, srv)
	if _, err := c.PostStatus(context.Background(), "with image", "k1", "m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PostStatus(context.Background(), "text only", "k2"); err != nil {
		t.Fatal(err)
	}
	ids, _ := bodies[0]["media_ids"].([]any)
	if len(ids) != 1 || ids[0] != "m1" {
		t.Errorf("first body media_ids = %v, want [m1]", bodies[0]["media_ids"])
	}
	if _, ok := bodies[1]["media_ids"]; ok {
		t.Errorf("second body has media_ids: %v", bodies[1])
	}
}
