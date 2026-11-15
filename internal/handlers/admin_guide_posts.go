package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

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
	Schedule(ctx context.Context, id int64, at time.Time) (guideposts.Post, error)
	Unschedule(ctx context.Context, id int64) (guideposts.Post, error)
	QueueNow(ctx context.Context, id int64) (guideposts.Post, string, error)
	Delete(ctx context.Context, id int64) error
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

// record writes e against guide post id, filling the target and IP.
func (h *AdminGuidePostsHandler) record(r *http.Request, id int64, e audit.Entry) {
	if h.auditor == nil {
		return
	}
	e.TargetType = audit.TargetGuidePost
	e.TargetID = &id
	e.IP = clientIP(r)
	h.auditor.Record(r.Context(), e)
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
	for _, name := range []string{"bluesky", "mastodon"} {
		ch, _ := outreach.ChannelByName(name)
		resp.Channels = append(resp.Channels, guideChannelView{Name: ch.Name, Label: ch.Label, CharLimit: ch.CharLimit, Configured: h.configured(ch.Name)})
	}
	writeJSON(w, http.StatusOK, resp)
}

// Overview handles GET /admin/guide-posts-overview: one row per topic guide.
func (h *AdminGuidePostsHandler) Overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	posts, err := h.store.List(ctx)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	clicks, known, clicksErr, err := h.clicks(ctx)
	if err != nil {
		writeGuidePostError(w, err)
		return
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
	writeJSON(w, http.StatusOK, resp)
}

func guideActor(w http.ResponseWriter, r *http.Request) (int64, bool) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return 0, false
	}
	return actor.ID, true
}

// mintAfterCreate gives a new draft its link and answers 201 with the post
// and link_error; a failed mint keeps the unlinked draft.
func (h *AdminGuidePostsHandler) mintAfterCreate(w http.ResponseWriter, r *http.Request, actorID int64, p guideposts.Post) {
	resp := guidePostLinkResponse{Post: p}
	linked, minted, err := h.linker.Ensure(r.Context(), p.ID)
	if err != nil {
		slog.Default().Warn("admin_guide_posts: link mint failed", "id", p.ID, "err", err)
		reason := linkFailureReason(err)
		resp.LinkError = &reason
	} else {
		resp.Post = linked
		if minted {
			h.recordLinkCreated(r, actorID, linked)
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *AdminGuidePostsHandler) recordLinkCreated(r *http.Request, actorID int64, p guideposts.Post) {
	key := ""
	if p.ShortLinkKey != nil {
		key = *p.ShortLinkKey
	}
	h.record(r, p.ID, audit.Entry{ActorID: &actorID, Action: audit.ActionGuidePostLinkCreated, Metadata: map[string]any{
		"slug": p.GuideSlug, "channel": p.Channel, "campaign": p.CampaignSlug, "short_link_key": key,
	}})
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
	p, err := h.store.Create(r.Context(), req.Slug, req.Channel, req.Body, h.baseURL, req.Campaign)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	h.record(r, p.ID, audit.Entry{ActorID: &actorID, Action: audit.ActionGuidePostCreated, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel, "campaign": p.CampaignSlug}})
	h.mintAfterCreate(w, r, actorID, p)
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
	p, err := h.store.CopyAsDraft(r.Context(), id, h.baseURL, req.Campaign)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	h.record(r, p.ID, audit.Entry{ActorID: &actorID, Action: audit.ActionGuidePostCreated, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel, "campaign": p.CampaignSlug, "copied_from": id}})
	h.mintAfterCreate(w, r, actorID, p)
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
	p, err := h.store.UpdateBody(r.Context(), id, req.Body)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	h.record(r, p.ID, audit.Entry{ActorID: &actorID, Action: audit.ActionGuidePostUpdated, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel}})
	writeJSON(w, http.StatusOK, p)
}

