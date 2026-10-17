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
	UpdatePostBody(ctx context.Context, id int64, body string, fingerprint *string) (outreach.Post, error)
	SetPrepared(ctx context.Context, id int64, key, shortURL, status string) (outreach.Post, error)
	MarkPosted(ctx context.Context, id int64, remoteURL *string, now time.Time) (outreach.Post, error)
	DeletePost(ctx context.Context, id int64) error
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
}

// AdminOutreachHandler serves the admin-only Outreach routes (#0544): start
// outreach for a workshop, draft per-channel posts, Prepare them (create the
// tracked short link), and mark them posted. All routes MUST be mounted
// behind RequireSession then RequireAdmin; the handler only reads the user
// to attribute audit rows. All ShortLinks calls are server-side.
type AdminOutreachHandler struct {
	store     outreachStore
	ws        outreachWorkshopReader
	links     shortLinker
	auditor   *audit.Logger
	baseURL   string
	mu        sync.Mutex
	shortBase string // cached Me().BaseURL; a failed fetch is not cached
}

// NewAdminOutreachHandler constructs the handler. baseURL is the site's own
// BaseURL, used for the tracked destination URLs; a nil auditor disables
// audit writes; a nil or unconfigured links makes Outreach report
// unavailable.
func NewAdminOutreachHandler(store outreachStore, ws outreachWorkshopReader, links shortLinker, auditor *audit.Logger, baseURL string) *AdminOutreachHandler {
	return &AdminOutreachHandler{store: store, ws: ws, links: links, auditor: auditor, baseURL: baseURL}
}

func (h *AdminOutreachHandler) linksEnabled() bool {
	return h.links != nil && h.links.Enabled()
}

