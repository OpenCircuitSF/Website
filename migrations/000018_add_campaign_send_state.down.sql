-- Reverse 000018 in the opposite order of its up.sql.

-- Reset any 'sending' rows to 'queued' BEFORE narrowing the CHECK below, or
-- the ADD CONSTRAINT fails against any database that was mid-send when this
-- migration is rolled back.
UPDATE email_sends SET status = 'queued' WHERE status = 'sending';

ALTER TABLE email_sends DROP COLUMN claimed_at;

ALTER TABLE email_sends DROP CONSTRAINT email_sends_status_check;
ALTER TABLE email_sends
    ADD CONSTRAINT email_sends_status_check
    CHECK (status IN ('queued', 'sent', 'failed', 'bounced', 'complained', 'skipped'));

DELETE FROM settings WHERE key IN ('max_send_rate', 'default_from_name');

ALTER TABLE email_campaigns DROP COLUMN test_sent_at;
ALTER TABLE email_campaigns DROP COLUMN materialized_at;
