-- audit_log: add message_id (#0501) — the SES message id of the inbound
-- message an audit row was produced by. Set only by the inbound mailto:
-- unsubscribe path (internal/handlers/ses_inbound.go), which carries
-- notification.mail.messageId out of the verified SES event payload and
-- stamps it on the subscriber.unsubscribed row it writes on a match. NULL
-- for every other writer: the column is optional by design, not a second
-- identifier scheme.
--
-- Why a column and not another metadata key: the inbound redelivery check
-- (#0501) must find the row with a plain indexed lookup, not a JSONB
-- scan, and the value is the trail's join key back to the S3 object
-- (the default object key is the message id) — a first-class column
-- makes that visible to GET /admin/audit readers without decoding
-- metadata. Parked-message rows keep their SNS message id in metadata,
-- where recordParked has always put it; this column is scoped to the
-- unsubscribe-processing rows the decision in issues/0501.md names.
--
-- The partial index serves exactly one query,
-- audit.Logger.MessageProcessed's EXISTS over (action, message_id) —
-- the idempotency check that treats a redelivered message id as a
-- no-op. A partial index keeps the usual NULL-heavy audit_log rows out
-- of it entirely.
ALTER TABLE audit_log ADD COLUMN message_id TEXT;

CREATE INDEX idx_audit_log_message_id ON audit_log (message_id)
    WHERE message_id IS NOT NULL;
