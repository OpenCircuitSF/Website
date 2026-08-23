-- workshops, workshop_interests: the public workshop listing schema for
-- Phase 6 (#0050, PRD §6.2). Numbered after interests (000009) because
-- workshop_interests carries a foreign key into it, and after campaigns
-- (000017) because this migration also pays off the FK that 000017 left
-- as a bare, REFERENCES-less column.
--
-- Migration ordering (PRD §6.2's "Migration ordering" note): Phase 5 landed
-- before Phase 6, so email_campaigns.workshop_id shipped in 000017 as a
-- plain nullable BIGINT with no REFERENCES clause, keeping the two phases
-- independently deployable. This migration is where that deferral is paid
-- off: workshops now exists, so the FK is attached with an ALTER TABLE
-- rather than by editing 000017 (which would violate the append-only rule
-- once this project is past its first deploy, and there is no reason to
-- break the pattern early).
CREATE TABLE workshops (
    id               BIGSERIAL PRIMARY KEY,
    slug             TEXT UNIQUE NOT NULL,
    title            TEXT NOT NULL,
    summary          TEXT,                  -- used in meta description + list cards
    body_md          TEXT,
    starts_at        TIMESTAMPTZ,
    ends_at          TIMESTAMPTZ,
    location_name    TEXT,
    location_address TEXT,
    location_note    TEXT,                  -- "exact address emailed to attendees"
    capacity         INT,                   -- display only in v1
    signup_url       TEXT,                  -- external RSVP link if used
    cover_image      TEXT,                  -- path under /assets
    status           TEXT NOT NULL DEFAULT 'draft',
                     -- draft | published | canceled
    published_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Guard the enum-like column at the database, matching the
-- email_campaigns_status_check / subscribers_status_check precedent: a
-- stray UPDATE can't park a row in a value no store method upstream
-- expects.
ALTER TABLE workshops
    ADD CONSTRAINT workshops_status_check
    CHECK (status IN ('draft', 'published', 'canceled'));

-- Public listing query (PRD §6.2 / #0051, widened by #0135): published AND
-- canceled workshops ordered by start time -- both statuses are visible to
-- the public (#0135: the index and the detail route must apply the same
-- visibility rule, or a canceled workshop silently vanishes from the index
-- while its direct link still 200s). Partial on status <> 'draft' so only
-- draft rows -- never publicly visible -- are excluded from the index the
-- public page's hot path scans.
CREATE INDEX idx_workshops_published ON workshops (starts_at DESC)
    WHERE status <> 'draft';

-- workshop_interests: which interests a workshop is tagged with, read by
-- #0051's public listing filters and any future campaign-audience
-- targeting. ON DELETE CASCADE on both columns per PRD §6.2: deleting a
-- workshop or an interest must not leave an orphaned tagging row behind.
CREATE TABLE workshop_interests (
    workshop_id BIGINT NOT NULL REFERENCES workshops(id) ON DELETE CASCADE,
    interest_id BIGINT NOT NULL REFERENCES interests(id) ON DELETE CASCADE,
    PRIMARY KEY (workshop_id, interest_id)
);
CREATE INDEX idx_workshop_interests_interest ON workshop_interests (interest_id);

-- Pay off 000017's deferral: email_campaigns.workshop_id is an optional
-- announcement source, now enforced as a real foreign key. No ON DELETE
-- clause, matching PRD §6.2's ALTER TABLE exactly and the
-- email_campaigns.created_by -> users(id) precedent (000017): a workshop
-- with campaigns pointing at it cannot be silently orphaned by a DELETE,
-- and #0041 already models "cancel", not delete, for campaigns.
ALTER TABLE email_campaigns
    ADD CONSTRAINT email_campaigns_workshop_id_fkey
    FOREIGN KEY (workshop_id) REFERENCES workshops(id);
