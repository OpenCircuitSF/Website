-- #0503: the inbound-address retention sweep (internal/audit's
-- RedactExpiredInboundSenders) redacts the local part of the From: address a
-- parked-message audit row carries once that row is past its 90-day retention
-- window. The sweep runs on the outbox worker's existing poll cadence, once
-- per pass, so its WHERE clause needs an index-shaped answer rather than a
-- sequential scan of the whole append-only audit_log: this partial index
-- covers exactly the rows the sweep can ever update -- parked-message rows
-- that carry a recorded From: address and whose parked reason is not the
-- subscriber's own auto-reply case (auto_reply is deliberately retained in
-- full and disclosed in the privacy policy, like the other seven
-- email-carrying audit call sites). The predicate mirrors the sweep's own
-- WHERE clause minus its created_at cutoff and its already-redacted guard;
-- audit_log.metadata is JSONB, so the reason test uses the ->> operator.
CREATE INDEX idx_audit_log_inbound_redactable ON audit_log (created_at)
    WHERE action = 'inbound_mail.parked'
      AND target_type = 'inbound_mail'
      AND metadata ? 'from'
      AND COALESCE(metadata->>'reason', '') <> 'auto_reply';
