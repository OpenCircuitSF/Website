// Preflight is the one pure pre-send gate function (#0045, PRD §6.6),
// evaluated at two call sites: advisory in internal/handlers' send-transition
// handler (via the campaignPreflightChecker adapter, worker_store.go), and
// authoritative in the worker itself (worker.go), immediately before a
// campaign leaves 'scheduled'. A requirement expressed twice drifts, so both
// call sites build a PreflightInput through SendStore.GatherPreflight and
// hand it to this single function — the same shape #0044 used for the
// audience predicate.
//
// Preflight takes no context, no database, no clock: every I/O concern is
// the caller's (GatherPreflight). That is what makes the order of its nine
// checks a plain, testable property (TestPreflight_FailureOrderIsStable)
// rather than something that could vary with map iteration or query timing.
//
// # Why this is not bypassable from the UI (CLAUDE.md §9)
//
// This function is not a flag the UI sets and the worker trusts — there is
// no "force send" parameter, no override column, no admin route that skips
// it. The worker evaluates it itself, reading physical_address (and every
// other input) fresh from the database immediately before claiming a
// campaign, regardless of how the campaign reached 'scheduled': the normal
// UI path, a raw curl, or a direct `UPDATE email_campaigns SET
// status='scheduled'` in psql all land in front of the worker's own copy of
// this gate. See worker.go's claimAndDrain for where that evaluation
// actually happens.
package mailing

import (
	"strings"
	"time"
)

// Preflight failure codes. Stable and machine-readable — #0047's compose UI
// keys off these exact strings, so renaming one is a breaking change to that
// screen's contract.
const (
	PreflightCodeSubjectEmpty      = "subject_empty"
	PreflightCodeBodyEmpty         = "body_empty"
	PreflightCodeBodyRenderFailed  = "body_render_failed"
	PreflightCodePhysicalAddress   = "physical_address_missing"
	PreflightCodeReplyToMissing    = "reply_to_missing"
	PreflightCodeNoTestSend        = "no_test_send"
	PreflightCodeAudienceInvalid   = "audience_invalid"
	PreflightCodeEmptyAudience     = "empty_audience"
	PreflightCodeListDomainUnset   = "list_domain_unset"
	PreflightCodeMailerUnavailable = "mailer_unavailable"
)

// PreflightFailure is one unmet pre-send requirement.
type PreflightFailure struct {
	// Code is stable and machine-readable; #0047 keys off it.
	Code string
	// Message is operator-facing; #0047 renders it verbatim.
	Message string
}

// PreflightResult is Preflight's return value: the (possibly empty) list of
// unmet requirements, in the fixed order Preflight evaluates them.
type PreflightResult struct {
	Failures []PreflightFailure
}

// OK reports whether the campaign may proceed — no unmet requirements.
func (r PreflightResult) OK() bool {
	return len(r.Failures) == 0
}

// Codes returns just the failure codes, in the same order as Failures, for
// callers (mutation tests, the worker's audit metadata) that only need the
// stable vocabulary rather than the full message text.
func (r PreflightResult) Codes() []string {
	if len(r.Failures) == 0 {
		return nil
	}
	codes := make([]string, len(r.Failures))
	for i, f := range r.Failures {
		codes[i] = f.Code
	}
	return codes
}

