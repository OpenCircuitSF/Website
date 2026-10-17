-- #0549: printable flyers. A flyer is a design plus a selection of
-- locations; the tracked link and QR belong to (outreach, location) so
-- every flyer for the same workshop and location shares them
-- (outreach_flyer_links_slot_unique).
CREATE TABLE outreach_flyers (
    id          BIGSERIAL PRIMARY KEY,
    outreach_id BIGINT NOT NULL REFERENCES outreach(id) ON DELETE CASCADE,
    photo       TEXT,
    focal_x     DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    focal_y     DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    template    TEXT NOT NULL DEFAULT 'photo-top',
    tagline     TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ,
    CONSTRAINT outreach_flyers_focal_check
        CHECK (focal_x BETWEEN 0 AND 1 AND focal_y BETWEEN 0 AND 1),
    CONSTRAINT outreach_flyers_template_check
        CHECK (template IN ('photo-top', 'full-photo', 'split')),
    CONSTRAINT outreach_flyers_tagline_check CHECK (char_length(tagline) <= 140)
);
CREATE INDEX outreach_flyers_outreach_idx ON outreach_flyers (outreach_id);

CREATE TABLE outreach_flyer_locations (
    flyer_id    BIGINT NOT NULL REFERENCES outreach_flyers(id) ON DELETE CASCADE,
    location_id BIGINT NOT NULL REFERENCES outreach_locations(id) ON DELETE CASCADE,
    copies      INT NOT NULL DEFAULT 1,
    PRIMARY KEY (flyer_id, location_id),
    CONSTRAINT outreach_flyer_locations_copies_check CHECK (copies BETWEEN 1 AND 50)
);

CREATE TABLE outreach_flyer_links (
    id             BIGSERIAL PRIMARY KEY,
    outreach_id    BIGINT NOT NULL REFERENCES outreach(id) ON DELETE CASCADE,
    location_id    BIGINT NOT NULL REFERENCES outreach_locations(id) ON DELETE RESTRICT,
    short_link_key TEXT NOT NULL,
    short_url      TEXT NOT NULL,
    qr_svg         TEXT,
    put_up_at      TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT outreach_flyer_links_slot_unique UNIQUE (outreach_id, location_id)
);
CREATE INDEX outreach_flyer_links_location_idx ON outreach_flyer_links (location_id);
