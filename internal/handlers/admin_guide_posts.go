package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/guideposts"
	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// guidePostStore is the part of *guideposts.Store the admin API uses.
type guidePostStore interface {
	List(ctx context.Context) ([]guideposts.Post, error)
	Get(ctx context.Context, id int64) (guideposts.Post, error)
	Create(ctx context.Context, slug, channel, body, base, campaign string) (guideposts.Post, error)
	UpdateBody(ctx context.Context, id int64, body string) (guideposts.Post, error)
	UpdateCampaign(ctx context.Context, id int64, campaign string) (guideposts.Post, error)
	Schedule(ctx context.Context, id int64, at time.Time) (guideposts.Post, error)
	Unschedule(ctx context.Context, id int64) (guideposts.Post, error)
	QueueNow(ctx context.Context, id int64) (guideposts.Post, string, error)
	Delete(ctx context.Context, id int64) error
	DeleteDraft(ctx context.Context, id int64) (guideposts.Post, error)
	CopyAsDraft(ctx context.Context, id int64, base, campaign string) (guideposts.Post, error)
	ClickKeys(ctx context.Context) ([]guideposts.CampaignKeys, error)
}

// guidePostLinker mints a draft's short link; *guideposts.Linker.
type guidePostLinker interface {
	Ensure(ctx context.Context, id int64) (guideposts.Post, bool, error)
}

// guidePostStats reads campaign clicks; *outreachconn.LiveShortLinks and
// *shortlinks.Client satisfy it.
type guidePostStats interface {
	Enabled() bool
	CampaignStats(ctx context.Context, slug string, from, to time.Time) (*shortlinks.CampaignRollup, error)
}

// guidePostChannels reports whether a channel's account is connected;
// *guideposts.Publisher.
type guidePostChannels interface {
	ChannelConfigured(name string) bool
}

// guideSignupsReader reads #0662's per-guide signup counts;
// *subscribers.Store.
type guideSignupsReader interface {
	SignupsByGuide(ctx context.Context, from, to time.Time) ([]subscribers.GuideSignups, error)
}

// AdminGuidePostsHandler serves /admin/guide-posts (#0659): drafts,
// scheduling and sending of topic-guide social posts, plus the
// recently-shared overview.
type AdminGuidePostsHandler struct {
	store    guidePostStore
	linker   guidePostLinker
	stats    guidePostStats
	auditor  *audit.Logger
	baseURL  string
	channels guidePostChannels
	hourFn   func() int
	signups  guideSignupsReader
	mu       sync.Mutex // guards cache
	cache    *shortlinks.StatsCache
	now      func() time.Time
}

// NewAdminGuidePostsHandler builds the handler. A nil or unconfigured stats
// leaves clicks unknown; a nil auditor disables audit writes.
func NewAdminGuidePostsHandler(store guidePostStore, linker guidePostLinker, stats guidePostStats, auditor *audit.Logger, baseURL string, channels guidePostChannels) *AdminGuidePostsHandler {
	h := &AdminGuidePostsHandler{store: store, linker: linker, stats: stats, auditor: auditor, baseURL: baseURL, channels: channels, now: time.Now}
	h.cache = shortlinks.NewStatsCache(h.fetchStats, shortlinks.StatsCacheTTL, shortlinks.StatsCacheFailTTL)
	return h
}

// SetPostHour sets the source of the Pacific hour the UI suggests.
func (h *AdminGuidePostsHandler) SetPostHour(fn func() int) { h.hourFn = fn }

// SetSignups attaches the signup reader; nil turns signup counts off.
func (h *AdminGuidePostsHandler) SetSignups(r guideSignupsReader) { h.signups = r }

// ResetShortLinks drops every memoized stat; called when the ShortLinks
// connection changes.
func (h *AdminGuidePostsHandler) ResetShortLinks() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache = shortlinks.NewStatsCache(h.fetchStats, shortlinks.StatsCacheTTL, shortlinks.StatsCacheFailTTL)
}

