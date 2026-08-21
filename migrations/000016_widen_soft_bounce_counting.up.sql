-- #0109 widens the repeated-soft-bounce count (#0039) two ways, both
-- decided in issues/0109.md:
--
-- 1. Undetermined bounces now count alongside Transient ones. PRD §6.5
--    reads "5 soft bounces in 30 days" — it does not say "transient" and
--    does not exclude Undetermined. An address that only ever produces
--    Undetermined bounces is, operationally, exactly as dead as one that
--    only produces Transient ones, and leaving it out was the exact hole
--    this feature exists to close.
-- 2. The sender-fault Transient subtypes (MessageTooLarge, ContentRejected,
--    AttachmentRejected) are now excluded. Those describe a fault in OUR
--    message, not evidence the recipient's address is bad, so they must
--    never suppress a live subscriber.
--
-- migrations/000014 is append-only and is not edited (CLAUDE.md §1). This
-- migration replaces its idx_email_events_soft_bounce partial index with
-- one matching the new predicate, keeping the same index name since it is
-- still "the" partial index internal/sesnotify's soft-bounce count query
-- needs.
DROP INDEX idx_email_events_soft_bounce;

CREATE INDEX idx_email_events_soft_bounce
    ON email_events (recipient, received_at DESC)
    WHERE event_type = 'Bounce'
      AND bounce_type IN ('Transient', 'Undetermined')
      AND (bounce_subtype IS NULL
           OR bounce_subtype NOT IN ('MessageTooLarge', 'ContentRejected', 'AttachmentRejected'));
