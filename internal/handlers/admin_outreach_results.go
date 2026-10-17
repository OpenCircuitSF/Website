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
	// FlyerClicks is the clicks on this workshop's flyer links (#0549). It
	// is part of Clicks, and no longer part of UnmatchedClicks.
	FlyerClicks int64 `json:"flyer_clicks"`
}

// resultsFlyerLocationView is one row of the "Flyers by location" table: a
// (workshop, location) link, with this workshop's scans and the scans of
// every workshop's flyers at that location. A nil count means ShortLinks
// could not say.
type resultsFlyerLocationView struct {
	LinkID             int64   `json:"link_id"`
	LocationID         int64   `json:"location_id"`
	Name               string  `json:"name"`
	Code               string  `json:"code"`
	ShortURL           string  `json:"short_url"`
	PutUpAt            *string `json:"put_up_at"`
	Clicks             *int64  `json:"clicks"`
	ClicksAllWorkshops *int64  `json:"clicks_all_workshops"`
}

type resultsChannelView struct {
	Name           string `json:"name"`
	Label          string `json:"label"`
	Clicks         *int64 `json:"clicks"`
	HasDailySeries bool   `json:"has_daily_series"`
	Prepared       int    `json:"prepared"`
	// Reactions is the sum of the channel's posts' latest snapshots (#0547);
	// null when none has one.
	Reactions *reactionsView `json:"reactions"`
}

// reactionsView is a set of engagement counts (#0547).
type reactionsView struct {
	Likes   int `json:"likes"`
	Reposts int `json:"reposts"`
	Replies int `json:"replies"`
	Quotes  int `json:"quotes"`
}

func toReactions(c outreach.Counts) *reactionsView {
	return &reactionsView{Likes: c.Likes, Reposts: c.Reposts, Replies: c.Replies, Quotes: c.Quotes}
}

// engagementView is a post's latest snapshot; the counts sit flat beside
// captured_at.
type engagementView struct {
	reactionsView
	CapturedAt string `json:"captured_at"`
}

// mentionView is one mention, reply or quote (#0547). Author, text and url are
// third-party content: the SPA renders them as text and links only an https
// url, and the url is also checked when it is stored.
type mentionView struct {
	ID            int64   `json:"id"`
	Channel       string  `json:"channel"`
	Kind          string  `json:"kind"`
	Author        string  `json:"author"`
	Text          string  `json:"text"`
	URL           *string `json:"url"`
	CreatedAt     string  `json:"created_at"`
	PostID        *int64  `json:"post_id"`
	Wave          *string `json:"wave"`
	WorkshopID    *int64  `json:"workshop_id"`
	WorkshopTitle *string `json:"workshop_title"`
}

func toMentionViews(ms []outreach.Mention) []mentionView {
	out := make([]mentionView, 0, len(ms))
	for _, m := range ms {
		out = append(out, mentionView{
			ID: m.ID, Channel: m.Channel, Kind: m.Kind, Author: m.Author, Text: m.Text, URL: m.URL,
			CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339), PostID: m.PostID, Wave: m.Wave,
			WorkshopID: m.WorkshopID, WorkshopTitle: m.WorkshopTitle,
		})
	}
	return out
}

// maxResultsMentions bounds the mentions feed in the per-workshop response.
const maxResultsMentions = 100

