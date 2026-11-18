package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/guideposts"
	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// guidePostEngagementReader reads each guide post's latest snapshot (#0704);
// *guideposts.Store. It is asserted on the guide-post store rather than added
// to guidePostStore, so the admin handler's fakes need not grow.
type guidePostEngagementReader interface {
	LatestEngagement(ctx context.Context, postIDs []int64) (map[int64]outreach.Engagement, error)
}

// The real store must keep satisfying it, or guideRows silently drops every
// guide post's engagement (#0708); the fakes deliberately do not.
var _ guidePostEngagementReader = (*guideposts.Store)(nil)

// postPerformanceOp runs get_post_performance (#0706): the cumulative results
// of the event and guide posts made in a window. now is a field so tests can
// pin the clock.
type postPerformanceOp struct {
	events *AdminOutreachHandler
	guides *AdminGuidePostsHandler
	now    func() time.Time
}

type postPerformanceIn struct {
	Since *string `json:"since,omitempty" query:"since" jsonschema:"window start, RFC 3339 (default 7 days before until), e.g. 2026-11-10T00:00:00-08:00"`
	Until *string `json:"until,omitempty" query:"until" jsonschema:"window end, RFC 3339 (default now); the window is at most 90 days"`
	Kind  *string `json:"kind,omitempty" query:"kind" jsonschema:"event, guide or all (default all)"`
}

type postPerformanceRow struct {
	Kind       string  `json:"kind"`
	Title      string  `json:"title"`
	Slug       string  `json:"slug"`
	Wave       *string `json:"wave"`
	Channel    string  `json:"channel"`
	RemoteURL  *string `json:"remote_url"`
	PostedAt   string  `json:"posted_at"`
	Likes      int     `json:"likes"`
	Reposts    int     `json:"reposts"`
	Replies    int     `json:"replies"`
	Quotes     int     `json:"quotes"`
	CapturedAt *string `json:"captured_at"`
	Clicks     *int64  `json:"clicks"`
}

type postPerformanceTotals struct {
	Posts   int   `json:"posts"`
	Likes   int   `json:"likes"`
	Reposts int   `json:"reposts"`
	Replies int   `json:"replies"`
	Quotes  int   `json:"quotes"`
	Clicks  int64 `json:"clicks"`
}

func (t *postPerformanceTotals) add(r postPerformanceRow) {
	t.Posts++
	t.Likes += r.Likes
	t.Reposts += r.Reposts
	t.Replies += r.Replies
	t.Quotes += r.Quotes
	if r.Clicks != nil {
		t.Clicks += *r.Clicks
	}
}

type postPerformanceView struct {
	Since       string                           `json:"since"`
	Until       string                           `json:"until"`
	Kind        string                           `json:"kind"`
	Posts       []postPerformanceRow             `json:"posts"`
	Total       postPerformanceTotals            `json:"total"`
	ByChannel   map[string]postPerformanceTotals `json:"by_channel"`
	ByKind      map[string]postPerformanceTotals `json:"by_kind"`
	ClicksError *string                          `json:"clicks_error"`
	Summary     string                           `json:"summary"`
}

func postPerformanceOperations(d V1Deps) []api.Operation {
	op := &postPerformanceOp{events: d.Outreach, guides: d.GuidePosts, now: time.Now}
	return []api.Operation{postPerformanceOperation(op)}
}

func postPerformanceOperation(op *postPerformanceOp) api.Operation {
	return api.Define(api.Operation{
		Name: "get_post_performance", Method: http.MethodGet, Path: "/api/v1/posts/performance",
		Scope:   apitokens.ScopeEngagementRead,
		Summary: "How the event and guide posts made in a window did (default the last 7 days): each post's latest likes, reposts, replies and quotes, and its clicks.",
		Description: "Unlike get_engagement, which counts new activity in a window, this gives the cumulative results of the posts posted in the window, " +
			"with totals per channel and per kind and a one-sentence summary. A ShortLinks failure never fails the call: clicks are null and clicks_error " +
			"names why (not_configured, rejected, unreachable, failed, ...). Times have a Pacific offset.",
		MayBeUnavailable: true,
	}, op.run)
}

