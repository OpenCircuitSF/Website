package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// pngKeepChunkTypes is the PNG chunk allowlist (issues/0433.md Plan §2):
// IHDR/PLTE/IDAT/IEND are required to decode at all, and the rendering
// ancillaries tRNS/gAMA/cHRM/sRGB/iCCP/sBIT affect how the pixel data is
// displayed (transparency, gamma, colour space). Everything else is
// dropped — that is where eXIf, tEXt, iTXt, zTXt, and tIME live.
var pngKeepChunkTypes = map[string]bool{
	"IHDR": true,
	"PLTE": true,
	"IDAT": true,
	"IEND": true,
	"tRNS": true,
	"gAMA": true,
	"cHRM": true,
	"sRGB": true,
	"iCCP": true,
	"sBIT": true,
}

// ErrMalformedPNG is returned by the PNG walker/stripper when the byte
// stream does not follow PNG's chunk structure.
var ErrMalformedPNG = errors.New("media: malformed PNG chunk stream")

// pngChunk is one chunk found while walking a PNG's chunk stream.
type pngChunk struct {
	typ string
	raw []byte // the whole chunk on disk: 4-byte length + 4-byte type + data + 4-byte CRC
}

// walkPNGChunks walks data's chunk stream (8-byte signature, then a
// sequence of length-prefixed chunks each closed with a CRC), calling visit
// once per chunk. It stops after visiting IEND, which the PNG spec requires
// to be the last chunk.
func walkPNGChunks(data []byte, visit func(c pngChunk) error) error {
	if len(data) < 8 || !bytes.Equal(data[:8], pngSignature) {
		return fmt.Errorf("%w: missing signature", ErrMalformedPNG)
	}
	pos := 8
	for {
		if pos+8 > len(data) {
			return fmt.Errorf("%w: truncated chunk header at offset %d", ErrMalformedPNG, pos)
		}
		length := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		typ := string(data[pos+4 : pos+8])
		total := 12 + length // 4 length + 4 type + length data + 4 CRC
		if length > len(data) || pos+total > len(data) {
			return fmt.Errorf("%w: invalid length for chunk %q", ErrMalformedPNG, typ)
		}
		chunk := pngChunk{typ: typ, raw: data[pos : pos+total]}
		if err := visit(chunk); err != nil {
			return err
		}
		pos += total
		if typ == "IEND" {
			return nil
		}
	}
}

// StripPNG returns a copy of data with every non-allowlisted chunk removed
// (pngKeepChunkTypes). Nothing is recomputed: each kept chunk, CRC included,
// is copied whole, exactly as issues/0433.md's plan specifies — this is
// chunk surgery, not a re-encode.
func StripPNG(data []byte) ([]byte, error) {
	var out bytes.Buffer
	if len(data) < 8 {
		return nil, fmt.Errorf("%w: too short", ErrMalformedPNG)
	}
	out.Write(data[:8]) // signature

	err := walkPNGChunks(data, func(c pngChunk) error {
		if pngKeepChunkTypes[c.typ] {
			out.Write(c.raw)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ErrPNGMetadataSurvived is returned by VerifyPNGStripped when a chunk
// StripPNG should have dropped is still present in the supposedly-stripped
// output.
var ErrPNGMetadataSurvived = errors.New("media: dropped PNG chunk survived stripping")

// VerifyPNGStripped re-walks data (StripPNG's own output) and returns
// ErrPNGMetadataSurvived if any non-allowlisted chunk is still present.
func VerifyPNGStripped(data []byte) error {
	return walkPNGChunks(data, func(c pngChunk) error {
		if !pngKeepChunkTypes[c.typ] {
			return fmt.Errorf("%w: chunk %q present", ErrPNGMetadataSurvived, c.typ)
		}
		return nil
	})
}
