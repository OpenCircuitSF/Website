package seo

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// This file builds #0273's per-workshop Open Graph card: a 1200x630 PNG with
// the workshop's own title, date, and venue, so three different workshops
// shared to the same Slack/iMessage/Bluesky/Mastodon channel no longer look
// identical (see the fallback this replaces, seo.go's workshopRouteMeta).
//
// Design, from #0273's phase-1 plan (measured in a throwaway spike, not
// assumed): a pre-rendered base image -- the dark ground, the tinted logo
// mark, and one rule that never moves -- decoded once at Site construction,
// composited under request-time text drawn with
// golang.org/x/image/font/opentype. golang.org/x/image/font/sfnt (which
// opentype wraps) can neither read .woff2 nor render a variable font at a
// specific weight, so the two embedded TTFs below are a one-time conversion
// of the project's self-hosted brand faces (web/public/fonts/), performed by
// assets/og/build-card-fonts.py -- the same fontTools _woff2_to_ttf step
// assets/og/build-og.py already runs at request time for the generic
// og-default.png. assets/og/build-card-base.py produces base-card.png the
// same way. Regenerate either by running the corresponding script and
// re-committing its output; go:embed cannot read a path outside this
// package's own directory, which is why both outputs live under
// internal/seo/cardassets/ rather than beside their .woff2 sources.
//
// Every font.Face is parsed and built once here, not per request (the spike
// this plan came from re-parsed on every render; production must not) --
// golang.org/x/image/font's Face and Drawer are both documented as unsafe
// for concurrent use, which is why every render in this file happens under
// cardCache's single mutex (site.go's WorkshopCardHandler is the only
// caller).

//go:embed cardassets/base-card.png
var cardBasePNG []byte

//go:embed cardassets/archivo-800.ttf
var cardArchivoTTF []byte

//go:embed cardassets/jetbrains-mono-400.ttf
var cardMonoTTF []byte

// Card geometry. cardMargin and cardRuleY are shared with
// assets/og/build-card-base.py -- see that script's doc comment for why they
// are kept in sync by hand rather than generated from one another.
const (
	cardW = 1200
	cardH = 630

	cardMargin = 70  // must match build-card-base.py's MARGIN
	cardRuleY  = 540 // baked into base-card.png; must match build-card-base.py's RULE_Y

	// cardTitleTop/cardTitleBottom bound the band the title block is
	// vertically centered within (see layoutTitle/drawTitle). Centering
	// here -- rather than a fixed baseline -- is #0273's plan's fix for the
	// defect its own spike found by rendering: a minimal-fields workshop
	// (no date, no venue) left a large empty band through the card's
	// middle when the title was pinned to a fixed y regardless of line
	// count.
	cardTitleTop    = 220
	cardTitleBottom = 480
	cardMaxLines    = 3

	cardMetaY    = 505 // baseline y for the "date · venue" line
	cardCommandY = 575 // baseline y for the fixed "$ opencircuitsf.com" line

	cardMetaSize    = 26.0 // JetBrains Mono point size, date/venue line
	cardCommandSize = 30.0 // JetBrains Mono point size, command line
)

// cardTitleSizeLadder is the descending Archivo 800 point-size ladder the
// title shrinks through before word-wrapping to cardMaxLines lines (#0273's
// plan §4: "shrink-to-fit across a descending size ladder, then greedy
// word-wrap ... then ellipsize the last line").
var cardTitleSizeLadder = []float64{64, 56, 48, 42}

// cardCommandText is the fixed command-prompt line every card carries,
// regardless of workshop content.
const cardCommandText = "$ opencircuitsf.com"

var (
	cardTextColor  = color.RGBA{R: 0xE8, G: 0xF0, B: 0xE8, A: 0xFF}
	cardMutedColor = color.RGBA{R: 0x9A, G: 0xA7, B: 0x9E, A: 0xFF}
	// cardGreenColor is brand green at its 14.8:1-on-dark-ground
	// measurement (CLAUDE.md §8) -- the card's ground is always the dark
	// brand color, so this never hits the 1.32:1-on-white case that forces
	// a tinted variant elsewhere in the site.
	cardGreenColor = color.RGBA{R: 0x68, G: 0xFF, B: 0x23, A: 0xFF}
)

