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
	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
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
	// ListPostsByOutreach is ListPosts for many outreaches in one query (#0593).
	ListPostsByOutreach(ctx context.Context, outreachIDs []int64) (map[int64][]outreach.Post, error)
	GetPost(ctx context.Context, id int64) (outreach.Post, error)
	CreatePost(ctx context.Context, outreachID int64, channel, wave, body, fingerprint string) (outreach.Post, error)
	UpdatePostBody(ctx context.Context, id int64, body string, fingerprint *string, customized *bool) (outreach.Post, error)
	// SetPrepared's bool is true only when this call's UPDATE changed the row.
	SetPrepared(ctx context.Context, id int64, key, shortURL, status string) (outreach.Post, bool, error)
	MarkPosted(ctx context.Context, id int64, remoteURL *string, now time.Time) (outreach.Post, error)
	// MarkUnposted undoes a hand-posted mark (#0588).
	MarkUnposted(ctx context.Context, id int64) (outreach.Post, error)
	// CheckTakedown and CompleteTakedown (#0604): delete a posted automatic
	// post and return it to draft.
	CheckTakedown(ctx context.Context, id int64) (outreach.Post, error)
	CompleteTakedown(ctx context.Context, id int64, remoteID string) (outreach.Post, error)
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
	ScheduleMany(ctx context.Context, ids []int64, at time.Time) ([]outreach.Post, error)
	UnscheduleMany(ctx context.Context, ids []int64) ([]outreach.Post, error)
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
	// DeleteRemote deletes a post from its network; existed is false when it
	// was already gone, which is success (#0604).
	DeleteRemote(ctx context.Context, p outreach.Post) (existed bool, err error)
}

