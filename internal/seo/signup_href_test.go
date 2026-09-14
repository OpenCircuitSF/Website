// Tests for #0524: a published workshop's raw HTML includes its external
// sign-up link when one is set, gated through the SAME validation the API
// applies (#0152's IsSafeLinkHref, now shared with internal/handlers) --
// an invalid or protocol-relative URL is never rendered.
package seo

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestRender_WorkshopDetailServesSignupLink is criterion 1 and 3: a
// published workshop with a sign-up URL gets it in the raw HTML, rendered
// with the same rel attributes the SPA's own external links carry, under
// the workshop's body (the SPA renders its link there too).
func TestRender_WorkshopDetailServesSignupLink(t *testing.T) {
	w := fullWorkshop("signup-101", WorkshopPublished)
	w.SignUpURL = "https://www.eventbrite.com/e/solder-101-tickets"
	source := fakeWorkshopSource{"signup-101": w}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/signup-101"))

	want := `<a href="https://www.eventbrite.com/e/solder-101-tickets" target="_blank" rel="noopener noreferrer">Register for this workshop</a>`
	if !strings.Contains(body, want) {
		t.Errorf("expected the validated sign-up anchor %q in body: %s", want, body)
	}
	// Criterion 3: same rel attributes as the SPA's external links -- the
	// pin above already carries them; this asserts they are the ONLY
	// anchor form the sign-up link takes.
	if strings.Count(body, "eventbrite") != 1 {
		t.Errorf("sign-up link rendered more than once: %s", body)
	}
}

// TestRender_WorkshopDetailSignupUnsetOmitsLink: no signup_url, no link.
func TestRender_WorkshopDetailSignupUnsetOmitsLink(t *testing.T) {
	w := fullWorkshop("no-signup", WorkshopPublished)
	source := fakeWorkshopSource{"no-signup": w}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/no-signup"))

	if strings.Contains(body, "Register for this workshop") {
		t.Errorf("unset signup_url must render no registration link: %s", body)
	}
}

// TestRender_WorkshopDetailSignupTrimmed mirrors
// hasExternalSignup's trim: a stored value with surrounding whitespace
// still renders, as its trimmed href.
func TestRender_WorkshopDetailSignupTrimmed(t *testing.T) {
	w := fullWorkshop("ws-signup", WorkshopPublished)
	w.SignUpURL = "  https://example.com/tickets  "
	source := fakeWorkshopSource{"ws-signup": w}
	r := newTestRenderer(source)
	body := string(r.Render("/workshops/ws-signup"))

	if !strings.Contains(body, `<a href="https://example.com/tickets" target="_blank" rel="noopener noreferrer">`) {
		t.Errorf("expected the trimmed href to render: %s", body)
	}
}

// TestRender_WorkshopDetailSignupSweepsURLValidatorFixture is criterion 2's
// strongest form: the SAME committed parity fixture that pins the Go and
// TypeScript validators against each other (#0152, #0157) also pins what
// the server fallback renders. Every link_href case is rendered as a
// workshop detail page; the sign-up link appears exactly when the validator
// accepts the URL and never when it refuses it.
func TestRender_WorkshopDetailSignupSweepsURLValidatorFixture(t *testing.T) {
	raw, err := os.ReadFile(repoPath("testdata/url_validators.json"))
	if err != nil {
		t.Fatalf("reading url_validators.json: %v", err)
	}
	var cases []struct {
		ID   string `json:"id"`
		Rule string `json:"rule"`
		Href string `json:"value"`
		Want bool   `json:"want"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parsing url_validators.json: %v", err)
	}

	count := 0
	for _, c := range cases {
		if c.Rule != "link_href" {
			continue
		}
		count++
		w := fullWorkshop("fixture-"+c.ID, WorkshopPublished)
		w.SignUpURL = c.Href
		source := fakeWorkshopSource{"fixture": w}
		r := newTestRenderer(source)
		body := string(r.Render("/workshops/fixture"))

		// html/template URL-escapes the href (non-ASCII paths become
		// percent-encoded), so rendered-ness is judged by the anchor's own
		// label, not by a raw substring of the URL. For an unsafe URL the
		// gate renders no anchor at all, so the label is the discriminator.
		rendered := strings.Contains(body, `>Register for this workshop</a>`)
		if c.Want != rendered {
			t.Errorf("case %s: url %q validator=%v rendered=%v -- the fallback's render gate disagrees with IsSafeLinkHref", c.ID, c.Href, c.Want, rendered)
		}
	}
	if count == 0 {
		t.Fatal("no link_href cases found in testdata/url_validators.json -- the fixture's shape changed and this sweep proves nothing")
	}
}

// TestSignupLabelAndRelMatchTheSPA pins criterion 3's other half: the
// label and the rel attributes the fallback renders are WorkshopDetail
// svelte's own, read straight from the view source -- the same
// normalization convention the fallback_copy guards use.
func TestSignupLabelAndRelMatchTheSPA(t *testing.T) {
	src, err := os.ReadFile(repoPath("web/src/views/WorkshopDetail.svelte"))
	if err != nil {
		t.Fatalf("reading WorkshopDetail.svelte: %v", err)
	}
	for _, want := range []string{`rel="noopener noreferrer"`, `target="_blank"`, "Register for this workshop"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("WorkshopDetail.svelte no longer contains %q -- the fallback's sign-up anchor has drifted from the SPA's own", want)
		}
	}
}
