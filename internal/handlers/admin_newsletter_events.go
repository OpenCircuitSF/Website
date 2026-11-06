package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// The seams AdminNewsletterEventsHandler reads through (#0643).
type newsletterCampaignGetter interface {
	GetByID(ctx context.Context, id int64) (mailing.Campaign, error)
}

type newsletterWorkshopLister interface {
	ListVisible(ctx context.Context, now time.Time) (upcoming, past []workshops.Workshop, err error)
}

type newsletterOutreachGetter interface {
	GetByWorkshop(ctx context.Context, workshopID int64) (outreach.Outreach, error)
}

type newsletterLinkStore interface {
	GetNewsletterLink(ctx context.Context, campaignID, workshopID int64) (outreach.NewsletterLink, error)
	InsertNewsletterLink(ctx context.Context, campaignID, workshopID int64, campaignSlug, key, shortURL string) (outreach.NewsletterLink, bool, error)
}

type newsletterAuditor interface {
	Record(ctx context.Context, e audit.Entry)
}

// newsletterLinksTimeout bounds one request's ShortLinks calls.
var newsletterLinksTimeout = 45 * time.Second

// AdminNewsletterEventsHandler builds the "upcoming events" block the campaign
// editor appends to a newsletter's body (#0643). Each event links through a
// ShortLink in that event's Outreach ShortLinks campaign, made once per
// (newsletter, event) and reused after; an event without one falls back to
// the plain event URL with the same UTM values. It MUST be mounted behind
// RequireSession then RequireAdmin.
type AdminNewsletterEventsHandler struct {
	campaigns newsletterCampaignGetter
	workshops newsletterWorkshopLister
	outreach  newsletterOutreachGetter
	links     newsletterLinkStore
	sl        shortLinker
	auditor   newsletterAuditor
	baseURL   string
	now       func() time.Time
}

// NewAdminNewsletterEventsHandler wires the handler. auditor may be nil.
func NewAdminNewsletterEventsHandler(campaigns newsletterCampaignGetter, ws newsletterWorkshopLister, o newsletterOutreachGetter, links newsletterLinkStore, sl shortLinker, auditor *audit.Logger, baseURL string) *AdminNewsletterEventsHandler {
	h := &AdminNewsletterEventsHandler{campaigns: campaigns, workshops: ws, outreach: o, links: links, sl: sl, baseURL: baseURL, now: time.Now}
	if auditor != nil {
		h.auditor = auditor
	}
	return h
}

// SetUpcomingEvents attaches the upcoming-events handler to the campaigns
// handler, so the route table mounts it with the other /admin/campaigns/{id}
// routes without widening the route table's parameter list.
func (h *AdminCampaignsHandler) SetUpcomingEvents(u *AdminNewsletterEventsHandler) {
	h.upcomingEvents = u
}

// UpcomingEventsHandler returns the handler SetUpcomingEvents attached, or nil.
func (h *AdminCampaignsHandler) UpcomingEventsHandler() *AdminNewsletterEventsHandler {
	return h.upcomingEvents
}