// outreachWorkshopReader loads the workshop an outreach belongs to.
type outreachWorkshopReader interface {
	GetByID(ctx context.Context, id int64) (workshops.Workshop, error)
	// ListVisible feeds the home timeline (#0646).
	ListVisible(ctx context.Context, now time.Time) (upcoming, past []workshops.Workshop, err error)
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
	// luma reads the Luma calendar feed for GET /admin/outreach-luma (#0648); nil means off.
	luma      lumaFeed
	store     outreachStore
	locations outreachLocationStore
	flyers    outreachFlyerStore
	ws        outreachWorkshopReader
	links     shortLinker
	auditor   *audit.Logger
	baseURL   string
	posting   outreachPosting
	mu        sync.Mutex // guards shortBase and stats; never held across a network call
	shortBase string     // cached Me().BaseURL; a failed fetch is not cached
	// stats memoizes campaign stats for the results routes (#0546). Always
	// built; fetchStats answers ErrUnavailable while ShortLinks is not
	// configured, and ResetShortLinks replaces it when the connection changes.
	stats *shortlinks.StatsCache
	// conns serves the connection-settings routes; nil when the server runs
	// without a database.
	conns *AdminOutreachConnectionsHandler
	// guidePosts serves the guide-post routes (#0659); nil when unattached.
	guidePosts *AdminGuidePostsHandler
	now        func() time.Time
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

// SetGuidePosts attaches the guide-post handler (#0659), so its routes are
// mounted exactly when Outreach is.
func (h *AdminOutreachHandler) SetGuidePosts(g *AdminGuidePostsHandler) {
	if h != nil {
		h.guidePosts = g
	}
}

// GuidePosts returns the attached guide-post handler, or nil.
func (h *AdminOutreachHandler) GuidePosts() *AdminGuidePostsHandler {
	if h == nil {
		return nil
	}
	return h.guidePosts
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
// is made for it, and the panel, results and links views leave out its drafts
// (postVisible keeps its scheduled, queued, posted and failed posts shown);
// posts and links it already has are kept, and come back if it is switched on
// again.
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

// enabledChannelNames lists the switched-on channel names, never nil.
func (h *AdminOutreachHandler) enabledChannelNames() []string {
	out := []string{}
	for _, c := range h.enabledChannels() {
		out = append(out, c.Name)
	}
	return out
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

// postVisible reports whether a post is shown and counted (#0588). A
// switched-off channel hides only the posts that never left drafting (draft
// and manual). One that is scheduled, queued, posted or failed stays: a
// scheduled post still goes out, so hiding it would post unseen, and a posted
// one is public history.
func (h *AdminOutreachHandler) postVisible(p outreach.Post) bool {
	return h.channelEnabled(p.Channel) || (p.Status != "draft" && p.Status != "manual")
}

// enabledPosts keeps the visible posts (postVisible).
func (h *AdminOutreachHandler) enabledPosts(posts []outreach.Post) []outreach.Post {
	out := make([]outreach.Post, 0, len(posts))
	for _, p := range posts {
		if h.postVisible(p) {
			out = append(out, p)
		}
	}
	return out
}

// shownChannels is the switched-on channels plus any switched-off one that
// still has a visible post, in channel order, so that post keeps its card.
func (h *AdminOutreachHandler) shownChannels(posts []outreach.Post) []outreach.Channel {
	has := map[string]bool{}
	for _, p := range h.enabledPosts(posts) {
		has[p.Channel] = true
	}
	var out []outreach.Channel
	for _, ch := range outreach.Channels() {
		if h.channelEnabled(ch.Name) || has[ch.Name] {
			out = append(out, ch)
		}
	}
	return out
}

// offChannels is every switched-off channel, in channel order (#0628), so
// the board can name one that has no post and therefore no column.
func (h *AdminOutreachHandler) offChannels() []outreach.Channel {
	out := []outreach.Channel{}
	for _, ch := range outreach.Channels() {
		if !h.channelEnabled(ch.Name) {
			out = append(out, ch)
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

// cardHost is the host of the site's base URL, "" when it does not parse.
func (h *AdminOutreachHandler) cardHost() string {
	u, err := url.Parse(strings.TrimSpace(h.baseURL))
	if err != nil {
		return ""
	}
	return u.Host
}

// thumbStatuser is the method thumbStatus looks for on the posting seam. The
// assertion below keeps a rename of Publisher.ThumbStatus a compile error
// instead of a quiet thumb_ok of false (#0597, #0601).
type thumbStatuser interface {
	ThumbStatus(cover *string) (bool, string)
}

var _ thumbStatuser = (*outreach.Publisher)(nil)

// thumbStatus asks the publisher whether it would attach the cover and, if
// not, why. A posting seam that cannot say answers false with no reason, as
// the publisher would send no picture.
func (h *AdminOutreachHandler) thumbStatus(cover *string) (bool, string) {
	t, ok := h.posting.(thumbStatuser)
	if !ok {
		return false, ""
	}
	return t.ThumbStatus(cover)
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
	// FreshBody: the text Regenerate would write, for a hand-posted post that can still change (#0635).
	FreshBody *string `json:"fresh_body,omitempty"`
}

// regeneratedBody is what Regenerate writes: today's template with the post's link in place.
func regeneratedBody(p outreach.Post, f outreach.Facts) string {
	ch, _ := outreach.ChannelByName(p.Channel)
	wv, _ := outreach.WaveByName(p.Wave)
	body := outreach.Draft(ch, wv, f)
	if p.ShortURL != nil {
		body = strings.ReplaceAll(body, outreach.LinkToken, *p.ShortURL)
	}
	return body
}

func (h *AdminOutreachHandler) postView(p outreach.Post, f outreach.Facts, workshopStatus string) outreachPostView {
	ch, _ := outreach.ChannelByName(p.Channel)
	wv, _ := outreach.WaveByName(p.Wave)
	v := outreachPostView{
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
	if p.Status != "posted" && p.Status != "queued" && !(ch.Automatic && h.channelConfigured(ch.Name)) {
		fresh := regeneratedBody(p, f)
		v.FreshBody = &fresh
	}
	return v
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

func (h *AdminOutreachHandler) channelViews(posts []outreach.Post) []outreachChannelView {
	chs := h.shownChannels(posts)
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
	// CoverAlt is always present ("" when unset): the Mastodon preview keys
	// on it to draw the attachment or the missing-alt notice (#0572).
	Summary    *string `json:"summary"`
	CoverImage *string `json:"cover_image"`
	CoverAlt   string  `json:"cover_alt"`
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
	// CardHost is the host of the site's base URL, which the canonical Bluesky
	// card shows; the SPA no longer guesses it from its own location (#0593).
	CardHost string `json:"card_host"`
	// ThumbOK is true when the publisher would attach the workshop cover as
	// the Bluesky card's picture (#0593).
	ThumbOK bool `json:"thumb_ok"`
	// ThumbReason is the plain reason there is no picture, set only when
	// ThumbOK is false and the publisher could say why (#0601).
	ThumbReason string `json:"thumb_reason,omitempty"`
	// LinkReport is set only on Start's response (#0568).
	LinkReport *outreachLinkReport `json:"link_report,omitempty"`
	// HandLinks is one row per switched-on hand-posted channel by wave (#0623).
	HandLinks []outreachHandLinkView `json:"hand_links"`
	// HandLinksPending is true when outreach is available and a row has no URL,
	// and the event has not started (#0638).
	HandLinksPending bool `json:"hand_links_pending"`
	// OffChannels is every switched-off channel, in channel order (#0628).
	OffChannels []outreach.Channel `json:"off_channels"`
}

// workshopView is the workshop part of the panel and of a timeline event.
func (h *AdminOutreachHandler) workshopView(wk workshops.Workshop) outreachWorkshopView {
	return outreachWorkshopView{ID: wk.ID, Slug: wk.Slug, Title: wk.Title, Status: wk.Status, StartsAt: rfc3339(wk.StartsAt), Summary: nonEmptyPtr(wk.Summary), CoverImage: nonEmptyPtr(wk.CoverImage), CoverAlt: derefOrEmpty(wk.CoverAlt)}
}

// waveViews lists every section with its suggested time for wk.
func (h *AdminOutreachHandler) waveViews(wk workshops.Workshop) []outreachWaveView {
	out := []outreachWaveView{}
	for _, w := range outreach.Waves() {
		wv := outreachWaveView{Name: w.Name, Label: w.Label}
		wv.SuggestedAt = rfc3339(outreach.SuggestedAt(w, wk.StartsAt, h.postHour()))
		out = append(out, wv)
	}
	return out
}

func (h *AdminOutreachHandler) panel(ctx context.Context, wk workshops.Workshop, o *outreach.Outreach) (outreachPanelView, error) {
	thumbOK, thumbReason := h.thumbStatus(wk.CoverImage)
	v := outreachPanelView{
		Workshop:    h.workshopView(wk),
		BlueskyCard: h.blueskyCard(),
		CardHost:    h.cardHost(),
		ThumbOK:     thumbOK,
		ThumbReason: thumbReason,
		Channels:    h.channelViews(nil),
		Waves:       h.waveViews(wk),
		Posts:       []outreachPostView{},
		HandLinks:   []outreachHandLinkView{},
		OffChannels: h.offChannels(),
	}
	switch {
	case !h.linksEnabled():
		v.UnavailableReason = "shortlinks_not_configured"
	case wk.Status != "published":
		v.UnavailableReason = "workshop_not_published"
	default:
		v.Available = true
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
		links, err := h.store.ListPostLinks(ctx, o.ID)
		if err != nil {
			return v, err
		}
		v.HandLinks, v.HandLinksPending = h.handLinkViews(links, posts)
		v.HandLinksPending = v.HandLinksPending && v.Available && !h.eventStarted(wk)
		f := h.facts(wk)
		for _, p := range h.enabledPosts(posts) {
			v.Posts = append(v.Posts, h.postView(p, f, wk.Status))
		}
		v.Channels = h.channelViews(posts)
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
	views, err := h.listSummaries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"outreach": views, "enabled_channels": h.enabledChannelNames()})
}

// listSummaries is List without the transport (#0697): every workshop with
// outreach, with per-status post counts. list_outreach calls it too.
func (h *AdminOutreachHandler) listSummaries(ctx context.Context) ([]outreachSummaryView, error) {
	rows, err := h.store.ListSummaries(ctx)
	if err != nil {
		return nil, err
	}
	postsBy, err := h.postsByOutreach(ctx, rows)
	if err != nil {
		return nil, err
	}
	views := make([]outreachSummaryView, 0, len(rows))
	for _, s := range rows {
		// A switched-off channel's drafts are not counted (#0593), the same
		// rule the panel and the results overview apply.
		counts := map[string]int{}
		for _, p := range h.enabledPosts(postsBy[s.OutreachID]) {
			counts[p.Status]++
		}
		s.Counts = counts
		views = append(views, outreachSummaryView{
			OutreachID: s.OutreachID, WorkshopID: s.WorkshopID, WorkshopSlug: s.WorkshopSlug,
			WorkshopTitle: s.WorkshopTitle, WorkshopStatus: s.WorkshopStatus, CampaignSlug: s.CampaignSlug,
			StartsAt: rfc3339(s.StartsAt), Counts: s.Counts, CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return views, nil
}

// postsByOutreach loads every summary's posts in one query.
func (h *AdminOutreachHandler) postsByOutreach(ctx context.Context, sums []outreach.Summary) (map[int64][]outreach.Post, error) {
	ids := make([]int64, 0, len(sums))
	for _, s := range sums {
		ids = append(ids, s.OutreachID)
	}
	return h.store.ListPostsByOutreach(ctx, ids)
}

// loadWorkshop writes the response and returns ok=false on failure.
func (h *AdminOutreachHandler) loadWorkshop(w http.ResponseWriter, r *http.Request, id int64) (workshops.Workshop, bool) {
	wk, err := h.ws.GetByID(r.Context(), id)
	switch {
	case err == nil:
		return wk, true
	case errors.Is(err, workshops.ErrNotFound):
		writeError(w, http.StatusNotFound, "event not found")
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
	v, err := h.panelFor(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, workshops.ErrNotFound):
		writeError(w, http.StatusNotFound, "event not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// panelFor is Get without the transport (#0697): the outreach panel for
// workshop id, or workshops.ErrNotFound. get_event_outreach calls it too.
func (h *AdminOutreachHandler) panelFor(ctx context.Context, id int64) (outreachPanelView, error) {
	wk, err := h.ws.GetByID(ctx, id)
	if err != nil {
		return outreachPanelView{}, err
	}
	var op *outreach.Outreach
	o, err := h.store.GetByWorkshop(ctx, id)
	switch {
	case err == nil:
		op = &o
	case errors.Is(err, outreach.ErrNotFound):
	default:
		return outreachPanelView{}, err
	}
	return h.panel(ctx, wk, op)
}

// outreachOpError is a refusal from a transport-free outreach write
// (#0699): the admin routes write status and msg as they always have, and
// /api/v1 maps it through outreachAPIError. field names the input at fault
// for a 400.
type outreachOpError struct {
	status int
	field  string
	msg    string
	// v1msg, when set, replaces msg on the v1 API, which never carries an
	// underlying error's text (api.AsError).
	v1msg string
}

func (e *outreachOpError) Error() string { return e.msg }

func outreachRefusal(status int, field, msg string) error {
	return &outreachOpError{status: status, field: field, msg: msg}
}

// outreachLinksError carries a ShortLinks failure out of a transport-free
// outreach write, so the admin route can still answer writeShortLinksError.
type outreachLinksError struct{ err error }

func (e outreachLinksError) Error() string { return "outreach short links: " + e.err.Error() }
func (e outreachLinksError) Unwrap() error { return e.err }

// writeOutreachError answers an admin request with err from a
// transport-free outreach write.
func writeOutreachError(w http.ResponseWriter, op string, err error) {
	var oe *outreachOpError
	var le outreachLinksError
	switch {
	case errors.As(err, &oe):
		writeError(w, oe.status, oe.msg)
	case errors.As(err, &le):
		writeShortLinksError(w, op, le.err)
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

// postErrorRefusal maps the store's post sentinels to refusals; any other
// error is returned as is.
func postErrorRefusal(err error) error {
	switch {
	case errors.Is(err, outreach.ErrPostLocked):
		return outreachRefusal(http.StatusConflict, "", "post is already posted")
	case errors.Is(err, outreach.ErrPostInFlight):
		return outreachRefusal(http.StatusConflict, "", "post is being sent")
	case errors.Is(err, outreach.ErrNotFound):
		return outreachRefusal(http.StatusNotFound, "", "post not found")
	}
	return err
}

// workshopFor loads workshop id, turning not-found into a refusal.
func (h *AdminOutreachHandler) workshopFor(ctx context.Context, id int64) (workshops.Workshop, error) {
	wk, err := h.ws.GetByID(ctx, id)
	if errors.Is(err, workshops.ErrNotFound) {
		return wk, outreachRefusal(http.StatusNotFound, "", "event not found")
	}
	return wk, err
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
	actorID := actor.ID
	v, created, err := h.startOutreach(r.Context(), clientIP(r), &actorID, id)
	if err != nil {
		writeOutreachError(w, "ensure campaign", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, v)
}

// startOutreach is Start without the transport (#0699); created reports a
// new outreach row. start_event_outreach calls it too.
func (h *AdminOutreachHandler) startOutreach(ctx context.Context, ip string, actorID *int64, id int64) (outreachPanelView, bool, error) {
	if !h.linksEnabled() {
		return outreachPanelView{}, false, outreachRefusal(http.StatusServiceUnavailable, "", "ShortLinks is not configured")
	}
	wk, err := h.workshopFor(ctx, id)
	if err != nil {
		return outreachPanelView{}, false, err
	}
	if wk.Status != "published" {
		return outreachPanelView{}, false, outreachRefusal(http.StatusConflict, "", "outreach can only start for a published event")
	}
	if existing, err := h.store.GetByWorkshop(ctx, id); err == nil {
		v, perr := h.panel(ctx, wk, &existing)
		return v, false, perr
	} else if !errors.Is(err, outreach.ErrNotFound) {
		return outreachPanelView{}, false, err
	}

	// No StartsAt/EndsAt (#0651): a date window on the ShortLinks campaign
	// hides every click made while promoting the event before it starts.
	req := shortlinks.CreateCampaignRequest{Description: wk.Title, DefaultUTMCampaign: wk.Slug}
	camp, err := h.links.EnsureCampaign(ctx, wk.Slug, req)
	if err != nil {
		return outreachPanelView{}, false, outreachLinksError{err}
	}
	o, created, err := h.store.Create(ctx, id, camp.Slug)
	if err != nil {
		return outreachPanelView{}, false, err
	}
	var report *outreachLinkReport
	if created {
		h.recordCtx(ctx, ip, audit.TargetOutreach, o.ID, audit.Entry{
			ActorID:  actorID,
			Action:   audit.ActionOutreachStarted,
			Metadata: map[string]any{"workshop_id": o.WorkshopID, "campaign_slug": o.CampaignSlug},
		})
		rep, lerr := h.ensureLinks(ctx, ip, actorID, o, wk)
		if lerr != nil {
			return outreachPanelView{}, false, lerr
		}
		report = &rep
	}
	v, err := h.panel(ctx, wk, &o)
	if err != nil {
		return outreachPanelView{}, false, err
	}
	v.LinkReport = report
	return v, created, nil
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
	actorID := actor.ID
	v, err := h.createPost(r.Context(), clientIP(r), &actorID, id, req)
	if err != nil {
		writeOutreachError(w, "create post", err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// createPost is CreatePost without the transport (#0699).
func (h *AdminOutreachHandler) createPost(ctx context.Context, ip string, actorID *int64, id int64, req createOutreachPostRequest) (outreachPostView, error) {
	ch, chOK := outreach.ChannelByName(req.Channel)
	wv, wvOK := outreach.WaveByName(req.Wave)
	if !chOK || !wvOK {
		field := "channel"
		if chOK {
			field = "wave"
		}
		return outreachPostView{}, outreachRefusal(http.StatusBadRequest, field, "unknown channel or wave")
	}
	if !h.channelEnabled(ch.Name) {
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", ch.Label+" is switched off in Settings")
	}
	if len(req.Body) > maxOutreachBodyBytes {
		return outreachPostView{}, outreachRefusal(http.StatusBadRequest, "body", "body is too long")
	}
	o, err := h.store.GetByID(ctx, id)
	if errors.Is(err, outreach.ErrNotFound) {
		return outreachPostView{}, outreachRefusal(http.StatusNotFound, "", "outreach not found")
	} else if err != nil {
		return outreachPostView{}, err
	}
	wk, err := h.workshopFor(ctx, o.WorkshopID)
	if err != nil {
		return outreachPostView{}, err
	}
	f := h.facts(wk)
	body := req.Body
	if strings.TrimSpace(body) == "" {
		body = outreach.Draft(ch, wv, f)
	}
	p, err := h.store.CreatePost(ctx, o.ID, ch.Name, wv.Name, body, outreach.Fingerprint(f))
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrPostExists):
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", "a post already exists for that channel and wave")
	default:
		return outreachPostView{}, err
	}
	// A slot whose link was created up front (#0568) opens already prepared.
	linkApplied := false
	switch pl, lerr := h.store.GetPostLink(ctx, o.ID, p.Channel, p.Wave); {
	case lerr == nil:
		status := p.Status
		if !ch.Automatic {
			status = "manual"
		}
		prepared, _, perr := h.store.SetPrepared(ctx, p.ID, pl.Key, pl.ShortURL, status)
		if perr != nil {
			return outreachPostView{}, perr
		}
		p = prepared
		linkApplied = true
	case errors.Is(lerr, outreach.ErrNotFound):
	default:
		return outreachPostView{}, lerr
	}
	meta := map[string]any{"outreach_id": o.ID, "channel": p.Channel, "wave": p.Wave}
	if linkApplied {
		meta["link_applied"] = true
	}
	h.recordCtx(ctx, ip, audit.TargetOutreachPost, p.ID, audit.Entry{
		ActorID: actorID, Action: audit.ActionOutreachPostCreated, Metadata: meta,
	})
	return h.postView(p, f, wk.Status), nil
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
	actorID := actor.ID
	v, err := h.updatePost(r.Context(), clientIP(r), &actorID, id, req)
	if err != nil {
		writeOutreachError(w, "update post", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// updatePost is PatchPost without the transport (#0699).
func (h *AdminOutreachHandler) updatePost(ctx context.Context, ip string, actorID *int64, id int64, req patchOutreachPostRequest) (outreachPostView, error) {
	regen := req.Regenerate != nil && *req.Regenerate
	if (req.Body != nil) == regen {
		return outreachPostView{}, outreachRefusal(http.StatusBadRequest, "body", "send exactly one of body or regenerate")
	}
	if regen && req.Customized != nil {
		return outreachPostView{}, outreachRefusal(http.StatusBadRequest, "customized", "customized goes with body, not regenerate")
	}
	p, err := h.store.GetPost(ctx, id)
	if err != nil {
		return outreachPostView{}, postErrorRefusal(err)
	}
	o, err := h.store.GetByID(ctx, p.OutreachID)
	if err != nil {
		return outreachPostView{}, err
	}
	wk, err := h.workshopFor(ctx, o.WorkshopID)
	if err != nil {
		return outreachPostView{}, err
	}
	f := h.facts(wk)
	var updated outreach.Post
	if regen {
		body := regeneratedBody(p, f)
		fp := outreach.Fingerprint(f)
		updated, err = h.store.UpdatePostBody(ctx, id, body, &fp, nil)
	} else {
		if strings.TrimSpace(*req.Body) == "" {
			return outreachPostView{}, outreachRefusal(http.StatusBadRequest, "body", "body cannot be empty")
		}
		if len(*req.Body) > maxOutreachBodyBytes {
			return outreachPostView{}, outreachRefusal(http.StatusBadRequest, "body", "body is too long")
		}
		updated, err = h.store.UpdatePostBody(ctx, id, *req.Body, nil, req.Customized)
	}
	if err != nil {
		return outreachPostView{}, postErrorRefusal(err)
	}
	h.recordCtx(ctx, ip, audit.TargetOutreachPost, updated.ID, audit.Entry{
		ActorID:  actorID,
		Action:   audit.ActionOutreachPostUpdated,
		Metadata: map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave, "regenerated": regen, "customized": updated.Customized},
	})
	return h.postView(updated, f, wk.Status), nil
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
	actorID := actor.ID
	v, err := h.prepare(r.Context(), clientIP(r), &actorID, id)
	if err != nil {
		writeOutreachError(w, "create link", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// prepare is Prepare without the transport (#0700); prepare_outreach_post
// calls it too.
func (h *AdminOutreachHandler) prepare(ctx context.Context, ip string, actorID *int64, id int64) (outreachPostView, error) {
	if !h.linksEnabled() {
		return outreachPostView{}, outreachRefusal(http.StatusServiceUnavailable, "", "ShortLinks is not configured")
	}
	p, err := h.store.GetPost(ctx, id)
	if err != nil {
		return outreachPostView{}, postErrorRefusal(err)
	}
	o, err := h.store.GetByID(ctx, p.OutreachID)
	if err != nil {
		return outreachPostView{}, err
	}
	wk, err := h.workshopFor(ctx, o.WorkshopID)
	if err != nil {
		return outreachPostView{}, err
	}
	if !h.postVisible(p) {
		ch, _ := outreach.ChannelByName(p.Channel)
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", ch.Label+" is switched off in Settings")
	}
	f := h.facts(wk)
	if p.Prepared() {
		return h.postView(p, f, wk.Status), nil
	}
	if wk.Status != "published" {
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", "outreach is only available for a published event")
	}
	ch, _ := outreach.ChannelByName(p.Channel)
	wv, _ := outreach.WaveByName(p.Wave)
	var linkKey, linkURL string
	switch pl, lerr := h.store.GetPostLink(ctx, o.ID, p.Channel, p.Wave); {
	case lerr == nil:
		// Created up front (#0568): no ShortLinks call at all.
		linkKey, linkURL = pl.Key, pl.ShortURL
	case errors.Is(lerr, outreach.ErrNotFound):
		base, berr := h.shortLinksBase(ctx)
		if berr != nil {
			return outreachPostView{}, outreachLinksError{berr}
		}
		link, cerr := h.links.CreateLink(ctx, h.postLinkRequest(o, wk, ch, wv))
		if cerr != nil {
			return outreachPostView{}, outreachLinksError{cerr}
		}
		// A racing Create links may have won the slot: use the stored row.
		row, _, ierr := h.store.InsertPostLink(ctx, o.ID, ch.Name, wv.Name, link.Key, shortlinks.ShortURL(base, link.Key))
		if ierr != nil {
			return outreachPostView{}, ierr
		}
		linkKey, linkURL = row.Key, row.ShortURL
	default:
		return outreachPostView{}, lerr
	}
	status := p.Status
	if !ch.Automatic {
		status = "manual"
	}
	updated, changed, err := h.store.SetPrepared(ctx, id, linkKey, linkURL, status)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrPostLocked):
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", "post is already posted")
	default:
		return outreachPostView{}, err
	}
	// A racing Prepare that lost gets the winner's row back with changed
	// false, so only the call whose UPDATE actually set the key audits. (The
	// stored key cannot decide this: ShortLinks de-duplicates the link, so
	// racing callers hold the same key.)
	if changed {
		h.recordCtx(ctx, ip, audit.TargetOutreachPost, updated.ID, audit.Entry{
			ActorID:  actorID,
			Action:   audit.ActionOutreachPostPrepared,
			Metadata: map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave, "short_link_key": linkKey},
		})
	}
	return h.postView(updated, f, wk.Status), nil
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
	actorID := actor.ID
	v, err := h.markPosted(r.Context(), clientIP(r), &actorID, id, req.RemoteURL)
	if err != nil {
		writeOutreachError(w, "mark posted", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// markPosted is MarkPosted without the transport (#0701).
func (h *AdminOutreachHandler) markPosted(ctx context.Context, ip string, actorID *int64, id int64, remoteURL *string) (outreachPostView, error) {
	if remoteURL != nil && strings.TrimSpace(*remoteURL) == "" {
		remoteURL = nil
	}
	if remoteURL != nil && !validRemoteURL(*remoteURL) {
		return outreachPostView{}, outreachRefusal(http.StatusBadRequest, "remote_url", "remote_url must be an absolute http(s) URL")
	}
	_, wk, err := h.postContextCtx(ctx, id)
	if err != nil {
		return outreachPostView{}, err
	}
	updated, err := h.store.MarkPosted(ctx, id, remoteURL, time.Now())
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrNotPrepared):
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", "prepare the post before marking it posted")
	case errors.Is(err, outreach.ErrPostLocked):
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", "post is already posted")
	case errors.Is(err, outreach.ErrPostInFlight):
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", "post is being sent")
	default:
		return outreachPostView{}, err
	}
	h.recordCtx(ctx, ip, audit.TargetOutreachPost, updated.ID, audit.Entry{
		ActorID:  actorID,
		Action:   audit.ActionOutreachPostPosted,
		Metadata: map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave, "has_remote_url": updated.RemoteURL != nil},
	})
	return h.postView(updated, h.facts(wk), wk.Status), nil
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
	actorID := actor.ID
	v, err := h.unmarkPosted(r.Context(), clientIP(r), &actorID, id)
	if err != nil {
		writeOutreachError(w, "unmark posted", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// unmarkPosted is UnmarkPosted without the transport (#0701).
func (h *AdminOutreachHandler) unmarkPosted(ctx context.Context, ip string, actorID *int64, id int64) (outreachPostView, error) {
	p, wk, err := h.postContextCtx(ctx, id)
	if err != nil {
		return outreachPostView{}, err
	}
	if h.channelConfigured(p.Channel) || p.RemoteID != nil {
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", "this post was sent by the site and cannot be unmarked")
	}
	updated, err := h.store.MarkUnposted(ctx, id)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrWrongStatus):
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", "post is not marked as posted")
	case errors.Is(err, outreach.ErrNotFound):
		return outreachPostView{}, outreachRefusal(http.StatusNotFound, "", "post not found")
	default:
		return outreachPostView{}, err
	}
	h.recordCtx(ctx, ip, audit.TargetOutreachPost, updated.ID, audit.Entry{
		ActorID:  actorID,
		Action:   audit.ActionOutreachPostUnmarked,
		Metadata: map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave},
	})
	return h.postView(updated, h.facts(wk), wk.Status), nil
}

// takedownBudget bounds the remote delete in Takedown.
const takedownBudget = 30 * time.Second

// Takedown handles POST /admin/outreach-posts/{id}/takedown (#0604): delete a
// posted automatic post from its network, then return the row to draft so it
// can be posted again with its link refreshed. A post already gone from the
// network (deleted by hand) counts as deleted. If the network refuses, the
// row stays posted and the response says why.
func (h *AdminOutreachHandler) Takedown(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	actorID := actor.ID
	v, err := h.takedown(r.Context(), clientIP(r), &actorID, id)
	if err != nil {
		writeOutreachError(w, "takedown", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// takedown is Takedown without the transport (#0701). A network refusal is
// a 502 refusal; the row stays posted.
func (h *AdminOutreachHandler) takedown(ctx context.Context, ip string, actorID *int64, id int64) (outreachPostView, error) {
	p, wk, err := h.postContextCtx(ctx, id)
	if err != nil {
		return outreachPostView{}, err
	}
	ch, _ := outreach.ChannelByName(p.Channel)
	if !ch.Automatic {
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", "only a post the site sent itself can be deleted here; a hand-posted one is taken back with Unmark")
	}
	if !h.channelConfigured(p.Channel) {
		return outreachPostView{}, outreachRefusal(http.StatusConflict, "", ch.Label+" is not connected on this server, so the post cannot be deleted from here")
	}
	checked, err := h.store.CheckTakedown(ctx, id)
	if err != nil {
		return outreachPostView{}, takedownRefusal(err)
	}
	dctx, cancel := context.WithTimeout(ctx, takedownBudget)
	defer cancel()
	existed, err := h.posting.DeleteRemote(dctx, checked)
	if err != nil {
		slog.Warn("outreach: remote delete failed", "post_id", id, "channel", p.Channel, "err", err)
		return outreachPostView{}, &outreachOpError{
			status: http.StatusBadGateway,
			msg:    ch.Label + " would not delete the post, so it is still marked posted: " + err.Error(),
			v1msg:  ch.Label + " would not delete the post, so it is still marked posted (reason: " + takedownFailureReason(err) + ")",
		}
	}
	updated, err := h.store.CompleteTakedown(ctx, id, *checked.RemoteID)
	if err != nil {
		return outreachPostView{}, takedownRefusal(err)
	}
	remoteURL := ""
	if checked.RemoteURL != nil {
		remoteURL = *checked.RemoteURL
	}
	h.recordCtx(ctx, ip, audit.TargetOutreachPost, updated.ID, audit.Entry{
		ActorID:  actorID,
		Action:   audit.ActionOutreachPostTakenDown,
		Metadata: map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave, "remote_url": remoteURL, "already_gone": !existed, "attempt_generation": updated.AttemptGeneration},
	})
	return h.postView(updated, h.facts(wk), wk.Status), nil
}

// takedownRefusal maps a takedown store error to a refusal; an unrecognised
// error is returned as is.
func takedownRefusal(err error) error {
	switch {
	case errors.Is(err, outreach.ErrNotFound):
		return outreachRefusal(http.StatusNotFound, "", "post not found")
	case errors.Is(err, outreach.ErrTakedownNotPosted):
		return outreachRefusal(http.StatusConflict, "", "this post is not posted, or was not sent by the site")
	case errors.Is(err, outreach.ErrTakedownInFlight):
		return outreachRefusal(http.StatusConflict, "", "this post is still being sent; try again in a minute")
	case errors.Is(err, outreach.ErrTakedownChanged):
		return outreachRefusal(http.StatusConflict, "", "this post changed while it was being deleted; reload and check it")
	}
	return err
}

// takedownFailureReason names a remote delete failure without its text, for
// the v1 API (#0708), the way linkFailureReason does for ShortLinks.
func takedownFailureReason(err error) string {
	switch {
	case errors.Is(err, bluesky.ErrRateLimited), errors.Is(err, mastodon.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, bluesky.ErrAuth), errors.Is(err, mastodon.ErrAuth):
		return "rejected"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, bluesky.ErrServer), errors.Is(err, mastodon.ErrServer):
		return "network_error"
	default:
		return "failed"
	}
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
	actorID := actor.ID
	if err := h.deletePost(r.Context(), clientIP(r), &actorID, id); err != nil {
		writeOutreachError(w, "delete post", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deletePost is DeletePost without the transport (#0699).
func (h *AdminOutreachHandler) deletePost(ctx context.Context, ip string, actorID *int64, id int64) error {
	p, err := h.store.GetPost(ctx, id)
	if err != nil {
		return postErrorRefusal(err)
	}
	if err := h.store.DeletePost(ctx, id); err != nil {
		return postErrorRefusal(err)
	}
	h.recordCtx(ctx, ip, audit.TargetOutreachPost, p.ID, audit.Entry{
		ActorID:  actorID,
		Action:   audit.ActionOutreachPostDeleted,
		Metadata: map[string]any{"outreach_id": p.OutreachID, "channel": p.Channel, "wave": p.Wave},
	})
	return nil
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
	actorID := actor.ID
	v, err := h.resetOutreach(r.Context(), clientIP(r), &actorID, id)
	if err != nil {
		writeOutreachError(w, "reset", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// resetOutreach is Reset without the transport (#0699).
func (h *AdminOutreachHandler) resetOutreach(ctx context.Context, ip string, actorID *int64, id int64) (outreachPanelView, error) {
	wk, err := h.workshopFor(ctx, id)
	if err != nil {
		return outreachPanelView{}, err
	}
	o, err := h.store.GetByWorkshop(ctx, id)
	if errors.Is(err, outreach.ErrNotFound) {
		return outreachPanelView{}, outreachRefusal(http.StatusNotFound, "", "outreach has not started for this event")
	} else if err != nil {
		return outreachPanelView{}, err
	}
	res, err := h.store.Reset(ctx, o.ID)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrResetPosted):
		return outreachPanelView{}, outreachRefusal(http.StatusConflict, "", "Something has already been posted, so outreach cannot be reset.")
	case errors.Is(err, outreach.ErrResetInFlight):
		return outreachPanelView{}, outreachRefusal(http.StatusConflict, "", "A post is queued or being sent, so outreach cannot be reset.")
	case errors.Is(err, outreach.ErrResetFlyerPutUp):
		return outreachPanelView{}, outreachRefusal(http.StatusConflict, "", "A flyer is marked put up, so outreach cannot be reset.")
	default:
		return outreachPanelView{}, err
	}
	h.recordCtx(ctx, ip, audit.TargetOutreach, o.ID, audit.Entry{
		ActorID:  actorID,
		Action:   audit.ActionOutreachReset,
		Metadata: map[string]any{"workshop_id": o.WorkshopID, "posts_removed": res.Posts, "flyers_removed": res.Flyers},
	})
	return h.panel(ctx, wk, &o)
}
