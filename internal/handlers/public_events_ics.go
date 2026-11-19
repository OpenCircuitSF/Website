package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/internal/ical"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// CalendarHandler serves GET /events.ics (#0732): every published or
// publicly-listed canceled upcoming event as an iCalendar feed. It shares
// List's ListVisible read, keeps only the upcoming slice, re-applies
// workshopPageStatus so a draft or never-published row can never appear, and
// skips rows with no start time, which a calendar cannot place. baseURL is
// the canonical origin used for each event's URL and its UID host.
func (h *PublicWorkshopsHandler) CalendarHandler(baseURL string) http.HandlerFunc {
	base := strings.TrimRight(baseURL, "/")
	host := base
	if u, err := url.Parse(base); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		upcoming, _, err := h.store.ListVisible(r.Context(), h.now())
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		events := make([]ical.Event, 0, len(upcoming))
		for _, wk := range upcoming {
			if workshopPageStatus(wk) != http.StatusOK || wk.StartsAt == nil {
				continue
			}
			eventURL := base + "/events/" + wk.Slug
			var loc []string
			for _, p := range []*string{wk.LocationName, wk.LocationAddress} {
				if p != nil && strings.TrimSpace(*p) != "" {
					loc = append(loc, strings.TrimSpace(*p))
				}
			}
			desc := eventURL
			if wk.Summary != nil && strings.TrimSpace(*wk.Summary) != "" {
				desc = strings.TrimSpace(*wk.Summary) + "\n\n" + eventURL
			}
			events = append(events, ical.Event{
				UID:         fmt.Sprintf("event-%d@%s", wk.ID, host),
				Start:       *wk.StartsAt,
				End:         wk.EndsAt,
				Stamp:       wk.UpdatedAt,
				Summary:     wk.Title,
				Location:    strings.Join(loc, ", "),
				Description: desc,
				URL:         eventURL,
				Cancelled:   wk.Status == workshops.StatusCanceled,
			})
		}
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(ical.Calendar("Open Circuit SF", events))
	}
}
