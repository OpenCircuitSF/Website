package handlers

import (
	"net/http"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// timelineWindow is how far ahead the Outreach home timeline looks (#0646,
// the user's decision): only published events starting within this window of
// now are on it. Later events stay in the list below it.
const timelineWindow = 30 * 24 * time.Hour

// outreachTimelineEvent is one event on the Outreach home timeline. Its
// channels, waves, posts and off_channels have the same shape as the panel's,
// so the SPA's section line and board cells run on it unchanged.
type outreachTimelineEvent struct {
	Workshop    outreachWorkshopView  `json:"workshop"`
	Outreach    *outreachRowView      `json:"outreach"`
	Channels    []outreachChannelView `json:"channels"`
	Waves       []outreachWaveView    `json:"waves"`
	Posts       []outreachPostView    `json:"posts"`
	OffChannels []outreach.Channel    `json:"off_channels"`
}

// Timeline handles GET /admin/outreach-timeline (#0646): every published
// event starting between now and now+timelineWindow, in start order, with its
// outreach row (null when not started) and its posts. Undated and canceled
// events are left out, and so are a switched-off channel's drafts, the same
// rule List and the panel apply. It makes no network call (no ShortLinks, no
// thumbnail check, unlike the panel), so the home stays fast and survives an
// outage.
func (h *AdminOutreachHandler) Timeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := h.now()
	upcoming, _, err := h.ws.ListVisible(ctx, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	limit := now.Add(timelineWindow)
	var picked []workshops.Workshop
	for _, wk := range upcoming {
		if wk.Status != workshops.StatusPublished || wk.StartsAt == nil || wk.StartsAt.After(limit) {
			continue
		}
		picked = append(picked, wk)
	}
	sums, err := h.store.ListSummaries(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	byWorkshop := map[int64]outreach.Summary{}
	for _, wk := range picked {
		for _, s := range sums {
			if s.WorkshopID == wk.ID {
				byWorkshop[wk.ID] = s
			}
		}
	}
	ids := make([]int64, 0, len(byWorkshop))
	for _, s := range byWorkshop {
		ids = append(ids, s.OutreachID)
	}
	postsBy, err := h.store.ListPostsByOutreach(ctx, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	events := make([]outreachTimelineEvent, 0, len(picked))
	for _, wk := range picked {
		ev := outreachTimelineEvent{
			Workshop: h.workshopView(wk), Waves: h.waveViews(wk), OffChannels: h.offChannels(),
			Channels: h.channelViews(nil), Posts: []outreachPostView{},
		}
		if s, ok := byWorkshop[wk.ID]; ok {
			posts := postsBy[s.OutreachID]
			ev.Outreach = &outreachRowView{ID: s.OutreachID, WorkshopID: s.WorkshopID, CampaignSlug: s.CampaignSlug, CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339)}
			ev.Channels = h.channelViews(posts)
			f := h.facts(wk)
			for _, p := range h.enabledPosts(posts) {
				ev.Posts = append(ev.Posts, h.postView(p, f, wk.Status))
			}
		}
		events = append(events, ev)
	}
	writeJSON(w, http.StatusOK, map[string]any{"now": now.UTC().Format(time.RFC3339), "events": events})
}
