package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// JPEG marker codes this file cares about. Every marker is written as the
// two bytes 0xFF, <code> in the file. See walkJPEGHeader's doc comment for
// the segment shapes this recognizes.
const (
	jpegMarkerTEM   = 0x01
	jpegMarkerRST0  = 0xD0
	jpegMarkerRST7  = 0xD7
	jpegMarkerSOI   = 0xD8
	jpegMarkerEOI   = 0xD9
	jpegMarkerSOS   = 0xDA
	jpegMarkerAPP0  = 0xE0
	jpegMarkerAPP1  = 0xE1
	jpegMarkerAPP2  = 0xE2
	jpegMarkerAPPF  = 0xEF // last APPn marker (APP15)
	jpegMarkerAPP13 = 0xED
	jpegMarkerCOM   = 0xFE
)

// iccProfileSignature is the fixed 12-byte prefix an APP2 segment's payload
// carries when it holds an embedded ICC colour profile (the
// "ICC_PROFILE\x00" marker string, per the ICC's own APP2 spec). #0417's
// review ruled colour profiles stay: stripping one degrades rendering on a
// wide-gamut display to save bytes nobody is counting.
var iccProfileSignature = []byte("ICC_PROFILE\x00")

// ErrMalformedJPEG is returned by the JPEG walker/stripper when the byte
// stream does not follow the marker-segment structure the JPEG spec
// requires — a genuinely corrupt or truncated file, not a format this
// package refuses on purpose (see ErrUnsupportedFormat/ErrUnrecognizedFormat
// in media.go for that).
var ErrMalformedJPEG = errors.New("media: malformed JPEG segment stream")

// jpegSegment is one marker segment found while walking a JPEG's header
// section (the part before SOS).
type jpegSegment struct {
	marker byte // the marker code, e.g. 0xE1 for APP1
	// raw is the segment's bytes exactly as they appear on disk, INCLUDING
	// the leading 0xFF <marker> pair — copying raw verbatim reproduces the
	// segment byte-for-byte.
	raw []byte
	// payload is raw's length-prefixed data, EXCLUDING the 0xFF marker
	// pair and the 2-byte length field itself. nil for a standalone marker
	// (TEM/RSTn) that carries no length or payload.
	payload []byte
}

// walkJPEGHeader walks data's marker-segment stream, calling visit once per
// segment found. data must begin with the 2-byte SOI marker (0xFFD8); visit
// is never called for SOI itself — callers that need to preserve it copy
// data[:2] directly, since SOI is fixed and never varies.
//
// When the walk reaches SOS (Start of Scan), it calls visit exactly once
// more for the SOS segment itself, with tail set to everything from the end
// of the SOS segment header through the end of data — the entropy-coded
// scan data plus the terminating EOI marker. That tail is never parsed
// further: it can legitimately contain 0xFF bytes (stuffed as 0xFF 0x00) and
// restart markers (RSTn) that are NOT delimited the same way header segments
// are, so the only safe treatment is "copy everything after SOS verbatim",
// exactly as issues/0433.md's plan specifies. walkJPEGHeader then returns,
// regardless of what visit returned for the SOS segment (an error from visit
// there still propagates).
func walkJPEGHeader(data []byte, visit func(seg jpegSegment, tail []byte) error) error {
	if len(data) < 2 || data[0] != 0xFF || data[1] != jpegMarkerSOI {
		return fmt.Errorf("%w: missing SOI", ErrMalformedJPEG)
	}
	pos := 2
	for {
		if pos >= len(data) {
			return fmt.Errorf("%w: truncated before SOS", ErrMalformedJPEG)
		}
		if data[pos] != 0xFF {
			return fmt.Errorf("%w: expected marker at offset %d", ErrMalformedJPEG, pos)
		}
		markerStart := pos
		pos++
		// Fill bytes: the spec permits extra 0xFF padding between the
		// marker prefix and the marker code itself.
		for pos < len(data) && data[pos] == 0xFF {
			pos++
		}
		if pos >= len(data) {
			return fmt.Errorf("%w: truncated marker", ErrMalformedJPEG)
		}
		marker := data[pos]
		pos++

		if marker == jpegMarkerTEM || (marker >= jpegMarkerRST0 && marker <= jpegMarkerRST7) {
			seg := jpegSegment{marker: marker, raw: data[markerStart:pos]}
			if err := visit(seg, nil); err != nil {
				return err
			}
			continue
		}

		if pos+2 > len(data) {
			return fmt.Errorf("%w: truncated length field for marker 0x%02X", ErrMalformedJPEG, marker)
		}
		length := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if length < 2 || pos+length > len(data) {
			return fmt.Errorf("%w: invalid length for marker 0x%02X", ErrMalformedJPEG, marker)
		}
		segEnd := pos + length
		seg := jpegSegment{
			marker:  marker,
			raw:     data[markerStart:segEnd],
			payload: data[pos+2 : segEnd],
		}

		if marker == jpegMarkerSOS {
			return visit(seg, data[segEnd:])
		}
		if err := visit(seg, nil); err != nil {
			return err
		}
		pos = segEnd
	}
}

