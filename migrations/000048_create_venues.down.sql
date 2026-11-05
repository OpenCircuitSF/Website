-- #0617: venues are the truth once this migration has run, so the text columns
-- are repopulated from them. The row subquery yields NULLs for a listing with
-- no venue, so one statement covers both cases.
UPDATE workshops w
   SET (location_name, location_address, location_note) =
       (SELECT v.name, v.address, v.note FROM venues v WHERE v.id = w.venue_id);
COMMENT ON COLUMN workshops.location_name IS NULL;
COMMENT ON COLUMN workshops.location_address IS NULL;
COMMENT ON COLUMN workshops.location_note IS NULL;
DROP INDEX idx_workshops_venue;
ALTER TABLE workshops DROP COLUMN venue_id;
DROP TABLE venues;
DELETE FROM settings WHERE key = 'default_venue_id';
