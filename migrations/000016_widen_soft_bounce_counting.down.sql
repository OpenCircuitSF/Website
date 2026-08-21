-- Reverses 000016.up.sql exactly back to migrations/000014's original
-- idx_email_events_soft_bounce definition.
DROP INDEX idx_email_events_soft_bounce;

CREATE INDEX idx_email_events_soft_bounce
    ON email_events (recipient, received_at DESC)
    WHERE event_type = 'Bounce' AND bounce_type = 'Transient';