func (h *AdminGuidePostsHandler) statsCache() *shortlinks.StatsCache {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cache
}

func (h *AdminGuidePostsHandler) fetchStats(ctx context.Context, slug string, from, to time.Time) (*shortlinks.CampaignRollup, error) {
	if h.stats == nil || !h.stats.Enabled() {
		return nil, shortlinks.ErrUnavailable
	}
	return h.stats.CampaignStats(ctx, slug, from, to)
}

func (h *AdminGuidePostsHandler) postHour() int {
	if h.hourFn == nil {
		return outreach.DefaultPostHour
	}
	return h.hourFn()
}

func (h *AdminGuidePostsHandler) configured(name string) bool {
	return h.channels != nil && h.channels.ChannelConfigured(name)
}

// recordCtx writes e against guide post id, filling the target and IP. ip ""
// takes the IP the v1 API put in ctx (api.ClientIPFromContext). An API-token
// request is attributed to its token (#0674); for a session request that is
// a no-op, so admin rows are unchanged.
func (h *AdminGuidePostsHandler) recordCtx(ctx context.Context, ip string, id int64, e audit.Entry) {
	if h.auditor == nil {
		return
	}
	e.TargetType = audit.TargetGuidePost
	e.TargetID = &id
	if ip == "" {
		ip = api.ClientIPFromContext(ctx)
	}
	e.IP = ip
	e = middleware.AttributeAPIToken(ctx, e)
	h.auditor.Record(ctx, e)
}

// guideTopicView is one postable guide in the List response.
type guideTopicView struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Hook        string `json:"hook"`
	Post        string `json:"post"`
	URL         string `json:"url"`
	DefaultBody string `json:"default_body"`
}

type guideChannelView struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	CharLimit  int    `json:"char_limit"`
	Configured bool   `json:"configured"`
}

// guideSignupCounts is a signup tally: requested, and how many confirmed.
type guideSignupCounts struct {
	Requested int `json:"requested"`
	Confirmed int `json:"confirmed"`
}

type guidePostView struct {
	guideposts.Post
	Clicks  *int64             `json:"clicks"`
	Signups *guideSignupCounts `json:"signups"`
	// Engagement is a posted post's latest reaction snapshot (#0708), or
	// null until the tracker captures one.
	Engagement *engagementView `json:"engagement"`
}

type guideShareView struct {
	guideposts.GuideShare
	Signups *guideSignupCounts `json:"signups"`
}

type guidePostsListResponse struct {
	Topics            []guideTopicView   `json:"topics"`
	Posts             []guidePostView    `json:"posts"`
	Channels          []guideChannelView `json:"channels"`
	PostHour          int                `json:"post_hour"`
	SuggestedCampaign string             `json:"suggested_campaign"`
	ClicksError       *string            `json:"clicks_error"`
	SignupsError      *string            `json:"signups_error"`
}

type guidePostsOverviewResponse struct {
	Guides       []guideShareView `json:"guides"`
	ClicksError  *string          `json:"clicks_error"`
	SignupsError *string          `json:"signups_error"`
}

type guidePostLinkResponse struct {
	Post      guideposts.Post `json:"post"`
	LinkError *string         `json:"link_error"`
}

func (h *AdminGuidePostsHandler) guideURL(slug string) string {
	return strings.TrimRight(h.baseURL, "/") + "/guides/" + slug
}

