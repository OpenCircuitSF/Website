-- event_types: the admin-managed list of what a workshops row can be (#0613,
-- PRD §6.2). Rows rather than a fixed `kind` column, so an admin can add,
-- rename, reorder and disable a type without a deploy. The workshops table
-- keeps its name; only its rows gain a type.
CREATE TABLE event_types (
    id         BIGSERIAL PRIMARY KEY,
    slug       TEXT UNIQUE NOT NULL,
    name       TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE event_types ADD CONSTRAINT event_types_slug_format
    CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$');

-- The default type must be one a new listing may actually use.
ALTER TABLE event_types ADD CONSTRAINT event_types_default_enabled
    CHECK (NOT is_default OR enabled);

-- At most one default. A partial unique index, so moving the default takes two
-- statements in one transaction: Postgres checks this index row by row, and a
-- single-statement swap raises a duplicate key.
CREATE UNIQUE INDEX event_types_one_default ON event_types (is_default) WHERE is_default;

INSERT INTO event_types (slug, name, is_default, sort_order) VALUES
    ('workshop', 'Workshop', TRUE, 10),
    ('hack-night', 'Hack Night', FALSE, 20),
    ('talk', 'Talk', FALSE, 30),
    ('hackathon', 'Hackathon', FALSE, 40),
    ('social', 'Social', FALSE, 50);

-- The column default below is this function rather than a literal id, so it
-- follows the current default type. It also does the backfill of every existing
-- row, and keeps each raw two-column INSERT INTO workshops working unchanged.
CREATE FUNCTION event_types_default_id() RETURNS BIGINT LANGUAGE sql STABLE
    AS $$ SELECT id FROM event_types WHERE is_default $$;

-- ON DELETE RESTRICT is the guard against deleting a type in use.
ALTER TABLE workshops ADD COLUMN event_type_id BIGINT NOT NULL
    DEFAULT event_types_default_id()
    REFERENCES event_types(id) ON DELETE RESTRICT;

CREATE INDEX idx_workshops_event_type ON workshops (event_type_id);
