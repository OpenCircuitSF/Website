package media

import (
	"bytes"
	"testing"
)

// jpegMarkersPresent walks data's header section and returns the set of
// marker codes found — the structural, non-text-search way this package
// proves a marker is present or absent (CLAUDE.md §8: BSD grep -P silently
// reports zero hits on bytes that are present, and issues/0433.md's plan
// requires walking the marker/chunk stream rather than searching text).
func jpegMarkersPresent(t *testing.T, data []byte) map[byte]bool {
	t.Helper()
	found := make(map[byte]bool)
	err := walkJPEGHeader(data, func(seg jpegSegment, tail []byte) error {
		found[seg.marker] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walkJPEGHeader: %v", err)
	}
	return found
}

// TestStripJPEG_PositiveControl_MetadataPresentBeforeStrip is the fixture's
// positive control (issues/0433.md: "a test using a file with no GPS to
// begin with proves nothing"): before stripping anything, assert the
// fixture's APP1 segment is actually present AND actually carries the real
// device/GPS byte sequences this test will later assert are gone. Without
// this, a no-op strip would pass the absence test below for the wrong
// reason.
func TestStripJPEG_PositiveControl_MetadataPresentBeforeStrip(t *testing.T) {
	input := buildJPEGWithMetadata(t)

	markers := jpegMarkersPresent(t, input)
	if !markers[jpegMarkerAPP1] {
		t.Fatal("fixture does not contain an APP1 segment — positive control fixture is broken")
	}
	if !markers[jpegMarkerAPP13] {
		t.Fatal("fixture does not contain an APP13 segment — positive control fixture is broken")
	}

	// The device tags and GPS ref bytes are inside APP1's payload. This
	// bytes.Contains check runs in Go over bytes we JUST constructed
	// ourselves, on the INPUT only — it is the positive control that proves
	// the fixture is real, not the mechanism that proves the strip worked
	// (that is VerifyJPEGStripped + the marker walk below, which never does
	// a text search).
	if !bytes.Contains(input, []byte("Apple")) {
		t.Fatal("fixture does not contain the device make 'Apple' — positive control fixture is broken")
	}
	if !bytes.Contains(input, []byte("iPhone 16")) {
		t.Fatal("fixture does not contain the device model 'iPhone 16' — positive control fixture is broken")
	}
	if !bytes.Contains(input, []byte{'N', 0, 0, 0}) {
		t.Fatal("fixture does not contain the GPS latitude ref — positive control fixture is broken")
	}
}

// TestStripJPEG_RemovesDeviceAndGPSMetadata is the actual regression test:
// APP1 (which carries both Exif device tags and GPS) and APP13 (IPTC) must
// both be gone from the stripped output, proven by walking the marker
// stream — never by searching for "Apple"/"iPhone" text in the output,
// which is exactly the shape CLAUDE.md §8 warns can silently prove nothing.
func TestStripJPEG_RemovesDeviceAndGPSMetadata(t *testing.T) {
	input := buildJPEGWithMetadata(t)

	stripped, err := StripJPEG(input)
	if err != nil {
		t.Fatalf("StripJPEG: %v", err)
	}

	markers := jpegMarkersPresent(t, stripped)
	if markers[jpegMarkerAPP1] {
		t.Error("APP1 (Exif/XMP, device + GPS) survived stripping")
	}
	if markers[jpegMarkerAPP13] {
		t.Error("APP13 (IPTC/Photoshop) survived stripping")
	}
	if !markers[jpegMarkerAPP0] {
		t.Error("APP0 (JFIF) was dropped, but it should have been kept")
	}
	if !markers[0xDB] || !markers[0xC0] || !markers[jpegMarkerSOS] {
		t.Error("a non-metadata segment (DQT/SOF0/SOS) was dropped — pixel-relevant structure must survive untouched")
	}

	if err := VerifyJPEGStripped(stripped); err != nil {
		t.Errorf("VerifyJPEGStripped on Strip's own output: %v", err)
	}

	// Entropy data + EOI must be byte-identical to the input's tail, since
	// StripJPEG never touches anything after SOS. Located structurally (the
	// SOS segment's own tail, from walkJPEGHeader), never by searching for
	// the marker bytes as text — an 0xFF 0xDA sequence could in principle
	// recur inside a payload.
	var inputTail, strippedTail []byte
	if err := walkJPEGHeader(input, func(seg jpegSegment, tail []byte) error {
		if seg.marker == jpegMarkerSOS {
			inputTail = tail
		}
		return nil
	}); err != nil {
		t.Fatalf("walkJPEGHeader(input): %v", err)
	}
	if err := walkJPEGHeader(stripped, func(seg jpegSegment, tail []byte) error {
		if seg.marker == jpegMarkerSOS {
			strippedTail = tail
		}
		return nil
	}); err != nil {
		t.Fatalf("walkJPEGHeader(stripped): %v", err)
	}
	if !bytes.Equal(inputTail, strippedTail) {
		t.Error("bytes after the SOS segment (entropy data + EOI) changed — pixel data must be untouched")
	}
}

// TestStripJPEG_KeepsICCAPP2AndDropsCOMAndNonICCAPP2 proves the allowlist's
// one exception (#0417's review: colour profiles stay) and its ordinary
// case (a comment and a non-ICC APP2 both go).
func TestStripJPEG_KeepsICCAPP2AndDropsCOMAndNonICCAPP2(t *testing.T) {
	input := buildJPEGWithICCAndCOM(t)

	inputMarkers := jpegMarkersPresent(t, input)
	if !inputMarkers[jpegMarkerAPP2] || !inputMarkers[jpegMarkerCOM] {
		t.Fatal("fixture is missing an APP2 or COM segment — fixture is broken")
	}

	stripped, err := StripJPEG(input)
	if err != nil {
		t.Fatalf("StripJPEG: %v", err)
	}
	if err := VerifyJPEGStripped(stripped); err != nil {
		t.Fatalf("VerifyJPEGStripped: %v", err)
	}

	// Walk again, this time distinguishing the ICC APP2 from the non-ICC one
	// by payload prefix, since both share the same marker byte.
	var sawICCAPP2, sawNonICCAPP2, sawCOM bool
	if err := walkJPEGHeader(stripped, func(seg jpegSegment, tail []byte) error {
		switch {
		case seg.marker == jpegMarkerAPP2 && bytes.HasPrefix(seg.payload, iccProfileSignature):
			sawICCAPP2 = true
		case seg.marker == jpegMarkerAPP2:
			sawNonICCAPP2 = true
		case seg.marker == jpegMarkerCOM:
			sawCOM = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walkJPEGHeader on stripped output: %v", err)
	}

	if !sawICCAPP2 {
		t.Error("ICC-carrying APP2 was dropped, but colour profiles must survive (#0417's review)")
	}
	if sawNonICCAPP2 {
		t.Error("non-ICC APP2 survived, but it should have been dropped")
	}
	if sawCOM {
		t.Error("COM segment survived, but it should have been dropped")
	}
}

func TestStripJPEG_MalformedInput(t *testing.T) {
	cases := map[string][]byte{
		"empty":            {},
		"too short":        {0xFF},
		"no SOI":           {0x00, 0x01, 0x02},
		"truncated marker": {0xFF, 0xD8, 0xFF},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := StripJPEG(data); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}