// clicks reads every campaign's clicks through the stats cache. A failing
// campaign leaves its keys out and sets clicksErr; known reports whether at
// least one campaign answered (or there were none). Only a database error is
// returned as err.
func (h *AdminGuidePostsHandler) clicks(ctx context.Context) (out map[string]int64, known bool, clicksErr *string, err error) {
	camps, err := h.store.ClickKeys(ctx)
	if err != nil {
		return nil, false, nil, err
	}
	out = map[string]int64{}
	known = len(camps) == 0
	cache := h.statsCache()
	now := h.now()
	for _, c := range camps {
		from, to := guideposts.ClickWindow(c.Since, now)
		rollup, _, ferr := cache.Get(ctx, c.CampaignSlug, from, to)
		if ferr != nil {
			slog.Default().Warn("admin_guide_posts: campaign stats failed", "campaign", c.CampaignSlug, "err", ferr)
			if clicksErr == nil {
				reason := linkFailureReason(ferr)
				clicksErr = &reason
			}
			continue
		}
		known = true
		for k, n := range outreach.ClicksByKey(c.Keys, rollup) {
			out[k] = n
		}
	}
	return out, known, clicksErr, nil
}

// signupRows reads signups over the window from the earliest post to now
// (the last 366 days when there are none). rows is nil when off or failed;
// signupsErr is set on failure.
func (h *AdminGuidePostsHandler) signupRows(ctx context.Context, posts []guideposts.Post) (rows []subscribers.GuideSignups, signupsErr *string) {
	if h.signups == nil {
		return nil, nil
	}
	now := h.now()
	since := now.AddDate(0, 0, -366)
	for i, p := range posts {
		if i == 0 || p.CreatedAt.Before(since) {
			since = p.CreatedAt
		}
	}
	from, to := guideposts.ClickWindow(since, now)
	rows, err := h.signups.SignupsByGuide(ctx, from, to)
	if err != nil {
		slog.Default().Warn("admin_guide_posts: signups failed", "err", err)
		failed := "failed"
		return nil, &failed
	}
	return rows, nil
}

func sumSignups(rows []subscribers.GuideSignups, match func(subscribers.GuideSignups) bool) *guideSignupCounts {
	c := &guideSignupCounts{}
	for _, r := range rows {
		if match(r) {
			c.Requested += r.Requested
			c.Confirmed += r.Confirmed
		}
	}
	return c
}

// List handles GET /admin/guide-posts.
func (h *AdminGuidePostsHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	posts, err := h.store.List(ctx)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	clicks, _, clicksErr, err := h.clicks(ctx)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	rows, signupsErr := h.signupRows(ctx, posts)
	resp := guidePostsListResponse{
		Topics: []guideTopicView{}, Posts: []guidePostView{}, Channels: []guideChannelView{},
		PostHour: h.postHour(), SuggestedCampaign: guideposts.SuggestedCampaign(h.now()),
		ClicksError: clicksErr, SignupsError: signupsErr,
	}
	for _, g := range guides.Topics() {
		resp.Topics = append(resp.Topics, guideTopicView{Slug: g.Slug, Title: g.Title, Hook: g.Hook, Post: g.Post, URL: h.guideURL(g.Slug), DefaultBody: guideposts.DefaultBody(g)})
	}
	for _, p := range posts {
		v := guidePostView{Post: p}
		if p.ShortLinkKey != nil {
			if n, ok := clicks[*p.ShortLinkKey]; ok {
				v.Clicks = &n
			}
		}
		if rows != nil {
			ref := guideposts.PostRef(p.ID)
			v.Signups = sumSignups(rows, func(s subscribers.GuideSignups) bool { return s.UTMContent == ref })
		}
		resp.Posts = append(resp.Posts, v)
	}
	if err := h.fillGuideEngagement(ctx, resp.Posts); err != nil {
		writeGuidePostError(w, err)
		return
	}
	for _, name := range []string{"bluesky", "mastodon"} {
		ch, _ := outreach.ChannelByName(name)
		resp.Channels = append(resp.Channels, guideChannelView{Name: ch.Name, Label: ch.Label, CharLimit: ch.CharLimit, Configured: h.configured(ch.Name)})
	}
	writeJSON(w, http.StatusOK, resp)
}

