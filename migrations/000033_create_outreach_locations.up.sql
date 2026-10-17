-- #0548: saved flyer locations. code_locked_at is set the first time any
-- flyer link uses the location (outreach.Store.MarkLocationUsed); from then on
-- the code is immutable and the row cannot be deleted, only deactivated.
-- outreach_locations_code_format_check mirrors outreach.ValidCode and the SPA's
-- validateCode: 2-16 lowercase letters, digits and single interior dashes.
CREATE TABLE outreach_locations (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL,
    code           TEXT NOT NULL,
    address        TEXT NOT NULL DEFAULT '',
    notes          TEXT NOT NULL DEFAULT '',
    active         BOOLEAN NOT NULL DEFAULT true,
    code_locked_at TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ,
    CONSTRAINT outreach_locations_code_unique UNIQUE (code),
    CONSTRAINT outreach_locations_code_format_check
        CHECK (char_length(code) BETWEEN 2 AND 16 AND code ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT outreach_locations_name_check
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 100)
);
