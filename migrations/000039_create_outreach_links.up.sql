-- #0568: one tracked short link per (outreach, channel, wave), created when
-- outreach starts, before any post exists. outreach_posts keeps its own copy
-- of the key and URL, written when a post is prepared. The slot constraint
-- outreach_links_slot_unique makes link creation idempotent.
CREATE TABLE outreach_links (
    id             BIGSERIAL PRIMARY KEY,
    outreach_id    BIGINT NOT NULL REFERENCES outreach(id) ON DELETE CASCADE,
    channel        TEXT NOT NULL,
    wave           TEXT NOT NULL,
    short_link_key TEXT NOT NULL,
    short_url      TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT outreach_links_slot_unique UNIQUE (outreach_id, channel, wave)
);
