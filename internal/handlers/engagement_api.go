package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// engagementWindowStore is the narrow store the engagement API reads.
type engagementWindowStore interface {
	EngagementWindow(ctx context.Context, since, until time.Time) (outreach.EngagementWindow, error)
}

// engagementOp runs get_engagement (#0653): Home Assistant's read-only daily
// check of new Bluesky and Mastodon engagement. now is a field so tests can
// pin the clock.
type engagementOp struct {
	store engagementWindowStore
	now   func() time.Time
}

// engagementInput is get_engagement's In. Both are strings on purpose, so
// the parsing below, and its exact messages, are what #0653 shipped.
type engagementInput struct {
	Hours *string `json:"hours,omitempty" query:"hours" jsonschema:"window length in hours, 1 to 168 (default 24); not with since"`
	Since *string `json:"since,omitempty" query:"since" jsonschema:"window start, RFC 3339, within the last 30 days; not with hours"`
}

// EngagementOperation is GET /api/v1/engagement (#0653, moved onto the
// registry by #0675). The success body is unchanged from #0653; errors are
// the v1 envelope. Building it never touches store.
func EngagementOperation(store engagementWindowStore) api.Operation {
	return engagementOperation(store, time.Now)
}

func engagementOperation(store engagementWindowStore, now func() time.Time) api.Operation {
	h := &engagementOp{store: store, now: now}
	return api.Define(api.Operation{
		Name: "get_engagement", Method: http.MethodGet, Path: "/api/v1/engagement",
		Scope:       apitokens.ScopeEngagementRead,
		Summary:     "New Bluesky and Mastodon engagement in a window (default the last 24 hours).",
		Description: "Counts likes, reposts, replies, quotes and mentions since the window start, per channel and per post, with a one-sentence summary. Output times are UTC.",
	}, h.run)
}

type engagementCounts struct {
	Likes    int `json:"likes"`
	Reposts  int `json:"reposts"`
	Replies  int `json:"replies"`
	Quotes   int `json:"quotes"`
	Mentions int `json:"mentions"`
	Total    int `json:"total"`
}

type engagementPost struct {
	Event     string  `json:"event"`
	EventDate *string `json:"event_date"`
	Wave      string  `json:"wave"`
	Channel   string  `json:"channel"`
	URL       *string `json:"url"`
	PostedAt  *string `json:"posted_at"`
	Likes     int     `json:"likes"`
	Reposts   int     `json:"reposts"`
	Replies   int     `json:"replies"`
	Quotes    int     `json:"quotes"`
	Total     int     `json:"total"`
}

type engagementResponse struct {
	Since         string           `json:"since"`
	Until         string           `json:"until"`
	DataAsOf      *string          `json:"data_as_of"`
	HasEngagement bool             `json:"has_engagement"`
	Total         int              `json:"total"`
	Bluesky       engagementCounts `json:"bluesky"`
	Mastodon      engagementCounts `json:"mastodon"`
	Posts         []engagementPost `json:"posts"`
	Summary       string           `json:"summary"`
}

func utcRFC3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// run reports the window (since, until]. The default is the last 24
// hours; ?hours=1..168 or ?since=<RFC 3339, within 30 days> override it.
func (h *engagementOp) run(ctx context.Context, in engagementInput) (engagementResponse, error) {
	bad := func(field, msg string) (engagementResponse, error) {
		return engagementResponse{}, api.Validation(map[string]string{field: msg})
	}
	until := h.now().UTC().Truncate(time.Second)
	hours := 24
	var since time.Time
	switch {
	case in.Hours != nil && in.Since != nil:
		return bad("hours", "use either hours or since, not both")
	case in.Hours != nil:
		n, err := strconv.Atoi(*in.Hours)
		if err != nil || n < 1 || n > 168 {
			return bad("hours", "hours must be a whole number from 1 to 168")
		}
		hours = n
	case in.Since != nil:
		s, err := time.Parse(time.RFC3339, *in.Since)
		if err != nil {
			return bad("since", "since must be an RFC 3339 time")
		}
		s = s.UTC().Truncate(time.Second)
		if s.After(until) {
			return bad("since", "since must not be in the future")
		}
		if until.Sub(s) > 30*24*time.Hour {
			return bad("since", "since must be within the last 30 days")
		}
		since, hours = s, 0
	}
	if hours > 0 {
		since = until.Add(-time.Duration(hours) * time.Hour)
	}

	// A store error goes back as-is: api.AsError logs it and answers 500.
	win, err := h.store.EngagementWindow(ctx, since, until)
	if err != nil {
		return engagementResponse{}, err
	}
	per := map[string]*outreach.ChannelEngagement{"bluesky": {}, "mastodon": {}}
	posts := []engagementPost{}
	for _, p := range win.Posts {
		if c, ok := per[p.Channel]; ok {
			c.Counts = c.Counts.Add(p.Delta)
		}
		var date *string
		if p.StartsAt != nil {
			d := outreach.PacificDate(*p.StartsAt)
			date = &d
		}
		posts = append(posts, engagementPost{
			Event: p.WorkshopTitle, EventDate: date, Wave: p.Wave, Channel: p.Channel,
			URL: p.URL, PostedAt: rfc3339(p.PostedAt),
			Likes: p.Delta.Likes, Reposts: p.Delta.Reposts, Replies: p.Delta.Replies, Quotes: p.Delta.Quotes,
			Total: p.Delta.Total(),
		})
	}
	for ch, c := range per {
		c.Mentions = win.Mentions[ch]
	}
	out := func(c *outreach.ChannelEngagement) engagementCounts {
		return engagementCounts{c.Likes, c.Reposts, c.Replies, c.Quotes, c.Mentions, c.Total()}
	}
	bsky, masto := per["bluesky"], per["mastodon"]
	total := bsky.Total() + masto.Total()
	resp := engagementResponse{
		Since: utcRFC3339(since), Until: utcRFC3339(until), DataAsOf: rfc3339(win.DataAsOf),
		HasEngagement: total > 0, Total: total,
		Bluesky: out(bsky), Mastodon: out(masto), Posts: posts,
		Summary: outreach.EngagementSummary(*bsky, *masto, outreach.WindowPhrase(hours, since)),
	}
	return resp, nil
}
