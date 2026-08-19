package mailing

import (
	"os"
	"strings"
	"testing"
	"time"
)

// updateGolden regenerates testdata/*.golden from the current renderer when
// UPDATE_GOLDEN=1 is set in the environment. Run once by hand after a
// deliberate template change, review the diff, then run the suite again
// without the env var so the assertion is real. This mirrors the project's
// only other golden-style fixtures (none yet) and Go's own
// testing/internal/testdata convention.
func updateGolden() bool { return os.Getenv("UPDATE_GOLDEN") == "1" }

func goldenFile(t *testing.T, name, got string) {
	t.Helper()
	path := "testdata/" + name
	if updateGolden() {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden file %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden file %s: %v (run with UPDATE_GOLDEN=1 to create it)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s does not match golden output.\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

// Fixed inputs shared by every golden test in this file. Using literal,
// obviously-fake tokens (rather than real crypto/rand output) keeps the
// golden files stable and the diff readable.
const (
	testBaseURL  = "https://www.opencircuitsf.com"
	testTo       = "alice@example.com"
	testConfirm  = "CONFIRM-TOKEN-AAA"
	testManage   = "MANAGE-TOKEN-BBB"
	testRegToken = "REGISTER-TOKEN-CCC"
	testRecToken = "RECOVER-TOKEN-DDD"
	testAddress  = "Open Circuit SF, PO Box 1234, San Francisco, CA 94110"
)

// testRevokedAt is the fixed timestamp used by every SendSessionsRevoked
// golden/property test, so the rendered output (and the golden fixture) is
// stable across runs rather than depending on wall-clock time.
var testRevokedAt = time.Date(2026, 8, 6, 15, 4, 5, 0, time.UTC)

func TestBuildConfirmationEmail_Golden(t *testing.T) {
	msg := BuildConfirmationEmail(testTo, testBaseURL, testConfirm, testManage, 7*24*time.Hour, testAddress)
	goldenFile(t, "confirmation.html", msg.HTMLBody)
	goldenFile(t, "confirmation.txt", msg.TextBody)
}

func TestBuildAlreadySubscribedEmail_Golden(t *testing.T) {
	msg := BuildAlreadySubscribedEmail(testTo, testBaseURL, testManage, testAddress)
	goldenFile(t, "already_subscribed.html", msg.HTMLBody)
	goldenFile(t, "already_subscribed.txt", msg.TextBody)
}

func TestBuildRegistrationEmail_Golden(t *testing.T) {
	msg := BuildRegistrationEmail(testTo, testBaseURL, testRegToken, 5*time.Minute)
	goldenFile(t, "registration.html", msg.HTMLBody)
	goldenFile(t, "registration.txt", msg.TextBody)
}

func TestBuildRecoveryEmail_Golden(t *testing.T) {
	msg := BuildRecoveryEmail(testTo, testBaseURL, testRecToken, 15*time.Minute)
	goldenFile(t, "recovery.html", msg.HTMLBody)
	goldenFile(t, "recovery.txt", msg.TextBody)
}

// TestBuildSessionsRevokedEmail_Golden covers the fifth template (#0076):
// SendSessionsRevoked, themed as an HTML+text pair consistent with the other
// three internal/auth mails instead of the plain-text-only shape #0028 left
// it in.
func TestBuildSessionsRevokedEmail_Golden(t *testing.T) {
	msg := BuildSessionsRevokedEmail(testTo, testBaseURL, testRevokedAt)
	goldenFile(t, "sessions_revoked.html", msg.HTMLBody)
	goldenFile(t, "sessions_revoked.txt", msg.TextBody)
}

// --- Property tests: the things that actually break in the wild ---
// (per the brief: "contains the word Confirm" proves nothing.)

// allMessages returns every one of the five non-campaign templates
// (#0028's original four, plus #0076's SendSessionsRevoked) built with
// distinct, per-template tokens, for the property tests below that must hold
// across all of them.
func allMessages() map[string]Message {
	return map[string]Message{
		"confirmation":       BuildConfirmationEmail(testTo, testBaseURL, testConfirm, testManage, 7*24*time.Hour, testAddress),
		"already_subscribed": BuildAlreadySubscribedEmail(testTo, testBaseURL, testManage, testAddress),
		"registration":       BuildRegistrationEmail(testTo, testBaseURL, testRegToken, 5*time.Minute),
		"recovery":           BuildRecoveryEmail(testTo, testBaseURL, testRecToken, 15*time.Minute),
		"sessions_revoked":   BuildSessionsRevokedEmail(testTo, testBaseURL, testRevokedAt),
	}
}

// TestAllTemplates_TextBodyNonEmpty proves every template actually has a
// text alternative, not just an HTML body. A message with HTMLBody set and
// TextBody empty would still "work" against SESMailer (it only rejects
// neither being set — mailer.go:105) but would violate the acceptance
// criterion and PRD §6.6's explicit "not optional" requirement. Mutation
// proof: temporarily drop TextBody from one Build* return and this test
// fails; see the Verification section in issues/0028.md for the observed
// failure text.
func TestAllTemplates_TextBodyNonEmpty(t *testing.T) {
	for name, msg := range allMessages() {
		if strings.TrimSpace(msg.TextBody) == "" {
			t.Errorf("%s: TextBody is empty — no plain-text alternative", name)
		}
		if strings.TrimSpace(msg.HTMLBody) == "" {
			t.Errorf("%s: HTMLBody is empty", name)
		}
	}
}

// TestAllTemplates_NoStyleTag proves the HTML layout does not depend on a
// <style> block: none of these templates should emit one at all, since a
// client that strips <head> content (many do) would silently break any
// layout that relied on it. Inline styles/attributes only.
func TestAllTemplates_NoStyleTag(t *testing.T) {
	for name, msg := range allMessages() {
		if strings.Contains(strings.ToLower(msg.HTMLBody), "<style") {
			t.Errorf("%s: HTML contains a <style> tag; layout must use inline styles only", name)
		}
	}
}

// TestAllTemplates_NoFlexboxOrCSSCustomProperties guards the other two
// letter-of-the-law constraints in the same acceptance criterion: no
// flexbox, no var(--...) custom properties anywhere in the markup.
func TestAllTemplates_NoFlexboxOrCSSCustomProperties(t *testing.T) {
	for name, msg := range allMessages() {
		lower := strings.ToLower(msg.HTMLBody)
		if strings.Contains(lower, "display:flex") || strings.Contains(lower, "display: flex") {
			t.Errorf("%s: HTML uses flexbox", name)
		}
		if strings.Contains(msg.HTMLBody, "var(--") {
			t.Errorf("%s: HTML references a CSS custom property", name)
		}
	}
}

// TestAllTemplates_OuterTableHasExplicitBackgroundColor proves the Outlook
// gotcha this issue calls out by name is actually addressed: the outer
// wrapping table must carry bgcolor as an HTML ATTRIBUTE (not merely a CSS
// background-color declaration, which Outlook's Word engine ignores on
// <body> and can drop elsewhere). Mutation proof: remove the bgcolor="..."
// attribute from the outer <table ...> line in templates.go and this test
// fails — see issues/0028.md ## Verification for the observed failure.
func TestAllTemplates_OuterTableHasExplicitBackgroundColor(t *testing.T) {
	for name, msg := range allMessages() {
		idx := strings.Index(msg.HTMLBody, "<table")
		if idx == -1 {
			t.Fatalf("%s: no <table> element found in HTML body", name)
		}
		// The outer table is the first one emitted; grab up to its closing '>'.
		end := strings.Index(msg.HTMLBody[idx:], ">")
		if end == -1 {
			t.Fatalf("%s: malformed outer <table> tag", name)
		}
		openTag := msg.HTMLBody[idx : idx+end]
		if !strings.Contains(openTag, `bgcolor="`+colorPageBG+`"`) {
			t.Errorf("%s: outer <table> tag missing explicit bgcolor attribute: %q", name, openTag)
		}
	}
}

// TestAllTemplates_LinksAreAbsolute proves every href in the rendered HTML,
// and every URL in the rendered text, is an absolute https:// URL — a
// relative URL is meaningless once the HTML is lifted out of its origin and
// read in an inbox. Mutation proof: change one Build* function to omit
// baseURL from a link (e.g. "/confirm?token=..." instead of
// baseURL+"/confirm?token=...") and this test fails.
func TestAllTemplates_LinksAreAbsolute(t *testing.T) {
	for name, msg := range allMessages() {
		for _, href := range extractHrefs(msg.HTMLBody) {
			if !strings.HasPrefix(href, "https://") && !strings.HasPrefix(href, "http://") {
				t.Errorf("%s: href %q is not absolute", name, href)
			}
		}
		if len(extractHrefs(msg.HTMLBody)) == 0 {
			t.Errorf("%s: no href found in HTML body at all", name)
		}
		for _, url := range extractTextURLs(msg.TextBody) {
			if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
				t.Errorf("%s: text URL %q is not absolute", name, url)
			}
		}
	}
}

// extractHrefs is a minimal href="..." scraper — no need for a full HTML
// parser given these templates hand-write their own markup.
func extractHrefs(htmlBody string) []string {
	var out []string
	const marker = `href="`
	rest := htmlBody
	for {
		i := strings.Index(rest, marker)
		if i == -1 {
			break
		}
		rest = rest[i+len(marker):]
		j := strings.Index(rest, `"`)
		if j == -1 {
			break
		}
		out = append(out, rest[:j])
		rest = rest[j:]
	}
	return out
}

// extractTextURLs pulls every https?:// token out of a plain-text body.
func extractTextURLs(text string) []string {
	var out []string
	for _, field := range strings.Fields(text) {
		field = strings.Trim(field, "()<>,")
		if strings.HasPrefix(field, "https://") || strings.HasPrefix(field, "http://") {
			out = append(out, field)
		}
	}
	return out
}

// TestConfirmationAndAlreadySubscribed_CarryFooterLink proves the two
// mailing-list templates both carry the "Manage your interests · Unsubscribe
// from everything" footer link pair, per PRD §6.5 Path 2 ("the footer of
// every email"). Mutation proof: flip ShowListFooter to false in
// BuildConfirmationEmail and this test fails on "manage link".
func TestConfirmationAndAlreadySubscribed_CarryFooterLink(t *testing.T) {
	for _, name := range []string{"confirmation", "already_subscribed"} {
		msg := allMessages()[name]
		if !strings.Contains(msg.HTMLBody, "Manage your interests") {
			t.Errorf("%s: HTML missing 'Manage your interests' footer text", name)
		}
		if !strings.Contains(msg.HTMLBody, "Unsubscribe from everything") {
			t.Errorf("%s: HTML missing 'Unsubscribe from everything' footer text", name)
		}
		if !strings.Contains(msg.TextBody, "Manage your interests:") {
			t.Errorf("%s: text missing 'Manage your interests:' footer line", name)
		}
		if !strings.Contains(msg.TextBody, "Unsubscribe from everything:") {
			t.Errorf("%s: text missing 'Unsubscribe from everything:' footer line", name)
		}
	}
}

// TestConfirmationAndAlreadySubscribed_NoCampaignHeaders proves neither
// mailing-list template attaches List-Unsubscribe / List-Unsubscribe-Post /
// List-Id — those are RFC 8058 one-click headers for CAMPAIGN mail (#0035,
// #0043) only. The privacy policy (#0075) deliberately narrows its one-click
// commitment to "every campaign email" for exactly this reason: the
// confirmation and already-subscribed mails are transactional, sent
// synchronously from the subscribe handler, not campaign sends.
func TestConfirmationAndAlreadySubscribed_NoCampaignHeaders(t *testing.T) {
	for _, name := range []string{"confirmation", "already_subscribed"} {
		msg := allMessages()[name]
		if len(msg.Headers) != 0 {
			t.Errorf("%s: message carries %d custom header(s), want 0 (no List-Unsubscribe on transactional mail): %+v", name, len(msg.Headers), msg.Headers)
		}
	}
}

// TestRegistrationAndRecovery_NoListFooter proves the three account emails
// (registration, recovery, and — since #0076's review bounce — the themed
// sessions_revoked notice) do NOT carry mailing-list footer links or a
// physical address — they have nothing to do with subscriber list
// membership, and PRD §6.6's physical address requirement is scoped to
// commercial/list mail. Before this test named sessions_revoked, the
// "no unsubscribe link on a security notice" property was golden-locked
// only; an unsubscribe link on a security notice would be actively
// misleading, since these are transactional, not campaign, mail.
func TestRegistrationAndRecovery_NoListFooter(t *testing.T) {
	for _, name := range []string{"registration", "recovery", "sessions_revoked"} {
		msg := allMessages()[name]
		if strings.Contains(msg.HTMLBody, "Manage your interests") {
			t.Errorf("%s: HTML unexpectedly carries the mailing-list footer", name)
		}
		if strings.Contains(msg.HTMLBody, testAddress) {
			t.Errorf("%s: HTML unexpectedly carries a physical address", name)
		}
	}
}

// TestConfirmationEmail_ExpiryTextTracksTTL proves the "expires in N days"
// copy is actually derived from the ttl argument, not a hard-coded string
// that could silently drift from whatever #0026 sets ConfirmTTL to.
// Mutation proof: hard-code "7 days" in BuildConfirmationEmail instead of
// formatDuration(ttl) and this test's second case (14 days) fails.
func TestConfirmationEmail_ExpiryTextTracksTTL(t *testing.T) {
	cases := []struct {
		ttl  time.Duration
		want string
	}{
		{7 * 24 * time.Hour, "expires in 7 days"},
		{14 * 24 * time.Hour, "expires in 14 days"},
		{1 * 24 * time.Hour, "expires in 1 day"},
	}
	for _, tc := range cases {
		msg := BuildConfirmationEmail(testTo, testBaseURL, testConfirm, testManage, tc.ttl, testAddress)
		if !strings.Contains(msg.TextBody, tc.want) {
			t.Errorf("ttl=%v: text body does not contain %q; got:\n%s", tc.ttl, tc.want, msg.TextBody)
		}
		if !strings.Contains(msg.HTMLBody, tc.want) {
			t.Errorf("ttl=%v: HTML body does not contain %q", tc.ttl, tc.want)
		}
	}
}

// TestPerRecipientTokenSubstitution_PutsTheRightTokenInTheRightMail proves
// that when two different subscribers' emails are built back to back, each
// message's links carry ITS OWN tokens — not a token left over from the
// previous call. This is the exact failure mode the brief warns about: a
// template test that only checks "contains a confirm link" cannot catch a
// mistake like a shared package-level buffer or an accidentally-reused
// emailContent value leaking one recipient's token into another's mail.
func TestPerRecipientTokenSubstitution_PutsTheRightTokenInTheRightMail(t *testing.T) {
	msgAlice := BuildConfirmationEmail("alice@example.com", testBaseURL, "alice-confirm-tok", "alice-manage-tok", 7*24*time.Hour, testAddress)
	msgBob := BuildConfirmationEmail("bob@example.com", testBaseURL, "bob-confirm-tok", "bob-manage-tok", 7*24*time.Hour, testAddress)

	if msgAlice.To != "alice@example.com" || msgBob.To != "bob@example.com" {
		t.Fatalf("To addresses swapped or wrong: alice.To=%q bob.To=%q", msgAlice.To, msgBob.To)
	}

	// Alice's mail must carry ONLY Alice's tokens.
	if !strings.Contains(msgAlice.HTMLBody, "alice-confirm-tok") {
		t.Errorf("alice's HTML missing her own confirm token")
	}
	if !strings.Contains(msgAlice.HTMLBody, "alice-manage-tok") {
		t.Errorf("alice's HTML missing her own manage token")
	}
	if strings.Contains(msgAlice.HTMLBody, "bob-confirm-tok") || strings.Contains(msgAlice.HTMLBody, "bob-manage-tok") {
		t.Errorf("alice's HTML leaked bob's token")
	}
	if strings.Contains(msgAlice.TextBody, "bob-confirm-tok") || strings.Contains(msgAlice.TextBody, "bob-manage-tok") {
		t.Errorf("alice's text leaked bob's token")
	}

	// And vice versa.
	if !strings.Contains(msgBob.HTMLBody, "bob-confirm-tok") {
		t.Errorf("bob's HTML missing his own confirm token")
	}
	if strings.Contains(msgBob.HTMLBody, "alice-confirm-tok") || strings.Contains(msgBob.HTMLBody, "alice-manage-tok") {
		t.Errorf("bob's HTML leaked alice's token")
	}
}

// TestAlreadySubscribedEmail_CarriesPreferenceLinkNotConfirmLink proves the
// already-subscribed template's primary link goes to /preferences, never to
// /confirm — there is nothing to confirm for an already-active subscriber.
// Mutation proof: point its ButtonURL at a confirm URL instead and this
// fails on the "/confirm?" assertion.
func TestAlreadySubscribedEmail_CarriesPreferenceLinkNotConfirmLink(t *testing.T) {
	msg := BuildAlreadySubscribedEmail(testTo, testBaseURL, testManage, testAddress)
	if !strings.Contains(msg.HTMLBody, testBaseURL+"/preferences?token="+testManage) {
		t.Errorf("HTML missing the preference-center link")
	}
	if strings.Contains(msg.HTMLBody, "/confirm?token=") {
		t.Errorf("already-subscribed HTML unexpectedly contains a /confirm link")
	}
	if strings.Contains(msg.TextBody, "/confirm?token=") {
		t.Errorf("already-subscribed text unexpectedly contains a /confirm link")
	}
}

// TestConfirmationEmail_CarriesConfirmLinkAndIfYouDidntRequestLine proves
// the two specific pieces of copy #0028's acceptance criteria name by exact
// content, not just presence: the /confirm?token= link, and the "if you
// didn't request this" disclaimer.
func TestConfirmationEmail_CarriesConfirmLinkAndIfYouDidntRequestLine(t *testing.T) {
	msg := BuildConfirmationEmail(testTo, testBaseURL, testConfirm, testManage, 7*24*time.Hour, testAddress)
	wantLink := testBaseURL + "/confirm?token=" + testConfirm
	if !strings.Contains(msg.HTMLBody, wantLink) {
		t.Errorf("HTML missing confirm link %q", wantLink)
	}
	if !strings.Contains(msg.TextBody, wantLink) {
		t.Errorf("text missing confirm link %q", wantLink)
	}
	if !strings.Contains(strings.ToLower(msg.TextBody), "if you didn't request this") {
		t.Errorf("text missing the 'if you didn't request this' disclaimer")
	}
}

// TestButtonTextIsNeverBrightGreen is a structural guard against the
// CLAUDE.md §8 contrast trap: colorHeading (#68ff23, only 1.32:1 on white)
// must never be used as the CTA button's own text color. The button's
// background can plausibly be stripped independently of the card behind it,
// so its text must stay legible on its own — colorButtonText (near-black) is
// legible whether the button's green fill renders or the canvas falls back
// to plain white; bright green on an unknown/absent background is not.
// Mutation proof: change the button <a> style from colorButtonText to
// colorHeading in templates.go and this test fails.
func TestButtonTextIsNeverBrightGreen(t *testing.T) {
	for name, msg := range allMessages() {
		// The button's <a> tag must use colorButtonText, not colorHeading —
		// bright green text is only safe on the guaranteed-dark card
		// background, and the button's own background can plausibly get
		// stripped independently of the card around it.
		if idx := strings.Index(msg.HTMLBody, `bgcolor="`+colorButtonBG+`"`); idx != -1 {
			// Look at the <a ...> that immediately follows the button cell.
			rest := msg.HTMLBody[idx:]
			aIdx := strings.Index(rest, "<a href=")
			if aIdx == -1 {
				continue
			}
			end := strings.Index(rest[aIdx:], ">")
			aTag := rest[aIdx : aIdx+end]
			if strings.Contains(aTag, "color:"+colorHeading) {
				t.Errorf("%s: button link text uses bright green (%s) instead of %s", name, colorHeading, colorButtonText)
			}
			if !strings.Contains(aTag, "color:"+colorButtonText) {
				t.Errorf("%s: button link text does not use the guaranteed-legible %s", name, colorButtonText)
			}
		}
	}
}

// TestNoTextBodyContainsButton is #0076's regression guard for the exact
// defect #0028's reviewer found by reading the golden files as a recipient
// rather than diffing them: the shared renderer wrote "click the button
// below" into the text part for the registration and recovery mails even
// though the text part has no button, only a raw URL. renderHTML and
// renderText now substitute different words for the shared {{cta}} token
// (see templates.go's ctaToken/resolveCTA), so this asserts the outcome that
// substitution exists to guarantee.
//
// Mutation proof: replace any use of resolveCTA(p, "link") in renderText
// with resolveCTA(p, "button") (or just hard-code the literal word "button"
// into a paragraph again, bypassing the token) and this test fails.
func TestNoTextBodyContainsButton(t *testing.T) {
	for name, msg := range allMessages() {
		if strings.Contains(strings.ToLower(msg.TextBody), "button") {
			t.Errorf("%s: TextBody contains the word 'button' — text readers have no button, only a link\nbody:\n%s", name, msg.TextBody)
		}
	}
}

// TestHTMLAndTextCTAWordingDiffer proves the HTML and text parts CAN differ
// on the {{cta}} word — not merely that text avoids "button", but that HTML
// positively uses it where a button actually exists and text positively uses
// "link" where only a raw URL does. Without this, TestNoTextBodyContainsButton
// alone would also pass for a renderer that dropped the CTA sentence from
// text entirely, which is not the fix this issue asked for.
//
// This asserts on the resolved CTA SENTENCE ("Click the button below" /
// "Click the link below"), not a bare substring. A bare "button" check is
// satisfied for every template by the unconditional "If the button above
// doesn't work…" fallback line renderHTML always emits when ButtonURL is
// set, and a bare "link" check is satisfied for registration/recovery by
// "This link expires in N minutes." — neither proves resolveCTA actually
// ran. #0076's review bounce caught this: the previous version of this test
// only genuinely exercised sessions_revoked. Mutation proof: replace either
// resolveCTA call in renderHTML/renderText with the other word (or the
// hard-coded literal) and this test fails on the sentence check, same as
// before, but now it also fails if the CTA sentence is silently dropped from
// a template while the unrelated "expires"/"doesn't work" lines remain.
func TestHTMLAndTextCTAWordingDiffer(t *testing.T) {
	for _, name := range []string{"registration", "recovery", "sessions_revoked"} {
		msg := allMessages()[name]
		if !strings.Contains(msg.HTMLBody, "Click the button below") {
			t.Errorf("%s: HTML body missing the resolved CTA sentence 'Click the button below'", name)
		}
		if !strings.Contains(msg.TextBody, "Click the link below") {
			t.Errorf("%s: text body missing the resolved CTA sentence 'Click the link below'", name)
		}
	}
}

// TestNoRenderedBodyContainsCTAToken is #0076's review-bounce finding 1:
// resolveCTA only runs over IntroParagraphs and NoteParagraphs. A probe
// template that placed {{cta}} in Subject, Preheader, Eyebrow, Heading,
// ButtonText, or ButtonURL shipped the literal token verbatim — 8 unresolved
// occurrences in the HTML part (including inside the href) and 4 in the
// text part. There's no injection risk (substitution precedes
// html.EscapeString, so an unresolved token is just escaped text) but it
// undercuts fix 1's claim that HTML and text "cannot be recoupled by
// accident": a future author who writes {{cta}} into any field but the two
// paragraph slices ships the raw token. This guards the templates that
// actually exist today; it will not catch a *new* field misuse until that
// field is added to allMessages()'s inputs, but it does prove none of the
// five current templates leak the token anywhere in their rendered output.
//
// Mutation proof: add "{{cta}}" to any Build*Email's Subject or ButtonText
// and this test fails, naming the field and the leaked token.
func TestNoRenderedBodyContainsCTAToken(t *testing.T) {
	for name, msg := range allMessages() {
		if strings.Contains(msg.HTMLBody, ctaToken) {
			t.Errorf("%s: HTMLBody contains unresolved %q", name, ctaToken)
		}
		if strings.Contains(msg.TextBody, ctaToken) {
			t.Errorf("%s: TextBody contains unresolved %q", name, ctaToken)
		}
		if strings.Contains(msg.Subject, ctaToken) {
			t.Errorf("%s: Subject contains unresolved %q", name, ctaToken)
		}
	}
}

// TestAllTemplates_TextBodyUsesCRLF asserts the RFC 5322 \r\n line-ending
// contract in Go, not only via the internal/mailing/testdata/*.txt -text
// override in .gitattributes (see testdata/README.md and #0078). Before this
// test, `grep -rn '\r' internal/mailing/*_test.go internal/auth/*_test.go`
// returned nothing anywhere in the repo — those four (now five) golden .txt
// files were the ONLY assertion of this contract, and ses_mailer.go hands
// TextBody straight to SES's TextBody field without re-adding CR itself. So
// the contract rested entirely on a git checkout attribute, not on any Go
// code path actually checking it.
//
// Mutation proof: change renderText's `strings.Join(lines, "\r\n")` to
// `strings.Join(lines, "\n")` in templates.go and this test fails — legibly,
// in `go test` output, rather than only via a diff on someone else's machine
// after a fresh checkout normalizes the fixture.
func TestAllTemplates_TextBodyUsesCRLF(t *testing.T) {
	for name, msg := range allMessages() {
		if !strings.HasSuffix(msg.TextBody, "\r\n") {
			t.Errorf("%s: TextBody does not end with \\r\\n", name)
			continue
		}
		// Every line break must be \r\n, not a bare \n: strip every \r\n pair
		// and confirm no \n remains.
		if strings.Contains(strings.ReplaceAll(msg.TextBody, "\r\n", ""), "\n") {
			t.Errorf("%s: TextBody contains a bare \\n not paired with \\r", name)
		}
	}
}
