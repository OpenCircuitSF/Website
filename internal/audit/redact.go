package audit

import (
	"context"
	"fmt"
	"time"
)

// inboundSenderRetention is how long a parked-message audit row keeps the
// full sender address in its metadata's "from" key before the retention
// sweep redacts the local part. 90 days is #0503's recorded decision: it is
// comfortably past the S3 lifecycle rule's 30-day window (#0057 A5), so the
// record still outlives the object it describes for its entire actionable
// life — the whole point #0499 gave it — while a non-subscriber's address
// does not become a permanent entry in an append-only table.
//
// A constant, not a config variable: every other window this project tunes
// at the worker layer (outboxOrphanStaleAfter, the timeout budget constants
// alongside it) lives in code next to the behavior it shapes, and #0503's
// design guidance asked for the existing pattern rather than a new env var.
// A var, not a const literal inline in the SQL below, so the test that pins
// the window's semantics can name it — changing it is a deliberate, reviewed
// change to the recorded decision, exactly like changing the SQL itself.
const inboundSenderRetention = 90 * 24 * time.Hour

// redactedLocalPart is the fixed placeholder the sweep writes in place of a
// From: address's local part. The result reads "redacted@<domain>": the
// domain and the row itself survive (the row stays a count an admin can act
// on), while the identifying half is gone. One-way by construction — the
// original local part is never stored anywhere, only overwritten in place —
// and idempotent by predicate: a row already carrying this local part never
// matches the sweep's WHERE clause again (see redactionSQL).
const redactedLocalPart = "redacted"

// redactionSQL is the sweep's single UPDATE. Every predicate is deliberate:
//
//   - action/target_type are inlined as literals rather than bound as
//     parameters so the statement's plan can always match
//     migrations/000030's partial index predicate (a generic prepared
//     statement cannot infer `action = $1` implies
//     `action = 'inbound_mail.parked'`, and a plan that misses the index
//     turns this per-pass sweep into a sequential scan of the whole
//     append-only table). TestRedactionSQLTracksActionConstants pins these
//     literals to the Go constants so the two cannot drift apart.
//   - metadata ? 'from' skips rows that never carried an address (an
//     unparseable message has neither From: nor Subject; recordParked omits
//     the empty keys).
//   - COALESCE(metadata->>'reason', ”) <> 'auto_reply' is the decision's
//     subscriber case: an auto-reply's From: is the subscriber's own address
//     (measured by #0499's test, which asserts the recorded from equals a
//     real seeded subscriber's Email), so it joins the privacy policy's
//     disclosure list and the erasure path like the other seven email-
//     carrying audit call sites and is retained in full. #0503's decision
//     names from_not_found and no_identifying_signal as the non-subscriber
//     reasons; the token path's token_not_found reason carries the same kind
//     of unverified address (the token path deliberately never checks From:
//     against the list — see internal/handlers/ses_inbound.go's match), so
//     the sweep excludes ONLY the auto_reply case and sweeps every other
//     parked reason, which also keeps a reason added later from silently
//     re-creating the gap. Rows without a reason key (none exist today —
//     recordParked always sets one) are swept too, the conservative
//     direction for an unverified address.
//   - position('@' ...) > 0 skips a From: value with no local/domain split at
//     all (recordParked passes pm.From through as-is); there is nothing to
//     preserve a domain from.
//   - split_part(..., 1) <> 'redacted' is the idempotency guard: a row the
//     sweep has already redacted never matches again, so a second pass both
//     writes nothing and reports zero — and a From: address that genuinely
//     had the local part "redacted" is left alone, already indistinguishable
//     from the placeholder.
//   - created_at < cutoff scopes the rewrite to rows past the window.
//
// The SET expression rewrites only the "from" key in place via jsonb_set;
// every other metadata key (the S3 key, the SNS message id, the Subject, the
// reason) is untouched, which is what keeps the row actionable as a count.
// regexp_replace('^[^@]+@', 'redacted@') replaces everything up to the first
// '@' — one-way, never stored — and keeps the domain part verbatim.
const redactionSQL = `UPDATE audit_log
   SET metadata = jsonb_set(metadata, '{from}',
           to_jsonb(regexp_replace(metadata->>'from', '^[^@]+@', $1)))
 WHERE action = 'inbound_mail.parked'
   AND target_type = 'inbound_mail'
   AND metadata ? 'from'
   AND COALESCE(metadata->>'reason', '') <> 'auto_reply'
   AND position('@' in metadata->>'from') > 0
   AND split_part(metadata->>'from', '@', 1) <> 'redacted'
   AND created_at < $2`

// RedactExpiredInboundSenders is #0503's retention sweep: one idempotent
// UPDATE that replaces the local part of the From: address stored in a
// parked-message audit row's metadata with redactedLocalPart once the row is
// older than inboundSenderRetention. It returns the number of rows it
// redacted this pass.
//
// # Why an UPDATE in an append-only table
//
// audit_log's package doc promise — rows are only ever inserted — is about
// the integrity of the record of what happened, not about holding strangers'
// addresses forever. #0503's decision keeps the record (action, target, S3
// key, message id, subject, reason — and the domain half of the address) so
// it stays a count an admin can act on, and removes only the identifying
// local part of an address belonging to someone with no relationship to the
// project. The exception is narrow — one metadata key, one row shape — and
// is documented where the promise is stated.
//
// # Where it runs
//
// The outbox worker's poll loop calls this once per pass (the same way its
// #0128 expire-pending sweep rides the loop — cheap, idempotent, near-zero
// matching rows in the common case, and migrations/000030's partial index
// keeps the per-pass cost off the rest of the table). It is therefore
// Postgres-only by construction, like everything else the outbox worker
// touches; the in-memory dev store has no audit_log to sweep.
//
// An error is returned, not swallowed: unlike Logger.Record — whose insert
// failure must never break an already-committed action — a failed sweep pass
// is the sweep not happening, and the worker's caller logs it. The next pass
// retries it; the window is 90 days long, so a few failed passes change
// nothing about the guarantee.
func (l *Logger) RedactExpiredInboundSenders(ctx context.Context, now time.Time) (int64, error) {
	tag, err := l.pool.Exec(ctx, redactionSQL,
		redactedLocalPart+"@",
		now.Add(-inboundSenderRetention),
	)
	if err != nil {
		return 0, fmt.Errorf("audit: redacting expired inbound sender addresses: %w", err)
	}
	return tag.RowsAffected(), nil
}
