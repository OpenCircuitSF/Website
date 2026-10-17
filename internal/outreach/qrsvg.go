package outreach

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// ErrBadQRSVG is returned by SanitizeQRSVG for any input that is not exactly
// the shape ShortLinks' QR renderer emits.
var ErrBadQRSVG = errors.New("outreach: QR SVG is not in the expected shape")

const (
	svgNS          = "http://www.w3.org/2000/svg"
	qrMaxBytes     = 256 << 10
	qrMaxRects     = 40000
	qrMinModules   = 21
	qrMaxModules   = 200
	qrDarkFill     = "#000"
	qrBackdropFill = "#fff"
)

var fillPattern = regexp.MustCompile(`^#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`)

func qrErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadQRSVG, fmt.Sprintf(format, a...))
}

// qrInt parses a plain non-negative decimal integer no longer than six
// digits.
func qrInt(s string) (int, bool) {
	if s == "" || len(s) > 6 {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

type qrRect struct{ x, y, w, h int }

// SanitizeQRSVG validates raw as a ShortLinks QR code and returns a freshly
// built SVG made only from the parsed integers. Nothing from the input is
// copied through, so no attribute, element, entity or script the upstream
// sent can reach the output. The result carries no width or height and so
// scales to whatever box it is placed in.
//
// The input must be an optional XML declaration and one svg element holding
// one background rect and one g of integer rects, with whitespace between
// them and nothing else. Anything else, a non-integer or out-of-range
// number, or an input over 256 KiB or 40,000 rects is rejected with
// ErrBadQRSVG.
func SanitizeQRSVG(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > qrMaxBytes {
		return nil, qrErr("size %d bytes", len(raw))
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = true
	dec.Entity = nil

	var (
		modules   int
		sawSVG    bool
		sawBack   bool
		sawGroup  bool
		depth     int // 0 outside svg, 1 in svg, 2 in g
		inGroup   bool
		dark      []qrRect
		seenFirst bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, qrErr("parse: %v", err)
		}
		switch t := tok.(type) {
		case xml.ProcInst:
			if seenFirst || t.Target != "xml" {
				return nil, qrErr("unexpected processing instruction")
			}
		case xml.Comment, xml.Directive:
			return nil, qrErr("comments and directives are not allowed")
		case xml.CharData:
			if len(bytes.TrimSpace(t)) != 0 {
				return nil, qrErr("unexpected text")
			}
		case xml.StartElement:
			seenFirst = true
			if t.Name.Space != svgNS {
				return nil, qrErr("element outside the SVG namespace")
			}
			switch {
			case depth == 0 && t.Name.Local == "svg" && !sawSVG:
				sawSVG = true
				n, err := parseSVGRoot(t.Attr)
				if err != nil {
					return nil, err
				}
				modules = n
				depth = 1
			case depth == 1 && t.Name.Local == "rect" && !sawBack:
				sawBack = true
				r, err := parseRect(t.Attr, true)
				if err != nil {
					return nil, err
				}
				if r != (qrRect{0, 0, modules, modules}) {
					return nil, qrErr("background rect does not cover the viewBox")
				}
				depth = 2
			case depth == 1 && t.Name.Local == "g" && !sawGroup:
				sawGroup = true
				for _, a := range t.Attr {
					if a.Name.Space != "" || a.Name.Local != "fill" || !fillPattern.MatchString(a.Value) {
						return nil, qrErr("attribute %q on g", a.Name.Local)
					}
				}
				inGroup = true
				depth = 2
			case depth == 2 && inGroup && t.Name.Local == "rect":
				r, err := parseRect(t.Attr, false)
				if err != nil {
					return nil, err
				}
				if r.w < 1 || r.h < 1 || r.x+r.w > modules || r.y+r.h > modules {
					return nil, qrErr("rect outside the viewBox")
				}
				if len(dark) >= qrMaxRects {
					return nil, qrErr("too many rects")
				}
				dark = append(dark, r)
				depth = 3
			default:
				return nil, qrErr("unexpected element %q", t.Name.Local)
			}
		case xml.EndElement:
			switch depth {
			case 3:
				depth = 2
			case 2:
				depth = 1
				inGroup = false
			case 1:
				depth = 0
			}
		}
	}
	if !sawSVG || !sawBack || !sawGroup || depth != 0 {
		return nil, qrErr("incomplete document")
	}
	if len(dark) == 0 {
		return nil, qrErr("no dark modules")
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="%s" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, svgNS, modules, modules)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/><g fill="%s">`, modules, modules, qrBackdropFill, qrDarkFill)
	for _, r := range dark {
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d"/>`, r.x, r.y, r.w, r.h)
	}
	b.WriteString(`</g></svg>`)
	return []byte(b.String()), nil
}

func parseSVGRoot(attrs []xml.Attr) (int, error) {
	var vb string
	for _, a := range attrs {
		switch {
		case a.Name.Space == "" && a.Name.Local == "xmlns" && a.Value == svgNS:
		case a.Name.Space == "" && a.Name.Local == "viewBox":
			vb = a.Value
		case a.Name.Space == "" && (a.Name.Local == "width" || a.Name.Local == "height"):
			if _, ok := qrInt(a.Value); !ok {
				return 0, qrErr("svg %s is not an integer", a.Name.Local)
			}
		case a.Name.Space == "" && a.Name.Local == "shape-rendering" && a.Value == "crispEdges":
		default:
			return 0, qrErr("attribute %q on svg", a.Name.Local)
		}
	}
	parts := strings.Fields(vb)
	if len(parts) != 4 || parts[0] != "0" || parts[1] != "0" || parts[2] != parts[3] {
		return 0, qrErr("viewBox")
	}
	n, ok := qrInt(parts[2])
	if !ok || n < qrMinModules || n > qrMaxModules {
		return 0, qrErr("viewBox size")
	}
	return n, nil
}

func parseRect(attrs []xml.Attr, background bool) (qrRect, error) {
	var r qrRect
	got := map[string]bool{}
	for _, a := range attrs {
		if a.Name.Space != "" {
			return r, qrErr("namespaced attribute on rect")
		}
		switch a.Name.Local {
		case "x", "y", "width", "height":
			n, ok := qrInt(a.Value)
			if !ok {
				return r, qrErr("rect %s is not an integer", a.Name.Local)
			}
			got[a.Name.Local] = true
			switch a.Name.Local {
			case "x":
				r.x = n
			case "y":
				r.y = n
			case "width":
				r.w = n
			case "height":
				r.h = n
			}
		case "fill":
			if !background || !fillPattern.MatchString(a.Value) {
				return r, qrErr("fill on rect")
			}
		default:
			return r, qrErr("attribute %q on rect", a.Name.Local)
		}
	}
	if !got["width"] || !got["height"] || (!background && (!got["x"] || !got["y"])) {
		return r, qrErr("rect is missing a coordinate")
	}
	return r, nil
}