// shortLinksBase returns the display host for short URLs, fetched once.
func (h *AdminOutreachHandler) shortLinksBase(ctx context.Context) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.shortBase != "" {
		return h.shortBase, nil
	}
	me, err := h.links.Me(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(me.BaseURL) == "" {
		return "", errors.New("shortlinks: /api/me returned no base_url")
	}
	h.shortBase = strings.TrimRight(me.BaseURL, "/")
	return h.shortBase, nil
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

func outreachFacts(wk workshops.Workshop) outreach.Facts {
	return outreach.Facts{Slug: wk.Slug, Title: wk.Title, StartsAt: wk.StartsAt, LocationName: wk.LocationName, LocationAddress: wk.LocationAddress}
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
	ID             int64           `json:"id"`
	OutreachID     int64           `json:"outreach_id"`
	Channel        string          `json:"channel"`
	Wave           string          `json:"wave"`
	Body           string          `json:"body"`
	Status         string          `json:"status"`
	Prepared       bool            `json:"prepared"`
	Stale          bool            `json:"stale"`
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

func (h *AdminOutreachHandler) postView(p outreach.Post, f outreach.Facts) outreachPostView {
	ch, _ := outreach.ChannelByName(p.Channel)
	wv, _ := outreach.WaveByName(p.Wave)
	return outreachPostView{
		ID: p.ID, OutreachID: p.OutreachID, Channel: p.Channel, Wave: p.Wave, Body: p.Body,
		Status: p.Status, Prepared: p.Prepared(),
		Stale:        p.Status != "posted" && p.SourceFingerprint != outreach.Fingerprint(f),
		ShortLinkKey: p.ShortLinkKey, ShortURL: p.ShortURL,
		DestinationURL: outreach.DestinationURL(h.baseURL, f, ch, wv),
		UTM:            outreachUTMView{Source: ch.UTMSource, Medium: ch.UTMMedium, Campaign: f.Slug, Content: wv.Name},
		ScheduledAt:    rfc3339(p.ScheduledAt), PostedAt: rfc3339(p.PostedAt), RemoteURL: p.RemoteURL, LastError: p.LastError,
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: rfc3339(p.UpdatedAt),
	}
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
}

type outreachPanelView struct {
	Available         bool                 `json:"available"`
	UnavailableReason string               `json:"unavailable_reason"`
	Workshop          outreachWorkshopView `json:"workshop"`
	Outreach          *outreachRowView     `json:"outreach"`
	Channels          []outreach.Channel   `json:"channels"`
	Waves             []outreachWaveView   `json:"waves"`
	Posts             []outreachPostView   `json:"posts"`
}

func (h *AdminOutreachHandler) panel(ctx context.Context, wk workshops.Workshop, o *outreach.Outreach) (outreachPanelView, error) {
	v := outreachPanelView{
		Workshop: outreachWorkshopView{ID: wk.ID, Slug: wk.Slug, Title: wk.Title, Status: wk.Status, StartsAt: rfc3339(wk.StartsAt)},
		Channels: outreach.Channels(),
		Waves:    []outreachWaveView{},
		Posts:    []outreachPostView{},
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
		if w.DaysBefore > 0 && wk.StartsAt != nil {
			t := wk.StartsAt.AddDate(0, 0, -w.DaysBefore)
			wv.SuggestedAt = rfc3339(&t)
		}
		v.Waves = append(v.Waves, wv)
	}
	if o != nil {
		v.Outreach = &outreachRowView{ID: o.ID, WorkshopID: o.WorkshopID, CampaignSlug: o.CampaignSlug, CreatedAt: o.CreatedAt.UTC().Format(time.RFC3339)}
		posts, err := h.store.ListPosts(ctx, o.ID)
		if err != nil {
			return v, err
		}
		f := outreachFacts(wk)
		for _, p := range posts {
			v.Posts = append(v.Posts, h.postView(p, f))
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
	v, err := h.panel(r.Context(), wk, &o)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
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
	f := outreachFacts(wk)
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
	if h.auditor != nil {
		actorID := actor.ID
		targetID := p.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachPostCreated,
			TargetType: audit.TargetOutreachPost,
			TargetID:   &targetID,
			Metadata:   map[string]any{"outreach_id": o.ID, "channel": p.Channel, "wave": p.Wave},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusCreated, h.postView(p, f))
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
	p, wk, ok := h.postContext(w, r, id)
	if !ok {
		return
	}
	f := outreachFacts(wk)
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
		updated, err = h.store.UpdatePostBody(r.Context(), id, body, &fp)
	} else {
		if strings.TrimSpace(*req.Body) == "" {
			writeError(w, http.StatusBadRequest, "body cannot be empty")
			return
		}
		if len(*req.Body) > maxOutreachBodyBytes {
			writeError(w, http.StatusBadRequest, "body is too long")
			return
		}
		updated, err = h.store.UpdatePostBody(r.Context(), id, *req.Body, nil)
	}
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrPostLocked):
		writeError(w, http.StatusConflict, "post is already posted")
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
			Metadata:   map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave, "regenerated": regen},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, h.postView(updated, f))
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
	f := outreachFacts(wk)
	if p.Prepared() {
		writeJSON(w, http.StatusOK, h.postView(p, f))
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
	base, err := h.shortLinksBase(r.Context())
	if err != nil {
		writeShortLinksError(w, "me", err)
		return
	}
	link, err := h.links.CreateLink(r.Context(), shortlinks.CreateLinkRequest{
		DestinationURL: outreach.DestinationURL(h.baseURL, f, ch, wv),
		Title:          wk.Title + " — " + ch.Label + " " + wv.Label,
		CampaignSlug:   o.CampaignSlug,
		UTMSource:      ch.UTMSource,
		UTMMedium:      ch.UTMMedium,
		UTMCampaign:    f.Slug,
		UTMContent:     wv.Name,
		Placement:      ch.Name,
	})
	if err != nil {
		writeShortLinksError(w, "create link", err)
		return
	}
	status := p.Status
	if !ch.Automatic {
		status = "manual"
	}
	updated, err := h.store.SetPrepared(r.Context(), id, link.Key, base+"/"+link.Key, status)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrPostLocked):
		writeError(w, http.StatusConflict, "post is already posted")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	// A racing Prepare that lost returns the winner's row unchanged; only the
	// call that actually set the key audits.
	if h.auditor != nil && updated.ShortLinkKey != nil && *updated.ShortLinkKey == link.Key {
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachPostPrepared,
			TargetType: audit.TargetOutreachPost,
			TargetID:   &targetID,
			Metadata:   map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave, "short_link_key": link.Key},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, h.postView(updated, f))
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
	writeJSON(w, http.StatusOK, h.postView(updated, outreachFacts(wk)))
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
