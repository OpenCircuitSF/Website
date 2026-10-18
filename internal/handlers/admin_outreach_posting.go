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
	switch {
	case p.Status == "posted":
		writeError(w, http.StatusConflict, "post is already posted")
	case p.Status == "queued":
		writeError(w, http.StatusConflict, "post is being sent")
	case !p.Prepared():
		writeError(w, http.StatusConflict, "prepare the post before sending it")
	case wk.Status != "published":
		writeError(w, http.StatusConflict, "the workshop is not published")
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
	switch {
	case errors.Is(err, outreach.ErrNotFound):
		writeError(w, http.StatusNotFound, "post not found")
	case errors.Is(err, outreach.ErrPostLocked):
		writeError(w, http.StatusConflict, "post is already posted")
	case errors.Is(err, outreach.ErrPostInFlight):
		writeError(w, http.StatusConflict, "post is being sent")
	case errors.Is(err, outreach.ErrNotPrepared):
		writeError(w, http.StatusConflict, "prepare the post before sending it")
	case errors.Is(err, outreach.ErrWorkshopNotPublished):
		writeError(w, http.StatusConflict, "the workshop is not published")
	case errors.Is(err, outreach.ErrWrongStatus):
		writeError(w, http.StatusConflict, "post is not in a status that allows this")
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
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
	at, err := time.Parse(time.RFC3339, req.ScheduledAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "scheduled_at must be an RFC 3339 time")
		return
	}
	now := h.now()
	if at.Before(now.Add(minScheduleLead)) {
		writeError(w, http.StatusBadRequest, "scheduled_at must be at least a minute from now")
		return
	}
	if at.After(now.Add(maxScheduleLead)) {
		writeError(w, http.StatusBadRequest, "scheduled_at must be within a year from now")
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
