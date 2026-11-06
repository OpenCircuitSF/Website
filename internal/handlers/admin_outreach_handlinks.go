package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// outreachHandLinkView is one hand-posted channel x wave slot and its short
// URL, null until the link exists (#0623).
type outreachHandLinkView struct {
	Channel  string  `json:"channel"`
	Wave     string  `json:"wave"`
	ShortURL *string `json:"short_url"`
}

// eventStarted reports whether the workshop's start time has passed
// (#0638), by the public listing's rule (workshops.Store.ListVisible): a
// start at or after now is upcoming, and an undated workshop never starts.
func (h *AdminOutreachHandler) eventStarted(wk workshops.Workshop) bool {
	return wk.StartsAt != nil && wk.StartsAt.Before(h.now())
}

// handPostedChannels is the switched-on channels the site can never post to
// itself. It reads the static Automatic flag, not whether credentials exist: an
// unconfigured Bluesky is still not hand-posted here.
func (h *AdminOutreachHandler) handPostedChannels() []outreach.Channel {
	var out []outreach.Channel
	for _, ch := range h.enabledChannels() {
		if !ch.Automatic {
			out = append(out, ch)
		}
	}
	return out
}

// handLinkViews builds one row per hand-posted channel by wave from what the
// database already holds, and reports whether any row has no URL. It makes no
// network call, so the panel's GET stays read-only and survives an outage. A
// post prepared before slot links existed counts, since the next pass adopts
// its link without calling ShortLinks.
func (h *AdminOutreachHandler) handLinkViews(links []outreach.PostLink, posts []outreach.Post) ([]outreachHandLinkView, bool) {
	urls := map[[2]string]string{}
	for _, p := range posts {
		if p.ShortURL != nil {
			urls[[2]string{p.Channel, p.Wave}] = *p.ShortURL
		}
	}
	for _, l := range links {
		urls[[2]string{l.Channel, l.Wave}] = l.ShortURL
	}
	out := []outreachHandLinkView{}
	missing := false
	for _, ch := range h.handPostedChannels() {
		for _, wv := range outreach.Waves() {
			row := outreachHandLinkView{Channel: ch.Name, Wave: wv.Name}
			if u, ok := urls[[2]string{ch.Name, wv.Name}]; ok {
				row.ShortURL = &u
			} else {
				missing = true
			}
			out = append(out, row)
		}
	}
	return out, missing
}

// EnsureHandLinks handles POST /admin/workshops/{id}/outreach/hand-links: make
// every missing link for the hand-posted channels, in all three waves. A draft
// is made only in the pass that creates its slot's link, and only in a wave
// that already shows a post:
//
//   - A draft in an unwritten wave would flip that wave from Write to open and
//     show its automatic channels as missing drafts.
//   - A slot whose link already exists is left alone, so a draft the user
//     deleted does not come back.
//
// A ShortLinks failure is a 200 whose report names it; the slot gets no link
// and no draft, and the next pass does both.
//
//   - A started workshop gets nothing new, and what it has is left as it is
//     (#0638).
//   - The pass runs to the end even if the browser goes away, so a slot is
//     never left with a link and no draft (#0630). outreachLinksTimeout still
//     bounds the ShortLinks calls.
//   - Accepted (#0630): a draft deleted before its slot had a link comes back
//     once, on the pass that makes the link, because no record of the delete
//     exists and keeping one needs a migration.
//   - Accepted (#0630): two passes at once (two tabs, or a reload while the
//     first is still running) still give each slot one link and at most one
//     draft. ShortLinks de-duplicates only a destination it has already
//     stored, though, so two truly simultaneous first creates can leave one
//     unused, zero-click link upstream.
func (h *AdminOutreachHandler) EnsureHandLinks(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusConflict, "outreach is only available for a published event")
		return
	}
	if h.eventStarted(wk) {
		writeJSON(w, http.StatusOK, outreachLinkReport{Failed: []outreachLinkFailure{}})
		return
	}
	r = r.WithContext(context.WithoutCancel(r.Context()))
	ctx := r.Context()
	posts, err := h.store.ListPosts(ctx, o.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	report, err := h.ensureLinksFor(r, actor.ID, *o, wk, h.handPostedChannels(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	have := map[[2]string]bool{}
	for _, p := range posts {
		have[[2]string{p.Channel, p.Wave}] = true
	}
	waveHasPost := map[string]bool{}
	for _, p := range h.enabledPosts(posts) {
		waveHasPost[p.Wave] = true
	}
	f := h.facts(wk)
	for _, l := range report.createdLinks {
		if !waveHasPost[l.Wave] || have[[2]string{l.Channel, l.Wave}] {
			continue
		}
		ch, _ := outreach.ChannelByName(l.Channel)
		wv, _ := outreach.WaveByName(l.Wave)
		p, err := h.store.CreatePost(ctx, o.ID, l.Channel, l.Wave, outreach.Draft(ch, wv, f), outreach.Fingerprint(f))
		if errors.Is(err, outreach.ErrPostExists) {
			continue
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if _, _, err := h.store.SetPrepared(ctx, p.ID, l.Key, l.ShortURL, "manual"); err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		report.Drafted++
		if h.auditor != nil {
			actorID, targetID := actor.ID, p.ID
			h.auditor.Record(ctx, audit.Entry{
				ActorID: &actorID, Action: audit.ActionOutreachPostCreated, TargetType: audit.TargetOutreachPost, TargetID: &targetID,
				Metadata: map[string]any{"outreach_id": o.ID, "channel": p.Channel, "wave": p.Wave, "bulk": true, "link_applied": true},
				IP:       clientIP(r),
			})
		}
	}
	writeJSON(w, http.StatusOK, report)
}
