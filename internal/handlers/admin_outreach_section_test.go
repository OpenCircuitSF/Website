package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// Tests for the section routes (#0640). Nothing here posts anywhere: the
// handler only writes rows, and fakePosting stands in for the publisher.

type sectionView struct {
	Posts     []postingPostView  `json:"posts"`
	LeftAlone []sectionLeftAlone `json:"left_alone"`
}

func sectionPath(workshopID int64, action string) string {
	return fmt.Sprintf("/admin/workshops/%d/outreach/%s", workshopID, action)
}

// sectionWorkshop seeds a published workshop with outreach and one Reminder
// post per channel named; each in prepare is also prepared.
func (e *outreachEnv) sectionWorkshop(t *testing.T, channels []string, prepare map[string]bool) (workshops.Workshop, map[string]int64) {
	t.Helper()
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	ids := map[string]int64{}
	for _, ch := range channels {
		p := e.createPost(t, o.ID, ch, "reminder")
		ids[ch] = p.ID
		if prepare[ch] {
			if code, b := e.do(t, http.MethodPost, postPath(p.ID, "prepare"), ""); code != http.StatusOK {
				t.Fatalf("prepare %s = %d (%s)", ch, code, b)
			}
		}
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM outbound_queue WHERE kind = 'outreach_post'`)
	})
	return w, ids
}

func (e *outreachEnv) postStatus(t *testing.T, id int64) (string, *time.Time) {
	t.Helper()
	var status string
	var at *time.Time
	if err := e.pool.QueryRow(context.Background(), `SELECT status, scheduled_at FROM outreach_posts WHERE id = $1`, id).Scan(&status, &at); err != nil {
		t.Fatal(err)
	}
	return status, at
}

func sectionAuditCount(t *testing.T, e *outreachEnv, action string, postID int64) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2 AND metadata->>'section' = 'true'`, action, postID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func scheduleBody(wave string, at time.Time) string {
	return fmt.Sprintf(`{"wave":%q,"scheduled_at":%q}`, wave, at.UTC().Format(time.RFC3339))
}

func TestAdminOutreachPosting_ScheduleSection(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true, "mastodon": true})
	w, ids := e.sectionWorkshop(t, []string{"mastodon", "bluesky"}, map[string]bool{"bluesky": true, "mastodon": true})
	at := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	code, b := e.do(t, http.MethodPost, sectionPath(w.ID, "schedule"), scheduleBody("reminder", at))
	if code != http.StatusOK {
		t.Fatalf("schedule section = %d (%s)", code, b)
	}
	if !strings.Contains(string(b), `"left_alone":[]`) {
		t.Errorf("left_alone should be an empty list: %s", b)
	}
	got := decodeOutreach[sectionView](t, b)
	if len(got.Posts) != 2 || got.Posts[0].ID != ids["bluesky"] || got.Posts[1].ID != ids["mastodon"] {
		t.Fatalf("posts = %+v, want Bluesky then Mastodon", got.Posts)
	}
	want := at.UTC().Format(time.RFC3339)
	for _, p := range got.Posts {
		if p.Status != "scheduled" || p.ScheduledAt == nil || *p.ScheduledAt != want {
			t.Errorf("post = %+v", p)
		}
		if n := e.queueRowCount(t, p.ID); n != 0 {
			t.Errorf("post %d: scheduling enqueued %d rows", p.ID, n)
		}
		if n := sectionAuditCount(t, e, audit.ActionOutreachPostScheduled, p.ID); n != 1 {
			t.Errorf("post %d: section scheduled audit rows = %d", p.ID, n)
		}
	}

	later := at.Add(time.Hour)
	code, b = e.do(t, http.MethodPost, sectionPath(w.ID, "schedule"), scheduleBody("reminder", later))
	if code != http.StatusOK {
		t.Fatalf("reschedule section = %d (%s)", code, b)
	}
	for _, id := range ids {
		if _, at := e.postStatus(t, id); at == nil || !at.Equal(later) {
			t.Errorf("post %d not rescheduled: %v", id, at)
		}
	}
}

func TestAdminOutreachPosting_ScheduleSectionLeavesPostedAlone(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true, "mastodon": true})
	w, ids := e.sectionWorkshop(t, []string{"bluesky", "mastodon"}, map[string]bool{"bluesky": true, "mastodon": true})
	if _, err := e.pool.Exec(context.Background(), `UPDATE outreach_posts SET status = 'posted', posted_at = now() WHERE id = $1`, ids["bluesky"]); err != nil {
		t.Fatal(err)
	}
	code, b := e.do(t, http.MethodPost, sectionPath(w.ID, "schedule"), scheduleBody("reminder", time.Now().Add(3*time.Hour)))
	if code != http.StatusOK {
		t.Fatalf("schedule section = %d (%s)", code, b)
	}
	got := decodeOutreach[sectionView](t, b)
	if len(got.Posts) != 1 || got.Posts[0].ID != ids["mastodon"] || got.Posts[0].Status != "scheduled" {
		t.Errorf("posts = %+v", got.Posts)
	}
	if len(got.LeftAlone) != 1 || got.LeftAlone[0] != (sectionLeftAlone{Channel: "bluesky", ChannelLabel: "Bluesky", Reason: "posted"}) {
		t.Errorf("left_alone = %+v", got.LeftAlone)
	}
}

