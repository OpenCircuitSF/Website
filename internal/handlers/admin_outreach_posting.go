package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// Bounds on a schedule request: far enough ahead that it cannot be "now"
// by accident, and not so far that a typo queues a post for next decade.
const (
	minScheduleLead = time.Minute
	maxScheduleLead = 366 * 24 * time.Hour
)

// parseScheduleAt validates a requested schedule time: an RFC 3339 instant
// within the lead window. It returns the instant in UTC, or a non-empty
// reason (phrased to follow a field name) when the time is refused. ScheduleSection
// uses it (#0640).
func parseScheduleAt(raw string, now time.Time) (time.Time, string) {
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, "must be an RFC 3339 time"
	}
	if at.Before(now.Add(minScheduleLead)) {
		return time.Time{}, "must be at least a minute from now"
	}
	if at.After(now.Add(maxScheduleLead)) {
		return time.Time{}, "must be within a year from now"
	}
	return at.UTC(), ""
}

// postContextCtx loads a post and its workshop without the transport
// (#0701): not-found is a refusal, any other error is returned as is.
func (h *AdminOutreachHandler) postContextCtx(ctx context.Context, id int64) (outreach.Post, workshops.Workshop, error) {
	p, err := h.store.GetPost(ctx, id)
	if errors.Is(err, outreach.ErrNotFound) {
		return p, workshops.Workshop{}, outreachRefusal(http.StatusNotFound, "", "post not found")
	} else if err != nil {
		return p, workshops.Workshop{}, err
	}
	o, err := h.store.GetByID(ctx, p.OutreachID)
	if err != nil {
		return p, workshops.Workshop{}, err
	}
	wk, err := h.workshopFor(ctx, o.WorkshopID)
	return p, wk, err
}

// postingGate does the checks Post now and Retry share, returning a refusal
// when one fails. It resolves the post, its workshop and its channel; the
// channel must be automatic and configured.
func (h *AdminOutreachHandler) postingGate(ctx context.Context, id int64) (outreach.Post, workshops.Workshop, outreach.Channel, error) {
	p, wk, err := h.postContextCtx(ctx, id)
	if err != nil {
		return p, wk, outreach.Channel{}, err
	}
	ch, chOK := outreach.ChannelByName(p.Channel)
	if !chOK || !ch.Automatic {
		return p, wk, ch, outreachRefusal(http.StatusBadRequest, "channel", "this channel is posted by hand")
	}
	if !h.channelConfigured(ch.Name) {
		return p, wk, ch, outreachRefusal(http.StatusServiceUnavailable, "", ch.Label+" is not configured")
	}
	if !h.postVisible(p) {
		// A hidden draft on a switched-off channel (#0588); a post already
		// scheduled or failed there is still shown, so it can be acted on.
		return p, wk, ch, outreachRefusal(http.StatusConflict, "", ch.Label+" is switched off in Settings")
	}
	switch {
	case p.Status == "posted":
		return p, wk, ch, outreachRefusal(http.StatusConflict, "", "post is already posted")
	case p.Status == "queued":
		return p, wk, ch, outreachRefusal(http.StatusConflict, "", "post is being sent")
	case !p.Prepared():
		return p, wk, ch, outreachRefusal(http.StatusConflict, "", "prepare the post before sending it")
	case wk.Status != "published":
		return p, wk, ch, outreachRefusal(http.StatusConflict, "", "the event is not published")
	}
	if err := outreach.ValidateForSend(ch, p.Body); err != nil {
		return p, wk, ch, outreachRefusal(http.StatusBadRequest, "body", err.Error())
	}
	return p, wk, ch, nil
}

// postingStoreRefusal turns a store refusal into an outreachOpError; an
// unrecognised error is returned as is (a 500).
func postingStoreRefusal(err error) error {
	code, msg := postingStoreStatus(err)
	if code == http.StatusInternalServerError {
		return err
	}
	return outreachRefusal(code, "", msg)
}

// postingStoreStatus is the status code and message for a store refusal.
func postingStoreStatus(err error) (int, string) {
	switch {
	case errors.Is(err, outreach.ErrNotFound):
		return http.StatusNotFound, "post not found"
	case errors.Is(err, outreach.ErrPostLocked):
		return http.StatusConflict, "post is already posted"
	case errors.Is(err, outreach.ErrPostInFlight):
		return http.StatusConflict, "post is being sent"
	case errors.Is(err, outreach.ErrNotPrepared):
		return http.StatusConflict, "prepare the post before sending it"
	case errors.Is(err, outreach.ErrWorkshopNotPublished):
		return http.StatusConflict, "the event is not published"
	case errors.Is(err, outreach.ErrWrongStatus):
		return http.StatusConflict, "post is not in a status that allows this"
	default:
		return http.StatusInternalServerError, "internal server error"
	}
}

