// Package media strips privacy-sensitive metadata from images uploaded
// through the admin console (#0433, reopening #0153's no-upload decision),
// without decoding or re-encoding pixel data.
//
// The strip is lossless segment/chunk surgery over the raw bytes, following
// the precedent #0417 set by hand for the two workshop covers already on
// disk (drop APP1/APP13, keep the pixel data untouched) — but inverted from
// a denylist into an ALLOWLIST: keep only what is required to render, drop
// everything else. #0417's own review found a denylist built from one
// sample file's segments the same class of mistake as the original test gap
// that let a phone photo's Exif ship publicly, so this package never
// repeats that shape. See jpeg.go and png.go for the two formats' allowlists.
//
// No new dependency is needed or wanted: the box this runs on has neither
// exiftool nor Pillow (CLAUDE.md §1, issues/0433.md's Obstacles), and this
// package uses only the standard library. It never calls image.Decode —
// only image.DecodeConfig, in the handler that owns the upload request,
// reads a header's dimensions without allocating a pixel buffer
// (issues/0433.md's Plan §3: fully decoding a 5 MiB 8000x6000 JPEG would
// allocate well over 100 MB on a box with ~166 MB available).
package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Format is one of the image formats this package accepts. There are
// deliberately only two — see Sniff's doc comment for why every other
// format, including HEIC, is refused rather than converted.
type Format string

const (
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
)

// Extension returns the filename extension Filename uses for f — "jpg" (not
// "jpeg", matching the two existing covers docs/media.md already documents:
// soldering.jpg, programming_leds.jpg) or "png".
func (f Format) Extension() string {
	switch f {
	case FormatJPEG:
		return "jpg"
	case FormatPNG:
		return "png"
	default:
		return ""
	}
}

// ErrUnrecognizedFormat is returned by Sniff when the bytes match none of
// the signatures this package knows about at all — not JPEG, PNG, HEIC,
// GIF, WebP, or SVG.
var ErrUnrecognizedFormat = errors.New("media: unrecognized image format")

// ErrUnsupportedFormat is returned by Sniff when the bytes are a
// recognizable format this package deliberately refuses: GIF, WebP
// (issues/0433.md Plan §2: a third parser for a format no phone produces by
// default), or SVG (script-bearing markup — docs/media.md's sandboxing CSP
// is a backstop, never a licence to skip validation).
var ErrUnsupportedFormat = errors.New("media: unsupported image format")

// ErrHEICUnsupported is returned by Sniff specifically for HEIC/HEIF —
// broken out from ErrUnsupportedFormat because an iPhone shoots HEIC unless
// the camera is set to "Most Compatible" (issues/0433.md Plan §2), so this
// is the single most likely format a real admin's first upload attempt will
// be. The caller must give this its own, specific error message rather than
// a generic "unsupported format" one — see
// internal/handlers/admin_media_upload.go's heicErrorMessage.
var ErrHEICUnsupported = errors.New("media: HEIC/HEIF is not supported")

// pngSignature is the fixed 8-byte PNG file signature (see png.go).
var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// Sniff identifies fmt from the file's magic bytes ONLY — never a filename
// extension, never a client-supplied Content-Type header, per
// issues/0433.md Plan §3 ("the client does not get a vote"). Returns
// (FormatJPEG or FormatPNG, nil) on success; otherwise a zero Format and one
// of ErrHEICUnsupported, ErrUnsupportedFormat, or ErrUnrecognizedFormat (use
// errors.Is to distinguish them).
func Sniff(data []byte) (Format, error) {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return FormatJPEG, nil
	case len(data) >= 8 && bytes.Equal(data[:8], pngSignature):
		return FormatPNG, nil
	case isHEIC(data):
		return "", ErrHEICUnsupported
	case len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))):
		return "", ErrUnsupportedFormat
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "", ErrUnsupportedFormat
	case looksLikeSVG(data):
		return "", ErrUnsupportedFormat
	default:
		return "", ErrUnrecognizedFormat
	}
}

// heicBrands are the ISO base media file format "major brand" and
// "compatible brand" 4-character codes an iPhone (or other HEIF encoder)
// writes into a heic/heif file's ftyp box. mif1/msf1/avif are generic
// HEIF/MIAF/AVIF brands rather than HEIC-specific, but on this project's own
// upload path (a phone camera roll) treating them as HEIC gives the
// specific, actionable error message rather than the generic "unsupported
// format" one.
var heicBrands = map[string]bool{
	"heic": true, "heix": true, "heim": true, "heis": true,
	"hevc": true, "hevx": true, "hevm": true, "hevs": true,
	"mif1": true, "msf1": true, "avif": true,
}

// isHEIC reports whether data opens with an ISO base media file "ftyp" box
// naming a HEIC/HEIF/AVIF brand. The box layout is a 4-byte big-endian size,
// then the ASCII type "ftyp" at bytes [4:8], then a 4-byte major brand at
// [8:12].
func isHEIC(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	if string(data[4:8]) != "ftyp" {
		return false
	}
	return heicBrands[string(data[8:12])]
}

// looksLikeSVG reports whether data is SVG-shaped: after skipping a UTF-8
// BOM and leading whitespace, it starts with an XML declaration or an <svg>
// tag. SVG is refused outright regardless of this match's precision — it is
// script-bearing markup — so this check only needs to be good enough to
// give a specific refusal rather than falling through to the generic
// "unrecognized format" one; a false negative here still ends in a refusal
// via ErrUnrecognizedFormat, just with less specific wording.
func looksLikeSVG(data []byte) bool {
	b := bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	b = bytes.TrimLeft(b, " \t\r\n")
	lower := strings.ToLower(string(firstN(b, 256)))
	return strings.HasPrefix(lower, "<?xml") || strings.HasPrefix(lower, "<svg")
}

