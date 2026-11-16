package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// walkV1 pages through path with limit=1 and returns every item, failing
// if the walk does not end.
func walkV1[T any](t *testing.T, e *v1EventsEnv, path, secret string) []T {
	t.Helper()
	var out []T
	cursor := ""
	for range 10000 {
		rec := e.call(t, http.MethodGet, path+"?limit=50"+cursor, secret, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body)
		}
		page := v1Decode[api.List[T]](t, rec)
		out = append(out, page.Items...)
		if page.NextCursor == nil {
			return out
		}
		cursor = "&cursor=" + url.QueryEscape(*page.NextCursor)
	}
	t.Fatalf("%s: walk did not end", path)
	return nil
}

func TestV1Lookups_VenuesEventTypesInterests(t *testing.T) {
	e := newV1EventsEnv(t)
	ctx := context.Background()
	offVenue := seedVenue(t, e.pool, fmt.Sprintf("Zz v1 off %d", testdb.Unique()), nil, nil)
	disableVenue(t, e.pool, offVenue)
	offType := seedEventTypeRow(t, e.pool)
	disableEventType(t, e.pool, offType.ID)
	inStore := interests.NewStore(e.pool)
	offInterest, err := inStore.Create(ctx, testInterestSlug(t, e.pool), "Zz v1 off interest", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := inStore.Deactivate(ctx, offInterest.ID); err != nil {
		t.Fatal(err)
	}

	vs := walkV1[venueV1](t, e, "/api/v1/venues", e.venuesRead)
	if !slices.IsSortedFunc(vs, func(a, b venueV1) int { return int(a.ID - b.ID) }) {
		t.Errorf("venues not sorted by id")
	}
	found := map[int64]venueV1{}
	for _, v := range vs {
		found[v.ID] = v
	}
	if v, ok := found[e.venueID]; !ok || !v.Enabled || v.Note == nil || *v.Note != "Ring the bell" {
		t.Errorf("own venue %+v %v", v, ok)
	}
	if v, ok := found[offVenue]; !ok || v.Enabled {
		t.Errorf("disabled venue %+v %v", v, ok)
	}

	ets := walkV1[eventTypeV1](t, e, "/api/v1/event-types", e.read)
	types := map[int64]eventTypeV1{}
	for _, et := range ets {
		types[et.ID] = et
	}
	if et, ok := types[e.typeID]; !ok || !et.Enabled || et.Slug != e.typeSlug {
		t.Errorf("own type %+v %v", et, ok)
	}
	if et, ok := types[offType.ID]; !ok || et.Enabled {
		t.Errorf("disabled type %+v %v", et, ok)
	}

	ins := walkV1[interestV1](t, e, "/api/v1/interests", e.read)
	byID := map[int64]interestV1{}
	for _, i := range ins {
		byID[i.ID] = i
	}
	if i, ok := byID[e.interestID]; !ok || !i.Active {
		t.Errorf("own interest %+v %v", i, ok)
	}
	if i, ok := byID[offInterest.ID]; !ok || i.Active {
		t.Errorf("inactive interest %+v %v", i, ok)
	}

	// limit=1 pages walk the same ids in order.
	var ids []int64
	cursor := ""
	for range len(vs) + 1 {
		rec := e.call(t, http.MethodGet, "/api/v1/venues?limit=1"+cursor, e.venuesRead, "")
		page := v1Decode[api.List[venueV1]](t, rec)
		for _, v := range page.Items {
			ids = append(ids, v.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = "&cursor=" + url.QueryEscape(*page.NextCursor)
	}
	var want []int64
	for _, v := range vs {
		want = append(want, v.ID)
	}
	if !slices.Equal(ids, want) {
		t.Errorf("limit=1 walk %v, want %v", ids, want)
	}
}
