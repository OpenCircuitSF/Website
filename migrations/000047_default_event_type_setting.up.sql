-- The default event type moves from event_types.is_default to a settings row
-- (#0618, PRD §6.2), so every default lives in one place. settings.value is text
-- with no foreign key, so internal/eventtypes now enforces what the check
-- constraint and the partial unique index enforced, by locking this row through
-- internal/defaults.
--
-- With no default row the subquery is NULL and settings.value's NOT NULL fails
-- the migration, which is the right outcome. No ON CONFLICT, for the same reason.
INSERT INTO settings (key, value, updated_at)
VALUES ('default_event_type_id', (SELECT id::text FROM event_types WHERE is_default), now());

-- Fails rather than picking another type. Comparing id::text = raw avoids a cast
-- error on a non-numeric value. CREATE OR REPLACE keeps the function's OID, so
-- workshops.event_type_id's column default keeps working. SQLSTATE OCDEF is what
-- internal/workshops maps to a named error.
CREATE OR REPLACE FUNCTION event_types_default_id() RETURNS BIGINT LANGUAGE plpgsql STABLE AS $$
DECLARE
    raw TEXT;
    resolved BIGINT;
BEGIN
    SELECT value INTO raw FROM settings WHERE key = 'default_event_type_id';
    IF raw IS NULL THEN
        RAISE EXCEPTION 'settings.default_event_type_id is missing' USING ERRCODE = 'OCDEF';
    END IF;
    SELECT id INTO resolved FROM event_types WHERE id::text = raw AND enabled;
    IF resolved IS NULL THEN
        RAISE EXCEPTION 'settings.default_event_type_id (%) names no enabled event type', raw
            USING ERRCODE = 'OCDEF';
    END IF;
    RETURN resolved;
END $$;

ALTER TABLE event_types DROP CONSTRAINT event_types_default_enabled;
DROP INDEX event_types_one_default;
ALTER TABLE event_types DROP COLUMN is_default;
