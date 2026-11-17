package seo

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/png" // registers the PNG decoder image.DecodeConfig uses
	"io/fs"
	"path"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
)

// guideArtFS holds the hand-made per-guide unfurl images (#0709), one
// <slug>.png per guide under cardassets/guides. The all: prefix plus the
// committed .gitkeep lets the package compile while no image exists yet.
//
//go:embed all:cardassets/guides
var guideArtFS embed.FS

// guideArtMaxBytes caps one image's size. It equals Bluesky's thumbnail
// limit (bluesky.MaxThumbBytes, pinned by a test), since the same bytes
// become a guide post's thumbnail (#0658).
const guideArtMaxBytes = 1_000_000

// guideArt is one loaded image: its bytes, the strong ETag served with it
// (cardETag's shape), and version, the first 8 hex digits of its SHA-256,
// appended to og:image as ?v= so a replaced image gets a new URL.
type guideArt struct {
	data    []byte
	etag    string
	version string
}

// defaultGuideArt is the embedded set. Bad embedded content is a build
// defect, so it panics, like guides.mustLoad.
var defaultGuideArt = mustLoadGuideArt()

func mustLoadGuideArt() map[string]guideArt {
	m, err := loadGuideArt(guideArtFS, "cardassets/guides")
	if err != nil {
		panic(err)
	}
	return m
}

// loadGuideArt reads every <slug>.png in dir, skipping dotfiles. Each must
// name a catalog guide, decode as a cardW x cardH PNG, and fit within
// guideArtMaxBytes; any violation is an error naming the file.
func loadGuideArt(fsys fs.FS, dir string) (map[string]guideArt, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("seo: guide art: %w", err)
	}
	m := make(map[string]guideArt)
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		slug, ok := strings.CutSuffix(name, ".png")
		if !ok || e.IsDir() {
			return nil, fmt.Errorf("seo: guide art %s: must be <slug>.png", name)
		}
		if _, ok := guides.Lookup(slug); !ok {
			return nil, fmt.Errorf("seo: guide art %s: no guide with slug %q", name, slug)
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("seo: guide art %s: %w", name, err)
		}
		if len(data) > guideArtMaxBytes {
			return nil, fmt.Errorf("seo: guide art %s: %d bytes, over the %d-byte limit", name, len(data), guideArtMaxBytes)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || format != "png" {
			return nil, fmt.Errorf("seo: guide art %s: not a PNG", name)
		}
		if cfg.Width != cardW || cfg.Height != cardH {
			return nil, fmt.Errorf("seo: guide art %s: %dx%d, must be %dx%d", name, cfg.Width, cfg.Height, cardW, cardH)
		}
		sum := sha256.Sum256(data)
		m[slug] = guideArt{data: data, etag: cardETag(data), version: hex.EncodeToString(sum[:])[:8]}
	}
	return m, nil
}

// guideOGImage is a guide page's og:image: its /guides/{slug}/og.png URL,
// with ?v=<version> when hand-made art exists (#0709) so link-preview
// caches keyed on the URL pick up new or replaced art.
func guideOGImage(baseURL, slug string, art map[string]guideArt) string {
	u := baseURL + "/guides/" + slug + "/og.png"
	if a, ok := art[slug]; ok {
		u += "?v=" + a.version
	}
	return u
}
