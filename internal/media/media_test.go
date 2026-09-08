package media

import (
	"errors"
	"strings"
	"testing"
)

func TestSniff(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		want    Format
		wantErr error
	}{
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0}, FormatJPEG, nil},
		{"png", append(append([]byte{}, pngSignature...), 0, 0, 0), FormatPNG, nil},
		{"heic", isoBaseMediaFile("heic"), "", ErrHEICUnsupported},
		{"avif", isoBaseMediaFile("avif"), "", ErrHEICUnsupported},
		{"gif87", []byte("GIF87a"), "", ErrUnsupportedFormat},
		{"gif89", []byte("GIF89a"), "", ErrUnsupportedFormat},
		{"webp", append(append([]byte("RIFF"), 0, 0, 0, 0), []byte("WEBP")...), "", ErrUnsupportedFormat},
		{"svg", []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"), "", ErrUnsupportedFormat},
		{"svg with xml decl", []byte("<?xml version=\"1.0\"?><svg></svg>"), "", ErrUnsupportedFormat},
		{"garbage", []byte("this is not an image at all"), "", ErrUnrecognizedFormat},
		{"empty", []byte{}, "", ErrUnrecognizedFormat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Sniff(c.data)
			if got != c.want {
				t.Errorf("Sniff() format = %q, want %q", got, c.want)
			}
			if !errors.Is(err, c.wantErr) {
				t.Errorf("Sniff() err = %v, want %v", err, c.wantErr)
			}
		})
	}
}

// isoBaseMediaFile builds a minimal ftyp box naming brand — enough for
// isHEIC to recognize it, nothing more.
func isoBaseMediaFile(brand string) []byte {
	out := make([]byte, 12)
	out[3] = 12 // size (unused by isHEIC, but structurally plausible)
	copy(out[4:8], "ftyp")
	copy(out[8:12], brand)
	return out
}

func TestCheckDimensions(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		wantErr       bool
	}{
		{"typical phone photo", 4032, 3024, false},
		{"tiny", 1, 1, false},
		{"exactly at edge limit", MaxImageEdgePixels, 100, false},
		{"exceeds edge limit", MaxImageEdgePixels + 1, 100, true},
		{"the plan's own example: 8000x6000", 8000, 6000, true},
		{"zero width", 0, 100, true},
		{"negative height", 100, -1, true},
		{"under edge but over total pixels", 5000, 5000, true}, // 25MP > 24MP cap
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckDimensions(c.width, c.height)
			if (err != nil) != c.wantErr {
				t.Errorf("CheckDimensions(%d,%d) err = %v, wantErr %v", c.width, c.height, err, c.wantErr)
			}
		})
	}
}

func TestSanitizeStem(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"soldering-101.jpg", "soldering-101"},
		{"IMG_1234.JPG", "img-1234"},
		{"My Photo (Final)!!.png", "my-photo-final"},
		{"../../etc/passwd.jpg", "passwd"},
		{"C:\\Users\\me\\photo.jpg", "photo"},
		{"", "image"},
		{"日本語.jpg", "image"},
		{strings.Repeat("a", 100) + ".jpg", strings.Repeat("a", 48)},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := SanitizeStem(c.in); got != c.want {
				t.Errorf("SanitizeStem(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestFilename_NoTraversalAndDeterministic(t *testing.T) {
	data := []byte("some stripped bytes")
	name := Filename("../../evil/soldering.jpg", data, FormatJPEG)

	if strings.ContainsAny(name, "/\\") {
		t.Errorf("Filename contains a path separator: %q", name)
	}
	if strings.Contains(name, "..") {
		t.Errorf("Filename contains '..': %q", name)
	}
	if !strings.HasSuffix(name, ".jpg") {
		t.Errorf("Filename %q does not end in .jpg", name)
	}
}

func TestFilename_ExactSameUploadConverges(t *testing.T) {
	data := []byte("some stripped bytes")
	a := Filename("soldering.jpg", data, FormatJPEG)
	b := Filename("soldering.jpg", data, FormatJPEG)
	if a != b {
		t.Errorf("re-uploading the identical file produced different names: %q vs %q", a, b)
	}
}

func TestFilename_IdenticalBytesConverge(t *testing.T) {
	data := []byte("identical stripped bytes")
	a := Filename("photo-one.jpg", data, FormatJPEG)
	b := Filename("photo-two.jpg", data, FormatJPEG)
	// Different original names but identical stripped bytes converge on
	// different stems but the SAME hash suffix -- the two names differ (the
	// stem differs) but the content-derived hash portion is identical,
	// which is what makes cache-busting/dedup work at the byte level rather
	// than the filename level.
	hashOf := func(name string) string {
		i := strings.LastIndex(name, "-")
		j := strings.LastIndex(name, ".")
		return name[i+1 : j]
	}
	if hashOf(a) != hashOf(b) {
		t.Errorf("hash portion differs for identical bytes: %q vs %q", a, b)
	}
}

func TestFilename_DifferentBytesDiverge(t *testing.T) {
	a := Filename("soldering.jpg", []byte("version one"), FormatJPEG)
	b := Filename("soldering.jpg", []byte("version two"), FormatJPEG)
	if a == b {
		t.Error("different stripped bytes produced the same filename")
	}
}
