-- #0658 (plan #0657): one scheduled social post for a topic guide
-- (/guides/{slug}), separate from workshop Outreach (outreach_posts).
-- guide_slug is checked against internal/guides in Go, not by a foreign
-- key: the guides are an embedded catalog, not rows. Each row gets its own
-- ShortLinks link; destination_url carries utm_content=gp{id}, which keeps
-- every destination, and so every short link, unique per row.
-- campaign_slug is the ShortLinks campaign the link is minted into, chosen
-- per draft (default guide-posts) so a later repost can count its clicks
-- under a new campaign; once linked it holds the slug ShortLinks returned.
CREATE TABLE guide_posts (
    id                 BIGSERIAL PRIMARY KEY,
    guide_slug         TEXT NOT NULL,
    channel            TEXT NOT NULL,
    body               TEXT NOT NULL,
    destination_url    TEXT NOT NULL,
    campaign_slug      TEXT NOT NULL,
    short_link_key     TEXT,
    short_url          TEXT,
    status             TEXT NOT NULL DEFAULT 'draft',
    scheduled_at       TIMESTAMPTZ,
    posted_at          TIMESTAMPTZ,
    remote_id          TEXT,
    remote_url         TEXT,
    last_error         TEXT,
    attempt_generation INTEGER NOT NULL DEFAULT 0 CHECK (attempt_generation >= 0),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ,
    CONSTRAINT guide_posts_channel_check CHECK (channel IN ('bluesky','mastodon')),
    CONSTRAINT guide_posts_status_check
        CHECK (status IN ('draft','scheduled','queued','posted','failed')),
    CONSTRAINT guide_posts_scheduled_at_check
        CHECK (status <> 'scheduled' OR scheduled_at IS NOT NULL),
    CONSTRAINT guide_posts_posted_at_check
        CHECK (status <> 'posted' OR posted_at IS NOT NULL),
    CONSTRAINT guide_posts_campaign_slug_check
        CHECK (campaign_slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT guide_posts_link_pair_check
        CHECK ((short_link_key IS NULL) = (short_url IS NULL)),
    CONSTRAINT guide_posts_link_check
        CHECK (status = 'draft' OR short_url IS NOT NULL)
);
CREATE UNIQUE INDEX guide_posts_short_link_key_key ON guide_posts (short_link_key) WHERE short_link_key IS NOT NULL;
CREATE UNIQUE INDEX guide_posts_destination_url_key ON guide_posts (destination_url);
CREATE INDEX idx_guide_posts_due ON guide_posts (scheduled_at, id) WHERE status = 'scheduled';
CREATE INDEX idx_guide_posts_slug ON guide_posts (guide_slug, id);