// cardRenderer holds the decoded base image and every font.Face card
// rendering needs, built once by newCardRenderer. Not safe for concurrent
// use on its own -- see this file's package doc comment -- callers must hold
// cardCache's mutex.
type cardRenderer struct {
	base image.Image

	titleFaces  map[float64]font.Face // keyed by cardTitleSizeLadder entries
	metaFace    font.Face
	commandFace font.Face
}

// newCardRenderer decodes the embedded base card and parses both embedded
// fonts exactly once, building every font.Face this package's fixed layout
// needs. Every input is compile-time-embedded and fixed at commit time, so a
// non-nil error here means a corrupted commit, not a runtime condition --
// mustCardRenderer below is the only caller and panics on one, the same
// "fail loudly at startup, never silently" posture regexp.MustCompile and
// template.Must already use elsewhere in this codebase.
func newCardRenderer() (*cardRenderer, error) {
	baseImg, err := png.Decode(bytes.NewReader(cardBasePNG))
	if err != nil {
		return nil, fmt.Errorf("card: decode embedded cardassets/base-card.png: %w", err)
	}
	if baseImg.Bounds().Dx() != cardW || baseImg.Bounds().Dy() != cardH {
		return nil, fmt.Errorf("card: embedded base-card.png is %dx%d, want %dx%d",
			baseImg.Bounds().Dx(), baseImg.Bounds().Dy(), cardW, cardH)
	}

	archivo, err := opentype.Parse(cardArchivoTTF)
	if err != nil {
		return nil, fmt.Errorf("card: parse embedded cardassets/archivo-800.ttf: %w", err)
	}
	mono, err := opentype.Parse(cardMonoTTF)
	if err != nil {
		return nil, fmt.Errorf("card: parse embedded cardassets/jetbrains-mono-400.ttf: %w", err)
	}

	titleFaces := make(map[float64]font.Face, len(cardTitleSizeLadder))
	for _, size := range cardTitleSizeLadder {
		f, err := opentype.NewFace(archivo, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, fmt.Errorf("card: build archivo face at %.0fpt: %w", size, err)
		}
		titleFaces[size] = f
	}
	metaFace, err := opentype.NewFace(mono, &opentype.FaceOptions{Size: cardMetaSize, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, fmt.Errorf("card: build mono meta face: %w", err)
	}
	commandFace, err := opentype.NewFace(mono, &opentype.FaceOptions{Size: cardCommandSize, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, fmt.Errorf("card: build mono command face: %w", err)
	}

	return &cardRenderer{
		base:        baseImg,
		titleFaces:  titleFaces,
		metaFace:    metaFace,
		commandFace: commandFace,
	}, nil
}

// mustCardRenderer is newCardRenderer, panicking on error -- see that
// function's doc comment for why that is the right posture for a set of
// compile-time-fixed, go:embed'd inputs. The only caller is newCardCache
// (site.go's NewSite).
func mustCardRenderer() *cardRenderer {
	cr, err := newCardRenderer()
	if err != nil {
		panic(err)
	}
	return cr
}

// titleLayout is layoutTitle's result: the wrapped lines and the face they
// were measured and will be drawn with.
type titleLayout struct {
	lines []string
	face  font.Face
}

// layoutTitle picks the largest size on cardTitleSizeLadder that wraps title
// to at most cardMaxLines lines, greedily word-wrapping at each candidate
// size. If even the smallest size still overflows, the wrapped result is
// hard-truncated to cardMaxLines lines and the last line is ellipsized
// (#0273's plan §4) -- this is the "very long title" acceptance case,
// pinned by card_test.go against a real rendered PNG.
func (cr *cardRenderer) layoutTitle(title string, maxWidth fixed.Int26_6) titleLayout {
	title = strings.TrimSpace(title)
	if title == "" {
		// Defensive only -- the workshops store requires a non-empty title
		// (internal/workshops), so a real request should never reach this,
		// but an empty title band would be exactly the "large empty
		// middle" defect this plan's layout was built to avoid.
		title = "Open Circuit SF"
	}

	var last titleLayout
	for _, size := range cardTitleSizeLadder {
		face := cr.titleFaces[size]
		d := &font.Drawer{Face: face}
		lines := wrapText(d, title, maxWidth)
		last = titleLayout{lines: lines, face: face}
		if len(lines) <= cardMaxLines {
			return last
		}
	}

	d := &font.Drawer{Face: last.face}
	lines := append([]string(nil), last.lines[:cardMaxLines]...)
	// The kept line already fits maxWidth (wrapText's own invariant), so a
	// plain ellipsize (which only trims when a string is TOO WIDE) would be
	// a no-op here -- forceEllipsis instead always signals that real
	// content was dropped after this line, trimming only as much as
	// appending "…" itself requires.
	lines[cardMaxLines-1] = forceEllipsis(d, lines[cardMaxLines-1], maxWidth)
	last.lines = lines
	return last
}

// wrapText greedily word-wraps text into lines no wider than maxWidth under
// d.Face, measured with d.MeasureString. A single word wider than maxWidth
// on its own is left on its own line unmodified -- workshop titles are
// ordinary admin-authored English, so this does not arise in practice, and
// ellipsize (applied only to the final line after layoutTitle's
// cardMaxLines truncation) is what keeps the card's actual output within
// its safe area regardless.
func wrapText(d *font.Drawer, text string, maxWidth fixed.Int26_6) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := words[0]
	for _, word := range words[1:] {
		cand := cur + " " + word
		if d.MeasureString(cand) <= maxWidth {
			cur = cand
			continue
		}
		lines = append(lines, cur)
		cur = word
	}
	lines = append(lines, cur)
	return lines
}

// ellipsize returns s unchanged if it already fits maxWidth under d.Face, or
// s trimmed rune-by-rune from the end with a trailing U+2026 appended,
// otherwise. Used for the meta line, where truncation is conditional on
// actual overflow -- see forceEllipsis for the title's truncation case,
// where an ellipsis must appear even though the kept line already fits.
func ellipsize(d *font.Drawer, s string, maxWidth fixed.Int26_6) string {
	if d.MeasureString(s) <= maxWidth {
		return s
	}
	return forceEllipsis(d, s, maxWidth)
}

// forceEllipsis always appends a trailing U+2026 to s (trimmed rune-by-rune
// from the end only as much as fitting the ellipsis itself under maxWidth
// requires), regardless of whether s alone already fits. layoutTitle uses
// this for the line kept at the cardMaxLines truncation boundary: that line
// already fits maxWidth by construction (wrapText never produces an
// overflowing line), so a conditional ellipsize would be a no-op there even
// though real content -- the rest of the title -- was genuinely dropped
// after it.
func forceEllipsis(d *font.Drawer, s string, maxWidth fixed.Int26_6) string {
	runes := []rune(s)
	for {
		cand := strings.TrimRight(string(runes), " ") + "…"
		if d.MeasureString(cand) <= maxWidth || len(runes) == 0 {
			return cand
		}
		runes = runes[:len(runes)-1]
	}
}

// cardMetaLine builds the "date · venue" line from w.StartsAt/w.LocationName,
// both of which are independently optional (Workshop's own doc comment).
// Per #0273's plan §4, the missing half and its separator are simply
// omitted rather than a placeholder like the UI's own "Location TBA"
// (workshopLocationLabel in web/src/lib/workshops.ts) -- that copy is aimed
// at a human reading a page that also shows the workshop's real state
// elsewhere; a share card has no such context, and a fabricated-looking
// placeholder on an otherwise-terse card reads worse than omitting the line
// segment entirely. Returns "" (draw nothing, see cardRenderer.render) when
// neither is set.
func cardMetaLine(w Workshop) string {
	date := formatCardDate(w.StartsAt)
	venue := strings.TrimSpace(w.LocationName)
	switch {
	case date != "" && venue != "":
		return date + "  ·  " + venue
	case date != "":
		return date
	case venue != "":
		return venue
	default:
		return ""
	}
}

// cardLocation is the timezone the card's date/venue line renders in.
//
// # Pacific, zone-labeled -- not UTC, and not per-request (#0273 review pass,
// amending this file's original UTC rendering)
//
// The workshops this card advertises are physical, in-person events in the
// Bay Area (#0144's own reasoning, restated here because this is the same
// defect on a more public surface): the correct zone is the venue's, which
// is a fixed site-level fact, not something read from a viewer's request --
// there is no viewer request here at all, since this handler is fetched by
// unfurler crawlers, not browsers. #0144 already settled this exact question
// for the analogous server-side surface, the announce email body
// (internal/handlers/admin_workshop_announce.go's announceLocation), and
// this mirrors that pattern rather than inventing a second one: one named
// var, loaded once at package init via time/tzdata (already imported by
// cmd/opencircuit/main.go, #0156), so a future WORKSHOP_TIMEZONE config
// field would be a local change here, not a repo-wide search.
var cardLocation = mustCardLocation("America/Los_Angeles")

func mustCardLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		// Only reachable if the Go toolchain's tzdata is missing/corrupt --
		// every card would be broken regardless, so fail loud at package
		// init rather than silently rendering the wrong time on every
		// Slack/iMessage/Bluesky/Mastodon unfurl.
		panic(fmt.Sprintf("seo: load card location %q: %v", name, err))
	}
	return loc
}