type upcomingEventView struct {
	WorkshopID int64  `json:"workshop_id"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	Link       string `json:"link"`
	Reused     bool   `json:"reused"`
	Reason     string `json:"reason"`
}

type upcomingEventsResponse struct {
	Month    string              `json:"month"`
	Label    string              `json:"label"`
	Markdown string              `json:"markdown"`
	Events   []upcomingEventView `json:"events"`
	Created  int                 `json:"created"`
	Reused   int                 `json:"reused"`
	Fallback int                 `json:"fallback"`
}

// UpcomingEvents serves POST /admin/campaigns/{id}/upcoming-events with body
// {"month":"YYYY-MM"}. It returns the month's published, dated, not yet
// started events as Markdown; it never edits the campaign itself.
func (h *AdminNewsletterEventsHandler) UpcomingEvents(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Month string `json:"month"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	camp, err := h.campaigns.GetByID(r.Context(), id)
	if errors.Is(err, mailing.ErrCampaignNotFound) {
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	}
	if err != nil {
		slog.Error("newsletter events: get campaign", "campaign_id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if camp.Status != mailing.CampaignStatusDraft && camp.Status != mailing.CampaignStatusScheduled {
		writeError(w, http.StatusConflict, "campaign is not editable")
		return
	}
	now := h.now()
	from, to, label, err := outreach.MonthWindow(strings.TrimSpace(body.Month), now)
	if err != nil {
		writeError(w, http.StatusBadRequest, "month must be YYYY-MM")
		return
	}
	upcoming, _, err := h.workshops.ListVisible(r.Context(), now)
	if err != nil {
		slog.Error("newsletter events: list workshops", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), newsletterLinksTimeout)
	defer cancel()

	resp := upcomingEventsResponse{Month: strings.TrimSpace(body.Month), Label: label, Events: []upcomingEventView{}}
	items := []outreach.NewsletterItem{}
	base := ""
	stopReason := "" // once set, every remaining event falls back with it
	for _, wk := range upcoming {
		if wk.Status != workshops.StatusPublished || wk.StartsAt == nil || wk.StartsAt.Before(from) || !wk.StartsAt.Before(to) {
			continue
		}
		plain := outreach.NewsletterDestinationURL(h.baseURL, wk.Slug, camp.Slug)
		ev := upcomingEventView{WorkshopID: wk.ID, Title: wk.Title, URL: plain, Link: "plain"}

		stored, err := h.links.GetNewsletterLink(ctx, camp.ID, wk.ID)
		switch {
		case err == nil:
			ev.URL, ev.Link, ev.Reused = stored.ShortURL, "short", true
		case !errors.Is(err, outreach.ErrNotFound):
			slog.Error("newsletter events: get link", "campaign_id", camp.ID, "workshop_id", wk.ID, "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		default:
			ev.Reason = stopReason
			if ev.Reason == "" {
				ev.Reason, err = h.makeLink(ctx, r, actor.ID, camp, wk, &ev, &base)
				if err != nil {
					slog.Error("newsletter events: store link", "campaign_id", camp.ID, "workshop_id", wk.ID, "err", err)
					writeError(w, http.StatusInternalServerError, "internal error")
					return
				}
				// A rate limit, an expired deadline, or a failed host lookup
				// (base still unknown) would fail every later event too.
				meFailed := base == "" && ev.Reason != "" && ev.Reason != "no_campaign" && ev.Reason != "not_configured"
				if ev.Reason == "rate_limited" || ev.Reason == "timeout" || ctx.Err() != nil || meFailed {
					stopReason = ev.Reason
				}
			}
		}
		switch {
		case ev.Reused:
			resp.Reused++
		case ev.Link == "short":
			resp.Created++
		default:
			resp.Fallback++
		}
		resp.Events = append(resp.Events, ev)
		venue := ""
		if wk.LocationName != nil {
			venue = *wk.LocationName
		}
		items = append(items, outreach.NewsletterItem{Title: wk.Title, URL: ev.URL, Venue: venue, StartsAt: *wk.StartsAt})
	}
	resp.Markdown = outreach.NewsletterEventsMarkdown(label, items)
	writeJSON(w, http.StatusOK, resp)
}

// makeLink creates and stores the event's newsletter ShortLink, filling ev on
// success. It returns a fallback reason (ev left plain) for anything that
// keeps the link from being made, and an error only for a local store
// failure. base caches the ShortLinks host for the request.
func (h *AdminNewsletterEventsHandler) makeLink(ctx context.Context, r *http.Request, actorID int64, camp mailing.Campaign, wk workshops.Workshop, ev *upcomingEventView, base *string) (string, error) {
	o, err := h.outreach.GetByWorkshop(ctx, wk.ID)
	if errors.Is(err, outreach.ErrNotFound) || (err == nil && strings.TrimSpace(o.CampaignSlug) == "") {
		return "no_campaign", nil
	}
	if err != nil {
		return "", err
	}
	if h.sl == nil || !h.sl.Enabled() {
		return "not_configured", nil
	}
	if *base == "" {
		me, err := h.sl.Me(ctx)
		if err != nil {
			slog.Warn("newsletter events: shortlinks me", "err", err)
			return linkFailureReason(err), nil
		}
		if strings.TrimSpace(me.BaseURL) == "" {
			return "failed", nil
		}
		*base = strings.TrimRight(me.BaseURL, "/")
	}
	// No custom Key, as in postLinkRequest: ShortLinks de-duplicates on the
	// destination, which heals a crash between the create and the insert.
	link, err := h.sl.CreateLink(ctx, shortlinks.CreateLinkRequest{
		DestinationURL: outreach.NewsletterDestinationURL(h.baseURL, wk.Slug, camp.Slug),
		Title:          wk.Title + " — Newsletter",
		CampaignSlug:   o.CampaignSlug,
		UTMSource:      outreach.NewsletterUTMSource,
		UTMMedium:      outreach.NewsletterUTMMedium,
		UTMCampaign:    wk.Slug,
		UTMContent:     camp.Slug,
		Placement:      outreach.NewsletterPlacement,
	})
	if err != nil {
		slog.Warn("newsletter events: create link", "workshop_id", wk.ID, "err", err)
		return linkFailureReason(err), nil
	}
	row, created, err := h.links.InsertNewsletterLink(ctx, camp.ID, wk.ID, o.CampaignSlug, link.Key, shortlinks.ShortURL(*base, link.Key))
	if err != nil {
		return "", err
	}
	ev.URL, ev.Link = row.ShortURL, "short"
	if !created {
		ev.Reused = true
		return "", nil
	}
	if h.auditor != nil {
		rid := row.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID: &actorID, Action: audit.ActionNewsletterLinkCreated, TargetType: audit.TargetNewsletterLink, TargetID: &rid,
			Metadata: map[string]any{"campaign_id": camp.ID, "workshop_id": wk.ID, "campaign_slug": o.CampaignSlug, "short_link_key": row.Key},
			IP:       clientIP(r),
		})
	}
	return "", nil
}
