package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

func seedPreviewWorkshop(t *testing.T, pool *pgxpool.Pool, bodyMD *string) workshops.Workshop {
	t.Helper()
	store := workshops.NewStore(pool)
	wk, err := store.Create(context.Background(), workshops.CreateInput{
		Title:  uniqueAdminWorkshopTitle(t),
		BodyMD: bodyMD,
	})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	cleanupAdminWorkshop(t, pool, wk.ID)
	return wk
}

func strPtr(s string) *string { return &s }

// ── Preview renders the stored row ──────────────────────────────────────────

func TestAdminWorkshopPreview_RendersStoredRow(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshop-preview-render@example.com")
	seedSession(t, pool, admin, "admin-token-workshop-preview-render")

	wk := seedPreviewWorkshop(t, pool, strPtr("Hello **world**, visit https://opencircuitsf.com."))

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/workshops/%d/preview", srv.URL, wk.ID),
		"admin-token-workshop-preview-render", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out workshopPreviewResponse
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(out.HTML, "<strong>world</strong>") {
		t.Errorf("HTML = %q, want it to contain the rendered body (goldmark, not the deleted client renderer)", out.HTML)
	}
}

// TestAdminWorkshopPreview_EmphasisInsideLinkText is this issue's own named
// acceptance criterion: `[**bold**](/p)` used to emit literal asterisks
// inside the anchor under web/src/lib/markdown.ts's single left-to-right
// scan (that scan existed specifically to stop emphasis regexes from
// rewriting an already-built href attribute -- #0052's finding 1). A real
// Markdown parser has no such conflict: goldmark parses the link's text as
// its own inline content, emphasis included, independently of how the
// destination is escaped.
func TestAdminWorkshopPreview_EmphasisInsideLinkText(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshop-preview-emphasis@example.com")
	seedSession(t, pool, admin, "admin-token-workshop-preview-emphasis")

	wk := seedPreviewWorkshop(t, pool, strPtr("[**bold**](/p)"))

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/workshops/%d/preview", srv.URL, wk.ID),
		"admin-token-workshop-preview-emphasis", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out workshopPreviewResponse
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := `<p><a href="/p"><strong>bold</strong></a></p>`
	if strings.TrimSpace(out.HTML) != want {
		t.Errorf("HTML = %q, want %q (no literal asterisks inside the anchor)", out.HTML, want)
	}
}

func TestAdminWorkshopPreview_EmptyBodyRendersEmptyHTML(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshop-preview-empty@example.com")
	seedSession(t, pool, admin, "admin-token-workshop-preview-empty")

	wk := seedPreviewWorkshop(t, pool, nil)

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/workshops/%d/preview", srv.URL, wk.ID),
		"admin-token-workshop-preview-empty", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out workshopPreviewResponse
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.HTML != "" {
		t.Errorf("HTML = %q, want empty for a workshop with no body_md", out.HTML)
	}
}

func TestAdminWorkshopPreview_WorkshopNotFound(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshop-preview-404@example.com")
	seedSession(t, pool, admin, "admin-token-workshop-preview-404")

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/workshops/%d/preview", srv.URL, int64(999999999)),
		"admin-token-workshop-preview-404", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}

