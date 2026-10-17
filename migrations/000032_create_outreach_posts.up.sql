-- #0544: one drafted post per (outreach, channel, wave). channel and wave are
-- deliberately unchecked here: the lists live in internal/outreach so adding
-- a channel needs no migration. "Prepared" is not a status; it is
-- short_url IS NOT NULL.
CREATE TABLE outreach_posts (
    id                 BIGSERIAL PRIMARY KEY,
    outreach_id        BIGINT NOT NULL REFERENCES outreach(id) ON DELETE CASCADE,
    channel            TEXT NOT NULL,
    wave               TEXT NOT NULL,
    body               TEXT NOT NULL,
    source_fingerprint TEXT NOT NULL,
    short_link_key     TEXT,
    short_url          TEXT,
    status             TEXT NOT NULL DEFAULT 'draft',
    scheduled_at       TIMESTAMPTZ,
    posted_at          TIMESTAMPTZ,
    remote_id          TEXT,
    remote_url         TEXT,
    last_error         TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ,
    CONSTRAINT outreach_posts_slot_unique UNIQUE (outreach_id, channel, wave),
    CONSTRAINT outreach_posts_status_check
        CHECK (status IN ('draft','scheduled','queued','posted','failed','manual')),
    CONSTRAINT outreach_posts_link_pair_check
        CHECK ((short_link_key IS NULL) = (short_url IS NULL)),
    CONSTRAINT outreach_posts_posted_at_check
        CHECK (status <> 'posted' OR posted_at IS NOT NULL)
);
