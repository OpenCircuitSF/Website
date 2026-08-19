-- interests: the workshop interest taxonomy. Rows, not a Go enum (PRD §6.1) —
-- new themes appear constantly and adding one must not require a deploy.
--
-- Numbered ahead of subscribers (#0025/000010): subscriber_interests will
-- carry a foreign key to this table, and the composite join table cannot be
-- created before both sides exist.
CREATE TABLE interests (
    id          BIGSERIAL PRIMARY KEY,
    slug        TEXT UNIQUE NOT NULL,
    name        TEXT NOT NULL,
    description TEXT,
    sort_order  INT NOT NULL DEFAULT 0,
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Slug format: lowercase, hyphenated. Enforced here (not just in the store
-- layer) so no code path — including a future manual INSERT — can slip a
-- malformed slug past the constraint. Matches ^[a-z0-9]+(-[a-z0-9]+)*$.
ALTER TABLE interests
    ADD CONSTRAINT interests_slug_format
    CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$');

-- Seed the twelve interests from PRD §6.1, in the order listed there.
-- ON CONFLICT DO NOTHING keeps this idempotent: re-running `up` against an
-- already-seeded database (or one where an admin has since edited a row via
-- #0024) neither duplicates nor overwrites anything.
INSERT INTO interests (slug, name, sort_order) VALUES
    ('microcontrollers', 'Microcontrollers (ESP32, Arduino, RP2040)', 10),
    ('soldering',        'Soldering & Assembly',                      20),
    ('homelab',          'Homelab & Self-Hosting',                    30),
    ('home-automation',  'Home Automation',                           40),
    ('pcb-design',       'PCB Design & Fabrication',                  50),
    ('sensors-iot',      'Sensors & IoT',                             60),
    ('robotics',         'Robotics & Motion',                         70),
    ('radio-rf',         'Radio & RF',                                80),
    ('retro-computing',  'Retro Computing & Repair',                  90),
    ('3d-printing',      '3D Printing & Enclosures',                 100),
    ('test-equipment',   'Test Equipment & Measurement',              110),
    ('beginner',         'Absolute Beginner Sessions',                120)
ON CONFLICT (slug) DO NOTHING;
