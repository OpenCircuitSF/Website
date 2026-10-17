package bluesky

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every value here is an obviously fake test fixture; nothing in this file
// touches a real PDS (#0545).
const (
	fakePassword = "fake-app-password-xyz"
	fakeHandle   = "alice.example.test"
	fakeDID      = "did:plc:fakefakefake"
)

type recorded struct {
	Method, Path, Auth, ContentType string
	Query                           string
	Body                            []byte
}

// fakePDS is a scriptable stand-in for a PDS. Handlers may be overridden per
// test; every request is recorded.
type fakePDS struct {
	srv *httptest.Server

	mu       sync.Mutex
	reqs     []recorded
	logins   int
	access   string // access token currently accepted
	refresh  string // refresh token currently accepted
	gen      int
	records  map[string]bool   // rkeys that exist
	notif    map[string]string // listNotifications bodies by cursor query value (#0547)
	override map[string]func(w http.ResponseWriter, r *http.Request)
}

func newFakePDS(t *testing.T) *fakePDS {
	t.Helper()
	f := &fakePDS{records: map[string]bool{}, override: map[string]func(http.ResponseWriter, *http.Request){}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePDS) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.reqs {
		if r.Path == path {
			n++
		}
	}
	return n
}

func (f *fakePDS) last(path string) recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.reqs) - 1; i >= 0; i-- {
		if f.reqs[i].Path == path {
			return f.reqs[i]
		}
	}
	return recorded{}
}

func (f *fakePDS) newTokens() {
	f.gen++
	f.access = "fake-access-" + string(rune('a'+f.gen))
	f.refresh = "fake-refresh-" + string(rune('a'+f.gen))
}

func (f *fakePDS) writeSession(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(map[string]string{"accessJwt": f.access, "refreshJwt": f.refresh, "did": fakeDID, "handle": fakeHandle})
}

func (f *fakePDS) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.reqs = append(f.reqs, recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.URL.RawQuery, body})
	ov := f.override[r.URL.Path]
	f.mu.Unlock()
	if ov != nil {
		ov(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/xrpc/com.atproto.server.createSession":
		var in map[string]string
		json.Unmarshal(body, &in)
		f.mu.Lock()
		defer f.mu.Unlock()
		if in["password"] != fakePassword {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"error": "AuthenticationRequired", "message": "Invalid identifier or password " + in["password"]})
			return
		}
		f.logins++
		f.newTokens()
		f.writeSession(w)
	case "/xrpc/com.atproto.server.refreshSession":
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+f.refresh {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "ExpiredToken", "message": "Token has expired"})
			return
		}
		f.newTokens()
		f.writeSession(w)
	default:
		f.mu.Lock()
		okAuth := r.Header.Get("Authorization") == "Bearer "+f.access
		f.mu.Unlock()
		if !okAuth {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "ExpiredToken", "message": "Token has expired"})
			return
		}
		switch r.URL.Path {
		case "/xrpc/com.atproto.repo.getRecord":
			f.mu.Lock()
			has := f.records[r.URL.Query().Get("rkey")]
			f.mu.Unlock()
			if !has {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "RecordNotFound", "message": "Could not locate record"})
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"uri": "at://" + fakeDID + "/app.bsky.feed.post/" + r.URL.Query().Get("rkey")})
		case "/xrpc/com.atproto.repo.createRecord":
			var in struct{ RKey string }
			json.Unmarshal(body, &in)
			f.mu.Lock()
			f.records[in.RKey] = true
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]string{"uri": "at://" + fakeDID + "/app.bsky.feed.post/" + in.RKey, "cid": "fakecid"})
		case "/xrpc/app.bsky.notification.listNotifications":
			f.mu.Lock()
			b, ok := f.notif[r.URL.Query().Get("cursor")]
			f.mu.Unlock()
			if !ok {
				b = `{"notifications":[]}`
			}
			io.WriteString(w, b)
		case "/xrpc/com.atproto.repo.uploadBlob":
			io.WriteString(w, `{"blob":{"$type":"blob","ref":{"$link":"fakeblobcid"},"mimeType":"image/jpeg","size":4}}`)
		default:
			w.WriteHeader(404)
		}
	}
}

