package seo

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Tests for #0528: a published campaign's meta description derives from the
// body's first paragraph of rendered prose when no preheader is set, instead
// of describing every preheader-less newsletter with the same generic
// sentence.

func TestArchiveDescription_PreheaderWins(t *testing.T) {
	e := ArchiveEntry{
		Preheader: "A hand-picked summary.",
		BodyMD:    "# Heading\n\nFirst paragraph of the body.",
		Published: true,
	}
	if got := archiveDescription(e); got != "A hand-picked summary." {
		t.Errorf("archiveDescription = %q, want the preheader", got)
	}
}

func TestArchiveDescription_WhitespaceOnlyPreheaderDoesNotWin(t *testing.T) {
	e := ArchiveEntry{
		Preheader: "   \n\t ",
		BodyMD:    "Real first paragraph.",
		Published: true,
	}
	if got := archiveDescription(e); got != "Real first paragraph." {
		t.Errorf("archiveDescription = %q, want the body paragraph", got)
	}
}

func TestArchiveDescription_FirstParagraphSkipsLeadingHeadings(t *testing.T) {
	// The September newsletter's shape: a body that opens with headings.
	e := ArchiveEntry{
		BodyMD: "# September news\n\n## What happened\n\nWe ran two soldering workshops last month.\n\n## Upcoming\n\nMore soon.",
	}
	got := archiveDescription(e)
	if got != "We ran two soldering workshops last month." {
		t.Errorf("archiveDescription = %q, want the first paragraph past the headings", got)
	}
}

func TestArchiveDescription_StripsInlineMarkdown(t *testing.T) {
	e := ArchiveEntry{
		BodyMD: "Intro with **bold**, *italics*, `code`, and a [link](https://example.com/x) inside.",
	}
	got := archiveDescription(e)
	if got != "Intro with bold, italics, code, and a link inside." {
		t.Errorf("archiveDescription = %q, want inline Markdown resolved to plain text", got)
	}
}

func TestArchiveDescription_DropsRawHTMLAndScript(t *testing.T) {
	// Goldmark safe mode drops raw HTML, so a hostile or sloppy body cannot
	// smuggle markup -- let alone a script -- into the description.
	e := ArchiveEntry{
		BodyMD: "<script>alert(1)</script>\n\nClean paragraph after the raw HTML.",
	}
	got := archiveDescription(e)
	if got != "Clean paragraph after the raw HTML." {
		t.Errorf("archiveDescription = %q, want the prose paragraph with raw HTML dropped", got)
	}
}

func TestArchiveDescription_CollapsesWhitespace(t *testing.T) {
	e := ArchiveEntry{
		BodyMD: "A paragraph hard-wrapped\nacross several\n    lines   with   extra   spaces.",
	}
	got := archiveDescription(e)
	if got != "A paragraph hard-wrapped across several lines with extra spaces." {
		t.Errorf("archiveDescription = %q, want whitespace collapsed", got)
	}
}

func TestArchiveDescription_TruncatesAtWordBoundaryWithEllipsis(t *testing.T) {
	long := strings.Repeat("word ", 60) // 300 runes of prose
	e := ArchiveEntry{BodyMD: long}
	got := archiveDescription(e)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("archiveDescription = %q, want a trailing ellipsis", got)
	}
	if n := utf8.RuneCountInString(got); n > archiveDescriptionMaxLen+1 {
		t.Errorf("archiveDescription is %d runes, want at most %d plus the ellipsis", n, archiveDescriptionMaxLen)
	}
	head := strings.TrimSuffix(got, "…")
	if !strings.HasPrefix(long, head+" ") && head != strings.TrimRight(strings.TrimSpace(long[:len(head)]), " ") {
		t.Errorf("truncation %q did not cut at a word boundary of the source", head)
	}
}

func TestArchiveDescription_NoEllipsisWhenItFits(t *testing.T) {
	e := ArchiveEntry{BodyMD: "Short and sweet."}
	got := archiveDescription(e)
	if strings.ContainsRune(got, '…') {
		t.Errorf("archiveDescription = %q, want no ellipsis on an untruncated description", got)
	}
	if got != "Short and sweet." {
		t.Errorf("archiveDescription = %q, want the paragraph verbatim", got)
	}
}