// sendGate loads post id, requires its channel to be connected and gives an
// unlinked draft its link, writing the response and returning ok=false when
// any step fails.
func (h *AdminGuidePostsHandler) sendGate(w http.ResponseWriter, r *http.Request, actorID, id int64) (guideposts.Post, bool) {
	p, err := h.store.Get(r.Context(), id)
	if err != nil {
		writeGuidePostError(w, err)
		return p, false
	}
	if !h.configured(p.Channel) {
		label := p.Channel
		if ch, ok := outreach.ChannelByName(p.Channel); ok {
			label = ch.Label
		}
		writeError(w, http.StatusConflict, label+" is not connected")
		return p, false
	}
	if p.ShortURL != nil || p.Status != "draft" {
		return p, true
	}
	linked, minted, err := h.linker.Ensure(r.Context(), id)
	if err != nil {
		if errors.Is(err, guideposts.ErrNotFound) || errors.Is(err, guideposts.ErrUnknownGuide) {
			writeGuidePostError(w, err)
		} else {
			writeShortLinksError(w, "guide_post_link", err)
		}
		return p, false
	}
	if minted {
		h.recordLinkCreated(r, actorID, linked)
	}
	return linked, true
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
	if _, ok := h.sendGate(w, r, actorID, id); !ok {
		return
	}
	p, err := h.store.Schedule(r.Context(), id, at)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	h.record(r, p.ID, audit.Entry{ActorID: &actorID, Action: audit.ActionGuidePostScheduled, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel, "scheduled_at": at.Format(time.RFC3339)}})
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
	if _, ok := h.sendGate(w, r, actorID, id); !ok {
		return
	}
	p, prev, err := h.store.QueueNow(r.Context(), id)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	h.record(r, p.ID, audit.Entry{ActorID: &actorID, Action: audit.ActionGuidePostQueued, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel, "retry": prev == "failed"}})
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
	p, err := h.store.Unschedule(r.Context(), id)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	h.record(r, p.ID, audit.Entry{ActorID: &actorID, Action: audit.ActionGuidePostUnscheduled, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel}})
	writeJSON(w, http.StatusOK, p)
}

// Delete handles DELETE /admin/guide-posts/{id}. The short link, if any,
// stays in ShortLinks.
func (h *AdminGuidePostsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actorID, ok := guideActor(w, r)
	if !ok {
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	p, err := h.store.Get(r.Context(), id)
	if err != nil {
		writeGuidePostError(w, err)
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		writeGuidePostError(w, err)
		return
	}
	h.record(r, id, audit.Entry{ActorID: &actorID, Action: audit.ActionGuidePostDeleted, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel}})
	w.WriteHeader(http.StatusNoContent)
}

// writeGuidePostError maps a guideposts error to a fixed response.
func writeGuidePostError(w http.ResponseWriter, err error) {
	var be *guideposts.BodyError
	switch {
	case errors.Is(err, guideposts.ErrNotFound):
		writeError(w, http.StatusNotFound, "post not found")
	case errors.Is(err, guideposts.ErrPostLocked):
		writeError(w, http.StatusConflict, "post is already posted")
	case errors.Is(err, guideposts.ErrPostInFlight):
		writeError(w, http.StatusConflict, "post is being sent")
	case errors.Is(err, guideposts.ErrWrongStatus):
		writeError(w, http.StatusConflict, "post is not in a state that allows this")
	case errors.Is(err, guideposts.ErrNoLink):
		writeError(w, http.StatusConflict, "the post has no short link yet")
	case errors.Is(err, guideposts.ErrUnknownGuide):
		writeError(w, http.StatusBadRequest, "not a topic guide")
	case errors.Is(err, guideposts.ErrChannelUnknown):
		writeError(w, http.StatusBadRequest, "unknown channel")
	case errors.Is(err, guideposts.ErrBadCampaignSlug):
		writeError(w, http.StatusUnprocessableEntity, "campaign may use only lowercase letters, digits and single hyphens")
	case errors.Is(err, guideposts.ErrLinkRemoved):
		writeError(w, http.StatusUnprocessableEntity, "the post must keep its short link")
	case errors.As(err, &be):
		writeError(w, http.StatusUnprocessableEntity, be.Error())
	default:
		slog.Default().Error("admin_guide_posts: request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}
