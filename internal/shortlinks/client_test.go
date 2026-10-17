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
	if got, err := c.EnsureCampaign(ctx, "exists", CreateCampaignRequest{Name: "Ignored"}); err != nil || got.ID != 1 || posts != 0 {
		t.Fatalf("existing: %+v %v posts=%d", got, err, posts)
	}
	got, err := c.EnsureCampaign(ctx, "fresh", CreateCampaignRequest{Name: "Fresh Display Name", Description: "Fresh Display Name", DefaultUTMMedium: "social"})
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
	if _, err := c.EnsureCampaign(context.Background(), "x", CreateCampaignRequest{}); err == nil {
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
		io.WriteString(w, `{"click_count":42,"excluded_bot_count":3,"window_from":"2026-10-01","window_to":"2026-10-09",
"timeseries":{"days":[{"date":"2026-10-03","count":5}]},
"by_link":[{"link_id":7,"key":"aB3x","title":"t","count":12}],
"series_by_link":[{"link_id":7,"key":"aB3x","title":"t","is_other":false,"days":[{"date":"2026-10-03","count":5}]},
{"link_id":null,"key":"","title":"Other","is_other":true,"days":[]}]}`)
	})
	ctx := context.Background()
	from := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	r, err := c.CampaignStats(ctx, "spring", from, to)
	if err != nil || gotQuery != "from=2026-10-01&to=2026-10-09" {
		t.Fatalf("%q %v", gotQuery, err)
	}
	if r.ClickCount != 42 || r.ExcludedBotCount != 3 || r.WindowTo != "2026-10-09" || len(r.ByLink) != 1 || r.ByLink[0].Key != "aB3x" {
		t.Fatalf("decoded %+v", r)
	}
	if len(r.SeriesByLink) != 2 || !r.SeriesByLink[1].IsOther || r.SeriesByLink[0].Days[0].Count != 5 || len(r.Timeseries.Days) != 1 {
		t.Fatalf("series %+v", r.SeriesByLink)
	}
	if _, err := c.CampaignStats(ctx, "spring", time.Time{}, time.Time{}); err != nil || gotQuery != "" {
		t.Fatalf("zero window query %q err %v", gotQuery, err)
	}
}

func TestCampaignStatsDecodeError(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"click_count":"x"}`)
	})
	_, err := c.CampaignStats(context.Background(), "spring", time.Time{}, time.Time{})
	if err == nil {
		t.Fatal("expected a decode error")
	}
	for _, s := range []error{ErrUnauthenticated, ErrForbidden, ErrNotFound, ErrConflict, ErrRateLimited, ErrUnavailable} {
		if errors.Is(err, s) {
			t.Fatalf("decode error matched sentinel %v", s)
		}
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
	// JSON error body echoing the token.
	c3 := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"bad header `+r.Header.Get("Authorization")+` seen"}`)
	})
	_, err = c3.Me(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || strings.Contains(err.Error(), testToken) || strings.Contains(ae.Message, testToken) || !strings.Contains(ae.Message, "bad header") {
		t.Fatalf("json err = %v", err)
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

func TestEnsureCampaignNamesCampaignWithSlug(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.WriteHeader(404)
			return
		}
		var b CreateCampaignRequest
		json.NewDecoder(r.Body).Decode(&b)
		if b.Name != "spring-2027" || b.Description != "Spring 2027 posters" {
			t.Errorf("body %+v", b)
		}
		w.WriteHeader(201)
		io.WriteString(w, `{"id":3,"name":"spring-2027","slug":"spring-2027"}`)
	})
	got, err := c.EnsureCampaign(context.Background(), "spring-2027", CreateCampaignRequest{Name: "Spring 2027!", Description: "Spring 2027 posters"})
	if err != nil || got.Slug != "spring-2027" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestEnsureLink(t *testing.T) {
	const dest = "https://x.test/w"
	run := func(reqDest, existingDest string, getStatus int) (*Link, error, int) {
		var posts int
		c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == "POST":
				posts++
				if posts == 1 {
					w.WriteHeader(201)
					io.WriteString(w, `{"id":5,"key":"k1","destination_url":"`+dest+`"}`)
					return
				}
				w.WriteHeader(409)
				io.WriteString(w, `{"error":"key taken"}`)
			case r.Method == "GET":
				if getStatus != 200 {
					w.WriteHeader(getStatus)
					return
				}
				io.WriteString(w, `{"id":5,"key":"k1","destination_url":"`+existingDest+`"}`)
			}
		})
		req := CreateLinkRequest{DestinationURL: reqDest, Key: "k1"}
		if _, err := c.EnsureLink(context.Background(), req); err != nil {
			t.Fatalf("first create: %v", err)
		}
		l, err := c.EnsureLink(context.Background(), req)
		return l, err, posts
	}
	if l, err, posts := run(dest, dest, 200); err != nil || l.ID != 5 || posts != 2 {
		t.Fatalf("matching retry: %+v %v posts=%d", l, err, posts)
	}
	if _, err, _ := run(dest, "https://other.test/", 200); !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched destination: %v", err)
	}
	if _, err, _ := run(dest, dest, 500); err == nil || errors.Is(err, ErrConflict) {
		t.Fatalf("GET failure: %v", err)
	}
	if _, err, _ := run(dest, dest, 404); !errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		t.Fatalf("GET 404 after 409 must stay ErrConflict: %v", err)
	}
	if l, err, _ := run("  "+dest+"\n", dest, 200); err != nil || l.ID != 5 {
		t.Fatalf("padded destination retry: %+v %v", l, err)
	}
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	if _, err := c.EnsureLink(context.Background(), CreateLinkRequest{DestinationURL: dest}); err == nil {
		t.Fatal("want error without Key")
	}
}
