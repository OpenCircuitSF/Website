package seo

import (
	"encoding/xml"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
)

// TestRender_GuideRoutesCarryOwnMeta (#0656): every guide URL
// server-renders its own title, plain-text description, canonical and
// per-guide OG card, because social crawlers do not run JS.
func TestRender_GuideRoutesCarryOwnMeta(t *testing.T) {
	r := newTestRenderer(nil)
	seen := map[string]bool{}
	for _, g := range guides.All() {
		body := string(r.Render(g.Path()))
		title := extractTag(t, body, "title")
		if title != html.EscapeString(g.Title+" — Open Circuit SF") {
			t.Errorf("%s: title = %q", g.Path(), title)
		}
		if seen[title] {
			t.Errorf("%s: title %q reused", g.Path(), title)
		}
		seen[title] = true
		if got := extractMetaContent(t, body, "description"); got != html.EscapeString(g.Description) {
			t.Errorf("%s: description = %q, want %q", g.Path(), got, html.EscapeString(g.Description))
		}
		if got := extractMetaContent(t, body, "og:description"); strings.ContainsRune(html.UnescapeString(got), 0xad) {
			t.Errorf("%s: og:description carries a soft hyphen: %q", g.Path(), got)
		}
		if got := extractMetaContent(t, body, "og:title"); got != html.EscapeString(g.Title) {
			t.Errorf("%s: og:title = %q", g.Path(), got)
		}
		ogURL := extractMetaContent(t, body, "og:url")
		if canon := extractCanonicalHref(t, body); canon != ogURL || ogURL != testBaseURL+g.Path() {
			t.Errorf("%s: canonical %q, og:url %q", g.Path(), canon, ogURL)
		}
		if got, want := extractMetaContent(t, body, "og:image"), testBaseURL+"/guides/"+g.Slug+"/og.png"; got != want {
			t.Errorf("%s: og:image = %q, want %q", g.Path(), got, want)
		}
	}
}

func TestRender_UnknownGuideSlugGetsNotFoundMeta(t *testing.T) {
	r := newTestRenderer(nil)
	notFound := extractTag(t, string(r.Render("/this-page-does-not-exist")), "title")
	for _, p := range []string{"/guides/nope", "/workshops/programming-leds-3"} {
		if got := extractTag(t, string(r.Render(p)), "title"); got != notFound {
			t.Errorf("%s: title = %q, want the not-found title %q", p, got, notFound)
		}
	}
}

func TestRenderPage_GuidesIndexListsEveryGuide(t *testing.T) {
	r := newTestRenderer(nil)
	body := string(r.Render("/guides"))
	if got := extractTag(t, body, "h1"); got != guidesH1 {
		t.Errorf("h1 = %q", got)
	}
	for _, g := range guides.All() {
		if !strings.Contains(body, `<a href="`+g.Path()+`">`+html.EscapeString(g.Title)+`</a>`) {
			t.Errorf("/guides fallback missing a link to %s", g.Path())
		}
	}
	if !strings.Contains(body, "<h2>"+guidesWorkshopH2+"</h2>") {
		t.Error("/guides fallback missing its Workshop guides heading")
	}
}

func TestRenderPage_GuideHasH1AndHook(t *testing.T) {
	r := newTestRenderer(nil)
	for _, g := range guides.All() {
		body := string(r.Render(g.Path()))
		if n := strings.Count(body, "<h1"); n != 1 {
			t.Errorf("%s: %d <h1>", g.Path(), n)
		}
		if got := extractTag(t, body, "h1"); got != html.EscapeString(g.Title) {
			t.Errorf("%s: h1 = %q", g.Path(), got)
		}
		if !strings.Contains(body, "<p>"+html.EscapeString(g.Hook)+"</p>") {
			t.Errorf("%s: hook not rendered", g.Path())
		}
		if !strings.Contains(body, `<a href="/guides">`+guidesSeeAll+`</a>`) {
			t.Errorf("%s: no link back to /guides", g.Path())
		}
	}
}

func TestGuideCardHandler_ServesPNGForEveryGuide(t *testing.T) {
	site := NewSite([]byte(testTemplate), testBaseURL, nil, nil)
	mux := http.NewServeMux()
	mux.Handle("GET /guides/{slug}/og.png", site.GuideCardHandler())
	for _, g := range guides.All() {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/guides/"+g.Slug+"/og.png", nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
			t.Errorf("%s card: status %d, type %q", g.Slug, rec.Code, rec.Header().Get("Content-Type"))
		}
		if !strings.HasPrefix(rec.Body.String(), "\x89PNG") {
			t.Errorf("%s card: body is not a PNG", g.Slug)
		}
	}
	if got := guideCardMeta(guides.Guide{Kind: guides.Workshop}); got != "Workshop guide" {
		t.Errorf("workshop meta = %q", got)
	}
	if got := guideCardMeta(guides.Guide{Kind: guides.Topic, Minutes: 3}); got != "Guide · 3 min" {
		t.Errorf("topic meta = %q", got)
	}
}

func TestGuideCardHandler_UnknownSlug404(t *testing.T) {
	site := NewSite([]byte(testTemplate), testBaseURL, nil, nil)
	mux := http.NewServeMux()
	mux.Handle("GET /guides/{slug}/og.png", site.GuideCardHandler())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/guides/nope/og.png", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestSitemap_ListsEveryGuideWithoutLastmod(t *testing.T) {
	body, err := NewSitemap(testBaseURL, nil, nil).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var parsed urlset
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	lastmod := map[string]string{}
	have := map[string]bool{}
	for _, u := range parsed.URLs {
		have[u.Loc] = true
		lastmod[u.Loc] = u.LastMod
	}
	for _, p := range append([]string{"/guides"}, guidePaths()...) {
		if !have[testBaseURL+p] {
			t.Errorf("sitemap missing %s", p)
		}
		if lastmod[testBaseURL+p] != "" {
			t.Errorf("%s: lastmod %q, want none", p, lastmod[testBaseURL+p])
		}
		if !handlers.IsKnownRoute(p) {
			t.Errorf("sitemap lists %s, which handlers.IsKnownRoute rejects", p)
		}
	}
}

func guidePaths() []string {
	var out []string
	for _, g := range guides.All() {
		out = append(out, g.Path())
	}
	return out
}
