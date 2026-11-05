-- #0617: an admin-managed venues table replaces the location text every listing
-- retyped. The three text columns on workshops stay, unread and unwritten, so
-- the previous binary keeps working until the restart, a binary-only rollback
-- stays possible, and a draft's different note can still be recovered. A later
-- issue drops them.

CREATE TABLE venues (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    address    TEXT,
    note       TEXT,
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT venues_name_not_blank CHECK (btrim(name) <> '')
);

ALTER TABLE workshops ADD COLUMN venue_id BIGINT REFERENCES venues(id) ON DELETE RESTRICT;
CREATE INDEX idx_workshops_venue ON workshops (venue_id);

COMMENT ON COLUMN workshops.location_name IS 'Retired by #0617: unread and unwritten; venues holds this. Kept for rollback and recovery.';
COMMENT ON COLUMN workshops.location_address IS 'Retired by #0617: unread and unwritten; venues holds this. Kept for rollback and recovery.';
COMMENT ON COLUMN workshops.location_note IS 'Retired by #0617: unread and unwritten; venues holds this. Kept for rollback and recovery.';

INSERT INTO venues (name, address, note, sort_order)
VALUES ('Sequoia Fabrica', '1736 18th Street, San Francisco, CA 94107', 'Foot of Potrero Hill', 0);

WITH cleaned AS (
    SELECT id, updated_at,
           NULLIF(btrim(location_name, E' \t\r\n'), '') AS n,
           NULLIF(btrim(location_address, E' \t\r\n'), '') AS a,
           NULLIF(btrim(location_note, E' \t\r\n'), '') AS t
      FROM workshops
), sources AS (
    SELECT id, updated_at,
           COALESCE(n, a, t) AS name,
           CASE WHEN n IS NOT NULL THEN a END AS address,
           CASE WHEN n IS NOT NULL OR a IS NOT NULL THEN t END AS note
      FROM cleaned
     WHERE COALESCE(n, a, t) IS NOT NULL
), inserted AS (
    INSERT INTO venues (name, address, note, sort_order)
    SELECT (array_agg(name ORDER BY updated_at DESC, id DESC))[1],
           mode() WITHIN GROUP (ORDER BY address),
           mode() WITHIN GROUP (ORDER BY note),
           10 * row_number() OVER (ORDER BY lower(name))
      FROM sources
     WHERE lower(name) <> 'sequoia fabrica'
     GROUP BY lower(name)
    RETURNING id, name
), targets AS (
    SELECT id, name FROM inserted
    UNION ALL
    SELECT id, name FROM venues WHERE name = 'Sequoia Fabrica'
)
UPDATE workshops w SET venue_id = tg.id
  FROM sources s
  JOIN targets tg ON lower(tg.name) = lower(s.name)
 WHERE s.id = w.id;

INSERT INTO settings (key, value, updated_at)
VALUES ('default_venue_id', (SELECT id::text FROM venues WHERE name = 'Sequoia Fabrica'), now());