// fillGuideEngagement sets each posted post's latest reaction snapshot
// (#0708), when the store can read them.
func (h *AdminGuidePostsHandler) fillGuideEngagement(ctx context.Context, posts []guidePostView) error {
	reader, ok := h.store.(guidePostEngagementReader)
	if !ok {
		return nil
	}
	var ids []int64
	for _, p := range posts {
		if p.Status == "posted" {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	latest, err := reader.LatestEngagement(ctx, ids)
	if err != nil {
		return err
	}
	for i := range posts {
		if e, ok := latest[posts[i].ID]; ok {
			posts[i].Engagement = &engagementView{reactionsView: *toReactions(e.Counts), CapturedAt: e.CapturedAt.UTC().Format(time.RFC3339)}
		}
	}
	return nil
}

// Overview handles GET /admin/guide-posts-overview: one row per topic guide.
func (h *AdminGuidePostsHandler) Overview(w http.ResponseWriter, r *http.Request) {
	resp, err := h.overview(r.Context())
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// overview builds the recently-shared overview; the admin route and the v1
// guide_posts_overview operation (#0676) both serve it.
func (h *AdminGuidePostsHandler) overview(ctx context.Context) (guidePostsOverviewResponse, error) {
	posts, err := h.store.List(ctx)
	if err != nil {
		return guidePostsOverviewResponse{}, err
	}
	clicks, known, clicksErr, err := h.clicks(ctx)
	if err != nil {
		return guidePostsOverviewResponse{}, err
	}
	rows, signupsErr := h.signupRows(ctx, posts)
	resp := guidePostsOverviewResponse{Guides: []guideShareView{}, ClicksError: clicksErr, SignupsError: signupsErr}
	for _, s := range guideposts.Overview(guides.Topics(), posts, h.baseURL, clicks, known) {
		v := guideShareView{GuideShare: s}
		if rows != nil {
			slug := s.Slug
			v.Signups = sumSignups(rows, func(g subscribers.GuideSignups) bool { return g.Guide == slug })
		}
		resp.Guides = append(resp.Guides, v)
	}
	return resp, nil
}

func guideActor(w http.ResponseWriter, r *http.Request) (int64, bool) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return 0, false
	}
	return actor.ID, true
}

// errChannelNotConnected is gate's refusal when the post's channel account
// is not connected.
type errChannelNotConnected struct{ label string }

func (e errChannelNotConnected) Error() string { return e.label + " is not connected" }

// errShortLinks wraps a ShortLinks failure while gate mints a link, so each
// transport can answer it as a ShortLinks problem.
type errShortLinks struct{ err error }

func (e errShortLinks) Error() string { return "guide post link: " + e.err.Error() }
func (e errShortLinks) Unwrap() error { return e.err }

// errDraftOnly is deletePost's refusal of a non-draft when draftOnly is set.
var errDraftOnly = errors.New("only a draft can be deleted; unschedule it first")

// mint gives new draft p its link and returns the post with link_error; a
// failed mint keeps the unlinked draft.
func (h *AdminGuidePostsHandler) mint(ctx context.Context, ip string, actorID *int64, p guideposts.Post) guidePostLinkResponse {
	resp := guidePostLinkResponse{Post: p}
	linked, minted, err := h.linker.Ensure(ctx, p.ID)
	if err != nil {
		slog.Default().Warn("admin_guide_posts: link mint failed", "id", p.ID, "err", err)
		reason := linkFailureReason(err)
		resp.LinkError = &reason
	} else {
		resp.Post = linked
		if minted {
			h.recordLinkCreated(ctx, ip, actorID, linked)
		}
	}
	return resp
}

func (h *AdminGuidePostsHandler) recordLinkCreated(ctx context.Context, ip string, actorID *int64, p guideposts.Post) {
	key := ""
	if p.ShortLinkKey != nil {
		key = *p.ShortLinkKey
	}
	h.recordCtx(ctx, ip, p.ID, audit.Entry{ActorID: actorID, Action: audit.ActionGuidePostLinkCreated, Metadata: map[string]any{
		"slug": p.GuideSlug, "channel": p.Channel, "campaign": p.CampaignSlug, "short_link_key": key,
	}})
}

// createDraft makes a draft for slug on channel, audits it and mints its
// link.
func (h *AdminGuidePostsHandler) createDraft(ctx context.Context, ip string, actorID *int64, slug, channel, body, campaign string) (guidePostLinkResponse, error) {
	p, err := h.store.Create(ctx, slug, channel, body, h.baseURL, campaign)
	if err != nil {
		return guidePostLinkResponse{}, err
	}
	h.recordCtx(ctx, ip, p.ID, audit.Entry{ActorID: actorID, Action: audit.ActionGuidePostCreated, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel, "campaign": p.CampaignSlug}})
	return h.mint(ctx, ip, actorID, p), nil
}

