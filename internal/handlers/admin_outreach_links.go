package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// outreachLinksTimeout bounds one link-creation pass. Apache's proxy timeout
// is its 60 s default, so the handler stops first and reports what it did; a
// variable so a test can shorten it.
var outreachLinksTimeout = 45 * time.Second

type outreachLinkFailure struct {
	Kind       string `json:"kind"` // "post" | "flyer"
	Channel    string `json:"channel,omitempty"`
	Wave       string `json:"wave,omitempty"`
	LocationID int64  `json:"location_id,omitempty"`
	Label      string `json:"label"` // "Bluesky · Reminder", "Flyer · Mission Library"
	Reason     string `json:"reason"`
}

type outreachLinkReport struct {
	Created  int                   `json:"created"`
	Existing int                   `json:"existing"` // includes adopted post links
	Failed   []outreachLinkFailure `json:"failed"`   // never null
	Stopped  bool                  `json:"stopped"`
}

// postLinkRequest is the ShortLinks request for one channel x wave slot. No
// Key is set: a custom key turns ShortLinks' de-duplication off, and that
// de-duplication is what heals a crash between the upstream call and the
// local insert.
func (h *AdminOutreachHandler) postLinkRequest(o outreach.Outreach, wk workshops.Workshop, ch outreach.Channel, wv outreach.Wave) shortlinks.CreateLinkRequest {
	f := h.facts(wk)
	return shortlinks.CreateLinkRequest{
		DestinationURL: outreach.DestinationURL(h.baseURL, f, ch, wv),
		Title:          wk.Title + " — " + ch.Label + " " + wv.Label,
		CampaignSlug:   o.CampaignSlug,
		UTMSource:      ch.UTMSource,
		UTMMedium:      ch.UTMMedium,
		UTMCampaign:    f.Slug,
		UTMContent:     wv.Name,
		Placement:      ch.Name,
	}
}

// linkFailureReason is a fixed string for one failed link; upstream text is
// logged by the caller and never returned.
func linkFailureReason(err error) string {
	switch {
	case errors.Is(err, shortlinks.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, shortlinks.ErrUnavailable):
		return "not_configured"
	case errors.Is(err, shortlinks.ErrUnauthenticated), errors.Is(err, shortlinks.ErrForbidden):
		return "rejected"
	case errors.Is(err, outreach.ErrLocationCodeChanged):
		return "code_changed"
	default:
		return "failed"
	}
}

type linkTask struct {
	kind     string
	channel  outreach.Channel
	wave     outreach.Wave
	location outreach.Location
}

func (t linkTask) failure(reason string) outreachLinkFailure {
	if t.kind == "flyer" {
		return outreachLinkFailure{Kind: "flyer", LocationID: t.location.ID, Label: "Flyer · " + t.location.Name, Reason: reason}
	}
	return outreachLinkFailure{Kind: "post", Channel: t.channel.Name, Wave: t.wave.Name, Label: t.channel.Label + " · " + t.wave.Label, Reason: reason}
}