// maxSummaryMentions bounds the all-workshops feed.
const maxSummaryMentions = 50

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
	// Engagement is the latest snapshot; null until the first capture (#0547).
	Engagement *engagementView `json:"engagement"`
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
	// FlyerLocations and FlyerAllStatus carry #0549's flyer table.
	// FlyerAllStatus says whether clicks_all_workshops is real: it is
	// "ok", or why every one of them is null.
	FlyerLocations []resultsFlyerLocationView `json:"flyer_locations"`
	FlyerAllStatus string                     `json:"flyer_all_status"`
	// Reactions is the workshop total of every post's latest snapshot, and
	// Mentions the feed tied to its posts (#0547). Both come from our own
	// database, so they are filled for every status but not_started.
	Reactions *reactionsView `json:"reactions"`
	Mentions  []mentionView  `json:"mentions"`
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
		FlyerLocations: []resultsFlyerLocationView{}, Mentions: []mentionView{},
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
	if !h.fillEngagement(w, r, &v, posts, o.ID) {
		return
	}

	allLinks, err := h.flyers.ListAllFlyerLinks(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	var ownLinks []outreach.FlyerLink
	for _, l := range allLinks {
		if l.OutreachID != o.ID {
			continue
		}
		ownLinks = append(ownLinks, l.FlyerLink)
		v.FlyerLocations = append(v.FlyerLocations, resultsFlyerLocationView{
			LinkID: l.ID, LocationID: l.LocationID, Name: l.LocationName, Code: l.Code,
			ShortURL: l.ShortURL, PutUpAt: rfc3339(l.PutUpAt),
		})
	}

	if h.stats == nil {
		v.Status = resultsNotConfigured
		v.FlyerAllStatus = resultsNotConfigured
		writeJSON(w, http.StatusOK, v)
		return
	}
	rollup, fetchedAt, err := h.stats.Get(r.Context(), o.CampaignSlug, from, to)
	if err != nil {
		v.Status = classifyResultsError("results for workshop", err)
		v.FlyerAllStatus = v.Status
		writeJSON(w, http.StatusOK, v)
		return
	}
	res := outreach.BuildResults(posts, rollup)
	flyerClicks := outreach.FlyerClicksByKey(ownLinks, rollup)
	var flyerTotal int64
	for i := range v.FlyerLocations {
		for _, l := range ownLinks {
			if l.ID == v.FlyerLocations[i].LinkID {
				n := flyerClicks[l.Key]
				v.FlyerLocations[i].Clicks = &n
				flyerTotal += n
			}
		}
	}
	// Flyer links are not posts, so BuildResults counted their clicks as
	// unmatched; they are matched now.
	res.UnmatchedClicks = max(0, res.UnmatchedClicks-flyerTotal)
	v.FlyerAllStatus = h.flyerAllWorkshops(r.Context(), v.FlyerLocations, allLinks, now)
	v.Status = resultsOK
	if res.WindowFrom != "" && res.WindowTo != "" {
		v.Window = &resultsWindowView{From: res.WindowFrom, To: res.WindowTo}
	}
	fa := fetchedAt.UTC().Format(time.RFC3339)
	v.FetchedAt = &fa
	v.Totals = &resultsTotalsView{Clicks: res.ClickCount, ExcludedBotClicks: res.ExcludedBotCount, UnmatchedClicks: res.UnmatchedClicks, FlyerClicks: flyerTotal}
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

// fillEngagement adds each post's latest snapshot, the channel and workshop
// reaction totals, and the mentions feed (#0547). It runs before any
// ShortLinks call, because none of it depends on ShortLinks. It writes the
// 500 itself and returns false on a store error.
func (h *AdminOutreachHandler) fillEngagement(w http.ResponseWriter, r *http.Request, v *outreachResultsView, posts []outreach.Post, outreachID int64) bool {
	ids := make([]int64, 0, len(posts))
	for _, p := range posts {
		ids = append(ids, p.ID)
	}
	latest, err := h.store.LatestEngagement(r.Context(), ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return false
	}
	var total outreach.Counts
	haveTotal := false
	byChannel := map[string]outreach.Counts{}
	for i, p := range v.Posts {
		e, ok := latest[p.ID]
		if !ok {
			continue
		}
		v.Posts[i].Engagement = &engagementView{reactionsView: *toReactions(e.Counts), CapturedAt: e.CapturedAt.UTC().Format(time.RFC3339)}
		total, haveTotal = total.Add(e.Counts), true
		byChannel[p.Channel] = byChannel[p.Channel].Add(e.Counts)
	}
	for i := range v.Channels {
		if c, ok := byChannel[v.Channels[i].Name]; ok {
			v.Channels[i].Reactions = toReactions(c)
		}
	}
	if haveTotal {
		v.Reactions = toReactions(total)
	}
	mentions, err := h.store.ListMentionsForOutreach(r.Context(), outreachID, maxResultsMentions)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return false
	}
	v.Mentions = toMentionViews(mentions)
	return true
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
	// Reactions is the sum of the workshop's posts' latest snapshots (#0547);
	// null when none has one.
	Reactions *reactionsView `json:"reactions"`
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
	totals, err := h.store.ReactionTotalsByOutreach(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	recent, err := h.store.ListRecentMentions(r.Context(), maxSummaryMentions)
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
		if c, ok := totals[s.OutreachID]; ok {
			row.Reactions = toReactions(c)
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
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "rows": rows, "mentions": toMentionViews(recent)})
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

// flyerAllWorkshops fills ClicksAllWorkshops on each row: the clicks on every
// workshop's flyer links at that location. ShortLinks is asked one campaign
// at a time, and the first failure ends the pass with that status and every
// count left null, because a partial sum would read as a total. The one
// campaign this request already fetched comes back from the stats cache.
func (h *AdminOutreachHandler) flyerAllWorkshops(ctx context.Context, rows []resultsFlyerLocationView, all []outreach.FlyerLinkAll, now time.Time) string {
	if len(rows) == 0 {
		return resultsOK
	}
	type campaign struct {
		from, to time.Time
		links    []outreach.FlyerLinkAll
	}
	order := []string{}
	byCampaign := map[string]*campaign{}
	for _, l := range all {
		c := byCampaign[l.CampaignSlug]
		if c == nil {
			from, to := outreach.ResultsWindow(l.OutreachCreatedAt, l.WorkshopStartsAt, now)
			c = &campaign{from: from, to: to}
			byCampaign[l.CampaignSlug] = c
			order = append(order, l.CampaignSlug)
		}
		c.links = append(c.links, l)
	}
	byLocation := map[int64]int64{}
	for _, slug := range order {
		c := byCampaign[slug]
		rollup, _, err := h.stats.Get(ctx, slug, c.from, c.to)
		if err != nil {
			return classifyResultsError("flyer clicks, all workshops", err)
		}
		links := make([]outreach.FlyerLink, 0, len(c.links))
		for _, l := range c.links {
			links = append(links, l.FlyerLink)
		}
		clicks := outreach.FlyerClicksByKey(links, rollup)
		for _, l := range c.links {
			byLocation[l.LocationID] += clicks[l.Key]
		}
	}
	for i := range rows {
		n := byLocation[rows[i].LocationID]
		rows[i].ClicksAllWorkshops = &n
	}
	return resultsOK
}
