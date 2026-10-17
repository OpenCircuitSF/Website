package outreach

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// shortLinksQR builds output in the shape of ShortLinks' qr.RenderSVG for a
// 21x21 matrix with a 4-module quiet zone absent (size is the whole bitmap):
// an XML declaration, one svg, a white background rect, and a g of
// row-run rects with height 1.
func shortLinksQR(size int, runs [][3]int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges">`+"\n", size, size, size*8, size*8)
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%d" height="%d" fill="#ffffff"/>`+"\n", size, size)
	b.WriteString(`<g fill="#000000">` + "\n")
	for _, r := range runs {
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="1"/>`+"\n", r[0], r[1], r[2])
	}
	b.WriteString("</g>\n</svg>\n")
	return b.String()
}

var sampleRuns = [][3]int{{4, 4, 7}, {4, 5, 1}, {10, 5, 1}, {4, 6, 1}, {6, 6, 3}, {10, 6, 1}, {20, 20, 1}}

func TestSanitizeQRSVGRebuildsFromIntegers(t *testing.T) {
	in := shortLinksQR(21, sampleRuns)
	out, err := SanitizeQRSVG([]byte(in))
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	s := string(out)
	if !strings.HasPrefix(s, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 21 21" shape-rendering="crispEdges">`) {
		t.Errorf("unexpected head: %s", s[:min(120, len(s))])
	}
	if strings.Contains(s, "width=\"168\"") || strings.Contains(s, "<?xml") {
		t.Errorf("output kept the sized or declared form: %s", s)
	}
	if got := strings.Count(s, `height="1"/>`); got != len(sampleRuns) {
		t.Errorf("dark rects = %d, want %d", got, len(sampleRuns))
	}
	for _, r := range sampleRuns {
		want := fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="1"/>`, r[0], r[1], r[2])
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
	// Byte-stable: sanitizing the output again changes nothing.
	again, err := SanitizeQRSVG(out)
	if err != nil || string(again) != s {
		t.Errorf("second pass differs or failed: %v", err)
	}
}

func TestSanitizeQRSVGRejects(t *testing.T) {
	good := shortLinksQR(21, sampleRuns)
	cases := map[string]string{
		"script element":      strings.Replace(good, "<g fill", `<script>alert(1)</script><g fill`, 1),
		"onload attribute":    strings.Replace(good, "<svg ", `<svg onload="alert(1)" `, 1),
		"foreignObject":       strings.Replace(good, "</svg>", `<foreignObject width="1" height="1"></foreignObject></svg>`, 1),
		"xlink href":          strings.Replace(good, `<g fill="#000000">`, `<g fill="#000000"><rect x="1" y="1" width="1" height="1" xlink:href="javascript:alert(1)"/>`, 1),
		"doctype with entity": strings.Replace(good, "<svg ", `<!DOCTYPE svg [<!ENTITY x "y">]><svg `, 1),
		"style attribute":     strings.Replace(good, `<g fill="#000000">`, `<g fill="#000000" style="fill:red">`, 1),
		"non-integer coord":   strings.Replace(good, `x="4" y="4"`, `x="4.5" y="4"`, 1),
		"negative coord":      strings.Replace(good, `x="4" y="4"`, `x="-1" y="4"`, 1),
		"out of range rect":   strings.Replace(good, `x="20" y="20" width="1"`, `x="20" y="20" width="5"`, 1),
		"comment":             strings.Replace(good, "<g fill", "<!-- hi --><g fill", 1),
		"text node":           strings.Replace(good, "<g fill", "hello<g fill", 1),
		"wrong namespace":     strings.Replace(good, `xmlns="http://www.w3.org/2000/svg"`, `xmlns="http://example.com/x"`, 1),
		"tiny viewBox":        strings.ReplaceAll(strings.Replace(good, `viewBox="0 0 21 21"`, `viewBox="0 0 5 5"`, 1), `width="21" height="21"`, `width="5" height="5"`),
		"non-square viewBox":  strings.Replace(good, `viewBox="0 0 21 21"`, `viewBox="0 0 21 22"`, 1),
		"second svg":          strings.Replace(good, "</svg>", "</svg><svg xmlns=\"http://www.w3.org/2000/svg\"></svg>", 1),
		"non-xml pi":          strings.Replace(good, `<?xml version="1.0" encoding="UTF-8"?>`, `<?php echo 1 ?>`, 1),
		"truncated":           good[:len(good)/2],
		"empty":               "",
		"oversize":            strings.Repeat(" ", qrMaxBytes+1),
		"no dark modules":     shortLinksQR(21, nil),
		"bad background":      strings.Replace(good, `width="21" height="21" fill`, `width="9" height="9" fill`, 1),
	}
	for name, in := range cases {
		out, err := SanitizeQRSVG([]byte(in))
		if err == nil {
			t.Errorf("%s: accepted, output %q", name, out)
			continue
		}
		if !errors.Is(err, ErrBadQRSVG) {
			t.Errorf("%s: err = %v, want ErrBadQRSVG", name, err)
		}
	}
}

func TestSanitizeQRSVGRectLimit(t *testing.T) {
	runs := make([][3]int, 0, qrMaxRects+1)
	for i := 0; i <= qrMaxRects; i++ {
		runs = append(runs, [3]int{0, i % 200, 1})
	}
	if _, err := SanitizeQRSVG([]byte(shortLinksQR(200, runs))); err == nil {
		t.Error("accepted more than the rect limit")
	}
}
