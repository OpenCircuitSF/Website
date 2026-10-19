-- #0604: "Delete and redo". Taking a posted automatic post down and posting it
-- again must not recover the deleted post. Bluesky's record key and
-- Mastodon's Idempotency-Key are both derived from the post row, so a re-send
-- would find the deleted record (or be handed the deleted status) unless the
-- derivation changes. attempt_generation counts takedowns: 0 keeps today's
-- keys exactly (so in-flight retries are unaffected), and each takedown adds
-- one, which the publisher mixes into both keys.
ALTER TABLE outreach_posts
    ADD COLUMN attempt_generation INTEGER NOT NULL DEFAULT 0 CHECK (attempt_generation >= 0);
