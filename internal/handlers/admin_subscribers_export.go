// admin_subscribers_export.go implements GET /admin/subscribers/export
// (#0059, PRD §8): a streaming CSV export of the subscribers table for
// backup, analysis, and portability.
//
// # Column set — narrower than the admin screen, on purpose
//
// email, status, interests (semicolon-joined slugs), confirmed_at,
// created_at, utm_source, utm_medium, utm_campaign — exactly #0059's
// acceptance criteria, no more. In particular this export omits everything
// subscriberView.go's doc comment (admin_subscribers.go) already explains
// the admin *screen* omits, for the same reasons, plus more that the screen
// includes but an export should not:
//
//   - confirm_token / manage_token: NEVER included anywhere in this package,
//     screen or export — they are live bearer credentials (an
//     unsubscribe/preference-center token), not display data. A CSV leaving
//     the admin console is the single easiest way for that rule to be
//     violated by accident, so internal/subscribers.ExportRow's fields don't
//     even have room for them — there is no column to forget to drop.
//   - id: an internal primary key with no meaning outside this database; a
//     restored/re-imported CSV should key on email, not a value from a table
//     it may never see again.
//   - signup_ip / signup_user_agent / unsubscribed_at / unsubscribe_source:
//     not asked for by #0059's acceptance criteria and not shown on the
//     admin list screen either (only the one-row detail view has them,
//     alongside the interest and soft-bounce detail also absent here).
//
// interests is exported as semicolon-joined SLUGS, not display names:
// slugs are constrained lowercase-hyphenated at the database
// (interests_slug_format, migrations/000009) so they can never themselves
// contain a semicolon, a comma, or a newline, and they are the stable
// machine-readable identifier #0024's admin taxonomy CRUD treats as primary
// — a re-import would match against slug, not the free-text name an
// operator can rename at any time.
//
// # CSV injection
//
// utm_source/utm_medium/utm_campaign are attacker-controlled: they come
// straight from the query string of the public /api/subscribe form
// (internal/handlers/subscribe.go), so a malicious signup can plant
// =cmd|'/c calc'!A1 or similar into a UTM field and have every future
// export of the list carry it into whatever spreadsheet application opens
// it. email is technically also visitor-controlled in principle (SMTPUTF8
// permits unusual local-parts, per internal/subscribers.Store's doc
// comment), and interests is admin-controlled but cheap to guard uniformly
// rather than special-cased. csvInjectionGuard below prefixes any field
// whose first NON-WHITESPACE character is =, +, -, or @ with a single
// quote, matching #0059's Notes: that neutralizes the formula interpretation
// in Excel/Sheets/Numbers while leaving the value visibly intact (a leading
// quote is stripped from Excel's on-screen render of a text cell, and is
// exactly what Excel itself inserts when you type a leading-apostrophe-
// protected value). Checking the first non-whitespace byte rather than just
// s[0] matters: a leading TAB or CR (named by OWASP alongside =+-@) — or
// LF, space, vertical tab — is trimmed by spreadsheet importers before the
// cell is evaluated, so a naive first-byte check is bypassed by exactly the
// characters an attacker would use to hide the leading =. Applied to every
// free-text column an external submitter can influence: email, interests,
// utm_source, utm_medium, utm_campaign. status is one of a fixed
// Go-constant vocabulary and confirmed_at/created_at are server-generated
// timestamps, so neither needs it.
//
// # Buffered vs. streamed
//
// Streamed, per #0059's explicit acceptance criterion. Two layers cooperate:
// subscribers.Store.StreamExport (internal/subscribers/export.go) invokes a
// callback per pgx row as it arrives off the wire rather than collecting a
// []Subscriber first, and this handler's callback writes that row straight
// into an encoding/csv.Writer wrapping the http.ResponseWriter — so at no
// point does either layer hold the full result set in memory. csv.Writer
// wraps its output in a bufio.Writer, which flushes to the underlying
// ResponseWriter on its own once its (small, fixed) internal buffer fills,
// so rows reach the client well before the query finishes even without an
// explicit per-row Flush call here.
//
// # Mid-stream failure — what the client actually sees, and the audit fix
//
// WriteHeader(200) happens before StreamExport runs, so a store failure
// partway through cannot change the status already sent. Measured directly
// (a store failing after 3 of 100 rows): status=200, a valid Content-Length,
// a nil body-read error, and a syntactically well-formed — merely short —
// CSV. There is NO broken-pipe signal and no truncation marker of any kind:
// a truncated export is indistinguishable from a complete one at the HTTP
// layer, and a well-behaved client saves the short file as a complete
// backup. (An external prior version of this comment claimed "a truncated
// CSV and a broken pipe on the server side"; that was never true — it
// described what might happen on a real client TCP disconnect, not on a
// server-side/store failure, which is the actually-reachable case this
// paragraph is about.)
//
// The one place this failure IS recorded is the audit row below:
// h.auditor.Record runs unconditionally, success or mid-stream failure, and
// its metadata carries "error" (only when streamErr != nil) alongside the
// partial row_count and the filter that was applied — enough to
// reconstruct who exported what, how much of it actually went out, and
// under what filter. That Record call uses context.WithoutCancel(r.Context()),
// not r.Context() — deliberately, and only here (see the Record call site
// below for why) — so the row survives even a genuine client disconnect,
// which is the case the naive r.Context() version of this handler lost
// entirely (#0059's phase-3 review).
//
// Decision, recorded per that same review: this handler does NOT
// panic(http.ErrAbortHandler) to turn a mid-stream store failure into a
// hard transfer error the client can detect. That is the idiomatic Go way
// to abort a response deliberately, and it was considered. Reasons against,
// for now: (1) it would change observable behavior for the admin console's
// own download on a failure mode — the backing store erroring mid-scan —
// that is not this project's normal operating condition (CLAUDE.md §5: no
// performance requirement, a marketing-site mailing list, not a bulk-data
// platform); (2) it interacts with the audit fix above in a way that is
// easy to get backwards — the panic must happen strictly AFTER
// h.auditor.Record, or aborting the response reintroduces the exact
// lost-audit-row defect this paragraph's fix exists to close; (3) the audit
// log already carries the forensic signal a hard abort would only gesture
// at (see above), which is sufficient for the operator-facing threat model
// this issue's Notes describe. Revisit if this endpoint ever gains a
// consumer other than a human at the admin console — e.g. a scheduled
// backup job — that would act on a short file as though it were complete;
// that is the point where the CLIENT needs its own failure signal, not just
// a record an operator can look up after the fact.
package handlers

