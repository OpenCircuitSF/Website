package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

const icsTestBase = "https://www.example.test"

func getEventsICS(t *testing.T, pool *pgxpool.Pool) (*http.Response, string) {
	t.Helper()
	h := NewPublicWorkshopsHandler(workshops.NewStore(pool), nil)
	mux := http.NewServeMux()
	mux.Handle("GET /events.ics", h.CalendarHandler(icsTestBase))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/events.ics")
	if err != nil {
		t.Fatalf("GET /events.ics: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	return resp, strings.ReplaceAll(string(body), "\r\n ", "")
}

// seedICSWorkshop creates a workshop and walks it to the wanted state:
// "published", "draft", "canceled" (via published) or "canceled-unpublished".
func seedICSWorkshop(t *testing.T, pool *pgxpool.Pool, title string, startsAt *time.Time, summary *string, state string) workshops.Workshop {
	t.Helper()
	store := workshops.NewStore(pool)
	ctx := context.Background()
	w, err := store.Create(ctx, workshops.CreateInput{Title: title, StartsAt: startsAt})
	if err != nil {
		t.Fatalf("seed %q: %v", title, err)
	}
	cleanupPublicWorkshop(t, pool, w.ID)
	step := func(status string) {
		updated, uerr := store.Update(ctx, w.ID, workshops.UpdateInput{Title: title, Summary: summary, StartsAt: startsAt, Status: status})
		if uerr != nil {
			t.Fatalf("transition %q to %s: %v", title, status, uerr)
		}
		w = updated
	}
	switch state {
	case "draft":
	case "published":
		step(workshops.StatusPublished)
	case "canceled":
		step(workshops.StatusPublished)
		step(workshops.StatusCanceled)
	case "canceled-unpublished":
		step(workshops.StatusCanceled)
	}
	return w
}

func icsUID(w workshops.Workshop) string { return fmt.Sprintf("UID:event-%d@www.example.test", w.ID) }

func TestEventsICS_ContentTypeAndCacheControl(t *testing.T) {
	pool := interestsTestPool(t)
	resp, body := getEventsICS(t, pool)
	if got := resp.Header.Get("Content-Type"); got != "text/calendar; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "max-age") {
		t.Errorf("Cache-Control = %q, want a max-age", got)
	}
	if !strings.HasPrefix(body, "BEGIN:VCALENDAR\r\n") {
		t.Errorf("body does not start with BEGIN:VCALENDAR: %.60q", body)
	}
}

func TestEventsICS_ExcludesDraftsPastAndUnpublished(t *testing.T) {
	pool := interestsTestPool(t)
	future := time.Now().Add(72 * time.Hour)
	past := time.Now().Add(-72 * time.Hour)
	pub := seedICSWorkshop(t, pool, uniquePublicWorkshopTitle(t)+" ics upcoming", &future, nil, "published")
	draft := seedICSWorkshop(t, pool, uniquePublicWorkshopTitle(t)+" ics draft", &future, nil, "draft")
	old := seedICSWorkshop(t, pool, uniquePublicWorkshopTitle(t)+" ics past", &past, nil, "published")
	never := seedICSWorkshop(t, pool, uniquePublicWorkshopTitle(t)+" ics never published", &future, nil, "canceled-unpublished")

	_, body := getEventsICS(t, pool)
	if !strings.Contains(body, icsUID(pub)) || !strings.Contains(body, "SUMMARY:"+pub.Title+"\r\n") {
		t.Errorf("published upcoming %q missing", pub.Title)
	}
	for _, w := range []workshops.Workshop{draft, old, never} {
		if strings.Contains(body, icsUID(w)) || strings.Contains(body, w.Title) {
			t.Errorf("%q must not appear in the feed", w.Title)
		}
	}
}

func TestEventsICS_CancelledUpcomingIsListedAsCancelled(t *testing.T) {
	pool := interestsTestPool(t)
	future := time.Now().Add(72 * time.Hour)
	c := seedICSWorkshop(t, pool, uniquePublicWorkshopTitle(t)+" ics canceled", &future, nil, "canceled")
	_, body := getEventsICS(t, pool)
	i := strings.Index(body, icsUID(c))
	if i < 0 {
		t.Fatalf("canceled upcoming %q missing", c.Title)
	}
	vevent := body[i:]
	vevent = vevent[:strings.Index(vevent, "END:VEVENT")]
	if !strings.Contains(vevent, "STATUS:CANCELLED\r\n") {
		t.Errorf("VEVENT lacks STATUS:CANCELLED:\n%s", vevent)
	}
}

func TestEventsICS_EventURLAndDescription(t *testing.T) {
	pool := interestsTestPool(t)
	future := time.Now().Add(72 * time.Hour)
	summary := "Bring a laptop, solder; and patience"
	w := seedICSWorkshop(t, pool, uniquePublicWorkshopTitle(t)+" ics url", &future, &summary, "published")
	_, body := getEventsICS(t, pool)
	eventURL := icsTestBase + "/events/" + w.Slug
	if !strings.Contains(body, "URL:"+eventURL+"\r\n") {
		t.Errorf("missing URL line for %s", eventURL)
	}
	bs := string(rune(92))
	wantDesc := "DESCRIPTION:Bring a laptop" + bs + ", solder" + bs + "; and patience" + bs + "n" + bs + "n" + eventURL + "\r\n"
	if !strings.Contains(body, wantDesc) {
		t.Errorf("missing %q", wantDesc)
	}
}

func TestEventsICS_SkipsUpcomingWithoutStart(t *testing.T) {
	pool := interestsTestPool(t)
	w := seedICSWorkshop(t, pool, uniquePublicWorkshopTitle(t)+" ics no start", nil, nil, "published")
	_, body := getEventsICS(t, pool)
	if strings.Contains(body, icsUID(w)) {
		t.Errorf("start-less %q must not produce a VEVENT", w.Title)
	}
}