func (op *postPerformanceOp) run(ctx context.Context, in postPerformanceIn) (postPerformanceView, error) {
	bad := func(field, msg string) (postPerformanceView, error) {
		return postPerformanceView{}, api.Validation(map[string]string{field: msg})
	}
	kind := "all"
	if in.Kind != nil && *in.Kind != "" {
		kind = strings.ToLower(strings.TrimSpace(*in.Kind))
	}
	if kind != "all" && kind != "event" && kind != "guide" {
		return bad("kind", "kind must be event, guide or all")
	}
	until := op.now()
	if in.Until != nil {
		u, err := time.Parse(time.RFC3339, *in.Until)
		if err != nil {
			return bad("until", "until must be an RFC 3339 time, e.g. 2026-11-17T00:00:00-08:00")
		}
		until = u
	}
	since := until.AddDate(0, 0, -7)
	if in.Since != nil {
		s, err := time.Parse(time.RFC3339, *in.Since)
		if err != nil {
			return bad("since", "since must be an RFC 3339 time, e.g. 2026-11-10T00:00:00-08:00")
		}
		since = s
	}
	if !since.Before(until) {
		return bad("since", "since must be before until")
	}
	if until.Sub(since) > 90*24*time.Hour {
		return bad("since", "the window is at most 90 days")
	}
	inWindow := func(t *time.Time) bool { return t != nil && !t.Before(since) && !t.After(until) }

	v := postPerformanceView{
		Since: pacificRFC3339(since.UTC().Format(time.RFC3339)), Until: pacificRFC3339(until.UTC().Format(time.RFC3339)),
		Kind: kind, Posts: []postPerformanceRow{},
		ByChannel: map[string]postPerformanceTotals{}, ByKind: map[string]postPerformanceTotals{},
	}
	setErr := func(reason string) {
		if v.ClicksError == nil && reason != "" {
			v.ClicksError = &reason
		}
	}
	if (kind == "all" || kind == "event") && op.events != nil {
		rows, reason, err := op.eventRows(ctx, inWindow)
		if err != nil {
			return postPerformanceView{}, err
		}
		setErr(reason)
		v.Posts = append(v.Posts, rows...)
	}
	if (kind == "all" || kind == "guide") && op.guides != nil {
		rows, reason, err := op.guideRows(ctx, inWindow)
		if err != nil {
			return postPerformanceView{}, err
		}
		setErr(reason)
		v.Posts = append(v.Posts, rows...)
	}
	sort.SliceStable(v.Posts, func(i, j int) bool { return v.Posts[i].PostedAt < v.Posts[j].PostedAt })
	for _, r := range v.Posts {
		v.Total.add(r)
		c, k := v.ByChannel[r.Channel], v.ByKind[r.Kind]
		c.add(r)
		k.add(r)
		v.ByChannel[r.Channel], v.ByKind[r.Kind] = c, k
	}
	for i := range v.Posts {
		v.Posts[i].PostedAt = pacificRFC3339(v.Posts[i].PostedAt)
		v.Posts[i].CapturedAt = pacificRFC3339Ptr(v.Posts[i].CapturedAt)
	}
	v.Summary = postPerformanceSummary(v)
	return v, nil
}

