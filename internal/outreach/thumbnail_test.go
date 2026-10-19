package outreach

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/media"
)

func noiseImage(w, h int, alpha uint8) *image.NRGBA {
	r := rand.New(rand.NewSource(1))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(r.Intn(256)), byte(r.Intn(256)), byte(r.Intn(256)), alpha
	}
	return img
}

func pngChunk(typ string, body []byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(len(body)))
	b.WriteString(typ)
	b.Write(body)
	binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), body...)))
	return b.Bytes()
}

// pixelBomb is a PNG whose header declares 30000x30000 and whose file is over
// the thumbnail limit, so it reaches the shrink path without being decodable.
func pixelBomb() []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 30000)
	binary.BigEndian.PutUint32(ihdr[4:], 30000)
	ihdr[8], ihdr[9] = 8, 0
	out := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	out = append(out, pngChunk("IHDR", ihdr)...)
	out = append(out, pngChunk("tEXt", bytes.Repeat([]byte("a"), bluesky.MaxThumbBytes+100))...)
	return append(out, pngChunk("IEND", nil)...)
}

func TestPublisher_ThumbnailShrinksOversized(t *testing.T) {
	dir := t.TempDir()
	var jbuf, pbuf bytes.Buffer
	if err := jpeg.Encode(&jbuf, noiseImage(1800, 1400, 255), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&pbuf, noiseImage(900, 900, 128)); err != nil {
		t.Fatal(err)
	}
	if jbuf.Len() <= bluesky.MaxThumbBytes || pbuf.Len() <= bluesky.MaxThumbBytes {
		t.Fatalf("fixtures not oversized: jpeg %d png %d", jbuf.Len(), pbuf.Len())
	}
	small := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2}
	for name, data := range map[string][]byte{"big.jpg": jbuf.Bytes(), "big.png": pbuf.Bytes(), "small.png": small, "bomb.png": pixelBomb()} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pub := NewPublisher(PublisherDeps{MediaDir: dir})
	str := func(s string) *string { return &s }

	for _, name := range []string{"big.jpg", "big.png"} {
		data, mime, reason := pub.thumbnail(str("/media/" + name))
		if mime != "image/jpeg" || reason != "" || len(data) == 0 || len(data) > bluesky.MaxThumbBytes {
			t.Fatalf("%s: mime %q reason %q len %d", name, mime, reason, len(data))
		}
		if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
			t.Errorf("%s: output does not decode: %v", name, err)
		}
		if ok, _ := pub.ThumbStatus(str("/media/" + name)); !ok {
			t.Errorf("%s: ThumbStatus not ok", name)
		}
	}

	data, mime, _ := pub.thumbnail(str("/media/small.png"))
	if mime != "image/png" || !bytes.Equal(data, small) {
		t.Error("an under-limit file must go out byte-identical")
	}

	data, mime, reason := pub.thumbnail(str("/media/bomb.png"))
	if data != nil || mime != "" || reason != ThumbReasonTooBig {
		t.Errorf("pixel bomb: got %d bytes, mime %q, reason %q", len(data), mime, reason)
	}
}

func TestPublisher_ThumbnailCachedByPathSizeMtime(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	jpeg.Encode(&buf, noiseImage(1800, 1400, 255), &jpeg.Options{Quality: 95})
	full := filepath.Join(dir, "c.jpg")
	os.WriteFile(full, buf.Bytes(), 0o644)
	pub := NewPublisher(PublisherDeps{MediaDir: dir})
	cover := "/media/c.jpg"
	first, _, _ := pub.thumbnail(&cover)
	second, _, _ := pub.thumbnail(&cover)
	if len(first) == 0 || &first[0] != &second[0] {
		t.Error("second call should return the cached bytes, not re-encode")
	}
	later := time.Now().Add(time.Hour)
	os.Chtimes(full, later, later)
	third, _, _ := pub.thumbnail(&cover)
	if len(third) == 0 || &third[0] == &first[0] {
		t.Error("a changed mtime must miss the cache")
	}
}

func TestPublisher_ThumbnailReasons(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.gif"), []byte("GIF89a...."), 0o644)
	pub := NewPublisher(PublisherDeps{MediaDir: dir})
	str := func(s string) *string { return &s }
	cases := []struct {
		name   string
		pub    *Publisher
		cover  *string
		reason string
	}{
		{"nil", pub, nil, ThumbReasonNoCover},
		{"blank", pub, str(" "), ThumbReasonNoCover},
		{"remote", pub, str("https://example.test/a.jpg"), ThumbReasonNotUploaded},
		{"missing", pub, str("/media/nope.jpg"), ThumbReasonMissing},
		{"gif", pub, str("/media/x.gif"), ThumbReasonFormat},
		{"no dir", NewPublisher(PublisherDeps{}), str("/media/x.gif"), ThumbReasonNoStorage},
	}
	for _, tc := range cases {
		if ok, reason := tc.pub.ThumbStatus(tc.cover); ok || reason != tc.reason {
			t.Errorf("%s: ok %v reason %q, want %q", tc.name, ok, reason, tc.reason)
		}
	}
}

// #0601 review: a cover that decodes fine but whose header is over the media
// pixel limits gets no picture. Unlike pixelBomb, this one would decode and
// shrink if the header check were dropped, so the check itself is what fails it.
func TestPublisher_ThumbnailOverPixelLimitRefused(t *testing.T) {
	var pbuf bytes.Buffer
	if err := png.Encode(&pbuf, noiseImage(media.MaxImageEdgePixels+1, 40, 255)); err != nil {
		t.Fatal(err)
	}
	enc := pbuf.Bytes()
	// Pad past the blob limit with a tEXt chunk after IHDR (8-byte signature,
	// then IHDR's 4+4+13+4 bytes), so the file reaches the shrink path.
	pad := pngChunk("tEXt", bytes.Repeat([]byte("a"), bluesky.MaxThumbBytes))
	data := append(append(append([]byte{}, enc[:33]...), pad...), enc[33:]...)
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("fixture must decode: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wide.png"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	cover := "/media/wide.png"
	got, mime, reason := NewPublisher(PublisherDeps{MediaDir: dir}).thumbnail(&cover)
	if got != nil || mime != "" || reason != ThumbReasonTooBig {
		t.Errorf("over the pixel limit: got %d bytes, mime %q, reason %q", len(got), mime, reason)
	}
}
