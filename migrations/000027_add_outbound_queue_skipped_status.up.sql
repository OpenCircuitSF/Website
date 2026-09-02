-- outbound_queue: add 'skipped' as a sixth status (#0365) — a send-time
-- eligibility re-check (internal/mailing.OutboxWorker.sendGate, calling
-- internal/subscribers.Store.SendEligibility) found the row's subscriber
-- no longer eligible for this kind between enqueue and drain: the
-- subscriber's status changed (e.g. to 'complained'), the address is now
-- in suppressions, or the live subscribers.email no longer matches
-- outbound_queue.recipient.
--
-- Deliberately NOT 'abandoned'. 'abandoned' is a delivery-health signal
-- with three live readers, none of which reads outbound_queue.error:
-- internal/handlers/admin_dashboard.go's AbandonedCountByKind (surfaced
-- to the admin overview as "how many pending signups never got a
-- confirmation delivered"), the outbound-queue-abandoned dashboard
-- warning (counts.Abandoned > 0, which never clears since abandoned rows
-- are terminal), and web/src/lib/pending.ts's queueStateBadgeClass, which
-- gives ONLY 'abandoned' the danger badge as "the one state that actually
-- explains a non-confirmation as a delivery failure". Routing a
-- deliberately-withheld send through 'abandoned' would misreport correct
-- behavior as a delivery failure and raise a permanent, never-clearing
-- warning over a single correctly-skipped confirmation.
--
-- 'skipped' instead mirrors email_sends' own status of the same name
-- (000018/000021's "same shape" precedent) — see
-- internal/outbox.Store.MarkSkipped's doc comment for the transition
-- itself, and internal/mailing/audience.go's MarkSkippedTx for the
-- campaign-mail twin this issue's plan deliberately matches
-- predicate-for-predicate and word-for-word.
--
-- 000021 (which created this CHECK) is frozen — CLAUDE.md §1 — so this
-- widens it via a new migration rather than an edit.
ALTER TABLE outbound_queue DROP CONSTRAINT outbound_queue_status_check;
ALTER TABLE outbound_queue
    ADD CONSTRAINT outbound_queue_status_check
    CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'abandoned', 'skipped'));
