-- suppressions: a global send-block list, independent of any subscribers
-- row. Checked before every send, regardless of subscriber status, and
-- survives both unsubscribe/resubscribe cycles and hard deletion of the
-- subscriber row (#0060) because it is keyed by normalized email address,
-- not subscriber id — there is deliberately no foreign key to subscribers.
-- Per PRD §6.2, #0033.
CREATE TABLE suppressions (
    email      TEXT PRIMARY KEY,   -- stored lower(trim(email)), same normalization as subscribers.email
    reason     TEXT NOT NULL,      -- hard_bounce | complaint | manual | repeated_soft_bounce
    note       TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE suppressions
    ADD CONSTRAINT suppressions_reason_check
    CHECK (reason IN ('hard_bounce', 'complaint', 'manual', 'repeated_soft_bounce'));

-- Carried in from #0025's review (issues/0033.md): give suppressions.email
-- the identical normalization CHECK subscribers_email_normalized already
-- enforces on subscribers.email. Without it a suppression row stored with
-- different casing silently fails to match at the send gate — a
-- suppression that silently fails to match is worse than none, since the
-- send proceeds and the person who asked to be left alone gets mail.
ALTER TABLE suppressions
    ADD CONSTRAINT suppressions_email_normalized
    CHECK (email = lower(trim(email)));
