package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/luma"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// Tests for GET /admin/outreach-luma (#0648). The shared database holds many
// seeded workshops, so each test moves its own to a 2031 date of its own and
// asserts by workshop id and leftover title, never by counts.

type lumaJSON struct {
	Status     string  `json:"status"`
	ReadAt     *string `json:"read_at"`
	LastGoodAt *string `json:"last_good_at"`
	Events     []struct {
		WorkshopID int64       `json:"workshop_id"`
		State      string      `json:"state"`
		LumaURL    *string     `json:"luma_url"`
		LumaTitle  *string     `json:"luma_title"`
		Diffs      []luma.Diff `json:"diffs"`
	} `json:"events"`
	Leftovers []struct {
		Title    string  `json:"title"`
		StartsAt string  `json:"starts_at"`
		LumaURL  *string `json:"luma_url"`
	} `json:"leftovers"`
}

func icsEvent(uid, title, start, url string) string {
	return "BEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTART:" + start + "\r\nSUMMARY:" + title +
		"\r\nDESCRIPTION:Get up-to-date information at: " + url + "\r\nEND:VEVENT\r\n"
}

func lumaFeedServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (e *outreachEnv) lumaRead(t *testing.T, feed *httptest.Server) (lumaJSON, []byte) {
	t.Helper()
	if feed != nil {
		e.h.SetLuma(luma.NewChecker(func() string { return "cal-test" }, feed.URL))
	}
	e.h.now = func() time.Time { return timelineNow }
	code, b := e.do(t, http.MethodGet, "/admin/outreach-luma", "")
	if code != http.StatusOK {
		t.Fatalf("luma status = %d (%s)", code, b)
	}
	return decodeOutreach[lumaJSON](t, b), b
}

func TestAdminOutreachLuma_Off(t *testing.T) {
	e := newOutreachEnv(t, true)
	got, raw := e.lumaRead(t, nil)
	if got.Status != "off" || len(got.Events) != 0 || len(got.Leftovers) != 0 {
		t.Fatalf("view = %s", raw)
	}
	if want := `"events":[]`; !contains(raw, want) || !contains(raw, `"leftovers":[]`) {
		t.Errorf("empty lists not encoded as []: %s", raw)
	}
}

func contains(b []byte, s string) bool { return strings.Contains(string(b), s) }

func TestAdminOutreachLuma_DiffersAndLeftover(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	moveWorkshop(t, e, w, time.Date(2031, 3, 5, 2, 30, 0, 0, time.UTC))
	stray := fmt.Sprintf("zz luma stray %d", testdb.Unique())
	feed := lumaFeedServer(t, http.StatusOK, "BEGIN:VCALENDAR\r\n"+
		icsEvent("u1", w.Title, "20310305T020000Z", "https://luma.com/abc123")+
		icsEvent("u2", stray, "20310310T030000Z", "https://luma.com/stray1")+
		"END:VCALENDAR\r\n")
	got, raw := e.lumaRead(t, feed)
	if got.Status != "ok" || got.ReadAt == nil || got.LastGoodAt == nil {
		t.Fatalf("view = %s", raw)
	}
	found := false
	for _, ev := range got.Events {
		if ev.WorkshopID != w.ID {
			continue
		}
		found = true
		want := luma.Diff{Field: "start", Label: "Start", Site: "6:30 PM", Luma: "6:00 PM"}
		if ev.State != "differs" || len(ev.Diffs) != 1 || ev.Diffs[0] != want || ev.LumaURL == nil || *ev.LumaURL != "https://luma.com/abc123" {
			t.Errorf("event = %+v", ev)
		}
	}
	if !found {
		t.Fatal("seeded workshop missing")
	}
	found = false
	for _, l := range got.Leftovers {
		if l.Title == stray {
			found = l.StartsAt == "2031-03-10T03:00:00Z" && l.LumaURL != nil && *l.LumaURL == "https://luma.com/stray1"
		}
	}
	if !found {
		t.Errorf("stray Luma event not in leftovers: %s", raw)
	}
}

func TestAdminOutreachLuma_NotOnLuma(t *testing.T) {
	e := newOutreachEnv(t, true)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	moveWorkshop(t, e, w, time.Date(2031, 4, 2, 2, 30, 0, 0, time.UTC))
	feed := lumaFeedServer(t, http.StatusOK, "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
	got, raw := e.lumaRead(t, feed)
	for _, ev := range got.Events {
		if ev.WorkshopID == w.ID {
			if ev.State != "not_on_luma" || ev.LumaURL != nil || ev.LumaTitle != nil || ev.Diffs == nil {
				t.Errorf("event = %+v", ev)
			}
			return
		}
	}
	t.Fatalf("seeded workshop missing: %s", raw)
}

func TestAdminOutreachLuma_Unreachable(t *testing.T) {
	e := newOutreachEnv(t, true)
	got, raw := e.lumaRead(t, lumaFeedServer(t, http.StatusServiceUnavailable, "down"))
	if got.Status != "unreachable" || len(got.Events) != 0 || got.ReadAt != nil || got.LastGoodAt != nil || !contains(raw, `"events":[]`) {
		t.Fatalf("view = %s", raw)
	}
}

func TestAdminOutreachLuma_Malformed(t *testing.T) {
	e := newOutreachEnv(t, true)
	got, raw := e.lumaRead(t, lumaFeedServer(t, http.StatusOK, "<!doctype html><p>nope</p>"))
	if got.Status != "malformed" || len(got.Events) != 0 {
		t.Fatalf("view = %s", raw)
	}
}

func TestAdminOutreachLuma_RequiresAdmin(t *testing.T) {
	e := newOutreachEnv(t, true)
	for token, want := range map[string]int{"": http.StatusUnauthorized, "outreach-user-token": http.StatusForbidden} {
		resp := doJSON(t, e.srv.Client(), http.MethodGet, e.srv.URL+"/admin/outreach-luma", token, "")
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("token=%q status = %d, want %d", token, resp.StatusCode, want)
		}
	}
}
