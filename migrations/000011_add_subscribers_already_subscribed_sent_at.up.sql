-- #0026's review (finding 1) found the "you're already subscribed" email had
-- no cooldown at all: 20 sequential submits of one active subscriber's
-- address produced 20 emails to that person, unauthenticated — both a
-- mail-amplification vector and half of a two-probe enumeration oracle
-- (the confirmation path already had a once-per-hour cooldown; this path
-- had none). Track the last successful send the same way confirm_sent_at
-- already does, so the identical atomic claim-before-send pattern
-- (internal/subscribers.ClaimAlreadySubscribedSend / ClaimConfirmationSend)
-- applies uniformly to both outbound messages this endpoint can send.
ALTER TABLE subscribers ADD COLUMN already_subscribed_sent_at TIMESTAMPTZ;
