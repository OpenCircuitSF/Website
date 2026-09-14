-- Reverse 000029 in the opposite order of its up.sql.
DROP INDEX idx_audit_log_message_id;
ALTER TABLE audit_log DROP COLUMN message_id;