// TestAdminWorkshopPreview_IgnoresRequestBody mirrors
// TestAdminCampaignPreview_Preview_IgnoresRequestBody (admin_campaign_preview_test.go):
// Preview never calls decodeJSON, so a request body claiming a different
// body_md has zero effect -- there is no override code path to forget to
// validate, because there is no override code path.
func TestAdminWorkshopPreview_IgnoresRequestBody(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshop-preview-nooverride@example.com")
	seedSession(t, pool, admin, "admin-token-workshop-preview-nooverride")

	wk := seedPreviewWorkshop(t, pool, strPtr("Stored body content"))

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/workshops/%d/preview", srv.URL, wk.ID),
		"admin-token-workshop-preview-nooverride",
		`{"body_md":"<script>alert(1)</script> ATTACKER BODY"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out workshopPreviewResponse
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if strings.Contains(out.HTML, "ATTACKER") {
		t.Errorf("HTML reflects the request body's override, want only the stored row: %s", out.HTML)
	}
	if !strings.Contains(out.HTML, "Stored body content") {
		t.Errorf("HTML = %q, want the stored body_md rendered", out.HTML)
	}
}

// ── Safety: the payloads #0052's reviews established ────────────────────────
//
// hrefStartsWithDangerousScheme is the precise safety property: whether an
// emitted `<a href="...">` would actually RESOLVE to a javascript:/data:
// navigation. This is deliberately narrower than "does the HTML contain the
// substring javascript: anywhere" -- see
// TestRenderWorkshopBodyHTML_LeadingControlCharPercentEncodedNotStripped's
// doc comment for why a percent-encoded control-character prefix
// (`href="%01javascript:..."`) contains that substring textually while
// never being interpreted as the javascript: scheme by a URL parser. A
// substring check that doesn't distinguish the two would fail on safe
// output.
func hrefStartsWithDangerousScheme(html string) bool {
	lower := strings.ToLower(html)
	for _, needle := range []string{`href="javascript:`, `href='javascript:`, `href="vbscript:`, `href='vbscript:`, `href="data:`, `href='data:`} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// TestRenderWorkshopBodyHTML_SafetyPayloads calls renderWorkshopBodyHTML
// directly -- no database, no HTTP -- so it can include payloads that could
// never survive a round trip through Postgres (a literal NUL byte in
// body_md; see the "leading NUL byte" case's own comment). This is the
// exhaustive version of the payload table #0052's phase-3 review built
// (issues/0052.md's "## Review notes"): five control-character disguises of
// `javascript:` that were confirmed executable against the WHATWG URL
// parser after passing web/src/lib/markdown.ts's ORIGINAL scheme check,
// plus a plain `javascript:`/`data:` scheme and raw `<script>`/`<img
// onerror>` HTML. Every one is re-run here against the SAME goldmark
// pipeline this preview endpoint (and the published page) now use.
//
// The two control-character cases are not simply "passed": their actual
// output was inspected before writing the assertion below (a temporary
// probe test against internal/mailing.RenderMarkdownHTML, run and then
// deleted -- see this issue's `## Verification` for the exact commands).
// goldmark's util.URLEscape PERCENT-ENCODES a raw control byte in a link
// destination (`\x01` -> `%01`, `\x00` -> `%00` -- three literal ASCII
// characters, none of them a C0 control) rather than leaving the raw
// control byte in the href attribute the way markdown.ts's original bug
// did. Verified independently against Node's WHATWG URL implementation:
//
//	new URL("%01javascript:alert(1)", "https://opencircuitsf.com/workshops/x")
//	  -> protocol: "https:", href: ".../workshops/%01javascript:alert(1)"
//	new URL("%00javascript:alert(1)", "https://opencircuitsf.com/workshops/x")
//	  -> protocol: "https:", href: ".../workshops/%00javascript:alert(1)"
//
// A percent-encoded "%01"/"%00" cannot start a URL scheme (RFC 3986/WHATWG
// both require a scheme to start with an ASCII letter, and "%" is not
// one), so the browser treats the whole string as a same-origin relative
// reference -- it never becomes `javascript:` the way the RAW control byte
// did in #0052's bug. The emitted HTML DOES contain the literal substring
// "javascript:" (as inert path text after the percent-encoded prefix),
// which is exactly why this test checks hrefStartsWithDangerousScheme
// rather than a bare substring search. The tab/CR-spliced forms fare even
// better: CommonMark's own link-destination grammar treats an unescaped
// ASCII whitespace/control character inside `(...)` as invalid link
// syntax, so those inputs don't parse as a link AT ALL -- they fall back to
// literal paragraph text with no `<a>` tag.
func TestRenderWorkshopBodyHTML_SafetyPayloads(t *testing.T) {
	cases := []struct {
		name string
		md   string
	}{
		{"raw script tag", "Hello <script>alert(document.cookie)</script> world."},
		{"img onerror", `Hello <img src=x onerror="alert(1)"> world.`},
		{"plain javascript: scheme", "[click](javascript:alert(document.cookie))"},
		{"plain data: scheme", "[x](data:text/html,evil)"},
		{"tab spliced into scheme", "[x](java\tscript:alert(1))"},
		{"CR spliced into scheme", "[x](java\rscript:alert(1))"},
		{"tab between scheme and colon", "[x](javascript\t:alert(1))"},
		{"leading U+0001 control char", "[x](\x01javascript:alert(1))"},
		// A literal NUL byte can never be stored in a Postgres TEXT column
		// (INSERT fails with SQLSTATE 22021, "invalid byte sequence for
		// encoding UTF8: 0x00") -- confirmed empirically: seeding a
		// workshop with this exact body_md via the real store fails before
		// any handler runs. So this bypass vector is structurally
		// unreachable for a stored workshop body regardless of what
		// goldmark does with it; tested here anyway (calling the renderer
		// directly, bypassing the store) because the DELETED client
		// renderer's own bug was about the STRING, not the database, and
		// this is the strongest available proof the string itself is inert.
		{"leading NUL byte", "[x](\x00javascript:alert(1))"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			html, err := renderWorkshopBodyHTML(&c.md)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if strings.Contains(html, "<script>") || strings.Contains(html, "onerror=") {
				t.Errorf("input %q: raw script/onerror markup passed through: %s", c.md, html)
			}
			if hrefStartsWithDangerousScheme(html) {
				t.Errorf("input %q: an href attribute resolves to a dangerous scheme: %s", c.md, html)
			}
		})
	}
}

