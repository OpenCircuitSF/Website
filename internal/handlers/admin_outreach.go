package handlers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// maxOutreachBodyBytes caps a post body. The longest channel limit is 500
// characters; this only stops absurd input.
const maxOutreachBodyBytes = 5000

// outreachStore is the data-layer behavior the outreach handler needs.
// *outreach.Store satisfies it.
type outreachStore interface {
	GetByWorkshop(ctx context.Context, workshopID int64) (outreach.Outreach, error)
	GetByID(ctx context.Context, id int64) (outreach.Outreach, error)
	Create(ctx context.Context, workshopID int64, campaignSlug string) (outreach.Outreach, bool, error)
	ListSummaries(ctx context.Context) ([]outreach.Summary, error)
	ListPosts(ctx context.Context, outreachID int64) ([]outreach.Post, error)
	GetPost(ctx context.Context, id int64) (outreach.Post, error)
	CreatePost(ctx context.Context, outreachID int64, channel, wave, body, fingerprint string) (outreach.Post, error)
	UpdatePostBody(ctx context.Context, id int64, body string, fingerprint *string, customized *bool) (outreach.Post, error)
	// SetPrepared's bool is true only when this call's UPDATE changed the row.
	SetPrepared(ctx context.Context, id int64, key, shortURL, status string) (outreach.Post, bool, error)
	MarkPosted(ctx context.Context, id int64, remoteURL *string, now time.Time) (outreach.Post, error)
	// MarkUnposted undoes a hand-posted mark (#0588).
	MarkUnposted(ctx context.Context, id int64) (outreach.Post, error)
	DeletePost(ctx context.Context, id int64) error
	// CanReset and Reset (#0592): start over while nothing is public.
	CanReset(ctx context.Context, outreachID int64) (bool, error)
	Reset(ctx context.Context, outreachID int64) (outreach.ResetResult, error)
	// Slot links (#0568): one tracked link per channel x wave, made before any
	// post exists. InsertPostLink's bool is true only when a row was inserted.
	ListPostLinks(ctx context.Context, outreachID int64) ([]outreach.PostLink, error)
	GetPostLink(ctx context.Context, outreachID int64, channel, wave string) (outreach.PostLink, error)
	InsertPostLink(ctx context.Context, outreachID int64, channel, wave, key, shortURL string) (outreach.PostLink, bool, error)
	QueueNow(ctx context.Context, id int64) (post outreach.Post, prevStatus string, err error)
	Schedule(ctx context.Context, id int64, at time.Time) (outreach.Post, error)
	Unschedule(ctx context.Context, id int64) (outreach.Post, error)
	// Engagement and mentions (#0547), read from our own database.
	LatestEngagement(ctx context.Context, postIDs []int64) (map[int64]outreach.Engagement, error)
	ReactionTotalsByOutreach(ctx context.Context) (map[int64]outreach.Counts, error)
	ListMentionsForOutreach(ctx context.Context, outreachID int64, limit int) ([]outreach.Mention, error)
	ListRecentMentions(ctx context.Context, limit int) ([]outreach.Mention, error)
}

// outreachPosting reports which automatic channels have working credentials
// (#0545). *outreach.Publisher satisfies it, and a nil *outreach.Publisher
// reports nothing configured.
type outreachPosting interface {
	ChannelConfigured(name string) bool
}

// outreachWorkshopReader loads the workshop an outreach belongs to.
type outreachWorkshopReader interface {
	GetByID(ctx context.Context, id int64) (workshops.Workshop, error)
}

// shortLinker is the slice of *shortlinks.Client the handler uses. A nil
// *shortlinks.Client satisfies it and reports Enabled() == false, but the
// interface holding it is non-nil, so every use goes through linksEnabled.
type shortLinker interface {
	Enabled() bool
	Me(ctx context.Context) (*shortlinks.Me, error)
	EnsureCampaign(ctx context.Context, slug string, req shortlinks.CreateCampaignRequest) (*shortlinks.Campaign, error)
	CreateLink(ctx context.Context, req shortlinks.CreateLinkRequest) (*shortlinks.Link, error)
	LinkQRSVG(ctx context.Context, key string) ([]byte, error)
	CampaignStats(ctx context.Context, slug string, from, to time.Time) (*shortlinks.CampaignRollup, error)
}

