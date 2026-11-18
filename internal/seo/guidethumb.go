package seo

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"log"
	"net/http"

	"golang.org/x/image/draw"

	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
)

// The /guides card thumbnail (#0710): the 1200x630 card scaled exactly to
// 640x336, which covers a ~320 CSS px card at 2x DPR.
const (
	guideThumbW       = 640
	guideThumbH       = 336
	guideThumbQuality = 80
	// guideThumbMaxBytes is the per-thumbnail ceiling the embedded-set test
	// enforces, with headroom over the measured 24,541 B maximum.
	guideThumbMaxBytes = 40_000
)

type guideThumb struct {
	data []byte
	etag string
}

// guideThumb returns the JPEG thumbnail of the bytes GuideCard returns for
// slug, generating it once and caching it. Both the art and the generated
// card are fixed per slug for the life of the binary, so the cache never
// needs invalidating. thumbMu serialises generation so a cold burst decodes
// one image at a time.
func (s *Site) guideThumb(slug string) (guideThumb, error) {
	s.thumbMu.Lock()
	defer s.thumbMu.Unlock()
	if t, ok := s.thumbs[slug]; ok {
		return t, nil
	}
	src, _, err := s.GuideCard(slug)
	if err != nil {
		return guideThumb{}, err
	}
	img, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return guideThumb{}, fmt.Errorf("seo: decode guide card %q: %w", slug, err)
	}
	dst := image.NewRGBA(image.Rect(0, 0, guideThumbW, guideThumbH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: guideThumbQuality}); err != nil {
		return guideThumb{}, fmt.Errorf("seo: encode guide thumb %q: %w", slug, err)
	}
	t := guideThumb{data: buf.Bytes(), etag: cardETag(buf.Bytes())}
	s.thumbs[slug] = t
	return t, nil
}

// GuideThumbHandler serves GET /guides/{slug}/thumb.jpg (#0710): the /guides
// card image, a small JPEG made from the same bytes GuideCardHandler serves
// (the hand-made art, else the generated title card). A slug outside the
// catalog is a 404.
func (s *Site) GuideThumbHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g, ok := guides.Lookup(r.PathValue("slug"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		t, err := s.guideThumb(g.Slug)
		if err != nil {
			log.Printf("seo: guide thumb %q: %v", g.Slug, err)
			http.Error(w, "failed to render thumbnail", http.StatusInternalServerError)
			return
		}
		writeImage(w, r, "image/jpeg", t.data, t.etag)
	}
}
