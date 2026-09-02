-- Reverse 000027 in the opposite order of its up.sql.

-- Reset any 'skipped' rows to 'abandoned' BEFORE narrowing the CHECK
-- below, or the ADD CONSTRAINT fails against any database holding a row
-- this migration's up.sql produced — same precedent as 000018's
-- down.sql resetting 'sending' rows to 'queued' before narrowing.
UPDATE outbound_queue SET status = 'abandoned' WHERE status = 'skipped';

ALTER TABLE outbound_queue DROP CONSTRAINT outbound_queue_status_check;
ALTER TABLE outbound_queue
    ADD CONSTRAINT outbound_queue_status_check
    CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'abandoned'));