// formatCardDate formats w.StartsAt (RFC 3339 with a UTC offset -- Workshop's
// own doc comment) for display on the card, in cardLocation with the zone
// abbreviation labeled. The "MST" reference field is Go's placeholder for
// "the zone abbreviation of whatever *time.Location the time.Time was
// converted into" (see announceDateLayout's doc comment, the precedent this
// mirrors) -- it renders "PDT"/"PST" here, not Mountain time. Returns "" for
// an empty or unparseable input, matching buildEvent's own "not enough data"
// posture (jsonld.go) rather than printing a broken string.
func formatCardDate(startsAt string) string {
	if startsAt == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		return ""
	}
	return t.In(cardLocation).Format("Jan 2, 2006, 3:04 PM MST")
}

// render composes and PNG-encodes w's card: the embedded base image, the
// vertically-centered title block, the optional date/venue line, and the
// fixed command line. Not safe for concurrent use -- see this file's package
// doc comment -- cardCache.get is the only caller and holds its own mutex
// across this call.
func (cr *cardRenderer) render(w Workshop) ([]byte, error) {
	dst := image.NewRGBA(image.Rect(0, 0, cardW, cardH))
	draw.Draw(dst, dst.Bounds(), cr.base, image.Point{}, draw.Src)

	maxWidth := fixed.I(cardW - 2*cardMargin)

	layout := cr.layoutTitle(w.Title, maxWidth)
	cr.drawTitle(dst, layout)

	if meta := cardMetaLine(w); meta != "" {
		cr.drawMeta(dst, meta, maxWidth)
	}
	cr.drawCommand(dst)

	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("card: encode png for slug %q: %w", w.Slug, err)
	}
	return buf.Bytes(), nil
}

