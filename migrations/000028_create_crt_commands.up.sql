-- crt_commands: the home hero's CRT screen session (#0270, #0274), moved
-- from a hard-coded array (web/src/lib/crtScreen.ts's CRT_SESSION) into
-- rows so the user can change what it says without a code edit, a rebuild,
-- and a deploy (#0393). Modelled deliberately on interests
-- (migrations/000009_create_interests.up.sql): same
-- slug/sort_order/active shape, the same lowercase-hyphenated slug CHECK,
-- the same idempotent ON CONFLICT DO NOTHING seed.
--
-- Append-only: the greenfield exception expired 2026-08-25 (CLAUDE.md §1).
-- This is a new migration, 000028; 000001-000027 are untouched.
CREATE TABLE crt_commands (
    id         BIGSERIAL PRIMARY KEY,
    slug       TEXT UNIQUE NOT NULL,
    command    TEXT NOT NULL,
    -- Newline-separated lines, not TEXT[]/jsonb -- keeps the admin editor a
    -- plain <textarea> and the store layer trivial (#0393's Design §1).
    -- For a live row (source <> 'static') this is the FALLBACK, not
    -- decoration: #0274's rule is that the screen never degrades to a blank
    -- block or an error string, so it is NOT NULL for every source
    -- including the live ones.
    output     TEXT NOT NULL,
    source     TEXT NOT NULL DEFAULT 'static',
    sort_order INT  NOT NULL DEFAULT 0,
    active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ
);

-- Slug format: lowercase, hyphenated. Enforced here (not just in the store
-- layer), matching interests_slug_format (migrations/000009), so no code
-- path -- including a future manual INSERT -- can slip a malformed slug
-- past the constraint. Matches ^[a-z0-9]+(-[a-z0-9]+)*$.
ALTER TABLE crt_commands
    ADD CONSTRAINT crt_commands_slug_format
    CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$');

-- source is a closed vocabulary (#0393's Design §1 table): 'static' means
-- the stored output verbatim; the other three each name a live builder in
-- web/src/lib/crtScreen.ts that replaces the stored lines at render time
-- when its own fetch succeeds. Guarded at the database, not just by handler
-- validation, for the same reason as the slug CHECK above.
ALTER TABLE crt_commands
    ADD CONSTRAINT crt_commands_source_check
    CHECK (source IN ('static', 'workshops', 'list_stats', 'interests'));

-- Seed the eight existing CRT_SESSION entries verbatim (web/src/lib/
-- crtScreen.ts) so today's behaviour is preserved exactly: 'workshops --next'
-- and 'subscribe --interests' keep their live sources ('workshops' and
-- 'list_stats' respectively); the rest are static. ON CONFLICT DO NOTHING
-- keeps this idempotent, matching interests' seed -- re-running `up` against
-- an already-seeded database (or one an admin has since edited via #0393's
-- admin screen) neither duplicates nor overwrites anything.
INSERT INTO crt_commands (slug, command, output, source, sort_order, active) VALUES
    ('workshops-next', 'workshops --next',
'soldering 101 ..... sat 12:30
kicad from scratch  sep 18
esp32 + sensors ... oct 02
3 scheduled, 12 seats open', 'workshops', 10, TRUE),
    ('whoami', 'whoami',
'open circuit sf
a san francisco group that
builds things on tables.', 'static', 20, TRUE),
    ('ls-tools', 'ls tools/',
'irons  multimeters  scopes
logic-analysers  hot-air
all provided. bring nothing.', 'static', 30, TRUE),
    ('cat-topics', 'cat topics.txt',
'microcontrollers
soldering
homelab
home automation', 'static', 40, TRUE),
    ('where-venues', 'where --venues',
'makerspaces, co-working rooms,
somebody''s garage.
venue-independent by design.', 'static', 50, TRUE),
    ('skill-required', 'skill --required',
'none.
absolute beginners welcome.', 'static', 60, TRUE),
    ('subscribe-interests', 'subscribe --interests',
'pick only what you want:
[x] workshops  [ ] digests
[ ] announcements
double opt-in. leave anytime.', 'list_stats', 70, TRUE),
    ('uptime', 'uptime',
'soldering irons hot since 2026
no analytics. no trackers.', 'static', 80, TRUE)
ON CONFLICT (slug) DO NOTHING;

-- "Some fun options" (#0393's Design §5) -- seeded INACTIVE so merging this
-- migration changes nothing on the live screen until the user switches one
-- on via the admin screen. Register matches the existing eight: lowercase,
-- short, dry, no line over 36 characters (web/src/lib/crtScreen.ts's
-- crtTruncate enforces that width at render time; this seed writes to the
-- budget rather than leaning on the truncation).
INSERT INTO crt_commands (slug, command, output, source, sort_order, active) VALUES
    ('fortune', 'fortune',
'solder flows toward heat.

so do good ideas.', 'static', 90, FALSE),
    ('sl', 'sl',
'( a train goes past. )
you typed it wrong. enjoy.', 'static', 100, FALSE),
    ('ping-bench', 'ping bench',
'bench alive, 0.4ms
irons hot. seats open.', 'static', 110, FALSE),
    ('man-patience', 'man patience',
'PATIENCE(1)
hold the iron still.
count to three.
the joint will tell you.', 'static', 120, FALSE),
    ('finger-members', 'finger @members',
'37 logged on
0 idle
all soldering.', 'static', 130, FALSE),
    ('cat-motd', 'cat /etc/motd',
'no ads. no trackers.
no cover charge.
bring your hands.', 'static', 140, FALSE),
    ('topics-subscribers', 'topics --subscribers',
'microcontrollers ...... 4
soldering ............. 3
homelab ............... 1
12 topics. pick your own.', 'interests', 150, FALSE),
    ('date', 'date',
'it is always a good day
to fix something.', 'static', 160, FALSE),
    ('ls-dev', 'ls /dev',
'ttyUSB0  ttyUSB1
i2c-1  spidev0.0
plug something in.', 'static', 170, FALSE),
    ('history-tail', 'history | tail',
'blinked an led
burned a finger
shipped a board', 'static', 180, FALSE)
ON CONFLICT (slug) DO NOTHING;
