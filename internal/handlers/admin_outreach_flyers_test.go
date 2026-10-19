package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

type testFlyerView struct {
	Flyer struct {
		ID               int64   `json:"id"`
		OutreachID       int64   `json:"outreach_id"`
		WorkshopID       int64   `json:"workshop_id"`
		Photo            *string `json:"photo"`
		FocalX           float64 `json:"focal_x"`
		FocalY           float64 `json:"focal_y"`
		Template         string  `json:"template"`
		Tagline          string  `json:"tagline"`
		Description      string  `json:"description"`
		DescriptionShort string  `json:"description_short"`
		TitlePosition    string  `json:"title_position"`
	} `json:"flyer"`
	Facts struct {
		Title       string `json:"title"`
		DateLine    string `json:"date_line"`
		PlaceLine   string `json:"place_line"`
		Description string `json:"description"`
	} `json:"facts"`
	Selection []struct {
		LocationID int64  `json:"location_id"`
		Name       string `json:"name"`
		Code       string `json:"code"`
		Copies     int    `json:"copies"`
		Link       *struct {
			ID       int64   `json:"id"`
			Key      string  `json:"short_link_key"`
			ShortURL string  `json:"short_url"`
			HasQR    bool    `json:"has_qr"`
			QRURL    *string `json:"qr_url"`
			PutUpAt  *string `json:"put_up_at"`
		} `json:"link"`
	} `json:"selection"`
	Remaining *int   `json:"remaining"`
	Message   string `json:"message"`
}

// validQR is output in the shape of ShortLinks' QR renderer.
func validQR() []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 21 21" width="168" height="168" shape-rendering="crispEdges">` + "\n")
	b.WriteString(`<rect x="0" y="0" width="21" height="21" fill="#ffffff"/>` + "\n<g fill=\"#000000\">\n")
	b.WriteString(`<rect x="2" y="2" width="7" height="1"/>` + "\n" + `<rect x="2" y="3" width="1" height="1"/>` + "\n")
	b.WriteString("</g>\n</svg>\n")
	return []byte(b.String())
}

func maliciousQR() []byte {
	return []byte(strings.Replace(string(validQR()), "<g fill", `<script>alert(1)</script><g fill`, 1))
}

type flyerEnv struct {
	*outreachEnv
	locs []outreach.Location
}