func TestAdminOutreachPosting_ScheduleSectionAllOrNothing(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true, "mastodon": true})
	w, ids := e.sectionWorkshop(t, []string{"bluesky", "mastodon"}, map[string]bool{"bluesky": true})
	code, b := e.do(t, http.MethodPost, sectionPath(w.ID, "schedule"), scheduleBody("reminder", time.Now().Add(3*time.Hour)))
	if code != http.StatusConflict || !strings.Contains(string(b), `"Mastodon`) {
		t.Fatalf("schedule section = %d (%s), want 409 naming Mastodon", code, b)
	}
	if status, at := e.postStatus(t, ids["bluesky"]); status != "draft" || at != nil {
		t.Errorf("Bluesky moved despite the refusal: %s %v", status, at)
	}
}

func TestAdminOutreachPosting_ScheduleSectionRefusals(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true, "mastodon": true})
	w, _ := e.sectionWorkshop(t, []string{"bluesky"}, map[string]bool{"bluesky": true})
	soon := time.Now().Add(3 * time.Hour)

	bare := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	draft := seedOutreachWorkshop(t, e.pool, e.ws, "draft")
	e.start(t, draft)
	handOnly, _ := e.sectionWorkshop(t, []string{"instagram"}, nil)

	for _, tc := range []struct {
		name     string
		workshop int64
		body     string
		code     int
		msg      string
	}{
		{"bad json", w.ID, `nope`, http.StatusBadRequest, ""},
		{"missing time", w.ID, `{"wave":"reminder"}`, http.StatusBadRequest, "scheduled_at"},
		{"past", w.ID, scheduleBody("reminder", time.Now().Add(-time.Hour)), http.StatusBadRequest, "scheduled_at"},
		{"unknown wave", w.ID, scheduleBody("someday", soon), http.StatusBadRequest, "wave must be one of"},
		{"no outreach", bare.ID, scheduleBody("reminder", soon), http.StatusConflict, "start outreach first"},
		{"unpublished", draft.ID, scheduleBody("reminder", soon), http.StatusConflict, "not published"},
		{"hand posts only", handOnly.ID, scheduleBody("reminder", soon), http.StatusConflict, "nothing in this section can be scheduled"},
	} {
		code, b := e.do(t, http.MethodPost, sectionPath(tc.workshop, "schedule"), tc.body)
		if code != tc.code || !strings.Contains(string(b), tc.msg) {
			t.Errorf("%s: %d (%s), want %d containing %q", tc.name, code, b, tc.code, tc.msg)
		}
	}

	resp := doJSON(t, e.srv.Client(), http.MethodPost, e.srv.URL+sectionPath(w.ID, "schedule"), "", scheduleBody("reminder", soon))
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated = %d, want 401", resp.StatusCode)
	}
}

func TestAdminOutreachPosting_UnscheduleSection(t *testing.T) {
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true, "mastodon": true})
	w, ids := e.sectionWorkshop(t, []string{"bluesky", "mastodon"}, map[string]bool{"bluesky": true, "mastodon": true})
	at := time.Now().Add(3 * time.Hour)
	if code, b := e.do(t, http.MethodPost, sectionPath(w.ID, "schedule"), scheduleBody("reminder", at)); code != http.StatusOK {
		t.Fatalf("schedule section = %d (%s)", code, b)
	}
	code, b := e.do(t, http.MethodPost, sectionPath(w.ID, "unschedule"), `{"wave":"reminder"}`)
	if code != http.StatusOK {
		t.Fatalf("unschedule section = %d (%s)", code, b)
	}
	got := decodeOutreach[sectionView](t, b)
	if len(got.Posts) != 2 {
		t.Fatalf("posts = %+v", got.Posts)
	}
	for _, p := range got.Posts {
		if p.Status != "draft" || p.ScheduledAt != nil {
			t.Errorf("post = %+v", p)
		}
		if n := sectionAuditCount(t, e, audit.ActionOutreachPostUnscheduled, p.ID); n != 1 {
			t.Errorf("post %d: section unscheduled audit rows = %d", p.ID, n)
		}
	}
	if code, b := e.do(t, http.MethodPost, sectionPath(w.ID, "unschedule"), `{"wave":"reminder"}`); code != http.StatusConflict || !strings.Contains(string(b), "nothing in this section is scheduled") {
		t.Errorf("second unschedule = %d (%s), want 409", code, b)
	}

	// One posted, one scheduled: the scheduled one goes back, the posted one is named.
	if code, b := e.do(t, http.MethodPost, sectionPath(w.ID, "schedule"), scheduleBody("reminder", at)); code != http.StatusOK {
		t.Fatalf("schedule section = %d (%s)", code, b)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE outreach_posts SET status = 'posted', scheduled_at = NULL, posted_at = now() WHERE id = $1`, ids["bluesky"]); err != nil {
		t.Fatal(err)
	}
	code, b = e.do(t, http.MethodPost, sectionPath(w.ID, "unschedule"), `{"wave":"reminder"}`)
	if code != http.StatusOK {
		t.Fatalf("unschedule section = %d (%s)", code, b)
	}
	got = decodeOutreach[sectionView](t, b)
	if len(got.Posts) != 1 || got.Posts[0].ID != ids["mastodon"] || got.Posts[0].Status != "draft" {
		t.Errorf("posts = %+v", got.Posts)
	}
	if len(got.LeftAlone) != 1 || got.LeftAlone[0].Channel != "bluesky" || got.LeftAlone[0].Reason != "posted" {
		t.Errorf("left_alone = %+v", got.LeftAlone)
	}
}
