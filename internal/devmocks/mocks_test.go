package devmocks_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/devmocks"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

func newMock(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(devmocks.New("http://127.0.0.1:1", nil, func(string, ...any) {}).Handler())
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestBlueskyRoundTrip(t *testing.T) {
	base := newMock(t)
	ctx := context.Background()
	c, err := bluesky.New(base+"/bluesky", "opencircuitsf.demo", "pw")
	if err != nil || c == nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.SetAppViewURL(base + "/bluesky"); err != nil {
		t.Fatal(err)
	}
	if h, err := c.VerifyCredentials(ctx); err != nil || h != "opencircuitsf.demo" {
		t.Fatalf("VerifyCredentials = %q, %v", h, err)
	}
	res, err := c.Post(ctx, bluesky.PostInput{Text: "LED workshop", RKey: "3mockrkey0001", CreatedAt: time.Now(),
		Embed: &bluesky.ExternalCard{URI: "http://x.test/a", Title: "t", Thumb: []byte("png"), ThumbMIME: "image/png"}})
	if err != nil || res.URI == "" || res.AlreadyExisted || res.ThumbErr != nil {
		t.Fatalf("Post = %+v, %v", res, err)
	}
	again, err := c.Post(ctx, bluesky.PostInput{Text: "LED workshop", RKey: "3mockrkey0001", CreatedAt: time.Now()})
	if err != nil || !again.AlreadyExisted {
		t.Fatalf("second Post = %+v, %v", again, err)
	}
	counts, err := c.GetPostCounts(ctx, []string{res.URI})
	if err != nil || counts[res.URI].Likes < 3 || counts[res.URI].Replies != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
	pg, err := c.ListNotifications(ctx, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var reply, mention bool
	for _, n := range pg.Items {
		reply = reply || n.Reason == "reply" && n.ReplyParent == res.URI
		mention = mention || n.Reason == "mention"
	}
	if !reply || !mention {
		t.Fatalf("notifications missing reply=%v mention=%v: %+v", reply, mention, pg.Items)
	}
	bad, _ := bluesky.New(base+"/bluesky", "opencircuitsf.demo", devmocks.BadToken)
	if _, err := bad.VerifyCredentials(ctx); !errors.Is(err, bluesky.ErrAuth) {
		t.Fatalf("bad token err = %v, want ErrAuth", err)
	}
}

func TestMastodonRoundTrip(t *testing.T) {
	base := newMock(t)
	ctx := context.Background()
	c, err := mastodon.New(base+"/mastodon", "tok")
	if err != nil || c == nil {
		t.Fatalf("New: %v", err)
	}
	if a, err := c.VerifyCredentials(ctx); err != nil || a.Acct != "opencircuitsf" {
		t.Fatalf("VerifyCredentials = %+v, %v", a, err)
	}
	st, err := c.PostStatus(ctx, "Join us https://go.example/abc", "key-1")
	if err != nil || st.ID == "" || !strings.Contains(st.URL, "/mastodon/@opencircuitsf/"+st.ID) {
		t.Fatalf("PostStatus = %+v, %v", st, err)
	}
	if dup, err := c.PostStatus(ctx, "ignored", "key-1"); err != nil || dup.ID != st.ID {
		t.Fatalf("idempotent PostStatus = %+v, %v", dup, err)
	}
	if found, ok, err := c.FindRecentStatusWithURL(ctx, "https://go.example/abc"); err != nil || !ok || found.ID != st.ID {
		t.Fatalf("FindRecentStatusWithURL = %+v %v %v", found, ok, err)
	}
	if n, err := c.GetStatusCounts(ctx, st.ID); err != nil || n.Likes < 4 || n.Replies != 1 {
		t.Fatalf("counts = %+v, %v", n, err)
	}
	items, err := c.ListNotifications(ctx, "", 40)
	if err != nil {
		t.Fatal(err)
	}
	var reply bool
	for _, n := range items {
		reply = reply || n.InReplyToID == st.ID
	}
	if !reply {
		t.Fatalf("no reply notification for %s: %+v", st.ID, items)
	}
	if newer, err := c.ListNotifications(ctx, items[0].ID, 40); err != nil || len(newer) != 0 {
		t.Fatalf("min_id page = %d items, %v", len(newer), err)
	}
	bad, _ := mastodon.New(base+"/mastodon", devmocks.BadToken)
	if _, err := bad.VerifyCredentials(ctx); !errors.Is(err, mastodon.ErrAuth) {
		t.Fatalf("bad token err = %v, want ErrAuth", err)
	}
}

func TestShortLinksRoundTrip(t *testing.T) {
	base := newMock(t)
	ctx := context.Background()
	c, err := shortlinks.New(base+"/shortlinks", "tok")
	if err != nil || c == nil {
		t.Fatalf("New: %v", err)
	}
	me, err := c.Me(ctx)
	if err != nil || !strings.HasSuffix(me.BaseURL, "/shortlinks/s") {
		t.Fatalf("Me = %+v, %v", me, err)
	}
	camp, err := c.EnsureCampaign(ctx, "led-programming", shortlinks.CreateCampaignRequest{})
	if err != nil || camp.Slug != "led-programming" {
		t.Fatalf("EnsureCampaign = %+v, %v", camp, err)
	}
	req := shortlinks.CreateLinkRequest{DestinationURL: "http://localhost:18080/workshops/led-programming", CampaignSlug: "led-programming",
		UTMSource: "bluesky", UTMMedium: "social", UTMContent: "announce", Placement: "bluesky"}
	l, err := c.CreateLink(ctx, req)
	if err != nil || l.Key == "" {
		t.Fatalf("CreateLink = %+v, %v", l, err)
	}
	if dup, err := c.CreateLink(ctx, req); err != nil || dup.Key != l.Key || dup.Duplicate == nil || !*dup.Duplicate {
		t.Fatalf("duplicate CreateLink = %+v, %v", dup, err)
	}
	req.Key = "fixed"
	if _, err := c.CreateLink(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateLink(ctx, req); !errors.Is(err, shortlinks.ErrConflict) {
		t.Fatalf("key reuse err = %v, want ErrConflict", err)
	}
	if _, err := c.GetLink(ctx, "nope"); !errors.Is(err, shortlinks.ErrNotFound) {
		t.Fatalf("GetLink missing err = %v", err)
	}
	svg, err := c.LinkQRSVG(ctx, l.Key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outreach.SanitizeQRSVG(svg); err != nil {
		t.Fatalf("QR is not accepted by the outreach sanitizer: %v", err)
	}
	// The short URL the site stores (#0603) redirects; the old /KEY form does not.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for u, want := range map[string]int{shortlinks.ShortURL(me.BaseURL, l.Key): http.StatusFound, me.BaseURL + "/" + l.Key: http.StatusNotFound} {
		// me.BaseURL carries the mock's advertised public host; dial the test server.
		resp, err := noFollow.Get(base + strings.TrimPrefix(u, "http://127.0.0.1:1"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("GET %s = %d, want %d", u, resp.StatusCode, want)
		}
	}
	st, err := c.CampaignStats(ctx, "led-programming", time.Time{}, time.Time{})
	if err != nil || st.ClickCount < 8 || len(st.ByLink) != 2 || len(st.SeriesByLink) != 2 {
		t.Fatalf("CampaignStats = %+v, %v", st, err)
	}
	var daySum int64
	for _, d := range st.Timeseries.Days {
		daySum += d.Count
	}
	if daySum != st.ClickCount {
		t.Fatalf("timeseries sums to %d, click_count %d", daySum, st.ClickCount)
	}
	// Every bucket lies inside the half-open window [WindowFrom, WindowTo),
	// or the SPA's chart drops it and its legend disagrees with the
	// per-link totals (#0590).
	for _, s := range st.SeriesByLink {
		for _, d := range s.Days {
			if d.Date < st.WindowFrom || d.Date >= st.WindowTo {
				t.Fatalf("series day %s outside [%s, %s)", d.Date, st.WindowFrom, st.WindowTo)
			}
		}
	}
	// The requested half-open window is honoured (#0593), buckets and all.
	from, to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	win, err := c.CampaignStats(ctx, "led-programming", from, to)
	if err != nil || win.WindowFrom != "2026-09-01" || win.WindowTo != "2026-09-11" {
		t.Fatalf("windowed CampaignStats = %+v, %v", win, err)
	}
	var winSum int64
	for _, d := range win.Timeseries.Days {
		winSum += d.Count
		if d.Date < win.WindowFrom || d.Date >= win.WindowTo {
			t.Fatalf("day %s outside [%s, %s)", d.Date, win.WindowFrom, win.WindowTo)
		}
	}
	if winSum != win.ClickCount {
		t.Fatalf("windowed timeseries sums to %d, click_count %d", winSum, win.ClickCount)
	}
	bad, _ := shortlinks.New(base+"/shortlinks", devmocks.BadToken)
	if _, err := bad.Me(ctx); !errors.Is(err, shortlinks.ErrUnauthenticated) {
		t.Fatalf("bad token err = %v, want ErrUnauthenticated", err)
	}
}

func TestRequireLoopback(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:18081": true, "localhost:1": true, "[::1]:9": true,
		"0.0.0.0:18081": false, ":18081": false, "192.168.64.1:18081": false, "example.com:80": false, "nonsense": false,
	} {
		if err := devmocks.RequireLoopback(addr); (err == nil) != ok {
			t.Errorf("RequireLoopback(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}

func TestRequireLocalDSN(t *testing.T) {
	for dsn, ok := range map[string]bool{
		"postgres://u:p@localhost:5432/opencircuit_demo?sslmode=disable": true,
		"postgres://u:p@127.0.0.1/x":                                     true,
		"postgres://u:p@db.example.com:5432/x":                           false,
		"postgres://u:p@10.0.0.5/x":                                      false,
		"postgres:///x":                                                  false,
		"postgres://u:p@localhost/x?host=db.example.com":                 false,
		"postgres://u:p@localhost/x?hostaddr=10.0.0.5":                   false,
		"": false,
	} {
		if err := devmocks.RequireLocalDSN(dsn); (err == nil) != ok {
			t.Errorf("RequireLocalDSN(%q) = %v, want ok=%v", dsn, err, ok)
		}
	}
}