// PreflightInput is every fact Preflight needs, gathered by
// SendStore.GatherPreflight so both call sites assemble it identically.
type PreflightInput struct {
	Subject      string
	BodyMarkdown string
	AudienceMode string
	InterestIDs  []int64

	// TestSentAt is email_campaigns.test_sent_at (migration 000018). nil
	// means no test send has ever been delivered — #0046 writes it. Only
	// its nilness is evaluated (PRD §6.6 gates on "a test send has been
	// delivered", not on freshness — see worker_store.go's doc comment on
	// why a freshness comparison would deadlock a legitimate test send).
	TestSentAt *time.Time

	// PhysicalAddress is the RAW settings.physical_address value,
	// UNTRIMMED — validSettingValue applies no format check to this key and
	// the admin UI saves whitespace anyway, so trimming happens here, not
	// at the read site, or a single space would satisfy "not empty" and
	// ship a CAN-SPAM §7704 violation.
	PhysicalAddress string
	// SettingsErr is the error from reading physical_address (e.g.
	// auth.ErrSettingNotFound for a deploy that skipped migration 000008).
	// Treated as "missing", never as a hard error that crashes the worker:
	// an unreadable address is an unset address.
	SettingsErr error

	// ReplyTo is the configured EMAIL_REPLY_TO value. A blank value would
	// otherwise silently ship campaign mail with no Reply-To header at all
	// (SESMailer.Send's `if m.replyTo != ""` guard) — a quiet PRD §6.6
	// violation. Carried in from #0043's review (2026-08-21).
	ReplyTo string

	ListDomain string
	BaseURL    string

	// MailerReady reports whether the mailer this worker/handler would send
	// through is actually configured (not MAILER_NOOP, not nil). Both
	// production call sites only ever construct with MailerReady=true,
	// since a SendStore is never wired at all under MAILER_NOOP=true (the
	// worker refuses to construct — see worker.go's NewWorker doc comment)
	// — this field exists so a future caller with a genuinely unconfigured
	// mailer has somewhere to report that, and so the code path is
	// testable.
	MailerReady bool

	// AudienceCount is the audience size: -1 means "not evaluated" (Preview
	// was skipped or itself errored — see AudienceErr) and produces no
	// empty_audience failure, so that code never fires alongside
	// audience_invalid for the same underlying problem. 0 or more is an
	// evaluated count.
	AudienceCount int64
	// AudienceErr is #0044's typed error from evaluating the audience
	// (ErrUnknownAudienceMode, ErrAudienceInterestsRequired, or a wrapped
	// I/O error), surfaced verbatim in the failure message.
	AudienceErr error

	// RenderErr is the error from a dry render of the campaign body through
	// #0042/#0043's renderer with the sentinel manage token
	// "PREFLIGHT-DRY-RUN" — see GatherPreflight. The rendered bytes
	// themselves are always discarded.
	RenderErr error
}

// Preflight evaluates every pre-send condition in a fixed, tested order and
// returns every unmet one — never just the first, so #0047 can show an
// operator everything that needs fixing in one pass rather than a
// fix-one-resubmit-see-the-next loop.
func Preflight(in PreflightInput) PreflightResult {
	var failures []PreflightFailure
	fail := func(code, message string) {
		failures = append(failures, PreflightFailure{Code: code, Message: message})
	}

	if strings.TrimSpace(in.Subject) == "" {
		fail(PreflightCodeSubjectEmpty, "Subject is empty.")
	}
	if strings.TrimSpace(in.BodyMarkdown) == "" {
		fail(PreflightCodeBodyEmpty, "Body is empty.")
	}
	if in.RenderErr != nil {
		fail(PreflightCodeBodyRenderFailed, "Body does not render: "+in.RenderErr.Error()+".")
	}
	if in.SettingsErr != nil || strings.TrimSpace(in.PhysicalAddress) == "" {
		fail(PreflightCodePhysicalAddress,
			"Physical mailing address is not set (Settings → physical address). CAN-SPAM §7704 requires it in every campaign email.")
	}
	if strings.TrimSpace(in.ReplyTo) == "" {
		fail(PreflightCodeReplyToMissing,
			"Reply-To address is not configured (EMAIL_REPLY_TO). PRD §6.6 requires a monitored reply address, not noreply@.")
	}
	if in.TestSentAt == nil {
		fail(PreflightCodeNoTestSend, "No test send has been delivered yet.")
	}
	if in.AudienceErr != nil {
		fail(PreflightCodeAudienceInvalid, in.AudienceErr.Error())
	}
	if in.AudienceCount == 0 {
		fail(PreflightCodeEmptyAudience, "No recipients match this audience.")
	}
	if strings.TrimSpace(in.ListDomain) == "" {
		fail(PreflightCodeListDomainUnset,
			"EMAIL_LIST_DOMAIN is not configured; the one-click unsubscribe header would be malformed.")
	}
	if !in.MailerReady {
		fail(PreflightCodeMailerUnavailable, "Outbound email is disabled or the SES mailer is not configured.")
	}

	return PreflightResult{Failures: failures}
}
