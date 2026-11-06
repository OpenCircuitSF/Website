-- #0643: the newsletter ShortLink made for one upcoming event in one email
-- campaign (newsletter issue). Kept apart from outreach_links, whose readers
-- assume every row's channel is a real Outreach channel. One row per
-- (email campaign, workshop), so re-clicking "Add upcoming events" reuses it.

CREATE TABLE outreach_newsletter_links (
    id                BIGSERIAL PRIMARY KEY,
    email_campaign_id BIGINT NOT NULL REFERENCES email_campaigns(id) ON DELETE CASCADE,
    workshop_id       BIGINT NOT NULL REFERENCES workshops(id) ON DELETE CASCADE,
    campaign_slug     TEXT NOT NULL,
    short_link_key    TEXT NOT NULL,
    short_url         TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT outreach_newsletter_links_unique UNIQUE (email_campaign_id, workshop_id)
);

CREATE INDEX idx_outreach_newsletter_links_workshop ON outreach_newsletter_links (workshop_id);