// again copies posted post id into a new draft with its own link.
func (h *AdminGuidePostsHandler) again(ctx context.Context, ip string, actorID *int64, id int64, campaign string) (guidePostLinkResponse, error) {
	p, err := h.store.CopyAsDraft(ctx, id, h.baseURL, campaign)
	if err != nil {
		return guidePostLinkResponse{}, err
	}
	h.recordCtx(ctx, ip, p.ID, audit.Entry{ActorID: actorID, Action: audit.ActionGuidePostCreated, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel, "campaign": p.CampaignSlug, "copied_from": id}})
	return h.mint(ctx, ip, actorID, p), nil
}

// updateBody edits post id's text.
func (h *AdminGuidePostsHandler) updateBody(ctx context.Context, ip string, actorID *int64, id int64, body string) (guideposts.Post, error) {
	p, err := h.store.UpdateBody(ctx, id, body)
	if err != nil {
		return guideposts.Post{}, err
	}
	h.recordCtx(ctx, ip, p.ID, audit.Entry{ActorID: actorID, Action: audit.ActionGuidePostUpdated, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel}})
	return p, nil
}

// gate loads post id, requires its channel to be connected and gives an
// unlinked draft its link. A disconnected channel is errChannelNotConnected
// and a ShortLinks failure errShortLinks.
func (h *AdminGuidePostsHandler) gate(ctx context.Context, ip string, actorID *int64, id int64) (guideposts.Post, error) {
	p, err := h.store.Get(ctx, id)
	if err != nil {
		return p, err
	}
	if !h.configured(p.Channel) {
		label := p.Channel
		if ch, ok := outreach.ChannelByName(p.Channel); ok {
			label = ch.Label
		}
		return p, errChannelNotConnected{label: label}
	}
	if p.ShortURL != nil || p.Status != "draft" {
		return p, nil
	}
	linked, minted, err := h.linker.Ensure(ctx, id)
	if err != nil {
		if errors.Is(err, guideposts.ErrNotFound) || errors.Is(err, guideposts.ErrUnknownGuide) {
			return p, err
		}
		return p, errShortLinks{err: err}
	}
	if minted {
		h.recordLinkCreated(ctx, ip, actorID, linked)
	}
	return linked, nil
}

// schedule gates post id and sets it to go out at.
func (h *AdminGuidePostsHandler) schedule(ctx context.Context, ip string, actorID *int64, id int64, at time.Time) (guideposts.Post, error) {
	if _, err := h.gate(ctx, ip, actorID, id); err != nil {
		return guideposts.Post{}, err
	}
	p, err := h.store.Schedule(ctx, id, at)
	if err != nil {
		return guideposts.Post{}, err
	}
	h.recordCtx(ctx, ip, p.ID, audit.Entry{ActorID: actorID, Action: audit.ActionGuidePostScheduled, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel, "scheduled_at": at.Format(time.RFC3339)}})
	return p, nil
}

