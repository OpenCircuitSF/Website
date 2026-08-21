package mailing

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validPreflightInput() PreflightInput {
	now := time.Now()
	return PreflightInput{
		Subject:         "A subject",
		BodyMarkdown:    "Some body",
		AudienceMode:    AudienceAll,
		TestSentAt:      &now,
		PhysicalAddress: "123 Main St",
		ReplyTo:         "hello@example.com",
		ListDomain:      "lists.example.com",
		BaseURL:         "https://www.example.com",
		MailerReady:     true,
		AudienceCount:   5,
	}
}

// TestPreflight_AllRequirementsMet_OK proves the "happy path" produces no
// failures — every other test below flips exactly one field off this
// baseline.
func TestPreflight_AllRequirementsMet_OK(t *testing.T) {
	result := Preflight(validPreflightInput())
	if !result.OK() {
		t.Fatalf("Preflight() = %+v, want OK", result.Failures)
	}
}

// TestPreflight_FailureOrderIsStable pins the fixed evaluation order (§2 of
// this issue's plan) so #0047's rendered list is deterministic rather than
// map-iteration-ordered. A campaign failing every check must produce codes
// in exactly this order.
func TestPreflight_FailureOrderIsStable(t *testing.T) {
	in := PreflightInput{
		// Every field left at its zero value fails every check:
		// Subject/BodyMarkdown empty, RenderErr set, PhysicalAddress empty,
		// ReplyTo empty, TestSentAt nil, AudienceErr set, AudienceCount==0,
		// ListDomain empty, MailerReady false.
		RenderErr:     errors.New("boom"),
		AudienceErr:   errors.New("bad audience"),
		AudienceCount: 0,
	}
	result := Preflight(in)
	want := []string{
		PreflightCodeSubjectEmpty,
		PreflightCodeBodyEmpty,
		PreflightCodeBodyRenderFailed,
		PreflightCodePhysicalAddress,
		PreflightCodeReplyToMissing,
		PreflightCodeNoTestSend,
		PreflightCodeAudienceInvalid,
		PreflightCodeEmptyAudience,
		PreflightCodeListDomainUnset,
		PreflightCodeMailerUnavailable,
	}
	got := result.Codes()
	if len(got) != len(want) {
		t.Fatalf("Codes() = %v (%d), want %d codes: %v", got, len(got), len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Codes()[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestPreflight_PhysicalAddressBlank_TrimsBeforeTesting is
// TestWorker_RefusesCampaignWhenPhysicalAddressBlank's pure-function half:
// the physical_address gate must trim before testing for empty, or a single
// space satisfies "not empty" and ships a CAN-SPAM §7704 violation.
func TestPreflight_PhysicalAddressBlank_TrimsBeforeTesting(t *testing.T) {
	cases := []struct {
		name    string
		address string
		err     error
	}{
		{"empty", "", nil},
		{"single space", " ", nil},
		{"tabs and newlines", "\t\n ", nil},
		{"settings error", "anything", errors.New("setting not found")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validPreflightInput()
			in.PhysicalAddress = tc.address
			in.SettingsErr = tc.err
			result := Preflight(in)
			found := false
			for _, f := range result.Failures {
				if f.Code == PreflightCodePhysicalAddress {
					found = true
				}
			}
			if !found {
				t.Errorf("Preflight(%+v) = %v, want physical_address_missing", in, result.Codes())
			}
		})
	}
}

// TestPreflight_PhysicalAddressPresent_NoFailure is the mutation-target
// baseline for the check above: a real, non-blank address with no settings
// error must NOT produce physical_address_missing.
func TestPreflight_PhysicalAddressPresent_NoFailure(t *testing.T) {
	result := Preflight(validPreflightInput())
	for _, f := range result.Failures {
		if f.Code == PreflightCodePhysicalAddress {
			t.Fatalf("Preflight() unexpectedly failed physical_address_missing: %+v", result.Failures)
		}
	}
}

func TestPreflight_ReplyToBlank_Fails(t *testing.T) {
	in := validPreflightInput()
	in.ReplyTo = "   "
	result := Preflight(in)
	if !containsCode(result, PreflightCodeReplyToMissing) {
		t.Fatalf("Preflight() = %v, want reply_to_missing", result.Codes())
	}
}

func TestPreflight_NoTestSend_Fails(t *testing.T) {
	in := validPreflightInput()
	in.TestSentAt = nil
	result := Preflight(in)
	if !containsCode(result, PreflightCodeNoTestSend) {
		t.Fatalf("Preflight() = %v, want no_test_send", result.Codes())
	}
}

func TestPreflight_EmptyAudience_Fails(t *testing.T) {
	in := validPreflightInput()
	in.AudienceCount = 0
	result := Preflight(in)
	if !containsCode(result, PreflightCodeEmptyAudience) {
		t.Fatalf("Preflight() = %v, want empty_audience", result.Codes())
	}
}

// TestPreflight_AudienceCountNotEvaluated_NoEmptyAudienceFailure proves the
// -1 sentinel means "not evaluated" rather than "zero" — so audience_invalid
// and empty_audience never both fire for the same underlying problem.
func TestPreflight_AudienceCountNotEvaluated_NoEmptyAudienceFailure(t *testing.T) {
	in := validPreflightInput()
	in.AudienceCount = -1
	in.AudienceErr = errors.New("mailing: unknown audience mode")
	result := Preflight(in)
	if containsCode(result, PreflightCodeEmptyAudience) {
		t.Fatalf("Preflight() = %v, empty_audience must not fire when AudienceCount is -1", result.Codes())
	}
	if !containsCode(result, PreflightCodeAudienceInvalid) {
		t.Fatalf("Preflight() = %v, want audience_invalid", result.Codes())
	}
}

// TestPreflight_ListDomainUnset_Fails is criterion 10 of this issue's plan
// §11 — kept even though #0105 made EMAIL_LIST_DOMAIN a required config
// field, per that issue's review: CampaignHeaders degrades silently on a
// blank listDomain, so this is the only LOUD guard.
func TestPreflight_ListDomainUnset_Fails(t *testing.T) {
	cases := []string{"", "   "}
	for _, ld := range cases {
		in := validPreflightInput()
		in.ListDomain = ld
		result := Preflight(in)
		if !containsCode(result, PreflightCodeListDomainUnset) {
			t.Errorf("Preflight() with ListDomain=%q = %v, want list_domain_unset", ld, result.Codes())
		}
	}
}

func TestPreflight_MailerNotReady_Fails(t *testing.T) {
	in := validPreflightInput()
	in.MailerReady = false
	result := Preflight(in)
	if !containsCode(result, PreflightCodeMailerUnavailable) {
		t.Fatalf("Preflight() = %v, want mailer_unavailable", result.Codes())
	}
}

func TestPreflight_RenderErr_Fails(t *testing.T) {
	in := validPreflightInput()
	in.RenderErr = errors.New("template blew up")
	result := Preflight(in)
	if !containsCode(result, PreflightCodeBodyRenderFailed) {
		t.Fatalf("Preflight() = %v, want body_render_failed", result.Codes())
	}
	msg := failureMessage(result, PreflightCodeBodyRenderFailed)
	if msg == "" || !strings.Contains(msg, "template blew up") {
		t.Errorf("body_render_failed message %q does not include the underlying error", msg)
	}
}

func TestPreflight_SubjectAndBodyEmpty_Fail(t *testing.T) {
	in := validPreflightInput()
	in.Subject = "   "
	in.BodyMarkdown = ""
	result := Preflight(in)
	if !containsCode(result, PreflightCodeSubjectEmpty) {
		t.Errorf("Preflight() = %v, want subject_empty", result.Codes())
	}
	if !containsCode(result, PreflightCodeBodyEmpty) {
		t.Errorf("Preflight() = %v, want body_empty", result.Codes())
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func containsCode(r PreflightResult, code string) bool {
	for _, f := range r.Failures {
		if f.Code == code {
			return true
		}
	}
	return false
}

func failureMessage(r PreflightResult, code string) string {
	for _, f := range r.Failures {
		if f.Code == code {
			return f.Message
		}
	}
	return ""
}
