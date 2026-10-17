package mastodon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	}
	for _, tc := range cases {
		if got := CountCharacters(tc.s); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