func TestArchiveDescription_GenericFallbackWhenNoProse(t *testing.T) {
	cases := map[string]ArchiveEntry{
		"empty body":    {BodyMD: ""},
		"blank body":    {BodyMD: "  \n  "},
		"headings only": {BodyMD: "# One\n\n## Two"},
		"list only":     {BodyMD: "- a\n- b"},
	}
	for name, e := range cases {
		if got := archiveDescription(e); got != archiveDescriptionFallback {
			t.Errorf("%s: archiveDescription = %q, want the generic fallback %q", name, got, archiveDescriptionFallback)
		}
	}
}

func TestArchiveDescription_ImageRendersAsThePageRendersIt(t *testing.T) {
	// Goldmark's safe mode renders an image as "[image not included: alt]"
	// -- the exact text the archive page's own body shows for that
	// paragraph. The description uses the same renderer's output as ground
	// truth, so it stays consistent with the page rather than inventing a
	// second image policy here.
	e := ArchiveEntry{BodyMD: "![banner](https://example.com/banner.png)"}
	if got := archiveDescription(e); got != "[image not included: banner]" {
		t.Errorf("archiveDescription = %q, want the renderer's own image placeholder", got)
	}

	// An image followed by prose: the image paragraph is still the first
	// paragraph, same as on the page.
	e2 := ArchiveEntry{
		BodyMD: "![banner](https://example.com/banner.png)\n\nThe real opening paragraph.",
	}
	if got := archiveDescription(e2); got != "[image not included: banner]" {
		t.Errorf("archiveDescription = %q, want the first paragraph's own rendered text", got)
	}
}

func TestArchiveDescription_Deterministic(t *testing.T) {
	e := ArchiveEntry{BodyMD: "# H\n\nSome paragraph with **emphasis** and plenty of words to consider. " + strings.Repeat("More ", 40)}
	first := archiveDescription(e)
	for i := 0; i < 5; i++ {
		if again := archiveDescription(e); again != first {
			t.Fatalf("archiveDescription is not deterministic: %q vs %q", again, first)
		}
	}
}

// TestRender_ArchiveDetailDescriptionFromBody pins the derivation end to end
// through archiveRouteMeta: a published campaign with no preheader carries
// its first paragraph as description, og:description, and twitter:description
// (the latter two mirror the description at substitution time, #0273).
func TestRender_ArchiveDetailDescriptionFromBody(t *testing.T) {
	source := fakeArchiveSource{
		"09-2026": {
			Slug: "09-2026", Subject: "September newsletter",
			BodyMD:    "# September news\n\nWe soldered, we debugged, we shipped.",
			Published: true, ArchivedAt: "2026-09-01T18:00:00Z", UpdatedAt: "2026-09-01",
		},
	}
	r := newTestRendererWithArchive(source)
	body := string(r.Render("/archive/09-2026"))

	for _, token := range []string{"We soldered, we debugged, we shipped."} {
		if !strings.Contains(body, token) {
			t.Errorf("rendered page missing body-derived description %q:\n%s", token, body)
		}
	}
	if strings.Contains(body, "Read this email from Open Circuit SF.") {
		t.Errorf("rendered page still carries the generic fallback despite available prose:\n%s", body)
	}
}

func TestRender_ArchiveDetailDescriptionEscaped(t *testing.T) {
	// #0528 criterion 6: the description is HTML-escaped where it is
	// injected, exactly as the preheader always was.
	source := fakeArchiveSource{
		"tricky": {
			Slug: "tricky", Subject: "Tricky",
			Preheader: `Quotes " and <tags> & amps`, Published: true,
		},
	}
	r := newTestRendererWithArchive(source)
	body := string(r.Render("/archive/tricky"))
	if !strings.Contains(body, `Quotes &#34; and &lt;tags&gt; &amp; amps`) {
		t.Errorf("description not HTML-escaped in rendered page:\n%s", body)
	}
	if strings.Contains(body, `<tags>`) {
		t.Errorf("raw markup leaked into the rendered description:\n%s", body)
	}
}
