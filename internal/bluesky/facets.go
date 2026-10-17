package bluesky

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Facet is one rich-text annotation. Index offsets are UTF-8 BYTE offsets
// into the post text, as the AT Protocol requires.
type Facet struct {
	Index    FacetIndex     `json:"index"`
	Features []FacetFeature `json:"features"`
}

// FacetIndex is a half-open byte range.
type FacetIndex struct {
	ByteStart int `json:"byteStart"`
	ByteEnd   int `json:"byteEnd"`
}

// FacetFeature is a link or a tag; the unused field is omitted.
type FacetFeature struct {
	Type string `json:"$type"`
	URI  string `json:"uri,omitempty"`
	Tag  string `json:"tag,omitempty"`
}

const (
	facetLinkType = "app.bsky.richtext.facet#link"
	facetTagType  = "app.bsky.richtext.facet#tag"
)

var (
	linkRE = regexp.MustCompile(`https?://[^\s]+`)
	tagRE  = regexp.MustCompile(`(?:^|\s)(#[\p{L}\p{M}\p{N}_]+)`)
)

// DetectFacets finds links and hashtags in text. Go strings are UTF-8 and
// the regexp package reports byte offsets, so the offsets are used as
// returned and never pass through a rune conversion.
func DetectFacets(text string) []Facet {
	facets := []Facet{}
	for _, m := range linkRE.FindAllStringIndex(text, -1) {
		start, end := m[0], m[1]
		end = trimLinkEnd(text, start, end)
		if end <= start {
			continue
		}
		facets = append(facets, Facet{
			Index:    FacetIndex{ByteStart: start, ByteEnd: end},
			Features: []FacetFeature{{Type: facetLinkType, URI: text[start:end]}},
		})
	}
	nLinks := len(facets)
	for _, m := range tagRE.FindAllStringSubmatchIndex(text, -1) {
		start, end := m[2], m[3]
		tag := text[start+1 : end]
		if allDigits(tag) || utf8.RuneCountInString(tag) > 64 {
			continue
		}
		if overlapsAny(facets[:nLinks], start, end) {
			continue
		}
		facets = append(facets, Facet{
			Index:    FacetIndex{ByteStart: start, ByteEnd: end},
			Features: []FacetFeature{{Type: facetTagType, Tag: tag}},
		})
	}
	sort.SliceStable(facets, func(i, j int) bool { return facets[i].Index.ByteStart < facets[j].Index.ByteStart })
	return facets
}

// trimLinkEnd drops trailing sentence punctuation from a matched URL, and a
// closing parenthesis when the URL has no opening one.
func trimLinkEnd(text string, start, end int) int {
	for end > start {
		c := text[end-1]
		switch {
		case strings.IndexByte(".,;:!?'\"", c) >= 0:
			end--
		case c == ')' && !strings.Contains(text[start:end], "("):
			end--
		default:
			return end
		}
	}
	return end
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func overlapsAny(fs []Facet, start, end int) bool {
	for _, f := range fs {
		if start < f.Index.ByteEnd && end > f.Index.ByteStart {
			return true
		}
	}
	return false
}