import (
	"context"
	"encoding/csv"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// exportCSVHeader is the header row, in the exact column order #0059's
// acceptance criteria list them.
var exportCSVHeader = []string{
	"email", "status", "interests", "confirmed_at", "created_at",
	"utm_source", "utm_medium", "utm_campaign",
}

// csvInjectionGuard prefixes s with a single quote if its first
// NON-WHITESPACE byte is one Excel/Sheets/Numbers treats as starting a
// formula (=, +, -, @) when a CSV cell is opened — see the package doc
// comment. Left untouched otherwise, including the empty string and a
// string that is all whitespace (nothing to guard).
//
// Checking s[0] alone (the original implementation) was bypassed by a
// leading TAB, CR, LF, space, or vertical tab: encoding/csv round-trips such
// a field byte-exact (quoting it because it contains a control character or
// starts with a space), but spreadsheet importers trim leading whitespace
// before evaluating the cell, so the trimmed value still opens as a formula.
// OWASP's CSV-injection guidance names TAB and CR alongside =, +, -, @ for
// exactly this reason. The quote is still inserted at the very front of the
// ORIGINAL (untrimmed) string, not the trimmed one: a leading apostrophe
// forces Excel to treat the whole cell as text regardless of what follows
// it, whitespace included, so there is no need to also strip the
// whitespace. This is also why the guard stays idempotent on re-export — a
// value already prefixed with "'" has "'" as its first non-whitespace byte,
// which is not one of the four guarded characters, so it is never
// double-prefixed.
func csvInjectionGuard(s string) string {
	trimmed := strings.TrimLeftFunc(s, unicode.IsSpace)
	if trimmed == "" {
		return s
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + s
	default:
		return s
	}
}

// derefOrEmpty reads *p, or "" when p is nil — the one place NULL-vs-empty
// mapping happens for the three UTM columns, so a NULL column can never
// render as the literal text "<nil>" the way fmt.Sprintf("%v", (*string)(nil))
// would.
func derefOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// formatExportTime renders a *time.Time as RFC 3339 UTC, or "" when nil
// (confirmed_at is NULL for a subscriber that never completed double
// opt-in — pending, or unsubscribed/bounced/complained before confirming).
func formatExportTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// exportRowToRecord converts one subscribers.ExportRow into the CSV record
// exportCSVHeader describes, applying csvInjectionGuard to every
// externally-influenced text field. encoding/csv.Writer.Write handles all
// quoting for embedded commas/quotes/newlines itself — nothing here does
// manual escaping.
func exportRowToRecord(row subscribers.ExportRow) []string {
	return []string{
		csvInjectionGuard(row.Email),
		row.Status, // fixed Go-constant vocabulary; never externally influenced
		csvInjectionGuard(row.Interests),
		formatExportTime(row.ConfirmedAt), // never externally influenced (timestamp)
		row.CreatedAt.UTC().Format(time.RFC3339),
		csvInjectionGuard(derefOrEmpty(row.UTMSource)),
		csvInjectionGuard(derefOrEmpty(row.UTMMedium)),
		csvInjectionGuard(derefOrEmpty(row.UTMCampaign)),
	}
}

// Export handles GET /admin/subscribers/export?status=&interest_id=&q=. It
// reuses List's own query-parameter validation (validSubscriberStatus,
// interest_id parsing) so an invalid filter is rejected with 400 before any
// header is written, exactly as it would be for GET /admin/subscribers.
//
// Audited unconditionally, success or failure (see the auditor.Record call
// below and ActionSubscriberExported's doc comment,
// internal/audit/actions.go) — an export is the highest-value single read an
// admin account can perform against this table, and it is exactly what an
// attacker holding a stolen session would do (#0059's Notes).
func (h *AdminSubscribersHandler) Export(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	q := r.URL.Query()
	status := q.Get("status")
	if status != "" && !validSubscriberStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	var interestID int64
	if raw := q.Get("interest_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid interest_id")
			return
		}
		interestID = id
	}
	query := q.Get("q")
	filter := subscribers.ListFilter{Status: status, InterestID: interestID, Query: query}

	// Headers before the first byte of the body: Content-Disposition's
	// filename is a fixed literal, never interpolated from user input (no
	// admin-controlled or attacker-controlled string reaches it), so there is
	// nothing to sanitize — it is the one place this handler could otherwise
	// have introduced a header-injection surface and deliberately doesn't.
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="subscribers-export.csv"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	if err := cw.Write(exportCSVHeader); err != nil {
		// The client disappeared before the header even landed; nothing left
		// to do but stop. No further response is possible: WriteHeader has
		// already committed 200.
		return
	}

	var rowCount int
	streamErr := h.store.StreamExport(r.Context(), filter, func(row subscribers.ExportRow) error {
		if err := cw.Write(exportRowToRecord(row)); err != nil {
			return err
		}
		rowCount++
		return nil
	})
	cw.Flush()
	if flushErr := cw.Error(); streamErr == nil {
		streamErr = flushErr
	}

	if streamErr != nil {
		// The response is already 200 with a partial CSV body in flight — an
		// HTTP status can't be changed at this point. Log server-side so a
		// truncated export is at least discoverable, and still audit below
		// with what was actually delivered.
		slog.Error("admin subscribers export: stream failed",
			"error", streamErr, "rows_written", rowCount, "actor_id", actor.ID)
	}

	if h.auditor != nil {
		actorID := actor.ID
		// "filter_query" is logged verbatim, deliberately: this row is the
		// anti-exfiltration control described below, so knowing exactly what
		// an admin searched for before exporting is the point. The
		// consequence, found and pinned by #0237's phase-3 review: if that
		// search happened to be a subscriber's own address, this Metadata
		// now carries it, and it survives erasure the same way the other
		// email-carrying audit rows do (nothing in
		// internal/subscribers/erase.go touches audit_log). Do not redact
		// or drop this to "fix" that — it would defeat the control — the
		// privacy page (PrivacyPolicy.svelte's audit-log item) discloses it
		// as a possibility instead, and
		// internal/handlers/audit_email_metadata_guard_test.go pins this
		// exact site (ActionSubscriberExported,
		// metadataKeyIsSuspectedEmailCarrier's "query" token) so a future
		// change here cannot silently drop that disclosure's premise.
		metadata := map[string]any{
			"row_count":          rowCount,
			"filter_status":      status,
			"filter_interest_id": interestID,
			"filter_query":       query,
		}
		if streamErr != nil {
			metadata["error"] = streamErr.Error()
		}
		// context.WithoutCancel, not r.Context(): this handler is the one
		// place in the tree where that matters (see the package doc
		// comment's "Audit and client disconnect" section) — a download
		// that runs for the duration of a large transfer, whose audit row
		// is an anti-exfiltration control rather than an ordinary change
		// record. r.Context() remains correct at every other Record call
		// site in this package/tree (22 of 26, per the phase-3 review); do
		// not change those — for them the mutation and the audit insert are
		// microseconds apart under the same request, so there is no
		// meaningful disconnect window to lose the row to.
		h.auditor.Record(context.WithoutCancel(r.Context()), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSubscriberExported,
			TargetType: audit.TargetSubscriber,
			Metadata:   metadata,
			IP:         clientIP(r),
		})
	}
}