// SendPost handles POST /admin/outreach-posts/{id}/send: Post now, or Retry
// for a failed post. The post is queued for the publisher, so the response is
// 202 and the post reads "queued" until the publisher has acted.
func (h *AdminOutreachHandler) SendPost(w http.ResponseWriter, r *http.Request) {
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
	v, err := h.sendNow(r.Context(), clientIP(r), &actorID, id)
	if err != nil {
		writeOutreachError(w, "send post", err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

// sendNow is SendPost without the transport (#0701).
func (h *AdminOutreachHandler) sendNow(ctx context.Context, ip string, actorID *int64, id int64) (outreachPostView, error) {
	_, wk, _, err := h.postingGate(ctx, id)
	if err != nil {
		return outreachPostView{}, err
	}
	queued, prev, err := h.store.QueueNow(ctx, id)
	if err != nil {
		return outreachPostView{}, postingStoreRefusal(err)
	}
	h.recordCtx(ctx, ip, audit.TargetOutreachPost, queued.ID, audit.Entry{
		ActorID:  actorID,
		Action:   audit.ActionOutreachPostQueued,
		Metadata: map[string]any{"outreach_id": queued.OutreachID, "channel": queued.Channel, "wave": queued.Wave, "retry": prev == "failed"},
	})
	return h.postView(queued, h.facts(wk), wk.Status), nil
}

type scheduleSectionRequest struct {
	Wave        string `json:"wave"`
	ScheduledAt string `json:"scheduled_at"`
}

type unscheduleSectionRequest struct {
	Wave string `json:"wave"`
}

// sectionLeftAlone is an automatic post the section call did not touch and why:
// posted, posting_now or not_connected.
type sectionLeftAlone struct {
	Channel      string `json:"channel"`
	ChannelLabel string `json:"channel_label"`
	Reason       string `json:"reason"`
}

type sectionResponse struct {
	Posts     []outreachPostView `json:"posts"`
	LeftAlone []sectionLeftAlone `json:"left_alone"`
}

// sectionTargets resolves one section's automatic posts for the section
// routes (#0640): the ones a call may act on, and the ones it leaves alone
// with the reason, both in channel order. anyConnection keeps a channel that
// is not connected among the targets: the unschedule route passes it, since
// stopping a post is always safe.
func (h *AdminOutreachHandler) sectionTargets(ctx context.Context, workshopID int64, wave string, anyConnection bool) (workshops.Workshop, []outreach.Post, []sectionLeftAlone, error) {
	wk, err := h.workshopFor(ctx, workshopID)
	if err != nil {
		return wk, nil, nil, err
	}
	o, err := h.store.GetByWorkshop(ctx, workshopID)
	if errors.Is(err, outreach.ErrNotFound) {
		return wk, nil, nil, outreachRefusal(http.StatusConflict, "", "start outreach first")
	} else if err != nil {
		return wk, nil, nil, err
	}
	if wk.Status != "published" {
		return wk, nil, nil, outreachRefusal(http.StatusConflict, "", "the event is not published")
	}
	if _, ok := outreach.WaveByName(wave); !ok {
		return wk, nil, nil, outreachRefusal(http.StatusBadRequest, "wave", "wave must be one of announce, reminder, last-call")
	}
	all, err := h.store.ListPosts(ctx, o.ID)
	if err != nil {
		return wk, nil, nil, err
	}
	posts := h.enabledPosts(all)
	targets := []outreach.Post{}
	left := []sectionLeftAlone{}
	for _, ch := range outreach.Channels() {
		if !ch.Automatic {
			continue
		}
		for _, p := range posts {
			if p.Wave != wave || p.Channel != ch.Name {
				continue
			}
			reason := ""
			switch {
			case !anyConnection && !h.channelConfigured(ch.Name):
				reason = "not_connected"
			case p.Status == "posted":
				reason = "posted"
			case p.Status == "queued":
				reason = "posting_now"
			}
			if reason != "" {
				left = append(left, sectionLeftAlone{Channel: ch.Name, ChannelLabel: ch.Label, Reason: reason})
				continue
			}
			targets = append(targets, p)
		}
	}
	return wk, targets, left, nil
}

// channelLabel is the display name of a channel, or the name itself.
func channelLabel(name string) string {
	if ch, ok := outreach.ChannelByName(name); ok {
		return ch.Label
	}
	return name
}

// sectionPostViews renders posts in channel order.
func (h *AdminOutreachHandler) sectionPostViews(posts []outreach.Post, wk workshops.Workshop) []outreachPostView {
	f := h.facts(wk)
	views := make([]outreachPostView, 0, len(posts))
	for _, ch := range outreach.Channels() {
		for _, p := range posts {
			if p.Channel == ch.Name {
				views = append(views, h.postView(p, f, wk.Status))
			}
		}
	}
	return views
}

// sectionRefusal maps a many-post store refusal to a refusal that names the
// channel it was about.
func sectionRefusal(err error, targets []outreach.Post) error {
	var pr *outreach.PostRefusal
	if !errors.As(err, &pr) {
		return postingStoreRefusal(err)
	}
	code, msg := postingStoreStatus(pr.Err)
	if code == http.StatusInternalServerError {
		return err
	}
	for _, p := range targets {
		if p.ID == pr.PostID {
			msg = channelLabel(p.Channel) + ": " + msg
			break
		}
	}
	return outreachRefusal(code, "", msg)
}

// ScheduleSection handles POST /admin/workshops/{id}/outreach/schedule: every
// automatic post of one section gets the same time in one call, all or
// nothing (#0640). Posts already posted or posting, and channels not
// connected, are left alone and listed in left_alone.
func (h *AdminOutreachHandler) ScheduleSection(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req scheduleSectionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	actorID := actor.ID
	res, err := h.scheduleWave(r.Context(), clientIP(r), &actorID, id, req.Wave, req.ScheduledAt)
	if err != nil {
		writeOutreachError(w, "schedule section", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// scheduleWave is ScheduleSection without the transport (#0701).
func (h *AdminOutreachHandler) scheduleWave(ctx context.Context, ip string, actorID *int64, workshopID int64, wave, scheduledAt string) (sectionResponse, error) {
	at, msg := parseScheduleAt(scheduledAt, h.now())
	if msg != "" {
		return sectionResponse{}, outreachRefusal(http.StatusBadRequest, "scheduled_at", "scheduled_at "+msg)
	}
	wk, targets, left, err := h.sectionTargets(ctx, workshopID, wave, false)
	if err != nil {
		return sectionResponse{}, err
	}
	if len(targets) == 0 {
		return sectionResponse{}, outreachRefusal(http.StatusConflict, "", "nothing in this section can be scheduled")
	}
	ids := make([]int64, 0, len(targets))
	for _, p := range targets {
		ch, _ := outreach.ChannelByName(p.Channel)
		if !p.Prepared() {
			return sectionResponse{}, outreachRefusal(http.StatusConflict, "", ch.Label+" has no link yet")
		}
		if err := outreach.ValidateForSend(ch, p.Body); err != nil {
			return sectionResponse{}, outreachRefusal(http.StatusBadRequest, "body", ch.Label+": "+err.Error())
		}
		ids = append(ids, p.ID)
	}
	updated, err := h.store.ScheduleMany(ctx, ids, at.UTC())
	if err != nil {
		return sectionResponse{}, sectionRefusal(err, targets)
	}
	for _, p := range updated {
		h.recordCtx(ctx, ip, audit.TargetOutreachPost, p.ID, audit.Entry{
			ActorID:  actorID,
			Action:   audit.ActionOutreachPostScheduled,
			Metadata: map[string]any{"outreach_id": p.OutreachID, "channel": p.Channel, "wave": p.Wave, "scheduled_at": at.UTC().Format(time.RFC3339), "section": true},
		})
	}
	return sectionResponse{Posts: h.sectionPostViews(updated, wk), LeftAlone: left}, nil
}

// UnscheduleSection handles POST /admin/workshops/{id}/outreach/unschedule:
// every scheduled automatic post of one section goes back to draft in one
// call, all or nothing (#0640), including one on a channel since disconnected
// or switched off.
func (h *AdminOutreachHandler) UnscheduleSection(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req unscheduleSectionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	actorID := actor.ID
	res, err := h.unscheduleWave(r.Context(), clientIP(r), &actorID, id, req.Wave)
	if err != nil {
		writeOutreachError(w, "unschedule section", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// unscheduleWave is UnscheduleSection without the transport (#0701).
func (h *AdminOutreachHandler) unscheduleWave(ctx context.Context, ip string, actorID *int64, workshopID int64, wave string) (sectionResponse, error) {
	wk, all, left, err := h.sectionTargets(ctx, workshopID, wave, true)
	if err != nil {
		return sectionResponse{}, err
	}
	var targets []outreach.Post
	ids := []int64{}
	for _, p := range all {
		if p.Status == "scheduled" {
			targets = append(targets, p)
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 0 {
		return sectionResponse{}, outreachRefusal(http.StatusConflict, "", "nothing in this section is scheduled")
	}
	updated, err := h.store.UnscheduleMany(ctx, ids)
	if err != nil {
		return sectionResponse{}, sectionRefusal(err, targets)
	}
	for _, p := range updated {
		h.recordCtx(ctx, ip, audit.TargetOutreachPost, p.ID, audit.Entry{
			ActorID:  actorID,
			Action:   audit.ActionOutreachPostUnscheduled,
			Metadata: map[string]any{"outreach_id": p.OutreachID, "channel": p.Channel, "wave": p.Wave, "section": true},
		})
	}
	return sectionResponse{Posts: h.sectionPostViews(updated, wk), LeftAlone: left}, nil
}
