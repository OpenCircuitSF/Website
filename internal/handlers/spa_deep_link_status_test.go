package handlers

// #0522's HTML-route status tests: GET /archive/{slug} and
// /workshops/{slug} through the SPA catch-all must answer with the SAME
// status their JSON APIs answer with — 410 for a withheld campaign, 404 for
// an unknown/draft/unsent slug, 200 for published/live records — while
// still serving the SPA shell as the body, so the in-app not-found and
// withheld views keep working on a hard refresh. The fake stores reuse the
// package's existing in-process fakes (public_archive_test.go's
// fakeArchiveStore, public_workshops_test.go's workshop store fake).
import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// spaShell is the minimal index.html the test SPAHandler serves; the
// assertions check it is present at every status, since #0522 criterion 2
// is "the response body is still the SPA shell".
const spaShell = "<!doctype html><title>Open Circuit SF</title><div id=\"app\"></div>"

func spaFS() fs.FS {
	return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(spaShell)}}
}

func serveSPAResolved(t *testing.T, resolvers []SPADeepLinkStatus, path string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewSPAHandler(spaFS(), resolvers...)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	h.ServeHTTP(rec, req)
	return rec
}

// TestSPAHandler_ArchiveDetailStatusMatchesAPI is #0522 criterion 1 and 2:
// the HTML route mirrors the JSON API's visibility rule (withheld 410,
// unsent 404, published 200) with the shell as the body in every case.
func TestSPAHandler_ArchiveDetailStatusMatchesAPI(t *testing.T) {
	cases := []struct {
		name     string
		campaign mailing.Campaign
		wantCode int
	}{
		{"published", mailing.Campaign{Slug: "x", Status: mailing.CampaignStatusSent, ArchiveStatus: mailing.ArchiveStatusPublished}, 200},
		{"withheld", mailing.Campaign{Slug: "x", Status: mailing.CampaignStatusSent, ArchiveStatus: mailing.ArchiveStatusWithheld}, 410},
		{"unsent draft", mailing.Campaign{Slug: "x", Status: mailing.CampaignStatusDraft}, 404},
		{"sent but pending", mailing.Campaign{Slug: "x", Status: mailing.CampaignStatusSent, ArchiveStatus: mailing.ArchiveStatusPending}, 404},
		{"future archive_status", mailing.Campaign{Slug: "x", Status: mailing.CampaignStatusSent, ArchiveStatus: "some-future-value"}, 404},
	}
	for _, c := range cases {
		t.Run(c.campaign.Status+"/"+string(c.campaign.ArchiveStatus), func(t *testing.T) {
			// A fresh handler per case: the fake is copied by value at
			// construction, so each case needs its own.
			h := NewPublicArchiveHandler(fakeArchiveStore{campaign: c.campaign})
			rec := serveSPAResolved(t, []SPADeepLinkStatus{h}, "/archive/"+c.campaign.Slug)
			if rec.Code != c.wantCode {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, c.wantCode, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "<!doctype html>") {
				t.Errorf("expected the SPA shell body even at %d", c.wantCode)
			}
		})
	}
}

// TestSPAHandler_WorkshopDetailStatusMatchesAPI is #0522 criterion 4's
// implementation for workshops: same defect, same fix. Draft and
// never-published-canceled slugs 404; published and canceled-once-published
// stay 200; the body is always the shell.
func TestSPAHandler_WorkshopDetailStatusMatchesAPI(t *testing.T) {
	pubAt := time.Now().Add(-time.Hour)
	store := &fakeWorkshopPageStore{bySlug: map[string]workshops.Workshop{
		"live":         {Slug: "live", Status: workshops.StatusPublished, PublishedAt: &pubAt},
		"canceled-pub": {Slug: "canceled-pub", Status: workshops.StatusCanceled, PublishedAt: &pubAt},
		"draft":        {Slug: "draft", Status: workshops.StatusDraft},
		"ghost-cancel": {Slug: "ghost-cancel", Status: workshops.StatusCanceled},
	}}
	h := NewPublicWorkshopsHandler(store, nil)

	cases := []struct {
		path     string
		wantCode int
	}{
		{"/workshops/live", 200},
		{"/workshops/canceled-pub", 200},
		{"/workshops/draft", 404},
		{"/workshops/ghost-cancel", 404},
		{"/workshops/no-such-slug", 404},
	}
	for _, c := range cases {
		rec := serveSPAResolved(t, []SPADeepLinkStatus{h}, c.path)
		if rec.Code != c.wantCode {
			t.Errorf("GET %s: status = %d, want %d (body: %s)", c.path, rec.Code, c.wantCode, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s: expected the SPA shell body even at %d", c.path, c.wantCode)
		}
	}
}

// TestSPAHandler_NilResolversKeepRouteTableBehavior pins the pre-#0522
// behavior when no resolver is wired (STORAGE=json dev mode, and any other
// deployment without a live-data store): the dynamic patterns stay 200 per
// the known-route table, exactly as before this issue.
func TestSPAHandler_NilResolversKeepRouteTableBehavior(t *testing.T) {
	for _, p := range []string{"/archive/anything", "/workshops/anything"} {
		rec := serveSPAResolved(t, nil, p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s with no resolvers: status = %d, want 200 (route table behavior unchanged)", p, rec.Code)
		}
	}
}

// TestSPADeepLinkStatus_ResolversDeclineOtherPaths pins the ok=false half of
// the seam: a resolver must not claim paths outside its own pattern, or the
// archive resolver would decide the status of, say, /about.
func TestSPADeepLinkStatus_ResolversDeclineOtherPaths(t *testing.T) {
	sentPublished := mailing.Campaign{Slug: "some-slug", Status: mailing.CampaignStatusSent, ArchiveStatus: mailing.ArchiveStatusPublished}
	if _, ok := (&PublicArchiveHandler{}).SPAStatus(httptest.NewRequest("GET", "/about", nil)); ok {
		t.Error("archive resolver claimed /about")
	}
	if _, ok := (&PublicWorkshopsHandler{}).SPAStatus(httptest.NewRequest("GET", "/archive/some-slug", nil)); ok {
		t.Error("workshop resolver claimed /archive/some-slug")
	}
	// A trailing slash is normalized to the same slug (ArchiveDetailSlug's
	// own rule, the one isKnownRoute applies), so the resolver claims it.
	if _, ok := (&PublicArchiveHandler{store: fakeArchiveStore{campaign: sentPublished}}).SPAStatus(httptest.NewRequest("GET", "/archive/some-slug/", nil)); !ok {
		t.Error("archive resolver did not claim the normalized trailing-slash form of its own pattern")
	}
}

// fakeWorkshopPageStore is a minimal publicWorkshopStore for the deep-link
// status tests: GetBySlug only, no DB.
type fakeWorkshopPageStore struct {
	bySlug map[string]workshops.Workshop
}

func (f *fakeWorkshopPageStore) ListVisible(ctx context.Context, now time.Time) ([]workshops.Workshop, []workshops.Workshop, error) {
	return nil, nil, nil
}

func (f *fakeWorkshopPageStore) GetBySlug(ctx context.Context, slug string) (workshops.Workshop, error) {
	w, ok := f.bySlug[slug]
	if !ok {
		return workshops.Workshop{}, workshops.ErrNotFound
	}
	return w, nil
}
