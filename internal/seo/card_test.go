package seo

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/math/fixed"
)

// cardSafeInset is the pixel-measured proof's safe-area margin (#0273's
// plan §4, criterion 4): every non-background pixel in a rendered card must
// lie within this many pixels of every edge. cardMargin (card.go) is 70, so
// this is deliberately smaller -- the assertion is "nothing draws
// dangerously close to an edge", not a re-statement of the layout's own
// margin constant.
const cardSafeInset = 36

// cardDebugDirEnv, when set, is a directory TestWorkshopCard_TableRendersReadableCards
// also writes each case's rendered PNG into, so a human (or the implementer,
// per #0273's plan §4 step 4: "writes the same PNGs to its scratchpad and
// looks at them") can view the actual output. t.TempDir() already receives
// every case unconditionally; this is the opt-in second copy that survives
// the test.
const cardDebugDirEnv = "CARD_DEBUG_DIR"

func cardTestBackground() color.Color {
	return color.RGBA{R: 0x0A, G: 0x0D, B: 0x0B, A: 0xFF}
}

// inkBBoxInRange returns the bounding box of every pixel in img whose RGB
// differs from bg, restricted to rows [yMin, yMax). ok is false if no such
// pixel exists in that range.
func inkBBoxInRange(t *testing.T, img image.Image, bg color.Color, yMin, yMax int) (bbox image.Rectangle, ok bool) {
	t.Helper()
	bgR, bgG, bgB, _ := bg.RGBA()
	bounds := img.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	if yMin < bounds.Min.Y {
		yMin = bounds.Min.Y
	}
	if yMax > bounds.Max.Y {
		yMax = bounds.Max.Y
	}
	for y := yMin; y < yMax; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r == bgR && g == bgG && b == bgB {
				continue
			}
			ok = true
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if !ok {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), true
}

// cardCase is one table row for TestWorkshopCard_TableRendersReadableCards:
// a workshop and which content bands its rendering is expected to produce
// non-background ink in.
type cardCase struct {
	name     string
	workshop Workshop
	wantMeta bool // is cardMetaLine(workshop) non-empty for this case?
}

func cardTestCases() []cardCase {
	return []cardCase{
		{
			name: "normal",
			workshop: Workshop{
				Slug:         "intro-to-soldering",
				Title:        "Introduction to Soldering",
				StartsAt:     "2026-09-12T18:00:00Z",
				LocationName: "Open Circuit SF Workshop Space",
			},
			wantMeta: true,
		},
		{
			// Deliberately long enough to overflow cardMaxLines even at the
			// smallest rung of cardTitleSizeLadder, exercising the
			// wrap-then-ellipsize path (#0273's plan §4's "very long title"
			// acceptance case).
			name: "very long title",
			workshop: Workshop{
				Slug:         "advanced-soldering",
				Title:        "An Extremely Long and Overly Descriptive Workshop Title About Advanced Through-Hole and Surface-Mount Soldering Techniques for Absolute Beginners and Curious Hobbyists Alike Who Want To Learn Everything At Once and Then Some More Besides Because This Title Simply Refuses To End Any Time Soon At All",
				StartsAt:     "2026-10-01T18:00:00Z",
				LocationName: "Open Circuit SF",
			},
			wantMeta: true,
		},
		{
			// No StartsAt, no LocationName -- #0273's plan §4's "one with
			// none of the optional fields" acceptance case, and the exact
			// shape that exposed the "large empty band through the middle"
			// defect the spike found (see cardTitleTop/cardTitleBottom's
			// doc comment in card.go). cardMetaLine must return "" for this
			// workshop -- pinned separately below.
			name: "minimal fields",
			workshop: Workshop{
				Slug:  "mystery-workshop",
				Title: "Mystery Workshop",
			},
			wantMeta: false,
		},
	}
}