// TestAdminWorkshopPreview_SafetyPayloads is the end-to-end counterpart:
// the same payloads (minus the NUL byte, which cannot be stored — see
// TestRenderWorkshopBodyHTML_SafetyPayloads's doc comment), routed through
// a real seeded workshop row and a real HTTP request against the admin
// preview endpoint, proving the wiring (store -> handler -> JSON response)
// doesn't reintroduce anything the pure-function test already ruled out.
func TestAdminWorkshopPreview_SafetyPayloads(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshop-preview-safety@example.com")
	seedSession(t, pool, admin, "admin-token-workshop-preview-safety")

	cases := []struct {
		name string
		md   string
	}{
		{"raw script tag", "Hello <script>alert(document.cookie)</script> world."},
		{"img onerror", `Hello <img src=x onerror="alert(1)"> world.`},
		{"plain javascript: scheme", "[click](javascript:alert(document.cookie))"},
		{"plain data: scheme", "[x](data:text/html,evil)"},
		{"tab spliced into scheme", "[x](java\tscript:alert(1))"},
		{"CR spliced into scheme", "[x](java\rscript:alert(1))"},
		{"tab between scheme and colon", "[x](javascript\t:alert(1))"},
		{"leading U+0001 control char", "[x](\x01javascript:alert(1))"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wk := seedPreviewWorkshop(t, pool, &c.md)
			resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/workshops/%d/preview", srv.URL, wk.ID),
				"admin-token-workshop-preview-safety", "")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
			}
			var out workshopPreviewResponse
			if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if strings.Contains(out.HTML, "<script>") || strings.Contains(out.HTML, "onerror=") {
				t.Errorf("input %q: raw script/onerror markup passed through: %s", c.md, out.HTML)
			}
			if hrefStartsWithDangerousScheme(out.HTML) {
				t.Errorf("input %q: an href attribute resolves to a dangerous scheme: %s", c.md, out.HTML)
			}
		})
	}
}

// TestAdminWorkshopPreview_ProtocolRelativeLinkInheritsCampaignBehavior
// documents (rather than asserts a fix for) a real, if lower-severity than
// XSS, gap: goldmark's safe mode (html.IsDangerousURL) only refuses a link
// whose DESTINATION SCHEME is dangerous (javascript:, vbscript:, file:,
// most data:); it does not refuse a protocol-relative destination
// ("//evil.host/x"), which resolves to a live, off-site `<a href>`. This is
// NOT a regression #0136 introduces: internal/mailing.RenderMarkdownHTML is
// the exact same function email_campaigns.body_md already renders through
// (#0042/#0043, shipped and reviewed), and it has always had this property
// there too -- reusing it for workshops inherits campaigns' existing
// posture unchanged, it does not weaken it further.
//
// It IS a narrowing of what the DELETED client-side renderer did for
// workshop BODY content specifically: web/src/lib/markdown.ts's
// renderMarkdownPreview called isSafeLinkHref on every link (including body
// links), and isSafeLinkHref has rejected protocol-relative destinations
// since #0138. The Go isSafeLinkHref twin (admin_workshops.go, #0152) still
// gates signup_url and cover_image at the API boundary exactly as before --
// unaffected by this change -- but nothing gates a protocol-relative link
// typed directly into a workshop's Markdown BODY, same as nothing gates one
// in a campaign body today. Left for the reviewer to decide whether that
// gap is worth a follow-up issue extending goldmark's link policy (out of
// this issue's scope: #0138/#0152 own URL-rule semantics, and any change
// here would also affect campaigns, an unrelated subsystem).
func TestAdminWorkshopPreview_ProtocolRelativeLinkInheritsCampaignBehavior(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-workshop-preview-protorel@example.com")
	seedSession(t, pool, admin, "admin-token-workshop-preview-protorel")

	wk := seedPreviewWorkshop(t, pool, strPtr("[x](//evil.host/y)"))

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/workshops/%d/preview", srv.URL, wk.ID),
		"admin-token-workshop-preview-protorel", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out workshopPreviewResponse
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Documents the current (inherited-from-campaigns) behavior rather than
	// a desired one: a protocol-relative link DOES survive goldmark's safe
	// mode as a live href. If this ever starts failing, campaigns' own
	// goldmark configuration changed underneath this test -- re-evaluate
	// whether it should, don't just widen the assertion.
	if !strings.Contains(out.HTML, `href="//evil.host/y"`) {
		t.Fatalf("expected goldmark's known behavior (protocol-relative link passes through unmodified) to still hold; got %s -- if this changed, campaigns' link policy changed too and deserves its own review", out.HTML)
	}
}

