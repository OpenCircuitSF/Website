package bluesky

import (
	"strings"
	"testing"
)

func TestDetectFacets_ByteOffsets(t *testing.T) {
	type want struct {
		start, end int
		text       string // text[start:end] must equal this
		link       string // expected uri, or ""
		tag        string // expected tag, or ""
	}
	cases := []struct {
		name string
		text string
		want []want
	}{
		{"ascii link", "See https://go.example/abc", []want{{4, 26, "https://go.example/abc", "https://go.example/abc", ""}}},
		{"two-byte e-acute shifts offsets", "Café https://go.example/k", []want{{6, 26, "https://go.example/k", "https://go.example/k", ""}}},
		{"emoji, tag, trailing dot excluded", "\U0001F527 Solder #DIY https://go.example/k.", []want{
			{12, 16, "#DIY", "", "DIY"},
			{17, 37, "https://go.example/k", "https://go.example/k", ""},
		}},
		{"zwj family is 18 bytes", "\U0001F469‍\U0001F469‍\U0001F467 family #Workshop", []want{{26, 35, "#Workshop", "", "Workshop"}}},
		{"cjk", "東京 #電子工作 https://go.example/x", []want{
			{7, 20, "#電子工作", "", "電子工作"},
			{21, 41, "https://go.example/x", "https://go.example/x", ""},
		}},
		{"closing paren excluded", "(see https://go.example/a)", []want{{5, 25, "https://go.example/a", "https://go.example/a", ""}}},
		{"digits and mid-word hash are not tags, fragment stays in link", "#123 a#tag https://go.example/p#frag", []want{
			{11, 36, "https://go.example/p#frag", "https://go.example/p#frag", ""},
		}},
		{"empty", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectFacets(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d facets %+v, want %d", len(got), got, len(tc.want))
			}
			for i, w := range tc.want {
				g := got[i]
				if g.Index.ByteStart != w.start || g.Index.ByteEnd != w.end {
					t.Errorf("facet %d range = %d-%d, want %d-%d", i, g.Index.ByteStart, g.Index.ByteEnd, w.start, w.end)
				}
				if sub := tc.text[g.Index.ByteStart:g.Index.ByteEnd]; sub != w.text {
					t.Errorf("facet %d covers %q, want %q", i, sub, w.text)
				}
				// Cross-check only: the hard-coded offsets above are the oracle.
				if idx := strings.Index(tc.text, w.text); idx >= 0 && w.tag != "" && idx != w.start {
					t.Errorf("facet %d: strings.Index disagrees with the table (%d vs %d)", i, idx, w.start)
				}
				f := g.Features[0]
				if w.link != "" && (f.Type != facetLinkType || f.URI != w.link) {
					t.Errorf("facet %d feature = %+v, want link %q", i, f, w.link)
				}
				if w.tag != "" && (f.Type != facetTagType || f.Tag != w.tag) {
					t.Errorf("facet %d feature = %+v, want tag %q", i, f, w.tag)
				}
			}
		})
	}
}

func TestDetectFacets_NeverNil(t *testing.T) {
	if DetectFacets("no links here") == nil {
		t.Error("DetectFacets should return an empty slice, not nil")
	}
}
