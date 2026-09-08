package media

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// This file builds a synthetic JPEG carrying REAL device and GPS tags in a
// hand-assembled TIFF/Exif structure inside its APP1 segment, plus a fake
// APP13 (IPTC/Photoshop) segment — the positive-control fixture #0417 never
// had (issues/0433.md's "The fixture is where #0417 went wrong" section:
// "a file with no GPS proves nothing"). jpeg_test.go's tests assert these
// tags are PRESENT in this fixture before asserting they are ABSENT from
// StripJPEG's output — never by a text search, always by walking the marker
// stream (walkJPEGHeader) or the raw bytes it copies as a segment payload.

// le16/le32 append a little-endian uint16/uint32 to buf — this test builds
// an "II" (little-endian) TIFF, one of the two byte orders real cameras use.
func le16(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func le32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

// leRational appends an 8-byte unsigned rational (numerator, denominator).
func leRational(buf *bytes.Buffer, num, den uint32) {
	le32(buf, num)
	le32(buf, den)
}

// buildFakeTIFFWithGPSAndDevice hand-assembles a minimal but structurally
// real little-endian TIFF (the format Exif embeds): IFD0 carries Make
// ("Apple") and Model ("iPhone 16") plus a pointer to a GPS IFD, and the GPS
// IFD carries GPSLatitudeRef/GPSLatitude/GPSLongitudeRef/GPSLongitude with
// real-looking rational degree/minute/second values (San Francisco's
// coordinates). Every offset below is computed by hand and cross-checked by
// the t.Fatalf assertions inline — see the doc comment on each block for the
// arithmetic. This is deliberately NOT built through image/tiff or any
// Exif library: none is available (CLAUDE.md §1, issues/0433.md's
// Obstacles), and the whole point is bytes this package's own stripper can
// be tested against structurally.
func buildFakeTIFFWithGPSAndDevice(t *testing.T) []byte {
	t.Helper()

	makeBytes := append([]byte("Apple"), 0)      // "Apple\x00", 6 bytes
	modelBytes := append([]byte("iPhone 16"), 0) // "iPhone 16\x00", 10 bytes

	const (
		ifd0Offset     = 8 // right after the 8-byte TIFF header
		ifd0EntryCount = 3 // Make, Model, GPSInfo
		ifd0Size       = 2 + ifd0EntryCount*12 + 4
	)
	ifd0DataStart := ifd0Offset + ifd0Size        // 8 + 42 = 50
	makeOffset := ifd0DataStart                   // 50
	modelOffset := makeOffset + len(makeBytes)    // 56
	ifd0ExtraEnd := modelOffset + len(modelBytes) // 66

	gpsIFDOffset := ifd0ExtraEnd // 66
	const (
		gpsEntryCount = 4 // LatRef, Lat, LonRef, Lon
		gpsIFDSize    = 2 + gpsEntryCount*12 + 4
	)
	gpsDataStart := gpsIFDOffset + gpsIFDSize // 66 + 54 = 120
	latOffset := gpsDataStart                 // 120, 24 bytes (3 rationals)
	lonOffset := latOffset + 24               // 144, 24 bytes (3 rationals)
	tiffEnd := lonOffset + 24                 // 168

	var buf bytes.Buffer

	// --- TIFF header (8 bytes) ---
	buf.WriteString("II") // little-endian
	le16(&buf, 42)        // magic
	le32(&buf, ifd0Offset)

	if buf.Len() != ifd0Offset {
		t.Fatalf("TIFF header length = %d, want %d", buf.Len(), ifd0Offset)
	}

	// --- IFD0 (offset 8) ---
	le16(&buf, ifd0EntryCount)
	// Make (0x010F), type ASCII(2), count 6, offset -> data area
	le16(&buf, 0x010F)
	le16(&buf, 2)
	le32(&buf, uint32(len(makeBytes)))
	le32(&buf, uint32(makeOffset))
	// Model (0x0110), type ASCII(2), count 10, offset -> data area
	le16(&buf, 0x0110)
	le16(&buf, 2)
	le32(&buf, uint32(len(modelBytes)))
	le32(&buf, uint32(modelOffset))
	// GPSInfo (0x8825), type LONG(4), count 1, value = GPS IFD offset (fits inline)
	le16(&buf, 0x8825)
	le16(&buf, 4)
	le32(&buf, 1)
	le32(&buf, uint32(gpsIFDOffset))
	// next IFD offset: none
	le32(&buf, 0)

	if buf.Len() != ifd0DataStart {
		t.Fatalf("after IFD0 buf.Len() = %d, want %d", buf.Len(), ifd0DataStart)
	}

	// --- IFD0's data area: Make, then Model ---
	buf.Write(makeBytes)
	buf.Write(modelBytes)

	if buf.Len() != gpsIFDOffset {
		t.Fatalf("after IFD0 data area buf.Len() = %d, want %d", buf.Len(), gpsIFDOffset)
	}

	// --- GPS IFD (offset gpsIFDOffset) ---
	le16(&buf, gpsEntryCount)
	// GPSLatitudeRef (0x0001), ASCII(2), count 2, inline "N\0" + 2 pad bytes
	le16(&buf, 0x0001)
	le16(&buf, 2)
	le32(&buf, 2)
	buf.Write([]byte{'N', 0, 0, 0})
	// GPSLatitude (0x0002), RATIONAL(5), count 3, offset -> data area
	le16(&buf, 0x0002)
	le16(&buf, 5)
	le32(&buf, 3)
	le32(&buf, uint32(latOffset))
	// GPSLongitudeRef (0x0003), ASCII(2), count 2, inline "W\0" + 2 pad bytes
	le16(&buf, 0x0003)
	le16(&buf, 2)
	le32(&buf, 2)
	buf.Write([]byte{'W', 0, 0, 0})
	// GPSLongitude (0x0004), RATIONAL(5), count 3, offset -> data area
	le16(&buf, 0x0004)
	le16(&buf, 5)
	le32(&buf, 3)
	le32(&buf, uint32(lonOffset))
	// next IFD offset: none
	le32(&buf, 0)

	if buf.Len() != gpsDataStart {
		t.Fatalf("after GPS IFD buf.Len() = %d, want %d", buf.Len(), gpsDataStart)
	}

	// --- GPS IFD's data area: latitude then longitude rationals ---
	// 37 deg 46 min 29.64 sec N (Open Circuit SF's real-ish latitude)
	leRational(&buf, 37, 1)
	leRational(&buf, 46, 1)
	leRational(&buf, 2964, 100)
	// 122 deg 25 min 9.84 sec W
	leRational(&buf, 122, 1)
	leRational(&buf, 25, 1)
	leRational(&buf, 984, 100)

	if buf.Len() != tiffEnd {
		t.Fatalf("final TIFF length = %d, want %d", buf.Len(), tiffEnd)
	}

	return buf.Bytes()
}

// buildEXIFApp1Payload wraps buildFakeTIFFWithGPSAndDevice in the "Exif\0\0"
// prefix that makes it a valid APP1 Exif payload.
func buildEXIFApp1Payload(t *testing.T) []byte {
	t.Helper()
	tiff := buildFakeTIFFWithGPSAndDevice(t)
	payload := make([]byte, 0, 6+len(tiff))
	payload = append(payload, "Exif\x00\x00"...)
	payload = append(payload, tiff...)
	return payload
}

// jpegSegmentBytes assembles one marker segment's on-disk bytes: 0xFF,
// marker, big-endian length (payload length + 2), payload.
func jpegSegmentBytes(marker byte, payload []byte) []byte {
	length := len(payload) + 2
	out := make([]byte, 0, 2+2+len(payload))
	out = append(out, 0xFF, marker)
	out = append(out, byte(length>>8), byte(length))
	out = append(out, payload...)
	return out
}

// buildMinimalJFIFApp0Payload is a standard 14-byte JFIF APP0 payload.
func buildMinimalJFIFApp0Payload() []byte {
	return []byte{
		'J', 'F', 'I', 'F', 0x00, // identifier
		0x01, 0x02, // version 1.2
		0x00,       // units: none
		0x00, 0x01, // Xdensity
		0x00, 0x01, // Ydensity
		0x00, 0x00, // thumbnail 0x0
	}
}

// buildMinimalSOF0Payload is a baseline SOF0 payload for a tiny 8x8
// single-component (greyscale) image.
func buildMinimalSOF0Payload(width, height uint16) []byte {
	var buf bytes.Buffer
	buf.WriteByte(8) // sample precision
	buf.WriteByte(byte(height >> 8))
	buf.WriteByte(byte(height))
	buf.WriteByte(byte(width >> 8))
	buf.WriteByte(byte(width))
	buf.WriteByte(1)              // 1 component
	buf.Write([]byte{1, 0x11, 0}) // component id, sampling factors, quant table selector
	return buf.Bytes()
}

// buildMinimalDQTPayload is a trivial (non-realistic, but structurally
// valid) quantization table: 1 byte precision/id, then 64 table entries.
func buildMinimalDQTPayload() []byte {
	payload := make([]byte, 1+64)
	payload[0] = 0x00 // 8-bit precision, table id 0
	for i := 1; i < len(payload); i++ {
		payload[i] = 16
	}
	return payload
}

// buildMinimalSOSPayload is a 1-component scan header.
func buildMinimalSOSPayload() []byte {
	return []byte{
		1,       // 1 component
		1, 0x00, // component id, DC/AC table selectors
		0, 63, 0x00, // spectral start, spectral end, approximation
	}
}

// buildJPEGWithMetadata assembles a full JPEG: SOI, APP0 (JFIF), APP1
// (real Exif GPS + device tags), APP13 (fake IPTC/Photoshop), DQT, SOF0,
// SOS, a few bytes of fake entropy-coded data, and EOI. It is a structurally
// walkable JPEG — every header segment is well-formed — even though the
// entropy data is not real compressed pixel data (this package never
// decodes it; see the package doc comment).
func buildJPEGWithMetadata(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xD8}) // SOI
	buf.Write(jpegSegmentBytes(jpegMarkerAPP0, buildMinimalJFIFApp0Payload()))
	buf.Write(jpegSegmentBytes(jpegMarkerAPP1, buildEXIFApp1Payload(t)))
	buf.Write(jpegSegmentBytes(jpegMarkerAPP13, []byte("Photoshop 3.0\x00fake IPTC block, not real IPTC structure")))
	buf.Write(jpegSegmentBytes(0xDB, buildMinimalDQTPayload()))
	buf.Write(jpegSegmentBytes(0xC0, buildMinimalSOF0Payload(8, 8)))
	buf.Write(jpegSegmentBytes(jpegMarkerSOS, buildMinimalSOSPayload()))
	buf.Write([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}) // fake entropy data
	buf.Write([]byte{0xFF, 0xD9})                                     // EOI
	return buf.Bytes()
}

