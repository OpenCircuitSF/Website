package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/luma"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// lumaFeed is the read of the Luma calendar feed the Luma route needs.
type lumaFeed interface {
	Feed(ctx context.Context) luma.Snapshot
}

// SetLuma wires the Luma calendar check (#0648); without it the route
// answers status "off".
func (h *AdminOutreachHandler) SetLuma(f lumaFeed) { h.luma = f }

type outreachLumaEventView struct {
	WorkshopID int64       `json:"workshop_id"`
	State      string      `json:"state"`
	LumaURL    *string     `json:"luma_url"`
	LumaTitle  *string     `json:"luma_title"`
	Diffs      []luma.Diff `json:"diffs"`
}

type outreachLumaLeftoverView struct {
	Title    string  `json:"title"`
	StartsAt string  `json:"starts_at"`
	LumaURL  *string `json:"luma_url"`
}

type outreachLumaView struct {
	Status     luma.Status                `json:"status"`
	ReadAt     *string                    `json:"read_at"`
	LastGoodAt *string                    `json:"last_good_at"`
	Events     []outreachLumaEventView    `json:"events"`
	Leftovers  []outreachLumaLeftoverView `json:"leftovers"`
}

func rfc3339OrNil(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Luma handles GET /admin/outreach-luma (#0648): each upcoming published
// event's state against the Luma calendar's public feed (on Luma, differs,
// not on Luma), the fields that differ, and the Luma events that match no
// event. The server reads the feed, cached by luma.Checker; the browser never
// calls Luma. It is a route of its own, apart from Timeline, because Timeline
// makes no network call and must stay fast when Luma is slow or down.
func (h *AdminOutreachHandler) Luma(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	view := outreachLumaView{Status: luma.StatusOff, Events: []outreachLumaEventView{}, Leftovers: []outreachLumaLeftoverView{}}
	if h.luma == nil {
		writeJSON(w, http.StatusOK, view)
		return
	}
	snap := h.luma.Feed(ctx)
	view.Status, view.LastGoodAt = snap.Status, rfc3339OrNil(snap.LastGoodAt)
	if snap.Status != luma.StatusOK {
		writeJSON(w, http.StatusOK, view)
		return
	}
	view.ReadAt = rfc3339OrNil(snap.ReadAt)
	now := h.now()
	upcoming, _, err := h.ws.ListVisible(ctx, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	var list []luma.Workshop
	for _, wk := range upcoming {
		if wk.Status != workshops.StatusPublished || wk.StartsAt == nil {
			continue
		}
		lw := luma.Workshop{ID: wk.ID, Title: wk.Title, Start: *wk.StartsAt, End: wk.EndsAt}
		if wk.LocationAddress != nil {
			lw.Address = *wk.LocationAddress
		}
		list = append(list, lw)
	}
	results, left := luma.Compare(list, snap.Events, now)
	for _, res := range results {
		ev := outreachLumaEventView{WorkshopID: res.WorkshopID, State: res.State, Diffs: res.Diffs}
		if res.Event != nil {
			ev.LumaURL, ev.LumaTitle = strOrNil(res.Event.URL), strOrNil(res.Event.Summary)
		}
		view.Events = append(view.Events, ev)
	}
	for _, e := range left {
		view.Leftovers = append(view.Leftovers, outreachLumaLeftoverView{Title: e.Summary, StartsAt: e.Start.UTC().Format(time.RFC3339), LumaURL: strOrNil(e.URL)})
	}
	writeJSON(w, http.StatusOK, view)
}