// TestWorkshopCard_TableRendersReadableCards is #0273's acceptance criterion
// 4's pixel-measured proof: dimensions, encoded size, safe-area containment,
// and per-band ink presence, asserted on the DECODED image rather than on
// the layout code, for all three cases plus the normal one. Criterion 4
// requires this be proved by rendering, not reasoning -- and the plan's own
// spike found a real defect this way, so this test also fails if that
// defect regresses.
func TestWorkshopCard_TableRendersReadableCards(t *testing.T) {
	cr := mustCardRenderer()
	bg := cardTestBackground()

	debugDir := os.Getenv(cardDebugDirEnv)

	for _, tc := range cardTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got := cardMetaLine(tc.workshop); (got != "") != tc.wantMeta {
				t.Fatalf("cardMetaLine(%+v) = %q, wantMeta=%v -- fixture doesn't match its own intent", tc.workshop, got, tc.wantMeta)
			}

			body, err := cr.render(tc.workshop)
			if err != nil {
				t.Fatalf("render: %v", err)
			}

			tmpPath := filepath.Join(t.TempDir(), tc.workshop.Slug+".png")
			if err := os.WriteFile(tmpPath, body, 0o644); err != nil {
				t.Fatalf("write rendered PNG to t.TempDir(): %v", err)
			}
			if debugDir != "" {
				if err := os.MkdirAll(debugDir, 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", debugDir, err)
				}
				if err := os.WriteFile(filepath.Join(debugDir, "0273-"+tc.workshop.Slug+".png"), body, 0o644); err != nil {
					t.Fatalf("write debug copy: %v", err)
				}
			}

			if kb := float64(len(body)) / 1024; kb >= 300 {
				t.Errorf("encoded PNG is %.1f KB, want < 300 KB", kb)
			}

			img, err := png.Decode(bytes.NewReader(body))
			if err != nil {
				t.Fatalf("decode rendered PNG: %v", err)
			}
			if got := img.Bounds(); got.Dx() != cardW || got.Dy() != cardH {
				t.Fatalf("decoded image is %dx%d, want %dx%d", got.Dx(), got.Dy(), cardW, cardH)
			}

			// Whole-image ink bounding box must lie wholly inside the safe
			// area -- no glyph (or the baked-in logo/rule) within
			// cardSafeInset px of any edge.
			full, ok := inkBBoxInRange(t, img, bg, 0, cardH)
			if !ok {
				t.Fatal("no non-background pixel found anywhere -- card rendered blank")
			}
			safe := image.Rect(cardSafeInset, cardSafeInset, cardW-cardSafeInset, cardH-cardSafeInset)
			if !full.In(safe) {
				t.Errorf("ink bounding box %v is not contained in the safe area %v", full, safe)
			}

			// Per-band presence: header (the baked-in logo -- always
			// present regardless of workshop content) and the title band
			// must always carry ink; the meta band only when this case has
			// a non-empty cardMetaLine; the command band always (the fixed
			// "$ opencircuitsf.com" line is unconditional).
			if _, ok := inkBBoxInRange(t, img, bg, 0, cardTitleTop); !ok {
				t.Error("header band (logo) has no ink -- base-card.png embedding may be broken")
			}
			if _, ok := inkBBoxInRange(t, img, bg, cardTitleTop, cardTitleBottom); !ok {
				t.Error("title band has no ink -- title failed to draw")
			}
			metaBBox, metaOK := inkBBoxInRange(t, img, bg, cardTitleBottom, cardRuleY)
			if metaOK != tc.wantMeta {
				t.Errorf("meta band ink present = %v (bbox %v), want %v", metaOK, metaBBox, tc.wantMeta)
			}
			if _, ok := inkBBoxInRange(t, img, bg, cardRuleY, cardH); !ok {
				t.Error("command band has no ink -- the fixed command line failed to draw")
			}
		})
	}
}

// TestWorkshopCard_MinimalFieldsTitleIsVerticallyCentered is the direct
// pixel-level proof that the "large empty band through the middle" defect
// the plan's spike found is fixed: for a workshop with neither StartsAt nor
// LocationName (so nothing draws in the meta band), the title block's own
// ink -- measured within the title band ONLY -- must be vertically centered
// near the band's midpoint, not pinned to a fixed offset that would leave
// the rest of the band empty.
func TestWorkshopCard_MinimalFieldsTitleIsVerticallyCentered(t *testing.T) {
	cr := mustCardRenderer()
	w := Workshop{Slug: "mystery-workshop", Title: "Mystery Workshop"}

	body, err := cr.render(w)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	bbox, ok := inkBBoxInRange(t, img, cardTestBackground(), cardTitleTop, cardTitleBottom)
	if !ok {
		t.Fatal("no ink found in the title band at all")
	}

	gotCenter := (bbox.Min.Y + bbox.Max.Y) / 2
	wantCenter := (cardTitleTop + cardTitleBottom) / 2
	const tolerance = 25 // px -- generous enough to absorb ascent/descent asymmetry, tight enough to catch a fixed-offset regression
	if diff := gotCenter - wantCenter; diff < -tolerance || diff > tolerance {
		t.Errorf("title ink vertical center = %d, band center = %d (band %d-%d) -- off by %d px, want within %d px; "+
			"a one-line title should sit near the middle of its band, not pinned to a fixed offset that leaves the rest empty",
			gotCenter, wantCenter, cardTitleTop, cardTitleBottom, diff, tolerance)
	}
}