func newTestClient(t *testing.T, f *fakePDS) *Client {
	t.Helper()
	c, err := New(f.srv.URL, fakeHandle, fakePassword)
	if err != nil || c == nil {
		t.Fatalf("New: %v %v", c, err)
	}
	return c
}

func samplePost(rkey string) PostInput {
	return PostInput{
		Text:      "Café night #DIY https://go.example.test/k",
		RKey:      rkey,
		CreatedAt: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
		Embed:     &ExternalCard{URI: "https://go.example.test/k", Title: "Soldering 101", Description: "Learn to solder."},
	}
}

func TestNew_Unconfigured(t *testing.T) {
	for _, tc := range [][2]string{{"", ""}, {"h", ""}, {"", "p"}} {
		c, err := New("", tc[0], tc[1])
		if c != nil || err != nil || c.Enabled() {
			t.Errorf("New(%q,%q) = %v, %v; want nil, nil", tc[0], tc[1], c, err)
		}
	}
	if _, err := New("not a url", "h", "p"); err == nil {
		t.Error("malformed PDS URL should be an error")
	}
	c, _ := New("", "h", "p")
	if !c.Enabled() || c.base.String() != DefaultPDSURL {
		t.Errorf("default PDS URL: %v", c.base)
	}
}

func TestPost_OneLoginAcrossPosts(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	for _, k := range []string{"2222222222222", "2222222222223", "2222222222224"} {
		if _, err := c.Post(context.Background(), samplePost(k)); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count("/xrpc/com.atproto.server.createSession"); n != 1 {
		t.Errorf("createSession called %d times, want 1", n)
	}
}

func TestPost_RequestBody(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	in := samplePost("2222222222222")
	res, err := c.Post(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.URI != "at://"+fakeDID+"/app.bsky.feed.post/2222222222222" || res.CID != "fakecid" || res.AlreadyExisted {
		t.Errorf("result = %+v", res)
	}
	if res.WebURL != "https://bsky.app/profile/"+fakeHandle+"/post/2222222222222" {
		t.Errorf("WebURL = %q", res.WebURL)
	}
	var body struct {
		Repo, Collection, RKey string
		Record                 struct {
			Type      string `json:"$type"`
			Text      string
			CreatedAt string
			Langs     []string
			Facets    []Facet
			Embed     struct {
				Type     string `json:"$type"`
				External map[string]json.RawMessage
			}
		}
	}
	if err := json.Unmarshal(f.last("/xrpc/com.atproto.repo.createRecord").Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Repo != fakeDID || body.Collection != "app.bsky.feed.post" || body.RKey != "2222222222222" {
		t.Errorf("envelope = %+v", body)
	}
	if body.Record.Type != "app.bsky.feed.post" || body.Record.Text != in.Text || body.Record.CreatedAt != "2026-10-16T12:00:00.000Z" {
		t.Errorf("record = %+v", body.Record)
	}
	// "Café night " is 12 bytes (é is two); #DIY is 12-16; the link starts at 17.
	fs := body.Record.Facets
	if len(fs) != 2 || fs[0].Index != (FacetIndex{12, 16}) || fs[0].Features[0].Tag != "DIY" || fs[1].Index != (FacetIndex{17, 17 + len("https://go.example.test/k")}) {
		t.Errorf("facets = %+v", fs)
	}
	if body.Record.Embed.Type != "app.bsky.embed.external" || string(body.Record.Embed.External["uri"]) != `"https://go.example.test/k"` {
		t.Errorf("embed = %+v", body.Record.Embed)
	}
	if _, has := body.Record.Embed.External["thumb"]; has {
		t.Error("no thumb was supplied but one was sent")
	}
	if f.count("/xrpc/com.atproto.repo.uploadBlob") != 0 {
		t.Error("uploadBlob called without a thumbnail")
	}
}

func TestPost_ThumbUploadedAndEmbeddedVerbatim(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	in := samplePost("2222222222222")
	in.Embed.Thumb, in.Embed.ThumbMIME = []byte{0xff, 0xd8, 0xff, 0xd9}, "image/jpeg"
	if _, err := c.Post(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	up := f.last("/xrpc/com.atproto.repo.uploadBlob")
	if up.ContentType != "image/jpeg" || len(up.Body) != 4 {
		t.Errorf("upload = %+v", up)
	}
	if !strings.Contains(string(f.last("/xrpc/com.atproto.repo.createRecord").Body), `"thumb":{"$type":"blob","ref":{"$link":"fakeblobcid"},"mimeType":"image/jpeg","size":4}`) {
		t.Errorf("blob object not embedded verbatim: %s", f.last("/xrpc/com.atproto.repo.createRecord").Body)
	}
}

func TestPost_ThumbUploadFailureIsNotFatal(t *testing.T) {
	f := newFakePDS(t)
	f.override["/xrpc/com.atproto.repo.uploadBlob"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }
	c := newTestClient(t, f)
	in := samplePost("2222222222222")
	in.Embed.Thumb, in.Embed.ThumbMIME = []byte{1, 2, 3}, "image/png"
	res, err := c.Post(context.Background(), in)
	if err != nil {
		t.Fatalf("post should succeed without the thumb: %v", err)
	}
	if res.ThumbErr == nil {
		t.Error("ThumbErr should report the failed upload")
	}
	if strings.Contains(string(f.last("/xrpc/com.atproto.repo.createRecord").Body), `"thumb"`) {
		t.Error("record carries a thumb that failed to upload")
	}
}

func TestPost_OversizeThumbSkipped(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	in := samplePost("2222222222222")
	in.Embed.Thumb, in.Embed.ThumbMIME = make([]byte, MaxThumbBytes+1), "image/jpeg"
	if _, err := c.Post(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if f.count("/xrpc/com.atproto.repo.uploadBlob") != 0 {
		t.Error("an over-limit thumbnail must not be uploaded")
	}
}

func TestPost_ExistingRecordIsNotRecreated(t *testing.T) {
	f := newFakePDS(t)
	f.records["2222222222222"] = true
	c := newTestClient(t, f)
	res, err := c.Post(context.Background(), samplePost("2222222222222"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.AlreadyExisted || res.URI == "" {
		t.Errorf("result = %+v", res)
	}
	if f.count("/xrpc/com.atproto.repo.createRecord") != 0 {
		t.Error("createRecord called although the record exists")
	}
}

func TestPost_ExpiredAccessTokenRefreshes(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	if _, err := c.Post(context.Background(), samplePost("2222222222222")); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.access = "server-rotated-access" // the cached access token is now stale
	f.mu.Unlock()
	if _, err := c.Post(context.Background(), samplePost("2222222222223")); err != nil {
		t.Fatal(err)
	}
	if f.count("/xrpc/com.atproto.server.refreshSession") != 1 {
		t.Errorf("refreshSession calls = %d, want 1", f.count("/xrpc/com.atproto.server.refreshSession"))
	}
	if f.count("/xrpc/com.atproto.server.createSession") != 1 {
		t.Errorf("createSession calls = %d, want 1 (no second login)", f.count("/xrpc/com.atproto.server.createSession"))
	}
}

func TestPost_RefreshRefusedLogsInOnce(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	if _, err := c.Post(context.Background(), samplePost("2222222222222")); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.access, f.refresh = "rotated-access", "rotated-refresh" // both cached tokens now stale
	f.mu.Unlock()
	if _, err := c.Post(context.Background(), samplePost("2222222222223")); err != nil {
		t.Fatal(err)
	}
	if n := f.count("/xrpc/com.atproto.server.createSession"); n != 2 {
		t.Errorf("createSession calls = %d, want exactly one new login (2 total)", n)
	}
}

func TestPost_BadPasswordIsPermanentAndDoesNotLoop(t *testing.T) {
	f := newFakePDS(t)
	c, _ := New(f.srv.URL, fakeHandle, "wrong-password")
	_, err := c.Post(context.Background(), samplePost("2222222222222"))
	if !IsPermanent(err) || !errorsIs(err, ErrAuth) {
		t.Fatalf("err = %v, want permanent ErrAuth", err)
	}
	if n := f.count("/xrpc/com.atproto.server.createSession"); n != 1 {
		t.Errorf("createSession calls = %d, want 1", n)
	}
	if strings.Contains(err.Error(), "wrong-password") {
		t.Errorf("error leaks the password: %v", err)
	}
}

func TestPost_ServerFaultsAreTransientAndClientFaultsPermanent(t *testing.T) {
	for _, tc := range []struct {
		status    int
		code      string
		permanent bool
	}{
		{429, "RateLimitExceeded", false},
		{500, "InternalServerError", false},
		{503, "", false},
		{400, "InvalidRequest", true},
		{422, "InvalidRequest", true},
	} {
		f := newFakePDS(t)
		f.override["/xrpc/com.atproto.repo.createRecord"] = func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			json.NewEncoder(w).Encode(map[string]string{"error": tc.code, "message": "boom"})
		}
		c := newTestClient(t, f)
		_, err := c.Post(context.Background(), samplePost("2222222222222"))
		if err == nil {
			t.Fatalf("%d: expected an error", tc.status)
		}
		if IsPermanent(err) != tc.permanent {
			t.Errorf("%d %s: permanent = %v, want %v (%v)", tc.status, tc.code, IsPermanent(err), tc.permanent, err)
		}
	}
}

func TestPost_NetworkErrorIsTransient(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	f.srv.Close()
	_, err := c.Post(context.Background(), samplePost("2222222222222"))
	if err == nil || IsPermanent(err) {
		t.Errorf("err = %v, want a transient error", err)
	}
}

func TestPost_OverLimitIsPermanentBeforeAnyRequest(t *testing.T) {
	f := newFakePDS(t)
	c := newTestClient(t, f)
	in := samplePost("2222222222222")
	in.Text = strings.Repeat("x", 301)
	_, err := c.Post(context.Background(), in)
	if !errorsIs(err, ErrTextTooLong) || !IsPermanent(err) {
		t.Errorf("err = %v", err)
	}
	f.mu.Lock()
	n := len(f.reqs)
	f.mu.Unlock()
	if n != 0 {
		t.Errorf("%d requests sent for an over-limit post", n)
	}
}

func TestErrors_NeverContainSecrets(t *testing.T) {
	f := newFakePDS(t)
	// A hostile or buggy server that echoes credentials into its messages.
	f.override["/xrpc/com.atproto.repo.createRecord"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "InvalidRequest", "message": "echo " + r.Header.Get("Authorization") + " " + fakePassword})
	}
	c := newTestClient(t, f)
	_, err := c.Post(context.Background(), samplePost("2222222222222"))
	if err == nil {
		t.Fatal("expected error")
	}
	c.mu.Lock()
	secrets := []string{fakePassword, c.sess.access, c.sess.refresh}
	c.mu.Unlock()
	for _, s := range secrets {
		if strings.Contains(err.Error(), s) {
			t.Errorf("error contains a secret: %v", err)
		}
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("expected redaction marker: %v", err)
	}
}

func TestNilClientPostIsPermanentError(t *testing.T) {
	var c *Client
	if _, err := c.Post(context.Background(), samplePost("2222222222222")); !IsPermanent(err) {
		t.Errorf("err = %v", err)
	}
}

func errorsIs(err, target error) bool { return errors.Is(err, target) }

func TestVerifyCredentials(t *testing.T) {
	f := newFakePDS(t)
	c, err := New(f.srv.URL, fakeHandle, fakePassword)
	if err != nil || c == nil {
		t.Fatal(err)
	}
	h, err := c.VerifyCredentials(context.Background())
	if err != nil || h != fakeHandle {
		t.Fatalf("handle=%q err=%v", h, err)
	}
	if n := f.count("/xrpc/com.atproto.server.createSession"); n != 1 {
		t.Errorf("createSession hits = %d, want 1", n)
	}

	bad, _ := New(f.srv.URL, fakeHandle, "wrong-password")
	if _, err := bad.VerifyCredentials(context.Background()); !errors.Is(err, ErrAuth) {
		t.Errorf("a wrong password must be ErrAuth, got %v", err)
	}
}

func TestVerifyCredentialsNilClient(t *testing.T) {
	var c *Client
	if _, err := c.VerifyCredentials(context.Background()); !errors.Is(err, ErrAuth) {
		t.Errorf("nil client must report ErrAuth, got %v", err)
	}
}
