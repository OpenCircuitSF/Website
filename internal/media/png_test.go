package media

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

// pngChunkBytes assembles one chunk's on-disk bytes: 4-byte big-endian
// length, 4-byte ASCII type, data, then a real IEEE CRC-32 over type+data
// (computed, not faked — StripPNG copies kept chunks' CRCs verbatim rather
// than recomputing them, so a real CRC here is what proves that).
func pngChunkBytes(typ string, data []byte) []byte {
	var buf bytes.Buffer
	var lenB [4]byte
	binary.BigEndian.PutUint32(lenB[:], uint32(len(data)))
	buf.Write(lenB[:])
	buf.WriteString(typ)
	buf.Write(data)
	sum := crc32.ChecksumIEEE(append([]byte(typ), data...))
	var crcB [4]byte
	binary.BigEndian.PutUint32(crcB[:], sum)
	buf.Write(crcB[:])
	return buf.Bytes()
}

// buildIHDRData is a minimal valid IHDR payload: 8x8, 8-bit truecolor,
// no interlace.
func buildIHDRData(width, height uint32) []byte {
	var buf bytes.Buffer
	var w, h [4]byte
	binary.BigEndian.PutUint32(w[:], width)
	binary.BigEndian.PutUint32(h[:], height)
	buf.Write(w[:])
	buf.Write(h[:])
	buf.Write([]byte{8, 2, 0, 0, 0}) // bit depth, color type (truecolor), compression, filter, interlace
	return buf.Bytes()
}

// buildPNGWithMetadata assembles a PNG carrying a real eXIf chunk (the same
// hand-built TIFF/GPS/device structure jpeg_fixture_test.go uses for JPEG,
// since PNG's own eXIf chunk holds the identical TIFF-shaped payload — no
// "Exif\0\0" prefix, unlike JPEG's APP1) plus a tEXt chunk, alongside the
// required IHDR/IDAT/IEND.
func buildPNGWithMetadata(t *testing.T) []byte {
	t.Helper()
	tiff := buildFakeTIFFWithGPSAndDevice(t)

	var buf bytes.Buffer
	buf.Write(pngSignature)
	buf.Write(pngChunkBytes("IHDR", buildIHDRData(8, 8)))
	buf.Write(pngChunkBytes("eXIf", tiff))
	buf.Write(pngChunkBytes("tEXt", []byte("Author\x00Taken on an iPhone 16")))
	buf.Write(pngChunkBytes("IDAT", []byte{0x78, 0x9c, 0x01, 0x00, 0x00, 0xff, 0xff, 0x00, 0x00, 0x00, 0x01})) // not real compressed pixel data; never decoded by this package
	buf.Write(pngChunkBytes("IEND", nil))
	return buf.Bytes()
}

// pngChunkTypesPresent walks data's chunk stream and returns the set of
// chunk types found — structural, not a text search.
func pngChunkTypesPresent(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	found := make(map[string]bool)
	if err := walkPNGChunks(data, func(c pngChunk) error {
		found[c.typ] = true
		return nil
	}); err != nil {
		t.Fatalf("walkPNGChunks: %v", err)
	}
	return found
}

func TestStripPNG_PositiveControl_MetadataPresentBeforeStrip(t *testing.T) {
	input := buildPNGWithMetadata(t)

	types := pngChunkTypesPresent(t, input)
	if !types["eXIf"] {
		t.Fatal("fixture does not contain an eXIf chunk — positive control fixture is broken")
	}
	if !types["tEXt"] {
		t.Fatal("fixture does not contain a tEXt chunk — positive control fixture is broken")
	}
	if !bytes.Contains(input, []byte("Apple")) {
		t.Fatal("fixture does not contain the device make 'Apple' — positive control fixture is broken")
	}
	if !bytes.Contains(input, []byte("iPhone 16")) {
		t.Fatal("fixture does not contain the device model 'iPhone 16' — positive control fixture is broken")
	}
}

func TestStripPNG_RemovesExifAndText(t *testing.T) {
	input := buildPNGWithMetadata(t)

	stripped, err := StripPNG(input)
	if err != nil {
		t.Fatalf("StripPNG: %v", err)
	}

	types := pngChunkTypesPresent(t, stripped)
	if types["eXIf"] {
		t.Error("eXIf chunk survived stripping")
	}
	if types["tEXt"] {
		t.Error("tEXt chunk survived stripping")
	}
	if !types["IHDR"] || !types["IDAT"] || !types["IEND"] {
		t.Error("a required chunk (IHDR/IDAT/IEND) was dropped")
	}

	if err := VerifyPNGStripped(stripped); err != nil {
		t.Errorf("VerifyPNGStripped on Strip's own output: %v", err)
	}

	// IHDR and IDAT must be byte-for-byte unchanged (CRC included) —
	// StripPNG recomputes nothing.
	var inputIHDR, strippedIHDR, inputIDAT, strippedIDAT []byte
	if err := walkPNGChunks(input, func(c pngChunk) error {
		switch c.typ {
		case "IHDR":
			inputIHDR = c.raw
		case "IDAT":
			inputIDAT = c.raw
		}
		return nil
	}); err != nil {
		t.Fatalf("walkPNGChunks(input): %v", err)
	}
	if err := walkPNGChunks(stripped, func(c pngChunk) error {
		switch c.typ {
		case "IHDR":
			strippedIHDR = c.raw
		case "IDAT":
			strippedIDAT = c.raw
		}
		return nil
	}); err != nil {
		t.Fatalf("walkPNGChunks(stripped): %v", err)
	}
	if !bytes.Equal(inputIHDR, strippedIHDR) {
		t.Error("IHDR changed across stripping")
	}
	if !bytes.Equal(inputIDAT, strippedIDAT) {
		t.Error("IDAT changed across stripping")
	}
}

func TestStripPNG_KeepsRenderingAncillaries(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(pngSignature)
	buf.Write(pngChunkBytes("IHDR", buildIHDRData(4, 4)))
	buf.Write(pngChunkBytes("gAMA", []byte{0, 0, 0x9a, 0}))
	buf.Write(pngChunkBytes("sRGB", []byte{0}))
	buf.Write(pngChunkBytes("iCCP", []byte("fake profile\x00\x00data")))
	buf.Write(pngChunkBytes("tIME", []byte{7, 0xea, 1, 1, 0, 0, 0})) // dropped: capture timestamp
	buf.Write(pngChunkBytes("IDAT", []byte{0x00}))
	buf.Write(pngChunkBytes("IEND", nil))
	input := buf.Bytes()

	stripped, err := StripPNG(input)
	if err != nil {
		t.Fatalf("StripPNG: %v", err)
	}
	types := pngChunkTypesPresent(t, stripped)
	for _, want := range []string{"gAMA", "sRGB", "iCCP"} {
		if !types[want] {
			t.Errorf("rendering-relevant chunk %q was dropped", want)
		}
	}
	if types["tIME"] {
		t.Error("tIME (capture timestamp) survived stripping")
	}
	if err := VerifyPNGStripped(stripped); err != nil {
		t.Errorf("VerifyPNGStripped: %v", err)
	}
}

func TestStripPNG_MalformedInput(t *testing.T) {
	cases := map[string][]byte{
		"empty":            {},
		"too short":        {0x89, 'P', 'N'},
		"bad signature":    append([]byte("NOTPNG!!"), 0),
		"truncated header": append(append([]byte{}, pngSignature...), 0x00),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := StripPNG(data); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}
