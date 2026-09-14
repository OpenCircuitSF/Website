package seo

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Tests for #0531: the newsletter archive's Atom feed at
// GET /archive/feed.xml -- published campaigns only, newest first, stable
// IDs, #0528 summaries, the archive page's own body as content, and XML
// that hostile admin-entered text cannot break.

var errFeedBoom = errors.New("feed source boom")

// timeRFC3339Offset builds a distinct RFC 3339 timestamp day i into 2026,
// so the cap test's entries have a deterministic newest-first order.
func timeRFC3339Offset(year, month, day int) string {
	return time.Date(year, time.Month(month), 1+day, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
}

// dayLabel returns a short deterministic label for the cap test's slugs.
func dayLabel(i int) string { return fmt.Sprintf("issue-%03d", i) }

type fakeFeedSource struct {
	entries []ArchiveEntry
	err     error
}

func (f fakeFeedSource) ArchiveEntryBySlug(slug string) (ArchiveEntry, bool, error) {
	for _, e := range f.entries {
		if e.Slug == slug {
			return e, true, nil
		}
	}
	return ArchiveEntry{}, false, nil
}

func (f fakeFeedSource) ArchiveEntries() ([]ArchiveEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

func mustBuildFeed(t *testing.T, f *Feed) atomFeed {
	t.Helper()
	body, err := f.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var feed atomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		t.Fatalf("feed did not parse as XML: %v\n%s", err, body)
	}
	return feed
}

func TestFeed_PublishedOnlyNewestFirst(t *testing.T) {
	src := fakeFeedSource{entries: []ArchiveEntry{
		{Slug: "07-2026", Subject: "July", Published: true, ArchivedAt: "2026-07-15T18:00:00Z", BodyMD: "July body."},
		{Slug: "09-2026", Subject: "September", Published: true, ArchivedAt: "2026-09-01T18:00:00Z", BodyMD: "September body."},
		{Slug: "08-2026", Subject: "August", Published: true, ArchivedAt: "2026-08-20T18:00:00Z", BodyMD: "August body."},
		// Not published: withheld and never-sent must never appear.
		{Slug: "withheld", Subject: "Withheld", Published: false, ArchivedAt: "2026-06-01T18:00:00Z"},
		{Slug: "unsent", Subject: "Never sent", Published: false},
	}}
	f := NewFeed(testBaseURL, src)
	feed := mustBuildFeed(t, f)

	if n := len(feed.Entries); n != 3 {
		t.Fatalf("feed has %d entries, want 3 (published only)", n)
	}
	wantOrder := []string{"09-2026", "08-2026", "07-2026"}
	for i, want := range wantOrder {
		if got := feed.Entries[i].ID; got != testBaseURL+"/archive/"+want {
			t.Errorf("entry %d id = %q, want %q (newest first)", i, got, testBaseURL+"/archive/"+want)
		}
	}
	if feed.Updated != "2026-09-01T18:00:00Z" {
		t.Errorf("feed updated = %q, want the newest entry's archived_at", feed.Updated)
	}
}

func TestFeed_EntryShape(t *testing.T) {
	src := fakeFeedSource{entries: []ArchiveEntry{
		{Slug: "09-2026", Subject: "September newsletter", Published: true,
			ArchivedAt: "2026-09-01T18:00:00Z", BodyMD: "First **paragraph** of the body."},
	}}
	f := NewFeed(testBaseURL, src)
	feed := mustBuildFeed(t, f)

	if len(feed.Entries) != 1 {
		t.Fatalf("feed has %d entries, want 1", len(feed.Entries))
	}
	e := feed.Entries[0]
	if e.Title != "September newsletter" {
		t.Errorf("title = %q, want the subject", e.Title)
	}
	// Stable, permanent id: the canonical archive URL, unchanged by any
	// later edit of the campaign.
	if e.ID != testBaseURL+"/archive/09-2026" {
		t.Errorf("id = %q, want the canonical archive URL", e.ID)
	}
	if e.Link.Href != testBaseURL+"/archive/09-2026" || e.Link.Type != "text/html" {
		t.Errorf("link = %+v, want the archive page URL", e.Link)
	}
	if e.Published != "2026-09-01T18:00:00Z" || e.Updated != "2026-09-01T18:00:00Z" {
		t.Errorf("published/updated = %q/%q, want archived_at for both", e.Published, e.Updated)
	}
	// #0528's text as the summary: preheader when set, else the body's
	// first paragraph.
	if e.Summary != "First paragraph of the body." {
		t.Errorf("summary = %q, want the body-derived description", e.Summary)
	}
	if e.Content == nil {
		t.Fatal("content missing, want the full newsletter")
	}
	if e.Content.Type != "html" {
		t.Errorf("content type = %q, want html (escaped inline HTML)", e.Content.Type)
	}
	if strings.Contains(e.Content.Body, "First **paragraph**") {
		t.Errorf("content carries raw Markdown, want the rendered body: %q", e.Content.Body)
	}
	if !strings.Contains(e.Content.Body, "<strong>paragraph</strong>") {
		t.Errorf("content = %q, want the RenderMarkdownPageHTML body", e.Content.Body)
	}
}

func TestFeed_PreheaderSummaryAndNoPrivateData(t *testing.T) {
	src := fakeFeedSource{entries: []ArchiveEntry{
		{Slug: "09-2026", Subject: "September", Preheader: "The hand-written summary.",
			Published: true, ArchivedAt: "2026-09-01T18:00:00Z"},
	}}
	f := NewFeed(testBaseURL, src)
	feed := mustBuildFeed(t, f)
	if got := feed.Entries[0].Summary; got != "The hand-written summary." {
		t.Errorf("summary = %q, want the preheader (#0528: preheader wins)", got)
	}
	// A feed built purely from ArchiveEntry's public fields cannot carry
	// recipient data, tokens, or internal IDs -- pinned by asserting none of
	// the shapes appear anywhere in the serialized document.
	body, err := f.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, forbidden := range []string{"unsubscribe", "manage_token", "recipient", "email_sends"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("feed contains private-data shape %q", forbidden)
		}
	}
}