// ── Admin gating ─────────────────────────────────────────────────────────────

func TestAdminWorkshopPreview_Unauthenticated(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	wk := seedPreviewWorkshop(t, pool, strPtr("hi"))

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/workshops/%d/preview", srv.URL, wk.ID), "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (no session)", resp.StatusCode)
	}
}

func TestAdminWorkshopPreview_NonAdminForbidden(t *testing.T) {
	pool := interestsTestPool(t)
	srv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer srv.Close()

	user := seedUser(t, pool, "regular-workshop-preview@example.com")
	seedSession(t, pool, user, "workshop-preview-user-token")

	wk := seedPreviewWorkshop(t, pool, strPtr("hi"))

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/workshops/%d/preview", srv.URL, wk.ID),
		"workshop-preview-user-token", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (non-admin session)", resp.StatusCode)
	}
}

// ── Preview/publish parity (this issue's own binding acceptance criterion) ──

// TestAdminWorkshopPreview_MatchesPublishedRender is the "demonstrate it,
// don't assert it" proof: the SAME stored workshop row is rendered through
// both POST /admin/workshops/{id}/preview (this file) and
// GET /api/workshops/{slug} (public_workshops.go's toPublicView, #0054),
// and the resulting HTML must be byte-identical. Both call the exact same
// renderWorkshopBodyHTML function (admin_workshop_preview.go's package doc
// comment), so this test is pinning that fact end-to-end over real HTTP
// requests against real handlers, not just reading the source and trusting
// it.
func TestAdminWorkshopPreview_MatchesPublishedRender(t *testing.T) {
	pool := interestsTestPool(t)
	adminSrv := httptest.NewServer(adminWorkshopsMux(pool, nil))
	defer adminSrv.Close()
	publicSrv := httptest.NewServer(publicWorkshopsMux(pool))
	defer publicSrv.Close()

	admin := seedAdmin(t, pool, "admin-workshop-preview-parity@example.com")
	seedSession(t, pool, admin, "admin-token-workshop-preview-parity")

	body := "# Soldering 101\n\nLearn to **solder** with us. Read the [**safety guide**](/safety) first.\n\n- Bring safety glasses\n- Bring a multimeter"
	wk := seedPreviewWorkshop(t, pool, &body)

	// Preview, before publishing -- Preview reads GetByID directly, so it
	// works regardless of status.
	previewResp := doJSON(t, adminSrv.Client(), "POST", fmt.Sprintf("%s/admin/workshops/%d/preview", adminSrv.URL, wk.ID),
		"admin-token-workshop-preview-parity", "")
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d, want 200 (body=%s)", previewResp.StatusCode, readBody(t, previewResp))
	}
	var preview workshopPreviewResponse
	if err := json.Unmarshal(readBody(t, previewResp), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.HTML == "" {
		t.Fatal("preview.HTML is empty, want the rendered body")
	}

	// Publish, then read back the public detail route.
	patchResp := doJSON(t, adminSrv.Client(), "PATCH", fmt.Sprintf("%s/admin/workshops/%d", adminSrv.URL, wk.ID),
		"admin-token-workshop-preview-parity", `{"status":"published"}`)
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("publish status = %d, want 200 (body=%s)", patchResp.StatusCode, readBody(t, patchResp))
	}

	publicResp, err := http.Get(publicSrv.URL + "/api/workshops/" + wk.Slug)
	if err != nil {
		t.Fatalf("GET /api/workshops/%s: %v", wk.Slug, err)
	}
	var published struct {
		BodyHTML *string `json:"body_html"`
	}
	if err := json.Unmarshal(readBody(t, publicResp), &published); err != nil {
		t.Fatalf("decode published workshop: %v", err)
	}
	if published.BodyHTML == nil {
		t.Fatal("published workshop's body_html is absent, want the rendered body")
	}

	if preview.HTML != *published.BodyHTML {
		t.Fatalf("preview and published HTML differ (should be byte-identical, same function):\npreview:   %q\npublished: %q",
			preview.HTML, *published.BodyHTML)
	}
	// And a positive check that this is genuinely the rendered content, not
	// two empty strings trivially matching each other.
	if !strings.Contains(preview.HTML, "<h1>Soldering 101</h1>") ||
		!strings.Contains(preview.HTML, `<a href="/safety"><strong>safety guide</strong></a>`) {
		t.Fatalf("preview.HTML does not look like the expected rendered body: %s", preview.HTML)
	}
}