// drawTitle draws layout's lines left-aligned at cardMargin, with the whole
// block vertically centered between cardTitleTop and cardTitleBottom -- see
// those constants' doc comment for why centering (not a fixed baseline) is
// load-bearing here.
func (cr *cardRenderer) drawTitle(dst *image.RGBA, layout titleLayout) {
	metrics := layout.face.Metrics()
	lineHeight := metrics.Height
	blockHeight := lineHeight * fixed.Int26_6(len(layout.lines))

	bandTop := fixed.I(cardTitleTop)
	bandHeight := fixed.I(cardTitleBottom - cardTitleTop)
	startY := bandTop + (bandHeight-blockHeight)/2 + metrics.Ascent

	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(cardTextColor),
		Face: layout.face,
	}
	y := startY
	for _, line := range layout.lines {
		d.Dot = fixed.Point26_6{X: fixed.I(cardMargin), Y: y}
		d.DrawString(line)
		y += lineHeight
	}
}

// drawMeta draws the date/venue line, ellipsizing it to maxWidth first --
// a very long venue name is possible even though a very long title is the
// acceptance criterion's named case.
func (cr *cardRenderer) drawMeta(dst *image.RGBA, text string, maxWidth fixed.Int26_6) {
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(cardMutedColor),
		Face: cr.metaFace,
	}
	d.Dot = fixed.Point26_6{X: fixed.I(cardMargin), Y: fixed.I(cardMetaY)}
	d.DrawString(ellipsize(d, text, maxWidth))
}