// queueNow gates post id and queues it for the publisher: a first send, or
// a retry of a failed post.
func (h *AdminGuidePostsHandler) queueNow(ctx context.Context, ip string, actorID *int64, id int64) (guideposts.Post, error) {
	if _, err := h.gate(ctx, ip, actorID, id); err != nil {
		return guideposts.Post{}, err
	}
	p, prev, err := h.store.QueueNow(ctx, id)
	if err != nil {
		return guideposts.Post{}, err
	}
	h.recordCtx(ctx, ip, p.ID, audit.Entry{ActorID: actorID, Action: audit.ActionGuidePostQueued, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel, "retry": prev == "failed"}})
	return p, nil
}

// unschedule returns scheduled post id to draft.
func (h *AdminGuidePostsHandler) unschedule(ctx context.Context, ip string, actorID *int64, id int64) (guideposts.Post, error) {
	p, err := h.store.Unschedule(ctx, id)
	if err != nil {
		return guideposts.Post{}, err
	}
	h.recordCtx(ctx, ip, p.ID, audit.Entry{ActorID: actorID, Action: audit.ActionGuidePostUnscheduled, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel}})
	return p, nil
}

// deletePost removes post id. draftOnly (the v1 API) refuses anything but a
// draft with errDraftOnly; the admin UI may also delete a scheduled or
// failed post. The short link, if any, stays in ShortLinks.
//
// draftOnly is one conditional DELETE (store.DeleteDraft, #0686), so a post
// scheduled between a caller's read and this call is refused, not deleted.
func (h *AdminGuidePostsHandler) deletePost(ctx context.Context, ip string, actorID *int64, id int64, draftOnly bool) error {
	var p guideposts.Post
	if draftOnly {
		var err error
		if p, err = h.store.DeleteDraft(ctx, id); err != nil {
			if errors.Is(err, guideposts.ErrNotDraft) {
				return errDraftOnly
			}
			return err
		}
	} else {
		var err error
		if p, err = h.store.Get(ctx, id); err != nil {
			return err
		}
		if err := h.store.Delete(ctx, id); err != nil {
			return err
		}
	}
	h.recordCtx(ctx, ip, id, audit.Entry{ActorID: actorID, Action: audit.ActionGuidePostDeleted, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel}})
	return nil
}