// eventRows lists the event posts posted in the window, with clicks and
// engagement from resultsFor (the stats cache get_outreach_results uses).
// reason is the first non-ok clicks status.
func (op *postPerformanceOp) eventRows(ctx context.Context, inWindow func(*time.Time) bool) ([]postPerformanceRow, string, error) {
	h := op.events
	sums, err := h.store.ListSummaries(ctx)
	if err != nil {
		return nil, "", err
	}
	ids := make([]int64, 0, len(sums))
	for _, s := range sums {
		ids = append(ids, s.OutreachID)
	}
	if len(ids) == 0 {
		return nil, "", nil
	}
	byOutreach, err := h.store.ListPostsByOutreach(ctx, ids)
	if err != nil {
		return nil, "", err
	}
	var rows []postPerformanceRow
	reason := ""
	for _, s := range sums {
		var posted []outreach.Post
		for _, p := range byOutreach[s.OutreachID] {
			if inWindow(p.PostedAt) {
				posted = append(posted, p)
			}
		}
		if len(posted) == 0 {
			continue
		}
		res, err := h.resultsFor(ctx, s.WorkshopID)
		if err != nil {
			return nil, "", err
		}
		if res.Status != resultsOK && reason == "" {
			reason = res.Status
		}
		byID := map[int64]resultsPostView{}
		for _, p := range res.Posts {
			byID[p.ID] = p
		}
		for _, p := range posted {
			wave := p.Wave
			r := postPerformanceRow{
				Kind: "event", Title: s.WorkshopTitle, Slug: s.WorkshopSlug, Wave: &wave, Channel: p.Channel,
				RemoteURL: p.RemoteURL, PostedAt: p.PostedAt.UTC().Format(time.RFC3339),
			}
			if rv, ok := byID[p.ID]; ok {
				r.Clicks = rv.Clicks
				if e := rv.Engagement; e != nil {
					r.Likes, r.Reposts, r.Replies, r.Quotes = e.Likes, e.Reposts, e.Replies, e.Quotes
					at := e.CapturedAt
					r.CapturedAt = &at
				}
			}
			rows = append(rows, r)
		}
	}
	return rows, reason, nil
}

// guideRows lists the guide posts posted in the window, with clicks from the
// guide-post stats cache and the latest engagement snapshot (#0704).
func (op *postPerformanceOp) guideRows(ctx context.Context, inWindow func(*time.Time) bool) ([]postPerformanceRow, string, error) {
	g := op.guides
	posts, err := g.store.List(ctx)
	if err != nil {
		return nil, "", err
	}
	var rows []postPerformanceRow
	var ids []int64
	for _, p := range posts {
		if !inWindow(p.PostedAt) {
			continue
		}
		title := p.GuideSlug
		if gd, ok := guides.Lookup(p.GuideSlug); ok {
			title = gd.Title
		}
		rows = append(rows, postPerformanceRow{
			Kind: "guide", Title: title, Slug: p.GuideSlug, Channel: p.Channel,
			RemoteURL: p.RemoteURL, PostedAt: p.PostedAt.UTC().Format(time.RFC3339),
		})
		ids = append(ids, p.ID)
	}
	if len(rows) == 0 {
		return nil, "", nil
	}
	eng := map[int64]outreach.Engagement{}
	if er, ok := g.store.(guidePostEngagementReader); ok {
		if eng, err = er.LatestEngagement(ctx, ids); err != nil {
			return nil, "", err
		}
	}
	clicks, _, clicksErr, err := g.clicks(ctx)
	if err != nil {
		return nil, "", err
	}
	byID := map[int64]int{}
	for i, id := range ids {
		byID[id] = i
	}
	for _, p := range posts {
		i, ok := byID[p.ID]
		if !ok {
			continue
		}
		if p.ShortLinkKey != nil {
			if n, ok := clicks[*p.ShortLinkKey]; ok {
				rows[i].Clicks = &n
			}
		}
		if e, ok := eng[p.ID]; ok {
			rows[i].Likes, rows[i].Reposts, rows[i].Replies, rows[i].Quotes = e.Likes, e.Reposts, e.Replies, e.Quotes
			at := e.CapturedAt.UTC().Format(time.RFC3339)
			rows[i].CapturedAt = &at
		}
	}
	reason := ""
	if clicksErr != nil {
		reason = *clicksErr
	}
	return rows, reason, nil
}

// postPerformanceSummary is the one sentence an assistant can read aloud.
func postPerformanceSummary(v postPerformanceView) string {
	t := v.Total
	if t.Posts == 0 {
		return "No posts were made in this window."
	}
	noun := "posts"
	if t.Posts == 1 {
		noun = "post"
	}
	clicks := fmt.Sprintf("%d clicks", t.Clicks)
	if v.ClicksError != nil {
		clicks = "clicks unavailable"
	}
	return fmt.Sprintf("%d %s (%d event, %d guide) have %d likes, %d reposts, %d replies, %d quotes and %s.",
		t.Posts, noun, v.ByKind["event"].Posts, v.ByKind["guide"].Posts, t.Likes, t.Reposts, t.Replies, t.Quotes, clicks)
}