func firstN(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// MaxImageEdgePixels and MaxImagePixels bound what image.DecodeConfig may
// report before a header is refused (issues/0433.md Plan §3): reading a
// header proves the format's structure is coherent and gives dimensions
// without allocating a pixel buffer, but an absurd header (an 8000x6000
// image is >100 MB of pixel data if ever decoded) should be refused before
// anything is written, on a box with ~166 MB available. 8000x6000 is the
// plan's own example of what must be rejected; the ceiling below rejects it
// on both grounds (edge and total pixels). It does not admit every real
// phone photo — a 24 MP frame (5712x4284, e.g. a recent iPhone's main
// sensor) is 24.47 MP and exceeds it — but the 5 MiB request-body cap
// (internal/handlers) refuses a file that large first in practice, so the
// dimension ceiling's role is to bound decode-header cost, not to be the
// first line of defense against a large sensor.
const (
	MaxImageEdgePixels = 6000
	MaxImagePixels     = 24_000_000 // 24 MP
)

// ErrDimensionsTooLarge is returned by CheckDimensions when an image's
// declared width/height exceed the ceiling above.
var ErrDimensionsTooLarge = errors.New("media: image dimensions exceed the allowed maximum")

// CheckDimensions applies MaxImageEdgePixels/MaxImagePixels to a decoded
// header's width and height. Called against the return of
// image.DecodeConfig, never image.Decode (see the package doc comment).
func CheckDimensions(width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("media: invalid dimensions %dx%d", width, height)
	}
	if width > MaxImageEdgePixels || height > MaxImageEdgePixels {
		return fmt.Errorf("%w: %dx%d exceeds the %dpx edge limit", ErrDimensionsTooLarge, width, height, MaxImageEdgePixels)
	}
	if width*height > MaxImagePixels {
		return fmt.Errorf("%w: %dx%d (%d px) exceeds the %d px limit", ErrDimensionsTooLarge, width, height, width*height, MaxImagePixels)
	}
	return nil
}

// SanitizeStem reduces an uploaded filename to a short, filesystem- and
// URL-safe stem: strip any directory components and the extension, fold to
// lowercase, and keep only [a-z0-9-], collapsing runs of anything else into
// a single '-' and trimming leading/trailing '-'. Falls back to "image" when
// nothing survives — an upload named entirely in, say, Kanji, or one with no
// basename at all, still gets a usable filename.
//
// This is a reduction, not a validator: traversal is prevented by
// construction, not by rejecting a "bad" name, because '.' and '/' are
// simply not in the output alphabet. Nothing here trusts the caller's name
// for anything beyond a cosmetic hint in the final filename — see
// Filename's doc comment for the part that actually makes the name unique
// and cache-busting.
func SanitizeStem(name string) string {
	// Strip any path components defensively, even though the multipart
	// filename header is never used as a path — see
	// internal/handlers/admin_media_upload.go's doc comment.
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndex(name, "."); i > 0 {
		name = name[:i]
	}
	name = strings.ToLower(name)

	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	stem := strings.Trim(b.String(), "-")
	const maxStemLen = 48
	if len(stem) > maxStemLen {
		stem = strings.Trim(stem[:maxStemLen], "-")
	}
	if stem == "" {
		stem = "image"
	}
	return stem
}

// Filename builds the on-disk name for a stripped upload:
// "<sanitised-stem>-<hash>.<ext>", where hash is a short prefix of the
// SHA-256 of strippedBytes — the STRIPPED output, never the original upload
// (issues/0433.md Plan §3). Hashing the stripped bytes, not the original, is
// what makes two uploads of visually-identical photos that differ only in
// stripped metadata converge on the same stored file rather than
// accumulating near-duplicates.
//
// This scheme answers three of #0433's design questions in one move:
// traversal is impossible (the extension comes from the sniffed format,
// never the client, and SanitizeStem's output alphabet excludes '.' and
// '/'), the week-long Cache-Control max-age is never stale (different bytes
// always produce a different name), and an upload of the exact same file —
// same original name and identical stripped bytes — converges on the same
// stored path instead of accumulating a near-duplicate. Identical bytes
// under a *different* original name only share the hash suffix: the stem
// differs, so the two still produce different paths.
func Filename(originalName string, strippedBytes []byte, format Format) string {
	stem := SanitizeStem(originalName)
	sum := sha256.Sum256(strippedBytes)
	hash := hex.EncodeToString(sum[:])[:10]
	return fmt.Sprintf("%s-%s.%s", stem, hash, format.Extension())
}

// Strip removes every metadata segment/chunk this package does not allowlist
// for format — see StripJPEG/StripPNG for the exact allowlist each format
// uses. format must be FormatJPEG or FormatPNG; any other value is a
// programmer error (Sniff never returns one).
func Strip(format Format, data []byte) ([]byte, error) {
	switch format {
	case FormatJPEG:
		return StripJPEG(data)
	case FormatPNG:
		return StripPNG(data)
	default:
		return nil, fmt.Errorf("media: cannot strip unrecognized format %q", format)
	}
}

// VerifyStripped re-walks data (the output of Strip) and returns an error if
// any segment/chunk Strip is supposed to have dropped is still present —
// issues/0433.md Plan §3's "re-run the structural walk on the output and
// assert no dropped segment survived": the strip's own output is the thing
// that gets written, so it is the thing that gets checked.
func VerifyStripped(format Format, data []byte) error {
	switch format {
	case FormatJPEG:
		return VerifyJPEGStripped(data)
	case FormatPNG:
		return VerifyPNGStripped(data)
	default:
		return fmt.Errorf("media: cannot verify unrecognized format %q", format)
	}
}