// newFlyerEnv seeds n active locations FIRST, so their cleanup runs after the
// workshop's (and its links, which RESTRICT a location delete).
func newFlyerEnv(t *testing.T, enabled bool, n int) *flyerEnv {
	t.Helper()
	e := newOutreachEnv(t, enabled)
	e.links.qrBody = validQR()
	store := outreach.NewStore(e.pool)
	fe := &flyerEnv{outreachEnv: e}
	for i := 0; i < n; i++ {
		l, err := store.CreateLocation(context.Background(), outreach.LocationInput{
			Name: fmt.Sprintf("Board %d", i), Code: "z" + strconv.FormatInt(testdb.Unique(), 36), Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		id := l.ID
		t.Cleanup(func() { _, _ = e.pool.Exec(context.Background(), `DELETE FROM outreach_locations WHERE id = $1`, id) })
		fe.locs = append(fe.locs, l)
	}
	return fe
}

// flyerFor starts outreach for a fresh published workshop, creates a flyer and
// selects the given locations (one copy each).
func (e *flyerEnv) flyerFor(t *testing.T, w workshops.Workshop, locs ...outreach.Location) testFlyerView {
	t.Helper()
	o := e.start(t, w).Outreach
	code, b := e.do(t, http.MethodPost, "/admin/outreach-flyers", fmt.Sprintf(`{"outreach_id":%d,"template":"split","tagline":"  Come solder  "}`, o.ID))
	if code != http.StatusCreated {
		t.Fatalf("create flyer = %d (%s)", code, b)
	}
	v := decodeOutreach[testFlyerView](t, b)
	e.selectLocations(t, v.Flyer.ID, locs...)
	return v
}

func (e *flyerEnv) selectLocations(t *testing.T, flyerID int64, locs ...outreach.Location) {
	t.Helper()
	parts := make([]string, 0, len(locs))
	for _, l := range locs {
		parts = append(parts, fmt.Sprintf(`{"location_id":%d,"copies":1}`, l.ID))
	}
	code, b := e.do(t, http.MethodPut, fmt.Sprintf("/admin/outreach-flyers/%d/locations", flyerID), `{"locations":[`+strings.Join(parts, ",")+`]}`)
	if code != http.StatusOK {
		t.Fatalf("select = %d (%s)", code, b)
	}
}

func (e *flyerEnv) generate(t *testing.T, flyerID int64) (int, testFlyerView, []byte) {
	t.Helper()
	code, b := e.do(t, http.MethodPost, fmt.Sprintf("/admin/outreach-flyers/%d/generate", flyerID), "")
	var v testFlyerView
	if code == http.StatusOK {
		v = decodeOutreach[testFlyerView](t, b)
	}
	return code, v, b
}

func TestAdminOutreachFlyers_AuthGuards(t *testing.T) {
	e := newFlyerEnv(t, true, 0)
	for _, rt := range []struct{ method, path string }{
		{"GET", "/admin/outreach-flyers"}, {"POST", "/admin/outreach-flyers"}, {"GET", "/admin/outreach-flyers/1"},
		{"PATCH", "/admin/outreach-flyers/1"}, {"PUT", "/admin/outreach-flyers/1/locations"}, {"DELETE", "/admin/outreach-flyers/1"},
		{"POST", "/admin/outreach-flyers/1/generate"}, {"GET", "/admin/outreach-flyer-links/1/qr.svg"},
		{"POST", "/admin/outreach-flyer-links/1/put-up"}, {"DELETE", "/admin/outreach-flyer-links/1/put-up"},
	} {
		for token, want := range map[string]int{"": http.StatusUnauthorized, "outreach-user-token": http.StatusForbidden} {
			resp := doJSON(t, e.srv.Client(), rt.method, e.srv.URL+rt.path, token, "")
			resp.Body.Close()
			if resp.StatusCode != want {
				t.Errorf("%s %s token=%q = %d, want %d", rt.method, rt.path, token, resp.StatusCode, want)
			}
		}
	}
}

func TestAdminOutreachFlyers_CRUDWorksWithoutShortLinks(t *testing.T) {
	e := newFlyerEnv(t, false, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o, _, err := outreach.NewStore(e.pool).Create(context.Background(), w.ID, w.Slug)
	if err != nil {
		t.Fatal(err)
	}
	code, b := e.do(t, http.MethodPost, "/admin/outreach-flyers", fmt.Sprintf(`{"outreach_id":%d,"photo":"/hero-crt-760.webp","focal_x":0.25}`, o.ID))
	if code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", code, b)
	}
	v := decodeOutreach[testFlyerView](t, b)
	if v.Flyer.Template != "full-photo" || v.Flyer.FocalX != 0.25 || v.Flyer.FocalY != 0.5 || v.Flyer.Photo == nil || v.Facts.Title != w.Title {
		t.Errorf("created view = %+v", v)
	}
	if !strings.HasPrefix(v.Facts.DateLine, "Sat, Dec 5 · 10:00 AM") || v.Facts.PlaceLine != "The Shop" {
		t.Errorf("facts = %+v", v.Facts)
	}
	path := fmt.Sprintf("/admin/outreach-flyers/%d", v.Flyer.ID)
	if code, b := e.do(t, http.MethodPatch, path, `{"tagline":"Hello","photo":""}`); code != http.StatusOK ||
		decodeOutreach[testFlyerView](t, b).Flyer.Photo != nil {
		t.Errorf("patch = %d (%s)", code, b)
	}
	e.selectLocations(t, v.Flyer.ID, e.locs[0])
	if code, _, _ := e.generate(t, v.Flyer.ID); code != http.StatusServiceUnavailable {
		t.Errorf("generate without ShortLinks = %d, want 503", code)
	}
	if code, b := e.do(t, http.MethodGet, "/admin/outreach-flyers", ""); code != http.StatusOK || !strings.Contains(string(b), `"workshop_title"`) {
		t.Errorf("list = %d (%s)", code, b)
	}
	if code, _ := e.do(t, http.MethodDelete, path, ""); code != http.StatusNoContent {
		t.Errorf("delete = %d", code)
	}
	if code, _ := e.do(t, http.MethodGet, path, ""); code != http.StatusNotFound {
		t.Errorf("get after delete = %d", code)
	}
	if n := outreachAuditCount(t, e.pool, "outreach_flyer.deleted", v.Flyer.ID); n != 1 {
		t.Errorf("deleted audit rows = %d", n)
	}
}

func TestAdminOutreachFlyers_Validation(t *testing.T) {
	e := newFlyerEnv(t, true, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	for name, body := range map[string]string{
		"no outreach":      `{}`,
		"unknown outreach": `{"outreach_id":999999999}`,
		"template":         fmt.Sprintf(`{"outreach_id":%d,"template":"poster"}`, o.ID),
		"focal":            fmt.Sprintf(`{"outreach_id":%d,"focal_x":1.5}`, o.ID),
		"tagline":          fmt.Sprintf(`{"outreach_id":%d,"tagline":%q}`, o.ID, strings.Repeat("é", 141)),
		"off-site photo":   fmt.Sprintf(`{"outreach_id":%d,"photo":"//evil.example/x.jpg"}`, o.ID),
		"description":      fmt.Sprintf(`{"outreach_id":%d,"description":%q}`, o.ID, strings.Repeat("é", 361)),
		"short desc":       fmt.Sprintf(`{"outreach_id":%d,"description_short":%q}`, o.ID, strings.Repeat("é", 141)),
		"title position":   fmt.Sprintf(`{"outreach_id":%d,"title_position":"middle"}`, o.ID),
		"absolute photo":   fmt.Sprintf(`{"outreach_id":%d,"photo":"https://evil.example/x.jpg"}`, o.ID),
	} {
		code, b := e.do(t, http.MethodPost, "/admin/outreach-flyers", body)
		want := http.StatusBadRequest
		if name == "unknown outreach" {
			want = http.StatusNotFound
		}
		if code != want {
			t.Errorf("%s: %d (%s), want %d", name, code, b, want)
		}
	}
	// 140 multi-byte runes is fine: the limit counts characters.
	if code, b := e.do(t, http.MethodPost, "/admin/outreach-flyers", fmt.Sprintf(`{"outreach_id":%d,"tagline":%q}`, o.ID, strings.Repeat("é", 140))); code != http.StatusCreated {
		t.Errorf("140 runes = %d (%s)", code, b)
	}
	if code, b := e.do(t, http.MethodPost, "/admin/outreach-flyers", fmt.Sprintf(`{"outreach_id":%d,"description":%q}`, o.ID, strings.Repeat("é", 360))); code != http.StatusCreated {
		t.Errorf("360 rune description = %d (%s)", code, b)
	}
	code, b := e.do(t, http.MethodPost, "/admin/outreach-flyers", fmt.Sprintf(`{"outreach_id":%d,"description":%q}`, o.ID, strings.Repeat("é", 361)))
	if code != http.StatusBadRequest || !strings.Contains(string(b), "description must be at most 360 characters") {
		t.Errorf("361 rune message = %d (%s)", code, b)
	}
}

func TestAdminOutreachFlyers_CaptionFields(t *testing.T) {
	e := newFlyerEnv(t, true, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	code, b := e.do(t, http.MethodPost, "/admin/outreach-flyers", fmt.Sprintf(`{"outreach_id":%d}`, o.ID))
	if code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", code, b)
	}
	v := decodeOutreach[testFlyerView](t, b)
	if v.Flyer.TitlePosition != "auto" || v.Flyer.Description != "" || v.Flyer.DescriptionShort != "" || v.Facts.Description != "" {
		t.Errorf("defaults = %+v / %+v", v.Flyer, v.Facts)
	}
	path := fmt.Sprintf("/admin/outreach-flyers/%d", v.Flyer.ID)

	// facts.description is the trimmed workshop summary.
	if _, err := e.pool.Exec(context.Background(), `UPDATE workshops SET summary = $2 WHERE id = $1`, w.ID, "  Solder an LED badge.  \n"); err != nil {
		t.Fatal(err)
	}
	code, b = e.do(t, http.MethodGet, path, "")
	if got := decodeOutreach[testFlyerView](t, b); code != http.StatusOK || got.Facts.Description != "Solder an LED badge." {
		t.Errorf("facts.description = %d %q", code, got.Facts.Description)
	}

	// A patch with only title_position is accepted.
	code, b = e.do(t, http.MethodPatch, path, `{"title_position":"above"}`)
	if got := decodeOutreach[testFlyerView](t, b); code != http.StatusOK || got.Flyer.TitlePosition != "above" {
		t.Errorf("patch title_position = %d (%s)", code, b)
	}

	// Description text is trimmed on save, and the audit row names fields only.
	code, b = e.do(t, http.MethodPatch, path, `{"description":"  Secret flyer words  ","description_short":" Short words "}`)
	got := decodeOutreach[testFlyerView](t, b)
	if code != http.StatusOK || got.Flyer.Description != "Secret flyer words" || got.Flyer.DescriptionShort != "Short words" || got.Flyer.TitlePosition != "above" {
		t.Errorf("patch descriptions = %d (%s)", code, b)
	}
	var meta string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT metadata::text FROM audit_log WHERE action = 'outreach_flyer.updated' AND target_id = $1 ORDER BY id DESC LIMIT 1`, v.Flyer.ID).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(meta, "description") || !strings.Contains(meta, "description_short") || strings.Contains(meta, "Secret") || strings.Contains(meta, "Short words") {
		t.Errorf("audit metadata = %s", meta)
	}
	if code, b := e.do(t, http.MethodPatch, path, `{"title_position":"middle"}`); code != http.StatusBadRequest {
		t.Errorf("bad position = %d (%s)", code, b)
	}
}

func TestAdminOutreachFlyers_SelectionValidation(t *testing.T) {
	e := newFlyerEnv(t, true, 2)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.flyerFor(t, w, e.locs[0])
	path := fmt.Sprintf("/admin/outreach-flyers/%d/locations", v.Flyer.ID)
	many := make([]string, 41)
	for i := range many {
		many[i] = fmt.Sprintf(`{"location_id":%d,"copies":1}`, e.locs[0].ID+int64(i))
	}
	off := false
	if _, err := outreach.NewStore(e.pool).UpdateLocation(context.Background(), e.locs[1].ID, outreach.LocationPatch{Active: &off}); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"41 locations": `{"locations":[` + strings.Join(many, ",") + `]}`,
		"zero copies":  fmt.Sprintf(`{"locations":[{"location_id":%d,"copies":0}]}`, e.locs[0].ID),
		"51 copies":    fmt.Sprintf(`{"locations":[{"location_id":%d,"copies":51}]}`, e.locs[0].ID),
		"duplicate":    fmt.Sprintf(`{"locations":[{"location_id":%d,"copies":1},{"location_id":%d,"copies":2}]}`, e.locs[0].ID, e.locs[0].ID),
		"unknown":      `{"locations":[{"location_id":999999999,"copies":1}]}`,
		"inactive new": fmt.Sprintf(`{"locations":[{"location_id":%d,"copies":1}]}`, e.locs[1].ID),
	} {
		if code, b := e.do(t, http.MethodPut, path, body); code != http.StatusBadRequest {
			t.Errorf("%s: %d (%s), want 400", name, code, b)
		}
	}
	if code, _ := e.do(t, http.MethodPut, "/admin/outreach-flyers/999999999/locations", `{"locations":[]}`); code != http.StatusNotFound {
		t.Errorf("unknown flyer = %d", code)
	}
}

func TestAdminOutreachFlyers_GenerateHappyPathIsIdempotent(t *testing.T) {
	e := newFlyerEnv(t, true, 2)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.flyerFor(t, w, e.locs[0], e.locs[1])

	code, got, b := e.generate(t, v.Flyer.ID)
	if code != http.StatusOK || got.Remaining == nil || *got.Remaining != 0 || got.Message != "" {
		t.Fatalf("generate = %d remaining=%v msg=%q (%s)", code, got.Remaining, got.Message, b)
	}
	if len(e.links.links) != 2 || len(e.links.qrKeys) != 2 {
		t.Fatalf("upstream calls: %d links, %d qr", len(e.links.links), len(e.links.qrKeys))
	}
	req := e.links.links[0]
	if req.UTMSource != "flyer" || req.UTMMedium != "print" || req.UTMCampaign != w.Slug || req.CampaignSlug != w.Slug ||
		req.Placement != "Board 0" || req.UTMContent != e.locs[0].Code ||
		req.DestinationURL != outreach.FlyerDestinationURL("https://site.example.test", w.Slug, e.locs[0].Code) {
		t.Errorf("CreateLink request = %+v", req)
	}
	for _, s := range got.Selection {
		if s.Link == nil || !s.Link.HasQR || s.Link.QRURL == nil || !strings.HasPrefix(s.Link.ShortURL, "https://go.example.test/u/k") {
			t.Errorf("selection %+v has no complete link", s)
		}
		if n := outreachAuditCount(t, e.pool, "outreach_flyer_link.created", s.Link.ID); n != 1 {
			t.Errorf("link.created audit rows for %d = %d", s.Link.ID, n)
		}
	}
	loc, _ := outreach.NewStore(e.pool).GetLocation(context.Background(), e.locs[0].ID)
	if loc.CodeLockedAt == nil {
		t.Error("location was not locked by Generate")
	}

	// A second Generate, and another flyer for the same workshop and
	// locations, make no upstream call at all and reuse the same links.
	if code, again, _ := e.generate(t, v.Flyer.ID); code != http.StatusOK || *again.Remaining != 0 ||
		again.Selection[0].Link.ID != got.Selection[0].Link.ID {
		t.Errorf("second generate = %d %+v", code, again)
	}
	other := decodeOutreach[testFlyerView](t, mustBody(t, e, http.MethodPost, "/admin/outreach-flyers", fmt.Sprintf(`{"outreach_id":%d}`, v.Flyer.OutreachID)))
	e.selectLocations(t, other.Flyer.ID, e.locs[0], e.locs[1])
	if code, third, _ := e.generate(t, other.Flyer.ID); code != http.StatusOK || *third.Remaining != 0 {
		t.Errorf("other flyer generate = %d", code)
	}
	if len(e.links.links) != 2 || len(e.links.qrKeys) != 2 {
		t.Errorf("repeat generates called upstream: %d links, %d qr", len(e.links.links), len(e.links.qrKeys))
	}
	var rows int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outreach_flyer_links WHERE outreach_id = $1`, v.Flyer.OutreachID).Scan(&rows)
	if rows != 2 {
		t.Errorf("link rows = %d, want 2", rows)
	}
}

func mustBody(t *testing.T, e *flyerEnv, method, path, body string) []byte {
	t.Helper()
	code, b := e.do(t, method, path, body)
	if code >= 300 {
		t.Fatalf("%s %s = %d (%s)", method, path, code, b)
	}
	return b
}

func TestAdminOutreachFlyers_GenerateResumesAfterQRFailure(t *testing.T) {
	e := newFlyerEnv(t, true, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.flyerFor(t, w, e.locs[0])
	secret := "upstream-secret-detail"
	e.links.qrErr = fmt.Errorf("%s: %w", secret, shortlinks.ErrUnauthenticated)

	code, got, b := e.generate(t, v.Flyer.ID)
	if code != http.StatusOK || got.Remaining == nil || *got.Remaining != 1 || got.Message == "" {
		t.Fatalf("generate = %d remaining=%v msg=%q", code, got.Remaining, got.Message)
	}
	if strings.Contains(string(b), secret) {
		t.Error("upstream error text leaked to the client")
	}
	if got.Selection[0].Link == nil || got.Selection[0].Link.HasQR {
		t.Errorf("link should be saved without a QR: %+v", got.Selection[0])
	}

	e.links.qrErr = nil
	code, got, _ = e.generate(t, v.Flyer.ID)
	if code != http.StatusOK || *got.Remaining != 0 || !got.Selection[0].Link.HasQR {
		t.Fatalf("retry = %d %+v", code, got)
	}
	if len(e.links.links) != 1 || len(e.links.qrKeys) != 2 {
		t.Errorf("retry should fetch only the QR: %d links, %d qr requests", len(e.links.links), len(e.links.qrKeys))
	}
}

func TestAdminOutreachFlyers_MaliciousQRIsNeverCached(t *testing.T) {
	e := newFlyerEnv(t, true, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.flyerFor(t, w, e.locs[0])
	e.links.qrBody = maliciousQR()
	code, got, _ := e.generate(t, v.Flyer.ID)
	if code != http.StatusOK || *got.Remaining != 1 || got.Selection[0].Link.HasQR || got.Selection[0].Link.QRURL != nil {
		t.Fatalf("malicious QR: %d %+v", code, got)
	}
	resp := doJSON(t, e.srv.Client(), http.MethodGet, e.srv.URL+fmt.Sprintf("/admin/outreach-flyer-links/%d/qr.svg", got.Selection[0].Link.ID), "outreach-admin-token", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("qr.svg before a QR is cached = %d, want 404", resp.StatusCode)
	}
}

func TestAdminOutreachFlyers_RateLimitedBeforeProgressIs429(t *testing.T) {
	e := newFlyerEnv(t, true, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.flyerFor(t, w, e.locs[0])
	e.links.linkErr = fmt.Errorf("wrapped: %w", shortlinks.ErrRateLimited)
	if code, _, b := e.generate(t, v.Flyer.ID); code != http.StatusTooManyRequests {
		t.Errorf("generate = %d (%s), want 429", code, b)
	}
}

func TestAdminOutreachFlyers_GenerateRequiresPublishedWorkshopAndSelection(t *testing.T) {
	e := newFlyerEnv(t, true, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.flyerFor(t, w)
	if code, _, _ := e.generate(t, v.Flyer.ID); code != http.StatusBadRequest {
		t.Errorf("empty selection = %d, want 400", code)
	}
	e.selectLocations(t, v.Flyer.ID, e.locs[0])
	if _, err := e.ws.Update(context.Background(), w.ID, workshops.UpdateInput{
		Title: w.Title, StartsAt: w.StartsAt, LocationName: w.LocationName, Status: "draft",
	}); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := e.generate(t, v.Flyer.ID); code != http.StatusConflict {
		t.Errorf("unpublished = %d, want 409", code)
	}
	if len(e.links.links) != 0 {
		t.Error("an unpublished workshop reached ShortLinks")
	}
}

func TestAdminOutreachFlyers_QRServedAsAttachment(t *testing.T) {
	e := newFlyerEnv(t, true, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.flyerFor(t, w, e.locs[0])
	_, got, _ := e.generate(t, v.Flyer.ID)
	resp := doJSON(t, e.srv.Client(), http.MethodGet, e.srv.URL+*got.Selection[0].Link.QRURL, "outreach-admin-token", "")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	h := resp.Header
	if resp.StatusCode != http.StatusOK || h.Get("Content-Type") != "image/svg+xml" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Disposition") != `attachment; filename="`+e.locs[0].Code+`.svg"` || !strings.HasPrefix(h.Get("Cache-Control"), "private") {
		t.Errorf("response = %d %v", resp.StatusCode, h)
	}
	if !strings.HasPrefix(string(body), "<svg ") || strings.Contains(string(body), "<?xml") || strings.Contains(string(body), "width=\"168\"") {
		t.Errorf("served the upstream bytes rather than the rebuilt SVG: %s", body)
	}
}

func TestAdminOutreachFlyers_PutUp(t *testing.T) {
	e := newFlyerEnv(t, true, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.flyerFor(t, w, e.locs[0])
	_, got, _ := e.generate(t, v.Flyer.ID)
	id := got.Selection[0].Link.ID
	path := fmt.Sprintf("/admin/outreach-flyer-links/%d/put-up", id)
	type linkResp struct {
		PutUpAt *string `json:"put_up_at"`
	}
	code, b := e.do(t, http.MethodPost, path, "")
	first := decodeOutreach[linkResp](t, b)
	if code != http.StatusOK || first.PutUpAt == nil {
		t.Fatalf("put up = %d (%s)", code, b)
	}
	_, b = e.do(t, http.MethodPost, path, "")
	if again := decodeOutreach[linkResp](t, b); again.PutUpAt == nil || *again.PutUpAt != *first.PutUpAt {
		t.Errorf("second put-up moved the timestamp: %v vs %v", again.PutUpAt, first.PutUpAt)
	}
	if n := outreachAuditCount(t, e.pool, "outreach_flyer_link.put_up", id); n != 1 {
		t.Errorf("put_up audit rows = %d, want 1 (only the real change)", n)
	}
	_, b = e.do(t, http.MethodDelete, path, "")
	if cleared := decodeOutreach[linkResp](t, b); cleared.PutUpAt != nil {
		t.Error("not cleared")
	}
	if n := outreachAuditCount(t, e.pool, "outreach_flyer_link.put_up_cleared", id); n != 1 {
		t.Errorf("cleared audit rows = %d", n)
	}
	if code, _ := e.do(t, http.MethodPost, "/admin/outreach-flyer-links/999999999/put-up", ""); code != http.StatusNotFound {
		t.Errorf("unknown link = %d", code)
	}
}

func TestAdminOutreachFlyers_DeleteLeavesLinks(t *testing.T) {
	e := newFlyerEnv(t, true, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.flyerFor(t, w, e.locs[0])
	e.generate(t, v.Flyer.ID)
	if code, _ := e.do(t, http.MethodDelete, fmt.Sprintf("/admin/outreach-flyers/%d", v.Flyer.ID), ""); code != http.StatusNoContent {
		t.Fatal(code)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outreach_flyer_links WHERE outreach_id = $1`, v.Flyer.OutreachID).Scan(&n)
	if n != 1 {
		t.Errorf("links after flyer delete = %d, want 1", n)
	}
}

func TestAdminOutreachFlyers_ResultsFlyerLocations(t *testing.T) {
	e := newFlyerEnv(t, true, 2)
	// Two workshops, one shared location, so "all workshops" can differ.
	w1 := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	w2 := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v1 := e.flyerFor(t, w1, e.locs[0], e.locs[1])
	v2 := e.flyerFor(t, w2, e.locs[0])
	_, g1, _ := e.generate(t, v1.Flyer.ID)
	_, g2, _ := e.generate(t, v2.Flyer.ID)
	key := func(g testFlyerView, loc outreach.Location) string {
		for _, s := range g.Selection {
			if s.LocationID == loc.ID {
				return s.Link.Key
			}
		}
		t.Fatalf("no link for %d", loc.ID)
		return ""
	}
	k1a, k1b, k2a := key(g1, e.locs[0]), key(g1, e.locs[1]), key(g2, e.locs[0])
	e.links.statsByCampaign = map[string]*shortlinks.CampaignRollup{
		w1.Slug: {ClickCount: 12, ByLink: []shortlinks.LinkBucket{{Key: k1a, Count: 5}, {Key: k1b, Count: 2}, {Key: "other-link", Count: 5}}},
		w2.Slug: {ClickCount: 4, ByLink: []shortlinks.LinkBucket{{Key: k2a, Count: 4}}},
	}
	type flyerResults struct {
		Status string `json:"status"`
		Totals struct {
			Clicks          int64 `json:"clicks"`
			UnmatchedClicks int64 `json:"unmatched_clicks"`
			FlyerClicks     int64 `json:"flyer_clicks"`
		} `json:"totals"`
		FlyerAllStatus string `json:"flyer_all_status"`
		FlyerLocations []struct {
			LocationID         int64  `json:"location_id"`
			Code               string `json:"code"`
			Clicks             *int64 `json:"clicks"`
			ClicksAllWorkshops *int64 `json:"clicks_all_workshops"`
			WorkshopsAt        int    `json:"workshops_at_location"`
		} `json:"flyer_locations"`
	}
	get := func() flyerResults {
		return decodeOutreach[flyerResults](t, mustBody(t, e, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach/results", w1.ID), ""))
	}
	r := get()
	if r.Status != "ok" || r.Totals.FlyerClicks != 7 || r.Totals.UnmatchedClicks != 5 || r.Totals.Clicks != 12 || r.FlyerAllStatus != "ok" || len(r.FlyerLocations) != 2 {
		t.Fatalf("results = %+v", r)
	}
	for _, fl := range r.FlyerLocations {
		if fl.Clicks == nil || fl.ClicksAllWorkshops == nil {
			t.Fatalf("nil counts: %+v", fl)
		}
		switch fl.LocationID {
		case e.locs[0].ID:
			if *fl.Clicks != 5 || *fl.ClicksAllWorkshops != 9 {
				t.Errorf("shared location: %d / %d, want 5 / 9", *fl.Clicks, *fl.ClicksAllWorkshops)
			}
			if fl.WorkshopsAt != 2 {
				t.Errorf("shared location workshops_at_location = %d, want 2 (#0590)", fl.WorkshopsAt)
			}
		case e.locs[1].ID:
			if *fl.Clicks != 2 || *fl.ClicksAllWorkshops != 2 {
				t.Errorf("own location: %d / %d, want 2 / 2", *fl.Clicks, *fl.ClicksAllWorkshops)
			}
			if fl.WorkshopsAt != 1 {
				t.Errorf("own location workshops_at_location = %d, want 1 (#0590)", fl.WorkshopsAt)
			}
		}
	}

	// One unreachable campaign nulls every all-workshops count, and this
	// workshop's own numbers still show. The stats cache already holds the
	// first two campaigns, so the failing one is a third, not yet fetched.
	w3 := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v3 := e.flyerFor(t, w3, e.locs[0])
	e.generate(t, v3.Flyer.ID)
	e.links.statsErrFor = map[string]error{w3.Slug: fmt.Errorf("boom: %w", shortlinks.ErrRateLimited)}
	r = get()
	if r.Status != "ok" || r.FlyerAllStatus != "unreachable" {
		t.Fatalf("after failure: status=%s all=%s", r.Status, r.FlyerAllStatus)
	}
	for _, fl := range r.FlyerLocations {
		if fl.Clicks == nil || fl.ClicksAllWorkshops != nil {
			t.Errorf("after failure %+v", fl)
		}
	}
}

func TestAdminOutreachFlyers_GenerateDeadlineReportsProgressAndResumes(t *testing.T) {
	old := flyerGenerateTimeout
	flyerGenerateTimeout = 150 * time.Millisecond
	t.Cleanup(func() { flyerGenerateTimeout = old })

	e := newFlyerEnv(t, true, 2)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.flyerFor(t, w, e.locs[0], e.locs[1])
	e.links.mu.Lock()
	e.links.qrBlock = true
	e.links.mu.Unlock()

	code, got, b := e.generate(t, v.Flyer.ID)
	if code != http.StatusOK || got.Remaining == nil || *got.Remaining != 2 || got.Message != msgFlyerGenerateTimeout {
		t.Fatalf("deadline generate = %d remaining=%v msg=%q (%s)", code, got.Remaining, got.Message, b)
	}
	if got.Selection[0].Link == nil || got.Selection[0].Link.HasQR {
		t.Errorf("first link should be saved without a QR: %+v", got.Selection[0])
	}
	if got.Selection[1].Link != nil {
		t.Errorf("second location should not have been reached: %+v", got.Selection[1])
	}

	e.links.mu.Lock()
	e.links.qrBlock = false
	e.links.mu.Unlock()
	code, got, b = e.generate(t, v.Flyer.ID)
	if code != http.StatusOK || got.Remaining == nil || *got.Remaining != 0 || got.Message != "" {
		t.Fatalf("resume = %d remaining=%v msg=%q (%s)", code, got.Remaining, got.Message, b)
	}
	for _, s := range got.Selection {
		if s.Link == nil || !s.Link.HasQR {
			t.Errorf("after resume %+v is incomplete", s)
		}
	}
	if len(e.links.links) != 2 {
		t.Errorf("resume re-created links: %d CreateLink calls, want 2", len(e.links.links))
	}
}

func TestAdminOutreachFlyers_AllWorkshopsIgnoresCampaignsAtOtherLocations(t *testing.T) {
	e := newFlyerEnv(t, true, 2)
	w1 := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	w2 := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v1 := e.flyerFor(t, w1, e.locs[0])
	v2 := e.flyerFor(t, w2, e.locs[1])
	_, g1, _ := e.generate(t, v1.Flyer.ID)
	e.generate(t, v2.Flyer.ID)
	e.links.statsByCampaign = map[string]*shortlinks.CampaignRollup{
		w1.Slug: {ClickCount: 3, ByLink: []shortlinks.LinkBucket{{Key: g1.Selection[0].Link.Key, Count: 3}}},
	}
	// w2's campaign sits only at a location w1 never selected, so it must not
	// be queried, and its failure must not blank w1's column.
	e.links.statsErrFor = map[string]error{w2.Slug: fmt.Errorf("boom: %w", shortlinks.ErrRateLimited)}
	type allResults struct {
		FlyerAllStatus string `json:"flyer_all_status"`
		FlyerLocations []struct {
			ClicksAllWorkshops *int64 `json:"clicks_all_workshops"`
		} `json:"flyer_locations"`
	}
	r := decodeOutreach[allResults](t, mustBody(t, e, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach/results", w1.ID), ""))
	if r.FlyerAllStatus != "ok" || len(r.FlyerLocations) != 1 || r.FlyerLocations[0].ClicksAllWorkshops == nil || *r.FlyerLocations[0].ClicksAllWorkshops != 3 {
		t.Fatalf("results = %+v", r)
	}
	e.links.mu.Lock()
	defer e.links.mu.Unlock()
	for _, c := range e.links.statsCalls {
		if c.slug == w2.Slug {
			t.Errorf("unrelated campaign %s was queried", w2.Slug)
		}
	}
}
