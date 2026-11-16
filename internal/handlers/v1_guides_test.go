package handlers

import (
	"net/http"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// TestV1Guides_ListAllAndFilters needs no database: listing reads only the
// embedded catalog, so the in-memory apitest router is enough.
func TestV1Guides_ListAllAndFilters(t *testing.T) {
	res := apitest.NewResolver()
	secret, _ := res.Key(t, "guides", apitokens.ScopeGuidesRead)
	h := apitest.Router(t, res, guideOperations("https://site.example.test", nil)...)

	rec := apitest.Do(t, h, http.MethodGet, "/api/v1/guides?limit=200", secret, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	all := v1Decode[api.List[guideV1]](t, rec)
	if len(all.Items) != len(guides.All()) || all.NextCursor != nil {
		t.Fatalf("got %d guides, want %d", len(all.Items), len(guides.All()))
	}
	for _, g := range all.Items {
		if g.Tags == nil || g.Set == "" || g.URL == "" {
			t.Errorf("%s: tags %v set %q url %q", g.Slug, g.Tags, g.Set, g.URL)
		}
		switch g.Kind {
		case "topic":
			if !g.Postable || g.Post == "" || g.DefaultBody == "" || g.URL != "https://site.example.test/guides/"+g.Slug {
				t.Errorf("topic %s: %+v", g.Slug, g)
			}
		case "workshop":
			if g.Postable || g.Post != "" || g.DefaultBody != "" || g.URL != "https://site.example.test/workshops/"+g.Slug {
				t.Errorf("workshop %s: %+v", g.Slug, g)
			}
		default:
			t.Errorf("%s: kind %q", g.Slug, g.Kind)
		}
	}

	esp := v1Decode[api.List[guideV1]](t, apitest.Do(t, h, http.MethodGet, "/api/v1/guides?set=esp32&limit=200", secret, ""))
	if len(esp.Items) == 0 {
		t.Fatal("no esp32 guides")
	}
	for _, g := range esp.Items {
		if g.Set != "esp32" {
			t.Errorf("set filter let %s (%s) through", g.Slug, g.Set)
		}
	}
	topics := v1Decode[api.List[guideV1]](t, apitest.Do(t, h, http.MethodGet, "/api/v1/guides?kind=topic&limit=200", secret, ""))
	if len(topics.Items) != len(guides.Topics()) {
		t.Errorf("kind=topic: %d, want %d", len(topics.Items), len(guides.Topics()))
	}

	rec = apitest.Do(t, h, http.MethodGet, "/api/v1/guides?set=nope&kind=book", secret, "")
	if e := apitest.ErrorOf(t, rec.Body.Bytes()); rec.Code != http.StatusBadRequest || e.Fields["set"] == "" || e.Fields["kind"] == "" {
		t.Fatalf("bad filters = %d %+v", rec.Code, e)
	}

	// Walk catalog order two at a time.
	var walked []string
	path := "/api/v1/guides?limit=2"
	for i := 0; i < 100; i++ {
		page := v1Decode[api.List[guideV1]](t, apitest.Do(t, h, http.MethodGet, path, secret, ""))
		for _, g := range page.Items {
			walked = append(walked, g.Slug)
		}
		if page.NextCursor == nil {
			break
		}
		path = "/api/v1/guides?limit=2&cursor=" + *page.NextCursor
	}
	if len(walked) != len(guides.All()) || walked[0] != guides.All()[0].Slug {
		t.Fatalf("walk got %d slugs", len(walked))
	}

	stale := api.EncodeCursor(guideCursor{Slug: "gone-guide"})
	rec = apitest.Do(t, h, http.MethodGet, "/api/v1/guides?cursor="+stale, secret, "")
	if e := apitest.ErrorOf(t, rec.Body.Bytes()); rec.Code != http.StatusBadRequest || e.Fields["cursor"] == "" {
		t.Fatalf("stale cursor = %d %+v", rec.Code, e)
	}

	// A guide_posts key may not read the catalog.
	other, _ := res.Key(t, "posts", apitokens.ScopeGuidePostsWrite)
	if rec := apitest.Do(t, h, http.MethodGet, "/api/v1/guides", other, ""); rec.Code != http.StatusForbidden {
		t.Errorf("guide_posts:write key on /guides = %d", rec.Code)
	}
}

func TestV1Guides_GetWithHistory(t *testing.T) {
	e := newGuidePostsEnv(t, true, fakePosting{"bluesky": true, "mastodon": true})
	e.h.SetSignups(&fakeGuideSignups{rows: []subscribers.GuideSignups{
		{Guide: e.slug, Requested: 3, Confirmed: 2},
		{Guide: "some-other-guide", Requested: 9, Confirmed: 9},
	}})
	b := e.create(t, "bluesky", "")
	e.create(t, "mastodon", "")
	e.markPosted(t, b.Post.ID)

	h := v1TestRouter(t, e.pool, guideOperations("https://site.example.test", e.h)...)
	secret, _ := v1TestKey(t, e.pool, apitokens.ScopeGuidesRead)

	rec := apitest.Do(t, h, http.MethodGet, "/api/v1/guides/"+e.slug, secret, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	got := v1Decode[guideDetailV1](t, rec)
	if got.Slug != e.slug || got.History == nil {
		t.Fatalf("got %+v", got)
	}
	hist := got.History
	if hist.PostsByChannel["bluesky"].Posted != 1 || hist.PostsByChannel["mastodon"].Draft != 1 || hist.PostedCount != 1 {
		t.Errorf("posts_by_channel %+v, posted %d", hist.PostsByChannel, hist.PostedCount)
	}
	if hist.LastPostedAt == nil || time.Since(*hist.LastPostedAt) > time.Hour {
		t.Errorf("last_posted_at %v", hist.LastPostedAt)
	}
	if hist.Signups == nil || hist.Signups.Requested != 3 || hist.Signups.Confirmed != 2 {
		t.Errorf("signups %+v", hist.Signups)
	}
	if hist.Clicks == nil {
		t.Errorf("clicks unknown with a working ShortLinks fake (clicks_error %v)", hist.ClicksError)
	}

	ws := guides.WorkshopGuides()[0].Slug
	rec = apitest.Do(t, h, http.MethodGet, "/api/v1/guides/"+ws, secret, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("workshop get = %d", rec.Code)
	}
	raw := v1Decode[map[string]any](t, rec)
	if h, ok := raw["history"]; !ok || h != nil {
		t.Errorf("workshop guide history = %v (present %v), want null", h, ok)
	}

	rec = apitest.Do(t, h, http.MethodGet, "/api/v1/guides/no-such-guide", secret, "")
	if e := apitest.ErrorOf(t, rec.Body.Bytes()); rec.Code != http.StatusNotFound || e.Code != api.CodeNotFound {
		t.Errorf("unknown slug = %d %+v", rec.Code, e)
	}
}
