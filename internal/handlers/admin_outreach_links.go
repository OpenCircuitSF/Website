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
	// Drafted counts the posts EnsureHandLinks made alongside new links (#0623).
	Drafted int `json:"drafted"`
	// createdLinks holds the slot links this pass inserted; never sent.
	createdLinks []outreach.PostLink
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
// channel x wave and one per active flyer location. It is the helper behind
// Start, so a link made by any path is the same row. A started workshop gets
// no hand-posted links (#0638). The error return is for store failures only;
// ShortLinks failures are reported per link and never abort the pass, except a
// rate limit or the deadline, which stop it with the rest reported as not
// tried.
func (h *AdminOutreachHandler) ensureLinks(r *http.Request, actorID int64, o outreach.Outreach, wk workshops.Workshop) (outreachLinkReport, error) {
	chs := h.enabledChannels()
	if h.eventStarted(wk) {
		var auto []outreach.Channel
		for _, ch := range chs {
			if ch.Automatic {
				auto = append(auto, ch)
			}
		}
		chs = auto
	}
	return h.ensureLinksFor(r, actorID, o, wk, chs, true)
}

// ensureLinksFor is ensureLinks limited to the given channels, with or without
// the flyer locations.
func (h *AdminOutreachHandler) ensureLinksFor(r *http.Request, actorID int64, o outreach.Outreach, wk workshops.Workshop, chs []outreach.Channel, withFlyers bool) (outreachLinkReport, error) {
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
	for _, ch := range chs {
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
	if withFlyers {
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
			row, created, err := h.store.InsertPostLink(ctx, o.ID, t.channel.Name, t.wave.Name, link.Key, shortlinks.ShortURL(base, link.Key))
			if err != nil {
				return report, err
			}
			if !created {
				report.Existing++
				break
			}
			report.Created++
			report.createdLinks = append(report.createdLinks, row)
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
