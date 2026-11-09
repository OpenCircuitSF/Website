package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// engagementWindowStore is the narrow store the engagement API reads.
type engagementWindowStore interface {
	EngagementWindow(ctx context.Context, since, until time.Time) (outreach.EngagementWindow, error)
}

// EngagementAPIHandler serves GET /api/v1/engagement (#0653): Home
// Assistant's read-only daily check of new Bluesky and Mastodon engagement.
type EngagementAPIHandler struct {
	store engagementWindowStore
	now   func() time.Time
}

// NewEngagementAPIHandler builds the handler over the outreach store.
func NewEngagementAPIHandler(store engagementWindowStore) *EngagementAPIHandler {
	return &EngagementAPIHandler{store: store, now: time.Now}
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

// ServeHTTP reports the window (since, until]. The default is the last 24
// hours; ?hours=1..168 or ?since=<RFC 3339, within 30 days> override it.
func (h *EngagementAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	until := h.now().UTC().Truncate(time.Second)
	hours := 24
	var since time.Time
	switch {
	case q.Has("hours") && q.Has("since"):
		writeError(w, http.StatusBadRequest, "use either hours or since, not both")
		return
	case q.Has("hours"):
		n, err := strconv.Atoi(q.Get("hours"))
		if err != nil || n < 1 || n > 168 {
			writeError(w, http.StatusBadRequest, "hours must be a whole number from 1 to 168")
			return
		}
		hours = n
	case q.Has("since"):
		s, err := time.Parse(time.RFC3339, q.Get("since"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be an RFC 3339 time")
			return
		}
		s = s.UTC().Truncate(time.Second)
		if s.After(until) {
			writeError(w, http.StatusBadRequest, "since must not be in the future")
			return
		}
		if until.Sub(s) > 30*24*time.Hour {
			writeError(w, http.StatusBadRequest, "since must be within the last 30 days")
			return
		}
		since, hours = s, 0
	}
	if hours > 0 {
		since = until.Add(-time.Duration(hours) * time.Hour)
	}

	win, err := h.store.EngagementWindow(r.Context(), since, until)
	if err != nil {
		slog.Error("engagement api: window", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
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
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}
