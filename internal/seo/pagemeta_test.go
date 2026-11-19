package seo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"golang.org/x/net/html"

	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
)

// parsedHead is what a browser's DOM would hold after loading Render's HTML.
type parsedHead struct {
	title        string
	meta         map[string]string // name or property -> content
	canonical    string
	hasCanonical bool
}

// parseRenderedHead parses rendered HTML with an HTML parser rather than
// re-reading resolve, so the parity oracle is a different representation
// from the endpoint under test (AGENTS.md §8).
func parseRenderedHead(t *testing.T, body []byte) parsedHead {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	h := parsedHead{meta: map[string]string{}}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			attr := func(k string) (string, bool) {
				for _, a := range n.Attr {
					if a.Key == k {
						return a.Val, true
					}
				}
				return "", false
			}
			switch n.Data {
			case "title":
				if n.FirstChild != nil {
					h.title = n.FirstChild.Data
				}
			case "meta":
				c, _ := attr("content")
				if k, ok := attr("name"); ok {
					h.meta[k] = c
				} else if k, ok := attr("property"); ok {
					h.meta[k] = c
				}
			case "link":
				if rel, _ := attr("rel"); rel == "canonical" {
					h.canonical, _ = attr("href")
					h.hasCanonical = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return h
}

func getPageMeta(t *testing.T, s *Site, path string) (int, pageMetaJSON) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/page-meta?path="+url.QueryEscape(path), nil)
	rec := httptest.NewRecorder()
	s.PageMetaHandler()(rec, req)
	var m pageMetaJSON
	if rec.Code == http.StatusOK {
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type = %q", path, ct)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
	}
	return rec.Code, m
}

// TestPageMeta_MatchesRenderedHead is #0711's parity guard: for every kind
// of route, GET /api/page-meta returns exactly what the rendered <head>
// carries, so a client applying it cannot drift from the server.
func TestPageMeta_MatchesRenderedHead(t *testing.T) {
	all := guides.All()
	artSlug := all[0].Slug
	art, err := loadGuideArt(fstest.MapFS{"d/" + artSlug + ".png": {Data: testPNG(t, cardW, cardH, false)}}, "d")
	if err != nil {
		t.Fatal(err)
	}
	ws := fakeWorkshopSource{
		"solder-101": {
			Slug: "solder-101", Title: "Intro to Soldering", Summary: "Learn soldering.",
			Status: WorkshopPublished, Published: true,
		},
		"gone": {Slug: "gone", Title: "Canceled Workshop", Summary: "s", Status: WorkshopCanceled, Published: true},
	}
	ar := fakeArchiveSource{
		"09-2026": {Slug: "09-2026", Subject: "September newsletter", BodyMD: "We soldered.", Published: true, UpdatedAt: "2026-09-01"},
	}
	site := newSite([]byte(testTemplate), testBaseURL, ws, ar, art)

	paths := []string{"/", "/about", "/privacy", "/events", "/subscribe", "/archive", "/guides", "/docs", "/docs/newsletter",
		"/events/solder-101", "/events/gone", "/events/nope", "/archive/09-2026",
		"/login", "/confirm", "/no-such-page", "/guides/"}
	for _, g := range all {
		paths = append(paths, g.Path())
	}
	for _, p := range paths {
		h := parseRenderedHead(t, site.renderer.Render(p))
		code, m := getPageMeta(t, site, p)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", p, code)
		}
		check := func(field, got, want string) {
			if got != want {
				t.Errorf("%s: %s = %q, rendered head has %q", p, field, got, want)
			}
		}
		check("title", m.Title, h.title)
		check("description", m.Description, h.meta["description"])
		check("og_title", m.OGTitle, h.meta["og:title"])
		check("og_description", m.OGDescription, h.meta["og:description"])
		check("og_image", m.OGImage, h.meta["og:image"])
		check("og_url", m.OGURL, h.meta["og:url"])
		check("og_type", m.OGType, h.meta["og:type"])
		check("twitter_card", m.TwitterCard, h.meta["twitter:card"])
		check("og_title vs twitter:title", m.OGTitle, h.meta["twitter:title"])
		check("og_description vs twitter:description", m.OGDescription, h.meta["twitter:description"])
		check("canonical", m.Canonical, h.canonical)
		if (m.Canonical != "") != h.hasCanonical {
			t.Errorf("%s: canonical presence: json %q, head has=%v", p, m.Canonical, h.hasCanonical)
		}
	}

	// Prove the fixtures are live, so the loop compared real variants.
	if _, m := getPageMeta(t, site, all[0].Path()); !strings.Contains(m.OGImage, "?v=") {
		t.Errorf("art-injected guide og_image %q lacks ?v=", m.OGImage)
	}
	_, pub := getPageMeta(t, site, "/events/solder-101")
	_, canceled := getPageMeta(t, site, "/events/gone")
	if !strings.Contains(pub.OGTitle, "Intro to Soldering") || strings.Contains(canceled.OGTitle, "Canceled Workshop") {
		t.Errorf("event fixtures not exercised: published %q, canceled %q", pub.OGTitle, canceled.OGTitle)
	}
	if _, a := getPageMeta(t, site, "/archive/09-2026"); !strings.Contains(a.OGTitle, "September newsletter") {
		t.Errorf("archive fixture not exercised: %q", a.OGTitle)
	}
}

func TestPageMetaHandler_RejectsBadPath(t *testing.T) {
	site := newSite([]byte(testTemplate), testBaseURL, nil, nil, nil)
	for _, p := range []string{"", "guides/x", "/" + strings.Repeat("a", maxPageMetaPath)} {
		if code, _ := getPageMeta(t, site, p); code != http.StatusBadRequest {
			t.Errorf("path %q: status %d, want 400", p[:min(len(p), 20)], code)
		}
	}
}

func TestPageMetaHandler_IgnoresQueryInPath(t *testing.T) {
	site := newSite([]byte(testTemplate), testBaseURL, nil, nil, nil)
	code, m := getPageMeta(t, site, "/guides/baud-rate?x=1")
	if code != http.StatusOK || !strings.HasSuffix(m.OGURL, "/guides/baud-rate") {
		t.Errorf("status %d og_url %q", code, m.OGURL)
	}
}