// Create handles POST /admin/guide-posts.
func (h *AdminGuidePostsHandler) Create(w http.ResponseWriter, r *http.Request) {
	actorID, ok := guideActor(w, r)
	if !ok {
		return
	}
	var req struct {
		Slug     string `json:"slug"`
		Channel  string `json:"channel"`
		Campaign string `json:"campaign"`
		Body     string `json:"body"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	resp, err := h.createDraft(r.Context(), clientIP(r), &actorID, req.Slug, req.Channel, req.Body, req.Campaign)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// Again handles POST /admin/guide-posts/{id}/again: "schedule again" copies
// a posted post into a new draft with its own link.
func (h *AdminGuidePostsHandler) Again(w http.ResponseWriter, r *http.Request) {
	actorID, ok := guideActor(w, r)
	if !ok {
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Campaign string `json:"campaign"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	resp, err := h.again(r.Context(), clientIP(r), &actorID, id, req.Campaign)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// Patch handles PATCH /admin/guide-posts/{id}: edit the text.
func (h *AdminGuidePostsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	actorID, ok := guideActor(w, r)
	if !ok {
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p, err := h.updateBody(r.Context(), clientIP(r), &actorID, id, req.Body)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// Schedule handles POST /admin/guide-posts/{id}/schedule.
func (h *AdminGuidePostsHandler) Schedule(w http.ResponseWriter, r *http.Request) {
	actorID, ok := guideActor(w, r)
	if !ok {
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	var req struct {
		ScheduledAt string `json:"scheduled_at"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	at, reason := parseScheduleAt(req.ScheduledAt, h.now())
	if reason != "" {
		writeError(w, http.StatusUnprocessableEntity, "scheduled_at "+reason)
		return
	}
	p, err := h.schedule(r.Context(), clientIP(r), &actorID, id, at)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// Send handles POST /admin/guide-posts/{id}/send: post now, or retry a
// failed post. The publisher sends it, so the answer is 202.
func (h *AdminGuidePostsHandler) Send(w http.ResponseWriter, r *http.Request) {
	actorID, ok := guideActor(w, r)
	if !ok {
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	p, err := h.queueNow(r.Context(), clientIP(r), &actorID, id)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, p)
}

// Unschedule handles POST /admin/guide-posts/{id}/unschedule.
func (h *AdminGuidePostsHandler) Unschedule(w http.ResponseWriter, r *http.Request) {
	actorID, ok := guideActor(w, r)
	if !ok {
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	p, err := h.unschedule(r.Context(), clientIP(r), &actorID, id)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// Delete handles DELETE /admin/guide-posts/{id}.
func (h *AdminGuidePostsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actorID, ok := guideActor(w, r)
	if !ok {
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	if err := h.deletePost(r.Context(), clientIP(r), &actorID, id, false); err != nil {
		writeGuidePostError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// guidePostErrInfo is the one table mapping a guide-post error to a status,
// the request field it concerns ("" when none) and a fixed message. The
// admin routes (writeGuidePostError) and the v1 operations
// (guidePostAPIError) both read it. status 500 means unrecognised.
func guidePostErrInfo(err error) (status int, field, msg string) {
	var be *guideposts.BodyError
	var nc errChannelNotConnected
	switch {
	case errors.Is(err, guideposts.ErrNotFound):
		return http.StatusNotFound, "", "post not found"
	case errors.Is(err, guideposts.ErrPostLocked):
		return http.StatusConflict, "", "post is already posted"
	case errors.Is(err, guideposts.ErrPostInFlight):
		return http.StatusConflict, "", "post is being sent"
	case errors.Is(err, guideposts.ErrWrongStatus):
		return http.StatusConflict, "", "post is not in a state that allows this"
	case errors.Is(err, guideposts.ErrNoLink):
		return http.StatusConflict, "", "the post has no short link yet"
	case errors.Is(err, guideposts.ErrLinkFixed):
		return http.StatusConflict, "campaign", "the campaign is fixed once the post has its short link; use post_guide_again with a new campaign"
	case errors.Is(err, errDraftOnly):
		return http.StatusConflict, "", errDraftOnly.Error()
	case errors.As(err, &nc):
		return http.StatusConflict, "channel", nc.Error()
	case errors.Is(err, guideposts.ErrUnknownGuide):
		return http.StatusBadRequest, "slug", "not a topic guide"
	case errors.Is(err, guideposts.ErrChannelUnknown):
		return http.StatusBadRequest, "channel", "unknown channel"
	case errors.Is(err, guideposts.ErrBadCampaignSlug):
		return http.StatusUnprocessableEntity, "campaign", "campaign may use only lowercase letters, digits and single hyphens"
	case errors.Is(err, guideposts.ErrLinkRemoved):
		return http.StatusUnprocessableEntity, "body", "the post must keep its short link"
	case errors.As(err, &be):
		return http.StatusUnprocessableEntity, "body", be.Error()
	}
	return http.StatusInternalServerError, "", "internal server error"
}

// writeGuidePostError maps a guide-post error to a fixed admin response.
func writeGuidePostError(w http.ResponseWriter, err error) {
	var sl errShortLinks
	if errors.As(err, &sl) {
		writeShortLinksError(w, "guide_post_link", sl.err)
		return
	}
	status, _, msg := guidePostErrInfo(err)
	if status == http.StatusInternalServerError {
		slog.Default().Error("admin_guide_posts: request failed", "err", err)
	}
	writeError(w, status, msg)
}
