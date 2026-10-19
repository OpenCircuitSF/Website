package outreach

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/image/draw"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/media"
)

// Why a card goes out without a picture. Each is short, plain text the admin
// preview shows as is (#0601).
const (
	ThumbReasonNoCover     = "No picture: this workshop has no cover image."
	ThumbReasonNotUploaded = "No picture: the cover is not an uploaded image, so the card cannot use it."
	ThumbReasonNoStorage   = "No picture: this server has no image folder set up."
	ThumbReasonMissing     = "No picture: the cover file was not found on the server."
	ThumbReasonFormat      = "No picture: only JPEG and PNG covers can go on the card."
	ThumbReasonTooBig      = "No picture: the cover could not be made small enough (under 1 MB)."
)

const (
	// maxThumbSourceBytes bounds how much of a cover file is ever read; a
	// larger file gets no picture.
	maxThumbSourceBytes = 25 << 20
	// thumbMinEdge stops the shrinking: below this a card picture is useless.
	thumbMinEdge = 200
	// thumbCacheMax bounds the shrunk results held in memory (about 1 MB each).
	thumbCacheMax = 8
)

// thumbQualities are tried in order at each size, best first.
var thumbQualities = []int{85, 75, 65, 50}

type thumbKey struct {
	path  string
	size  int64
	mtime int64
}

type thumbResult struct {
	data   []byte
	mime   string
	reason string
}

// thumbCache holds shrunk covers keyed on path, size and mtime, so the
// preview's frequent GETs do not re-decode a large JPEG each time. Zero value
// is ready to use.
type thumbCache struct {
	mu sync.Mutex
	m  map[thumbKey]thumbResult
}

func (c *thumbCache) get(k thumbKey) (thumbResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.m[k]
	return r, ok
}

func (c *thumbCache) put(k thumbKey, r thumbResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= thumbCacheMax {
		c.m = map[thumbKey]thumbResult{}
	}
	c.m[k] = r
}

// ThumbOK reports whether a post for a workshop with this cover would go out
// with a thumbnail (#0593): the same check the send makes, so the admin
// preview drops the picture exactly when the publisher does. A nil publisher
// attaches nothing.
func (p *Publisher) ThumbOK(cover *string) bool {
	ok, _ := p.ThumbStatus(cover)
	return ok
}

// ThumbStatus is ThumbOK plus, when the answer is no, the plain reason (#0601).
func (p *Publisher) ThumbStatus(cover *string) (bool, string) {
	if p == nil {
		return false, ""
	}
	data, _, reason := p.thumbnail(cover)
	return data != nil, reason
}

// thumbnail loads a workshop cover from local disk only. The value must be a
// same-site /media/ path and a media directory must be configured; the file
// must sniff as JPEG or PNG. A file within Bluesky's blob limit goes out
// byte-identical; a larger one is re-encoded as a JPEG, stepping quality down
// and then scaling down until it fits (#0601). Anything else yields no
// thumbnail, with the reason, and the card is sent without one. A server-side
// URL fetch is never made.
func (p *Publisher) thumbnail(cover *string) (data []byte, mime, reason string) {
	dir := p.mediaDir()
	if cover == nil || strings.TrimSpace(*cover) == "" {
		return nil, "", ThumbReasonNoCover
	}
	if !strings.HasPrefix(*cover, "/media/") {
		return nil, "", ThumbReasonNotUploaded
	}
	if dir == "" {
		return nil, "", ThumbReasonNoStorage
	}
	name := filepath.Base(*cover)
	if name == "." || name == "/" || name == ".." || name == "" {
		return nil, "", ThumbReasonNotUploaded
	}
	full := filepath.Join(dir, name)
	fi, err := os.Stat(full)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
		return nil, "", ThumbReasonMissing
	}
	if fi.Size() > maxThumbSourceBytes {
		return nil, "", ThumbReasonTooBig
	}
	key := thumbKey{full, fi.Size(), fi.ModTime().UnixNano()}
	if fi.Size() > int64(bluesky.MaxThumbBytes) {
		if r, ok := p.thumbs.get(key); ok {
			return r.data, r.mime, r.reason
		}
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, "", ThumbReasonMissing
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxThumbSourceBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxThumbSourceBytes {
		return nil, "", ThumbReasonMissing
	}
	format, err := media.Sniff(raw)
	if err != nil || (format != media.FormatJPEG && format != media.FormatPNG) {
		return nil, "", ThumbReasonFormat
	}
	if len(raw) <= bluesky.MaxThumbBytes {
		if format == media.FormatJPEG {
			return raw, "image/jpeg", ""
		}
		return raw, "image/png", ""
	}
	res := shrinkToJPEG(raw)
	p.thumbs.put(key, res)
	return res.data, res.mime, res.reason
}

// shrinkToJPEG re-encodes an oversized JPEG or PNG as a JPEG under
// bluesky.MaxThumbBytes. The pixel count is checked from the header before
// any decode, so a hostile image costs no memory.
func shrinkToJPEG(raw []byte) thumbResult {
	fail := thumbResult{reason: ThumbReasonTooBig}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || media.CheckDimensions(cfg.Width, cfg.Height) != nil {
		return fail
	}
	var src image.Image
	if bytes.HasPrefix(raw, []byte{0x89, 'P', 'N', 'G'}) {
		src, err = png.Decode(bytes.NewReader(raw))
	} else {
		src, err = jpeg.Decode(bytes.NewReader(raw))
	}
	if err != nil {
		return fail
	}
	b := src.Bounds()
	// Flatten onto white: JPEG has no alpha.
	cur := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(cur, cur.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(cur, cur.Bounds(), src, b.Min, draw.Over)
	for {
		for _, q := range thumbQualities {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, cur, &jpeg.Options{Quality: q}); err != nil {
				return fail
			}
			if buf.Len() <= bluesky.MaxThumbBytes {
				return thumbResult{data: buf.Bytes(), mime: "image/jpeg"}
			}
		}
		w, h := cur.Bounds().Dx()*4/5, cur.Bounds().Dy()*4/5
		if w < thumbMinEdge || h < thumbMinEdge {
			return fail
		}
		next := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(next, next.Bounds(), cur, cur.Bounds(), draw.Src, nil)
		cur = next
	}
}
