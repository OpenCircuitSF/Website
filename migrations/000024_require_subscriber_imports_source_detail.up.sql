-- #0291 (found by #0125's review): PRD §6.10 names source_detail as one of
-- the four fields a subscriber_imports row requires -- "source_detail --
-- the specific event or export it came from" -- but migrations/000023 left
-- the column nullable, and ImportStore.Commit only enforced consent_note
-- and collected_at. #0129's invitation copy is assembled from
-- subscriber_imports.source, source_detail, and collected_at (PRD §6.10.1:
-- "The source sentence is built from ... which is the second reason those
-- three fields are mandatory"), so a blank source_detail would surface as a
-- blank clause in a real invitation email once #0129 lands.
--
-- 000023 is frozen (CLAUDE.md §1 -- applied to production since 2026-08-25,
-- the greenfield exception expired that day), so this lands as a new
-- ALTER TABLE rather than an edit to the CREATE TABLE that owns the column.
--
-- Existing-row safety (criterion 3): established, not assumed. #0125 (which
-- created this table) resolved on 2026-08-27, the same day this issue was
-- filed against its review; subscriber_imports has exactly one producer,
-- ImportStore.Commit, and every other required field (source, consent_note,
-- collected_at) was already enforced by that method before this migration.
-- There is no code path, seed script, or fixture in this repository that
-- inserts a subscriber_imports row (grep for the literal table name found
-- none outside this package and its own tests), so no row with a null or
-- empty source_detail is expected to exist anywhere this migration runs.
-- The backfill below runs anyway, unconditionally, so this migration is
-- safe even if that expectation turns out to be wrong on some database this
-- was not tested against -- an UPDATE matching zero rows is a no-op.
UPDATE subscriber_imports
   SET source_detail = 'unspecified (backfilled by migration 000024 -- no detail was recorded at import time)'
 WHERE source_detail IS NULL OR btrim(source_detail) = '';

ALTER TABLE subscriber_imports
    ALTER COLUMN source_detail SET NOT NULL;

ALTER TABLE subscriber_imports
    ADD CONSTRAINT subscriber_imports_source_detail_check
    CHECK (btrim(source_detail) <> '');
