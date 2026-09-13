// Tests for #0519's server-rendered fallback content (fallback.go):
// substituted into tokenBody for every public route, empty for every
// private/token-bearing/unknown route. Uses the same fakeWorkshopSource
// (seo_test.go) and fakeArchiveSource (archive_test.go) fixtures the rest
// of this package's tests already use, plus newTestRenderer/
// newTestRendererWithArchive.
package seo

import (
	"errors"
	"html"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// publicFallbackRoutes is every route this issue's plan gives real fallback
// content: the six static/index routes, plus a published workshop and a
// published archive entry set up per-test below.
var publicStaticRoutes = []string{"/", "/about", "/privacy", "/subscribe", "/workshops", "/archive"}

// TestRender_PublicRoutesServeExactlyOneH1 is acceptance criterion 1: every
// public route serves exactly one <h1>, and a static route's <h1> text
// equals staticPageCopy's own H1 for that route.
func TestRender_PublicRoutesServeExactlyOneH1(t *testing.T) {
	workshopSource := fakeWorkshopSource{
		"solder-101": {Slug: "solder-101", Title: "Intro to Soldering", Status: WorkshopPublished, Published: true},
	}
	archiveSource := fakeArchiveSource{
		"sept-recap": {Slug: "sept-recap", Subject: "September Recap", Published: true},
	}
	r := NewRenderer([]byte(testTemplate), testBaseURL, workshopSource, archiveSource)

	for _, p := range publicStaticRoutes {
		body := string(r.Render(p))
		if got := strings.Count(body, "<h1"); got != 1 {
			t.Errorf("path %q: <h1> count = %d, want 1: %s", p, got, body)
			continue
		}
		if copy, ok := staticPageCopy[p]; ok {
			got := extractTag(t, body, "h1")
			if got != copy.H1 {
				t.Errorf("path %q: <h1> text = %q, want %q", p, got, copy.H1)
			}
		}
	}

	for _, p := range []string{"/workshops/solder-101", "/archive/sept-recap"} {
		body := string(r.Render(p))
		if got := strings.Count(body, "<h1"); got != 1 {
			t.Errorf("path %q: <h1> count = %d, want 1: %s", p, got, body)
		}
	}
}

// TestRender_PrivateAndUnknownRoutesInjectNothing is acceptance criterion
// 5: every private/token-bearing route, the 404 default, a draft workshop
// slug, and a withheld archive slug get NO injected content -- the served
// body keeps the empty <div id="app"></div>, byte for byte.
func TestRender_PrivateAndUnknownRoutesInjectNothing(t *testing.T) {
	workshopSource := fakeWorkshopSource{
		"secret-draft": {Slug: "secret-draft", Title: "Unreleased Workshop", Status: WorkshopDraft},
	}
	archiveSource := fakeArchiveSource{
		"withheld-campaign": {Slug: "withheld-campaign", Subject: "Retracted Subject", Published: false},
	}
	r := NewRenderer([]byte(testTemplate), testBaseURL, workshopSource, archiveSource)

	routes := []string{
		"/confirm", "/preferences", "/unsubscribe", "/subscribe/thanks",
		"/login", "/account", "/admin", "/register/verify", "/recover/verify",
		"/nonexistent", "/workshops/secret-draft", "/archive/withheld-campaign",
	}
	for _, p := range routes {
		body := string(r.Render(p))
		if !strings.Contains(body, `<div id="app"></div>`) {
			t.Errorf("path %q: expected the untouched empty app div, got: %s", p, body)
		}
		if strings.Contains(body, "<h1") {
			t.Errorf("path %q: expected no <h1>, got: %s", p, body)
		}
	}
}

// TestRender_InternalNavLinksArePlainAnchors is acceptance criterion 3:
// every fallbackNav href appears as a plain <a href="..."> on every public
// route.
func TestRender_InternalNavLinksArePlainAnchors(t *testing.T) {
	r := newTestRenderer(nil)
	for _, p := range publicStaticRoutes {
		body := string(r.Render(p))
		for _, link := range fallbackNav {
			want := `<a href="` + link.Href + `">`
			if !strings.Contains(body, want) {
				t.Errorf("path %q: missing nav link %s: %s", p, want, body)
			}
		}
	}
}

// TestRender_WorkshopDetailServesTitleDateVenueAndBody covers the workshop
// detail page's title, date/venue block, and body -- the same
// mailing.RenderMarkdownPageHTML output renderWorkshopBodyHTML wraps
// (#0519's remedy: heading levels demoted since this body sits under the
// workshop's own title <h1>).
func TestRender_WorkshopDetailServesTitleDateVenueAndBody(t *testing.T) {
	md := "Bring your own **tools**."
	w := Workshop{
		Slug: "solder-night", Title: "Solder Night", Status: WorkshopPublished, Published: true,
		StartsAt: "2026-09-12T18:00:00Z", EndsAt: "2026-09-12T19:30:00Z",
		LocationName: "Noisebridge", LocationAddress: "2169 Mission St, San Francisco, CA",
		BodyMD: md,
	}
	source := fakeWorkshopSource{"solder-night": w}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/solder-night"))

	if got := strings.Count(body, "<h1"); got != 1 {
		t.Fatalf("want exactly one <h1>, got %d: %s", got, body)
	}
	if !strings.Contains(body, "Solder Night") {
		t.Errorf("title missing from body: %s", body)
	}
	if !strings.Contains(body, "Sep 12, 2026, 11:00 AM") || !strings.Contains(body, "12:30 PM PDT") {
		t.Errorf("date label not found in body: %s", body)
	}
	if !strings.Contains(body, "Noisebridge") {
		t.Errorf("location name not found in body: %s", body)
	}
	if !strings.Contains(body, "2169 Mission St, San Francisco, CA") {
		t.Errorf("location address not found in body: %s", body)
	}
	want, err := mailing.RenderMarkdownPageHTML(md)
	if err != nil {
		t.Fatalf("RenderMarkdownPageHTML: %v", err)
	}
	if !strings.Contains(body, want) {
		t.Errorf("body does not contain the exact RenderMarkdownPageHTML output %q: %s", want, body)
	}
	if !strings.Contains(body, `<a href="/workshops">`) {
		t.Errorf("missing 'see all workshops' link: %s", body)
	}
}

// TestRender_WorkshopDetailBodyHeadingDoesNotBecomeSecondH1 is #0519's
// review-bounced defect 1, for the workshop detail page: a body starting
// with a Markdown "# Heading" must not itself render as an <h1> alongside
// the page's own title <h1>.
func TestRender_WorkshopDetailBodyHeadingDoesNotBecomeSecondH1(t *testing.T) {
	w := Workshop{
		Slug: "heading-body", Title: "Heading Body Workshop", Status: WorkshopPublished, Published: true,
		BodyMD: "# Body Heading\n\nSome text.",
	}
	source := fakeWorkshopSource{"heading-body": w}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/heading-body"))

	if got := strings.Count(body, "<h1"); got != 1 {
		t.Fatalf("want exactly one <h1>, got %d: %s", got, body)
	}
	if !strings.Contains(body, "<h2>Body Heading</h2>") {
		t.Errorf("expected the body heading demoted to <h2>, got: %s", body)
	}
}

// TestRender_CanceledWorkshopDetailSaysCanceled proves the canceled notice
// text appears on a canceled (but previously published) workshop's detail
// page.
func TestRender_CanceledWorkshopDetailSaysCanceled(t *testing.T) {
	w := fullWorkshop("canceled-night", WorkshopCanceled)
	source := fakeWorkshopSource{"canceled-night": w}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/canceled-night"))

	if !strings.Contains(body, "This workshop has been canceled.") {
		t.Errorf("canceled notice missing: %s", body)
	}
}

// TestRender_WorkshopsIndexMirrorsListVisible pins buildWorkshopsIndexItems'
// mirror of internal/workshops/store.go's ListVisible: visibility, upcoming/
// past split, sort order (ascending with an unscheduled workshop last;
// descending for past), and the canceled badge text.
func TestRender_WorkshopsIndexMirrorsListVisible(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	source := fakeWorkshopSource{
		"draft-ws":       {Slug: "draft-ws", Title: "Draft Workshop", Status: WorkshopDraft, Published: false},
		"unpublished-ws": {Slug: "unpublished-ws", Title: "Unpublished Workshop", Status: WorkshopUnpublished, Published: true},
		"never-canceled": {Slug: "never-canceled", Title: "Never Published Canceled", Status: WorkshopCanceled, Published: false},
		"tba":            {Slug: "tba", Title: "TBA Workshop", Status: WorkshopPublished, Published: true},
		"soon":           {Slug: "soon", Title: "Soon Workshop", Status: WorkshopPublished, Published: true, StartsAt: "2026-09-13T18:00:00Z"},
		"mid":            {Slug: "mid", Title: "Canceled Upcoming", Status: WorkshopCanceled, Published: true, StartsAt: "2026-09-15T18:00:00Z"},
		"later":          {Slug: "later", Title: "Later Workshop", Status: WorkshopPublished, Published: true, StartsAt: "2026-09-20T18:00:00Z"},
		"past-recent":    {Slug: "past-recent", Title: "Past Recent Workshop", Status: WorkshopPublished, Published: true, StartsAt: "2026-09-10T18:00:00Z"},
		"past-older":     {Slug: "past-older", Title: "Past Older Workshop", Status: WorkshopPublished, Published: true, StartsAt: "2026-09-01T18:00:00Z"},
	}
	r := newTestRenderer(source)
	r.now = func() time.Time { return now }
	body := string(r.Render("/workshops"))

	for _, absent := range []string{"Draft Workshop", "Unpublished Workshop", "Never Published Canceled"} {
		if strings.Contains(body, absent) {
			t.Errorf("body should not contain invisible workshop %q: %s", absent, body)
		}
	}
	if !strings.Contains(body, "Canceled · ") {
		t.Errorf("expected the canceled badge prefix in the body: %s", body)
	}

	idx := func(s string) int {
		i := strings.Index(body, s)
		if i == -1 {
			t.Fatalf("expected to find %q in body: %s", s, body)
		}
		return i
	}
	upcomingHeading := idx(workshopsUpcomingH2)
	soon := idx("Soon Workshop")
	mid := idx("Canceled Upcoming")
	later := idx("Later Workshop")
	tba := idx("TBA Workshop")
	pastHeading := idx(workshopsPastH2)
	pastRecent := idx("Past Recent Workshop")
	pastOlder := idx("Past Older Workshop")

	if !(upcomingHeading < soon && soon < mid && mid < later && later < tba && tba < pastHeading) {
		t.Errorf("upcoming order wrong (want soon, canceled-upcoming, later, tba, all before Past workshops): %s", body)
	}
	if !(pastHeading < pastRecent && pastRecent < pastOlder) {
		t.Errorf("past order wrong (want past-recent before past-older, descending): %s", body)
	}

	t.Run("empty state when nothing is visible", func(t *testing.T) {
		r := newTestRenderer(nil)
		body := string(r.Render("/workshops"))
		// html.EscapeString because html/template escapes the apostrophe in
		// "we'll" to "&#39;" in text context -- the rendered body carries the
		// escaped form, not the Go constant's literal bytes.
		if !strings.Contains(body, html.EscapeString(workshopsUpcomingEmpty)) {
			t.Errorf("missing upcoming empty state: %s", body)
		}
		if !strings.Contains(body, html.EscapeString(workshopsPastEmpty)) {
			t.Errorf("missing past empty state: %s", body)
		}
	})
}

// TestRender_ArchiveIndexListsOnlyPublished proves the /archive index shows
// only published entries, links to them, and shows the empty state when
// none exist.
func TestRender_ArchiveIndexListsOnlyPublished(t *testing.T) {
	source := fakeArchiveSource{
		"published-1": {Slug: "published-1", Subject: "Published Subject", Published: true, UpdatedAt: "2026-08-01", Preheader: "A preheader line."},
		"pending-1":   {Slug: "pending-1", Subject: "Pending Subject", Published: false},
		"withheld-1":  {Slug: "withheld-1", Subject: "Withheld Subject", Published: false},
	}
	r := newTestRendererWithArchive(source)
	body := string(r.Render("/archive"))

	if !strings.Contains(body, "Published Subject") {
		t.Errorf("published entry missing: %s", body)
	}
	if !strings.Contains(body, "A preheader line.") {
		t.Errorf("preheader missing: %s", body)
	}
	if !strings.Contains(body, `<a href="/archive/published-1">`) {
		t.Errorf("missing link to published entry: %s", body)
	}
	if strings.Contains(body, "Pending Subject") || strings.Contains(body, "Withheld Subject") {
		t.Errorf("non-published entry leaked into archive index: %s", body)
	}

	t.Run("empty state when nothing is published", func(t *testing.T) {
		r := newTestRendererWithArchive(fakeArchiveSource{})
		body := string(r.Render("/archive"))
		if !strings.Contains(body, html.EscapeString(archiveEmpty)) {
			t.Errorf("missing empty state: %s", body)
		}
	})
}

// TestRender_ArchiveDetailBodyIsTheAPIRenderersOutput is acceptance
// criterion 2: the archive detail page's body is byte-for-byte
// mailing.RenderMarkdownPageHTML's own output over the campaign's BodyMD --
// the same call PublicArchiveHandler.GetBySlug makes (#0519's remedy).
func TestRender_ArchiveDetailBodyIsTheAPIRenderersOutput(t *testing.T) {
	md := "Thanks for coming! Next month: **PCB design**."
	source := fakeArchiveSource{
		"sept-recap": {Slug: "sept-recap", Subject: "September Recap", Published: true, UpdatedAt: "2026-09-01", BodyMD: md},
	}
	r := newTestRendererWithArchive(source)
	body := string(r.Render("/archive/sept-recap"))

	want, err := mailing.RenderMarkdownPageHTML(md)
	if err != nil {
		t.Fatalf("RenderMarkdownPageHTML: %v", err)
	}
	if !strings.Contains(body, want) {
		t.Errorf("archive detail body does not contain the exact RenderMarkdownPageHTML output %q: %s", want, body)
	}
	if !strings.Contains(body, `<a href="/archive">`) {
		t.Errorf("missing 'see the archive' link: %s", body)
	}
}

// TestRender_ArchiveDetailBodyHeadingDoesNotBecomeSecondH1 is #0519's
// review-bounced defect 1: the real September newsletter's body starts with
// a Markdown "# Heading", which rendered as a second <h1> beneath the
// archive page's own subject <h1>. Both the raw HTML and the API's
// body_html (via mailing.RenderMarkdownPageHTML directly) must show exactly
// one <h1> total and zero inside the body.
func TestRender_ArchiveDetailBodyHeadingDoesNotBecomeSecondH1(t *testing.T) {
	md := "# Solder Night Recap\n\nGreat turnout this month."
	source := fakeArchiveSource{
		"sept-recap": {Slug: "sept-recap", Subject: "September Recap", Published: true, UpdatedAt: "2026-09-01", BodyMD: md},
	}
	r := newTestRendererWithArchive(source)
	body := string(r.Render("/archive/sept-recap"))

	if got := strings.Count(body, "<h1"); got != 1 {
		t.Fatalf("want exactly one <h1> on the page, got %d: %s", got, body)
	}
	if !strings.Contains(body, "<h2>Solder Night Recap</h2>") {
		t.Errorf("expected the body heading demoted to <h2>, got: %s", body)
	}

	bodyHTML, err := mailing.RenderMarkdownPageHTML(md)
	if err != nil {
		t.Fatalf("RenderMarkdownPageHTML: %v", err)
	}
	if strings.Contains(bodyHTML, "<h1") {
		t.Errorf("the API's body_html (the same call) must contain no <h1>: %s", bodyHTML)
	}
}

// TestRender_ArchiveDateIsLosAngelesNotUTC is #0519's review-bounced defect
// 2: an archived_at just after midnight UTC is the previous evening in
// America/Los_Angeles, and both the archive list and the detail page must
// show that Pacific calendar date, matching web/src/lib/archive.ts's
// formatArchivedDate (which formats in the viewer's own zone) rather than
// the UTC date the fallback served before this fix.
func TestRender_ArchiveDateIsLosAngelesNotUTC(t *testing.T) {
	source := fakeArchiveSource{
		"sept-recap": {
			Slug: "sept-recap", Subject: "September Recap", Published: true,
			UpdatedAt:  "2026-09-13", // the stale UTC-truncated date this fix stops using for display
			ArchivedAt: "2026-09-13T01:18:11Z",
		},
	}
	r := newTestRendererWithArchive(source)

	list := string(r.Render("/archive"))
	if !strings.Contains(list, "Sep 12, 2026") {
		t.Errorf("archive list: want Sep 12, 2026 (Pacific), got: %s", list)
	}
	if strings.Contains(list, "Sep 13, 2026") {
		t.Errorf("archive list: UTC date Sep 13, 2026 leaked through: %s", list)
	}

	detail := string(r.Render("/archive/sept-recap"))
	if !strings.Contains(detail, "Sep 12, 2026") {
		t.Errorf("archive detail: want Sep 12, 2026 (Pacific), got: %s", detail)
	}
	if strings.Contains(detail, "Sep 13, 2026") {
		t.Errorf("archive detail: UTC date Sep 13, 2026 leaked through: %s", detail)
	}
}

// TestRender_WithheldArchiveEntryDropsBodyWithoutInvalidate proves
// acceptance criterion 7's second half: resolve runs on every request, so a
// withheld entry's subject/body vanish immediately -- no Invalidate call
// needed -- because the request moves to the shared fallback bucket rather
// than the entry's own now-stale cache key.
func TestRender_WithheldArchiveEntryDropsBodyWithoutInvalidate(t *testing.T) {
	source := fakeArchiveSource{
		"oct-update": {Slug: "oct-update", Subject: "October Update", Published: true, UpdatedAt: "2026-10-01", BodyMD: "Body text."},
	}
	r := newTestRendererWithArchive(source)
	clock := &fakeClock{t: time.Unix(0, 0)}
	r.now = clock.now

	first := string(r.Render("/archive/oct-update"))
	if !strings.Contains(first, "October Update") {
		t.Fatalf("first render missing subject: %s", first)
	}

	entry := source["oct-update"]
	entry.Published = false
	source["oct-update"] = entry
	clock.advance(1 * time.Second) // still well within the TTL, no Invalidate

	second := string(r.Render("/archive/oct-update"))
	if strings.Contains(second, "October Update") || strings.Contains(second, "Body text") {
		t.Errorf("withheld entry's subject/body should be gone immediately: %s", second)
	}
}

// TestRender_ArchiveIndexDropsWithheldEntryOnInvalidate is acceptance
// criterion 7's first half: unlike the detail page above, the /archive
// INDEX is cached under its one static key, so a mutation needs an explicit
// Invalidate to be reflected before the TTL elapses.
func TestRender_ArchiveIndexDropsWithheldEntryOnInvalidate(t *testing.T) {
	source := fakeArchiveSource{
		"nov-update": {Slug: "nov-update", Subject: "November Update", Published: true, UpdatedAt: "2026-11-01"},
	}
	r := newTestRendererWithArchive(source)
	clock := &fakeClock{t: time.Unix(0, 0)}
	r.now = clock.now

	first := string(r.Render("/archive"))
	if !strings.Contains(first, "November Update") {
		t.Fatalf("first render missing entry: %s", first)
	}

	entry := source["nov-update"]
	entry.Published = false
	source["nov-update"] = entry
	clock.advance(1 * time.Second) // within the TTL

	stillCached := string(r.Render("/archive"))
	if !strings.Contains(stillCached, "November Update") {
		t.Errorf("expected the /archive list to stay cached within the TTL, got: %s", stillCached)
	}

	r.Invalidate()
	after := string(r.Render("/archive"))
	if strings.Contains(after, "November Update") {
		t.Errorf("expected Invalidate to drop the withheld entry immediately, got: %s", after)
	}
}

// TestRender_FallbackEscapesAdminInput is acceptance criterion 6's escaping
// half: admin-authored title/summary/subject/preheader/location text is
// HTML-escaped, and a raw <script> line inside a Markdown body never
// reaches the page unescaped either (goldmark's own safe mode, exercised
// through the same mailing.RenderMarkdownHTML call as the API).
func TestRender_FallbackEscapesAdminInput(t *testing.T) {
	malicious := `Solder & Circuits <script>alert(1)</script> "quoted"`
	workshopSource := fakeWorkshopSource{
		"xss-workshop": {
			Slug: "xss-workshop", Title: malicious, Summary: malicious,
			Status: WorkshopPublished, Published: true, LocationName: malicious,
		},
	}
	maliciousImg := `<img src=x onerror=alert(1)>`
	archiveSource := fakeArchiveSource{
		"xss-archive": {
			Slug: "xss-archive", Subject: maliciousImg, Preheader: maliciousImg, Published: true,
			BodyMD: "Normal text.\n\n<script>alert(1)</script>\n\nMore text.",
		},
	}
	r := NewRenderer([]byte(testTemplate), testBaseURL, workshopSource, archiveSource)

	for _, path := range []string{"/workshops/xss-workshop", "/archive/xss-archive"} {
		body := string(r.Render(path))
		if strings.Contains(body, "<script>alert(1)") {
			t.Errorf("path %q: unescaped <script> reached the page: %s", path, body)
		}
		if strings.Contains(body, "<img src=x") {
			t.Errorf("path %q: unescaped <img> reached the page: %s", path, body)
		}
	}

	workshopBody := string(r.Render("/workshops/xss-workshop"))
	if !strings.Contains(workshopBody, "&lt;script&gt;") {
		t.Errorf("expected the workshop title/summary to be HTML-escaped: %s", workshopBody)
	}
	if !strings.Contains(workshopBody, "&amp;") {
		t.Errorf("expected the literal & to be HTML-escaped: %s", workshopBody)
	}
}

// TestRender_FallbackAddsNoScriptElement is acceptance criterion 6's other
// half: the fallback content itself never adds a <script> element. The only
// <script> that can appear is #0055's JSON-LD block, which is unrelated to
// this issue -- so every route's <script> count must equal its (unchanged)
// JSON-LD count: 0, or 1 for a workshop with enough data to qualify.
func TestRender_FallbackAddsNoScriptElement(t *testing.T) {
	workshopSource := fakeWorkshopSource{
		"json-ld-workshop": fullWorkshop("json-ld-workshop", WorkshopPublished),
		"plain-workshop":   {Slug: "plain-workshop", Title: "Plain Workshop", Status: WorkshopPublished, Published: true},
	}
	archiveSource := fakeArchiveSource{
		"an-entry": {Slug: "an-entry", Subject: "An Entry", Published: true, BodyMD: "Just text."},
	}
	r := NewRenderer([]byte(testTemplate), testBaseURL, workshopSource, archiveSource)

	cases := []struct {
		path      string
		wantCount int
	}{
		{"/", 0}, {"/about", 0}, {"/privacy", 0}, {"/subscribe", 0},
		{"/workshops", 0}, {"/archive", 0},
		{"/workshops/plain-workshop", 0},
		{"/workshops/json-ld-workshop", 1},
		{"/archive/an-entry", 0},
	}
	for _, c := range cases {
		body := string(r.Render(c.path))
		if got := strings.Count(body, "<script"); got != c.wantCount {
			t.Errorf("path %q: <script count = %d, want %d: %s", c.path, got, c.wantCount, body)
		}
	}
}

// erroringWorkshopSource errors on its first Workshops() call, then
// delegates to inner -- used to prove a transient list-source failure still
// renders the <h1> and doesn't get cached (TestRender_ListSourceError
// RendersHeadingAndSkipsCache below).
type erroringWorkshopSource struct {
	inner WorkshopSource
	err   error
	calls int
}

func (e *erroringWorkshopSource) WorkshopBySlug(slug string) (Workshop, bool, error) {
	return e.inner.WorkshopBySlug(slug)
}

func (e *erroringWorkshopSource) Workshops() ([]Workshop, error) {
	e.calls++
	if e.calls == 1 {
		return nil, e.err
	}
	return e.inner.Workshops()
}

// TestRender_ListSourceErrorRendersHeadingAndSkipsCache is #0519's plan §4:
// a source error still renders the <h1> (never a blank page), and the
// degraded render is never cached, so the very next request -- still well
// within the TTL, with no Invalidate call -- sees the recovered list.
func TestRender_ListSourceErrorRendersHeadingAndSkipsCache(t *testing.T) {
	inner := fakeWorkshopSource{
		"soon": {Slug: "soon", Title: "Soon Workshop", Status: WorkshopPublished, Published: true, StartsAt: "2026-09-20T18:00:00Z"},
	}
	source := &erroringWorkshopSource{inner: inner, err: errors.New("store unavailable")}
	r := newTestRenderer(source)
	clock := &fakeClock{t: time.Unix(0, 0)}
	r.now = clock.now

	first := string(r.Render("/workshops"))
	if got := strings.Count(first, "<h1"); got != 1 {
		t.Fatalf("expected exactly one <h1> even on a source error, got %d: %s", got, first)
	}
	if strings.Contains(first, "Soon Workshop") {
		t.Errorf("expected no list items on a source error: %s", first)
	}

	clock.advance(1 * time.Second) // still well within the TTL, no Invalidate
	second := string(r.Render("/workshops"))
	if !strings.Contains(second, "Soon Workshop") {
		t.Errorf("expected the recovered source's list on the next render (the error render must not have been cached): %s", second)
	}
}
