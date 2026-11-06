package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// Tests for GET /admin/outreach-timeline (#0646). Other tests share the
// database, so each finds its own workshop by ID and never counts events.

type timelineEventJSON struct {
	Workshop struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	} `json:"workshop"`
	Outreach *struct {
		ID int64 `json:"id"`
	} `json:"outreach"`
	Channels []map[string]any `json:"channels"`
	Waves    []struct {
		Name        string  `json:"name"`
		SuggestedAt *string `json:"suggested_at"`
	} `json:"waves"`
	Posts []struct {
		ID          int64   `json:"id"`
		Channel     string  `json:"channel"`
		Wave        string  `json:"wave"`
		Status      string  `json:"status"`
		ScheduledAt *string `json:"scheduled_at"`
		PostedAt    *string `json:"posted_at"`
	} `json:"posts"`
	OffChannels []struct {
		Name string `json:"name"`
	} `json:"off_channels"`
}

type timelineJSON struct {
	Now    string              `json:"now"`
	Events []timelineEventJSON `json:"events"`
}

var timelineNow = time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)

func (e *outreachEnv) timeline(t *testing.T) (timelineJSON, []byte) {
	t.Helper()
	e.h.now = func() time.Time { return timelineNow }
	code, b := e.do(t, http.MethodGet, "/admin/outreach-timeline", "")
	if code != http.StatusOK {
		t.Fatalf("timeline status = %d (%s)", code, b)
	}
	return decodeOutreach[timelineJSON](t, b), b
}

func findTimelineEvent(tl timelineJSON, id int64) *timelineEventJSON {
	for i := range tl.Events {
		if tl.Events[i].Workshop.ID == id {
			return &tl.Events[i]
		}
	}
	return nil
}

func moveWorkshop(t *testing.T, e *outreachEnv, w workshops.Workshop, start time.Time) {
	t.Helper()
	if _, err := e.ws.Update(context.Background(), w.ID, workshops.UpdateInput{Title: w.Title, StartsAt: &start, VenueID: w.VenueID, Status: w.Status}); err != nil {
		t.Fatalf("move workshop: %v", err)
	}
}

func TestAdminOutreachTimeline_UpcomingPublishedOnly(t *testing.T) {
	e := newOutreachEnv(t, true)
	in := seedOutreachWorkshop(t, e.pool, e.ws, "published") // 2026-12-05, exactly 30 days out
	draft := seedOutreachWorkshop(t, e.pool, e.ws, "draft")
	canceled := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	if _, err := e.ws.Update(context.Background(), canceled.ID, workshops.UpdateInput{Title: canceled.Title, StartsAt: canceled.StartsAt, VenueID: canceled.VenueID, Status: "canceled"}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	past := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	moveWorkshop(t, e, past, time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC))
	tl, _ := e.timeline(t)
	if findTimelineEvent(tl, in.ID) == nil {
		t.Error("published event 30 days out is missing")
	}
	for name, id := range map[string]int64{"draft": draft.ID, "canceled": canceled.ID, "past": past.ID} {
		if findTimelineEvent(tl, id) != nil {
			t.Errorf("%s event is on the timeline", name)
		}
	}
	if tl.Now != timelineNow.Format(time.RFC3339) {
		t.Errorf("now = %q", tl.Now)
	}
}

func TestAdminOutreachTimeline_ThirtyDayWindow(t *testing.T) {
	e := newOutreachEnv(t, true)
	far := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	moveWorkshop(t, e, far, timelineNow.Add(timelineWindow+time.Hour))
	tl, _ := e.timeline(t)
	if findTimelineEvent(tl, far.ID) != nil {
		t.Error("an event more than 30 days out is on the timeline")
	}
}

func TestAdminOutreachTimeline_NotStarted(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	tl, raw := e.timeline(t)
	ev := findTimelineEvent(tl, w.ID)
	if ev == nil {
		t.Fatal("event missing")
	}
	if ev.Outreach != nil || len(ev.Posts) != 0 || len(ev.Channels) == 0 || len(ev.Waves) != 3 {
		t.Fatalf("event = %+v", ev)
	}
	if !strings.Contains(string(raw), `"posts":[]`) {
		t.Error(`posts is not encoded as []`)
	}
	rw, _ := outreach.WaveByName("reminder")
	want := outreach.SuggestedAt(rw, w.StartsAt, outreach.DefaultPostHour).UTC().Format(time.RFC3339)
	for _, wv := range ev.Waves {
		switch wv.Name {
		case "announce":
			if wv.SuggestedAt != nil {
				t.Errorf("announce suggested_at = %v", *wv.SuggestedAt)
			}
		case "reminder":
			if wv.SuggestedAt == nil || *wv.SuggestedAt != want {
				t.Errorf("reminder suggested_at = %v, want %s", wv.SuggestedAt, want)
			}
		}
	}
}

func TestAdminOutreachTimeline_PostsCarryStatusAndTimes(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	p := e.start(t, w)
	a := e.createPost(t, p.Outreach.ID, "bluesky", "announce")
	r := e.createPost(t, p.Outreach.ID, "mastodon", "reminder")
	posted := time.Date(2026, 11, 4, 17, 0, 0, 0, time.UTC)
	sched := time.Date(2026, 11, 28, 18, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE outreach_posts SET status = 'posted', posted_at = $2 WHERE id = $1`, a.ID, posted); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE outreach_posts SET status = 'scheduled', scheduled_at = $2 WHERE id = $1`, r.ID, sched); err != nil {
		t.Fatal(err)
	}
	tl, _ := e.timeline(t)
	ev := findTimelineEvent(tl, w.ID)
	if ev == nil || ev.Outreach == nil || ev.Outreach.ID != p.Outreach.ID {
		t.Fatalf("event = %+v", ev)
	}
	got := map[int64]string{}
	for _, q := range ev.Posts {
		switch q.ID {
		case a.ID:
			if q.PostedAt == nil || *q.PostedAt != posted.Format(time.RFC3339) {
				t.Errorf("posted_at = %v", q.PostedAt)
			}
		case r.ID:
			if q.ScheduledAt == nil || *q.ScheduledAt != sched.Format(time.RFC3339) {
				t.Errorf("scheduled_at = %v", q.ScheduledAt)
			}
		}
		got[q.ID] = q.Status
	}
	if got[a.ID] != "posted" || got[r.ID] != "scheduled" {
		t.Errorf("statuses = %v", got)
	}
}

func TestAdminOutreachTimeline_SwitchedOffDraftOmitted(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	p := e.start(t, w)
	d := e.createPost(t, p.Outreach.ID, "mastodon", "announce")
	e.h.SetEnabledChannels(func() []string { return []string{"bluesky"} })
	tl, _ := e.timeline(t)
	ev := findTimelineEvent(tl, w.ID)
	if ev == nil {
		t.Fatal("event missing")
	}
	for _, q := range ev.Posts {
		if q.ID == d.ID {
			t.Error("switched-off draft is on the timeline")
		}
	}
	off := false
	for _, c := range ev.OffChannels {
		off = off || c.Name == "mastodon"
	}
	if !off {
		t.Errorf("off_channels = %+v", ev.OffChannels)
	}
}

func TestAdminOutreachTimeline_RequiresAdmin(t *testing.T) {
	e := newOutreachEnv(t, true)
	for token, want := range map[string]int{"": http.StatusUnauthorized, "outreach-user-token": http.StatusForbidden} {
		resp := doJSON(t, e.srv.Client(), http.MethodGet, e.srv.URL+"/admin/outreach-timeline", token, "")
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("token=%q status = %d, want %d", token, resp.StatusCode, want)
		}
	}
}
