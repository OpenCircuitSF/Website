package seo

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"testing/fstest"

	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
)

var thumbFixtureColor = color.RGBA{R: 200, G: 40, B: 90, A: 255}

func solidPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, cardW, cardH))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = thumbFixtureColor.R, thumbFixtureColor.G, thumbFixtureColor.B, 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// thumbFixture returns a mux serving thumbnails, with art for the first
// catalog guide only, and the first two slugs.
func thumbFixture(t *testing.T) (*http.ServeMux, string, string) {
	t.Helper()
	all := guides.All()
	a, b := all[0].Slug, all[1].Slug
	art, err := loadGuideArt(fstest.MapFS{"d/" + a + ".png": {Data: solidPNG(t)}}, "d")
	if err != nil {
		t.Fatal(err)
	}
	site := newSite([]byte(testTemplate), testBaseURL, nil, nil, art)
	mux := http.NewServeMux()
	mux.Handle("GET /guides/{slug}/thumb.jpg", site.GuideThumbHandler())
	return mux, a, b
}

func getThumb(mux *http.ServeMux, path, inm string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if inm != "" {
		req.Header.Set("If-None-Match", inm)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeThumb(t *testing.T, body []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("not a JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != guideThumbW || b.Dy() != guideThumbH {
		t.Fatalf("thumb is %dx%d, want %dx%d", b.Dx(), b.Dy(), guideThumbW, guideThumbH)
	}
	return img
}

func near(a, b uint8) bool { d := int(a) - int(b); return d > -12 && d < 12 }

func TestGuideThumb_FromArt(t *testing.T) {
	mux, a, _ := thumbFixture(t)
	rec := getThumb(mux, "/guides/"+a+"/thumb.jpg", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct, cc := rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"); ct != "image/jpeg" || cc != "public, max-age=3600" {
		t.Errorf("headers: Content-Type %q, Cache-Control %q", ct, cc)
	}
	img := decodeThumb(t, rec.Body.Bytes())
	r, g, b, _ := img.At(guideThumbW/2, guideThumbH/2).RGBA()
	if !near(uint8(r>>8), thumbFixtureColor.R) || !near(uint8(g>>8), thumbFixtureColor.G) || !near(uint8(b>>8), thumbFixtureColor.B) {
		t.Errorf("centre pixel = %d,%d,%d, want near %v", r>>8, g>>8, b>>8, thumbFixtureColor)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	if rec2 := getThumb(mux, "/guides/"+a+"/thumb.jpg", etag); rec2.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: status %d, want 304", rec2.Code)
	}
	if rec3 := getThumb(mux, "/guides/"+a+"/thumb.jpg", ""); !bytes.Equal(rec3.Body.Bytes(), rec.Body.Bytes()) {
		t.Error("second GET returned different bytes")
	}
}

func TestGuideThumb_FallbackIsTitleCard(t *testing.T) {
	mux, a, b := thumbFixture(t)
	recA := getThumb(mux, "/guides/"+a+"/thumb.jpg", "")
	recB := getThumb(mux, "/guides/"+b+"/thumb.jpg", "")
	if recB.Code != http.StatusOK {
		t.Fatalf("status %d", recB.Code)
	}
	decodeThumb(t, recB.Body.Bytes())
	if bytes.Equal(recA.Body.Bytes(), recB.Body.Bytes()) {
		t.Error("art-less guide served the same bytes as the art guide")
	}
}

func TestGuideThumb_UnknownSlug404(t *testing.T) {
	mux, _, _ := thumbFixture(t)
	if rec := getThumb(mux, "/guides/nope/thumb.jpg", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

// TestGuideThumb_EmbeddedSetWeight builds every embedded art's thumbnail and
// holds each under guideThumbMaxBytes, so a re-encode or quality bump cannot
// quietly make /guides heavy. Its log line is the measured page weight.
func TestGuideThumb_EmbeddedSetWeight(t *testing.T) {
	site := NewSite([]byte(testTemplate), testBaseURL, nil, nil)
	slugs := make([]string, 0, len(defaultGuideArt))
	for s := range defaultGuideArt {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	total, largest := 0, 0
	for _, s := range slugs {
		th, err := site.guideThumb(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		n := len(th.data)
		if n > guideThumbMaxBytes {
			t.Errorf("%s thumbnail is %d bytes, over %d", s, n, guideThumbMaxBytes)
		}
		total += n
		if n > largest {
			largest = n
		}
	}
	t.Logf("guide thumbnails: %d images, %d bytes total, largest %d", len(slugs), total, largest)
}
