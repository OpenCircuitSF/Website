package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

// Results statuses (#0546). Both results routes answer 200 for a known
// workshop and say here whether the click numbers are real; a ShortLinks
// failure never fails the request, because the posts come from our own
// database and always render.
const (
	resultsOK              = "ok"
	resultsNotConfigured   = "not_configured"
	resultsNotStarted      = "not_started"
	resultsCampaignMissing = "campaign_missing"
	resultsRejected        = "rejected"
	resultsUnreachable     = "unreachable"
)

// classifyResultsError maps a ShortLinks failure to a status. Only the
// status leaves the server; the upstream text is logged, never returned.
func classifyResultsError(op string, err error) string {
	switch {
	case errors.Is(err, shortlinks.ErrNotFound):
		return resultsCampaignMissing
	case errors.Is(err, shortlinks.ErrUnauthenticated), errors.Is(err, shortlinks.ErrForbidden):
		slog.Default().Error("admin_outreach: ShortLinks rejected the API token", "op", op, "err", err)
		return resultsRejected
	default:
		slog.Default().Error("admin_outreach: ShortLinks stats failed", "op", op, "err", err)
		return resultsUnreachable
	}
}

const dateLayout = "2006-01-02"

type resultsWindowView struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type resultsTotalsView struct {
	Clicks            int64 `json:"clicks"`
	ExcludedBotClicks int64 `json:"excluded_bot_clicks"`
	UnmatchedClicks   int64 `json:"unmatched_clicks"`
}

type resultsChannelView struct {
	Name           string `json:"name"`
	Label          string `json:"label"`
	Clicks         *int64 `json:"clicks"`
	HasDailySeries bool   `json:"has_daily_series"`
	Prepared       int    `json:"prepared"`
}

type resultsPostView struct {
	ID           int64   `json:"id"`
	Channel      string  `json:"channel"`
	Wave         string  `json:"wave"`
	Status       string  `json:"status"`
	ShortLinkKey *string `json:"short_link_key"`
	ShortURL     *string `json:"short_url"`
	PostedAt     *string `json:"posted_at"`
	RemoteURL    *string `json:"remote_url"`
	Clicks       *int64  `json:"clicks"`
}

type resultsSeriesView struct {
	ID       string                 `json:"id"`
	Label    string                 `json:"label"`
	Channels []string               `json:"channels"`
	IsOther  bool                   `json:"is_other"`
	Days     []shortlinks.DayBucket `json:"days"`
}

type outreachResultsView struct {
	Status         string               `json:"status"`
	Workshop       outreachWorkshopView `json:"workshop"`
	CampaignSlug   *string              `json:"campaign_slug"`
	Window         *resultsWindowView   `json:"window"`
	PartialLastDay bool                 `json:"partial_last_day"`
	FetchedAt      *string              `json:"fetched_at"`
	Totals         *resultsTotalsView   `json:"totals"`
	Channels       []resultsChannelView `json:"channels"`
	Posts          []resultsPostView    `json:"posts"`
	Series         []resultsSeriesView  `json:"series"`
}

// sortPostsForDisplay orders posts channel order x wave order, as the panel.
func sortPostsForDisplay(posts []outreach.Post) []outreach.Post {
	chIdx := map[string]int{}
	for i, c := range outreach.Channels() {
		chIdx[c.Name] = i
	}
	wvIdx := map[string]int{}
	for i, w := range outreach.Waves() {
		wvIdx[w.Name] = i
	}
	out := append([]outreach.Post(nil), posts...)
	sort.SliceStable(out, func(i, j int) bool {
		if chIdx[out[i].Channel] != chIdx[out[j].Channel] {
			return chIdx[out[i].Channel] < chIdx[out[j].Channel]
		}
		return wvIdx[out[i].Wave] < wvIdx[out[j].Wave]
	})
	return out
}

