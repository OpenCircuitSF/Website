package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/guideposts"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

type postPerfEnv struct {
	*v1OutreachWriteEnv
	guides *guidePostsEnv
}

// newPostPerfEnv serves get_post_performance through the full v1 registry,
// with both the outreach and the guide-post handlers wired.
func newPostPerfEnv(t *testing.T) *postPerfEnv {
	t.Helper()
	// Each env truncates the credential tables, so the guide env goes first
	// and its admin session is seeded again after the outreach env's.
	ge := newGuidePostsEnv(t, true, nil)
	oe := newOutreachEnv(t, true)
	seedSession(t, oe.pool, seedAdmin(t, oe.pool, "admin-guideposts@example.com"), "guideposts-admin-token")
	reg := BuildV1Registry(api.Config{Version: "test", Idempotency: api.NewIdempotencyStore(oe.pool)}, V1Deps{Outreach: oe.h, GuidePosts: ge.h})
	h := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(oe.pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})
	read, _ := v1TestKey(t, oe.pool, apitokens.ScopeEngagementRead)
	none, _ := v1TestKey(t, oe.pool, apitokens.ScopeOutreachRead)
	return &postPerfEnv{v1OutreachWriteEnv: &v1OutreachWriteEnv{outreachEnv: oe, api: h, read: read, write: none}, guides: ge}
}

// seed posts one event post (bluesky, now) and one 30 days ago (instagram,
// outside the default window), and one guide post now, with snapshots.
func (e *postPerfEnv) seed(t *testing.T) (eventPost, oldPost, guidePost int64) {
	t.Helper()
	ctx := context.Background()
	_, panel := e.seedResultsOutreach(t)
	for _, p := range panel.Posts {
		switch p.Channel {
		case "bluesky":
			eventPost = p.ID
		case "instagram":
			oldPost = p.ID
		}
	}
	if _, err := e.pool.Exec(ctx, `UPDATE outreach_posts SET status='posted', posted_at=now() WHERE id=$1`, eventPost); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE outreach_posts SET status='posted', posted_at=now() - interval '30 days' WHERE id=$1`, oldPost); err != nil {
		t.Fatal(err)
	}
	if err := outreach.NewStore(e.pool).RecordEngagement(ctx, eventPost, time.Now(), outreach.Counts{Likes: 4, Reposts: 2, Replies: 1, Quotes: 3}); err != nil {
		t.Fatal(err)
	}
	g := e.guides.create(t, "mastodon", "c-perf")
	guidePost = g.Post.ID
	e.guides.markPosted(t, guidePost)
	gs := guideposts.NewStore(e.pool, outbox.NewStore(e.pool))
	if err := gs.RecordEngagement(ctx, guidePost, time.Now(), outreach.Counts{Likes: 10, Replies: 2}); err != nil {
		t.Fatal(err)
	}
	e.guides.links.statsByCampaign = map[string]*shortlinks.CampaignRollup{"c-perf": rollup(*g.Post.ShortLinkKey, 6)}
	return eventPost, oldPost, guidePost
}

func (e *postPerfEnv) get(t *testing.T, query string, want int) (postPerformanceView, []byte) {
	t.Helper()
	b := e.v1(t, http.MethodGet, "/api/v1/posts/performance"+query, e.read, "", want)
	var v postPerformanceView
	if want == http.StatusOK {
		assertPacific(t, b)
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
	}
	return v, b
}

func TestV1PostPerformance_EventsAndGuides(t *testing.T) {
	e := newPostPerfEnv(t)
	e.links.statsRollup = &shortlinks.CampaignRollup{
		ClickCount: 9, WindowFrom: "2026-10-01", WindowTo: "2026-10-17",
		ByLink: []shortlinks.LinkBucket{{Key: "k1", Count: 7}, {Key: "k2", Count: 2}},
	}
	eventPost, oldPost, guidePost := e.seed(t)

	e.v1(t, http.MethodGet, "/api/v1/posts/performance", e.write, "", http.StatusForbidden)
	v, b := e.get(t, "", http.StatusOK)
	if len(v.Posts) != 2 || v.ClicksError != nil {
		t.Fatalf("posts = %s", b)
	}
	if strings.Contains(string(b), "instagram") {
		t.Errorf("post %d from 30 days ago is in the default window: %s", oldPost, b)
	}
	for _, r := range v.Posts {
		switch r.Kind {
		case "event":
			if r.Channel != "bluesky" || r.Wave == nil || *r.Wave != "announce" || r.Likes != 4 || r.Quotes != 3 ||
				r.Clicks == nil || *r.Clicks != 7 || r.CapturedAt == nil || r.Title == "" {
				t.Errorf("event row %d = %+v", eventPost, r)
			}
		case "guide":
			if r.Channel != "mastodon" || r.Likes != 10 || r.Replies != 2 || r.Clicks == nil || *r.Clicks != 6 || r.CapturedAt == nil || r.Wave != nil {
				t.Errorf("guide row %d = %+v", guidePost, r)
			}
		}
	}
	if v.Total.Posts != 2 || v.Total.Likes != 14 || v.Total.Clicks != 13 ||
		v.ByKind["event"].Posts != 1 || v.ByKind["guide"].Clicks != 6 || v.ByChannel["bluesky"].Likes != 4 {
		t.Errorf("totals = %+v %+v %+v", v.Total, v.ByKind, v.ByChannel)
	}
	if !strings.HasPrefix(v.Summary, "2 posts (1 event, 1 guide) have 14 likes") || !strings.HasSuffix(v.Summary, "13 clicks.") {
		t.Errorf("summary = %q", v.Summary)
	}

	// A wider window brings the older post in; kind narrows.
	since := time.Now().AddDate(0, 0, -40).Format(time.RFC3339)
	if v, _ := e.get(t, "?since="+since, http.StatusOK); len(v.Posts) != 3 {
		t.Errorf("40-day window posts = %d, want 3", len(v.Posts))
	}
	if v, _ := e.get(t, "?kind=guide", http.StatusOK); len(v.Posts) != 1 || v.Posts[0].Kind != "guide" {
		t.Errorf("kind=guide = %+v", v.Posts)
	}
	if _, b := e.get(t, "?kind=newsletter", http.StatusBadRequest); !strings.Contains(string(b), "event, guide or all") {
		t.Errorf("bad kind = %s", b)
	}
	old := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	if _, b := e.get(t, "?since="+old, http.StatusBadRequest); !strings.Contains(string(b), "90 days") {
		t.Errorf("long window = %s", b)
	}
}

// ShortLinks down still answers 200: clicks null, clicks_error named, and no
// underlying error text.
func TestV1PostPerformance_ShortLinksDown(t *testing.T) {
	e := newPostPerfEnv(t)
	e.links.statsErr = shortlinks.ErrUnauthenticated
	e.seed(t)
	e.guides.links.statsByCampaign = nil
	e.guides.links.statsErr = shortlinks.ErrUnauthenticated
	v, b := e.get(t, "", http.StatusOK)
	if len(v.Posts) != 2 || v.ClicksError == nil || *v.ClicksError != "rejected" {
		t.Fatalf("body = %s", b)
	}
	for _, r := range v.Posts {
		if r.Clicks != nil || r.Likes == 0 {
			t.Errorf("row = %+v", r)
		}
	}
	if !strings.HasSuffix(v.Summary, "clicks unavailable.") || strings.Contains(string(b), "unauthenticated") {
		t.Errorf("summary = %q / %s", v.Summary, b)
	}
}
