package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

type testResults struct {
	Status       string  `json:"status"`
	CampaignSlug *string `json:"campaign_slug"`
	Window       *struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"window"`
	PartialLastDay bool    `json:"partial_last_day"`
	FetchedAt      *string `json:"fetched_at"`
	Totals         *struct {
		Clicks            int64 `json:"clicks"`
		ExcludedBotClicks int64 `json:"excluded_bot_clicks"`
		UnmatchedClicks   int64 `json:"unmatched_clicks"`
	} `json:"totals"`
	Channels []struct {
		Name           string `json:"name"`
		Clicks         *int64 `json:"clicks"`
		HasDailySeries bool   `json:"has_daily_series"`
		Prepared       int    `json:"prepared"`
	} `json:"channels"`
	Posts []struct {
		ID           int64   `json:"id"`
		Channel      string  `json:"channel"`
		Wave         string  `json:"wave"`
		ShortLinkKey *string `json:"short_link_key"`
		Clicks       *int64  `json:"clicks"`
	} `json:"posts"`
	Series []struct {
		ID      string `json:"id"`
		IsOther bool   `json:"is_other"`
		Days    []struct {
			Date  string `json:"date"`
			Count int64  `json:"count"`
		} `json:"days"`
	} `json:"series"`
}

type testResultsSummary struct {
	Status string `json:"status"`
	Rows   []struct {
		WorkshopID  int64  `json:"workshop_id"`
		PostsTotal  int    `json:"posts_total"`
		PostsPosted int    `json:"posts_posted"`
		Clicks      *int64 `json:"clicks"`
		Status      string `json:"status"`
	} `json:"rows"`
}

// outreachResultsEnv starts outreach for a fresh published workshop with a
// prepared bluesky post (key k1), a prepared instagram post (key k2) and an
// unprepared mastodon draft.
func (e *outreachEnv) seedResultsOutreach(t *testing.T) (workshops.Workshop, testPanel) {
	t.Helper()
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	bsky := e.createPost(t, o.ID, "bluesky", "announce")
	ig := e.createPost(t, o.ID, "instagram", "reminder")
	e.createPost(t, o.ID, "mastodon", "announce")
	for _, id := range []int64{bsky.ID, ig.ID} {
		if code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-posts/%d/prepare", id), ""); code != http.StatusOK {
			t.Fatalf("prepare = %d (%s)", code, b)
		}
	}
	code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("panel = %d", code)
	}
	return w, decodeOutreach[testPanel](t, b)
}

func (e *outreachEnv) results(t *testing.T, w workshops.Workshop) testResults {
	t.Helper()
	code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach/results", w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("results status = %d (%s)", code, b)
	}
	return decodeOutreach[testResults](t, b)
}

func TestAdminOutreachResults_AuthGuards(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	for _, path := range []string{fmt.Sprintf("/admin/workshops/%d/outreach/results", w.ID), "/admin/outreach-results"} {
		for token, want := range map[string]int{"": http.StatusUnauthorized, "outreach-user-token": http.StatusForbidden} {
			resp := doJSON(t, e.srv.Client(), http.MethodGet, e.srv.URL+path, token, "")
			resp.Body.Close()
			if resp.StatusCode != want {
				t.Errorf("GET %s token=%q status = %d, want %d", path, token, resp.StatusCode, want)
			}
		}
	}
	if len(e.links.statsCalls) != 0 {
		t.Error("an unauthorized request reached ShortLinks")
	}
}

func TestAdminOutreachResults_UnknownAndNotStarted(t *testing.T) {
	e := newOutreachEnv(t, true)
	if code, _ := e.do(t, http.MethodGet, "/admin/workshops/999999999/outreach/results", ""); code != http.StatusNotFound {
		t.Errorf("unknown workshop = %d, want 404", code)
	}
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	r := e.results(t, w)
	if r.Status != "not_started" || r.Window != nil || r.Totals != nil || len(r.Posts) != 0 || r.Series == nil || len(r.Channels) != 5 {
		t.Errorf("not started = %+v", r)
	}
	if len(e.links.statsCalls) != 0 {
		t.Error("not_started must not call ShortLinks")
	}
}

func TestAdminOutreachResults_NotConfiguredStillListsPosts(t *testing.T) {
	e := newOutreachEnv(t, false)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	store := outreach.NewStore(e.pool)
	o, _, err := store.Create(context.Background(), w.ID, w.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePost(context.Background(), o.ID, "bluesky", "announce", "hello", "fp"); err != nil {
		t.Fatal(err)
	}
	r := e.results(t, w)
	if r.Status != "not_configured" || len(r.Posts) != 1 || r.Posts[0].Clicks != nil || r.Totals != nil || len(r.Series) != 0 {
		t.Errorf("not configured = %+v", r)
	}
	code, b := e.do(t, http.MethodGet, "/admin/outreach-results", "")
	if code != http.StatusOK {
		t.Fatalf("summary = %d", code)
	}
	sum := decodeOutreach[testResultsSummary](t, b)
	if sum.Status != "not_configured" {
		t.Errorf("summary status = %q", sum.Status)
	}
}

func TestAdminOutreachResults_UpstreamFailuresNeverFailTheRequest(t *testing.T) {
	secret := "upstream-secret-text-9f3a"
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"not found", fmt.Errorf("%s: %w", secret, shortlinks.ErrNotFound), "campaign_missing"},
		{"unauthenticated", fmt.Errorf("%s: %w", secret, shortlinks.ErrUnauthenticated), "rejected"},
		{"forbidden", fmt.Errorf("%s: %w", secret, shortlinks.ErrForbidden), "rejected"},
		{"plain error", errors.New(secret), "unreachable"},
		{"rate limited", fmt.Errorf("%s: %w", secret, shortlinks.ErrRateLimited), "unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newOutreachEnv(t, true)
			w, _ := e.seedResultsOutreach(t)
			e.links.statsErr = tc.err
			code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach/results", w.ID), "")
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
			if strings.Contains(string(b), secret) {
				t.Errorf("upstream text leaked: %s", b)
			}
			r := decodeOutreach[testResults](t, b)
			if r.Status != tc.want || len(r.Posts) != 3 || r.Totals != nil || r.Window == nil {
				t.Errorf("result = %+v", r)
			}
			for _, p := range r.Posts {
				if p.Clicks != nil {
					t.Errorf("post %d has clicks on a failed fetch", p.ID)
				}
			}
		})
	}
}

func TestAdminOutreachResults_OKMapsClicksToPostsAndAsksForTheResultsWindow(t *testing.T) {
	e := newOutreachEnv(t, true)
	w, panel := e.seedResultsOutreach(t)
	e.links.statsRollup = &shortlinks.CampaignRollup{
		ClickCount: 14, ExcludedBotCount: 2, WindowFrom: "2026-10-01", WindowTo: "2026-10-17",
		ByLink: []shortlinks.LinkBucket{{Key: "k1", Count: 7}, {Key: "k2", Count: 3}, {Key: "other-link", Count: 4}},
		SeriesByLink: []shortlinks.LinkSeries{
			{Key: "k1", Days: []shortlinks.DayBucket{{Date: "2026-10-02", Count: 7}}},
			{Key: "k2", Days: []shortlinks.DayBucket{{Date: "2026-10-03", Count: 3}}},
			{Key: "other-link", Days: []shortlinks.DayBucket{{Date: "2026-10-03", Count: 4}}},
		},
	}
	before := time.Now()
	r := e.results(t, w)
	after := time.Now()
	if r.Status != "ok" || r.Totals == nil || r.Totals.Clicks != 14 || r.Totals.ExcludedBotClicks != 2 || r.Totals.UnmatchedClicks != 4 {
		t.Fatalf("result = %+v", r)
	}
	if r.Window == nil || r.Window.From != "2026-10-01" || r.Window.To != "2026-10-17" || r.FetchedAt == nil {
		t.Errorf("window/fetched = %+v %v", r.Window, r.FetchedAt)
	}
	byChannel := map[string]*int64{}
	for _, p := range r.Posts {
		byChannel[p.Channel] = p.Clicks
	}
	if byChannel["bluesky"] == nil || *byChannel["bluesky"] != 7 || byChannel["instagram"] == nil || *byChannel["instagram"] != 3 || byChannel["mastodon"] != nil {
		t.Errorf("post clicks = %v", byChannel)
	}
	if r.Channels[0].Name != "bluesky" || r.Channels[0].Clicks == nil || *r.Channels[0].Clicks != 7 || !r.Channels[0].HasDailySeries {
		t.Errorf("channels = %+v", r.Channels)
	}
	var ids []string
	for _, s := range r.Series {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "bluesky,instagram,other" {
		t.Errorf("series = %v", ids)
	}

	// The request carries ResultsWindow's explicit window, never ShortLinks'
	// default (the workshop session itself).
	if len(e.links.statsCalls) != 1 {
		t.Fatalf("stats calls = %d", len(e.links.statsCalls))
	}
	o, err := outreach.NewStore(e.pool).GetByWorkshop(context.Background(), w.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantFrom, wantTo := outreach.ResultsWindow(o.CreatedAt, w.StartsAt, before)
	_, wantTo2 := outreach.ResultsWindow(o.CreatedAt, w.StartsAt, after)
	got := e.links.statsCalls[0]
	if got.slug != panel.Outreach.CampaignSlug || !got.from.Equal(wantFrom) || (!got.to.Equal(wantTo) && !got.to.Equal(wantTo2)) {
		t.Errorf("stats request = %+v, want %s..%s", got, wantFrom, wantTo)
	}
	if got.from.IsZero() || got.to.IsZero() {
		t.Error("the request left a bound to ShortLinks' default window")
	}

	// A second request inside the TTL is served from the cache.
	e.results(t, w)
	if len(e.links.statsCalls) != 1 {
		t.Errorf("stats calls after a repeat = %d, want 1", len(e.links.statsCalls))
	}
}

func TestAdminOutreachResults_SummaryShortCircuitsOnUnreachable(t *testing.T) {
	e := newOutreachEnv(t, true)
	e.seedResultsOutreach(t)
	e.seedResultsOutreach(t)
	e.links.statsErr = errors.New("down")
	code, b := e.do(t, http.MethodGet, "/admin/outreach-results", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	sum := decodeOutreach[testResultsSummary](t, b)
	if sum.Status != "unreachable" || len(sum.Rows) < 2 {
		t.Fatalf("summary = %+v", sum)
	}
	for _, row := range sum.Rows {
		if row.Status != "unreachable" || row.Clicks != nil {
			t.Errorf("row = %+v", row)
		}
	}
	if len(e.links.statsCalls) != 1 {
		t.Errorf("ShortLinks was called %d times, want 1", len(e.links.statsCalls))
	}
}

func TestAdminOutreachResults_SummaryOK(t *testing.T) {
	e := newOutreachEnv(t, true)
	w, _ := e.seedResultsOutreach(t)
	e.links.statsRollup = &shortlinks.CampaignRollup{ClickCount: 21}
	code, b := e.do(t, http.MethodGet, "/admin/outreach-results", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	sum := decodeOutreach[testResultsSummary](t, b)
	if sum.Status != "ok" {
		t.Fatalf("summary = %+v", sum)
	}
	found := false
	for _, row := range sum.Rows {
		if row.WorkshopID == w.ID {
			found = true
			if row.Clicks == nil || *row.Clicks != 21 || row.PostsTotal != 3 || row.PostsPosted != 0 || row.Status != "ok" {
				t.Errorf("row = %+v", row)
			}
		}
	}
	if !found {
		t.Error("workshop row missing")
	}
}

// testEngagement decodes the #0547 additions to the results responses.
type testEngagement struct {
	Reactions *struct{ Likes, Reposts, Replies, Quotes int } `json:"reactions"`
	Channels  []struct {
		Name      string                                         `json:"name"`
		Reactions *struct{ Likes, Reposts, Replies, Quotes int } `json:"reactions"`
	} `json:"channels"`
	Posts []struct {
		ID         int64 `json:"id"`
		Engagement *struct {
			Likes, Reposts, Replies, Quotes int
			CapturedAt                      string `json:"captured_at"`
		} `json:"engagement"`
	} `json:"posts"`
	Mentions []struct {
		ID       int64   `json:"id"`
		Kind     string  `json:"kind"`
		Author   string  `json:"author"`
		Text     string  `json:"text"`
		URL      *string `json:"url"`
		PostID   *int64  `json:"post_id"`
		Wave     *string `json:"wave"`
		Workshop *int64  `json:"workshop_id"`
	} `json:"mentions"`
}

type testEngagementSummary struct {
	Rows []struct {
		WorkshopID int64                                          `json:"workshop_id"`
		Reactions  *struct{ Likes, Reposts, Replies, Quotes int } `json:"reactions"`
	} `json:"rows"`
	Mentions []struct {
		Text     string  `json:"text"`
		PostID   *int64  `json:"post_id"`
		Workshop *int64  `json:"workshop_id"`
		Title    *string `json:"workshop_title"`
	} `json:"mentions"`
}

// TestAdminOutreachResults_EngagementDoesNotNeedShortLinks proves the
// engagement, reaction totals and mentions come from our own database: a
// deployment with no ShortLinks client still carries them (#0547).
func TestAdminOutreachResults_EngagementDoesNotNeedShortLinks(t *testing.T) {
	e := newOutreachEnv(t, false)
	ctx := context.Background()
	store := outreach.NewStore(e.pool)

	// not_started: mentions is an empty array, reactions null.
	empty := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	code, b := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach/results", empty.ID), "")
	if code != http.StatusOK || !strings.Contains(string(b), `"mentions":[]`) || !strings.Contains(string(b), `"reactions":null`) {
		t.Errorf("not_started = %d %s", code, b)
	}

	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o, _, err := store.Create(ctx, w.ID, w.Slug)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := store.CreatePost(ctx, o.ID, "bluesky", "announce", "hello", "fp")
	if err != nil {
		t.Fatal(err)
	}
	pm, err := store.CreatePost(ctx, o.ID, "mastodon", "announce", "hello", "fp")
	if err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("hx%dy", time.Now().UnixNano())
	for _, id := range []int64{pb.ID, pm.ID} {
		if _, err := e.pool.Exec(ctx, `UPDATE outreach_posts SET status='posted', posted_at=now(), remote_id=$2 WHERE id=$1`, id, fmt.Sprintf("r%d%s", id, tag)); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(ctx, `DELETE FROM outreach_mentions WHERE remote_id LIKE '%' || $1 || '%'`, tag)
	})
	now := time.Now()
	store.RecordEngagement(ctx, pb.ID, now.Add(-time.Hour), outreach.Counts{Likes: 1})
	store.RecordEngagement(ctx, pb.ID, now, outreach.Counts{Likes: 3, Reposts: 1})
	store.RecordEngagement(ctx, pm.ID, now, outreach.Counts{Likes: 2, Replies: 2})
	link := "https://bsky.app/profile/alice.example.test/post/1"
	if _, err := store.InsertMention(ctx, outreach.Mention{Channel: "bluesky", RemoteID: "tied" + tag, PostID: &pb.ID, Kind: "reply", Author: "@alice.example.test", Text: "<b>see you</b>", URL: &link, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertMention(ctx, outreach.Mention{Channel: "bluesky", RemoteID: "untied" + tag, Kind: "mention", Author: "@bob.example.test", Text: "untied " + tag, CreatedAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}

	code, b = e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach/results", w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("results = %d", code)
	}
	var base testResults
	json.Unmarshal(b, &base)
	if base.Status != "not_configured" {
		t.Fatalf("status = %q, want not_configured", base.Status)
	}
	r := decodeOutreach[testEngagement](t, b)
	if r.Reactions == nil || *r.Reactions != (struct{ Likes, Reposts, Replies, Quotes int }{5, 1, 2, 0}) {
		t.Errorf("workshop reactions = %+v, want latest-per-post sum 5/1/2/0", r.Reactions)
	}
	for _, p := range r.Posts {
		if p.ID == pb.ID && (p.Engagement == nil || p.Engagement.Likes != 3 || p.Engagement.CapturedAt == "") {
			t.Errorf("bluesky post engagement = %+v", p.Engagement)
		}
	}
	for _, c := range r.Channels {
		switch c.Name {
		case "bluesky":
			if c.Reactions == nil || c.Reactions.Likes != 3 {
				t.Errorf("bluesky channel reactions = %+v", c.Reactions)
			}
		case "instagram":
			if c.Reactions != nil {
				t.Errorf("a channel with no snapshots must report null reactions: %+v", c.Reactions)
			}
		}
	}
	if len(r.Mentions) != 1 || r.Mentions[0].Kind != "reply" || r.Mentions[0].PostID == nil || *r.Mentions[0].PostID != pb.ID ||
		r.Mentions[0].Wave == nil || *r.Mentions[0].Wave != "announce" || r.Mentions[0].Text != "<b>see you</b>" {
		t.Errorf("mentions = %+v (only the tied one belongs to this workshop; text is returned verbatim for the SPA to render as text)", r.Mentions)
	}

	code, b = e.do(t, http.MethodGet, "/admin/outreach-results", "")
	if code != http.StatusOK {
		t.Fatalf("summary = %d", code)
	}
	sum := decodeOutreach[testEngagementSummary](t, b)
	var rowFound, untied bool
	for _, row := range sum.Rows {
		if row.WorkshopID == w.ID {
			rowFound = row.Reactions != nil && row.Reactions.Likes == 5
		}
	}
	for _, m := range sum.Mentions {
		if strings.Contains(m.Text, "untied "+tag) {
			untied = m.PostID == nil && m.Workshop == nil && m.Title == nil
		}
	}
	if !rowFound || !untied {
		t.Errorf("summary: row reactions ok=%v, untied mention ok=%v (%s)", rowFound, untied, b)
	}
}

// #0552: a client-cancelled request still reports unreachable but is not
// logged as a ShortLinks failure.
func TestClassifyResultsError_CancelledIsNotLogged(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	if got := classifyResultsError("results summary", fmt.Errorf("get: %w", context.Canceled)); got != resultsUnreachable {
		t.Errorf("status = %q, want unreachable", got)
	}
	if buf.Len() != 0 {
		t.Errorf("cancelled request was logged: %s", buf.String())
	}
	classifyResultsError("results summary", errors.New("boom"))
	if !strings.Contains(buf.String(), "ShortLinks stats failed") {
		t.Errorf("a real failure must still be logged, got %q", buf.String())
	}
}