// AdminOutreachHandler serves the admin-only Outreach routes (#0544): start
// outreach for a workshop, draft per-channel posts, Prepare them (create the
// tracked short link), and mark them posted. All routes MUST be mounted
// behind RequireSession then RequireAdmin; the handler only reads the user
// to attribute audit rows. All ShortLinks calls are server-side.
type AdminOutreachHandler struct {
	hourFn    func() int
	tagsFn    func() string
	accountFn func(string) string
	cardFn    func() string
	// channelsFn lists the channels switched on in Settings (#0588); nil means all.
	channelsFn func() []string
	store      outreachStore
	locations  outreachLocationStore
	flyers     outreachFlyerStore
	ws         outreachWorkshopReader
	links      shortLinker
	auditor    *audit.Logger
	baseURL    string
	posting    outreachPosting
	mu         sync.Mutex // guards shortBase and stats; never held across a network call
	shortBase  string     // cached Me().BaseURL; a failed fetch is not cached
	// stats memoizes campaign stats for the results routes (#0546). Always
	// built; fetchStats answers ErrUnavailable while ShortLinks is not
	// configured, and ResetShortLinks replaces it when the connection changes.
	stats *shortlinks.StatsCache
	// conns serves the connection-settings routes; nil when the server runs
	// without a database.
	conns *AdminOutreachConnectionsHandler
	now   func() time.Time
}

// NewAdminOutreachHandler constructs the handler. baseURL is the site's own
// BaseURL, used for the tracked destination URLs; a nil auditor disables
// audit writes; a nil or unconfigured links makes Outreach report
// unavailable. A nil posting means no automatic channel is configured.
func NewAdminOutreachHandler(store outreachStore, locations outreachLocationStore, flyers outreachFlyerStore, ws outreachWorkshopReader, links shortLinker, auditor *audit.Logger, baseURL string, posting outreachPosting) *AdminOutreachHandler {
	h := &AdminOutreachHandler{store: store, locations: locations, flyers: flyers, ws: ws, links: links, auditor: auditor, baseURL: baseURL, posting: posting, now: time.Now}
	h.stats = shortlinks.NewStatsCache(h.fetchStats, shortlinks.StatsCacheTTL, shortlinks.StatsCacheFailTTL)
	return h
}

// fetchStats is the stats cache's source: it reads the live ShortLinks
// connection on every miss.
func (h *AdminOutreachHandler) fetchStats(ctx context.Context, slug string, from, to time.Time) (*shortlinks.CampaignRollup, error) {
	if !h.linksEnabled() {
		return nil, shortlinks.ErrUnavailable
	}
	return h.links.CampaignStats(ctx, slug, from, to)
}

// statsCache returns the current stats cache.
func (h *AdminOutreachHandler) statsCache() *shortlinks.StatsCache {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stats
}

// ResetShortLinks drops the cached short-URL host and every memoized stat;
// called when the ShortLinks connection changes (#0564).
func (h *AdminOutreachHandler) ResetShortLinks() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.shortBase = ""
	h.stats = shortlinks.NewStatsCache(h.fetchStats, shortlinks.StatsCacheTTL, shortlinks.StatsCacheFailTTL)
}

// SetConnections attaches the connection-settings handler, so its routes are
// mounted exactly when Outreach is.
func (h *AdminOutreachHandler) SetConnections(c *AdminOutreachConnectionsHandler) {
	if h != nil {
		h.conns = c
	}
}

// Connections returns the attached connection-settings handler, or nil.
func (h *AdminOutreachHandler) Connections() *AdminOutreachConnectionsHandler {
	if h == nil {
		return nil
	}
	return h.conns
}

func (h *AdminOutreachHandler) linksEnabled() bool {
	return h.links != nil && h.links.Enabled()
}

// shortLinksBase returns the display host for short URLs, fetched once. The
// mutex guards only the cached value: the network call to Me runs outside it,
// so concurrent first callers do not queue behind one upstream round trip
// (they may each fetch once; the result is identical and the last store
// wins). A failed fetch is not cached.
func (h *AdminOutreachHandler) shortLinksBase(ctx context.Context) (string, error) {
	h.mu.Lock()
	cached := h.shortBase
	h.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	me, err := h.links.Me(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(me.BaseURL) == "" {
		return "", errors.New("shortlinks: /api/me returned no base_url")
	}
	base := strings.TrimRight(me.BaseURL, "/")
	h.mu.Lock()
	h.shortBase = base
	h.mu.Unlock()
	return base, nil
}

// writeShortLinksError maps an upstream failure to a response that never
// echoes upstream text; the detail goes to the server log (the client has
// already redacted the token from it).
func writeShortLinksError(w http.ResponseWriter, op string, err error) {
	var apiErr *shortlinks.APIError
	switch {
	case errors.Is(err, shortlinks.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "ShortLinks is not configured")
	case errors.Is(err, shortlinks.ErrRateLimited):
		if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(apiErr.RetryAfter/time.Second)))
		}
		writeError(w, http.StatusTooManyRequests, "ShortLinks is rate limiting requests; try again shortly")
	default:
		slog.Default().Error("admin_outreach: ShortLinks request failed", "op", op, "err", err)
		writeError(w, http.StatusBadGateway, "ShortLinks request failed")
	}
}

