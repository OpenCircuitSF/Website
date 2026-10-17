package shortlinks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const testToken = "slk_test_secret_value"

func newTest(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, testToken)
	if err != nil || c == nil {
		t.Fatalf("New: %v %v", c, err)
	}
	return c
}

func requireAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
		t.Errorf("Authorization = %q", got)
	}
	if r.URL.Query().Get("token") != "" {
		t.Error("token in query string")
	}
}

func TestNewDisabledWhenUnset(t *testing.T) {
	for _, tc := range [][2]string{{"", ""}, {"http://x", ""}, {"", "tok"}} {
		c, err := New(tc[0], tc[1])
		if c != nil || err != nil {
			t.Errorf("New(%q,%q) = %v, %v; want nil,nil", tc[0], tc[1], c, err)
		}
		if c.Enabled() {
			t.Error("nil client reports Enabled")
		}
	}
	if _, err := New("ftp://x", "t"); err == nil {
		t.Error("expected error for non-http base URL")
	}
	var c *Client
	if _, err := c.Me(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil client Me err = %v", err)
	}
}

func TestMe(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.URL.Path != "/api/me" || r.Method != "GET" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"id":1,"email":"a@b.c","base_url":"http://localhost:8080","api_token":{"id":3,"name":"n","read_only":false}}`)
	})
	me, err := c.Me(context.Background())
	if err != nil || me.BaseURL != "http://localhost:8080" || me.APIToken == nil || me.APIToken.Name != "n" {
		t.Fatalf("%+v %v", me, err)
	}
}

func TestGetCampaignAndCreate(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/campaigns/spring":
			io.WriteString(w, `{"id":2,"name":"spring","slug":"spring","link_count":4}`)
		case r.Method == "POST" && r.URL.Path == "/api/campaigns":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			if b["name"] != "new-one" || b["default_utm_source"] != "poster" {
				t.Errorf("body %v", b)
			}
			w.WriteHeader(201)
			io.WriteString(w, `{"id":9,"name":"new-one","slug":"new-one"}`)
		default:
			w.WriteHeader(404)
			io.WriteString(w, `{"error":"campaign not found"}`)
		}
	})
	ctx := context.Background()
	got, err := c.GetCampaign(ctx, "spring")
	if err != nil || got.LinkCount != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := c.GetCampaign(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	made, err := c.CreateCampaign(ctx, CreateCampaignRequest{Name: "new-one", DefaultUTMSource: "poster"})
	if err != nil || made.Slug != "new-one" {
		t.Fatalf("%+v %v", made, err)
	}
}

func TestEnsureCampaign(t *testing.T) {
	var posts int
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/campaigns/exists":
			io.WriteString(w, `{"id":1,"name":"exists","slug":"exists"}`)
		case r.Method == "GET":
			w.WriteHeader(404)
			io.WriteString(w, `{"error":"not found"}`)
		case r.Method == "POST":
			posts++
			var b CreateCampaignRequest
			json.NewDecoder(r.Body).Decode(&b)
			if b.Name != "fresh" || b.DefaultUTMMedium != "social" {
				t.Errorf("create body %+v", b)
			}
			w.WriteHeader(201)
			io.WriteString(w, `{"id":2,"name":"fresh","slug":"fresh"}`)
		}
	})
	ctx := context.Background()
	if got, err := c.EnsureCampaign(ctx, "exists", "exists", CreateCampaignRequest{}); err != nil || got.ID != 1 || posts != 0 {
		t.Fatalf("existing: %+v %v posts=%d", got, err, posts)
	}
	got, err := c.EnsureCampaign(ctx, "fresh", "fresh", CreateCampaignRequest{DefaultUTMMedium: "social"})
	if err != nil || got.Slug != "fresh" || posts != 1 {
		t.Fatalf("fresh: %+v %v posts=%d", got, err, posts)
	}
}

func TestEnsureCampaignPropagatesNon404(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			t.Error("must not create after a non-404 failure")
		}
		w.WriteHeader(500)
	})
	if _, err := c.EnsureCampaign(context.Background(), "x", "x", CreateCampaignRequest{}); err == nil {
		t.Fatal("want error")
	}
}

func TestCreateLinkAndGetLink(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/links":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			for k, v := range map[string]string{
				"destination_url": "https://x.test/w?utm_source=poster", "campaign_slug": "spring",
				"utm_source": "poster", "utm_medium": "print", "placement": "lobby", "key": "spr-lobby", "title": "T",
			} {
				if b[k] != v {
					t.Errorf("%s = %v, want %v", k, b[k], v)
				}
			}
			if _, ok := b["utm_term"]; ok {
				t.Error("empty fields must be omitted")
			}
			io.WriteString(w, `{"id":5,"key":"spr-lobby","duplicate":true}`)
		case r.Method == "GET" && r.URL.Path == "/api/links/spr-lobby":
			io.WriteString(w, `{"id":5,"key":"spr-lobby","click_count":7}`)
		default:
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	l, err := c.CreateLink(ctx, CreateLinkRequest{
		DestinationURL: "https://x.test/w?utm_source=poster", Title: "T", Key: "spr-lobby",
		CampaignSlug: "spring", UTMSource: "poster", UTMMedium: "print", Placement: "lobby",
	})
	if err != nil || l.Key != "spr-lobby" || l.Duplicate == nil || !*l.Duplicate {
		t.Fatalf("%+v %v", l, err)
	}
	g, err := c.GetLink(ctx, "spr-lobby")
	if err != nil || g.ClickCount != 7 {
		t.Fatalf("%+v %v", g, err)
	}
}

func TestLinkQRSVG(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.URL.Path != "/api/links/abc/qr.svg" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		io.WriteString(w, `<svg></svg>`)
	})
	b, err := c.LinkQRSVG(context.Background(), "abc")
	if err != nil || string(b) != "<svg></svg>" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestCampaignStatsWindow(t *testing.T) {
	var gotQuery string
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		gotQuery = r.URL.RawQuery
		if r.URL.Path != "/api/campaigns/spring/stats" {
			t.Errorf("path %s", r.URL.Path)
		}
		io.WriteString(w, `{"stats":{"click_count":3}}`)
	})
	ctx := context.Background()
	from := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	raw, err := c.CampaignStats(ctx, "spring", from, to)
	if err != nil || gotQuery != "from=2026-10-01&to=2026-10-09" || !strings.Contains(string(raw), "click_count") {
		t.Fatalf("%q %q %v", raw, gotQuery, err)
	}
	if _, err := c.CampaignStats(ctx, "spring", time.Time{}, time.Time{}); err != nil || gotQuery != "" {
		t.Fatalf("zero window query %q err %v", gotQuery, err)
	}
}

func TestErrorDecoding(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{401, ErrUnauthenticated}, {403, ErrForbidden}, {404, ErrNotFound}, {409, ErrConflict}, {429, ErrRateLimited},
	}
	for _, tc := range cases {
		c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
			if tc.status == 429 {
				w.Header().Set("Retry-After", "12")
			}
			w.WriteHeader(tc.status)
			io.WriteString(w, `{"error":"boom message"}`)
		})
		_, err := c.Me(context.Background())
		if !errors.Is(err, tc.want) {
			t.Errorf("%d: err = %v", tc.status, err)
			continue
		}
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != tc.status || ae.Message != "boom message" {
			t.Errorf("%d: APIError = %+v", tc.status, ae)
		}
		if tc.status == 429 && ae.RetryAfter != 12*time.Second {
			t.Errorf("RetryAfter = %v", ae.RetryAfter)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Errorf("token leaked: %v", err)
		}
	}
}

func TestTokenNeverInErrors(t *testing.T) {
	// Server echoing the Authorization header in its error body must not
	// matter to the client's own formatting for non-JSON bodies.
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, "plain text "+r.Header.Get("Authorization"))
	})
	_, err := c.Me(context.Background())
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("err = %v", err)
	}
	// Connection failure.
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	c2, _ := New(base, testToken)
	if _, err := c2.Me(context.Background()); err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("dial err = %v", err)
	}
}

func TestResponseSizeLimited(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, maxResponseBytes+10))
	})
	if _, err := c.LinkQRSVG(context.Background(), "k"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
}

func TestTimeoutBounded(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	if c.http.Timeout <= 0 {
		t.Fatal("client has no timeout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Me(ctx); err == nil {
		t.Fatal("want context error")
	}
}

// TestLiveMe is an optional smoke test against the configured local server.
// Skipped unless SHORTLINKS_LIVE_TEST=1; read-only (GET /api/me).
func TestLiveMe(t *testing.T) {
	if os.Getenv("SHORTLINKS_LIVE_TEST") != "1" {
		t.Skip("set SHORTLINKS_LIVE_TEST=1 with SHORTLINKS_BASE_URL and SHORTLINKS_API_TOKEN to run")
	}
	c, err := New(os.Getenv("SHORTLINKS_BASE_URL"), os.Getenv("SHORTLINKS_API_TOKEN"))
	if err != nil || c == nil {
		t.Skip("SHORTLINKS_BASE_URL / SHORTLINKS_API_TOKEN not set")
	}
	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.BaseURL == "" {
		t.Error("empty base_url")
	}
}