// buildJPEGWithICCAndCOM is a second fixture used to prove APP2/ICC
// survives while a COM segment and a non-ICC APP2 do not.
func buildJPEGWithICCAndCOM(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xD8})
	buf.Write(jpegSegmentBytes(jpegMarkerAPP0, buildMinimalJFIFApp0Payload()))
	iccPayload := append(append([]byte{}, iccProfileSignature...), []byte{1, 1, 'f', 'a', 'k', 'e', 'I', 'C', 'C'}...)
	buf.Write(jpegSegmentBytes(jpegMarkerAPP2, iccPayload))
	buf.Write(jpegSegmentBytes(0xE2, []byte("not an ICC profile at all"))) // non-ICC APP2
	buf.Write(jpegSegmentBytes(jpegMarkerCOM, []byte("a comment nobody needs")))
	buf.Write(jpegSegmentBytes(0xDB, buildMinimalDQTPayload()))
	buf.Write(jpegSegmentBytes(0xC0, buildMinimalSOF0Payload(8, 8)))
	buf.Write(jpegSegmentBytes(jpegMarkerSOS, buildMinimalSOSPayload()))
	buf.Write([]byte{0x00, 0x01, 0x02, 0x03})
	buf.Write([]byte{0xFF, 0xD9})
	return buf.Bytes()
}
