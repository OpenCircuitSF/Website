package outreach

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

func TestFlyerDestinationURL(t *testing.T) {
	got := FlyerDestinationURL("https://www.opencircuitsf.com/", "soldering-101", "mission-1")
	want := "https://www.opencircuitsf.com/workshops/soldering-101?utm_campaign=soldering-101&utm_content=mission-1&utm_medium=print&utm_source=flyer"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if again := FlyerDestinationURL("https://www.opencircuitsf.com", "soldering-101", "mission-1"); again != got {
		t.Error("not deterministic across a trailing slash")
	}
}

func TestFlyerDateLine(t *testing.T) {
	la := pacific
	at := func(y int, m time.Month, d, h, min int) *time.Time {
		v := time.Date(y, m, d, h, min, 0, 0, la)
		return &v
	}
	cases := []struct {
		name       string
		start, end *time.Time
		want       string
	}{
		{"no start", nil, at(2026, 11, 14, 12, 0), "Date TBA"},
		{"no end", at(2026, 11, 14, 10, 0), nil, "Sat, Nov 14 · 10:00 AM"},
		{"same day", at(2026, 11, 14, 10, 0), at(2026, 11, 14, 12, 30), "Sat, Nov 14 · 10:00 AM–12:30 PM"},
		{"cross day", at(2026, 11, 14, 22, 0), at(2026, 11, 15, 1, 0), "Sat, Nov 14 · 10:00 PM"},
		{"dst date", at(2026, 7, 4, 9, 0), at(2026, 7, 4, 11, 0), "Sat, Jul 4 · 9:00 AM–11:00 AM"},
	}
	for _, c := range cases {
		if got := FlyerDateLine(c.start, c.end); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// A UTC instant is shown in Pacific time.
	u := time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC)
	if got := FlyerDateLine(&u, nil); got != "Sat, Nov 14 · 10:00 AM" {
		t.Errorf("utc input: %q", got)
	}
}

func TestFlyerClicksByKey(t *testing.T) {
	links := []FlyerLink{{Key: "aaa"}, {Key: "bbb"}, {Key: "ccc"}}
	r := &shortlinks.CampaignRollup{ByLink: []shortlinks.LinkBucket{{Key: "aaa", Count: 3}, {Key: "zzz", Count: 9}, {Key: "bbb", Count: 2}}}
	got := FlyerClicksByKey(links, r)
	if got["aaa"] != 3 || got["bbb"] != 2 || got["ccc"] != 0 || len(got) != 3 {
		t.Errorf("got %v", got)
	}
	if len(FlyerClicksByKey(links, nil)) != 0 {
		t.Error("nil rollup should give an empty map")
	}
}

func seedFlyerFixture(t *testing.T, s *Store) (Outreach, Location, Location) {
	t.Helper()
	// Locations first: cleanups run last-in first-out, so the workshop (and
	// through it the links that RESTRICT location deletes) goes before them.
	a, b := seedLocation(t, s), seedLocation(t, s)
	w := seedWorkshop(t, s.pool)
	o, _, err := s.Create(context.Background(), w.ID, w.Slug)
	if err != nil {
		t.Fatal(err)
	}
	return o, a, b
}