// SetPostHour sets the source of the Pacific hour suggested times use; unset,
// it is outreach.DefaultPostHour. Read on every request so a Settings save
// applies at once (#0574).
func (h *AdminOutreachHandler) SetPostHour(fn func() int) { h.hourFn = fn }

func (h *AdminOutreachHandler) postHour() int {
	if h.hourFn == nil {
		return outreach.DefaultPostHour
	}
	return h.hourFn()
}

// SetHashtags sets the source of the site-wide hashtag line drafts end with
// (#0573); unset, drafts carry none. Read per request.
func (h *AdminOutreachHandler) SetHashtags(fn func() string) { h.tagsFn = fn }

// SetAccounts sets the source of the public account name an automatic channel
// posts as (#0581), shown in the Post now confirmation. Read per request;
// unset, every account is empty and the SPA words the line without a name.
func (h *AdminOutreachHandler) SetAccounts(fn func(string) string) { h.accountFn = fn }

// SetEnabledChannels sets the source of the channel names outreach is switched
// on for (#0588), read per request so a Settings save applies at once. Unset,
// every channel is on. A disabled channel is not drafted, no new tracked link
// is made for it, and the panel, results and links views leave it out; posts
// and links it already has are kept, and come back if it is switched on again.
func (h *AdminOutreachHandler) SetEnabledChannels(fn func() []string) { h.channelsFn = fn }

func (h *AdminOutreachHandler) channelEnabled(name string) bool {
	if h.channelsFn == nil {
		return true
	}
	for _, n := range h.channelsFn() {
		if n == name {
			return true
		}
	}
	return false
}

// enabledChannels is outreach.Channels() without the switched-off ones.
func (h *AdminOutreachHandler) enabledChannels() []outreach.Channel {
	var out []outreach.Channel
	for _, ch := range outreach.Channels() {
		if h.channelEnabled(ch.Name) {
			out = append(out, ch)
		}
	}
	return out
}

// enabledPosts drops the posts of switched-off channels.
func (h *AdminOutreachHandler) enabledPosts(posts []outreach.Post) []outreach.Post {
	out := make([]outreach.Post, 0, len(posts))
	for _, p := range posts {
		if h.channelEnabled(p.Channel) {
			out = append(out, p)
		}
	}
	return out
}

// SetCardURL sets the source of the Bluesky card URL choice, "short" or
// "canonical" (#0583). Read per request; unset, the card is "short".
func (h *AdminOutreachHandler) SetCardURL(fn func() string) { h.cardFn = fn }

// blueskyCard mirrors the publisher: "canonical" only when asked for and the
// site's base URL is known, otherwise the publisher falls back to the short link.
func (h *AdminOutreachHandler) blueskyCard() string {
	if h.cardFn != nil && h.cardFn() == "canonical" && h.baseURL != "" {
		return "canonical"
	}
	return "short"
}

// nonEmptyPtr drops a nil or blank optional text field.
func nonEmptyPtr(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	return s
}

// facts is the slice of a workshop that drafts are written from.
func (h *AdminOutreachHandler) facts(wk workshops.Workshop) outreach.Facts {
	f := outreach.Facts{Slug: wk.Slug, Title: wk.Title, StartsAt: wk.StartsAt, LocationName: wk.LocationName, LocationAddress: wk.LocationAddress, Summary: strings.TrimSpace(derefOrEmpty(wk.Summary))}
	if h.tagsFn != nil {
		f.Hashtags = h.tagsFn()
	}
	return f
}

