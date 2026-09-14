-- Reverse of 000030: the retention sweep's partial index (see the up
-- migration's comment). Dropping it leaves the sweep correct but slow: the
-- UPDATE falls back to a sequential scan of audit_log.
DROP INDEX IF EXISTS idx_audit_log_inbound_redactable;