// sortedLocations orders by lower(name), then id.
func sortedLocations(locs []outreach.Location) []outreach.Location {
	out := append([]outreach.Location(nil), locs...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ensureLinks creates every missing tracked link for an outreach: one per
// channel x wave and one per active flyer location. It is the single helper
// behind Start, Create links and Prepare's table, so a link made by any path
// is the same row. The error return is for store failures only; ShortLinks
// failures are reported per link and never abort the pass, except a rate
// limit or the deadline, which stop it with the rest reported as not tried.
func (h *AdminOutreachHandler) ensureLinks(r *http.Request, actorID int64, o outreach.Outreach, wk workshops.Workshop) (outreachLinkReport, error) {
	report := outreachLinkReport{Failed: []outreachLinkFailure{}}
	ctx, cancel := context.WithTimeout(r.Context(), outreachLinksTimeout)
	defer cancel()

	postLinks, err := h.store.ListPostLinks(ctx, o.ID)
	if err != nil {
		return report, err
	}
	posts, err := h.store.ListPosts(ctx, o.ID)
	if err != nil {
		return report, err
	}
	flyerLinks, err := h.flyers.ListFlyerLinks(ctx, o.ID)
	if err != nil {
		return report, err
	}
	locs, err := h.locations.ListLocations(ctx)
	if err != nil {
		return report, err
	}
	haveLink := map[[2]string]bool{}
	for _, l := range postLinks {
		haveLink[[2]string{l.Channel, l.Wave}] = true
	}
	postBySlot := map[[2]string]outreach.Post{}
	for _, p := range posts {
		postBySlot[[2]string{p.Channel, p.Wave}] = p
	}
	haveFlyer := map[int64]bool{}
	for _, l := range flyerLinks {
		haveFlyer[l.LocationID] = true
	}

	var tasks []linkTask
	for _, ch := range h.enabledChannels() {
		for _, wv := range outreach.Waves() {
			slot := [2]string{ch.Name, wv.Name}
			if haveLink[slot] {
				report.Existing++
				continue
			}
			// A post prepared before slot links existed: adopt its link.
			if p, ok := postBySlot[slot]; ok && p.ShortLinkKey != nil && p.ShortURL != nil {
				if _, _, err := h.store.InsertPostLink(ctx, o.ID, ch.Name, wv.Name, *p.ShortLinkKey, *p.ShortURL); err != nil {
					return report, err
				}
				report.Existing++
				continue
			}
			tasks = append(tasks, linkTask{kind: "post", channel: ch, wave: wv})
		}
	}
	for _, l := range sortedLocations(locs) {
		if !l.Active {
			continue
		}
		if haveFlyer[l.ID] {
			report.Existing++
			continue
		}
		tasks = append(tasks, linkTask{kind: "flyer", location: l})
	}
	if len(tasks) == 0 {
		return report, nil
	}

	stop := func(from int, err error) {
		reason := linkFailureReason(err)
		if reason == "failed" && ctx.Err() != nil {
			reason = "timeout"
		}
		for _, t := range tasks[from:] {
			report.Failed = append(report.Failed, t.failure(reason))
		}
		report.Stopped = true
	}

	base, err := h.shortLinksBase(ctx)
	if err != nil {
		slog.Default().Error("admin_outreach: ShortLinks base lookup failed", "op", "ensure links", "err", err)
		stop(0, err)
		return report, nil
	}
	for i, t := range tasks {
		if ctx.Err() != nil {
			stop(i, ctx.Err())
			return report, nil
		}
		var cerr error
		switch t.kind {
		case "post":
			link, err := h.links.CreateLink(ctx, h.postLinkRequest(o, wk, t.channel, t.wave))
			if err != nil {
				cerr = err
				break
			}
			row, created, err := h.store.InsertPostLink(ctx, o.ID, t.channel.Name, t.wave.Name, link.Key, base+"/"+link.Key)
			if err != nil {
				return report, err
			}
			if !created {
				report.Existing++
				break
			}
			report.Created++
			if h.auditor != nil {
				id := row.ID
				h.auditor.Record(r.Context(), audit.Entry{
					ActorID: &actorID, Action: audit.ActionOutreachLinkCreated, TargetType: audit.TargetOutreachLink, TargetID: &id,
					Metadata: map[string]any{"outreach_id": o.ID, "channel": t.channel.Name, "wave": t.wave.Name, "short_link_key": row.Key},
					IP:       clientIP(r),
				})
			}
		case "flyer":
			sel := outreach.FlyerSelection{LocationID: t.location.ID, Name: t.location.Name, Code: t.location.Code}
			l, created, err := h.generateLink(ctx, o, wk, sel, base)
			if err != nil {
				cerr = err
				break
			}
			if !created {
				report.Existing++
				break
			}
			report.Created++
			h.auditFlyerLink(r, actorID, audit.ActionOutreachFlyerLinkCreated, l.ID, o.ID, t.location.ID, map[string]any{
				"code": t.location.Code, "short_link_key": l.Key,
			})
		}
		if cerr == nil {
			continue
		}
		slog.Default().Error("admin_outreach: creating a tracked link failed", "op", "ensure links", "kind", t.kind, "err", cerr)
		if errors.Is(cerr, shortlinks.ErrRateLimited) || ctx.Err() != nil {
			stop(i, cerr)
			return report, nil
		}
		report.Failed = append(report.Failed, t.failure(linkFailureReason(cerr)))
	}
	return report, nil
}

type outreachLinkRowView struct {
	Kind           string          `json:"kind"`
	Channel        string          `json:"channel"`
	ChannelLabel   string          `json:"channel_label"`
	Wave           string          `json:"wave"`
	WaveLabel      string          `json:"wave_label"`
	LocationID     *int64          `json:"location_id"`
	LocationName   *string         `json:"location_name"`
	LocationCode   *string         `json:"location_code"`
	ShortLinkKey   *string         `json:"short_link_key"`
	ShortURL       *string         `json:"short_url"`
	DestinationURL string          `json:"destination_url"`
	UTM            outreachUTMView `json:"utm"`
	Clicks         *int64          `json:"clicks"`
}

type outreachLinksView struct {
	Available         bool                  `json:"available"`
	UnavailableReason string                `json:"unavailable_reason"`
	Outreach          *outreachLinksRowRef  `json:"outreach"`
	ClicksStatus      string                `json:"clicks_status"`
	Missing           int                   `json:"missing"`
	Links             []outreachLinkRowView `json:"links"`
	Report            *outreachLinkReport   `json:"report"`
}

type outreachLinksRowRef struct {
	ID           int64  `json:"id"`
	CampaignSlug string `json:"campaign_slug"`
}

// linksView builds the links table for a workshop.
func (h *AdminOutreachHandler) linksView(ctx context.Context, wk workshops.Workshop, o *outreach.Outreach) (outreachLinksView, error) {
	v := outreachLinksView{Links: []outreachLinkRowView{}, ClicksStatus: "not_started"}
	switch {
	case !h.linksEnabled():
		v.UnavailableReason = "shortlinks_not_configured"
	case wk.Status != "published":
		v.UnavailableReason = "workshop_not_published"
	default:
		v.Available = true
	}
	if o == nil {
		return v, nil
	}
	v.Outreach = &outreachLinksRowRef{ID: o.ID, CampaignSlug: o.CampaignSlug}

	postLinks, err := h.store.ListPostLinks(ctx, o.ID)
	if err != nil {
		return v, err
	}
	posts, err := h.store.ListPosts(ctx, o.ID)
	if err != nil {
		return v, err
	}
	flyerLinks, err := h.flyers.ListFlyerLinks(ctx, o.ID)
	if err != nil {
		return v, err
	}
	locs, err := h.locations.ListLocations(ctx)
	if err != nil {
		return v, err
	}
	rowBySlot := map[[2]string]outreach.PostLink{}
	for _, l := range postLinks {
		rowBySlot[[2]string{l.Channel, l.Wave}] = l
	}
	postBySlot := map[[2]string]outreach.Post{}
	for _, p := range posts {
		postBySlot[[2]string{p.Channel, p.Wave}] = p
	}
	flyerByLoc := map[int64]outreach.FlyerLink{}
	for _, l := range flyerLinks {
		flyerByLoc[l.LocationID] = l
	}

	f := h.facts(wk)
	for _, ch := range h.enabledChannels() {
		for _, wv := range outreach.Waves() {
			slot := [2]string{ch.Name, wv.Name}
			row := outreachLinkRowView{
				Kind: "post", Channel: ch.Name, ChannelLabel: ch.Label, Wave: wv.Name, WaveLabel: wv.Label,
				DestinationURL: outreach.DestinationURL(h.baseURL, f, ch, wv),
				UTM:            outreachUTMView{Source: ch.UTMSource, Medium: ch.UTMMedium, Campaign: f.Slug, Content: wv.Name},
			}
			if l, ok := rowBySlot[slot]; ok {
				k, u := l.Key, l.ShortURL
				row.ShortLinkKey, row.ShortURL = &k, &u
			} else if p, ok := postBySlot[slot]; ok && p.ShortLinkKey != nil && p.ShortURL != nil {
				row.ShortLinkKey, row.ShortURL = p.ShortLinkKey, p.ShortURL
			}
			v.Links = append(v.Links, row)
		}
	}
	for _, l := range sortedLocations(locs) {
		fl, has := flyerByLoc[l.ID]
		if !l.Active && !has {
			continue
		}
		id, name, code := l.ID, l.Name, l.Code
		row := outreachLinkRowView{
			Kind: "flyer", ChannelLabel: "Flyer", LocationID: &id, LocationName: &name, LocationCode: &code,
			DestinationURL: outreach.FlyerDestinationURL(h.baseURL, wk.Slug, l.Code),
			UTM:            outreachUTMView{Source: "flyer", Medium: "print", Campaign: wk.Slug, Content: l.Code},
		}
		if has {
			k, u := fl.Key, fl.ShortURL
			row.ShortLinkKey, row.ShortURL = &k, &u
		}
		v.Links = append(v.Links, row)
	}
	var keys []string
	for _, row := range v.Links {
		if row.ShortLinkKey == nil {
			v.Missing++
		} else {
			keys = append(keys, *row.ShortLinkKey)
		}
	}
	switch {
	case !h.linksEnabled():
		v.ClicksStatus = resultsNotConfigured
	case len(keys) == 0:
		v.ClicksStatus = resultsOK
	default:
		from, to := outreach.ResultsWindow(o.CreatedAt, wk.StartsAt, h.now())
		rollup, _, err := h.statsCache().Get(ctx, o.CampaignSlug, from, to)
		if err != nil {
			v.ClicksStatus = classifyResultsError("links", err)
			break
		}
		v.ClicksStatus = resultsOK
		clicks := outreach.ClicksByKey(keys, rollup)
		for i := range v.Links {
			if k := v.Links[i].ShortLinkKey; k != nil {
				n := clicks[*k]
				v.Links[i].Clicks = &n
			}
		}
	}
	return v, nil
}

// workshopOutreach loads the workshop named by the path and its outreach, if
// any; ok=false means the response is written.
func (h *AdminOutreachHandler) workshopOutreach(w http.ResponseWriter, r *http.Request) (workshops.Workshop, *outreach.Outreach, bool) {
	id, ok := outreachPathID(w, r)
	if !ok {
		return workshops.Workshop{}, nil, false
	}
	wk, ok := h.loadWorkshop(w, r, id)
	if !ok {
		return wk, nil, false
	}
	o, err := h.store.GetByWorkshop(r.Context(), id)
	switch {
	case err == nil:
		return wk, &o, true
	case errors.Is(err, outreach.ErrNotFound):
		return wk, nil, true
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return wk, nil, false
	}
}

// Links handles GET /admin/workshops/{id}/outreach/links.
func (h *AdminOutreachHandler) Links(w http.ResponseWriter, r *http.Request) {
	wk, o, ok := h.workshopOutreach(w, r)
	if !ok {
		return
	}
	v, err := h.linksView(r.Context(), wk, o)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// CreateLinks handles POST /admin/workshops/{id}/outreach/links: create every
// missing tracked link. Idempotent; a partial failure is a 200 whose report
// names what failed.
func (h *AdminOutreachHandler) CreateLinks(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if !h.linksEnabled() {
		writeError(w, http.StatusServiceUnavailable, "ShortLinks is not configured")
		return
	}
	wk, o, ok := h.workshopOutreach(w, r)
	if !ok {
		return
	}
	if o == nil {
		writeError(w, http.StatusConflict, "start outreach first")
		return
	}
	if wk.Status != "published" {
		writeError(w, http.StatusConflict, "outreach is only available for a published workshop")
		return
	}
	report, err := h.ensureLinks(r, actor.ID, *o, wk)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	v, err := h.linksView(r.Context(), wk, o)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	v.Report = &report
	writeJSON(w, http.StatusOK, v)
}

type draftAllRequest struct {
	// Waves lists the optional waves to draft besides Announce.
	Waves []string `json:"waves"`
}

type draftAllCreated struct {
	PostID  int64  `json:"post_id"`
	Channel string `json:"channel"`
	Wave    string `json:"wave"`
	HasLink bool   `json:"has_link"`
}

type draftAllReport struct {
	Created     []draftAllCreated `json:"created"`
	Skipped     int               `json:"skipped"`
	WithoutLink int               `json:"without_link"`
}

// DraftAll handles POST /admin/workshops/{id}/outreach/draft-all: draft every
// slot that has no post, from the same template Regenerate uses, with the
// slot's link already in place. An existing post is never touched, and no
// ShortLinks call is made.
func (h *AdminOutreachHandler) DraftAll(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	wk, o, ok := h.workshopOutreach(w, r)
	if !ok {
		return
	}
	if o == nil {
		writeError(w, http.StatusConflict, "start outreach first")
		return
	}
	ctx := r.Context()
	posts, err := h.store.ListPosts(ctx, o.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	links, err := h.store.ListPostLinks(ctx, o.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	have := map[[2]string]bool{}
	for _, p := range posts {
		have[[2]string{p.Channel, p.Wave}] = true
	}
	linkBySlot := map[[2]string]outreach.PostLink{}
	for _, l := range links {
		linkBySlot[[2]string{l.Channel, l.Wave}] = l
	}
	// Announce is always drafted; Reminder and Last call only when asked for
	// (#0574), so a default Draft all makes a post per channel, not fifteen.
	var req draftAllRequest
	if raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	} else if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	wantWave := map[string]bool{"announce": true}
	for _, name := range req.Waves {
		if _, ok := outreach.WaveByName(name); !ok {
			writeError(w, http.StatusBadRequest, "unknown wave")
			return
		}
		wantWave[name] = true
	}
	f := h.facts(wk)
	rep := draftAllReport{Created: []draftAllCreated{}}
	for _, ch := range h.enabledChannels() {
		for _, wv := range outreach.Waves() {
			if !wantWave[wv.Name] {
				continue
			}
			slot := [2]string{ch.Name, wv.Name}
			if have[slot] {
				rep.Skipped++
				continue
			}
			p, err := h.store.CreatePost(ctx, o.ID, ch.Name, wv.Name, outreach.Draft(ch, wv, f), outreach.Fingerprint(f))
			if errors.Is(err, outreach.ErrPostExists) {
				rep.Skipped++
				continue
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			hasLink := false
			if l, ok := linkBySlot[slot]; ok {
				status := p.Status
				if !ch.Automatic {
					status = "manual"
				}
				if _, _, err := h.store.SetPrepared(ctx, p.ID, l.Key, l.ShortURL, status); err != nil {
					writeError(w, http.StatusInternalServerError, "internal server error")
					return
				}
				hasLink = true
			} else {
				rep.WithoutLink++
			}
			rep.Created = append(rep.Created, draftAllCreated{PostID: p.ID, Channel: ch.Name, Wave: wv.Name, HasLink: hasLink})
			if h.auditor != nil {
				actorID, targetID := actor.ID, p.ID
				h.auditor.Record(ctx, audit.Entry{
					ActorID: &actorID, Action: audit.ActionOutreachPostCreated, TargetType: audit.TargetOutreachPost, TargetID: &targetID,
					Metadata: map[string]any{"outreach_id": o.ID, "channel": p.Channel, "wave": p.Wave, "bulk": true, "link_applied": hasLink},
					IP:       clientIP(r),
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, rep)
}

type scheduleAllRequest struct {
	DryRun  bool    `json:"dry_run"`
	PostIDs []int64 `json:"post_ids"`
	// Times optionally overrides a chosen post's planned time (#0587), keyed by
	// post id, RFC 3339. Each must lie within the same lead window a single
	// Schedule uses; an override for a post not in post_ids is ignored.
	Times map[int64]string `json:"times"`
}

type scheduleAllItemView struct {
	PostID       int64  `json:"post_id"`
	Channel      string `json:"channel"`
	ChannelLabel string `json:"channel_label"`
	Wave         string `json:"wave"`
	WaveLabel    string `json:"wave_label"`
	ScheduledAt  string `json:"scheduled_at"`
}

type scheduleAllSkipView struct {
	PostID       *int64  `json:"post_id"`
	Channel      string  `json:"channel"`
	ChannelLabel string  `json:"channel_label"`
	Wave         string  `json:"wave"`
	WaveLabel    string  `json:"wave_label"`
	Reason       string  `json:"reason"`
	SuggestedAt  *string `json:"suggested_at"`
}

type scheduleAllResponse struct {
	DryRun     bool                  `json:"dry_run"`
	ToSchedule []scheduleAllItemView `json:"to_schedule"`
	Skipped    []scheduleAllSkipView `json:"skipped"`
}

func slotLabels(channel, wave string) (string, string) {
	chl, wvl := channel, wave
	if c, ok := outreach.ChannelByName(channel); ok {
		chl = c.Label
	}
	if wv, ok := outreach.WaveByName(wave); ok {
		wvl = wv.Label
	}
	return chl, wvl
}

// ScheduleAll handles POST /admin/workshops/{id}/outreach/schedule-all. With
// dry_run it only reports what would be scheduled; confirming sends the
// previewed post_ids and the server schedules only those still in its own
// plan, at each wave's suggested time. It never posts anything now.
func (h *AdminOutreachHandler) ScheduleAll(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	wk, o, ok := h.workshopOutreach(w, r)
	if !ok {
		return
	}
	if o == nil {
		writeError(w, http.StatusConflict, "start outreach first")
		return
	}
	if wk.Status != "published" {
		writeError(w, http.StatusConflict, "outreach is only available for a published workshop")
		return
	}
	var req scheduleAllRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !req.DryRun && len(req.PostIDs) == 0 {
		writeError(w, http.StatusBadRequest, "post_ids is required unless dry_run is true")
		return
	}
	overrides := map[int64]time.Time{}
	if !req.DryRun {
		now := h.now()
		for _, id := range req.PostIDs {
			raw, has := req.Times[id]
			if !has {
				continue
			}
			at, msg := parseScheduleAt(raw, now)
			if msg != "" {
				writeError(w, http.StatusBadRequest, "a chosen time "+msg)
				return
			}
			overrides[id] = at
		}
	}
	ctx := r.Context()
	posts, err := h.store.ListPosts(ctx, o.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	posts = h.enabledPosts(posts)
	items, skips := outreach.PlanScheduleAll(posts, wk.StartsAt, h.now(), minScheduleLead, maxScheduleLead, h.postHour(), h.channelConfigured)

	resp := scheduleAllResponse{DryRun: req.DryRun, ToSchedule: []scheduleAllItemView{}, Skipped: []scheduleAllSkipView{}}
	itemView := func(it outreach.ScheduleItem) scheduleAllItemView {
		cl, wl := slotLabels(it.Channel, it.Wave)
		return scheduleAllItemView{PostID: it.PostID, Channel: it.Channel, ChannelLabel: cl, Wave: it.Wave, WaveLabel: wl, ScheduledAt: it.ScheduledAt.UTC().Format(time.RFC3339)}
	}
	skipView := func(postID *int64, channel, wave, reason string, suggested *time.Time) scheduleAllSkipView {
		cl, wl := slotLabels(channel, wave)
		return scheduleAllSkipView{PostID: postID, Channel: channel, ChannelLabel: cl, Wave: wave, WaveLabel: wl, Reason: reason, SuggestedAt: rfc3339(suggested)}
	}
	if req.DryRun {
		for _, it := range items {
			resp.ToSchedule = append(resp.ToSchedule, itemView(it))
		}
		for _, s := range skips {
			resp.Skipped = append(resp.Skipped, skipView(s.PostID, s.Channel, s.Wave, s.Reason, s.SuggestedAt))
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	wanted := map[int64]bool{}
	for _, id := range req.PostIDs {
		wanted[id] = true
	}
	planned := map[int64]bool{}
	for _, it := range items {
		planned[it.PostID] = true
		if !wanted[it.PostID] {
			continue
		}
		at, overridden := overrides[it.PostID]
		if overridden {
			it.ScheduledAt = at
		}
		updated, err := h.store.Schedule(ctx, it.PostID, it.ScheduledAt)
		if err != nil {
			id := it.PostID
			resp.Skipped = append(resp.Skipped, skipView(&id, it.Channel, it.Wave, "changed", &it.ScheduledAt))
			continue
		}
		resp.ToSchedule = append(resp.ToSchedule, itemView(it))
		if h.auditor != nil {
			actorID, targetID := actor.ID, updated.ID
			h.auditor.Record(ctx, audit.Entry{
				ActorID: &actorID, Action: audit.ActionOutreachPostScheduled, TargetType: audit.TargetOutreachPost, TargetID: &targetID,
				Metadata: map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave, "scheduled_at": it.ScheduledAt.UTC().Format(time.RFC3339), "bulk": true, "time_overridden": overridden},
				IP:       clientIP(r),
			})
		}
	}
	byID := map[int64]outreach.Post{}
	for _, p := range posts {
		byID[p.ID] = p
	}
	for _, id := range req.PostIDs {
		if planned[id] {
			continue
		}
		id := id
		p := byID[id]
		resp.Skipped = append(resp.Skipped, skipView(&id, p.Channel, p.Wave, "changed", nil))
	}
	writeJSON(w, http.StatusOK, resp)
}