// jpegSegmentKeep reports whether seg should survive StripJPEG, per
// issues/0433.md Plan §2's allowlist: keep APP0 (JFIF/JFXX) and APP2 only
// when it carries an ICC colour profile; keep every non-APPn/COM segment
// (DQT, SOF, DHT, DRI, SOS, and the rare standalone TEM/RSTn) untouched;
// drop every other APPn (APP1 covers both Exif and XMP, and a GPS
// MakerNote lives inside APP1; APP13 covers IPTC/Photoshop) and COM.
func jpegSegmentKeep(seg jpegSegment) bool {
	switch {
	case seg.marker == jpegMarkerAPP0:
		return true
	case seg.marker == jpegMarkerAPP2:
		return bytes.HasPrefix(seg.payload, iccProfileSignature)
	case seg.marker >= jpegMarkerAPP0 && seg.marker <= jpegMarkerAPPF:
		return false // every other APPn
	case seg.marker == jpegMarkerCOM:
		return false
	default:
		return true // DQT, SOF*, DHT, DRI, SOS, standalone TEM/RSTn, etc.
	}
}

// StripJPEG returns a copy of data with every non-allowlisted segment
// removed (jpegSegmentKeep), doing no decoding and no re-encoding: SOI is
// copied directly, every kept header segment is copied byte-for-byte, and
// once SOS is reached the entropy-coded scan data and EOI are copied
// verbatim without further parsing (see walkJPEGHeader's doc comment for
// why). The result is bit-identical to data except for the dropped
// segments — pixel data is never touched.
func StripJPEG(data []byte) ([]byte, error) {
	var out bytes.Buffer
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: too short", ErrMalformedJPEG)
	}
	out.Write(data[:2]) // SOI

	err := walkJPEGHeader(data, func(seg jpegSegment, tail []byte) error {
		if seg.marker == jpegMarkerSOS {
			if jpegSegmentKeep(seg) {
				out.Write(seg.raw)
				out.Write(tail)
			}
			return nil
		}
		if jpegSegmentKeep(seg) {
			out.Write(seg.raw)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ErrJPEGMetadataSurvived is returned by VerifyJPEGStripped when a segment
// StripJPEG should have dropped is still present in the supposedly-stripped
// output — see media.go's VerifyStripped doc comment for why this
// post-condition check exists.
var ErrJPEGMetadataSurvived = errors.New("media: dropped JPEG segment survived stripping")

// VerifyJPEGStripped re-walks data (StripJPEG's own output) and returns
// ErrJPEGMetadataSurvived if any segment jpegSegmentKeep would refuse is
// still present.
func VerifyJPEGStripped(data []byte) error {
	return walkJPEGHeader(data, func(seg jpegSegment, tail []byte) error {
		if seg.marker == jpegMarkerSOS {
			return nil // reached the scan data; nothing more to check
		}
		if !jpegSegmentKeep(seg) {
			return fmt.Errorf("%w: marker 0x%02X present", ErrJPEGMetadataSurvived, seg.marker)
		}
		return nil
	})
}