// drawCommand draws the fixed "$ opencircuitsf.com" line every card carries.
func (cr *cardRenderer) drawCommand(dst *image.RGBA) {
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(cardGreenColor),
		Face: cr.commandFace,
	}
	d.Dot = fixed.Point26_6{X: fixed.I(cardMargin), Y: fixed.I(cardCommandY)}
	d.DrawString(cardCommandText)
}

// cardCacheEntry is one rendered card, cached alongside the ETag computed
// from its own bytes so WorkshopCardHandler (site.go) never has to hash the
// same body twice.
type cardCacheEntry struct {
	body []byte
	etag string
}

// maxCardCacheEntries bounds cardCache the same way Renderer's
// maxCacheEntries bounds its own cache (seo.go) -- a full flush on overflow
// rather than partial eviction, for the identical reason: re-rendering is
// cheap (measured in #0273's plan: 8-13ms) and a hard cap is simpler to
// reason about than recency bookkeeping. The realistic keyspace is the
// published-workshop catalog: WorkshopCardHandler only ever renders for
// Renderer.cardableWorkshop's published-and-actually-published gate, so
// unlike Renderer's own notfound/fallback buckets this cache is never
// attacker-controlled by requesting distinct nonexistent paths. 64 is ample
// headroom above a three-workshop catalog.
const maxCardCacheEntries = 64

// cardCache caches rendered per-workshop card PNGs, keyed by slug, and
// serializes every render behind its own mutex -- see this file's package
// doc comment for why that serialization is required (golang.org/x/image/font
// Face/Drawer are not safe for concurrent use), not just an optimization.
type cardCache struct {
	mu      sync.Mutex
	entries map[string]cardCacheEntry
	render  *cardRenderer
}

func newCardCache(render *cardRenderer) *cardCache {
	return &cardCache{entries: make(map[string]cardCacheEntry), render: render}
}

// get returns w's cached card body and ETag, rendering and caching on a
// miss. A burst of concurrent first-requests for the same slug renders once:
// every caller blocks on the same mutex, and the first to acquire it
// populates the cache for the rest.
func (c *cardCache) get(w Workshop) ([]byte, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[w.Slug]; ok {
		return e.body, e.etag, nil
	}

	body, err := c.render.render(w)
	if err != nil {
		return nil, "", err
	}
	etag := cardETag(body)

	if _, exists := c.entries[w.Slug]; !exists && len(c.entries) >= maxCardCacheEntries {
		c.entries = make(map[string]cardCacheEntry)
	}
	c.entries[w.Slug] = cardCacheEntry{body: body, etag: etag}
	return body, etag, nil
}

// invalidate clears every cached card. Deliberately unexported, mirroring
// Sitemap.invalidate (#0337) -- CLAUDE.md §8's satisfier-set entry is why:
// an exported Invalidate() here would structurally (if accidentally) satisfy
// handlers.seoCacheInvalidator and mailing.ArchiveCacheInvalidator, both
// single-method interfaces requiring exactly that shape. See
// invalidator_satisfier_guard_test.go's TestInvalidatorSatisfierSet, whose
// `want` map carries "cardCache": false for exactly this reason.
func (c *cardCache) invalidate() {
	c.mu.Lock()
	c.entries = make(map[string]cardCacheEntry)
	c.mu.Unlock()
}

// cardETag computes a strong ETag from body's own bytes -- SHA-256 rather
// than a weaker/faster hash, since this runs once per cache miss (bounded by
// the published-workshop catalog size, not per request) rather than on every
// request.
func cardETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}
