package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// publicWorkshopsMux wires the real public workshop read routes — no
// session/admin guard, mirrors publicInterestsH's own wiring in
// cmd/opencircuit/main.go.
func publicWorkshopsMux(pool *pgxpool.Pool) http.Handler {
	store := workshops.NewStore(pool)
	interestsStore := interests.NewStore(pool)
	h := NewPublicWorkshopsHandler(store, interestsStore)
	mux := http.NewServeMux()
	mux.Handle("GET /api/workshops", http.HandlerFunc(h.List))
	mux.Handle("GET /api/workshops/{slug}", http.HandlerFunc(h.GetBySlug))
	return mux
}

func uniquePublicWorkshopTitle(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-subtest-pubworkshop-%d", testdb.Unique())
}

func cleanupPublicWorkshop(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, id)
	})
}

type decodedPublicWorkshop struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type decodedPublicWorkshopsList struct {
	Upcoming []decodedPublicWorkshop `json:"upcoming"`
	Past     []decodedPublicWorkshop `json:"past"`
}

// TestPublicWorkshops_List_SplitsUpcomingPastExcludesDraft is #0051's own
// acceptance criterion, widened by #0135: GET /api/workshops returns
// published-or-canceled workshops split into upcoming and past, and never
// surfaces a draft.
func TestPublicWorkshops_List_SplitsUpcomingPastExcludesDraft(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(publicWorkshopsMux(pool))
	defer srv.Close()

	store := workshops.NewStore(pool)
	ctx := context.Background()
	future := time.Now().Add(72 * time.Hour)
	past := time.Now().Add(-72 * time.Hour)

	mk := func(title string, startsAt *time.Time, status string) workshops.Workshop {
		w, err := store.Create(ctx, workshops.CreateInput{Title: title, StartsAt: startsAt})
		if err != nil {
			t.Fatalf("seed %q: %v", title, err)
		}
		cleanupPublicWorkshop(t, pool, w.ID)
		if status != workshops.StatusDraft {
			updated, uerr := store.Update(ctx, w.ID, workshops.UpdateInput{Title: w.Title, StartsAt: startsAt, Status: status})
			if uerr != nil {
				t.Fatalf("transition %q to %s: %v", title, status, uerr)
			}
			return updated
		}
		return w
	}

	upcoming := mk(uniquePublicWorkshopTitle(t)+" upcoming", &future, workshops.StatusPublished)
	pastW := mk(uniquePublicWorkshopTitle(t)+" past", &past, workshops.StatusPublished)
	draftW := mk(uniquePublicWorkshopTitle(t)+" draft", &future, workshops.StatusDraft)
	// #0135: a canceled workshop belongs in the index now, split by
	// starts_at exactly like a published one -- one canceled-and-upcoming
	// (the case that matters: a reader checking on a session they planned to
	// attend) and one canceled-and-past (harmless either way, honest
	// history), so both halves of the split are covered.
	canceledUpcomingW := mk(uniquePublicWorkshopTitle(t)+" canceled upcoming", &future, workshops.StatusCanceled)
	canceledPastW := mk(uniquePublicWorkshopTitle(t)+" canceled past", &past, workshops.StatusCanceled)

	resp, err := http.Get(srv.URL + "/api/workshops")
	if err != nil {
		t.Fatalf("GET /api/workshops: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	var list decodedPublicWorkshopsList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}

	upcomingByslug := map[string]decodedPublicWorkshop{}
	for _, w := range list.Upcoming {
		upcomingByslug[w.Slug] = w
	}
	pastByslug := map[string]decodedPublicWorkshop{}
	for _, w := range list.Past {
		pastByslug[w.Slug] = w
	}

	if _, ok := upcomingByslug[upcoming.Slug]; !ok {
		t.Errorf("upcoming workshop %q missing from upcoming list", upcoming.Slug)
	}
	if _, ok := pastByslug[pastW.Slug]; !ok {
		t.Errorf("past workshop %q missing from past list", pastW.Slug)
	}
	if _, ok := upcomingByslug[draftW.Slug]; ok {
		t.Errorf("draft workshop %q leaked into upcoming list", draftW.Slug)
	}
	if _, ok := pastByslug[draftW.Slug]; ok {
		t.Errorf("draft workshop %q leaked into past list", draftW.Slug)
	}

	// The end-to-end proof #0135 asks for: a canceled workshop actually
	// appears in the index payload, with status="canceled" so the frontend
	// badge (isCanceled/workshopBadgeLabel in web/src/lib/workshops.ts) can
	// render it, placed in the correct half of the split by starts_at.
	if got, ok := upcomingByslug[canceledUpcomingW.Slug]; !ok {
		t.Errorf("canceled-but-upcoming workshop %q missing from upcoming list", canceledUpcomingW.Slug)
	} else if got.Status != workshops.StatusCanceled {
		t.Errorf("canceled-but-upcoming workshop %q: Status = %q, want %q", canceledUpcomingW.Slug, got.Status, workshops.StatusCanceled)
	}
	if _, ok := pastByslug[canceledUpcomingW.Slug]; ok {
		t.Errorf("canceled-but-upcoming workshop %q incorrectly in past list", canceledUpcomingW.Slug)
	}
	if got, ok := pastByslug[canceledPastW.Slug]; !ok {
		t.Errorf("canceled-and-past workshop %q missing from past list", canceledPastW.Slug)
	} else if got.Status != workshops.StatusCanceled {
		t.Errorf("canceled-and-past workshop %q: Status = %q, want %q", canceledPastW.Slug, got.Status, workshops.StatusCanceled)
	}
	if _, ok := upcomingByslug[canceledPastW.Slug]; ok {
		t.Errorf("canceled-and-past workshop %q incorrectly in upcoming list", canceledPastW.Slug)
	}
}

// TestPublicWorkshops_GetBySlug_VisibilityByStatus covers #0051's own
// acceptance criterion ("drafts 404 for the public") plus the Notes'
// requirement that a canceled workshop stays visible with its status field
// intact, for each of the three statuses.
func TestPublicWorkshops_GetBySlug_VisibilityByStatus(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(publicWorkshopsMux(pool))
	defer srv.Close()

	store := workshops.NewStore(pool)
	ctx := context.Background()

	mk := func(status string) workshops.Workshop {
		w, err := store.Create(ctx, workshops.CreateInput{Title: uniquePublicWorkshopTitle(t)})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		cleanupPublicWorkshop(t, pool, w.ID)
		if status == workshops.StatusDraft {
			return w
		}
		updated, err := store.Update(ctx, w.ID, workshops.UpdateInput{Title: w.Title, Status: status})
		if err != nil {
			t.Fatalf("transition to %s: %v", status, err)
		}
		return updated
	}

	cases := []struct {
		status     string
		wantStatus int
	}{
		{workshops.StatusDraft, http.StatusNotFound},
		{workshops.StatusPublished, http.StatusOK},
		{workshops.StatusCanceled, http.StatusOK},
	}
	for _, c := range cases {
		w := mk(c.status)
		resp, err := http.Get(srv.URL + "/api/workshops/" + w.Slug)
		if err != nil {
			t.Fatalf("GET /api/workshops/%s: %v", w.Slug, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != c.wantStatus {
			t.Errorf("status=%s: GET /api/workshops/%s = %d, want %d (body=%s)", c.status, w.Slug, resp.StatusCode, c.wantStatus, body)
			continue
		}
		if c.wantStatus == http.StatusOK {
			var got decodedPublicWorkshop
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode: %v (body=%s)", err, body)
			}
			if got.Status != c.status {
				t.Errorf("status=%s: response Status field = %q, want %q", c.status, got.Status, c.status)
			}
			if got.Slug != w.Slug {
				t.Errorf("status=%s: response Slug = %q, want %q", c.status, got.Slug, w.Slug)
			}
		}
	}
}

func TestPublicWorkshops_GetBySlug_MissingSlugIs404(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(publicWorkshopsMux(pool))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/workshops/zz-does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestPublicWorkshops_GetBySlug_IncludesInterestTags proves the tag
// resolution path #0054 needs (interest slug/name pairs, not bare ids).
func TestPublicWorkshops_GetBySlug_IncludesInterestTags(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(publicWorkshopsMux(pool))
	defer srv.Close()

	interestsStore := interests.NewStore(pool)
	interestSlug := fmt.Sprintf("zz-test-pubworkshop-interest-%d", testdb.Unique())
	it, err := interestsStore.Create(context.Background(), interestSlug, "Test Interest", nil, 0)
	if err != nil {
		t.Fatalf("seed interest: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM interests WHERE id = $1`, it.ID) })

	store := workshops.NewStore(pool)
	w, err := store.Create(context.Background(), workshops.CreateInput{
		Title:       uniquePublicWorkshopTitle(t),
		InterestIDs: []int64{it.ID},
	})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupPublicWorkshop(t, pool, w.ID)
	if _, err := store.Update(context.Background(), w.ID, workshops.UpdateInput{
		Title: w.Title, InterestIDs: []int64{it.ID}, Status: workshops.StatusPublished,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/workshops/" + w.Slug)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	var got struct {
		Interests []struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"interests"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if len(got.Interests) != 1 || got.Interests[0].Slug != interestSlug {
		t.Errorf("Interests = %+v, want one entry with slug %q", got.Interests, interestSlug)
	}
}
