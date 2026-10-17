-- #0544: one outreach row per workshop; ties the workshop to its ShortLinks
-- campaign. ON DELETE CASCADE: deleting a workshop removes its outreach (the
-- campaign on the ShortLinks side is inert and left alone).
CREATE TABLE outreach (
    id                       BIGSERIAL PRIMARY KEY,
    workshop_id              BIGINT NOT NULL UNIQUE REFERENCES workshops(id) ON DELETE CASCADE,
    shortlinks_campaign_slug TEXT NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ,
    archived_at              TIMESTAMPTZ
);
