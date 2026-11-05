package handlers

import (
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
// reason (phrased to follow a field name) when the time is refused. Single
// Schedule and Schedule all's per-post overrides (#0587) share it so their
// windows cannot drift apart.
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

// postingGate does the checks Post now, Retry and Schedule share, writing the
// response and returning ok=false when one fails. It resolves the post, its
// workshop and its channel; the channel must be automatic and configured.
func (h *AdminOutreachHandler) postingGate(w http.ResponseWriter, r *http.Request, id int64) (outreach.Post, workshops.Workshop, outreach.Channel, bool) {
	p, wk, ok := h.postContext(w, r, id)
	if !ok {
		return p, wk, outreach.Channel{}, false
	}
	ch, chOK := outreach.ChannelByName(p.Channel)
	if !chOK || !ch.Automatic {
		writeError(w, http.StatusBadRequest, "this channel is posted by hand")
		return p, wk, ch, false
	}
	if !h.channelConfigured(ch.Name) {
		writeError(w, http.StatusServiceUnavailable, ch.Label+" is not configured")
		return p, wk, ch, false
	}
	if !h.postVisible(p) {
		// A hidden draft on a switched-off channel (#0588); a post already
		// scheduled or failed there is still shown, so it can be acted on.
		writeError(w, http.StatusConflict, ch.Label+" is switched off in Settings")
		return p, wk, ch, false
	}
	switch {
	case p.Status == "posted":
		writeError(w, http.StatusConflict, "post is already posted")
	case p.Status == "queued":
		writeError(w, http.StatusConflict, "post is being sent")
	case !p.Prepared():
		writeError(w, http.StatusConflict, "prepare the post before sending it")
	case wk.Status != "published":
		writeError(w, http.StatusConflict, "the event is not published")
	default:
		if err := outreach.ValidateForSend(ch, p.Body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			break
		}
		return p, wk, ch, true
	}
	return p, wk, ch, false
}

// writePostingStoreError maps a store refusal to a response.
func writePostingStoreError(w http.ResponseWriter, err error) {
	code, msg := postingStoreStatus(err)
	writeError(w, code, msg)
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
	_, wk, _, ok := h.postingGate(w, r, id)
	if !ok {
		return
	}
	queued, prev, err := h.store.QueueNow(r.Context(), id)
	if err != nil {
		writePostingStoreError(w, err)
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := queued.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachPostQueued,
			TargetType: audit.TargetOutreachPost,
			TargetID:   &targetID,
			Metadata:   map[string]any{"outreach_id": queued.OutreachID, "channel": queued.Channel, "wave": queued.Wave, "retry": prev == "failed"},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusAccepted, h.postView(queued, h.facts(wk), wk.Status))
}

type scheduleOutreachPostRequest struct {
	ScheduledAt string `json:"scheduled_at"`
}

// SchedulePost handles POST /admin/outreach-posts/{id}/schedule. The time
// must be an RFC 3339 instant between a minute and a year from now. Calling
// it on an already scheduled post reschedules it.
func (h *AdminOutreachHandler) SchedulePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	var req scheduleOutreachPostRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	at, msg := parseScheduleAt(req.ScheduledAt, h.now())
	if msg != "" {
		writeError(w, http.StatusBadRequest, "scheduled_at "+msg)
		return
	}
	_, wk, _, ok := h.postingGate(w, r, id)
	if !ok {
		return
	}
	updated, err := h.store.Schedule(r.Context(), id, at.UTC())
	if err != nil {
		writePostingStoreError(w, err)
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachPostScheduled,
			TargetType: audit.TargetOutreachPost,
			TargetID:   &targetID,
			Metadata:   map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave, "scheduled_at": at.UTC().Format(time.RFC3339)},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, h.postView(updated, h.facts(wk), wk.Status))
}