// TestWorkshopCard_LongTitleWrapsAndEllipsizes pins layoutTitle's own
// contract directly (independent of the pixel-measured proof above): at
// most cardMaxLines lines, and if the title needed truncating, the last line
// ends in U+2026.
func TestWorkshopCard_LongTitleWrapsAndEllipsizes(t *testing.T) {
	cr := mustCardRenderer()
	maxWidth := fixed.I(cardW - 2*cardMargin)

	long := "An Extremely Long and Overly Descriptive Workshop Title About Advanced Through-Hole and Surface-Mount Soldering Techniques for Absolute Beginners and Curious Hobbyists Alike Who Want To Learn Everything At Once and Then Some More Besides Because This Title Simply Refuses To End Any Time Soon At All"
	layout := cr.layoutTitle(long, maxWidth)

	if len(layout.lines) == 0 {
		t.Fatal("layoutTitle returned zero lines")
	}
	if len(layout.lines) > cardMaxLines {
		t.Fatalf("layoutTitle produced %d lines, want <= %d", len(layout.lines), cardMaxLines)
	}
	last := layout.lines[len(layout.lines)-1]
	if !strings.HasSuffix(last, "…") {
		t.Errorf("last line %q does not end in U+2026 -- expected the long title to be truncated", last)
	}

	short := cr.layoutTitle("Introduction to Soldering", maxWidth)
	for _, line := range short.lines {
		if strings.HasSuffix(line, "…") {
			t.Errorf("short title line %q ends in U+2026, want no truncation", line)
		}
	}
	if len(short.lines) != 1 {
		t.Errorf("short title wrapped to %d lines, want 1", len(short.lines))
	}
}

// TestWorkshopRouteMetaAndCardHandler_StatusCrossProduct is #0273's plan §7
// table: workshopRouteMeta's og:image choice and WorkshopCardHandler's HTTP
// status must agree, for every workshop status × Published combination, plus
// an unknown slug and a nil WorkshopSource. This is what pins #0135's
// canceled-workshop ruling against a silent reversal -- the canceled row
// asserts BOTH that the rendered og:image contains "/og-default.png" AND
// that it does NOT contain "/og.png" (an assertion on the presence of the
// right URL alone would pass if both were emitted), and that the handler
// answers 404 rather than serving the generic PNG under a
// workshop-specific URL (criterion 8: that would itself leak the slug's
// existence).
func TestWorkshopRouteMetaAndCardHandler_StatusCrossProduct(t *testing.T) {
	source := fakeWorkshopSource{
		"published-ws": {
			Slug: "published-ws", Title: "Published Workshop", Status: WorkshopPublished, Published: true,
		},
		"canceled-published-ws": {
			Slug: "canceled-published-ws", Title: "Canceled Workshop", Status: WorkshopCanceled, Published: true,
		},
		"canceled-never-published-ws": {
			Slug: "canceled-never-published-ws", Title: "Never Published", Status: WorkshopCanceled, Published: false,
		},
		"draft-ws": {
			Slug: "draft-ws", Title: "Draft Workshop", Status: WorkshopDraft, Published: false,
		},
		"unpublished-ws": {
			Slug: "unpublished-ws", Title: "Unpublished Workshop", Status: WorkshopUnpublished, Published: true,
		},
	}

	site := NewSite([]byte(testTemplate), testBaseURL, source, nil)
	mux := http.NewServeMux()
	mux.Handle("GET /workshops/{slug}/og.png", site.WorkshopCardHandler())

	cases := []struct {
		name        string
		slug        string
		wantCardURL bool // og:image should contain the workshop-specific card URL
		wantStatus  int
	}{
		{name: "published and published-true", slug: "published-ws", wantCardURL: true, wantStatus: http.StatusOK},
		{name: "canceled but published-true (#0135)", slug: "canceled-published-ws", wantCardURL: false, wantStatus: http.StatusNotFound},
		{name: "canceled and never published", slug: "canceled-never-published-ws", wantCardURL: false, wantStatus: http.StatusNotFound},
		{name: "draft", slug: "draft-ws", wantCardURL: false, wantStatus: http.StatusNotFound},
		{name: "unpublished", slug: "unpublished-ws", wantCardURL: false, wantStatus: http.StatusNotFound},
		{name: "unknown slug", slug: "does-not-exist", wantCardURL: false, wantStatus: http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := string(site.renderer.Render("/workshops/" + tc.slug))
			cardURL := "/workshops/" + tc.slug + "/og.png"
			hasCardURL := strings.Contains(body, cardURL)
			hasDefault := strings.Contains(body, "/og-default.png")

			if tc.wantCardURL {
				if !hasCardURL {
					t.Errorf("rendered meta missing card URL %q", cardURL)
				}
				if hasDefault {
					t.Errorf("rendered meta contains /og-default.png for a workshop that should get its own card")
				}
			} else {
				if hasCardURL {
					t.Errorf("rendered meta contains card URL %q, want the generic fallback only (#0135)", cardURL)
				}
				if !hasDefault {
					t.Error("rendered meta missing /og-default.png fallback")
				}
			}

			req := httptest.NewRequest(http.MethodGet, "/workshops/"+tc.slug+"/og.png", nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("GET %s = %d, want %d", req.URL.Path, rec.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusOK {
				if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
					t.Errorf("Content-Type = %q, want image/png", ct)
				}
				if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
					t.Errorf("Cache-Control = %q, want %q", cc, "public, max-age=3600")
				}
				etag := rec.Header().Get("ETag")
				if etag == "" {
					t.Error("ETag header missing")
				}

				// A second request carrying If-None-Match must 304.
				req2 := httptest.NewRequest(http.MethodGet, "/workshops/"+tc.slug+"/og.png", nil)
				req2.Header.Set("If-None-Match", etag)
				rec2 := httptest.NewRecorder()
				mux.ServeHTTP(rec2, req2)
				if rec2.Code != http.StatusNotModified {
					t.Errorf("GET with If-None-Match = %d, want %d", rec2.Code, http.StatusNotModified)
				}
			}
		})
	}

	t.Run("nil WorkshopSource", func(t *testing.T) {
		nilSite := NewSite([]byte(testTemplate), testBaseURL, nil, nil)
		nilMux := http.NewServeMux()
		nilMux.Handle("GET /workshops/{slug}/og.png", nilSite.WorkshopCardHandler())

		body := string(nilSite.renderer.Render("/workshops/anything"))
		if !strings.Contains(body, "/og-default.png") {
			t.Error("rendered meta with nil WorkshopSource missing /og-default.png fallback")
		}

		req := httptest.NewRequest(http.MethodGet, "/workshops/anything/og.png", nil)
		rec := httptest.NewRecorder()
		nilMux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET .../og.png with nil WorkshopSource = %d, want 404", rec.Code)
		}
	})
}

