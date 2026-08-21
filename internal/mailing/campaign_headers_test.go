package mailing

import (
	"net/url"
	"strings"
	"testing"
)

// testListDomain mirrors config.Config.EmailListDomain's real value
// (internal/config/config_test.go asserts EMAIL_LIST_DOMAIN=lists.opencircuitsf.com
// loads to exactly this) — lists.opencircuitsf.com, plural, distinct from
// campaignListID's singular "list.opencircuitsf.com".
const testListDomain = "lists.opencircuitsf.com"

// headerValue returns the value of the first header named name, failing the
// test if none is found.
func headerValue(t *testing.T, headers []Header, name string) string {
	t.Helper()
	for _, h := range headers {
		if h.Name == name {
			return h.Value
		}
	}
	t.Fatalf("no header named %q in %+v", name, headers)
	return ""
}

func TestCampaignHeaders_ReturnsExactlyTheThreeRFC8058Headers(t *testing.T) {
	got := CampaignHeaders(testBaseURL, testListDomain, testManage)
	if len(got) != 3 {
		t.Fatalf("got %d headers, want 3: %+v", len(got), got)
	}
	wantNames := []string{"List-Unsubscribe", "List-Unsubscribe-Post", "List-Id"}
	for i, name := range wantNames {
		if got[i].Name != name {
			t.Errorf("header %d: got name %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestCampaignHeaders_ListUnsubscribe_CarriesHTTPSFormWithRecipientToken(t *testing.T) {
	headers := CampaignHeaders(testBaseURL, testListDomain, testManage)
	got := headerValue(t, headers, "List-Unsubscribe")

	want := "<https://www.opencircuitsf.com/api/unsubscribe?token=" + testManage + ">"
	if !strings.Contains(got, want) {
		t.Errorf("List-Unsubscribe missing HTTPS form: got %q, want it to contain %q", got, want)
	}
}

func TestCampaignHeaders_ListUnsubscribe_CarriesMailtoFormOnListDomain(t *testing.T) {
	headers := CampaignHeaders(testBaseURL, testListDomain, testManage)
	got := headerValue(t, headers, "List-Unsubscribe")

	if !strings.Contains(got, "<mailto:unsubscribe@lists.opencircuitsf.com?subject=") {
		t.Errorf("List-Unsubscribe missing mailto form on lists.opencircuitsf.com: got %q", got)
	}
	// CLAUDE.md §9: never point anything at the apex MX. Path 3's inbound
	// mailbox lives on the dedicated lists.opencircuitsf.com subdomain
	// (#0057), not the apex.
	if strings.Contains(got, "mailto:unsubscribe@opencircuitsf.com?") {
		t.Errorf("List-Unsubscribe wrongly targets the apex domain instead of lists.opencircuitsf.com: %q", got)
	}
}

func TestCampaignHeaders_ListUnsubscribe_BothFormsCommaSeparated(t *testing.T) {
	// RFC 8058: multiple List-Unsubscribe URIs are comma-separated,
	// each individually angle-bracketed.
	headers := CampaignHeaders(testBaseURL, testListDomain, testManage)
	got := headerValue(t, headers, "List-Unsubscribe")

	if !strings.Contains(got, ">, <") {
		t.Errorf("List-Unsubscribe forms are not comma-separated per RFC 8058: got %q", got)
	}
	if strings.Count(got, "<") != 2 || strings.Count(got, ">") != 2 {
		t.Errorf("List-Unsubscribe should carry exactly two angle-bracketed URIs: got %q", got)
	}
}

func TestCampaignHeaders_MailtoSubjectDecodesToUnsubscribeColonToken(t *testing.T) {
	headers := CampaignHeaders(testBaseURL, testListDomain, testManage)
	got := headerValue(t, headers, "List-Unsubscribe")

	start := strings.Index(got, "subject=")
	if start == -1 {
		t.Fatalf("mailto URI has no subject= parameter: %q", got)
	}
	encoded := got[start+len("subject="):]
	encoded = strings.TrimSuffix(encoded, ">")
	decoded, err := url.QueryUnescape(encoded)
	if err != nil {
		t.Fatalf("decoding percent-encoded subject: %v", err)
	}
	want := "unsubscribe:" + testManage
	if decoded != want {
		t.Errorf("mailto Subject decodes to %q, want %q (PRD §6.5's literal form)", decoded, want)
	}
}

func TestCampaignHeaders_ListUnsubscribePost_IsOneClickMarker(t *testing.T) {
	headers := CampaignHeaders(testBaseURL, testListDomain, testManage)
	got := headerValue(t, headers, "List-Unsubscribe-Post")
	want := "List-Unsubscribe=One-Click"
	if got != want {
		t.Errorf("List-Unsubscribe-Post = %q, want %q", got, want)
	}
}

func TestCampaignHeaders_ListId_MatchesPRDLiteralForm(t *testing.T) {
	headers := CampaignHeaders(testBaseURL, testListDomain, testManage)
	got := headerValue(t, headers, "List-Id")
	want := "Open Circuit SF <list.opencircuitsf.com>"
	if got != want {
		t.Errorf("List-Id = %q, want %q", got, want)
	}
}

// TestCampaignHeaders_MultiRecipientBatch_TokensDoNotCrossContaminate is the
// per-recipient correctness proof this issue's acceptance criteria demand:
// simulating the shape of #0045's send loop — one Message built per
// recipient in a batch — recipient A's headers must carry only recipient
// A's own manage_token, never another recipient's.
func TestCampaignHeaders_MultiRecipientBatch_TokensDoNotCrossContaminate(t *testing.T) {
	recipients := []struct {
		to    string
		token string
	}{
		{"alice@example.com", "TOKEN-ALICE-AAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{"bob@example.com", "TOKEN-BOB-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"},
		{"carol@example.com", "TOKEN-CAROL-CCCCCCCCCCCCCCCCCCCCCCCCCCCC"},
	}

	messages := make([]Message, len(recipients))
	for i, r := range recipients {
		messages[i] = Message{
			To:       r.to,
			Subject:  "Test campaign",
			HTMLBody: "<p>campaign body</p>",
			TextBody: "campaign body",
			Headers:  CampaignHeaders(testBaseURL, testListDomain, r.token),
		}
	}

	for i, r := range recipients {
		got := headerValue(t, messages[i].Headers, "List-Unsubscribe")
		if !strings.Contains(got, r.token) {
			t.Errorf("recipient %s: List-Unsubscribe missing its own token %q: %q", r.to, r.token, got)
		}
		for j, other := range recipients {
			if i == j {
				continue
			}
			if strings.Contains(got, other.token) {
				t.Errorf("recipient %s: List-Unsubscribe leaked recipient %s's token %q: %q", r.to, other.to, other.token, got)
			}
		}
		// List-Unsubscribe-Post and List-Id carry no token at all, so they
		// are identical (and safely so) across every recipient in the batch.
		if got := headerValue(t, messages[i].Headers, "List-Id"); got != campaignListID {
			t.Errorf("recipient %s: List-Id = %q, want %q", r.to, got, campaignListID)
		}
	}
}

func TestCampaignHeaders_DifferentTokens_ProduceDifferentListUnsubscribeValues(t *testing.T) {
	a := CampaignHeaders(testBaseURL, testListDomain, "TOKEN-A")
	b := CampaignHeaders(testBaseURL, testListDomain, "TOKEN-B")

	if headerValue(t, a, "List-Unsubscribe") == headerValue(t, b, "List-Unsubscribe") {
		t.Error("two different manage_tokens produced identical List-Unsubscribe headers")
	}
	// List-Unsubscribe-Post and List-Id are token-independent and must stay
	// identical.
	if headerValue(t, a, "List-Unsubscribe-Post") != headerValue(t, b, "List-Unsubscribe-Post") {
		t.Error("List-Unsubscribe-Post unexpectedly varies by recipient")
	}
	if headerValue(t, a, "List-Id") != headerValue(t, b, "List-Id") {
		t.Error("List-Id unexpectedly varies by recipient")
	}
}
