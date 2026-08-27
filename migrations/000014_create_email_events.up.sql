-- email_events: the raw SES/SNS event log (#0038, PRD §6.7). Every
-- interpretable-or-not notification that clears sesnotify.Verifier gets a
-- row here BEFORE any interpretation happens, so a bug in the
-- interpretation logic is recoverable from the evidence already stored.
--
-- Migration numbering: #0038's own planning pass named this 000012, but
-- 000012 was taken by #0033 (create_suppressions) and 000013 by #0100
-- (widening its primary key) by the time this issue was implemented — see
-- issues/0038.md's "Carried in from #0100's plan".
--
-- Dedupe key: sns_message_id (SNS's at-least-once delivery id for one
-- publish), not ses_message_id (SES's id for one outbound email, which one
-- SNS message never repeats but one email can appear under many DIFFERENT
-- sns_message_ids — Send, Delivery, Bounce, Complaint are each their own
-- SNS publish). A single Bounce/Complaint carries an ARRAY of recipients,
-- so the key must be composite: UNIQUE (sns_message_id, recipient), one row
-- per recipient. recipient is therefore NOT NULL DEFAULT '' rather than
-- nullable — Postgres NULLs never conflict with each other, so a nullable
-- recipient would defeat the dedupe entirely for events that carry no
-- recipient list at all.
CREATE TABLE email_events (
    id             BIGSERIAL PRIMARY KEY,
    sns_message_id TEXT NOT NULL,
    ses_message_id TEXT,               -- SES's id for the outbound email; #0049's reconciliation key, not a dedupe key
    event_type     TEXT NOT NULL,      -- Bounce | Complaint | Delivery | Reject | RenderingFailure | DeliveryDelay | Send | ''
                                        -- deliberately UNCONSTRAINED: unlike subscribers.status (a genuinely
                                        -- closed set), SES adds event types over time and this table's job is to
                                        -- record whatever arrives, not to gatekeep it.
    bounce_type    TEXT,               -- Permanent | Transient | Undetermined (Bounce events only)
    bounce_subtype TEXT,               -- #0039 needs this; PRD §6.5's out-of-office note depends on subType too
    recipient      TEXT NOT NULL DEFAULT '', -- normalised lower(trim(...)); '' when the event carries none
    event_at       TIMESTAMPTZ,        -- mail.timestamp / the event's own timestamp field, when parseable
    payload        JSONB NOT NULL,     -- the raw inner SES event JSON (or a {"raw": "..."} wrapper when it
                                        -- wasn't valid JSON at all) — recorded before interpretation, always
    received_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (sns_message_id, recipient)
);

ALTER TABLE email_events
    ADD CONSTRAINT email_events_recipient_normalized
    CHECK (recipient = lower(trim(recipient)));

CREATE INDEX idx_email_events_message_id ON email_events (ses_message_id);
CREATE INDEX idx_email_events_recipient  ON email_events (recipient);

-- #0124 (PRD §6.9): backs GET /admin/deliverability and
-- /admin/deliverability/{email} — the per-address bounce history, sorted by
-- recency. This migration originally (000014, then widened by 000016) also
-- carried a PARTIAL index (idx_email_events_soft_bounce) backing #0039's
-- rolling-30-day-window soft-bounce count. #0124 replaces that rule with a
-- consecutive streak counted incrementally on subscribers.soft_bounce_streak
-- (migration 000010) rather than by re-querying email_events, so the
-- windowed count query — and the partial index it needed — no longer exist.
-- 000016 (which only widened that now-removed index) is kept as a
-- documented no-op rather than deleted outright — removing the file would
-- force renumbering every migration from 000017 onward to satisfy
-- verifyContiguous's gap-free sequence requirement (#0083), for no
-- behavioral gain; see migrations/000016_widen_soft_bounce_counting.up.sql's
-- comment, docs/database.md's migration table, and issues/0124.md's Notes.
CREATE INDEX idx_email_events_recipient_time ON email_events (recipient, received_at DESC);