// TestWorkshopCoverImageStillWins pins #0273's plan explicit requirement
// that cover_image, when set, keeps taking precedence over the generated
// card -- unchanged from the pre-#0273 behavior.
func TestWorkshopCoverImageStillWins(t *testing.T) {
	source := fakeWorkshopSource{
		"has-cover": {
			Slug: "has-cover", Title: "Has A Cover Image", Status: WorkshopPublished, Published: true,
			CoverImage: "/soldering.jpg",
		},
	}
	site := NewSite([]byte(testTemplate), testBaseURL, source, nil)
	body := string(site.renderer.Render("/workshops/has-cover"))
	if !strings.Contains(body, testBaseURL+"/soldering.jpg") {
		t.Errorf("rendered meta does not contain the workshop's own cover_image URL: %s", body)
	}
	if strings.Contains(body, "/workshops/has-cover/og.png") {
		t.Error("rendered meta contains the generated card URL even though cover_image was set")
	}
}

// TestCardCache_CachesAndInvalidates proves cardCache actually caches
// (rather than merely being idempotent) by mutating the workshop's Title
// between two get() calls under the SAME slug: a real cache hit must return
// the FIRST render's bytes unchanged, and only after invalidate() does a
// subsequent get() reflect the new title.
func TestCardCache_CachesAndInvalidates(t *testing.T) {
	cache := newCardCache(mustCardRenderer())

	w := Workshop{Slug: "cache-demo", Title: "Original Title"}
	body1, etag1, err := cache.get(w)
	if err != nil {
		t.Fatalf("get (miss): %v", err)
	}

	w.Title = "Completely Different Title Entirely"
	body2, etag2, err := cache.get(w)
	if err != nil {
		t.Fatalf("get (should be a hit): %v", err)
	}
	if !bytes.Equal(body1, body2) || etag1 != etag2 {
		t.Fatal("get() with a mutated Title but the same Slug returned different bytes -- cache did not hit, it re-rendered")
	}

	cache.invalidate()
	body3, etag3, err := cache.get(w)
	if err != nil {
		t.Fatalf("get (after invalidate): %v", err)
	}
	if bytes.Equal(body1, body3) || etag1 == etag3 {
		t.Fatal("get() after invalidate() returned the stale pre-invalidate bytes -- invalidate() did not clear the cache")
	}
}