func rfc3339(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

type outreachUTMView struct {
	Source   string `json:"source"`
	Medium   string `json:"medium"`
	Campaign string `json:"campaign"`
	Content  string `json:"content"`
}

type outreachPostView struct {
	ID         int64  `json:"id"`
	OutreachID int64  `json:"outreach_id"`
	Channel    string `json:"channel"`
	Wave       string `json:"wave"`
	Body       string `json:"body"`
	Status     string `json:"status"`
	Prepared   bool   `json:"prepared"`
	Stale      bool   `json:"stale"`
	// Customized: hand-edited, left alone by a shared-message save (#0582).
	Customized bool `json:"customized"`
	// Held is true for a scheduled or queued post whose workshop is not
	// published: it will not be sent while that stays true (#0545).
	Held           bool            `json:"held"`
	ShortLinkKey   *string         `json:"short_link_key"`
	ShortURL       *string         `json:"short_url"`
	DestinationURL string          `json:"destination_url"`
	UTM            outreachUTMView `json:"utm"`
	ScheduledAt    *string         `json:"scheduled_at"`
	PostedAt       *string         `json:"posted_at"`
	RemoteURL      *string         `json:"remote_url"`
	LastError      *string         `json:"last_error"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      *string         `json:"updated_at"`
}

func (h *AdminOutreachHandler) postView(p outreach.Post, f outreach.Facts, workshopStatus string) outreachPostView {
	ch, _ := outreach.ChannelByName(p.Channel)
	wv, _ := outreach.WaveByName(p.Wave)
	return outreachPostView{
		ID: p.ID, OutreachID: p.OutreachID, Channel: p.Channel, Wave: p.Wave, Body: p.Body,
		Status: p.Status, Prepared: p.Prepared(), Customized: p.Customized,
		Stale:        p.Status != "posted" && p.SourceFingerprint != outreach.Fingerprint(f),
		Held:         (p.Status == "scheduled" || p.Status == "queued") && workshopStatus != "published",
		ShortLinkKey: p.ShortLinkKey, ShortURL: p.ShortURL,
		DestinationURL: outreach.DestinationURL(h.baseURL, f, ch, wv),
		UTM:            outreachUTMView{Source: ch.UTMSource, Medium: ch.UTMMedium, Campaign: f.Slug, Content: wv.Name},
		ScheduledAt:    rfc3339(p.ScheduledAt), PostedAt: rfc3339(p.PostedAt), RemoteURL: p.RemoteURL, LastError: p.LastError,
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: rfc3339(p.UpdatedAt),
	}
}

// outreachChannelView is a channel plus whether the site can post to it
// itself. An automatic channel that is not configured behaves exactly like a
// manual one in the UI.
type outreachChannelView struct {
	outreach.Channel
	Configured bool `json:"configured"`
	// Account is the public account the channel posts as (#0581), empty for a
	// manual channel or one with nothing set.
	Account string `json:"account"`
}

func (h *AdminOutreachHandler) channelConfigured(name string) bool {
	ch, ok := outreach.ChannelByName(name)
	return ok && ch.Automatic && h.posting != nil && h.posting.ChannelConfigured(name)
}

func (h *AdminOutreachHandler) channelViews() []outreachChannelView {
	chs := h.enabledChannels()
	out := make([]outreachChannelView, 0, len(chs))
	for _, ch := range chs {
		v := outreachChannelView{Channel: ch, Configured: h.channelConfigured(ch.Name)}
		if ch.Automatic && h.accountFn != nil {
			v.Account = h.accountFn(ch.Name)
		}
		out = append(out, v)
	}
	return out
}

type outreachWaveView struct {
	Name        string  `json:"name"`
	Label       string  `json:"label"`
	SuggestedAt *string `json:"suggested_at"`
}

type outreachRowView struct {
	ID           int64  `json:"id"`
	WorkshopID   int64  `json:"workshop_id"`
	CampaignSlug string `json:"campaign_slug"`
	CreatedAt    string `json:"created_at"`
}

type outreachWorkshopView struct {
	ID       int64   `json:"id"`
	Slug     string  `json:"slug"`
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	StartsAt *string `json:"starts_at"`
	// Summary and CoverImage let the previews draw the link card (#0583).
	Summary    *string `json:"summary"`
	CoverImage *string `json:"cover_image"`
}

type outreachPanelView struct {
	Available         bool                  `json:"available"`
	UnavailableReason string                `json:"unavailable_reason"`
	Workshop          outreachWorkshopView  `json:"workshop"`
	Outreach          *outreachRowView      `json:"outreach"`
	Channels          []outreachChannelView `json:"channels"`
	Waves             []outreachWaveView    `json:"waves"`
	Posts             []outreachPostView    `json:"posts"`
	// CanReset is true when an outreach exists and nothing for it has gone
	// public (#0592); the Reset button shows from it.
	CanReset bool `json:"can_reset"`
	// BlueskyCard is which URL the Bluesky card points at, "short" or
	// "canonical" (#0583); the preview shows the matching host.
	BlueskyCard string `json:"bluesky_card"`
	// LinkReport is set only on Start's response (#0568).
	LinkReport *outreachLinkReport `json:"link_report,omitempty"`
}

func (h *AdminOutreachHandler) panel(ctx context.Context, wk workshops.Workshop, o *outreach.Outreach) (outreachPanelView, error) {
	v := outreachPanelView{
		Workshop:    outreachWorkshopView{ID: wk.ID, Slug: wk.Slug, Title: wk.Title, Status: wk.Status, StartsAt: rfc3339(wk.StartsAt), Summary: nonEmptyPtr(wk.Summary), CoverImage: nonEmptyPtr(wk.CoverImage)},
		BlueskyCard: h.blueskyCard(),
		Channels:    h.channelViews(),
		Waves:       []outreachWaveView{},
		Posts:       []outreachPostView{},
	}
	switch {
	case !h.linksEnabled():
		v.UnavailableReason = "shortlinks_not_configured"
	case wk.Status != "published":
		v.UnavailableReason = "workshop_not_published"
	default:
		v.Available = true
	}
	for _, w := range outreach.Waves() {
		wv := outreachWaveView{Name: w.Name, Label: w.Label}
		wv.SuggestedAt = rfc3339(outreach.SuggestedAt(w, wk.StartsAt, h.postHour()))
		v.Waves = append(v.Waves, wv)
	}
	if o != nil {
		v.Outreach = &outreachRowView{ID: o.ID, WorkshopID: o.WorkshopID, CampaignSlug: o.CampaignSlug, CreatedAt: o.CreatedAt.UTC().Format(time.RFC3339)}
		posts, err := h.store.ListPosts(ctx, o.ID)
		if err != nil {
			return v, err
		}
		if v.CanReset, err = h.store.CanReset(ctx, o.ID); err != nil {
			return v, err
		}
		f := h.facts(wk)
		for _, p := range h.enabledPosts(posts) {
			v.Posts = append(v.Posts, h.postView(p, f, wk.Status))
		}
	}
	return v, nil
}

func outreachPathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

type outreachSummaryView struct {
	OutreachID     int64          `json:"outreach_id"`
	WorkshopID     int64          `json:"workshop_id"`
	WorkshopSlug   string         `json:"workshop_slug"`
	WorkshopTitle  string         `json:"workshop_title"`
	WorkshopStatus string         `json:"workshop_status"`
	CampaignSlug   string         `json:"campaign_slug"`
	StartsAt       *string        `json:"starts_at"`
	Counts         map[string]int `json:"counts"`
	CreatedAt      string         `json:"created_at"`
}

// List handles GET /admin/outreach: every workshop with outreach.
func (h *AdminOutreachHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.ListSummaries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views := make([]outreachSummaryView, 0, len(rows))
	for _, s := range rows {
		views = append(views, outreachSummaryView{
			OutreachID: s.OutreachID, WorkshopID: s.WorkshopID, WorkshopSlug: s.WorkshopSlug,
			WorkshopTitle: s.WorkshopTitle, WorkshopStatus: s.WorkshopStatus, CampaignSlug: s.CampaignSlug,
			StartsAt: rfc3339(s.StartsAt), Counts: s.Counts, CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"outreach": views})
}

// loadWorkshop writes the response and returns ok=false on failure.
func (h *AdminOutreachHandler) loadWorkshop(w http.ResponseWriter, r *http.Request, id int64) (workshops.Workshop, bool) {
	wk, err := h.ws.GetByID(r.Context(), id)
	switch {
	case err == nil:
		return wk, true
	case errors.Is(err, workshops.ErrNotFound):
		writeError(w, http.StatusNotFound, "workshop not found")
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
	return workshops.Workshop{}, false
}

// Get handles GET /admin/workshops/{id}/outreach. Always 200 for a known
// workshop; the view says whether outreach is available and why not.
func (h *AdminOutreachHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	wk, ok := h.loadWorkshop(w, r, id)
	if !ok {
		return
	}
	var op *outreach.Outreach
	o, err := h.store.GetByWorkshop(r.Context(), id)
	switch {
	case err == nil:
		op = &o
	case errors.Is(err, outreach.ErrNotFound):
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	v, err := h.panel(r.Context(), wk, op)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// Start handles POST /admin/workshops/{id}/outreach: ensure the ShortLinks
// campaign and create the outreach row. 503 when ShortLinks is not
// configured, 409 unless the workshop is published, 200 when outreach already
// exists (no ShortLinks call), 201 when created.
func (h *AdminOutreachHandler) Start(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	if !h.linksEnabled() {
		writeError(w, http.StatusServiceUnavailable, "ShortLinks is not configured")
		return
	}
	wk, ok := h.loadWorkshop(w, r, id)
	if !ok {
		return
	}
	if wk.Status != "published" {
		writeError(w, http.StatusConflict, "outreach can only start for a published workshop")
		return
	}
	if existing, err := h.store.GetByWorkshop(r.Context(), id); err == nil {
		v, perr := h.panel(r.Context(), wk, &existing)
		if perr != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	} else if !errors.Is(err, outreach.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	req := shortlinks.CreateCampaignRequest{Description: wk.Title, DefaultUTMCampaign: wk.Slug}
	if wk.StartsAt != nil {
		req.StartsAt = wk.StartsAt.UTC().Format(time.RFC3339)
	}
	if wk.EndsAt != nil {
		req.EndsAt = wk.EndsAt.UTC().Format(time.RFC3339)
	}
	camp, err := h.links.EnsureCampaign(r.Context(), wk.Slug, req)
	if err != nil {
		writeShortLinksError(w, "ensure campaign", err)
		return
	}
	o, created, err := h.store.Create(r.Context(), id, camp.Slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		if h.auditor != nil {
			actorID := actor.ID
			targetID := o.ID
			h.auditor.Record(r.Context(), audit.Entry{
				ActorID:    &actorID,
				Action:     audit.ActionOutreachStarted,
				TargetType: audit.TargetOutreach,
				TargetID:   &targetID,
				Metadata:   map[string]any{"workshop_id": o.WorkshopID, "campaign_slug": o.CampaignSlug},
				IP:         clientIP(r),
			})
		}
	}
	var report *outreachLinkReport
	if created {
		rep, lerr := h.ensureLinks(r, actor.ID, o, wk)
		if lerr != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		report = &rep
	}
	v, err := h.panel(r.Context(), wk, &o)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	v.LinkReport = report
	writeJSON(w, status, v)
}

type createOutreachPostRequest struct {
	Channel string `json:"channel"`
	Wave    string `json:"wave"`
	Body    string `json:"body"`
}

// CreatePost handles POST /admin/outreach/{id}/posts. An omitted body is
// generated from the channel template.
func (h *AdminOutreachHandler) CreatePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	var req createOutreachPostRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ch, chOK := outreach.ChannelByName(req.Channel)
	wv, wvOK := outreach.WaveByName(req.Wave)
	if !chOK || !wvOK {
		writeError(w, http.StatusBadRequest, "unknown channel or wave")
		return
	}
	if !h.channelEnabled(ch.Name) {
		writeError(w, http.StatusConflict, ch.Label+" is switched off in Settings")
		return
	}
	if len(req.Body) > maxOutreachBodyBytes {
		writeError(w, http.StatusBadRequest, "body is too long")
		return
	}
	o, err := h.store.GetByID(r.Context(), id)
	if errors.Is(err, outreach.ErrNotFound) {
		writeError(w, http.StatusNotFound, "outreach not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	wk, ok := h.loadWorkshop(w, r, o.WorkshopID)
	if !ok {
		return
	}
	f := h.facts(wk)
	body := req.Body
	if strings.TrimSpace(body) == "" {
		body = outreach.Draft(ch, wv, f)
	}
	p, err := h.store.CreatePost(r.Context(), o.ID, ch.Name, wv.Name, body, outreach.Fingerprint(f))
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrPostExists):
		writeError(w, http.StatusConflict, "a post already exists for that channel and wave")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	// A slot whose link was created up front (#0568) opens already prepared.
	linkApplied := false
	switch pl, lerr := h.store.GetPostLink(r.Context(), o.ID, p.Channel, p.Wave); {
	case lerr == nil:
		status := p.Status
		if !ch.Automatic {
			status = "manual"
		}
		prepared, _, perr := h.store.SetPrepared(r.Context(), p.ID, pl.Key, pl.ShortURL, status)
		if perr != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		p = prepared
		linkApplied = true
	case errors.Is(lerr, outreach.ErrNotFound):
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := p.ID
		meta := map[string]any{"outreach_id": o.ID, "channel": p.Channel, "wave": p.Wave}
		if linkApplied {
			meta["link_applied"] = true
		}
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachPostCreated,
			TargetType: audit.TargetOutreachPost,
			TargetID:   &targetID,
			Metadata:   meta,
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusCreated, h.postView(p, f, wk.Status))
}

// postContext loads a post, its outreach and its workshop; ok=false means the
// response is written.
func (h *AdminOutreachHandler) postContext(w http.ResponseWriter, r *http.Request, id int64) (outreach.Post, workshops.Workshop, bool) {
	p, err := h.store.GetPost(r.Context(), id)
	if errors.Is(err, outreach.ErrNotFound) {
		writeError(w, http.StatusNotFound, "post not found")
		return p, workshops.Workshop{}, false
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return p, workshops.Workshop{}, false
	}
	o, err := h.store.GetByID(r.Context(), p.OutreachID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return p, workshops.Workshop{}, false
	}
	wk, ok := h.loadWorkshop(w, r, o.WorkshopID)
	return p, wk, ok
}

type patchOutreachPostRequest struct {
	Body       *string `json:"body"`
	Regenerate *bool   `json:"regenerate"`
	// Customized marks a hand edit so a shared-message save skips the post
	// (#0582). Only valid with body.
	Customized *bool `json:"customized"`
}

// PatchPost handles PATCH /admin/outreach-posts/{id}: exactly one of
// {"body"} or {"regenerate": true}.
func (h *AdminOutreachHandler) PatchPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	var req patchOutreachPostRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	regen := req.Regenerate != nil && *req.Regenerate
	if (req.Body != nil) == regen {
		writeError(w, http.StatusBadRequest, "send exactly one of body or regenerate")
		return
	}
	if regen && req.Customized != nil {
		writeError(w, http.StatusBadRequest, "customized goes with body, not regenerate")
		return
	}
	p, wk, ok := h.postContext(w, r, id)
	if !ok {
		return
	}
	f := h.facts(wk)
	var updated outreach.Post
	var err error
	if regen {
		ch, _ := outreach.ChannelByName(p.Channel)
		wv, _ := outreach.WaveByName(p.Wave)
		body := outreach.Draft(ch, wv, f)
		if p.ShortURL != nil {
			body = strings.ReplaceAll(body, outreach.LinkToken, *p.ShortURL)
		}
		fp := outreach.Fingerprint(f)
		updated, err = h.store.UpdatePostBody(r.Context(), id, body, &fp, nil)
	} else {
		if strings.TrimSpace(*req.Body) == "" {
			writeError(w, http.StatusBadRequest, "body cannot be empty")
			return
		}
		if len(*req.Body) > maxOutreachBodyBytes {
			writeError(w, http.StatusBadRequest, "body is too long")
			return
		}
		updated, err = h.store.UpdatePostBody(r.Context(), id, *req.Body, nil, req.Customized)
	}
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrPostLocked):
		writeError(w, http.StatusConflict, "post is already posted")
		return
	case errors.Is(err, outreach.ErrPostInFlight):
		writeError(w, http.StatusConflict, "post is being sent")
		return
	case errors.Is(err, outreach.ErrNotFound):
		writeError(w, http.StatusNotFound, "post not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachPostUpdated,
			TargetType: audit.TargetOutreachPost,
			TargetID:   &targetID,
			Metadata:   map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave, "regenerated": regen, "customized": updated.Customized},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, h.postView(updated, f, wk.Status))
}

// Prepare handles POST /admin/outreach-posts/{id}/prepare: create the tagged
// short link and put its URL into the body. Idempotent; an already-prepared
// post is returned unchanged with no ShortLinks call.
func (h *AdminOutreachHandler) Prepare(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	if !h.linksEnabled() {
		writeError(w, http.StatusServiceUnavailable, "ShortLinks is not configured")
		return
	}
	p, wk, ok := h.postContext(w, r, id)
	if !ok {
		return
	}
	f := h.facts(wk)
	if p.Prepared() {
		writeJSON(w, http.StatusOK, h.postView(p, f, wk.Status))
		return
	}
	if wk.Status != "published" {
		writeError(w, http.StatusConflict, "outreach is only available for a published workshop")
		return
	}
	o, err := h.store.GetByID(r.Context(), p.OutreachID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	ch, _ := outreach.ChannelByName(p.Channel)
	wv, _ := outreach.WaveByName(p.Wave)
	var linkKey, linkURL string
	switch pl, lerr := h.store.GetPostLink(r.Context(), o.ID, p.Channel, p.Wave); {
	case lerr == nil:
		// Created up front (#0568): no ShortLinks call at all.
		linkKey, linkURL = pl.Key, pl.ShortURL
	case errors.Is(lerr, outreach.ErrNotFound):
		base, berr := h.shortLinksBase(r.Context())
		if berr != nil {
			writeShortLinksError(w, "me", berr)
			return
		}
		link, cerr := h.links.CreateLink(r.Context(), h.postLinkRequest(o, wk, ch, wv))
		if cerr != nil {
			writeShortLinksError(w, "create link", cerr)
			return
		}
		// A racing Create links may have won the slot: use the stored row.
		row, _, ierr := h.store.InsertPostLink(r.Context(), o.ID, ch.Name, wv.Name, link.Key, base+"/"+link.Key)
		if ierr != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		linkKey, linkURL = row.Key, row.ShortURL
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	status := p.Status
	if !ch.Automatic {
		status = "manual"
	}
	updated, changed, err := h.store.SetPrepared(r.Context(), id, linkKey, linkURL, status)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrPostLocked):
		writeError(w, http.StatusConflict, "post is already posted")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	// A racing Prepare that lost gets the winner's row back with changed
	// false, so only the call whose UPDATE actually set the key audits. (The
	// stored key cannot decide this: ShortLinks de-duplicates the link, so
	// racing callers hold the same key.)
	if h.auditor != nil && changed {
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachPostPrepared,
			TargetType: audit.TargetOutreachPost,
			TargetID:   &targetID,
			Metadata:   map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave, "short_link_key": linkKey},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, h.postView(updated, f, wk.Status))
}

type markOutreachPostedRequest struct {
	RemoteURL *string `json:"remote_url"`
}

// validRemoteURL accepts an absolute http(s) URL of at most 2048 characters.
func validRemoteURL(s string) bool {
	if len(s) > 2048 {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// MarkPosted handles POST /admin/outreach-posts/{id}/posted.
func (h *AdminOutreachHandler) MarkPosted(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	var req markOutreachPostedRequest
	if err := decodeJSON(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RemoteURL != nil && strings.TrimSpace(*req.RemoteURL) == "" {
		req.RemoteURL = nil
	}
	if req.RemoteURL != nil && !validRemoteURL(*req.RemoteURL) {
		writeError(w, http.StatusBadRequest, "remote_url must be an absolute http(s) URL")
		return
	}
	_, wk, ok := h.postContext(w, r, id)
	if !ok {
		return
	}
	updated, err := h.store.MarkPosted(r.Context(), id, req.RemoteURL, time.Now())
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrNotPrepared):
		writeError(w, http.StatusConflict, "prepare the post before marking it posted")
		return
	case errors.Is(err, outreach.ErrPostLocked):
		writeError(w, http.StatusConflict, "post is already posted")
		return
	case errors.Is(err, outreach.ErrPostInFlight):
		writeError(w, http.StatusConflict, "post is being sent")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachPostPosted,
			TargetType: audit.TargetOutreachPost,
			TargetID:   &targetID,
			Metadata:   map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave, "has_remote_url": updated.RemoteURL != nil},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, h.postView(updated, h.facts(wk), wk.Status))
}

// UnmarkPosted handles POST /admin/outreach-posts/{id}/unmark (#0588): take back
// a Mark posted, for a channel the site does not post to itself. A post the
// site sent through a configured channel is really public and is refused.
func (h *AdminOutreachHandler) UnmarkPosted(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	p, wk, ok := h.postContext(w, r, id)
	if !ok {
		return
	}
	if h.channelConfigured(p.Channel) {
		writeError(w, http.StatusConflict, "this post was sent by the site and cannot be unmarked")
		return
	}
	updated, err := h.store.MarkUnposted(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrWrongStatus):
		writeError(w, http.StatusConflict, "post is not marked as posted")
		return
	case errors.Is(err, outreach.ErrNotFound):
		writeError(w, http.StatusNotFound, "post not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachPostUnmarked,
			TargetType: audit.TargetOutreachPost,
			TargetID:   &targetID,
			Metadata:   map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, h.postView(updated, h.facts(wk), wk.Status))
}

// DeletePost handles DELETE /admin/outreach-posts/{id}.
func (h *AdminOutreachHandler) DeletePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	p, err := h.store.GetPost(r.Context(), id)
	if errors.Is(err, outreach.ErrNotFound) {
		writeError(w, http.StatusNotFound, "post not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	switch err := h.store.DeletePost(r.Context(), id); {
	case err == nil:
	case errors.Is(err, outreach.ErrPostLocked):
		writeError(w, http.StatusConflict, "post is already posted")
		return
	case errors.Is(err, outreach.ErrPostInFlight):
		writeError(w, http.StatusConflict, "post is being sent")
		return
	case errors.Is(err, outreach.ErrNotFound):
		writeError(w, http.StatusNotFound, "post not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := p.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachPostDeleted,
			TargetType: audit.TargetOutreachPost,
			TargetID:   &targetID,
			Metadata:   map[string]any{"outreach_id": p.OutreachID, "channel": p.Channel, "wave": p.Wave},
			IP:         clientIP(r),
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// Reset handles POST /admin/workshops/{id}/outreach/reset (#0592): remove the
// workshop's posts and flyers so drafting can start over. 409 when anything
// has gone public. The outreach row and its short links are kept (ShortLinks
// has no delete), so the next Draft reuses the same links.
func (h *AdminOutreachHandler) Reset(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	wk, ok := h.loadWorkshop(w, r, id)
	if !ok {
		return
	}
	o, err := h.store.GetByWorkshop(r.Context(), id)
	if errors.Is(err, outreach.ErrNotFound) {
		writeError(w, http.StatusNotFound, "outreach has not started for this workshop")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	res, err := h.store.Reset(r.Context(), o.ID)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrResetPosted):
		writeError(w, http.StatusConflict, "Something has already been posted, so outreach cannot be reset.")
		return
	case errors.Is(err, outreach.ErrResetInFlight):
		writeError(w, http.StatusConflict, "A post is queued or being sent, so outreach cannot be reset.")
		return
	case errors.Is(err, outreach.ErrResetFlyerPutUp):
		writeError(w, http.StatusConflict, "A flyer is marked put up, so outreach cannot be reset.")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := o.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachReset,
			TargetType: audit.TargetOutreach,
			TargetID:   &targetID,
			Metadata:   map[string]any{"workshop_id": o.WorkshopID, "posts_removed": res.Posts, "flyers_removed": res.Flyers},
			IP:         clientIP(r),
		})
	}
	v, err := h.panel(r.Context(), wk, &o)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, v)
}
