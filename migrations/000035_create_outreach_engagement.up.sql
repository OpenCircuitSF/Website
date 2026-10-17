-- #0547: engagement snapshots for posted Bluesky/Mastodon posts, one row per
-- capture (hourly for 7 days after posting, daily to 30, then none).
CREATE TABLE outreach_engagement (
    id          BIGSERIAL PRIMARY KEY,
    post_id     BIGINT NOT NULL REFERENCES outreach_posts(id) ON DELETE CASCADE,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    likes       INTEGER NOT NULL DEFAULT 0,
    reposts     INTEGER NOT NULL DEFAULT 0,
    replies     INTEGER NOT NULL DEFAULT 0,
    quotes      INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT outreach_engagement_counts_check
        CHECK (likes >= 0 AND reposts >= 0 AND replies >= 0 AND quotes >= 0)
);
CREATE INDEX outreach_engagement_post_captured_idx
    ON outreach_engagement (post_id, captured_at DESC);

-- #0547: mentions, replies and quotes of our accounts. post_id is set when
-- the item replies to or quotes one of our outreach posts.
CREATE TABLE outreach_mentions (
    id         BIGSERIAL PRIMARY KEY,
    channel    TEXT NOT NULL,
    remote_id  TEXT NOT NULL,
    post_id    BIGINT REFERENCES outreach_posts(id) ON DELETE SET NULL,
    kind       TEXT NOT NULL,
    author     TEXT NOT NULL,
    text       TEXT NOT NULL DEFAULT '',
    url        TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT outreach_mentions_remote_id_unique UNIQUE (remote_id),
    CONSTRAINT outreach_mentions_kind_check CHECK (kind IN ('mention','reply','quote'))
);
CREATE INDEX outreach_mentions_post_created_idx ON outreach_mentions (post_id, created_at DESC);
CREATE INDEX outreach_mentions_created_idx ON outreach_mentions (created_at DESC);

-- #0547: per-channel notification position (a Bluesky high-water mark or a
-- Mastodon notification id).
CREATE TABLE outreach_mention_cursors (
    channel    TEXT PRIMARY KEY,
    cursor     TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