// ResultsForWorkshop handles GET /admin/workshops/{id}/outreach/results: the
// workshop's clicks from ShortLinks, matched to its posts by short link key.
func (h *AdminOutreachHandler) ResultsForWorkshop(w http.ResponseWriter, r *http.Request) {
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	wk, ok := h.loadWorkshop(w, r, id)
	if !ok {
		return
	}
	v := outreachResultsView{
		Workshop: outreachWorkshopView{ID: wk.ID, Slug: wk.Slug, Title: wk.Title, Status: wk.Status, StartsAt: rfc3339(wk.StartsAt)},
		Channels: []resultsChannelView{}, Posts: []resultsPostView{}, Series: []resultsSeriesView{},
	}
	for _, c := range outreach.Channels() {
		v.Channels = append(v.Channels, resultsChannelView{Name: c.Name, Label: c.Label})
	}
	o, err := h.store.GetByWorkshop(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrNotFound):
		v.Status = resultsNotStarted
		writeJSON(w, http.StatusOK, v)
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	posts, err := h.store.ListPosts(r.Context(), o.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	posts = sortPostsForDisplay(posts)
	slug := o.CampaignSlug
	v.CampaignSlug = &slug
	now := h.now()
	from, to := outreach.ResultsWindow(o.CreatedAt, wk.StartsAt, now)
	v.Window = &resultsWindowView{From: from.Format(dateLayout), To: to.Format(dateLayout)}
	v.PartialLastDay = to.Equal(outreachUTCDate(now).AddDate(0, 0, 1))
	for _, p := range posts {
		v.Posts = append(v.Posts, resultsPostView{
			ID: p.ID, Channel: p.Channel, Wave: p.Wave, Status: p.Status, ShortLinkKey: p.ShortLinkKey,
			ShortURL: p.ShortURL, PostedAt: rfc3339(p.PostedAt), RemoteURL: p.RemoteURL,
		})
	}
	for i := range v.Channels {
		for _, p := range posts {
			if p.Channel == v.Channels[i].Name && p.ShortLinkKey != nil && *p.ShortLinkKey != "" {
				v.Channels[i].Prepared++
			}
		}
	}

	if h.stats == nil {
		v.Status = resultsNotConfigured
		writeJSON(w, http.StatusOK, v)
		return
	}
	rollup, fetchedAt, err := h.stats.Get(r.Context(), o.CampaignSlug, from, to)
	if err != nil {
		v.Status = classifyResultsError("results for workshop", err)
		writeJSON(w, http.StatusOK, v)
		return
	}
	res := outreach.BuildResults(posts, rollup)
	v.Status = resultsOK
	if res.WindowFrom != "" && res.WindowTo != "" {
		v.Window = &resultsWindowView{From: res.WindowFrom, To: res.WindowTo}
	}
	fa := fetchedAt.UTC().Format(time.RFC3339)
	v.FetchedAt = &fa
	v.Totals = &resultsTotalsView{Clicks: res.ClickCount, ExcludedBotClicks: res.ExcludedBotCount, UnmatchedClicks: res.UnmatchedClicks}
	for i, c := range res.Channels {
		clicks := c.Clicks
		v.Channels[i].Clicks = &clicks
		v.Channels[i].HasDailySeries = c.HasDailySeries
		v.Channels[i].Prepared = c.Prepared
	}
	for i := range v.Posts {
		if n, ok := res.PostClicks[v.Posts[i].ID]; ok {
			n := n
			v.Posts[i].Clicks = &n
		}
	}
	for _, s := range res.Series {
		days := s.Days
		if days == nil {
			days = []shortlinks.DayBucket{}
		}
		chs := s.Channels
		if chs == nil {
			chs = []string{}
		}
		v.Series = append(v.Series, resultsSeriesView{ID: s.ID, Label: s.Label, Channels: chs, IsOther: s.IsOther, Days: days})
	}
	writeJSON(w, http.StatusOK, v)
}

func outreachUTCDate(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

type resultsSummaryRow struct {
	WorkshopID    int64   `json:"workshop_id"`
	WorkshopSlug  string  `json:"workshop_slug"`
	WorkshopTitle string  `json:"workshop_title"`
	StartsAt      *string `json:"starts_at"`
	PostsTotal    int     `json:"posts_total"`
	PostsPosted   int     `json:"posts_posted"`
	Clicks        *int64  `json:"clicks"`
	Status        string  `json:"status"`
}

// ResultsSummary handles GET /admin/outreach-results: one row per workshop
// with outreach. ShortLinks is asked one campaign at a time, and after the
// first rejected or unreachable answer it is not asked again, so one dead
// upstream costs one timeout rather than one per row.
func (h *AdminOutreachHandler) ResultsSummary(w http.ResponseWriter, r *http.Request) {
	sums, err := h.store.ListSummaries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	status := resultsOK
	if h.stats == nil {
		status = resultsNotConfigured
	}
	stopped := "" // set to the first unreachable/rejected status
	rows := make([]resultsSummaryRow, 0, len(sums))
	now := h.now()
	for _, s := range sums {
		row := resultsSummaryRow{
			WorkshopID: s.WorkshopID, WorkshopSlug: s.WorkshopSlug, WorkshopTitle: s.WorkshopTitle, StartsAt: rfc3339(s.StartsAt),
			PostsPosted: s.Counts["posted"],
		}
		for _, n := range s.Counts {
			row.PostsTotal += n
		}
		switch {
		case h.stats == nil:
			row.Status = resultsNotConfigured
		case stopped != "":
			row.Status = stopped
		default:
			row.Status = h.summaryClicks(r.Context(), &row, s, now)
			if row.Status == resultsUnreachable || row.Status == resultsRejected {
				stopped = row.Status
				if status == resultsOK {
					status = stopped
				}
			}
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "rows": rows})
}

// summaryClicks fills row.Clicks from the campaign's rollup and returns the
// row's status.
func (h *AdminOutreachHandler) summaryClicks(ctx context.Context, row *resultsSummaryRow, s outreach.Summary, now time.Time) string {
	from, to := outreach.ResultsWindow(s.CreatedAt, s.StartsAt, now)
	rollup, _, err := h.stats.Get(ctx, s.CampaignSlug, from, to)
	if err != nil {
		return classifyResultsError("results summary", err)
	}
	n := rollup.ClickCount
	row.Clicks = &n
	return resultsOK
}
