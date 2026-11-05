ALTER TABLE event_types ADD COLUMN is_default BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE event_types SET is_default = TRUE
 WHERE id::text = (SELECT value FROM settings WHERE key = 'default_event_type_id');

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM event_types WHERE is_default) THEN
        RAISE EXCEPTION 'settings.default_event_type_id names no event type; cannot restore is_default';
    END IF;
END $$;

ALTER TABLE event_types ADD CONSTRAINT event_types_default_enabled
    CHECK (NOT is_default OR enabled);

CREATE UNIQUE INDEX event_types_one_default ON event_types (is_default) WHERE is_default;

CREATE OR REPLACE FUNCTION event_types_default_id() RETURNS BIGINT LANGUAGE sql STABLE
    AS $$ SELECT id FROM event_types WHERE is_default $$;

DELETE FROM settings WHERE key = 'default_event_type_id';