// UnschedulePost handles POST /admin/outreach-posts/{id}/unschedule: a
// scheduled post goes back to draft.
func (h *AdminOutreachHandler) UnschedulePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachPathID(w, r)
	if !ok {
		return
	}
	_, wk, ok := h.postContext(w, r, id)
	if !ok {
		return
	}
	updated, err := h.store.Unschedule(r.Context(), id)
	if err != nil {
		writePostingStoreError(w, err)
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachPostUnscheduled,
			TargetType: audit.TargetOutreachPost,
			TargetID:   &targetID,
			Metadata:   map[string]any{"outreach_id": updated.OutreachID, "channel": updated.Channel, "wave": updated.Wave},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, h.postView(updated, h.facts(wk), wk.Status))
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
// stopping a post is always safe. ok=false means the response is written.
func (h *AdminOutreachHandler) sectionTargets(w http.ResponseWriter, r *http.Request, wave string, anyConnection bool) (workshops.Workshop, []outreach.Post, []sectionLeftAlone, bool) {
	wk, o, ok := h.workshopOutreach(w, r)
	if !ok {
		return wk, nil, nil, false
	}
	if o == nil {
		writeError(w, http.StatusConflict, "start outreach first")
		return wk, nil, nil, false
	}
	if wk.Status != "published" {
		writeError(w, http.StatusConflict, "the event is not published")
		return wk, nil, nil, false
	}
	if _, ok := outreach.WaveByName(wave); !ok {
		writeError(w, http.StatusBadRequest, "wave must be one of announce, reminder, last-call")
		return wk, nil, nil, false
	}
	all, err := h.store.ListPosts(r.Context(), o.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return wk, nil, nil, false
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
	return wk, targets, left, true
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

// writeSectionRefusal maps a many-post store refusal to a response that
// names the channel it was about.
func writeSectionRefusal(w http.ResponseWriter, err error, targets []outreach.Post) {
	var pr *outreach.PostRefusal
	if !errors.As(err, &pr) {
		writePostingStoreError(w, err)
		return
	}
	code, msg := postingStoreStatus(pr.Err)
	for _, p := range targets {
		if p.ID == pr.PostID {
			msg = channelLabel(p.Channel) + ": " + msg
			break
		}
	}
	writeError(w, code, msg)
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
	at, msg := parseScheduleAt(req.ScheduledAt, h.now())
	if msg != "" {
		writeError(w, http.StatusBadRequest, "scheduled_at "+msg)
		return
	}
	wk, targets, left, ok := h.sectionTargets(w, r, req.Wave, false)
	if !ok {
		return
	}
	if len(targets) == 0 {
		writeError(w, http.StatusConflict, "nothing in this section can be scheduled")
		return
	}
	ids := make([]int64, 0, len(targets))
	for _, p := range targets {
		ch, _ := outreach.ChannelByName(p.Channel)
		if !p.Prepared() {
			writeError(w, http.StatusConflict, ch.Label+" has no link yet")
			return
		}
		if err := outreach.ValidateForSend(ch, p.Body); err != nil {
			writeError(w, http.StatusBadRequest, ch.Label+": "+err.Error())
			return
		}
		ids = append(ids, p.ID)
	}
	updated, err := h.store.ScheduleMany(r.Context(), ids, at.UTC())
	if err != nil {
		writeSectionRefusal(w, err, targets)
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		for _, p := range updated {
			targetID := p.ID
			h.auditor.Record(r.Context(), audit.Entry{
				ActorID:    &actorID,
				Action:     audit.ActionOutreachPostScheduled,
				TargetType: audit.TargetOutreachPost,
				TargetID:   &targetID,
				Metadata:   map[string]any{"outreach_id": p.OutreachID, "channel": p.Channel, "wave": p.Wave, "scheduled_at": at.UTC().Format(time.RFC3339), "section": true},
				IP:         clientIP(r),
			})
		}
	}
	writeJSON(w, http.StatusOK, sectionResponse{Posts: h.sectionPostViews(updated, wk), LeftAlone: left})
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
	wk, all, left, ok := h.sectionTargets(w, r, req.Wave, true)
	if !ok {
		return
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
		writeError(w, http.StatusConflict, "nothing in this section is scheduled")
		return
	}
	updated, err := h.store.UnscheduleMany(r.Context(), ids)
	if err != nil {
		writeSectionRefusal(w, err, targets)
		return
	}
	if h.auditor != nil {
		actorID := actor.ID
		for _, p := range updated {
			targetID := p.ID
			h.auditor.Record(r.Context(), audit.Entry{
				ActorID:    &actorID,
				Action:     audit.ActionOutreachPostUnscheduled,
				TargetType: audit.TargetOutreachPost,
				TargetID:   &targetID,
				Metadata:   map[string]any{"outreach_id": p.OutreachID, "channel": p.Channel, "wave": p.Wave, "section": true},
				IP:         clientIP(r),
			})
		}
	}
	writeJSON(w, http.StatusOK, sectionResponse{Posts: h.sectionPostViews(updated, wk), LeftAlone: left})
}