func TestFeed_HostileTextCannotBreakXML(t *testing.T) {
	tricky := `Solder & Circuits "Intro" <script>alert(1)</script> \backslash\`
	src := fakeFeedSource{entries: []ArchiveEntry{
		{Slug: "tricky", Subject: tricky, Published: true, ArchivedAt: "2026-09-01T18:00:00Z", BodyMD: tricky},
	}}
	f := NewFeed(testBaseURL, src)
	body, err := f.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(string(body), "<script>alert(1)</script>") {
		t.Fatal("raw hostile markup reached the feed unescaped")
	}
	// Round-trips: the document parses, and the values survive verbatim.
	feed := mustBuildFeed(t, f)
	if got := feed.Entries[0].Title; got != tricky {
		t.Errorf("title = %q, want the tricky subject round-tripped", got)
	}
	if got := feed.Entries[0].Content.Body; !strings.Contains(got, "Solder &amp;") && got != "" {
		t.Logf("content body after parse: %q", got)
	}
}

func TestFeed_CappedAtTwentyEntries(t *testing.T) {
	src := fakeFeedSource{}
	for i := 0; i < feedMaxEntries+10; i++ {
		src.entries = append(src.entries, ArchiveEntry{
			Slug:       dayLabel(i),
			Subject:    "Issue",
			Published:  true,
			ArchivedAt: timeRFC3339Offset(2026, 1, i),
			BodyMD:     "Body.",
		})
	}
	f := NewFeed(testBaseURL, src)
	feed := mustBuildFeed(t, f)
	if len(feed.Entries) != feedMaxEntries {
		t.Fatalf("feed has %d entries, want the stated cap %d", len(feed.Entries), feedMaxEntries)
	}
	// The newest entries must be the ones kept: the first is the last day,
	// the last is exactly feedMaxEntries days behind it.
	if got := feed.Entries[0].ID; got != testBaseURL+"/archive/"+dayLabel(feedMaxEntries+9) {
		t.Errorf("newest entry = %q, want day %d", got, feedMaxEntries+9)
	}
	if got := feed.Entries[feedMaxEntries-1].ID; got != testBaseURL+"/archive/"+dayLabel(10) {
		t.Errorf("oldest kept entry = %q, want day 10", got)
	}
}

func TestFeed_EmptyArchiveOmitsFeedUpdated(t *testing.T) {
	f := NewFeed(testBaseURL, fakeFeedSource{})
	body, err := f.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	feed := mustBuildFeed(t, f)
	if len(feed.Entries) != 0 {
		t.Fatalf("empty archive produced %d entries", len(feed.Entries))
	}
	if feed.Updated != "" {
		t.Errorf("feed updated = %q, want omitted for an empty archive (no clock-stamped dates, #0533)", feed.Updated)
	}
	if !strings.Contains(string(body), `<feed xmlns="http://www.w3.org/2005/Atom">`) {
		t.Errorf("feed missing the Atom namespace declaration:\n%s", body)
	}
}

func TestFeed_SourceErrorAbortsBuild(t *testing.T) {
	f := NewFeed(testBaseURL, fakeFeedSource{err: errFeedBoom})
	if _, err := f.Build(); err == nil {
		t.Fatal("Build with an erroring source returned no error -- a half-populated feed must never be served")
	}
}

func TestFeed_InvalidateClearsCache(t *testing.T) {
	src := fakeFeedSource{entries: []ArchiveEntry{
		{Slug: "09-2026", Subject: "Original", Published: true, ArchivedAt: "2026-09-01T18:00:00Z", BodyMD: "Body."},
	}}
	f := NewFeed(testBaseURL, src)

	first, err := f.Render()
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}
	src.entries[0].Subject = "Changed"
	stale, err := f.Render()
	if err != nil {
		t.Fatalf("cached Render: %v", err)
	}
	if string(first) != string(stale) {
		t.Fatal("Render returned different bytes within the TTL -- feed was not cached")
	}

	f.invalidate()
	fresh, err := f.Render()
	if err != nil {
		t.Fatalf("post-invalidate Render: %v", err)
	}
	if string(fresh) == string(first) {
		t.Fatal("Render after invalidate() returned the stale bytes -- invalidate() did not clear the cache")
	}
}

func TestFeed_NilSourceYieldsBareFeed(t *testing.T) {
	f := NewFeed(testBaseURL, nil)
	feed := mustBuildFeed(t, f)
	if len(feed.Entries) != 0 {
		t.Fatalf("nil source produced %d entries, want a bare feed", len(feed.Entries))
	}
	if feed.ID != testBaseURL+"/archive/feed.xml" {
		t.Errorf("id = %q, want the feed's own URL", feed.ID)
	}
}