func TestFlyerCRUDAndChecks(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	o, _, _ := seedFlyerFixture(t, s)

	f, err := s.CreateFlyer(ctx, FlyerInput{OutreachID: o.ID, FocalX: 0.5, FocalY: 0.5, Template: "photo-top", Tagline: "Come solder"})
	if err != nil {
		t.Fatal(err)
	}
	if f.UpdatedAt != nil || f.Photo != nil {
		t.Errorf("fresh flyer: %+v", f)
	}
	// A no-op patch keeps updated_at NULL; a real change sets it.
	same := "Come solder"
	if got, err := s.UpdateFlyer(ctx, f.ID, FlyerPatch{Tagline: &same}); err != nil || got.UpdatedAt != nil {
		t.Errorf("no-op patch: %+v %v", got, err)
	}
	photo, tpl, fx := "/hero-crt-760.webp", "split", 0.25
	got, err := s.UpdateFlyer(ctx, f.ID, FlyerPatch{Photo: &photo, Template: &tpl, FocalX: &fx})
	if err != nil || got.UpdatedAt == nil || got.Photo == nil || *got.Photo != photo || got.Template != "split" || got.FocalX != 0.25 || got.FocalY != 0.5 {
		t.Errorf("patch: %+v %v", got, err)
	}
	if got, err := s.UpdateFlyer(ctx, f.ID, FlyerPatch{ClearPhoto: true}); err != nil || got.Photo != nil {
		t.Errorf("clear photo: %+v %v", got, err)
	}
	list, err := s.ListFlyers(ctx, o.ID)
	if err != nil || len(list) != 1 || list[0].WorkshopTitle == "" {
		t.Errorf("list: %+v %v", list, err)
	}

	for name, in := range map[string]FlyerInput{
		"template": {OutreachID: o.ID, FocalX: 0.5, FocalY: 0.5, Template: "poster"},
		"focal x":  {OutreachID: o.ID, FocalX: 1.5, FocalY: 0.5, Template: "split"},
		"focal y":  {OutreachID: o.ID, FocalX: 0.5, FocalY: -0.1, Template: "split"},
		"tagline":  {OutreachID: o.ID, FocalX: 0.5, FocalY: 0.5, Template: "split", Tagline: strings.Repeat("x", 141)},
	} {
		if _, err := s.CreateFlyer(ctx, in); err == nil {
			t.Errorf("%s: CHECK did not reject", name)
		}
	}
	if err := s.DeleteFlyer(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFlyer(ctx, f.ID); !errors.Is(err, ErrFlyerNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if err := s.DeleteFlyer(ctx, f.ID); !errors.Is(err, ErrFlyerNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestFlyerSelection(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	o, a, b := seedFlyerFixture(t, s)
	f, _ := s.CreateFlyer(ctx, FlyerInput{OutreachID: o.ID, FocalX: 0.5, FocalY: 0.5, Template: "split"})

	// Order is lower(name), id. Rename so the order differs from the id order.
	zz, aa := "Zebra Board", "alpha board"
	if _, err := s.UpdateLocation(ctx, a.ID, LocationPatch{Name: &zz}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateLocation(ctx, b.ID, LocationPatch{Name: &aa}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceFlyerSelection(ctx, f.ID, []SelectionInput{{a.ID, 2}, {b.ID, 3}}); err != nil {
		t.Fatal(err)
	}
	sel, err := s.ListFlyerSelection(ctx, f.ID)
	if err != nil || len(sel) != 2 || sel[0].LocationID != b.ID || sel[0].Copies != 3 || sel[1].LocationID != a.ID || sel[1].Link != nil {
		t.Fatalf("selection: %+v %v", sel, err)
	}

	for name, in := range map[string][]SelectionInput{
		"zero copies":  {{a.ID, 0}},
		"51 copies":    {{a.ID, 51}},
		"duplicate":    {{a.ID, 1}, {a.ID, 1}},
		"unknown":      {{a.ID + b.ID + 9999999, 1}},
		"41 locations": make([]SelectionInput, 41),
	} {
		if err := s.ReplaceFlyerSelection(ctx, f.ID, in); !errors.Is(err, ErrFlyerSelectionInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// A refused replace leaves the selection alone.
	if sel, _ := s.ListFlyerSelection(ctx, f.ID); len(sel) != 2 {
		t.Errorf("selection changed by refused replace: %+v", sel)
	}

	// Inactive: refused when new, kept when already selected.
	off := false
	if _, err := s.UpdateLocation(ctx, a.ID, LocationPatch{Active: &off}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceFlyerSelection(ctx, f.ID, []SelectionInput{{a.ID, 1}, {b.ID, 1}}); err != nil {
		t.Errorf("keeping an already-selected inactive location: %v", err)
	}
	g, _ := s.CreateFlyer(ctx, FlyerInput{OutreachID: o.ID, FocalX: 0.5, FocalY: 0.5, Template: "split"})
	if err := s.ReplaceFlyerSelection(ctx, g.ID, []SelectionInput{{a.ID, 1}}); !errors.Is(err, ErrLocationInactive) {
		t.Errorf("new inactive: %v", err)
	}
	if err := s.ReplaceFlyerSelection(ctx, 999999999, nil); !errors.Is(err, ErrFlyerNotFound) {
		t.Errorf("unknown flyer: %v", err)
	}
	// Deleting a never-generated location drops it from selections.
	if err := s.DeleteLocation(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if sel, _ := s.ListFlyerSelection(ctx, f.ID); len(sel) != 1 || sel[0].LocationID != a.ID {
		t.Errorf("after location delete: %+v", sel)
	}
}

func TestInsertFlyerLinkIdempotentAndLocks(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	o, a, _ := seedFlyerFixture(t, s)

	l1, created, err := s.InsertFlyerLink(ctx, o.ID, a.ID, a.Code, "key1", "https://go.example/key1")
	if err != nil || !created || l1.Key != "key1" || l1.HasQR {
		t.Fatalf("first: %+v %v %v", l1, created, err)
	}
	loc, _ := s.GetLocation(ctx, a.ID)
	if loc.CodeLockedAt == nil {
		t.Fatal("location not locked")
	}
	lockedAt := *loc.CodeLockedAt

	l2, created, err := s.InsertFlyerLink(ctx, o.ID, a.ID, a.Code, "other-key", "https://go.example/other-key")
	if err != nil || created || l2.ID != l1.ID || l2.Key != "key1" {
		t.Fatalf("second: %+v %v %v", l2, created, err)
	}
	if loc2, _ := s.GetLocation(ctx, a.ID); !loc2.CodeLockedAt.Equal(lockedAt) {
		t.Error("second insert moved code_locked_at")
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outreach_flyer_links WHERE outreach_id = $1`, o.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("link rows = %d %v", n, err)
	}
}

func TestInsertFlyerLinkCodeChangedRollsBack(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	o, a, _ := seedFlyerFixture(t, s)
	newCode := uniqueLocCode()
	if _, err := s.UpdateLocation(ctx, a.ID, LocationPatch{Code: &newCode}); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.InsertFlyerLink(ctx, o.ID, a.ID, a.Code, "k", "https://go.example/k")
	if !errors.Is(err, ErrLocationCodeChanged) {
		t.Fatalf("err = %v", err)
	}
	var n int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM outreach_flyer_links WHERE outreach_id = $1`, o.ID).Scan(&n)
	if loc, _ := s.GetLocation(ctx, a.ID); n != 0 || loc.CodeLockedAt != nil {
		t.Errorf("rows = %d, locked = %v; want nothing written", n, loc.CodeLockedAt)
	}
	if _, _, err := s.InsertFlyerLink(ctx, o.ID, a.ID+99999999, "x", "k", "u"); !errors.Is(err, ErrLocationNotFound) {
		t.Errorf("unknown location: %v", err)
	}
}

// The FK is the backstop for DeleteLocation: with code_locked_at forced back
// to NULL the DELETE's own WHERE no longer protects the row, and only the
// RESTRICT foreign key stands between a used location and deletion.
func TestFlyerLinkForeignKeyBlocksLocationDelete(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	o, a, _ := seedFlyerFixture(t, s)
	if _, _, err := s.InsertFlyerLink(ctx, o.ID, a.ID, a.Code, "k", "https://go.example/k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE outreach_locations SET code_locked_at = NULL WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteLocation(ctx, a.ID); !errors.Is(err, ErrLocationInUse) {
		t.Errorf("err = %v, want ErrLocationInUse via 23503", err)
	}
	if _, err := s.GetLocation(ctx, a.ID); err != nil {
		t.Errorf("location vanished: %v", err)
	}
}

func TestFlyerLinkQRAndPutUp(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	o, a, _ := seedFlyerFixture(t, s)
	l, _, err := s.InsertFlyerLink(ctx, o.ID, a.ID, a.Code, "k", "https://go.example/k")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.FlyerLinkQR(ctx, l.ID); !errors.Is(err, ErrFlyerLinkNotFound) {
		t.Errorf("QR before cache: %v", err)
	}
	if err := s.SetFlyerLinkQR(ctx, l.ID, []byte("<svg>one</svg>")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFlyerLinkQR(ctx, l.ID, []byte("<svg>two</svg>")); err != nil {
		t.Fatal(err)
	}
	svg, code, err := s.FlyerLinkQR(ctx, l.ID)
	if err != nil || string(svg) != "<svg>one</svg>" || code != a.Code {
		t.Errorf("QR kept first: %q %q %v", svg, code, err)
	}
	if got, _ := s.GetFlyerLink(ctx, l.ID); !got.HasQR {
		t.Error("HasQR false after caching")
	}
	if err := s.SetFlyerLinkQR(ctx, l.ID+99999999, []byte("x")); !errors.Is(err, ErrFlyerLinkNotFound) {
		t.Errorf("unknown link: %v", err)
	}

	first, err := s.SetPutUp(ctx, l.ID, true)
	if err != nil || first.PutUpAt == nil {
		t.Fatalf("put up: %+v %v", first, err)
	}
	again, _ := s.SetPutUp(ctx, l.ID, true)
	if !again.PutUpAt.Equal(*first.PutUpAt) {
		t.Error("second put-up moved the first timestamp")
	}
	cleared, _ := s.SetPutUp(ctx, l.ID, false)
	if cleared.PutUpAt != nil {
		t.Error("put-up not cleared")
	}
	if _, err := s.SetPutUp(ctx, l.ID+99999999, true); !errors.Is(err, ErrFlyerLinkNotFound) {
		t.Errorf("unknown: %v", err)
	}
}

func TestFlyerCascadeOnOutreachDelete(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	o, a, _ := seedFlyerFixture(t, s)
	f, _ := s.CreateFlyer(ctx, FlyerInput{OutreachID: o.ID, FocalX: 0.5, FocalY: 0.5, Template: "split"})
	if err := s.ReplaceFlyerSelection(ctx, f.ID, []SelectionInput{{a.ID, 1}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.InsertFlyerLink(ctx, o.ID, a.ID, a.Code, "k", "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM outreach WHERE id = $1`, o.ID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM outreach_flyers WHERE outreach_id = ` + strconv.FormatInt(o.ID, 10),
		`SELECT count(*) FROM outreach_flyer_links WHERE outreach_id = ` + strconv.FormatInt(o.ID, 10),
		`SELECT count(*) FROM outreach_flyer_locations WHERE flyer_id = ` + strconv.FormatInt(f.ID, 10),
	} {
		var n int
		if err := s.pool.QueryRow(ctx, q).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s = %d %v", q, n, err)
		}
	}
}
